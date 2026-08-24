package main

import (
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
