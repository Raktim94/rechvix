// Starts and stops the bundled backend: a local Postgres instance plus the
// rechvix-server binary, both as child processes on 127.0.0.1 only. This
// is what turns the desktop shell from "point me at your own server" into
// a self-contained app that works with zero setup and no internet — see
// docs/architecture.md §13 and apps/desktop/README.md in the main repo.
//
// Single-role Postgres, deliberately: production/CasaOS installs split
// migrations (owning role `billing_migrator`) from runtime traffic
// (non-owning, RLS-respecting `billing_app`) — see
// migrations/0001_organisation_hierarchy.up.sql's DEPLOYMENT REQUIREMENT
// comment in the main repo. That split defends against a shared,
// multi-tenant server accidentally bypassing RLS. A desktop install is a
// single user's own loopback-only database with at most one organisation
// (created once through the existing bootstrap flow) — the risk that
// split defends against doesn't apply here, so this uses one role for
// everything (`DATABASE_AUTO_MIGRATE=true`), matching what
// internal/platform/database/database.go already documents as a
// "legitimate, explicit choice" for local/single-instance use.
use crate::paths::AppPaths;
use crate::secrets;
use std::net::TcpListener;
use std::process::{Child, Command, Stdio};
use std::time::{Duration, Instant};

pub struct RunningBackend {
    server_process: Child,
    pub http_port: u16,
}

#[derive(Debug)]
pub enum StartupError {
    PortUnavailable(String),
    Initdb(String),
    PostgresStart(String),
    ServerSpawn(String),
    ServerExited(String),
    ServerUnhealthy,
}

impl StartupError {
    /// What the loading window shows — there's no settings-page fallback
    /// anymore, so every failure needs a legible, specific message here
    /// rather than a blank window.
    pub fn user_message(&self, paths: &AppPaths) -> String {
        let logs = paths.logs_dir.display();
        match self {
            StartupError::PortUnavailable(e) => format!(
                "Rechvix couldn't find a free local port to use ({e}). Close other apps and try reopening Rechvix."
            ),
            StartupError::Initdb(e) => format!(
                "Rechvix couldn't set up its local database ({e}). Check free disk space, then try reopening Rechvix. Details: {logs}"
            ),
            StartupError::PostgresStart(e) => format!(
                "Rechvix's local database didn't start ({e}). This can happen on first launch or on a slow disk — try waiting a moment and reopening Rechvix. Details: {logs}"
            ),
            StartupError::ServerSpawn(e) => format!(
                "Rechvix couldn't start its own server ({e}). Details: {logs}"
            ),
            StartupError::ServerExited(e) => format!(
                "Rechvix's server closed unexpectedly while starting up ({e}). Details: {logs}"
            ),
            StartupError::ServerUnhealthy => format!(
                "Rechvix's server didn't respond in time. Try reopening Rechvix. Details: {logs}"
            ),
        }
    }
}

fn pick_free_port() -> Result<u16, StartupError> {
    let listener = TcpListener::bind("127.0.0.1:0")
        .map_err(|e| StartupError::PortUnavailable(e.to_string()))?;
    Ok(listener.local_addr().map_err(|e| StartupError::PortUnavailable(e.to_string()))?.port())
    // `listener` drops here, freeing the port for the child process we're
    // about to start. A small window exists between this drop and the
    // child's own bind — acceptable for a single-user desktop app; this
    // is the standard "let the OS pick" pattern.
}

#[cfg(windows)]
fn no_window(cmd: &mut Command) {
    use std::os::windows::process::CommandExt;
    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    cmd.creation_flags(CREATE_NO_WINDOW);
}
#[cfg(not(windows))]
fn no_window(_cmd: &mut Command) {}

fn append_log(path: &std::path::Path) -> Stdio {
    std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(path)
        .map(Stdio::from)
        .unwrap_or_else(|_| Stdio::null())
}

