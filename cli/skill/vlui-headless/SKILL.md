---
name: vlui-headless
description: 用 vlui（Go 版 very-lazy，別名 nvl）的 --headless 模式非互動地跑 Azure DevOps 流程：(1) 依階層建單——給 Feature 就建 PBI、給 PBI 就建 Task，自動綁父層、繼承 Area/Iteration、建立前查重；(2) 把散在 working tree 的改動拆給各張 Task、開分支 commit 上去；(3) 建分支→建 PR→Slack 通知。只要使用者提到 headless、非互動/自動化跑 vlui 或 nvl、要在 CI／排程器裡跑 very-lazy，或說出「這張單我已經 commit/push 了幫我開 PR」「幫我在這個 Feature 底下開幾張前端單」「往下找沒有前端的就建出來」「不要進畫面直接跑完」這類意思，都要用這個 skill。也涵蓋「code 寫好還沒 commit，幫我拆給這幾張單」「挑一張單開分支把對應的檔案 commit 上去」「這批改動照單分別開 PR」這類要把工作項跟 commit/分支對應起來的請求。給了一個 Feature/PBI 單號要往下開單、或工作項已有分支+commit 只差開 PR 時，優先用這個 skill 而不是互動 TUI 或 PowerShell 版 vl（PS 版沒有階層判斷，給 Feature 會直接建出 Task、跳過 PBI 層，結構是錯的）。發版與修補也涵蓋：「幫我對 release/vX.Y.Z 開雙 PR」「這版要發了幫我開 master 跟 develop 的 PR」用 `--release`（但 release.sh 要另外先跑）；「幫我開 hotfix 分支」「hotfix 改版號」「hotfix 開 PR」用 `--hotfix`（分 --branch / --bump / 開 PR 三次呼叫）。整個 very-lazy 流程現在都能非互動跑，只有互動式的 /init 設定精靈要用 TUI。
---

# vlui --headless 使用指南

## 動手前：這顆執行檔夠新嗎

**結論先講：不認得的旗標現在一律 exit 2，不會再靜默開 TUI 把你卡住。** 所以直接下
`nvl --headless …` 是安全的——最壞情況是印一行「不認得的參數」加 exit 2（碰不到 az/git、
保證沒有副作用），不是一個永遠不回來的指令。

要看版本就跑這個，它印一行 `cli-vX.Y.Z` 就結束：

```powershell
nvl --version
```

而且這份指南是**內嵌在 binary 裡**、每次啟動自動刷新的（檔尾帶一行 vlui 的刷新標記；
把那行拿掉就不會再被覆寫）。你讀到的指南就是這顆執行檔的行為，不會漂移——正常情況下
不需要為了「它認不認得某個旗標」再做任何檢查。

**唯一的例外**：`cli-v0.5.0` 及更早的執行檔既沒有 `--version`，也還會對不認得的旗標
靜默開 TUI，所以 `nvl --version` 在那些版本上**會卡住等按鍵**。只有在「這份指南是別人
手動放的／從別台匯出的，你不確定本機 binary 是哪一版」時，才改用這條不執行、瞬間完成的
檢查：

```powershell
[System.Text.Encoding]::ASCII.GetString([System.IO.File]::ReadAllBytes("$env:LOCALAPPDATA\Programs\very-lazy\vlui.exe")) -match '--version'
```

`False` 表示那顆是舊的：先請使用者跑 `nvl` → `/update` 升上去；臨時要跑就走原始碼模式
（慢幾秒，功能一樣）：

```bash
cd C:/test/very-lazy/cli && go run . --headless <id> [旗標…]
```

發新版一律走 `.\release.ps1 <版號>`（CI 會把 tag 注入成 binary 版號，`--version` 跟
`/update` 的字串比對自然對齊），**不要**本機 build 亂填版號蓋過去。

下面的例子都寫 `nvl`；只有碰到上面那個例外才需要換成 `go run .`。

---

`vlui`（Go 重寫版 very-lazy，使用者機器上的別名是 **`nvl`**）的 `--headless` 是一條
完全不進 TUI 畫面的執行路徑，印純文字結果並回傳有意義的 exit code。它有這幾種模式：

