// Package app is the notifications module's application/use-case layer.
// Actual provider sending happens inside the outbox Handler (registered
// against EventTypeSend), never inline with the HTTP request that
// triggered a share — a slow/down email/SMS/WhatsApp provider can never
// block the request that asked for it (brief Rule 12).
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/modules/notifications/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/crypto"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/outbox"
	"rechvix/internal/platform/permissions"
)

const EventTypeSend = "notification.send"

// shareLinkTTL is the default validity window for a generated share link
// (brief §21 "expire") — not user-configurable in this stage; a fixed,
// reasonable default (a customer needs time to open an emailed invoice
// link) beats no expiry at all.
const shareLinkTTL = 30 * 24 * time.Hour

type SendPayload struct {
	Channel      domain.Channel `json:"channel"`
	Recipient    string         `json:"recipient"`
	DocumentType string         `json:"document_type"`
	DocumentID   uuid.UUID      `json:"document_id"`
	Subject      string         `json:"subject,omitempty"`
	BodyHTML     string         `json:"body_html,omitempty"`
	TemplateName string         `json:"template_name,omitempty"`
	TemplateArgs []string       `json:"template_args,omitempty"`
}

type Service struct {
	pool       database.Runner
	shareLinks domain.ShareLinkRepository
	outbox     outbox.Writer
	perms      *permissions.Checker
	audit      audit.Recorder
	email      domain.EmailProvider // nil if not configured for this deployment
	sms        domain.SMSProvider
	whatsapp   domain.WhatsAppProvider
	now        func() time.Time
}

func NewService(
	pool database.Runner,
	shareLinks domain.ShareLinkRepository,
	outboxWriter outbox.Writer,
	perms *permissions.Checker,
	recorder audit.Recorder,
	email domain.EmailProvider,
	sms domain.SMSProvider,
	whatsapp domain.WhatsAppProvider,
) *Service {
	return &Service{
		pool: pool, shareLinks: shareLinks, outbox: outboxWriter, perms: perms, audit: recorder,
		email: email, sms: sms, whatsapp: whatsapp, now: time.Now,
	}
}

// --- Share links ---

func (s *Service) CreateShareLink(ctx context.Context, principal permissions.Principal, documentType string, documentID uuid.UUID) (string, error) {
	if err := s.perms.Require(ctx, principal, "notifications.share", permissions.Scope{}); err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("notifications: generating share link id: %w", err)
	}
	_, raw, err := crypto.RandomToken(32)
	if err != nil {
		return "", fmt.Errorf("notifications: generating share token: %w", err)
	}
	now := s.now()
	link := &domain.ShareLink{
		ID: id, OrganisationID: principal.OrganisationID, DocumentType: documentType, DocumentID: documentID,
		TokenHash: crypto.HashToken(raw), ExpiresAt: now.Add(shareLinkTTL), CreatedAt: now, CreatedBy: principal.UserID,
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.shareLinks.Create(ctx, link); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "sharelink.create", EntityType: documentType, EntityID: &documentID,
			AfterState: map[string]any{"share_link_id": id, "expires_at": link.ExpiresAt}, At: now,
		})
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

// RedeemShareLink resolves a raw bearer token to the full ShareLink row
// it grants access to, or ErrLinkInvalid for anything wrong — same "no
// detail about why" shape as ValidateSession/ValidateAPIKey. Called
// unscoped (see migrations/0027).
//
// Returns the whole row (not just document_type/document_id) because
// httpapi's document-rendering path (WithDocumentRenderer) needs
// OrganisationID and CreatedBy too — CreatedBy is how an anonymous
// recipient's read gets authorized at all (see that doc comment): the
// share link's own creator already passed notifications.share's
// permission check at CreateShareLink time, so re-using their identity
// to resolve the ONE document this link was made for isn't a new
// authorization bypass, it's exactly what creating the link authorized.
func (s *Service) RedeemShareLink(ctx context.Context, rawToken string) (*domain.ShareLink, error) {
	link, err := s.shareLinks.GetByTokenHash(ctx, crypto.HashToken(rawToken))
	if err != nil {
		return nil, domain.ErrLinkInvalid
	}
	if link.RevokedAt != nil || s.now().After(link.ExpiresAt) {
		return nil, domain.ErrLinkInvalid
	}
	return link, nil
}

