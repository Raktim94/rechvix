//go:build integration

package integration

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	esignapp "rechvix/internal/modules/esign"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

type fakeRenderer struct{ version string }

func (f *fakeRenderer) Render(context.Context, uuid.UUID, uuid.UUID, string, uuid.UUID) ([]byte, string, string, error) {
	return []byte("%PDF-fake " + f.version), "application/pdf", "doc.pdf", nil
}
func (f *fakeRenderer) Fingerprint(context.Context, uuid.UUID, uuid.UUID, string, uuid.UUID) (string, error) {
	return esignapp.Fingerprint([]byte(f.version)), nil
}

type fakeMailer struct {
	sent []string
}

func (f *fakeMailer) EmailConfigured() bool { return true }
func (f *fakeMailer) SendSigningLink(_ context.Context, _ permissions.Principal, _ uuid.UUID, to, _, body string) error {
	f.sent = append(f.sent, to+"|"+body)
	return nil
}

func signaturePNGDataURL() string {
	// 1x1 PNG
	b, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
}

func TestESign_RequestSignDeclineAndGuards(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	doc := finalizedSale(t, ctx, salesSvc, fx, "1", "100")
	rend, mail := &fakeRenderer{version: "v1"}, &fakeMailer{}
	svc := esignapp.NewService(sharedPool, rend, mail, permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool), audit.NewPGRecorder(sharedPool))

	if _, _, err := svc.Create(ctx, fx.Principal, "purchase_document", doc.ID, "", "", ""); !errors.Is(err, esignapp.ErrInvalid) {
		t.Fatalf("only sales documents can be signed: %v", err)
	}
	req, raw, err := svc.Create(ctx, fx.Principal, "sales_document", doc.ID, "Ana Ruiz", "ana@example.org", "https://billing.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 1 || !strings.Contains(mail.sent[0], "https://billing.example.com/sign/"+raw) {
		t.Fatalf("the signing link is emailed: %v", mail.sent)
	}
	pub, err := svc.Lookup(ctx, raw)
	if err != nil || pub.Status != "PENDING" || pub.Name != "Ana Ruiz" || pub.Consent == "" {
		t.Fatalf("lookup: %+v %v", pub, err)
	}
	if _, err := svc.Lookup(ctx, "wrong-token"); !errors.Is(err, esignapp.ErrLink) {
		t.Fatalf("unknown token: %v", err)
	}
	data, ct, _, err := svc.Document(ctx, raw)
	if err != nil || ct != "application/pdf" || !strings.Contains(string(data), "v1") {
		t.Fatalf("the signer sees the document: %v", err)
	}

	if _, err := svc.Submit(ctx, raw, esignapp.SignInput{Name: "A", Agree: true}); !errors.Is(err, esignapp.ErrInvalid) {
		t.Fatalf("a real name is required: %v", err)
	}
	if _, err := svc.Submit(ctx, raw, esignapp.SignInput{Name: "Ana Ruiz", Agree: false}); !errors.Is(err, esignapp.ErrInvalid) {
		t.Fatalf("consent is required: %v", err)
	}
	if _, err := svc.Submit(ctx, raw, esignapp.SignInput{Name: "Ana Ruiz", Agree: true, DrawingPNG: []byte("not a png at all")}); !errors.Is(err, esignapp.ErrInvalid) {
		t.Fatalf("a drawing must be a PNG: %v", err)
	}
	png, err := esignapp.DataURLToPNG(signaturePNGDataURL())
	if err != nil {
		t.Fatal(err)
	}
	// The document changing after the request refuses the signature.
	rend.version = "v2"
	if _, err := svc.Submit(ctx, raw, esignapp.SignInput{Name: "Ana Ruiz", Agree: true}); !errors.Is(err, esignapp.ErrChanged) {
		t.Fatalf("a changed document must not be signed: %v", err)
	}
	rend.version = "v1"
	signed, err := svc.Submit(ctx, raw, esignapp.SignInput{Name: "  Ana Ruiz ", Agree: true, DrawingPNG: png, IP: "203.0.113.9", Agent: "TestBrowser/1"})
	if err != nil || signed.Status != "SIGNED" || signed.SignerName != "Ana Ruiz" || signed.SignedAt == nil {
		t.Fatalf("sign: %+v %v", signed, err)
	}
	if _, err := svc.Submit(ctx, raw, esignapp.SignInput{Name: "Ana Ruiz", Agree: true}); !errors.Is(err, esignapp.ErrLink) {
		t.Fatalf("a signed request cannot be signed again: %v", err)
	}
	reqs, _ := svc.List(ctx, fx.Principal, "sales_document", doc.ID)
	if len(reqs) != 1 || reqs[0].Status != "SIGNED" || !reqs[0].HasDrawing || reqs[0].SignerIP != "203.0.113.9" || reqs[0].DocumentHash == "" || reqs[0].ID != req.ID {
		t.Fatalf("evidence kept: %+v", reqs)
	}
	note, err := svc.SignedNote(ctx, doc.ID)
	if err != nil || note == nil || note.Name != "Ana Ruiz" || len(note.Ref) != 8 {
		t.Fatalf("signed note: %+v %v", note, err)
	}

	// Decline, revoke and expiry.
	_, raw2, _ := svc.Create(ctx, fx.Principal, "sales_document", doc.ID, "", "", "")
	if p, err := svc.Submit(ctx, raw2, esignapp.SignInput{Decline: true, Reason: "Wrong price"}); err != nil || p.Status != "DECLINED" {
		t.Fatalf("decline: %+v %v", p, err)
	}
	req3, raw3, _ := svc.Create(ctx, fx.Principal, "sales_document", doc.ID, "", "", "")
	if err := svc.Revoke(ctx, fx.Principal, req3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Lookup(ctx, raw3); !errors.Is(err, esignapp.ErrLink) {
		t.Fatalf("a revoked link is dead: %v", err)
	}
	_, raw4, _ := svc.Create(ctx, fx.Principal, "sales_document", doc.ID, "", "", "")
	if _, err := sharedPool.Q(ctx).Exec(ctx, `UPDATE document_signatures SET expires_at = now() - interval '1 day' WHERE document_id = $1 AND status = 'PENDING'`, doc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Submit(ctx, raw4, esignapp.SignInput{Name: "Ana Ruiz", Agree: true}); !errors.Is(err, esignapp.ErrLink) {
		t.Fatalf("an expired request cannot be signed: %v", err)
	}
}
