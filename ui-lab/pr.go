package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ---- PR + reviewer + Slack (the tail of the /task flow) ----

type reviewer struct {
	email   string
	slackID string // "" if not mapped to a Slack member
	name    string
}

type reviewersMsg struct {
	list []reviewer
	err  error
}

type prCreatedMsg struct {
	id          int
	url         string
	title       string
	reviewerErr string
	err         error
}

type slackDoneMsg struct {
	ok  bool
	err error
}

// slackIDFor finds a member's Slack id by matching the email's username against
// the member key (as the PowerShell Select-Reviewer does).
func slackIDFor(email string, members []slackMember) string {
	user := email
	if i := strings.IndexByte(email, '@'); i >= 0 {
		user = email[:i]
	}
	user = strings.ToLower(user)
	for _, m := range members {
		if m.Key != "" && strings.Contains(strings.ToLower(m.Key), user) {
			return m.Value
		}
	}
	return ""
}

// listReviewersCmd lists the code project's team members as reviewer candidates,
// excluding the current user. When Slack members are configured it keeps only
// those we can notify (matched to a Slack id); otherwise it keeps everyone.
func listReviewersCmd(org, project string, members []slackMember) tea.Cmd {
	return func() tea.Msg {
		self, _ := run("az", "account", "show", "--query", "user.name", "-o", "tsv")
		self = strings.TrimSpace(self)

		teamsOut, err := run("az", "devops", "team", "list", "--organization", org, "--project", project, "-o", "json")
		if err != nil {
			return reviewersMsg{err: err}
		}
		var teams []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(teamsOut), &teams) != nil {
			return reviewersMsg{err: errors.New("解析團隊清單失敗")}
		}

		seen := map[string]bool{}
		var out []reviewer
		for _, t := range teams {
			mOut, e := run("az", "devops", "team", "list-member",
				"--team", t.Name, "--organization", org, "--project", project, "-o", "json")
			if e != nil {
				continue
			}
			var mem []struct {
				Identity struct {
					UniqueName  string `json:"uniqueName"`
					DisplayName string `json:"displayName"`
				} `json:"identity"`
			}
			if json.Unmarshal([]byte(mOut), &mem) != nil {
				continue
			}
			for _, mm := range mem {
				email := strings.TrimSpace(mm.Identity.UniqueName)
				if email == "" || seen[email] || strings.EqualFold(email, self) {
					continue
				}
				slackID := slackIDFor(email, members)
				if len(members) > 0 && slackID == "" {
					continue // members configured -> only reviewers we can notify on Slack
				}
				seen[email] = true
				out = append(out, reviewer{email: email, slackID: slackID, name: mm.Identity.DisplayName})
			}
		}
		return reviewersMsg{list: out}
	}
}

// createPRCmd creates the PR (linking all work items, transitioning them, deleting
// the source branch on merge) and, if a reviewer is given, adds them as required
// and turns on auto-complete. Mirrors the PowerShell Start-Pr.
func createPRCmd(org, project, repo, source, target, title, desc string, workItems []int, rev reviewer) tea.Cmd {
	return func() tea.Msg {
		args := []string{"repos", "pr", "create",
			"--organization", org, "--project", project, "--repository", repo,
			"--source-branch", source, "--target-branch", target,
			"--title", title, "--description", desc,
			"--transition-work-items", "true", "--delete-source-branch", "true", "-o", "json"}
		if len(workItems) > 0 {
			args = append(args, "--work-items")
			for _, id := range workItems {
				args = append(args, strconv.Itoa(id))
			}
		}
		out, err := run("az", args...)
		if err != nil {
			return prCreatedMsg{err: err}
		}
		var pr struct {
			PullRequestID int    `json:"pullRequestId"`
			Title         string `json:"title"`
		}
		if json.Unmarshal([]byte(out), &pr) != nil || pr.PullRequestID == 0 {
			return prCreatedMsg{err: errors.New("建立 PR 失敗：沒有 pullRequestId")}
		}
		link := strings.TrimRight(org, "/") + "/" + url.PathEscape(project) +
			"/_git/" + url.PathEscape(repo) + "/pullrequest/" + strconv.Itoa(pr.PullRequestID)

		revErr := ""
		if rev.email != "" {
			if _, e := run("az", "repos", "pr", "reviewer", "add",
				"--id", strconv.Itoa(pr.PullRequestID), "--reviewers", rev.email,
				"--required", "--organization", org, "-o", "json"); e != nil {
				revErr = e.Error()
			} else {
				run("az", "repos", "pr", "update",
					"--id", strconv.Itoa(pr.PullRequestID), "--auto-complete", "true",
					"--organization", org, "-o", "json")
			}
		}
		return prCreatedMsg{id: pr.PullRequestID, url: link, title: pr.Title, reviewerErr: revErr}
	}
}