/// Recursively copies `src` into `dst`, creating directories as needed.
/// Hand-rolled rather than pulling in a crate for one call site — used as
/// the non-Windows fallback only; Windows uses `robocopy` instead (see
/// `ensure_writable_pg_install`), which is dramatically faster for the
/// thousands of small files under Postgres's `share/timezone/`.
#[cfg(not(windows))]
fn copy_dir_recursive(src: &std::path::Path, dst: &std::path::Path) -> std::io::Result<()> {
    std::fs::create_dir_all(dst)?;
    for entry in std::fs::read_dir(src)? {
        let entry = entry?;
        let ty = entry.file_type()?;
        let dst_path = dst.join(entry.file_name());
        if ty.is_dir() {
            copy_dir_recursive(&entry.path(), &dst_path)?;
        } else {
            std::fs::copy(entry.path(), &dst_path)?;
        }
    }
    Ok(())
}

/// Postgres cannot run from the packaged, read-only, ACL-locked
/// `install_dir` — see `AppPaths::pg_bin_dir`'s doc comment for exactly
/// why (confirmed against a real MSIX install: `initdb` re-execing
/// `postgres -V` fails with Access Denied from inside
/// `C:\Program Files\WindowsApps\...`). Copies the bundled Postgres into
/// the writable per-package data directory once; a no-op on every
/// subsequent launch.
///
/// On Windows this shells out to `robocopy` rather than copying file by
/// file from Rust: Postgres's `share/timezone/` alone is 600+ small
/// files, and a naive per-file copy loop against files living under
/// WindowsApps (which involves some reparse-point/virtualization
/// overhead on every open) was measured taking long enough on a GitHub
/// Actions runner to blow well past a 90s startup budget. `robocopy` is
/// built for exactly this (bulk directory mirroring) and is an ordinary
/// System32 binary reading from — not executing anything inside —
/// `install_dir`, so it isn't subject to the ACL restriction that blocks
/// `initdb`/`postgres.exe` from launching *further* child processes
/// there.
#[cfg(windows)]
fn ensure_writable_pg_install(paths: &AppPaths) -> Result<(), StartupError> {
    if paths.pg_bin_dir().join(exe_name("postgres")).exists() {
        return Ok(());
    }
    let mut cmd = Command::new("robocopy.exe");
    cmd.arg(&paths.pg_install_source_dir)
        .arg(&paths.pg_install_dir)
        .arg("/E") // include subdirectories, including empty ones
        .arg("/R:2").arg("/W:1") // don't hang retrying a locked file for the default 1M×30s
        .arg("/MT:8") // multi-threaded — the thousands-of-small-files case this exists for
        .arg("/NFL").arg("/NDL").arg("/NJH").arg("/NJS").arg("/NP"); // quiet: only the exit code matters
    no_window(&mut cmd);
    let status = cmd.status().map_err(|e| {
        StartupError::Initdb(format!("could not copy the bundled database into a writable location: {e}"))
    })?;
    // robocopy's exit code is a bitmask, not a plain 0/nonzero: 0-7 are
    // all success/informational (files copied, some skipped because
    // identical, etc.); 8+ means a real failure. This is standard
    // robocopy behavior, not something to "fix" — checking `.success()`
    // here would treat a completely normal run as an error.
    let code = status.code().unwrap_or(-1);
    if code >= 8 {
        return Err(StartupError::Initdb(format!("robocopy exited with code {code}")));
    }
    Ok(())
}

#[cfg(not(windows))]
fn ensure_writable_pg_install(paths: &AppPaths) -> Result<(), StartupError> {
    if paths.pg_bin_dir().join(exe_name("postgres")).exists() {
        return Ok(());
    }
    copy_dir_recursive(&paths.pg_install_source_dir, &paths.pg_install_dir)
        .map_err(|e| StartupError::Initdb(format!("could not copy the bundled database into a writable location: {e}")))
}

fn exe_name(base: &str) -> String {
    if cfg!(windows) {
        format!("{base}.exe")
    } else {
        base.to_string()
    }
}