| 模式 | 指令 | 做什麼 |
|---|---|---|
| **建單** | `nvl --headless <parentID> --new "標題" [--new "標題"…]` | 在父單底下建下一層單（Feature→PBI、PBI→Task），綁父層、繼承 Area/Iteration、建前查重 |
| **只開分支** | `nvl --headless <taskID> --branch` | 建/重用該 Task 的分支就停。不查 commit、不建 PR、不需要本機 repo |
| **開 PR** | `nvl --headless <taskID> [更多 taskID…] [旗標…]` | 取工作項 → 專案對應 → 建/重用分支 → 建 PR（可掛多張單）→ Slack 通知 |
| **發版雙 PR** | `nvl --headless --release --project <對應名> --version <x.y.z> <Release單ID> --reviewer <email>` | 對既有的 `release/vX.Y.Z` 分支開 master + develop 兩張 PR（掛上工作項）→ Slack。**不會跑 `release.sh`** |
| **Hotfix** | `nvl --headless --hotfix --project <對應名> --version <x.y.z> [--branch\|--bump\|<單ID> --reviewer <email>]` | 分三次呼叫：`--branch` 從 master 開分支、`--bump` 改版號、都不帶則開雙 PR（要給單 ID 跟 reviewer 決定） |

**發版/hotfix 的「開 PR」那步強制要求兩件事**（參數階段就擋，exit 2，碰不到 az）：

1. **至少一個工作項 ID**（通常是那張 Release 單）。master 的分支原則有「PR 必須掛工作項」
   的 required check，沒掛的 PR 會建出來但**永遠不能合併**，卡在檢查那裡。
2. **明確的 reviewer 決定**：給 `--reviewer <email>`，或明確給 `--skip-reviewer`。
   發版 PR 沒有審核者就沒人會核准、auto-complete 也沒開，會一直停在 Active。
   `/task` 那種「沒給就默默略過」的預設在發版不適用——那裡略過是低風險，這裡是卡死。

問使用者要 Release 單號跟 reviewer 的時機是**開 PR 之前**，不是撞了 exit 2 之後。

**這些是分開的呼叫，不會一次做完**：剛建好的 Task 一定還沒有 commit，建單後直接跑 PR
流程 100% 會停在 commit 關卡（exit 3）。但**開分支不受這個限制**——沒 code 也能先把分支
開出來，所以 `--branch` 是獨立一步。

完整一輪：

```
建單            只開分支           拆 commit（你用 git）        開 PR
--new  ────▶  --branch  ────▶  add 子集 + commit + push  ────▶  <taskID> [更多 ID…]
```

**要不要開分支、對哪張 Task 開，由你判斷**——建完單後不會自動開，也沒有預設「第一張」。
判斷依據見下面「分支邊界怎麼切」——一句話版本：**互相編譯相依的單合在一個分支、
一張 PR 掛多張；彼此不相干的才各自切開。** 這是使用者團隊的慣例：
一張 PR 掛多張 Task，但只有相關的才這樣掛。

中間拆 commit 那段為什麼不包進工具：判斷「哪些檔案屬於哪張單」要讀懂 code，CLI 決定
不了。細節見下面「PR 模式沒有涵蓋的那一段」。

## 這個 skill 最重要的一件事：headless 就是設計給你直接跑的

`vl`（PowerShell 版）的主流程有 `Read-Host` 互動提示，非互動 shell 硬跑會中途炸掉，
所以那邊的規矩是「只組指令、不代跑」。**headless 完全沒有互動提示，它存在的目的就是讓你
（AI）自己從頭跑到尾。** 使用者請你用這個 skill 的時候，預期是你把事情做完並回報結果，
不是每一步都回頭問一次。

**預設行為：自己判斷、自己跑、跑完回報。** 標準節奏是「先 `--dry-run` 看清楚 → 沒問題
就直接跑真的 → 回報結果」，中間不需要停下來要許可。dry-run 永遠是安全的（純讀取），
拿它當你自己的檢查步驟，而不是拿去問使用者的理由。

### 這些情況才停下來問使用者

停下來的判準是「這個決定我沒有足夠資訊、或猜錯的代價很高」，不是「這步有副作用」：

- **範圍不明確**：看不出來要建哪一層、要建幾張，而且從 Feature 描述跟既有子項也推不出來。
- **dry-run 結果跟預期不符**：對應到你沒預期的 repo/Area、或查重命中一張你不確定是不是
  同一件事的既有單（標題像但不完全一樣的情況特別容易誤判）。
- **exit 3 而且 working tree 是乾淨的**：真的還沒有人寫 code，你變不出 commit，回報就好。
  （working tree 有改動、而使用者要你 commit 的話那不是要停的情況——見「把 code commit
  到對的分支」那節，那是你的工作。）
