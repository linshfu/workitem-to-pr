# AzureDevOps.psm1 - Azure DevOps Operations Module

# Dependencies: Config.psm1, UI.psm1, Git.psm1 (loaded via dot sourcing in main.ps1)

# Route az calls through UTF-8 wrapper to avoid garbled CJK text in titles/branch names.
function az {
    Invoke-AzCli @args
}

# ==================== Login & Auth ====================

function Start-Az-Login {
    Show-Msg '確認登入中... ' -Type 'Process' -NoNewline
    if (-not (az account show 2>$null)) {
        Show-Msg '尚未登入 Azure CLI，正在登入...' -Type 'Warning'
        az login --allow-no-subscriptions
    }
    Show-Msg '登入成功' -Type 'Success'
}

# ==================== Work Item Operations ====================

function Get-Az-WorkItem {
    param(
        [int]$WorkItemId,
        [switch]$child
    )
    
    if (-not $WorkItemId) {
        do {
            $workItemIdInput = Read-Host '請輸入工作項 ID'
        } until ([int]::TryParse($workItemIdInput, [ref]$WorkItemId))
    }

    if ($child) {
        Show-Msg ", #${WorkItemId}" -Type 'Process' -NoNewline
    }
    else {
        Show-Msg "取得工作項 #${WorkItemId}" -Type 'Process' -NoNewline
    }
    
    try {
        $workItem = az boards work-item show --id $WorkItemId --output json | ConvertFrom-Json
        $workItemFields = $workItem.fields
        $workItemRelations = $workItem.relations
        return $workItemFields, $workItemRelations
    }
    catch {
        Show-Msg "取得工作項 #${WorkItemId} 失敗: $($_.Exception.Message)" -Type 'Error'
        Show-Msg "錯誤細節: $($_.ScriptStackTrace)" -Type 'Debug'
        exit 1
    }
}

function Get-Relations-Detail {
    param(
        [object]$startWorkItemRelations,
        [array]$routeWorkItems
    )
    
    $relationsChild = $startWorkItemRelations | Where-Object { $_.attributes.name -eq 'Child' }
    $taskWorkItem = @()
    $enhancedRelations = @()
    
    foreach ($relation in $relationsChild) {
        $relationId = $relation.url.Split('/')[-1]
        $relationFields, $relationRelations = Get-Az-WorkItem -WorkItemId $relationId -child
        
        $enhancedRelation = [PSCustomObject]@{
            url        = $relation.url
            attributes = $relation.attributes
            fields     = $relationFields
            relations  = $relationRelations
        }
        
        $enhancedRelations += $enhancedRelation
        
        if ($relationFields.'System.WorkItemType' -eq 'Task') {
            $taskWorkItem += $relationFields
        }
    }
    
    if ($taskWorkItem.Count -eq 0 -and $enhancedRelations.Count -gt 0) {
        $nextTaskWorkItem = Select-Helper -data $enhancedRelations
        $updatedRouteWorkItems = $routeWorkItems + $nextTaskWorkItem.fields
        $nextResults, $finalRouteWorkItems = Get-Relations-Detail -startWorkItemRelations $nextTaskWorkItem.relations -routeWorkItems $updatedRouteWorkItems
        return $nextResults, $finalRouteWorkItems
    }
    return $taskWorkItem, $routeWorkItems
}

function New-Task {
    param(
        [int]$ParentWorkItemId,
        [string]$TaskTitle,
        [string]$AreaPath = $null,
        [string]$IterationPath = $null
    )
    try {
        Show-Msg "Creating new Task..." -Type 'Process'
        $currentUser = az account show --query user.name -o tsv
        
        $createParams = @(
            "boards", "work-item", "create"
            "--type", "Task"
            "--title", $TaskTitle
            "--assigned-to", $currentUser
        )
        
        $fields = @()
        if ($AreaPath -and -not [string]::IsNullOrWhiteSpace($AreaPath)) {
            $fields += "System.AreaPath=$AreaPath"
        }
        if ($IterationPath -and -not [string]::IsNullOrWhiteSpace($IterationPath)) {
            $fields += "System.IterationPath=$IterationPath"
        }
        
        if ($fields.Count -gt 0) {
            $createParams += @("--fields")
            $createParams += $fields
        }
        
        $createParams += @("-o", "json")
        
        $createOutput = & az @createParams 2>&1
        $createError = $createOutput | Where-Object { $_ -is [System.Management.Automation.ErrorRecord] }
        
        if ($createError) {
            Show-Msg "Failed to create Task: $createError" -Type 'Error'
            return $null
        }
        
        $createJson = $createOutput | ConvertFrom-Json
        
        if (-not $createJson -or -not $createJson.id) {
            Show-Msg "Failed to create Task: Cannot get Task ID" -Type 'Error'
            return $null
        }
        
        Show-Msg "Creating relationship with parent work item #$ParentWorkItemId..." -Type 'Process'
        $relationOutput = az boards work-item relation add --id $ParentWorkItemId --relation-type child --target-id $createJson.id 2>&1
        $relationError = $relationOutput | Where-Object { $_ -is [System.Management.Automation.ErrorRecord] }
        
        $relationSuccess = $true
        if ($relationError) {
            Show-Msg "Warning: Error creating parent-child relationship: $relationError" -Type 'Warning'
            Show-Msg "Task #$($createJson.id) created but not linked to parent #$ParentWorkItemId" -Type 'Warning'
            $relationSuccess = $false
        } else {
            $verifyParent = az boards work-item show --id $createJson.id -o json | ConvertFrom-Json
            if ($verifyParent -and $verifyParent.relations) {
                $hasParent = $verifyParent.relations | Where-Object { 
                    $_.rel -eq 'System.LinkTypes.Hierarchy-Reverse' -and 
                    $_.url -like "*$ParentWorkItemId*" 
                }
                if ($hasParent) {
                    Show-Msg "Successfully created relationship with parent #$ParentWorkItemId" -Type 'Success'
                } else {
                    Show-Msg "Warning: Cannot verify parent-child relationship" -Type 'Warning'
                    $relationSuccess = $false
                }
            }
        }
        
        if ($relationSuccess) {
            Show-Msg "已建立子 Task: #$($createJson.id)（指派給: $currentUser，已連結父項 #$ParentWorkItemId）" -Type 'Success'
        } else {
            Show-Msg "Task #$($createJson.id) 已建立（指派給: $currentUser），請手動連結父項 #$ParentWorkItemId" -Type 'Warning'
        }
        
        $verifyTask = az boards work-item show --id $createJson.id -o json | ConvertFrom-Json
        if ($verifyTask) {
            $actualArea = $verifyTask.fields.'System.AreaPath'
            $actualIteration = $verifyTask.fields.'System.IterationPath'
            
            if ($AreaPath -and $actualArea -ne $AreaPath) {
                Show-Msg "Warning: Area path may not be set correctly" -Type 'Warning'
                Show-Msg "   Expected: $AreaPath" -Type 'Information'
                Show-Msg "   Actual: $actualArea" -Type 'Information'
            }
            
            if ($IterationPath -and $actualIteration -ne $IterationPath) {
                Show-Msg "Warning: Iteration path may not be set correctly" -Type 'Warning'
                Show-Msg "   Expected: $IterationPath" -Type 'Information'
                Show-Msg "   Actual: $actualIteration" -Type 'Information'
                Show-Msg "   Please manually check Task #$($createJson.id) Iteration" -Type 'Information'
            }
        }
        
        return $createJson.id
    }
    catch {
        Show-Msg "建立 Task 失敗: $($_.Exception.Message)" -Type 'Error'
        return $null
    }
}

