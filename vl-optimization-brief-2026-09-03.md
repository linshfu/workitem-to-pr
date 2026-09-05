# vl / nvl 優化提議（2026-09-03）

> 這份是給**下一個對話**的起始簡報。內容全部來自 2026-09-03 一次真實的 Chem 發版流程，
> 每條提議都附當時的證據。不是憑空發想的 wishlist。
>
> 讀完直接從「下一步」那節開始做，不用回頭問前一個對話。

---

## 0. 那天發生了什麼（背景）

任務：修 Chem 的 Bug 36623（化學品配置圖，完成區域繪製後方塊無法跟隨滑鼠移動）。

實際走過的路：

1. 改完 code（4 個檔案，+430/−215），型別檢查過
2. `nvl --headless 36623 --new "..."` → 建出 Task #36627
3. 使用者說「這次算 hotfix」→ 走 `--hotfix --branch` / `--bump` / 雙 PR
4. **`--bump` 炸了**：`npm ci` 撞到 dev server 的檔案鎖，`node_modules` 半毀
5. 手動繞過後開出雙 PR → **兩張 PR 在 1 分鐘內自動合併**，master 建置完成
6. 使用者發現版號搞錯：這批應該跟 Release 36606（v5.32.2）一起上，不是獨立的 5.32.1
7. 所幸正式機沒被動到——真正的閘門在 release pipeline 的 `Swap to Prod`，還卡在待核准
8. 取消該 release → 走正規 `release.sh 5.32.2` + `nvl --release` → 雙 PR 開好

整條線最後是成功的，但中間踩到的幾個坑都是**工具可以幫忙擋掉、但目前沒擋**的。

---

## 1. 提議清單

依「投報比 × 今天實際造成的損害」排序。

### P1 ─ `nvl --version`，取代掃執行檔的版本檢查儀式 🔴

**現況**：`vlui-headless` skill 的第一段要求先跑這個：

```powershell
[System.Text.Encoding]::ASCII.GetString([System.IO.File]::ReadAllBytes("$env:LOCALAPPDATA\Programs\very-lazy\vlui.exe")) -match '--headless'
```

用 ASCII 掃整顆 binary 找字串，來判斷它認不認得某個旗標。

**為什麼會變成這樣**：舊版 `vlui.exe` 遇到不認得的 `--headless` **不會報錯，會靜默開啟互動 TUI 然後卡在等按鍵**。在非互動 shell 裡就是一個永遠不回來的指令。

**提議**（兩件事，都要做）：

1. **`nvl --version` 印出版本號**，skill 改成「跑 `nvl --version`，低於 0.4.0 就走原始碼模式」。
2. **根本修法：未知旗標一律 exit 2，不要 fallback 進 TUI。** 這樣以後新增任何旗標都不需要再發明一次檢查儀式。

第 2 點救不了已經流出去的舊版，所以第 1 點還是需要——但它讓這個問題有終點。

---

### P2 ─ 補一個 read 指令：`nvl --headless --status <project>` 🔴

**證據**：那天要回答「這版上正式機了沒」，走 ADO MCP 的 `pipelines_build list`：

- 回傳 **76,901 字元 / 1,754 行**，超過單次工具輸出上限，被迫存檔
- 再 grep 兩次才拼出答案
- **而且結論還是錯的**——看到 master build 綠燈就推論已上線，漏掉 `Swap to Prod` 是獨立的人工關卡

**提議**：印最近 3–5 筆 release 與各 stage 狀態，五行以內。

```
$ nvl --headless --status chem
Chem-web-master
  Release-405  20260903.1  master   Deploy To Dev Site: ✓   Swap to Prod: ⏸ 待核准 (3h)
  Release-404  20260825.2  master   Deploy To Dev Site: ✓   Swap to Prod: ✓
  Release-403  20260825.1  master   Deploy To Dev Site: ✓   Swap to Prod: ✓
```

**判準（重要，別無限擴張）**：同一個 read 問題問到第三次，才值得寫成指令。一次性的探索留給
MCP——為每個可能的問題寫 subcommand 是輸家遊戲。目前只有這一個達標。

---

### P3 ─ `--bump` 動手前先擋掉 dev server 🔴

