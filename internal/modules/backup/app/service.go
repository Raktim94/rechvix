// Package app is the backup module's business logic: export a full,
// encrypted database backup, and restore one back over the live
// database. No domain/pg layers exist here (unlike every other module)
// because this module persists nothing of its own — a backup's
// "storage" is the file itself, downloaded once and kept wherever the
// operator keeps it, not a database row.
//
// Both directions need a database connection that can see every row of
// every table, unfiltered — the app's normal runtime role (billing_app)
// is deliberately RLS-restricted (docs/architecture.md's tenant-
// isolation design) and would produce a silently-incomplete backup (a
// dump containing zero rows from every RLS-protected table, since no
// request-scoped app.current_organisation_id session variable is set
// outside a real HTTP request) or a restore that silently fails to
// write rows RLS hides from it. BackupDSN must point at a role that
// bypasses RLS — the same billing_migrator role migrations already run
// as (a table owner bypasses RLS by default; see migrations/0001's
// "DEPLOYMENT REQUIREMENT" comment) — configured via a separate,
// optional BACKUP_DATABASE_DSN environment variable so an operator who
// hasn't set one up gets a clear "not configured" error instead of a
// silently-wrong backup.
package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"rechvix/internal/platform/audit"
	appcrypto "rechvix/internal/platform/crypto"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
	"rechvix/internal/platform/pgtools"
)

var (
	ErrNotConfigured        = errors.New("backup: BACKUP_DATABASE_DSN is not configured on this deployment")
	ErrConfirmationRequired = errors.New("backup: restore requires the literal confirmation phrase")
	ErrInvalidFile          = errors.New("backup: not a valid backup file")
	ErrChecksumMismatch     = errors.New("backup: decrypted archive does not match its recorded checksum — the file may be corrupted or tampered with")
)

// backupMagic identifies a Rechvix backup file at a glance (e.g. `file`
// or a hex editor) — 4 ASCII bytes, not a random value, on purpose.
const backupMagic = "NDRB"

// backupFormatVersion guards a future incompatible format change — this
// service refuses to even attempt decrypting a file whose version it
// doesn't recognize, rather than guessing.
const backupFormatVersion = 1

// RestoreConfirmationPhrase is the literal string a caller (httpapi,
// which gets it from the frontend's typed-confirmation input) must pass
// to Restore — a second, independent gate on top of whatever the UI
// itself already required, since this function is the last line of
// defense before a real, disruptive pg_restore runs.
const RestoreConfirmationPhrase = "RESTORE"