# 把 az boards area/iteration 的樹狀結果攤平成完整路徑清單
function Get-Classification-Paths {
    param(
        [object]$Node,
        [string]$Prefix = ""
    )

    $path = if ($Prefix) { "$Prefix\$($Node.name)" } else { $Node.name }
    $paths = @($path)
    if (($Node.PSObject.Properties.Name -contains 'children') -and $Node.children) {
        foreach ($child in $Node.children) {
            $paths += Get-Classification-Paths -Node $child -Prefix $path
        }
    }
    return $paths
}

function New-Pbi {
    param(
        [string]$Title,
        [string]$TeamProject,
        [string]$AreaPath,
        [string]$IterationPath,
        [string]$AssignedTo,
        [string]$AzOrg
    )

    try {
        Show-Msg "建立 PBI..." -Type 'Process'
        $createParams = @(
            "boards", "work-item", "create"
            "--type", "Product Backlog Item"
            "--title", $Title
            "--assigned-to", $AssignedTo
            "--project", $TeamProject
            "--org", $AzOrg
            "--area", $AreaPath
            "--iteration", $IterationPath
            "-o", "json"
        )

        $createOutput = & az @createParams 2>&1
        $createError = $createOutput | Where-Object { $_ -is [System.Management.Automation.ErrorRecord] }
        if ($createError) {
            Show-Msg "建立 PBI 失敗: $createError" -Type 'Error'
            return $null
        }

        $createJson = $createOutput | ConvertFrom-Json
        if (-not $createJson -or -not $createJson.id) {
            Show-Msg "建立 PBI 失敗: 無法取得 PBI ID" -Type 'Error'
            return $null
        }

        Show-Msg "已建立 PBI #$($createJson.id)（指派給: $AssignedTo）" -Type 'Success'
        return $createJson.id
    }
    catch {
        Show-Msg "建立 PBI 失敗: $($_.Exception.Message)" -Type 'Error'
        return $null
    }
}

# 互動式建立 PBI：選專案 -> 輸入標題 -> 自動帶 Area / Iteration(當年當月) / 指派自己
function Start-New-Pbi {
    param([string]$AzOrg)

    $config = Load-Config
    $mappings = $config.azureProjectMappings
    if (-not $mappings) {
        Show-Msg "config.json 缺少 azureProjectMappings，無法選擇專案" -Type 'Error'
        return $null
    }

    # 工作項所在的 team project（Area/Iteration 樹的根），與 repo 所在專案不同
    $wiProject = $null
    if ($config.ContainsKey('workItemProject') -and $config.workItemProject) {
        $wiProject = $config.workItemProject
    }
    if (-not $wiProject) {
        Show-Msg 'config.json 缺少 workItemProject（工作項所在的 team project），無法建立 PBI' -Type 'Error'
        Show-Msg '請在 config.json 加上 "workItemProject": "你的專案名稱"' -Type 'Information'
        return $null
    }

    # 1) 選專案
    Show-Msg "請選擇要建立 PBI 的專案" -Type 'Information'
    $keys = @($mappings.PSObject.Properties.Name | Sort-Object)
    $displayItems = @($keys | ForEach-Object { "$_  ($($mappings.$_.azureProject) / $($mappings.$_.azureRepository))" })
    $selectedDisplay = Select-Helper -data $displayItems
    if (-not $selectedDisplay) { return $null }
    $projectKey = $keys[[array]::IndexOf($displayItems, $selectedDisplay)]
    $mapping = $mappings.$projectKey

    # 2) Area Path：優先用 config 的 areaPath；沒設定就列 Area 樹手動選，並可存回 config
    $areaPath = $null
    if (($mapping.PSObject.Properties.Name -contains 'areaPath') -and $mapping.areaPath) {
        $areaPath = $mapping.areaPath
    }
    else {
        Show-Msg "專案 '$projectKey' 尚未設定 areaPath，列出 $wiProject 的 Area 供選擇..." -Type 'Warning'
        $areaRoot = az boards area project list --project $wiProject --org $AzOrg --depth 5 -o json | ConvertFrom-Json
        if (-not $areaRoot) {
            Show-Msg "取不到 Area 清單" -Type 'Error'
            return $null
        }
        $areaPaths = @(Get-Classification-Paths -Node $areaRoot)
        $areaPath = Select-Helper -data $areaPaths
        if (-not $areaPath) { return $null }

        if (Confirm-Action -Message "將 areaPath '$areaPath' 存到專案 '$projectKey' 供下次自動使用？") {
            if (Save-Project-AreaPath -ProjectKey $projectKey -AreaPath $areaPath) {
                Show-Msg "已儲存 areaPath：$projectKey -> $areaPath" -Type 'Success'
            }
        }
    }

    # 3) 輸入標題
    $title = ''
    do {
        $title = Read-Host "請輸入 PBI 標題"
    } until (-not [string]::IsNullOrWhiteSpace($title))
    $title = $title.Trim()

    # 4) Iteration：自動帶當年當月（<root>\yyyy年\M月），不存在時改手動選
    $now = Get-Date
    $iterationPath = "$wiProject\$($now.Year)年\$($now.Month)月"
    try {
        $iterRoot = az boards iteration project list --project $wiProject --org $AzOrg --depth 2 -o json | ConvertFrom-Json
        $yearNode = $null
        if ($iterRoot -and ($iterRoot.PSObject.Properties.Name -contains 'children') -and $iterRoot.children) {
            $yearNode = $iterRoot.children | Where-Object { $_.name -eq "$($now.Year)年" }
        }
        $monthNode = $null
        if ($yearNode -and ($yearNode.PSObject.Properties.Name -contains 'children') -and $yearNode.children) {
            $monthNode = $yearNode.children | Where-Object { $_.name -eq "$($now.Month)月" }
        }
        if (-not $monthNode) {
            Show-Msg "找不到 Iteration '$iterationPath'，請手動選擇" -Type 'Warning'
            $iterPaths = @(Get-Classification-Paths -Node $iterRoot)
            $iterationPath = Select-Helper -data $iterPaths
            if (-not $iterationPath) { return $null }
        }
    }
    catch {
        Show-Msg "無法驗證 Iteration，直接使用 '$iterationPath'" -Type 'Warning'
    }

    # 5) 指派給自己並建立
    $currentUser = az account show --query user.name -o tsv
    if (-not $currentUser) {
        Show-Msg "無法取得目前使用者" -Type 'Error'
        return $null
    }

    Show-Msg "PBI 內容確認：" -Type 'Information'
    Show-Msg "  標題:      $title" -Type 'Information'
    Show-Msg "  Area:      $areaPath" -Type 'Information'
    Show-Msg "  Iteration: $iterationPath" -Type 'Information'
    Show-Msg "  指派給:    $currentUser" -Type 'Information'
    if (-not (Confirm-Action -Message "確認建立 PBI？")) {
        Show-Msg "已取消建立 PBI" -Type 'Warning'
        return $null
    }

    return New-Pbi -Title $title -TeamProject $wiProject -AreaPath $areaPath -IterationPath $iterationPath -AssignedTo $currentUser -AzOrg $AzOrg
}