// prResultText is the Slack-friendly summary (matches the PowerShell format).
func prResultText(project, target, link, title string, id int) string {
	return "[" + project + "] -> " + target + "\n<" + link + "|Pull Request " + strconv.Itoa(id) + ">: " + title
}

// slackNotifyCmd posts the PR to the review channel, @-mentioning the reviewer.
func slackNotifyCmd(token, channel, slackID, prResult string) tea.Cmd {
	return func() tea.Msg {
		text := prResult
		if slackID != "" {
			text = "<@" + slackID + "> Please help review\n" + prResult
		}
		payload, _ := json.Marshal(map[string]string{"channel": channel, "text": text})
		req, err := http.NewRequest("POST", "https://slack.com/api/chat.postMessage", bytes.NewReader(payload))
		if err != nil {
			return slackDoneMsg{err: err}
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return slackDoneMsg{err: err}
		}
		defer resp.Body.Close()
		var r struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&r)
		if !r.OK {
			return slackDoneMsg{err: errors.New(r.Error)}
		}
		return slackDoneMsg{ok: true}
	}
}

// openURLCmd opens a link in the default browser (fire-and-forget).
func openURLCmd(link string) tea.Cmd {
	return func() tea.Msg {
		exec.Command("cmd", "/c", "start", "", link).Start()
		return nil
	}
}

// ---- Slack setup (used by /init) ----

type slackAuthMsg struct {
	team string
	err  error
}

type slackMembersMsg struct {
	members []slackMember
	err     error
}

