# Rechvix desktop shell

A thin Tauri 2 window around your own rechvix server — see
`docs/architecture.md` §13 in the main repo. No tax/inventory/accounting
logic lives here; this app only knows how to show a "which server?" page
once, remember the answer, and open that URL in a native window from then
on. Everything real happens on the server, same as the web UI.

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

See `msix/` — `AppxManifest.xml`, `build-msix.ps1`, and `../TESTING.md`
for the full install/uninstall/lifecycle checklist. Requires Windows (the
Windows SDK's `makeappx.exe`/`signtool.exe`, plus Rust's
`x86_64-pc-windows-msvc` target) — this can't be built or tested from
this repo's Linux dev environment.

Before submitting to Partner Center:

1. Reserve the app name in [Partner Center](https://partner.microsoft.com/dashboard) and copy its exact `Identity.Name`/`Publisher` into `msix/AppxManifest.xml` (currently placeholder `REPLACE_ME` values).
2. Replace `src-tauri/icons/*` with rechvix's real logo — the current set is Tauri's generic scaffold icon.
3. Run through every item in `../TESTING.md`.
4. Build the final `.msix` with `msix/build-msix.ps1`, then follow Microsoft's own [manual upload-package guide](https://learn.microsoft.com/en-us/windows/msix/packaging/packaging-uwp-apps#create-your-app-package-upload-file-manually) to wrap it into the `.msixupload` Partner Center's submission form expects (`makeappx.exe` alone doesn't produce that wrapper format).