// Header is the backup file's plaintext-but-authenticated metadata —
// readable without decrypting the archive itself (Inspect), so a restore
// UI can show "created 2026-09-01, 4.2MB" before committing to
// decrypting and restoring it. Authenticated via AEAD's additionalData
// (see Export/Restore below), so it can be read but not silently
// swapped onto a different ciphertext.
type Header struct {
	Version         int       `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	PostgresVersion string    `json:"postgres_version"`
	// SHA256 is the checksum of the PLAINTEXT pg_dump archive (before
	// encryption) — verified again after decrypting on restore, brief
	// §15's "integrity hash" requirement, and a real corruption check
	// independent of AEAD's own tamper-detection (which proves the
	// ciphertext wasn't modified, not that the plaintext inside it is
	// what Export originally produced, in the — currently theoretical —
	// event those ever diverge).
	SHA256 string `json:"sha256"`
	// ArchiveBytes is informational only (shown in the UI), not
	// re-verified — SHA256 is the actual integrity check.
	ArchiveBytes int `json:"archive_bytes"`
}

type Service struct {
	// pool is the app's NORMAL, RLS-scoped connection pool — used only to
	// write this module's own audit_log entries (audit_log has the same
	// per-organisation RLS policy every other tenant table does, so
	// recording one outside a RunScoped block would silently fail). It is
	// never used for the dump/restore itself — that's backupDSN's job,
	// specifically because pool's billing_app role can't see RLS-hidden
	// rows.
	pool        *database.Pool
	backupDSN   string // "" means Enabled() is false
	aead        *appcrypto.AEAD
	permissions *permissions.Checker
	audit       audit.Recorder
	now         func() time.Time
}

func NewService(pool *database.Pool, backupDSN string, aead *appcrypto.AEAD, perms *permissions.Checker, recorder audit.Recorder) *Service {
	return &Service{pool: pool, backupDSN: backupDSN, aead: aead, permissions: perms, audit: recorder, now: time.Now}
}

// recordAudit writes an audit_log entry through the normal RLS-scoped
// pool — audit_log's tenant-isolation policy requires
// app.current_organisation_id to be set, which only RunScoped does.
// Best-effort: a failed audit write doesn't unwind an already-completed
// export or restore, same as every other module's audit calls in this
// codebase treat it as a side effect of the real action, not a
// precondition for it.
func (s *Service) recordAudit(ctx context.Context, entry audit.Entry) {
	_ = s.pool.RunScoped(ctx, entry.OrganisationID, func(ctx context.Context) error {
		return s.audit.Record(ctx, entry)
	})
}

// Enabled reports whether BACKUP_DATABASE_DSN was configured — httpapi
// uses this to return a clear, honest "not available on this
// deployment" response instead of a generic 500 the first time anyone
// tries.
func (s *Service) Enabled() bool { return s.backupDSN != "" }

// Export produces one complete, encrypted backup file. permission:
// backup.manage (migrations/0035) — deliberately distinct from
// settings.manage, see that migration's comment.
func (s *Service) Export(ctx context.Context, principal permissions.Principal) (data []byte, filename string, err error) {
	if err := s.permissions.Require(ctx, principal, "backup.manage", permissions.Scope{}); err != nil {
		return nil, "", err
	}
	if !s.Enabled() {
		return nil, "", ErrNotConfigured
	}

	dump, err := pgtools.Dump(ctx, s.backupDSN)
	if err != nil {
		return nil, "", fmt.Errorf("backup: %w", err)
	}
	// Best-effort — a backup with an empty PostgresVersion field is still
	// a completely valid, restorable backup; failing the whole export
	// over this one informational field would be the wrong trade-off.
	pgVersion, _ := pgtools.Version(ctx)

	sum := sha256.Sum256(dump)
	now := s.now()
	header := Header{
		Version: backupFormatVersion, CreatedAt: now, PostgresVersion: pgVersion,
		SHA256: hex.EncodeToString(sum[:]), ArchiveBytes: len(dump),
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return nil, "", fmt.Errorf("backup: encoding header: %w", err)
	}
	// headerJSON is AEAD "additional data": authenticated (Open fails if
	// it's altered) but not itself encrypted — exactly what Inspect below
	// needs to read metadata without decrypting the archive.
	sealed, err := s.aead.Seal(dump, headerJSON)
	if err != nil {
		return nil, "", fmt.Errorf("backup: encrypting: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString(backupMagic)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(headerJSON)))
	buf.Write(lenBuf[:])
	buf.Write(headerJSON)
	buf.Write(sealed)

	s.recordAudit(ctx, audit.Entry{
		// This backup spans every organisation on this instance, not just
		// principal's own — there is no more specific OrganisationID to
		// record it under than the one the initiating user actually
		// belongs to, same as any other action only an authenticated user
		// can take.
		OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
		Action: "backup.exported", EntityType: "database",
		AfterState: map[string]any{"archive_bytes": len(dump), "postgres_version": pgVersion}, At: now,
	})

	return buf.Bytes(), fmt.Sprintf("rechvix-backup-%s.nodedrbackup", now.Format("2006-01-02-150405")), nil
}

// Inspect parses a backup file's header WITHOUT decrypting the archive
// — a restore UI's "preview" step (show what you're about to restore
// before committing to it) needs this, and it works even with the wrong
// encryption key (only Restore's actual decryption would fail then),
// so a corrupted-vs-wrong-key distinction is visible to the caller.
func (s *Service) Inspect(fileBytes []byte) (*Header, error) {
	_, headerJSON, _, err := parseFile(fileBytes)
	if err != nil {
		return nil, err
	}
	var header Header
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidFile, err.Error())
	}
	return &header, nil
}

// Restore decrypts fileBytes and restores it over the live database.
// Requires confirm to be exactly RestoreConfirmationPhrase — an
// independent gate from whatever the UI's own confirmation step already
// did, since this is the last function standing between a request and a
// real, disruptive pg_restore against the live database. Genuinely
// destructive: existing objects the archive also defines are dropped
// and recreated (pgtools.Restore's --clean --if-exists), and the
// database is unavailable to normal application traffic for however
// long that takes on a real dataset — callers should say so plainly
// before ever reaching this method, not just gate it.
func (s *Service) Restore(ctx context.Context, principal permissions.Principal, fileBytes []byte, confirm string) error {
	if err := s.permissions.Require(ctx, principal, "backup.manage", permissions.Scope{}); err != nil {
		return err
	}
	if !s.Enabled() {
		return ErrNotConfigured
	}
	if confirm != RestoreConfirmationPhrase {
		return ErrConfirmationRequired
	}

	magic, headerJSON, sealed, err := parseFile(fileBytes)
	if err != nil {
		return err
	}
	_ = magic
	var header Header
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidFile, err.Error())
	}
	if header.Version != backupFormatVersion {
		return fmt.Errorf("%w: unsupported backup format version %d (this build supports version %d)", ErrInvalidFile, header.Version, backupFormatVersion)
	}

	dump, err := s.aead.Open(sealed, headerJSON)
	if err != nil {
		return fmt.Errorf("backup: could not decrypt this file — it may have been encrypted with a different AEAD_ENCRYPTION_KEY, or is corrupted: %w", err)
	}
	sum := sha256.Sum256(dump)
	if hex.EncodeToString(sum[:]) != header.SHA256 {
		return ErrChecksumMismatch
	}

	if err := pgtools.Restore(ctx, s.backupDSN, dump); err != nil {
		return fmt.Errorf("backup: %w", err)
	}

	s.recordAudit(ctx, audit.Entry{
		OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
		Action: "backup.restored", EntityType: "database",
		AfterState: map[string]any{"backup_created_at": header.CreatedAt, "archive_bytes": header.ArchiveBytes}, At: s.now(),
	})
	return nil
}

// parseFile splits a raw backup file into (magic, headerJSON, sealed
// archive) without interpreting either — shared by Inspect (which stops
// here) and Restore (which goes on to decrypt).
func parseFile(fileBytes []byte) (magic string, headerJSON, sealed []byte, err error) {
	const prefixLen = len(backupMagic) + 4
	if len(fileBytes) < prefixLen {
		return "", nil, nil, fmt.Errorf("%w: file too short", ErrInvalidFile)
	}
	magic = string(fileBytes[:len(backupMagic)])
	if magic != backupMagic {
		return "", nil, nil, fmt.Errorf("%w: missing magic bytes", ErrInvalidFile)
	}
	headerLen := binary.BigEndian.Uint32(fileBytes[len(backupMagic):prefixLen])
	if prefixLen+int(headerLen) > len(fileBytes) {
		return "", nil, nil, fmt.Errorf("%w: truncated header", ErrInvalidFile)
	}
	headerJSON = fileBytes[prefixLen : prefixLen+int(headerLen)]
	sealed = fileBytes[prefixLen+int(headerLen):]
	return magic, headerJSON, sealed, nil
}
