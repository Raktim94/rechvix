<#
.SYNOPSIS
  Installs Rechvix.msix and proves install / launch / minimize / maximize /
  single-instance / close / uninstall actually work — not "written against
  the docs", actually exercised. Throws (fails the run) on the first thing
  that doesn't hold; nothing here is a soft warning.

.DESCRIPTION
  Run from apps/desktop/msix, after build-msix.ps1 -Sign has produced
  Rechvix.msix + RechvixTestCert.pfx in this same folder.
#>
$ErrorActionPreference = "Stop"

$msixDir = $PSScriptRoot
$msixPath = Join-Path $msixDir "Rechvix.msix"
$pfxPath = Join-Path $msixDir "RechvixTestCert.pfx"
$manifest = [xml](Get-Content (Join-Path $msixDir "AppxManifest.xml"))
$identityName = $manifest.Package.Identity.Name
$appId = $manifest.Package.Applications.Application.Id

Add-Type @"
using System;
using System.Runtime.InteropServices;
public static class Win32 {
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);
  [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr hWnd);
  [DllImport("user32.dll")] public static extern bool IsZoomed(IntPtr hWnd);
}
"@
$SW_MAXIMIZE = 3
$SW_MINIMIZE = 6
$SW_RESTORE = 9

function Wait-ForCondition {
  param([string]$What, [scriptblock]$Test, [int]$TimeoutSec = 20)
  $deadline = (Get-Date).AddSeconds($TimeoutSec)
  while ((Get-Date) -lt $deadline) {
    if (& $Test) { return }
    Start-Sleep -Milliseconds 300
  }
  throw "Timed out waiting for: $What"
}

