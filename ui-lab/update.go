package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	latestReleaseAPI  = "https://api.github.com/repos/linshfu/workitem-to-pr/releases/latest"
	binaryDownloadURL = "https://github.com/linshfu/workitem-to-pr/releases/latest/download/vlui.exe"
)

// updateMsg carries the latest release tag from an on-demand check.
// latest is "" if the check failed (offline / rate-limited / API error).
type updateMsg struct{ latest string }

// updateDoneMsg is the result of running /update (download + self-replace).
type updateDoneMsg struct{ err error }

// checkUpdateCmd asks GitHub for the latest release tag. It runs only when the
// user invokes /update (no background/startup check). Fail-silent: any error
// yields latest="" so the flow can say "couldn't check" instead of crashing.
func checkUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		client := &http.Client{Timeout: 6 * time.Second}
		req, err := http.NewRequest("GET", latestReleaseAPI, nil)
		if err != nil {
			return updateMsg{}
		}
		req.Header.Set("User-Agent", "very-lazy-cli")
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := client.Do(req)
		if err != nil {
			return updateMsg{}
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return updateMsg{}
		}
		var body struct {
			TagName string `json:"tag_name"`
		}
		if json.NewDecoder(resp.Body).Decode(&body) != nil {
			return updateMsg{}
		}
		return updateMsg{latest: strings.TrimSpace(body.TagName)}
	}
}

// doUpdateCmd downloads the latest binary next to the running exe and swaps it in
// with the Windows-safe rename dance: a running .exe can be renamed but not
// overwritten, so we move the current one aside and rename the new one into place.
// On failure it rolls back, so the working exe is never lost.
func doUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		exe, err := os.Executable()
		if err != nil {
			return updateDoneMsg{err: err}
		}
		if resolved, e := filepath.EvalSymlinks(exe); e == nil && resolved != "" {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		newPath := filepath.Join(dir, "vlui.new.exe")
		oldPath := filepath.Join(dir, "vlui.old.exe")

		if err := downloadFile(binaryDownloadURL, newPath); err != nil {
			os.Remove(newPath)
			return updateDoneMsg{err: err}
		}
		if fi, err := os.Stat(newPath); err != nil || fi.Size() == 0 {
			os.Remove(newPath)
			return updateDoneMsg{err: errors.New("下載的檔案是空的")}
		}

		os.Remove(oldPath) // clear any stale .old
		if err := os.Rename(exe, oldPath); err != nil {
			os.Remove(newPath)
			return updateDoneMsg{err: err}
		}
		if err := os.Rename(newPath, exe); err != nil {
			os.Rename(oldPath, exe) // rollback
			return updateDoneMsg{err: err}
		}
		return updateDoneMsg{}
	}
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 90 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "very-lazy-cli")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("下載失敗：HTTP " + resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// cleanupOldBinary best-effort removes leftovers from a prior /update.
func cleanupOldBinary() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, e := filepath.EvalSymlinks(exe); e == nil && resolved != "" {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	os.Remove(filepath.Join(dir, "vlui.old.exe"))
	os.Remove(filepath.Join(dir, "vlui.new.exe"))
}

// enterUpdate starts the /update flow (download + self-replace).
func (m *model) enterUpdate() tea.Cmd {
	m.mode = modeUpdate
	m.upStatus = "檢查最新版本…"
	m.upErr = ""
	m.upDone = false
	m.upToDate = false
	m.latestVer = ""
	m.loading = true
	m.input.SetValue("")
	m.input.Placeholder = ""
	return checkUpdateCmd()
}

func (m model) updateUpdate(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.loading && !m.upDone && m.upErr == "" {
		return m, nil // updating in progress; wait it out
	}
	switch key.String() {
	case "esc", "enter":
		m.mode = modeHome
		m.upDone = false
		m.input.Placeholder = homePlaceholder
		return m, nil
	}
	return m, nil
}

func (m model) viewUpdate() string {
	var body strings.Builder
	body.WriteString(styleBold(accent, "更新 very-lazy") + "\n\n")
	body.WriteString(styleFg(muted, "目前 ") + version + "\n")
	if m.latestVer != "" {
		body.WriteString(styleFg(muted, "最新 ") + m.latestVer + "\n")
	}
	body.WriteString("\n")
	switch {
	case m.upErr != "":
		body.WriteString(styleFg(errCol, "⚠ "+m.upErr))
	case m.upDone && m.upToDate:
		body.WriteString(styleFg(okCol, "✓ 已經是最新版"))
	case m.upDone:
		body.WriteString(styleFg(okCol, "✓ 更新完成") + "\n" + styleFg(muted, "重開一次就會是新版。"))
	default:
		body.WriteString(m.spin.View() + " " + styleFg(muted, m.upStatus))
	}

	content := strings.TrimRight(body.String(), "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dim).
		Padding(0, 2).
		Render(content)
	hint := "⏎ 返回"
	if !m.upDone && m.upErr == "" {
		hint = "請稍候…"
	}
	return m.banner() + "\n" + box + "\n" + m.hintbar(hint)
}
