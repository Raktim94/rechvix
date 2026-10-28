package crm

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/legacyimport"
	"rechvix/internal/platform/importer"
	"rechvix/internal/platform/permissions"
)

// LeadFields are the columns of a leads file. The synonyms are the headers
// other CRMs, lead-generation exports and hand-made sheets use, so a file
// maps without renaming anything first.
var LeadFields = []importer.Field{
	{Name: "title", Label: "Lead / deal name", Synonyms: []string{"Lead Name", "Deal", "Deal Name", "Opportunity", "Opportunity Name", "Subject"}},
	{Name: "company_name", Label: "Company", Synonyms: []string{"Company Name", "Organisation", "Organization", "Business", "Business Name", "Firm", "Account"}},
	{Name: "contact_name", Label: "Contact person", Synonyms: []string{"Contact", "Contact Name", "Name", "Full Name", "Person", "Customer Name", "Lead"}},
	{Name: "contact_phone", Label: "Phone", Synonyms: []string{"Phone", "Phone No.", "Phone Number", "Mobile", "Mobile No.", "Mobile Number", "Contact Number", "WhatsApp", "Cell"}},
	{Name: "contact_email", Label: "Email", Synonyms: []string{"Email", "E-mail", "Email Address", "Mail"}},
	{Name: "value", Label: "Deal value", Numeric: true, Synonyms: []string{"Value", "Amount", "Deal Value", "Budget", "Expected Revenue", "Potential"}},
	{Name: "currency", Label: "Currency", Synonyms: []string{"Currency Code"}},
	{Name: "source", Label: "Source", Synonyms: []string{"Lead Source", "Channel", "Origin", "Campaign"}},
	{Name: "stage", Label: "Stage", Synonyms: []string{"Status", "Pipeline Stage", "Lead Status"}},
	{Name: "owner", Label: "Owner", Synonyms: []string{"Assigned To", "Sales Person", "Salesperson", "Sales Rep", "Lead Owner", "Executive"}},
	{Name: "expected_close", Label: "Expected close date", Synonyms: []string{"Close Date", "Expected Close", "Follow Up Date", "Closing Date"}},
	{Name: "notes", Label: "Notes", Synonyms: []string{"Notes", "Remarks", "Comments", "Description", "Requirement"}},
}

var nonDigit = regexp.MustCompile(`\D`)

// phoneKey reduces a phone number to its last ten digits, so 98765 43210,
// +91-9876543210 and 09876543210 are recognised as the same lead.
func phoneKey(s string) string {
	d := nonDigit.ReplaceAllString(s, "")
	if len(d) > 10 {
		d = d[len(d)-10:]
	}
	if len(d) < 7 {
		return ""
	}
	return d
}

