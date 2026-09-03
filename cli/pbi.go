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
	pbBind     pbiStep = iota // (可選) 綁定目標單：Feature→parent / Release→related
	pbProject                 // pick project from mappings
	pbArea                    // fallback: pick Area when the mapping has none
	pbTitle                   // enter the PBI title
	pbDupCheck                // 查是否已有同名 PBI
	pbDupFound                // 有同名，讓使用者選：仍新建 / 取消
	pbResolve                 // resolve the current-month iteration
	pbIter                    // fallback: pick Iteration when the month is missing
	pbConfirm                 // show summary, Enter to create
	pbCreating                // creating…
	pbBinding                 // linking the new PBI to the target work item
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

type bindTargetMsg struct {
	id    int
	typ   string
	title string
	err   error
}

type bindDoneMsg struct{ err error }

// fetchBindTargetCmd 查要綁的目標單，回傳其 type/title 供判斷 parent/related。
func fetchBindTargetCmd(org string, id int) tea.Cmd {
	return func() tea.Msg {
		wi, err := showWorkItem(org, id)
		if err != nil {
			return bindTargetMsg{err: err}
		}
		return bindTargetMsg{id: wi.id, typ: wi.typ, title: wi.title}
	}
}

// addRelationCmd 把新 PBI 綁到目標單（parent = 掛在 Feature 下 / related = 關聯 Release）。
func addRelationCmd(org string, pbiID int, kind string, targetID int) tea.Cmd {
	return func() tea.Msg {
		_, err := run("az", "boards", "work-item", "relation", "add",
			"--id", strconv.Itoa(pbiID), "--relation-type", kind,
			"--target-id", strconv.Itoa(targetID), "--organization", org, "-o", "json")
		return bindDoneMsg{err: err}
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
	m.pstep = pbBind
	m.errMsg = ""
	m.pKeys = sortedMappingKeys(m.cfg.Mappings)
	m.pCursor = 0
	m.pMapKey, m.pAreaPath, m.pTitle, m.pIterPath, m.pUser = "", "", "", "", ""
	m.pCreatedID, m.pURL = 0, ""
	m.pBindID, m.pBindKind, m.pBindType, m.pBindTitle, m.pBindOK = 0, "", "", "", false
	m.input.SetValue("")
	m.input.Placeholder = "要綁的單 ID(可留空直接 Enter 跳過)"
	return whoAmICmd() // resolve assignee in the background
}

// enterPbiForParent 從導航器在某父單（Feature/Release）下建立 PBI：預帶綁定目標、
// 跳過 pbBind，建完/取消回導航器（navReturn）。
func (m *model) enterPbiForParent(parent workItem) tea.Cmd {
	if m.cfg.WorkItemProject == "" || len(m.cfg.Mappings) == 0 {
		m.errMsg = "config 缺 workItemProject 或專案對應，無法建立 PBI"
		return nil
	}
	m.mode = modePbi
	m.navReturn = true
	m.pstep = pbProject
	m.errMsg = ""
	m.pKeys = sortedMappingKeys(m.cfg.Mappings)
	m.pCursor = 0
	m.pMapKey, m.pAreaPath, m.pTitle, m.pIterPath, m.pUser = "", "", "", "", ""
	m.pCreatedID, m.pURL = 0, ""
	m.pBindID, m.pBindType, m.pBindTitle, m.pBindOK = parent.id, parent.typ, parent.title, false
	if strings.EqualFold(parent.typ, "Release") {
		m.pBindKind = "related"
	} else {
		m.pBindKind = "parent"
	}
	m.input.SetValue("")
	m.input.Placeholder = "篩選專案…"
	return whoAmICmd()
}

// pbiBackToNav 從 pbi 建立流程回到導航器層視圖，並把剛建/補綁的 PBI 加進可鑽清單。
func (m model) pbiBackToNav() model {
	m.navReturn = false
	m.mode = modeTask
	m.tstep = tkShow
	m.loading = false
	m.errMsg = ""
	m.input.SetValue("")
	m.input.Placeholder = ""
	if m.pCreatedID > 0 {
		// 要帶上剛綁好的父層與 Area/Iteration：少了父層，導航器會誤判「這張沒綁父層」
		// 而多跳一個綁定提示；少了 Area/Iteration，在這張底下建 Task 就沒東西可繼承。
		nw := workItem{
			id: m.pCreatedID, title: m.pTitle, typ: "Product Backlog Item",
			area: m.pAreaPath, iteration: m.pIterPath,
		}
		if m.pBindID > 0 && m.pBindOK {
			if m.pBindKind == "related" {
				nw.relatedID = m.pBindID // Release 走 related
			} else {
				nw.parentID = m.pBindID // Feature 走 parent
			}
			nw.parentTyp, nw.parentTitle = m.pBindType, m.pBindTitle
		}
		m.drillOthers = append(m.drillOthers, nw)
		m.taskCursor = len(m.drillOthers) - 1
	}
	return m
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

// pbiPhase 回傳目前在第幾個概念步驟（1..5）。Area / Iteration 的 fallback 併入
// 「選專案」「確認資料」，讓步驟表固定 5 步、不會忽多忽少。
type pbiRef struct {
	id       int
	title    string
	state    string
	assignee string
}

type dupPbiMsg struct {
	items []pbiRef
	err   error
}

// searchByTitleCmd 查同專案是否已有同型別、同標題（完全相同）的單，用來在建立前提醒重複。
func searchByTitleCmd(org, project, typ, title string) tea.Cmd {
	return func() tea.Msg {
		esc := strings.ReplaceAll(title, "'", "''") // WIQL 字串用兩個單引號跳脫
		wiql := "SELECT [System.Id],[System.Title],[System.State],[System.AssignedTo] FROM WorkItems" +
			" WHERE [System.TeamProject]='" + project + "'" +
			" AND [System.WorkItemType]='" + strings.ReplaceAll(typ, "'", "''") + "'" +
			" AND [System.Title]='" + esc + "'"
		out, err := run("az", "boards", "query", "--organization", org, "--project", project, "--wiql", wiql, "-o", "json")
		if err != nil {
			return dupPbiMsg{err: err}
		}
		var rows []struct {
			ID     int `json:"id"`
			Fields struct {
				Title    string `json:"System.Title"`
				State    string `json:"System.State"`
				Assigned struct {
					DisplayName string `json:"displayName"`
				} `json:"System.AssignedTo"`
			} `json:"fields"`
		}
		if json.Unmarshal([]byte(out), &rows) != nil {
			return dupPbiMsg{} // 解析不出＝當作沒重複，不擋流程
		}
		items := make([]pbiRef, 0, len(rows))
		for _, r := range rows {
			items = append(items, pbiRef{id: r.ID, title: r.Fields.Title, state: r.Fields.State, assignee: r.Fields.Assigned.DisplayName})
		}
		return dupPbiMsg{items: items}
	}
}

func (m model) pbiPhase() int {
	switch m.pstep {
	case pbBind:
		return 1
	case pbProject, pbArea:
		return 2
	case pbTitle, pbDupCheck, pbDupFound:
		return 3
	case pbResolve, pbIter, pbConfirm:
		return 4
	default: // pbCreating, pbBinding, pbDone
		return 5
	}
}

// pbiStepsView 列出 /pbi 的所有步驟。前面的查詢/選擇都只是讀取或本地暫存，
// 只有最後「建立（＋綁定）」會真的寫入 Azure。
func (m model) pbiStepsView() string {
	cur := m.pbiPhase()
	bindVal := ""
	if m.pBindID > 0 {
		bindVal = fmt.Sprintf("%s #%d（%s）", m.pBindType, m.pBindID, m.pBindKind)
	} else if cur > 1 {
		bindVal = "略過"
	}
	createLabel := "建立 PBI"
	if m.pBindID > 0 {
		createLabel = "建立 PBI ＋ 綁定"
	}
	return stepsView([]wizStep{
		{label: "綁定目標單（可選）", val: bindVal},
		{label: "選專案", val: m.pMapKey},
		{label: "輸入標題", val: m.pTitle},
		{label: "確認資料", val: m.pIterPath},
		{label: createLabel, mutates: true},
	}, cur)
}

func (m model) updatePbi(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() == "esc" && m.pstep != pbCreating && m.pstep != pbBinding {
		if m.navReturn {
			return m.pbiBackToNav(), nil
		}
		return m.pbiHome(), nil
	}

	switch m.pstep {
	case pbBind:
		if m.loading {
			return m, nil
		}
		if key.String() == "enter" {
			raw := strings.TrimSpace(m.input.Value())
			if raw == "" {
				m.pBindID, m.pBindKind = 0, ""
				m.errMsg = ""
				m.pstep = pbProject
				m.input.SetValue("")
				m.input.Placeholder = "篩選專案…"
				return m, nil
			}
			id, err := strconv.Atoi(raw)
			if err != nil || id <= 0 {
				m.errMsg = "請輸入數字工作項 ID，或留空 Enter 跳過"
				return m, nil
			}
			m.loading = true
			m.errMsg = ""
			return m, fetchBindTargetCmd(m.cfg.AzureOrg, id)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return m, cmd
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
			m.pstep = pbDupCheck
			m.loading = true
			return m, searchByTitleCmd(m.cfg.AzureOrg, m.cfg.WorkItemProject, "Product Backlog Item", t)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return m, cmd

	case pbDupFound:
		if m.pBindID > 0 {
			// 有父單：每張現有 PBI 可補綁到父單，最後一項＝仍要新建
			n := len(m.pDups) + 1
			switch key.String() {
			case "up":
				m.pDupCursor = (m.pDupCursor - 1 + n) % n
			case "down":
				m.pDupCursor = (m.pDupCursor + 1) % n
			case "enter":
				if m.pDupCursor < len(m.pDups) {
					dup := m.pDups[m.pDupCursor]
					m.pCreatedID = dup.id
					m.pURL = strings.TrimRight(m.cfg.AzureOrg, "/") + "/" + m.cfg.WorkItemProject + "/_workitems/edit/" + strconv.Itoa(dup.id)
					m.pstep = pbBinding
					m.loading = true
					m.errMsg = ""
					return m, addRelationCmd(m.cfg.AzureOrg, dup.id, m.pBindKind, m.pBindID)
				}
				m.pstep = pbResolve // 仍要新建
				m.loading = true
				return m, listIterationsCmd(m.cfg.AzureOrg, m.cfg.WorkItemProject)
			}
			return m, nil
		}
		// 無父單：仍新建 / 取消
		switch key.String() {
		case "up":
			m.pDupCursor = (m.pDupCursor - 1 + 2) % 2
		case "down":
			m.pDupCursor = (m.pDupCursor + 1) % 2
		case "enter":
			if m.pDupCursor == 0 {
				m.pstep = pbResolve
				m.loading = true
				return m, listIterationsCmd(m.cfg.AzureOrg, m.cfg.WorkItemProject)
			}
			return m.pbiHome(), nil
		}
		return m, nil

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
			if m.navReturn {
				return m.pbiBackToNav(), nil
			}
			return m.pbiHome(), nil
		}
		return m, nil
	}
	return m, nil
}

func (m model) viewPbi() string {
	var body strings.Builder

	switch m.pstep {
	case pbBind:
		body.WriteString(styleBold(accent, "建立 PBI — 綁定目標單（可選）") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "查詢目標單…"))
		} else {
			body.WriteString(styleFg(muted, "輸入要綁的單 ID：Feature 用 parent、Release 用 related 綁；留空 Enter 跳過。"))
		}
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
	case pbDupCheck:
		body.WriteString(styleBold(accent, "建立 PBI — 查重") + "\n\n")
		body.WriteString(m.spin.View() + " " + styleFg(muted, "查詢是否已有同名 PBI…"))
	case pbDupFound:
		body.WriteString(styleBold(accent, "已有同名 PBI") + "\n\n")
		body.WriteString(styleFg(muted, "「"+m.pTitle+"」已經存在下列 PBI：") + "\n\n")
		if m.pBindID > 0 {
			var items []string
			for _, d := range m.pDups {
				meta := d.state
				if d.assignee != "" {
					meta += " · " + d.assignee
				}
				items = append(items, "綁 #"+strconv.Itoa(d.id)+" "+d.title+"（"+meta+"）到 "+m.pBindType+" #"+strconv.Itoa(m.pBindID))
			}
			items = append(items, "仍要新建一張")
			body.WriteString(renderList(items, m.pDupCursor))
		} else {
			for _, d := range m.pDups {
				meta := d.state
				if d.assignee != "" {
					meta += " · " + d.assignee
				}
				body.WriteString("  " + styleFg(accent, "#"+strconv.Itoa(d.id)) + " " + d.title + "  " + styleFg(dim, meta) + "\n")
			}
			body.WriteString("\n")
			body.WriteString(renderList([]string{"仍要新建一張", "取消（去用上面現有的）"}, m.pDupCursor))
		}
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
		body.WriteString("\n\n" + styleFg(errCol, "⚠ 按 Enter 會實際建立 PBI"))
		if m.pBindID > 0 {
			body.WriteString(styleFg(errCol, "，並綁定 "+m.pBindType+" #"+strconv.Itoa(m.pBindID)))
		}
	case pbCreating:
		body.WriteString(styleBold(accent, "建立 PBI") + "\n\n")
		body.WriteString(m.spin.View() + " " + styleFg(muted, "建立中…"))
	case pbBinding:
		body.WriteString(styleBold(accent, "建立 PBI") + "\n\n")
		body.WriteString(m.spin.View() + " " + styleFg(muted, fmt.Sprintf("綁定到 %s #%d…", m.pBindType, m.pBindID)))
	case pbDone:
		body.WriteString(styleBold(accent, "建立 PBI") + "\n\n")
		body.WriteString(styleFg(okCol, fmt.Sprintf("✓ 已建立 PBI #%d", m.pCreatedID)) + "\n")
		body.WriteString(styleFg(dim, m.pURL))
	}

	if m.errMsg != "" {
		body.WriteString("\n\n" + styleFg(errCol, "⚠ "+m.errMsg))
	}

	content := strings.TrimRight(body.String(), "\n")
	if m.pstep == pbBind || m.pstep == pbProject || m.pstep == pbArea || m.pstep == pbTitle || m.pstep == pbIter {
		content = m.input.View() + "\n\n" + content
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dim).
		Padding(0, 2).
		Render(content)

	hint := "↑↓ 選擇   ⏎ 確認   esc 取消"
	switch m.pstep {
	case pbBind:
		hint = "⏎ 綁定/跳過   esc 取消"
	case pbTitle:
		hint = "⏎ 繼續   esc 取消"
	case pbConfirm:
		hint = "⏎ 建立   esc 取消"
	case pbDone:
		hint = "⏎ 返回"
	case pbResolve, pbCreating, pbBinding, pbDupCheck:
		hint = "請稍候…"
	}
	return m.banner() + m.pbiStepsView() + "\n" + box + "\n" + m.hintbar(hint)
}
