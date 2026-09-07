# Deployment

Covers the self-hosted Docker Compose path (`deploy/compose/`) and the
CasaOS/ZimaOS app store path (`casaos/`). Both are described
architecturally in `docs/architecture.md` §12; this doc is the actual
step-by-step. As of this writing there is no `apps/web` yet (Stage 10 —
this doc's Stage 10a scope is packaging the Go backend/API only) — the API
is fully functional, but there's no browser UI to open yet.

## Self-hosted (Docker Compose)

```bash
git clone https://github.com/Raktim94/rechvix.git
cd rechvix/deploy/compose
cp .env.example .env
```

Edit `.env` and set three required values:

- `POSTGRES_PASSWORD` — for `billing_migrator`, the schema-owning role that
  runs migrations.
- `BILLING_APP_PASSWORD` — for `billing_app`, the separate role the app
  actually connects as at runtime. **Must be different from
  `POSTGRES_PASSWORD`** — see "Why two database roles" below.
- `AEAD_ENCRYPTION_KEY` — encrypts stored integration credentials at rest.
  Generate with `openssl rand -base64 32`. Back this up separately from
  your database backup; losing it makes those specific stored values
  unrecoverable (nothing else is affected).

Generate strong passwords for the first two the same way, e.g.
`openssl rand -base64 24`.

Then:

```bash
docker compose up -d
```

This builds and starts four containers: `postgres`, a one-shot `migrate`
service (applies pending schema migrations, then exits — check
`docker compose ps` shows it `Exited (0)`, not still running or a
non-zero exit), `app` (the API, published on port 8080 by default), and
`worker` (background job processing — e-Invoice/e-Way Bill/webhook/
notification delivery). Confirm it's healthy:

```bash
curl http://localhost:8080/health/ready   # expect HTTP 200
```

To stop: `docker compose down` (keeps your data). To fully reset,
including deleting all data: `docker compose down -v`.

### Why two database roles

The schema uses PostgreSQL Row-Level Security as a defense-in-depth tenant
isolation layer (`docs/architecture.md` §10) — every business table is
scoped by `organisation_id`, enforced both at the application layer and by
RLS policies. Postgres exempts a table's *owner* from its own RLS
policies. If the application connected as the same role that owns the
tables (the natural default — whichever role ran the migrations), every
RLS policy in the schema would be silently a no-op for it, and tenant
isolation would reduce to "hope the application-layer check never has a
bug" — exactly the single point of failure RLS exists to prevent. The
`migrate` service runs as `billing_migrator` (the owner); `app` and
`worker` run as `billing_app` (a separate role with only ordinary
SELECT/INSERT/UPDATE/DELETE + function-execute privileges, granted by
`migrations/0029_runtime_role_grants.up.sql`). `internal/platform/database`
also logs a startup warning if it detects this misconfigured — worth
watching for after any deployment change to the database role setup.

### No external connection required for core operation

Creating invoices, tracking stock, running reports, and closing the books
all work fully offline — the app talks only to its own Postgres container.
The only things that ever reach the internet are integrations you
explicitly enable: `EINVOICE_PROVIDER=sandbox` (government e-Invoice
sandbox, Stage 8), or a real WhatsApp/email provider once configured
(Stage 9). Everything defaults to off/mock.

### Optional Compose profiles

None of these start with a plain `docker compose up -d`:

- `--profile redis` — a Redis instance, provisioned for future horizontal-
  scaling use. **Not yet wired to anything** (no session/cache store reads
  `REDIS_URL` as of this version) — safe to enable early if you want it
  provisioned, but it does nothing yet.
- `--profile minio` — an S3-compatible object store for self-hosters
  without real S3. **Also not yet wired** (no `internal/platform/files`
  storage abstraction exists as of this version, brief §41). Set
  `MINIO_ROOT_PASSWORD` in `.env` before enabling.
- `--profile reverse-proxy` — an opt-in Caddy reverse proxy with automatic
  HTTPS, for a bare VPS with nothing already in front of it. Most
  deployments don't need this: CasaOS already proxies at the host level,
  and most cloud/VPS setups already sit behind an existing
  Caddy/Traefik/nginx/ALB (`docs/architecture.md` §12's reasoning). To
  enable: `cp Caddyfile.example Caddyfile`, edit the domain, then
  `docker compose --profile reverse-proxy up -d`.

### Backups

Settings → Backup (in the app itself) downloads one complete, encrypted
`.nodedrbackup` file with every organisation's data on this instance —
click "Download backup," keep the file somewhere safe. Restoring one
(same screen) requires typing a literal confirmation phrase first and
**replaces everything currently in the database** — there is no undo.

This works automatically on the standard `docker compose` install:
`BACKUP_DATABASE_DSN` in `docker-compose.yml` is already set from the
same `POSTGRES_PASSWORD` the `migrate` service uses (the `billing_migrator`
role — a table owner, which bypasses Row-Level Security by default, and
so is the only role that can see every organisation's data unfiltered;
the app's normal `billing_app` connection deliberately cannot, by
design). No extra secret to generate. A deployment that doesn't set
`BACKUP_DATABASE_DSN` simply has the feature disabled — Settings →
Backup says so plainly rather than failing confusingly.

Under the hood this shells out to the real `pg_dump`/`pg_restore`
(`internal/platform/pgtools`, copied into the runtime image from the
official `postgres:18` image at build time — see
`deploy/docker/server.Dockerfile`), not a from-scratch reimplementation,
and encrypts the archive with the same `AEAD_ENCRYPTION_KEY` every other
encrypted-at-rest value in this app uses. Losing that key makes existing
backups undecryptable, same caveat as the MFA-secret note above.

There is still no automated **scheduled** backup or restore-verification
job (brief §42's fuller ask) — this is a real, honest, manually-triggered
export/import, not a cron job. Scripting a periodic call to
`POST /api/v1/backup/export` (e.g. from `cron` + `curl`, authenticated
the same way any other API client would be) is a reasonable way to add
scheduling yourself today; a built-in scheduler is a real gap, not
silently assumed done.

## CasaOS / ZimaOS

`casaos/docker-compose.yml` is the app store manifest — same
four-service shape as the self-hosted compose file, but referencing
pre-built, version-pinned images from GHCR instead of building from
source (CasaOS installs directly from the manifest with no access to this
repo).

**Not yet installable from a real CasaOS instance as of this version** —
`.github/workflows/docker-publish.yml` (multi-arch amd64+arm64 image
build/push) exists but hasn't run against a real version tag yet, so the
`ghcr.io/raktim94/rechvix-{server,worker}:0.1.0` images the
manifest references don't exist on GHCR yet. Cut a `v0.1.0` tag (or
trigger the workflow manually via `workflow_dispatch`) before actually
submitting to CasaOS-AppStore. The manifest's structure is validated
(`docker compose config -q` passes) — what's unverified is the actual
install experience on a real CasaOS box, which needs the images to exist
first.

Also not yet done: real icon/thumbnail/screenshot assets (the manifest
references placeholder paths — no logo exists yet, since `apps/web`/brand
work hasn't started).

## CI validation

Both compose files are checked in CI (or should be — wire this into
whatever CI workflow this repo eventually runs, brief §73):

```bash
cd deploy/compose && docker compose config -q
cd deploy/compose && docker compose --profile redis --profile minio --profile reverse-proxy config -q
cd casaos && docker compose config -q
```
