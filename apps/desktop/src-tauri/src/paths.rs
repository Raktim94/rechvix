// Resolves every on-disk location the bundled backend needs, split into
// two families with very different lifetimes:
//
//   - "install" paths (server exe, web assets, Postgres binaries) live
//     next to `desktop.exe` itself. build-msix.ps1 copies them there by
//     hand — MSIX packaging bypasses Tauri's own bundler entirely (it
//     runs `tauri build --no-bundle` and assembles the package itself),
//     so there is no `externalBin`/`resources` indirection to rely on;
//     "wherever this exe is" is the one path that's true in every case
//     (packaged MSIX, or a loose `.\package` folder registered directly
//     per TESTING.md's "Quick loop").
//
//   - "data" paths (Postgres's actual data directory, the persisted
//     encryption key, logs, the runtime-info file test tooling reads)
//     resolve to the REAL, already-redirected location on disk —
//     `%LOCALAPPDATA%\Packages\<PackageFamilyName>\LocalCache\Local\<bundle
//     identifier>\...` — computed explicitly (see `real_local_data_dir`),
//     NOT via Tauri's `app_local_data_dir()`. That distinction matters:
//     Tauri's resolver returns the *logical*, pre-redirection path
//     (`%LOCALAPPDATA%\<bundle-id>\...`), which the OS transparently maps
//     to the real container location for any *normal* child process in
//     this package's process tree — confirmed working for initdb, which
//     is spawned that way. But `pg_ctl start` on Windows always relaunches
//     postgres via `CreateProcessAsUser` with an explicitly constructed
//     restricted token (see `backend.rs::start_postgres`'s doc comment) —
//     and that restricted grandchild does NOT get the same transparent
//     redirection, so it sees the literal, pre-redirection path string and
//     finds nothing there ("The system cannot find the path specified" —
//     confirmed against a real MSIX install: this exact failure, gone the
//     moment every path handed to Postgres became the real, concrete one
//     instead of relying on redirection to hold for a process it doesn't
//     cover). Resolving the real path ourselves, once, sidesteps needing
//     redirection to work uniformly across every process in play. It's
//     still the genuine per-package storage location either way, so
//     `Remove-AppxPackage` still removes all of it on uninstall — nothing
//     about that guarantee changes.
use std::path::PathBuf;
use tauri::{AppHandle, Manager};

/// Must match `AppxManifest.xml`'s `Identity.Name`/`Publisher`-derived
/// Package Family Name (Partner Center's reserved value, Store ID
/// 9NMPSP7CR5RW) and `tauri.conf.json`'s `identifier`. Both are fixed,
/// already-reserved values, not expected to change.
#[cfg(windows)]
const PACKAGE_FAMILY_NAME: &str = "NODEDRINFOTECHLIMITED.Rechvix_wsh4jzg5a6682";
#[cfg(windows)]
const BUNDLE_IDENTIFIER: &str = "com.nodedr.rechvix";

/// Returns the real, concrete, already-redirected data directory — see
/// this module's header comment for why this can't just be
/// `app.path().app_local_data_dir()`. Falls back to Tauri's resolver
/// when the package container doesn't exist (a bare `tauri dev` run,
/// which isn't packaged at all — `TESTING.md`'s "Quick loop"), and on
/// non-Windows targets, where none of this applies.
#[cfg(windows)]
fn real_local_data_dir(app: &AppHandle) -> Result<PathBuf, String> {
    if let Ok(local_app_data) = std::env::var("LOCALAPPDATA") {
        let package_root = PathBuf::from(local_app_data).join("Packages").join(PACKAGE_FAMILY_NAME);
        if package_root.exists() {
            return Ok(package_root.join("LocalCache").join("Local").join(BUNDLE_IDENTIFIER));
        }
    }
    app.path().app_local_data_dir().map_err(|e| format!("could not resolve the local data directory: {e}"))
}
#[cfg(not(windows))]
fn real_local_data_dir(app: &AppHandle) -> Result<PathBuf, String> {
    app.path().app_local_data_dir().map_err(|e| format!("could not resolve the local data directory: {e}"))
}