function Start-Task {
    param(
        [int]$WorkItemId,
        [string[]]$NewTask = @()
    )

    $workItemFields, $workItemRelations = Get-Az-WorkItem -WorkItemId $WorkItemId
    $workItemType = $workItemFields.'System.WorkItemType'

    if ($workItemType -eq 'Task') {
        Show-Msg "..." -Type 'Process'
        Show-WorkItem-Info -workItemFields $workItemFields
        return [PSCustomObject]@{
            TaskId     = $WorkItemId
            TaskFields = $workItemFields
            WorkItemType = 'Task'
            AllTaskIds = @("$WorkItemId")
        }
    }
    elseif ($workItemType -eq 'Release') {
        Show-Msg "..." -Type 'Process'
        Show-Msg "Detected Release work item" -Type 'Success'
        Show-WorkItem-Info -workItemFields $workItemFields

        return [PSCustomObject]@{
            TaskId     = $WorkItemId
            TaskFields = $workItemFields
            WorkItemType = 'Release'
            AllTaskIds = @("$WorkItemId")
        }
    }

    # Skip task selection and create directly when titles are provided（支援一次建立多張）
    $newTaskTitles = @($NewTask | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | ForEach-Object { $_.Trim() })
    if ($newTaskTitles.Count -gt 0) {
        Show-Msg "..." -Type 'Process'
        Show-WorkItem-Info -workItemFields $workItemFields

        $areaPath = $workItemFields.'System.AreaPath'
        $iterationPath = $workItemFields.'System.IterationPath'

        $createdTasks = @()
        foreach ($title in $newTaskTitles) {
            Show-Msg "建立新 Task: $title" -Type 'Information'
            $newTaskId = New-Task -ParentWorkItemId $WorkItemId -TaskTitle $title -AreaPath $areaPath -IterationPath $iterationPath
            if ($newTaskId) {
                $newTaskFields, $null = Get-Az-WorkItem -WorkItemId $newTaskId -child
                $createdTasks += [PSCustomObject]@{ Id = [int]$newTaskId; Fields = $newTaskFields }
            }
            else {
                Show-Msg "Task creation failed: $title" -Type 'Error'
            }
        }

        if ($createdTasks.Count -eq 0) {
            Show-Msg "Task creation failed" -Type 'Error'
            return $null
        }
        if ($createdTasks.Count -lt $newTaskTitles.Count) {
            Show-Msg "部分 Task 建立失敗（$($createdTasks.Count)/$($newTaskTitles.Count)），已建立的會繼續流程" -Type 'Warning'
        }

        # 列出新建＋既有 Task 供 PR 連結多選；直接 Enter 預設只連結新建的
        $existingTasks = @()
        $taskWorkItems, $null = Get-Relations-Detail -startWorkItemRelations $workItemRelations -routeWorkItems @($workItemFields)
        if ($taskWorkItems) { $existingTasks = @($taskWorkItems) }

        $selectionPool = @()
        foreach ($t in $createdTasks) {
            $selectionPool += [PSCustomObject]@{ Id = $t.Id; Title = $t.Fields.'System.Title'; Fields = $t.Fields; IsNew = $true }
        }
        foreach ($t in $existingTasks) {
            $selectionPool += [PSCustomObject]@{ Id = [int]$t.'System.Id'; Title = $t.'System.Title'; Fields = $t; IsNew = $false }
        }

        $selectedTasks = @($selectionPool | Where-Object { $_.IsNew })
        if ($existingTasks.Count -gt 0) {
            Show-Msg "PR 要連結的 Task（直接 Enter = 只連結新建的）:" -Type 'Warning'
            for ($i = 0; $i -lt $selectionPool.Count; $i++) {
                $mark = if ($selectionPool[$i].IsNew) { "（新建，預設）" } else { "" }
                Show-Msg "  [$i] " -Type 'Information' -NoNewline
                Show-Msg "#$($selectionPool[$i].Id) " -Type 'Warning' -NoNewline
                Show-Msg "- $($selectionPool[$i].Title)$mark" -Type 'Default'
            }
            do {
                $isValid = $true
                $answer = Read-Host "輸入編號（逗號分隔可多選，第一個為分支命名的主要 Task；直接 Enter 用預設）"
                if (-not [string]::IsNullOrWhiteSpace($answer)) {
                    $picked = @()
                    foreach ($part in $answer.Split(',')) {
                        $idx = 0
                        if (-not [int]::TryParse($part.Trim(), [ref]$idx) -or $idx -lt 0 -or $idx -ge $selectionPool.Count) {
                            Show-Msg "無效的編號: $part" -Type 'Error'
                            $isValid = $false
                            break
                        }
                        if (-not ($picked | Where-Object { $_.Id -eq $selectionPool[$idx].Id })) {
                            $picked += $selectionPool[$idx]
                        }
                    }
                    if ($isValid) { $selectedTasks = $picked }
                }
            } while (-not $isValid)
        }

        $primary = $selectedTasks[0]
        return [PSCustomObject]@{
            TaskId     = $primary.Id
            TaskFields = $primary.Fields
            WorkItemType = 'Task'
            AllTaskIds = @($selectedTasks | ForEach-Object { "$($_.Id)" })
        }
    }

    $taskWorkItems, $routeWorkItems = Get-Relations-Detail -startWorkItemRelations $workItemRelations -routeWorkItems @($workItemFields)
    
    if ($taskWorkItems -and $taskWorkItems.Count -gt 0) {
        Show-Msg "..." -Type 'Process'
        Show-WorkItem-Info -workItemFields $workItemFields
        
        $indentLevel = 1
        if ($routeWorkItems -and $routeWorkItems.Count -gt 1) {
            for ($i = 1; $i -lt $routeWorkItems.Count; $i++) {
                $routeWorkItem = $routeWorkItems[$i]
                $indent = "  " * $indentLevel + "-> "
                Show-Msg $indent -Type 'Process' -NoNewline
                Show-WorkItem-Info -workItemFields $routeWorkItem
                $indentLevel++
            }
        }
        
        foreach ($task in $taskWorkItems) {
            $indent = "  " * $indentLevel + "->  "
            Show-Msg $indent -Type 'Process' -NoNewline
            Show-WorkItem-Info -workItemFields $task
        }
        
        if ($taskWorkItems.Count -eq 1) {
            $selectedTask = $taskWorkItems[0]
        }
        else {
            Show-Msg "Please select Task:" -Type 'Warning'
            
            Show-Msg "Please select:" -Type 'Warning'
            for ($i = 0; $i -lt $taskWorkItems.Count; $i++) {
                $task = $taskWorkItems[$i]
                Show-Msg "  [$i] " -Type 'Information' -NoNewline
                Show-Msg "#$($task.'System.Id') " -Type 'Warning' -NoNewline
                Show-Msg "- $($task.'System.Title')" -Type 'Default'
            }
            Show-Msg "  [N] " -Type 'Information' -NoNewline
            Show-Msg "Create new Task" -Type 'Success'
            
            do {
                $selectedIndex = Read-Host "Enter item number (0-$($taskWorkItems.Count-1) or N, default 0)"
                
                if ($selectedIndex -match '^[Nn]$') {
                    Show-Msg "Creating new Task..." -Type 'Information'
                    
                    $taskTitle = $workItemFields.'System.Title'
                    $areaPath = $workItemFields.'System.AreaPath'
                    $iterationPath = $workItemFields.'System.IterationPath'
                    Show-Msg "Suggested Task title: $taskTitle (Area: $areaPath, Iteration: $iterationPath)" -Type 'Information'
                    
                    $titleChoice = Read-Host "Use this title? (Y/E=Edit, default Y)"
                    if ($titleChoice -match '^[Ee]') {
                        $customTitle = Read-Host "Enter Task title"
                        if (-not [string]::IsNullOrWhiteSpace($customTitle)) {
                            $taskTitle = $customTitle.Trim()
                        }
                    }
                    
                    $newTaskId = New-Task -ParentWorkItemId $WorkItemId -TaskTitle $taskTitle -AreaPath $areaPath -IterationPath $iterationPath
                    
                    if ($newTaskId) {
                        $newTaskFields, $newTaskRelations = Get-Az-WorkItem -WorkItemId $newTaskId -child

                        return [PSCustomObject]@{
                            TaskId     = $newTaskId
                            TaskFields = $newTaskFields
                            WorkItemType = 'Task'
                            AllTaskIds = @("$newTaskId")
                        }
                    } else {
                        Show-Msg "Task creation failed" -Type 'Error'
                        return $null
                    }
                }
                
                $indexValue = 0
                if ([string]::IsNullOrWhiteSpace($selectedIndex)) {
                    $indexValue = 0
                } elseif ([int]::TryParse($selectedIndex, [ref]$indexValue)) {
                    if ($indexValue -lt 0 -or $indexValue -ge $taskWorkItems.Count) {
                        Show-Msg "Invalid selection, please try again" -Type 'Error'
                        $selectedIndex = $null
                    }
                } else {
                    Show-Msg "Invalid selection, please try again" -Type 'Error'
                    $selectedIndex = $null
                }
            } while ($null -eq $selectedIndex)
            
            $selectedTask = $taskWorkItems[$indexValue]
        }
        
        return [PSCustomObject]@{
            TaskId     = $selectedTask.'System.Id'
            TaskFields = $selectedTask
            WorkItemType = 'Task'
            AllTaskIds = @("$($selectedTask.'System.Id')")
        }
    }
    else {
        Show-Msg "No Task found" -Type 'Warning'
        $createChoice = Read-Host "Create new Task? (Y/N, default Y)"
        
        if ($createChoice -notmatch '^[Nn]') {
            $taskTitle = $workItemFields.'System.Title'
            $areaPath = $workItemFields.'System.AreaPath'
            $iterationPath = $workItemFields.'System.IterationPath'
            Show-Msg "Suggested Task title: $taskTitle (Area: $areaPath, Iteration: $iterationPath)" -Type 'Information'
            
            $titleChoice = Read-Host "Use this title? (Y/E=Edit, default Y)"
            if ($titleChoice -match '^[Ee]') {
                $customTitle = Read-Host "Enter Task title"
                if (-not [string]::IsNullOrWhiteSpace($customTitle)) {
                    $taskTitle = $customTitle.Trim()
                }
            }
            
            $newTaskId = New-Task -ParentWorkItemId $WorkItemId -TaskTitle $taskTitle -AreaPath $areaPath -IterationPath $iterationPath
            if ($newTaskId) {
                $newTaskFields, $null = Get-Az-WorkItem -WorkItemId $newTaskId
                return [PSCustomObject]@{
                    TaskId     = $newTaskId
                    TaskFields = $newTaskFields
                    WorkItemType = 'Task'
                    AllTaskIds = @("$newTaskId")
                }
            }
        }
        
        Show-Msg "No Task selected or created, cannot continue" -Type 'Error'
        return $null
    }
}

