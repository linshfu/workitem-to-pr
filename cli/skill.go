package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ---- AI 使用指南（skill）的散佈：內嵌在 binary 裡，隨版本走 ----
//
// SKILL.md 是給 AI 助手（Claude Code 等）看的操作指南。內嵌進 binary 的用意是
// 讓「指南版本」永遠跟「binary 行為」一致：/update 換了 binary，指南跟著新，
// 不會出現文件講舊行為的漂移。
//
//   --install-skill        寫到 Claude Code 的使用者層級 skills 目錄（~\.claude\skills\）
//   --export-skill <目錄>   吐出原始檔，給用其他 AI 的人交給自己的 AI 安置
//
// 另外每次啟動會靜默刷新已安裝的副本（只碰帶 managed 標記的檔案，手動改過的不動）。

//go:embed skill/vlui-headless/SKILL.md
var embeddedSkillMD string

//go:embed skill/vlui-headless/USAGE-PROMPTS.md
var embeddedUsageMD string

const skillDirName = "vlui-headless"

// skillMarker 讓自動刷新能認出「這份是 vlui 裝的」——沒有這行的檔案一律不覆寫。
const skillMarker = "<!-- managed by vlui; update 後會自動刷新，手動修改請先移除此行 -->"

// managedSkillFiles 是要安裝的檔名 -> 內容（已加標記、統一 LF——本機 checkout 是
// CRLF、CI 是 LF，不統一的話兩種來源 build 出的 binary 會彼此把對方的輸出當「不同」
// 反覆重寫）。
func managedSkillFiles() map[string]string {
	norm := func(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
	withMarker := func(s string) string {
		return strings.TrimRight(norm(s), "\n") + "\n\n" + skillMarker + "\n"
	}
	return map[string]string{
		"SKILL.md":         withMarker(embeddedSkillMD),
		"USAGE-PROMPTS.md": withMarker(embeddedUsageMD),
	}
}

// claudeSkillDir 是 Claude Code 使用者層級的 skill 目錄。~\.claude 不存在就視為
// 這台沒在用 Claude Code（不硬建目錄，改用 --export-skill 交給該機器的 AI 自己安置）。
func claudeSkillDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	claude := filepath.Join(home, ".claude")
	if fi, err := os.Stat(claude); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("找不到 %s（這台沒裝 Claude Code？）；其他 AI 請改用 --export-skill <目錄>，把吐出的檔案交給你的 AI 放到它會生效的位置", claude)
	}
	return filepath.Join(claude, "skills", skillDirName), nil
}

// installSkillTo 把帶標記的指南寫進 dir（會建目錄）。
func installSkillTo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, content := range managedSkillFiles() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// exportSkillTo 吐出「不帶標記」的原始檔——給非 Claude 的 AI 用，標記的自動刷新
// 承諾在那些位置不成立，留著只會誤導。
func exportSkillTo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	norm := func(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
	files := map[string]string{
		"SKILL.md":         norm(embeddedSkillMD),
		"USAGE-PROMPTS.md": norm(embeddedUsageMD),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// refreshSkillAt 是啟動時的靜默同步：目錄裡已有「帶標記」的 SKILL.md 且內容跟
// 內嵌版不同才重寫。沒裝過（檔案不存在）或使用者自己改過（沒有標記）都不動。
func refreshSkillAt(dir string) {
	existing, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil || !strings.Contains(string(existing), "managed by vlui") {
		return
	}
	files := managedSkillFiles()
	if string(existing) == files["SKILL.md"] {
		return
	}
	if err := installSkillTo(dir); err != nil {
		dbg("skill refresh failed: %v", err)
	}
}

// autoRefreshSkill 在每次啟動（TUI 與 headless 都是）跑一次，失敗不吭聲。
func autoRefreshSkill() {
	dir, err := claudeSkillDir()
	if err != nil {
		return
	}
	refreshSkillAt(dir)
}

// installSkillNote 給 /init 完成畫面用：裝得成就裝，裝不成回一行說明（不擋流程）。
func installSkillNote() string {
	dir, err := claudeSkillDir()
	if err != nil {
		return "AI 使用指南未安裝：" + err.Error()
	}
	if err := installSkillTo(dir); err != nil {
		return "AI 使用指南安裝失敗：" + err.Error()
	}
	return "AI 使用指南（skill）已裝到 " + dir
}

// handleSkillFlags 處理 --install-skill / --export-skill。這兩個是獨立的一次性指令，
// 在進 TUI / headless 之前攔截。回傳 (exit code, 有沒有攔到)。
func handleSkillFlags(args []string) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	switch {
	case args[0] == "--install-skill":
		dir, err := claudeSkillDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "install-skill 失敗:", err)
			return 1, true
		}
		if err := installSkillTo(dir); err != nil {
			fmt.Fprintln(os.Stderr, "install-skill 失敗:", err)
			return 1, true
		}
		fmt.Println("AI 使用指南已安裝到 " + dir)
		fmt.Println("之後 /update 換版時會自動刷新這份指南（手動改過的不會被覆寫）。")
		return 0, true
	case args[0] == "--export-skill" || strings.HasPrefix(args[0], "--export-skill="):
		dir := strings.TrimPrefix(args[0], "--export-skill=")
		if dir == "--export-skill" || dir == "" {
			if len(args) >= 2 {
				dir = args[1]
			} else {
				dir = "vlui-skill"
			}
		}
		if err := exportSkillTo(dir); err != nil {
			fmt.Fprintln(os.Stderr, "export-skill 失敗:", err)
			return 1, true
		}
		abs, _ := filepath.Abs(dir)
		fmt.Println("AI 使用指南已輸出到 " + abs)
		fmt.Println("請把這些檔案交給你的 AI 助手，請它放到自己會生效的位置")
		fmt.Println("（Claude Code 是 ~\\.claude\\skills\\vlui-headless\\，其他 AI 依各自的規則檔慣例）。")
		return 0, true
	}
	return 0, false
}