fn run_initdb(paths: &AppPaths) -> Result<(), StartupError> {
    if paths.pgdata_dir.join("PG_VERSION").exists() {
        return Ok(()); // already initialised on a previous launch
    }
    let mut cmd = Command::new(paths.pg_bin("initdb"));
    cmd.arg("--pgdata").arg(&paths.pgdata_dir)
        .arg("--username").arg("rechvix")
        // Loopback-only + single local user, so trust auth is a
        // deliberate, documented choice (mirroring database.go's own
        // framing of the single-role decision), not an oversight —
        // Postgres is only ever bound to 127.0.0.1 (see start_postgres),
        // never 0.0.0.0, so trust auth is never reachable off-machine.
        .arg("--auth").arg("trust")
        .arg("--encoding").arg("UTF8")
        .stdout(append_log(&paths.pg_log_file))
        .stderr(append_log(&paths.pg_log_file));
    no_window(&mut cmd);
    let status = cmd.status().map_err(|e| StartupError::Initdb(e.to_string()))?;
    if !status.success() {
        return Err(StartupError::Initdb(format!("initdb exited with {status}")));
    }
    Ok(())
}

/// Starts Postgres via `pg_ctl start`, not by spawning `postgres.exe`
/// directly — confirmed necessary against a real MSIX install: `postgres`
/// refuses outright to run under a token with the Administrators group
/// enabled ("Execution of PostgreSQL by a user with administrative
/// permissions is not permitted"), which a GitHub Actions Windows runner
/// hits (its default account runs fully elevated, unlike a normal
/// UAC-filtered desktop session — a real user double-clicking this app's
/// Start Menu tile runs at the same medium integrity level our
/// `packagedClassicApp` manifest already declares, so this is expected to
/// be a CI-environment-specific wrinkle, not a real end-user blocker, but
/// `pg_ctl` is the actual documented fix either way). `pg_ctl start` on
/// Windows automatically detects an elevated token and re-launches
/// postgres under a restricted one (`CreateRestrictedToken` internally) —
/// this is Postgres's own built-in answer to exactly this situation, not
/// a workaround bolted on here. `-w` makes pg_ctl block until the server
/// is actually accepting connections (or the timeout elapses), which
/// folds in what a separate pg_isready poll would otherwise do.
fn start_postgres(paths: &AppPaths, port: u16) -> Result<(), StartupError> {
    let mut cmd = Command::new(paths.pg_bin("pg_ctl"));
    cmd.arg("start")
        .arg("-D").arg(&paths.pgdata_dir)
        .arg("-l").arg(&paths.pg_log_file)
        .arg("-w").arg("-t").arg("30")
        .arg("-o").arg(format!("-p {port} -h 127.0.0.1")) // never 0.0.0.0 — see the trust-auth note above
        // pg_ctl's OWN stdout/stderr, separate from `-l`'s target: pg_ctl
        // can fail before the server ever launches (bad argument, can't
        // find postgres.exe, the restricted-token dance itself failing),
        // in which case `-l`'s file stays empty and this is the only
        // place any diagnostic ends up.
        .stdout(append_log(&paths.pgctl_log_file))
        .stderr(append_log(&paths.pgctl_log_file));
    no_window(&mut cmd);
    let status = cmd.status().map_err(|e| StartupError::PostgresStart(e.to_string()))?;
    if !status.success() {
        return Err(StartupError::PostgresStart(format!("pg_ctl start exited with {status}")));
    }
    Ok(())
}

fn wait_for_http_ready(port: u16, server: &mut Child, timeout: Duration) -> Result<(), StartupError> {
    let deadline = Instant::now() + timeout;
    let url = format!("http://127.0.0.1:{port}/health/ready");
    loop {
        if let Ok(Some(status)) = server.try_wait() {
            return Err(StartupError::ServerExited(format!("exited with {status} before becoming healthy")));
        }
        if let Ok(resp) = ureq::get(&url).call() {
            if resp.status() == 200 {
                return Ok(());
            }
        }
        if Instant::now() >= deadline {
            return Err(StartupError::ServerUnhealthy);
        }
        std::thread::sleep(Duration::from_millis(300));
    }
}

