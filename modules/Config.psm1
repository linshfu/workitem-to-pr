# Config.psm1 - Configuration Management Module

# Module level variables
$script:ConfigFilePath = $null   # config.json       - mappings + slack members (gitignored, kept locally)
$script:SecretFilePath = $null   # config.local.json - secrets (token), never committed
$script:AzureOrg = $null

# Initialize environment variables
function Initialize-Environment {
    param([string]$ConfigRoot = $null)

    if ($ConfigRoot) {
        $configDir = $ConfigRoot
    } else {
        $configDir = Join-Path $PSScriptRoot ".."
    }

    $script:ConfigFilePath = Join-Path $configDir "config.json"
    $script:SecretFilePath = Join-Path $configDir "config.local.json"

    # Org precedence: AZURE_DEVOPS_ORG env var -> config.json "azureOrg"
    $script:AzureOrg = $env:AZURE_DEVOPS_ORG
    if (-not $script:AzureOrg) {
        $config = Load-Config
        if ($config -and $config.ContainsKey('azureOrg') -and $config.azureOrg) {
            $script:AzureOrg = $config.azureOrg
        }
    }
    if (-not $script:AzureOrg) {
        Write-Warning "Azure DevOps organization not set. Set `"azureOrg`" in config.json (e.g. `"https://dev.azure.com/your-org`") or the AZURE_DEVOPS_ORG env var."
    }

    return @{
        AzureOrg = $script:AzureOrg
        ConfigFilePath = $script:ConfigFilePath
        SecretFilePath = $script:SecretFilePath
    }
}

# Get Azure DevOps organization URL
function Get-AzureOrg {
    return $script:AzureOrg
}

# Load configuration file
function Load-Config {
    param([string]$ConfigType)
    
    $configFile = $script:ConfigFilePath
    if (-not $configFile) {
        $configFile = Join-Path $PSScriptRoot "..\config.json"
    }
    
    $config = @{}
    
    if (Test-Path $configFile) {
        try {
            $configContent = Get-Content $configFile -Encoding UTF8 -Raw | ConvertFrom-Json
            $config = @{}
            $configContent.PSObject.Properties | ForEach-Object {
                $config[$_.Name] = $_.Value
            }
        }
        catch {
            Write-Warning "Failed to load config file: $($_.Exception.Message)"
        }
    }
    
    if ($ConfigType -eq "project") {
        if (-not $config.ContainsKey("projectPaths") -or $config.projectPaths.Count -eq 0) {
            Write-Warning "Project paths config not found"
            Write-Host "Please set project path mapping in config.json, e.g.:" -ForegroundColor Cyan
            Write-Host '{' -ForegroundColor Cyan
            Write-Host '  "projectPaths": {' -ForegroundColor Cyan
            Write-Host '    "ProjectName": "C:\\Your\\Project\\Path",' -ForegroundColor Cyan
            Write-Host '    "RepositoryName": "C:\\Your\\Repository\\Path"' -ForegroundColor Cyan
            Write-Host '  }' -ForegroundColor Cyan
            Write-Host '}' -ForegroundColor Cyan
            return $null
        }
        return $config.projectPaths
    }
    elseif ($ConfigType -eq "slackConfig") {
        if (-not $config.ContainsKey("slackConfig")) {
            return $null
        }
        return $config.slackConfig
    }
    
    return $config
}

