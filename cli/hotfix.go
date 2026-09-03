package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---- /hotfix: 從 master 開 hotfix/vX.Y.Z、等你 push 修正、改版號、開 master + develop PR + Slack ----
// 對齊 PowerShell 的 Start-Hotfix-Process。跟 /release 的差別：不是跑 release.sh，而是
// 自己開 hotfix 分支 → 等修正 commit → 用 npm 改版號 commit；reviewer / PR / Slack 與 /release 共用。

type hotfixStep int

const (
	hfProject       hotfixStep = iota // 選有本機路徑的專案
	hfVersion                         // 版本號 x.y.z
	hfBranchConfirm                   // 確認：更新 master、開 hotfix/vX.Y.Z 並 push
	hfBranching                       // 開分支中（git）
	hfWaitCommit                      // 等你 push 修正 commit（可重新檢查）
	hfBumpConfirm                     // 確認：npm 改版號 + commit + push
	hfBumping                         // 改版號執行中（輸出接管終端機）
	hfReviewer                        // 選 reviewer（或略過）
	hfPRCreating                      // 建立 master / develop PR
	hfDone                            // 完成：兩個 PR + Slack 狀態
)

// hotfixBranchMsg 是開 hotfix 分支的結果：branch 是分支名，reused 表示沿用既有遠端分支。
type hotfixBranchMsg struct {
	branch string
	reused bool
	err    error
}

// hotfixCommitMsg 是 origin/master..origin/<hotfix> 的 commit 檢查結果。
type hotfixCommitMsg struct {
	commits string
	count   int
	err     error
}

// hotfixBumpMsg 是改版號腳本（npm run release / build:prod / commit / push）的結果。
type hotfixBumpMsg struct {
	err error
}

// hotfixBranchCmd：工作目錄要乾淨 → 更新 master → 若遠端已有該 hotfix 分支就沿用，
// 否則從 master 開新分支並 push 到 origin。對齊 Update-Local-Branch + New-Pushed-Branch。
func hotfixBranchCmd(path, branch string) tea.Cmd {
	return func() tea.Msg {
		if st, _ := gitIn(path, "status", "--porcelain"); strings.TrimSpace(st) != "" {
			return hotfixBranchMsg{err: fmt.Errorf("工作目錄有未 commit 的變更，請先 commit 或 stash")}
		}
		if _, err := gitIn(path, "checkout", "master"); err != nil {
			return hotfixBranchMsg{err: fmt.Errorf("切換到 master 失敗：%v", err)}
		}
		if _, err := gitIn(path, "pull", "origin", "master"); err != nil {
			return hotfixBranchMsg{err: fmt.Errorf("更新 master 失敗：%v", err)}
		}
		_, _ = gitIn(path, "fetch", "origin")
		if rb, _ := gitIn(path, "branch", "-r", "--list", "origin/"+branch); strings.TrimSpace(rb) != "" {
			// 遠端已存在（例如接續一個中斷的 hotfix）→ 沿用
			if _, err := gitIn(path, "checkout", branch); err != nil {
				if _, err2 := gitIn(path, "checkout", "-b", branch, "origin/"+branch); err2 != nil {
					return hotfixBranchMsg{err: fmt.Errorf("切換到既有 %s 失敗：%v", branch, err2)}
				}
			}
			_, _ = gitIn(path, "pull", "origin", branch)
			return hotfixBranchMsg{branch: branch, reused: true}
		}
		if _, err := gitIn(path, "checkout", "-b", branch); err != nil {
			return hotfixBranchMsg{err: fmt.Errorf("建立分支 %s 失敗：%v", branch, err)}
		}
		if _, err := gitIn(path, "push", "-u", "origin", branch); err != nil {
			return hotfixBranchMsg{err: fmt.Errorf("推送 %s 到 origin 失敗：%v", branch, err)}
		}
		return hotfixBranchMsg{branch: branch}
	}
}

// hotfixCommitCmd 撈 origin/master..origin/<hotfix> 的 commit，當作 PR 描述用（同 commitCheckCmd 規則）。
func hotfixCommitCmd(path, branch string) tea.Cmd {
	return func() tea.Msg {
		if _, err := gitIn(path, "fetch", "origin"); err != nil {
			return hotfixCommitMsg{err: fmt.Errorf("git fetch 失敗：%v", err)}
		}
		out, err := gitIn(path, "log", "origin/master..origin/"+branch, "--oneline", "--no-merges", "--encoding=UTF-8")
		if err != nil {
			return hotfixCommitMsg{err: err}
		}
		var lines []string
		for _, l := range strings.Split(out, "\n") {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			if i := strings.IndexByte(l, ' '); i >= 0 {
				l = strings.TrimSpace(l[i+1:]) // 去掉 short hash
			}
			if strings.HasPrefix(l, "Merged PR") || strings.HasPrefix(l, "Merge branch") {
				continue
			}
			lines = append(lines, "- "+l)
		}
		return hotfixCommitMsg{commits: strings.Join(lines, "\n"), count: len(lines)}
	}
}

