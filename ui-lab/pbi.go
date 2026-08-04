package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---- /pbi: 建立 Product Backlog Item（選專案 -> 標題 -> 自動帶 Area/Iteration -> 指派自己）----

type pbiStep int

const (
	pbProject  pbiStep = iota // pick project from mappings
	pbArea                    // fallback: pick Area when the mapping has none
	pbTitle                   // enter the PBI title
	pbResolve                 // resolve the current-month iteration
	pbIter                    // fallback: pick Iteration when the month is missing
	pbConfirm                 // show summary, Enter to create
	pbCreating                // creating…
	pbDone                    // created, show id + url
)

type whoAmIMsg struct {
	name string
	err  error
}

type iterationsMsg struct {
	iters []string
	err   error
}

type pbiCreatedMsg struct {
	id  int
	url string
	err error
}

// whoAmICmd resolves the signed-in az user (used as the PBI assignee).
func whoAmICmd() tea.Cmd {
	return func() tea.Msg {
		out, err := run("az", "account", "show", "--query", "user.name", "-o", "tsv")
		if err != nil {
			return whoAmIMsg{err: err}
		}
		return whoAmIMsg{name: strings.TrimSpace(out)}
	}
}

// listIterationsCmd returns iteration paths in work-item form ("Project\2026年\8月").
func listIterationsCmd(org, project string) tea.Cmd {
	return func() tea.Msg {
		raw, err := run("az", "boards", "iteration", "project", "list",
			"--organization", org, "--project", project, "--depth", "5", "-o", "json")
		if err != nil {
			return iterationsMsg{err: err}
		}
		var root areaNode
		if e := json.Unmarshal([]byte(raw), &root); e != nil {
			return iterationsMsg{err: e}
		}
		var iters []string
		flattenIterations(root, true, &iters)
		return iterationsMsg{iters: iters}
	}
}

func flattenIterations(n areaNode, isRoot bool, out *[]string) {
	if !isRoot {
		*out = append(*out, normalizeIterationPath(n.Path))
	}
	for _, c := range n.Children {
		flattenIterations(c, false, out)
	}
}

// normalizeIterationPath turns "\ESHClouds\Iteration\2026年\8月" into "ESHClouds\2026年\8月",
// the form `az boards work-item create --iteration` expects.
func normalizeIterationPath(p string) string {
	p = strings.TrimPrefix(p, "\\")
	segs := strings.Split(p, "\\")
	if len(segs) >= 2 && segs[1] == "Iteration" {
		segs = append(segs[:1:1], segs[2:]...)
	}
	return strings.Join(segs, "\\")
}

// createPbiCmd creates a Product Backlog Item assigned to assignee.
func createPbiCmd(org, project, title, area, iteration, assignee string) tea.Cmd {
	return func() tea.Msg {
		args := []string{
			"boards", "work-item", "create",
			"--type", "Product Backlog Item",
			"--title", title,
			"--project", project,
			"--organization", org,
			"--area", area,
			"--iteration", iteration,
			"-o", "json",
		}
		if assignee != "" {
			args = append(args, "--assigned-to", assignee)
		}
		out, err := run("az", args...)
		if err != nil {
			return pbiCreatedMsg{err: err}
		}
		var raw struct {
			ID int `json:"id"`
		}
		if e := json.Unmarshal([]byte(out), &raw); e != nil {
			return pbiCreatedMsg{err: e}
		}
		if raw.ID == 0 {
			return pbiCreatedMsg{err: errors.New("建立失敗：沒有回傳 ID")}
		}
		url := strings.TrimRight(org, "/") + "/" + project + "/_workitems/edit/" + strconv.Itoa(raw.ID)
		return pbiCreatedMsg{id: raw.ID, url: url}
	}
}

// computeIterPath is the auto-guessed current-month iteration: "<wiProject>\<year>年\<month>月".
func (m model) computeIterPath() string {
	now := time.Now()
	return fmt.Sprintf("%s\\%d年\\%d月", m.cfg.WorkItemProject, now.Year(), int(now.Month()))
}

