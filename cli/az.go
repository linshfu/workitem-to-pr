package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ---- data ----
type checkResult struct {
	name   string
	ok     bool
	detail string
}

type repoInfo struct {
	name   string
	branch string
}

type areaInfo struct {
	name string // leaf name, e.g. "Chem"
	path string // normalized System.AreaPath, e.g. "ESHClouds\1.Product\Chem"
}

// areaNode mirrors the classification-node JSON from `az boards area project list`.
type areaNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	Children []areaNode `json:"children"`
}

// mappingEntry is one azureProjectMappings row being built by the wizard.
type mappingEntry struct {
	key      string
	areaPath string
	alias    string
	project  string
	repo     string
	branch   string
}

// ---- messages (async results) ----
type envMsg struct {
	results []checkResult
	azOK    bool
}
type orgDefaultMsg struct{ org string }
type orgsMsg struct {
	orgs []string
	err  error
}
type projectsMsg struct {
	projects []string
	err      error
}
type reposMsg struct {
	repos []repoInfo
	err   error
}
type areasMsg struct {
	areas []areaInfo
	err   error
}

type workItem struct {
	id          int
	title       string
	typ         string
	state       string
	area        string
	iteration   string
	assigned    string
	childIDs    []int
	parentID    int    // Hierarchy-Reverse 目標（綁 Feature 用 parent；0＝無）
	relatedID   int    // System.LinkTypes.Related 第一個（綁 Release 用 related；0＝無）
	parentTyp   string // 綁的上層 type（顯示用，延遲載入）
	parentTitle string // 綁的上層標題（顯示用）
}

// boundParentID 回傳這張單綁的上層：Feature 走 parent、Release 走 related；0＝沒綁。
func (w workItem) boundParentID() int {
	if w.parentID > 0 {
		return w.parentID
	}
	return w.relatedID
}

type workItemMetaMsg struct {
	wi  workItem
	err error
}

// childOneMsg is one streamed child load: rest = sibling ids still to load,
// parent = the node these children belong to.
type childOneMsg struct {
	child  workItem
	rest   []int
	parent workItem
	err    error
}
type writtenMsg struct {
	path string
	err  error
}

// dbg appends a line to vlui-debug.log (cwd) — temporary instrumentation.
func dbg(format string, a ...any) {
	f, err := os.OpenFile("vlui-debug.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, format+"\n", a...)
}

// azCommand rewrites an `az ...` call to invoke the bundled Python directly with
// `-X utf8`. az.cmd runs Python with -I (isolated), which ignores PYTHON* env vars,
// so non-ASCII output comes back as the system code page (CP950) and mojibakes.
// -X utf8 forces UTF-8 output. Falls back to plain "az" if the python isn't found.
func azCommand(args []string) (string, []string) {
	azPath, err := exec.LookPath("az.cmd")
	if err != nil {
		azPath, err = exec.LookPath("az")
	}
	if err == nil {
		py := filepath.Join(filepath.Dir(azPath), "..", "python.exe")
		if _, e := os.Stat(py); e == nil {
			return py, append([]string{"-X", "utf8", "-IBm", "azure.cli"}, args...)
		}
	}
	return "az", args
}

// cmdTimeout bounds every az/git call so a hung process can't wedge the run
// forever (headless has no human to notice a spinner that never stops). It is
// deliberately generous — normal calls take seconds, the slowest observed are
// `az devops team list-member` over many teams and `git fetch` on a big repo —
// so hitting it means something is genuinely wrong, not merely slow.
// Override with VLUI_TIMEOUT_SEC if some environment really is slower.
func cmdTimeout() time.Duration {
	if s := os.Getenv("VLUI_TIMEOUT_SEC"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 120 * time.Second
}

// run executes a command and returns trimmed stdout. On failure it surfaces the
// first line of stderr (az errors are multi-line) so the UI can show something useful.
func run(name string, args ...string) (string, error) {
	label := name // azCommand 會把 "az" 換成 python.exe 的完整路徑，訊息要用原本的名字
	if name == "az" {
		name, args = azCommand(args)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		e := strings.TrimSpace(stderr.String())
		dbg("RUN FAIL: %s %s\n  err=%v\n  stderr=%s", name, strings.Join(args, " "), err, e)
		// 逾時要跟一般失敗分開講：指令可能其實已經在伺服器端生效了（例如 PR 建到
		// 一半），直接重跑有可能建出第二份，所以提示先去確認再重試。
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("%s 執行逾時（超過 %s）；重跑前請先確認這個動作是不是其實已經生效", label, cmdTimeout())
		}
		if e != "" {
			return "", fmt.Errorf("%s", firstLine(e))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// checkEnvCmd verifies git / az / azure-devops extension / az login.
func checkEnvCmd() tea.Cmd {
	return func() tea.Msg {
		var res []checkResult
		azOK := false

		if _, err := run("git", "--version"); err == nil {
			res = append(res, checkResult{"git", true, ""})
		} else {
			res = append(res, checkResult{"git", false, "找不到 git"})
		}

		if _, err := run("az", "version"); err == nil {
			res = append(res, checkResult{"az (Azure CLI)", true, ""})
			azOK = true
		} else {
			res = append(res, checkResult{"az (Azure CLI)", false, "找不到 Azure CLI"})
		}

		if azOK {
			if _, err := run("az", "extension", "show", "--name", "azure-devops"); err == nil {
				res = append(res, checkResult{"azure-devops 擴充", true, ""})
			} else {
				res = append(res, checkResult{"azure-devops 擴充", false, "az extension add --name azure-devops"})
			}
			if _, err := run("az", "account", "show"); err == nil {
				res = append(res, checkResult{"az 已登入", true, ""})
			} else {
				res = append(res, checkResult{"az 已登入", false, "請先執行 az login"})
			}
		}
		return envMsg{results: res, azOK: azOK}
	}
}

// defaultOrgCmd reads the organization already saved as an az devops default.
func defaultOrgCmd() tea.Cmd {
	return func() tea.Msg {
		out, err := run("az", "devops", "configure", "--list")
		if err != nil {
			return orgDefaultMsg{""}
		}
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "organization") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					return orgDefaultMsg{strings.TrimSpace(parts[1])}
				}
			}
		}
		return orgDefaultMsg{""}
	}
}

