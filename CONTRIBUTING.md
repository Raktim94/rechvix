# Contributing to Rechvix

Thanks for considering a contribution. Rechvix is a real production-grade
platform, not a toy project — the bar for changes is "would I trust this
with a real business's books," and the review process reflects that.

## Before you start

Read [`README.md`](README.md) and [`docs/architecture.md`](docs/architecture.md)
first. The architecture doc explains *why* the codebase is shaped the way it
is (modular monolith, `NUMERIC` money, append-only stock ledger, double-entry
enforced at three layers, generic tax engine) — most "obviously simpler"
alternatives someone new to the codebase suggests are already covered there,
with the reasoning for why they weren't chosen.

For anything beyond a small fix, open an issue first describing what you want
to change and why, before writing code. It saves everyone time when a PR
turns out to conflict with a design decision or an in-progress stage.

## Development setup

Requires **Go 1.27+** (or let `go`'s toolchain auto-download it — `go.mod`
already pins the version) and a **PostgreSQL 18** instance, or Docker if you'd
rather not install Postgres locally.

```bash
git clone https://github.com/Raktim94/rechvix.git
cd rechvix

# Option A — your own local Postgres 18
export DATABASE_DSN="postgres://user:pass@localhost:5432/rechvix?sslmode=disable"
export DATABASE_AUTO_MIGRATE=true
go run ./apps/server

# Option B — Docker Compose (matches production topology)
cd deploy/compose
cp .env.example .env   # fill in the required secrets, see comments in the file
docker compose up -d
```

Frontend (separate terminal):

```bash
cd apps/web
npm install
npm run dev   # proxies /api to :8080 automatically, see vite.config.ts
```

Then open `http://localhost:5173/setup` to bootstrap your first organisation.

See `internal/platform/config/config.go` for every environment variable the
server reads — each has either a required source or a documented default,
and a missing required value fails fast at startup with a clear message.

## Running tests

```bash
go test ./...                              # unit tests, no external dependencies
go test -tags=integration ./...            # integration tests — needs Docker (Testcontainers spins up real postgres:18)
```

Frontend:

```bash
cd apps/web
npm run lint    # oxlint
npx tsc -b      # TypeScript strict mode, must be zero errors
npm run build
```

A change isn't "done" until both unit and (where relevant) integration tests
pass, and the frontend still builds clean under strict TypeScript.

For changes to the packaged systemd units, install the candidate `.deb` on a
Debian or Ubuntu test machine. Run the check from the repository root.

```bash
bash scripts/test-deb-units.sh
```

The check verifies both installed units and confirms that systemd rejects a
temporary copy of the worker with its `ExecStart` directive removed.

## Code conventions

- **Money is never `float32`/`float64`.** Always the internal `Money` type
  wrapping `shopspring/decimal`, backed by `NUMERIC` columns. A PR that
  introduces a float for anything money-shaped will be rejected outright —
  see the README's "Core design decisions" table for why.
- **Module layering**: each `internal/modules/*` package follows
  `domain/` (pure business rules, no I/O) → `app/` (use-case orchestration)
  → `pg/` (repository implementation) → `httpapi/` (HTTP handlers). Look at
  `internal/modules/organisation` as the reference shape before adding a new
  module or a new use case to an existing one.
- **Every tenant-scoped table** must be filtered by `organisation_id` at the
  application layer *and* rely on PostgreSQL Row-Level Security — defense in
  depth, not either/or (see `docs/adr/0006-getbyid-organisation-scoping.md`
  for a real example of this being enforced after a gap was found).
- **Structured logging** via `log/slog`, never `fmt.Println`/`log.Print` in
  application code.
- **No `UPDATE` on posted journal rows.** Accounting corrections are new
  entries (reversals/adjustments), never in-place edits — this is enforced
  at the database grant level, not just convention.
- **Government API adapters are versioned by directory** (`einvoice/v1`,
  `.../vNext`, etc.) — a schema change from GSTN is a new adapter, not an
  edit to an existing one that breaks anyone still on the old version.

## Commit messages

Short, imperative, specific about *what changed and why it mattered* — look
at `git log` for the house style. Good examples from this repo's own
history: `Fix critical bug: no real user could ever finalize an invoice
(found by actually running the app)`, `Security hardening: add
organisation_id filter to every GetByID (defense in depth)`. Avoid vague
messages like "fix stuff" or "update code."

## Opening a pull request

1. Fork the repo, branch off `main`.
2. Keep the PR focused — one logical change per PR is easier to review and
   easier to revert if something's wrong.
3. Include tests for new behavior and for any bug fix (a regression test
   that would have caught the bug, not just the fix itself).
4. Make sure `go build ./...`, `go vet ./...`, `go test ./...`, and the
   frontend build/lint all pass locally before requesting review.
5. Describe *why* the change is needed, not just what it does — reviewers
   need the reasoning to judge tradeoffs, not just read a diff.

## License

Rechvix is licensed under the [GNU Affero General Public License v3.0](LICENSE)
(AGPL-3.0). By contributing, you agree that your contributions will be
licensed under the same terms.

## Reporting security issues

Please do **not** open a public issue for a security vulnerability. Instead,
report it privately — see [`docs/architecture.md`](docs/architecture.md) for
the threat model this project is built against, and reach out via
[nodedr.com](https://www.nodedr.com/) for a private disclosure channel.
