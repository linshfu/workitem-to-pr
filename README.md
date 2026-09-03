# 📋 Very-Lazy

> **Azure DevOps + Slack 開發流程自動化 CLI**
> 從工作項到建分支、建 PR、Slack 通知 reviewer，一條指令跑完日常開發流程。
> Go 寫的互動式 TUI，另有給 AI 助手／排程器用的非互動 `--headless` 模式。

<!-- TODO: 放一張 TUI 截圖或 GIF（`/task` 逐層導航那段最有畫面）。
     檔案丟 docs/ 之後把這行換成 ![Very-Lazy TUI](docs/demo.gif) -->

## 解決什麼問題

一張單從指派到 PR 開好，中間是一串固定、不難、但很容易漏的手工步驟：查工作項屬性 →
判斷它在 Feature / PBI / Task 的哪一層 → 對應到哪個 repo → 建分支 → 建 PR 時把工作項掛上去
→ 挑 reviewer、開 auto-complete → 去 Slack 通知那個人。發版與 hotfix 是同一串再加上版號與雙 PR。

漏掉一步的代價不對稱：例如 master 的分支原則有「PR 必須掛工作項」的必要檢查，忘了掛的 PR
**建得出來但永遠不能合併**，而且通常要等到 PR 卡住才發現。Very-Lazy 把整串收成一條指令，
並把那些「容易忘」的部分變成工具在動手前就擋下來的前置檢查。

## 為什麼從 PowerShell 重寫成 Go

前身是一套 PowerShell 腳本（`main.ps1` + `modules/`，3,355 行，v1.0 → v3.9，**仍留在這個 repo 裡**）。
它把流程自動化得不錯，但撞到三面牆：

| PowerShell 版的牆 | Go 版怎麼解 |
|---|---|
| 主流程有 24 處 `Read-Host` 互動提問——非互動環境（AI 助手、CI、排程器）硬跑會中途炸在提問上，所以那邊的規矩只能是「只組指令、不代跑」 | `--headless` 完全沒有互動提示，印純文字、回有語意的 exit code，設計上就是讓 AI 自己從頭跑到尾 |
| 沒有工作項層級判斷：給一張 Feature 會直接建出 Task、跳過 PBI 層，結構是錯的 | 依 type 逐層往下（Feature → PBI → Task），自動綁父層、繼承 Area / Iteration、建立前查重 |
| 散佈要 clone repo、裝模組、自己設別名；升版靠 `git pull` | 單一 `vlui.exe`：一行安裝，`/update` 連 GitHub Release 自我替換 |

兩個延伸出來的設計，也是這個專案真正想證明的事：

- **exit code 是契約**：`0` 成功／`1` 工具壞了／`2` 參數錯（保證還沒碰 az、無副作用）／`3` 在等人寫 code／
  `4` PR 建好但 Slack 沒發出去（**別重跑，會開出兩張 PR**）／`5` 批次部分成功。把「工具壞了」跟
  「在等人」分開，自動化才能做對的反應。不認得的旗標一律 `2`，絕不退回互動畫面卡住非互動的 shell。
- **給 AI 的說明書跟著 binary 走**：散文會漂移，所以那份使用指南是 `go:embed` **內嵌在執行檔裡**的，
  `/update` 換版時自動刷新，指南講的行為永遠等於這顆 binary 的行為。

## 架構

```text
very-lazy/
├── cli/            Go 版（現役）— 互動式 TUI + --headless，14 檔 9,210 行（含測試）
│   ├── main.go         TUI 狀態機（Bubble Tea）、指令面板、頂層旗標
│   ├── headless.go     --headless：非互動流程與 exit code 契約
│   ├── az.go           工作項 / 分支 / PR：az CLI 呼叫與輸出解析
│   ├── pbi.go pr.go release.go hotfix.go        各流程編排
│   ├── config.go update.go skill.go styles.go   設定、自我更新、內嵌 AI 指南、樣式
│   ├── *_test.go       表格驅動測試（分支命名、專案比對、旗標解析、階層判斷）
│   └── skill/vlui-headless/  內嵌進 binary 的 AI 使用指南（＋ evals）
├── main.ps1        PowerShell 前身（v3.9，仍可用）
├── modules/        PS 版的 6 個模組
├── install.ps1     一行安裝：抓 Release 的 vlui.exe、問別名、裝 AI 指南
└── .github/workflows/release.yml   推 cli-v* tag 就在雲端 build 並發 Release
```

