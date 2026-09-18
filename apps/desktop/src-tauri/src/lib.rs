// Rechvix desktop shell.
//
// This is intentionally the *entire* app: a native window that either
// shows the first-run "which server?" page (bundled index.html) or, once
// a server URL has been saved, points straight at that server —
// http://localhost:8090 for a local install, or a remote/hosted URL. No
// tax/inventory/accounting/permission logic lives here; the app talks to
// the same rechvix Go server everyone else does (docs/architecture.md
// §13 in the main rechvix repo). Once the main window is showing the
// real rechvix UI, that page is just a normal website in the webview —
// it is never granted access to any Tauri API (see
// capabilities/default.json's `windows` scoping), so this shell adds no
// attack surface beyond "open this one window".

use tauri::menu::{MenuBuilder, SubmenuBuilder};
use tauri::{AppHandle, Manager, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_store::StoreExt;
use url::Url;

const STORE_FILE: &str = "settings.json";
const SERVER_URL_KEY: &str = "server_url";
const SETTINGS_WINDOW_LABEL: &str = "settings";

/// A saved server URL must be a real http(s) URL — anything else (a bare
/// hostname, a `file://` path, a typo) would otherwise get handed
/// straight to `WebviewWindow::navigate` and fail confusingly deep inside
/// the webview instead of with a clear message on the form that took it.
fn parse_server_url(raw: &str) -> Result<Url, String> {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return Err("Enter your rechvix server's address.".into());
    }
    let url = Url::parse(trimmed)
        .map_err(|_| "That doesn't look like a valid URL — e.g. https://rechvix.example.com or http://192.168.1.40:8090".to_string())?;
    match url.scheme() {
        "http" | "https" => Ok(url),
        other => Err(format!("Unsupported address scheme \"{other}:\" — use http:// or https://")),
    }
}

fn read_saved_server_url(app: &AppHandle) -> Option<Url> {
    let store = app.store(STORE_FILE).ok()?;
    let raw = store.get(SERVER_URL_KEY)?;
    let raw = raw.as_str()?;
    Url::parse(raw).ok()
}

/// Called from the bundled settings page (first run, or reopened later
/// via the "Change Server…" menu item) once the user submits an address.
/// Persists it, points the main window at it, and — if this call came
/// from the secondary settings window rather than the main one — closes
/// that secondary window, since its job is done.
#[tauri::command]
fn save_server_url(app: AppHandle, window: tauri::WebviewWindow, url: String) -> Result<(), String> {
    let parsed = parse_server_url(&url)?;

    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    store.set(SERVER_URL_KEY, serde_json::Value::String(parsed.to_string()));
    store.save().map_err(|e| e.to_string())?;

    let main = app.get_webview_window("main").ok_or("Main window is gone.")?;
    main.navigate(parsed).map_err(|e| e.to_string())?;
    main.show().map_err(|e| e.to_string())?;
    let _ = main.set_focus();

    if window.label() != "main" {
        let _ = window.close();
    }
    Ok(())
}

/// Prefills the settings page with whatever's already saved, so
/// reopening it via "Change Server…" doesn't present a blank form.
#[tauri::command]
fn get_server_url(app: AppHandle) -> Option<String> {
    read_saved_server_url(&app).map(|u| u.to_string())
}

/// Opens the settings page as its own small window, reused if it's
/// already open rather than stacking duplicates — this is the only way
/// back to "change server" once the main window has navigated away to
/// the real (external, un-privileged) rechvix site.
fn open_settings_window(app: &AppHandle) {
    if let Some(existing) = app.get_webview_window(SETTINGS_WINDOW_LABEL) {
        let _ = existing.show();
        let _ = existing.set_focus();
        return;
    }
    let _ = WebviewWindowBuilder::new(app, SETTINGS_WINDOW_LABEL, WebviewUrl::App("index.html".into()))
        .title("Rechvix — Server settings")
        .inner_size(480.0, 360.0)
        .resizable(false)
        .center()
        .build();
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let mut builder = tauri::Builder::default();

    // A second launch attempt (Start Menu tile double-clicked while
    // already running) focuses the running window instead of opening a
    // second process pointed at the same server.
    #[cfg(desktop)]
    {
        builder = builder.plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(window) = app
                .get_webview_window("main")
                .or_else(|| app.get_webview_window(SETTINGS_WINDOW_LABEL))
            {
                let _ = window.unminimize();
                let _ = window.show();
                let _ = window.set_focus();
            }
        }));
    }

    builder
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_store::Builder::new().build())
        .invoke_handler(tauri::generate_handler![save_server_url, get_server_url])
        .setup(|app| {
            let app_menu = SubmenuBuilder::new(app, "Rechvix")
                .text("change_server", "Change Server…")
                .text("reload", "Reload")
                .separator()
                .text("quit", "Quit Rechvix")
                .build()?;
            let menu = MenuBuilder::new(app).items(&[&app_menu]).build()?;
            app.set_menu(menu)?;

            let handle = app.handle().clone();
            app.on_menu_event(move |app_handle, event| match event.id().0.as_str() {
                "change_server" => open_settings_window(app_handle),
                "reload" => {
                    if let Some(window) = app_handle.get_webview_window("main") {
                        let _ = window.eval("window.location.reload()");
                    }
                }
                "quit" => app_handle.exit(0),
                _ => {}
            });

            // First run (no saved URL yet): the main window's static
            // config already points at the bundled settings page, so
            // there's nothing to navigate — just show it. Once a server
            // is already known, skip the settings page entirely and go
            // straight to it, so a returning user never sees it flash by.
            let main = handle.get_webview_window("main").expect("main window must exist");
            if let Some(saved) = read_saved_server_url(&handle) {
                main.navigate(saved)?;
            }
            main.show()?;
            main.set_focus()?;

            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