Write-Host "==> Importing test certificate into Trusted People" -ForegroundColor Cyan
Import-PfxCertificate -FilePath $pfxPath -CertStoreLocation Cert:\LocalMachine\TrustedPeople `
  -Password (ConvertTo-SecureString "rechvix-test" -Force -AsPlainText) | Out-Null

Write-Host "==> Installing $msixPath" -ForegroundColor Cyan
Add-AppxPackage -Path $msixPath

$pkg = Get-AppxPackage -Name $identityName
if (-not $pkg) { throw "Get-AppxPackage found nothing for '$identityName' after install." }
Write-Host "    OK: installed $($pkg.PackageFullName) at $($pkg.InstallLocation)" -ForegroundColor Green

$aumid = "$($pkg.PackageFamilyName)!$appId"

Write-Host "==> Launching via Start Menu identity ($aumid)" -ForegroundColor Cyan
Start-Process "explorer.exe" -ArgumentList "shell:AppsFolder\$aumid"

Wait-ForCondition "desktop.exe process to appear" { Get-Process desktop -ErrorAction SilentlyContinue }
Wait-ForCondition "main window handle" { ((Get-Process desktop -ErrorAction SilentlyContinue).MainWindowHandle -ne 0) }
$proc = Get-Process desktop
$hwnd = $proc.MainWindowHandle
Write-Host "    OK: desktop.exe PID $($proc.Id), hwnd $hwnd" -ForegroundColor Green

Write-Host "==> Open: window visible with a real handle — confirmed above" -ForegroundColor Cyan

# The bundled backend (Postgres + rechvix-server.exe) starts asynchronously
# after the window appears — src-tauri writes runtime.json (via
# AppPaths::resolve()'s app_local_data_dir(), which Windows redirects into
# this package's own storage) once /health/ready is actually passing.
# Searched recursively rather than hardcoding the exact redirected
# subfolder (LocalCache\Local vs LocalState is a Tauri/dirs-crate/Windows
# implementation detail not worth pinning down by hand here).
$localDataRoot = Join-Path $env:LOCALAPPDATA "Packages\$($pkg.PackageFamilyName)"
Write-Host "==> Waiting for the bundled backend to report ready ($localDataRoot)" -ForegroundColor Cyan
try {
  Wait-ForCondition "runtime.json to appear (backend finished starting)" {
    Get-ChildItem -Path $localDataRoot -Recurse -Filter "runtime.json" -ErrorAction SilentlyContinue
  } 120
} catch {
  Write-Host "==> Backend never reported ready — dumping whatever diagnostics exist under $localDataRoot" -ForegroundColor Red
  Get-ChildItem -Path $localDataRoot -Recurse -ErrorAction SilentlyContinue | ForEach-Object { Write-Host "    found: $($_.FullName)" }
  foreach ($name in @("startup-error.txt", "server.log", "postgres.log")) {
    $found = Get-ChildItem -Path $localDataRoot -Recurse -Filter $name -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($found) {
      Write-Host "----- $($found.FullName) -----" -ForegroundColor Yellow
      Get-Content $found.FullName -Tail 100 | Write-Host
    } else {
      Write-Host "    ($name not found anywhere under $localDataRoot)" -ForegroundColor Yellow
    }
  }
  throw
}
$runtimeInfoPath = (Get-ChildItem -Path $localDataRoot -Recurse -Filter "runtime.json" -ErrorAction SilentlyContinue | Select-Object -First 1).FullName
$runtimeInfo = Get-Content $runtimeInfoPath | ConvertFrom-Json
$httpPort = $runtimeInfo.http_port
Write-Host "    OK: backend reports http_port=$httpPort (pg_port=$($runtimeInfo.pg_port))" -ForegroundColor Green

Write-Host "==> Healthcheck: GET http://127.0.0.1:$httpPort/health/ready" -ForegroundColor Cyan
$health = Invoke-WebRequest "http://127.0.0.1:$httpPort/health/ready" -UseBasicParsing
if ($health.StatusCode -ne 200) { throw "Expected 200 from /health/ready, got $($health.StatusCode)." }
Write-Host "    OK: bundled server is healthy and serving on 127.0.0.1" -ForegroundColor Green

Write-Host "==> Bootstrap screen reachable (fresh install, no organisation yet)" -ForegroundColor Cyan
$bootstrap = Invoke-WebRequest "http://127.0.0.1:$httpPort/api/v1/auth/bootstrap" -UseBasicParsing
$bootstrapBody = $bootstrap.Content | ConvertFrom-Json
if (-not $bootstrapBody.available) {
  throw "Expected /api/v1/auth/bootstrap to report available:true on a fresh install (got $($bootstrap.Content)) — first launch should always be able to create an account."
}
Write-Host "    OK: account creation is available on first launch, no pre-existing data" -ForegroundColor Green

Write-Host "==> Minimize" -ForegroundColor Cyan
[Win32]::ShowWindow($hwnd, $SW_MINIMIZE) | Out-Null
Wait-ForCondition "IsIconic true" { [Win32]::IsIconic($hwnd) }
Write-Host "    OK: minimized" -ForegroundColor Green

Write-Host "==> Restore then maximize" -ForegroundColor Cyan
[Win32]::ShowWindow($hwnd, $SW_RESTORE) | Out-Null
Wait-ForCondition "IsIconic false after restore" { -not [Win32]::IsIconic($hwnd) }
[Win32]::ShowWindow($hwnd, $SW_MAXIMIZE) | Out-Null
Wait-ForCondition "IsZoomed true" { [Win32]::IsZoomed($hwnd) }
Write-Host "    OK: maximized" -ForegroundColor Green
[Win32]::ShowWindow($hwnd, $SW_RESTORE) | Out-Null
Wait-ForCondition "IsZoomed false after restore" { -not [Win32]::IsZoomed($hwnd) }

Write-Host "==> Single-instance: launching a second time while running" -ForegroundColor Cyan
$firstPid = $proc.Id
Start-Process "explorer.exe" -ArgumentList "shell:AppsFolder\$aumid"
Start-Sleep -Seconds 5
$stillRunning = @(Get-Process desktop -ErrorAction SilentlyContinue)
if ($stillRunning.Count -ne 1) {
  throw "Expected exactly 1 desktop.exe process after a second launch, found $($stillRunning.Count) — single-instance plugin not preventing a second process."
}
if ($stillRunning[0].Id -ne $firstPid) {
  throw "Second launch produced a different PID ($($stillRunning[0].Id)) instead of reusing PID $firstPid."
}
Write-Host "    OK: still exactly 1 process (PID $firstPid) — tauri-plugin-single-instance works" -ForegroundColor Green

Write-Host "==> Close (WM_CLOSE — same path as the title-bar close button)" -ForegroundColor Cyan
$proc = Get-Process -Id $firstPid
$null = $proc.CloseMainWindow()
Wait-ForCondition "process to exit after close" { -not (Get-Process -Id $firstPid -ErrorAction SilentlyContinue) } 15
Write-Host "    OK: desktop.exe (PID $firstPid) exited cleanly, no lingering process" -ForegroundColor Green

Write-Host "==> Uninstall" -ForegroundColor Cyan
$installLocation = $pkg.InstallLocation
Remove-AppxPackage -Package $pkg.PackageFullName
Start-Sleep -Seconds 2
if (Get-AppxPackage -Name $identityName) { throw "Package still present after Remove-AppxPackage." }
if (Test-Path $installLocation) { throw "Install directory $installLocation still exists after uninstall." }
Write-Host "    OK: package and install directory fully removed" -ForegroundColor Green

Write-Host "==> Confirming local data (pgdata + AEAD key + runtime.json) cleaned up too" -ForegroundColor Cyan
if (Test-Path $localDataRoot) {
  throw "Local data directory $localDataRoot still exists after uninstall — pgdata/secrets not cleaned up."
}
Write-Host "    OK: local data directory fully removed" -ForegroundColor Green

Write-Host "==> Re-install after uninstall (update/reset flow)" -ForegroundColor Cyan
Add-AppxPackage -Path $msixPath
$pkg2 = Get-AppxPackage -Name $identityName
if (-not $pkg2) { throw "Re-install after uninstall failed." }
Write-Host "    OK: re-install succeeded ($($pkg2.PackageFullName))" -ForegroundColor Green
Remove-AppxPackage -Package $pkg2.PackageFullName

Write-Host ""
Write-Host "==> ALL CHECKS PASSED: install, open, minimize, maximize, single-instance, close, uninstall, reinstall" -ForegroundColor Green
