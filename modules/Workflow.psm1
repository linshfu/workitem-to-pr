# Workflow.psm1 - Core workflow orchestration

# Dependencies: Config.psm1, UI.psm1, Git.psm1, AzureDevOps.psm1, Slack.psm1

function Show-Usage {
    Show-Msg "Very-Lazy 使用說明" -Type 'Warning'
    Show-Msg "" -Type 'Information'
    Show-Msg "常用指令" -Type 'Warning'
    Show-Msg "  vl 22222" -Type 'Information'
    Show-Msg "    處理工作項 22222（分支、PR、Slack 通知）" -Type 'Information'
    Show-Msg "" -Type 'Information'
    Show-Msg "  vl 34429 -NewTask `"[AT][前端] 標題`"" -Type 'Information'
    Show-Msg "    直接在 PBI 下建立新 Task（略過現有 Task 選單），然後繼續分支/PR/Slack 流程" -Type 'Information'
    Show-Msg "" -Type 'Information'
    Show-Msg "  vl 34429 -NewTask `"[AT][前端] 標題A`",`"[AT][前端] 標題B`"" -Type 'Information'
    Show-Msg "    一次建立多張 Task（逗號分隔多個標題），可加選既有 Task，一張 PR 連結全部" -Type 'Information'
    Show-Msg "" -Type 'Information'
    Show-Msg "  vl -NewPbi" -Type 'Information'
    Show-Msg "    先建立 PBI 再繼續：選專案 -> 輸入標題 -> 自動帶 Area/Iteration(當年當月)/指派自己" -Type 'Information'
    Show-Msg "    建完接原本 vl <id> 流程；可搭 -NewTask `"標題`" 直接開子 Task" -Type 'Information'
    Show-Msg "" -Type 'Information'
    Show-Msg "  vl -ManualRelease -Project lite -ReleaseVersion 1.6.2" -Type 'Information'
    Show-Msg "    針對指定專案與版本執行手動 Release" -Type 'Information'
    Show-Msg "" -Type 'Information'
    Show-Msg "  vl -Hotfix -Project lite -ReleaseVersion 1.6.3" -Type 'Information'
    Show-Msg "    Hotfix：更新 master -> 開 hotfix/v1.6.3 -> 等你 push 修正 -> 改版號 commit -> PR 到 master/develop" -Type 'Information'
    Show-Msg "    （-Project/-ReleaseVersion 可省略，會互動詢問）" -Type 'Information'
    Show-Msg "" -Type 'Information'
    Show-Msg "  vl 22222 -Reviewer someone@example.com" -Type 'Information'
    Show-Msg "    指定 reviewer，略過互動式 reviewer 選擇" -Type 'Information'
    Show-Msg "" -Type 'Information'
    Show-Msg "  vl 22222 -SkipReviewer" -Type 'Information'
    Show-Msg "    建立 PR 但不指定審核者、不設 auto-complete（會一併略過 Slack）" -Type 'Information'
    Show-Msg "" -Type 'Information'
    Show-Msg "  vl -ManualRelease -Project lite -ReleaseVersion 1.6.2 -Reviewer someone@example.com" -Type 'Information'
    Show-Msg "    手動 Release 並指定 reviewer" -Type 'Information'
    Show-Msg "" -Type 'Information'
    Show-Msg "選項" -Type 'Warning'
    Show-Msg "  -NewPbi          先互動式建立 PBI（選專案/輸入標題/自動 Area+Iteration+指派自己）再繼續" -Type 'Information'
    Show-Msg "  -NewTask `"標題`"  直接建立新 Task，略過現有 Task 選單；`"A`",`"B`" 可一次建多張並由 PR 連結全部" -Type 'Information'
    Show-Msg "  -SkipSlack       只建立 PR，不發 Slack 通知" -Type 'Information'
    Show-Msg "  -SkipReviewer    建立 PR 但不加必要 reviewer / 不設 auto-complete（會一併略過 Slack）" -Type 'Information'
    Show-Msg "  -DryRun          模擬模式，不建立分支/PR" -Type 'Information'
    Show-Msg "" -Type 'Information'
    Show-Msg "專案對應" -Type 'Warning'
    Show-Msg "  PR 目標與切分支基底依各專案 config 的 defaultBranch（未設定預設 develop）" -Type 'Information'
    Show-Msg "  例：action-new = main，其餘專案 = develop" -Type 'Information'
    Show-Msg "  比對失敗手動選 project/repo 後可輸入關鍵字儲存，下次自動比對（一般與 Release 流程皆適用）" -Type 'Information'
    Show-Msg "" -Type 'Information'
    Show-Msg "Release" -Type 'Warning'
    Show-Msg "  Release 分支建立後會先停下確認，再建立 PR（master/develop）與發送 Slack" -Type 'Information'
}

function Get-Slack-Token-With-Prompt {
    # Precedence: config.local.json (gitignored) -> SLACK_BOT_TOKEN env var -> prompt once.
    $token = Get-SlackTokenFromConfig
    if ($token) {
        return $token
    }

    if ($env:SLACK_BOT_TOKEN) {
        return $env:SLACK_BOT_TOKEN
    }

    Show-Msg "找不到 Slack token。" -Type 'Warning'
    Show-Msg "請前往 https://api.slack.com/apps" -Type 'Link'
    Show-Msg "選擇 App -> OAuth & Permissions -> Bot User OAuth Token" -Type 'Information'

    $token = Read-Host "請輸入 Slack Bot Token（xoxb-...）"
    if ([string]::IsNullOrWhiteSpace($token)) {
        throw "Slack token 不可為空"
    }
    $token = $token.Trim()

    # Validate before saving so we never persist a typo'd / revoked token.
    if (-not (Test-Slack-Token-Validity -slackToken $token)) {
        Show-Msg "Token 驗證未通過，這次不會儲存到 config.local.json，請確認後重試。" -Type 'Warning'
        return $token
    }

    try {
        Save-SlackTokenToConfig -Token $token
        Show-Msg "已儲存 Slack token 到 config.local.json（已被 .gitignore 排除）。" -Type 'Success'
    }
    catch {
        Show-Msg "無法儲存 Slack token: $($_.Exception.Message)" -Type 'Warning'
    }

    return $token
}

function Get-Release-Project-Info {
    param(
        [string]$ReleaseTitle,
        [string]$ProjectName = $null
    )

    $projectDirectoryMap = Load-Config -ConfigType "project"
    if (-not $projectDirectoryMap) {
        Show-Msg "Cannot load project path config" -Type 'Error'
        return $null
    }

    $matchedProject = $null
    $matchedPath = $null

    if ($ProjectName -and -not [string]::IsNullOrWhiteSpace($ProjectName)) {
        if ($projectDirectoryMap.$ProjectName) {
            $matchedProject = $ProjectName
            $matchedPath = $projectDirectoryMap.$ProjectName
        }
        else {
            Show-Msg "Project '$ProjectName' is not configured" -Type 'Error'
            return $null
        }
    }
    else {
        $matchedProjects = @()
        foreach ($projectKey in $projectDirectoryMap.PSObject.Properties.Name) {
            $index = $ReleaseTitle.ToLower().IndexOf($projectKey.ToLower())
            if ($index -ge 0) {
                $matchedProjects += [PSCustomObject]@{
                    ProjectKey = $projectKey
                    Position = $index
                    Path = $projectDirectoryMap.$projectKey
                }
            }
        }

        if ($matchedProjects.Count -gt 0) {
            $firstMatch = $matchedProjects | Sort-Object Position | Select-Object -First 1
            $matchedProject = $firstMatch.ProjectKey
            $matchedPath = $firstMatch.Path
        }

        if (-not $matchedProject) {
            Show-Msg "Cannot auto-detect project from title" -Type 'Warning'
            Show-Msg "改用手動選擇 Release 專案" -Type 'Information'

            $config = Load-Config
            $mappings = $config.azureProjectMappings
            $candidateKeys = @()
            if ($mappings) {
                # Release 需要本機路徑跑 npm ci / release.sh，只列有 localPath 的項目
                $candidateKeys = @($mappings.PSObject.Properties.Name | Where-Object { $mappings.$_.localPath } | Sort-Object)
            }
            if ($candidateKeys.Count -eq 0) {
                Show-Msg "azureProjectMappings 沒有含 localPath 的專案可選" -Type 'Error'
                return $null
            }

            $displayItems = @($candidateKeys | ForEach-Object { "$_  ($($mappings.$_.azureProject) / $($mappings.$_.azureRepository))" })
            $selectedDisplay = Select-Helper -data $displayItems
            if (-not $selectedDisplay) { return $null }
            $selectedKey = $candidateKeys[[array]::IndexOf($displayItems, $selectedDisplay)]

            $matchedProject = $selectedKey
            $matchedPath = $mappings.$selectedKey.localPath

            # 標題為空（例如 hotfix 手動選專案）時，存關鍵字沒有意義，略過詢問
            if (-not [string]::IsNullOrWhiteSpace($ReleaseTitle)) {
                $saveKeyword = Read-Host "儲存此對應？輸入標題關鍵字供下次自動比對（直接 Enter 略過，例如 LawUpdate）"
                if (-not [string]::IsNullOrWhiteSpace($saveKeyword)) {
                    $saveKeyword = $saveKeyword.Trim()
                    if (Save-Keyword-Mapping -Keyword $saveKeyword -SourceProjectKey $selectedKey) {
                        Show-Msg "已儲存對應：$saveKeyword -> $($mappings.$selectedKey.azureProject) / $($mappings.$selectedKey.azureRepository)" -Type 'Success'
                    }
                }
            }
        }
    }

    $mapping = Get-Azure-Project-Mapping -ProjectKey $matchedProject
    if ($mapping.LocalPath) {
        $matchedPath = $mapping.LocalPath
    }

    return [PSCustomObject]@{
        ProjectKey = $matchedProject
        ProjectPath = $matchedPath
        AzureProject = $mapping.AzureProject
        Repository = $mapping.AzureRepository
    }
}

function Test-Release-Script {
    param([string]$ProjectPath)

    $srcReleaseScriptPath = Join-Path $ProjectPath "src\release.sh"
    $rootReleaseScriptPath = Join-Path $ProjectPath "release.sh"

    if (Test-Path $srcReleaseScriptPath) { return $srcReleaseScriptPath }
    if (Test-Path $rootReleaseScriptPath) { return $rootReleaseScriptPath }

    Show-Msg "release.sh not found" -Type 'Error'
    return $false
}

function Invoke-Npm-CI {
    param([string]$ProjectPath)

    try {
        Set-Location $ProjectPath

        $srcPackageJsonPath = Join-Path $ProjectPath "src\package.json"
        $rootPackageJsonPath = Join-Path $ProjectPath "package.json"

        if (Test-Path $srcPackageJsonPath) {
            Set-Location (Join-Path $ProjectPath "src")
        }
        elseif (-not (Test-Path $rootPackageJsonPath)) {
            Show-Msg "package.json not found" -Type 'Error'
            return $false
        }

        npm ci
        if ($LASTEXITCODE -ne 0) {
            Show-Msg "npm ci failed" -Type 'Error'
            return $false
        }

        return $true
    }
    catch {
        Show-Msg "npm ci error: $($_.Exception.Message)" -Type 'Error'
        return $false
    }
}

function Invoke-Release-Script {
    param(
        [string]$ProjectPath,
        [string]$ScriptPath,
        [PSCustomObject]$TaskResult,
        [string]$Version = $null
    )

    try {
        if (-not $Version -or [string]::IsNullOrWhiteSpace($Version)) {
            $versionPattern = "v(\d+\.\d+\.\d+)"
            $releaseTitle = $TaskResult.TaskFields.'System.Title'
            if ($releaseTitle -match $versionPattern) {
                $Version = $matches[1]
            }
            else {
                $Version = Read-Host "Please enter release version (e.g. 1.6.2)"
            }
        }

        if ([string]::IsNullOrWhiteSpace($Version)) {
            Show-Msg "Release version cannot be empty" -Type 'Error'
            return $false
        }

        $gitBashPath = "C:\Program Files\Git\bin\bash.exe"
        if (-not (Test-Path $gitBashPath)) {
            Show-Msg "Git Bash not found, cannot execute release.sh" -Type 'Error'
            return $false
        }

        $scriptDir = Split-Path -Path $ScriptPath -Parent
        $scriptFile = Split-Path -Path $ScriptPath -Leaf
        $bashScriptDir = $scriptDir -replace '^([A-Z]):', '/$1' -replace '\\', '/'
        $bashCommand = "cd `"$bashScriptDir`" && ./`"$scriptFile`" $Version"

        & $gitBashPath -c $bashCommand
        if ($LASTEXITCODE -ne 0) {
            Show-Msg "release.sh failed with code: $LASTEXITCODE" -Type 'Error'
            return $false
        }

        $releaseBranches = git branch | Where-Object { $_ -match "release/" }
        if ($releaseBranches) {
            return ($releaseBranches | Sort-Object | Select-Object -Last 1).Trim().Replace("* ", "")
        }

        return $true
    }
    catch {
        Show-Msg "release.sh execution error: $($_.Exception.Message)" -Type 'Error'
        return $false
    }
}

