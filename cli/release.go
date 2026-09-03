package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---- /release: 跑專案的 release.sh 產生 release 分支，成功後開 master + develop 兩個 PR + Slack ----
// 對齊 PowerShell 的 Start-Manual-Release-Process / Start-Release-Pr-Process。

type releaseStep int

const (
	rlProject    releaseStep = iota // 選有本機路徑的專案
	rlVersion                       // 輸入版本號 x.y.z
	rlConfirm                       // 確認後用 Git Bash 執行 release.sh
	rlRunning                       // release.sh 執行中（輸出接管終端機）
	rlAfterRun                      // 跑完：顯示 release 分支或錯誤，確認才建 PR
	rlReviewer                      // 選 reviewer（或略過）
	rlPRCreating                    // 建立 master / develop PR
	rlDone                          // 完成：兩個 PR + Slack 狀態
)

// releaseRanMsg 是 release.sh 執行結果：branch 是產生的 release/* 分支，err 非空代表失敗。
type releaseRanMsg struct {
	branch string
	err    error
}

// releasePRsMsg 是 master + develop 兩個 PR 都建立後的結果。
type releasePRsMsg struct {
	masterURL   string
	developURL  string
	prResult    string // Slack 用的合併訊息（master + develop）
	reviewerErr string
	err         error
}

var releaseVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// releaseGitBashPath 是 release.sh 需要的 Git Bash（與 PowerShell 版寫死同一路徑）。
func releaseGitBashPath() string { return `C:\Program Files\Git\bin\bash.exe` }

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// winToBashPath 把 C:\a\b 轉成 Git Bash 的 /C/a/b（與 PowerShell 版同規則）。
func winToBashPath(p string) string {
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + p[:1] + p[2:]
	}
	return strings.ReplaceAll(p, "\\", "/")
}

// mappingLocalPathOf 找某個對應的本機路徑：先看 mapping 自身的 localPath，
// 再退回 projectPaths（以 key / repo / project 為鍵，與 /task 取路徑同邏輯）。
func mappingLocalPathOf(c config, key string) string {
	mp := c.Mappings[key]
	if p := cleanPath(mp.LocalPath); p != "" {
		return p
	}
	for _, k := range []string{key, mp.AzureRepository, mp.AzureProject} {
		if k != "" {
			if p := cleanPath(c.ProjectPaths[k]); p != "" {
				return p
			}
		}
	}
	return ""
}

