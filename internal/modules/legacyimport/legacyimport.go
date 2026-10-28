// Package legacyimport brings a business over from another billing app (Vyapar, Tally, Marg or a spreadsheet). The books
// start from opening balances, which are real journal entries per customer and supplier. Past invoices are kept as a
// history register that never touches stock, tax returns or the ledger, so a year of old invoices cannot double-count.
package legacyimport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	accountingapp "rechvix/internal/modules/accounting/app"
	accountingdomain "rechvix/internal/modules/accounting/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/importer"
	"rechvix/internal/platform/money"
	"rechvix/internal/platform/permissions"
)

var ErrInvalid = errors.New("legacyimport: invalid request")

const (
	SourceOpeningBalance = "opening_balance"
	KindSale             = "SALE"
	KindPurchase         = "PURCHASE"
)

// OpeningBalanceFields are the columns of an opening-balance file.
var OpeningBalanceFields = []importer.Field{
	{Name: "party_name", Label: "Customer / supplier name", Required: true, Synonyms: []string{"Party Name", "Name", "Customer Name", "Supplier Name", "Ledger Name", "Legal Name", "legal_name"}},
	{Name: "balance", Label: "Amount outstanding", Required: true, Numeric: true, Synonyms: []string{"Balance", "Opening Balance", "Closing Balance", "Outstanding", "Amount", "Receivable", "Payable", "Amount Due"}},
	{Name: "balance_type", Label: "RECEIVABLE (they owe you) or PAYABLE (you owe them)", Synonyms: []string{"Type", "Balance Type", "Dr/Cr", "To Collect / To Pay", "Direction"}},
}

// HistoricalInvoiceFields are the columns of a past-invoice file.
var HistoricalInvoiceFields = []importer.Field{
	{Name: "party_name", Label: "Customer / supplier name", Required: true, Synonyms: []string{"Party Name", "Name", "Customer Name", "Supplier Name", "Ledger Name", "Party"}},
	{Name: "document_number", Label: "Invoice number", Required: true, Synonyms: []string{"Invoice No.", "Invoice No", "Invoice Number", "Bill No.", "Bill Number", "Voucher No.", "Voucher Number", "Doc No", "Number", "Ref No."}},
	{Name: "document_date", Label: "Invoice date", Required: true, Synonyms: []string{"Date", "Invoice Date", "Bill Date", "Voucher Date", "Doc Date"}},
	{Name: "total", Label: "Invoice total", Required: true, Numeric: true, Synonyms: []string{"Total", "Total Amount", "Invoice Amount", "Grand Total", "Amount", "Bill Amount"}},
	{Name: "paid", Label: "Amount already paid", Numeric: true, Synonyms: []string{"Paid", "Paid Amount", "Received", "Received Amount", "Amount Received", "Amount Paid"}},
	{Name: "notes", Label: "Notes", Synonyms: []string{"Notes", "Remarks", "Description", "Narration"}},
}

type Service struct {
	pool        *database.Pool
	accounting  *accountingapp.Service
	permissions *permissions.Checker
	audit       audit.Recorder
	now         func() time.Time
}

func NewService(pool *database.Pool, accounting *accountingapp.Service, p *permissions.Checker, a audit.Recorder) *Service {
	return &Service{pool: pool, accounting: accounting, permissions: p, audit: a, now: time.Now}
}

func (s *Service) manage(ctx context.Context, p permissions.Principal) error {
	return s.permissions.Require(ctx, p, "accounting.post", permissions.Scope{})
}

func (s *Service) view(ctx context.Context, p permissions.Principal) error {
	return s.permissions.Require(ctx, p, "accounting.view", permissions.Scope{})
}

type party struct {
	id uuid.UUID
	pt string
}

// partiesByName loads the organisation's active parties keyed by lower-cased legal name. More than one party can share a
// name; the caller treats that as ambiguous rather than guessing.
func (s *Service) partiesByName(ctx context.Context, org uuid.UUID) (map[string][]party, error) {
	out := map[string][]party{}
	rows, err := s.pool.Q(ctx).Query(ctx, `SELECT id, party_type, legal_name FROM parties WHERE organisation_id = $1 AND status = 'ACTIVE'`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p party
		var name string
		if err := rows.Scan(&p.id, &p.pt, &name); err != nil {
			return nil, err
		}
		k := strings.ToLower(strings.TrimSpace(name))
		out[k] = append(out[k], p)
	}
	return out, rows.Err()
}

