package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ---- headless /task mode: fully non-interactive, no TUI. ----
//
// Mirrors the interactive /task flow (fetchWorkItemMetaCmd -> resolveMapping ->
// listRefsCmd -> createBranchCmd -> commitCheckCmd -> createOnePR ->
// slackNotifyCmd) by calling the same underlying Cmd functions synchronously —
// they are plain `func() tea.Msg` closures with no Bubble Tea dependency, so
// no tea.Program is ever started for this path.

type headlessOpts struct {
	workItemID    int
	extraIDs      []int // 一起掛到同一張 PR 的其他 Task（分支名只用 workItemID 那張）
	dryRun        bool
	skipSlack     bool
	skipReviewer  bool
	branchOnly    bool // 只把分支準備好就停，不檢查 commit、不建 PR
	reviewerEmail string
	newTitles     []string // 非空＝建單模式（在 workItemID 底下建下一層單）
}

// linkedIDs is every work item the PR should link: the primary first (it named
// the branch), then the extras — mirrors the interactive flow's allTaskIDs.
func (o headlessOpts) linkedIDs() []int {
	out := []int{o.workItemID}
	for _, id := range o.extraIDs {
		if id != o.workItemID {
			out = append(out, id)
		}
	}
	return out
}

// createMode reports whether this run creates child work items instead of
// running the branch/PR flow. The two are deliberately separate invocations:
// a freshly created Task has no commits yet, so chaining straight into a PR
// would always stop at the commit gate.
func (o headlessOpts) createMode() bool { return len(o.newTitles) > 0 }

const (
	exitHeadlessOK           = 0
	exitHeadlessFail         = 1
	exitHeadlessUsage        = 2
	exitHeadlessNeedsCommits = 3
	exitHeadlessSlackFailed  = 4
	exitHeadlessPartial      = 5
)

// isHeadless reports whether args (os.Args[1:]) request headless mode.
func isHeadless(args []string) bool {
	for _, a := range args {
		if a == "--headless" {
			return true
		}
	}
	return false
}