- **拆不出某個檔案屬於哪張單**：硬塞會讓 PR 的 diff 跟單的描述不符，寧可問。
- **exit 4 / 5**：部分完成的狀態，要判斷哪些該補、哪些不能重跑。
- **工作項屬於 Go config 沒有的專案**（錯誤訊息會列出目前設定了哪些，見下方陷阱）。
- **要建的數量明顯偏多**（例如一次十幾張），先把標題清單列出來讓使用者掃一眼，
  因為建完再改標題很麻煩。

反過來說，這些不用問：跑任何 `--dry-run`、讀取工作項資訊、使用者已經講清楚要建什麼
而 dry-run 也乾淨時直接建、對已經有 commit 的 Task 開 PR、以及使用者已經交辦「幫我拆
commit」時的 `git checkout -b` / `add` / `commit` / `push`。

dry-run 值得每次都先跑，因為它會用真實資料告訴你父單型別推出哪一層、Area/Iteration
會繼承成什麼、標題有沒有撞到既有單、分支對不對、有幾筆 commit——這些都是寫下去之前
你自己該先確認的東西。

## 建單模式：依階層自動決定要建什麼

```
nvl --headless <parentID> --new "標題A" [--new "標題B" ...] [--dry-run]
```

**你不需要指定要建什麼型別，工具會依父單的型別自己推**（跟互動版共用同一條規則）：

| 父單型別 | 會建出 | 綁法 |
|---|---|---|
| Feature | Product Backlog Item | `parent`（掛在 Feature 底下） |
| Release | Product Backlog Item | `related`（Release 不在階層裡） |
| Product Backlog Item / Bug | Task | `child`（掛在 PBI 底下） |
| Task | 拒絕，exit 1 | Task 底下不再建子單 |

新單會**繼承父單的 Area 與 Iteration**，並指派給當前 az 登入者。建立前會用
「同專案＋同型別＋標題完全相同」查重，命中就略過並回報既有單號——所以同一條指令
重跑不會長出一堆重複單，這對自動化很重要。

### 給一個 Feature、要往下開單時該怎麼想

使用者常常只丟一個 Feature 單號，說「這底下要開幾張前端單」。這時候的判斷順序：

1. **先看清楚 Feature 底下現在有什麼**。用 `--dry-run` 或直接查工作項，確認既有子項
   是哪些、是前端還是後端。常見情況是 Feature 底下只有後端 PBI，前端的整條都還沒開。
2. **判斷缺的是哪一層**。Feature 底下該接 PBI，不是直接接 Task。如果前端連 PBI 都沒有，
   那就是先建一張前端 PBI，**再**在那張 PBI 底下建 Task；不要想一步在 Feature 底下塞 Task。
3. **擬標題**。沿用團隊慣例 `[標籤][前端] 描述`：標籤取自 Area Path 對應的專案
   （`ESHClouds\1.Product\Legal` → `[Legal]`），描述用中文講清楚畫面/模組。
   先把標題清單列給使用者過目再建，因為標題之後要改很麻煩。
4. **分兩次跑**：先建 PBI，拿到新 ID，再用那個 ID 建底下的 Task。輸出的「下一步」那行
   會直接告訴你該跑什麼。

範例：Feature 36261 底下只有後端 PBI，要補整條前端

```bash
nvl --headless 36261 --new "[Legal][前端] 標籤篩選改用標籤統計" --dry-run
```

確認沒問題後去掉 `--dry-run` 真的建，拿到新 PBI 例如 #36300，再建底下的 Task：

```bash
nvl --headless 36300 --new "[Legal][前端] 我的最愛列表 標籤篩選改用標籤統計" --new "[Legal][前端] 法規守規性報表 標籤篩選改用標籤統計"
```

### 建單模式不接受的旗標

`--reviewer` / `--skip-slack` / `--skip-reviewer` 跟 `--new` 一起給會直接 exit 2 擋掉。
因為建單不會建 PR，這些旗標放進來是無效的，通常代表使用者誤以為一次指令能建單又開 PR。

## 開 PR 模式

```
nvl --headless <taskID> [--dry-run] [--skip-slack] [--reviewer <email>|--skip-reviewer]
```

這個模式只吃 **`Task` 型別**（給 PBI 或 Feature 的 ID 會以 exit 1 拒絕，因為互動版在這裡是
讓人挑要處理哪張子 Task，headless 沒有人可以問）。而且它假設本機該分支**已經有 commit
並 push 上去**——沒有的話會停在 exit 3。

### PR 模式沒有涵蓋的那一段：把 code commit 到對的分支

headless 只做「分支 → PR → Slack」，**它不會幫你 commit，也不會動本機的 working tree**。
「把散在 working tree 的改動拆給各張 Task、commit 到各自的分支」這件事是**你自己用
`git` 做**——這是刻意的分工，因為判斷「哪些檔案屬於哪張單」需要讀懂 code，不是 CLI
能決定的事。