func resolveParty(m map[string][]party, name string, allowed ...string) (party, string) {
	cands := m[strings.ToLower(strings.TrimSpace(name))]
	var ok []party
	for _, c := range cands {
		for _, a := range allowed {
			if c.pt == a {
				ok = append(ok, c)
				break
			}
		}
	}
	switch {
	case len(cands) == 0:
		return party{}, fmt.Sprintf("no customer or supplier named %q; import your contacts first", name)
	case len(ok) == 0:
		return party{}, fmt.Sprintf("%q exists but is not the right kind of contact for this file", name)
	case len(ok) > 1:
		return party{}, fmt.Sprintf("more than one contact is named %q; rename one so this row can be matched", name)
	}
	return ok[0], ""
}

// ImportOpeningBalances posts one journal per row: a customer's receivable as Dr Accounts Receivable / Cr Opening Balance
// Equity, a supplier's payable as Dr Opening Balance Equity / Cr Accounts Payable, tagged to the party so ledgers, ageing
// and the dashboard pick it up. A party already given an opening balance is reported as a duplicate, so a file can be
// re-run safely. Each row posts in its own transaction and is reported individually.
func (s *Service) ImportOpeningBalances(ctx context.Context, principal permissions.Principal, rows []importer.Row, asOf time.Time, dryRun bool) (importer.Report, error) {
	if err := s.manage(ctx, principal); err != nil {
		return importer.Report{}, err
	}
	if asOf.IsZero() {
		asOf = s.now()
	}
	if asOf.After(s.now().AddDate(0, 0, 1)) {
		return importer.Report{}, fmt.Errorf("%w: the opening balance date cannot be in the future", ErrInvalid)
	}
	b := importer.NewBuilder(dryRun)

	var parties map[string][]party
	has := map[uuid.UUID]bool{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		if parties, err = s.partiesByName(ctx, principal.OrganisationID); err != nil {
			return err
		}
		jr, err := s.pool.Q(ctx).Query(ctx, `SELECT source_id FROM journals WHERE organisation_id = $1 AND source_type = $2 AND source_id IS NOT NULL`, principal.OrganisationID, SourceOpeningBalance)
		if err != nil {
			return err
		}
		defer jr.Close()
		for jr.Next() {
			var id uuid.UUID
			if err := jr.Scan(&id); err != nil {
				return err
			}
			has[id] = true
		}
		return jr.Err()
	})
	if err != nil {
		return importer.Report{}, err
	}

	for _, row := range rows {
		name := strings.TrimSpace(row.Fields["party_name"])
		if name == "" {
			b.Error(row.Number, "party_name is required")
			continue
		}
		amount, err := decimal.NewFromString(row.Fields["balance"])
		if err != nil || !amount.IsPositive() {
			b.Error(row.Number, "balance %q must be a number above zero (use balance_type for the direction)", row.Fields["balance"])
			continue
		}
		if amount.Exponent() < -2 {
			b.Error(row.Number, "balance %q has more than two decimal places", row.Fields["balance"])
			continue
		}
		p, msg := resolveParty(parties, name, "CUSTOMER", "SUPPLIER", "BOTH")
		if msg != "" {
			b.Error(row.Number, "%s", msg)
			continue
		}
		side := strings.ToUpper(strings.TrimSpace(row.Fields["balance_type"]))
		switch side {
		case "", "RECEIVABLE", "PAYABLE":
		case "DR", "TO COLLECT", "DEBIT":
			side = "RECEIVABLE"
		case "CR", "TO PAY", "CREDIT":
			side = "PAYABLE"
		default:
			b.Error(row.Number, "balance_type %q must be RECEIVABLE or PAYABLE", row.Fields["balance_type"])
			continue
		}
		if side == "" {
			switch p.pt {
			case "CUSTOMER":
				side = "RECEIVABLE"
			case "SUPPLIER":
				side = "PAYABLE"
			default:
				b.Error(row.Number, "%q is both a customer and a supplier; say RECEIVABLE or PAYABLE in balance_type", name)
				continue
			}
		}
		if (side == "RECEIVABLE" && p.pt == "SUPPLIER") || (side == "PAYABLE" && p.pt == "CUSTOMER") {
			b.Error(row.Number, "%q is a %s, so a %s balance does not fit", name, strings.ToLower(p.pt), strings.ToLower(side))
			continue
		}
		if has[p.id] {
			b.Duplicate(row.Number, "%q already has an opening balance", name)
			continue
		}
		if dryRun {
			has[p.id] = true
			b.Valid(row.Number)
			continue
		}
		id := p.id
		lines := []accountingapp.JournalLineRequest{
			{AccountCode: accountingdomain.CodeAccountsReceivable, PartyID: &id, Debit: amount, Description: "Opening balance"},
			{AccountCode: accountingdomain.CodeOpeningBalanceEquity, Credit: amount, Description: "Opening balance"},
		}
		if side == "PAYABLE" {
			lines = []accountingapp.JournalLineRequest{
				{AccountCode: accountingdomain.CodeOpeningBalanceEquity, Debit: amount, Description: "Opening balance"},
				{AccountCode: accountingdomain.CodeAccountsPayable, PartyID: &id, Credit: amount, Description: "Opening balance"},
			}
		}
		if _, err := s.accounting.Post(ctx, principal, accountingapp.JournalRequest{
			OrganisationID: principal.OrganisationID, SourceType: SourceOpeningBalance, SourceID: &id, JournalDate: asOf,
			Description: "Opening balance: " + name, CreatedBy: principal.UserID, Lines: lines,
		}); err != nil {
			b.Error(row.Number, "could not post: %v", err)
			continue
		}
		has[p.id] = true
		b.Committed(row.Number)
	}
	return b.Report(), nil
}