// listOrgsCmd lists the Azure DevOps organizations the signed-in account belongs to,
// via the accounts REST API (profile -> memberId -> accounts). Query params are passed
// as separate --url-parameters args because a literal "&" in --url gets split by cmd.exe
// when Go invokes az.cmd.
func listOrgsCmd() tea.Cmd {
	return func() tea.Msg {
		const res = "499b84ac-1321-427f-aa17-267ca6975798" // Azure DevOps token audience
		id, err := run("az", "rest", "--resource", res,
			"--url", "https://app.vssps.visualstudio.com/_apis/profile/profiles/me",
			"--url-parameters", "api-version=7.1",
			"--query", "id", "-o", "tsv")
		if err != nil {
			return orgsMsg{err: err}
		}
		if id == "" {
			return orgsMsg{err: fmt.Errorf("讀不到 profile id")}
		}
		// NOTE: no JMESPath sort() here — parentheses in an arg get mangled by
		// cmd.exe when Go invokes az.cmd. Sort in Go instead.
		out, err := run("az", "rest", "--resource", res,
			"--url", "https://app.vssps.visualstudio.com/_apis/accounts",
			"--url-parameters", "memberId="+id, "api-version=7.1",
			"--query", "value[].accountName", "-o", "tsv")
		if err != nil {
			return orgsMsg{err: err}
		}
		var orgs []string
		for _, l := range strings.Split(out, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				orgs = append(orgs, l)
			}
		}
		sort.Strings(orgs)
		return orgsMsg{orgs: orgs}
	}
}

// listProjectsCmd fetches project names in the org.
func listProjectsCmd(org string) tea.Cmd {
	return func() tea.Msg {
		out, err := run("az", "devops", "project", "list",
			"--organization", org, "--query", "value[].name", "-o", "tsv")
		if err != nil {
			return projectsMsg{err: err}
		}
		var ps []string
		for _, l := range strings.Split(out, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				ps = append(ps, l)
			}
		}
		sort.Strings(ps)
		return projectsMsg{projects: ps}
	}
}