先看 working tree 的狀態決定走哪條路，這一步別跳過（`git -C <localPath> status --porcelain`）：

**A. 已經有未提交的改動（最常見）→ 本機先開分支**

```bash
# 1. 先問出分支名（唯讀，不會建立任何東西）
nvl --headless <taskID> --dry-run          # 讀輸出的「分支:」那行

# 2. 本機開分支並切過去（未提交的改動會跟著過去，不會遺失）
git -C <localPath> checkout -b <那個分支名>

# 3. 只 add 屬於這張單的檔案，然後 commit
git -C <localPath> add <這張單的檔案...>
git -C <localPath> commit -m "<type>: <中文描述>"

# 4. push（headless 是看 origin 上的 commit，沒 push 它看不到）
git -C <localPath> push -u origin <分支名>

# 5. 這時候才跑 headless 開 PR（相關的多張單掛同一張 PR）
nvl --headless <taskID> [其他相關 taskID…] --reviewer <email>
```

這條路不需要 `--branch`——本機 `checkout -b` 加 `push -u` 就等於把分支開出來了，
headless 開 PR 時會偵測到既有分支直接重用。

**B. 還沒開始寫 code，只是要先把分支準備好 → 用 `--branch`**

```bash
nvl --headless <taskID> --branch     # 分支建在遠端，exit 0，不碰 commit 也不建 PR
```

這是你要「先有分支才能 commit」時的正確做法。之後
`git fetch && git checkout <branch>` 就能接上 A 的第 3 步。

**不要**為了開分支而跑不帶 `--branch` 的完整流程——那會一路走到 commit 關卡撞 exit 3。
分支雖然也會被建出來，但輸出看起來像失敗，容易被誤判。`--branch` 就是為了避免這個。

**分支名一律從 `--dry-run` 的輸出讀，不要自己拼。** 推導規則（剝標籤、去括號空白、
限 50 字元）藏在 Go 裡，自己組很容易跟工具算出來的不一致。順帶一提，重用判斷只看
「分支名有沒有包含這個 task ID」，所以名字不用完全一致也接得上——但沒理由不一致。

### 怎麼判斷哪些檔案屬於哪張單

沒有標準答案，這是要讀 code 的判斷。可靠的方法依序是：

1. **拿 Task 標題去對目錄／模組名**。標題通常直接點名畫面，例如「我的最愛列表」對
   `pages/my-favorite/`、「法規守規性報表」對 `pages/report/compliance/`。
2. **找 code 裡本來就存在、跟這批單一一對應的列舉或常數**。這種重構常常會新增一個
   enum，成員剛好就是各張單的範圍——那是作者自己的拆法，比你猜的準。
3. **共用層（`access/repository/`、`shared/models/`、`shared/enums/`）是自己一張單**，
   標題裡有「共用層」「API 改版」字樣的通常就是它。注意這只決定「檔案歸哪張單」，
   不代表它要獨立一個分支——其他頁面 import 它的話，分支要合在一起，見下面
   「分支邊界怎麼切」。在同一個分支裡它應該是**第一個 commit**。
4. **對不上的就停下來問**，不要硬塞進某張單。典型情況是列舉只有 4 個成員但單有 5 張，
   或某個檔案兩張單都沾得上——猜錯會讓 PR 的 diff 跟單的描述不符，之後很難查。

commit 完之前用 `git status` 再看一遍：**剩下的未提交檔案應該剛好是其他單的**。如果
剩下你完全解釋不了的檔案，那就是拆錯了或有無關改動混進來，停下來問。

commit 訊息照**該 repo 既有慣例**（跑 `git log --oneline -20` 看，不要憑印象）。
`legal` / `chem` 目前是 Conventional Commits 加中文描述，例如
`fix: 新增或複製查核任務，調整錯誤訊息`、`chore: auth網址修改`。

### 分支邊界怎麼切：用「編譯相依性」判斷，不是「一單一分支」

這是最容易做錯的決定，判準只有一條：

> **B 的改動沒有 A 的改動就編不過 → A 和 B 必須在同一個分支、同一張 PR（掛多張單）。
> 各自獨立能編譯 → 各自一個分支、各自一張 PR。**

理由很實際：分支都是從 `develop` 切出來的，如果 4 張單的頁面改動都 import 共用層新增的
型別／列舉，那把共用層單獨切一個分支、其他 4 張各切一個，**後面那 4 個分支單獨拿出來
都是編譯不過的**（它們找不到還沒合進 develop 的型別）。CI 會紅、reviewer 也沒辦法看。

