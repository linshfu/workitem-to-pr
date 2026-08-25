package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallSkillTo(t *testing.T) {
	dir := t.TempDir()
	if err := installSkillTo(dir); err != nil {
		t.Fatalf("installSkillTo: %v", err)
	}
	for _, name := range []string{"SKILL.md", "USAGE-PROMPTS.md"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s 沒被寫出: %v", name, err)
		}
		s := string(b)
		if !strings.Contains(s, "managed by vlui") {
			t.Errorf("%s 少了 managed 標記，自動刷新會認不出它", name)
		}
		if strings.Contains(s, "\r\n") {
			t.Errorf("%s 含 CRLF——輸出要統一 LF，否則本機建置與 CI 建置的 binary 會互相改寫", name)
		}
	}
	if s, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); !strings.Contains(string(s), "--headless") {
		t.Error("SKILL.md 內容不像使用指南（沒提到 --headless）")
	}
}

func TestExportSkillToHasNoMarker(t *testing.T) {
	dir := t.TempDir()
	if err := exportSkillTo(dir); err != nil {
		t.Fatalf("exportSkillTo: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md 沒被寫出: %v", err)
	}
	// export 是給非 Claude 的 AI 用的，自動刷新在那些位置不會發生，
	// 帶著「會自動刷新」的標記只會誤導。
	if strings.Contains(string(b), "managed by vlui") {
		t.Error("export 出的檔案不該帶 managed 標記")
	}
}

func TestRefreshSkillAt(t *testing.T) {
	t.Run("沒裝過就不動（不無中生有）", func(t *testing.T) {
		dir := t.TempDir()
		refreshSkillAt(dir)
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
			t.Error("refresh 不該在沒裝過的位置生出檔案")
		}
	})

	t.Run("沒有標記的檔案不覆寫（保護手動修改）", func(t *testing.T) {
		dir := t.TempDir()
		custom := "# 我自己改過的指南\n"
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(custom), 0o644)
		refreshSkillAt(dir)
		b, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if string(b) != custom {
			t.Error("沒有 managed 標記的檔案被覆寫了")
		}
	})

	t.Run("帶標記且內容過期就刷新", func(t *testing.T) {
		dir := t.TempDir()
		stale := "# 舊版指南\n\nmanaged by vlui\n"
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(stale), 0o644)
		refreshSkillAt(dir)
		b, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if string(b) == stale {
			t.Fatal("過期的 managed 檔案沒被刷新")
		}
		if want := managedSkillFiles()["SKILL.md"]; string(b) != want {
			t.Error("刷新後內容不等於內嵌版")
		}
	})

	t.Run("帶標記且內容已同步就不重寫", func(t *testing.T) {
		dir := t.TempDir()
		if err := installSkillTo(dir); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "SKILL.md")
		before, _ := os.Stat(p)
		refreshSkillAt(dir)
		after, _ := os.Stat(p)
		if !after.ModTime().Equal(before.ModTime()) {
			t.Error("內容相同仍被重寫（每次啟動都白寫一次）")
		}
	})
}

func TestHandleSkillFlags(t *testing.T) {
	t.Run("不相關的參數不攔", func(t *testing.T) {
		for _, args := range [][]string{{}, {"--headless", "1"}, {"35744"}, {"/task"}} {
			if _, handled := handleSkillFlags(args); handled {
				t.Errorf("%v 不該被 skill flags 攔下", args)
			}
		}
	})

	t.Run("export 到指定目錄", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "out")
		code, handled := handleSkillFlags([]string{"--export-skill", dir})
		if !handled || code != 0 {
			t.Fatalf("handled=%v code=%d", handled, code)
		}
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			t.Errorf("export 沒有寫出 SKILL.md: %v", err)
		}
	})
}
