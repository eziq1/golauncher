<#
.SYNOPSIS
  Prints the CHANGELOG.md section of one version (without its heading), for
  release notes. Fails when the section is missing or empty.

.EXAMPLE
  pwsh scripts/changelog.ps1 -Version 0.1.0 > notes.md
  pwsh scripts/changelog.ps1 -Version v0.1.0
#>
param(
    [Parameter(Mandatory)]
    [string]$Version,
    [string]$Path = ''
)

$ErrorActionPreference = 'Stop'
if (-not $Path) { $Path = Join-Path (Split-Path -Parent $PSScriptRoot) 'CHANGELOG.md' }
$Version = $Version -replace '^v', ''

$heading = '^## \[' + [regex]::Escape($Version) + '\]'
$lines = Get-Content -LiteralPath $Path -Encoding utf8
$out = [System.Collections.Generic.List[string]]::new()
$inside = $false
foreach ($line in $lines) {
    if ($line -match '^## ') {
        if ($inside) { break }
        $inside = $line -match $heading
        continue
    }
    # Link reference definitions at the end of the file.
    if ($inside -and $line -match '^\[[^\]]+\]: ') { break }
    if ($inside) { $out.Add($line) }
}
$text = ($out -join "`n").Trim()
if (-not $text) { throw "CHANGELOG.md has no section for $Version" }
$text