所以典型的「共用層 + N 個頁面套用」重構（一次 API 改版、一次型別重構），正確做法是
**一個分支、一張 PR、掛上全部相關的單**：

```bash
# 共用層那張當主要（決定分支名），其餘一起掛
git -C <localPath> checkout -b task/<共用層ID>-<slug>
git -C <localPath> add <全部相關檔案>
git -C <localPath> commit -m "refactor: 標籤篩選改用標籤統計 API"
git -C <localPath> push -u origin task/<共用層ID>-<slug>
nvl --headless <共用層ID> <頁面ID1> <頁面ID2> … --reviewer <email>
```

真正該切開的是**彼此不相干**的工作：同一個 sprint 裡「標籤重構」跟「修登入 bug」是兩件
事，各自一個分支一張 PR，即使它們都掛在同一個 PBI 底下。

想切細一點也可以在**同一個分支裡分多個 commit**（一個 commit 對一張單），這樣 PR 的
commit 歷史仍然看得出哪些改動屬於哪張單，而且不會有編譯不過的問題。這通常是最好的
折衷：**分支/PR 用相依性切，commit 用單切。**

旗標跟 ID 的順序不拘，`--reviewer` 也吃 `--reviewer=someone@wishingsoft.com` 寫法。

| 旗標 | 效果 |
|---|---|
| （不帶任何旗標） | 完整跑完，但**預設略過 reviewer**（不加必要審核者、不設 auto-complete、連帶不發 Slack） |
| `--dry-run` | 唯讀模擬。照樣真的去查工作項、專案對應、分支、commit，但不建分支/不開 PR/不發 Slack |
| `--branch` | 只把分支準備好就停（不查 commit、不建 PR、不需要本機 repo）。不能跟 reviewer/Slack 旗標或 `--new` 併用 |
| `--skip-slack` | 建 PR 但不發 Slack（reviewer 照加） |
| `--reviewer <email>` | 指定必要審核者**並開啟 auto-complete**；不限於系統列出的候選清單（等同 PS 版的 `-Reviewer`） |
| `--skip-reviewer` | 明確表示不要 reviewer（跟「不帶旗標」同義，寫出來只是讓意圖明確） |

**`--reviewer` 的連帶效果要講給使用者知道**：它會同時打開 auto-complete。人工關卡還是有
——必要審核者得真的按核准——但**核准之後就沒有第二道關卡了**：不會停下來等人按「完成」，
會直接合併進 base branch，掛上的工作項也一起轉狀態（`--transition-work-items` 固定開）。
這是設計如此、不是 bug，也沒有旗標可以關掉。回報結果時要把這條講明：「等 ahbao 核准後
就會自動合併，5 張單會一起變 Done」，讓使用者知道核准是最後一道關卡而不是中間一站。

**多個 Task ID**：開 PR 模式可以給多個 ID（`nvl --headless 36346 36347 36348`），
**第一個決定分支名**、全部一起掛到同一張 PR——對應「一張 PR 掛多張相關 Task」的慣例，
跟互動版「第一個選擇＝主要 Task」是同一個規則。重複給同一個 ID 會自動去重。
只有相關的改動才這樣掛；不相關的各自跑一次。

**為什麼零旗標時是略過 reviewer 而不是自動挑一個**：候選清單的順序只是
`az devops team list` 剛好回傳的順序，沒有任何業務意義。在無人看管的自動化情境下拿它
指派一個真人當必要審核者、還開 auto-complete，是在賭運氣。要指定就明確給 `--reviewer`。

## exit code：這是自動化情境下唯一該信的東西

| code | 意思 | 該怎麼反應 |
|---|---|---|
| 0 | 成功（單建好；或 PR 建好且 Slack 發成功／本來就略過） | 完成 |
| 1 | 一般失敗：工作項抓不到、型別不對（開 PR 模式非 Task／建單模式是 Task）、專案對應不到、本機路徑不是 git repo、建分支或建 PR 失敗、建單全部失敗 | 讀錯誤訊息對症處理 |
| 2 | CLI 參數錯誤（缺 ID、旗標衝突、不認得的旗標——**不帶 `--headless` 時也一樣 exit 2，不會退回 TUI**）。**在碰任何 az/git 之前就結束，保證沒有副作用** | 修指令重下 |
| 3 | commit 檢查沒過：分支還沒 push，或分支相對 base 沒有新 commit | **要人先去寫 code/push，不是重跑就會好**。排程器可以選擇晚點再試 |
| 4 | PR 已經建好了，但 Slack 通知失敗 | PR 是有效的、連結在輸出裡，只是沒人被通知到。別把它當成「PR 失敗」去重跑，會變成開兩張 PR。（會另印一行警告到 stderr，讓只監看 stderr 的排程器也收得到。）**修法是請使用者跑互動模式的 `/init` 重設 Slack token**（見下方） |
| 5 | 建單模式部分成功（有些標題建好、有些失敗） | 看輸出逐行核對哪些成功。**只針對失敗的標題重跑**，成功的會被查重擋掉所以其實整條重跑也安全，但逐條比較清楚 |