// ListShareLinksForDocument is the "which links exist for this
// document, and are any of them still live" read path — the repository
// method (ListForDocument) existed from Stage 9, but nothing above it
// ever called it: SalesDetailPage's own "Share via WhatsApp" comment
// claims each link is "independently revocable from this same click",
// but no screen could actually list one to revoke it. TokenHash is
// intentionally never returned — only a hash is stored, the raw token
// shown once at creation can't be reconstructed from it, which is
// exactly why this is a list of "revoke this" rows, not "here's the
// link again" rows.
func (s *Service) ListShareLinksForDocument(ctx context.Context, principal permissions.Principal, documentType string, documentID uuid.UUID) ([]*domain.ShareLink, error) {
	if err := s.perms.Require(ctx, principal, "notifications.share", permissions.Scope{}); err != nil {
		return nil, err
	}
	var out []*domain.ShareLink
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.shareLinks.ListForDocument(ctx, principal.OrganisationID, documentType, documentID)
		return err
	})
	return out, err
}

func (s *Service) RevokeShareLink(ctx context.Context, principal permissions.Principal, id uuid.UUID) error {
	if err := s.perms.Require(ctx, principal, "notifications.share", permissions.Scope{}); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		return s.shareLinks.Revoke(ctx, id, s.now())
	})
}

// --- Sending ---

// QueueSend requires notifications.share and enqueues delivery through
// the outbox — never sends inline. Returns immediately once the event is
// durably queued.
func (s *Service) QueueSend(ctx context.Context, principal permissions.Principal, p SendPayload) error {
	if err := s.perms.Require(ctx, principal, "notifications.share", permissions.Scope{}); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("notifications: generating send id: %w", err)
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.outbox.Enqueue(ctx, principal.OrganisationID, EventTypeSend, "notification:send:"+id.String(), p); err != nil {
			return fmt.Errorf("queuing notification: %w", err)
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "notification.share", EntityType: p.DocumentType, EntityID: &p.DocumentID,
			// Recipient is opaque JSON here, protected by the same
			// audit.view permission as every other sensitive action (brief
			// §20's "access must be permission controlled") — this is the
			// "who shared what document to whom via which provider" record
			// the brief asks for; status is recorded separately by the
			// Handler once delivery actually completes/fails (below).
			AfterState: map[string]any{"channel": p.Channel, "recipient": p.Recipient}, At: s.now(),
		})
	})
}

// Handler adapts the actual provider send to outbox.Handler's signature.
// A provider that isn't configured for this deployment is a permanent
// failure for that channel (retrying won't make a missing provider
// appear), not a transient one.
func (s *Service) Handler() outbox.Handler {
	return func(ctx context.Context, event outbox.Event) error {
		var p SendPayload
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return outbox.Permanent(fmt.Errorf("notifications: malformed send payload: %w", err))
		}

		var sendErr error
		switch p.Channel {
		case domain.ChannelEmail:
			if s.email == nil {
				return outbox.Permanent(fmt.Errorf("notifications: no email provider configured"))
			}
			sendErr = s.email.SendEmail(ctx, p.Recipient, p.Subject, p.BodyHTML, "", nil)
		case domain.ChannelSMS:
			if s.sms == nil {
				return outbox.Permanent(fmt.Errorf("notifications: no SMS provider configured"))
			}
			sendErr = s.sms.SendSMS(ctx, p.Recipient, p.BodyHTML)
		case domain.ChannelWhatsApp:
			if s.whatsapp == nil {
				return outbox.Permanent(fmt.Errorf("notifications: no WhatsApp provider configured"))
			}
			sendErr = s.whatsapp.SendTemplateMessage(ctx, p.Recipient, p.TemplateName, p.TemplateArgs)
		default:
			return outbox.Permanent(fmt.Errorf("notifications: unknown channel %q", p.Channel))
		}

		status := "sent"
		if sendErr != nil {
			status = "failed"
		}
		_ = s.audit.Record(ctx, audit.Entry{
			OrganisationID: event.OrganisationID, ActorType: audit.ActorSystem,
			Action: "notification.delivery_status", EntityType: p.DocumentType, EntityID: &p.DocumentID,
			AfterState: map[string]any{"channel": p.Channel, "status": status}, At: s.now(),
		})
		return sendErr
	}
}