fn write_runtime_info(paths: &AppPaths, http_port: u16, pg_port: u16, server_pid: u32) {
    let body = serde_json::json!({
        "http_port": http_port,
        "pg_port": pg_port,
        "server_pid": server_pid,
    });
    let tmp = paths.runtime_info_file.with_extension("json.tmp");
    if std::fs::write(&tmp, body.to_string()).is_ok() {
        let _ = std::fs::rename(&tmp, &paths.runtime_info_file);
    }
}

/// First run is meaningfully slower than subsequent ones (initdb, and a
/// full pass over all embedded migrations, vs. a near-instant no-op on
/// every run after) — give it a longer healthcheck budget rather than
/// making every launch pay for the worst case.
fn is_first_run(paths: &AppPaths) -> bool {
    !paths.pgdata_dir.join("PG_VERSION").exists()
}

pub fn start_backend(paths: &AppPaths) -> Result<RunningBackend, StartupError> {
    let first_run = is_first_run(paths);

    let pg_port = pick_free_port()?;
    let http_port = pick_free_port()?;

    ensure_writable_pg_install(paths)?;
    run_initdb(paths)?;
    start_postgres(paths, pg_port)?; // blocks until Postgres is actually ready (pg_ctl -w) or returns an error

    let aead_key = match secrets::load_or_generate_aead_key(paths) {
        Ok(key) => key,
        Err(e) => {
            let _ = pg_process_stop(paths);
            return Err(StartupError::ServerSpawn(e));
        }
    };

    let dsn = format!("postgres://rechvix@127.0.0.1:{pg_port}/postgres?sslmode=disable");
    let mut cmd = Command::new(paths.server_exe());
    cmd.env("DATABASE_DSN", &dsn)
        .env("DATABASE_AUTO_MIGRATE", "true")
        .env("HTTP_PORT", http_port.to_string())
        .env("WEB_DIST_DIR", paths.web_dist_dir())
        .env("SESSION_COOKIE_SECURE", "false") // plain HTTP on loopback only
        .env("ENABLE_BOOTSTRAP", "true") // safe to leave on: self-disables once an org exists
        .env("AEAD_ENCRYPTION_KEY", &aead_key)
        .env("LOG_LEVEL", "info")
        .stdout(append_log(&paths.server_log_file))
        .stderr(append_log(&paths.server_log_file));
    no_window(&mut cmd);
    let mut server_process = match cmd.spawn() {
        Ok(child) => child,
        Err(e) => {
            let _ = pg_process_stop(paths);
            return Err(StartupError::ServerSpawn(e.to_string()));
        }
    };

    // First run now also pays for copying the bundled Postgres into a
    // writable location (see ensure_writable_pg_install) on top of initdb
    // and the first migration pass — a wider budget than every run after.
    let healthcheck_budget = if first_run { Duration::from_secs(120) } else { Duration::from_secs(30) };
    if let Err(e) = wait_for_http_ready(http_port, &mut server_process, healthcheck_budget) {
        let _ = server_process.kill();
        let _ = server_process.wait();
        let _ = pg_process_stop(paths);
        return Err(e);
    }

    write_runtime_info(paths, http_port, pg_port, server_process.id());

    Ok(RunningBackend { server_process, http_port })
}

fn pg_process_stop(paths: &AppPaths) -> std::io::Result<std::process::ExitStatus> {
    let mut cmd = Command::new(paths.pg_bin("pg_ctl"));
    cmd.arg("stop").arg("-D").arg(&paths.pgdata_dir).arg("-m").arg("fast").arg("-t").arg("20");
    no_window(&mut cmd);
    cmd.status()
}

/// Windows has no real SIGTERM delivery to a child process — `Child::kill`
/// maps to `TerminateProcess`, so the Go server never gets its graceful
/// `server.Shutdown` path. Accepted as a v1 simplification: worst case is
/// one dropped in-flight HTTP response, not data loss, since every write
/// goes through a Postgres transaction — and `pg_ctl stop -m fast`
/// afterward still lets Postgres itself shut down cleanly (checkpoint,
/// release the data directory lock) rather than being hard-killed mid-write.
pub fn stop_backend(mut backend: RunningBackend, paths: &AppPaths) {
    let _ = backend.server_process.kill();
    let _ = backend.server_process.wait();
    let _ = pg_process_stop(paths);
}