exit 3 跟 exit 1 刻意分開，就是為了讓自動化能區分「工具壞了要告警」跟「在等人寫 code」。
exit 4 也刻意不併進 0 或 1，因為那兩種反應都是錯的（當成功會漏掉沒人 review；當失敗會重複開 PR）。
exit 5 同理：整批當失敗會讓人以為什麼都沒建，實際上有些已經在了。

## 領域知識與陷阱

- **兩個別名不是同一個工具**：`vl` = 舊版 PowerShell（`C:\test\very-lazy\main.ps1`）；
  `nvl` = Go 版 `vlui.exe`。`--headless` 只有 Go 版有，對 `vl` 下 `--headless` 不會有用。
  在 `C:\test\very-lazy\cli` 目錄裡用 `go run . --headless <id>` 是改原始碼時的本機開發模式。

- **設定檔是兩份不同的檔案，而且內容不一樣**（這是目前最容易踩的坑）：
  - PS 版讀 repo 根目錄 `C:\test\very-lazy\config.json`
  - Go 版（含 headless）讀 `%AppData%\very-lazy\config.json`

  兩邊的 `azureProjectMappings` 內容不同步——PS 版有 15 個專案對應，Go 版**只有使用者
  跑過 `/init` 設定過的那幾個**（會隨時間增加，別背清單；要知道現在有哪些，讀
  `%AppData%\very-lazy\config.json` 的 `azureProjectMappings` keys，或看專案對應失敗時
  錯誤訊息裡列的清單）。工作項屬於還沒設定的專案時，「專案對應」這步會以 exit 1 失敗。
  遇到這種失敗，先確認是不是這個原因，而不是急著懷疑 headless 壞了。**正確處理方式是
  通知使用者去跑互動模式的 `/init` 把那個專案設定進去**——不要自己去編輯 config，
  headless 刻意不寫設定。

- **需要本機 git repo**：headless 靠本機 repo 跑 `git log origin/<base>..origin/<branch>` 生 PR 描述，
  所以該 mapping 一定要有可用的 `localPath`（或 `projectPaths` 裡有對應）。
  沒有的話會以 exit 1 失敗，**headless 不會幫你 clone**——clone 是「第一次設定這個專案」
  才做的事，屬於互動模式 `/init` 的職責。遇到這個失敗就請使用者先 clone 或跑 `/init`。
  `--branch` 是唯一不需要本機 repo 的模式（建分支是 server-side 操作）。

- **每個 az / git 呼叫都有 120 秒 timeout**（可用環境變數 `VLUI_TIMEOUT_SEC` 覆蓋）。
  正常呼叫是幾秒級，所以逾時代表真的有問題而不只是慢。**逾時的錯誤訊息會提醒先確認
  該動作是不是其實已經生效再重試**——例如 `az repos pr create` 逾時但伺服器端其實建好了，
  直接重跑會開出第二張 PR。

- **`/task` 開 PR 的 base branch 固定是該 mapping 的 `defaultBranch`**（沒設就 `develop`），
  沒有旗標可以改。「要進 master」的需求走的是 `--release`／`--hotfix`（master + develop
  雙 PR），不是幫 `/task` 換 base——一般 Task 的 PR 本來就不該直接進 master。

- **PR 標題是分支名，描述是 commit 清單**，這是沿用互動版與 PS 版的既有行為，不是 bug。

- **不要用 `vl`（PS 版）建 Feature 底下的單**。PS 的 `Start-Task` 只特判 Task/Release，
  其他型別一律走 `New-Task`，而 `New-Task` **固定建 `Task` 型別**並掛在你給的那張單底下——
  所以 `vl <FeatureID> "標題"` 會直接在 Feature 底下生出 Task、跳過 PBI 層，結構是錯的。
  階層判斷只有 Go 版有（互動導航器與這裡的 `--new`）。

- 工作項都在 `ESHClouds` team project（建單時 `--project` 用的是 config 的 `workItemProject`，
  不是 code 專案）；repo 在各自的 Azure 專案（`Chem`、`Legal`…）。