function Get-Task-AssignToMe {
    param([string]$AzOrg)
    
    try {
        Show-Msg "Getting current user info..." -Type 'Process'
        $currentUser = az account show --query user.name -o tsv
        if (-not $currentUser) {
            Show-Msg "Cannot get current user info" -Type 'Error'
            return $null
        }
        
        Show-Msg "Current user: $currentUser" -Type 'Information'
        
        Show-Msg "Searching for your assigned work items..." -Type 'Process'
        $wiql = "SELECT [System.Id], [System.Title], [System.WorkItemType], [System.State], [System.AssignedTo] FROM WorkItems WHERE [System.AssignedTo] = '$currentUser' AND [System.State] <> 'Closed' AND [System.State] <> 'Removed' AND [System.State] <> 'Done' ORDER BY [System.Id] DESC"
        
        $queryResult = az boards query --wiql "$wiql" --org $AzOrg -o json | ConvertFrom-Json
        
        if (-not $queryResult -or -not $queryResult.workItems -or $queryResult.workItems.Count -eq 0) {
            Show-Msg "No work items assigned to you found" -Type 'Warning'
            return $null
        }

        $selectedWorkItem = Select-Helper -data $queryResult
        
        Show-Msg "Selected work item: #$($selectedWorkItem.id) - $($selectedWorkItem.fields.'System.Title')" -Type 'Success'
        return $selectedWorkItem.id
        
    }
    catch {
        Show-Msg "Error searching for your work items: $($_.Exception.Message)" -Type 'Error'
        return $null
    }
}

function Get-Task-AssignToMe-ForRelease {
    param([string]$AzOrg)
    
    try {
        Show-Msg "Getting current user info..." -Type 'Process'
        $currentUser = az account show --query user.name -o tsv
        if (-not $currentUser) {
            Show-Msg "Cannot get current user info" -Type 'Error'
            return $null
        }
        
        Show-Msg "Current user: $currentUser" -Type 'Information'
        
        Show-Msg "Searching for your assigned Task work items..." -Type 'Process'
        $wiql = "SELECT [System.Id], [System.Title], [System.WorkItemType], [System.State], [System.AssignedTo] FROM WorkItems WHERE [System.AssignedTo] = '$currentUser' AND [System.WorkItemType] = 'Task' AND [System.State] <> 'Closed' AND [System.State] <> 'Removed' AND [System.State] <> 'Done' ORDER BY [System.Id] DESC"
        
        $queryResult = az boards query --wiql "$wiql" --org $AzOrg -o json | ConvertFrom-Json
        
        if (-not $queryResult -or -not $queryResult.workItems -or $queryResult.workItems.Count -eq 0) {
            Show-Msg "No Task work items assigned to you found" -Type 'Warning'
            return $null
        }

        $workItemsForSelection = @()
        foreach ($workItem in $queryResult.workItems) {
            $fullWorkItem = az boards work-item show --id $workItem.id -o json | ConvertFrom-Json
            $workItemsForSelection += $fullWorkItem
        }
        
        $selectedWorkItem = Select-Helper -data $workItemsForSelection
        
        if ($selectedWorkItem) {
            $selectedWorkItemId = $selectedWorkItem.fields.'System.Id'
            $selectedWorkItemTitle = $selectedWorkItem.fields.'System.Title'
            Show-Msg "Selected work item: #$selectedWorkItemId - $selectedWorkItemTitle" -Type 'Success'
            return $selectedWorkItemId
        }
        
        return $null
        
    }
    catch {
        Show-Msg "Error searching for your Task work items: $($_.Exception.Message)" -Type 'Error'
        return $null
    }
}

# ==================== Project & Repository Operations ====================

