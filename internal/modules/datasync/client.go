package datasync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

// Status is what the settings screen shows about the link.
type Status struct {
	Linked       bool       `json:"linked"`
	RemoteURL    string     `json:"remote_url,omitempty"`
	Enabled      bool       `json:"enabled"`
	LastSyncAt   *time.Time `json:"last_sync_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	LastPushed   int        `json:"last_pushed"`
	LastPulled   int        `json:"last_pulled"`
	LastConflict int        `json:"last_conflicts"`
	// Waiting is how many local changes have not reached the server yet.
	Waiting int `json:"waiting"`
}

type link struct {
	remoteURL  string
	remoteOrg  uuid.UUID
	apiKey     string
	deviceID   string
	enabled    bool
	lastPushed int64
	lastPulled int64
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// cleanURL accepts https anywhere and plain http only for a machine on this computer or a private network.
func cleanURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("%w: enter the server address, for example https://rechvix.example.com", ErrInvalid)
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		ip := net.ParseIP(host)
		local := host == "localhost" || (ip != nil && (ip.IsLoopback() || ip.IsPrivate()))
		if !local {
			return "", fmt.Errorf("%w: use https for a server on the internet, so the key and your data are encrypted", ErrInvalid)
		}
	}
	u.Path, u.RawQuery, u.Fragment = strings.TrimRight(u.Path, "/"), "", ""
	return u.String(), nil
}

func (l *link) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, l.remoteURL+"/api/v1"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+l.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the server: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("the server rejected the API key")
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("the API key is not allowed to sync (it needs the \"sync:readwrite\" scope)")
	case resp.StatusCode >= 300:
		return fmt.Errorf("the server answered %d: %s", resp.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 200)])))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (s *Service) loadLink(ctx context.Context, orgID uuid.UUID) (*link, error) {
	l := &link{}
	var enc []byte
	err := s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		return s.pool.Q(ctx).QueryRow(ctx, `SELECT remote_url, remote_org_id, api_key_encrypted, device_id, enabled, last_pushed_seq, last_pulled_seq
			FROM sync_links WHERE organisation_id = $1`, orgID).Scan(&l.remoteURL, &l.remoteOrg, &enc, &l.deviceID, &l.enabled, &l.lastPushed, &l.lastPulled)
	})
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, ErrNotLinked
		}
		return nil, err
	}
	key, err := s.aead.Open(enc, []byte("sync-link:"+orgID.String()))
	if err != nil {
		return nil, fmt.Errorf("datasync: reading the stored API key: %w", err)
	}
	l.apiKey = string(key)
	return l, nil
}

// Link connects this install to a central server after checking the key works there.
func (s *Service) Link(ctx context.Context, p permissions.Principal, rawURL, apiKey string) (*Status, error) {
	if err := s.manage(ctx, p); err != nil {
		return nil, err
	}
	base, err := cleanURL(rawURL)
	if err != nil {
		return nil, err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%w: paste an API key from the server (Settings, API keys, scope sync:readwrite)", ErrInvalid)
	}
	l := &link{remoteURL: base, apiKey: apiKey}
	var info struct {
		Protocol int       `json:"protocol"`
		Org      uuid.UUID `json:"organisation_id"`
	}
	if err := l.call(ctx, http.MethodGet, "/sync/info", nil, &info); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if info.Protocol != Protocol {
		return nil, fmt.Errorf("%w: the server speaks sync protocol %d, this app speaks %d; update the older one", ErrInvalid, info.Protocol, Protocol)
	}
	sealed, err := s.aead.Seal([]byte(apiKey), []byte("sync-link:"+p.OrganisationID.String()))
	if err != nil {
		return nil, err
	}
	err = s.pool.RunScoped(ctx, p.OrganisationID, func(ctx context.Context) error {
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO sync_links (organisation_id, remote_url, remote_org_id, api_key_encrypted, device_id)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (organisation_id) DO UPDATE SET remote_url = EXCLUDED.remote_url, remote_org_id = EXCLUDED.remote_org_id,
				api_key_encrypted = EXCLUDED.api_key_encrypted, enabled = true, last_error = NULL, updated_at = now()`,
			p.OrganisationID, base, info.Org, sealed, uuid.NewString()); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: p.OrganisationID, ActorUserID: &p.UserID, ActorType: audit.ActorUser,
			Action: "sync.linked", EntityType: "organisation", EntityID: &p.OrganisationID, AfterState: map[string]any{"remote_url": base}, At: s.now()})
	})
	if err != nil {
		return nil, err
	}
	return s.Status(ctx, p)
}

// Unlink stops syncing. Nothing already copied is removed on either side.
func (s *Service) Unlink(ctx context.Context, p permissions.Principal) error {
	if err := s.manage(ctx, p); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, p.OrganisationID, func(ctx context.Context) error {
		if _, err := s.pool.Q(ctx).Exec(ctx, `DELETE FROM sync_links WHERE organisation_id = $1`, p.OrganisationID); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: p.OrganisationID, ActorUserID: &p.UserID, ActorType: audit.ActorUser,
			Action: "sync.unlinked", EntityType: "organisation", EntityID: &p.OrganisationID, At: s.now()})
	})
}