function Start-Release-Process {
    param(
        [PSCustomObject]$TaskResult,
        [string]$ProjectName = $null,
        [string]$Version = $null,
        [switch]$DryRun
    )

    $releaseTitle = $TaskResult.TaskFields.'System.Title'
    $projectInfo = Get-Release-Project-Info -ReleaseTitle $releaseTitle -ProjectName $ProjectName
    if (-not $projectInfo) { return $null }

    if ($DryRun) {
        $simulatedBranch = if ($Version -and -not [string]::IsNullOrWhiteSpace($Version)) { "release/v$Version" } else { "release/dry-run" }
        Show-Msg "[DRY RUN] Skip git/npm/release script execution" -Type 'Warning'
        return [PSCustomObject]@{
            Project = $projectInfo.AzureProject
            Repository = $projectInfo.Repository
            BranchName = $simulatedBranch
            ProjectPath = $projectInfo.ProjectPath
        }
    }

    if (-not (Test-Git-Status -projectPath $projectInfo.ProjectPath)) { return $null }
    if (-not (Update-Develop-Branch -projectPath $projectInfo.ProjectPath)) { return $null }

    $scriptPath = Test-Release-Script -ProjectPath $projectInfo.ProjectPath
    if (-not $scriptPath) { return $null }

    if (-not (Invoke-Npm-CI -ProjectPath $projectInfo.ProjectPath)) { return $null }

    $releaseResult = Invoke-Release-Script -ProjectPath $projectInfo.ProjectPath -ScriptPath $scriptPath -TaskResult $TaskResult -Version $Version
    $actualResult = if ($releaseResult -is [array]) { $releaseResult[-1] } else { $releaseResult }
    if (-not $actualResult -or $actualResult -eq $false) { return $null }

    $sourceBranch = if ($actualResult -is [string] -and $actualResult -ne $true) { $actualResult } else { "release/latest" }

    return [PSCustomObject]@{
        Project = $projectInfo.AzureProject
        Repository = $projectInfo.Repository
        BranchName = $sourceBranch
        ProjectPath = $projectInfo.ProjectPath
    }
}