// buildHotfixBumpCommand 組出改版號的 Git Bash 指令：切到 hotfix 分支拉最新 → npm ci →
// npm run release <ver> → npm run build:prod → git add/commit/push。對齊 Invoke-Version-Bump。
func buildHotfixBumpCommand(projectPath, branch, version string) (string, error) {
	npmDir := projectPath
	switch {
	case fileExists(filepath.Join(projectPath, "src", "package.json")):
		npmDir = filepath.Join(projectPath, "src")
	case fileExists(filepath.Join(projectPath, "package.json")):
		npmDir = projectPath
	default:
		return "", fmt.Errorf("找不到 package.json（%s 或 %s\\src）", projectPath, projectPath)
	}
	root := winToBashPath(projectPath)
	nd := winToBashPath(npmDir)
	msg := "release: v" + version
	return fmt.Sprintf(
		"cd %q && git checkout %s && git pull origin %s && cd %q && npm ci && npm run release %s && npm run build:prod && cd %q && git add -A && git commit -m %q && git push origin %s",
		root, branch, branch, nd, version, root, msg, branch,
	), nil
}

// enterHotfix 啟動 /hotfix。config 未就緒或沒有含本機路徑的專案時，留在 home 並提示。
func (m *model) enterHotfix() tea.Cmd {
	if !m.cfgOK {
		m.status = "找不到設定，請先跑 /init"
		return nil
	}
	keys := releaseCandidateKeys(m.cfg)
	if len(keys) == 0 {
		m.status = "沒有含本機路徑(localPath)的專案；請先在 /task 或 /init 設定專案的本機路徑"
		return nil
	}
	m.mode = modeHotfix
	m.hfStep = hfProject
	m.errMsg, m.slackMsg, m.revNote = "", "", ""
	m.hfKeys = keys
	m.hfCursor = 0
	m.hfMapKey, m.hfPath, m.hfVersion, m.hfBranch = "", "", "", ""
	m.hfReused, m.hfCommits = false, ""
	m.hfMasterURL, m.hfDevelopURL, m.hfPRResult = "", "", ""
	m.chosenRev = reviewer{}
	m.reviewers = nil
	m.input.SetValue("")
	m.input.Placeholder = "篩選專案…"
	return nil
}

func (m model) hotfixHome() model {
	m.mode = modeHome
	m.loading = false
	m.errMsg = ""
	m.input.SetValue("")
	m.input.Placeholder = homePlaceholder
	return m
}

func (m model) hotfixFilteredKeys() []string {
	q := strings.TrimSpace(m.input.Value())
	var out []string
	for _, k := range m.hfKeys {
		mp := m.cfg.Mappings[k]
		label := k + " " + mp.AzureProject + " " + mp.AzureRepository
		if q == "" || fuzzyMatch(q, label) {
			out = append(out, k)
		}
	}
	return out
}

func (m model) hotfixProjectItems() []string {
	var out []string
	for _, k := range m.hotfixFilteredKeys() {
		mp := m.cfg.Mappings[k]
		out = append(out, k+"  ("+mp.AzureProject+" / "+mp.AzureRepository+")")
	}
	return out
}

// hotfixPhase 回傳目前在第幾個概念步驟（1..7）。
func (m model) hotfixPhase() int {
	switch m.hfStep {
	case hfProject:
		return 1
	case hfVersion:
		return 2
	case hfBranchConfirm, hfBranching:
		return 3
	case hfWaitCommit:
		return 4
	case hfBumpConfirm, hfBumping:
		return 5
	case hfReviewer:
		return 6
	default: // hfPRCreating, hfDone
		return 7
	}
}

// hotfixStepsView 列出 /hotfix 的所有步驟。開 hotfix 分支（push）、改版號 commit、建 PR
// 會實際寫入；選專案 / 版本號 / 等 commit 只是本地暫存或檢查。
func (m model) hotfixStepsView() string {
	return stepsView([]wizStep{
		{label: "選專案", val: m.hfMapKey},
		{label: "版本號", val: m.hfVersion},
		{label: "開 hotfix 分支", val: m.hfBranch, mutates: true},
		{label: "等修正 commit"},
		{label: "改版號 commit", mutates: true},
		{label: "選 reviewer"},
		{label: "建 master / develop PR", mutates: true},
	}, m.hotfixPhase())
}