var dateLayouts = []string{"2006-01-02", "02/01/2006", "2/1/2006", "02-01-2006", "2-1-2006", "02.01.2006", "2 Jan 2006", "02 Jan 2006", "02-Jan-2006", "2-Jan-2006", "Jan 2, 2006", "2006/01/02", "02/01/06"}

// ParseDate reads the date formats Indian billing apps and spreadsheets produce (day first, never month first).
func ParseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ImportHistoricalInvoices records past invoices in the history register. kind is SALE or PURCHASE. The contact must
// already exist (masters first), and an invoice number already registered for that kind is reported as a duplicate.
func (s *Service) ImportHistoricalInvoices(ctx context.Context, principal permissions.Principal, kind string, rows []importer.Row, dryRun bool) (importer.Report, error) {
	if err := s.manage(ctx, principal); err != nil {
		return importer.Report{}, err
	}
	if kind != KindSale && kind != KindPurchase {
		return importer.Report{}, fmt.Errorf("%w: kind must be SALE or PURCHASE", ErrInvalid)
	}
	allowed := []string{"CUSTOMER", "BOTH"}
	if kind == KindPurchase {
		allowed = []string{"SUPPLIER", "BOTH"}
	}
	b := importer.NewBuilder(dryRun)
	batch, err := uuid.NewV7()
	if err != nil {
		return importer.Report{}, err
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		parties, err := s.partiesByName(ctx, principal.OrganisationID)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		er, err := s.pool.Q(ctx).Query(ctx, `SELECT document_number FROM historical_invoices WHERE organisation_id = $1 AND kind = $2`, principal.OrganisationID, kind)
		if err != nil {
			return err
		}
		for er.Next() {
			var n string
			if err := er.Scan(&n); err != nil {
				er.Close()
				return err
			}
			seen[n] = true
		}
		er.Close()
		if err := er.Err(); err != nil {
			return err
		}
		currency := money.LedgerCurrency(ctx)

		for _, row := range rows {
			number := strings.TrimSpace(row.Fields["document_number"])
			switch {
			case number == "":
				b.Error(row.Number, "document_number is required")
				continue
			case len(number) > 60:
				b.Error(row.Number, "document_number %q is longer than 60 characters", number)
				continue
			}
			date, ok := ParseDate(row.Fields["document_date"])
			if !ok {
				b.Error(row.Number, "document_date %q is not a date (use day/month/year, for example 25/12/2025)", row.Fields["document_date"])
				continue
			}
			if date.After(s.now().AddDate(0, 0, 1)) {
				b.Error(row.Number, "document_date %q is in the future", row.Fields["document_date"])
				continue
			}
			total, err := decimal.NewFromString(row.Fields["total"])
			if err != nil || total.IsNegative() {
				b.Error(row.Number, "total %q must be a number, zero or more", row.Fields["total"])
				continue
			}
			paid := decimal.Zero
			if v := row.Fields["paid"]; v != "" {
				if paid, err = decimal.NewFromString(v); err != nil || paid.IsNegative() {
					b.Error(row.Number, "paid %q must be a number, zero or more", v)
					continue
				}
			}
			if paid.GreaterThan(total) {
				b.Error(row.Number, "paid %s is more than the total %s", paid, total)
				continue
			}
			p, msg := resolveParty(parties, row.Fields["party_name"], allowed...)
			if msg != "" {
				b.Error(row.Number, "%s", msg)
				continue
			}
			if seen[number] {
				b.Duplicate(row.Number, "%s invoice %q is already in the history register", strings.ToLower(kind), number)
				continue
			}
			seen[number] = true
			if dryRun {
				b.Valid(row.Number)
				continue
			}
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			if _, err := s.pool.Q(ctx).Exec(ctx, `
				INSERT INTO historical_invoices (id, organisation_id, kind, party_id, document_number, document_date, total_amount, paid_amount, currency_code, notes, batch_id, created_by)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12)`,
				id, principal.OrganisationID, kind, p.id, number, date.Format("2006-01-02"), total, paid, currency, strings.TrimSpace(row.Fields["notes"]), batch, principal.UserID); err != nil {
				return err
			}
			b.Committed(row.Number)
		}
		if !dryRun {
			return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorType: audit.ActorUser, ActorUserID: &principal.UserID, Action: "legacyimport.invoices_imported",
				EntityType: "historical_invoices", EntityID: &batch, AfterState: map[string]any{"kind": kind, "rows": len(rows)}, At: s.now()})
		}
		return nil
	})
	if err != nil {
		return importer.Report{}, err
	}
	return b.Report(), nil
}

