package main

import "testing"

func TestIsHeadless(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"absent", []string{"35744"}, false},
		{"present first", []string{"--headless", "35744"}, true},
		{"present last", []string{"35744", "--dry-run", "--headless"}, true},
		{"empty", []string{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isHeadless(c.args); got != c.want {
				t.Errorf("isHeadless(%v) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

func TestParseHeadlessArgs(t *testing.T) {
	t.Run("bare id defaults to skip-reviewer", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "35744"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.workItemID != 35744 {
			t.Errorf("taskID = %d, want 35744", o.workItemID)
		}
		if !o.skipReviewer {
			t.Error("skipReviewer = false, want true when --reviewer is omitted")
		}
		if o.dryRun || o.skipSlack || o.reviewerEmail != "" {
			t.Errorf("unexpected flags set: %+v", o)
		}
	})

	t.Run("flags before id", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "--dry-run", "--skip-slack", "35744"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.workItemID != 35744 || !o.dryRun || !o.skipSlack {
			t.Errorf("got %+v", o)
		}
	})

	t.Run("flags after id", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "35744", "--dry-run", "--skip-slack"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.workItemID != 35744 || !o.dryRun || !o.skipSlack {
			t.Errorf("got %+v", o)
		}
	})

	t.Run("reviewer flag with separate value", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "35744", "--reviewer", "a@b.com"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.reviewerEmail != "a@b.com" || o.skipReviewer {
			t.Errorf("got %+v", o)
		}
	})

	t.Run("reviewer flag with equals form", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "35744", "--reviewer=a@b.com"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.reviewerEmail != "a@b.com" || o.skipReviewer {
			t.Errorf("got %+v", o)
		}
	})

	t.Run("explicit skip-reviewer", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "35744", "--skip-reviewer"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !o.skipReviewer || o.reviewerEmail != "" {
			t.Errorf("got %+v", o)
		}
	})

	t.Run("reviewer and skip-reviewer conflict", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "35744", "--reviewer", "a@b.com", "--skip-reviewer"})
		if err == nil {
			t.Fatal("expected error for conflicting --reviewer/--skip-reviewer")
		}
	})

	t.Run("missing task id", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "--dry-run"})
		if err == nil {
			t.Fatal("expected error for missing task id")
		}
	})

	t.Run("extra ids become additional linked work items", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "36346", "36347", "36348"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.workItemID != 36346 {
			t.Errorf("primary = %d, want 36346 (first id names the branch)", o.workItemID)
		}
		got := o.linkedIDs()
		want := []int{36346, 36347, 36348}
		if len(got) != len(want) {
			t.Fatalf("linkedIDs() = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("linkedIDs()[%d] = %d, want %d", i, got[i], want[i])
			}
		}
	})

	t.Run("linkedIDs drops a repeated primary", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "36346", "36346", "36347"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := o.linkedIDs()
		if len(got) != 2 || got[0] != 36346 || got[1] != 36347 {
			t.Errorf("linkedIDs() = %v, want [36346 36347]", got)
		}
	})

	t.Run("reviewer flag missing value", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "35744", "--reviewer"})
		if err == nil {
			t.Fatal("expected error for --reviewer with no value")
		}
	})

	t.Run("unknown flag", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "35744", "--bogus"})
		if err == nil {
			t.Fatal("expected error for unknown flag")
		}
	})

	t.Run("non-numeric id", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "abc"})
		if err == nil {
			t.Fatal("expected error for non-numeric task id")
		}
	})
}

func TestParseHeadlessArgsBranchOnly(t *testing.T) {
	t.Run("branch flag sets branchOnly", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "36346", "--branch"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !o.branchOnly || o.workItemID != 36346 {
			t.Errorf("got %+v", o)
		}
	})

	t.Run("branch allows dry-run", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "36346", "--branch", "--dry-run"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !o.branchOnly || !o.dryRun {
			t.Errorf("got %+v", o)
		}
	})

	t.Run("branch rejects PR-only flags", func(t *testing.T) {
		for _, extra := range []string{"--reviewer", "--skip-slack", "--skip-reviewer"} {
			args := []string{"--headless", "36346", "--branch", extra}
			if extra == "--reviewer" {
				args = append(args, "a@b.com")
			}
			if _, err := parseHeadlessArgs(args); err == nil {
				t.Errorf("expected error for --branch with %s", extra)
			}
		}
	})

	t.Run("branch rejects extra ids", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "36346", "36347", "--branch"})
		if err == nil {
			t.Fatal("expected error: --branch takes a single task id")
		}
	})

	t.Run("branch rejects create mode", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "36344", "--new", "A", "--branch"})
		if err == nil {
			t.Fatal("expected error: --new cannot combine with --branch")
		}
	})
}

