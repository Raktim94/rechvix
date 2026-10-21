//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/sepa"
	"rechvix/internal/platform/audit"
	appcrypto "rechvix/internal/platform/crypto"
	"rechvix/internal/platform/permissions"
)

func nextWeekday(from time.Time, add int) time.Time {
	d := from
	for n := 0; n < add; {
		d = d.AddDate(0, 0, 1)
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			n++
		}
	}
	return d
}

func TestSEPA_MandateBatchAndCollection(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	aead, _ := appcrypto.NewAEAD(key)
	svc := sepa.NewService(sharedPool, acct, aead, permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool), audit.NewPGRecorder(sharedPool))
	inv := finalizedSale(t, ctx, salesSvc, fx, "1", "100") // 121.00
	when := nextWeekday(time.Now(), 3)

	if _, err := svc.CreateBatch(ctx, fx.Principal, fx.LegalEntityID, when, []uuid.UUID{inv.ID}); !errors.Is(err, sepa.ErrInvalid) {
		t.Fatalf("no creditor details yet: %v", err)
	}
	if err := svc.SaveSettings(ctx, fx.Principal, fx.LegalEntityID, sepa.Settings{CreditorName: "Empresa Demo SL", CreditorID: "ES12ZZZ12345678", IBAN: "ES91 2100 0418 4502 0005 1332"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveSettings(ctx, fx.Principal, fx.LegalEntityID, sepa.Settings{CreditorName: "x", CreditorID: "nope", IBAN: "ES9121000418450200051332"}); !errors.Is(err, sepa.ErrInvalid) {
		t.Fatalf("a bad creditor id is refused: %v", err)
	}
	if _, err := svc.CreateBatch(ctx, fx.Principal, fx.LegalEntityID, when, []uuid.UUID{inv.ID}); !errors.Is(err, sepa.ErrInvalid) || !strings.Contains(err.Error(), "mandate") {
		t.Fatalf("the customer has no mandate: %v", err)
	}
	if _, err := svc.CreateMandate(ctx, fx.Principal, sepa.MandateInput{PartyID: fx.CustomerID, SignedOn: time.Now(), IBAN: "ES91 2100 0418 4502 0005 1333", AccountHolder: "Cliente"}); !errors.Is(err, sepa.ErrInvalid) {
		t.Fatalf("a wrong IBAN checksum is refused: %v", err)
	}
	m, err := svc.CreateMandate(ctx, fx.Principal, sepa.MandateInput{PartyID: fx.CustomerID, SignedOn: time.Now().AddDate(0, 0, -3), IBAN: "DE89 3704 0044 0532 0130 00", AccountHolder: "Müller & Söhne"})
	if err != nil {
		t.Fatal(err)
	}
	if m.IBANLast4 != "3000" || m.Ref == "" || m.Status != "ACTIVE" {
		t.Fatalf("mandate: %+v", m)
	}
	var raw []byte
	_ = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx, `SELECT iban_encrypted FROM sepa_mandates WHERE id = $1`, m.ID).Scan(&raw)
	})
	if strings.Contains(string(raw), "DE89") {
		t.Fatal("the debtor IBAN must be stored encrypted")
	}

	if _, err := svc.CreateBatch(ctx, fx.Principal, fx.LegalEntityID, time.Now(), []uuid.UUID{inv.ID}); !errors.Is(err, sepa.ErrInvalid) {
		t.Fatalf("a collection date that is too soon is refused: %v", err)
	}
	batch, err := svc.CreateBatch(ctx, fx.Principal, fx.LegalEntityID, when, []uuid.UUID{inv.ID})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if batch.Total != "121.00" || batch.Count != 1 || batch.Status != "DRAFT" {
		t.Fatalf("batch: %+v", batch)
	}
	xml, name, err := svc.XML(ctx, fx.Principal, batch.ID)
	if err != nil || !strings.HasSuffix(name, ".xml") || !strings.Contains(xml, "<SeqTp>FRST</SeqTp>") || !strings.Contains(xml, "DE89370400440532013000") || !strings.Contains(xml, "Muller + Sohne") {
		t.Fatalf("file: %v\n%s", err, xml)
	}
	if _, err := svc.CreateBatch(ctx, fx.Principal, fx.LegalEntityID, when, []uuid.UUID{inv.ID}); !errors.Is(err, sepa.ErrInvalid) {
		t.Fatalf("an invoice already in a debit cannot be added again: %v", err)
	}
	cols, _ := svc.Collections(ctx, fx.Principal, batch.ID)
	if len(cols) != 1 || cols[0].Status != "PENDING" || cols[0].SequenceType != "FRST" {
		t.Fatalf("collections: %+v", cols)
	}
	if err := svc.Result(ctx, fx.Principal, cols[0].ID, true, ""); !errors.Is(err, sepa.ErrInvalid) {
		t.Fatalf("results need the batch marked as sent: %v", err)
	}
	if err := svc.MarkSent(ctx, fx.Principal, batch.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Discard(ctx, fx.Principal, batch.ID); !errors.Is(err, sepa.ErrInvalid) {
		t.Fatalf("a sent batch cannot be discarded: %v", err)
	}
	if err := svc.Result(ctx, fx.Principal, cols[0].ID, true, ""); err != nil {
		t.Fatalf("collected: %v", err)
	}
	if got := receiptTotal(t, ctx, fx, inv.ID); !got.Equal(decimal.RequireFromString("121")) {
		t.Fatalf("the collection books a receipt of 121: %s", got)
	}
	if err := svc.Result(ctx, fx.Principal, cols[0].ID, true, ""); !errors.Is(err, sepa.ErrInvalid) {
		t.Fatalf("a result is recorded once: %v", err)
	}

	// The next invoice for the same customer uses RCUR; a rejected debit releases the invoice for another attempt.
	inv2 := finalizedSale(t, ctx, salesSvc, fx, "1", "50")
	b2, err := svc.CreateBatch(ctx, fx.Principal, fx.LegalEntityID, when, []uuid.UUID{inv2.ID})
	if err != nil {
		t.Fatal(err)
	}
	if xml2, _, _ := svc.XML(ctx, fx.Principal, b2.ID); !strings.Contains(xml2, "<SeqTp>RCUR</SeqTp>") || strings.Contains(xml2, "<SeqTp>FRST</SeqTp>") {
		t.Fatal("after a sent first collection the mandate is recurring")
	}
	_ = svc.MarkSent(ctx, fx.Principal, b2.ID)
	c2, _ := svc.Collections(ctx, fx.Principal, b2.ID)
	if err := svc.Result(ctx, fx.Principal, c2[0].ID, false, "AC04 account closed"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateBatch(ctx, fx.Principal, fx.LegalEntityID, when, []uuid.UUID{inv2.ID}); err != nil {
		t.Fatalf("a rejected invoice can be collected again: %v", err)
	}
	if err := svc.RevokeMandate(ctx, fx.Principal, m.ID); err != nil {
		t.Fatal(err)
	}
	inv3 := finalizedSale(t, ctx, salesSvc, fx, "1", "10")
	if _, err := svc.CreateBatch(ctx, fx.Principal, fx.LegalEntityID, when, []uuid.UUID{inv3.ID}); !errors.Is(err, sepa.ErrInvalid) {
		t.Fatalf("a revoked mandate collects nothing: %v", err)
	}
	other := spainFixture(t, ctx, acct)
	if rows, _ := svc.Mandates(ctx, other.Principal, nil); len(rows) != 0 {
		t.Fatal("tenant isolation")
	}
}
