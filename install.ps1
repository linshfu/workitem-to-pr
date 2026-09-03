# very-lazy installer
#
#   irm https://raw.githubusercontent.com/linshfu/very-lazy/main/install.ps1 | iex
#
# Downloads the very-lazy TUI, asks what name you want to call it by, and puts it
# on your PATH. No git clone, no manual setup. Re-run any time to upgrade.

$ErrorActionPreference = 'Stop'
# GitHub needs TLS 1.2 (PowerShell 5.1 may still default to 1.0)
try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch {}

$Product     = 'very-lazy'
$RepoSlug    = 'linshfu/very-lazy'
$BinaryName  = 'vlui.exe'
$DefaultName = 'vl'
$InstallDir  = if ($env:VL_INSTALL_DIR) { $env:VL_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\$Product" }
$DownloadUrl = "https://github.com/$RepoSlug/releases/latest/download/$BinaryName"

function Step($m){ Write-Host "-> $m" -ForegroundColor Cyan }
function Ok($m)  { Write-Host "   [OK] $m" -ForegroundColor Green }
function Note($m){ Write-Host "   [!] $m" -ForegroundColor Yellow }
function Bad($m) { Write-Host "   [x] $m" -ForegroundColor Red }

Write-Host ""
Write-Host "  very-lazy " -ForegroundColor Cyan -NoNewline
Write-Host "— Azure DevOps 工作流 CLI" -ForegroundColor Gray
Write-Host ""

# 1) fetch the binary (environment/az/login are checked on first run, in init)
Step "安裝主程式到 $InstallDir"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$dest = Join-Path $InstallDir $BinaryName
if ($env:VL_BINARY -and (Test-Path $env:VL_BINARY)) {
    Copy-Item -LiteralPath $env:VL_BINARY -Destination $dest -Force
    Ok "從本機安裝（$env:VL_BINARY）"
} else {
    try {
        Invoke-WebRequest -Uri $DownloadUrl -OutFile $dest -UseBasicParsing
        Ok "已下載最新版"
    } catch {
        Bad "下載失敗：$($_.Exception.Message)"
        Write-Host "       來源：$DownloadUrl" -ForegroundColor DarkGray
        Write-Host "       若 Release 尚未發佈，可先： `$env:VL_BINARY='C:\path\to\vlui.exe'; 再重跑本安裝" -ForegroundColor DarkGray
        return
    }
}
if (-not (Test-Path $dest) -or (Get-Item $dest).Length -eq 0) { Bad "主程式檔案異常，安裝中止"; return }

# 2) choose the command name
Step "設定呼叫名稱"
$fromEnv = [bool]$env:VL_NAME
$name = $env:VL_NAME
if (-not $name) {
    try { $name = Read-Host "   要用什麼名稱呼叫？(直接按 Enter 用預設 $DefaultName)" } catch { $name = '' }
}
$name = ($name -replace '[^A-Za-z0-9_-]', '')
if (-not $name) { $name = $DefaultName }

# A name already on PATH would win over our shim (we append to PATH), so the alias
# simply wouldn't reach very-lazy. Steer the user to a free name.
$shim = Join-Path $InstallDir "$name.cmd"
$existing = Get-Command $name -ErrorAction SilentlyContinue
if ($existing -and $existing.Source -ne $shim) {
    Note "'$name' 已經是系統上的指令（$($existing.Source)），用它會抓到那個、不是 very-lazy。"
    if (-not $fromEnv) {
        $alt = ''
        try { $alt = Read-Host "   換一個名稱（直接 Enter 用 $DefaultName）" } catch {}
        $alt = ($alt -replace '[^A-Za-z0-9_-]', '')
        if (-not $alt) { $alt = $DefaultName }
        $name = $alt
        $shim = Join-Path $InstallDir "$name.cmd"
        Ok "改用 $name"
    } else {
        Note "（你用 VL_NAME 指定了它，仍照做，但這個名稱可能吃不到）"
    }
}

# 3) launcher shim + PATH  (a .cmd works from PowerShell, cmd, and the Run box)
"@echo off`r`n`"%~dp0$BinaryName`" %*" | Set-Content -LiteralPath $shim -Encoding ASCII
if (-not $env:VL_SKIP_PATH) {
    $userPath = [Environment]::GetEnvironmentVariable('Path','User')
    if (($userPath -split ';') -notcontains $InstallDir) {
        $newPath = if ($userPath) { "$userPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        Ok "已加入 PATH"
    }
}
if (($env:Path -split ';') -notcontains $InstallDir) { $env:Path = "$env:Path;$InstallDir" }
Ok "指令名稱：$name"

# 4) AI usage guide (skill) — installs into Claude Code's user-level skills dir
#    when ~\.claude exists; other AI assistants can get it via --export-skill.
if (Test-Path (Join-Path $env:USERPROFILE '.claude')) {
    Step "安裝 AI 使用指南（skill）"
    try { & $dest --install-skill | ForEach-Object { Write-Host "   $_" -ForegroundColor Gray } } catch { Note "skill 安裝失敗（不影響主程式）：$($_.Exception.Message)" }
} else {
    Note "未偵測到 Claude Code（~\.claude）。其他 AI 助手請跑： $name --export-skill <目錄>，再叫你的 AI 把檔案放到它會生效的位置。"
}

# 5) done
Write-Host ""
Write-Host "  安裝完成。" -ForegroundColor Green
Write-Host "  開一個新的終端機視窗，輸入 " -NoNewline; Write-Host $name -ForegroundColor Cyan -NoNewline
Write-Host " 就能開始。"
Write-Host "  第一次會自動檢查環境（git / az / 登入）並帶你做設定。" -ForegroundColor Gray
Write-Host ""