// releaseCandidateKeys 是可跑 release 的專案（有本機路徑），排序後回傳。
func releaseCandidateKeys(c config) []string {
	var keys []string
	for k := range c.Mappings {
		if mappingLocalPathOf(c, k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// buildReleaseBashCommand 組出在 Git Bash 執行的指令：cd 到 release.sh 所在目錄，
// 有 package.json 就先 npm ci（確保依賴），再 ./release.sh <version>。
func buildReleaseBashCommand(projectPath, version string) (string, error) {
	workDir := projectPath
	switch {
	case fileExists(filepath.Join(projectPath, "src", "release.sh")):
		workDir = filepath.Join(projectPath, "src")
	case fileExists(filepath.Join(projectPath, "release.sh")):
		workDir = projectPath
	default:
		return "", fmt.Errorf("找不到 release.sh（%s 或 %s\\src）", projectPath, projectPath)
	}
	npm := ""
	if fileExists(filepath.Join(workDir, "package.json")) {
		npm = "npm ci && "
	}
	return fmt.Sprintf("cd %q && %s./release.sh %s", winToBashPath(workDir), npm, version), nil
}

// latestReleaseBranch 撈本機最新的 release/* 分支（字典序最後，與 PowerShell 版 sort|tail 一致）。
func latestReleaseBranch(path string) string {
	out, err := gitIn(path, "branch", "--list", "release/*")
	if err != nil {
		return ""
	}
	var names []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "* "))
		if l != "" {
			names = append(names, l)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[len(names)-1]
}

// createReleasePRsCmd 序列開兩個 PR：master（保留來源分支）與 develop（合併後刪分支、
// 標題加 -develop 後綴），對齊 PowerShell 的 Start-Release-Pr-Process。
func createReleasePRsCmd(org, project, repo, branch, desc string, taskIDs []int, rev reviewer) tea.Cmd {
	return func() tea.Msg {
		mr := createOnePR(org, project, repo, branch, "master", branch, desc, taskIDs, rev, false)
		if mr.err != nil {
			return releasePRsMsg{err: fmt.Errorf("master PR：%v", mr.err)}
		}
		dr := createOnePR(org, project, repo, branch, "develop", branch+"-develop", desc, taskIDs, rev, true)
		if dr.err != nil {
			return releasePRsMsg{masterURL: mr.url, err: fmt.Errorf("develop PR：%v（master PR 已建立：%s）", dr.err, mr.url)}
		}
		prResult := prResultText(project, "master", mr.url, mr.title, mr.id) + "\n" +
			prResultText(project, "develop", dr.url, dr.title, dr.id)
		revErr := mr.reviewerErr
		if revErr == "" {
			revErr = dr.reviewerErr
		}
		return releasePRsMsg{masterURL: mr.url, developURL: dr.url, prResult: prResult, reviewerErr: revErr}
	}
}

// enterRelease 啟動 /release。config 未就緒或沒有含本機路徑的專案時，留在 home 並提示。
func (m *model) enterRelease() tea.Cmd {
	if !m.cfgOK {
		m.status = "找不到設定，請先跑 /init"
		return nil
	}
	keys := releaseCandidateKeys(m.cfg)
	if len(keys) == 0 {
		m.status = "沒有含本機路徑(localPath)的專案；請先在 /task 或 /init 設定專案的本機路徑"
		return nil
	}
	m.mode = modeRelease
	m.rstep = rlProject
	m.errMsg = ""
	m.slackMsg = ""
	m.revNote = ""
	m.rlKeys = keys
	m.rlCursor = 0
	m.rlMapKey, m.rlPath, m.rlVersion, m.rlBranch = "", "", "", ""
	m.rlMasterURL, m.rlDevelopURL, m.rlPRResult = "", "", ""
	m.chosenRev = reviewer{}
	m.reviewers = nil
	m.input.SetValue("")
	m.input.Placeholder = "篩選專案…"
	return nil
}

func (m model) releaseHome() model {
	m.mode = modeHome
	m.loading = false
	m.errMsg = ""
	m.input.SetValue("")
	m.input.Placeholder = homePlaceholder
	return m
}

func (m model) releaseFilteredKeys() []string {
	q := strings.TrimSpace(m.input.Value())
	var out []string
	for _, k := range m.rlKeys {
		mp := m.cfg.Mappings[k]
		label := k + " " + mp.AzureProject + " " + mp.AzureRepository
		if q == "" || fuzzyMatch(q, label) {
			out = append(out, k)
		}
	}
	return out
}

func (m model) releaseProjectItems() []string {
	var out []string
	for _, k := range m.releaseFilteredKeys() {
		mp := m.cfg.Mappings[k]
		out = append(out, k+"  ("+mp.AzureProject+" / "+mp.AzureRepository+")")
	}
	return out
}

func (m model) releaseReviewerItems() []string {
	var out []string
	for _, r := range m.reviewers {
		out = append(out, reviewerLabel(r))
	}
	return append(out, "略過（不指定 reviewer）")
}

// releasePhase 回傳目前在第幾個概念步驟（1..6）。
func (m model) releasePhase() int {
	switch m.rstep {
	case rlProject:
		return 1
	case rlVersion:
		return 2
	case rlConfirm:
		return 3
	case rlRunning, rlAfterRun:
		return 4
	case rlReviewer:
		return 5
	default: // rlPRCreating, rlDone
		return 6
	}
}

// releaseStepsView 列出 /release 的所有步驟。跑 release.sh（改版號、push release 分支）
// 與建 PR 會實際寫入；前面的選擇都只是本地暫存。
func (m model) releaseStepsView() string {
	return stepsView([]wizStep{
		{label: "選專案", val: m.rlMapKey},
		{label: "版本號", val: m.rlVersion},
		{label: "確認執行"},
		{label: "跑 release.sh", val: m.rlBranch, mutates: true},
		{label: "選 reviewer"},
		{label: "建 master / develop PR", mutates: true},
	}, m.releasePhase())
}

// ---- async result handlers (dispatched from the main Update) ----

func (m model) onReleaseRan(msg releaseRanMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	m.rstep = rlAfterRun
	if msg.err != nil {
		m.errMsg = "release.sh 失敗：" + msg.err.Error()
		return m, nil
	}
	if msg.branch == "" {
		m.errMsg = "release.sh 完成，但找不到 release/* 分支"
		return m, nil
	}
	m.rlBranch, m.errMsg = msg.branch, ""
	return m, nil
}

func (m model) onReleasePRs(msg releasePRsMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.loading = false
		m.errMsg = "建立 PR 失敗：" + msg.err.Error()
		m.rlMasterURL = msg.masterURL
		m.rstep = rlAfterRun // 回到確認點，分支仍在，可再試
		return m, nil
	}
	m.rlMasterURL, m.rlDevelopURL, m.rlPRResult, m.errMsg = msg.masterURL, msg.developURL, msg.prResult, ""
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
		return m, slackNotifyCmd(m.cfg.SlackToken, m.cfg.Slack.Channel, m.chosenRev.slackID, m.rlPRResult)
	}
	m.loading = false
	if !m.slackConfigured() {
		m.slackMsg = "Slack 未設定，略過通知"
	} else {
		m.slackMsg = "reviewer 無 Slack 對應，略過通知"
	}
	m.rstep = rlDone
	return m, nil
}