# Save configuration file
function Save-Config {
    param([hashtable]$Config)
    
    $configFile = $script:ConfigFilePath
    if (-not $configFile) {
        $configFile = Join-Path $PSScriptRoot "..\config.json"
    }
    
    try {
        $configObject = [PSCustomObject]@{}
        foreach ($key in $Config.Keys) {
            if ($key -eq "slackConfig") {
                # Preserve ALL slackConfig keys (e.g. token, channel); only normalize members.
                $slackObject = [PSCustomObject]@{}
                $slackPairs = @{}
                $slackSource = $Config[$key]
                if ($slackSource -is [hashtable]) {
                    foreach ($sk in $slackSource.Keys) { $slackPairs[$sk] = $slackSource[$sk] }
                } elseif ($slackSource -is [PSCustomObject]) {
                    $slackSource.PSObject.Properties | ForEach-Object { $slackPairs[$_.Name] = $_.Value }
                }
                foreach ($sk in $slackPairs.Keys) {
                    if ($sk -eq 'members' -and $slackPairs[$sk]) {
                        $membersArray = @()
                        foreach ($member in $slackPairs[$sk]) {
                            if ($member -is [hashtable]) {
                                $membersArray += [PSCustomObject]@{
                                    key = $member.key
                                    value = $member.value
                                }
                            } else {
                                $membersArray += $member
                            }
                        }
                        $slackObject | Add-Member -MemberType NoteProperty -Name 'members' -Value $membersArray
                    } else {
                        $slackObject | Add-Member -MemberType NoteProperty -Name $sk -Value $slackPairs[$sk]
                    }
                }
                $configObject | Add-Member -MemberType NoteProperty -Name $key -Value $slackObject
            } else {
                if ($Config[$key] -is [hashtable]) {
                    $hashObject = [PSCustomObject]@{}
                    foreach ($hashKey in $Config[$key].Keys) {
                        $hashObject | Add-Member -MemberType NoteProperty -Name $hashKey -Value $Config[$key][$hashKey]
                    }
                    $configObject | Add-Member -MemberType NoteProperty -Name $key -Value $hashObject
                } else {
                    $configObject | Add-Member -MemberType NoteProperty -Name $key -Value $Config[$key]
                }
            }
        }
        
        $configObject | ConvertTo-Json -Depth 10 | Set-Content $configFile -Encoding UTF8
        Write-Host "Config saved to $configFile" -ForegroundColor Green
    }
    catch {
        Write-Warning "Failed to save config file: $($_.Exception.Message)"
    }
}

# ==================== Secrets (config.local.json) ====================

function Get-SlackTokenFromConfig {
    if ($script:SecretFilePath -and (Test-Path $script:SecretFilePath)) {
        try {
            $secret = Get-Content $script:SecretFilePath -Encoding UTF8 -Raw | ConvertFrom-Json
            if ($secret -and ($secret.PSObject.Properties.Name -contains 'slackToken') -and $secret.slackToken) {
                return $secret.slackToken
            }
        }
        catch {
            Write-Warning "Failed to read secret file: $($_.Exception.Message)"
        }
    }
    return $null
}

function Save-SlackTokenToConfig {
    param([string]$Token)

    $secretHash = @{}
    if ($script:SecretFilePath -and (Test-Path $script:SecretFilePath)) {
        try {
            (Get-Content $script:SecretFilePath -Encoding UTF8 -Raw | ConvertFrom-Json).PSObject.Properties |
                ForEach-Object { $secretHash[$_.Name] = $_.Value }
        }
        catch { }
    }
    $secretHash['slackToken'] = $Token

    $secretObject = [PSCustomObject]@{}
    foreach ($k in $secretHash.Keys) {
        $secretObject | Add-Member -MemberType NoteProperty -Name $k -Value $secretHash[$k]
    }
    $secretObject | ConvertTo-Json -Depth 5 | Set-Content $script:SecretFilePath -Encoding UTF8
}

# Notification channel name from config.json slackConfig.channel (no default — every team names theirs differently)
function Get-SlackChannel {
    $slackConfig = Load-Config -ConfigType "slackConfig"
    if ($slackConfig -and ($slackConfig.PSObject.Properties.Name -contains 'channel') -and $slackConfig.channel) {
        return $slackConfig.channel
    }
    return $null
}

# Shared project mapping function
function Get-Azure-Project-Mapping {
    param([string]$ProjectKey)
    
    $config = Load-Config
    $mappings = $config.azureProjectMappings
    
    if (-not $mappings) {
        Write-Warning "azureProjectMappings not found in config, using default mapping"
        return [PSCustomObject]@{
            ProjectKey = $ProjectKey
            AzureProject = $ProjectKey
            AzureRepository = $ProjectKey
            LocalPath = $null
            DefaultBranch = 'develop'
        }
    }
    
    if ($mappings.$ProjectKey) {
        $branch = if ($mappings.$ProjectKey.defaultBranch) { $mappings.$ProjectKey.defaultBranch } else { 'develop' }
        return [PSCustomObject]@{
            ProjectKey = $ProjectKey
            AzureProject = $mappings.$ProjectKey.azureProject
            AzureRepository = $mappings.$ProjectKey.azureRepository
            LocalPath = $mappings.$ProjectKey.localPath
            DefaultBranch = $branch
        }
    }
    
    Write-Warning "Project '$ProjectKey' not found in mapping config, using default mapping"
    return [PSCustomObject]@{
        ProjectKey = $ProjectKey
        AzureProject = $ProjectKey
        AzureRepository = $ProjectKey
        LocalPath = $null
    }
}

