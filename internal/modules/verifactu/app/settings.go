package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/modules/verifactu/aeat"
	"rechvix/internal/modules/verifactu/domain"
	"rechvix/internal/modules/verifactu/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// SettingsView is what the settings screen shows.
type SettingsView struct {
	LegalEntityID      uuid.UUID  `json:"legal_entity_id"`
	Applicable         bool       `json:"applicable"`
	Enabled            bool       `json:"enabled"`
	Environment        string     `json:"environment"`
	InstallationNumber string     `json:"installation_number"`
	HasCertificate     bool       `json:"has_certificate"`
	CertSubject        string     `json:"cert_subject,omitempty"`
	CertNotAfter       *time.Time `json:"cert_not_after,omitempty"`
	ProducerConfigured bool       `json:"producer_configured"`
	Problems           []string   `json:"problems"`
	Records            int        `json:"records"`
	Pending            int        `json:"pending"`
}

func (s *Service) view(ctx context.Context, principal permissions.Principal, legalEntityID uuid.UUID) error {
	return s.permissions.Require(ctx, principal, "settings.view", permissions.Scope{LegalEntityID: &legalEntityID})
}

func (s *Service) manage(ctx context.Context, principal permissions.Principal, legalEntityID uuid.UUID) error {
	return s.permissions.Require(ctx, principal, "settings.manage", permissions.Scope{LegalEntityID: &legalEntityID})
}

// Settings returns the company's VERI*FACTU state and anything that blocks switching it on.
func (s *Service) Settings(ctx context.Context, principal permissions.Principal, legalEntityID uuid.UUID) (*SettingsView, error) {
	if err := s.view(ctx, principal, legalEntityID); err != nil {
		return nil, err
	}
	var v *SettingsView
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		le, err := s.org.GetLegalEntityForOtherModule(ctx, principal.OrganisationID, legalEntityID)
		if err != nil {
			return err
		}
		v = &SettingsView{LegalEntityID: legalEntityID, Applicable: strings.EqualFold(le.CountryCode, "ES"), Environment: "test",
			ProducerConfigured: s.producer.Configured(), Problems: []string{}}
		if !v.Applicable {
			v.Problems = append(v.Problems, "VERI*FACTU applies to companies whose country is Spain.")
		}
		if len(strings.TrimSpace(le.TaxID)) != 9 {
			v.Problems = append(v.Problems, "Set the company's NIF (9 characters) in company settings.")
		}
		if !v.ProducerConfigured {
			v.Problems = append(v.Problems, "The software producer's identifier is not configured on this server (VERIFACTU_PRODUCER_ID).")
		}
		st, err := s.repo.GetSettings(ctx, legalEntityID)
		if err != nil && !errors.Is(err, pg.ErrNotFound) {
			return err
		}
		if st != nil {
			v.Enabled, v.Environment, v.InstallationNumber = st.Enabled, st.Environment, st.InstallationNumber
			v.HasCertificate = len(st.CertEncrypted) > 0
			if st.CertSubject != nil {
				v.CertSubject = *st.CertSubject
			}
			v.CertNotAfter = st.CertNotAfter
		}
		if !v.HasCertificate {
			v.Problems = append(v.Problems, "Upload the company's electronic certificate (.p12/.pfx) so records can be sent to AEAT.")
		}
		rows, err := s.repo.List(ctx, legalEntityID, 1000, 0)
		if err != nil {
			return err
		}
		v.Records = len(rows)
		for _, r := range rows {
			if r.Status == domain.StatusPending || r.Status == domain.StatusError {
				v.Pending++
			}
		}
		return nil
	})
	return v, err
}

