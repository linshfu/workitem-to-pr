package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// version is injected at build time via -ldflags "-X main.version=<tag>".
// It must match the release's Git tag so the update check compares correctly.
var version = "dev"

const homePlaceholder = ""
const manualOrgLabel = "（手動輸入 URL…）"

// ---- commands shown in the palette ----
type command struct{ name, desc string }

var commands = []command{
	{"/init", "初始化精靈:環境檢查、az 探索、產生 config"},
	{"/task", "處理工作項:建分支、建 PR、Slack 通知 reviewer"},
	{"/pbi", "建立 PBI(自動帶 Area / Iteration / 指派自己)"},
	{"/release", "建立 Release PR(目標 master 與 develop)"},
	{"/hotfix", "Hotfix:開 hotfix 分支、改版號、回 PR"},
	{"/update", "更新到最新版(下載並自我替換)"},
	{"/help", "顯示常用指令與參數"},
}

// ---- modes / steps ----
type mode int

const (
	modeHome mode = iota
	modeInit
	modeTask
	modeUpdate
	modePbi
)

type taskStep int

const (
	tkInput     taskStep = iota // enter work item id
	tkShow                      // show work item + child tasks (multi-select)
	tkNewTask                   // enter title(s) for new task(s) under the PBI
	tkBranch                    // resolve project/repo + confirm branch
	tkPath                      // no local path -> clone or enter existing
	tkPathInput                 // input the path / clone target dir
	tkCommit                    // wait for commits (PR description)
	tkReviewer                  // pick a reviewer (or skip)
	tkPRCreating                // creating PR + reviewer + Slack
	tkPRDone                    // PR created, show url + Slack status
)

type initStep int

const (
	stEnv initStep = iota
	stOrg
	stMode
	stWIP
	stArea
	stTag
	stCodeProj
	stCodeRepo
	stMore
	stReuse // menu shown when re-running init over an existing config
	stSlackAsk
	stSlackToken
	stSlackMode // pick existing channel vs create a new one
	stSlackPick // pick an existing channel
	stSlackNew  // enter a new channel name
	stConfirm
	stDone
)

// identMode: how work items are identified as belonging to a project.
type identMode int

const (
	identArea       identMode = iota // Area Path (+ title tag) — centralized tickets
	identTag                         // title [Tag] only — centralized tickets
	identPerProject                  // each product is its own project
)

var modeLabels = []string{
	"Area Path + 標題標籤（單集中在一個專案，用 Area 分）",
	"只用標題標籤 [Tag]（單集中，但沒用 Area）",
	"每個專案各自開單（單就在該專案裡）",
}

// ---- model ----
type model struct {
	mode     mode
	width    int
	input    textinput.Model
	spin     spinner.Model
	status   string
	quitting bool
	boot     tea.Cmd // fired from Init(); first-run drops straight into /init

	// self-update (on-demand, via /update)
	latestVer string
	upStatus  string
	upErr     string
	upDone    bool
	upToDate  bool

	// /pbi flow
	pstep      pbiStep
	pKeys      []string
	pCursor    int
	pMapKey    string
	pMapping   mappingCfg
	pAreaPath  string
	pTitle     string
	pAreaList  []areaInfo
	pIterPath  string
	pIterList  []string
	pUser      string
	pCreatedID int
	pURL       string

	// home palette
	cmdMatches []command
	cmdCursor  int

	// init wizard
	step       initStep
	loading    bool
	envResults []checkResult
	azOK       bool
	org        string
	orgs       []string
	orgCursor  int
	orgManual  bool

	identMode  identMode
	modeCursor int

	wip        string // workItemProject (where tickets live)
	areas      []areaInfo
	areaCursor int

	projects   []string
	projCursor int
	project    string // selected code project
	repos      []repoInfo
	repoCursor int
	repo       repoInfo

	// current mapping being built
	curArea  areaInfo
	curAlias string
	curKey   string

	mappings   []mappingEntry
	moreCursor int

	writtenPath string
	errMsg      string

	// slack setup (in /init)
	slackAskCursor  int
	slackModeCursor int
	slackToken      string
	slackChannel    string
	slackChannels   []slackChannel
	slackChanCursor int
	slackMembers    []slackMember
	slackDone       bool
	slackSkipped    bool
	manifestCopied  bool

	// re-running /init over an existing config
	reconfig      bool // an existing config was loaded
	returnToReuse bool // a sub-step was entered from the reuse menu; return to it
	reuseCursor   int

	// config + task flow
	cfg        config
	cfgOK      bool
	tstep      taskStep
	wi         workItem
	children   []workItem
	taskCursor int

	// task multi-select + new tasks
	taskSel    map[int]bool // task id -> selected
	selOrder   []int        // selection order; [0] = primary (branch naming)
	newIDs     map[int]bool // which children were newly created (for the label)
	allTaskIDs []int        // finalized selected ids, carried for the PR to link all

	// branch step
	selTask      workItem
	mapKey       string
	mapping      mappingCfg
	baseBranch   string
	branchName   string
	branchReuse  string // existing branch to reuse (empty = create new)
	baseObjectID string

	// local path + commit check
	localPath   string
	cloneMode   bool
	pathCursor  int
	commits     string
	commitCount int // -1 = branch not pushed, 0 = no new commits, >0 = commits

	// PR + reviewer + Slack
	reviewers []reviewer
	revCursor int
	chosenRev reviewer
	prID      int
	prURL     string
	prResult  string
	revNote   string
	slackMsg  string
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = homePlaceholder
	ti.Prompt = "> "
	ti.Focus()
	ti.CharLimit = 200
	ti.Width = 60

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(accent)

	cleanupOldBinary() // remove leftovers from a prior /update
	cfg, cfgOK := loadConfig()

	m := model{
		mode:       modeHome,
		input:      ti,
		spin:       sp,
		cmdMatches: commands,
		cfg:        cfg,
		cfgOK:      cfgOK,
	}
	if !cfgOK {
		// fresh install, no config yet -> walk straight into setup
		m.boot = m.enterInit()
	}
	return m
}

func (m model) Init() tea.Cmd { return tea.Batch(textinput.Blink, m.spin.Tick, m.boot) }

func (m *model) refreshMatches() {
	q := strings.TrimSpace(m.input.Value())
	if i := strings.IndexByte(q, ' '); i >= 0 {
		q = q[:i] // match on the command token, ignore any args after a space
	}
	q = strings.TrimPrefix(q, "/")
	var matches []command
	for _, c := range commands {
		if q == "" || fuzzyMatch(q, strings.TrimPrefix(c.name, "/")) {
			matches = append(matches, c)
		}
	}
	m.cmdMatches = matches
	if m.cmdCursor >= len(matches) {
		m.cmdCursor = 0
	}
}