// listReposCmd fetches repos (with default branch) for a project.
func listReposCmd(org, project string) tea.Cmd {
	return func() tea.Msg {
		out, err := run("az", "repos", "list",
			"--organization", org, "--project", project,
			"--query", "[].[name,defaultBranch]", "-o", "tsv")
		if err != nil {
			return reposMsg{err: err}
		}
		var rs []repoInfo
		for _, l := range strings.Split(out, "\n") {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			cols := strings.Split(l, "\t")
			branch := "develop"
			if len(cols) > 1 && cols[1] != "" && cols[1] != "None" {
				branch = strings.TrimPrefix(cols[1], "refs/heads/")
			}
			rs = append(rs, repoInfo{name: cols[0], branch: branch})
		}
		return reposMsg{repos: rs}
	}
}

// listAreasCmd fetches the Area tree of a project and flattens it (root excluded).
func listAreasCmd(org, project string) tea.Cmd {
	return func() tea.Msg {
		raw, err := run("az", "boards", "area", "project", "list",
			"--organization", org, "--project", project, "--depth", "5", "-o", "json")
		if err != nil {
			return areasMsg{err: err}
		}
		var root areaNode
		if e := json.Unmarshal([]byte(raw), &root); e != nil {
			return areasMsg{err: e}
		}
		var areas []areaInfo
		flattenAreas(root, true, &areas)
		return areasMsg{areas: areas}
	}
}

func flattenAreas(n areaNode, isRoot bool, out *[]areaInfo) {
	if !isRoot {
		*out = append(*out, areaInfo{name: n.Name, path: normalizeAreaPath(n.Path)})
	}
	for _, c := range n.Children {
		flattenAreas(c, false, out)
	}
}

// normalizeAreaPath turns a classification-node path like "\ESHClouds\Area\1.Product\Chem"
// into the work item's System.AreaPath form "ESHClouds\1.Product\Chem".
func normalizeAreaPath(p string) string {
	p = strings.TrimPrefix(p, "\\")
	segs := strings.Split(p, "\\")
	if len(segs) >= 2 && segs[1] == "Area" {
		segs = append(segs[:1:1], segs[2:]...)
	}
	return strings.Join(segs, "\\")
}

// ---- work items ----

type wiRaw struct {
	Fields struct {
		Title     string `json:"System.Title"`
		Type      string `json:"System.WorkItemType"`
		State     string `json:"System.State"`
		Area      string `json:"System.AreaPath"`
		Iteration string `json:"System.IterationPath"`
		Assigned  struct {
			DisplayName string `json:"displayName"`
		} `json:"System.AssignedTo"`
	} `json:"fields"`
	Relations []struct {
		Rel string `json:"rel"`
		URL string `json:"url"`
	} `json:"relations"`
}

func showWorkItem(org string, id int) (workItem, error) {
	out, err := run("az", "boards", "work-item", "show",
		"--id", strconv.Itoa(id), "--organization", org, "--expand", "all", "-o", "json")
	if err != nil {
		return workItem{}, err
	}
	var raw wiRaw
	if e := json.Unmarshal([]byte(out), &raw); e != nil {
		return workItem{}, e
	}
	wi := workItem{
		id:        id,
		title:     raw.Fields.Title,
		typ:       raw.Fields.Type,
		state:     raw.Fields.State,
		area:      raw.Fields.Area,
		iteration: raw.Fields.Iteration,
		assigned:  raw.Fields.Assigned.DisplayName,
	}
	for _, rel := range raw.Relations {
		switch rel.Rel {
		case "System.LinkTypes.Hierarchy-Forward":
			if cid := lastIntSegment(rel.URL); cid > 0 {
				wi.childIDs = append(wi.childIDs, cid)
			}
		case "System.LinkTypes.Hierarchy-Reverse":
			if pid := lastIntSegment(rel.URL); pid > 0 {
				wi.parentID = pid
			}
		case "System.LinkTypes.Related":
			if rid := lastIntSegment(rel.URL); rid > 0 && wi.relatedID == 0 {
				wi.relatedID = rid
			}
		}
	}
	return wi, nil
}

type bindParentMsg struct {
	targetID  int
	targetTyp string
	err       error
}

