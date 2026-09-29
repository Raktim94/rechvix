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
//     live under Tauri's own `app_local_data_dir()`. For a packaged
//     MSIX app specifically, Windows transparently redirects this into
//     `%LOCALAPPDATA%\Packages\<PackageFamilyName>\...` — the same
//     per-package storage `tauri-plugin-store` already relied on for
//     `settings.json`, which is why `Remove-AppxPackage` cleans it up
//     on uninstall with zero uninstall-specific code on our part.
use std::path::PathBuf;
use tauri::{AppHandle, Manager};

pub struct AppPaths {
    pub install_dir: PathBuf,
    pub pgdata_dir: PathBuf,
    pub secrets_dir: PathBuf,
    pub aead_key_file: PathBuf,
    pub logs_dir: PathBuf,
    pub server_log_file: PathBuf,
    pub pg_log_file: PathBuf,
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

        let data_dir = app
            .path()
            .app_local_data_dir()
            .map_err(|e| format!("could not resolve the local data directory: {e}"))?;

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
            install_dir,
            pgdata_dir,
            secrets_dir: secrets_dir.clone(),
            aead_key_file: secrets_dir.join("aead.key"),
            logs_dir: logs_dir.clone(),
            server_log_file: logs_dir.join("server.log"),
            pg_log_file: logs_dir.join("postgres.log"),
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

    pub fn pg_bin_dir(&self) -> PathBuf {
        self.install_dir.join("pgsql").join("bin")
    }

    pub fn pg_bin(&self, name: &str) -> PathBuf {
        self.pg_bin_dir().join(exe_name(name))
    }
}
