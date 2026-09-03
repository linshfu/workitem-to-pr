# build.ps1 — produce a release binary of the very-lazy TUI at dist\vlui.exe
#
# This is the asset you upload to the GitHub Release that install.ps1 downloads.
# Requires Go (winget install GoLang.Go).
#
# Release build:  .\build.ps1 -Version cli-v0.2.0   (must match the Git tag of the Release)

param([string]$Version = 'dev')

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path

$go = $null
$c = Get-Command go -ErrorAction SilentlyContinue
if ($c) { $go = $c.Source }
if (-not $go) {
    foreach ($p in @("$env:ProgramFiles\Go\bin\go.exe", "$env:LOCALAPPDATA\Programs\Go\bin\go.exe", "C:\Go\bin\go.exe")) {
        if (Test-Path $p) { $go = $p; break }
    }
}
if (-not $go) { throw "找不到 go，請先安裝 Go： winget install GoLang.Go" }

$dist = Join-Path $root 'dist'
New-Item -ItemType Directory -Force -Path $dist | Out-Null
$out = Join-Path $dist 'vlui.exe'

Push-Location (Join-Path $root 'cli')
try {
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'
    & $go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $out .
    if ($LASTEXITCODE -ne 0) { throw "go build 失敗 ($LASTEXITCODE)" }
} finally { Pop-Location }

$mb = [math]::Round((Get-Item $out).Length / 1MB, 1)
Write-Host "已產生 $out ($mb MB)，版本 $Version" -ForegroundColor Green
Write-Host "下一步：發一個 tag = $Version 的 GitHub Release，把它當資產上傳（asset 檔名需為 vlui.exe）。" -ForegroundColor Gray
