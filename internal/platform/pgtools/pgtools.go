// Package pgtools shells out to the real pg_dump/pg_restore binaries
// (deploy/docker/server.Dockerfile copies them, plus their glibc runtime
// dependencies, from the official postgres image into the otherwise-musl
// Alpine runtime — verified to actually run, not just build, before
// relying on it here) rather than reimplementing PostgreSQL's own
// dump/restore format and dependency-ordering logic in Go. This is the
// one place in the codebase that executes an external binary — used only
// by internal/modules/backup, which requires a separate, explicitly
// elevated (RLS-bypassing) connection distinct from the app's normal
// billing_app runtime role; see that package's doc comment for why a
// normal request-scoped connection can't do this at all.
package pgtools

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// connEnv turns a postgres:// DSN into pg_dump/pg_restore's discrete
// -h/-p/-U/-d flags plus a PGPASSWORD/PGSSLMODE env var — never a raw
// connection URI on the command line, which would otherwise put the
// password in plaintext in that process's argv, visible to any other
// local user via `ps` for as long as the command runs. This is the only
// reason this function exists instead of passing dsn straight through.
func connEnv(dsn string) (args []string, env []string, dbName string, err error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, nil, "", fmt.Errorf("pgtools: parsing DSN: %w", err)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	dbName = strings.TrimPrefix(u.Path, "/")
	if dbName == "" {
		return nil, nil, "", fmt.Errorf("pgtools: DSN has no database name")
	}
	args = []string{"-h", host, "-p", port}
	if user := u.User.Username(); user != "" {
		args = append(args, "-U", user)
	}
	if pw, ok := u.User.Password(); ok {
		env = append(env, "PGPASSWORD="+pw)
	}
	if sslmode := u.Query().Get("sslmode"); sslmode != "" {
		env = append(env, "PGSSLMODE="+sslmode)
	}
	// Extend the current process's real environment (PATH etc.) rather
	// than replacing it outright — pg_dump/pg_restore's dynamic linker
	// and locale handling can depend on variables beyond the two set
	// above, and there's no reason to withhold them from a subprocess
	// this code itself decided to launch.
	env = append(os.Environ(), env...)
	return args, env, dbName, nil
}

// Dump runs `pg_dump -Fc` (PostgreSQL's compressed custom archive
// format — the one pg_restore actually needs, not plain SQL text)
// against dsn and returns the raw archive bytes. dsn must be a role
// that can read every row of every table unfiltered — RLS-restricted
// roles like billing_app cannot produce a complete backup (see
// internal/modules/backup's doc comment).
func Dump(ctx context.Context, dsn string) ([]byte, error) {
	args, env, dbName, err := connEnv(dsn)
	if err != nil {
		return nil, err
	}
	args = append(args, "-d", dbName, "-Fc")
	cmd := exec.CommandContext(ctx, "pg_dump", args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pgtools: pg_dump failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// Restore runs `pg_restore --clean --if-exists --no-owner` (drop-then-
// recreate every object the archive contains, tolerating one that
// doesn't already exist, and never trying to reassign ownership to a
// role name that might not exist in the target environment) against
// dsn, feeding archive on stdin. dsn must be the same kind of
// RLS-bypassing role Dump requires — restoring as billing_app would
// silently fail to write rows RLS hides from it, corrupting the restore
// in a way that wouldn't necessarily error loudly.
//
// This is a genuinely disruptive operation against a live database —
// callers must have their own explicit-confirmation gate before ever
// reaching this function; it performs none of its own.
func Restore(ctx context.Context, dsn string, archive []byte) error {
	args, env, dbName, err := connEnv(dsn)
	if err != nil {
		return err
	}
	args = append(args, "-d", dbName, "--clean", "--if-exists", "--no-owner")
	cmd := exec.CommandContext(ctx, "pg_restore", args...)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(archive)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pgtools: pg_restore failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Version runs `pg_dump --version` — used only as a startup/health
// check (internal/modules/backup logs a warning, doesn't fail startup,
// if this errors) to confirm the binary is actually present and
// runnable in this environment before a user ever reaches the feature
// and gets a confusing mid-request failure instead.
func Version(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "pg_dump", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("pgtools: pg_dump --version failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
