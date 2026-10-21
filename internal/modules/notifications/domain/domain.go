// Package domain holds the notifications module's provider interfaces,
// entity types, and repository interfaces (docs/architecture.md §2,
// brief §20-21).
package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound    = errors.New("notifications: not found")
	ErrLinkInvalid = errors.New("notifications: share link invalid, expired, or revoked")
	// ErrEmailNotConfigured means no SMTP relay is set up on this server.
	ErrEmailNotConfigured = errors.New("notifications: email is not configured on this server (set SMTP_HOST)")
	ErrInvalidRecipient   = errors.New("notifications: invalid recipient")
)

// Channel is which transport a notification/share went out over.
type Channel string

const (
	ChannelEmail    Channel = "EMAIL"
	ChannelSMS      Channel = "SMS"
	ChannelWhatsApp Channel = "WHATSAPP"
)

// EmailProvider, SMSProvider, and WhatsAppProvider (brief §20-21) are
// deliberately separate, narrow interfaces — a deployment might configure
// only email, or email + WhatsApp, without needing an SMS account. Each
// method is synchronous from the caller's point of view, but the only
// caller in this codebase is notifications.Service's outbox Handler
// (registered against EventTypeSend), which itself runs on apps/worker,
// never inline with the HTTP request that triggered a share — so a slow
// or down provider never blocks the request that asked for a share link
// or a notification to be queued (brief Rule 12's reasoning, same as
// e-Invoice/webhooks).
type EmailProvider interface {
	SendEmail(ctx context.Context, to, subject, bodyHTML string, attachmentName string, attachment []byte) error
}

type SMSProvider interface {
	SendSMS(ctx context.Context, toE164 string, body string) error
}

// WhatsAppProvider is intentionally shaped around the official WhatsApp
// Business Platform's template-message model (brief §20: "no WhatsApp Web
// scraping, ever") — a real implementation calls Meta's Cloud API with a
// pre-approved template name and parameters, not a freeform message body.
// No real adapter ships in this stage: unlike e-Invoice's NIC sandbox
// (Stage 8), there is no public, credential-free WhatsApp Business API
// sandbox to build and verify a real adapter against — doing so would
// mean guessing at an untested integration, which brief Rule 2 ("never
// invent...") argues against just as much for a third-party API as for
// government tax rules. The interface plus a mock (v1/mock) is the
// correct, honest scope here; a real adapter is a follow-up once genuine
// Meta Business API credentials exist to build and test against.
type WhatsAppProvider interface {
	SendTemplateMessage(ctx context.Context, toE164, templateName string, params []string) error
}

type ShareLink struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	DocumentType   string
	DocumentID     uuid.UUID
	TokenHash      string
	ExpiresAt      time.Time
	RevokedAt      *time.Time
	CreatedAt      time.Time
	CreatedBy      uuid.UUID
	// Read receipt: set by the public viewer, never by the sender.
	FirstViewedAt *time.Time
	LastViewedAt  *time.Time
	ViewCount     int
}

type ShareLinkRepository interface {
	Create(ctx context.Context, l *ShareLink) error
	// GetByTokenHash is, like SessionRepository.GetByTokenHash, called
	// against an unscoped transaction — see
	// migrations/0027_notifications.up.sql.
	GetByTokenHash(ctx context.Context, tokenHash string) (*ShareLink, error)
	Revoke(ctx context.Context, id uuid.UUID, at time.Time) error
	// RecordView counts one open of the link (called unscoped, like GetByTokenHash).
	RecordView(ctx context.Context, id uuid.UUID, at time.Time) error
	ListForDocument(ctx context.Context, organisationID uuid.UUID, documentType string, documentID uuid.UUID) ([]*ShareLink, error)
}
