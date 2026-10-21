package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"rechvix/internal/modules/notifications/domain"
	"rechvix/internal/platform/database"
)

type ShareLinkRepo struct{ pool *database.Pool }

func NewShareLinkRepo(pool *database.Pool) *ShareLinkRepo { return &ShareLinkRepo{pool: pool} }

func (r *ShareLinkRepo) Create(ctx context.Context, l *domain.ShareLink) error {
	const q = `
		INSERT INTO share_links (id, organisation_id, document_type, document_id, token_hash, expires_at, created_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, l.ID, l.OrganisationID, l.DocumentType, l.DocumentID, l.TokenHash,
		l.ExpiresAt, l.CreatedAt, l.CreatedBy)
	if err != nil {
		return fmt.Errorf("notifications: inserting share link: %w", err)
	}
	return nil
}

// GetByTokenHash is deliberately called against an unscoped transaction —
// see migrations/0027_notifications.up.sql.
func (r *ShareLinkRepo) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.ShareLink, error) {
	const q = `
		SELECT id, organisation_id, document_type, document_id, token_hash, expires_at, revoked_at, created_at, created_by, first_viewed_at, last_viewed_at, view_count
		FROM share_links WHERE token_hash = $1`
	row := r.pool.Q(ctx).QueryRow(ctx, q, tokenHash)
	var l domain.ShareLink
	if err := row.Scan(&l.ID, &l.OrganisationID, &l.DocumentType, &l.DocumentID, &l.TokenHash,
		&l.ExpiresAt, &l.RevokedAt, &l.CreatedAt, &l.CreatedBy, &l.FirstViewedAt, &l.LastViewedAt, &l.ViewCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("notifications: querying share link: %w", err)
	}
	return &l, nil
}

func (r *ShareLinkRepo) Revoke(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := r.pool.Q(ctx).Exec(ctx,
		`UPDATE share_links SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`, id, at)
	if err != nil {
		return fmt.Errorf("notifications: revoking share link: %w", err)
	}
	return nil
}

func (r *ShareLinkRepo) ListForDocument(ctx context.Context, organisationID uuid.UUID, documentType string, documentID uuid.UUID) ([]*domain.ShareLink, error) {
	const q = `
		SELECT id, organisation_id, document_type, document_id, token_hash, expires_at, revoked_at, created_at, created_by, first_viewed_at, last_viewed_at, view_count
		FROM share_links
		WHERE organisation_id = $1 AND document_type = $2 AND document_id = $3
		ORDER BY created_at DESC`
	rows, err := r.pool.Q(ctx).Query(ctx, q, organisationID, documentType, documentID)
	if err != nil {
		return nil, fmt.Errorf("notifications: listing share links: %w", err)
	}
	defer rows.Close()
	var out []*domain.ShareLink
	for rows.Next() {
		var l domain.ShareLink
		if err := rows.Scan(&l.ID, &l.OrganisationID, &l.DocumentType, &l.DocumentID, &l.TokenHash,
			&l.ExpiresAt, &l.RevokedAt, &l.CreatedAt, &l.CreatedBy, &l.FirstViewedAt, &l.LastViewedAt, &l.ViewCount); err != nil {
			return nil, fmt.Errorf("notifications: scanning share link row: %w", err)
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}

func (r *ShareLinkRepo) RecordView(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := r.pool.Q(ctx).Exec(ctx, `
		UPDATE share_links SET first_viewed_at = COALESCE(first_viewed_at, $2), last_viewed_at = $2, view_count = view_count + 1
		WHERE id = $1`, id, at)
	if err != nil {
		return fmt.Errorf("notifications: recording share link view: %w", err)
	}
	return nil
}