- **建單查重是「完全相同標題」比對**。差一個字、全角半角不同、多一個空白都算不同標題、
  會照建。要靠查重達到重跑安全，標題就得逐字一致。

- **Task 建立失敗是逐條獨立的**：`createTasksCmd` 一張一張建，某張失敗不影響其他張，
  所以會有 exit 5（部分成功）。掛父層失敗則是「非致命」——單本身建好了只是沒掛上去，
  輸出會明講要手動補掛，不會當成失敗。

- `config.local.json` 存 Slack token，**絕對不要讀取或顯示**；`slackConfig` 含成員個資，
  不要輸出到對話裡。

- **發版是兩段，`release.sh` 不由 headless 跑**：`--release` 只負責「對既有的
  `release/vX.Y.Z` 分支開 master + develop 兩張 PR + Slack」。分支是 `release.sh`
  建的，那支腳本還會改版號、跑 `npm run build:prod`（幾分鐘）、commit、push。
  **而且它的 ERR trap 裡有 `read -p "請按 Enter 關閉..."`**——失敗時會等鍵盤輸入，
  在 stdin 不會有輸入的環境下會永遠卡住，而且卡在「已經 checkout 新分支、可能已改版號」
  的中途狀態。所以正確的順序是：

  1. **由你（AI）幫使用者跑 `release.sh <版號>`**，把輸出貼出來（尤其 build 結果）
  2. 使用者確認 build 正常
  3. 才跑 `nvl --headless --release --project <對應名> --version <x.y.z> <Release單ID> --reviewer <email>`

  第 3 步會先檢查 origin 上有沒有那條分支，沒有就以 exit 3 擋下並要你先去跑 `release.sh`
  ——不會硬建一條空分支開出沒內容的 PR。

### `release.sh` 被權限系統擋下時的固定 fallback 順序（不要各自即興）

執行整支 `release.sh`（會 build + push）有時會被權限分類器擋下——這跟指令的組合方式與
當下情境有關，同一份 skill 在不同對話可能一個放行一個擋，你控制不了它，但被擋之後的
行為必須一致。**依序 fallback，不要跳步、也不要直接放棄**：

1. **整支跑**：`bash release.sh <版號>`（在專案目錄）。被擋才進下一步。
2. **拆步等效執行**——每個單一指令會各自過權限審核，通常放得行。動手前**必須先讀懂
   兩支腳本**：該專案的 `release.sh`，以及 `npm run release` 實際指到的腳本（各專案不
   同，例如有的 `release.js` 只改 `package.json` 跟一個 config 常數檔）。然後照
   `release.sh` 的順序逐步做：

   ```bash
   git checkout develop && git pull        # （被擋就再拆成兩條）
   git checkout -b release/v<版號>
   # 改版號：npm run release <版號>；連這也被擋就用編輯工具改「腳本會改的那幾個檔案」
   npm run build:prod                      # 驗證關卡，絕不可跳過
   git add <只有版號相關的檔案> && git commit -m "release: v<版號>"
   git push -u origin release/v<版號>
   ```

   守則：(a) commit 訊息**逐字用 `release: v<版號>`**，不要加任何多餘的行（等效執行的
   目標是產出跟腳本一模一樣的結果）；(b) commit 前用 `git status` 確認**只有**版號相關
   檔案被改；(c) `build:prod` 失敗就停下回報，不要 push 一個沒過 build 的發版分支。
3. **兩條路都被擋才交還使用者**：把指令組好請使用者自己跑，跑完回來接第 3 步開 PR。

已發生過的實例：Permit v2.3.0 走了第 1 步（放行）、License v5.1.0 走了第 2 步（整支被
擋、拆步全放行、結果與腳本一致）、Legal v7.14.5 當時 skill 還沒寫這段所以直接停在第 3
步——三種都「對」，但使用者體驗不一致，這就是為什麼順序要固定下來。

- **`--release` 開 PR 必須掛工作項、必須明確決定 reviewer**（詳見上方「發版/hotfix 的
  『開 PR』那步強制要求兩件事」）。舊版曾拒收工作項 ID，那是錯的——master 分支原則
  要求 PR 掛單，沒掛會卡在 required check 永遠合不了。

- **`--release` 的 master PR 刻意保留來源分支**（`--delete-source-branch false`），
  因為 develop 那張 PR 還要用同一條分支；develop 那張才會在合併後刪掉分支。
  如果 master 成功、develop 失敗，輸出會印出 master 的 PR 連結——**那張 PR 已經存在了，
  不要整條重跑**，否則會多出一張 master PR。