// bindParentCmd 把 childID 綁到 targetID：查 target 的 type 決定 parent/related。
// Release 不在階層裡，用 related 綁；其餘一律 parent，合不合法交給 Azure 判。
func bindParentCmd(org string, childID, targetID int) tea.Cmd {
	return func() tea.Msg {
		if childID == targetID {
			return bindParentMsg{err: fmt.Errorf("不能綁自己 (#%d)", childID)}
		}
		t, err := showWorkItem(org, targetID)
		if err != nil {
			return bindParentMsg{err: err}
		}
		kind := "parent"
		if strings.EqualFold(t.typ, "Release") {
			kind = "related"
		}
		if _, e := run("az", "boards", "work-item", "relation", "add",
			"--id", strconv.Itoa(childID), "--relation-type", kind,
			"--target-id", strconv.Itoa(targetID), "--organization", org, "-o", "json"); e != nil {
			return bindParentMsg{err: e}
		}
		return bindParentMsg{targetID: targetID, targetTyp: t.typ}
	}
}

type parentInfoMsg struct {
	ownerID  int
	parentID int
	typ      string
	title    string
}

// loadParentCmd 查某節點父層的 type/title（延遲載入，供步驟一顯示）。ownerID 用來
// 比對回來時是否仍是當前節點，避免鑽層後更新到別層。
func loadParentCmd(org string, ownerID, parentID int) tea.Cmd {
	return func() tea.Msg {
		p, err := showWorkItem(org, parentID)
		if err != nil {
			return parentInfoMsg{ownerID: ownerID}
		}
		return parentInfoMsg{ownerID: ownerID, parentID: parentID, typ: p.typ, title: p.title}
	}
}

func lastIntSegment(u string) int {
	i := strings.LastIndexByte(u, '/')
	if i < 0 {
		return 0
	}
	n, _ := strconv.Atoi(u[i+1:])
	return n
}

// fetchWorkItemMetaCmd loads just the work item (one call, fast). The Task-layer
// drill-down (PowerShell Get-Relations-Detail) then runs child-by-child via
// loadChildCmd so the UI can stream progress instead of freezing on a spinner.
func fetchWorkItemMetaCmd(org string, id int) tea.Cmd {
	return func() tea.Msg {
		wi, err := showWorkItem(org, id)
		return workItemMetaMsg{wi: wi, err: err}
	}
}

// loadChildCmd loads one child work item. rest = sibling ids still to load;
// parent = the node these children belong to.
func loadChildCmd(org string, id int, rest []int, parent workItem) tea.Cmd {
	return func() tea.Msg {
		wi, err := showWorkItem(org, id)
		return childOneMsg{child: wi, rest: rest, parent: parent, err: err}
	}
}

type taskCreatedMsg struct {
	created []workItem
	failed  []string
}

// createTasksCmd creates one Task per title under parentID: it inherits the
// parent's Area/Iteration, is assigned to the current az user, and is linked as
// a child of the parent (mirrors the PowerShell New-Task). A failed link is
// non-fatal (the Task still exists); a failed create is collected in `failed`.
func createTasksCmd(org, project string, parentID int, area, iteration string, titles []string) tea.Cmd {
	return func() tea.Msg {
		user, _ := run("az", "account", "show", "--query", "user.name", "-o", "tsv")
		user = strings.TrimSpace(user)

		var created []workItem
		var failed []string
		for _, title := range titles {
			args := []string{"boards", "work-item", "create",
				"--type", "Task", "--title", title,
				"--project", project, "--organization", org, "-o", "json"}
			if user != "" {
				args = append(args, "--assigned-to", user)
			}
			var fields []string
			if area != "" {
				fields = append(fields, "System.AreaPath="+area)
			}
			if iteration != "" {
				fields = append(fields, "System.IterationPath="+iteration)
			}
			if len(fields) > 0 {
				args = append(args, "--fields")
				args = append(args, fields...)
			}
			out, err := run("az", args...)
			if err != nil {
				failed = append(failed, title)
				continue
			}
			var raw struct {
				ID int `json:"id"`
			}
			if json.Unmarshal([]byte(out), &raw) != nil || raw.ID == 0 {
				failed = append(failed, title)
				continue
			}
			// link the new Task as a child of the parent (non-fatal on failure).
			// parentID<=0 表示獨立 Task（不綁父單），跳過。
			if parentID > 0 {
				run("az", "boards", "work-item", "relation", "add",
					"--id", strconv.Itoa(parentID), "--relation-type", "child",
					"--target-id", strconv.Itoa(raw.ID), "--organization", org, "-o", "json")
			}
			created = append(created, workItem{id: raw.ID, title: title, typ: "Task", state: "New"})
		}
		return taskCreatedMsg{created: created, failed: failed}
	}
}

