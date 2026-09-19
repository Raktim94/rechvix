# Backup & Restore

Covers what actually exists today: the `Settings → Backup` UI, the
`POST /api/v1/backup/*` API it calls, and `internal/platform/pgtools`
underneath both. This doc also gives an operator a runnable drill for
proving a backup is genuinely restorable, an unattended-export script,
and how DB migrations apply on upgrade. See `docs/operations/deployment.md`
("Backups" section) for the one-paragraph version and the required
env vars (`BACKUP_DATABASE_DSN`, `AEAD_ENCRYPTION_KEY`).

There is **no built-in scheduler and no built-in automated
restore-verification job**. Both are the operator's responsibility —
this doc is the concrete "how," not a promise that either runs by
itself.

## How it works today

### The pieces

- `internal/platform/pgtools/pgtools.go` shells out to the real
  `pg_dump`/`pg_restore` binaries (copied into the runtime image from the
  official `postgres:18` image — `deploy/docker/server.Dockerfile`).
  `Dump` runs `pg_dump -Fc` (compressed custom format) and returns raw
  archive bytes. `Restore` runs `pg_restore --clean --if-exists
  --no-owner` with the archive piped in on stdin. `connEnv` turns the DSN
  into discrete `-h/-p/-U/-d` flags plus a `PGPASSWORD` env var
  specifically so the password never appears in `ps` output. `Version`
  runs `pg_dump --version` as a startup sanity check only.
- `internal/modules/backup/app/service.go` is the module that owns the
  file format and the permission/confirmation gates. It does not persist
  anything of its own — a backup's only "storage" is the file itself.
  - `Export` requires the `backup.manage` permission (migration `0035`,
    deliberately distinct from `settings.manage`), calls `pgtools.Dump`
    against `BACKUP_DATABASE_DSN`, SHA-256-hashes the plaintext dump,
    builds a `Header{Version, CreatedAt, PostgresVersion, SHA256,
    ArchiveBytes}`, and seals the dump with AES-GCM-style AEAD
    (`AEAD_ENCRYPTION_KEY`), using the header JSON as authenticated
    *additional data* — readable without decrypting, but tamper-evident.
    The file on disk is `"NDRB" + uint32(header length) + header JSON +
    sealed archive`, and `Export` names it
    `rechvix-backup-<YYYY-MM-DD-HHMMSS>.nodedrbackup`.
  - `Inspect` parses just the header (no decryption, no permission check
    beyond being authenticated) — this is what powers the restore UI's
    preview step and distinguishes "wrong key" from "corrupted file."
  - `Restore` also requires `backup.manage`, rejects anything but the
    literal confirmation string `"RESTORE"`
    (`RestoreConfirmationPhrase`), decrypts, **re-verifies the SHA-256
    against the header** (`ErrChecksumMismatch` if it doesn't match —
    independent of AEAD's own tamper check, which only proves the
    ciphertext wasn't altered, not that the plaintext still matches what
    `Export` originally produced), then calls `pgtools.Restore`. There is
    no rollback: `--clean --if-exists` drops and recreates every object
    the archive defines, live, against the running database.
  - Both directions run against `BACKUP_DATABASE_DSN`, not the app's
    normal `billing_app` runtime connection, because `billing_app` is
    deliberately RLS-restricted and would produce a backup with zero rows
    from every tenant-scoped table (or a restore that silently fails to
    write rows RLS hides from it). `BACKUP_DATABASE_DSN` must point at the
    schema-owner role (`billing_migrator` in the standard Compose install
    — a table owner bypasses its own RLS policies by default). On the
    standard `docker compose` install this is wired automatically from
    the same `POSTGRES_PASSWORD` the `migrate` service already uses; a
    deployment that never set it just has `Enabled() == false` and the UI
    says so plainly instead of failing confusingly mid-request.

### The API

Mounted by `internal/modules/backup/httpapi/handlers.go`, under the
`RequireAuthOrAPIKey` route group in `apps/server/main.go`:

| Route | Auth requirement | Notes |
|---|---|---|
| `GET /api/v1/backup/status` | any authenticated request | `{"enabled": bool}` — reflects whether `BACKUP_DATABASE_DSN` is set |
| `POST /api/v1/backup/export` | `backup.manage` | streams the `.nodedrbackup` file back with `Content-Disposition: attachment` |
| `POST /api/v1/backup/inspect` | any authenticated request | raw file body in, `Header` JSON out |
| `POST /api/v1/backup/restore?confirm=RESTORE` | `backup.manage` | raw file body in, `{"status":"restored"}` out |

