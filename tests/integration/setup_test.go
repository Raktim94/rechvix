//go:build integration

// Package integration runs Stage 2's integration tests against a real
// PostgreSQL 18 container (Testcontainers) — no mocks for the database
// layer, per brief §65. Run with:
//
//	go test ./tests/integration/... -tags=integration -v
//
// Excluded from the default `go test ./...` run (which stays fast and
// Docker-free) by the integration build tag.
package integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"rechvix/internal/platform/database"
	"rechvix/migrations"
)

// sharedPool is set up once for the whole integration test binary
// (TestMain) rather than per-test, since spinning up a fresh Postgres
// container per test would make this suite unpleasantly slow. Isolation
// between tests comes from each test using its own freshly generated
// UUIDs (organisations, users, ...), not from a clean database per test.
//
// Deliberately connects as a NON-owning role (billing_app), not the role
// that ran migrations — see the deployment-requirement comment in
// migrations/0001_organisation_hierarchy.up.sql. The first version of
// this suite connected as the migration/owner role for everything, which
// made every RLS test pass FOR THE WRONG REASON: Postgres exempts a
// table's owner from its own RLS policies, so the tests weren't
// exercising RLS at all, they were exercising owner-bypass. Provisioning
// a second role here is what makes TestRLS_* actually test what their
// names claim.
var sharedPool *database.Pool

// sharedMigratorDSN is the billing_migrator-role DSN — a table owner,
// which bypasses RLS by default (migrations/0001's "DEPLOYMENT
// REQUIREMENT" comment). sharedPool above deliberately does NOT use
// this (see its own doc comment: connecting the whole suite as the
// owning role would make every RLS test pass for the wrong reason).
// backup_test.go is the one place that legitimately needs it — the
// same role internal/modules/backup's real BACKUP_DATABASE_DSN points
// at in production, since a full backup/restore must see every row of
// every table unfiltered.
var sharedMigratorDSN string

func TestMain(m *testing.M) {
	ctx := context.Background()

	migratorDSN, cleanup, err := acquirePostgres(ctx)
	if err != nil {
		panic(err.Error())
	}
	defer cleanup()
	sharedMigratorDSN = migratorDSN

	if err := database.Migrate(migratorDSN, migrations.FS); err != nil {
		panic("applying migrations: " + err.Error())
	}

	if err := provisionAppRole(ctx, migratorDSN); err != nil {
		panic("provisioning billing_app role: " + err.Error())
	}

	appDSN := strings.Replace(migratorDSN, "billing_migrator:billing_migrator@", "billing_app:billing_app_pw@", 1)

	pool, err := database.NewPool(ctx, database.Config{DSN: appDSN, MaxConns: 10})
	if err != nil {
		panic("connecting pool: " + err.Error())
	}
	defer pool.Close()

	sharedPool = pool

	m.Run()
}