// SaveSettings switches VERI*FACTU on or off and picks the AEAT environment. Switching it on needs a Spanish company
// with a NIF and a configured producer; a certificate is needed to send, not to record.
func (s *Service) SaveSettings(ctx context.Context, principal permissions.Principal, legalEntityID uuid.UUID, enabled bool, environment string) error {
	if err := s.manage(ctx, principal, legalEntityID); err != nil {
		return err
	}
	if environment != "test" && environment != "production" {
		return errors.New("verifactu: environment must be test or production")
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		le, err := s.org.GetLegalEntityForOtherModule(ctx, principal.OrganisationID, legalEntityID)
		if err != nil {
			return err
		}
		st, err := s.repo.GetSettings(ctx, legalEntityID)
		if errors.Is(err, pg.ErrNotFound) {
			st = &pg.Settings{LegalEntityID: legalEntityID, OrganisationID: principal.OrganisationID, InstallationNumber: "RX-" + strings.ToUpper(uuid.NewString()[:8])}
		} else if err != nil {
			return err
		}
		if enabled {
			if !strings.EqualFold(le.CountryCode, "ES") {
				return errors.New("verifactu: only companies in Spain can use VERI*FACTU")
			}
			if len(strings.TrimSpace(le.TaxID)) != 9 {
				return errors.New("verifactu: set the company's NIF (9 characters) before enabling")
			}
			if !s.producer.Configured() {
				return errors.New("verifactu: the software producer's identifier is not configured on this server")
			}
		}
		before := st.Enabled
		st.Enabled, st.Environment = enabled, environment
		if err := s.repo.UpsertSettings(ctx, st); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "verifactu.settings_changed", EntityType: "legal_entity", EntityID: &legalEntityID,
			BeforeState: map[string]any{"enabled": before}, AfterState: map[string]any{"enabled": enabled, "environment": environment}, At: s.now()})
	})
}

// UploadCertificate stores the company's electronic certificate encrypted at rest.
func (s *Service) UploadCertificate(ctx context.Context, principal permissions.Principal, legalEntityID uuid.UUID, p12 []byte, password string) error {
	if err := s.manage(ctx, principal, legalEntityID); err != nil {
		return err
	}
	if len(p12) == 0 || len(p12) > 1<<20 {
		return errors.New("verifactu: certificate file is empty or larger than 1 MB")
	}
	_, subject, notAfter, err := aeat.ParseCertificate(p12, password)
	if err != nil {
		return err
	}
	if time.Now().After(notAfter) {
		return fmt.Errorf("verifactu: this certificate expired on %s", notAfter.Format("2006-01-02"))
	}
	aad := []byte(legalEntityID.String())
	sealed, err := s.aead.Seal(p12, aad)
	if err != nil {
		return err
	}
	sealedPw, err := s.aead.Seal([]byte(password), aad)
	if err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		st, err := s.repo.GetSettings(ctx, legalEntityID)
		if errors.Is(err, pg.ErrNotFound) {
			st = &pg.Settings{LegalEntityID: legalEntityID, OrganisationID: principal.OrganisationID, Environment: "test", InstallationNumber: "RX-" + strings.ToUpper(uuid.NewString()[:8])}
		} else if err != nil {
			return err
		}
		st.CertEncrypted, st.CertPassword, st.CertSubject, st.CertNotAfter = sealed, sealedPw, &subject, &notAfter
		if err := s.repo.UpsertSettings(ctx, st); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "verifactu.certificate_uploaded", EntityType: "legal_entity", EntityID: &legalEntityID,
			AfterState: map[string]any{"subject": subject, "not_after": notAfter.Format(time.RFC3339)}, At: s.now()})
	})
}

