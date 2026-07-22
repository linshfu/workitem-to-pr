[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)]
    [int]$WorkItemId,
    [switch]$Help,
    [switch]$ListChannels,
    [switch]$ManualRelease,
    [switch]$Hotfix,
    [string]$Project = "",
    [string]$ReleaseVersion = "",
    [string]$Reviewer = "",
    [switch]$SkipSlack,
    [switch]$SkipReviewer,
    [switch]$DryRun,
    [string]$SearchKeyword = "",
    [switch]$TestSlackMessage,
    [switch]$NewPbi,
    # 標題可直接接在 ID 之後（空白分隔、各自加引號），例如：
    #   vl 34591 "[Chem][前端] 標題A" "[Chem][前端] 標題B"
    # 仍相容具名 + 逗號寫法：vl 34591 -NewTask "A","B"
    # （PositionalBinding=$false 讓其餘具名參數不吃位置，標題才能全部落到這裡）
    [Parameter(Position = 1, ValueFromRemainingArguments = $true)]
    [string[]]$NewTask = @()
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Load modules in dependency order
Remove-Module Config, UI, Git, AzureDevOps, Slack, Workflow -ErrorAction SilentlyContinue
Import-Module (Join-Path $PSScriptRoot "modules\Config.psm1") -Force -DisableNameChecking
Import-Module (Join-Path $PSScriptRoot "modules\UI.psm1") -Force -DisableNameChecking
Import-Module (Join-Path $PSScriptRoot "modules\Git.psm1") -Force -DisableNameChecking
Import-Module (Join-Path $PSScriptRoot "modules\AzureDevOps.psm1") -Force -DisableNameChecking
Import-Module (Join-Path $PSScriptRoot "modules\Slack.psm1") -Force -DisableNameChecking
Import-Module (Join-Path $PSScriptRoot "modules\Workflow.psm1") -Force -DisableNameChecking

Initialize-Environment -ConfigRoot $PSScriptRoot | Out-Null

try {
    if ($Help) {
        Show-Usage
        return
    }

    if ($ManualRelease) {
        $slackToken = $null
        if (-not $SkipSlack) {
            $slackToken = Get-Slack-Token-With-Prompt
        }
        $azOrg = Get-AzureOrg
        Start-Az-Login

        Start-Manual-Release-Process -AzOrg $azOrg -SlackToken $slackToken -Project $Project -ReleaseVersion $ReleaseVersion -OverrideReviewer $Reviewer -SkipSlack:$SkipSlack -DryRun:$DryRun
        return
    }

    if ($Hotfix) {
        $slackToken = $null
        if (-not $SkipSlack) {
            $slackToken = Get-Slack-Token-With-Prompt
        }
        $azOrg = Get-AzureOrg
        Start-Az-Login

        Start-Hotfix-Process -AzOrg $azOrg -SlackToken $slackToken -Project $Project -HotfixVersion $ReleaseVersion -OverrideReviewer $Reviewer -SkipSlack:$SkipSlack -DryRun:$DryRun
        return
    }

    if ($ListChannels) {
        if (-not $SearchKeyword) {
            Show-Msg "❌ 請提供搜尋關鍵字" -Type 'Error'
            Show-Msg "使用方式: .\main.ps1 -ListChannels -SearchKeyword \"關鍵字\"" -Type 'Information'
            return
        }

        Show-Msg "🔍 搜尋頻道 ID..." -Type 'Information'
        $slackToken = Get-Slack-Token-With-Prompt
        Find-Channel-ID -slackToken $slackToken -searchKeyword $SearchKeyword
        return
    }

    if ($TestSlackMessage) {
        Show-Msg "🧪 測試 Slack 訊息發送..." -Type 'Information'
        $slackToken = Get-Slack-Token-With-Prompt

        Show-Msg "正在取得 Slack 成員列表..." -Type 'Process'
        $members = Get-Slack-Members -slackToken $slackToken

        if ($members) {
            Show-Msg "取得成員成功，請選擇一個成員進行測試" -Type 'Success'
            $testSlackMessage = "這是一條測試訊息，確認 Slack 通知功能正常運作中。 `n時間: $(Get-Date)"
            Send-Slack-Message -slackToken $slackToken -members $members -prResult $testSlackMessage | Out-Null
        }
        else {
            Show-Msg "❌ 無法取得 Slack 成員列表，請檢查 Token 權限" -Type 'Error'
        }
        return
    }

    Invoke-VeryLazyMain -WorkItemId $WorkItemId -Reviewer $Reviewer -SkipSlack:$SkipSlack -SkipReviewer:$SkipReviewer -DryRun:$DryRun -NewTask $NewTask -NewPbi:$NewPbi
}
catch {
    Show-Msg "致命錯誤: $($_.Exception.Message)" -Type 'Error'
    throw
}