func splitFirst(s string) (string, string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

func resolveCommand(tok string) string {
	t := "/" + strings.TrimPrefix(strings.TrimSpace(tok), "/")
	for _, c := range commands {
		if strings.EqualFold(c.name, t) {
			return c.name
		}
	}
	return ""
}

// ---- update ----
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.input.Width = max(20, msg.Width-8)
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case envMsg:
		m.loading = false
		m.envResults = msg.results
		m.azOK = msg.azOK
		if msg.azOK && allOK(msg.results) {
			if m.reconfig {
				// existing config -> reuse menu instead of a forced full redo
				m.step = stReuse
				m.reuseCursor = 0
				m.input.SetValue("")
				m.input.Placeholder = ""
				return m, nil
			}
			// no problems -> skip the Enter, go straight to org discovery
			m.step = stOrg
			m.loading = true
			m.orgManual = false
			m.orgCursor = 0
			m.input.SetValue("")
			m.input.Placeholder = "輸入關鍵字篩選組織…"
			return m, listOrgsCmd()
		}
		return m, nil
	case orgDefaultMsg:
		if msg.org != "" && m.org == "" {
			m.org = msg.org
		}
		return m, nil
	case orgsMsg:
		m.loading = false
		if msg.err != nil || len(msg.orgs) == 0 {
			// couldn't list orgs -> fall back to manual URL entry
			m.orgManual = true
			m.orgs = nil
			m.input.SetValue(m.org) // prefill the az devops default if we have one
			m.input.Placeholder = "https://dev.azure.com/your-org"
			if msg.err != nil {
				m.errMsg = "列組織失敗:" + msg.err.Error()
			}
		} else if len(msg.orgs) == 1 {
			// only one org -> pick it and continue automatically
			m.orgs = msg.orgs
			m.org = "https://dev.azure.com/" + msg.orgs[0]
			m.errMsg = ""
			m.gotoMode()
			return m, nil
		} else {
			m.orgs = msg.orgs
			m.orgCursor = 0
			m.orgManual = false
			m.errMsg = ""
		}
		return m, nil
	case projectsMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = "取得專案失敗:" + msg.err.Error()
		} else {
			m.projects, m.projCursor, m.errMsg = msg.projects, 0, ""
		}
		return m, nil
	case reposMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = "取得儲存庫失敗:" + msg.err.Error()
		} else {
			m.repos, m.repoCursor, m.errMsg = msg.repos, 0, ""
		}
		return m, nil
	case areasMsg:
		m.loading = false
		if m.mode == modePbi {
			if msg.err != nil {
				m.errMsg = "取得 Area 失敗:" + msg.err.Error()
			} else {
				m.pAreaList, m.pCursor, m.errMsg = msg.areas, 0, ""
			}
			return m, nil
		}
		if msg.err != nil {
			m.errMsg = "取得 Area 失敗:" + msg.err.Error()
		} else {
			m.areas, m.areaCursor, m.errMsg = msg.areas, 0, ""
		}
		return m, nil
	case whoAmIMsg:
		if msg.err == nil {
			m.pUser = msg.name
		}
		return m, nil
	case slackAuthMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = "Slack token 無效:" + msg.err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.step = stSlackMode
		m.slackModeCursor = 0
		m.input.EchoMode = textinput.EchoNormal
		m.input.SetValue("")
		m.input.Placeholder = ""
		return m, nil
	case channelsMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = "取得頻道失敗:" + msg.err.Error()
			return m, nil
		}
		m.slackChannels, m.slackChanCursor, m.errMsg = msg.channels, 0, ""
		return m, nil
	case manifestMsg:
		m.manifestCopied = msg.ok
		return m, nil
	case slackMembersMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = "抓頻道成員失敗:" + msg.err.Error()
			return m, nil
		}
		m.slackMembers, m.slackDone, m.slackSkipped, m.errMsg = msg.members, true, false, ""
		if m.returnToReuse {
			m.returnToReuse = false
			m.step = stReuse
		} else {
			m.step = stConfirm
		}
		m.input.SetValue("")
		m.input.Placeholder = ""
		return m, nil
	case iterationsMsg:
		if m.mode != modePbi {
			return m, nil
		}
		m.loading = false
		target := m.computeIterPath()
		if msg.err != nil {
			// can't verify -> use the computed current-month path anyway
			m.pIterPath, m.pstep = target, pbConfirm
			return m, nil
		}
		m.pIterList = msg.iters
		if containsStr(msg.iters, target) {
			m.pIterPath, m.pstep = target, pbConfirm
			return m, nil
		}
		m.pstep, m.pCursor = pbIter, 0
		m.input.SetValue("")
		m.input.Placeholder = "篩選 Iteration…"
		return m, nil
	case pbiCreatedMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg, m.pstep = msg.err.Error(), pbConfirm
			return m, nil
		}
		m.pCreatedID, m.pURL, m.pstep, m.errMsg = msg.id, msg.url, pbDone, ""
		return m, nil
	case workItemMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = "取得工作項失敗:" + msg.err.Error()
			return m, nil
		}
		m.wi, m.children, m.taskCursor, m.errMsg = msg.wi, msg.children, 0, ""
		if strings.EqualFold(m.wi.typ, "Task") {
			// the id is itself a Task -> skip child selection, go straight to branch
			m.allTaskIDs = []int{m.wi.id}
			return m.toBranch(m.wi)
		}
		return m, nil
	case taskCreatedMsg:
		m.loading = false
		if len(msg.created) == 0 {
			m.errMsg = "Task 建立失敗"
			m.tstep = tkShow
			return m, nil
		}
		for _, t := range msg.created {
			m.children = append(m.children, t)
			if m.newIDs == nil {
				m.newIDs = map[int]bool{}
			}
			m.newIDs[t.id] = true
			m.toggleTask(t.id) // auto-select newly created tasks
		}
		if len(msg.failed) > 0 {
			m.errMsg = fmt.Sprintf("部分建立失敗(%d)：%s", len(msg.failed), strings.Join(msg.failed, ", "))
		} else {
			m.errMsg = ""
		}
		m.tstep = tkShow
		m.taskCursor = len(m.children) + 1 // land on the "繼續" row
		m.input.SetValue("")
		return m, nil
	case reviewersMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = "取得 reviewer 失敗:" + msg.err.Error()
			m.reviewers = nil
			return m, nil
		}
		m.reviewers, m.revCursor, m.errMsg = msg.list, 0, ""
		return m, nil
	case prCreatedMsg:
		if msg.err != nil {
			m.loading = false
			m.errMsg = "建立 PR 失敗:" + msg.err.Error()
			m.tstep = tkReviewer // let them pick again / retry
			return m, nil
		}
		m.prID, m.prURL, m.errMsg = msg.id, msg.url, ""
		m.prResult = prResultText(m.mapping.AzureProject, m.baseBranch, msg.url, msg.title, msg.id)
		switch {
		case msg.reviewerErr != "":
			m.revNote = "reviewer 未加成功:" + msg.reviewerErr
		case m.chosenRev.email != "":
			m.revNote = "必要 reviewer:" + reviewerLabel(m.chosenRev) + "（已設 auto-complete）"
		default:
			m.revNote = "未加 reviewer"
		}
		if m.slackConfigured() && m.chosenRev.slackID != "" {
			m.slackMsg = "通知 Slack 中…"
			return m, tea.Batch(openURLCmd(m.prURL),
				slackNotifyCmd(m.cfg.SlackToken, m.cfg.Slack.Channel, m.chosenRev.slackID, m.prResult))
		}
		m.loading = false
		if !m.slackConfigured() {
			m.slackMsg = "Slack 未設定，略過通知"
		} else {
			m.slackMsg = "reviewer 無 Slack 對應，略過通知"
		}
		m.tstep = tkPRDone
		return m, openURLCmd(m.prURL)
	case slackDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.slackMsg = "Slack 通知失敗:" + msg.err.Error()
		} else {
			m.slackMsg = "已通知 Slack #" + m.cfg.Slack.Channel
		}
		m.tstep = tkPRDone
		return m, nil
	case refsMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = "取得分支清單失敗:" + msg.err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.branchReuse = ""
		m.baseObjectID = ""
		tag := strconv.Itoa(m.selTask.id)
		for _, r := range msg.refs {
			name := strings.TrimPrefix(r.name, "refs/heads/")
			if m.branchReuse == "" && strings.Contains(name, tag) && !strings.HasPrefix(name, "release/") {
				m.branchReuse = name
			}
			if strings.EqualFold(r.name, "refs/heads/"+m.baseBranch) {
				m.baseObjectID = r.objectID
			}
		}
		return m, nil
	case branchMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = "建立分支失敗:" + msg.err.Error()
			return m, nil
		}
		m.branchName = msg.branch
		m.errMsg = ""
		return m.afterBranch()
	case commitsMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		} else if msg.missing {
			m.commits, m.commitCount, m.errMsg = "", -1, ""
		} else {
			m.commits, m.commitCount, m.errMsg = msg.commits, msg.count, ""
		}
		return m, nil
	case cloneMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = "clone 失敗:" + msg.err.Error()
			return m, nil
		}
		m.localPath, m.errMsg = msg.path, ""
		m.tstep = tkCommit
		m.loading = true
		return m, commitCheckCmd(m.localPath, m.baseBranch, m.branchName)
	case writtenMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = "寫入失敗:" + msg.err.Error()
		} else {
			m.writtenPath, m.step, m.errMsg = msg.path, stDone, ""
		}
		return m, nil
	case updateMsg:
		if m.mode != modeUpdate {
			return m, nil
		}
		if msg.latest == "" {
			m.loading = false
			m.upErr = "查不到最新版本(可能離線或 GitHub 限流)"
			return m, nil
		}
		m.latestVer = msg.latest
		if msg.latest == version {
			m.loading, m.upDone, m.upToDate = false, true, true
			return m, nil
		}
		m.upStatus = "發現新版 " + msg.latest + "，下載更新中…"
		return m, doUpdateCmd()
	case updateDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.upErr = msg.err.Error()
		} else {
			m.upDone = true
		}
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
		if m.mode == modeInit {
			return m.updateInit(msg)
		}
		if m.mode == modeTask {
			return m.updateTask(msg)
		}
		if m.mode == modeUpdate {
			return m.updateUpdate(msg)
		}
		if m.mode == modePbi {
			return m.updatePbi(msg)
		}
		return m.updateHome(msg)
	}
	// unhandled (e.g. cursor blink) -> let the text input process it
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) updateHome(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.quitting = true
		return m, tea.Quit
	case "up":
		if n := len(m.cmdMatches); n > 0 {
			m.cmdCursor = (m.cmdCursor - 1 + n) % n
		}
		return m, nil
	case "down":
		if n := len(m.cmdMatches); n > 0 {
			m.cmdCursor = (m.cmdCursor + 1) % n
		}
		return m, nil
	case "tab":
		// autocomplete the highlighted command into the input, ready for args
		if len(m.cmdMatches) > 0 {
			m.input.SetValue(m.cmdMatches[m.cmdCursor].name + " ")
			m.refreshMatches()
		}
		return m, nil
	case "enter":
		raw := strings.TrimSpace(m.input.Value())
		if id, err := strconv.Atoi(raw); err == nil && id > 0 {
			// bare work item id -> task flow (like `vl 35015`)
			m.input.SetValue("")
			m.refreshMatches()
			return m.startTask(raw)
		}
		tok, arg := splitFirst(raw)
		name := resolveCommand(tok) // exact "/task" etc, honoring any typed arg
		if name == "" && len(m.cmdMatches) > 0 {
			name = m.cmdMatches[m.cmdCursor].name // else run the highlighted one
		}
		if name != "" {
			m.input.SetValue("")
			m.refreshMatches()
			return m.launch(name, arg)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	m.refreshMatches()
	return m, cmd
}

func (m model) startTask(idStr string) (tea.Model, tea.Cmd) {
	if !m.cfgOK {
		m.status = "找不到設定，請先跑 /init"
		return m, nil
	}
	id, err := strconv.Atoi(strings.TrimSpace(idStr))
	if err != nil || id <= 0 {
		m.status = "工作項 ID 需為數字"
		return m, nil
	}
	m.mode = modeTask
	m.tstep = tkShow
	m.loading = true
	m.errMsg = ""
	m.wi = workItem{}
	m.children = nil
	m.taskCursor = 0
	m.taskSel = map[int]bool{}
	m.selOrder = nil
	m.newIDs = map[int]bool{}
	m.allTaskIDs = nil
	m.reviewers, m.revCursor, m.chosenRev = nil, 0, reviewer{}
	m.prID, m.prURL, m.prResult, m.revNote, m.slackMsg = 0, "", "", "", ""
	m.input.SetValue("")
	m.input.Placeholder = ""
	return m, fetchWorkItemCmd(m.cfg.AzureOrg, id)
}

// enterInit resets wizard state and starts the environment check. Shared by the
// /init command and by first-run (no config yet), so a freshly installed binary
// drops straight into setup instead of showing an empty home screen.
func (m *model) enterInit() tea.Cmd {
	m.mode = modeInit
	m.step = stEnv
	m.loading = true
	m.errMsg = ""
	m.status = ""
	m.envResults = nil
	// reset wizard state for a clean run
	m.org, m.orgs, m.orgManual = "", nil, false
	m.wip = ""
	m.areas, m.projects, m.repos = nil, nil, nil
	m.mappings = nil
	m.curArea, m.curAlias, m.curKey = areaInfo{}, "", ""
	m.project = ""
	m.slackAskCursor, m.slackToken, m.slackChannel = 0, "", ""
	m.slackMembers, m.slackDone, m.slackSkipped = nil, false, false
	m.reconfig, m.returnToReuse, m.reuseCursor = false, false, 0
	// re-running over an existing config: pre-load it so nothing is lost and the
	// user only changes what they want (a reuse menu, not a forced full redo).
	if m.cfgOK {
		m.reconfig = true
		m.org = m.cfg.AzureOrg
		m.wip = m.cfg.WorkItemProject
		m.mappings = existingMappingsAsEntries(m.cfg)
		m.identMode = inferIdentMode(m.mappings)
		m.slackChannel = m.cfg.Slack.Channel
		m.slackMembers = m.cfg.Slack.Members
		m.slackToken = m.cfg.SlackToken
		m.slackDone = m.cfg.Slack.Channel != ""
	}
	m.input.SetValue("")
	m.input.EchoMode = textinput.EchoNormal
	m.input.Placeholder = "環境檢查中…"
	return checkEnvCmd()
}

func existingMappingsAsEntries(c config) []mappingEntry {
	var out []mappingEntry
	for _, k := range sortedMappingKeys(c.Mappings) {
		mp := c.Mappings[k]
		alias := ""
		if len(mp.Aliases) > 0 {
			alias = mp.Aliases[0]
		}
		out = append(out, mappingEntry{
			key: k, project: mp.AzureProject, repo: mp.AzureRepository,
			branch: mp.DefaultBranch, areaPath: mp.AreaPath, alias: alias,
		})
	}
	return out
}

func (m model) visibleChannels() []slackChannel {
	q := strings.TrimSpace(m.input.Value())
	if q == "" {
		return m.slackChannels
	}
	var out []slackChannel
	for _, c := range m.slackChannels {
		if fuzzyMatch(q, c.name) {
			out = append(out, c)
		}
	}
	return out
}

// mappedProjects is the unique Azure code projects from the mappings — the teams
// to seed a new Slack channel from.
func (m model) mappedProjects() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range m.mappings {
		if e.project != "" && !seen[e.project] {
			seen[e.project] = true
			out = append(out, e.project)
		}
	}
	return out
}

