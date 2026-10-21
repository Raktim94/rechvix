package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	esignapp "rechvix/internal/modules/esign"
	notificationsapp "rechvix/internal/modules/notifications/app"
	notificationsdomain "rechvix/internal/modules/notifications/domain"
	salesapp "rechvix/internal/modules/sales/app"
	"rechvix/internal/modules/sales/printing"
	"rechvix/internal/platform/permissions"
)

// salesDocRenderer shows a sales document to a signer and fingerprints what they were shown.
type salesDocRenderer struct{ sales *salesapp.Service }

func (r salesDocRenderer) data(ctx context.Context, orgID, createdBy uuid.UUID, documentType string, documentID uuid.UUID) (*printing.InvoiceData, error) {
	if documentType != "sales_document" {
		return nil, fmt.Errorf("main: signing document type %q is not supported", documentType)
	}
	return r.sales.BuildInvoiceDataForShareLink(ctx, orgID, createdBy, documentID)
}

func (r salesDocRenderer) Render(ctx context.Context, orgID, createdBy uuid.UUID, documentType string, documentID uuid.UUID) ([]byte, string, string, error) {
	data, err := r.data(ctx, orgID, createdBy, documentType, documentID)
	if err != nil {
		return nil, "", "", err
	}
	name := data.DocumentNumber
	if name == "" {
		name = "document"
	}
	if printing.NeedsBrowserPrint(*data) { // Devanagari needs browser shaping: show the HTML page instead of a PDF
		html, err := printing.RenderHTML(printing.TemplateA4GSTInvoice, *data)
		return html, "text/html; charset=utf-8", name + ".html", err
	}
	pdf, err := printing.RenderPDF(printing.TemplateA4GSTInvoice, printing.ThemeClassic, *data)
	return pdf, "application/pdf", name + ".pdf", err
}

// Fingerprint hashes the printable content, ignoring the "signed by" note so a second pending request is unaffected
// when the first one is signed.
func (r salesDocRenderer) Fingerprint(ctx context.Context, orgID, createdBy uuid.UUID, documentType string, documentID uuid.UUID) (string, error) {
	data, err := r.data(ctx, orgID, createdBy, documentType, documentID)
	if err != nil {
		return "", err
	}
	cp := *data
	cp.SignedNote = ""
	b, err := json.Marshal(cp)
	if err != nil {
		return "", err
	}
	return esignapp.Fingerprint(b), nil
}

// notificationsMailer lets esign queue the signing email through the notifications outbox.
type notificationsMailer struct{ svc *notificationsapp.Service }

func (m notificationsMailer) EmailConfigured() bool { return m.svc.EmailConfigured() }

func (m notificationsMailer) SendSigningLink(ctx context.Context, p permissions.Principal, documentID uuid.UUID, to, subject, bodyHTML string) error {
	return m.svc.QueueSend(ctx, p, notificationsapp.SendPayload{Channel: notificationsdomain.ChannelEmail, Recipient: to, DocumentType: "sales_document", DocumentID: documentID, Subject: subject, BodyHTML: bodyHTML})
}