**證據**：`--bump` 的第一步是 `npm ci`。`npm ci` **會先清空 `node_modules` 再重裝**。當時有
`ng serve` 在跑，它的 esbuild 子行程握著 `node_modules/@esbuild/win32-x64/esbuild.exe`，
`npm ci` 在「已刪掉大半、正要覆蓋那顆 exe」時以 `EPERM unlink` 失敗。

結果：

- `node_modules` 半毀（頂層剩 105 個、`.bin` 全空、`ng` 不見）
- **但 `git status` 完全乾淨** → 很容易誤判成「失敗在第一步，什麼都沒改」（當時就是這樣誤判的）
- dev server 因為都已載入記憶體還繼續跑，更難發現
- 使用者被迫中斷正開給別人測試的 server 才能收拾

**提議**：

1. **前置檢查**：`--bump` 在碰任何東西之前，偵測有沒有工作目錄指向該 `localPath` 的
   node/esbuild 行程。有就 exit 2，印出 PID 與「請先停掉 dev server」。exit 2 的語意
   正好是「參數/前置條件錯誤，保證沒有副作用」，符合現有 exit code 規範。
2. **失敗後的診斷**：`npm ci` 非零退出時，順手檢查 `node_modules/.bin` 是否為空，
   是的話明確印出「`node_modules` 已半毀，需要重跑 `npm ci`（先確認檔案鎖已釋放）」。
   不要讓使用者只看 `git status` 就以為沒事。

---

### P4 ─ 開 PR 前把「會不會自動合併」講出來 🟠

**證據**：那天用 `--reviewer` 開的兩張 hotfix PR，**建立於 04:26 UTC，04:27 就已合併，
全程沒有任何人按過按鈕**。原因是 master 分支根本沒有人工核准政策，auto-complete 在建置
政策通過的瞬間就生效了。

當時的判斷是「有指定 reviewer = 有人工關卡，所以開 PR 是安全的」。**這個假設是錯的**，
而且錯得看不出來——skill 有寫「`--reviewer` 會同時打開 auto-complete」，但沒寫
「目標分支可能根本不需要核准」。

**提議**：開 PR 前（以及 `--dry-run` 時）查一次目標分支的 branch policy，明確印一行：

```
⚠ master 沒有必要核准者政策 → 這張 PR 會在建置通過後「自動合併」，沒有人工關卡
  develop 需要 1 位核准 → 會停在等待核准
```

成本低（多一次 policy API 呼叫），價值高——它把一個隱藏的政策變成輸出的一行字。
對 AI 使用者尤其重要：我們是照輸出做判斷的，沒印出來的東西就等於不存在。

**順帶**：skill 的「發版 PR」那段可以補一句——合併進 master **不等於上正式機**，
Chem 的正式機閘門在 `Chem-web-master` release pipeline 的 `Swap to Prod` stage。

---

### P5 ─ `release.sh` 的 ERR trap 會在非互動環境卡死 🟠

> 注意：`release.sh` 不在 very-lazy repo 裡，它在**各專案的 repo 根目錄**
> （例如 `C:\front\chem\release.sh`）。這條要逐專案改，或做成範本統一發。

**現況**：

```bash
trap 'echo "❌ 發生錯誤..."; read -p "請按 Enter 關閉..."; exit 1' ERR
```

`read -p` 在 stdin 沒有輸入的環境會**永遠卡住**，而且卡在「已經 checkout 新分支、
可能已改版號」的中途狀態。skill 為此寫了一整段警告。

那天的繞法是 `bash release.sh 5.32.2 < /dev/null`（讓 read 立刻 EOF）。

**提議**：改成只在互動終端才等按鍵。

```bash
trap 'echo "❌ 發生錯誤..."; [ -t 0 ] && read -p "請按 Enter 關閉..."; exit 1' ERR
```

三個字元的修改，可以從 skill 裡刪掉一整段警告。**能推進腳本的就別留在 skill 裡。**

---

### P6 ─ `release.sh` 的 `git add .` 太寬 🟡

**現況**：發版腳本用 `git add .`，會把工作目錄裡任何殘留檔案掃進發版 commit。

