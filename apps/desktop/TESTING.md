# Testing the Rechvix desktop shell

`.github/workflows/desktop-msix.yml` runs the checklist below for real,
automatically, on a Windows GitHub Actions runner: it cross-compiles
`apps/server`, builds `apps/web`, downloads a portable Postgres, packages
everything into an MSIX, installs it, launches it, waits for the bundled
backend to report healthy, and scripts
minimize/maximize/single-instance/close/uninstall/reinstall through real
Win32 calls — throwing (failing the run) the instant any of those don't
hold. Check the Actions tab for the latest `Desktop MSIX` run before
trusting anything below by hand; `msix/test-install.ps1` is what it runs.

Everything below still runs on Windows and is useful for interactive
testing (the CI job can't click a title-bar button with a mouse), but the
install/uninstall/lifecycle behavior itself is no longer unverified —
see the note at the bottom for exactly what CI does and doesn't cover.

## What changed from a "thin client"

This app used to just ask "which server?" and open that URL. It now
bundles the real `apps/server` binary and a local Postgres, starts both as
child processes on launch, and points itself at `http://127.0.0.1:<port>`
— no server, no account, no internet connection needed. First launch
lands on the product's own "Set up your business" screen, same as any
fresh self-hosted install. See `src-tauri/src/backend.rs` for the startup
sequence and `src-tauri/src/paths.rs` for where everything lives on disk.

## Quick loop (no packaging, no signing)

`tauri dev` needs `rechvix-server.exe`, `web/`, and `pgsql/` sitting next
to wherever it puts the dev binary (`src-tauri/src/paths.rs` resolves
everything relative to the running exe's own directory — the same
convention `build-msix.ps1` uses for the packaged build). Fastest way to
get those in place for local iteration:

```powershell
# from the repo root
$env:CGO_ENABLED="0"; $env:GOOS="windows"; $env:GOARCH="amd64"
go build -o apps\desktop\src-tauri\target\debug\rechvix-server.exe .\apps\server
cd apps\web; npm ci; npm run build; cd ..\..
Copy-Item -Recurse apps\web\dist apps\desktop\src-tauri\target\debug\web
# extract a portable Postgres zip (see build-msix.ps1's $PostgresUrl) to
# apps\desktop\src-tauri\target\debug\pgsql

cd apps\desktop
npm install
npm run tauri dev
```

This runs the real app in a dev window. Confirm:

- [ ] Window shows "Starting Rechvix…" briefly, then the real app's login/setup screen appears — no blank window at any point
- [ ] On a genuinely fresh `pgdata` (delete `target\debug\pgdata` under Tauri's dev app-data dir first), the login screen shows a "Set up your business" link with no manual seeding
- [ ] Completing setup creates the org/owner and logs you in
- [ ] Closing and reopening the dev window skips straight past the loading screen to the login screen (not setup again) — Postgres data persisted
- [ ] Menu bar → Rechvix → "Reload" refreshes the current page without losing anything
- [ ] Menu bar → Rechvix → "Open Data Folder" opens the logs directory
- [ ] Menu bar → Rechvix → "Quit Rechvix" exits the process **and** its Postgres/server children (check Task Manager — no `desktop.exe`, `rechvix-server.exe`, or `postgres.exe` left running)
- [ ] Deliberately breaking startup (occupy a port, rename `pgdata` mid-run, delete `pgsql\bin\postgres.exe`) shows a specific, readable error in the window — never a blank screen — and "Try again" recovers once the problem is fixed

## Packaged (loose) install — no .msix needed yet

Registers the app from the built package folder directly, per Microsoft's
own guidance (fastest way to test install/uninstall behavior without
building or signing an actual .msix):

```powershell
cd apps\desktop\msix
.\build-msix.ps1          # cross-compiles the server, builds apps/web, fetches Postgres, assembles .\package, and packs Rechvix.msix — .\package itself is left behind too
Add-AppxPackage -Register .\package\AppxManifest.xml
```

- [ ] App appears in the Start Menu as "Rechvix" with the correct icon
- [ ] Launching from the Start Menu tile works identically to `tauri dev`
- [ ] First launch takes a few seconds longer (initdb + first migration pass) — still ends on the setup screen, not stuck on "Starting Rechvix…"
- [ ] **Minimize**: title bar minimize button drops it to the taskbar; clicking the taskbar icon restores it
- [ ] **Single instance**: with the app running, launch it again from the Start Menu — the *existing* window gets focus, no second `desktop.exe` process appears in Task Manager
- [ ] **Close**: title bar close button actually exits — confirm `desktop.exe`, `rechvix-server.exe`, and `postgres.exe` are all gone from Task Manager within a few seconds, not lingering
- [ ] **Uninstall**: Settings → Apps → Rechvix → Uninstall removes the Start Menu entry, the installed files, and the local database/secrets cleanly
- [ ] **Re-install after uninstall**: the setup screen appears again (genuine first-run state) — expected, not a bug: `pgdata`/the persisted encryption key live in this package's own per-package storage, which Windows deletes on uninstall along with everything else

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

1. Bump `Version` in `msix/AppxManifest.xml` (e.g. `0.2.0.0` → `0.2.1.0`).
2. Rebuild and reinstall: `Add-AppxPackage .\Rechvix.msix`.
3. Confirm: the local database from before the update is still there
   (per-package storage persists across an in-place upgrade, unlike an
   uninstall/reinstall) — the app should go straight to the login screen
   with existing data, not back to first-run setup, and any pending
   migrations added since the previous version should apply cleanly on
   this first post-update launch.

## What CI verifies, what it doesn't

`.github/workflows/desktop-msix.yml` (`windows-latest`) now actually:
cross-compiles `apps/server` for Windows, builds `apps/web`, downloads a
portable Postgres, compiles `src-tauri` for real
(`x86_64-pc-windows-msvc`), packages `Rechvix.msix` twice (unsigned Store
copy + self-signed sideload-test copy), and runs `msix/test-install.ps1`,
which installs it, launches it via its real Start Menu identity
(`shell:AppsFolder\...`), waits for the bundled backend to write
`runtime.json` and confirms `GET /health/ready` returns 200 and
`GET /api/v1/auth/bootstrap` reports `available: true` on the fresh
install, drives Win32 `ShowWindow`/`IsIconic`/`IsZoomed`/`CloseMainWindow`
to prove minimize, maximize, single-instance (a second launch focuses the
existing process instead of spawning one), and close (the process
actually exits, not just the window hiding) — then uninstalls, confirms
the install directory, the local data directory (pgdata/secrets), and
`Get-AppxPackage` entry are all gone, and reinstalls to confirm the
update/reset flow. Any of those failing fails the workflow run — check
the Actions tab before trusting a build.

What CI does **not** yet cover: an actual bootstrap → close → relaunch →
login-screen round trip proving real data persistence (the healthcheck
above proves the backend starts and is reachable, not that a created
account survives a restart) — a good next addition once the basics above
are proven stable. Also not covered, because it drives the window
programmatically rather than with a mouse: actually clicking the
title-bar buttons, visually confirming the icon/branding render
correctly, and the loading/error screen's real UX (exercised by hand —
see "Quick loop" above).