# Release 分支建好後、建立 PR 前的確認點；取消時保留分支，不建 PR、不發 Slack
function Confirm-Release-Pr {
    param(
        [PSCustomObject]$BranchResult,
        [switch]$DryRun
    )

    if ($DryRun) { return $true }

    Show-Msg "Release 分支已就緒：$($BranchResult.BranchName)（$($BranchResult.Project) / $($BranchResult.Repository)）" -Type 'Information'
    if (Confirm-Action -Message "確認建立 Release PR（→ master / develop）？") {
        return $true
    }

    Show-Msg "已取消建立 PR，Release 分支保留：$($BranchResult.BranchName)" -Type 'Warning'
    return $false
}

function Start-Release-Pr-Process {
    param(
        [string]$AzOrg,
        [int]$TaskId,
        [string]$TaskTitle,
        [PSCustomObject]$BranchResult,
        [string]$OverrideReviewer = "",
        [switch]$SkipSlack,
        [switch]$DryRun,
        [string]$TaskDescription = ""
    )

    $commitHistory = if (-not [string]::IsNullOrWhiteSpace($TaskDescription)) { $TaskDescription } else { "Release: $TaskTitle" }

    $prResultMaster = Start-Pr -DryRun:$DryRun -AzOrg $AzOrg -TaskId $TaskId -SourceBranch $BranchResult.BranchName -TargetBranch "master" -Project $BranchResult.Project -Repository $BranchResult.Repository -TaskDescription $commitHistory -DeleteSourceBranch $false -OverrideReviewer $OverrideReviewer -Silent
    if (-not $prResultMaster) { return $null }

    # develop PR 標題加 -develop 後綴，讓審核者一眼分辨目標分支
    $developPrTitle = "$($BranchResult.BranchName)-develop"
    $prResultDevelop = Start-Pr -DryRun:$DryRun -AzOrg $AzOrg -TaskId $TaskId -SourceBranch $BranchResult.BranchName -TargetBranch "develop" -Project $BranchResult.Project -Repository $BranchResult.Repository -TaskDescription $commitHistory -DeleteSourceBranch $true -OverrideReviewer $OverrideReviewer -Silent -PrTitle $developPrTitle
    if (-not $prResultDevelop) { return $null }

    return "$prResultMaster`n$prResultDevelop"
}