// ---- async result handlers (dispatched from the main Update) ----

func (m model) onHotfixBranched(msg hotfixBranchMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.loading = false
		m.hfStep = hfBranchConfirm
		m.errMsg = msg.err.Error()
		return m, nil
	}
	m.hfBranch, m.hfReused, m.errMsg = msg.branch, msg.reused, ""
	m.hfStep = hfWaitCommit
	m.loading = true // 進來先自動檢查一次 commit
	return m, hotfixCommitCmd(m.hfPath, m.hfBranch)
}

func (m model) onHotfixCommits(msg hotfixCommitMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.errMsg = "檢查 commit 失敗：" + msg.err.Error()
		return m, nil
	}
	m.hfCommits, m.errMsg = msg.commits, "" // 空字串＝還沒有修正 commit
	return m, nil
}

func (m model) onHotfixBumped(msg hotfixBumpMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.loading = false
		m.hfStep = hfBumpConfirm
		m.errMsg = "改版號失敗：" + msg.err.Error()
		return m, nil
	}
	m.hfStep = hfReviewer
	m.loading = true
	m.revCursor = 0
	m.errMsg = ""
	return m, listReviewersCmd(m.cfg.AzureOrg, m.hfMapping.AzureProject, m.cfg.Slack.Members)
}

func (m model) onHotfixPRs(msg releasePRsMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.loading = false
		m.errMsg = "建立 PR 失敗：" + msg.err.Error()
		m.hfMasterURL = msg.masterURL
		m.hfStep = hfReviewer // 分支與 commit 都在，可重選 reviewer 再試
		return m, nil
	}
	m.hfMasterURL, m.hfDevelopURL, m.hfPRResult, m.errMsg = msg.masterURL, msg.developURL, msg.prResult, ""
	switch {
	case msg.reviewerErr != "":
		m.revNote = "reviewer 未加成功：" + msg.reviewerErr
	case m.chosenRev.email != "":
		m.revNote = "必要 reviewer：" + reviewerLabel(m.chosenRev) + "（已設 auto-complete）"
	default:
		m.revNote = "未加 reviewer"
	}
	if m.slackConfigured() && m.chosenRev.slackID != "" {
		m.slackMsg = "通知 Slack 中…"
		return m, slackNotifyCmd(m.cfg.SlackToken, m.cfg.Slack.Channel, m.chosenRev.slackID, m.hfPRResult)
	}
	m.loading = false
	if !m.slackConfigured() {
		m.slackMsg = "Slack 未設定，略過通知"
	} else {
		m.slackMsg = "reviewer 無 Slack 對應，略過通知"
	}
	m.hfStep = hfDone
	return m, nil
}

func (m model) onHotfixSlackDone(msg slackDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.slackMsg = "Slack 通知失敗：" + msg.err.Error()
	} else {
		m.slackMsg = "已通知 Slack #" + m.cfg.Slack.Channel
	}
	m.hfStep = hfDone
	return m, nil
}

// ---- key handling ----

