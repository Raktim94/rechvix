<#
.SYNOPSIS
  Fails unless the Store package and the package that was install-tested hold exactly the same files.

.DESCRIPTION
  Only the signature (AppxSignature.p7x) and the content-types list that mentions it may differ. Every other entry, including
  both executables, the bundled PostgreSQL, the web app and the manifest, must be byte-identical. This is what lets "the
  installer test passed" mean "the file we upload to the Store works".
#>
param(
  [Parameter(Mandatory = $true)][string]$Store,
  [Parameter(Mandatory = $true)][string]$Tested
)

$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.IO.Compression.FileSystem

function Get-EntryHashes([string]$path) {
  $zip = [IO.Compression.ZipFile]::OpenRead((Resolve-Path $path).Path)
  try {
    $h = @{}
    foreach ($e in $zip.Entries) {
      $s = $e.Open()
      $sha = [Security.Cryptography.SHA256]::Create()
      try { $h[$e.FullName] = [BitConverter]::ToString($sha.ComputeHash($s)) } finally { $s.Dispose(); $sha.Dispose() }
    }
    return $h
  } finally { $zip.Dispose() }
}

$a = Get-EntryHashes $Store
$b = Get-EntryHashes $Tested
$ignore = @("AppxSignature.p7x", "[Content_Types].xml")
$all = @($a.Keys) + @($b.Keys) | Sort-Object -Unique
$diff = $all | Where-Object { ($ignore -notcontains $_) -and ($a[$_] -ne $b[$_]) }
if ($diff) { throw "The Store package and the tested package differ in: $($diff -join ', ')" }
Write-Host "==> Store package and tested package hold identical files ($($a.Count) entries; only the signature differs)." -ForegroundColor Green