func (s *Service) Status(ctx context.Context, p permissions.Principal) (*Status, error) {
	if err := s.manage(ctx, p); err != nil {
		return nil, err
	}
	st := &Status{}
	err := s.pool.RunScoped(ctx, p.OrganisationID, func(ctx context.Context) error {
		var lastErr *string
		var pushedSeq int64
		err := s.pool.Q(ctx).QueryRow(ctx, `SELECT remote_url, enabled, last_sync_at, last_error, last_pushed, last_pulled, last_conflicts, last_pushed_seq
			FROM sync_links WHERE organisation_id = $1`, p.OrganisationID).Scan(&st.RemoteURL, &st.Enabled, &st.LastSyncAt, &lastErr, &st.LastPushed, &st.LastPulled, &st.LastConflict, &pushedSeq)
		if err != nil {
			if strings.Contains(err.Error(), "no rows") {
				return nil
			}
			return err
		}
		st.Linked = true
		if lastErr != nil {
			st.LastError = *lastErr
		}
		return s.pool.Q(ctx).QueryRow(ctx, `SELECT count(*) FROM sync_changes WHERE organisation_id = $1 AND seq > $2 AND origin IS NULL`, p.OrganisationID, pushedSeq).Scan(&st.Waiting)
	})
	return st, err
}

// SyncNow runs one push-then-pull round for the caller's organisation.
func (s *Service) SyncNow(ctx context.Context, p permissions.Principal) (*Status, error) {
	if err := s.manage(ctx, p); err != nil {
		return nil, err
	}
	if err := s.syncOrg(ctx, p.OrganisationID); err != nil {
		return nil, err
	}
	return s.Status(ctx, p)
}

// syncOrg is also what the background loop calls; it records its outcome on the link and returns the error, if any.
func (s *Service) syncOrg(ctx context.Context, orgID uuid.UUID) error {
	l, err := s.loadLink(ctx, orgID)
	if err != nil {
		return err
	}
	pushed, pulled, conflicts, runErr := s.round(ctx, orgID, l)
	msg := (*string)(nil)
	if runErr != nil {
		m := runErr.Error()
		msg = &m
	}
	_ = s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		_, err := s.pool.Q(ctx).Exec(ctx, `UPDATE sync_links SET last_sync_at = now(), last_error = $2, last_pushed = $3, last_pulled = $4, last_conflicts = $5, updated_at = now()
			WHERE organisation_id = $1`, orgID, msg, pushed, pulled, conflicts)
		return err
	})
	return runErr
}

func (s *Service) round(ctx context.Context, orgID uuid.UUID, l *link) (pushed, pulled, conflicts int, err error) {
	// Push everything this install changed since the last push.
	for {
		var batch []Change
		var top int64
		err = s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
			rows, err := s.pool.Q(ctx).Query(ctx, `SELECT seq, table_name, row_id, op, row_data, changed_at FROM sync_changes
				WHERE organisation_id = $1 AND seq > $2 AND origin IS NULL ORDER BY seq LIMIT 500`, orgID, l.lastPushed)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var c Change
				var data []byte
				if err := rows.Scan(&c.Seq, &c.Table, &c.RowID, &c.Op, &data, &c.ChangedAt); err != nil {
					return err
				}
				c.Row = data
				top = c.Seq
				batch = append(batch, c)
			}
			return rows.Err()
		})
		if err != nil {
			return
		}
		if len(batch) == 0 {
			break
		}
		var res ApplyResult
		if err = l.call(ctx, http.MethodPost, "/sync/push", map[string]any{"device_id": l.deviceID, "changes": batch}, &res); err != nil {
			return
		}
		s.logger.Debug("sync push", "applied", res.Applied, "stale", res.Stale, "conflicts", res.Conflicts, "errors", res.Errors, "batch", len(batch))
		pushed += res.Applied
		conflicts += res.Conflicts
		l.lastPushed = top
		if err = s.saveCursor(ctx, orgID, "last_pushed_seq", top); err != nil {
			return
		}
	}
	// Pull what others changed.
	for {
		var res PullResult
		path := fmt.Sprintf("/sync/pull?after=%d&device_id=%s&limit=500", l.lastPulled, url.QueryEscape(l.deviceID))
		if err = l.call(ctx, http.MethodGet, path, nil, &res); err != nil {
			return
		}
		if len(res.Changes) > 0 {
			var applied ApplyResult
			err = s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
				// What we apply here came from the server, so it must not be logged as a local change.
				if _, err := s.pool.Q(ctx).Exec(ctx, `SELECT set_config('rechvix.sync_skip', 'on', true)`); err != nil {
					return err
				}
				applied = s.applyBatch(ctx, orgID, res.Changes)
				return nil
			})
			if err != nil {
				return
			}
			pulled += applied.Applied
			conflicts += applied.Conflicts
		}
		l.lastPulled = res.Cursor
		if err = s.saveCursor(ctx, orgID, "last_pulled_seq", res.Cursor); err != nil {
			return
		}
		if !res.More {
			break
		}
	}
	return
}

func (s *Service) saveCursor(ctx context.Context, orgID uuid.UUID, column string, v int64) error {
	if column != "last_pushed_seq" && column != "last_pulled_seq" {
		return fmt.Errorf("datasync: unknown cursor %q", column)
	}
	return s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		_, err := s.pool.Q(ctx).Exec(ctx, `UPDATE sync_links SET `+column+` = $2 WHERE organisation_id = $1`, orgID, v)
		return err
	})
}

// Run syncs every linked organisation on an interval until ctx ends.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			orgs, err := s.linkedOrgs(ctx)
			if err != nil {
				s.logger.Warn("sync: listing linked organisations", "error", err)
				continue
			}
			for _, id := range orgs {
				if err := s.syncOrg(ctx, id); err != nil && err != ErrNotLinked {
					s.logger.Warn("sync round failed", "organisation_id", id, "error", err)
				}
			}
		}
	}
}

func (s *Service) linkedOrgs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT organisation_id FROM sync_linked_orgs()`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
