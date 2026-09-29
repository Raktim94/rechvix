// Rechvix desktop shell.
//
// This bundles the real backend: on launch it starts a local Postgres
// instance and the rechvix-server binary as child processes (both on
// 127.0.0.1 only — see backend.rs), then points the main window at the
// server it just started. No tax/inventory/accounting/permission logic
// lives here in Rust or JS — that's all in the Go server, same as every
// other rechvix deployment (docs/architecture.md §13 in the main repo).
// The difference from a normal self-hosted install is only *where* that
// server runs: bundled and local instead of something you set up
// yourself, so the app works offline with zero setup, and first launch
// lands on the product's own account-creation ("bootstrap") screen
// instead of asking "which server?".
//
// build-msix.ps1 is what actually assembles the pieces this code expects
// to find next to its own exe (rechvix-server.exe, web/, pgsql/) — see
// paths.rs's module comment for why "next to this exe" rather than
// Tauri's own externalBin/resources bundler mechanism, which MSIX
// packaging bypasses entirely.

mod backend;
mod paths;
mod secrets;

use backend::RunningBackend;
use paths::AppPaths;
use std::sync::Mutex;
use tauri::menu::{MenuBuilder, SubmenuBuilder};
use tauri::{AppHandle, Manager};
use tauri_plugin_opener::OpenerExt;
use url::Url;

struct BackendState(Mutex<Option<RunningBackend>>);

/// Runs the full startup sequence and either navigates the main window to
/// the now-running local server, or shows the failure in the loading
/// window itself — there's no settings-page fallback anymore, so every
/// failure has to be legible right here. Shared between the initial
/// launch (`.setup()`) and the "Try again" button's `retry_startup`
/// command, since both need to do exactly the same thing.
fn attempt_startup(app: AppHandle) {
    let paths = match AppPaths::resolve(&app) {
        Ok(p) => p,
        Err(e) => {
            show_error(&app, &e);
            return;
        }
    };

    match backend::start_backend(&paths) {
        Ok(running) => {
            let _ = std::fs::remove_file(&paths.startup_error_file); // clear any stale failure from a previous attempt
            let port = running.http_port;
            if let Some(state) = app.try_state::<BackendState>() {
                *state.0.lock().unwrap() = Some(running);
            }
            if let Some(window) = app.get_webview_window("main") {
                if let Ok(url) = Url::parse(&format!("http://127.0.0.1:{port}/")) {
                    let _ = window.navigate(url);
                }
            }
        }
        Err(e) => {
            let message = e.user_message(&paths);
            // Written unconditionally, separate from the in-window JS
            // eval below — this is what makes a failed launch debuggable
            // from the outside (CI, or a user who can't get a screenshot
            // of the error text out): a stable, documented file path
            // rather than only a message drawn into a webview.
            let _ = std::fs::write(&paths.startup_error_file, &message);
            show_error(&app, &message);
        }
    }
}

fn show_error(app: &AppHandle, message: &str) {
    if let Some(window) = app.get_webview_window("main") {
        if let Ok(payload) = serde_json::to_string(message) {
            let _ = window.eval(&format!("window.showStartupError({payload})"));
        }
    }
}

/// Re-run by the loading page's "Try again" button. Any child processes
/// from a failed attempt are already cleaned up inside `start_backend`
/// itself before it returns an error, so there's nothing to tear down
/// here first.
#[tauri::command]
fn retry_startup(app: AppHandle) {
    attempt_startup(app);
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let mut builder = tauri::Builder::default();

    // A second launch attempt (Start Menu tile double-clicked while
    // already running) focuses the running window instead of starting a
    // second local server on top of the first.
    #[cfg(desktop)]
    {
        builder = builder.plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.unminimize();
                let _ = window.show();
                let _ = window.set_focus();
            }
        }));
    }

    let app = builder
        .plugin(tauri_plugin_opener::init())
        .manage(BackendState(Mutex::new(None)))
        .invoke_handler(tauri::generate_handler![retry_startup])
        .setup(|app| {
            let app_menu = SubmenuBuilder::new(app, "Rechvix")
                .text("reload", "Reload")
                .separator()
                .text("open_data_folder", "Open Data Folder")
                .separator()
                .text("quit", "Quit Rechvix")
                .build()?;
            let menu = MenuBuilder::new(app).items(&[&app_menu]).build()?;
            app.set_menu(menu)?;

            app.on_menu_event(move |app_handle, event| match event.id().0.as_str() {
                "reload" => {
                    if let Some(window) = app_handle.get_webview_window("main") {
                        let _ = window.eval("window.location.reload()");
                    }
                }
                "open_data_folder" => {
                    if let Ok(paths) = AppPaths::resolve(app_handle) {
                        let _ = app_handle
                            .opener()
                            .open_path(paths.logs_dir.to_string_lossy(), None::<&str>);
                    }
                }
                "quit" => app_handle.exit(0),
                _ => {}
            });

            let main = app.get_webview_window("main").expect("main window must exist");
            main.show()?;
            main.set_focus()?;

            let handle = app.handle().clone();
            tauri::async_runtime::spawn_blocking(move || attempt_startup(handle));

            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building tauri application");

    app.run(|app_handle, event| {
        if let tauri::RunEvent::ExitRequested { .. } | tauri::RunEvent::Exit = event {
            if let Some(state) = app_handle.try_state::<BackendState>() {
                if let Some(running) = state.0.lock().unwrap().take() {
                    if let Ok(paths) = AppPaths::resolve(app_handle) {
                        backend::stop_backend(running, &paths);
                    }
                }
            }
        }
    });
}