func (m model) updateHotfix(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() == "esc" && m.hfStep != hfBranching && m.hfStep != hfBumping && m.hfStep != hfPRCreating {
		return m.hotfixHome(), nil
	}

	switch m.hfStep {
	case hfProject:
		keys := m.hotfixFilteredKeys()
		switch key.String() {
		case "up":
			if n := len(keys); n > 0 {
				m.hfCursor = (m.hfCursor - 1 + n) % n
			}
			return m, nil
		case "down":
			if n := len(keys); n > 0 {
				m.hfCursor = (m.hfCursor + 1) % n
			}
			return m, nil
		case "enter":
			if m.hfCursor >= len(keys) {
				return m, nil
			}
			m.hfMapKey = keys[m.hfCursor]
			m.hfMapping = m.cfg.Mappings[m.hfMapKey]
			m.hfPath = mappingLocalPathOf(m.cfg, m.hfMapKey)
			m.input.SetValue("")
			m.input.Placeholder = "版本號，例如 1.6.3"
			m.hfStep = hfVersion
			return m, nil
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(key)
			m.hfCursor = 0
			return m, cmd
		}

	case hfVersion:
		if key.String() == "enter" {
			v := strings.TrimSpace(m.input.Value())
			if !releaseVersionRe.MatchString(v) {
				m.errMsg = "版本號格式需為 x.y.z（例如 1.6.3）"
				return m, nil
			}
			m.hfVersion, m.hfBranch, m.errMsg = v, "hotfix/v"+v, ""
			m.input.SetValue("")
			m.hfStep = hfBranchConfirm
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return m, cmd

	case hfBranchConfirm:
		if key.String() == "enter" {
			m.hfStep = hfBranching
			m.loading = true
			m.errMsg = ""
			return m, hotfixBranchCmd(m.hfPath, m.hfBranch)
		}
		return m, nil

	case hfBranching:
		return m, nil // wait for hotfixBranchMsg

	case hfWaitCommit:
		if m.loading {
			return m, nil
		}
		switch key.String() {
		case "enter":
			if m.hfCommits == "" {
				m.loading = true
				m.errMsg = ""
				return m, hotfixCommitCmd(m.hfPath, m.hfBranch)
			}
			m.hfStep = hfBumpConfirm
			m.errMsg = ""
			return m, nil
		case "r":
			m.loading = true
			m.errMsg = ""
			return m, hotfixCommitCmd(m.hfPath, m.hfBranch)
		}
		return m, nil

	case hfBumpConfirm:
		if key.String() == "enter" {
			gitBash := releaseGitBashPath()
			if !fileExists(gitBash) {
				m.errMsg = "找不到 Git Bash（" + gitBash + "），無法改版號"
				return m, nil
			}
			script, err := buildHotfixBumpCommand(m.hfPath, m.hfBranch, m.hfVersion)
			if err != nil {
				m.errMsg = err.Error()
				return m, nil
			}
			m.hfStep = hfBumping
			m.loading = true
			m.errMsg = ""
			cmd := exec.Command(gitBash, "-c", script)
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return hotfixBumpMsg{err: err}
			})
		}
		return m, nil

	case hfBumping:
		return m, nil // wait for hotfixBumpMsg

	case hfReviewer:
		if m.loading {
			return m, nil
		}
		n := len(m.reviewers) + 1 // + 略過
		switch key.String() {
		case "up":
			m.revCursor = (m.revCursor - 1 + n) % n
		case "down":
			m.revCursor = (m.revCursor + 1) % n
		case "enter":
			if m.revCursor < len(m.reviewers) {
				m.chosenRev = m.reviewers[m.revCursor]
			} else {
				m.chosenRev = reviewer{}
			}
			m.hfStep = hfPRCreating
			m.loading = true
			m.errMsg = ""
			desc := m.hfCommits
			if strings.TrimSpace(desc) == "" {
				desc = "Hotfix v" + m.hfVersion
			}
			return m, createReleasePRsCmd(m.cfg.AzureOrg, m.hfMapping.AzureProject, m.hfMapping.AzureRepository,
				m.hfBranch, desc, nil, m.chosenRev)
		}
		return m, nil

	case hfPRCreating:
		return m, nil // wait for releasePRsMsg

	case hfDone:
		if key.String() == "enter" {
			return m.hotfixHome(), nil
		}
		return m, nil
	}
	return m, nil
}

// ---- view ----

