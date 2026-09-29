# Rechvix desktop shell

A self-contained Tauri 2 app: it bundles the real `apps/server` binary and
a local Postgres, starts both as child processes on launch (see
`src-tauri/src/backend.rs`), and points its window at the server it just
started — see `docs/architecture.md` §13 in the main repo. No
tax/inventory/accounting logic lives here in Rust or JS; that's all in the
Go server, exactly the same as every other rechvix deployment. The only
difference from a normal self-hosted install is *where* that server runs
— bundled and local, so the app works fully offline with zero setup, and
first launch lands on the product's own account-creation ("Set up your
business") screen instead of asking "which server?".

## Develop

```bash
npm install
npm run tauri dev
```

## Build

```bash
npm run tauri build          # produces an NSIS/WiX installer (Windows), a .dmg (macOS), or an AppImage/.deb (Linux)
```

## Microsoft Store (MSIX)

See `msix/` — `AppxManifest.xml`, `build-msix.ps1`, `test-install.ps1`,
and `../TESTING.md` for the full install/uninstall/lifecycle checklist.
Building and packaging needs Windows (the Windows SDK's
`makeappx.exe`/`signtool.exe`, Go 1.27.1 to cross-compile `apps/server`,
plus Rust's `x86_64-pc-windows-msvc` target), which this repo's Linux dev
environment doesn't have — so `.github/workflows/desktop-msix.yml` does
the actual build (server + web + a portable Postgres, then the shell
itself), packaging, and install/open/minimize/maximize/single-instance/
close/uninstall testing, plus a healthcheck against the bundled backend,
on a real `windows-latest` GitHub Actions runner on every push that
touches `apps/desktop/**`, `apps/server/**`, or `apps/web/**`, and on
demand via `workflow_dispatch`. It uploads both an unsigned
Store-submission `.msix` and a signed sideload-test `.msix` + test
certificate as workflow artifacts. Expect ~150-250MB, dominated by the
bundled Postgres — that's correct for a genuinely self-contained offline
database app, not a regression from earlier, much smaller thin-client
builds.

`src-tauri/icons/*` is Rechvix's real logo mark (cropped from
`rechvix.nodedr.com/public/brand/logo-square.webp`, background removed),
not Tauri's generic scaffold icon.

The app name is reserved in Partner Center (Store ID `9NMPSP7CR5RW`, Package Family Name `NODEDRINFOTECHLIMITED.Rechvix_wsh4jzg5a6682`) and `msix/AppxManifest.xml`'s `Identity.Name`/`Publisher` already match it exactly.

Before submitting to Partner Center:

0. This app no longer needs a test account or a reachable server for
   certification — since v0.2.0.0 it bundles its own backend and database
   and lands the reviewer straight on the account-creation screen. If a
   past submission was rejected under policy 10.3.1 ("App Is Testable")
   for lacking test credentials, that's now moot; no certification notes
   are required for this reason anymore.
1. Download the `rechvix-desktop-msix` artifact from the latest successful run of `desktop-msix.yml` (Actions tab) — confirms the build actually compiled and passed the install/uninstall/lifecycle/backend-healthcheck test on real Windows before you ever touch it.
2. Take `Rechvix-StoreSubmission.msix` from that artifact and follow Microsoft's own [manual upload-package guide](https://learn.microsoft.com/en-us/windows/msix/packaging/packaging-uwp-apps#create-your-app-package-upload-file-manually) to wrap it into the `.msixupload` Partner Center's submission form expects (`makeappx.exe` alone doesn't produce that wrapper format).
