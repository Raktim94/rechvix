package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/modules/verifactu/aeat"
	"rechvix/internal/modules/verifactu/domain"
	"rechvix/internal/modules/verifactu/pg"
	"rechvix/internal/platform/outbox"
)

func defaultSender(ctx context.Context, env domain.Environment, cert tls.Certificate, body string) (*aeat.Response, error) {
	return aeat.NewClient(env, cert).Send(ctx, body)
}

// ErrWaiting means AEAT asked us to wait before the next submission; the outbox retries later.
var ErrWaiting = errors.New("verifactu: waiting for the tax agency's submission interval")

// Handler is the outbox handler for EventTypeSubmit.
func (s *Service) Handler() outbox.Handler {
	return func(ctx context.Context, event outbox.Event) error {
		var p struct {
			LegalEntityID uuid.UUID `json:"legal_entity_id"`
		}
		if err := jsonUnmarshal(event.Payload, &p); err != nil || p.LegalEntityID == uuid.Nil {
			return outbox.Permanent(fmt.Errorf("verifactu: bad submit event payload"))
		}
		return s.SubmitPending(ctx, event.OrganisationID, p.LegalEntityID)
	}
}

// SubmitPending sends the next batch of unsent records of a company, in chain order. Call it inside a transaction
// scoped to the organisation (the outbox poller and the HTTP layer both do).
func (s *Service) SubmitPending(ctx context.Context, orgID, legalEntityID uuid.UUID) error {
	st, err := s.repo.GetSettings(ctx, legalEntityID)
	if errors.Is(err, pg.ErrNotFound) || (err == nil && !st.Enabled) {
		return nil
	}
	if err != nil {
		return err
	}
	due, err := s.repo.EarliestDue(ctx, legalEntityID)
	if err != nil {
		return err
	}
	if !due {
		pending, err := s.repo.Due(ctx, legalEntityID, 1)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		return ErrWaiting
	}
	var cert tls.Certificate
	if s.transport == nil {
		if cert, err = s.certificate(st); err != nil {
			return err
		}
	}
	records, err := s.repo.Due(ctx, legalEntityID, batchSize)
	if err != nil || len(records) == 0 {
		return err
	}
	le, err := s.org.GetLegalEntityForOtherModule(ctx, orgID, legalEntityID)
	if err != nil {
		return err
	}
	xmls := make([]string, len(records))
	for i, r := range records {
		xmls[i] = r.XML
	}
	body := domain.Envelope(le.LegalName, strings.ToUpper(strings.TrimSpace(le.TaxID)), xmls)

	var resp *aeat.Response
	if s.transport != nil {
		resp, err = s.transport(ctx, domain.Environment(st.Environment), body)
	} else {
		resp, err = s.sender(ctx, domain.Environment(st.Environment), cert, body)
	}
	if err != nil {
		wait := 5 * time.Minute
		for _, r := range records {
			_ = s.repo.SetOutcome(ctx, pg.Outcome{RecordID: r.ID, Status: domain.StatusError, ErrorText: truncate(err.Error(), 1000), NextAfter: wait})
		}
		return fmt.Errorf("verifactu: submission failed: %w", err)
	}
	wait := time.Duration(resp.WaitSeconds) * time.Second
	if wait < 60*time.Second {
		wait = 60 * time.Second
	}
	byKey := map[string]aeat.Line{}
	for _, l := range resp.Lines {
		byKey[l.Number+"|"+l.IssueDate+"|"+l.Operation] = l
	}
	for _, r := range records {
		op := "Alta"
		if r.Type == domain.RecordAnulacion {
			op = "Anulacion"
		}
		l, ok := byKey[r.InvoiceNumber+"|"+r.IssueDate.Format(domain.DateLayout)+"|"+op]
		out := pg.Outcome{RecordID: r.ID, CSV: resp.CSV, NextAfter: wait, Response: resp.Raw}
		switch {
		case !ok && resp.State == "Correcto":
			out.Status = domain.StatusAccepted
		case !ok:
			out.Status, out.ErrorText = domain.StatusError, "AEAT returned no result for this record"
		case l.State == "Correcto":
			out.Status = domain.StatusAccepted
		case l.State == "AceptadoConErrores":
			out.Status, out.ErrorCode, out.ErrorText = domain.StatusAcceptedWithErrors, l.ErrorCode, l.ErrorText
		default:
			out.Status, out.ErrorCode, out.ErrorText = domain.StatusRejected, l.ErrorCode, l.ErrorText
		}
		if err := s.repo.SetOutcome(ctx, out); err != nil {
			return err
		}
	}
	more, err := s.repo.Due(ctx, legalEntityID, 1)
	if err == nil && len(more) > 0 {
		return ErrWaiting
	}
	return err
}

func (s *Service) certificate(st *pg.Settings) (tls.Certificate, error) {
	if len(st.CertEncrypted) == 0 || len(st.CertPassword) == 0 {
		return tls.Certificate{}, domain.ErrNoCertificate
	}
	aad := []byte(st.LegalEntityID.String())
	p12, err := s.aead.Open(st.CertEncrypted, aad)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("verifactu: certificate cannot be decrypted (was AEAD_ENCRYPTION_KEY changed?): %w", err)
	}
	pw, err := s.aead.Open(st.CertPassword, aad)
	if err != nil {
		return tls.Certificate{}, err
	}
	cert, _, _, err := aeat.ParseCertificate(p12, string(pw))
	return cert, err
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