func slackGet(token, url string, out any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// buildManifest is the app manifest (JSON, matching the dialog's default tab)
// for the "From a manifest" flow. features.bot_user is required whenever bot
// scopes are declared ("Oauth requires bot_user"); its display_name is
// personalized so several people can each have their own app.
func buildManifest(botName string) string {
	return `{
  "display_information": {
    "name": "very-lazy"
  },
  "features": {
    "bot_user": {
      "display_name": "` + botName + `",
      "always_online": false
    }
  },
  "oauth_config": {
    "scopes": {
      "bot": [
        "users:read",
        "users:read.email",
        "channels:read",
        "channels:join",
        "channels:manage",
        "groups:read",
        "chat:write"
      ]
    }
  },
  "settings": {
    "org_deploy_enabled": false,
    "socket_mode_enabled": false
  }
}
`
}

// slackBotName is "<az-username>-testbot", e.g. sean@wishingsoft.com -> sean-testbot.
func slackBotName() string {
	user, _ := run("az", "account", "show", "--query", "user.name", "-o", "tsv")
	user = strings.TrimSpace(user)
	if i := strings.IndexByte(user, '@'); i >= 0 {
		user = user[:i]
	}
	if user == "" {
		return "very-lazy-bot"
	}
	return user + "-testbot"
}

type manifestMsg struct{ ok bool }

// copyManifestCmd copies the JSON manifest to the clipboard (built-in clip.exe,
// no dependency) so the user can paste it straight into Slack. No file needed.
func copyManifestCmd() tea.Cmd {
	return func() tea.Msg {
		clip := exec.Command("clip")
		clip.Stdin = strings.NewReader(buildManifest(slackBotName()))
		if clip.Run() != nil {
			return manifestMsg{}
		}
		return manifestMsg{ok: true}
	}
}

// validateSlackTokenCmd checks a bot token via auth.test.
func validateSlackTokenCmd(token string) tea.Cmd {
	return func() tea.Msg {
		var r struct {
			OK    bool   `json:"ok"`
			Team  string `json:"team"`
			Error string `json:"error"`
		}
		if err := slackGet(token, "https://slack.com/api/auth.test", &r); err != nil {
			return slackAuthMsg{err: err}
		}
		if !r.OK {
			return slackAuthMsg{err: errors.New(r.Error)}
		}
		return slackAuthMsg{team: r.Team}
	}
}

type slackChannel struct {
	id      string
	name    string
	private bool
}

type channelsMsg struct {
	channels []slackChannel
	err      error
}

func slackPost(token, apiURL string, body []byte, out any) error {
	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// slackUserIndex fetches users.list once and indexes it by id and by (lowercased)
// email. Email is only present when the token has the users:read.email scope.
func slackUserIndex(token string) (byID map[string]slackMember, byEmail map[string]string, err error) {
	var users struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		Members []struct {
			ID      string `json:"id"`
			Deleted bool   `json:"deleted"`
			Profile struct {
				Display string `json:"display_name"`
				Real    string `json:"real_name"`
				Email   string `json:"email"`
			} `json:"profile"`
		} `json:"members"`
	}
	if e := slackGet(token, "https://slack.com/api/users.list?limit=1000", &users); e != nil {
		return nil, nil, e
	}
	if !users.OK {
		return nil, nil, errors.New(users.Error)
	}
	byID = map[string]slackMember{}
	byEmail = map[string]string{}
	for _, u := range users.Members {
		if u.Deleted || u.Profile.Display == "" {
			continue
		}
		name := u.Profile.Display
		if u.Profile.Real != "" {
			name = u.Profile.Display + " (" + u.Profile.Real + ")"
		}
		byID[u.ID] = slackMember{Key: name, Value: u.ID}
		if u.Profile.Email != "" {
			byEmail[strings.ToLower(u.Profile.Email)] = u.ID
		}
	}
	return byID, byEmail, nil
}

// listChannelsCmd lists channels the bot can see. Public and private are queried
// separately so a missing private scope (groups:read) doesn't hide the public
// channels — and if a call fails, the exact missing scope is surfaced.
func listChannelsCmd(token string) tea.Cmd {
	return func() tea.Msg {
		var all []slackChannel
		got := false
		var lastErr, lastNeeded string
		for _, typ := range []string{"public_channel", "private_channel"} {
			var r struct {
				OK       bool   `json:"ok"`
				Error    string `json:"error"`
				Needed   string `json:"needed"`
				Channels []struct {
					ID        string `json:"id"`
					Name      string `json:"name"`
					IsPrivate bool   `json:"is_private"`
				} `json:"channels"`
			}
			if err := slackGet(token, "https://slack.com/api/conversations.list?types="+typ+"&exclude_archived=true&limit=1000", &r); err != nil {
				lastErr = err.Error()
				continue
			}
			if !r.OK {
				lastErr, lastNeeded = r.Error, r.Needed
				continue
			}
			got = true
			for _, c := range r.Channels {
				all = append(all, slackChannel{id: c.ID, name: c.Name, private: c.IsPrivate})
			}
		}
		if !got {
			msg := lastErr
			if lastNeeded != "" {
				msg += "（缺 scope：" + lastNeeded + " → 到 App 的 OAuth & Permissions 加上並 Reinstall）"
			}
			return channelsMsg{err: errors.New(msg)}
		}
		sort.Slice(all, func(i, j int) bool { return all[i].name < all[j].name })
		return channelsMsg{channels: all}
	}
}

// channelMembersCmd joins the (public) channel then returns its members mapped
// to Slack ids. A join failure is non-fatal (already in / private channel).
func channelMembersCmd(token, channelID string) tea.Cmd {
	return func() tea.Msg {
		byID, _, err := slackUserIndex(token)
		if err != nil {
			return slackMembersMsg{err: err}
		}
		jp, _ := json.Marshal(map[string]any{"channel": channelID})
		var jr struct{ OK bool `json:"ok"` }
		slackPost(token, "https://slack.com/api/conversations.join", jp, &jr)

		var cm struct {
			OK      bool     `json:"ok"`
			Error   string   `json:"error"`
			Members []string `json:"members"`
		}
		if err := slackGet(token, "https://slack.com/api/conversations.members?channel="+channelID+"&limit=1000", &cm); err != nil {
			return slackMembersMsg{err: err}
		}
		if !cm.OK {
			return slackMembersMsg{err: errors.New(cm.Error)}
		}
		var out []slackMember
		for _, id := range cm.Members {
			if sm, ok := byID[id]; ok {
				out = append(out, sm)
			}
		}
		if len(out) == 0 {
			return slackMembersMsg{err: errors.New("頻道裡沒有可用成員")}
		}
		return slackMembersMsg{members: out}
	}
}

// azTeamMembers gathers unique team members (email + display name) across the
// given code projects' teams — the reviewer pool to seed a new channel from.
func azTeamMembers(org string, projects []string) []struct{ email, name string } {
	seen := map[string]bool{}
	var out []struct{ email, name string }
	for _, proj := range projects {
		teamsOut, err := run("az", "devops", "team", "list", "--organization", org, "--project", proj, "-o", "json")
		if err != nil {
			continue
		}
		var teams []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(teamsOut), &teams) != nil {
			continue
		}
		for _, t := range teams {
			mOut, e := run("az", "devops", "team", "list-member", "--team", t.Name, "--organization", org, "--project", proj, "-o", "json")
			if e != nil {
				continue
			}
			var mem []struct {
				Identity struct {
					UniqueName  string `json:"uniqueName"`
					DisplayName string `json:"displayName"`
				} `json:"identity"`
			}
			if json.Unmarshal([]byte(mOut), &mem) != nil {
				continue
			}
			for _, mm := range mem {
				email := strings.TrimSpace(mm.Identity.UniqueName)
				if email == "" || seen[email] {
					continue
				}
				seen[email] = true
				out = append(out, struct{ email, name string }{email, mm.Identity.DisplayName})
			}
		}
	}
	return out
}