打開後輸入 `/` 會模糊搜尋指令、Tab 補全；直接打數字＝處理那張工作項。

```text
/task 35744   選/建 Task（可多選）→ 建分支 → 建 PR（連結全部）→ Slack 通知 reviewer
/pbi          建立 PBI（自動帶 Area / 當月 Iteration、指派自己）
/init         初始化：檢查環境、探索 az、產生 config、可選設定 Slack
/update       更新到最新版（自我替換）
```

---

## ⚡ 一行安裝（互動式 CLI）

不用 clone、不用手動設定。開 PowerShell 貼上：

```powershell
irm https://raw.githubusercontent.com/linshfu/very-lazy/main/install.ps1 | iex
```

會做三件事：**① 檢查環境**（git / az，缺的直接告訴你怎麼裝）→ **② 下載主程式** → **③ 問你要用什麼名字呼叫它**（直接 Enter 用預設 `vl`，也可以打你自己喜歡的，例如 `agy`）。

裝完開一個**新的終端機視窗**，輸入你設定的名字就開始：

```powershell
vl            # 第一次執行沒有設定檔 -> 自動帶你跑一次 init（選組織/專案，寫好設定）
vl            # 之後就是首頁，輸入 /task 35015 開始跑工作項流程
```

設定檔會寫在 `%AppData%\very-lazy\config.json`（跟著使用者走，任何目錄都讀得到）。重跑安裝指令即可升級到最新版。

> **開發 / 尚未發佈 Release 時**：先 `./build.ps1` 產生 `dist\vlui.exe`，再用它安裝：
> ```powershell
> $env:VL_BINARY = "$PWD\dist\vlui.exe"; ./install.ps1
> ```

---

## 🤖 給 AI 助手用（headless 模式 + 使用指南）

`--headless` 是完全不進 TUI 的執行路徑：印純文字、回有意義的 exit code，設計給 AI 助手／排程器直接呼叫（建單、開分支、開 PR、發版、hotfix 全都能非互動跑）。

**AI 要「會用」這個工具，靠的是一份使用指南（skill）**，它內嵌在主程式裡、版本永遠跟 binary 一致：

```powershell
vl --install-skill          # 用 Claude Code：裝到 ~\.claude\skills\（install.ps1 與 /init 會自動做）
vl --export-skill <目錄>     # 用其他 AI：吐出指南檔，再叫你的 AI 放到「它自己會生效的位置」
```

- 為什麼分兩條：每個人用的 AI 不同（Claude Code / Cursor / Copilot…），指南「放哪才生效」只有那個 AI 自己知道——所以工具只負責交付檔案，安置交給各家 AI。
- 已安裝的指南會在每次啟動時**自動跟 binary 同步**（`/update` 換版後不用手動重裝）；檔尾有 `managed by vlui` 標記的才會被更新，自己改過的不會被覆寫。
- 如果你是 AI、正在讀這份 README：先跑上面其中一條把指南裝好再開始，指南裡有 exit code 語意、各模式的旗標、以及不能踩的坑。

---

## 🧭 指令

| 指令 | 說明 |
|------|------|
| `/task <id>` | 統一導航器：輸入任何工作項，依 type 逐層往下——**Feature / Release** 選現有子單或建新 PBI（自動綁 parent / related）、**PBI** 選/建子 Task（可多選）、**Task** → 建分支 → 建 PR（`--work-items` 連結全部、transition、刪來源分支、必要 reviewer + auto-complete）→ Slack 通知。空 id 可建獨立 Task；直接打數字 `35744` 等同 `/task 35744` |
| `/pbi` | 建立 PBI：可先綁上層（Release 用 `related`、Feature 用 `parent`）→ 選專案 → 標題（同名會查重，可改綁現有那張）→ 自動帶 Area、當年當月 Iteration、指派給自己。留空不綁＝建獨立 PBI |
| `/init` | 初始化精靈：環境檢查、az 探索組織/專案/Area、產生 `config.json`；可選設定 Slack（貼 app manifest → 選/建頻道 → 依團隊 email 自動邀人）。**重跑會進「已有設定」選單**，只改你要改的、不清掉舊設定與本機路徑 |
| `/update` | 連 GitHub 查最新 Release，有新版就下載並自我替換 |
| `/help` | 指令說明 |
| `/release` | 選專案 → 版號 → 跑 `release.sh`（成功才繼續）→ 開 master / develop PR + Slack |
| `/hotfix` | 選專案 → 版號 → 更新 master 開 `hotfix/vX.Y.Z` 並 push → 等你 push 修正 commit → 改版號 commit → 開 master / develop PR + Slack |