func inferIdentMode(entries []mappingEntry) identMode {
	for _, e := range entries {
		if e.areaPath != "" {
			return identArea
		}
	}
	for _, e := range entries {
		if e.alias != "" {
			return identTag
		}
	}
	return identArea
}

func (m model) launch(name, arg string) (tea.Model, tea.Cmd) {
	switch name {
	case "/init":
		return m, m.enterInit()

	case "/update":
		return m, m.enterUpdate()

	case "/pbi":
		return m, m.enterPbi()

	case "/task":
		if !m.cfgOK {
			m.status = "找不到設定，請先跑 /init（或確認 config.json 存在）"
			return m, nil
		}
		if id, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil && id > 0 {
			return m.startTask(arg) // /task 35744
		}
		m.mode = modeTask
		m.tstep = tkInput
		m.wi = workItem{}
		m.children = nil
		m.taskCursor = 0
		m.errMsg = ""
		m.input.SetValue("")
		m.input.Placeholder = "輸入工作項 ID，例如 35015"
		return m, nil
	}
	m.status = name + " 還沒接(目前只做 /init、/task)"
	return m, nil
}

func (m model) updateInit(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() == "esc" {
		m.mode = modeHome
		m.loading = false
		m.errMsg = ""
		m.input.SetValue("")
		m.input.EchoMode = textinput.EchoNormal
		m.input.Placeholder = homePlaceholder
		return m, nil
	}

	switch m.step {
	case stEnv:
		if key.String() == "enter" && !m.loading && m.azOK {
			if m.reconfig {
				m.step = stReuse
				m.reuseCursor = 0
				m.input.SetValue("")
				m.input.Placeholder = ""
				return m, nil
			}
			m.step = stOrg
			m.loading = true
			m.orgManual = false
			m.orgCursor = 0
			m.input.SetValue("")
			m.input.Placeholder = "篩選組織…"
			return m, listOrgsCmd()
		}
		return m, nil

	case stReuse:
		rows := 4 // wip / mappings / slack / done
		switch key.String() {
		case "up":
			m.reuseCursor = (m.reuseCursor - 1 + rows) % rows
		case "down":
			m.reuseCursor = (m.reuseCursor + 1) % rows
		case "enter":
			switch m.reuseCursor {
			case 0: // 工作項專案
				m.returnToReuse = true
				return m, m.gotoWIP()
			case 1: // 專案對應
				m.returnToReuse = true
				m.step = stMore
				m.moreCursor = 0
				m.input.SetValue("")
				m.input.Placeholder = ""
			case 2: // Slack
				m.returnToReuse = true
				m.step = stSlackAsk
				m.slackAskCursor = 0
				m.input.SetValue("")
				m.input.Placeholder = ""
			case 3: // 完成，寫入
				m.step = stConfirm
				m.input.SetValue("")
				m.input.Placeholder = ""
			}
		}
		return m, nil

	case stOrg:
		if m.loading {
			return m, nil
		}
		if m.orgManual {
			if key.String() == "enter" {
				v := strings.TrimSpace(m.input.Value())
				if v == "" {
					return m, nil
				}
				m.org = v
				m.gotoMode()
				return m, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(key)
			return m, cmd
		}
		items := m.orgItems()
		switch key.String() {
		case "up":
			if n := len(items); n > 0 {
				m.orgCursor = (m.orgCursor - 1 + n) % n
			}
			return m, nil
		case "down":
			if n := len(items); n > 0 {
				m.orgCursor = (m.orgCursor + 1) % n
			}
			return m, nil
		case "enter":
			if len(items) == 0 {
				return m, nil
			}
			sel := items[m.orgCursor]
			if sel == manualOrgLabel {
				m.orgManual = true
				m.input.SetValue("")
				m.input.Placeholder = "https://dev.azure.com/your-org"
				return m, nil
			}
			m.org = "https://dev.azure.com/" + sel
			m.gotoMode()
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		if n := len(m.orgItems()); n == 0 || m.orgCursor >= n {
			m.orgCursor = 0
		}
		return m, cmd

	case stMode:
		switch key.String() {
		case "up":
			m.modeCursor = (m.modeCursor - 1 + len(modeLabels)) % len(modeLabels)
		case "down":
			m.modeCursor = (m.modeCursor + 1) % len(modeLabels)
		case "enter":
			m.identMode = identMode(m.modeCursor)
			if m.identMode == identPerProject {
				cmd := m.gotoCodeProj("")
				return m, cmd
			}
			cmd := m.gotoWIP()
			return m, cmd
		}
		return m, nil

	case stWIP:
		if m.loading {
			return m, nil
		}
		vis := m.visibleProjects()
		switch key.String() {
		case "up":
			if n := len(vis); n > 0 {
				m.projCursor = (m.projCursor - 1 + n) % n
			}
			return m, nil
		case "down":
			if n := len(vis); n > 0 {
				m.projCursor = (m.projCursor + 1) % n
			}
			return m, nil
		case "enter":
			if len(vis) > 0 {
				m.wip = vis[m.projCursor]
				if m.returnToReuse {
					m.returnToReuse = false
					m.step = stReuse
					m.input.SetValue("")
					m.input.Placeholder = ""
					return m, nil
				}
				if m.identMode == identArea {
					cmd := m.gotoCodeProj("")
					return m, cmd
				}
				m.gotoTag()
				return m, nil
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		if n := len(m.visibleProjects()); n == 0 || m.projCursor >= n {
			m.projCursor = 0
		}
		return m, cmd

	case stArea:
		if m.loading {
			return m, nil
		}
		vis := m.visibleAreas()
		switch key.String() {
		case "up":
			if n := len(vis); n > 0 {
				m.areaCursor = (m.areaCursor - 1 + n) % n
			}
			return m, nil
		case "down":
			if n := len(vis); n > 0 {
				m.areaCursor = (m.areaCursor + 1) % n
			}
			return m, nil
		case "enter":
			if len(vis) > 0 {
				m.curArea = vis[m.areaCursor]
				m.curAlias = m.curArea.name
				m.curKey = sanitizeKey(m.curArea.name)
				cmd := m.gotoCodeRepo()
				return m, cmd
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		if n := len(m.visibleAreas()); n == 0 || m.areaCursor >= n {
			m.areaCursor = 0
		}
		return m, cmd

	case stTag:
		if key.String() == "enter" {
			v := strings.TrimSpace(m.input.Value())
			if v == "" {
				return m, nil
			}
			m.curArea = areaInfo{}
			m.curAlias = v
			m.curKey = sanitizeKey(v)
			cmd := m.gotoCodeProj(v)
			return m, cmd
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return m, cmd

	case stCodeProj:
		if m.loading {
			return m, nil
		}
		vis := m.visibleProjects()
		switch key.String() {
		case "up":
			if n := len(vis); n > 0 {
				m.projCursor = (m.projCursor - 1 + n) % n
			}
			return m, nil
		case "down":
			if n := len(vis); n > 0 {
				m.projCursor = (m.projCursor + 1) % n
			}
			return m, nil
		case "enter":
			if len(vis) > 0 {
				m.project = vis[m.projCursor]
				if m.identMode == identArea {
					// project first -> now pick its Area (pre-filtered by project name)
					cmd := m.gotoArea()
					return m, cmd
				}
				if m.identMode == identPerProject {
					m.wip = m.project
					m.curKey = sanitizeKey(m.project)
					m.curAlias = ""
					m.curArea = areaInfo{}
				}
				cmd := m.gotoCodeRepo()
				return m, cmd
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		if n := len(m.visibleProjects()); n == 0 || m.projCursor >= n {
			m.projCursor = 0
		}
		return m, cmd

	case stCodeRepo:
		if m.loading {
			return m, nil
		}
		vis := m.visibleRepos()
		switch key.String() {
		case "up":
			if n := len(vis); n > 0 {
				m.repoCursor = (m.repoCursor - 1 + n) % n
			}
			return m, nil
		case "down":
			if n := len(vis); n > 0 {
				m.repoCursor = (m.repoCursor + 1) % n
			}
			return m, nil
		case "enter":
			if len(vis) > 0 {
				m.repo = vis[m.repoCursor]
				m.finalizeMapping()
				m.gotoMore()
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		if n := len(m.visibleRepos()); n == 0 || m.repoCursor >= n {
			m.repoCursor = 0
		}
		return m, cmd

	case stMore:
		switch key.String() {
		case "up", "down":
			m.moreCursor = (m.moreCursor + 1) % 2
		case "enter":
			if m.moreCursor == 0 {
				if m.identMode == identTag {
					m.gotoTag()
					return m, nil
				}
				cmd := m.gotoCodeProj("")
				return m, cmd
			}
			if m.returnToReuse {
				m.returnToReuse = false
				m.step = stReuse
			} else {
				m.step = stSlackAsk
				m.slackAskCursor = 0
			}
			m.input.SetValue("")
			m.input.Placeholder = ""
		}
		return m, nil

	case stSlackAsk:
		switch key.String() {
		case "up", "down":
			m.slackAskCursor = (m.slackAskCursor + 1) % 2
		case "enter":
			if m.slackAskCursor == 0 { // 設定 Slack
				m.step = stSlackToken
				m.errMsg = ""
				m.input.SetValue("")
				m.input.EchoMode = textinput.EchoPassword
				m.input.Placeholder = "貼上 Slack Bot Token (xoxb-…)"
				m.manifestCopied = false
				// open the create page + copy the manifest to the clipboard
				return m, tea.Batch(openURLCmd("https://api.slack.com/apps"), copyManifestCmd())
			}
			{ // 略過
				m.slackSkipped, m.slackDone = true, true
				if m.returnToReuse {
					m.returnToReuse = false
					m.step = stReuse
				} else {
					m.step = stConfirm
				}
				m.input.SetValue("")
				m.input.Placeholder = ""
			}
		}
		return m, nil

	case stSlackToken:
		if m.loading {
			return m, nil
		}
		if key.String() == "enter" {
			tok := strings.TrimSpace(m.input.Value())
			if tok == "" {
				return m, nil
			}
			m.slackToken = tok
			m.loading = true
			m.errMsg = ""
			return m, validateSlackTokenCmd(tok)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return m, cmd

	case stSlackMode:
		switch key.String() {
		case "up", "down":
			m.slackModeCursor = (m.slackModeCursor + 1) % 2
		case "enter":
			if m.slackModeCursor == 0 { // 用現有頻道
				m.step = stSlackPick
				m.loading = true
				m.slackChanCursor = 0
				m.input.SetValue("")
				m.input.Placeholder = "篩選頻道…"
				return m, listChannelsCmd(m.slackToken)
			}
			m.step = stSlackNew // 建立新頻道
			m.input.SetValue("")
			m.input.Placeholder = "新頻道名稱（小寫、用 - 連字，例：pr-review）"
		}
		return m, nil

	case stSlackPick:
		if m.loading {
			return m, nil
		}
		vis := m.visibleChannels()
		switch key.String() {
		case "up":
			if n := len(vis); n > 0 {
				m.slackChanCursor = (m.slackChanCursor - 1 + n) % n
			}
			return m, nil
		case "down":
			if n := len(vis); n > 0 {
				m.slackChanCursor = (m.slackChanCursor + 1) % n
			}
			return m, nil
		case "enter":
			if len(vis) == 0 {
				return m, nil
			}
			ch := vis[m.slackChanCursor]
			m.slackChannel = ch.name
			m.loading = true
			m.errMsg = ""
			return m, channelMembersCmd(m.slackToken, ch.id)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		if n := len(m.visibleChannels()); n == 0 || m.slackChanCursor >= n {
			m.slackChanCursor = 0
		}
		return m, cmd

	case stSlackNew:
		if m.loading {
			return m, nil
		}
		if key.String() == "enter" {
			name := strings.TrimSpace(m.input.Value())
			if name == "" {
				return m, nil
			}
			m.slackChannel = name
			m.loading = true
			m.errMsg = ""
			return m, createChannelWithTeamCmd(m.slackToken, m.cfg.AzureOrg, name, m.mappedProjects())
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return m, cmd

	case stConfirm:
		if key.String() == "enter" {
			m.loading = true
			m.errMsg = ""
			return m, writeConfigCmd(m.org, m.wip, m.mappings, m.slackChannel, m.slackMembers, m.slackToken)
		}
		return m, nil

	case stDone:
		if key.String() == "enter" {
			m.status = "init 完成 → " + m.writtenPath
			m.mode = modeHome
			m.input.SetValue("")
			m.input.Placeholder = homePlaceholder
		}
		return m, nil
	}
	return m, nil
}

// extractTags returns every [tag] from a title.
func extractTags(s string) []string {
	var tags []string
	for {
		i := strings.IndexByte(s, '[')
		if i < 0 {
			break
		}
		j := strings.IndexByte(s[i:], ']')
		if j < 0 {
			break
		}
		tags = append(tags, s[i+1:i+j])
		s = s[i+j+1:]
	}
	return tags
}

// resolveMapping picks a config mapping for a work item: Area Path -> [Tag] -> keyword.
func (m model) resolveMapping(wi workItem) (string, mappingCfg, bool) {
	maps := m.cfg.Mappings

	// 1. Area Path
	if wi.area != "" {
		segs := strings.FieldsFunc(wi.area, func(r rune) bool { return r == '\\' || r == '/' })
		lowArea := strings.ToLower(wi.area)
		var candKeys []string
		for k, mp := range maps {
			if mp.AreaPath != "" && (strings.EqualFold(wi.area, mp.AreaPath) ||
				strings.HasPrefix(lowArea, strings.ToLower(mp.AreaPath)+"\\")) {
				candKeys = append(candKeys, k)
				continue
			}
			for _, s := range segs {
				if mp.AzureProject != "" && strings.EqualFold(s, mp.AzureProject) {
					candKeys = append(candKeys, k)
					break
				}
			}
		}
		repoSet := map[string]bool{}
		for _, k := range candKeys {
			repoSet[maps[k].AzureRepository] = true
		}
		if len(candKeys) >= 1 && len(repoSet) == 1 {
			return candKeys[0], maps[candKeys[0]], true
		}
	}

	// 2. Title [Tag]
	for _, tag := range extractTags(wi.title) {
		for k, mp := range maps {
			cands := append([]string{k, mp.AzureProject}, mp.Aliases...)
			for _, c := range cands {
				if c != "" && strings.EqualFold(tag, c) {
					return k, mp, true
				}
			}
		}
	}

	// 3. Keyword substring (keys by length desc, skip < 3)
	var keys []string
	for k := range maps {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	low := strings.ToLower(wi.title)
	for _, k := range keys {
		if len(k) < 3 {
			continue
		}
		if strings.Contains(low, strings.ToLower(k)) {
			return k, maps[k], true
		}
	}
	return "", mappingCfg{}, false
}

// deriveBranchName mirrors New-Branch: task/<id>-<title with tags/brackets/whitespace stripped>.
func deriveBranchName(taskID int, title string) string {
	c := title
	for {
		t := strings.TrimSpace(c)
		if strings.HasPrefix(t, "[") {
			if j := strings.IndexByte(t, ']'); j >= 0 {
				c = t[j+1:]
				continue
			}
		}
		c = t
		break
	}
	c = strings.ReplaceAll(c, ",", "")
	for _, ch := range []string{"(", ")", "（", "）", "[", "]", "【", "】"} {
		c = strings.ReplaceAll(c, ch, "")
	}
	c = strings.ReplaceAll(c, "&", "and")
	c = strings.ReplaceAll(c, "/", "")
	c = strings.ReplaceAll(c, "\\", "")
	c = strings.Join(strings.Fields(c), "") // remove all whitespace
	for strings.Contains(c, "--") {
		c = strings.ReplaceAll(c, "--", "-")
	}
	c = strings.Trim(c, "-")
	if r := []rune(c); len(r) > 50 {
		c = string(r[:50])
	}
	return fmt.Sprintf("task/%d-%s", taskID, c)
}

func (m model) resolveLocalPath() string {
	for _, p := range []string{
		m.mapping.LocalPath,
		m.cfg.ProjectPaths[m.mapKey],
		m.cfg.ProjectPaths[m.mapping.AzureRepository],
		m.cfg.ProjectPaths[m.mapping.AzureProject],
	} {
		if p != "" {
			return p
		}
	}
	return ""
}

func isGitRepo(path string) bool {
	if path == "" {
		return false
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return false
	}
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

// afterBranch runs once the branch exists: resolve the local repo, else ask.
func (m model) afterBranch() (tea.Model, tea.Cmd) {
	m.localPath = m.resolveLocalPath()
	if isGitRepo(m.localPath) {
		m.tstep = tkCommit
		m.loading = true
		return m, commitCheckCmd(m.localPath, m.baseBranch, m.branchName)
	}
	m.tstep = tkPath
	m.pathCursor = 0
	m.input.SetValue("")
	return m, nil
}

// toggleTask selects/deselects a task, tracking selection order so the first
// selected becomes the primary (used for branch naming).
func (m *model) toggleTask(id int) {
	if m.taskSel == nil {
		m.taskSel = map[int]bool{}
	}
	if m.taskSel[id] {
		delete(m.taskSel, id)
		for i, x := range m.selOrder {
			if x == id {
				m.selOrder = append(m.selOrder[:i], m.selOrder[i+1:]...)
				break
			}
		}
		return
	}
	m.taskSel[id] = true
	m.selOrder = append(m.selOrder, id)
}

func (m model) taskByID(id int) workItem {
	for _, c := range m.children {
		if c.id == id {
			return c
		}
	}
	return workItem{id: id}
}

func joinTaskIDs(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = "#" + strconv.Itoa(id)
	}
	return strings.Join(parts, " ")
}

// taskRows renders the child tasks (with checkboxes) plus the two action rows.
func (m model) taskRows() []string {
	var rows []string
	for _, c := range m.children {
		mark := "[ ]"
		if m.taskSel[c.id] {
			mark = "[x]"
		}
		label := mark + " #" + strconv.Itoa(c.id) + " " + c.title
		if c.state != "" {
			label += " (" + c.state + ")"
		}
		if m.newIDs[c.id] {
			label += " ★新"
		}
		rows = append(rows, label)
	}
	return append(rows, "＋ 建立新 Task", "→ 完成，繼續")
}

// toBranch resolves the project/repo for a Task and moves to the branch step.
func (m model) toBranch(t workItem) (tea.Model, tea.Cmd) {
	key, mp, ok := m.resolveMapping(t)
	if !ok {
		m.errMsg = "無法自動對應專案（Area / [Tag] / 關鍵字都沒中）；手動選之後補"
		m.tstep = tkShow
		return m, nil
	}
	m.selTask = t
	m.mapKey = key
	m.mapping = mp
	m.baseBranch = mp.DefaultBranch
	if m.baseBranch == "" {
		m.baseBranch = "develop"
	}
	m.branchName = deriveBranchName(t.id, t.title)
	m.tstep = tkBranch
	m.loading = true
	m.errMsg = ""
	return m, listRefsCmd(m.cfg.AzureOrg, mp.AzureProject, mp.AzureRepository)
}

func (m model) updateTask(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() == "esc" {
		if m.tstep == tkNewTask && !m.loading {
			// cancel new-task entry -> back to the selection list, keep selections
			m.tstep = tkShow
			m.errMsg = ""
			m.input.SetValue("")
			return m, nil
		}
		m.mode = modeHome
		m.loading = false
		m.errMsg = ""
		m.input.SetValue("")
		m.input.Placeholder = homePlaceholder
		return m, nil
	}

	switch m.tstep {
	case tkInput:
		if key.String() == "enter" {
			return m.startTask(m.input.Value())
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return m, cmd

	case tkShow:
		if m.loading {
			return m, nil
		}
		rows := m.taskRows()
		n := len(rows)
		newIdx := len(m.children)   // "＋ 建立新 Task"
		goIdx := len(m.children) + 1 // "→ 完成，繼續"
		switch key.String() {
		case "up":
			m.taskCursor = (m.taskCursor - 1 + n) % n
		case "down":
			m.taskCursor = (m.taskCursor + 1) % n
		case " ":
			if m.taskCursor < len(m.children) {
				m.toggleTask(m.children[m.taskCursor].id)
			}
		case "enter":
			switch {
			case m.taskCursor < len(m.children):
				m.toggleTask(m.children[m.taskCursor].id)
			case m.taskCursor == newIdx:
				m.tstep = tkNewTask
				m.errMsg = ""
				m.input.SetValue("")
				m.input.Placeholder = "Task 標題（多張用逗號分隔）"
			case m.taskCursor == goIdx:
				if len(m.selOrder) == 0 {
					m.errMsg = "先用 space 選至少一張 Task，或建立新的"
					return m, nil
				}
				m.allTaskIDs = append([]int(nil), m.selOrder...)
				m.errMsg = ""
				return m.toBranch(m.taskByID(m.selOrder[0]))
			}
		}
		return m, nil

	case tkNewTask:
		if m.loading {
			return m, nil
		}
		if key.String() == "enter" {
			var titles []string
			for _, p := range strings.Split(m.input.Value(), ",") {
				if p = strings.TrimSpace(p); p != "" {
					titles = append(titles, p)
				}
			}
			if len(titles) == 0 {
				return m, nil
			}
			m.loading = true
			m.errMsg = ""
			return m, createTasksCmd(m.cfg.AzureOrg, m.cfg.WorkItemProject, m.wi.id, m.wi.area, m.wi.iteration, titles)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return m, cmd

	case tkBranch:
		if m.loading {
			return m, nil
		}
		if key.String() == "enter" {
			if m.branchReuse != "" {
				m.branchName = m.branchReuse
				return m.afterBranch()
			}
			if m.baseObjectID == "" {
				m.errMsg = "找不到基底分支 " + m.baseBranch + " 的 ref"
				return m, nil
			}
			m.loading = true
			m.errMsg = ""
			return m, createBranchCmd(m.cfg.AzureOrg, m.mapping.AzureProject, m.mapping.AzureRepository, m.branchName, m.baseObjectID)
		}
		return m, nil

	case tkPath:
		switch key.String() {
		case "up":
			m.pathCursor = (m.pathCursor - 1 + 2) % 2
		case "down":
			m.pathCursor = (m.pathCursor + 1) % 2
		case "enter":
			m.cloneMode = m.pathCursor == 1
			m.tstep = tkPathInput
			m.errMsg = ""
			if m.cloneMode {
				m.input.SetValue("C:\\front\\" + m.mapKey) // your convention; editable
				m.input.Placeholder = "clone 目標資料夾"
			} else {
				m.input.SetValue("")
				m.input.Placeholder = "現有專案資料夾路徑（含 .git）"
			}
		}
		return m, nil

	case tkPathInput:
		if m.loading {
			return m, nil
		}
		if key.String() == "enter" {
			p := strings.TrimSpace(m.input.Value())
			if p == "" {
				return m, nil
			}
			if m.cloneMode {
				m.loading = true
				m.errMsg = ""
				return m, cloneCmd(m.cfg.AzureOrg, m.mapping.AzureProject, m.mapping.AzureRepository, p)
			}
			if !isGitRepo(p) {
				m.errMsg = "這個路徑不是 git repo（要有 .git）"
				return m, nil
			}
			m.localPath = p
			m.tstep = tkCommit
			m.loading = true
			m.errMsg = ""
			return m, commitCheckCmd(m.localPath, m.baseBranch, m.branchName)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return m, cmd

	case tkCommit:
		if m.loading {
			return m, nil
		}
		if key.String() == "enter" {
			if m.commitCount > 0 {
				// commits ready -> pick a reviewer, then build the PR
				m.tstep = tkReviewer
				m.loading = true
				m.errMsg = ""
				m.revCursor = 0
				return m, listReviewersCmd(m.cfg.AzureOrg, m.mapping.AzureProject, m.cfg.Slack.Members)
			}
			m.loading = true
			m.errMsg = ""
			return m, commitCheckCmd(m.localPath, m.baseBranch, m.branchName)
		}
		return m, nil

	case tkReviewer:
		if m.loading {
			return m, nil
		}
		n := len(m.reviewers) + 1 // + "略過"
		switch key.String() {
		case "up":
			m.revCursor = (m.revCursor - 1 + n) % n
		case "down":
			m.revCursor = (m.revCursor + 1) % n
		case "enter":
			if m.revCursor < len(m.reviewers) {
				m.chosenRev = m.reviewers[m.revCursor]
			} else {
				m.chosenRev = reviewer{} // skip
			}
			m.tstep = tkPRCreating
			m.loading = true
			m.errMsg = ""
			title := m.branchName
			return m, createPRCmd(m.cfg.AzureOrg, m.mapping.AzureProject, m.mapping.AzureRepository,
				m.branchName, m.baseBranch, title, m.commits, m.allTaskIDs, m.chosenRev)
		}
		return m, nil

	case tkPRCreating:
		return m, nil // wait for the async result

	case tkPRDone:
		if key.String() == "enter" {
			m.mode = modeHome
			m.input.SetValue("")
			m.input.Placeholder = homePlaceholder
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

func allOK(rs []checkResult) bool {
	for _, r := range rs {
		if !r.ok {
			return false
		}
	}
	return true
}

func (m *model) gotoMode() {
	m.step = stMode
	m.modeCursor = 0
	m.input.SetValue("")
	m.input.Placeholder = ""
}

func (m *model) gotoWIP() tea.Cmd {
	m.step = stWIP
	m.loading = true
	m.errMsg = ""
	m.projCursor = 0
	m.input.SetValue("")
	m.input.Placeholder = "篩選專案（工單所在）"
	return listProjectsCmd(m.org)
}

func (m *model) gotoArea() tea.Cmd {
	m.step = stArea
	m.errMsg = ""
	m.areaCursor = 0
	m.input.SetValue(m.project) // pre-filter Areas by the picked project name
	m.input.Placeholder = "篩選 Area（產品）"
	if len(m.areas) > 0 {
		m.loading = false
		return nil // Area tree already fetched earlier in the loop
	}
	m.loading = true
	return listAreasCmd(m.org, m.wip)
}

func (m *model) gotoTag() {
	m.step = stTag
	m.input.SetValue("")
	m.input.Placeholder = "輸入標題標籤，例如 Chem（對應 [Chem]）"
}

func (m *model) gotoCodeProj(defaultName string) tea.Cmd {
	m.step = stCodeProj
	m.errMsg = ""
	m.input.SetValue("")
	m.input.Placeholder = "篩選專案（code / repo 所在）"
	if len(m.projects) == 0 {
		m.loading = true
		m.projCursor = 0
		return listProjectsCmd(m.org)
	}
	// reuse already-fetched projects; default-select the same-named one
	m.projCursor = 0
	for i, p := range m.projects {
		if strings.EqualFold(p, defaultName) {
			m.projCursor = i
			break
		}
	}
	return nil
}

func (m *model) gotoCodeRepo() tea.Cmd {
	m.step = stCodeRepo
	m.loading = true
	m.errMsg = ""
	m.repoCursor = 0
	m.input.SetValue("")
	m.input.Placeholder = "篩選儲存庫"
	return listReposCmd(m.org, m.project)
}

func (m *model) gotoMore() {
	m.step = stMore
	m.moreCursor = 0
	m.input.SetValue("")
	m.input.Placeholder = ""
}

func (m *model) finalizeMapping() {
	m.mappings = append(m.mappings, mappingEntry{
		key:      m.curKey,
		areaPath: m.curArea.path,
		alias:    m.curAlias,
		project:  m.project,
		repo:     m.repo.name,
		branch:   m.repo.branch,
	})
	m.curArea, m.curAlias, m.curKey = areaInfo{}, "", ""
}

// orgItems is the filtered org list plus a trailing manual-entry sentinel.
func (m model) orgItems() []string {
	q := strings.TrimSpace(m.input.Value())
	var out []string
	for _, o := range m.orgs {
		if q == "" || fuzzyMatch(q, o) {
			out = append(out, o)
		}
	}
	return append(out, manualOrgLabel)
}

func (m model) visibleProjects() []string {
	q := strings.TrimSpace(m.input.Value())
	if q == "" {
		return m.projects
	}
	var out []string
	for _, p := range m.projects {
		if fuzzyMatch(q, p) {
			out = append(out, p)
		}
	}
	return out
}

func (m model) visibleRepos() []repoInfo {
	q := strings.TrimSpace(m.input.Value())
	if q == "" {
		return m.repos
	}
	var out []repoInfo
	for _, r := range m.repos {
		if fuzzyMatch(q, r.name) {
			out = append(out, r)
		}
	}
	return out
}

func (m model) visibleAreas() []areaInfo {
	q := strings.TrimSpace(m.input.Value())
	if q == "" {
		return m.areas
	}
	var out []areaInfo
	for _, a := range m.areas {
		if fuzzyMatch(q, a.name) || fuzzyMatch(q, a.path) {
			out = append(out, a)
		}
	}
	return out
}

// ---- view ----
func (m model) View() string {
	if m.quitting {
		return "\n  " + styleFg(muted, "已離開") + "\n\n"
	}
	if m.mode == modeInit {
		return m.viewInit()
	}
	if m.mode == modeTask {
		return m.viewTask()
	}
	if m.mode == modeUpdate {
		return m.viewUpdate()
	}
	if m.mode == modePbi {
		return m.viewPbi()
	}
	return m.viewHome()
}

func (m model) banner() string {
	return "\n " + brand("very-lazy") + "  " + styleFg(muted, version) + "\n"
}

// commandBar is the full-width input between two rules — only shown on the home screen.
func (m model) commandBar() string {
	w := m.width
	if w <= 0 {
		w = 84
	}
	rule := styleFg(dim, strings.Repeat("─", w))
	return rule + "\n " + m.input.View() + "\n" + rule + "\n"
}

// stepUsesInput reports whether the current init step drives the text input
// (as a list filter or a value entry). Those steps render the input inside the box.
func (m model) stepUsesInput() bool {
	switch m.step {
	case stOrg, stWIP, stArea, stCodeProj, stCodeRepo, stTag, stSlackToken, stSlackPick, stSlackNew:
		return true
	}
	return false
}

func (m model) filterLine() string {
	label := "篩選 "
	switch {
	case m.step == stTag, m.step == stSlackToken, m.step == stSlackNew, m.step == stOrg && m.orgManual:
		label = "" // value entry, not a filter
	}
	return styleFg(muted, label) + m.input.View()
}

func (m model) hintbar(hint string) string {
	return "\n " + styleFg(muted, hint) + "\n"
}

func (m model) viewHome() string {
	var body strings.Builder
	if len(m.cmdMatches) == 0 {
		body.WriteString("   " + styleFg(muted, "找不到符合的指令"))
	} else {
		for i, c := range m.cmdMatches {
			name := styleBold(accent, padRight(c.name, 10))
			desc := styleFg(muted, c.desc)
			if i == m.cmdCursor {
				body.WriteString(" " + styleFg(accent, "❯ ") + name + " " + desc)
			} else {
				body.WriteString("   " + name + " " + desc)
			}
			if i < len(m.cmdMatches)-1 {
				body.WriteString("\n")
			}
		}
	}
	if m.status != "" {
		body.WriteString("\n\n   " + styleFg(accent, "• "+m.status))
	}
	return m.banner() + "\n" + m.commandBar() + "\n" + body.String() + "\n" +
		m.hintbar("↑↓ 選擇   Tab 補全   ⏎ 執行   esc 離開")
}

const listMax = 8 // max rows shown at once (keeps the header/input on screen)

// windowSlice returns the [start,end) window of a list, scrolled to keep cursor in view.
func windowSlice(n, cursor, maxVisible int) (int, int) {
	if n <= maxVisible {
		return 0, n
	}
	start := cursor - maxVisible/2
	if start < 0 {
		start = 0
	}
	end := start + maxVisible
	if end > n {
		end = n
		start = end - maxVisible
	}
	return start, end
}

// renderWindow renders a scrolling window with "more above/below" hints.
func renderWindow(n, cursor, maxVisible int, row func(i int, selected bool) string) string {
	start, end := windowSlice(n, cursor, maxVisible)
	var b strings.Builder
	if start > 0 {
		b.WriteString("  " + styleFg(dim, fmt.Sprintf("↑ 還有 %d 個", start)) + "\n")
	}
	for i := start; i < end; i++ {
		b.WriteString(row(i, i == cursor))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	if end < n {
		b.WriteString("\n  " + styleFg(dim, fmt.Sprintf("↓ 還有 %d 個", n-end)))
	}
	return b.String()
}

func selRow(text string) string {
	return lipgloss.NewStyle().Background(selBg).Foreground(selFg).Bold(true).Padding(0, 1).Render("❯ " + text)
}

func renderList(items []string, cursor int) string {
	return renderWindow(len(items), cursor, listMax, func(i int, sel bool) string {
		if sel {
			return selRow(items[i])
		}
		return "  " + styleFg(muted, items[i])
	})
}

func renderRepoList(items []repoInfo, cursor int) string {
	return renderWindow(len(items), cursor, listMax, func(i int, sel bool) string {
		r := items[i]
		if sel {
			return selRow(r.name + "  (" + r.branch + ")")
		}
		return "  " + styleFg(muted, r.name) + "  " + styleFg(dim, "("+r.branch+")")
	})
}

// initStatus is the at-a-glance checklist shown under the banner during /init,
// so you can see which steps are already done/verified.
func (m model) initStatus() string {
	var lines []string
	add := func(label, val string) {
		lines = append(lines, " "+styleFg(okCol, "✓")+" "+styleFg(muted, label)+" "+val)
	}
	if len(m.envResults) > 0 && m.azOK {
		add("環境", styleFg(dim, "git / az / 擴充 / 登入"))
	}
	if m.org != "" {
		add("組織", m.org)
	}
	if m.wip != "" {
		add("工作項專案", m.wip)
	}
	if len(m.mappings) > 0 {
		add("專案對應", fmt.Sprintf("%d 個", len(m.mappings)))
	}
	if m.slackDone {
		if m.slackSkipped {
			add("Slack", styleFg(dim, "略過"))
		} else {
			add("Slack", m.slackChannel+fmt.Sprintf("（%d 人）", len(m.slackMembers)))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func (m model) viewInit() string {
	var body strings.Builder

	switch m.step {
	case stEnv:
		body.WriteString(styleBold(accent, "環境檢查") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "檢查 git / az / 擴充 / 登入…"))
		} else {
			for _, r := range m.envResults {
				mark := styleFg(okCol, "✔")
				if !r.ok {
					mark = styleFg(errCol, "✗")
				}
				line := mark + " " + r.name
				if !r.ok && r.detail != "" {
					line += "   " + styleFg(muted, r.detail)
				}
				body.WriteString(line + "\n")
			}
			body.WriteString("\n")
			if m.azOK {
				body.WriteString(styleFg(muted, "按 Enter 繼續 →"))
			} else {
				body.WriteString(styleFg(errCol, "az 尚未就緒，先處理上面問題再重跑（esc 返回）"))
			}
		}

	case stOrg:
		body.WriteString(styleBold(accent, "Azure DevOps 組織") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "探索組織中…"))
		} else if m.orgManual {
			body.WriteString(styleFg(muted, "在上面輸入框貼上組織 URL，Enter 繼續。"))
		} else {
			body.WriteString(styleFg(muted, "選擇組織（打字篩選）：") + "\n\n")
			body.WriteString(renderList(m.orgItems(), m.orgCursor))
		}

	case stMode:
		body.WriteString(styleBold(accent, "工單怎麼對應到專案？") + "\n\n")
		body.WriteString(renderList(modeLabels, m.modeCursor))

	case stWIP:
		body.WriteString(styleBold(accent, "工作項所在的專案") + "\n\n")
		body.WriteString(styleFg(muted, "你的 Task / PBI 開在哪個 Azure DevOps 專案（Area、Iteration 的根）。") + "\n")
		body.WriteString(styleFg(dim, "通常整個團隊共用一個，例：ESHClouds。") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "探索專案中…"))
		} else if vis := m.visibleProjects(); len(vis) == 0 {
			body.WriteString(styleFg(muted, "沒有符合的專案"))
		} else {
			body.WriteString(renderList(vis, m.projCursor))
		}

	case stArea:
		body.WriteString(styleBold(accent, "對應的 Area") + "\n\n")
		body.WriteString(styleFg(muted, "為 ") + styleFg(accent, m.project) +
			styleFg(muted, " 選對應的 Area（已用專案名預篩，可清空看全部）：") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "探索 Area 中…"))
		} else if vis := m.visibleAreas(); len(vis) == 0 {
			body.WriteString(styleFg(muted, "沒有符合的 Area（清空篩選看全部）"))
		} else {
			body.WriteString(renderAreaList(vis, m.areaCursor))
		}

	case stTag:
		body.WriteString(styleBold(accent, "標題標籤") + "\n\n")
		body.WriteString(styleFg(muted, "輸入這個產品在標題裡的標籤（不含中括號），例如 Chem → 對應 [Chem]。Enter 繼續。"))

	case stCodeProj:
		body.WriteString(styleBold(accent, "程式碼 / repo 所在的專案") + "\n\n")
		body.WriteString(styleFg(muted, "這個產品的 code 放在哪個 Azure 專案 —— 就是你要開分支、建 PR 的地方。") + "\n")
		body.WriteString(styleFg(dim, "跟上面「工作項專案」可能不同；一個產品一個，例：Chem。") + "\n\n")
		if m.curAlias != "" {
			body.WriteString(styleFg(muted, "正在為 ") + styleFg(accent, m.curAlias) +
				styleFg(muted, " 設定：") + "\n\n")
		}
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "探索專案中…"))
		} else if vis := m.visibleProjects(); len(vis) == 0 {
			body.WriteString(styleFg(muted, "沒有符合的專案"))
		} else {
			body.WriteString(renderList(vis, m.projCursor))
		}

	case stCodeRepo:
		body.WriteString(styleBold(accent, "儲存庫") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "探索儲存庫中…"))
		} else if vis := m.visibleRepos(); len(vis) == 0 {
			body.WriteString(styleFg(muted, "沒有符合的儲存庫"))
		} else {
			body.WriteString(renderRepoList(vis, m.repoCursor))
		}

	case stMore:
		body.WriteString(styleBold(accent, "已加入的對應") + "\n\n")
		for _, e := range m.mappings {
			body.WriteString("  " + styleFg(accent, e.key) + styleFg(muted, " → "+e.project+"/"+e.repo))
			if e.areaPath != "" {
				body.WriteString(styleFg(dim, "  ["+e.areaPath+"]"))
			}
			body.WriteString("\n")
		}
		body.WriteString("\n")
		moreDone := "完成，寫入設定"
		if m.returnToReuse {
			moreDone = "完成，回選單"
		}
		body.WriteString(renderList([]string{"再加一個對應", moreDone}, m.moreCursor))

	case stReuse:
		body.WriteString(styleBold(accent, "已有設定 — 要改哪個？") + "\n\n")
		body.WriteString(styleFg(muted, "選要重設的項目，其它沿用（本機路徑等不會被清掉）。") + "\n\n")
		wip := m.wip
		if wip == "" {
			wip = "（未設定）"
		}
		slack := "未設定"
		if m.slackDone {
			if m.slackChannel != "" {
				slack = m.slackChannel + fmt.Sprintf("（%d 人）", len(m.slackMembers))
			} else {
				slack = "略過"
			}
		}
		body.WriteString(renderList([]string{
			"工作項專案：" + wip,
			fmt.Sprintf("專案對應：%d 個（檢視 / 新增）", len(m.mappings)),
			"Slack：" + slack,
			"✓ 完成，寫入設定",
		}, m.reuseCursor))

	case stSlackAsk:
		body.WriteString(styleBold(accent, "Slack 通知（可選）") + "\n\n")
		body.WriteString(styleFg(muted, "建 PR 後自動到頻道 tag reviewer 請 review。要現在設定嗎？") + "\n\n")
		body.WriteString(renderList([]string{"設定 Slack", "略過（之後再 /init 補）"}, m.slackAskCursor))

	case stSlackToken:
		body.WriteString(styleBold(accent, "Slack Bot Token") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "驗證 token…"))
		} else {
			if m.manifestCopied {
				body.WriteString(styleBold(okCol, "✓ manifest(JSON)已複製到剪貼簿") + "  " + styleFg(muted, "已開 api.slack.com/apps") + "\n\n")
			} else {
				body.WriteString(styleFg(accent, "已開 ") + styleBold(accent, "https://api.slack.com/apps") + "\n\n")
			}
			body.WriteString(styleFg(accent, "1. ") + styleFg(muted, "Create New App → From a manifest → 選你的 workspace") + "\n")
			body.WriteString(styleFg(accent, "2. ") + styleFg(muted, "預設 JSON 分頁 → Ctrl+A 全選 → Ctrl+V 貼上 → Next → Create and install → Go to App settings") + "\n")
			body.WriteString(styleFg(accent, "3. ") + styleFg(muted, "找到 OAuth & Permissions → 複製 Bot User OAuth Token（xoxb-…）") + "\n\n")
			body.WriteString(styleBold(accent, "貼上 token") + styleFg(muted, "（會遮蔽顯示），Enter 驗證。"))
		}

	case stSlackMode:
		body.WriteString(styleBold(accent, "審核頻道") + "\n\n")
		body.WriteString(styleFg(okCol, "✓ token 有效") + "\n\n")
		body.WriteString(renderList([]string{"用現有頻道（挑一個，bot 自動加入）", "建立新頻道（依你的專案團隊自動邀人）"}, m.slackModeCursor))

	case stSlackPick:
		body.WriteString(styleBold(accent, "選審核頻道") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "讀取頻道…（選完會自動加入並抓成員）"))
		} else {
			var items []string
			for _, c := range m.visibleChannels() {
				mark := "# "
				if c.private {
					mark = "🔒 "
				}
				items = append(items, mark+c.name)
			}
			if len(items) == 0 {
				body.WriteString(styleFg(muted, "沒有符合的頻道"))
			} else {
				body.WriteString(renderList(items, m.slackChanCursor))
			}
		}

	case stSlackNew:
		body.WriteString(styleBold(accent, "建立新頻道") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "建頻道、依 email 對 Slack、邀人中…"))
		} else {
			body.WriteString(styleFg(muted, "輸入新頻道名稱（小寫、用 - 連字，不含 #），Enter 建立。") + "\n")
			body.WriteString(styleFg(dim, "會自動把 "+strings.Join(m.mappedProjects(), " / ")+" 團隊裡對得到 Slack 的人邀進來。"))
		}

	case stConfirm:
		body.WriteString(styleBold(accent, "確認並寫入") + "\n\n")
		body.WriteString(styleFg(muted, "組織    ") + m.org + "\n")
		body.WriteString(styleFg(muted, "工單專案 ") + m.wip + "\n")
		body.WriteString(styleFg(muted, "對應") + "\n")
		for _, e := range m.mappings {
			body.WriteString("  " + e.key + " → " + e.project + "/" + e.repo + " (" + e.branch + ")\n")
			if e.areaPath != "" {
				body.WriteString(styleFg(dim, "     area: "+e.areaPath) + "\n")
			}
			if e.alias != "" {
				body.WriteString(styleFg(dim, "     tag : ["+e.alias+"]") + "\n")
			}
		}
		if m.slackSkipped {
			body.WriteString(styleFg(muted, "Slack    ") + styleFg(dim, "略過") + "\n")
		} else if m.slackDone {
			body.WriteString(styleFg(muted, "Slack    ") + m.slackChannel + fmt.Sprintf("（%d 人）", len(m.slackMembers)) + "\n")
		}
		body.WriteString("\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "寫入中…"))
		} else {
			body.WriteString(styleFg(muted, "按 Enter 寫入設定"))
		}

	case stDone:
		body.WriteString(styleBold(okCol, "完成 🎉") + "\n\n")
		body.WriteString(styleFg(muted, "已寫入：") + "\n" + m.writtenPath + "\n\n")
		if m.slackSkipped {
			body.WriteString(styleFg(dim, "（Slack 未設定，之後 /init 可補）") + "\n")
		}
		body.WriteString(styleFg(muted, "按 Enter 回首頁"))
	}

	if m.errMsg != "" {
		body.WriteString("\n\n" + styleFg(errCol, "⚠ "+m.errMsg))
	}

	content := strings.TrimRight(body.String(), "\n")
	if m.stepUsesInput() {
		// filter / value input lives inside the step box (not the home command bar)
		content = m.filterLine() + "\n\n" + content
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dim).
		Padding(0, 2).
		Render(content)

	return m.banner() + m.initStatus() + "\n" + box + "\n" + m.hintbar("↑↓ 選擇   ⏎ 確認   esc 取消")
}

func renderAreaList(items []areaInfo, cursor int) string {
	return renderWindow(len(items), cursor, listMax, func(i int, sel bool) string {
		a := items[i]
		if sel {
			return selRow(a.name + "  " + a.path)
		}
		return "  " + styleFg(muted, a.name) + "  " + styleFg(dim, a.path)
	})
}

func (m model) viewTask() string {
	var body strings.Builder

	switch m.tstep {
	case tkInput:
		body.WriteString(styleBold(accent, "處理工作項") + "\n\n")
		body.WriteString(styleFg(muted, "輸入工作項 ID。"))

	case tkShow:
		if m.loading {
			body.WriteString(styleBold(accent, "處理工作項") + "\n\n")
			body.WriteString(m.spin.View() + " " + styleFg(muted, "讀取工作項…"))
		} else {
			w := m.wi
			body.WriteString(styleFg(muted, "["+w.typ+"] ") + styleBold(accent, "#"+strconv.Itoa(w.id)) + " " + w.title + "\n")
			meta := "狀態 " + w.state
			if w.area != "" {
				meta += "   Area " + w.area
			}
			body.WriteString(styleFg(muted, meta) + "\n\n")
			body.WriteString(renderList(m.taskRows(), m.taskCursor))
			if n := len(m.selOrder); n > 0 {
				body.WriteString("\n\n" + styleFg(okCol, fmt.Sprintf("已選 %d 張，主要 #%d（分支命名）", n, m.selOrder[0])))
			}
		}

	case tkNewTask:
		body.WriteString(styleBold(accent, "建立新 Task") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "建立中…"))
		} else {
			body.WriteString(styleFg(muted, "父項 ") + "#" + strconv.Itoa(m.wi.id) + " " + m.wi.title + "\n")
			if m.wi.area != "" {
				body.WriteString(styleFg(muted, "Area ") + m.wi.area + "\n")
			}
			body.WriteString("\n" + styleFg(muted, "會自動帶父項的 Area/Iteration、指派給自己。"))
			body.WriteString("\n" + styleFg(muted, "一次多張用逗號分隔，例：A,B"))
		}

	case tkBranch:
		body.WriteString(styleBold(accent, "建立分支") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "查詢分支中…"))
		} else {
			body.WriteString(styleFg(muted, "Task   ") + "#" + strconv.Itoa(m.selTask.id) + " " + m.selTask.title + "\n")
			if len(m.allTaskIDs) > 1 {
				body.WriteString(styleFg(muted, "PR 連結 ") + joinTaskIDs(m.allTaskIDs) + "\n")
			}
			body.WriteString(styleFg(muted, "專案   ") + m.mapping.AzureProject + " / " + m.mapping.AzureRepository + "\n")
			body.WriteString(styleFg(muted, "基底   ") + m.baseBranch + "\n\n")
			if m.branchReuse != "" {
				body.WriteString(styleFg(okCol, "重用既有分支：") + m.branchReuse)
			} else {
				body.WriteString(styleFg(accent, "新建分支：") + m.branchName + "\n")
				body.WriteString(styleFg(dim, "會在 "+m.mapping.AzureRepository+" server 端建立"))
			}
		}

	case tkPath:
		body.WriteString(styleBold(accent, "本機專案路徑") + "\n\n")
		body.WriteString(styleFg(muted, "專案 ") + m.mapKey + "  " + m.mapping.AzureProject + " / " + m.mapping.AzureRepository + "\n\n")
		body.WriteString(styleFg(muted, "這個 repo 沒有設定本機路徑，要怎麼取得？") + "\n\n")
		body.WriteString(renderList([]string{"填現有專案資料夾", "clone 到指定目錄"}, m.pathCursor))

	case tkPathInput:
		proj := styleFg(muted, "專案 ") + m.mapKey + "  " + m.mapping.AzureProject + " / " + m.mapping.AzureRepository + "\n\n"
		if m.cloneMode {
			body.WriteString(styleBold(accent, "clone 專案") + "\n\n")
			body.WriteString(proj)
			if m.loading {
				body.WriteString(m.spin.View() + " " + styleFg(muted, "clone 中…（第一次可能較久）"))
			} else {
				body.WriteString(styleFg(muted, "輸入 clone 目標資料夾。"))
			}
		} else {
			body.WriteString(styleBold(accent, "現有專案路徑") + "\n\n")
			body.WriteString(proj)
			body.WriteString(styleFg(muted, "輸入專案資料夾（要有 .git）。"))
		}

	case tkCommit:
		body.WriteString(styleBold(accent, "等待 commit") + "\n\n")
		body.WriteString(styleFg(muted, "分支 ") + m.branchName + "\n")
		body.WriteString(styleFg(muted, "本機 ") + m.localPath + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "檢查 commit 中…"))
		} else if m.commitCount == -1 {
			body.WriteString(styleFg(muted, "分支還沒 push 到 origin，push 後重新檢查。"))
		} else if m.commitCount == 0 {
			body.WriteString(styleFg(muted, "還沒有新 commit（vs "+m.baseBranch+"），commit + push 後重新檢查。"))
		} else {
			body.WriteString(styleFg(okCol, fmt.Sprintf("找到 %d 個 commit：", m.commitCount)) + "\n")
			body.WriteString(styleFg(dim, m.commits))
		}

	case tkReviewer:
		body.WriteString(styleBold(accent, "選 Reviewer") + "\n\n")
		if m.loading {
			body.WriteString(m.spin.View() + " " + styleFg(muted, "讀取團隊成員…"))
		} else {
			body.WriteString(styleFg(muted, "PR ") + m.branchName + styleFg(muted, "  →  ") + m.baseBranch + "\n")
			if len(m.allTaskIDs) > 0 {
				body.WriteString(styleFg(muted, "連結 ") + joinTaskIDs(m.allTaskIDs) + "\n")
			}
			body.WriteString("\n")
			var items []string
			for _, r := range m.reviewers {
				label := r.name
				if label == "" {
					label = r.email
				} else {
					label += "  " + r.email
				}
				if r.slackID != "" {
					label += "  (Slack✓)"
				}
				items = append(items, label)
			}
			items = append(items, "略過（不加 reviewer）")
			body.WriteString(renderList(items, m.revCursor))
		}

	case tkPRCreating:
		body.WriteString(styleBold(accent, "建立 PR") + "\n\n")
		body.WriteString(m.spin.View() + " " + styleFg(muted, "建立 PR / 加 reviewer / 通知…"))

	case tkPRDone:
		body.WriteString(styleBold(accent, "PR 已建立") + "\n\n")
		body.WriteString(styleFg(okCol, "✓ Pull Request #"+strconv.Itoa(m.prID)) + "\n")
		body.WriteString(styleFg(dim, m.prURL) + "\n\n")
		if len(m.allTaskIDs) > 0 {
			body.WriteString(styleFg(muted, "連結工作項 ") + joinTaskIDs(m.allTaskIDs) + "\n")
		}
		if m.revNote != "" {
			body.WriteString(styleFg(muted, m.revNote) + "\n")
		}
		if m.slackMsg != "" {
			body.WriteString(styleFg(muted, m.slackMsg) + "\n")
		}
	}

	if m.errMsg != "" {
		body.WriteString("\n\n" + styleFg(errCol, "⚠ "+m.errMsg))
	}

	content := strings.TrimRight(body.String(), "\n")
	if m.tstep == tkInput || m.tstep == tkPathInput || m.tstep == tkNewTask {
		content = m.input.View() + "\n\n" + content
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dim).
		Padding(0, 2).
		Render(content)

	return m.banner() + "\n" + box + "\n" + m.hintbar(m.taskHint())
}

// taskHint is the single source of key hints for the /task flow — shown only in
// the bottom bar, not scattered inside the step box.
func (m model) taskHint() string {
	switch m.tstep {
	case tkInput:
		return "⏎ 讀取   esc 取消"
	case tkShow:
		return "↑↓ 移動   space 選取   ⏎ 建立/繼續   esc 取消"
	case tkNewTask:
		return "⏎ 建立   esc 返回"
	case tkBranch:
		return "⏎ 建立分支   esc 取消"
	case tkPath:
		return "↑↓ 選擇   ⏎ 確認   esc 取消"
	case tkPathInput:
		return "⏎ 確認   esc 取消"
	case tkCommit:
		if m.commitCount > 0 {
			return "⏎ 繼續建 PR   esc 取消"
		}
		return "⏎ 重新檢查   esc 取消"
	case tkReviewer:
		return "↑↓ 選擇   ⏎ 確認   esc 取消"
	case tkPRDone:
		return "⏎ 返回"
	case tkPRCreating:
		return "請稍候…"
	}
	return "↑↓ 選擇   ⏎ 確認   esc 取消"
}

func main() {
	if _, err := tea.NewProgram(initialModel()).Run(); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}
