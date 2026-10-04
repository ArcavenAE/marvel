package api

import (
	"os"
	"path/filepath"
	"testing"
)

// One row per (harness, spelling). want is "default" for "", "same" for the label
// of the real acct-one directory, "own" for a spelling that stays distinct from
// both, "two" for the label of acct-two. The body of the PR carries the same table
// beside what #549's canonicalConfigHome says, as the AccountKey switch checklist.
func TestCanonicalConfigDirCaseTable(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	claude, codex := mk(".claude"), mk(".codex")
	one, two := mk("acct-one"), mk("acct-two")
	link := func(name, target string) string {
		p := filepath.Join(home, name)
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	linkOne := link("link-one", one)
	linkClaude := link("link-claude", claude)
	linkCodex := link("link-codex", codex)

	wantOne := CanonicalConfigDir("claude", one, home)
	wantTwo := CanonicalConfigDir("claude", two, home)
	if wantOne == "" || wantTwo == "" || wantOne == wantTwo {
		t.Fatalf("labels: %q %q", wantOne, wantTwo)
	}
	resolvedClaude, _ := filepath.EvalSymlinks(claude)
	resolvedCodex, _ := filepath.EvalSymlinks(codex)

	rows := []struct {
		harness, dir, want string
	}{
		{"claude", "", ""},
		{"claude", claude, ""},
		{"claude", claude + "/", ""},
		{"claude", linkClaude, ""},
		{"claude", "~/.claude", "~/.claude"},
		{"claude", codex, resolvedCodex},
		{"claude", one, wantOne},
		{"claude", one + "/", wantOne},
		{"claude", linkOne, wantOne},
		{"claude", filepath.Join(home, ".", "acct-one"), wantOne},
		{"claude", "~/acct-one", "~/acct-one"},
		{"claude", two, wantTwo},
		{"claude", "/no/such/dir/", "/no/such/dir"},
		{"codex", "", ""},
		{"codex", codex, ""},
		{"codex", linkCodex, ""},
		{"codex", claude, resolvedClaude},
		{"codex", one, wantOne},
		{"codex", linkOne, wantOne},
		{"codex", "~/.codex", "~/.codex"},
		{"forestage", "", ""},
		{"forestage", claude, resolvedClaude},
		{"forestage", codex, resolvedCodex},
		{"forestage", one, wantOne},
	}
	for _, r := range rows {
		if got := CanonicalConfigDir(r.harness, r.dir, home); got != r.want {
			t.Errorf("%s %q = %q, want %q", r.harness, r.dir, got, r.want)
		}
	}
}