function Get-Project-And-Repo {
    param(
        [string]$AzOrg,
        [string]$WorkItemTitle,
        [string]$AreaPath = ""
    )

    Show-Msg "分析工作項專案資訊..." -Type 'Process'
    Show-Msg "工作項標題: $WorkItemTitle" -Type 'Information'
    if ($AreaPath) { Show-Msg "Area Path: $AreaPath" -Type 'Information' }

    $config = Load-Config
    $mappings = $config.azureProjectMappings

    $selectedProject = $null
    $selectedRepo = $null

    if ($mappings) {
        $mappingKeys = @($mappings.PSObject.Properties.Name)

        # 1) Most reliable: match the mapping's configured areaPath (exact or parent of the
        #    work item's Area Path), falling back to azureProject vs Area Path segments.
        #    Only auto-pick the repo when it is unambiguous (one repo for that project).
        if (-not $selectedProject -and $AreaPath) {
            $areaSegments = @($AreaPath -split '[\\/]' | Where-Object { $_ -and $_.Trim() -ne '' })
            $areaCandidates = @()
            foreach ($projectKey in $mappingKeys) {
                $m = $mappings.$projectKey
                $cfgArea = if (($m.PSObject.Properties.Name -contains 'areaPath') -and $m.areaPath) { $m.areaPath } else { $null }
                if ($cfgArea -and (($AreaPath -ieq $cfgArea) -or ($AreaPath -ilike "$cfgArea\*"))) {
                    $areaCandidates += $m
                }
                elseif ($m.azureProject -and ($areaSegments -contains $m.azureProject)) {
                    $areaCandidates += $m
                }
            }
            $distinctRepos = @($areaCandidates | Select-Object -ExpandProperty azureRepository -Unique)
            if ($distinctRepos.Count -eq 1) {
                $selectedProject = $areaCandidates[0].azureProject
                $selectedRepo = $areaCandidates[0].azureRepository
                Show-Msg "找到映射（依 Area Path）: $selectedProject / $selectedRepo" -Type 'Success'
            }
            elseif ($distinctRepos.Count -gt 1) {
                Show-Msg "Area Path 對應到多個儲存庫，改用手動選擇 repo" -Type 'Warning'
            }
        }

        # 2) Match [Tag] prefixes in the title against key / azureProject / aliases.
        if (-not $selectedProject) {
            $tags = @([regex]::Matches($WorkItemTitle, '\[([^\]]+)\]') | ForEach-Object { $_.Groups[1].Value.Trim() })
            foreach ($projectKey in $mappingKeys) {
                $m = $mappings.$projectKey
                $candidates = @($projectKey, $m.azureProject)
                if (($m.PSObject.Properties.Name -contains 'aliases') -and $m.aliases) { $candidates += @($m.aliases) }
                $hit = $false
                foreach ($tag in $tags) {
                    foreach ($cand in $candidates) {
                        if ($cand -and ($tag -ieq $cand)) { $hit = $true; break }
                    }
                    if ($hit) { break }
                }
                if ($hit) {
                    $selectedProject = $m.azureProject
                    $selectedRepo = $m.azureRepository
                    Show-Msg "找到映射（依標題標籤）: $projectKey -> $selectedProject / $selectedRepo" -Type 'Success'
                    break
                }
            }
        }

        # 3) Legacy fallback: substring match on the whole title.
        #    Skip keys shorter than 3 chars so e.g. 'AT' no longer matches 'st[at]us'.
        if (-not $selectedProject) {
            $orderedKeys = @($mappingKeys | Sort-Object { $_.Length } -Descending)
            foreach ($projectKey in $orderedKeys) {
                if ($projectKey.Length -lt 3) { continue }
                if ($WorkItemTitle -ilike "*$projectKey*") {
                    $m = $mappings.$projectKey
                    $selectedProject = $m.azureProject
                    $selectedRepo = $m.azureRepository
                    Show-Msg "找到映射（依標題關鍵字）: $projectKey -> $selectedProject / $selectedRepo" -Type 'Success'
                    break
                }
            }
        }
    }
    
    if (-not $selectedProject) {
        Show-Msg "Warning: Mapping match failed, using traditional project selection" -Type 'Warning'
        
        Show-Msg "Getting projects... " -Type 'Process' -NoNewline
        $projects = az devops project list --org $AzOrg -o json | ConvertFrom-Json | Select-Object -ExpandProperty value

        # 手動選單排除非 repo 用途的專案（wildcard 比對），config.json 的 projectListExcludes 設定
        $fullConfig = Load-Config
        if ($fullConfig -and $fullConfig.ContainsKey('projectListExcludes') -and $fullConfig.projectListExcludes) {
            foreach ($excludePattern in $fullConfig.projectListExcludes) {
                $projects = $projects | Where-Object { $_.name -notlike $excludePattern }
            }
        }
        
        $sortedProjects = $projects | Sort-Object { $_.name.Length } -Descending
        
        foreach ($project in $sortedProjects) {
            $ProjectArray = $project.name -split ' '
            foreach ($projectName in $ProjectArray) {
                if ($WorkItemTitle -like "*$($projectName)*") {
                    $selectedProject = $project.name
                    break
                }
            }
        }

        do {
            if (-not $selectedProject) {
                $filteredProjects = $sortedProjects | Select-Object -ExpandProperty name
                $filteredProjects += "< View all projects >"
                $selectedProject = Select-Helper -data $filteredProjects
                
                if ($selectedProject -eq "< View all projects >") {
                    $allProjects = az devops project list --org $AzOrg -o json | ConvertFrom-Json | Select-Object -ExpandProperty value
                    $allProjectNames = $allProjects | Select-Object -ExpandProperty name
                    $selectedProject = Select-Helper -data $allProjectNames
                }
            }

            $selectedRepo = Get-Repository -selectedProject $selectedProject -AzOrg $AzOrg
            
            if ($selectedRepo -eq "RESELECT_PROJECT") {
                $selectedProject = $null
                $selectedRepo = $null
            }
        } while ($selectedRepo -eq "RESELECT_PROJECT" -or $selectedRepo -eq $null)

        # Offer to save the manually-selected project/repo mapping for future auto-matching
        $saveKeyword = Read-Host "儲存此對應？輸入關鍵字供下次自動比對（直接 Enter 略過，例如 AT）"
        if (-not [string]::IsNullOrWhiteSpace($saveKeyword)) {
            $saveKeyword = $saveKeyword.Trim()
            $cfg = Load-Config
            $cfgHash = @{}
            if ($cfg -is [PSCustomObject]) {
                $cfg.PSObject.Properties | ForEach-Object { $cfgHash[$_.Name] = $_.Value }
            } else {
                $cfgHash = $cfg
            }

            if (-not $cfgHash.ContainsKey('azureProjectMappings')) {
                $cfgHash['azureProjectMappings'] = @{}
            }
            if ($cfgHash['azureProjectMappings'] -is [PSCustomObject]) {
                $newMappings = @{}
                $cfgHash['azureProjectMappings'].PSObject.Properties | ForEach-Object { $newMappings[$_.Name] = $_.Value }
                $cfgHash['azureProjectMappings'] = $newMappings
            }

            $newEntry = [PSCustomObject]@{ azureProject = $selectedProject; azureRepository = $selectedRepo }

            # Auto-fill localPath from projectPaths if available
            $pp = $cfgHash['projectPaths']
            if ($pp) {
                $lp = $null
                if ($pp -is [PSCustomObject] -and $pp.PSObject.Properties[$selectedRepo]) { $lp = $pp.$selectedRepo }
                elseif ($pp -is [hashtable] -and $pp.ContainsKey($selectedRepo)) { $lp = $pp[$selectedRepo] }
                if ($lp) { $newEntry | Add-Member -MemberType NoteProperty -Name 'localPath' -Value $lp }
            }

            $cfgHash['azureProjectMappings'][$saveKeyword] = $newEntry
            Save-Config -Config $cfgHash
            Show-Msg "已儲存對應：$saveKeyword -> $selectedProject / $selectedRepo" -Type 'Success'
        }
    }

    # 解析基底/目標分支：依選定 repo 對應（同時涵蓋自動比對與互動選擇兩種路徑），無設定預設 develop
    $baseBranch = 'develop'
    if ($mappings) {
        foreach ($mappingKey in $mappings.PSObject.Properties.Name) {
            $m = $mappings.$mappingKey
            if ($m.azureRepository -eq $selectedRepo -and $m.defaultBranch) {
                $baseBranch = $m.defaultBranch
                break
            }
        }
    }

    return [PSCustomObject]@{
        Project    = $selectedProject
        Repository = $selectedRepo
        BaseBranch = $baseBranch
    }
}

