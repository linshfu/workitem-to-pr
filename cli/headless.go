package main

import (
	"fmt"
	"os"
	"os/exec"
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

	releaseMode bool   // --release：對既有的 release/vX.Y.Z 分支開 master + develop 兩張 PR
	hotfixMode  bool   // --hotfix：從 master 開 hotfix 分支 / 改版號 / 開雙 PR（分三次呼叫）
	bump        bool   // --bump：只跑改版號那段（npm run release + build:prod + commit + push）
	projectKey  string // --project：release/hotfix 模式用哪個 mapping（沒有工作項可以推）
	version     string // --version：x.y.z
}

// projectMode 是靠 --project/--version 定位、不吃工作項 ID 的模式。
func (o headlessOpts) projectMode() bool { return o.releaseMode || o.hotfixMode }

// releaseBranch/hotfixBranch 是各自流程用的分支名。

// releaseBranch 是 release.sh 產生的分支名（`release/v` + 版號），headless 只
// 驗證它存在、不自己建——建分支跟 prod build 都是 release.sh 的事。
func (o headlessOpts) releaseBranch() string { return "release/v" + o.version }
func (o headlessOpts) hotfixBranch() string  { return "hotfix/v" + o.version }

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

// flagValue reads a flag's value, accepting both `--flag value` and `--flag=value`.
// i is advanced past a consumed separate value.
func flagValue(arg, name string, args []string, i *int) (string, error) {
	if strings.HasPrefix(arg, name+"=") {
		v := strings.TrimSpace(strings.TrimPrefix(arg, name+"="))
		if v == "" {
			return "", fmt.Errorf("%s 後面缺值", name)
		}
		return v, nil
	}
	if *i+1 >= len(args) {
		return "", fmt.Errorf("%s 後面缺值", name)
	}
	*i++
	v := strings.TrimSpace(args[*i])
	if v == "" {
		return "", fmt.Errorf("%s 後面缺值", name)
	}
	return v, nil
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
		case a == "--release":
			o.releaseMode = true
		case a == "--hotfix":
			o.hotfixMode = true
		case a == "--bump":
			o.bump = true
		case a == "--project", strings.HasPrefix(a, "--project="):
			v, err := flagValue(a, "--project", args, &i)
			if err != nil {
				return headlessOpts{}, err
			}
			o.projectKey = v
		case a == "--version", strings.HasPrefix(a, "--version="):
			v, err := flagValue(a, "--version", args, &i)
			if err != nil {
				return headlessOpts{}, err
			}
			o.version = v
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
	// release / hotfix 沒有工作項可以推，靠 --project / --version 定位，所以必填條件
	// 跟另外兩種模式完全不同，先擋掉組合錯誤再檢查 ID。
	if o.releaseMode && o.hotfixMode {
		return headlessOpts{}, fmt.Errorf("--release 跟 --hotfix 不能同時給")
	}
	if o.bump && !o.hotfixMode {
		return headlessOpts{}, fmt.Errorf("--bump 只有 --hotfix 模式會用到")
	}
	if o.projectMode() {
		label := "--release"
		if o.hotfixMode {
			label = "--hotfix"
		}
		if o.createMode() {
			return headlessOpts{}, fmt.Errorf("%s 不能跟 --new 一起用", label)
		}
		if o.releaseMode && o.branchOnly {
			return headlessOpts{}, fmt.Errorf("--release 不能跟 --branch 一起用（發版分支是 release.sh 建的）")
		}
		if o.branchOnly && o.bump {
			return headlessOpts{}, fmt.Errorf("--branch 跟 --bump 是不同的步驟，不能同時給")
		}
		if o.projectKey == "" || o.version == "" {
			return headlessOpts{}, fmt.Errorf("%s 需要 --project <對應名> 跟 --version <x.y.z>", label)
		}
		if !releaseVersionRe.MatchString(o.version) {
			return headlessOpts{}, fmt.Errorf("版號格式要是 x.y.z（收到 %q）", o.version)
		}
		// --branch / --bump 都不會建 PR，reviewer/Slack/工作項在那兩步沒有意義。
		if o.branchOnly || o.bump {
			if o.reviewerEmail != "" || o.skipSlack || sawSkipReviewer {
				return headlessOpts{}, fmt.Errorf("這一步不建 PR，不能跟 --reviewer/--skip-slack/--skip-reviewer 一起用")
			}
			if haveID || len(o.extraIDs) > 0 {
				return headlessOpts{}, fmt.Errorf("這一步不建 PR，不用給工作項 ID（開 PR 那次再給）")
			}
		} else {
			// 開 PR 這步：master 分支原則要求「PR 必須掛工作項」，沒掛的話 PR 會直接
			// 卡在 required check 不能合併。與其開一張不能用的 PR，不如在碰 az 之前擋下來。
			if !haveID {
				return headlessOpts{}, fmt.Errorf("%s 開 PR 需要至少一個工作項 ID（通常是那張 Release 單，例如 %s --project %s --version %s 35718）；master 的分支原則要求 PR 必須掛工作項，沒掛會卡在 required check 不能合併",
					label, label, o.projectKey, o.version)
			}
			// 同理，發版 PR 沒有審核者就沒人會去按核准，會一直停在那裡。要略過必須
			// 明講，不能靠「沒給就當作不要」——那是 /task 的預設，發版不適用。
			if o.reviewerEmail == "" && !sawSkipReviewer {
				return headlessOpts{}, fmt.Errorf("%s 開 PR 要指定 --reviewer <email>；發版 PR 沒有審核者就不會有人核准、也不會開 auto-complete，會一直卡著。真的不要審核者請明確加 --skip-reviewer",
					label)
			}
		}
	} else {
		if o.projectKey != "" || o.version != "" {
			return headlessOpts{}, fmt.Errorf("--project/--version 只有 --release/--hotfix 模式會用到")
		}
		if !haveID {
			return headlessOpts{}, fmt.Errorf("缺少工作項 ID，用法: vlui --headless <id> [更多 ID…] [--branch] [--dry-run] [--skip-slack] [--reviewer <email>|--skip-reviewer]  或  vlui --headless <parentID> --new \"標題\" [--new \"標題\"...]  或  vlui --headless --release|--hotfix --project <對應名> --version <x.y.z>；完整 AI 使用指南：--install-skill（Claude Code）或 --export-skill <目錄>")
		}
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

	// hotfix 用
	bumped bool // 版號那筆 commit 已經在（剛跑完 --bump，或在 commit 清單裡看到）
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
	switch {
	case opts.releaseMode:
		return r.executeRelease()
	case opts.hotfixMode:
		return r.executeHotfix()
	case opts.createMode():
		return r.executeCreate()
	}
	return r.execute()
}

// executeHotfix runs one of the three hotfix steps, split where the flow needs a
// human: --branch opens hotfix/vX.Y.Z off master, then someone writes and pushes
// the fix; --bump runs the version-bump chain (npm ci + release + prod build +
// commit + push, minutes long); with neither it verifies the commits and opens
// the master + develop PR pair. Mirrors the interactive /hotfix order.
func (r *headlessRun) executeHotfix() int {
	mapping, ok := r.cfg.Mappings[r.opts.projectKey]
	if !ok {
		return r.fail(exitHeadlessFail, "專案對應",
			fmt.Errorf("設定裡沒有 %q 這個對應（目前有: %s）；要新增請跑互動模式的 /init",
				r.opts.projectKey, strings.Join(sortedMappingKeys(r.cfg.Mappings), ", ")))
	}
	r.mapKey, r.mapping = r.opts.projectKey, mapping
	r.branchName = r.opts.hotfixBranch()
	r.baseBranch = "master" // hotfix 一律從 master 出來，與互動版／PS 版一致

	r.localPath = mappingLocalPathOf(r.cfg, r.opts.projectKey)
	if !isGitRepo(r.localPath) {
		where := "設定裡沒有填路徑"
		if r.localPath != "" {
			where = fmt.Sprintf("設定指到 %q，但那裡不是 git repo", r.localPath)
		}
		return r.fail(exitHeadlessFail, "本機路徑",
			fmt.Errorf("專案 %q 沒有可用的本機 git repo（%s）；hotfix 全程都靠本機 git，請先 clone 或跑 /init 設定路徑",
				r.opts.projectKey, where))
	}

	switch {
	case r.opts.branchOnly:
		return r.hotfixStepBranch()
	case r.opts.bump:
		return r.hotfixStepBump()
	}
	return r.hotfixStepPRs()
}

// hotfixStepBranch updates master and opens (or reuses) the hotfix branch.
func (r *headlessRun) hotfixStepBranch() int {
	if r.opts.dryRun {
		r.slackState = ""
		fmt.Println(formatHeadlessHotfixSummary(r, true, "", nil))
		return exitHeadlessOK
	}
	msg := hotfixBranchCmd(r.localPath, r.branchName)().(hotfixBranchMsg)
	if msg.err != nil {
		return r.fail(exitHeadlessFail, "開 hotfix 分支", msg.err)
	}
	r.branchName, r.reused, r.branchReal = msg.branch, msg.reused, true
	fmt.Println(formatHeadlessHotfixSummary(r, true, "", nil))
	return exitHeadlessOK
}

// hotfixStepBump runs the version-bump chain in Git Bash. It streams straight to
// this process's stdout/stderr (no Bubble Tea to hand the terminal to) and gets
// no timeout, because `npm ci` plus a prod build legitimately takes minutes.
// Stdin is left nil so anything that tries to prompt hits EOF instead of hanging.
func (r *headlessRun) hotfixStepBump() int {
	gitBash := releaseGitBashPath()
	if !fileExists(gitBash) {
		return r.fail(exitHeadlessFail, "改版號", fmt.Errorf("找不到 Git Bash（%s）", gitBash))
	}
	script, err := buildHotfixBumpCommand(r.localPath, r.branchName, r.opts.version)
	if err != nil {
		return r.fail(exitHeadlessFail, "改版號", err)
	}
	if r.opts.dryRun {
		fmt.Println("[DRY RUN] 會在 Git Bash 執行：")
		fmt.Println("  " + script)
		fmt.Println(formatHeadlessHotfixSummary(r, true, "", nil))
		return exitHeadlessOK
	}
	fmt.Println("執行改版號（npm ci + npm run release + build:prod + commit + push，需要幾分鐘）…")
	cmd := exec.Command(gitBash, "-c", script)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, nil
	if err := cmd.Run(); err != nil {
		return r.fail(exitHeadlessFail, "改版號",
			fmt.Errorf("%v；上面的輸出有實際原因。注意這條指令是一連串動作，可能已經改了版號或 commit，"+
				"重跑前先看 git status / git log", err))
	}
	r.branchReal = true
	r.bumped = true
	fmt.Println(formatHeadlessHotfixSummary(r, true, "", nil))
	return exitHeadlessOK
}

// hotfixStepPRs verifies the fix is on origin, then opens the master + develop PRs.
func (r *headlessRun) hotfixStepPRs() int {
	// 先確認分支在 origin 上。少了這一步，下面的 git log origin/master..origin/<分支>
	// 只會回一個看不懂的 exit 128，而真正的原因是「還沒跑 --branch」。
	if _, err := gitIn(r.localPath, "fetch", "origin"); err != nil {
		return r.fail(exitHeadlessFail, "git fetch", err)
	}
	rb, _ := gitIn(r.localPath, "branch", "-r", "--list", "origin/"+r.branchName)
	if strings.TrimSpace(rb) == "" {
		return r.fail(exitHeadlessNeedsCommits, "檢查 hotfix 分支",
			fmt.Errorf("origin 上找不到 %s；請先跑 --branch 開分支，把修正 commit + push 之後再回來開 PR", r.branchName))
	}

	cm := hotfixCommitCmd(r.localPath, r.branchName)().(hotfixCommitMsg)
	if cm.err != nil {
		return r.fail(exitHeadlessFail, "commit 檢查", cm.err)
	}
	r.commits, r.commitCount = cm.commits, cm.count
	r.branchReal = true
	if r.commitCount == 0 {
		return r.fail(exitHeadlessNeedsCommits, "commit 檢查",
			fmt.Errorf("%s 相對 master 沒有新 commit；請先把修正 commit 並 push（還沒開分支的話先跑 --branch）", r.branchName))
	}
	// 改版號那筆 commit 訊息固定是 "release: v<版號>"（buildHotfixBumpCommand 定的），
	// 沒看到就提醒——漏改版號是這條流程最常見的失誤，但不擋，使用者可能另有做法。
	r.bumped = strings.Contains(r.commits, "release: v"+r.opts.version)

	if r.opts.reviewerEmail != "" {
		r.chosenRev = reviewer{
			email:   r.opts.reviewerEmail,
			slackID: slackIDFor(r.opts.reviewerEmail, r.cfg.Slack.Members),
		}
	}

	desc := r.commits
	if strings.TrimSpace(desc) == "" {
		desc = "Hotfix v" + r.opts.version
	}
	var prResult string
	if r.opts.dryRun {
		r.prURL = "DRY-RUN"
	} else {
		msg := createReleasePRsCmd(r.cfg.AzureOrg, r.mapping.AzureProject, r.mapping.AzureRepository,
			r.branchName, desc, r.opts.linkedIDs(), r.chosenRev)().(releasePRsMsg)
		if msg.err != nil {
			r.prURL = msg.masterURL // master 成功、develop 失敗時要讓人看到已存在的那張
			return r.fail(exitHeadlessFail, "建立 hotfix PR", msg.err)
		}
		r.prURL = msg.masterURL + "\n     develop: " + msg.developURL
		r.reviewerErr = msg.reviewerErr
		prResult = msg.prResult
	}

	slackFailed := false
	switch {
	case r.opts.skipSlack:
		r.slackState = "略過 (--skip-slack)"
	case r.chosenRev.slackID == "":
		r.slackState = "略過 (沒有指定 reviewer，或 reviewer 沒有對應 Slack 帳號)"
	case !(model{cfg: r.cfg}).slackConfigured():
		r.slackState = "略過 (config 沒有設定 Slack token/channel；要啟用請跑互動模式的 /init)"
	case r.opts.dryRun:
		r.slackState = "[DRY RUN] 會通知 " + r.chosenRev.email
	default:
		sd := slackNotifyCmd(r.cfg.SlackToken, r.cfg.Slack.Channel, r.chosenRev.slackID, prResult)().(slackDoneMsg)
		if sd.err != nil {
			r.slackState = slackFailNote(sd.err)
			slackFailed = true
		} else {
			r.slackState = "已通知 #" + r.cfg.Slack.Channel
		}
	}

	fmt.Println(formatHeadlessHotfixSummary(r, true, "", nil))
	if slackFailed {
		return r.slackFailedExit()
	}
	return exitHeadlessOK
}

// formatHeadlessHotfixSummary reports whichever hotfix step just ran.
func formatHeadlessHotfixSummary(r *headlessRun, ok bool, failPhase string, failErr error) string {
	var b strings.Builder
	step := "開 PR"
	switch {
	case r.opts.branchOnly:
		step = "開分支"
	case r.opts.bump:
		step = "改版號"
	}
	fmt.Fprintf(&b, "Hotfix v%s（步驟：%s）\n", r.opts.version, step)
	if r.mapKey != "" {
		fmt.Fprintf(&b, "專案對應: %s -> %s/%s\n", r.mapKey, r.mapping.AzureProject, r.mapping.AzureRepository)
	}
	if r.localPath != "" {
		fmt.Fprintf(&b, "本機路徑: %s\n", r.localPath)
	}
	if r.branchName != "" {
		// 狀態要按步驟講：只有「開分支」那步會建立它，其他兩步是假設它已經在了。
		var state string
		switch {
		case r.reused:
			state = "沿用既有"
		case r.branchReal:
			state = "已建立並 push"
		case r.opts.branchOnly && r.opts.dryRun:
			state = "[DRY RUN] 不會真的建立"
		case r.opts.branchOnly:
			state = "尚未建立"
		default:
			state = "假設已存在（由 --branch 那步建立）"
		}
		fmt.Fprintf(&b, "分支: %s (%s, 從 master)\n", r.branchName, state)
	}

	if r.opts.branchOnly && ok && !r.opts.dryRun {
		b.WriteString("下一步: 在這個分支上完成修正、commit、push，接著跑 --bump 改版號\n")
	}
	if r.opts.bump {
		if ok && !r.opts.dryRun {
			b.WriteString("版號已改並 push；下一步: 不帶 --branch/--bump 再跑一次開雙 PR\n")
		}
	}
	if !r.opts.branchOnly && !r.opts.bump {
		if r.commitCount > 0 {
			fmt.Fprintf(&b, "commit: %d 筆（相對 master）\n", r.commitCount)
		}
		if r.commitCount > 0 && !r.bumped {
			fmt.Fprintf(&b, "⚠ 沒看到 \"release: v%s\" 這筆 commit，版號可能還沒改（要改的話先跑 --bump）\n", r.opts.version)
		}
		if ids := r.opts.linkedIDs(); r.opts.workItemID > 0 {
			parts := make([]string, len(ids))
			for i, id := range ids {
				parts[i] = "#" + strconv.Itoa(id)
			}
			fmt.Fprintf(&b, "PR 掛的工作項: %s\n", strings.Join(parts, ", "))
		}
		if r.opts.reviewerEmail != "" {
			fmt.Fprintf(&b, "reviewer: %s\n", r.opts.reviewerEmail)
		} else {
			b.WriteString("reviewer: 略過 (未指定 --reviewer)\n")
		}
		if r.reviewerErr != "" {
			fmt.Fprintf(&b, "reviewer 警告: %s\n", r.reviewerErr)
		}
		if r.prURL != "" {
			fmt.Fprintf(&b, "PR: master: %s\n", r.prURL)
		}
		if r.slackState != "" {
			fmt.Fprintf(&b, "Slack: %s\n", r.slackState)
		}
	}

	switch {
	case !ok:
		fmt.Fprintf(&b, "結果: 失敗 (%s): %v\n", failPhase, failErr)
	case r.opts.dryRun:
		b.WriteString("結果: 模擬完成 (沒有任何寫入)\n")
	case r.opts.branchOnly:
		b.WriteString("結果: 成功 (分支就緒，未建 PR)\n")
	case r.opts.bump:
		b.WriteString("結果: 成功 (版號已改，未建 PR)\n")
	default:
		b.WriteString("結果: 成功 (master 與 develop 兩張 PR 都建好)\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// executeRelease opens the master + develop PRs for an existing release/vX.Y.Z
// branch. It deliberately does NOT run release.sh: that script creates the
// branch, bumps the version and runs a prod build (minutes), and its ERR trap
// waits on `read -p`, which would hang forever where stdin never delivers.
// Running it stays a separate, watched step; headless only does the PR pair.
func (r *headlessRun) executeRelease() int {
	mapping, ok := r.cfg.Mappings[r.opts.projectKey]
	if !ok {
		return r.fail(exitHeadlessFail, "專案對應",
			fmt.Errorf("設定裡沒有 %q 這個對應（目前有: %s）；要新增請跑互動模式的 /init",
				r.opts.projectKey, strings.Join(sortedMappingKeys(r.cfg.Mappings), ", ")))
	}
	r.mapKey, r.mapping = r.opts.projectKey, mapping
	r.branchName = r.opts.releaseBranch()
	r.baseBranch = "master" // 發版固定對 master + develop 兩邊，與互動版一致

	// 分支必須已經存在——不存在就是 release.sh 還沒跑（或版號打錯），這種情況
	// 硬建一個空分支只會製造出一張沒有內容的 PR，所以直接擋下來。
	refs := listRefsCmd(r.cfg.AzureOrg, mapping.AzureProject, mapping.AzureRepository)().(refsMsg)
	if refs.err != nil {
		return r.fail(exitHeadlessFail, "取得分支清單", refs.err)
	}
	found := false
	for _, ref := range refs.refs {
		if strings.EqualFold(strings.TrimPrefix(ref.name, "refs/heads/"), r.branchName) {
			found = true
			break
		}
	}
	if !found {
		return r.fail(exitHeadlessNeedsCommits, "檢查發版分支",
			fmt.Errorf("origin 上找不到 %s；請先在該專案跑 release.sh %s（它會建分支、改版號、build 並 push），跑完確認正常再重跑這條指令",
				r.branchName, r.opts.version))
	}
	r.branchReal = true
	r.reused = true

	if r.opts.reviewerEmail != "" {
		r.chosenRev = reviewer{
			email:   r.opts.reviewerEmail,
			slackID: slackIDFor(r.opts.reviewerEmail, r.cfg.Slack.Members),
		}
	}

	desc := "Release v" + r.opts.version
	var prResult string
	if r.opts.dryRun {
		r.prURL = "DRY-RUN"
	} else {
		msg := createReleasePRsCmd(r.cfg.AzureOrg, mapping.AzureProject, mapping.AzureRepository,
			r.branchName, desc, r.opts.linkedIDs(), r.chosenRev)().(releasePRsMsg)
		if msg.err != nil {
			// master 成功、develop 失敗時 masterURL 有值——一定要印出來，
			// 否則使用者不知道已經有一張 PR 在了，重跑會變成兩張。
			r.prURL = msg.masterURL
			return r.fail(exitHeadlessFail, "建立發版 PR", msg.err)
		}
		r.prURL = msg.masterURL + "\n     develop: " + msg.developURL
		r.reviewerErr = msg.reviewerErr
		prResult = msg.prResult
	}

	slackFailed := false
	switch {
	case r.opts.skipSlack:
		r.slackState = "略過 (--skip-slack)"
	case r.chosenRev.slackID == "":
		r.slackState = "略過 (沒有指定 reviewer，或 reviewer 沒有對應 Slack 帳號)"
	case !(model{cfg: r.cfg}).slackConfigured():
		r.slackState = "略過 (config 沒有設定 Slack token/channel；要啟用請跑互動模式的 /init)"
	case r.opts.dryRun:
		r.slackState = "[DRY RUN] 會通知 " + r.chosenRev.email
	default:
		sd := slackNotifyCmd(r.cfg.SlackToken, r.cfg.Slack.Channel, r.chosenRev.slackID, prResult)().(slackDoneMsg)
		if sd.err != nil {
			r.slackState = slackFailNote(sd.err)
			slackFailed = true
		} else {
			r.slackState = "已通知 #" + r.cfg.Slack.Channel
		}
	}

	fmt.Println(formatHeadlessReleaseSummary(r, true, "", nil))
	if slackFailed {
		return r.slackFailedExit()
	}
	return exitHeadlessOK
}

// formatHeadlessReleaseSummary reports the release run in plain text.
func formatHeadlessReleaseSummary(r *headlessRun, ok bool, failPhase string, failErr error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "發版 v%s\n", r.opts.version)
	if r.mapKey != "" {
		fmt.Fprintf(&b, "專案對應: %s -> %s/%s\n", r.mapKey, r.mapping.AzureProject, r.mapping.AzureRepository)
	}
	if r.branchName != "" {
		state := "origin 上不存在"
		if r.branchReal {
			state = "已存在（release.sh 建的）"
		}
		fmt.Fprintf(&b, "發版分支: %s (%s)\n", r.branchName, state)
	}
	if ids := r.opts.linkedIDs(); r.opts.workItemID > 0 {
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = "#" + strconv.Itoa(id)
		}
		fmt.Fprintf(&b, "PR 掛的工作項: %s\n", strings.Join(parts, ", "))
	}
	if r.opts.reviewerEmail != "" {
		fmt.Fprintf(&b, "reviewer: %s\n", r.opts.reviewerEmail)
	} else {
		b.WriteString("reviewer: 略過 (未指定 --reviewer)\n")
	}
	if r.reviewerErr != "" {
		fmt.Fprintf(&b, "reviewer 警告: %s\n", r.reviewerErr)
	}
	if r.prURL != "" {
		fmt.Fprintf(&b, "PR: master: %s\n", r.prURL)
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
		b.WriteString("結果: 成功 (master 與 develop 兩張 PR 都建好)\n")
	}
	return strings.TrimRight(b.String(), "\n")
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
		r.slackState = "略過 (config 沒有設定 Slack token/channel；要啟用請跑互動模式的 /init)"
	case r.opts.dryRun || !r.branchReal:
		r.slackState = "[DRY RUN] 會通知 " + r.chosenRev.email
	default:
		prResult := prResultText(r.mapping.AzureProject, r.baseBranch, r.prURL, r.branchName, r.prID)
		sd := slackNotifyCmd(r.cfg.SlackToken, r.cfg.Slack.Channel, r.chosenRev.slackID, prResult)().(slackDoneMsg)
		if sd.err != nil {
			r.slackState = slackFailNote(sd.err)
			slackFailed = true
		} else {
			r.slackState = "已通知 #" + r.cfg.Slack.Channel
		}
	}

	fmt.Println(formatHeadlessSummary(r, true, "", nil))
	if slackFailed {
		return r.slackFailedExit()
	}
	return exitHeadlessOK
}

// slackFailNote 是三條流程共用的 Slack 失敗說明。最常見的原因是 token 失效或被停用
// （Slack 會回 account_inactive），而 token 存在 config.local.json、只有互動模式的
// /init 會寫，所以修法一律是去跑 /init 重新設定，headless 自己不寫設定。
func slackFailNote(err error) string {
	return "失敗: " + err.Error() +
		"（多半是 Slack token 失效或被停用；token 存在 config.local.json，請跑互動模式的 /init 重新設定）"
}

// slackFailedExit 收尾 exit 4：PR 已經建好了，只有通知沒發出去。額外印一行到 stderr，
// 讓只監看 stderr 的排程器也收得到訊號——但務必不要把它當成「PR 失敗」去重跑。
func (r *headlessRun) slackFailedExit() int {
	fmt.Fprintln(os.Stderr, "headless 警告 (exit 4): PR 已建立，但 Slack 通知失敗。"+
		"不要重跑（會多開一張 PR）；請跑 /init 重設 Slack token，或手動把 PR 連結貼到頻道")
	return exitHeadlessSlackFailed
}

func (r *headlessRun) fail(code int, phase string, err error) int {
	switch {
	case r.opts.releaseMode:
		fmt.Println(formatHeadlessReleaseSummary(r, false, phase, err))
	case r.opts.hotfixMode:
		fmt.Println(formatHeadlessHotfixSummary(r, false, phase, err))
	case r.opts.createMode():
		fmt.Println(formatHeadlessCreateSummary(r, false, phase, err))
	default:
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
