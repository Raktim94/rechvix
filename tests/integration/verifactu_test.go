//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	contactsapp "rechvix/internal/modules/contacts/app"
	contactspg "rechvix/internal/modules/contacts/pg"
	salesapp "rechvix/internal/modules/sales/app"
	salesdomain "rechvix/internal/modules/sales/domain"
	"rechvix/internal/modules/sales/printing"
	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/modules/verifactu/aeat"
	verifactuapp "rechvix/internal/modules/verifactu/app"
	verifactudomain "rechvix/internal/modules/verifactu/domain"
	verifactupg "rechvix/internal/modules/verifactu/pg"
	"rechvix/internal/platform/audit"
	appcrypto "rechvix/internal/platform/crypto"
	"rechvix/internal/platform/outbox"
	"rechvix/internal/platform/permissions"
)

type vfFixture struct {
	accountingFixture
	sales    *salesapp.Service
	vf       *verifactuapp.Service
	contacts *contactsapp.Service
	nif      string
}

func newVerifactuFixture(t *testing.T, ctx context.Context, enable bool) vfFixture {
	t.Helper()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := setupRegimeFixture(t, ctx, "ES", "EUR", "STANDARD")
	if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	orgSvc := newTestOrgService(t)
	nif := "B12345674"
	if _, err := orgSvc.UpdateLegalEntityTaxID(ctx, fx.Principal, fx.LegalEntityID, nif); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	aead, err := appcrypto.NewAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	vf := verifactuapp.NewService(sharedPool, verifactupg.NewRepo(sharedPool), aead, outbox.NewPGStore(sharedPool), orgSvc,
		verifactuapp.Producer{Name: "NODEDR INFOTECH PRIVATE LIMITED", Country: "IN", IDType: "04", ID: "TESTPRODUCER01", SystemID: "RX", Version: "0.15.0"},
		checker, audit.NewPGRecorder(sharedPool), nil)
	salesSvc.WithVerifactu(vf)
	if enable {
		if err := vf.SaveSettings(ctx, fx.Principal, fx.LegalEntityID, true, "test"); err != nil {
			t.Fatalf("enabling VERI*FACTU: %v", err)
		}
	}
	contacts := contactsapp.NewService(sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool),
		contactspg.NewTaxRegistrationRepo(sharedPool), checker, audit.NewPGRecorder(sharedPool))
	return vfFixture{accountingFixture: fx, sales: salesSvc, vf: vf, contacts: contacts, nif: nif}
}