// parseHeadlessArgs parses either of the two modes:
//
//	--headless <taskID> [--dry-run] [--skip-slack] [--reviewer <email>|--skip-reviewer]
//	--headless <parentID> --new "<title>" [--new "<title>" ...] [--dry-run]
//
// flags and the id may appear in any order. Omitting both --reviewer and
// --skip-reviewer defaults to --skip-reviewer, so a bare `--headless <id>`
// still runs to completion without a human to pick a reviewer.
func parseHeadlessArgs(args []string) (headlessOpts, error) {
	var o headlessOpts
	haveID := false
	sawSkipReviewer := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--headless":
			// mode switch itself; already consumed by isHeadless.
		case a == "--dry-run":
			o.dryRun = true
		case a == "--skip-slack":
			o.skipSlack = true
		case a == "--branch":
			o.branchOnly = true
		case a == "--skip-reviewer":
			o.skipReviewer = true
			sawSkipReviewer = true
		case a == "--reviewer":
			if i+1 >= len(args) {
				return headlessOpts{}, fmt.Errorf("--reviewer 後面缺 email")
			}
			i++
			o.reviewerEmail = strings.TrimSpace(args[i])
			if o.reviewerEmail == "" {
				return headlessOpts{}, fmt.Errorf("--reviewer 後面缺 email")
			}
		case strings.HasPrefix(a, "--reviewer="):
			o.reviewerEmail = strings.TrimSpace(strings.TrimPrefix(a, "--reviewer="))
			if o.reviewerEmail == "" {
				return headlessOpts{}, fmt.Errorf("--reviewer 後面缺 email")
			}
		case a == "--new":
			if i+1 >= len(args) {
				return headlessOpts{}, fmt.Errorf("--new 後面缺標題")
			}
			i++
			t := strings.TrimSpace(args[i])
			if t == "" {
				return headlessOpts{}, fmt.Errorf("--new 後面缺標題")
			}
			o.newTitles = append(o.newTitles, t)
		case strings.HasPrefix(a, "--new="):
			t := strings.TrimSpace(strings.TrimPrefix(a, "--new="))
			if t == "" {
				return headlessOpts{}, fmt.Errorf("--new 後面缺標題")
			}
			o.newTitles = append(o.newTitles, t)
		case strings.HasPrefix(a, "-"):
			return headlessOpts{}, fmt.Errorf("不認得的參數: %s", a)
		default:
			id, err := strconv.Atoi(a)
			if err != nil || id <= 0 {
				return headlessOpts{}, fmt.Errorf("不是有效的工作項 ID: %s", a)
			}
			// 第一個 ID 是主要工作項（決定分支名），後面的一起掛到同一張 PR。
			if haveID {
				o.extraIDs = append(o.extraIDs, id)
			} else {
				o.workItemID = id
				haveID = true
			}
		}
	}
	if !haveID {
		return headlessOpts{}, fmt.Errorf("缺少工作項 ID，用法: vlui --headless <id> [更多 ID…] [--branch] [--dry-run] [--skip-slack] [--reviewer <email>|--skip-reviewer]  或  vlui --headless <parentID> --new \"標題\" [--new \"標題\"...]")
	}
	if o.reviewerEmail != "" && sawSkipReviewer {
		return headlessOpts{}, fmt.Errorf("--reviewer 跟 --skip-reviewer 不能同時給")
	}
	// 建單模式不會建 PR，也就不會用到 reviewer / Slack。這些旗標一起給通常代表
	// 使用者誤以為一次指令能建單又開 PR，明講比默默忽略好。
	if o.createMode() && (o.reviewerEmail != "" || o.skipSlack || sawSkipReviewer) {
		return headlessOpts{}, fmt.Errorf("--new（建單）不會建 PR，不能跟 --reviewer/--skip-slack/--skip-reviewer 一起用；建完單再單獨對新的 Task ID 跑一次")
	}
	if o.createMode() && len(o.extraIDs) > 0 {
		return headlessOpts{}, fmt.Errorf("--new（建單）只能給一個父單 ID，多餘的: %v", o.extraIDs)
	}
	if o.createMode() && o.branchOnly {
		return headlessOpts{}, fmt.Errorf("--new（建單）不能跟 --branch 一起用；建完單再對要開分支的那張 Task 跑 --branch")
	}
	// --branch 只把分支準備好，同樣不會建 PR。
	if o.branchOnly && (o.reviewerEmail != "" || o.skipSlack || sawSkipReviewer) {
		return headlessOpts{}, fmt.Errorf("--branch 只建分支、不建 PR，不能跟 --reviewer/--skip-slack/--skip-reviewer 一起用")
	}
	if o.branchOnly && len(o.extraIDs) > 0 {
		return headlessOpts{}, fmt.Errorf("--branch 只用一張 Task 決定分支名，多餘的 ID: %v（要掛多張請在開 PR 那次給）", o.extraIDs)
	}
	if o.reviewerEmail == "" {
		o.skipReviewer = true
	}
	return o, nil
}

// headlessRun carries the state accumulated while executing the flow, purely
// so formatHeadlessSummary can report what was learned even on failure.
type headlessRun struct {
	cfg  config
	opts headlessOpts

	wi         workItem
	mapKey     string
	mapping    mappingCfg
	baseBranch string
	branchName string
	reused     bool
	branchReal bool // true once the branch actually exists on origin (reused or really created)

	localPath      string
	commits        string
	commitCount    int
	commitsMissing bool

	chosenRev   reviewer
	prID        int
	prURL       string
	reviewerErr string
	slackState  string

	// 建單模式用
	childType string
	outcomes  []createOutcome
}

// runHeadless is the process-level entry point: parse args, load config, run
// the flow, print the summary, and return the exit code for main() to use.
func runHeadless(args []string) int {
	opts, err := parseHeadlessArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "headless 參數錯誤:", err)
		return exitHeadlessUsage
	}
	cfg, ok := loadConfig()
	if !ok {
		fmt.Fprintln(os.Stderr, "headless 失敗: 找不到設定檔，請先跑 /init")
		return exitHeadlessFail
	}
	r := &headlessRun{cfg: cfg, opts: opts}
	if opts.createMode() {
		return r.executeCreate()
	}
	return r.execute()
}

// createOutcome is one title's fate, kept so the summary can report every title
// even when only some of them made it.
type createOutcome struct {
	title string
	id    int
	note  string
	ok    bool
}