func sortedMappingKeys(mp map[string]mappingCfg) []string {
	keys := make([]string, 0, len(mp))
	for k := range mp {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// enterPbi starts the /pbi flow. Returns nil (staying home) if config isn't ready.
func (m *model) enterPbi() tea.Cmd {
	if !m.cfgOK {
		m.status = "找不到設定，請先跑 /init"
		return nil
	}
	if m.cfg.WorkItemProject == "" {
		m.status = "config 缺少 workItemProject，無法建立 PBI"
		return nil
	}
	if len(m.cfg.Mappings) == 0 {
		m.status = "config 沒有專案對應(azureProjectMappings)"
		return nil
	}
	m.mode = modePbi
	m.pstep = pbProject
	m.errMsg = ""
	m.pKeys = sortedMappingKeys(m.cfg.Mappings)
	m.pCursor = 0
	m.pMapKey, m.pAreaPath, m.pTitle, m.pIterPath, m.pUser = "", "", "", "", ""
	m.pCreatedID, m.pURL = 0, ""
	m.input.SetValue("")
	m.input.Placeholder = "篩選專案…"
	return whoAmICmd() // resolve assignee in the background
}

func (m model) pbiFilteredKeys() []string {
	q := strings.TrimSpace(m.input.Value())
	var out []string
	for _, k := range m.pKeys {
		mp := m.cfg.Mappings[k]
		label := k + " " + mp.AzureProject + " " + mp.AzureRepository
		if q == "" || fuzzyMatch(q, label) {
			out = append(out, k)
		}
	}
	return out
}

func (m model) pbiProjectItems() []string {
	var out []string
	for _, k := range m.pbiFilteredKeys() {
		mp := m.cfg.Mappings[k]
		out = append(out, k+"  ("+mp.AzureProject+" / "+mp.AzureRepository+")")
	}
	return out
}

func (m model) pbiAreaItems() []areaInfo {
	q := strings.TrimSpace(m.input.Value())
	var out []areaInfo
	for _, a := range m.pAreaList {
		if q == "" || fuzzyMatch(q, a.name) || fuzzyMatch(q, a.path) {
			out = append(out, a)
		}
	}
	return out
}

func (m model) pbiIterItems() []string {
	q := strings.TrimSpace(m.input.Value())
	var out []string
	for _, it := range m.pIterList {
		if q == "" || fuzzyMatch(q, it) {
			out = append(out, it)
		}
	}
	return out
}

func (m model) pbiHome() model {
	m.mode = modeHome
	m.loading = false
	m.errMsg = ""
	m.input.SetValue("")
	m.input.Placeholder = homePlaceholder
	return m
}

func (m model) updatePbi(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() == "esc" && m.pstep != pbCreating {
		return m.pbiHome(), nil
	}

	switch m.pstep {
	case pbProject:
		keys := m.pbiFilteredKeys()
		switch key.String() {
		case "up":
			if n := len(keys); n > 0 {
				m.pCursor = (m.pCursor - 1 + n) % n
			}
			return m, nil
		case "down":
			if n := len(keys); n > 0 {
				m.pCursor = (m.pCursor + 1) % n
			}
			return m, nil
		case "enter":
			if m.pCursor >= len(keys) {
				return m, nil
			}
			m.pMapKey = keys[m.pCursor]
			m.pMapping = m.cfg.Mappings[m.pMapKey]
			m.input.SetValue("")
			if m.pMapping.AreaPath != "" {
				m.pAreaPath = m.pMapping.AreaPath
				m.pstep = pbTitle
				m.input.Placeholder = "輸入 PBI 標題"
				return m, nil
			}
			m.pstep = pbArea
			m.loading = true
			m.pCursor = 0
			m.input.Placeholder = "篩選 Area…"
			return m, listAreasCmd(m.cfg.AzureOrg, m.cfg.WorkItemProject)
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(key)
			m.pCursor = 0
			return m, cmd
		}

	case pbArea:
		if m.loading {
			return m, nil
		}
		items := m.pbiAreaItems()
		switch key.String() {
		case "up":
			if n := len(items); n > 0 {
				m.pCursor = (m.pCursor - 1 + n) % n
			}
			return m, nil
		case "down":
			if n := len(items); n > 0 {
				m.pCursor = (m.pCursor + 1) % n
			}
			return m, nil
		case "enter":
			if m.pCursor >= len(items) {
				return m, nil
			}
			m.pAreaPath = items[m.pCursor].path
			m.input.SetValue("")
			m.pstep = pbTitle
			m.input.Placeholder = "輸入 PBI 標題"
			return m, nil
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(key)
			m.pCursor = 0
			return m, cmd
		}

	case pbTitle:
		if key.String() == "enter" {
			t := strings.TrimSpace(m.input.Value())
			if t == "" {
				return m, nil
			}
			m.pTitle = t
			m.input.SetValue("")
			m.pstep = pbResolve
			m.loading = true
			return m, listIterationsCmd(m.cfg.AzureOrg, m.cfg.WorkItemProject)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return m, cmd

	case pbIter:
		items := m.pbiIterItems()
		switch key.String() {
		case "up":
			if n := len(items); n > 0 {
				m.pCursor = (m.pCursor - 1 + n) % n
			}
			return m, nil
		case "down":
			if n := len(items); n > 0 {
				m.pCursor = (m.pCursor + 1) % n
			}
			return m, nil
		case "enter":
			if m.pCursor >= len(items) {
				return m, nil
			}
			m.pIterPath = items[m.pCursor]
			m.input.SetValue("")
			m.pstep = pbConfirm
			return m, nil
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(key)
			m.pCursor = 0
			return m, cmd
		}

	case pbConfirm:
		if key.String() == "enter" {
			m.pstep = pbCreating
			m.loading = true
			m.errMsg = ""
			return m, createPbiCmd(m.cfg.AzureOrg, m.cfg.WorkItemProject, m.pTitle, m.pAreaPath, m.pIterPath, m.pUser)
		}
		return m, nil

	case pbDone:
		if key.String() == "enter" {
			return m.pbiHome(), nil
		}
		return m, nil
	}
	return m, nil
}

func (m model) viewPbi() string {
	var body strings.Builder

	switch m.pstep {
	case pbProject:
		body.WriteString(styleBold(accent, "建立 PBI — 選專案") + "\n\n")
		body.WriteString(renderList(m.pbiProjectItems(), m.pCursor))
	case pbArea:
		body.WriteString(styleBold(accent, "建立 PBI — 選 Area") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "讀取 Area…"))
		} else {
			body.WriteString(styleFg(muted, m.pMapKey+" 沒設定 areaPath，選一個：") + "\n\n")
			var items []string
			for _, a := range m.pbiAreaItems() {
				items = append(items, a.path)
			}
			body.WriteString(renderList(items, m.pCursor))
		}
	case pbTitle:
		body.WriteString(styleBold(accent, "建立 PBI — 標題") + "\n\n")
		body.WriteString(styleFg(muted, "專案 ") + m.pMapKey + "\n")
		body.WriteString(styleFg(muted, "Area ") + m.pAreaPath + "\n\n")
		body.WriteString(styleFg(muted, "輸入 PBI 標題。"))
	case pbResolve:
		body.WriteString(styleBold(accent, "建立 PBI") + "\n\n")
		body.WriteString(m.spin.View() + " " + styleFg(muted, "確認 Iteration…"))
	case pbIter:
		body.WriteString(styleBold(accent, "建立 PBI — 選 Iteration") + "\n\n")
		body.WriteString(styleFg(muted, "找不到當月 Iteration，選一個：") + "\n\n")
		body.WriteString(renderList(m.pbiIterItems(), m.pCursor))
	case pbConfirm:
		body.WriteString(styleBold(accent, "建立 PBI — 確認") + "\n\n")
		who := m.pUser
		if who == "" {
			who = "(未取得)"
		}
		body.WriteString(styleFg(muted, "標題      ") + m.pTitle + "\n")
		body.WriteString(styleFg(muted, "Area      ") + m.pAreaPath + "\n")
		body.WriteString(styleFg(muted, "Iteration ") + m.pIterPath + "\n")
		body.WriteString(styleFg(muted, "指派給    ") + who)
	case pbCreating:
		body.WriteString(styleBold(accent, "建立 PBI") + "\n\n")
		body.WriteString(m.spin.View() + " " + styleFg(muted, "建立中…"))
	case pbDone:
		body.WriteString(styleBold(accent, "建立 PBI") + "\n\n")
		body.WriteString(styleFg(okCol, fmt.Sprintf("✓ 已建立 PBI #%d", m.pCreatedID)) + "\n")
		body.WriteString(styleFg(dim, m.pURL))
	}

	if m.errMsg != "" {
		body.WriteString("\n\n" + styleFg(errCol, "⚠ "+m.errMsg))
	}

	content := strings.TrimRight(body.String(), "\n")
	if m.pstep == pbProject || m.pstep == pbArea || m.pstep == pbTitle || m.pstep == pbIter {
		content = m.input.View() + "\n\n" + content
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dim).
		Padding(0, 2).
		Render(content)

	hint := "↑↓ 選擇   ⏎ 確認   esc 取消"
	switch m.pstep {
	case pbTitle:
		hint = "⏎ 繼續   esc 取消"
	case pbConfirm:
		hint = "⏎ 建立   esc 取消"
	case pbDone:
		hint = "⏎ 返回"
	case pbResolve, pbCreating:
		hint = "請稍候…"
	}
	return m.banner() + "\n" + box + "\n" + m.hintbar(hint)
}
