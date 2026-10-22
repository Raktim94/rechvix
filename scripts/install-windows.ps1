# Native Windows desktop installation through the verified Microsoft Store listing.
# No WSL, Docker, source download, or administrator package bootstrap is required.
$ErrorActionPreference = 'Stop'
if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
    throw 'WinGet is required. Install or update App Installer from Microsoft Store, or open https://apps.microsoft.com/detail/9NMPSP7CR5RW to install Rechvix directly.'
}
Write-Host 'Installing Rechvix from Microsoft Store. Review any Store agreement prompts.'
winget install --id 9NMPSP7CR5RW --source msstore
if ($LASTEXITCODE -ne 0) {
    throw "Microsoft Store installation did not complete (WinGet exit code $LASTEXITCODE). Review the message above or open https://apps.microsoft.com/detail/9NMPSP7CR5RW."
}
Write-Host 'Rechvix installation completed through Microsoft Store. Open Rechvix from Start.'