- **Hotfix 是三次呼叫**，切點就在兩個需要人的地方：

  ```bash
  # 1. 從 master 開 hotfix/vX.Y.Z 並 push（要求工作目錄乾淨；分支已存在就沿用）
  nvl --headless --hotfix --project legal --version 7.14.4 --branch
  #    ↑ 接著使用者在這個分支上完成修正、commit、push

  # 2. 改版號（npm ci + npm run release + build:prod + commit + push，要幾分鐘）
  nvl --headless --hotfix --project legal --version 7.14.4 --bump

  # 3. 開 master + develop 兩張 PR + Slack
  nvl --headless --hotfix --project legal --version 7.14.4 <單ID> --reviewer <email>
  ```

  跟 `/task` 不同，hotfix 全程走**本機 git**（不是 az 建分支），所以一定要有可用的
  `localPath`，連 `--branch` 那步也要。第 1 步會先擋「工作目錄有未 commit 的變更」。
  第 3 步會先確認 origin 上有那條分支，沒有就 exit 3 叫你先跑 `--branch`。

  `--bump` 那步**沒有 timeout**（prod build 本來就要幾分鐘），輸出直接串到終端機；
  子行程的 stdin 是關著的，所以腳本裡任何想等輸入的地方會立刻 EOF 而不是卡住。
  它是一連串 `&&` 串起來的動作，中途失敗可能已經改了版號或 commit——錯誤訊息會提醒
  先看 `git status` / `git log` 再決定怎麼重跑。

  第 3 步會檢查 commit 清單裡有沒有 `release: v<版號>` 那筆，**沒有就印警告說版號可能
  還沒改**（不擋，因為你可能另有做法）。漏改版號是這條流程最常見的失誤。

## 常見錯誤解讀

| 輸出/症狀 | 原因與建議 |
|---|---|
| `結果: 失敗 (型別檢查): #N 是 Product Backlog Item，不是 Task` | 開 PR 模式給錯 ID 了。要嘛改用 `--new` 在這張 PBI 底下建 Task，要嘛找出底下已經有的那張 Task 的 ID |
| `結果: 失敗 (階層檢查): #N 是 Task，底下不支援建立子單` | 對 Task 用了 `--new`。Task 是最底層，要建同層的單請對它的父 PBI 下指令 |
| 建單跑完但 `已存在同名 X #M，略過` | 查重命中，這是保護機制不是錯誤。確認 #M 真的是你要的那張；如果只是標題剛好一樣但要另外建一張，改個標題讓它有區別 |
| `已建立，但綁父層失敗` | 單建好了但沒掛上父層。去 Azure DevOps 手動把它掛到輸出提到的那張父單底下，不用重建 |
| exit 5 部分失敗 | 看輸出逐行核對。整條重跑是安全的（成功的會被查重擋掉），但建議只重跑失敗的標題 |
| `結果: 失敗 (專案對應)` | 該專案還沒在 Go 版 config 設定過（錯誤訊息會列出目前有哪些）。請使用者跑互動模式的 `/init` 把它加進去，不要自己編輯 config |
| `結果: 失敗 (本機路徑)` | 該 mapping 沒設 `localPath`，或路徑不是 git repo。補設定，不要期待它自動 clone |
| exit 3 `分支 X 還沒 push 到遠端` | 分支只存在本機，headless 看的是 origin。`git push -u origin <branch>` 後再跑 |
| exit 3 `相對 develop 沒有新 commit` | 分支是空的（通常是剛建好）。**先看 `git status`**：working tree 有改動就是該去拆 commit（見「把 code commit 到對的分支」），乾淨才是真的在等人寫 code。這是刻意的關卡不是 bug |
| exit 4 Slack 失敗但 PR 成功 | 別重跑！PR 已經在了。**請使用者跑互動模式的 `/init` 重新設定 Slack token**，並把 PR 連結手動貼給 reviewer 補上這次的通知 |
| Slack 回 `account_inactive` | 這是**token 自己的身分被停用或 app 被移除**，不是 reviewer 的帳號有問題。代表**之後每次跑到 Slack 都會失敗**，不是一次性的——一定要跑 `/init` 換新 token，否則每次都會停在 exit 4 |
| 中文變亂碼 | az CLI 在 Windows 用系統碼頁輸出。Go 版已經繞過（直接呼叫 az 內建的 `python.exe -X utf8`），如果還是亂碼是新問題，值得回報 |
| 想確認到底會做什麼 | 一律先 `--dry-run`。它會用真實資料印出分支、專案對應、commit 筆數、reviewer 決定 |