// HistoricalInvoice is one row of the history register.
type HistoricalInvoice struct {
	ID       uuid.UUID `json:"id"`
	Kind     string    `json:"kind"`
	PartyID  uuid.UUID `json:"party_id"`
	Party    string    `json:"party_name"`
	Number   string    `json:"document_number"`
	Date     string    `json:"document_date"`
	Total    string    `json:"total"`
	Paid     string    `json:"paid"`
	Due      string    `json:"due"`
	Currency string    `json:"currency"`
	Notes    string    `json:"notes,omitempty"`
}

// ListHistoricalInvoices returns the register, newest first, optionally for one kind and a search on number or party.
func (s *Service) ListHistoricalInvoices(ctx context.Context, principal permissions.Principal, kind, query string, limit int) ([]HistoricalInvoice, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	out := []HistoricalInvoice{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT h.id, h.kind, h.party_id, p.legal_name, h.document_number, h.document_date::text, h.total_amount::text, h.paid_amount::text, h.currency_code, COALESCE(h.notes, '')
			FROM historical_invoices h JOIN parties p ON p.id = h.party_id
			WHERE h.organisation_id = $1 AND ($2 = '' OR h.kind = $2)
			  AND ($3 = '' OR h.document_number ILIKE '%' || $3 || '%' OR p.legal_name ILIKE '%' || $3 || '%')
			ORDER BY h.document_date DESC, h.document_number DESC LIMIT $4`, principal.OrganisationID, kind, strings.TrimSpace(query), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h HistoricalInvoice
			if err := rows.Scan(&h.ID, &h.Kind, &h.PartyID, &h.Party, &h.Number, &h.Date, &h.Total, &h.Paid, &h.Currency, &h.Notes); err != nil {
				return err
			}
			t, _ := decimal.NewFromString(h.Total)
			pd, _ := decimal.NewFromString(h.Paid)
			h.Total, h.Paid, h.Due = t.StringFixed(2), pd.StringFixed(2), t.Sub(pd).StringFixed(2)
			out = append(out, h)
		}
		return rows.Err()
	})
	return out, err
}
