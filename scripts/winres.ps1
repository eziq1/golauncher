<#
.SYNOPSIS
  Regenerates the Windows resources (icon, manifest, version information) of
  golauncher.exe and glctl.exe: cmd/<name>/rsrc_windows_{amd64,arm64}.syso.

.DESCRIPTION
  The resources come from cmd/<name>/winres/winres.json, the manifests next to
  it and the icons in assets/ (the designer's PNG files; replace them there
  when the icon changes).

  The committed .syso files carry the version in winres.json. -Version stamps
  another one (as the release workflow and build.ps1 do); build.ps1 restores
  the committed files afterwards.

.EXAMPLE
  pwsh scripts/winres.ps1
  pwsh scripts/winres.ps1 -Version 0.2.0
#>
param(
    [string]$Version = ''
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$goWinres = 'github.com/tc-hib/go-winres@v0.3.3'

foreach ($name in 'golauncher', 'glctl') {
    Push-Location (Join-Path $root "cmd/$name")
    try {
        $winresArgs = @('make', '--arch', 'amd64,arm64')
        if ($Version) {
            $winresArgs += @('--file-version', $Version, '--product-version', $Version)
        }
        go run $goWinres @winresArgs
        if ($LASTEXITCODE -ne 0) { throw "go-winres failed for $name" }
    } finally {
        Pop-Location
    }
}
