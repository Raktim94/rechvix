<#
.SYNOPSIS
  Builds the Rechvix desktop shell and packages it as an MSIX.

.DESCRIPTION
  Run this on Windows, from apps/desktop/msix, with:
    - Rust + the MSVC target installed (rustup default-host x86_64-pc-windows-msvc)
    - Node.js
    - Go 1.27.1 (must match go.mod at the repo root — the bundled server is
      cross-compiled from the exact same source everyone else runs)
    - The Windows SDK (for makeappx.exe / signtool.exe) — installed with
      Visual Studio's "Desktop development with C++" workload, or standalone
      from https://developer.microsoft.com/windows/downloads/windows-sdk/
    - Internet access (this script only downloads the portable Postgres
      binaries used to BUILD the package — the resulting .msix itself needs
      no network access to install or run; see PostgresVersion below)

  It does five things:
    1. Cross-compiles the real Go server (CGO_ENABLED=0 GOOS=windows
       GOARCH=amd64) — the actual backend, not a stub.
    2. Builds apps/web's production SPA.
    3. Downloads (or reuses a cached copy of) a portable Postgres for
       Windows — the app's own local database, bundled, not something the
       user or a Microsoft reviewer needs to install separately.
    4. `npm ci` + a release build of src-tauri (no NSIS/WiX installer, just
       the raw exe: MSIX wraps that directly, not an installer-of-an-installer).
    5. Assembles a package layout (desktop.exe + AppxManifest.xml + icons +
       rechvix-server.exe + web\ + pgsql\) in .\package, mirroring what
       AppxManifest.xml expects and what src-tauri/src/paths.rs resolves at
       runtime (everything as a plain sibling of desktop.exe — MSIX
       packaging bypasses Tauri's own externalBin/resources bundler
       entirely, so this script assembles the layout by hand), then runs
       makeappx.exe to produce Rechvix.msix in this folder.

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
$repoRoot = Split-Path -Parent (Split-Path -Parent $root)
$desktopDir = $root
$webDir = Join-Path (Split-Path -Parent $desktopDir) "web"
$msixDir = Join-Path $desktopDir "msix"
$packageDir = Join-Path $msixDir "package"
$assetsDir = Join-Path $packageDir "Assets"
$iconsDir = Join-Path $desktopDir "src-tauri\icons"

# Pinned to match deploy/docker/server.Dockerfile's postgres:18 (used
# there for pg_dump/pg_restore compatibility) — keep these in sync if
# either ever bumps. Bump the `key:` suffix in
# .github/workflows/desktop-msix.yml's actions/cache step whenever this
# changes, so a stale cached zip is never silently reused.
$PostgresVersion = "18.0-1"
$PostgresZipName = "postgresql-$PostgresVersion-windows-x64-binaries.zip"
$PostgresUrl = "https://get.enterprisedb.com/postgresql/$PostgresZipName"

Write-Host "==> Cross-compiling the Go server for Windows (go.mod pins 1.27.1)" -ForegroundColor Cyan
$goVersionOutput = & go version
if ($LASTEXITCODE -ne 0) { throw "Go toolchain not found — install Go 1.27.1 first." }
Write-Host "    $goVersionOutput"
$serverExe = Join-Path $desktopDir "rechvix-server.exe"
Push-Location $repoRoot
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -trimpath -ldflags="-s -w" -o $serverExe ./apps/server
if ($LASTEXITCODE -ne 0) { throw "go build failed with exit code $LASTEXITCODE" }
Remove-Item Env:\CGO_ENABLED, Env:\GOOS, Env:\GOARCH -ErrorAction SilentlyContinue
Pop-Location
if (-not (Test-Path $serverExe)) { throw "Expected build output not found at $serverExe" }

Write-Host "==> Building apps/web's production SPA" -ForegroundColor Cyan
Push-Location $webDir
npm ci
npm run build
Pop-Location
$webDistDir = Join-Path $webDir "dist"
if (-not (Test-Path $webDistDir)) { throw "Expected apps/web build output not found at $webDistDir" }

