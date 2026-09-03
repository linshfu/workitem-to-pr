# 交辦文案（給使用者複製貼上用）

這份是給**人**看的：要請 AI 用 `vlui-headless` skill 跑流程時，可以直接複製下面的句子。
不需要在句子裡指名 skill，描述夠具體就會自動觸發；真的沒觸發時在句尾加一句
「用 vlui-headless skill」。

---

## 1. 從 Feature 往下開整條單（最常用）

```
Feature 36261 底下前端的單還沒開，幫我往下看缺什麼、依階層把單建出來。
標題照 [Legal][前端] 慣例命名，內容你依 Feature 的說明擬。
建之前先 dry-run 確認，標題列給我看一眼再建。
```

想直接授權它做完、不要中間停下來：

```
Feature 36261 底下前端的單還沒開，幫我建出來（先 PBI 再底下的 Task）。
標題你擬，dry-run 確認沒問題就直接建，跑完把單號回報給我。
```

## 2. 已經有 PBI，只要建底下的 Task

```
PBI 36300 底下幫我建這幾張前端 Task，標題照慣例：
標籤查詢API改版 共用層與適用性清單列表標籤側欄
我的最愛列表 標籤篩選改用標籤統計
法規守規性報表 標籤篩選改用標籤統計
符合性判別 標籤篩選改用標籤統計
新增編輯查核任務 挑選法規 標籤篩選改用標籤統計
```

## 3. 開分支（還沒寫 code 的階段）

```
Task 36310 幫我把分支開出來，我要切過去寫 code。
```

> AI 會跑 `nvl --headless 36310`，分支建好後因為還沒有 commit 會停在 exit 3——
> **這是正常的**，分支已經在了。AI 應該回報分支名稱請你去 commit。

## 3b. code 已經寫好但還沒 commit，要拆給各張單

這是「改動全散在 working tree、單也開好了、但一個 commit 都還沒有」的情況。

挑一張先跑通：

```
PBI 36344 底下的單都開好了，code 我寫在 working tree 還沒 commit。
先挑共用層那張，開分支、把屬於它的檔案 commit 上去，push 完幫我開 PR。
其他單的檔案先留著不要動。
```

一次把整批拆完：

```
PBI 36344 底下 5 張單，code 都在 working tree 還沒 commit。
幫我一張一張來：判斷哪些檔案屬於哪張單、各自開分支 commit + push + 開 PR。
共用層那張要先做。拆不出來的先問我，不要硬塞。
```

> AI 會先 `git status` 看有哪些改動、用 `--dry-run` 問出分支名，再本機
> `git checkout -b` → `git add <子集>` → commit → push → 跑 headless 開 PR。
> commit 訊息會照 repo 既有的 `<type>: <中文>` 慣例。

## 4. 寫完 code 了，要開 PR

```
Task 36310 我已經 commit + push 了，幫我開 PR，reviewer 找 tony。
```

不指定 reviewer（不加必要審核者、不設 auto-complete、也不發 Slack）：

```
Task 36310 已經 push 了，幫我開 PR，reviewer 不用加。
```

只開 PR 但這次不要吵 Slack：

```
Task 36310 已經 push 了，開 PR、reviewer 給 tony，但 Slack 先不要發。
```

## 5. 一次交辦整條（建單 → 開分支 → 等我寫 → 開 PR）

```
Feature 36261 的前端我要開始做。幫我：
1. 依階層把前端的 PBI 跟底下的 Task 建出來（標題你擬，照 [Legal][前端] 慣例）
2. 挑第一張 Task 把分支開出來，告訴我分支名
我 commit + push 之後會再叫你開 PR。
```

## 6. 出問題時

```
我跑 nvl --headless 36310 吐這些出來、exit code 是 3，什麼意思？
<貼上輸出>
```

---

## 幾個你會想知道的行為

- **重跑是安全的**：建單有查重（同專案＋同型別＋標題完全相同就略過），所以整條指令
  重跑不會長出重複單。但查重是逐字比對，標題差一個字就會被當成新的單。
- **exit 3 不是失敗**：代表「分支好了，在等你寫 code」。
- **exit 4 不要重跑**：PR 已經建好了，只是 Slack 沒發成功。重跑會開出第二張 PR。
- **只支援 config 裡設定過的專案**：Go 版的 config（`%AppData%\very-lazy\config.json`）
  只認 `/init` 設定過的專案對應，其他專案的工作項會在「專案對應」這步失敗——
  失敗訊息會列出目前有哪些；缺的請跑 `nvl` 的 `/init` 加進去。
- **Release / Hotfix 也支援了**：發版 `--release`（release.sh 先另外跑）、
  修補 `--hotfix`（--branch / --bump / 開 PR 三次呼叫）。兩者開 PR 都必須
  給工作項 ID 跟明確的 reviewer 決定。詳見 SKILL.md。