// ---- branches ----

type refInfo struct {
	name     string // e.g. "refs/heads/develop"
	objectID string
}

type refsMsg struct {
	refs []refInfo
	err  error
}

type branchMsg struct {
	branch string
	err    error
}

func listRefsCmd(org, project, repo string) tea.Cmd {
	return func() tea.Msg {
		out, err := run("az", "repos", "ref", "list",
			"--repository", repo, "--project", project, "--organization", org,
			"--filter", "heads", "-o", "json")
		if err != nil {
			return refsMsg{err: err}
		}
		var raw []struct {
			Name     string `json:"name"`
			ObjectID string `json:"objectId"`
		}
		if e := json.Unmarshal([]byte(out), &raw); e != nil {
			return refsMsg{err: e}
		}
		var refs []refInfo
		for _, r := range raw {
			refs = append(refs, refInfo{name: r.Name, objectID: r.ObjectID})
		}
		return refsMsg{refs: refs}
	}
}

// createBranchCmd creates refs/heads/<branch> from baseObjectID (server-side, no local git).
func createBranchCmd(org, project, repo, branch, baseObjectID string) tea.Cmd {
	return func() tea.Msg {
		_, err := run("az", "repos", "ref", "create",
			"--name", "refs/heads/"+branch, "--object-id", baseObjectID,
			"--repository", repo, "--project", project, "--organization", org, "-o", "json")
		if err != nil {
			return branchMsg{err: err}
		}
		return branchMsg{branch: branch}
	}
}

// ---- local repo: commit check + clone ----

type commitsMsg struct {
	commits string // "- msg\n- msg" for the PR description
	count   int
	missing bool // source branch not pushed to origin yet
	err     error
}

type cloneMsg struct {
	path string
	err  error
}

// gitIn runs git in a repo. Like run() it surfaces the first line of stderr on
// failure — without that, callers only get "exit status 128", which tells nobody
// (least of all a headless run with no human to guess) what actually went wrong.
func gitIn(path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", path}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("git %s 執行逾時（超過 %s）", strings.Join(args, " "), cmdTimeout())
	}
	if err != nil {
		if e := strings.TrimSpace(stderr.String()); e != "" {
			dbg("GIT FAIL: git -C %s %s\n  err=%v\n  stderr=%s", path, strings.Join(args, " "), err, e)
			return "", fmt.Errorf("%s", firstLine(e))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// commitCheckCmd reads git log origin/<base>..origin/<branch> for the PR description.
func commitCheckCmd(path, base, branch string) tea.Cmd {
	return func() tea.Msg {
		if _, err := gitIn(path, "fetch", "origin"); err != nil {
			return commitsMsg{err: fmt.Errorf("git fetch 失敗: %v", err)}
		}
		rb, _ := gitIn(path, "branch", "-r", "--list", "origin/"+branch)
		if strings.TrimSpace(rb) == "" {
			return commitsMsg{missing: true}
		}
		out, err := gitIn(path, "log", "origin/"+base+"..origin/"+branch,
			"--oneline", "--no-merges", "--encoding=UTF-8")
		if err != nil {
			return commitsMsg{err: err}
		}
		var lines []string
		for _, l := range strings.Split(out, "\n") {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			if i := strings.IndexByte(l, ' '); i >= 0 {
				l = strings.TrimSpace(l[i+1:]) // strip the short hash
			}
			if strings.HasPrefix(l, "Merged PR") || strings.HasPrefix(l, "Merge branch") {
				continue
			}
			lines = append(lines, "- "+l)
		}
		return commitsMsg{commits: strings.Join(lines, "\n"), count: len(lines)}
	}
}

// cloneCmd clones an Azure DevOps repo into dest.
func cloneCmd(org, project, repo, dest string) tea.Cmd {
	return func() tea.Msg {
		u := strings.TrimRight(org, "/") + "/" + url.PathEscape(project) + "/_git/" + url.PathEscape(repo)
		cmd := exec.Command("git", "clone", u, dest)
		var se strings.Builder
		cmd.Stderr = &se
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(se.String())
			if msg != "" {
				return cloneMsg{err: fmt.Errorf("%s", firstLine(msg))}
			}
			return cloneMsg{err: err}
		}
		return cloneMsg{path: dest}
	}
}

// writeConfigCmd writes a vl-compatible config.json to the per-user config path
// (%AppData%\very-lazy\config.json), creating the directory if needed, so the
// installed binary can read it back from any working directory. The Slack bot
// token (a secret) goes to config.local.json next to it, never into config.json.
func writeConfigCmd(org, wip string, maps []mappingEntry, slackChannel string, slackMembers []slackMember, slackToken string) tea.Cmd {
	return func() tea.Msg {
		mappings := map[string]any{}
		for _, mp := range maps {
			entry := map[string]any{
				"azureProject":    mp.project,
				"azureRepository": mp.repo,
				"defaultBranch":   mp.branch,
			}
			if mp.areaPath != "" {
				entry["areaPath"] = mp.areaPath
			}
			if mp.alias != "" {
				entry["aliases"] = []any{mp.alias}
			}
			mappings[mp.key] = entry
		}
		members := make([]any, 0, len(slackMembers))
		for _, sm := range slackMembers {
			members = append(members, map[string]any{"key": sm.Key, "value": sm.Value})
		}
		dest := userConfigPath()
		existing := map[string]any{}
		if b, e := os.ReadFile(dest); e == nil {
			json.Unmarshal(b, &existing)
		}
		cfg := mergeVLConfig(existing, org, wip, mappings, slackChannel, members)
		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return writtenMsg{err: err}
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return writtenMsg{err: err}
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return writtenMsg{err: err}
		}
		// the token is a secret -> config.local.json (gitignored), next to config.json
		if slackToken != "" {
			local, _ := json.MarshalIndent(map[string]any{"slackToken": slackToken}, "", "  ")
			os.WriteFile(filepath.Join(filepath.Dir(dest), "config.local.json"), local, 0o600)
		}
		abs, _ := filepath.Abs(dest)
		return writtenMsg{path: abs}
	}
}

