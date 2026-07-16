# Slack.psm1 - Slack Operations Module

# Dependencies: Config.psm1, UI.psm1 (loaded via dot sourcing in main.ps1)

# ==================== Message Operations ====================

function Show-Slack-Token-Setup-Guidance {
    Show-Msg "Slack Token 可能已過期或失效。" -Type 'Error'
    Show-Msg "請前往 https://api.slack.com/apps" -Type 'Link'
    Show-Msg "選擇你的 App -> OAuth & Permissions -> Bot User OAuth Token" -Type 'Information'
}

function Show-Slack-Channel-Join-Guidance {
    param([string]$channel)
    Show-Msg "請檢查 Slack 的 #$channel 頻道是否已加入你的 bot。" -Type 'Warning'
    Show-Msg "在頻道輸入 /invite @你的BotName，或 hover bot 名稱開啟選單後選擇『將此應用程式新增至頻道』。" -Type 'Information'
}

function Show-Slack-Channel-Config-Guidance {
    Show-Msg "config.json 未設定通知頻道。" -Type 'Error'
    Show-Msg '請在 config.json 的 slackConfig 加上 "channel": "你的頻道名稱"（不含 #）。' -Type 'Information'
}

function Test-Slack-Token-Validity {
    param([string]$slackToken)

    try {
        $authResponse = Invoke-RestMethod -Uri "https://slack.com/api/auth.test" -Method Get -Headers @{
            "Authorization" = "Bearer $slackToken"
        }

        if (-not $authResponse.ok) {
            if ($authResponse.error -in @('invalid_auth', 'token_revoked', 'account_inactive', 'not_authed')) {
                Show-Slack-Token-Setup-Guidance
            }
            else {
                Show-Msg "Slack token 驗證失敗: $($authResponse.error)" -Type 'Error'
            }
            return $false
        }

        return $true
    }
    catch {
        Show-Msg "Slack token 驗證時發生錯誤: $($_.Exception.Message)" -Type 'Error'
        Show-Slack-Token-Setup-Guidance
        return $false
    }
}

function Send-Slack-Message {
    param(
        [string]$slackToken,
        [array]$members,
        [string]$prResult
    )
    $selectedMember = Select-Helper -data $members -keyValue

    if (-not (Test-Slack-Token-Validity -slackToken $slackToken)) {
        return @{ Success = $false }
    }

    $slackChannel = Get-SlackChannel
    if (-not $slackChannel) {
        Show-Slack-Channel-Config-Guidance
        return @{ Success = $false }
    }

    try {
        $selectedMemberInfo = $members | Where-Object { $_.Value -eq $selectedMember }
        $memberKey = $selectedMemberInfo.Key

        Show-Msg "Sending to #$slackChannel channel..." -Type 'Process'
        $slackUrl = "https://slack.com/api/chat.postMessage"
        $slackMessage = "<@$selectedMember> Please help review`n$prResult"

        $bodyJson = @{
            "channel" = $slackChannel
            "text" = $slackMessage
        } | ConvertTo-Json -Depth 10
        
        $channelResponse = Invoke-RestMethod -Uri $slackUrl -Method Post -Headers @{
            "Authorization" = "Bearer $slackToken"
            "Content-Type" = "application/json; charset=utf-8"
        } -Body ([System.Text.Encoding]::UTF8.GetBytes($bodyJson))
        
        if ($channelResponse.ok) {
            Show-Msg "已成功發送 Slack 訊息到 #$slackChannel，通知 $memberKey" -Type 'Success'
            
            return @{
                Success = $true
                MessageTs = $channelResponse.ts
                ChannelId = $channelResponse.channel
            }
        } else {
            Show-Msg "發送 Slack 訊息失敗: $($channelResponse.error)" -Type 'Error'

            if ($channelResponse.error -in @('invalid_auth', 'token_revoked', 'account_inactive', 'not_authed')) {
                Show-Slack-Token-Setup-Guidance
            }
            elseif ($channelResponse.error -in @('channel_not_found', 'not_in_channel')) {
                Show-Slack-Channel-Join-Guidance -channel $slackChannel
            }

            return @{ Success = $false }
        }
    }
    catch {
        Show-Msg "發送 Slack 訊息發生錯誤: $($_.Exception.Message)" -Type 'Error'
        Show-Msg "Please manually notify $memberKey to check PR" -Type 'Information'
        return @{ Success = $false }
    }
}

# ==================== Channel Operations ====================

function Find-Channel-ID {
    param(
        [string]$slackToken,
        [string]$searchKeyword
    )
    
    try {
        $slackUrl = "https://slack.com/api/conversations.list"
        $response = Invoke-RestMethod -Uri $slackUrl -Method Get -Headers @{
            "Authorization" = "Bearer $slackToken"
        } -Body @{
            "types" = "public_channel,private_channel"
            "limit" = 1000
        }
        
        if ($response.ok) {
            $filteredChannels = $response.channels | Where-Object { 
                $_.name -ilike "*$searchKeyword*"
            }
            
            if ($filteredChannels.Count -eq 0) {
                Show-Msg "No channels found containing '$searchKeyword'" -Type 'Error'
                return
            }
            
            Show-Msg "Found $($filteredChannels.Count) related channels:" -Type 'Success'
            foreach ($channel in $filteredChannels) {
                Show-Msg "  $($channel.name) -> $($channel.id)" -Type 'Information'
            }
        } else {
            Show-Msg "Cannot get channel list: $($response.error)" -Type 'Error'
        }
    }
    catch {
        Show-Msg "Error searching channel: $($_.Exception.Message)" -Type 'Error'
    }
}

