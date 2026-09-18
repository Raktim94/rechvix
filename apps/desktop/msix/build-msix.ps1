<#
.SYNOPSIS
  Builds the Rechvix desktop shell and packages it as an MSIX.

.DESCRIPTION
  Run this on Windows, from apps/desktop/msix, with:
    - Rust + the MSVC target installed (rustup default-host x86_64-pc-windows-msvc)
    - Node.js
    - The Windows SDK (for makeappx.exe / signtool.exe) — installed with
      Visual Studio's "Desktop development with C++" workload, or standalone
      from https://developer.microsoft.com/windows/downloads/windows-sdk/

  It does three things:
    1. `npm ci` + a release build of src-tauri (no NSIS/WiX installer, just
       the raw exe: MSIX wraps that directly, not an installer-of-an-installer).
    2. Assembles a package layout (exe + AppxManifest.xml + icons under
       Assets\) in .\package, mirroring what AppxManifest.xml expects.
    3. Runs makeappx.exe to produce Rechvix.msix in this folder.

  It does NOT sign the package. For local sideload testing, the fastest
  loop skips packaging entirely — see TESTING.md's "Quick loop" section,
  which registers the loose .\package folder directly via
  `Add-AppxPackage -Register`. Use -Sign (below) only when you actually
  need a real .msix file to hand to someone else or to Partner Center's
  submission validator.

.PARAMETER Sign
  After packaging, sign Rechvix.msix with a self-signed certificate
  (generated if one doesn't already exist at .\RechvixTestCert.pfx) so it
  can be installed on another machine without disabling signature checks.
  This is a LOCAL TEST certificate only — it does not survive Store
  submission. Partner Center signs the package you upload with its own
  certificate during certification; you never need your own signing
  certificate for the Store path itself, only for sideloading a .msix to
  a machine that isn't yours before you get that far.
#>
param(
  [switch]$Sign
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot   # apps/desktop
$desktopDir = $root
$msixDir = Join-Path $desktopDir "msix"
$packageDir = Join-Path $msixDir "package"
$assetsDir = Join-Path $packageDir "Assets"
$iconsDir = Join-Path $desktopDir "src-tauri\icons"

Write-Host "==> Installing frontend dependencies" -ForegroundColor Cyan
Push-Location $desktopDir
npm ci

Write-Host "==> Building the desktop shell (release, no installer bundle)" -ForegroundColor Cyan
# --no-bundle: we want just target/release/desktop.exe, not Tauri's own
# NSIS/WiX installer output — MSIX wraps the raw exe directly.
npx tauri build --no-bundle
Pop-Location

$exePath = Join-Path $desktopDir "src-tauri\target\release\desktop.exe"
if (-not (Test-Path $exePath)) {
  throw "Expected build output not found at $exePath — did the build above fail?"
}

Write-Host "==> Assembling package layout at $packageDir" -ForegroundColor Cyan
if (Test-Path $packageDir) { Remove-Item $packageDir -Recurse -Force }
New-Item -ItemType Directory -Path $packageDir | Out-Null
New-Item -ItemType Directory -Path $assetsDir | Out-Null

Copy-Item $exePath -Destination $packageDir
Copy-Item (Join-Path $msixDir "AppxManifest.xml") -Destination $packageDir

foreach ($icon in @(
  "StoreLogo.png",
  "Square44x44Logo.png",
  "Square150x150Logo.png",
  "Square71x71Logo.png"
)) {
  Copy-Item (Join-Path $iconsDir $icon) -Destination $assetsDir
}

# WebView2Loader.dll: only present if the build actually links it as a
# loose file rather than statically — copy it along if it exists next to
# the exe so the packaged app isn't missing it; harmless no-op otherwise.
$webviewLoader = Join-Path $desktopDir "src-tauri\target\release\WebView2Loader.dll"
if (Test-Path $webviewLoader) {
  Copy-Item $webviewLoader -Destination $packageDir
}

Write-Host "==> Locating makeappx.exe" -ForegroundColor Cyan
$makeappx = Get-ChildItem "C:\Program Files (x86)\Windows Kits\10\bin" -Recurse -Filter "makeappx.exe" -ErrorAction SilentlyContinue |
  Where-Object { $_.FullName -match "x64" } | Select-Object -First 1 -ExpandProperty FullName
if (-not $makeappx) {
  throw "makeappx.exe not found under Windows Kits. Install the Windows SDK (or Visual Studio's 'Desktop development with C++' workload) first."
}

$msixPath = Join-Path $msixDir "Rechvix.msix"
if (Test-Path $msixPath) { Remove-Item $msixPath -Force }

Write-Host "==> Packaging $msixPath" -ForegroundColor Cyan
& $makeappx pack /o /d $packageDir /p $msixPath
if ($LASTEXITCODE -ne 0) { throw "makeappx failed with exit code $LASTEXITCODE" }

if ($Sign) {
  $signtool = Get-ChildItem "C:\Program Files (x86)\Windows Kits\10\bin" -Recurse -Filter "signtool.exe" -ErrorAction SilentlyContinue |
    Where-Object { $_.FullName -match "x64" } | Select-Object -First 1 -ExpandProperty FullName
  if (-not $signtool) { throw "signtool.exe not found under Windows Kits." }

  $pfxPath = Join-Path $msixDir "RechvixTestCert.pfx"
  $subject = ([xml](Get-Content (Join-Path $msixDir "AppxManifest.xml"))).Package.Identity.Publisher
  if (-not (Test-Path $pfxPath)) {
    Write-Host "==> No test certificate found, generating one for Subject: $subject" -ForegroundColor Cyan
    $cert = New-SelfSignedCertificate -Type Custom -Subject $subject `
      -KeyUsage DigitalSignature -FriendlyName "Rechvix MSIX test cert" `
      -CertStoreLocation "Cert:\CurrentUser\My" `
      -TextExtension @("2.5.29.37={text}1.3.6.1.5.5.7.3.3", "2.5.29.19={text}")
    $pwd = ConvertTo-SecureString -String "rechvix-test" -Force -AsPlainText
    Export-PfxCertificate -Cert $cert -FilePath $pfxPath -Password $pwd | Out-Null
    Write-Host "    Test cert exported to $pfxPath (password: rechvix-test)." -ForegroundColor Yellow
    Write-Host "    Import it into 'Trusted People' on any machine you sideload this .msix to:" -ForegroundColor Yellow
    Write-Host "    Import-PfxCertificate -FilePath `"$pfxPath`" -CertStoreLocation Cert:\LocalMachine\TrustedPeople -Password (ConvertTo-SecureString rechvix-test -Force -AsPlainText)" -ForegroundColor Yellow
  }
  $pwd = ConvertTo-SecureString -String "rechvix-test" -Force -AsPlainText
  & $signtool sign /fd SHA256 /a /f $pfxPath /p "rechvix-test" $msixPath
  if ($LASTEXITCODE -ne 0) { throw "signtool failed with exit code $LASTEXITCODE" }
}

Write-Host "==> Done: $msixPath" -ForegroundColor Green