func TestParseHeadlessArgsReleaseMode(t *testing.T) {
	t.Run("project and version, separate values", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "--release", "--project", "legal", "--version", "7.14.4"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !o.releaseMode || o.projectKey != "legal" || o.version != "7.14.4" {
			t.Errorf("got %+v", o)
		}
		if got := o.releaseBranch(); got != "release/v7.14.4" {
			t.Errorf("releaseBranch() = %q, want release/v7.14.4", got)
		}
	})

	t.Run("equals form", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "--release", "--project=legal", "--version=1.2.3"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.projectKey != "legal" || o.version != "1.2.3" {
			t.Errorf("got %+v", o)
		}
	})

	t.Run("rejects bad version format", func(t *testing.T) {
		for _, v := range []string{"7.14", "v7.14.4", "7.14.4-beta", "abc"} {
			_, err := parseHeadlessArgs([]string{"--headless", "--release", "--project", "legal", "--version", v})
			if err == nil {
				t.Errorf("expected error for version %q", v)
			}
		}
	})

	t.Run("requires project and version", func(t *testing.T) {
		for _, args := range [][]string{
			{"--headless", "--release"},
			{"--headless", "--release", "--project", "legal"},
			{"--headless", "--release", "--version", "1.2.3"},
		} {
			if _, err := parseHeadlessArgs(args); err == nil {
				t.Errorf("expected error for %v", args)
			}
		}
	})

	t.Run("rejects work item ids", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "--release", "--project", "legal", "--version", "1.2.3", "36346"})
		if err == nil {
			t.Fatal("expected error: --release takes no work item id")
		}
	})

	t.Run("rejects combining with new or branch", func(t *testing.T) {
		for _, extra := range [][]string{{"--new", "A"}, {"--branch"}} {
			args := append([]string{"--headless", "--release", "--project", "legal", "--version", "1.2.3"}, extra...)
			if _, err := parseHeadlessArgs(args); err == nil {
				t.Errorf("expected error for %v", args)
			}
		}
	})

	t.Run("project/version rejected outside release mode", func(t *testing.T) {
		for _, args := range [][]string{
			{"--headless", "36346", "--project", "legal"},
			{"--headless", "36346", "--version", "1.2.3"},
		} {
			if _, err := parseHeadlessArgs(args); err == nil {
				t.Errorf("expected error for %v", args)
			}
		}
	})

	t.Run("reviewer and dry-run allowed", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "--release", "--project", "legal",
			"--version", "1.2.3", "--reviewer", "a@b.com", "--dry-run"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.reviewerEmail != "a@b.com" || !o.dryRun {
			t.Errorf("got %+v", o)
		}
	})
}

