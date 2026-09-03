package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestExtractTags(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"[abc] title", []string{"abc"}},
		{"no tags here", nil},
		{"[a][b] two tags", []string{"a", "b"}},
		{"[unterminated title", nil},
	}
	for _, c := range cases {
		got := extractTags(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("extractTags(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDeriveBranchName(t *testing.T) {
	t.Run("basic slug", func(t *testing.T) {
		got := deriveBranchName(123, "Fix login bug")
		want := "task/123-Fixloginbug"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("strips leading ascii tag and parens", func(t *testing.T) {
		got := deriveBranchName(456, "[ABC] Do something (urgent)")
		want := "task/456-Dosomethingurgent"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("strips full-width brackets anywhere in the title", func(t *testing.T) {
		got := deriveBranchName(789, "【緊急】修復登入問題")
		want := "task/789-緊急修復登入問題"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("truncates by rune count, not byte count", func(t *testing.T) {
		title := strings.Repeat("測", 60) // 60 runes, 180 bytes in UTF-8
		got := deriveBranchName(1, title)
		want := "task/1-" + strings.Repeat("測", 50)
		if got != want {
			t.Errorf("got %d runes of slug, want 50", len([]rune(got))-len("task/1-"))
		}
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestResolveMapping(t *testing.T) {
	t.Run("tier 1: area path prefix match with unique repo", func(t *testing.T) {
		cfg := config{Mappings: map[string]mappingCfg{
			"chem": {AzureProject: "ESHClouds", AzureRepository: "chem-repo", AreaPath: `ESHClouds\1.Product\Chem`},
		}}
		wi := workItem{area: `ESHClouds\1.Product\Chem\SubTeam`, title: "Some title"}
		key, mp, ok := (model{cfg: cfg}).resolveMapping(wi)
		if !ok || key != "chem" || mp.AzureRepository != "chem-repo" {
			t.Errorf("got key=%q mp=%+v ok=%v", key, mp, ok)
		}
	})

	t.Run("tier 2: title [tag] match", func(t *testing.T) {
		cfg := config{Mappings: map[string]mappingCfg{
			"abc": {AzureProject: "ABC-Project", AzureRepository: "abc-repo"},
		}}
		wi := workItem{title: "[abc] fix something"}
		key, _, ok := (model{cfg: cfg}).resolveMapping(wi)
		if !ok || key != "abc" {
			t.Errorf("got key=%q ok=%v, want abc/true", key, ok)
		}
	})

	t.Run("tier 3: keyword substring match", func(t *testing.T) {
		cfg := config{Mappings: map[string]mappingCfg{
			"chemcloud": {AzureProject: "P", AzureRepository: "R"},
		}}
		wi := workItem{title: "update chemcloud dashboard"}
		key, _, ok := (model{cfg: cfg}).resolveMapping(wi)
		if !ok || key != "chemcloud" {
			t.Errorf("got key=%q ok=%v, want chemcloud/true", key, ok)
		}
	})

	t.Run("no match falls through to false", func(t *testing.T) {
		cfg := config{Mappings: map[string]mappingCfg{
			"xyz": {AzureProject: "P", AzureRepository: "R"},
		}}
		wi := workItem{title: "totally unrelated title", area: `SomeOther\Area`}
		_, _, ok := (model{cfg: cfg}).resolveMapping(wi)
		if ok {
			t.Error("expected no match, got ok=true")
		}
	})
}

// muteStdio 把 stdout/stderr 導到 NUL——handleTopLevelFlags 會直接印，不導掉的話
// go test 的輸出會被用法區塊塞滿。
func muteStdio(t *testing.T) {
	t.Helper()
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("開 %s 失敗: %v", os.DevNull, err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devNull, devNull
	t.Cleanup(func() {
		os.Stdout, os.Stderr = origOut, origErr
		devNull.Close()
	})
}

func TestHandleTopLevelFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		code    int
		handled bool
	}{
		{"不帶參數 -> 進 TUI", nil, 0, false},
		{"純數字 -> 進 TUI", []string{"35744"}, 0, false},
		{"斜線指令 -> 進 TUI", []string{"/task", "35744"}, 0, false},
		{"--version -> 印版號後 exit 0", []string{"--version"}, exitHeadlessOK, true},
		{"不認得的旗標 -> exit 2", []string{"--bogus"}, exitHeadlessUsage, true},
		{"旗標在指令後面也要擋", []string{"/task", "--bogus"}, exitHeadlessUsage, true},
		// 這格是整個函式的重點：--headless 打錯字絕不能 fallback 進 TUI 卡住非互動 shell。
		{"--headless 打錯字 -> exit 2 而不是開 TUI", []string{"--headles", "35744"}, exitHeadlessUsage, true},
	}
	muteStdio(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, handled := handleTopLevelFlags(c.args)
			if code != c.code || handled != c.handled {
				t.Errorf("handleTopLevelFlags(%q) = (%d, %v), want (%d, %v)", c.args, code, handled, c.code, c.handled)
			}
		})
	}
}