**諷刺的是**：skill 在「拆步等效執行」的 fallback 段落已經要求人只 add 版號相關檔案
（`git add <只有版號相關的檔案>`），腳本本身反而沒這個約束。

**提議**：只 add `npm run release` 實際會動的兩個檔案：

```bash
git add package.json src/app/shared/constants/global-config.const.ts
```

（路徑各專案不同，可從 `script/release.js` 讀。）

---

### P7 ─ 持續把 skill 裡的知識往 binary 推 🟡

這不是單一改動，是一條原則：**skill 的散文是建議，binary 的行為是規則。**
能編碼進程式的就別留在散文裡——散文會漂移，而且 AI 在長對話裡會偏離。

目前 skill 裡還屬於「散文型知識」、可以考慮往下推的：

| skill 裡的內容 | 可以怎麼推進 binary |
|---|---|
| 版本檢查儀式 | → P1 `--version` |
| 「開 PR 前要先問 reviewer，不要撞了 exit 2 才問」 | 已由 exit 2 強制，散文只需保留「時機」提醒 ✓ |
| 「專案對應不到時去跑 `/init`」 | 錯誤訊息已列出現有 mapping ✓ 做得好 |
| 「分支邊界用編譯相依性切」 | **無法編碼**，這種判斷本來就該留在 skill |
| 「exit 4 別重跑」 | 已由 exit code + stderr 警告表達 ✓ |

打勾的是已經做對的，別動。真正待辦的只有 P1。

---

## 2. 明確**不要**做的事

- ❌ **不要把查詢類功能大規模搬進 nvl。** MCP 在臨時查詢上是對的工具。只補 P2 那一個，
  因為那是反覆問、而且 MCP 回傳量爆炸的特例。
- ❌ **不要動現有的輸出格式。** 目前每個指令固定幾行、結尾有「下一步：...」提示，
  這對 AI 使用者非常好用（明確、可解析、告訴我接下來該跑什麼）。新指令沿用同一套。
- ❌ **不要改 exit code 的既有語意。** 0/1/2/3/4/5 目前分得很準，尤其
  exit 3（在等人寫 code）跟 exit 1（工具壞了）分開這件事。新增狀況請沿用，別重編號。
- ❌ **不要碰 `config.local.json`**（Slack token）與 `slackConfig`（成員個資）。

---

## 3. 下一步

建議順序：**P1 → P3 → P2 → P4**，然後 P5/P6（那兩條在各專案 repo，不在 very-lazy）。

P1 跟 P3 都是「防止工具靜默造成損害」，優先做。P2 是省 token 與避免誤判。
P4 是資訊性的但影響決策品質。

開工前先跑：

```bash
cd /c/test/very-lazy/ui-lab && git log --oneline -10
```

確認目前的 binary 版本與 skill 記載的一致（skill 底部有 `<!-- managed by vlui -->`，
更新後會自動刷新）。發新版走 `.\release.ps1 <版號>`，不要本機 build 亂填版號。

---

## 附錄：token 成本的實測對照

同一次對話裡的實際數字，作為「CLI vs MCP」取捨的參考：

| 操作 | 路徑 | 輸出量 |
|---|---|---|
| 建 Task（含綁父層、繼承 Area/Iteration、查重） | `nvl --new` | 6 行 ≈ 100 tokens |
| 開雙 PR ＋掛單＋reviewer＋auto-complete＋Slack | `nvl --release` | 8 行 ≈ 120 tokens |
| 查一張工作項完整內容 | MCP `wit_work_item get` | ≈ 3,000 tokens |
| 查 5 張單狀態（有加 `fields` 投影） | MCP `get_batch` | ≈ 700 tokens |
| 查最近 12 筆 build | MCP `pipelines_build list` | **76,901 字元 ≈ 19,000 tokens**（實測，爆掉存檔） |

除了最後一列是實測，其餘為估計。

結構性差異：ADO MCP 約 40 個工具，全部常駐大約每個 request 吃 15–20k tokens 的 schema
（本次是 deferred 載入，只載了 5 個 ≈ 3,000 tokens）。`nvl` 的 schema 成本是**零**，
skill 那份散文約 5–6k tokens 但整個 session 只在觸發時載入一次。

工具數量往上加時，這個差異會放大。