// mergeVLConfig updates only the fields the wizard manages, preserving everything
// else in the existing config (projectPaths, and each mapping's localPath) so
// re-running /init never wipes them.
func mergeVLConfig(existing map[string]any, org, wip string, mappings map[string]any, slackChannel string, members []any) map[string]any {
	cfg := existing
	if cfg == nil {
		cfg = map[string]any{}
	}
	if oldMaps, ok := cfg["azureProjectMappings"].(map[string]any); ok {
		for key, entry := range mappings {
			old, ok1 := oldMaps[key].(map[string]any)
			em, ok2 := entry.(map[string]any)
			if ok1 && ok2 {
				if lp, ok := old["localPath"]; ok {
					em["localPath"] = lp
				}
			}
		}
	}
	cfg["azureOrg"] = org
	cfg["workItemProject"] = wip
	cfg["azureProjectMappings"] = mappings
	cfg["slackConfig"] = map[string]any{"channel": slackChannel, "members": members}
	if _, ok := cfg["projectPaths"]; !ok {
		cfg["projectPaths"] = map[string]any{}
	}
	return cfg
}

type projectPathSavedMsg struct {
	key  string
	path string
	err  error
}

// saveProjectPathCmd 把某個對應的本機路徑寫進 config 的 projectPaths（保留其餘設定），
// 讓 /release、/hotfix 之後讀得到。/task 填現有資料夾或 clone 完成時呼叫。
func saveProjectPathCmd(mapKey, localPath string) tea.Cmd {
	return func() tea.Msg {
		dest := userConfigPath()
		existing := map[string]any{}
		if b, e := os.ReadFile(dest); e == nil {
			json.Unmarshal(b, &existing)
		}
		pp, _ := existing["projectPaths"].(map[string]any)
		if pp == nil {
			pp = map[string]any{}
		}
		pp[mapKey] = localPath
		existing["projectPaths"] = pp
		data, err := json.MarshalIndent(existing, "", "  ")
		if err != nil {
			return projectPathSavedMsg{err: err}
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return projectPathSavedMsg{err: err}
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return projectPathSavedMsg{err: err}
		}
		return projectPathSavedMsg{key: mapKey, path: localPath}
	}
}

func sanitizeKey(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "app"
	}
	return b.String()
}
