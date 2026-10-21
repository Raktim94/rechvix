package app

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"rechvix/internal/modules/reporting/domain"
	"rechvix/internal/modules/reporting/taxpack"
	"rechvix/internal/platform/permissions"
	"time"
)

var ErrInvalidAccountantRequest = fmt.Errorf("accountant export: invalid request")

func (s *Service) AccountantPackage(ctx context.Context, principal permissions.Principal, company uuid.UUID, from, to time.Time) (*taxpack.Package, error) {
	if company == uuid.Nil || from.IsZero() || to.IsZero() || to.Before(from) || to.Sub(from) > 366*24*time.Hour || from.Year() != 2026 || to.Year() != 2026 {
		return nil, fmt.Errorf("%w: select one company and an inclusive date range within 2026 (the verified mapping year)", ErrInvalidAccountantRequest)
	}
	scope := permissions.Scope{LegalEntityID: &company}
	// Both permissions must cover this exact entity. HasAny alone would allow a
	// user with export rights in another company to download this registration.
	for _, permission := range []string{"reports.view", "reports.export"} {
		if err := s.perms.Require(ctx, principal, permission, scope); err != nil {
			return nil, err
		}
	}
	var data *domain.AccountantData
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		data, err = s.repo.AccountantData(ctx, domain.Filter{OrganisationID: principal.OrganisationID, LegalEntityID: &company, From: &from, To: &to})
		return err
	})
	if err != nil {
		return nil, err
	}
	pack, err := taxpack.Build(*data, from, to)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidAccountantRequest, err)
	}
	return pack, nil
}
