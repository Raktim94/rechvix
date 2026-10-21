// Package esign lets a customer sign a document electronically through a link: a simple electronic signature with the
// evidence kept (document fingerprint, time, network address, browser). It is not a qualified electronic signature.
package esign

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/crypto"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

var (
	ErrInvalid  = errors.New("esign: invalid request")
	ErrNotFound = errors.New("esign: not found")
	ErrLink     = errors.New("esign: this signing link is invalid, expired or no longer open")
	ErrChanged  = errors.New("esign: the document changed after the signature was requested; ask for a new request")
)

const (
	linkTTL     = 14 * 24 * time.Hour
	maxSigBytes = 150 << 10
	// ConsentText is stored with every signature so the evidence shows exactly what the signer agreed to.
	ConsentText = "I have read this document and I agree to it. I understand that typing my name, or drawing my signature, here is my electronic signature."
)

// Renderer produces the document a signer reads (PDF or HTML) and a stable fingerprint of its content.
type Renderer interface {
	Render(ctx context.Context, orgID, createdBy uuid.UUID, documentType string, documentID uuid.UUID) (data []byte, contentType, filename string, err error)
	Fingerprint(ctx context.Context, orgID, createdBy uuid.UUID, documentType string, documentID uuid.UUID) (string, error)
}

type Request struct {
	ID             uuid.UUID  `json:"id"`
	DocumentType   string     `json:"document_type"`
	DocumentID     uuid.UUID  `json:"document_id"`
	Status         string     `json:"status"`
	RequestedName  string     `json:"requested_name,omitempty"`
	RequestedEmail string     `json:"requested_email,omitempty"`
	SignerName     string     `json:"signer_name,omitempty"`
	SignedAt       *time.Time `json:"signed_at,omitempty"`
	SignerIP       string     `json:"signer_ip,omitempty"`
	DocumentHash   string     `json:"document_hash"`
	HasDrawing     bool       `json:"has_drawing"`
	DeclineReason  string     `json:"decline_reason,omitempty"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Public is what the signer sees before signing.
type Public struct {
	DocumentType string     `json:"document_type"`
	Status       string     `json:"status"`
	Name         string     `json:"requested_name,omitempty"`
	ExpiresAt    time.Time  `json:"expires_at"`
	SignedAt     *time.Time `json:"signed_at,omitempty"`
	SignerName   string     `json:"signer_name,omitempty"`
	Consent      string     `json:"consent"`
}

// Note is the line printed on a signed document.
type Note struct {
	Name string
	At   time.Time
	Ref  string
}

type Service struct {
	pool        *database.Pool
	renderer    Renderer
	mail        Mailer
	permissions *permissions.Checker
	audit       audit.Recorder
	now         func() time.Time
}

// Mailer queues the signing email; nil means no email is sent.
type Mailer interface {
	EmailConfigured() bool
	SendSigningLink(ctx context.Context, principal permissions.Principal, documentID uuid.UUID, to, subject, bodyHTML string) error
}

func NewService(pool *database.Pool, r Renderer, m Mailer, p *permissions.Checker, a audit.Recorder) *Service {
	return &Service{pool: pool, renderer: r, mail: m, permissions: p, audit: a, now: time.Now}
}

func (s *Service) share(ctx context.Context, p permissions.Principal) error {
	return s.permissions.Require(ctx, p, "notifications.share", permissions.Scope{})
}

// Create opens a signature request and returns the raw token (shown once) for the link.
func (s *Service) Create(ctx context.Context, principal permissions.Principal, documentType string, documentID uuid.UUID, name, email, baseURL string) (*Request, string, error) {
	if err := s.share(ctx, principal); err != nil {
		return nil, "", err
	}
	if documentType != "sales_document" {
		return nil, "", fmt.Errorf("%w: only sales documents can be signed", ErrInvalid)
	}
	email = strings.TrimSpace(email)
	if email != "" && !strings.Contains(email, "@") {
		return nil, "", fmt.Errorf("%w: the email address looks wrong", ErrInvalid)
	}
	hash, err := s.renderer.Fingerprint(ctx, principal.OrganisationID, principal.UserID, documentType, documentID)
	if err != nil {
		return nil, "", fmt.Errorf("%w: that document cannot be signed (%v)", ErrInvalid, err)
	}
	_, raw, err := crypto.RandomToken(32)
	if err != nil {
		return nil, "", err
	}
	id, _ := uuid.NewV7()
	now := s.now()
	req := &Request{ID: id, DocumentType: documentType, DocumentID: documentID, Status: "PENDING", RequestedName: strings.TrimSpace(name), RequestedEmail: email, DocumentHash: hash, ExpiresAt: now.Add(linkTTL), CreatedAt: now}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO document_signatures (id, organisation_id, document_type, document_id, token_hash, requested_name, requested_email, document_hash, expires_at, created_by)
			VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,$9,$10)`, id, principal.OrganisationID, documentType, documentID, crypto.HashToken(raw), req.RequestedName, email, hash, req.ExpiresAt, principal.UserID); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "esign.requested", EntityType: documentType, EntityID: &documentID, AfterState: map[string]any{"request_id": id, "to": email}, At: now})
	})
	if err != nil {
		return nil, "", err
	}
	if email != "" && s.mail != nil && s.mail.EmailConfigured() && baseURL != "" {
		link := strings.TrimRight(baseURL, "/") + "/sign/" + raw
		body := "<p>Please review and sign this document.</p><p><a href=\"" + html.EscapeString(link) + "\">Open the document to sign</a></p><p>The link is valid for 14 days.</p>"
		if err := s.mail.SendSigningLink(ctx, principal, documentID, email, "Please sign your document", body); err != nil {
			return req, raw, nil // the request exists; the link can still be shared by hand
		}
	}
	return req, raw, nil
}