// createChannelWithTeamCmd creates a channel, then invites the code-project team
// members that match a Slack account by email, and returns them as the mapping.
func createChannelWithTeamCmd(token, org, channelName string, projects []string) tea.Cmd {
	return func() tea.Msg {
		byID, byEmail, err := slackUserIndex(token)
		if err != nil {
			return slackMembersMsg{err: err}
		}
		cp, _ := json.Marshal(map[string]any{"name": channelName})
		var cr struct {
			OK      bool   `json:"ok"`
			Error   string `json:"error"`
			Channel struct {
				ID string `json:"id"`
			} `json:"channel"`
		}
		if e := slackPost(token, "https://slack.com/api/conversations.create", cp, &cr); e != nil {
			return slackMembersMsg{err: e}
		}
		if !cr.OK {
			return slackMembersMsg{err: errors.New("建頻道失敗：" + cr.Error)}
		}

		var members []slackMember
		var inviteIDs []string
		for _, tm := range azTeamMembers(org, projects) {
			id, ok := byEmail[strings.ToLower(tm.email)]
			if !ok {
				continue
			}
			inviteIDs = append(inviteIDs, id)
			if sm, ok := byID[id]; ok {
				members = append(members, sm)
			} else {
				members = append(members, slackMember{Key: tm.name, Value: id})
			}
		}
		if len(inviteIDs) > 0 {
			ip, _ := json.Marshal(map[string]any{"channel": cr.Channel.ID, "users": strings.Join(inviteIDs, ",")})
			var inv struct{ OK bool `json:"ok"` }
			slackPost(token, "https://slack.com/api/conversations.invite", ip, &inv) // non-fatal
		}
		if len(members) == 0 {
			return slackMembersMsg{err: errors.New("AZ 團隊沒有人對到 Slack（確認 email 一致、token 有 users:read.email）")}
		}
		return slackMembersMsg{members: members}
	}
}

// slackConfigured reports whether a Slack notification can be sent.
func (m model) slackConfigured() bool {
	return m.cfg.SlackToken != "" && m.cfg.Slack.Channel != ""
}

func reviewerLabel(r reviewer) string {
	if r.name != "" {
		return r.name + " (" + r.email + ")"
	}
	return r.email
}
