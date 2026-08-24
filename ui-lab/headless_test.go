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