func TestParseHeadlessArgsHotfixMode(t *testing.T) {
	base := []string{"--headless", "--hotfix", "--project", "legal", "--version", "7.14.4"}

	t.Run("three steps parse distinctly", func(t *testing.T) {
		cases := []struct {
			name       string
			extra      []string
			branchOnly bool
			bump       bool
		}{
			{"branch step", []string{"--branch"}, true, false},
			{"bump step", []string{"--bump"}, false, true},
			{"pr step", nil, false, false},
		}
		for _, c := range cases {
			o, err := parseHeadlessArgs(append(append([]string{}, base...), c.extra...))
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", c.name, err)
			}
			if !o.hotfixMode || o.projectKey != "legal" || o.version != "7.14.4" {
				t.Errorf("%s: got %+v", c.name, o)
			}
			if o.branchOnly != c.branchOnly || o.bump != c.bump {
				t.Errorf("%s: branchOnly=%v bump=%v, want %v/%v", c.name, o.branchOnly, o.bump, c.branchOnly, c.bump)
			}
			if got := o.hotfixBranch(); got != "hotfix/v7.14.4" {
				t.Errorf("%s: hotfixBranch() = %q", c.name, got)
			}
		}
	})

	t.Run("branch and bump are mutually exclusive", func(t *testing.T) {
		_, err := parseHeadlessArgs(append(append([]string{}, base...), "--branch", "--bump"))
		if err == nil {
			t.Fatal("expected error for --branch with --bump")
		}
	})

	t.Run("bump requires hotfix mode", func(t *testing.T) {
		for _, args := range [][]string{
			{"--headless", "36346", "--bump"},
			{"--headless", "--release", "--project", "legal", "--version", "1.2.3", "--bump"},
		} {
			if _, err := parseHeadlessArgs(args); err == nil {
				t.Errorf("expected error for %v", args)
			}
		}
	})

	t.Run("release and hotfix cannot combine", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "--release", "--hotfix",
			"--project", "legal", "--version", "1.2.3"})
		if err == nil {
			t.Fatal("expected error for --release with --hotfix")
		}
	})

	t.Run("branch/bump steps reject PR flags", func(t *testing.T) {
		for _, step := range []string{"--branch", "--bump"} {
			args := append(append([]string{}, base...), step, "--reviewer", "a@b.com")
			if _, err := parseHeadlessArgs(args); err == nil {
				t.Errorf("expected error for %s with --reviewer", step)
			}
		}
	})

	t.Run("pr step accepts reviewer", func(t *testing.T) {
		o, err := parseHeadlessArgs(append(append([]string{}, base...), "--reviewer", "a@b.com"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.reviewerEmail != "a@b.com" || o.branchOnly || o.bump {
			t.Errorf("got %+v", o)
		}
	})

	t.Run("requires project and version", func(t *testing.T) {
		for _, args := range [][]string{
			{"--headless", "--hotfix"},
			{"--headless", "--hotfix", "--project", "legal"},
			{"--headless", "--hotfix", "--version", "1.2.3"},
		} {
			if _, err := parseHeadlessArgs(args); err == nil {
				t.Errorf("expected error for %v", args)
			}
		}
	})

	t.Run("rejects work item ids and --new", func(t *testing.T) {
		for _, extra := range [][]string{{"36346"}, {"--new", "A"}} {
			args := append(append([]string{}, base...), extra...)
			if _, err := parseHeadlessArgs(args); err == nil {
				t.Errorf("expected error for %v", args)
			}
		}
	})
}

func TestParseHeadlessArgsCreateMode(t *testing.T) {
	t.Run("single new title", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "36261", "--new", "[Legal][前端] 標籤側欄"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !o.createMode() {
			t.Error("createMode() = false, want true when --new is given")
		}
		if o.workItemID != 36261 {
			t.Errorf("workItemID = %d, want 36261", o.workItemID)
		}
		if len(o.newTitles) != 1 || o.newTitles[0] != "[Legal][前端] 標籤側欄" {
			t.Errorf("newTitles = %v", o.newTitles)
		}
	})

	t.Run("repeated new preserves order", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "36261", "--new", "A", "--new", "B", "--new", "C"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"A", "B", "C"}
		if len(o.newTitles) != len(want) {
			t.Fatalf("newTitles = %v, want %v", o.newTitles, want)
		}
		for i := range want {
			if o.newTitles[i] != want[i] {
				t.Errorf("newTitles[%d] = %q, want %q", i, o.newTitles[i], want[i])
			}
		}
	})

	t.Run("new with equals form", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "36261", "--new=A"})
		if err != nil || len(o.newTitles) != 1 || o.newTitles[0] != "A" {
			t.Errorf("got %+v err=%v", o, err)
		}
	})

	t.Run("create mode allows dry-run", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "36261", "--new", "A", "--dry-run"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !o.dryRun || !o.createMode() {
			t.Errorf("got %+v", o)
		}
	})

	t.Run("create mode rejects reviewer", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "36261", "--new", "A", "--reviewer", "a@b.com"})
		if err == nil {
			t.Fatal("expected error: --new cannot combine with --reviewer")
		}
	})

	t.Run("create mode rejects skip-slack", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "36261", "--new", "A", "--skip-slack"})
		if err == nil {
			t.Fatal("expected error: --new cannot combine with --skip-slack")
		}
	})

	t.Run("create mode rejects skip-reviewer", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "36261", "--new", "A", "--skip-reviewer"})
		if err == nil {
			t.Fatal("expected error: --new cannot combine with --skip-reviewer")
		}
	})

	t.Run("new flag missing title", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "36261", "--new"})
		if err == nil {
			t.Fatal("expected error for --new with no title")
		}
	})

	t.Run("bare id is not create mode", func(t *testing.T) {
		o, err := parseHeadlessArgs([]string{"--headless", "36261"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.createMode() {
			t.Error("createMode() = true without --new")
		}
	})

	t.Run("create mode rejects extra parent ids", func(t *testing.T) {
		_, err := parseHeadlessArgs([]string{"--headless", "36261", "36262", "--new", "A"})
		if err == nil {
			t.Fatal("expected error: create mode takes exactly one parent id")
		}
	})
}

func TestNextLevelType(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Feature", "Product Backlog Item"},
		{"Release", "Product Backlog Item"},
		{"Product Backlog Item", "Task"},
		{"Bug", "Task"},
		{"Task", ""},
		{"feature", "Product Backlog Item"}, // case-insensitive
		{"Epic", ""},
	}
	for _, c := range cases {
		if got := nextLevelType(c.in); got != c.want {
			t.Errorf("nextLevelType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