func (m model) viewHotfix() string {
	var body strings.Builder

	switch m.hfStep {
	case hfProject:
		body.WriteString(styleBold(accent, "Hotfix — 選專案") + "\n\n")
		body.WriteString(renderList(m.hotfixProjectItems(), m.hfCursor))
	case hfVersion:
		body.WriteString(styleBold(accent, "Hotfix — 版本號") + "\n\n")
		body.WriteString(styleFg(muted, "專案 ") + m.hfMapKey + "  (" + m.hfMapping.AzureProject + " / " + m.hfMapping.AzureRepository + ")\n\n")
		body.WriteString(styleFg(muted, "輸入 hotfix 版本號（x.y.z），分支會是 hotfix/vX.Y.Z。"))
	case hfBranchConfirm:
		body.WriteString(styleBold(accent, "Hotfix — 確認開分支") + "\n\n")
		body.WriteString(styleFg(muted, "專案     ") + m.hfMapKey + "  (" + m.hfMapping.AzureProject + " / " + m.hfMapping.AzureRepository + ")\n")
		body.WriteString(styleFg(muted, "本機路徑 ") + m.hfPath + "\n")
		body.WriteString(styleFg(muted, "分支     ") + m.hfBranch + "\n\n")
		body.WriteString(styleFg(muted, "將更新 master、從 master 開 ") + styleBold(accent, m.hfBranch) +
			styleFg(muted, " 並 push 到 origin。") + "\n")
		body.WriteString(styleFg(errCol, "⚠ 按 Enter 會實際 push 新分支（寫入遠端）。"))
	case hfBranching:
		body.WriteString(styleBold(accent, "Hotfix — 開分支中") + "\n\n")
		body.WriteString(m.spin.View() + " " + styleFg(muted, "更新 master、開 "+m.hfBranch+"、push…"))
	case hfWaitCommit:
		body.WriteString(styleBold(accent, "Hotfix — 等修正 commit") + "\n\n")
		if m.hfReused {
			body.WriteString(styleFg(muted, "（沿用既有遠端分支 ") + m.hfBranch + styleFg(muted, "）") + "\n")
		}
		body.WriteString(styleFg(muted, "請把修正 commit push 到 ") + styleBold(accent, m.hfBranch) + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "檢查 commit…"))
		} else if m.hfCommits == "" {
			body.WriteString(styleFg(muted, "尚未偵測到新 commit。push 完按 ⏎ 重新檢查。"))
		} else {
			body.WriteString(styleFg(okCol, "✓ 已偵測到修正 commit：") + "\n" + styleFg(dim, m.hfCommits))
		}
	case hfBumpConfirm:
		body.WriteString(styleBold(accent, "Hotfix — 確認改版號") + "\n\n")
		body.WriteString(styleFg(muted, "分支 ") + m.hfBranch + styleFg(muted, "   版本 ") + m.hfVersion + "\n\n")
		body.WriteString(styleFg(muted, "將用 Git Bash 跑 ") + styleBold(accent, "npm ci → npm run release "+m.hfVersion+" → npm run build:prod") +
			styleFg(muted, "，再 commit（release: v"+m.hfVersion+"）並 push。過程顯示在終端機。") + "\n")
		body.WriteString(styleFg(errCol, "⚠ 按 Enter 會實際 commit 並 push（寫入遠端）。"))
	case hfBumping:
		body.WriteString(styleBold(accent, "Hotfix — 改版號中") + "\n\n")
		body.WriteString(m.spin.View() + " " + styleFg(muted, "npm 改版號、build、commit、push…（輸出顯示於終端機）"))
	case hfReviewer:
		body.WriteString(styleBold(accent, "Hotfix — 選 reviewer") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "讀取 reviewer…"))
		} else {
			body.WriteString(styleFg(muted, "分支 ") + m.hfBranch + "\n\n")
			body.WriteString(renderList(m.releaseReviewerItems(), m.revCursor))
		}
	case hfPRCreating:
		body.WriteString(styleBold(accent, "Hotfix — 建立 PR") + "\n\n")
		body.WriteString(m.spin.View() + " " + styleFg(muted, "建立 master / develop PR…"))
	case hfDone:
		body.WriteString(styleBold(accent, "Hotfix 完成") + "\n\n")
		body.WriteString(styleFg(okCol, "✓ master  ") + styleFg(dim, m.hfMasterURL) + "\n")
		body.WriteString(styleFg(okCol, "✓ develop ") + styleFg(dim, m.hfDevelopURL) + "\n")
		if m.revNote != "" {
			body.WriteString(styleFg(muted, m.revNote) + "\n")
		}
		if m.slackMsg != "" {
			body.WriteString(styleFg(muted, m.slackMsg))
		}
	}

	if m.errMsg != "" {
		body.WriteString("\n\n" + styleFg(errCol, "⚠ "+m.errMsg))
	}

	content := strings.TrimRight(body.String(), "\n")
	if m.hfStep == hfProject || m.hfStep == hfVersion {
		content = m.input.View() + "\n\n" + content
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dim).
		Padding(0, 2).
		Render(content)

	hint := "↑↓ 選擇   ⏎ 確認   esc 取消"
	switch m.hfStep {
	case hfVersion:
		hint = "⏎ 繼續   esc 取消"
	case hfBranchConfirm:
		hint = "⏎ 更新 master 並開分支   esc 取消"
	case hfWaitCommit:
		if m.hfCommits == "" {
			hint = "⏎ 重新檢查   esc 取消"
		} else {
			hint = "⏎ 繼續改版號   r 重新檢查   esc 取消"
		}
	case hfBumpConfirm:
		hint = "⏎ 改版號並 push   esc 取消"
	case hfBranching, hfBumping, hfPRCreating:
		hint = "請稍候…"
	case hfDone:
		hint = "⏎ 返回"
	}
	return m.banner() + m.hotfixStepsView() + "\n" + box + "\n" + m.hintbar(hint)
}