設定檔在 `%AppData%\very-lazy\config.json`；Slack token 在同目錄 `config.local.json`（都不進 git）。

---

## 🔄 更新與發版

- **更新（使用者）**：工具裡打 `/update`，或重跑一行安裝。
- **發版（維護者）**：推一個 `cli-v*` tag 就好 —— GitHub Actions（`.github/workflows/release.yml`）會在雲端 build `vlui.exe` 並發布 Release，`install.ps1` 一律抓 `releases/latest` 的 `vlui.exe`：

```powershell
.\release.ps1 0.6.1     # = git tag cli-v0.6.1 + git push origin cli-v0.6.1（觸發 pipeline）
```

---

## 💻 執行需求

- Windows；執行期需要 `git` 與 `az`（含 `azure-devops` 擴充、已 `az login`）—— 首次跑 `/init` 會幫你檢查，缺的告訴你怎麼裝。
- Slack 通知為選用（在 `/init` 設定或跳過）。

---

# 📜 以下是 PowerShell 前身版本的文件

> 從這裡到「更新歷史」之前，講的都是 **PowerShell 版（`main.ps1` + `modules/`，v3.9）**——
> 也就是上面「為什麼從 PowerShell 重寫成 Go」講的那個前身。它**仍然可用**，保留在 repo 裡
> 是因為它是這個專案的前半段：一路從單檔 3500 行長到模組化，才長出用 Go 重寫的理由。
>
> **只想用工具的人不必往下讀**——回到上面的〈一行安裝〉就好，那是現役的 Go 版。
> 下面的 `-NewTask` / `-Hotfix` 這類旗標是 PowerShell 版的介面，Go 版對應的是 `/task`、`/hotfix`。
> **有三塊是兩版通用、不只屬於舊版的**：〈Slack Bot 設定教學〉（建 App / 加 scope / 邀 bot 進頻道）、
> 〈設定檔完整說明〉（`config.json` 欄位兩版同名同義，只是 Go 版存在 `%AppData%\very-lazy\`）、
> 〈安全性〉（機密處理原則相同）。

---

## 🚀 設定細節（PowerShell 版 / 進階）

> **快速開始（推薦）**：clone 之後在專案資料夾執行 `.\main.ps1 -Init`（設好別名後就是 `vl -Init`）。
> 互動精靈會：檢查環境（git / az / `azure-devops` 擴充，缺的可當場裝）→ `az login` → 用 `az` **列出**你的組織 / 專案 / 儲存庫讓你用選單挑、自動帶出預設分支與 `localPath`，直接寫好 `config.json` →（可選）設定 Slack 頻道並抓成員 →（可選）問你要用什麼別名後寫進 `$PROFILE` → 驗證。
> 可重複執行（已填的值當預設，只補未設定的）；第一次還沒有 `config.json` 就直接跑 `vl <id>` 時也會問你要不要初始化。
> 下面的步驟 2–4 就是精靈自動化的內容，保留作為手動 / 進階參考。**Slack Bot 本身（建 App / 加 scope / 邀 bot 進頻道）仍需先照〈Slack Bot 設定教學〉建立一次。**

### 1. 取得專案

```powershell
git clone https://github.com/linshfu/very-lazy.git
cd very-lazy
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