function Get-Repository {
    param(
        [string]$selectedProject,
        [string]$AzOrg
    )

    Show-Msg "Getting project repositories... " -Type 'Process' -NoNewline
    $repositories = az repos list --project $selectedProject --org $AzOrg -o json | ConvertFrom-Json

    $webRepos = $repositories | Where-Object { ($_.name -like "*Web*" -or $_.name -like "*Front*") -and $_.name -notlike "*Japan*" }
    $webRepos = @($webRepos)

    $selectedRepo = $null
    if ($webRepos.Count -gt 1) {
        $webRepoNames = $webRepos | Select-Object -ExpandProperty name
        $webRepoNames += "< Reselect project >"
        $selectedRepo = Select-Helper -data $webRepoNames
        if ($selectedRepo -eq "< Reselect project >") {
            return "RESELECT_PROJECT"
        }
    }
    elseif ($webRepos.Count -eq 1) {
        $webRepoName = $webRepos[0].name
        $selectedRepo = $webRepoName
    }
    else {
        $allRepoNames = $repositories | Select-Object -ExpandProperty name
        if ($allRepoNames.Count -eq 0) {
            Show-Msg "No repositories available for this project" -Type 'Warning'
            return $null
        }
        $allRepoNames += "< Reselect project >"
        $selectedRepo = Select-Helper -data $allRepoNames
        if ($selectedRepo -eq "< Reselect project >") {
            return "RESELECT_PROJECT"
        }
    }
    return $selectedRepo
}

# ==================== Branch Operations ====================

function Start-Branch {
    param(
        [string]$AzOrg,
        [int]$TaskId,
        [object]$TaskFields
    )
    
    $projectResult = Get-Project-And-Repo -AzOrg $AzOrg -WorkItemTitle $TaskFields.'System.Title' -AreaPath $TaskFields.'System.AreaPath'
    
    if (-not $projectResult) {
        Show-Msg "Project selection failed" -Type 'Error'
        return $null
    }
    
    $branchResult = New-Branch -TaskId $TaskId -TaskTitle $TaskFields.'System.Title' -Project $projectResult.Project -Repository $projectResult.Repository -AzOrg $AzOrg -BaseBranch $projectResult.BaseBranch

    if (-not $branchResult) {
        Show-Msg "Branch creation failed" -Type 'Error'
        return $null
    }

    return [PSCustomObject]@{
        Project    = $projectResult.Project
        Repository = $projectResult.Repository
        BranchName = $branchResult.BranchName
        BaseBranch = $projectResult.BaseBranch
    }
}

function New-Branch {
    param(
        [int]$TaskId,
        [string]$TaskTitle,
        [string]$Project,
        [string]$Repository,
        [string]$AzOrg,
        [string]$BaseBranch = 'develop'
    )

    Show-Msg "檢查可用分支..." -Type 'Process'
    
    $allBranches = az repos ref list --repository $Repository --project $Project --org $AzOrg --filter "heads" -o json | ConvertFrom-Json

    $similarBranches = @()
    foreach ($branch in $allBranches) {
        $branchName = $branch.name -replace '^refs/heads/', ''
        
        if ($branchName -like "*$TaskId*" -and $branchName -notmatch "^release/") {
            $similarBranches += [PSCustomObject]@{
                Name     = $branchName
                ObjectId = $branch.objectId
            }
        }
    }

    $selectedBranchName = $null
    if ($similarBranches.Count -gt 1) {
        $similarBranchNames = $similarBranches | Select-Object -ExpandProperty Name
        $selectedBranchName = Select-Helper -data $similarBranchNames
    }
    elseif ($similarBranches.Count -eq 1) {
        $selectedBranchName = $similarBranches[0].Name
    }
    elseif ($similarBranches.Count -eq 0) {
        $cleanTitle = $TaskTitle -replace '^(\[.*?\]\s*)+', ''
        $cleanTitle = $cleanTitle -replace '[,]', ''
        $cleanTitle = $cleanTitle -replace '[()（）\[\]【】]', ''
        $cleanTitle = $cleanTitle -replace '[&]', 'and'
        $cleanTitle = $cleanTitle -replace '[\\/]', ''   # strip slashes so the title doesn't add extra branch hierarchy
        $cleanTitle = $cleanTitle -replace '\s+', ''
        $cleanTitle = $cleanTitle -replace '-+', '-' -replace '^-|-$', ''
        
        if ($cleanTitle.Length -gt 50) {
            $cleanTitle = $cleanTitle.Substring(0, 50)
        }
        $suggestedBranchName = "task/$TaskId-$cleanTitle"

        Show-Msg "No available branch, suggested branch name: " -Type 'Information' -NoNewline
        Show-Msg "$suggestedBranchName" -Type 'Default'
    
        $ans = Read-Host "Use this branch name? (Y/N/E=Edit, default Y)"
        switch ($ans) {
            { $_ -match '^[Ee]$' } {
                $prefix = "task/$TaskId-"
                do {
                    $customName = Read-Host "New branch name"
                    if ($customName -notlike "$prefix*") {
                        $customName = "$prefix$customName"
                    }
                    $customName = $customName -replace '[<>:"|?*\\]+', ''
                    if ($customName.Length -lt $prefix.Length + 3) {
                        Show-Msg "Branch name too short, please include at least 3 characters description" -Type 'Error'
                    }
                } while ($customName.Length -lt $prefix.Length + 3)
                $selectedBranchName = $customName
            }
            { $_ -match '^[Nn]$' } {
                Show-Msg "Branch creation cancelled" -Type 'Warning'
                return $null
            }
            default {
                $selectedBranchName = $suggestedBranchName
            }
        }
    
        $preferredBranch = $allBranches | Where-Object { $_.name -eq "refs/heads/$BaseBranch" }
        if ($preferredBranch) {
            $baseBranchRef = $preferredBranch
        }
        else {
            if ($BaseBranch) {
                Show-Msg "Configured base branch '$BaseBranch' not found, falling back to develop/main/master" -Type 'Warning'
            }
            $developBranch = $allBranches | Where-Object { $_.name -eq 'refs/heads/develop' }
            $mainBranch = $allBranches | Where-Object { $_.name -eq 'refs/heads/main' }
            $masterBranch = $allBranches | Where-Object { $_.name -eq 'refs/heads/master' }

            if ($developBranch) {
                $baseBranchRef = $developBranch
            }
            elseif ($mainBranch) {
                $baseBranchRef = $mainBranch
            }
            elseif ($masterBranch) {
                $baseBranchRef = $masterBranch
            }
            else {
                Show-Msg "Cannot find branch '$BaseBranch', develop, main or master" -Type 'Error'
                return $null
            }
        }
    
        Show-Msg "Creating remote branch: $selectedBranchName..." -Type 'Process'
        try {
            $branchRef = "refs/heads/$selectedBranchName"
            az repos ref create --name $branchRef --object-id $baseBranchRef.objectId --repository $Repository --project $Project --org $AzOrg | Out-Null
        }
        catch {
            Show-Msg "Failed to create branch: $($_.Exception.Message)" -Type 'Error'
            return $null
        }
    }

    return [PSCustomObject]@{
        BranchName = $selectedBranchName
    }
}