# ==================== Member Operations ====================

function Initialize-Slack-Config {
    param([string]$slackToken)
    try {
        Show-Msg "Getting all Slack member info..." -Type 'Process'
        $slackUrl = "https://slack.com/api/users.list"
        $slackUsersResponse = Invoke-RestMethod -Uri $slackUrl -Method Get -Headers @{
            "Authorization" = "Bearer $slackToken"
        }
        
        $slackUsers = $slackUsersResponse.members | Where-Object { $_.deleted -eq $false }
        $slackUsers = $slackUsers | Where-Object { $_.profile.display_name -ne $null -and $_.profile.display_name -ne "" }
        $slackUsers = $slackUsers | Where-Object { $_.profile.real_name -notlike "*bot*" -and $_.profile.real_name -notlike "*google*" }
        
        $slackChannel = Get-SlackChannel
        if (-not $slackChannel) {
            Show-Slack-Channel-Config-Guidance
            return $null
        }

        Show-Msg "取得 $slackChannel 頻道成員中..." -Type 'Process'
        $slackUrl = "https://slack.com/api/conversations.list?types=public_channel"
        $slackGroupsResponse = Invoke-RestMethod -Uri $slackUrl -Method Get -Headers @{
            "Authorization" = "Bearer $slackToken"
        }

        $issueReportChannel = $slackGroupsResponse.channels | Where-Object { $_.name -eq $slackChannel }
        if (-not $issueReportChannel) {
            Show-Msg "找不到 $slackChannel 頻道" -Type 'Error'
            Show-Slack-Channel-Join-Guidance -channel $slackChannel
            return $null
        }
        
        $slackUrl = "https://slack.com/api/conversations.members?channel=$($issueReportChannel.id)"
        $slackGroupsMembersResponse = Invoke-RestMethod -Uri $slackUrl -Method Get -Headers @{
            "Authorization" = "Bearer $slackToken"
        }
        
        $availableMembers = @()
        foreach ($id in $slackGroupsMembersResponse.members) {
            $user = $slackUsers | Where-Object { $_.id -eq $id }
            if ($user) {
                $availableMembers += [PSCustomObject]@{
                    Key   = "$($user.profile.display_name) ($($user.profile.real_name))"
                    Value = $user.id
                    Id    = $user.id
                    DisplayName = $user.profile.display_name
                    RealName = $user.profile.real_name
                }
            }
        }
        
        if ($availableMembers.Count -eq 0) {
            Show-Msg "找不到可用的頻道成員" -Type 'Error'
            return $null
        }
        
        Show-Msg "Found $($availableMembers.Count) available members, please select members to keep:" -Type 'Warning'
        
        Show-Msg "`nAvailable members:" -Type 'Information'
        $selectedOption = Select-Helper -data $availableMembers -keyValue -multiple
        
        $slackConfig = @{
            members = @()
        }
        
        foreach ($member in $selectedOption) {
            $memberInfo = $availableMembers | Where-Object { $_.Value -eq $member }
            if ($memberInfo) {
                $slackConfig.members += @{
                    key = $memberInfo.Key
                    value = $memberInfo.Value
                }
            }
        }
        
        Show-Msg "Configured $($selectedOption.Count) Slack members" -Type 'Success'
        return $slackConfig
        
    }
    catch {
        Show-Msg "Failed to initialize Slack config: $($_.Exception.Message)" -Type 'Error'
        return $null
    }
}

function Get-Slack-Members {
    param([string]$slackToken)

    if (-not (Test-Slack-Token-Validity -slackToken $slackToken)) {
        return $null
    }
    
    $slackConfig = Load-Config -ConfigType "slackConfig"
    
    if (-not $slackConfig -or -not $slackConfig.members -or $slackConfig.members.Count -eq 0) {
        Show-Msg "No Slack config found or member list empty, starting setup..." -Type 'Warning'
        $slackConfig = Initialize-Slack-Config -slackToken $slackToken
        if ($slackConfig) {
            $fullConfig = Load-Config
            if (-not $fullConfig) {
                $fullConfig = @{}
            }
            # Merge: keep existing slackConfig keys (e.g. token); only update members
            $existingSlack = $fullConfig['slackConfig']
            $mergedSlack = @{}
            if ($existingSlack -is [PSCustomObject]) {
                $existingSlack.PSObject.Properties | ForEach-Object { $mergedSlack[$_.Name] = $_.Value }
            } elseif ($existingSlack -is [hashtable]) {
                foreach ($k in $existingSlack.Keys) { $mergedSlack[$k] = $existingSlack[$k] }
            }
            $mergedSlack['members'] = $slackConfig.members
            $fullConfig['slackConfig'] = $mergedSlack
            Save-Config -Config $fullConfig
        } else {
            return $null
        }
    }

    $currentUser = az account show --query user.name -o tsv
    $currentUserName = $currentUser -split '@' | Select-Object -First 1

    $slackMembers = $slackConfig.members
    $filteredMembers = @()
    
    if ($slackMembers -and $slackMembers.Count -gt 0) {
        foreach ($member in $slackMembers) {
            if ($member.key -inotmatch [regex]::Escape($currentUserName)) {
                $filteredMembers += $member
            }
        }
    }

    $finalMembers = @()
    foreach ($member in $filteredMembers) {
        $finalMembers += [PSCustomObject]@{
            Key   = $member.key
            Value = $member.value
        }
    }
    
    return $finalMembers
}

# Note: Functions are available via dot sourcing in main.ps1