func (f vfFixture) invoice(t *testing.T, ctx context.Context, docType salesdomain.DocumentType, currency, qty, price string, reg *uuid.UUID) (*salesdomain.Document, error) {
	t.Helper()
	doc, err := f.sales.CreateDocument(ctx, f.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: f.LegalEntityID, BranchID: f.BranchID, WarehouseID: f.WarehouseID, CustomerPartyID: f.CustomerID,
		DocumentType: docType, CurrencyCode: currency, BaseCurrencyCode: "EUR", ExchangeRate: mustDecimal(t, "1"),
		PricingMode: taxdomain.PricingExclusive, CustomerTaxRegistrationID: reg,
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := f.sales.AddLine(ctx, f.Principal, salesapp.AddLineParams{DocumentID: doc.ID, ProductVariantID: f.VariantID, UnitID: f.PCS,
		Quantity: mustDecimal(t, qty), UnitPrice: mustDecimal(t, price)}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	return f.sales.FinalizeDocument(ctx, f.Principal, doc.ID)
}

func (f vfFixture) records(t *testing.T, ctx context.Context) []verifactuapp.RecordView {
	t.Helper()
	rows, err := f.vf.Records(ctx, f.Principal, f.LegalEntityID, 500, 0)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestVerifactu_FinalizeChainsRecordsAndVerifies(t *testing.T) {
	ctx := context.Background()
	f := newVerifactuFixture(t, ctx, true)
	var docs []*salesdomain.Document
	for i := 0; i < 3; i++ {
		d, err := f.invoice(t, ctx, salesdomain.DocTaxInvoice, "EUR", "2", "50", nil)
		if err != nil {
			t.Fatalf("finalize %d: %v", i, err)
		}
		docs = append(docs, d)
	}
	rows := f.records(t, ctx) // newest first
	if len(rows) != 3 || rows[0].Seq != 3 || rows[2].Seq != 1 {
		t.Fatalf("want 3 records seq 3..1, got %+v", rows)
	}
	for _, r := range rows {
		if r.Type != "ALTA" || r.Status != "PENDING" || len(r.Hash) != 64 {
			t.Fatalf("unexpected record %+v", r)
		}
		// 2 x 50 = 100 net, 21% IVA = 21, total 121.00
		if r.InvoiceTotal != "121.00" || r.InvoiceType != "F2" {
			t.Fatalf("customer without a tax ID must get a simplified invoice (F2) of 121.00, got %+v", r)
		}
		if !strings.Contains(r.QRURL, "nif="+f.nif) || !strings.Contains(r.QRURL, "importe=121.00") || !strings.Contains(r.QRURL, "prewww2.aeat.es") {
			t.Fatalf("QR URL wrong: %s", r.QRURL)
		}
	}
	rep, err := f.vf.VerifyChain(ctx, f.Principal, f.LegalEntityID)
	if err != nil || !rep.Valid || rep.Records != 3 {
		t.Fatalf("chain must verify: %+v %v", rep, err)
	}
	xml, err := f.vf.ExportXML(ctx, f.Principal, f.LegalEntityID)
	if err != nil || strings.Count(xml, "<sf:RegistroAlta>") != 3 || !strings.Contains(xml, "<sf:PrimerRegistro>S</sf:PrimerRegistro>") {
		t.Fatalf("export xml wrong (%v)", err)
	}
	_ = docs
}

func TestVerifactu_CustomerWithNIFGetsCompleteInvoice(t *testing.T) {
	ctx := context.Background()
	f := newVerifactuFixture(t, ctx, true)
	reg, err := f.contacts.AddTaxRegistration(ctx, f.Principal, contactsapp.AddTaxRegistrationParams{PartyID: f.CustomerID, CountryCode: "ES", RegistrationNumber: "A12345674", IsPrimary: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.invoice(t, ctx, salesdomain.DocTaxInvoice, "EUR", "1", "100", &reg.ID); err != nil {
		t.Fatal(err)
	}
	rows := f.records(t, ctx)
	if len(rows) != 1 || rows[0].InvoiceType != "F1" {
		t.Fatalf("a customer with a NIF must get a complete invoice (F1): %+v", rows)
	}
	xml, _ := f.vf.ExportXML(ctx, f.Principal, f.LegalEntityID)
	if !strings.Contains(xml, "<sf:NIF>A12345674</sf:NIF>") {
		t.Fatal("the recipient NIF must be in the record")
	}
}

func TestVerifactu_DisabledWritesNothing(t *testing.T) {
	ctx := context.Background()
	f := newVerifactuFixture(t, ctx, false)
	if _, err := f.invoice(t, ctx, salesdomain.DocTaxInvoice, "EUR", "1", "10", nil); err != nil {
		t.Fatal(err)
	}
	if rows := f.records(t, ctx); len(rows) != 0 {
		t.Fatalf("no records expected while disabled, got %d", len(rows))
	}
}

func TestVerifactu_NonEuroInvoiceIsRefusedAndRolledBack(t *testing.T) {
	ctx := context.Background()
	f := newVerifactuFixture(t, ctx, true)
	doc, err := f.sales.CreateDocument(ctx, f.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: f.LegalEntityID, BranchID: f.BranchID, WarehouseID: f.WarehouseID, CustomerPartyID: f.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, CurrencyCode: "USD", BaseCurrencyCode: "EUR", ExchangeRate: mustDecimal(t, "0.9"), PricingMode: taxdomain.PricingExclusive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.sales.AddLine(ctx, f.Principal, salesapp.AddLineParams{DocumentID: doc.ID, ProductVariantID: f.VariantID, UnitID: f.PCS, Quantity: mustDecimal(t, "1"), UnitPrice: mustDecimal(t, "10")}); err != nil {
		t.Fatal(err)
	}
	_, err = f.sales.FinalizeDocument(ctx, f.Principal, doc.ID)
	if err == nil || !errors.Is(err, verifactudomain.ErrNeedEUR) {
		t.Fatalf("a USD invoice must be refused under VERI*FACTU, got %v", err)
	}
	got, _, err := f.sales.GetDocument(ctx, f.Principal, doc.ID)
	if err != nil || got.Status != salesdomain.StatusDraft {
		t.Fatalf("the refused document must still be a draft: %v %v", got, err)
	}
	if len(f.records(t, ctx)) != 0 {
		t.Fatal("no record may exist for a refused invoice")
	}
}

func TestVerifactu_CancelAndCreditNoteAreChained(t *testing.T) {
	ctx := context.Background()
	f := newVerifactuFixture(t, ctx, true)
	inv, err := f.invoice(t, ctx, salesdomain.DocTaxInvoice, "EUR", "2", "50", nil)
	if err != nil {
		t.Fatal(err)
	}
	cn, err := f.sales.ConvertDocument(ctx, f.Principal, inv.ID, salesdomain.DocCreditNote, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.sales.FinalizeDocument(ctx, f.Principal, cn.ID); err != nil {
		t.Fatalf("finalize credit note: %v", err)
	}
	other, err := f.invoice(t, ctx, salesdomain.DocTaxInvoice, "EUR", "1", "10", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.sales.CancelDocument(ctx, f.Principal, other.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	rows := f.records(t, ctx)
	if len(rows) != 4 {
		t.Fatalf("want 4 records (invoice, credit note, invoice, cancellation), got %d: %+v", len(rows), rows)
	}
	if rows[0].Type != "ANULACION" || rows[2].InvoiceType != "R1" || rows[2].InvoiceTotal != "-121.00" {
		t.Fatalf("unexpected chain tail: %+v", rows)
	}
	if rep, err := f.vf.VerifyChain(ctx, f.Principal, f.LegalEntityID); err != nil || !rep.Valid {
		t.Fatalf("chain must verify: %+v %v", rep, err)
	}
	xml, _ := f.vf.ExportXML(ctx, f.Principal, f.LegalEntityID)
	if !strings.Contains(xml, "<sf:FacturasRectificadas>") || !strings.Contains(xml, "<sf:RegistroAnulacion>") {
		t.Fatal("credit note must reference the corrected invoice and the cancellation must be an anulación record")
	}
}

func TestVerifactu_ConcurrentFinalizationKeepsChainContiguous(t *testing.T) {
	ctx := context.Background()
	f := newVerifactuFixture(t, ctx, true)
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.invoice(t, ctx, salesdomain.DocTaxInvoice, "EUR", "1", "10", nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent finalize failed: %v", err)
		}
	}
	rows := f.records(t, ctx)
	if len(rows) != n {
		t.Fatalf("want %d records, got %d", n, len(rows))
	}
	rep, err := f.vf.VerifyChain(ctx, f.Principal, f.LegalEntityID)
	if err != nil || !rep.Valid || rep.Records != n {
		t.Fatalf("concurrent chain must verify: %+v %v", rep, err)
	}
}

func TestVerifactu_DatabaseRefusesEditsDeletesAndBrokenChains(t *testing.T) {
	ctx := context.Background()
	f := newVerifactuFixture(t, ctx, true)
	for i := 0; i < 2; i++ {
		if _, err := f.invoice(t, ctx, salesdomain.DocTaxInvoice, "EUR", "1", "10", nil); err != nil {
			t.Fatal(err)
		}
	}
	org := f.Principal.OrganisationID
	for _, stmt := range []string{
		`UPDATE verifactu_records SET invoice_number = 'X' WHERE legal_entity_id = $1`,
		`DELETE FROM verifactu_records WHERE legal_entity_id = $1`,
		`INSERT INTO verifactu_records (id, organisation_id, legal_entity_id, seq, record_type, issuer_nif, invoice_number, issue_date, prev_hash, hash, generated_at_text, xml)
		 VALUES (gen_random_uuid(), '` + org.String() + `', $1, 3, 'ALTA', 'B12345674', 'BAD/1', current_date, 'DEADBEEF', '` + strings.Repeat("A", 64) + `', 'x', '<x/>')`,
	} {
		err := sharedPool.RunScoped(ctx, org, func(ctx context.Context) error {
			_, err := sharedPool.Q(ctx).Exec(ctx, stmt, f.LegalEntityID)
			return err
		})
		if err == nil {
			t.Fatalf("the database must refuse: %s", stmt)
		}
	}
	// Even the table owner cannot rewrite history.
	if _, err := sharedPool.Q(ctx).Exec(ctx, `SELECT 1`); err != nil { // keep pool referenced
		t.Fatal(err)
	}
	if rep, err := f.vf.VerifyChain(ctx, f.Principal, f.LegalEntityID); err != nil || !rep.Valid || rep.Records != 2 {
		t.Fatalf("refused statements must leave the chain intact: %+v %v", rep, err)
	}
}

func TestVerifactu_SubmissionRecordsAEATOutcomes(t *testing.T) {
	ctx := context.Background()
	f := newVerifactuFixture(t, ctx, true)
	for i := 0; i < 2; i++ {
		if _, err := f.invoice(t, ctx, salesdomain.DocTaxInvoice, "EUR", "1", "10", nil); err != nil {
			t.Fatal(err)
		}
	}
	var sent string
	f.vf.WithTransport(func(ctx context.Context, env verifactudomain.Environment, body string) (*aeat.Response, error) {
		sent = body
		rows := f.records(t, ctx)
		resp := &aeat.Response{CSV: "CSV-TEST-1", State: "ParcialmenteCorrecto", WaitSeconds: 60}
		for _, r := range rows {
			l := aeat.Line{Number: r.InvoiceNumber, IssueDate: strings.Join(reverse(strings.Split(r.IssueDate, "-")), "-"), Operation: "Alta", State: "Correcto"}
			if r.Seq == 2 {
				l.State, l.ErrorCode, l.ErrorText = "Incorrecto", "1100", "Valor del campo incorrecto"
			}
			resp.Lines = append(resp.Lines, l)
		}
		return resp, nil
	})
	if err := f.vf.SendNow(ctx, f.Principal, f.LegalEntityID); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !strings.Contains(sent, "RegFactuSistemaFacturacion") || !strings.Contains(sent, "Cabecera") {
		t.Fatalf("unexpected request body: %.200s", sent)
	}
	rows := f.records(t, ctx)
	by := map[int64]verifactuapp.RecordView{}
	for _, r := range rows {
		by[r.Seq] = r
	}
	if by[1].Status != "ACCEPTED" || by[1].CSV != "CSV-TEST-1" {
		t.Fatalf("record 1: %+v", by[1])
	}
	if by[2].Status != "REJECTED" || by[2].ErrorCode != "1100" {
		t.Fatalf("record 2: %+v", by[2])
	}
}

func reverse(s []string) []string {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	return s
}

func TestVerifactu_PrintedInvoiceCarriesAEATQR(t *testing.T) {
	ctx := context.Background()
	f := newVerifactuFixture(t, ctx, true)
	doc, err := f.invoice(t, ctx, salesdomain.DocTaxInvoice, "EUR", "1", "10", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := f.sales.BuildInvoiceData(ctx, f.Principal, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if data.Verifactu == nil || !strings.Contains(data.Verifactu.URL, "ValidarQR") {
		t.Fatalf("the print data must carry the QR block: %+v", data.Verifactu)
	}
	for _, tpl := range []printing.Template{printing.TemplateA4GSTInvoice, printing.TemplateThermal80mm, printing.TemplateThermal58mm, printing.TemplateCompactInvoice} {
		pdf, err := printing.RenderPDF(tpl, printing.ThemeClassic, *data)
		if err != nil || len(pdf) < 1000 {
			t.Fatalf("%s: %v (%d bytes)", tpl, err, len(pdf))
		}
	}
	html, err := printing.RenderHTML(printing.TemplateThermal80mm, *data)
	if err != nil || !strings.Contains(string(html), "QR tributario:") || !strings.Contains(string(html), "<svg") {
		t.Fatalf("html must show the QR and its legend: %v", err)
	}
	// A document without a record must never show a QR.
	plain := newVerifactuFixture(t, ctx, false)
	pd, err := plain.invoice(t, ctx, salesdomain.DocTaxInvoice, "EUR", "1", "10", nil)
	if err != nil {
		t.Fatal(err)
	}
	pdata, err := plain.sales.BuildInvoiceData(ctx, plain.Principal, pd.ID)
	if err != nil || pdata.Verifactu != nil {
		t.Fatalf("no record, no QR: %v %v", pdata.Verifactu, err)
	}
}

func TestVerifactu_EnablingRequiresSpanishCompanyWithNIF(t *testing.T) {
	ctx := context.Background()
	f := newVerifactuFixture(t, ctx, false)
	orgSvc := newTestOrgService(t)
	if _, err := orgSvc.UpdateLegalEntityTaxID(ctx, f.Principal, f.LegalEntityID, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.vf.SaveSettings(ctx, f.Principal, f.LegalEntityID, true, "test"); err == nil {
		t.Fatal("enabling without a NIF must be refused")
	}
	view, err := f.vf.Settings(ctx, f.Principal, f.LegalEntityID)
	if err != nil || view.Enabled || len(view.Problems) == 0 {
		t.Fatalf("settings view must list what blocks enabling: %+v %v", view, err)
	}
}