// Import creates one open lead per row. A row whose phone, email or
// name-and-company matches a lead already in the CRM (or earlier in the same
// file) is reported as a duplicate and skipped, so a file can be re-run
// safely. Rows are independent: a bad row never stops the others.
func (s *Service) Import(ctx context.Context, principal permissions.Principal, rows []importer.Row, dryRun bool) (importer.Report, error) {
	if err := s.manage(ctx, principal); err != nil {
		return importer.Report{}, err
	}
	b := importer.NewBuilder(dryRun)

	seen := map[string]bool{}
	var currency string
	stages := map[string]uuid.UUID{}
	var first uuid.UUID
	owners := map[string]uuid.UUID{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.ensureStages(ctx, principal.OrganisationID); err != nil {
			return err
		}
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT default_currency_code FROM organisations WHERE id = $1`, principal.OrganisationID).Scan(&currency); err != nil {
			return err
		}
		rs, err := s.pool.Q(ctx).Query(ctx, `SELECT id, lower(name), kind FROM crm_stages WHERE organisation_id = $1 ORDER BY sort_order`, principal.OrganisationID)
		if err != nil {
			return err
		}
		for rs.Next() {
			var id uuid.UUID
			var name, kind string
			if err := rs.Scan(&id, &name, &kind); err != nil {
				rs.Close()
				return err
			}
			stages[name] = id
			if first == uuid.Nil && kind == "OPEN" {
				first = id
			}
		}
		rs.Close()
		us, err := s.pool.Q(ctx).Query(ctx, `SELECT id, lower(full_name), lower(email) FROM users WHERE organisation_id = $1 AND status = 'ACTIVE'`, principal.OrganisationID)
		if err != nil {
			return err
		}
		for us.Next() {
			var id uuid.UUID
			var name, email string
			if err := us.Scan(&id, &name, &email); err != nil {
				us.Close()
				return err
			}
			owners[name], owners[email] = id, id
		}
		us.Close()
		os, err := s.pool.Q(ctx).Query(ctx, `SELECT lower(title), lower(coalesce(company_name,'')), coalesce(contact_phone,''), lower(coalesce(contact_email,'')) FROM crm_opportunities WHERE organisation_id = $1`, principal.OrganisationID)
		if err != nil {
			return err
		}
		defer os.Close()
		for os.Next() {
			var title, company, phone, email string
			if err := os.Scan(&title, &company, &phone, &email); err != nil {
				return err
			}
			markSeen(seen, title, company, phone, email)
		}
		return os.Err()
	})
	if err != nil {
		return importer.Report{}, err
	}

	for _, row := range rows {
		f := row.Fields
		company := strings.TrimSpace(f["company_name"])
		contact := strings.TrimSpace(f["contact_name"])
		title := strings.TrimSpace(f["title"])
		if title == "" {
			title = company
		}
		if title == "" {
			title = contact
		}
		if title == "" {
			b.Error(row.Number, "needs a lead name, company or contact person")
			continue
		}
		in := Input{Title: title, CompanyName: company, ContactName: contact, ContactEmail: strings.TrimSpace(f["contact_email"]),
			ContactPhone: strings.TrimSpace(f["contact_phone"]), Currency: strings.ToUpper(strings.TrimSpace(f["currency"])),
			Source: strings.TrimSpace(f["source"]), Notes: strings.TrimSpace(f["notes"]), Value: decimal.Zero}
		if in.Currency == "" {
			in.Currency = currency
		}
		if v := strings.TrimSpace(f["value"]); v != "" {
			d, err := decimal.NewFromString(v)
			if err != nil {
				b.Error(row.Number, "deal value %q is not a number", v)
				continue
			}
			in.Value = d
		}
		if v := strings.TrimSpace(f["expected_close"]); v != "" {
			t, ok := legacyimport.ParseDate(v)
			if !ok {
				b.Error(row.Number, "expected close date %q is not a date (try 2026-12-31 or 31/12/2026)", v)
				continue
			}
			in.ExpectedClose = &t
		}
		if v := strings.ToLower(strings.TrimSpace(f["owner"])); v != "" {
			id, ok := owners[v]
			if !ok {
				b.Error(row.Number, "owner %q is not an active user (use their full name or email)", f["owner"])
				continue
			}
			in.OwnerUserID = &id
		}
		stageID := first
		if v := strings.ToLower(strings.TrimSpace(f["stage"])); v != "" {
			id, ok := stages[v]
			if !ok {
				b.Error(row.Number, "stage %q does not exist in your pipeline", f["stage"])
				continue
			}
			stageID = id
		}
		if err := in.validate(); err != nil {
			b.Error(row.Number, "%s", strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
			continue
		}
		if dup := matchSeen(seen, strings.ToLower(in.Title), strings.ToLower(in.CompanyName), in.ContactPhone, strings.ToLower(in.ContactEmail)); dup != "" {
			b.Duplicate(row.Number, "already in the CRM (same %s)", dup)
			continue
		}
		if dryRun {
			markSeen(seen, strings.ToLower(in.Title), strings.ToLower(in.CompanyName), in.ContactPhone, strings.ToLower(in.ContactEmail))
			b.Valid(row.Number)
			continue
		}
		o, err := s.Create(ctx, principal, in)
		if err != nil {
			if errors.Is(err, ErrInvalid) {
				b.Error(row.Number, "%s", strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
			} else {
				b.Error(row.Number, "could not save: %v", err)
			}
			continue
		}
		if stageID != first {
			if _, err := s.Move(ctx, principal, o.ID, stageID); err != nil {
				b.Committed(row.Number, "saved, but could not move it to the stage you asked for")
				markSeen(seen, strings.ToLower(in.Title), strings.ToLower(in.CompanyName), in.ContactPhone, strings.ToLower(in.ContactEmail))
				continue
			}
		}
		markSeen(seen, strings.ToLower(in.Title), strings.ToLower(in.CompanyName), in.ContactPhone, strings.ToLower(in.ContactEmail))
		b.Committed(row.Number)
	}
	return b.Report(), nil
}

func markSeen(seen map[string]bool, title, company, phone, email string) {
	if title != "" {
		seen["n:"+title+"|"+company] = true
	}
	if k := phoneKey(phone); k != "" {
		seen["p:"+k] = true
	}
	if email != "" {
		seen["e:"+email] = true
	}
}

func matchSeen(seen map[string]bool, title, company, phone, email string) string {
	if k := phoneKey(phone); k != "" && seen["p:"+k] {
		return "phone number"
	}
	if email != "" && seen["e:"+email] {
		return "email"
	}
	if seen["n:"+title+"|"+company] {
		return "name and company"
	}
	return ""
}