function Start-Manual-Release-Process {
    param(
        [string]$AzOrg,
        [string]$SlackToken,
        [string]$Project = "",
        [string]$ReleaseVersion = "",
        [string]$OverrideReviewer = "",
        [switch]$SkipSlack,
        [switch]$DryRun
    )

    Show-Msg "手動 Release 模式" -Type 'Information'

    $taskWorkItemId = $null
    $taskTitle = ""

    if ($Project -and -not [string]::IsNullOrWhiteSpace($Project) -and $ReleaseVersion -and -not [string]::IsNullOrWhiteSpace($ReleaseVersion)) {
        $taskTitle = "Release $Project v$ReleaseVersion"
        $taskResult = [PSCustomObject]@{
            TaskId = 0
            TaskFields = [PSCustomObject]@{ 'System.Title' = $taskTitle }
            WorkItemType = 'Task'
        }
    }
    else {
        Show-Msg "請選擇要掛載 Release PR 的 Task" -Type 'Information'
        $taskWorkItemId = Get-Task-AssignToMe-ForRelease -AzOrg $AzOrg
        if (-not $taskWorkItemId) { return }

        $taskFields, $null = Get-Az-WorkItem -WorkItemId $taskWorkItemId
        if (-not $taskFields) { return }

        Show-WorkItem-Info -workItemFields $taskFields

        $taskTitle = $taskFields.'System.Title'
        $taskResult = [PSCustomObject]@{
            TaskId = $taskWorkItemId
            TaskFields = $taskFields
            WorkItemType = 'Task'
        }
    }

    $branchResult = Start-Release-Process -TaskResult $taskResult -ProjectName $Project -Version $ReleaseVersion -DryRun:$DryRun
    if (-not $branchResult) {
        Show-Msg "Release 流程失敗" -Type 'Error'
        return
    }

    if (-not (Confirm-Release-Pr -BranchResult $branchResult -DryRun:$DryRun)) { return }

    $prTaskId = if ($taskWorkItemId) { $taskWorkItemId } else { 0 }
    $prResult = Start-Release-Pr-Process -DryRun:$DryRun -AzOrg $AzOrg -TaskId $prTaskId -TaskTitle $taskTitle -BranchResult $branchResult -OverrideReviewer $OverrideReviewer -SkipSlack:$SkipSlack

    if ($prResult -is [string]) {
        if ($SkipSlack) {
            Show-Msg "已啟用 SkipSlack，已產生 PR 連結。" -Type 'Warning'
        }
        else {
            $finalMembers = Get-Slack-Members -slackToken $SlackToken
            if ($finalMembers) {
                Send-Slack-Message -slackToken $SlackToken -members $finalMembers -prResult $prResult | Out-Null
            }
        }

        Show-Msg "手動 Release 流程完成。" -Type 'Success'
    }
}