# Save a title keyword as an alias of an existing project entry.
# Writes both azureProjectMappings[keyword] (so Get-Azure-Project-Mapping resolves it)
# and projectPaths[keyword] (so the release title matcher hits it next time).
function Save-Keyword-Mapping {
    param(
        [string]$Keyword,
        [string]$SourceProjectKey
    )

    $config = Load-Config
    $mappings = $config.azureProjectMappings
    if (-not $mappings -or -not $mappings.$SourceProjectKey) {
        Write-Warning "Project '$SourceProjectKey' not found in azureProjectMappings, keyword not saved"
        return $false
    }

    $source = $mappings.$SourceProjectKey
    $entry = [PSCustomObject]@{
        azureProject    = $source.azureProject
        azureRepository = $source.azureRepository
    }
    if ($source.localPath) { $entry | Add-Member -MemberType NoteProperty -Name 'localPath' -Value $source.localPath }
    if ($source.defaultBranch) { $entry | Add-Member -MemberType NoteProperty -Name 'defaultBranch' -Value $source.defaultBranch }

    $mappings | Add-Member -MemberType NoteProperty -Name $Keyword -Value $entry -Force

    if ($source.localPath) {
        if (-not $config.ContainsKey('projectPaths') -or -not $config.projectPaths) {
            $config['projectPaths'] = [PSCustomObject]@{}
        }
        $config.projectPaths | Add-Member -MemberType NoteProperty -Name $Keyword -Value $source.localPath -Force
    }

    Save-Config -Config $config
    return $true
}

# Save an areaPath onto an existing azureProjectMappings entry (used by PBI creation)
function Save-Project-AreaPath {
    param(
        [string]$ProjectKey,
        [string]$AreaPath
    )

    $config = Load-Config
    $mappings = $config.azureProjectMappings
    if (-not $mappings -or -not $mappings.$ProjectKey) {
        Write-Warning "Project '$ProjectKey' not found in azureProjectMappings, areaPath not saved"
        return $false
    }

    $mappings.$ProjectKey | Add-Member -MemberType NoteProperty -Name 'areaPath' -Value $AreaPath -Force
    Save-Config -Config $config
    return $true
}

# Validate configuration
function Test-Configuration {
    $errors = @()
    $warnings = @()
    
    if (-not (Get-SlackTokenFromConfig) -and -not $env:SLACK_BOT_TOKEN) {
        $warnings += "No Slack token found (config.local.json or SLACK_BOT_TOKEN env var); you'll be prompted once"
    }
    
    $config = Load-Config
    if (-not $config) {
        $errors += "Cannot load config file"
    } else {
        if (-not $config.projectPaths) {
            $warnings += "Config file missing projectPaths setting"
        }
        if (-not $config.azureProjectMappings) {
            $warnings += "Config file missing azureProjectMappings setting"
        }
        if (-not $env:AZURE_DEVOPS_ORG -and -not ($config.ContainsKey('azureOrg') -and $config.azureOrg)) {
            $errors += "Azure DevOps org not set: add `"azureOrg`" to config.json or set AZURE_DEVOPS_ORG env var"
        }
        if (-not (Get-SlackChannel)) {
            $warnings += "Slack notification channel not set: add `"channel`" under slackConfig in config.json"
        }
    }
    
    if ($errors.Count -gt 0) {
        $errors | ForEach-Object { Write-Host "ERROR: $_" -ForegroundColor Red }
    }
    if ($warnings.Count -gt 0) {
        $warnings | ForEach-Object { Write-Host "WARNING: $_" -ForegroundColor Yellow }
    }
    
    if ($errors.Count -eq 0 -and $warnings.Count -eq 0) {
        Write-Host "Config check passed" -ForegroundColor Green
    }
    
    return $errors.Count -eq 0
}

# Note: Functions are available via dot sourcing in main.ps1