// executeCreate creates the next level down from the given work item: a Feature
// (or Release) gets Product Backlog Items, a PBI (or Bug) gets Tasks — the same
// hierarchy rule the interactive navigator uses via nextLevelType. New items
// inherit the parent's Area/Iteration, matching createTasksCmd and the
// PowerShell New-Task.
func (r *headlessRun) executeCreate() int {
	meta := fetchWorkItemMetaCmd(r.cfg.AzureOrg, r.opts.workItemID)().(workItemMetaMsg)
	if meta.err != nil {
		return r.fail(exitHeadlessFail, "取得工作項", meta.err)
	}
	r.wi = meta.wi

	childType := nextLevelType(r.wi.typ)
	if childType == "" {
		return r.fail(exitHeadlessFail, "階層檢查",
			fmt.Errorf("#%d 是 %s，底下不支援建立子單（Feature/Release 底下建 PBI，PBI/Bug 底下建 Task）", r.wi.id, r.wi.typ))
	}
	r.childType = childType

	if r.cfg.WorkItemProject == "" {
		return r.fail(exitHeadlessFail, "設定檢查", fmt.Errorf("config 缺少 workItemProject，無法建立工作項"))
	}

	// 查重：同專案同型別同標題已存在就不重建。自動化重跑時這一步是避免長出一堆
	// 重複單的關鍵（互動版建 PBI 也有同樣的查重）。
	var toCreate []string
	for _, t := range r.opts.newTitles {
		dup := searchByTitleCmd(r.cfg.AzureOrg, r.cfg.WorkItemProject, childType, t)().(dupPbiMsg)
		if dup.err == nil && len(dup.items) > 0 {
			r.outcomes = append(r.outcomes, createOutcome{
				title: t, id: dup.items[0].id,
				note: fmt.Sprintf("已存在同名 %s #%d，略過", shortType(childType), dup.items[0].id),
			})
			continue
		}
		toCreate = append(toCreate, t)
	}

	if r.opts.dryRun {
		for _, t := range toCreate {
			r.outcomes = append(r.outcomes, createOutcome{title: t, note: "[DRY RUN] 會建立", ok: true})
		}
		fmt.Println(formatHeadlessCreateSummary(r, true, "", nil))
		return exitHeadlessOK
	}

	switch childType {
	case "Task":
		if len(toCreate) > 0 {
			msg := createTasksCmd(r.cfg.AzureOrg, r.cfg.WorkItemProject, r.wi.id,
				r.wi.area, r.wi.iteration, toCreate)().(taskCreatedMsg)
			for _, c := range msg.created {
				r.outcomes = append(r.outcomes, createOutcome{title: c.title, id: c.id, note: "已建立", ok: true})
			}
			for _, f := range msg.failed {
				r.outcomes = append(r.outcomes, createOutcome{title: f, note: "建立失敗"})
			}
		}
	default: // Product Backlog Item
		assignee := ""
		if who, ok := whoAmICmd()().(whoAmIMsg); ok && who.err == nil {
			assignee = who.name
		}
		// Release 不在階層裡（用 related 綁），其餘綁 parent — 同 enterPbiForParent。
		kind := "parent"
		if strings.EqualFold(r.wi.typ, "Release") {
			kind = "related"
		}
		for _, t := range toCreate {
			pc := createPbiCmd(r.cfg.AzureOrg, r.cfg.WorkItemProject, t, r.wi.area, r.wi.iteration, assignee)().(pbiCreatedMsg)
			if pc.err != nil {
				r.outcomes = append(r.outcomes, createOutcome{title: t, note: "建立失敗: " + pc.err.Error()})
				continue
			}
			note := "已建立"
			if bd, ok := addRelationCmd(r.cfg.AzureOrg, pc.id, kind, r.wi.id)().(bindDoneMsg); ok && bd.err != nil {
				// 單本身建好了，只是沒掛上父層 — 不當成失敗，但要講出來讓人補。
				note = "已建立，但綁父層失敗(" + bd.err.Error() + ")，請手動掛到 #" + strconv.Itoa(r.wi.id)
			}
			r.outcomes = append(r.outcomes, createOutcome{title: t, id: pc.id, note: note, ok: true})
		}
	}

	okCount, failCount := 0, 0
	for _, o := range r.outcomes {
		if o.ok || o.id > 0 {
			okCount++
		} else {
			failCount++
		}
	}
	fmt.Println(formatHeadlessCreateSummary(r, failCount == 0, "", nil))
	switch {
	case failCount > 0 && okCount > 0:
		fmt.Fprintf(os.Stderr, "headless 建單部分失敗: 成功 %d、失敗 %d\n", okCount, failCount)
		return exitHeadlessPartial
	case failCount > 0:
		fmt.Fprintln(os.Stderr, "headless 建單全部失敗")
		return exitHeadlessFail
	}
	return exitHeadlessOK
}

