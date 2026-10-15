<#
.SYNOPSIS
  Builds golauncher.exe and glctl.exe into dist/, optionally zipped as in a
  release.

.DESCRIPTION
  The version comes from "git describe --tags --always --dirty" (without the
  leading "v"), unless -Version is given. It is linked into both binaries
  (-X main.version) and stamped into their version resources; the committed
  .syso files are restored afterwards.

  -Zip also writes dist/golauncher_<version>_windows_<arch>.zip with
  golauncher.exe, glctl.exe, LICENSE, README.md, README.zh-CN.md (if
  present) and CHANGELOG.md, the layout of the release archives
  (.github/workflows/release.yml runs this script).

.EXAMPLE
  pwsh scripts/build.ps1
  pwsh scripts/build.ps1 -Arch arm64 -Zip
  pwsh scripts/build.ps1 -Version 0.1.0 -Zip
#>
param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = '',
    [string]$Version = '',
    [string]$OutDir = '',
    [switch]$Zip,
    # Skip stamping the version into the Windows resources (keeps the
    # committed .syso files, and needs no go-winres).
    [switch]$NoResources
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
if (-not $OutDir) { $OutDir = Join-Path $root 'dist' }
if (-not $Arch) { $Arch = (go env GOHOSTARCH).Trim() }
if ($Arch -notin 'amd64', 'arm64') { throw "unsupported architecture $Arch (want amd64 or arm64)" }

if (-not $Version) {
    $described = (git -C $root describe --tags --always --dirty 2>$null)
    if ($LASTEXITCODE -ne 0 -or -not $described) { $described = 'dev' }
    $described = $described.Trim()
    if ($described -match '^v?\d+\.\d+') {
        $Version = $described -replace '^v', ''
    } else {
        # No tag yet: a commit hash, perhaps with -dirty.
        $Version = "0.0.0-$described"
    }
}
Write-Host "golauncher $Version windows/$Arch -> $OutDir"

New-Item -ItemType Directory -Force $OutDir | Out-Null

# Stamp the version into the resources, remembering the committed files.
$sysos = @{}
if (-not $NoResources) {
    foreach ($f in Get-ChildItem (Join-Path $root 'cmd') -Recurse -Filter 'rsrc_windows_*.syso') {
        $sysos[$f.FullName] = [System.IO.File]::ReadAllBytes($f.FullName)
    }
}

$saved = @{ GOOS = $env:GOOS; GOARCH = $env:GOARCH; CGO_ENABLED = $env:CGO_ENABLED }
try {
    if (-not $NoResources) {
        & (Join-Path $PSScriptRoot 'winres.ps1') -Version $Version
    }
    $env:GOOS = 'windows'
    $env:GOARCH = $Arch
    $env:CGO_ENABLED = '0'
    $ldflags = "-s -w -X main.version=$Version"
    Push-Location $root
    try {
        go build -trimpath -ldflags "-H windowsgui $ldflags" -o (Join-Path $OutDir 'golauncher.exe') ./cmd/golauncher
        if ($LASTEXITCODE -ne 0) { throw 'go build ./cmd/golauncher failed' }
        go build -trimpath -ldflags $ldflags -o (Join-Path $OutDir 'glctl.exe') ./cmd/glctl
        if ($LASTEXITCODE -ne 0) { throw 'go build ./cmd/glctl failed' }
    } finally {
        Pop-Location
    }
} finally {
    foreach ($k in $saved.Keys) {
        if ($null -eq $saved[$k]) { Remove-Item "env:$k" -ErrorAction SilentlyContinue }
        else { Set-Item "env:$k" $saved[$k] }
    }
    foreach ($path in $sysos.Keys) { [System.IO.File]::WriteAllBytes($path, $sysos[$path]) }
}

if ($Zip) {
    $name = "golauncher_${Version}_windows_$Arch"
    $stage = Join-Path $OutDir $name
    Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force $stage | Out-Null
    $files = @(
        (Join-Path $OutDir 'golauncher.exe'),
        (Join-Path $OutDir 'glctl.exe'),
        (Join-Path $root 'LICENSE'),
        (Join-Path $root 'README.md'),
        (Join-Path $root 'README.zh-CN.md'),
        (Join-Path $root 'CHANGELOG.md')
    ) | Where-Object { Test-Path $_ }
    Copy-Item $files $stage
    $zipPath = Join-Path $OutDir "$name.zip"
    Remove-Item -Force $zipPath -ErrorAction SilentlyContinue
    Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zipPath
    Remove-Item -Recurse -Force $stage
    Write-Host "wrote $zipPath"
}
