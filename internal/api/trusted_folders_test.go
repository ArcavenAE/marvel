package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// trustRepo makes a git repository with one commit (plumbing, so no signing)
// and returns its top-level with symlinks resolved.
func trustRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("update-ref", "refs/heads/main", run("commit-tree", run("mktree"), "-m", "fixture"))
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

const trustedHead = "[workspace]\nname = \"aae\"\n%s\n[[team]]\nname = \"squad\"\n  [[team.role]]\n  name = \"w\"\n  replicas = 1\n    [team.role.runtime]\n    command = \"claude\"\n"

func trustManifest(t *testing.T, workspaceExtra string) *Manifest {
	t.Helper()
	m, err := ParseManifestBytes([]byte(strings.Replace(trustedHead, "%s", workspaceExtra, 1)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return m
}

// An absent key, an empty list and a listed path read differently, because an
// apply that names an empty list revokes and one that names nothing does not.
func TestManifestTrustedFoldersAbsentEmptyAndListed(t *testing.T) {
	if m := trustManifest(t, ""); m.Workspace.TrustedFolders != nil {
		t.Errorf("absent key = %v, want nil", *m.Workspace.TrustedFolders)
	}
	empty := trustManifest(t, "trusted_folders = []")
	if empty.Workspace.TrustedFolders == nil || len(*empty.Workspace.TrustedFolders) != 0 {
		t.Errorf("empty list = %v, want a non-nil empty list", empty.Workspace.TrustedFolders)
	}
	listed := trustManifest(t, `trusted_folders = ["~/work/aae-orc", "i-orc"]`)
	if listed.Workspace.TrustedFolders == nil || !reflect.DeepEqual(*listed.Workspace.TrustedFolders, []string{"~/work/aae-orc", "i-orc"}) {
		t.Errorf("listed = %v", listed.Workspace.TrustedFolders)
	}

	yamlSrc := func(extra string) string {
		return "workspace:\n  name: aae\n" + extra + "teams:\n  - name: squad\n    roles:\n      - name: w\n        replicas: 1\n        runtime:\n          command: claude\n"
	}
	for name, tc := range map[string]struct {
		extra string
		want  *[]string
	}{
		"absent": {"", nil},
		"empty":  {"  trusted_folders: []\n", &[]string{}},
		"listed": {"  trusted_folders:\n    - ~/work/aae-orc\n", &[]string{"~/work/aae-orc"}},
	} {
		m, err := ParseManifestBytes([]byte(yamlSrc(tc.extra)))
		if err != nil {
			t.Fatalf("yaml %s: %v", name, err)
		}
		got := m.Workspace.TrustedFolders
		if (got == nil) != (tc.want == nil) || (got != nil && !reflect.DeepEqual(*got, *tc.want)) {
			t.Errorf("yaml %s: trusted_folders = %v, want %v", name, got, tc.want)
		}
	}
}

func TestValidateTrustedFoldersAcceptsAMainCheckoutTopLevel(t *testing.T) {
	repo := trustRepo(t, filepath.Join(t.TempDir(), "aae-orc"))
	m := trustManifest(t, `trusted_folders = ["`+repo+`"]`)
	if err := m.ValidateTrustedFolders(); err != nil {
		t.Fatalf("a main checkout top-level was refused: %v", err)
	}
	if err := trustManifest(t, "").ValidateTrustedFolders(); err != nil {
		t.Fatalf("no list was refused: %v", err)
	}
}

func TestValidateTrustedFoldersRefusals(t *testing.T) {
	repo := trustRepo(t, filepath.Join(t.TempDir(), "aae-orc"))
	sub := filepath.Join(repo, "docs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", wt, "-b", "feat").CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	plain := t.TempDir()
	for name, tc := range map[string]struct{ path, want string }{
		"missing":          {filepath.Join(plain, "nope"), "does not exist"},
		"not a git folder": {plain, "not a git"},
		"not a top-level":  {sub, "top-level"},
		"linked worktree":  {wt, "linked worktree"},
	} {
		err := trustManifest(t, `trusted_folders = ["`+tc.path+`"]`).ValidateTrustedFolders()
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "trusted_folders") {
			t.Errorf("%s: err = %v, want trusted_folders naming %q", name, err, tc.want)
		}
	}
}

// ~/ expands against HOME and a relative path against workspace.root.
func TestValidateTrustedFoldersExpandsHomeAndResolvesRelative(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	trustRepo(t, filepath.Join(home, "work", "aae-orc"))
	if err := trustManifest(t, `trusted_folders = ["~/work/aae-orc"]`).ValidateTrustedFolders(); err != nil {
		t.Errorf("a ~/ path: %v", err)
	}
	root := t.TempDir()
	trustRepo(t, filepath.Join(root, "i-orc"))
	m := trustManifest(t, `root = "`+root+`"`+"\n"+`trusted_folders = ["i-orc"]`)
	if err := m.ValidateTrustedFolders(); err != nil {
		t.Errorf("a relative path against the root: %v", err)
	}
	err := trustManifest(t, `trusted_folders = ["i-orc"]`).ValidateTrustedFolders()
	if err == nil || !strings.Contains(err.Error(), "workspace.root") {
		t.Errorf("a relative path with no root: err = %v, want it to name workspace.root", err)
	}
}

// Apply: a present list replaces the stored one (an empty list revokes), and an
// absent key leaves it alone.
func TestApplyTrustedFoldersReplaceRevokeAndAbsent(t *testing.T) {
	repo := trustRepo(t, filepath.Join(t.TempDir(), "aae-orc"))
	store := NewStore()
	apply := func(extra string) []string {
		t.Helper()
		m := trustManifest(t, extra)
		if err := m.Apply(store); err != nil {
			t.Fatalf("apply: %v", err)
		}
		w, err := store.GetWorkspace("aae")
		if err != nil {
			t.Fatal(err)
		}
		return w.TrustedFolders
	}
	if got := apply(`trusted_folders = ["` + repo + `"]`); !reflect.DeepEqual(got, []string{repo}) {
		t.Fatalf("first apply stored %v, want %s", got, repo)
	}
	if got := apply(""); !reflect.DeepEqual(got, []string{repo}) {
		t.Errorf("an apply with no key stored %v, want the list left alone", got)
	}
	if got := apply("trusted_folders = []"); len(got) != 0 {
		t.Errorf("an empty list stored %v, want it revoked", got)
	}
	if got := apply(`trusted_folders = ["` + repo + `"]`); !reflect.DeepEqual(got, []string{repo}) {
		t.Errorf("a new list stored %v, want %s", got, repo)
	}
}

// A differing list is said aloud: several manifests can share one workspace and
// the last apply wins silently otherwise.
func TestTrustedFolderNotesWarnWhenTheListDiffers(t *testing.T) {
	store := NewStore()
	if err := trustManifest(t, `trusted_folders = ["/a"]`).Apply(store); err != nil {
		t.Fatal(err)
	}
	if notes := trustManifest(t, `trusted_folders = ["/a"]`).TrustedFolderNotes(store); len(notes) != 0 {
		t.Errorf("the same list warned: %v", notes)
	}
	if notes := trustManifest(t, "").TrustedFolderNotes(store); len(notes) != 0 {
		t.Errorf("an absent key warned: %v", notes)
	}
	notes := trustManifest(t, `trusted_folders = ["/b"]`).TrustedFolderNotes(store)
	if len(notes) != 1 || !strings.Contains(notes[0], "trusted_folders") || !strings.Contains(notes[0], "/a") || !strings.Contains(notes[0], "/b") {
		t.Errorf("a differing list: notes = %v, want one naming both lists", notes)
	}
	if notes := trustManifest(t, "trusted_folders = []").TrustedFolderNotes(store); len(notes) != 1 {
		t.Errorf("a revoke: notes = %v, want one", notes)
	}
}
