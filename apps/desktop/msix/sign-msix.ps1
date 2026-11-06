<#
.SYNOPSIS
  Signs an EXISTING .msix with the local test certificate, without rebuilding anything.

.DESCRIPTION
  CI builds the package once, keeps that file unsigned for the Microsoft Store, then signs a COPY of it with this script and
  installs the copy for testing. Signing only adds a signature, so the tested payload is byte-identical to the file that is
  submitted. (Rebuilding for the signed copy would produce a different executable than the one shipped, and the shipped one
  would never have been tested.) The test certificate does not survive Store submission: Partner Center re-signs the upload.
#>
param(
  [Parameter(Mandatory = $true)][string]$Path
)

$ErrorActionPreference = "Stop"
$msixDir = $PSScriptRoot
$msixPath = (Resolve-Path $Path).Path

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
}

& $signtool sign /fd SHA256 /a /f $pfxPath /p "rechvix-test" $msixPath
if ($LASTEXITCODE -ne 0) { throw "signtool failed with exit code $LASTEXITCODE" }
Write-Host "==> Signed $msixPath" -ForegroundColor Green