// acquirePostgres returns a billing_migrator-role DSN pointing at a
// fresh billing_test database, plus a cleanup func. Two paths:
//
//  1. Default (Docker available): Testcontainers spins up a real,
//     always-fresh postgres:18 container — this is the path every other
//     stage's own testing was verified against and stays the default so
//     CI and any developer with Docker gets unchanged behavior.
//  2. TEST_POSTGRES_ADMIN_DSN set (no Docker in this environment):
//     connects to an already-running external PostgreSQL instance as a
//     superuser and drops+recreates billing_test fresh, so isolation
//     between runs matches the container path even though the server
//     itself is long-lived. Added specifically because this environment
//     has no Docker daemon and no root to install one — a real
//     PostgreSQL 18 (via a user-space conda-forge install, no root
//     needed) is a genuine, non-mocked substitute for exercising this
//     suite's RLS/security tests, not a shortcut around them.
func acquirePostgres(ctx context.Context) (migratorDSN string, cleanup func(), err error) {
	if adminDSN := os.Getenv("TEST_POSTGRES_ADMIN_DSN"); adminDSN != "" {
		return acquireExternalPostgres(ctx, adminDSN)
	}

	container, err := tcpostgres.Run(ctx, "postgres:18",
		tcpostgres.WithDatabase("billing_test"),
		tcpostgres.WithUsername("billing_migrator"),
		tcpostgres.WithPassword("billing_migrator"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		return "", nil, fmt.Errorf("starting postgres container: %w", err)
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", nil, fmt.Errorf("getting connection string: %w", err)
	}
	return dsn, func() { _ = container.Terminate(ctx) }, nil
}

// acquireExternalPostgres connects to adminDSN (any role that can DROP/
// CREATE DATABASE and roles — e.g. the postgres superuser) and resets
// billing_test + the billing_migrator role to a clean state, matching
// what a fresh Testcontainers container provides.
func acquireExternalPostgres(ctx context.Context, adminDSN string) (migratorDSN string, cleanup func(), err error) {
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		return "", nil, fmt.Errorf("connecting to TEST_POSTGRES_ADMIN_DSN: %w", err)
	}
	defer admin.Close()

	// DROP DATABASE cannot run inside a transaction/pipelined batch —
	// each statement goes over its own Exec call, not a single
	// semicolon-joined string. billing_app is deliberately NOT dropped
	// here (see provisionAppRole's DO-block comment) — it's a
	// cluster-wide role name shared with migrate_test.go's own separate
	// database, so dropping it here can fail with "objects depend on it"
	// whenever that test has already granted it privileges there.
	for _, stmt := range []string{
		`DROP DATABASE IF EXISTS billing_test`,
		`DROP ROLE IF EXISTS billing_migrator`,
		`CREATE ROLE billing_migrator WITH LOGIN PASSWORD 'billing_migrator' CREATEDB CREATEROLE`,
		`CREATE DATABASE billing_test OWNER billing_migrator`,
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			return "", nil, fmt.Errorf("resetting billing_test (%s): %w", stmt, err)
		}
	}

	// Rebuild the DSN against billing_test as billing_migrator, keeping
	// adminDSN's host/port — sslmode=disable since this is always a
	// local, trust-auth, throwaway test instance.
	base := strings.SplitN(adminDSN, "@", 2)
	if len(base) != 2 {
		return "", nil, fmt.Errorf("TEST_POSTGRES_ADMIN_DSN is not in postgres://user:pass@host:port/db form")
	}
	hostPart := strings.SplitN(base[1], "/", 2)[0]
	dsn := fmt.Sprintf("postgres://billing_migrator:billing_migrator@%s/billing_test?sslmode=disable", hostPart)
	return dsn, func() {}, nil
}

// provisionAppRole creates the non-owning runtime role and grants it
// exactly the privileges apps/server/apps/worker need: DML on every
// table, and EXECUTE on the one SECURITY DEFINER bootstrap function.
// A real deployment does this once, as part of provisioning (documented
// in migrations/0001_organisation_hierarchy.up.sql), not on every
// process startup — this test suite does it here because it is also
// standing up the entire database from scratch.
func provisionAppRole(ctx context.Context, migratorDSN string) error {
	conn, err := pgxpool.New(ctx, migratorDSN)
	if err != nil {
		return err
	}
	defer conn.Close()

	statements := []string{
		// Idempotent (DO-block guarded, same pattern as
		// migrations/0029_runtime_role_grants.up.sql), not a bare CREATE
		// ROLE — billing_app is a cluster-wide role name that migrate_test.go's
		// own full migration up/down cycle (against its own separate
		// billing_migrate_test database) also creates/grants via that same
		// migration 0029, since role existence checks aren't per-database.
		// A bare CREATE ROLE here would fail on the TEST_POSTGRES_ADMIN_DSN
		// path whenever that test runs first in the same long-lived cluster.
		`DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'billing_app') THEN CREATE ROLE billing_app WITH LOGIN PASSWORD 'billing_app_pw'; END IF; END $$`,
		`GRANT USAGE ON SCHEMA public TO billing_app`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO billing_app`,
		`GRANT EXECUTE ON FUNCTION auth_lookup_user_by_email(text) TO billing_app`,
	}
	for _, stmt := range statements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
