# Git.psm1 - Git Operations Module

# Dependencies: Config.psm1, UI.psm1 (loaded via dot sourcing in main.ps1)

# Override az call for UTF-8 encoding
function Invoke-AzCli {
    $Arguments = $args
    
    $azCliPath = (Get-Command -ErrorAction Ignore az.cmd).Path
    if (-not $azCliPath) { throw "'az.cmd' cannot be located via the system's path." }
    $bundledPythonExe = Convert-Path -ErrorAction Ignore -LiteralPath "$azCliPath\..\..\python.exe"
    if (-not $bundledPythonExe) { throw "Failed to load Python executable." }
  
    $prevValue = $env:AZ_INSTALLER; $env:AZ_INSTALLER = 'MSI'
    $prevEncoding = [Console]::OutputEncoding; [Console]::OutputEncoding = [Text.UTF8Encoding]::new()
  
    & $bundledPythonExe -X utf8 -IBm azure.cli @Arguments
  
    [Console]::OutputEncoding = $prevEncoding
    $env:AZ_INSTALLER = $prevValue
}

# Override git call for UTF-8 encoding
function Invoke-GitWithEncoding {
    param(
        [Parameter(ValueFromRemainingArguments = $true)]
        [string[]]$Arguments
    )
    
    $prevEncoding = [Console]::OutputEncoding
    $prevLcAll = $env:LC_ALL
    $prevLang = $env:LANG
    
    try {
        [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()
        $env:LC_ALL = "C.UTF-8"
        $env:LANG = "C.UTF-8"
        
        & git -c core.quotepath=false @Arguments
    }
    finally {
        [Console]::OutputEncoding = $prevEncoding
        if ($prevLcAll) { $env:LC_ALL = $prevLcAll } else { Remove-Item Env:LC_ALL -ErrorAction SilentlyContinue }
        if ($prevLang) { $env:LANG = $prevLang } else { Remove-Item Env:LANG -ErrorAction SilentlyContinue }
    }
}

# Check Git status
function Test-Git-Status {
    param([string]$projectPath)
    
    Show-Msg "Checking Git status..." -Type 'Process'
    
    try {
        Set-Location $projectPath
        $gitStatus = git status --porcelain
        
        if ($gitStatus) {
            Show-Msg "Uncommitted changes found:" -Type 'Error'
            $gitStatus | ForEach-Object { Show-Msg "  $_" -Type 'Information' }
            Show-Msg "Please commit or stash all changes before Release" -Type 'Warning'
            return $false
        } 
        else {
            Show-Msg "Git status clean, no uncommitted changes" -Type 'Success'
            return $true
        }
    }
    catch {
        Show-Msg "Error checking Git status: $($_.Exception.Message)" -Type 'Error'
        return $false
    }
}

# Switch to a branch and pull latest
function Update-Local-Branch {
    param(
        [string]$projectPath,
        [string]$Branch
    )

    Show-Msg "Switching to $Branch branch and updating..." -Type 'Process'

    try {
        Set-Location $projectPath

        git checkout $Branch
        if ($LASTEXITCODE -ne 0) {
            Show-Msg "Failed to switch to $Branch branch" -Type 'Error'
            return $false
        }

        Show-Msg "Pulling latest changes..." -Type 'Process'
        git pull origin $Branch
        if ($LASTEXITCODE -ne 0) {
            Show-Msg "Failed to pull latest changes" -Type 'Error'
            return $false
        }

        Show-Msg "$Branch branch updated to latest" -Type 'Success'
        return $true
    }
    catch {
        Show-Msg "Error updating $Branch branch: $($_.Exception.Message)" -Type 'Error'
        return $false
    }
}

# Switch to develop branch and update
function Update-Develop-Branch {
    param([string]$projectPath)
    return Update-Local-Branch -projectPath $projectPath -Branch 'develop'
}

# Create a new branch from the current HEAD and push it to origin.
# If the branch already exists on origin (e.g. resuming an interrupted hotfix), offer to reuse it.
function New-Pushed-Branch {
    param(
        [string]$projectPath,
        [string]$BranchName
    )

    try {
        Set-Location $projectPath

        git fetch origin 2>$null | Out-Null
        $remoteExists = git branch -r --list "origin/$BranchName"
        if ($remoteExists) {
            Show-Msg "遠端已存在分支 origin/$BranchName" -Type 'Warning'
            if (-not (Confirm-Action -Message "使用既有分支繼續？")) { return $false }
            git checkout $BranchName 2>$null
            if ($LASTEXITCODE -ne 0) {
                git checkout -b $BranchName "origin/$BranchName"
            }
            if ($LASTEXITCODE -ne 0) {
                Show-Msg "無法切換到 $BranchName" -Type 'Error'
                return $false
            }
            git pull origin $BranchName
            if ($LASTEXITCODE -ne 0) {
                Show-Msg "無法更新 $BranchName" -Type 'Error'
                return $false
            }
            return $true
        }

        Show-Msg "從目前分支建立 $BranchName..." -Type 'Process'
        git checkout -b $BranchName
        if ($LASTEXITCODE -ne 0) {
            Show-Msg "本機可能已存在 $BranchName" -Type 'Warning'
            if (-not (Confirm-Action -Message "改用本機既有分支 $BranchName 繼續？")) { return $false }
            git checkout $BranchName
            if ($LASTEXITCODE -ne 0) {
                Show-Msg "無法切換到 $BranchName" -Type 'Error'
                return $false
            }
        }

        git push -u origin $BranchName
        if ($LASTEXITCODE -ne 0) {
            Show-Msg "推送 $BranchName 到 origin 失敗" -Type 'Error'
            return $false
        }

        Show-Msg "分支 $BranchName 已建立並推送到 origin" -Type 'Success'
        return $true
    }
    catch {
        Show-Msg "建立分支錯誤: $($_.Exception.Message)" -Type 'Error'
        return $false
    }
}

# Get commit history
function Get-Commit-History {
    param(
        [string]$Project,
        [string]$Repository,
        [string]$SourceBranch,
        [string]$TargetBranch = 'develop'
    )
    
    $projectDirectoryMap = Load-Config -ConfigType "project"
    if (-not $projectDirectoryMap) {
        return $null
    }

    $projectPath = $null
    
    # 1. Exact match project name
    if ($projectDirectoryMap.PSObject.Properties.Name -contains $Project) {
        $projectPath = $projectDirectoryMap.$Project
    }
    # 2. Exact match repository name
    elseif ($projectDirectoryMap.PSObject.Properties.Name -contains $Repository) {
        $projectPath = $projectDirectoryMap.$Repository
    }
    # 3. Exact match combined name
    elseif ($projectDirectoryMap.PSObject.Properties.Name -contains "$Project-$Repository") {
        $projectPath = $projectDirectoryMap."$Project-$Repository"
    }
    # 4. Split project name by space and match
    else {
        $ProjectArray = $Project -split ' '
        foreach ($proj in $ProjectArray) {
            if ($projectDirectoryMap.PSObject.Properties.Name -contains $proj) {
                $projectPath = $projectDirectoryMap.$proj
                break
            }
        }
    }

    if (-not $projectPath) {
        Show-Msg "No local path mapping found for project '$Project' or repository '$Repository'" -Type 'Warning'
        Show-Msg "Please select corresponding project from config:" -Type 'Information'
        
        $availableProjects = $projectDirectoryMap.PSObject.Properties.Name
        $choice = Select-Helper -data $availableProjects
        
        $projectPath = $projectDirectoryMap.$choice
        $saveChoice = Read-Host "Save this mapping? '$Repository' -> '$choice' (Y/N, default Y)"
        if ($saveChoice -notmatch '^[Nn]') {
            $config = Load-Config
            if (-not $config.projectPaths) {
                $config.projectPaths = @{}
            }
            if ($config.projectPaths -is [PSCustomObject]) {
                $newProjectPaths = @{}
                $config.projectPaths.PSObject.Properties | ForEach-Object {
                    $newProjectPaths[$_.Name] = $_.Value
                }
                $config.projectPaths = $newProjectPaths
            }
            $config.projectPaths[$Repository] = $projectPath
            Save-Config -Config $config
            Show-Msg "Saved path mapping: $Repository -> $choice ($projectPath)" -Type 'Success'
        }
    }

    if (-not (Test-Path $projectPath)) {
        Show-Msg "Project path does not exist: $projectPath" -Type 'Error'
        return $null
    }

    if (-not (Test-Path (Join-Path $projectPath ".git"))) {
        Show-Msg "Path is not a Git repository: $projectPath" -Type 'Error'
        return $null
    }

    try {
        $originalLocation = Get-Location
        Set-Location $projectPath
        
        Invoke-GitWithEncoding fetch origin 2>$null | Out-Null
        
        $branchExists = Invoke-GitWithEncoding branch -r --list "origin/$SourceBranch" 2>$null
        if (-not $branchExists) {
            Show-Msg "Remote branch does not exist: origin/$SourceBranch" -Type 'Error'
            return $null
        }
        
        $commitDiff = Invoke-GitWithEncoding log "origin/$TargetBranch..origin/$SourceBranch" --oneline --no-merges --encoding=UTF-8 2>$null
        
        if ($commitDiff) {
            $commitMessages = @()
            foreach ($line in $commitDiff) {
                if ($line -and $line.Trim() -ne "") {
                    $message = $line -replace '^[a-f0-9]+\s+', ''
                    if ($message -notmatch "^Merged PR" -and 
                        $message -notmatch "^Merge branch" -and
                        $message.Trim() -ne "") {
                        $commitMessages += "- $($message.Trim())"
                    }
                }
            }
            
            if ($commitMessages.Count -gt 0) {
                $commitHistory = $commitMessages -join "`n"
                Show-Msg "Commit 歷史:" -Type 'Process'
                Show-Msg "$commitHistory" -Type 'Warning'
                return $commitHistory
            }
        }
        
        Show-Msg "No commit history found (branch $SourceBranch has no new commits compared to $TargetBranch)" -Type 'Warning'
        return $null
        
    }
    catch {
        Show-Msg "Error getting commit history: $($_.Exception.Message)" -Type 'Error'
        return $null
    }
    finally {
        Set-Location $originalLocation
    }
}

# Note: Functions are available via dot sourcing in main.ps1
