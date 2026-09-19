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

See `msix/` — `AppxManifest.xml`, `build-msix.ps1`, `test-install.ps1`,
and `../TESTING.md` for the full install/uninstall/lifecycle checklist.
Building and packaging needs Windows (the Windows SDK's
`makeappx.exe`/`signtool.exe`, plus Rust's `x86_64-pc-windows-msvc`
target), which this repo's Linux dev environment doesn't have — so
`.github/workflows/desktop-msix.yml` does the actual build, packaging,
and install/open/minimize/maximize/single-instance/close/uninstall
testing on a real `windows-latest` GitHub Actions runner on every push
that touches `apps/desktop/**`, and on demand via `workflow_dispatch`.
It uploads both an unsigned Store-submission `.msix` and a signed
sideload-test `.msix` + test certificate as workflow artifacts.

`src-tauri/icons/*` is Rechvix's real logo mark (cropped from
`rechvix.nodedr.com/public/brand/logo-square.webp`, background removed),
not Tauri's generic scaffold icon.

The app name is reserved in Partner Center (Store ID `9NMPSP7CR5RW`, Package Family Name `NODEDRINFOTECHLIMITED.Rechvix_wsh4jzg5a6682`) and `msix/AppxManifest.xml`'s `Identity.Name`/`Publisher` already match it exactly.

Before submitting to Partner Center:

1. Download the `rechvix-desktop-msix` artifact from the latest successful run of `desktop-msix.yml` (Actions tab) — confirms the build actually compiled and passed the install/uninstall/lifecycle test on real Windows before you ever touch it.
2. Take `Rechvix-StoreSubmission.msix` from that artifact and follow Microsoft's own [manual upload-package guide](https://learn.microsoft.com/en-us/windows/msix/packaging/packaging-uwp-apps#create-your-app-package-upload-file-manually) to wrap it into the `.msixupload` Partner Center's submission form expects (`makeappx.exe` alone doesn't produce that wrapper format).