Request bodies for `inspect`/`restore` are the raw file bytes, not
multipart — same pattern as the catalogue CSV/XLSX importer, capped at
2 GiB (`maxBackupFileBytes`).

**Auth note that matters for scripting this** (see the cron example
below): the route group accepts either a session cookie or an
`Authorization: Bearer <api-key>` header
(`internal/modules/identity/httpapi/middleware.go`,
`RequireAuthOrAPIKey`). But API keys (`migrations/0025_api_keys.up.sql`)
draw from a closed scope vocabulary — `products:read`, `inventory:read`,
`customers:read`, `customers:write`, `invoices:read`, `invoices:write`,
`reports:read` (`internal/modules/identity/domain/domain.go`'s
`ValidScopes`) — and `internal/platform/permissions/apikeyscope.go`'s
`APIScopePermissions` map, which is what an API key's scopes actually get
translated into for the `permissions.Checker.Require` check, **has no
entry that grants `backup.manage`**. `Require` intersects an API-key
request's scope-derived permission set with the underlying user's real
RBAC grants, and rejects the request if the permission isn't in *both* —
so no combination of currently-valid API-key scopes can ever satisfy
`backup.manage`, no matter what role the key's owning user holds. In
practice today: an API key can call `/backup/status` and `/backup/inspect`
(no `backup.manage` check), but **cannot** call `/backup/export` or
`/backup/restore` — those return 403 `FORBIDDEN` for any API key. A
session cookie is the only auth path that currently works for
export/restore. (If you want API-key-driven backups, the actual fix is
adding a `backup:manage` entry to both `ValidScopes` and
`APIScopePermissions` — that's a code change, not a config toggle.)

### UI flow

`apps/web/src/pages/backup/BackupPage.tsx`:
- Checks `GET /backup/status` first; if disabled, shows the
  "set `BACKUP_DATABASE_DSN`" message instead of the panels.
- **Export panel**: one button, `POST`s to `/backup/export` via `fetch`
  (not a plain anchor — `downloadPost()` reads the response as a blob and
  clicks a throwaway `<a download>` itself, since `fetch` has no built-in
  equivalent of a browser auto-saving a `Content-Disposition` response).
- **Restore panel**: choosing a file immediately fires
  `POST /backup/inspect` and renders the returned header (created-at,
  size, Postgres version). The "Restore now" button stays disabled until
  the operator types the literal word `RESTORE` into a text input
  (`RESTORE_CONFIRM_PHRASE`), matching the second, independent
  server-side gate in `Service.Restore`. On success it tells the operator
  to sign in again — the restore just replaced the session table too.

## Restore-drill runbook

The point of a backup you've never restored is that you don't actually
know it works. This drill restores a real exported `.nodedrbackup` file
into a **throwaway** Postgres container — never against a database you
care about — and runs sanity queries against it directly with `psql`.
Budget 15–20 minutes.

### 0. Prerequisites

- `pg_dump`/`pg_restore` matching the server's Postgres major version
  (18) on your workstation, or just run the drill from inside a
  `postgres:18` container as done below — simplest, and guaranteed
  version-matched.
- The `.nodedrbackup` file you're verifying, and the `AEAD_ENCRYPTION_KEY`
  it was created under (from the deployment's `.env`).
- `openssl`, `curl`, `jq`, `python3` (or any small language) — used below
  only to speak the same length-prefixed file format `parseFile` in
  `internal/modules/backup/app/service.go` parses, and to drive the AEAD
  decryption. You do not need to reimplement AEAD by hand: the simplest
  real-world path is described in Option A below (drive the app's own
  `/backup/restore` endpoint against a scratch app+DB stack), which never
  requires decrypting the file yourself.

### Option A — restore via the app's own endpoint (closest to reality)

This is what the app itself does on a real restore, so it's the highest-
fidelity drill: stand up a full scratch stack (Postgres + this app), not
just Postgres.

```bash
# 1. Scratch network + a throwaway Postgres 18, isolated from anything real.
docker network create ndr-restore-drill
docker run -d --name ndr-drill-pg --network ndr-restore-drill \
  -e POSTGRES_USER=billing_migrator \
  -e POSTGRES_PASSWORD=drill-only-password \
  -e POSTGRES_DB=billing \
  postgres:18

# 2. Wait for it to accept connections.
until docker exec ndr-drill-pg pg_isready -U billing_migrator; do sleep 1; done

# 3. Run this repo's migrations against it, as the owner role — same
#    "-migrate" one-shot pattern deploy/compose/docker-compose.yml uses.
docker run --rm --network ndr-restore-drill \
  -e DATABASE_DSN="postgres://billing_migrator:drill-only-password@ndr-drill-pg:5432/billing?sslmode=disable" \
  ghcr.io/raktim94/rechvix-server:<the version the backup was taken from> -migrate

# 4. Grant billing_app the same runtime privileges the real install's
#    deploy/compose/postgres-init/01-create-runtime-role.sh script sets
#    up (see migrations/0029_runtime_role_grants.up.sql for exactly what
#    that role needs) — or, for this drill only, just also point
#    DATABASE_DSN at billing_migrator for the app container. That
#    reintroduces the RLS-bypass problem for real traffic, which is why
#    production never does it — but it's fine for a disposable drill
#    container nobody else ever talks to.

# 5. Start the app against the scratch DB, with the SAME
#    AEAD_ENCRYPTION_KEY and a BACKUP_DATABASE_DSN pointed at the same
#    scratch Postgres as billing_migrator.
docker run -d --name ndr-drill-app --network ndr-restore-drill -p 18080:8080 \
  -e DATABASE_DSN="postgres://billing_app:drill-only-password@ndr-drill-pg:5432/billing?sslmode=disable" \
  -e BACKUP_DATABASE_DSN="postgres://billing_migrator:drill-only-password@ndr-drill-pg:5432/billing?sslmode=disable" \
  -e AEAD_ENCRYPTION_KEY="<the real deployment's key>" \
  -e DATABASE_AUTO_MIGRATE=false \
  ghcr.io/raktim94/rechvix-server:<version>

curl -f http://localhost:18080/health/ready

# 6. Bootstrap-free login isn't possible here (the drill DB has whatever
#    org/user the restored backup contains, not a fresh one) — restore
#    IS the bootstrap step: log in with a real owner account from the
#    backup after step 7. So do the restore against the drill's actual
#    empty-but-migrated DB using the ADMIN login you'll only have once
#    the backup is restored — meaning for this option, do the restore
#    call directly against the drill's BACKUP_DATABASE_DSN via pgtools
#    (Option B below) as the very first action, THEN come back and
#    verify via login + the app's own reports. In practice: run Option B
#    first, then hit the app's UI/API for the invoice/trial-balance
#    checks in step 4 below instead of raw SQL, if you'd rather exercise
#    real app code paths (accounting.GetTrialBalance etc.) than SQL.
```

Option A is worth doing once to prove the *app* can read a restored
database end-to-end (login works, dashboard loads, reports run) — but it
has a bootstrapping wrinkle (you need to already be inside the restored
org to use the authenticated endpoints), so the sanity checks below use
the simpler, no-app-required path.

### Option B — restore directly with `pgtools`-equivalent commands + raw SQL checks (fastest, recommended default)

```bash
# 1. Throwaway Postgres 18 — nothing else talks to it.
docker network create ndr-restore-drill 2>/dev/null || true
docker run -d --name ndr-drill-pg --network ndr-restore-drill \
  -e POSTGRES_USER=billing_migrator \
  -e POSTGRES_PASSWORD=drill-only-password \
  -e POSTGRES_DB=billing \
  postgres:18
until docker exec ndr-drill-pg pg_isready -U billing_migrator >/dev/null 2>&1; do sleep 1; done

# 2. Apply this repo's schema first — pg_restore --clean --if-exists
#    (what Restore() runs) expects the target objects to already exist so
#    it can drop them; on a genuinely bare DB it's also fine (--if-exists
#    just means "don't error if something's missing"), but running
#    migrations first matches what a real restore target (an already-
#    running app's DB) looks like.
docker run --rm --network ndr-restore-drill \
  -e DATABASE_DSN="postgres://billing_migrator:drill-only-password@ndr-drill-pg:5432/billing?sslmode=disable" \
  ghcr.io/raktim94/rechvix-server:<version> -migrate

# 3. Decrypt the .nodedrbackup file into a plain pg_dump archive. This is
#    the one step with no ready-made CLI — Export/Restore's AEAD sealing
#    lives in internal/platform/crypto and internal/modules/backup/app,
#    not exposed as a standalone tool. The fastest real path is a tiny
#    throwaway Go program using this repo's own packages directly:

cat > /tmp/decrypt_backup.go <<'EOF'
package main

import (
	"encoding/binary"
	"encoding/base64"
	"fmt"
	"os"

	appcrypto "rechvix/internal/platform/crypto"
)

func main() {
	fileBytes, _ := os.ReadFile(os.Args[1])
	keyB64 := os.Args[2] // same AEAD_ENCRYPTION_KEY value as the source deployment's .env
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil { panic(err) }
	aead, err := appcrypto.NewAEAD(key)
	if err != nil { panic(err) }

	const magicLen = 4
	headerLen := binary.BigEndian.Uint32(fileBytes[magicLen : magicLen+4])
	headerJSON := fileBytes[magicLen+4 : magicLen+4+int(headerLen)]
	sealed := fileBytes[magicLen+4+int(headerLen):]

	dump, err := aead.Open(sealed, headerJSON)
	if err != nil { panic(fmt.Sprintf("decrypt failed (wrong key, or corrupted): %v", err)) }
	os.WriteFile(os.Args[3], dump, 0o600)
	fmt.Println("wrote", len(dump), "bytes; header:", string(headerJSON))
}
EOF
# Run from the repo root so the rechvix/internal/... import resolves:
go run /tmp/decrypt_backup.go /path/to/rechvix-backup-*.nodedrbackup "$AEAD_ENCRYPTION_KEY" /tmp/plain.dump

# 4. Restore the plain archive with real pg_restore, same flags
#    pgtools.Restore uses.
docker cp /tmp/plain.dump ndr-drill-pg:/tmp/plain.dump
docker exec -e PGPASSWORD=drill-only-password ndr-drill-pg \
  pg_restore -U billing_migrator -d billing --clean --if-exists --no-owner /tmp/plain.dump
```

### Sanity checks (run against the drill container — never production)

```bash
PSQL="docker exec -e PGPASSWORD=drill-only-password ndr-drill-pg psql -U billing_migrator -d billing -qt -c"

# a. Overall row-count sanity — table exists and isn't empty/truncated.
$PSQL "SELECT count(*) FROM organisations;"
$PSQL "SELECT count(*) FROM sales_documents;"
$PSQL "SELECT count(*) FROM journal_lines;"

# b. A known invoice actually round-tripped. Pick a real document_number
#    you know exists in the source system (Settings > ... or ask the
#    business) and confirm it, and its total, are present:
$PSQL "SELECT document_number, document_type, status, grand_total_amount
       FROM sales_documents
       WHERE document_type = 'TAX_INVOICE' AND document_number = '<a real invoice number>';"

# c. Double-entry accounting invariant: every journal must balance, so
#    total debits must equal total credits, PER ORGANISATION, across the
#    whole restored ledger. This is the trial-balance check
#    internal/modules/reporting/pg/pg.go's TrialBalance query is built
#    on, run here directly as raw SQL against the restored DB (no app,
#    no HTTP, no RLS role needed since billing_migrator sees everything):
$PSQL "SELECT organisation_id,
              SUM(debit_amount)  AS total_debit,
              SUM(credit_amount) AS total_credit,
              SUM(debit_amount) - SUM(credit_amount) AS out_of_balance
       FROM journal_lines
       GROUP BY organisation_id;"
# out_of_balance must be exactly 0 for every organisation. Any nonzero
# value means the restored ledger is corrupt — a strictly stronger check
# than "pg_restore exited 0."

# d. Spot-check one specific account's balance against what the source
#    system's Trial Balance report showed at backup time, e.g. Accounts
#    Receivable (code 1100):
$PSQL "SELECT a.code, a.name,
              COALESCE(SUM(jl.debit_amount),0) - COALESCE(SUM(jl.credit_amount),0) AS balance
       FROM accounts a
       LEFT JOIN journal_lines jl ON jl.account_id = a.id
       WHERE a.code = '1100'
       GROUP BY a.code, a.name;"
```

If (a)–(d) all check out, the backup is genuinely restorable, not just
"the restore command exited zero." Tear the drill down when done:

```bash
docker rm -f ndr-drill-pg ndr-drill-app 2>/dev/null
docker network rm ndr-restore-drill 2>/dev/null
```

Run this drill periodically (monthly is reasonable for a small-business
deployment) and after any major version upgrade — a schema change that
breaks `pg_restore --clean --if-exists` against an *older* backup's
archive is exactly the kind of thing that should be caught here, not
during a real incident.

## Scheduled/unattended export (cron + curl)

There is no built-in scheduler (see Gaps below), so this is on the
operator. Because API keys structurally cannot call `/backup/export`
today (see the auth note above), the only working unattended path is a
scripted session login, kept alive only long enough to make the one
export call, with the session immediately logged out afterward:

```bash
#!/usr/bin/env bash
# /opt/rechvix/scripts/nightly-backup.sh — run via cron, e.g.:
#   0 2 * * * /opt/rechvix/scripts/nightly-backup.sh >> /var/log/rechvix-backup.log 2>&1
set -euo pipefail

API_BASE="https://your-rechvix-host/api/v1"
BACKUP_DIR="/var/backups/rechvix"
COOKIE_JAR="$(mktemp)"
trap 'curl -sS -b "$COOKIE_JAR" -X POST "$API_BASE/auth/logout" >/dev/null || true; rm -f "$COOKIE_JAR"' EXIT

mkdir -p "$BACKUP_DIR"
STAMP="$(date +%Y-%m-%dT%H-%M-%S)"

# A dedicated, low-privilege login used only for this script — grant it
# ONLY the backup.manage permission (plus whatever bootstrap-default role
# minimum RBAC requires), not a full Owner/Admin account, per the usual
# least-privilege reasoning. There is currently no API-key path for this
# (see docs/operations/backup-restore.md's auth note) — a real session
# login is the only mechanism that satisfies backup.manage today.
curl -sS -c "$COOKIE_JAR" -X POST "$API_BASE/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"${RECHVIX_BACKUP_EMAIL}\",\"password\":\"${RECHVIX_BACKUP_PASSWORD}\"}" \
  -o /dev/null -w '%{http_code}\n' | grep -q '^200$' || { echo "login failed" >&2; exit 1; }

OUT_FILE="$BACKUP_DIR/rechvix-backup-$STAMP.nodedrbackup"
HTTP_CODE=$(curl -sS -b "$COOKIE_JAR" -X POST "$API_BASE/backup/export" -o "$OUT_FILE" -w '%{http_code}')
if [ "$HTTP_CODE" != "200" ]; then
  echo "export failed: HTTP $HTTP_CODE" >&2
  rm -f "$OUT_FILE"
  exit 1
fi

# Basic sanity before trusting the file at all: non-trivial size and the
# "NDRB" magic bytes Service.parseFile checks for.
SIZE=$(stat -c%s "$OUT_FILE" 2>/dev/null || stat -f%z "$OUT_FILE")
MAGIC=$(head -c4 "$OUT_FILE")
if [ "$SIZE" -lt 100 ] || [ "$MAGIC" != "NDRB" ]; then
  echo "exported file looks wrong (size=$SIZE magic=$MAGIC)" >&2
  exit 1
fi

echo "backup ok: $OUT_FILE ($SIZE bytes)"
# Retention: keep the trailing N days locally, ship the rest off-box —
# this script deliberately does neither by default; wire in your own
# `rclone`/`aws s3 cp`/etc. call here. Off-host copy is not optional in
# practice — a backup that lives only on the machine it backs up doesn't
# survive the failure modes that matter (disk death, host compromise).
```

`RECHVIX_BACKUP_EMAIL`/`RECHVIX_BACKUP_PASSWORD` should be an env file
readable only by the cron user (`chmod 600`), sourced before this script
runs, for a dedicated user with only the RBAC permissions needed (at
minimum `backup.manage`) — not the actual Owner login. If that account
also has MFA enrolled, `POST /auth/login` needs `mfa_code` in the body
too (`internal/modules/identity/httpapi/handlers.go`'s `loginRequest`);
either exempt this account from MFA or extend the script to generate a
TOTP code from a stored secret.

### If you want this to work with an API key instead

The clean fix is a small code change: add `"backup:manage"` (or similar)
to `domain.ValidScopes` and map it to `"backup.manage"` in
`permissions.APIScopePermissions`. Once that exists, the script above
simplifies to a single `curl -H "Authorization: Bearer $API_KEY" -X POST
$API_BASE/backup/export -o "$OUT_FILE"` with no login/cookie-jar/logout
dance — API keys don't expire the way sessions do, so this is the better
long-term shape for unattended callers. That change doesn't exist in
this repo as of this writing.

## Upgrade approach: how new migrations actually get applied

There is no separate "upgrade" command — the Compose file's `migrate`
service (`command: ["-migrate"]`, `deploy/compose/docker-compose.yml`)
runs the same binary as `app`/`worker`, in one-shot mode, on every
`docker compose up -d` — including an upgrade, which is just "pull/build
a newer image, `docker compose up -d` again":

1. `postgres` starts (or is already running/reused, same volume).
2. `migrate` waits for `service_healthy` on `postgres`, then runs
   `internal/platform/database.Migrate()` against `DATABASE_DSN` as
   `billing_migrator`. This calls `golang-migrate/v4`'s `m.Up()` against
   the embedded `migrations.FS` (`migrations/embed.go`) — every
   `NNNN_name.up.sql` file not yet recorded in golang-migrate's own
   `schema_migrations` tracking table gets applied, in numeric order.
   **It is idempotent**: with nothing new pending, `m.Up()` returns
   `migrate.ErrNoChange`, which `Migrate()` treats as success, not an
   error — so re-running `docker compose up -d` with no new migrations
   is always safe.
3. `app` and `worker` both declare `depends_on: migrate:
   condition: service_completed_successfully` — they will not start at
   all if `migrate` exits nonzero, so a broken migration blocks the
   deploy rather than starting the app against a half-migrated schema.
   Both also run with `DATABASE_AUTO_MIGRATE: "false"` and connect as
   `billing_app`, which has no schema-modification privileges by design
   — they cannot and do not attempt migrations themselves.
4. New migration files ship as part of a normal code release; there's no
   separate migration-only artifact or release channel. `CHANGELOG.md`
   entries for a stage describe the schema/behavior change in prose (e.g.
   Stage 14's `receivable_reminders` table, Stage 12's team-member
   tables) — as of this writing the changelog does not yet follow a
   formal "migration NNNN must run before deploy X" callout convention;
   the practical signal that a release added migrations is simply new
   `migrations/NNNN_*.up.sql`/`.down.sql` files in that release's diff,
   which `migrate`'s ordinary sequential apply picks up automatically
   the next time `docker compose up -d` runs. There is nothing to do
   manually beyond the normal upgrade step — no separate "remember to
   run migrations" instruction exists or is needed, precisely because
   `migrate` always runs before `app`/`worker` on every `up -d`.
5. **Rollback is intentionally one-directional in production.**
   `database.MigrateDown` (steps back N migrations) exists and is
   exercised by the integration suite (`go test -tags=integration`
   applies Up then Down to prove both directions are consistent) and is
   fine for local dev, but brief §72's stance — reflected in this
   codebase's actual migrations — is forward-only: a broken migration
   gets fixed by a new corrective migration, not by rolling the schema
   backward under a live deployment. Treat a full restore from a
   pre-upgrade backup (this doc's restore drill, for real, if it ever
   comes to that) as the actual rollback mechanism for a bad release, not
   `MigrateDown` against production.

**Take a backup immediately before any upgrade that changes migrations**
(diff `migrations/` between your currently-deployed tag and the one
you're upgrading to — a new file there means schema changes are coming).
`pg_restore --clean --if-exists` restoring an *older* backup's archive
against a *newer* schema is exactly the scenario this doc's restore drill
exists to catch before it becomes a production incident.

## Known gaps (honest, as of this writing)

- **No built-in scheduled backup job.** Nothing in this repo triggers
  `/backup/export` on a timer. The cron script above is the concrete
  workaround, not a stand-in for a real feature.
- **No built-in automated restore-verification job.** Nothing replays a
  backup into a scratch DB and checks it automatically. The restore drill
  above is a manual (or operator-scripted-into-their-own-cron) procedure,
  not something this codebase runs for you.
- **API keys cannot drive export/restore** under the current fixed scope
  vocabulary (see the auth note above) — only a session-cookie login can,
  which is why the cron example logs in rather than using a bearer token.
- **No off-host shipping built in.** `/backup/export` gets you a file on
  whatever machine ran the request; getting it to a second location
  (object storage, a second host, etc.) is left to the operator (or the
  cron script's "wire in your own upload call" comment).

None of these are silently assumed solved elsewhere — they're real,
open gaps, tracked the same way `docs/operations/deployment.md`'s
Backups section already flags the missing scheduler.