func (m model) onReleaseSlackDone(msg slackDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.slackMsg = "Slack 通知失敗：" + msg.err.Error()
	} else {
		m.slackMsg = "已通知 Slack #" + m.cfg.Slack.Channel
	}
	m.rstep = rlDone
	return m, nil
}

// ---- key handling ----

func (m model) updateRelease(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() == "esc" && m.rstep != rlRunning && m.rstep != rlPRCreating {
		return m.releaseHome(), nil
	}

	switch m.rstep {
	case rlProject:
		keys := m.releaseFilteredKeys()
		switch key.String() {
		case "up":
			if n := len(keys); n > 0 {
				m.rlCursor = (m.rlCursor - 1 + n) % n
			}
			return m, nil
		case "down":
			if n := len(keys); n > 0 {
				m.rlCursor = (m.rlCursor + 1) % n
			}
			return m, nil
		case "enter":
			if m.rlCursor >= len(keys) {
				return m, nil
			}
			m.rlMapKey = keys[m.rlCursor]
			m.rlMapping = m.cfg.Mappings[m.rlMapKey]
			m.rlPath = mappingLocalPathOf(m.cfg, m.rlMapKey)
			m.input.SetValue("")
			m.input.Placeholder = "版本號，例如 1.6.2"
			m.rstep = rlVersion
			return m, nil
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(key)
			m.rlCursor = 0
			return m, cmd
		}

	case rlVersion:
		if key.String() == "enter" {
			v := strings.TrimSpace(m.input.Value())
			if !releaseVersionRe.MatchString(v) {
				m.errMsg = "版本號格式需為 x.y.z（例如 1.6.2）"
				return m, nil
			}
			m.rlVersion, m.errMsg = v, ""
			m.input.SetValue("")
			m.rstep = rlConfirm
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return m, cmd

	case rlConfirm:
		if key.String() == "enter" {
			gitBash := releaseGitBashPath()
			if !fileExists(gitBash) {
				m.errMsg = "找不到 Git Bash（" + gitBash + "），無法執行 release.sh"
				return m, nil
			}
			script, err := buildReleaseBashCommand(m.rlPath, m.rlVersion)
			if err != nil {
				m.errMsg = err.Error()
				return m, nil
			}
			m.rstep = rlRunning
			m.loading = true
			m.errMsg = ""
			path := m.rlPath
			cmd := exec.Command(gitBash, "-c", script)
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				if err != nil {
					return releaseRanMsg{err: err}
				}
				return releaseRanMsg{branch: latestReleaseBranch(path)}
			})
		}
		return m, nil

	case rlRunning:
		return m, nil // wait for ExecProcess

	case rlAfterRun:
		if m.errMsg != "" {
			return m, nil // 失敗：只能 esc 返回（已在最上面處理）
		}
		if key.String() == "enter" {
			m.rstep = rlReviewer
			m.loading = true
			m.revCursor = 0
			m.errMsg = ""
			return m, listReviewersCmd(m.cfg.AzureOrg, m.rlMapping.AzureProject, m.cfg.Slack.Members)
		}
		return m, nil

	case rlReviewer:
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
			m.rstep = rlPRCreating
			m.loading = true
			m.errMsg = ""
			desc := "Release v" + m.rlVersion
			return m, createReleasePRsCmd(m.cfg.AzureOrg, m.rlMapping.AzureProject, m.rlMapping.AzureRepository,
				m.rlBranch, desc, nil, m.chosenRev)
		}
		return m, nil

	case rlPRCreating:
		return m, nil // wait for releasePRsMsg

	case rlDone:
		if key.String() == "enter" {
			return m.releaseHome(), nil
		}
		return m, nil
	}
	return m, nil
}

