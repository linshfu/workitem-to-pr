# UI.psm1 - User Interface Module

# Message color definitions
$script:MessageColor = @{
    Error       = 'Red'
    Warning     = 'Yellow'
    Information = 'Cyan'
    Success     = 'Green'
    Process     = 'DarkGray'
    Debug       = 'DarkYellow'
    Default     = 'White'
    Link        = 'Blue'
}

# Display message with type
function Show-Msg {
    param(
        [string]$Message,
        [string]$Type = 'Information',
        [switch]$NoNewline
    )
    
    $color = $script:MessageColor[$Type]
    if (-not $color) {
        $color = 'White'
    }
    
    if ($NoNewline) {
        Write-Host $Message -ForegroundColor $color -NoNewline
    }
    else {
        Write-Host $Message -ForegroundColor $color 
    }
}

# Display work item basic info
function Show-WorkItem-Info {
    param([object]$workItemFields)
    
    $workItemType = $workItemFields.'System.WorkItemType'
    $workItemId = $workItemFields.'System.Id'
    $workItemTitle = $workItemFields.'System.Title'

    Show-Msg "[$workItemType]-" -Type 'Information' -NoNewline
    Show-Msg "#$workItemId-" -Type 'Warning' -NoNewline
    Show-Msg "$workItemTitle" -Type 'Default'
}

# Manual selection helper
function Select-Helper {
    param(
        [array]$data,
        [switch]$keyValue,
        [switch]$multiple
    )
    
    if ($data.Count -eq 0) {
        return $null
    }
    
    if ($data.Count -eq 1) {
        if ($keyValue) {
            return $data[0].Value
        } else {
            return $data[0]
        }
    }
    
    Show-Msg "Please select:" -Type 'Warning'
    for ($i = 0; $i -lt $data.Count; $i++) {
        Show-Msg "  [$i] " -Type 'Information' -NoNewline
        if ($data[$i].PSObject.Properties.Name -contains 'fields' -and $data[$i].fields) {
            $workItemType = $data[$i].fields.'System.WorkItemType'
            $workItemId = $data[$i].fields.'System.Id'
            $workItemTitle = $data[$i].fields.'System.Title'
            Show-Msg "[$workItemType] " -Type 'Default' -NoNewline
            Show-Msg "#$workItemId " -Type 'Warning' -NoNewline
            Show-Msg "- $workItemTitle" -Type 'Default'
        }
        elseif ($data[$i].PSObject.Properties.Name -contains 'System.Id') {
            $workItemType = $data[$i].'System.WorkItemType'
            $workItemId = $data[$i].'System.Id'
            $workItemTitle = $data[$i].'System.Title'
            Show-Msg "[$workItemType] " -Type 'Default' -NoNewline
            Show-Msg "#$workItemId " -Type 'Warning' -NoNewline
            Show-Msg "- $workItemTitle" -Type 'Default'
        }
        else {
            if ($keyValue) {
                Show-Msg "$($data[$i].Key)" -Type 'Default'
            }
            else {
                Show-Msg "$($data[$i])" -Type 'Default'
            }
        }
    }
    
    do {
        if ($multiple) {
            $selectedIndex = Read-Host "Enter item number (0-$($data.Count-1), default 0, multiple: 0,1,2)"
        }
        else {
            $selectedIndex = Read-Host "Enter item number (0-$($data.Count-1), default 0)"
        }
        $indexValue = 0
        
        if ([string]::IsNullOrWhiteSpace($selectedIndex)) {
            $indexValue = 0
            $isValidInput = $true
            Show-Msg "Using default: 0" -Type 'Information'
        }
        else {
            $isValidInput = [int]::TryParse($selectedIndex, [ref]$indexValue)
            
            if ($multiple) {
                $selectedIndex = $selectedIndex.Split(',')
                $isValidInput = $true
                foreach ($index in $selectedIndex) {
                    if (-not [int]::TryParse($index, [ref]$indexValue)) {
                        Show-Msg "Please enter valid number (0-$($data.Count-1))" -Type 'Error'
                        $isValidInput = $false
                        break
                    }
                }
            }
            
            if (-not $isValidInput -or $indexValue -lt 0 -or $indexValue -ge $data.Count) {
                Show-Msg "Please enter valid number (0-$($data.Count-1))" -Type 'Error'
                $isValidInput = $false
            }
        }
    } while (-not $isValidInput)
    
    if ($keyValue) {
        if ($multiple) {
            $selectedIndex = $selectedIndex.Split(',')
            $selectedOptions = @()
            foreach ($index in $selectedIndex) {
                $selectedOptions += $data[$index].Value
            }
            return $selectedOptions
        }
        else {
            return $data[$indexValue].Value
        }
    }
    else {
        if ($multiple) {
            $selectedIndex = $selectedIndex.Split(',')
            $selectedOptions = @()
            foreach ($index in $selectedIndex) {
                $selectedOptions += $data[$index]
            }
            return $selectedOptions
        }
        else {
            return $data[$indexValue]
        }
    }
}

# Confirm dialog
function Confirm-Action {
    param(
        [string]$Message,
        [string]$DefaultChoice = 'Y'
    )
    
    $prompt = if ($DefaultChoice -eq 'Y') { "(Y/N, default Y)" } else { "(Y/N, default N)" }
    $response = Read-Host "$Message $prompt"
    
    if ([string]::IsNullOrWhiteSpace($response)) {
        return $DefaultChoice -eq 'Y'
    }
    
    return $response -match '^[Yy]'
}

# Display separator line
function Show-Separator {
    param([int]$Length = 40)
    Show-Msg ("-" * $Length) -Type 'Information'
}

# Display header block
function Show-Header {
    param([string]$Title)
    Show-Msg "=== $Title ===" -Type 'Warning'
}

# Note: Functions are available via dot sourcing in main.ps1