func (s *Service) List(ctx context.Context, principal permissions.Principal, documentType string, documentID uuid.UUID) ([]Request, error) {
	if err := s.share(ctx, principal); err != nil {
		return nil, err
	}
	out := []Request{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT id, document_type, document_id, status, COALESCE(requested_name, ''), COALESCE(requested_email, ''), COALESCE(signer_name, ''), signed_at,
			       COALESCE(signer_ip, ''), document_hash, signature_png IS NOT NULL, COALESCE(decline_reason, ''), expires_at, created_at
			FROM document_signatures WHERE organisation_id = $1 AND document_type = $2 AND document_id = $3 ORDER BY created_at DESC`, principal.OrganisationID, documentType, documentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r Request
			if err := rows.Scan(&r.ID, &r.DocumentType, &r.DocumentID, &r.Status, &r.RequestedName, &r.RequestedEmail, &r.SignerName, &r.SignedAt, &r.SignerIP, &r.DocumentHash, &r.HasDrawing, &r.DeclineReason, &r.ExpiresAt, &r.CreatedAt); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) Revoke(ctx context.Context, principal permissions.Principal, id uuid.UUID) error {
	if err := s.share(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		tag, err := s.pool.Q(ctx).Exec(ctx, `UPDATE document_signatures SET status = 'REVOKED' WHERE id = $1 AND organisation_id = $2 AND status = 'PENDING'`, id, principal.OrganisationID)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		return nil
	})
}

type row struct {
	id, org, docID, createdBy uuid.UUID
	docType, status, hash     string
	name                      *string
	signerName                *string
	signedAt                  *time.Time
	expires                   time.Time
}

func (s *Service) byToken(ctx context.Context, raw string) (*row, error) {
	var r row
	err := s.pool.Q(ctx).QueryRow(ctx, `
		SELECT id, organisation_id, document_id, created_by, document_type, status, document_hash, requested_name, signer_name, signed_at, expires_at
		FROM document_signatures WHERE token_hash = $1`, crypto.HashToken(raw)).
		Scan(&r.id, &r.org, &r.docID, &r.createdBy, &r.docType, &r.status, &r.hash, &r.name, &r.signerName, &r.signedAt, &r.expires)
	if err != nil {
		return nil, ErrLink
	}
	return &r, nil
}

// Lookup returns what the signer sees. A signed or declined request is still viewable (as a receipt); an expired or
// revoked one is not.
func (s *Service) Lookup(ctx context.Context, raw string) (*Public, error) {
	var out *Public
	err := s.pool.Run(ctx, func(ctx context.Context) error {
		r, err := s.byToken(ctx, raw)
		if err != nil {
			return err
		}
		if r.status == "REVOKED" || (r.status == "PENDING" && s.now().After(r.expires)) {
			return ErrLink
		}
		out = &Public{DocumentType: r.docType, Status: r.status, ExpiresAt: r.expires, SignedAt: r.signedAt, Consent: ConsentText}
		if r.name != nil {
			out.Name = *r.name
		}
		if r.signerName != nil {
			out.SignerName = *r.signerName
		}
		return nil
	})
	return out, err
}

// Document renders the document for the signer.
func (s *Service) Document(ctx context.Context, raw string) ([]byte, string, string, error) {
	var r *row
	if err := s.pool.Run(ctx, func(ctx context.Context) error {
		var err error
		r, err = s.byToken(ctx, raw)
		return err
	}); err != nil {
		return nil, "", "", err
	}
	if r.status == "REVOKED" || (r.status == "PENDING" && s.now().After(r.expires)) {
		return nil, "", "", ErrLink
	}
	return s.renderer.Render(ctx, r.org, r.createdBy, r.docType, r.docID)
}

// SignInput is what the signer submits.
type SignInput struct {
	Name       string
	DrawingPNG []byte // optional drawn signature
	Agree      bool
	Decline    bool
	Reason     string
	IP         string
	Agent      string
}

func isPNG(b []byte) bool { return len(b) > 8 && string(b[1:4]) == "PNG" }

// Submit records the signature (or a refusal). It refuses a request that is not open, has expired, or whose document
// changed since the request was made.
func (s *Service) Submit(ctx context.Context, raw string, in SignInput) (*Public, error) {
	name := strings.TrimSpace(in.Name)
	if !in.Decline {
		if len([]rune(name)) < 2 || len([]rune(name)) > 120 {
			return nil, fmt.Errorf("%w: type your full name", ErrInvalid)
		}
		if !in.Agree {
			return nil, fmt.Errorf("%w: tick the box to confirm you agree", ErrInvalid)
		}
		if len(in.DrawingPNG) > maxSigBytes || (len(in.DrawingPNG) > 0 && !isPNG(in.DrawingPNG)) {
			return nil, fmt.Errorf("%w: the drawn signature must be a PNG under 150 KB", ErrInvalid)
		}
	}
	var r *row
	if err := s.pool.Run(ctx, func(ctx context.Context) error {
		var err error
		r, err = s.byToken(ctx, raw)
		return err
	}); err != nil {
		return nil, err
	}
	if r.status != "PENDING" || s.now().After(r.expires) {
		return nil, ErrLink
	}
	err := s.pool.RunScoped(ctx, r.org, func(ctx context.Context) error {
		if in.Decline {
			_, err := s.pool.Q(ctx).Exec(ctx, `UPDATE document_signatures SET status = 'DECLINED', decline_reason = NULLIF($2, ''), signer_ip = $3, signer_agent = $4 WHERE id = $1 AND status = 'PENDING'`,
				r.id, truncate(in.Reason, 1000), in.IP, truncate(in.Agent, 400))
			return err
		}
		current, err := s.renderer.Fingerprint(ctx, r.org, r.createdBy, r.docType, r.docID)
		if err != nil {
			return err
		}
		if current != r.hash {
			return ErrChanged
		}
		var drawing any
		if len(in.DrawingPNG) > 0 {
			drawing = in.DrawingPNG
		}
		tag, err := s.pool.Q(ctx).Exec(ctx, `
			UPDATE document_signatures SET status = 'SIGNED', signer_name = $2, signature_png = $3, signed_at = now(), signer_ip = $4, signer_agent = $5, consent_text = $6
			WHERE id = $1 AND status = 'PENDING'`, r.id, name, drawing, in.IP, truncate(in.Agent, 400), ConsentText)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrLink
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: r.org, ActorType: audit.ActorSystem, Action: "esign.signed", EntityType: r.docType, EntityID: &r.docID,
			AfterState: map[string]any{"request_id": r.id, "signer": name, "document_hash": r.hash}, At: s.now()})
	})
	if err != nil {
		return nil, err
	}
	return s.Lookup(ctx, raw)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// SignedNote returns the line to print on a document that has been signed, or nil.
func (s *Service) SignedNote(ctx context.Context, documentID uuid.UUID) (*Note, error) {
	var n Note
	var id uuid.UUID
	err := s.pool.Q(ctx).QueryRow(ctx, `
		SELECT id, signer_name, signed_at FROM document_signatures WHERE document_id = $1 AND status = 'SIGNED' ORDER BY signed_at DESC LIMIT 1`, documentID).Scan(&id, &n.Name, &n.At)
	if err != nil {
		return nil, nil
	}
	n.Ref = strings.ToUpper(hex.EncodeToString(id[:4]))
	return &n, nil
}

// DataURLToPNG decodes a canvas data URL ("data:image/png;base64,...").
func DataURLToPNG(s string) ([]byte, error) {
	const prefix = "data:image/png;base64,"
	if s == "" {
		return nil, nil
	}
	if !strings.HasPrefix(s, prefix) {
		return nil, fmt.Errorf("%w: the drawn signature must be a PNG", ErrInvalid)
	}
	b, err := base64.StdEncoding.DecodeString(s[len(prefix):])
	if err != nil {
		return nil, fmt.Errorf("%w: the drawn signature is not valid", ErrInvalid)
	}
	return b, nil
}

// Fingerprint hashes any stable JSON-like bytes; exposed so the composition root can build a Renderer.
func Fingerprint(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