# ==================== PR Operations ====================

function Select-Reviewer {
    param(
        [string]$AzOrg,
        [string]$Project,
        [string]$OverrideReviewer = ""
    )

    if (-not [string]::IsNullOrWhiteSpace($OverrideReviewer)) {
        Show-Msg "Using specified reviewer: $OverrideReviewer" -Type 'Information'
        return $OverrideReviewer
    }

    $teams = az devops team list --org $AzOrg --project $Project -o json | ConvertFrom-Json
    $teamNames = $teams | Select-Object -ExpandProperty name
    $selectedTeam = Select-Helper -data $teamNames
    $reviewers = az devops team list-member --team "$selectedTeam" --org $AzOrg --project $Project -o json | ConvertFrom-Json
    $reviewerNames = $reviewers | Select-Object -ExpandProperty identity | Select-Object -ExpandProperty uniqueName

    $slackConfig = Load-Config -ConfigType "slackConfig"
    $slackMembers = $slackConfig.members
    if ($slackMembers -and $slackMembers.Count -gt 0) {
        $reviewerNames = $reviewerNames | Where-Object { 
            $currentReviewer = $_
            $userName = $currentReviewer -split '@' | Select-Object -First 1
            $found = $false
            foreach ($member in $slackMembers) {
                if ($member.key -imatch [regex]::Escape($userName)) {
                    $found = $true
                    break
                }
            }
            return $found
        }
    } else {
        Show-Msg "slackMembers empty or not found, skipping Slack member filter" -Type 'Warning'
    }
    $currentUserEmail = az account show --query user.name -o tsv
    $reviewerNames = $reviewerNames | Where-Object { $_ -ne $currentUserEmail }
    $selectedReviewer = Select-Helper -data $reviewerNames

    return $selectedReviewer
}

