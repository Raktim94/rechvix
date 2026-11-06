<#
.SYNOPSIS
  Proves an UPDATE keeps a customer's data: install the previous release, create a business and some data, update in place to the
  new package, and check the same login still works and the data is still there.

.DESCRIPTION
  A fresh-install test cannot catch the failure that matters most to people already using the app: an update that starts on top of
  an existing database and loses or breaks it. This installs OLD, bootstraps a business through the bundled server's real API,
  creates a customer, writes a marker file into the package's data folder, closes the app, then installs NEW over it with
  Add-AppxPackage (an in-place update: same package family, higher version) and verifies:
    - the package family is unchanged and the version went up;
    - the data folder (and the marker in it) survived the update;
    - the new server starts on the old database (its migrations apply) and reports ready;
    - bootstrap is no longer offered (the business exists), the SAME owner can sign in, and the customer is still there.
  Finally the package is removed and its data folder must be gone, exactly as for a normal uninstall.

.PARAMETER OldMsix / NewMsix  signed (sideload) packages; the same package family, NewMsix having the higher version.
.PARAMETER Pfx               test certificate(s) to trust (each package may have been signed by its own run's certificate).
#>
param(
  [Parameter(Mandatory = $true)][string]$OldMsix,
  [Parameter(Mandatory = $true)][string]$NewMsix,
  [Parameter(Mandatory = $true)][string[]]$Pfx
)

$ErrorActionPreference = "Stop"
$OldMsix = (Resolve-Path $OldMsix).Path
$NewMsix = (Resolve-Path $NewMsix).Path

function Wait-ForCondition {
  param([string]$What, [scriptblock]$Test, [int]$TimeoutSec = 30)
  $deadline = (Get-Date).AddSeconds($TimeoutSec)
  while ((Get-Date) -lt $deadline) { if (& $Test) { return }; Start-Sleep -Milliseconds 400 }
  throw "Timed out waiting for: $What"
}

function Get-PackageManifest([string]$msix) {
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $zip = [IO.Compression.ZipFile]::OpenRead($msix)
  try {
    $e = $zip.GetEntry("AppxManifest.xml")
    $sr = New-Object IO.StreamReader($e.Open())
    try { return [xml]$sr.ReadToEnd() } finally { $sr.Dispose() }
  } finally { $zip.Dispose() }
}

$oldManifest = Get-PackageManifest $OldMsix
$newManifest = Get-PackageManifest $NewMsix
$identityName = $newManifest.Package.Identity.Name
$appId = $newManifest.Package.Applications.Application.Id
$oldVer = [version]$oldManifest.Package.Identity.Version
$newVer = [version]$newManifest.Package.Identity.Version
if ($oldManifest.Package.Identity.Name -ne $identityName) { throw "Old and new packages have different identities: $($oldManifest.Package.Identity.Name) vs $identityName" }
if ($newVer -le $oldVer) { throw "The new package ($newVer) must have a higher version than the old one ($oldVer)." }
Write-Host "==> Upgrade test: $oldVer -> $newVer ($identityName)" -ForegroundColor Cyan

foreach ($p in $Pfx) {
  Import-PfxCertificate -FilePath $p -CertStoreLocation Cert:\LocalMachine\TrustedPeople `
    -Password (ConvertTo-SecureString "rechvix-test" -Force -AsPlainText) | Out-Null
}

function Start-AppAndWait([string]$family) {
  $aumid = "$family!$appId"
  $root = Join-Path $env:LOCALAPPDATA "Packages\$family"
  # runtime.json from a previous run holds a dead port; the app rewrites it once its backend is ready again.
  Get-ChildItem -Path $root -Recurse -Filter "runtime.json" -ErrorAction SilentlyContinue | Remove-Item -Force -ErrorAction SilentlyContinue
  Start-Process "explorer.exe" -ArgumentList "shell:AppsFolder\$aumid"
  Wait-ForCondition "desktop.exe to appear" { Get-Process desktop -ErrorAction SilentlyContinue } 60
  Wait-ForCondition "backend to report ready (runtime.json)" { Get-ChildItem -Path $root -Recurse -Filter "runtime.json" -ErrorAction SilentlyContinue } 240
  $rt = Get-Content (Get-ChildItem -Path $root -Recurse -Filter "runtime.json" | Select-Object -First 1).FullName | ConvertFrom-Json
  return [int]$rt.http_port
}

function Stop-App {
  $p = Get-Process desktop -ErrorAction SilentlyContinue
  if ($p) { $p | ForEach-Object { $_.CloseMainWindow() | Out-Null } }
  try { Wait-ForCondition "desktop.exe to exit" { -not (Get-Process desktop -ErrorAction SilentlyContinue) } 60 } catch { Get-Process desktop -ErrorAction SilentlyContinue | Stop-Process -Force }
  # the bundled database must not outlive the app, or the update could not replace files it holds open
  Wait-ForCondition "bundled server and database to stop" { -not (Get-Process rechvix-server, postgres -ErrorAction SilentlyContinue) } 60
}

function Invoke-Api([int]$port, [string]$method, [string]$path, $body, [string]$cookie) {
  $h = @{}
  if ($cookie) { $h["Cookie"] = $cookie }
  $args = @{ Uri = "http://127.0.0.1:$port/api/v1$path"; Method = $method; UseBasicParsing = $true; Headers = $h }
  if ($null -ne $body) { $args["Body"] = ($body | ConvertTo-Json -Depth 8); $args["ContentType"] = "application/json" }
  return Invoke-WebRequest @args
}

# ---------------------------------------------------------------- OLD version, with real data
Write-Host "==> Installing the OLD version ($oldVer)" -ForegroundColor Cyan
Add-AppxPackage -Path $OldMsix
$pkgOld = Get-AppxPackage -Name $identityName
if ([version]$pkgOld.Version -ne $oldVer) { throw "Expected $oldVer installed, found $($pkgOld.Version)." }
$family = $pkgOld.PackageFamilyName
$port = Start-AppAndWait $family
Write-Host "    OK: old version running on port $port" -ForegroundColor Green

$email = "upgrade.owner@example.test"
$password = "Upgrade-Test-Pass-12345!"
$customer = "Upgrade Test Customer $([guid]::NewGuid().ToString('N').Substring(0,8))"
$boot = (Invoke-Api $port GET "/auth/bootstrap" $null $null).Content | ConvertFrom-Json
if (-not $boot.available) { throw "Fresh install should offer account creation." }
Invoke-Api $port POST "/auth/bootstrap" @{
  organisation_name = "Upgrade Test Co"; default_currency_code = "INR"; default_timezone = "Asia/Kolkata"; legal_entity_name = "Upgrade Test Co Pvt Ltd"
  country_code = "IN"; gst_state_code = "27"; branch_code = "BR1"; branch_name = "Main"; warehouse_code = "WH1"; warehouse_name = "Main warehouse"
  owner_full_name = "Upgrade Owner"; owner_email = $email; owner_password = $password
} $null | Out-Null
$login = Invoke-Api $port POST "/auth/login" @{ email = $email; password = $password; mfa_code = "" } $null
$cookie = ($login.Headers["Set-Cookie"] | Select-Object -First 1) -replace ";.*$", ""
if (-not $cookie) { throw "Login did not return a session cookie." }
Invoke-Api $port POST "/contacts/parties" @{ party_type = "CUSTOMER"; legal_name = $customer; trade_name = ""; phone = ""; email = ""; currency_code = "INR" } $cookie | Out-Null
Write-Host "    OK: business created, owner signed in, customer '$customer' saved" -ForegroundColor Green

$dataDir = (Get-ChildItem -Path (Join-Path $env:LOCALAPPDATA "Packages\$family") -Recurse -Filter "PG_VERSION" -ErrorAction SilentlyContinue | Select-Object -First 1).Directory.Parent.FullName
if (-not $dataDir) { throw "Could not find the database data folder under the package storage." }
$marker = Join-Path $dataDir "upgrade-marker.txt"
Set-Content -Path $marker -Value "written by the old version"
Write-Host "    OK: data folder is $dataDir" -ForegroundColor Green

Stop-App
Write-Host "    OK: old version closed cleanly" -ForegroundColor Green

# ---------------------------------------------------------------- UPDATE in place
Write-Host "==> Updating in place to $newVer (the user's data stays)" -ForegroundColor Cyan
Add-AppxPackage -Path $NewMsix
$pkgNew = Get-AppxPackage -Name $identityName
if ([version]$pkgNew.Version -ne $newVer) { throw "Expected $newVer after the update, found $($pkgNew.Version)." }
if ($pkgNew.PackageFamilyName -ne $family) { throw "Package family changed across the update ($family -> $($pkgNew.PackageFamilyName)): users would lose their data." }
if (-not (Test-Path $marker)) { throw "The data folder did not survive the update (marker file is gone)." }
Write-Host "    OK: $($pkgOld.Version) -> $($pkgNew.Version), same package family, data folder and marker intact" -ForegroundColor Green

# ---------------------------------------------------------------- NEW version on the OLD database
$port2 = Start-AppAndWait $family
Write-Host "    OK: new version started on the existing database (port $port2)" -ForegroundColor Green
$health = Invoke-WebRequest "http://127.0.0.1:$port2/health/ready" -UseBasicParsing
if ($health.StatusCode -ne 200) { throw "New version not healthy after the update." }

$boot2 = (Invoke-Api $port2 GET "/auth/bootstrap" $null $null).Content | ConvertFrom-Json
if ($boot2.available) { throw "After the update the app offers 'create your business' again: it did not see the existing database." }
Write-Host "    OK: the business still exists (setup is not offered again)" -ForegroundColor Green

$login2 = Invoke-Api $port2 POST "/auth/login" @{ email = $email; password = $password; mfa_code = "" } $null
$cookie2 = ($login2.Headers["Set-Cookie"] | Select-Object -First 1) -replace ";.*$", ""
if (-not $cookie2) { throw "The same owner could not sign in after the update." }
Write-Host "    OK: the same owner signs in with the same password" -ForegroundColor Green

$parties = ((Invoke-Api $port2 GET "/contacts/parties" $null $cookie2).Content | ConvertFrom-Json).parties
if (-not ($parties | Where-Object { $_.LegalName -eq $customer })) { throw "Customer '$customer' is missing after the update." }
Write-Host "    OK: the customer saved before the update is still there" -ForegroundColor Green

# ---------------------------------------------------------------- uninstall cleans up
Stop-App
Get-AppxPackage -Name $identityName | Remove-AppxPackage
Wait-ForCondition "data folder to be removed on uninstall" { -not (Test-Path (Join-Path $env:LOCALAPPDATA "Packages\$family")) } 60
Write-Host "    OK: uninstall removed the package and its data" -ForegroundColor Green

Write-Host "`n==> UPGRADE TEST PASSED: update keeps the business, the login and the data" -ForegroundColor Green