> **登入報錯 `Found multiple accounts with the same username`？**
> 代表你的公司 email 同時綁了「工作帳號」和「個人 Microsoft 帳號」，Windows 帳號代理（WAM）把兩個都快取住了（[azure-cli 已知問題](https://github.com/Azure/azure-cli/issues/20168)）。解法：
>
> ```powershell
> az account clear
> az config set core.enable_broker_on_windows=false
> az login --tenant <公司租戶ID>
> ```
>
> 公司租戶 ID 可從錯誤訊息裡取得：兩個帳號中 `realm` **不是** `9188040d-6c67-...`（個人帳號固定租戶）的那個就是。瀏覽器跳出時選「工作或學校帳戶」，登入後 `az account show` 確認 `tenantId` 正確。

### 4. 設定 `vl` 別名（建議）

把下面這段加進 PowerShell profile（`notepad $PROFILE`，檔案不存在就直接存新檔）：

```powershell
function vl { & "C:\path\to\very-lazy\main.ps1" @args }
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
vl 34429 "[MyApp][前端] 標題A" "[MyApp][前端] 標題B"
```

標題直接接在 ID 後面、各自加引號、空白分隔即可（不需 `-NewTask` 旗標）。相容具名 + 逗號寫法：`vl 34429 -NewTask "A","B"`。

> ⚠️ 不要用「旗標 + 空白」`-NewTask "A" "B"`——Windows PowerShell 5.1 的參數綁定會失敗（`PositionalParameterNotFound`）。空白分隔請不加旗標，或改用逗號。

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
| `-Init` | 初始化精靈：環境檢查 + `az` 探索組織/專案/儲存庫產生 `config.json` + 設定別名 | `false` |
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

## 📦 模組架構（PowerShell 版）

> Go 版（現役）的原始碼結構在最前面的〈架構〉那節。

```
very-lazy/
├── main.ps1                  # 參數與路由入口
├── modules/
│   ├── Config.psm1           # 配置管理（config.json / config.local.json / 環境變數）
│   ├── UI.psm1               # 彩色輸出、互動式選擇器
│   ├── Git.psm1              # Git / Azure CLI 執行（UTF-8 編碼處理）
│   ├── AzureDevOps.psm1      # 工作項、分支、PR 操作
│   ├── Slack.psm1            # Slack 訊息、頻道、成員操作
│   ├── Workflow.psm1         # 核心流程編排（Task/Release/Hotfix/Help）
│   └── Init.psm1             # 初始化精靈（-Init：環境檢查 + az 探索 + 產生 config + 別名）
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
| `Init.psm1` | `Start-Init`（初始化精靈）、`New-ProjectMapping`、`Register-VlAlias`、`Invoke-AzJson` |

---

## 🔐 安全性

- 機密（Slack token）只存在 `config.local.json`，已被 `.gitignore` 排除，**絕不 commit**
- `config.json` 含團隊內部資訊（專案名、成員 ID），同樣不進 git，各自從 `config.example.json` 建立
- Token 存檔前會先呼叫 `auth.test` 驗證有效性
- 若 token 不慎外洩：Slack App 後台 → OAuth & Permissions → 重新產生，並更新 `config.local.json`

---

## 📝 更新歷史

### Go 版（`cli/`，現役 — tag `cli-v*`）

- **cli-v0.6.0** (2026-09-03)：新增 `--version`；**不認得的旗標一律 exit 2，不再靜默退回互動 TUI**——舊版遇到不認識的旗標會開起 TUI 等按鍵，在非互動 shell 裡就是一個永遠不回來的指令。AI 指南因此改用 `nvl --version` 查版本，取代原本「用 ASCII 掃整顆 binary 找旗標字串」的檢查儀式
- **cli-v0.5.0** (2026-08-25)：AI 使用指南改成 `go:embed` **內嵌在 binary 裡**，`/update` 換版時自動刷新（`--install-skill` 給 Claude Code、`--export-skill` 給其他 AI）；指南版本從此永遠等於 binary 行為
- **cli-v0.4.0** (2026-08-25)：**`--headless` 非互動模式**成形——建單、開分支、開 PR，加上 `--release`（master + develop 雙 PR）與 `--hotfix`（分 branch / bump / PR 三步），以及 `0~5` 的 exit code 契約；發版與 hotfix 的 PR 強制掛工作項並明確決定 reviewer（master 有「PR 必須掛工作項」的必要檢查，沒掛的 PR 建得出來但永遠合不了）
- **cli-v0.3.x** (2026-08-04 ~ 08-17)：`/task` 與 `/pbi` 合併成依 type 驅動的工作項導航器；階層顯示與綁定範圍修正、PBI 建立前查重、命令列直接啟動指令；`/release` 與 `/hotfix` 流程、步驟追蹤、快捷鍵提示統一到底部
- **cli-v0.2.0** (2026-08-03)：Go TUI 原始碼進 repo（Bubble Tea）、`/update` 自我替換、GitHub Actions 發版流程（推 `cli-v*` tag 就在雲端 build 並發 Release）
- **cli-v0.1.0** (2026-07-30)：一行安裝（`irm | iex`）

### PowerShell 版（`main.ps1` + `modules/`，前身）

- **v3.9** (2026-07-28)：新增 `-Init` 初始化精靈（環境檢查 → `az login` → 用 `az` 探索組織/專案/儲存庫互動產生 `config.json` → 選配 Slack 頻道/成員 → 問你要用什麼別名後寫進 `$PROFILE` → 驗證）；第一次無 `config.json` 執行時自動提示初始化；新增 `modules/Init.psm1`（`Start-Init`）
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