# 在 hotfix 分支上執行改版本號段（等同 release.sh 後半：npm run release + build:prod + commit + push）
function Invoke-Version-Bump {
    param(
        [string]$ProjectPath,
        [string]$BranchName,
        [string]$Version
    )

    try {
        Set-Location $ProjectPath

        git checkout $BranchName
        if ($LASTEXITCODE -ne 0) {
            Show-Msg "無法切換到 $BranchName" -Type 'Error'
            return $false
        }
        git pull origin $BranchName
        if ($LASTEXITCODE -ne 0) {
            Show-Msg "無法更新 $BranchName" -Type 'Error'
            return $false
        }

        if (-not (Invoke-Npm-CI -ProjectPath $ProjectPath)) { return $false }

        # npm scripts 跑在 package.json 所在目錄（與 Invoke-Npm-CI 同邏輯）
        $npmDir = $ProjectPath
        if (Test-Path (Join-Path $ProjectPath "src\package.json")) {
            $npmDir = Join-Path $ProjectPath "src"
        }
        Set-Location $npmDir

        Show-Msg "更新版本號到 $Version..." -Type 'Process'
        npm run release $Version
        if ($LASTEXITCODE -ne 0) {
            Show-Msg "npm run release 失敗" -Type 'Error'
            return $false
        }

        Show-Msg "建置專案（build:prod）..." -Type 'Process'
        npm run build:prod
        if ($LASTEXITCODE -ne 0) {
            Show-Msg "npm run build:prod 失敗" -Type 'Error'
            return $false
        }

        Set-Location $ProjectPath
        git add .
        git commit -m "release: v$Version"
        if ($LASTEXITCODE -ne 0) {
            Show-Msg "建立改版 commit 失敗（可能沒有變更）" -Type 'Error'
            return $false
        }
        git push origin $BranchName
        if ($LASTEXITCODE -ne 0) {
            Show-Msg "推送改版 commit 失敗" -Type 'Error'
            return $false
        }

        Show-Msg "已建立改版 commit（release: v$Version）並推送" -Type 'Success'
        return $true
    }
    catch {
        Show-Msg "版本更新失敗: $($_.Exception.Message)" -Type 'Error'
        return $false
    }
}