// RecordView is one row of the register.
type RecordView struct {
	Seq           int64      `json:"seq"`
	Type          string     `json:"type"`
	InvoiceNumber string     `json:"invoice_number"`
	IssueDate     string     `json:"issue_date"`
	InvoiceType   string     `json:"invoice_type,omitempty"`
	InvoiceTotal  string     `json:"invoice_total,omitempty"`
	Hash          string     `json:"hash"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	CSV           string     `json:"aeat_csv,omitempty"`
	ErrorCode     string     `json:"error_code,omitempty"`
	ErrorMessage  string     `json:"error_message,omitempty"`
	LastAttempt   *time.Time `json:"last_attempt_at,omitempty"`
	QRURL         string     `json:"qr_url,omitempty"`
}

// Records lists a company's records newest first.
func (s *Service) Records(ctx context.Context, principal permissions.Principal, legalEntityID uuid.UUID, limit, offset int) ([]RecordView, error) {
	if err := s.view(ctx, principal, legalEntityID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []RecordView
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.repo.List(ctx, legalEntityID, limit, offset)
		if err != nil {
			return err
		}
		for _, r := range rows {
			v := RecordView{Seq: r.Seq, Type: r.Type, InvoiceNumber: r.InvoiceNumber, IssueDate: r.IssueDate.Format("2006-01-02"),
				Hash: r.Hash, Status: r.Status, Attempts: r.Attempts, LastAttempt: r.LastAttempt}
			if r.InvoiceType != nil {
				v.InvoiceType = *r.InvoiceType
			}
			if r.InvoiceTotal != nil {
				v.InvoiceTotal = r.InvoiceTotal.StringFixed(2)
			}
			if r.AEATCSV != nil {
				v.CSV = *r.AEATCSV
			}
			if r.ErrorCode != nil {
				v.ErrorCode = *r.ErrorCode
			}
			if r.ErrorMessage != nil {
				v.ErrorMessage = *r.ErrorMessage
			}
			if r.QRURL != nil {
				v.QRURL = *r.QRURL
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// SendNow retries submission immediately instead of waiting for the background worker.
func (s *Service) SendNow(ctx context.Context, principal permissions.Principal, legalEntityID uuid.UUID) error {
	if err := s.manage(ctx, principal, legalEntityID); err != nil {
		return err
	}
	var sendErr error
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.repo.RetryNow(ctx, legalEntityID); err != nil {
			return err
		}
		sendErr = s.SubmitPending(ctx, principal.OrganisationID, legalEntityID)
		return nil // keep the recorded outcomes even when the send itself failed
	})
	if err != nil {
		return err
	}
	if errors.Is(sendErr, ErrWaiting) {
		return nil
	}
	return sendErr
}

// ChainReport is the result of re-checking a company's whole chain.
type ChainReport struct {
	Records  int    `json:"records"`
	Valid    bool   `json:"valid"`
	BrokenAt int64  `json:"broken_at,omitempty"`
	Message  string `json:"message,omitempty"`
}

// VerifyChain recomputes every fingerprint from the stored record fields and checks each link.
func (s *Service) VerifyChain(ctx context.Context, principal permissions.Principal, legalEntityID uuid.UUID) (*ChainReport, error) {
	if err := s.view(ctx, principal, legalEntityID); err != nil {
		return nil, err
	}
	rep := &ChainReport{Valid: true}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		recs, err := s.repo.All(ctx, legalEntityID)
		if err != nil {
			return err
		}
		rep.Records = len(recs)
		prev := ""
		for _, r := range recs {
			at, err := time.Parse(domain.TimestampLayout, r.GeneratedAtText)
			if err != nil {
				return fmt.Errorf("record %d has an unreadable timestamp: %w", r.Seq, err)
			}
			id := domain.InvoiceID{IssuerNIF: r.IssuerNIF, Number: r.InvoiceNumber, IssueDate: r.IssueDate}
			var want string
			if r.Type == domain.RecordAlta {
				if r.TaxTotal == nil || r.InvoiceTotal == nil || r.InvoiceType == nil {
					rep.Valid, rep.BrokenAt, rep.Message = false, r.Seq, "invoice record is missing its totals"
					return nil
				}
				want = domain.AltaHash(domain.AltaInput{ID: id, InvoiceType: *r.InvoiceType, TaxTotal: *r.TaxTotal, InvoiceTotal: *r.InvoiceTotal, PreviousHash: r.PrevHash, GeneratedAt: at})
			} else {
				want = domain.CancelHash(domain.CancelInput{ID: id, PreviousHash: r.PrevHash, GeneratedAt: at})
			}
			if want != r.Hash {
				rep.Valid, rep.BrokenAt, rep.Message = false, r.Seq, "stored fingerprint does not match the record's fields"
				return nil
			}
			if r.PrevHash != prev {
				rep.Valid, rep.BrokenAt, rep.Message = false, r.Seq, "record does not continue the previous fingerprint"
				return nil
			}
			prev = r.Hash
		}
		return nil
	})
	return rep, err
}

// ExportXML returns every record of the company as one AEAT request document, for inspection or a tax-agency request.
func (s *Service) ExportXML(ctx context.Context, principal permissions.Principal, legalEntityID uuid.UUID) (string, error) {
	if err := s.view(ctx, principal, legalEntityID); err != nil {
		return "", err
	}
	var out string
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		le, err := s.org.GetLegalEntityForOtherModule(ctx, principal.OrganisationID, legalEntityID)
		if err != nil {
			return err
		}
		recs, err := s.repo.All(ctx, legalEntityID)
		if err != nil {
			return err
		}
		xmls := make([]string, len(recs))
		for i, r := range recs {
			xmls[i] = r.XML
		}
		out = `<?xml version="1.0" encoding="UTF-8"?>` + domain.Envelope(le.LegalName, strings.ToUpper(strings.TrimSpace(le.TaxID)), xmls)
		return nil
	})
	return out, err
}