Write-Host "==> Fetching portable Postgres $PostgresVersion for Windows (build-time only — the shipped .msix needs no network)" -ForegroundColor Cyan
$pgCacheDir = if ($env:RUNNER_TEMP) { Join-Path $env:RUNNER_TEMP "pg-cache" } else { Join-Path $env:TEMP "rechvix-pg-cache" }
New-Item -ItemType Directory -Force -Path $pgCacheDir | Out-Null
$pgZip = Join-Path $pgCacheDir $PostgresZipName
if (-not (Test-Path $pgZip)) {
  Write-Host "    Downloading $PostgresUrl" -ForegroundColor Cyan
  Invoke-WebRequest -Uri $PostgresUrl -OutFile $pgZip
} else {
  Write-Host "    Reusing cached $pgZip" -ForegroundColor Cyan
}

Write-Host "==> Assembling package layout at $packageDir" -ForegroundColor Cyan
if (Test-Path $packageDir) { Remove-Item $packageDir -Recurse -Force }
New-Item -ItemType Directory -Path $packageDir | Out-Null
New-Item -ItemType Directory -Path $assetsDir | Out-Null

Write-Host "==> Building the desktop shell (release, no installer bundle)" -ForegroundColor Cyan
# --no-bundle: we want just target/release/desktop.exe, not Tauri's own
# NSIS/WiX installer output — MSIX wraps the raw exe directly.
Push-Location $desktopDir
npm ci
npx tauri build --no-bundle
Pop-Location

$exePath = Join-Path $desktopDir "src-tauri\target\release\desktop.exe"
if (-not (Test-Path $exePath)) {
  throw "Expected build output not found at $exePath — did the build above fail?"
}

Copy-Item $exePath -Destination $packageDir
Copy-Item (Join-Path $msixDir "AppxManifest.xml") -Destination $packageDir
Copy-Item $serverExe -Destination $packageDir

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

Write-Host "==> Copying apps/web's build into package\web" -ForegroundColor Cyan
Copy-Item -Recurse $webDistDir (Join-Path $packageDir "web")

Write-Host "==> Extracting portable Postgres into package\pgsql" -ForegroundColor Cyan
$pgExtractTemp = Join-Path $pgCacheDir "extracted-$PostgresVersion"
if (-not (Test-Path (Join-Path $pgExtractTemp "pgsql"))) {
  if (Test-Path $pgExtractTemp) { Remove-Item $pgExtractTemp -Recurse -Force }
  New-Item -ItemType Directory -Path $pgExtractTemp | Out-Null
  Expand-Archive -Path $pgZip -DestinationPath $pgExtractTemp -Force
}
# The EDB zip's top-level entry is already named "pgsql" — copy it as-is.
Copy-Item -Recurse (Join-Path $pgExtractTemp "pgsql") (Join-Path $packageDir "pgsql")
# Strip what a bundled app never needs (headers, docs, pgAdmin, StackBuilder)
# — keeps the .msix meaningfully smaller without touching anything
# initdb/pg_ctl/postgres.exe/pg_isready actually load at runtime.
foreach ($trim in @("doc", "include", "pgAdmin 4", "StackBuilder", "symbols")) {
  $trimPath = Join-Path $packageDir "pgsql\$trim"
  if (Test-Path $trimPath) { Remove-Item $trimPath -Recurse -Force }
}

$packageSizeMB = [math]::Round(((Get-ChildItem $packageDir -Recurse | Measure-Object -Property Length -Sum).Sum / 1MB), 1)
Write-Host "==> Package layout assembled: $packageSizeMB MB total" -ForegroundColor Cyan
if ($packageSizeMB -lt 50) {
  # A truly self-contained build (Go server + web SPA + a real Postgres)
  # should be well over 100MB. A small number here almost certainly means
  # a copy step above silently failed to find its source — fail loudly
  # instead of shipping a broken package that "looks done."
  throw "Package is suspiciously small ($packageSizeMB MB) for a bundle that includes Postgres — check the copy steps above for a silent failure."
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

Write-Host "==> Done: $msixPath ($packageSizeMB MB)" -ForegroundColor Green