# Hotfix 流程：更新 master -> 從 master 開 hotfix/vX.Y.Z 並推送 -> 等修正 commit 推上來
# -> 改版本號 commit（同 release）-> 確認後開 PR 到 master 與 develop -> Slack
function Start-Hotfix-Process {
    param(
        [string]$AzOrg,
        [string]$SlackToken,
        [string]$Project = "",
        [string]$HotfixVersion = "",
        [string]$OverrideReviewer = "",
        [switch]$SkipSlack,
        [switch]$DryRun
    )

    Show-Msg "Hotfix 模式" -Type 'Information'

    $projectInfo = Get-Release-Project-Info -ReleaseTitle "" -ProjectName $Project
    if (-not $projectInfo) {
        Show-Msg "Hotfix 流程失敗" -Type 'Error'
        return
    }

    if (-not $HotfixVersion -or [string]::IsNullOrWhiteSpace($HotfixVersion)) {
        do {
            $HotfixVersion = Read-Host "請輸入 hotfix 版本號（例如 1.6.3）"
        } until ($HotfixVersion -match '^\d+\.\d+\.\d+$')
    }
    elseif ($HotfixVersion -notmatch '^\d+\.\d+\.\d+$') {
        Show-Msg "版本號格式錯誤：'$HotfixVersion'（需為 x.y.z）" -Type 'Error'
        return
    }

    $branchName = "hotfix/v$HotfixVersion"
    $taskTitle = "Hotfix $($projectInfo.ProjectKey) v$HotfixVersion"

    $branchResult = [PSCustomObject]@{
        Project     = $projectInfo.AzureProject
        Repository  = $projectInfo.Repository
        BranchName  = $branchName
        ProjectPath = $projectInfo.ProjectPath
    }

    $commitHistory = $null
    if ($DryRun) {
        Show-Msg "[DRY RUN] Skip git/npm 操作" -Type 'Warning'
    }
    else {
        if (-not (Test-Git-Status -projectPath $projectInfo.ProjectPath)) { return }

        # 1) 更新 master 並從 master 建立 hotfix 分支（推送到 origin）
        if (-not (Update-Local-Branch -projectPath $projectInfo.ProjectPath -Branch 'master')) { return }
        if (-not (New-Pushed-Branch -projectPath $projectInfo.ProjectPath -BranchName $branchName)) { return }

        # 2) 等修正 commit 推上 hotfix 分支（同子單分支的 commit 檢查迴圈）
        Show-Msg "請將修正 commit push 到 $branchName" -Type 'Warning'
        $commitHistory = Get-Commit-History -Project $projectInfo.AzureProject -Repository $projectInfo.Repository -SourceBranch $branchName -TargetBranch 'master'
        while (-not $commitHistory) {
            Show-Msg "尚未偵測到新 commit，完成 push 後按 Enter 重新檢查。" -Type 'Warning'
            Read-Host "按 Enter 重新檢查"
            $commitHistory = Get-Commit-History -Project $projectInfo.AzureProject -Repository $projectInfo.Repository -SourceBranch $branchName -TargetBranch 'master'
        }

        # 3) 改版本號 commit（等同 release 的版本段）
        if (-not (Invoke-Version-Bump -ProjectPath $projectInfo.ProjectPath -BranchName $branchName -Version $HotfixVersion)) {
            Show-Msg "Hotfix 流程失敗" -Type 'Error'
            return
        }
    }

    # 4) 確認後建立 PR（master + develop）
    if (-not (Confirm-Release-Pr -BranchResult $branchResult -DryRun:$DryRun)) { return }

    $description = if ($commitHistory) { $commitHistory } else { "Hotfix: $taskTitle" }
    $prResult = Start-Release-Pr-Process -DryRun:$DryRun -AzOrg $AzOrg -TaskId 0 -TaskTitle $taskTitle -BranchResult $branchResult -OverrideReviewer $OverrideReviewer -SkipSlack:$SkipSlack -TaskDescription $description
    if (-not $prResult) {
        Show-Msg "Hotfix PR 流程失敗" -Type 'Error'
        return
    }

    if ($prResult -is [string]) {
        if ($SkipSlack) {
            Show-Msg "已啟用 SkipSlack，已產生 PR 連結。" -Type 'Warning'
        }
        else {
            $finalMembers = Get-Slack-Members -slackToken $SlackToken
            if ($finalMembers) {
                Send-Slack-Message -slackToken $SlackToken -members $finalMembers -prResult $prResult | Out-Null
            }
        }

        Show-Msg "Hotfix 流程完成。" -Type 'Success'
    }
}