// formatHeadlessCreateSummary renders the create-mode result: what the parent
// was, what type was created under it, and every title's fate.
func formatHeadlessCreateSummary(r *headlessRun, ok bool, failPhase string, failErr error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "父單 #%d", r.opts.workItemID)
	if r.wi.title != "" {
		fmt.Fprintf(&b, ": %s", r.wi.title)
	}
	if r.wi.typ != "" {
		fmt.Fprintf(&b, " [%s]", r.wi.typ)
	}
	b.WriteString("\n")
	if r.childType != "" {
		fmt.Fprintf(&b, "建立型別: %s（依階層規則由 %s 推得）\n", r.childType, r.wi.typ)
	}
	if r.wi.area != "" {
		fmt.Fprintf(&b, "Area: %s\n", r.wi.area)
	}
	if r.wi.iteration != "" {
		fmt.Fprintf(&b, "Iteration: %s\n", r.wi.iteration)
	}
	if len(r.outcomes) > 0 {
		b.WriteString("\n")
		for _, o := range r.outcomes {
			if o.id > 0 {
				fmt.Fprintf(&b, "  #%d  %s — %s\n", o.id, o.title, o.note)
			} else {
				fmt.Fprintf(&b, "  --      %s — %s\n", o.title, o.note)
			}
		}
	}
	b.WriteString("\n")
	switch {
	case failErr != nil:
		fmt.Fprintf(&b, "結果: 失敗 (%s): %v\n", failPhase, failErr)
	case r.opts.dryRun:
		b.WriteString("結果: 模擬完成 (沒有任何寫入)\n")
	case ok:
		b.WriteString("結果: 成功\n")
	default:
		b.WriteString("結果: 部分失敗\n")
	}
	if !r.opts.dryRun && r.childType == "Task" {
		b.WriteString("下一步: 對要開 PR 的 Task 各跑一次 `--headless <taskID>`（需先 commit + push）\n")
	}
	if !r.opts.dryRun && r.childType == "Product Backlog Item" {
		b.WriteString("下一步: 對新 PBI 跑 `--headless <pbiID> --new \"...\"` 建底下的 Task\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r *headlessRun) execute() int {
	metaMsg := fetchWorkItemMetaCmd(r.cfg.AzureOrg, r.opts.workItemID)().(workItemMetaMsg)
	if metaMsg.err != nil {
		return r.fail(exitHeadlessFail, "取得工作項", metaMsg.err)
	}
	r.wi = metaMsg.wi

	if !strings.EqualFold(r.wi.typ, "Task") {
		return r.fail(exitHeadlessFail, "型別檢查",
			fmt.Errorf("#%d 是 %s，不是 Task；headless 模式只接受已存在的 Task 型別工作項", r.wi.id, r.wi.typ))
	}

	key, mapping, ok := (model{cfg: r.cfg}).resolveMapping(r.wi)
	if !ok {
		return r.fail(exitHeadlessFail, "專案對應",
			fmt.Errorf("Area Path %q 跟標題都對不到設定裡的任何專案（目前有: %s）。"+
				"這個專案還沒設定過，請跑互動模式的 /init 加上它（headless 不會自己寫設定）",
				r.wi.area, strings.Join(sortedMappingKeys(r.cfg.Mappings), ", ")))
	}
	r.mapKey, r.mapping = key, mapping

	r.branchName = deriveBranchName(r.wi.id, r.wi.title)
	r.baseBranch = r.mapping.DefaultBranch
	if r.baseBranch == "" {
		r.baseBranch = "develop"
	}

	refs := listRefsCmd(r.cfg.AzureOrg, r.mapping.AzureProject, r.mapping.AzureRepository)().(refsMsg)
	if refs.err != nil {
		return r.fail(exitHeadlessFail, "取得分支清單", refs.err)
	}
	reuse, baseObjectID := pickBranchReuseAndBase(refs.refs, r.wi.id, r.baseBranch)

	switch {
	case reuse != "":
		r.branchName = reuse
		r.reused = true
		r.branchReal = true
	case baseObjectID == "":
		return r.fail(exitHeadlessFail, "建立分支",
			fmt.Errorf("找不到 base branch %q，請確認 mapping.defaultBranch 設定正確", r.baseBranch))
	case r.opts.dryRun:
		r.branchReal = false
	default:
		bm := createBranchCmd(r.cfg.AzureOrg, r.mapping.AzureProject, r.mapping.AzureRepository, r.branchName, baseObjectID)().(branchMsg)
		if bm.err != nil {
			return r.fail(exitHeadlessFail, "建立分支", bm.err)
		}
		r.branchName = bm.branch
		r.branchReal = true
	}

	// --branch：分支準備好就收工。建分支是 server-side 的操作，不需要本機 repo，
	// 所以這條路徑刻意不檢查 localPath——本機還沒 clone 也能先把分支開出來。
	if r.opts.branchOnly {
		fmt.Println(formatHeadlessSummary(r, true, "", nil))
		return exitHeadlessOK
	}

	r.localPath = (model{cfg: r.cfg, mapping: r.mapping, mapKey: r.mapKey}).resolveLocalPath()
	if !isGitRepo(r.localPath) {
		where := "設定裡沒有填路徑"
		if r.localPath != "" {
			where = fmt.Sprintf("設定指到 %q，但那裡不是 git repo", r.localPath)
		}
		// clone 是「第一次設定這個專案」的事，屬於互動模式 /init 的職責，
		// headless 刻意不自己 clone（無人看管時 clone 到哪、誰清理都沒有好答案）。
		return r.fail(exitHeadlessFail, "本機路徑",
			fmt.Errorf("專案 %q 沒有可用的本機 git repo（%s）。"+
				"還沒 clone 過的話請先 clone，或跑互動模式的 /init 設定路徑", r.mapKey, where))
	}

	if r.branchReal {
		cm := commitCheckCmd(r.localPath, r.baseBranch, r.branchName)().(commitsMsg)
		if cm.err != nil {
			return r.fail(exitHeadlessFail, "commit 檢查", cm.err)
		}
		r.commits, r.commitCount, r.commitsMissing = cm.commits, cm.count, cm.missing
		if r.commitsMissing {
			return r.fail(exitHeadlessNeedsCommits, "commit 檢查",
				fmt.Errorf("分支 %s 還沒 push 到遠端，請先 push 再重跑", r.branchName))
		}
		if r.commitCount == 0 {
			return r.fail(exitHeadlessNeedsCommits, "commit 檢查",
				fmt.Errorf("分支 %s 相對 %s 沒有新 commit，請先完成 commit 再重跑", r.branchName, r.baseBranch))
		}
	}

	if r.opts.reviewerEmail != "" {
		r.chosenRev = reviewer{
			email:   r.opts.reviewerEmail,
			slackID: slackIDFor(r.opts.reviewerEmail, r.cfg.Slack.Members),
		}
	}
	// else: opts.skipReviewer is true (parseHeadlessArgs defaults it when
	// --reviewer is omitted), r.chosenRev stays the zero value -> no reviewer.

	switch {
	case !r.branchReal:
		r.prURL = "(dry-run：分支尚未建立，無法預覽 PR)"
	case r.opts.dryRun:
		r.prURL = "DRY-RUN"
	default:
		pr := createOnePR(r.cfg.AzureOrg, r.mapping.AzureProject, r.mapping.AzureRepository,
			r.branchName, r.baseBranch, r.branchName, r.commits, r.opts.linkedIDs(), r.chosenRev, true)
		if pr.err != nil {
			return r.fail(exitHeadlessFail, "建立 PR", pr.err)
		}
		r.prID, r.prURL, r.reviewerErr = pr.id, pr.url, pr.reviewerErr
	}

	slackFailed := false
	switch {
	case r.opts.skipSlack:
		r.slackState = "略過 (--skip-slack)"
	case r.chosenRev.slackID == "":
		r.slackState = "略過 (沒有指定 reviewer，或 reviewer 沒有對應 Slack 帳號)"
	case !(model{cfg: r.cfg}).slackConfigured():
		r.slackState = "略過 (config 沒有設定 Slack token/channel)"
	case r.opts.dryRun || !r.branchReal:
		r.slackState = "[DRY RUN] 會通知 " + r.chosenRev.email
	default:
		prResult := prResultText(r.mapping.AzureProject, r.baseBranch, r.prURL, r.branchName, r.prID)
		sd := slackNotifyCmd(r.cfg.SlackToken, r.cfg.Slack.Channel, r.chosenRev.slackID, prResult)().(slackDoneMsg)
		if sd.err != nil {
			r.slackState = "失敗: " + sd.err.Error()
			slackFailed = true
		} else {
			r.slackState = "已通知 #" + r.cfg.Slack.Channel
		}
	}

	fmt.Println(formatHeadlessSummary(r, true, "", nil))
	if slackFailed {
		return exitHeadlessSlackFailed
	}
	return exitHeadlessOK
}

func (r *headlessRun) fail(code int, phase string, err error) int {
	if r.opts.createMode() {
		fmt.Println(formatHeadlessCreateSummary(r, false, phase, err))
	} else {
		fmt.Println(formatHeadlessSummary(r, false, phase, err))
	}
	fmt.Fprintf(os.Stderr, "headless 失敗 (%s): %v\n", phase, err)
	return code
}

// formatHeadlessSummary renders a plain-text (non-Slack-mrkdwn) result for
// stdout, showing whatever fields were populated before success or failure.
func formatHeadlessSummary(r *headlessRun, ok bool, failPhase string, failErr error) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Task #%d", r.opts.workItemID)
	if r.wi.title != "" {
		fmt.Fprintf(&b, ": %s", r.wi.title)
	}
	if r.wi.typ != "" {
		fmt.Fprintf(&b, " [%s]", r.wi.typ)
	}
	b.WriteString("\n")

	if r.mapKey != "" {
		fmt.Fprintf(&b, "專案對應: %s -> %s/%s\n", r.mapKey, r.mapping.AzureProject, r.mapping.AzureRepository)
	}
	if r.branchName != "" {
		state := "新建"
		switch {
		case r.reused:
			state = "重用既有"
		case !r.branchReal:
			state = "[DRY RUN] 模擬建立"
		}
		fmt.Fprintf(&b, "分支: %s (%s, base=%s)\n", r.branchName, state, r.baseBranch)
	}
	// --branch 到分支就結束。commit/reviewer/PR/Slack 這幾行在這個模式下都還沒發生
	// （它連 commit 都不查），印出來只會讓讀輸出的人以為流程走得比實際更遠。
	if r.opts.branchOnly {
		switch {
		case !ok:
			fmt.Fprintf(&b, "結果: 失敗 (%s): %v\n", failPhase, failErr)
		case r.opts.dryRun:
			b.WriteString("結果: 模擬完成 (沒有任何寫入)\n")
		default:
			b.WriteString("下一步: 切到這個分支 commit + push，再跑一次（不帶 --branch）開 PR\n")
			b.WriteString("結果: 成功 (只建分支，未建 PR)\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}

	// 只有真的走到分支這一步才報 commit 狀態；更早失敗（例如取不到工作項）時
	// 印這行會讓人以為流程走得比實際更遠。
	if r.branchName != "" {
		switch {
		case r.branchReal && r.commitsMissing:
			b.WriteString("commit: 分支還沒 push 到遠端\n")
		case r.branchReal && r.commitCount > 0:
			fmt.Fprintf(&b, "commit: %d 筆新 commit\n", r.commitCount)
		case !r.branchReal && r.opts.dryRun:
			b.WriteString("commit: [DRY RUN] 分支尚未建立，無法預覽\n")
		}
	}

	if ids := r.opts.linkedIDs(); len(ids) > 1 {
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = "#" + strconv.Itoa(id)
		}
		fmt.Fprintf(&b, "PR 掛的工作項: %s (第一張決定分支名)\n", strings.Join(parts, ", "))
	}
	if r.opts.reviewerEmail != "" {
		fmt.Fprintf(&b, "reviewer: %s\n", r.opts.reviewerEmail)
	} else {
		b.WriteString("reviewer: 略過 (未指定 --reviewer，可用 --reviewer <email> 指定)\n")
	}
	if r.prURL != "" {
		fmt.Fprintf(&b, "PR: %s\n", r.prURL)
	}
	if r.slackState != "" {
		fmt.Fprintf(&b, "Slack: %s\n", r.slackState)
	}

	switch {
	case !ok:
		fmt.Fprintf(&b, "結果: 失敗 (%s): %v\n", failPhase, failErr)
	case r.opts.dryRun:
		b.WriteString("結果: 模擬完成 (沒有任何寫入)\n")
	default:
		b.WriteString("結果: 成功\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