pub struct AppPaths {
    pub install_dir: PathBuf,
    /// A plain, writable directory every spawned Command explicitly uses
    /// as its working directory — see `start_backend`'s use of it via
    /// `Command::current_dir`. Never rely on inheriting our own process's
    /// CWD for a child that itself launches a further child (pg_ctl
    /// relaunching postgres under a restricted token): a packaged app's
    /// own default CWD can resolve somewhere under the ACL-locked
    /// `install_dir`/WindowsApps tree, and a restricted token that can't
    /// traverse into it makes `CreateProcess` fail with "The system
    /// cannot find the path specified" — confirmed against a real MSIX
    /// install, this exact error, once `pg_ctl` tried to re-launch
    /// `postgres.exe`.
    pub data_dir: PathBuf,
    pub pg_install_source_dir: PathBuf,
    pub pg_install_dir: PathBuf,
    pub pgdata_dir: PathBuf,
    pub secrets_dir: PathBuf,
    pub aead_key_file: PathBuf,
    pub logs_dir: PathBuf,
    pub server_log_file: PathBuf,
    pub pg_log_file: PathBuf,
    /// `pg_ctl`'s own stdout/stderr — separate from `pg_log_file` (which
    /// `-l` points at for the actual Postgres server's log): pg_ctl can
    /// fail before ever launching the server (e.g. a bad argument), in
    /// which case nothing lands in `-l`'s file at all and this is the
    /// only place to look.
    pub pgctl_log_file: PathBuf,
    pub runtime_info_file: PathBuf,
    /// Written (and cleared on the next successful start) whenever
    /// `start_backend` fails — a stable, documented location external
    /// tooling (or a support request) can point at, separate from the
    /// in-window error message which only exists inside a live webview.
    pub startup_error_file: PathBuf,
}

fn exe_name(base: &str) -> String {
    if cfg!(windows) {
        format!("{base}.exe")
    } else {
        base.to_string()
    }
}

impl AppPaths {
    pub fn resolve(app: &AppHandle) -> Result<Self, String> {
        let install_dir = std::env::current_exe()
            .map_err(|e| format!("could not resolve the app's own install directory: {e}"))?
            .parent()
            .ok_or("the app's exe path has no parent directory")?
            .to_path_buf();

        let data_dir = real_local_data_dir(app)?;

        let pgdata_dir = data_dir.join("pgdata");
        let secrets_dir = data_dir.join("secrets");
        let logs_dir = data_dir.join("logs");

        for dir in [&data_dir, &secrets_dir, &logs_dir] {
            std::fs::create_dir_all(dir)
                .map_err(|e| format!("could not create {}: {e}", dir.display()))?;
        }
        // pgdata_dir itself is created by `initdb`, not here — initdb
        // refuses to run against a directory that already exists but
        // isn't its own (empty is fine; leave that check to initdb).

        Ok(Self {
            pg_install_source_dir: install_dir.join("pgsql"),
            pg_install_dir: data_dir.join("pgsql"),
            data_dir: data_dir.clone(),
            install_dir,
            pgdata_dir,
            secrets_dir: secrets_dir.clone(),
            aead_key_file: secrets_dir.join("aead.key"),
            logs_dir: logs_dir.clone(),
            server_log_file: logs_dir.join("server.log"),
            pg_log_file: logs_dir.join("postgres.log"),
            pgctl_log_file: logs_dir.join("pgctl.log"),
            runtime_info_file: data_dir.join("runtime.json"),
            startup_error_file: data_dir.join("startup-error.txt"),
        })
    }

    pub fn server_exe(&self) -> PathBuf {
        self.install_dir.join(exe_name("rechvix-server"))
    }

    pub fn web_dist_dir(&self) -> PathBuf {
        self.install_dir.join("web")
    }

    /// Postgres must run from a writable, ordinary directory, never from
    /// the packaged install directory — the app's own exe and the Go
    /// server run fine directly out of `install_dir` (a plain one-level
    /// child spawn from an already-packaged process), but Postgres needs
    /// more than that: `initdb` re-execs `postgres -V` as a further child
    /// to sanity-check it, and the running server re-execs itself for
    /// every new backend process on Windows (it has no fork()) — both are
    /// a *second* level of process creation, which fails with "Access is
    /// denied" from inside `C:\Program Files\WindowsApps\...`'s locked-down
    /// ACLs (confirmed against a real MSIX install, not theoretical).
    /// `start_backend` copies `pg_install_source_dir` here once, on first
    /// run, before ever touching Postgres.
    pub fn pg_bin_dir(&self) -> PathBuf {
        self.pg_install_dir.join("bin")
    }

    pub fn pg_bin(&self, name: &str) -> PathBuf {
        self.pg_bin_dir().join(exe_name(name))
    }
}
