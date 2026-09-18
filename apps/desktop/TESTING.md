# Testing the Rechvix desktop shell

Everything below runs on Windows — this app was written without a Windows
machine available to build or run it on (see the note at the bottom).
Nothing here has actually been executed; treat this as the checklist to
run through once, not a report of results.

## Quick loop (no packaging, no signing)

Fastest way to iterate on the app itself:

```powershell
cd apps\desktop
npm install
npm run tauri dev
```

This runs the real app in a dev window. Confirm:

- [ ] First run shows the "Connect to your rechvix server" page (no saved URL yet)
- [ ] Entering a bad value (`not a url`, `ftp://x`, empty) shows the inline error and does **not** navigate away
- [ ] Entering a real rechvix URL (e.g. `http://localhost:8090` against a running `apps/server`, or `https://rechvix.nodedr.com`) connects and shows the actual app
- [ ] Closing and reopening the dev window goes **straight** to the app — no settings-page flash
- [ ] Menu bar → Rechvix → "Change Server…" opens a small second window, prefilled with the current URL; saving a different one re-points the main window
- [ ] Menu bar → Rechvix → "Reload" refreshes the current page without losing the saved server URL
- [ ] Menu bar → Rechvix → "Quit Rechvix" exits the process (check Task Manager — no `desktop.exe` left running)

## Packaged (loose) install — no .msix needed yet

Registers the app from the built package folder directly, per Microsoft's
own guidance (fastest way to test install/uninstall behavior without
building or signing an actual .msix):

```powershell
cd apps\desktop\msix
.\build-msix.ps1          # builds the release exe, assembles .\package, and packs Rechvix.msix — .\package itself is left behind too
Add-AppxPackage -Register .\package\AppxManifest.xml
```

- [ ] App appears in the Start Menu as "Rechvix" with the correct icon
- [ ] Launching from the Start Menu tile works identically to `tauri dev`
- [ ] **Minimize**: title bar minimize button drops it to the taskbar; clicking the taskbar icon restores it
- [ ] **Single instance**: with the app running, launch it again from the Start Menu — the *existing* window gets focus, no second `desktop.exe` process appears in Task Manager
- [ ] **Close**: title bar close button actually exits — confirm `desktop.exe` is gone from Task Manager within a second or two, not lingering
- [ ] **Uninstall**: Settings → Apps → Rechvix → Uninstall removes the Start Menu entry and the installed files cleanly
- [ ] **Re-install after uninstall**: the settings page appears again (first-run state) — this is expected, not a bug: `tauri-plugin-store`'s `settings.json` lives in this package's own per-package storage, which Windows deletes on uninstall along with everything else

## Full .msix package

```powershell
cd apps\desktop\msix
.\build-msix.ps1
Add-AppxPackage .\Rechvix.msix
```

Without `-Sign`, this only installs on the machine it was built on (the
build machine's own cert store trusts locally-generated packages for
`Add-AppxPackage` in developer mode). To test on a *different* machine:

```powershell
.\build-msix.ps1 -Sign
# then on the target machine, import the printed test certificate into
# Trusted People (command is printed by the script) before installing
Add-AppxPackage .\Rechvix.msix
```

Repeat the same minimize/single-instance/close/uninstall checklist above
against this real .msix install.

## Update flow

1. Bump `Version` in `msix/AppxManifest.xml` (e.g. `0.1.0.0` → `0.1.1.0`).
2. Rebuild and reinstall: `Add-AppxPackage .\Rechvix.msix`.
3. Confirm: the saved server URL from before the update is still there
   (per-package storage persists across an in-place upgrade, unlike an
   uninstall/reinstall) — the app should go straight to the connected
   server, not back to the settings page.

## What wasn't verified here

This shell's Rust code (`src-tauri/src/lib.rs`) was written against
Tauri v2's and Microsoft's own current documentation (fetched live, not
from memory) for every API used — `WebviewWindow::navigate`,
`WebviewWindowBuilder`, the menu APIs, `tauri-plugin-store`, and
`tauri-plugin-single-instance` — and the frontend (`index.html`,
`src/main.ts`) was built and typechecked for real (`npm run build`,
zero errors). Cargo dependency resolution for every crate (including
both plugins) was also verified for real on Linux, with no version
conflicts.

What was **not** verified: an actual compile of `src-tauri`'s own Rust
code. The Linux machine this was built on has no GTK/WebKitGTK system
libraries installed (and no root access to install them), which Tauri's
Linux backend requires just to run `cargo check` — an unrelated
requirement to the Windows build, which uses WebView2 instead, but it
meant `cargo check`/`cargo build` never actually ran against this code.
Run `npm run tauri dev` (first item above) before anything else — if
there's a real compile error in `lib.rs`, that's where it will surface,
immediately and cheaply, before any packaging work.
