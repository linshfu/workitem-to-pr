# 📋 Very-Lazy

> **Azure DevOps + Slack 自動化 CLI 工具**
> 從工作項（Work Item）到建立分支、建立 PR、發送 Slack 通知，一條指令跑完整個日常開發流程。
> **版本**：v3.8

```powershell
vl 22222        # 處理工作項 22222：選 Task -> 建分支 -> 建 PR -> Slack 通知 reviewer
```

---

## ✨ 功能總覽

- **工作項流程自動化**：輸入工作項 ID，自動列出/建立 Task、建立分支、建立 PR、通知 reviewer
- **PBI/Task 快速建單**：`-NewPbi` 互動式建 PBI、`-NewTask` 一次建多張 Task 並由一張 PR 連結全部
- **Release 自動化**：一次建立目標 `master` 與 `develop` 的兩張 Release PR
- **Hotfix 流程**：從 master 開 hotfix 分支、等修正 commit、自動改版號、PR 回 master/develop
- **Slack 整合**：PR 建立後自動到指定頻道 tag reviewer 請求 review
- **智慧專案比對**：依 Area Path / 標題標籤自動對應 Azure DevOps 專案與儲存庫，手動選過一次即可記住

---

## 💻 系統需求

| 需求 | 說明 |
|------|------|
| Windows PowerShell 5.1 或 PowerShell 7+ | 主程式為 PowerShell 腳本 |
| [Azure CLI](https://learn.microsoft.com/cli/azure/install-azure-cli) | 需安裝 DevOps 擴充：`az extension add --name azure-devops` |
| Git | 分支操作 |
| Node.js + npm | 僅 Release / Hotfix 流程需要（`npm ci`、`npm run release`、`npm run build:prod`） |
| Azure DevOps 帳號 | 對目標專案有建立分支 / PR / 工作項的權限 |
| Slack Workspace | 需可建立 Slack App（或請管理員代建）；不用 Slack 通知可一律加 `-SkipSlack` |

---

## 🚀 安裝與設定

### 1. 取得專案

```powershell
git clone https://github.com/linshfu/workitem-to-pr.git
cd workitem-to-pr
```

### 2. 建立設定檔

```powershell
Copy-Item config.example.json config.json
Copy-Item config.local.example.json config.local.json
```

| 檔案 | 內容 | 進 git？ |
|------|------|----------|
| `config.json` | Azure org、專案對應表、Slack 頻道/成員 | ❌（gitignore） |
| `config.local.json` | 機密：Slack Bot Token | ❌（gitignore，絕不進 git） |

編輯 `config.json`，至少設定：

```json
{
  "azureOrg": "https://dev.azure.com/your-org",
  "workItemProject": "YourTeamProject",
  "azureProjectMappings": {
    "myapp": {
      "azureProject": "MyApp",
      "azureRepository": "MyApp-Web",
      "localPath": "C:\\code\\myapp"
    }
  },
  "projectPaths": {
    "myapp": "C:\\code\\myapp"
  },
  "slackConfig": {
    "channel": "your-review-channel",
    "members": []
  }
}
```

- `azureOrg`：你的 Azure DevOps 組織 URL（也可用環境變數 `AZURE_DEVOPS_ORG` 覆寫）
- `workItemProject`：工作項（PBI/Task）所在的 team project（Area/Iteration 樹的根）——與 repo 所在專案是兩回事
- `azureProjectMappings`：本地專案鍵 → Azure 專案/儲存庫/本機路徑的對應表（比對不到時程式會讓你手動選，選完可自動存回，所以一開始不用填齊）
- `slackConfig.channel`：PR 通知要發到的 Slack 頻道名稱（**不含 `#`**）
- `slackConfig.members`：留空即可，第一次發通知時程式會自動抓頻道成員讓你挑選並存檔

### 3. Azure CLI 登入

```powershell
az login
az extension add --name azure-devops
```

### 4. 設定 `vl` 別名（建議）

把下面這段加進 PowerShell profile（`notepad $PROFILE`，檔案不存在就直接存新檔）：

```powershell
function vl { & "C:\path\to\workitem-to-pr\main.ps1" @args }
```

重開 PowerShell 後就能用 `vl 22222` 這種短指令。

---

## 🤖 Slack Bot 設定教學

Slack 通知需要一個有 Bot Token 的 Slack App。整個流程約 5 分鐘：

### 1. 建立 Slack App

1. 開啟 <https://api.slack.com/apps> → **Create New App** → **From scratch**
2. App Name 隨意取（例如 `very-lazy-bot`），選擇你的 Workspace → **Create App**

### 2. 開通 Bot 權限（OAuth Scopes）

左側選單 **OAuth & Permissions** → 捲到 **Scopes** → **Bot Token Scopes** → **Add an OAuth Scope**，加入以下四個：

| Scope | 用途 |
|-------|------|
| `chat:write` | 發送 PR 通知訊息 |
| `channels:read` | 列出公開頻道、讀取頻道成員（初始化成員名單、`-ListChannels`） |
| `groups:read` | 列出 bot 所在的私人頻道（`-ListChannels` 搜尋私人頻道用） |
| `users:read` | 取得成員顯示名稱（把頻道成員 ID 對應成人名） |

### 3. 安裝到 Workspace、取得 Token

1. 同頁面上方 **Install to Workspace**（改過 scopes 後會變成 **Reinstall to Workspace**）→ **Allow**
2. 複製 **Bot User OAuth Token**（`xoxb-` 開頭）

Token 不需要手動貼到任何檔案：**第一次執行需要 Slack 的指令時，程式會提示你輸入，驗證通過後自動存進 `config.local.json`**（已被 gitignore），之後不會再問。

> Token 讀取順序：`config.local.json` 的 `slackToken` → 環境變數 `SLACK_BOT_TOKEN` → 互動式輸入。
> ⚠️ Token 等同 bot 的密碼：不要 commit 進 git、不要貼到公開場合。若不慎外洩，到 App 後台 **OAuth & Permissions → Rotate/Regenerate** 重新產生。

### 4. 把 Bot 加進通知頻道

Bot 必須「在頻道裡」才能發訊息與讀取成員：

1. 到你要發通知的頻道（即 `config.json` 裡 `slackConfig.channel` 設定的頻道）
2. 輸入 `/invite @你的BotName`（或：頻道名稱點開 → **Integrations** → **Add apps** → 選你的 App）

### 5. 測試

```powershell
.\main.ps1 -TestSlackMessage
```

第一次會依序：要求輸入 token → 抓頻道成員讓你挑「日後要通知的人」（存進 `config.json`）→ 發一則測試訊息。看到訊息出現在頻道就完成了。

常見錯誤：

| 錯誤 | 原因 / 解法 |
|------|------------|
| `invalid_auth` / `token_revoked` | Token 打錯或已被重新產生 → 回 App 後台重新複製 |
| `channel_not_found` / `not_in_channel` | Bot 還沒被加進頻道 → 回到步驟 4 |
| `missing_scope` | 少加了 scope → 補上後**必須 Reinstall to Workspace** 才會生效 |
| 找不到頻道 | `slackConfig.channel` 名稱打錯或含 `#` → 改成純頻道名稱 |

---

## 🎯 主要執行模式

### 1. 一般模式（最常用）

```powershell
vl 22222
```

自動流程：讀取工作項 → 列出/建立子 Task → 建立分支 → 等你 commit → 建立 PR（連結工作項、設 reviewer 與 auto-complete）→ Slack 通知 reviewer。

### 1.1 直接建立新 Task（略過選單）

```powershell
vl 34429 -NewTask "[MyApp][前端] 列表頁手機版 Header 對齊"
```

當 PBI 底下 Task 很多、確定要建新的時最省時；建立後自動繼續分支/PR/Slack 流程。

### 1.2 一次建立多張 Task

```powershell
vl 34429 -NewTask "[MyApp][前端] 標題A","[MyApp][前端] 標題B"
```

適合「先寫 code、後補單」——一個 commit 對一張 Task。建立後列出**新建＋既有** Task 讓你多選 PR 要連結哪些（直接 Enter＝只連結新建的；第一個選擇作為分支命名的主要 Task），最後**一張 PR 同時連結所有選中的 Task**。

### 2. 先建 PBI 再續跑

```powershell
vl -NewPbi
vl -NewPbi -NewTask "[MyApp][前端] 標題"   # 建完 PBI 直接開子 Task
```

選專案 → 輸入標題 → 自動帶 Area Path（config 的 `areaPath`）、Iteration（當年當月）、指派給自己 → 建立 PBI 後接回一般流程。

### 3. 手動 Release

```powershell
vl -ManualRelease -Project myapp -ReleaseVersion 1.6.2
```

會同時建立兩張 PR——目標 `master`（標題 `release/vX.Y.Z`）與目標 `develop`（標題 `release/vX.Y.Z-develop`）。也可直接 `vl <Release 工作項 ID>`。分支建立後會**先停下確認**才建 PR 與發 Slack。

### 4. Hotfix

```powershell
vl -Hotfix -Project myapp -ReleaseVersion 1.6.3   # 參數省略會互動詢問
```

更新 master → 開 `hotfix/vX.Y.Z` → 等你 push 修正 commit → 自動改版號 commit → PR 到 master/develop → Slack 通知。

### 5. 輔助工具

```powershell
vl -Help                                          # 顯示常用指令與參數
.\main.ps1 -ListChannels -SearchKeyword "關鍵字"   # 依關鍵字搜尋 Slack 頻道 ID
.\main.ps1 -TestSlackMessage                      # 發測試訊息驗證 Slack 設定
```

---

## 🔧 參數一覽

| 參數 | 說明 | 預設值 |
|------|------|--------|
| `-WorkItemId` | 工作項 ID（位置參數，可直接 `vl 22222`） | 自動搜尋指派給自己的工作項 |
| `-NewPbi` | 先互動式建立 PBI 再續跑 | `false` |
| `-NewTask` | 直接以此標題建立新 Task；`"A","B"` 一次建多張 | 空 |
| `-Hotfix` | Hotfix 流程 | `false` |
| `-ManualRelease` | 手動 Release 流程 | `false` |
| `-Project` | Release/Hotfix 專案鍵 | - |
| `-ReleaseVersion` | Release/Hotfix 版本號 | - |
| `-Reviewer` | 指定 reviewer email，略過互動選擇 | 空 |
| `-SkipSlack` | 跳過 Slack 通知 | `false` |
| `-SkipReviewer` | 建 PR 但不加必要 reviewer（連帶略過 Slack） | `false` |
| `-DryRun` | 模擬執行，不實際建立分支/PR | `false` |
| `-Help` | 顯示用法 | `false` |

### 環境變數（選用）

| 變數 | 說明 |
|------|------|
| `SLACK_BOT_TOKEN` | Slack Bot Token（沒有 `config.local.json` 時的備援） |
| `AZURE_DEVOPS_ORG` | Azure DevOps 組織 URL（優先於 config.json 的 `azureOrg`） |

---

## ⚙️ 設定檔完整說明

```json
{
  "azureOrg": "https://dev.azure.com/your-org",
  "workItemProject": "YourTeamProject",
  "projectListExcludes": [ "SomePrefix*" ],
  "azureProjectMappings": {
    "myapp": {
      "azureProject": "MyApp",
      "azureRepository": "MyApp-Web",
      "localPath": "C:\\code\\myapp",
      "areaPath": "YourTeamProject\\Product\\MyApp",
      "defaultBranch": "develop",
      "aliases": ["我的專案"]
    }
  },
  "projectPaths": {
    "myapp": "C:\\code\\myapp"
  },
  "slackConfig": {
    "channel": "your-review-channel",
    "members": [
      { "key": "DisplayName (RealName)", "value": "U0XXXXXXXX" }
    ]
  }
}
```

| 欄位 | 說明 |
|------|------|
| `azureOrg` | Azure DevOps 組織 URL |
| `workItemProject` | 工作項所在 team project（Area/Iteration 樹的根），與 repo 所在專案是兩回事 |
| `projectListExcludes` | （選用）手動選專案時要排除的專案名稱 wildcard 清單 |
| `azureProjectMappings.<key>` | 專案對應：`azureProject`（repo 所在 Azure 專案）、`azureRepository`、`localPath`（Release/Hotfix 需要）、`areaPath`（建 PBI 用 + 參與自動比對）、`defaultBranch`（PR 目標與分支基底，預設 `develop`）、`aliases`（標題標籤別名，支援中文） |
| `projectPaths` | Release 標題比對用的 key → 本機路徑 |
| `slackConfig.channel` | PR 通知頻道名稱（不含 `#`） |
| `slackConfig.members` | 可被選為通知對象的成員（`value` 為 Slack member ID；首次使用自動初始化） |

### 專案自動比對順序

處理工作項時 `Get-Project-And-Repo` 依序嘗試：

1. **Area Path（最可靠）**：以工作項 Area Path 的路徑段比對各 mapping 的 `azureProject` 或 `areaPath`
2. **標題標籤 `[Tag]`**：比對 mapping 的 key / `azureProject` / `aliases`（中文標籤可透過 `aliases` 對應）
3. **標題關鍵字**：整串標題子字串比對（跳過長度 < 3 的 key，避免誤中英文單字）
4. 都比不中 → 手動選單，選完可輸入關鍵字**自動存回 config**，下次直接命中

Release 流程另以標題比對 `projectPaths`；比不中時列出有 `localPath` 的專案手動選，同樣可儲存。

---

## 📦 模組架構

```
workitem-to-pr/
├── main.ps1                  # 參數與路由入口
├── modules/
│   ├── Config.psm1           # 配置管理（config.json / config.local.json / 環境變數）
│   ├── UI.psm1               # 彩色輸出、互動式選擇器
│   ├── Git.psm1              # Git / Azure CLI 執行（UTF-8 編碼處理）
│   ├── AzureDevOps.psm1      # 工作項、分支、PR 操作
│   ├── Slack.psm1            # Slack 訊息、頻道、成員操作
│   └── Workflow.psm1         # 核心流程編排（Task/Release/Hotfix/Help）
├── config.example.json       # config.json 範本（進 git）
├── config.local.example.json # config.local.json 範本（進 git）
└── env.example.txt           # 環境變數範本（進 git）
```

| 模組 | 主要函數 |
|------|----------|
| `Config.psm1` | `Initialize-Environment`、`Load-Config`/`Save-Config`、`Get-AzureOrg`、`Get-SlackChannel`、`Get/Save-SlackTokenToConfig` |
| `UI.psm1` | `Show-Msg`、`Select-Helper`、`Confirm-Action`、`Show-WorkItem-Info` |
| `Git.psm1` | `Invoke-GitWithEncoding`、`Invoke-AzCli`、`Update-Local-Branch`、`New-Pushed-Branch` |
| `AzureDevOps.psm1` | `Get-Az-WorkItem`、`Start-Task`、`Start-Branch`、`Start-Pr`、`Select-Reviewer`、`Get-Project-And-Repo`、`Start-New-Pbi` |
| `Slack.psm1` | `Send-Slack-Message`、`Get-Slack-Members`、`Test-Slack-Token-Validity`、`Find-Channel-ID` |
| `Workflow.psm1` | `Invoke-VeryLazyMain`、`Start-Manual-Release-Process`、`Show-Usage` |

---

## 🔐 安全性

- 機密（Slack token）只存在 `config.local.json`，已被 `.gitignore` 排除，**絕不 commit**
- `config.json` 含團隊內部資訊（專案名、成員 ID），同樣不進 git，各自從 `config.example.json` 建立
- Token 存檔前會先呼叫 `auth.test` 驗證有效性
- 若 token 不慎外洩：Slack App 後台 → OAuth & Permissions → 重新產生，並更新 `config.local.json`

---

## 📝 更新歷史

- **v3.8** (2026-07-06)：`-NewTask` 支援一次建立多張 Task；PR 可同時連結多個工作項（`az repos pr create --work-items`）；修正手動 Release 誤帶 `--work-items 0`
- **v3.7** (2026-07-02)：新增 `-Hotfix` 流程（master → hotfix 分支 → 等修正 → 自動改版號 → 雙 PR）；`Git.psm1` 新增 `Update-Local-Branch`、`New-Pushed-Branch`
- **v3.6** (2026-07-02)：新增 `-NewPbi`（自動帶 Area/Iteration/指派自己）；config 新增 `areaPath`、`workItemProject`；Area Path 比對支援 config `areaPath`
- **v3.5** (2026-07-02)：Release 建 PR 前先停下確認；Release 專案比對失敗改列清單手動選並可儲存；修正 `-DryRun` 未傳遞的問題
- **v3.4** (2026-06-15)：設定檔分層（config.json / config.local.json + 範本 + .gitignore）；專案比對改 Area Path 優先；移除失效的 Slack 監控模式；修正 token 反覆要求重設與 UTF-8 BOM 問題
- **v3.3** (2026-06-12)：新增 `-NewTask`；手動選專案後可儲存關鍵字對應
- **v3.2** (2026-05-20)：全模式模組化
- **v3.1** (2026-05-19)：`main.ps1` 改為路由入口，新增 `Workflow.psm1`
- **v3.0** (2025-12-29)：模組化架構重構（單檔 3500+ 行拆為獨立模組）；移除硬編碼機密
- **v2.x / v1.0**：Release 自動化、配置驅動映射、初始版本
