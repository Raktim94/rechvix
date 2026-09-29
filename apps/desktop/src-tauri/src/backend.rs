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
    pg_process: Child,
    server_process: Child,
    pub http_port: u16,
}

#[derive(Debug)]
pub enum StartupError {
    PortUnavailable(String),
    Initdb(String),
    PostgresStart(String),
    PostgresUnready,
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
                "Rechvix's local database didn't start ({e}). Details: {logs}"
            ),
            StartupError::PostgresUnready => format!(
                "Rechvix's local database is taking longer than expected to start. This can happen on first launch or on a slow disk — try waiting a moment and reopening Rechvix. Details: {logs}"
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

fn start_postgres(paths: &AppPaths, port: u16) -> Result<Child, StartupError> {
    let mut cmd = Command::new(paths.pg_bin("postgres"));
    cmd.arg("-D").arg(&paths.pgdata_dir)
        .arg("-p").arg(port.to_string())
        .arg("-h").arg("127.0.0.1") // never 0.0.0.0 — see the trust-auth note above
        .arg("-c").arg("logging_collector=off")
        .stdout(append_log(&paths.pg_log_file))
        .stderr(append_log(&paths.pg_log_file));
    no_window(&mut cmd);
    cmd.spawn().map_err(|e| StartupError::PostgresStart(e.to_string()))
}

fn wait_for_postgres_ready(paths: &AppPaths, port: u16, timeout: Duration) -> Result<(), StartupError> {
    let deadline = Instant::now() + timeout;
    loop {
        let mut cmd = Command::new(paths.pg_bin("pg_isready"));
        cmd.arg("-h").arg("127.0.0.1").arg("-p").arg(port.to_string());
        no_window(&mut cmd);
        if let Ok(status) = cmd.status() {
            if status.success() {
                return Ok(());
            }
        }
        if Instant::now() >= deadline {
            return Err(StartupError::PostgresUnready);
        }
        std::thread::sleep(Duration::from_millis(300));
    }
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

    run_initdb(paths)?;
    let mut pg_process = start_postgres(paths, pg_port)?;
    if let Err(e) = wait_for_postgres_ready(paths, pg_port, Duration::from_secs(20)) {
        let _ = pg_process.kill();
        let _ = pg_process.wait();
        return Err(e);
    }

    let aead_key = match secrets::load_or_generate_aead_key(paths) {
        Ok(key) => key,
        Err(e) => {
            let _ = pg_process_stop(paths);
            let _ = pg_process.wait();
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
            let _ = pg_process.wait();
            return Err(StartupError::ServerSpawn(e.to_string()));
        }
    };

    let healthcheck_budget = if first_run { Duration::from_secs(60) } else { Duration::from_secs(30) };
    if let Err(e) = wait_for_http_ready(http_port, &mut server_process, healthcheck_budget) {
        let _ = server_process.kill();
        let _ = server_process.wait();
        let _ = pg_process_stop(paths);
        let _ = pg_process.wait();
        return Err(e);
    }

    write_runtime_info(paths, http_port, pg_port, server_process.id());

    Ok(RunningBackend { pg_process, server_process, http_port })
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
    let _ = backend.pg_process.wait();
}
