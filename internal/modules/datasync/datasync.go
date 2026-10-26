// Package datasync keeps master data (products, contacts and what hangs off them) identical between a desktop install
// and a central server. Every change to a synced table lands in sync_changes (migration 0062); a device pushes its
// own changes and pulls the others', last write wins per record. See docs/adr/0003-offline-sync.md for what is and is
// not covered.
package datasync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/crypto"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

// Protocol is bumped whenever the wire format changes; a peer on another protocol is refused rather than guessed at.
const Protocol = 1

// Tables lists the synced tables parents-first, which is also the order a first sync has to create them in.
var Tables = []string{
	"units_of_measure", "categories", "brands", "parties", "products", "unit_conversions",
	"product_variants", "product_barcodes", "party_addresses", "party_tax_registrations",
}

var (
	ErrInvalid   = errors.New("datasync: invalid request")
	ErrNotLinked = errors.New("datasync: this install is not linked to a server")
)

// Change is one row-level change in the log.
type Change struct {
	Seq       int64           `json:"seq,omitempty"`
	Table     string          `json:"table"`
	RowID     uuid.UUID       `json:"row_id"`
	Op        string          `json:"op"` // "U" upsert, "D" delete
	Row       json.RawMessage `json:"row,omitempty"`
	ChangedAt time.Time       `json:"changed_at"`
}

// ApplyResult counts what happened to a batch. Conflicts are changes that could not be applied (a different row
// already uses the same unique value, or a parent never arrived); Stale are changes that lost to a newer local copy.
type ApplyResult struct {
	Applied   int      `json:"applied"`
	Stale     int      `json:"stale"`
	Conflicts int      `json:"conflicts"`
	Errors    []string `json:"errors,omitempty"`
}

type Service struct {
	pool        *database.Pool
	permissions *permissions.Checker
	audit       audit.Recorder
	aead        *crypto.AEAD
	logger      *slog.Logger
	now         func() time.Time

	colsMu sync.Mutex
	cols   map[string][]string
}

func NewService(pool *database.Pool, p *permissions.Checker, a audit.Recorder, aead *crypto.AEAD, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, permissions: p, audit: a, aead: aead, logger: logger, now: time.Now, cols: map[string][]string{}}
}

func (s *Service) manage(ctx context.Context, p permissions.Principal) error {
	return s.permissions.Require(ctx, p, "sync.manage", permissions.Scope{})
}

func validTable(t string) bool {
	for _, x := range Tables {
		if x == t {
			return true
		}
	}
	return false
}

// columns returns the table's column names, read once from the catalogue; table names never come from input.
func (s *Service) columns(ctx context.Context, q database.Querier, table string) ([]string, error) {
	s.colsMu.Lock()
	defer s.colsMu.Unlock()
	if c, ok := s.cols[table]; ok {
		return c, nil
	}
	rows, err := q.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("datasync: table %s has no columns", table)
	}
	s.cols[table] = out
	return out, nil
}

type outcome int

const (
	outApplied outcome = iota
	outStale
	outConflict
	outRetry // a parent row is missing; try again after the rest of the batch
)

func ident(s string) string { return pgx.Identifier{s}.Sanitize() }

// applyOne applies a single change inside its own savepoint so one bad row cannot poison the batch.
func (s *Service) applyOne(ctx context.Context, orgID uuid.UUID, c Change) (outcome, error) {
	if !validTable(c.Table) || (c.Op != "U" && c.Op != "D") {
		return outConflict, fmt.Errorf("%w: unknown table or operation %q/%q", ErrInvalid, c.Table, c.Op)
	}
	tx, ok := database.TxFromContext(ctx)
	if !ok {
		return outConflict, errors.New("datasync: apply needs a transaction")
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return outConflict, err
	}
	res, err := s.applyIn(ctx, sp, orgID, c)
	if err != nil {
		_ = sp.Rollback(ctx)
		var pg *pgconn.PgError
		if errors.As(err, &pg) {
			switch pg.Code {
			case "23503": // foreign_key_violation: parent not here yet, or a child still references this row
				return outRetry, err
			case "23505", "23514", "23502": // unique / check / not-null: this row cannot live here as sent
				return outConflict, err
			}
		}
		return outConflict, err
	}
	if err := sp.Commit(ctx); err != nil {
		return outConflict, err
	}
	return res, nil
}