function Start-Pr {
    param(
        [string]$AzOrg,
        [string]$TaskId,
        [string]$SourceBranch,
        [string]$TargetBranch,
        [string]$Project,
        [string]$Repository,
        [string]$TaskDescription,
        [bool]$DeleteSourceBranch = $true,
        [switch]$DryRun,
        [string]$OverrideReviewer = "",
        [switch]$Silent,
        [switch]$SkipReviewer,
        [string]$PrTitle = "",
        [string[]]$AllTaskIds = @()
    )

    # PR 可一次連結多個工作項；未提供 AllTaskIds 時回退為單一 TaskId（Release/Hotfix 呼叫端不受影響）
    $workItemLinks = @($AllTaskIds | Where-Object { $_ -and "$_" -ne '0' })
    if ($workItemLinks.Count -eq 0 -and $TaskId -and "$TaskId" -ne '0') {
        $workItemLinks = @("$TaskId")
    }

    Show-Msg "檢查可用 PR..." -Type 'Process' -NoNewline
    $prListOutput = az repos pr list --source-branch $SourceBranch --target-branch $TargetBranch --org $AzOrg --project $Project --repository $Repository --status active -o json 2>&1
    $prListError = $prListOutput | Where-Object { $_ -is [System.Management.Automation.ErrorRecord] }
    
    if ($prListError) {
        Show-Msg "" -Type 'Default'
        Show-Msg "Failed to query PR: $($prListError.Exception.Message)" -Type 'Error'
        Show-Msg "Project: $Project, Repository: $Repository" -Type 'Information'
        return $null
    }
    
    $allPrs = $prListOutput | ConvertFrom-Json
    if (-not $allPrs) {
        $allPrs = @()
    }

    $similarPrs = @()
    foreach ($pr in $allPrs) {
        if ($pr.title -like "*$TaskId*") {
            $similarPrs += [PSCustomObject]@{
                Name     = $pr.title
                ObjectId = $pr.pullRequestId
            }
        }
    }

    $selectedPrName = $null
    if ($similarPrs.Count -gt 1) {
        $similarPrNames = $similarPrs | Select-Object -ExpandProperty Name
        $selectedPrName = Select-Helper -data $similarPrNames
    }
    elseif ($similarPrs.Count -eq 1) {
        $selectedPrName = $similarPrs[0].Name
    }
    elseif ($similarPrs.Count -eq 0) {
        $selectedPrName = if (-not [string]::IsNullOrWhiteSpace($PrTitle)) { $PrTitle } else { $SourceBranch }

        Show-Msg "找不到既有 PR" -Type 'Process'

        $selectedReviewer = $null
        if (-not $SkipReviewer) {
            $selectedReviewer = Select-Reviewer -AzOrg $AzOrg -Project $Project -OverrideReviewer $OverrideReviewer
        }
        else {
            Show-Msg "已啟用 -SkipReviewer：略過審核者選擇" -Type 'Warning'
        }
        try {
            $prParams = @(
                "--org", $AzOrg
                "--project", $Project
                "--repository", $Repository
                "--source-branch", $SourceBranch
                "--target-branch", $TargetBranch
                "--title", $selectedPrName
                "--description", $TaskDescription
                "--transition-work-items", "true"
            )
            
            if ($DeleteSourceBranch) {
                $prParams += @("--delete-source-branch", "true")
            }

            if ($workItemLinks.Count -gt 0) {
                $prParams += @("--work-items") + $workItemLinks
            }

            Show-Msg "=== PR 建立參數 ===" -Type 'Warning'
            Show-Msg "PR 標題: " -Type 'Information' -NoNewline
            Show-Msg "$selectedPrName" -Type 'Default'
            Show-Msg "專案/儲存庫: " -Type 'Information' -NoNewline
            Show-Msg "$Project/$Repository" -Type 'Default'
            Show-Msg "來源分支: " -Type 'Information' -NoNewline
            Show-Msg "$SourceBranch" -Type 'Default'
            Show-Msg "目標分支: " -Type 'Information' -NoNewline
            Show-Msg "$TargetBranch" -Type 'Default'
            Show-Msg "工作項連結: " -Type 'Information' -NoNewline
            if ($workItemLinks.Count -gt 0) {
                Show-Msg ($workItemLinks -join ', ') -Type 'Default'
            } else {
                Show-Msg "無" -Type 'Default'
            }
            Show-Msg "必要 Reviewer: " -Type 'Information' -NoNewline
            Show-Msg "$selectedReviewer" -Type 'Default'
            Show-Msg "刪除來源分支: " -Type 'Information' -NoNewline
            Show-Msg "$DeleteSourceBranch" -Type 'Default'
            if ($DryRun) {
                Show-Msg "[DRY RUN] 略過 PR 建立" -Type 'Warning'
                $dryRunPrUrl = "$AzOrg/$([uri]::EscapeDataString($Project))/_git/$([uri]::EscapeDataString($Repository))/pullrequest/DRY-RUN"
                $dryRunResult = "[$Project] -> $TargetBranch`n"
                $dryRunResult += "<$dryRunPrUrl|Pull Request DRY-RUN>: $selectedPrName"
                return $dryRunResult
            }

            if (-not $Silent) {
                Show-Msg "=== 請確認參數 ===" -Type 'Warning'
                $ans = Read-Host "按 Enter 執行，或 Ctrl+C 取消"
            }
            else {
                $ans = ""
            }

            if ([string]::IsNullOrWhiteSpace($ans) -or $ans -eq "") {
                $prCreateOutput = & az repos pr create @prParams 2>&1
                $prCreateError = $prCreateOutput | Where-Object { $_ -is [System.Management.Automation.ErrorRecord] }
                
                if ($prCreateError) {
                    Show-Msg "Failed to create PR: $($prCreateError.Exception.Message)" -Type 'Error'
                    return $null
                }
                
                $pr = $prCreateOutput | ConvertFrom-Json

                if (-not $pr -or -not $pr.pullRequestId) {
                    Show-Msg "Failed to create PR: Cannot get PR info" -Type 'Error'
                    return $null
                }

                if ($selectedReviewer) {
                    az repos pr reviewer add --id $pr.pullRequestId --reviewers $selectedReviewer --required --org $AzOrg | Out-Null
                    az repos pr update --id $pr.pullRequestId --auto-complete true --org $AzOrg | Out-Null
                    Show-Msg "已加入必要 reviewer 並設定 auto-complete: $selectedReviewer" -Type 'Success'
                }
                else {
                    Show-Msg "已略過 reviewer（未加入必要審核者、未設定 auto-complete）" -Type 'Warning'
                }

                Show-Msg "已成功建立 PR，連結: " -Type 'Success' -NoNewline
                $prUrl = "$AzOrg/$([uri]::EscapeDataString($Project))/_git/$([uri]::EscapeDataString($Repository))/pullrequest/$($pr.pullRequestId)"
                Show-Msg "$prUrl" -Type 'Link'

                Open-Browser -Url $prUrl

                $prResult = "[$Project] -> $TargetBranch`n"
                $prResult += "<$prUrl|Pull Request $($pr.pullRequestId)>: $($pr.title)"
                return $prResult
            }

        }
        catch {
            Show-Msg "Failed to create PR: $($_.Exception.Message)" -Type 'Error' 
            return $null
        }
    }

    $prObject = $allPrs | Where-Object { $_.title -eq $selectedPrName }
    
    if (-not $prObject) {
        Show-Msg "找不到對應的 PR 物件" -Type 'Error'
        return $null
    }
    
    Show-Msg "找到既有 PR" -Type 'Process'

    $selectedReviewer = $null
    if (-not $SkipReviewer) {
        $selectedReviewer = Select-Reviewer -AzOrg $AzOrg -Project $Project -OverrideReviewer $OverrideReviewer
    }
    else {
        Show-Msg "已啟用 -SkipReviewer：略過審核者更新" -Type 'Warning'
    }

    Show-Msg "=== Existing PR Info and Update Parameters ===" -Type 'Warning'
    Show-Msg "PR Title: " -Type 'Information' -NoNewline
    Show-Msg "$($prObject.title)" -Type 'Default'
    Show-Msg "Project/Repository: " -Type 'Information' -NoNewline
    Show-Msg "$Project/$Repository" -Type 'Default'
    Show-Msg "Source Branch: " -Type 'Information' -NoNewline
    Show-Msg "$SourceBranch" -Type 'Default'
    Show-Msg "Target Branch: " -Type 'Information' -NoNewline
    Show-Msg "$TargetBranch" -Type 'Default'
    Show-Msg "Work Item Link: " -Type 'Information' -NoNewline
    if ($TaskId -and $TaskId -ne 0) {
        Show-Msg "$TaskId" -Type 'Default'
    } else {
        Show-Msg "None" -Type 'Default'
    }
    Show-Msg "Will update required reviewer to: " -Type 'Information' -NoNewline
    Show-Msg "$selectedReviewer" -Type 'Default'
    Show-Msg "PR Status: " -Type 'Information' -NoNewline
    Show-Msg "$($prObject.status)" -Type 'Default'
    Show-Msg "PR Link: " -Type 'Information' -NoNewline
    $prUrl = "$AzOrg/$([uri]::EscapeDataString($Project))/_git/$([uri]::EscapeDataString($Repository))/pullrequest/$($prObject.pullRequestId)"
    Show-Msg "$prUrl" -Type 'Link'
    if ($DryRun) {
        Show-Msg "[DRY RUN] 略過既有 PR 的 reviewer 更新" -Type 'Warning'
        $prResult = "[$Project] -> $TargetBranch`n"
        $prResult += "<$prUrl|Pull Request $($prObject.pullRequestId)>: $($prObject.title)"
        return $prResult
    }

    if (-not $Silent) {
        Show-Msg "=== Please confirm parameters ===" -Type 'Warning'
        $ans = Read-Host "按 Enter 執行（會更新 reviewer），或 Ctrl+C 取消"
    }
    else {
        $ans = ""
    }

    if ([string]::IsNullOrWhiteSpace($ans) -or $ans -eq "") {
        try {
            if (-not $selectedReviewer) {
                Show-Msg "已略過 reviewer 更新" -Type 'Warning'
            }
            elseif ($prObject.pullRequestId) {
                az repos pr reviewer add --id $prObject.pullRequestId --reviewers $selectedReviewer --required --org $AzOrg | Out-Null
                Show-Msg "已更新必要 reviewer: $selectedReviewer" -Type 'Success'
            } else {
                Show-Msg "PR 物件缺少 pullRequestId，無法更新 reviewer" -Type 'Error'
                return $null
            }
        } catch {
            Show-Msg "更新 reviewer 失敗: $($_.Exception.Message)" -Type 'Warning'
        }
        
        $prResult = "[$Project] -> $TargetBranch`n"
        $prResult += "<$prUrl|Pull Request $($prObject.pullRequestId)>: $($prObject.title)"
        return $prResult
    } else {
        Show-Msg "已取消操作" -Type 'Warning'
        return [PSCustomObject]@{
            PrName     = $selectedPrName
            Project    = $Project
            Repository = $Repository
            prObject   = $prObject
        }
    }
}

# ==================== Helper Functions ====================

function Open-Browser {
    param([string]$Url)
    
    $chromePath = $null
    $possibleChromePaths = @(
        "chrome.exe",
        "${env:ProgramFiles}\Google\Chrome\Application\chrome.exe",
        "${env:ProgramFiles(x86)}\Google\Chrome\Application\chrome.exe",
        "${env:LOCALAPPDATA}\Google\Chrome\Application\chrome.exe"
    )
    
    foreach ($path in $possibleChromePaths) {
        if ($path -eq "chrome.exe") {
            $chromePath = Get-Command "chrome.exe" -ErrorAction SilentlyContinue
            if ($chromePath) {
                break
            }
        } else {
            if (Test-Path $path) {
                $chromePath = $path
                break
            }
        }
    }
    
    if ($chromePath) {
        try {
            Start-Process $chromePath -ArgumentList "`"$Url`""
            Show-Msg "Opened browser to show PR link" -Type 'Success'
            return
        } catch {
            Show-Msg "Warning: Cannot open Chrome ($($_.Exception.Message)), trying default browser..." -Type 'Warning'
        }
    }

    # Fall back to the system default browser.
    try {
        Start-Process $Url
        Show-Msg "Opened default browser to show PR link" -Type 'Success'
    } catch {
        Show-Msg "Warning: Cannot open browser: $($_.Exception.Message)" -Type 'Warning'
        Show-Msg "Please manually copy the link above to browser" -Type 'Information'
    }
}

# Note: Functions are available via dot sourcing in main.ps1