function Invoke-VeryLazyMain {
    param(
        [int]$WorkItemId,
        [string]$Reviewer = "",
        [switch]$SkipSlack,
        [switch]$SkipReviewer,
        [switch]$DryRun,
        [string[]]$NewTask = @(),
        [switch]$NewPbi
    )

    $AzOrg = Get-AzureOrg
    # 略過 reviewer 時，一併略過 Slack 通知
    $effectiveSkipSlack = $SkipSlack -or $SkipReviewer
    $SlackToken = $null
    if (-not $effectiveSkipSlack) {
        $SlackToken = Get-Slack-Token-With-Prompt
    }

    Start-Az-Login

    if ($NewPbi) {
        $WorkItemId = Start-New-Pbi -AzOrg $AzOrg
        if (-not $WorkItemId) {
            Show-Msg "PBI 建立失敗" -Type 'Error'
            return
        }
    }

    if (-not $WorkItemId) {
        Show-Msg "未提供 WorkItemId，正在查詢指派給您的工作項..." -Type 'Information'
        $WorkItemId = Get-Task-AssignToMe -AzOrg $AzOrg
        if (-not $WorkItemId) {
            Show-Msg "找不到可處理的工作項" -Type 'Error'
            return
        }
    }

    $taskResult = Start-Task -WorkItemId $WorkItemId -NewTask $NewTask
    if (-not $taskResult) {
        Show-Msg "Task 流程失敗" -Type 'Error'
        return
    }

    if ($taskResult.WorkItemType -eq 'Release') {
        $branchResult = Start-Release-Process -TaskResult $taskResult -DryRun:$DryRun
        if (-not $branchResult) {
            Show-Msg "Release 流程失敗" -Type 'Error'
            return
        }

        if (-not (Confirm-Release-Pr -BranchResult $branchResult -DryRun:$DryRun)) { return }

        $prResult = Start-Release-Pr-Process -DryRun:$DryRun -AzOrg $AzOrg -TaskId $taskResult.TaskId -TaskTitle $taskResult.TaskFields.'System.Title' -BranchResult $branchResult -OverrideReviewer $Reviewer -SkipSlack:$SkipSlack
        if (-not $prResult) {
            Show-Msg "Release PR 流程失敗" -Type 'Error'
            return
        }
    }
    else {
        $branchResult = Start-Branch -AzOrg $AzOrg -TaskId $taskResult.TaskId -TaskFields $taskResult.TaskFields
        if (-not $branchResult) {
            Show-Msg "分支流程失敗" -Type 'Error'
            return
        }

        # 目標/基底分支依專案設定（config defaultBranch），無設定預設 develop
        $targetBranch = if ($branchResult.BaseBranch) { $branchResult.BaseBranch } else { 'develop' }

        $commitHistory = Get-Commit-History -Project $branchResult.Project -Repository $branchResult.Repository -SourceBranch $branchResult.BranchName -TargetBranch $targetBranch
        while (-not $commitHistory) {
            Show-Msg "請先完成 commit 並 push，按 Enter 重新檢查。" -Type 'Warning'
            Read-Host "按 Enter 重新檢查"
            $commitHistory = Get-Commit-History -Project $branchResult.Project -Repository $branchResult.Repository -SourceBranch $branchResult.BranchName -TargetBranch $targetBranch
        }

        $prResult = Start-Pr -DryRun:$DryRun -AzOrg $AzOrg -TaskId $taskResult.TaskId -AllTaskIds $taskResult.AllTaskIds -SourceBranch $branchResult.BranchName -TargetBranch $targetBranch -Project $branchResult.Project -Repository $branchResult.Repository -TaskDescription $commitHistory -OverrideReviewer $Reviewer -SkipReviewer:$SkipReviewer
        if (-not $prResult) {
            Show-Msg "PR 流程失敗" -Type 'Error'
            return
        }
    }

    if ($prResult -is [string]) {
        if ($effectiveSkipSlack) {
            $skipReason = if ($SkipReviewer) { "（略過 reviewer，連帶略過 Slack）" } else { "" }
            Show-Msg "已略過 Slack 通知，已產生 PR 連結。$skipReason" -Type 'Warning'
        }
        else {
            $finalMembers = Get-Slack-Members -slackToken $SlackToken
            if ($finalMembers) {
                Send-Slack-Message -slackToken $SlackToken -members $finalMembers -prResult $prResult | Out-Null
            }
            else {
                Show-Msg "無法取得 Slack 成員，略過 Slack 通知" -Type 'Warning'
            }
        }
    }
}