func (s *Service) applyIn(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, c Change) (outcome, error) {
	t := ident(c.Table)
	if c.Op == "D" {
		_, err := tx.Exec(ctx, `DELETE FROM `+t+` WHERE id = $1 AND organisation_id = $2`, c.RowID, orgID)
		return outApplied, err
	}
	var row map[string]any
	if err := json.Unmarshal(c.Row, &row); err != nil || row == nil {
		return outConflict, fmt.Errorf("%w: row for %s/%s is not an object", ErrInvalid, c.Table, c.RowID)
	}
	// The sender's organisation id means nothing here; the row always belongs to the organisation being synced.
	row["organisation_id"] = orgID.String()
	row["id"] = c.RowID.String()
	body, _ := json.Marshal(row)

	cols, err := s.columns(ctx, txQuerier{tx}, c.Table)
	if err != nil {
		return outConflict, err
	}
	var sets []string
	hasUpdated := false
	for _, col := range cols {
		if col == "id" || col == "organisation_id" {
			continue
		}
		if col == "updated_at" {
			hasUpdated = true
		}
		sets = append(sets, ident(col)+" = EXCLUDED."+ident(col))
	}
	// Last write wins by updated_at; tables without one are immutable once written, so a repeat is a no-op.
	where := ` WHERE x.updated_at < EXCLUDED.updated_at`
	if !hasUpdated {
		where = ` WHERE false`
	}
	q := `INSERT INTO ` + t + ` AS x SELECT * FROM jsonb_populate_record(NULL::` + t + `, $1::jsonb)
		ON CONFLICT (id) DO UPDATE SET ` + strings.Join(sets, ", ") + where
	tag, err := tx.Exec(ctx, q, string(body))
	if err != nil {
		return outConflict, err
	}
	if tag.RowsAffected() == 0 {
		return outStale, nil
	}
	return outApplied, nil
}

type txQuerier struct{ tx pgx.Tx }

func (q txQuerier) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := q.tx.Exec(ctx, sql, args...)
	return tag.RowsAffected(), err
}
func (q txQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return q.tx.Query(ctx, sql, args...)
}
func (q txQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return q.tx.QueryRow(ctx, sql, args...)
}

// applyBatch applies changes in order, then gives the ones that were waiting for a parent a second pass.
func (s *Service) applyBatch(ctx context.Context, orgID uuid.UUID, changes []Change) ApplyResult {
	var res ApplyResult
	note := func(c Change, err error) {
		if err != nil && len(res.Errors) < 20 {
			res.Errors = append(res.Errors, fmt.Sprintf("%s %s: %v", c.Table, c.RowID, err))
		}
	}
	var retry []Change
	count := func(o outcome) {
		switch o {
		case outApplied:
			res.Applied++
		case outStale:
			res.Stale++
		}
	}
	for _, c := range changes {
		o, err := s.applyOne(ctx, orgID, c)
		switch o {
		case outRetry:
			retry = append(retry, c)
		case outConflict:
			res.Conflicts++
			note(c, err)
		default:
			count(o)
		}
	}
	for _, c := range retry {
		o, err := s.applyOne(ctx, orgID, c)
		switch o {
		case outRetry, outConflict:
			res.Conflicts++
			note(c, err)
		default:
			count(o)
		}
	}
	return res
}

// ---- central side -------------------------------------------------------------------------------------------------

// Info tells a connecting device which organisation it would be syncing with.
func (s *Service) Info(ctx context.Context, p permissions.Principal) (map[string]any, error) {
	if err := s.manage(ctx, p); err != nil {
		return nil, err
	}
	return map[string]any{"protocol": Protocol, "organisation_id": p.OrganisationID}, nil
}

// Push applies a device's changes. They are logged with the device as origin so the device does not pull them back.
func (s *Service) Push(ctx context.Context, p permissions.Principal, deviceID string, changes []Change) (*ApplyResult, error) {
	if err := s.manage(ctx, p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(deviceID) == "" || len(deviceID) > 64 || len(changes) > 1000 {
		return nil, fmt.Errorf("%w: a device id and at most 1000 changes are required", ErrInvalid)
	}
	var res ApplyResult
	err := s.pool.RunScoped(ctx, p.OrganisationID, func(ctx context.Context) error {
		if _, err := s.pool.Q(ctx).Exec(ctx, `SELECT set_config('rechvix.sync_origin', $1, true)`, deviceID); err != nil {
			return err
		}
		res = s.applyBatch(ctx, p.OrganisationID, changes)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &res, nil
}

type PullResult struct {
	Changes []Change `json:"changes"`
	// Cursor is the highest log position examined; send it back as `after` for the next page.
	Cursor int64 `json:"cursor"`
	More   bool  `json:"more"`
}

// Pull returns the changes after a cursor, leaving out the ones the asking device made itself.
func (s *Service) Pull(ctx context.Context, p permissions.Principal, deviceID string, after int64, limit int) (*PullResult, error) {
	if err := s.manage(ctx, p); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	out := &PullResult{Changes: []Change{}, Cursor: after}
	err := s.pool.RunScoped(ctx, p.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT seq, table_name, row_id, op, row_data, COALESCE(origin, ''), changed_at
			FROM sync_changes WHERE organisation_id = $1 AND seq > $2 ORDER BY seq LIMIT $3`, p.OrganisationID, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			var c Change
			var origin string
			var data []byte
			if err := rows.Scan(&c.Seq, &c.Table, &c.RowID, &c.Op, &data, &origin, &c.ChangedAt); err != nil {
				return err
			}
			if n == limit {
				out.More = true
				break
			}
			n++
			out.Cursor = c.Seq
			if origin == deviceID {
				continue
			}
			c.Row = data
			out.Changes = append(out.Changes, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