// ---- view ----

func (m model) viewRelease() string {
	var body strings.Builder

	switch m.rstep {
	case rlProject:
		body.WriteString(styleBold(accent, "Release — 選專案") + "\n\n")
		body.WriteString(renderList(m.releaseProjectItems(), m.rlCursor))
	case rlVersion:
		body.WriteString(styleBold(accent, "Release — 版本號") + "\n\n")
		body.WriteString(styleFg(muted, "專案 ") + m.rlMapKey + "  (" + m.rlMapping.AzureProject + " / " + m.rlMapping.AzureRepository + ")\n\n")
		body.WriteString(styleFg(muted, "輸入版本號（x.y.z），會傳給 release.sh。"))
	case rlConfirm:
		body.WriteString(styleBold(accent, "Release — 確認執行") + "\n\n")
		body.WriteString(styleFg(muted, "專案     ") + m.rlMapKey + "  (" + m.rlMapping.AzureProject + " / " + m.rlMapping.AzureRepository + ")\n")
		body.WriteString(styleFg(muted, "本機路徑 ") + m.rlPath + "\n")
		body.WriteString(styleFg(muted, "版本     ") + m.rlVersion + "\n\n")
		body.WriteString(styleFg(muted, "將用 Git Bash 執行 ") + styleBold(accent, "release.sh "+m.rlVersion) +
			styleFg(muted, "（有 package.json 會先 npm ci）。過程顯示在終端機，成功後才建 PR。"))
	case rlRunning:
		body.WriteString(styleBold(accent, "Release — 執行中") + "\n\n")
		body.WriteString(m.spin.View() + " " + styleFg(muted, "執行 release.sh…（輸出顯示於終端機）"))
	case rlAfterRun:
		body.WriteString(styleBold(accent, "Release — release.sh 完成") + "\n\n")
		if m.errMsg == "" {
			body.WriteString(styleFg(okCol, "✓ release 分支：") + m.rlBranch + "\n\n")
			body.WriteString(styleFg(muted, "確認後建立 PR（→ master / develop）。取消(esc)則保留分支、不建 PR、不發 Slack。"))
		}
	case rlReviewer:
		body.WriteString(styleBold(accent, "Release — 選 reviewer") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "讀取 reviewer…"))
		} else {
			body.WriteString(styleFg(muted, "release 分支 ") + m.rlBranch + "\n\n")
			body.WriteString(renderList(m.releaseReviewerItems(), m.revCursor))
		}
	case rlPRCreating:
		body.WriteString(styleBold(accent, "Release — 建立 PR") + "\n\n")
		body.WriteString(m.spin.View() + " " + styleFg(muted, "建立 master / develop PR…"))
	case rlDone:
		body.WriteString(styleBold(accent, "Release 完成") + "\n\n")
		body.WriteString(styleFg(okCol, "✓ master  ") + styleFg(dim, m.rlMasterURL) + "\n")
		body.WriteString(styleFg(okCol, "✓ develop ") + styleFg(dim, m.rlDevelopURL) + "\n")
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
	if m.rstep == rlProject || m.rstep == rlVersion {
		content = m.input.View() + "\n\n" + content
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dim).
		Padding(0, 2).
		Render(content)

	hint := "↑↓ 選擇   ⏎ 確認   esc 取消"
	switch m.rstep {
	case rlVersion:
		hint = "⏎ 繼續   esc 取消"
	case rlConfirm:
		hint = "⏎ 執行 release.sh   esc 取消"
	case rlAfterRun:
		if m.errMsg != "" {
			hint = "esc 返回"
		} else {
			hint = "⏎ 建立 PR   esc 取消（保留分支）"
		}
	case rlRunning, rlPRCreating:
		hint = "請稍候…"
	case rlDone:
		hint = "⏎ 返回"
	}
	return m.banner() + m.releaseStepsView() + "\n" + box + "\n" + m.hintbar(hint)
}
