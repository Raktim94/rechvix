$ErrorActionPreference = 'Stop'
$scriptFile = Join-Path $PSScriptRoot 'install-windows.ps1'
$global:InstallerCalls = @()
$global:InstallerTestExit = 0
function global:winget {
    $global:InstallerCalls += ,$args
    $global:LASTEXITCODE = $global:InstallerTestExit
}
& $scriptFile
$actual = $global:InstallerCalls[0] -join ' '
if ($actual -ne 'install --id 9NMPSP7CR5RW --source msstore') { throw "Unexpected WinGet arguments: $actual" }
$global:InstallerTestExit = 17
$failed = $false
try { & $scriptFile } catch { $failed = $true }
if (-not $failed) { throw 'Failed WinGet installation must not report success.' }
Remove-Item Function:winget
$global:LASTEXITCODE = 0
Write-Host 'PASS: exact Store package/source, no implicit agreement acceptance, failure propagates.'
