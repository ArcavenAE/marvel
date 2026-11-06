package api

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/gittest"
)

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
	listed := trustManifest(t, `trusted_folders = ["~/work/repo-a", "repo-b"]`)
	if listed.Workspace.TrustedFolders == nil || !reflect.DeepEqual(*listed.Workspace.TrustedFolders, []string{"~/work/repo-a", "repo-b"}) {
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
		"listed": {"  trusted_folders:\n    - ~/work/repo-a\n", &[]string{"~/work/repo-a"}},
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
	repo := gittest.Repo(t, filepath.Join(t.TempDir(), "repo-a"))
	m := trustManifest(t, `trusted_folders = ["`+repo+`"]`)
	if err := m.ValidateTrustedFolders(); err != nil {
		t.Fatalf("a main checkout top-level was refused: %v", err)
	}
	if err := trustManifest(t, "").ValidateTrustedFolders(); err != nil {
		t.Fatalf("no list was refused: %v", err)
	}
}

func TestValidateTrustedFoldersRefusals(t *testing.T) {
	repo := gittest.Repo(t, filepath.Join(t.TempDir(), "repo-a"))
	sub := filepath.Join(repo, "docs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := gittest.Worktree(t, repo, "wt")
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
	gittest.Repo(t, filepath.Join(home, "work", "repo-a"))
	if err := trustManifest(t, `trusted_folders = ["~/work/repo-a"]`).ValidateTrustedFolders(); err != nil {
		t.Errorf("a ~/ path: %v", err)
	}
	root := t.TempDir()
	gittest.Repo(t, filepath.Join(root, "repo-b"))
	m := trustManifest(t, `root = "`+root+`"`+"\n"+`trusted_folders = ["repo-b"]`)
	if err := m.ValidateTrustedFolders(); err != nil {
		t.Errorf("a relative path against the root: %v", err)
	}
	err := trustManifest(t, `trusted_folders = ["repo-b"]`).ValidateTrustedFolders()
	if err == nil || !strings.Contains(err.Error(), "workspace.root") {
		t.Errorf("a relative path with no root: err = %v, want it to name workspace.root", err)
	}
}

// Apply: a present list replaces the stored one (an empty list revokes), and an
// absent key leaves it alone.
func TestApplyTrustedFoldersReplaceRevokeAndAbsent(t *testing.T) {
	repo := gittest.Repo(t, filepath.Join(t.TempDir(), "repo-a"))
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

// GitMainRoot answers about the folder it is given, not about a repository the
// daemon's environment points at: a daemon started from a git hook inherits
// GIT_DIR, and the answer must not follow it.
func TestGitMainRootIgnoresAnInheritedGitDir(t *testing.T) {
	other := gittest.Repo(t, filepath.Join(t.TempDir(), "other"))
	repo := gittest.Repo(t, filepath.Join(t.TempDir(), "repo-a"))
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	root, reason := GitMainRoot(repo)
	if root != repo || reason != "" {
		t.Fatalf("GitMainRoot(%s) = %q, %q, want the folder's own root", repo, root, reason)
	}
}

// plantFsmonitor points a repository's core.fsmonitor at a script that writes
// a marker file inside scratch, and returns the marker's path. The config is
// written to the file directly: gittest refuses -c and config, on purpose.
func plantFsmonitor(t *testing.T, repo string) string {
	t.Helper()
	scratch := t.TempDir()
	marker := filepath.Join(scratch, "marker")
	hook := filepath.Join(scratch, "hook.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(repo, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("[core]\n\tfsmonitor = " + hook + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return marker
}

// gitDirect runs git in the repository the way a caller without gitOut would.
func gitDirect(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = gittest.Env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// A repository's own config does not run a program through gitOut. The control
// first shows the planted hook fires when git status runs without the
// hardening, so a quiet marker below means the clearing worked and not that the
// mechanism was missing.
func TestGitOutDoesNotRunAPlantedFsmonitor(t *testing.T) {
	repo := gittest.Repo(t, filepath.Join(t.TempDir(), "seat-repo"))
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDirect(t, repo, "add", "f")
	marker := plantFsmonitor(t, repo)

	gitDirect(t, repo, "status")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("control: git status did not run the planted fsmonitor (%v); the test cannot tell anything", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	top, err := gitOut(repo, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		t.Fatalf("gitOut rev-parse = %q, %v", top, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("gitOut ran the repository's core.fsmonitor")
	}

	// The clearing is a second layer behind the subcommand list. Let status
	// through for this check and see that the clearing alone holds.
	gitOutSubcommands["status"] = true
	t.Cleanup(func() { delete(gitOutSubcommands, "status") })
	if _, err := gitOut(repo, "status", "--porcelain"); err != nil {
		t.Fatalf("gitOut status: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("gitOut ran the repository's core.fsmonitor for status")
	}
}

// gitOut runs only the subcommands it is meant to. status is the one that
// reads the work tree and starts the fsmonitor program.
func TestGitOutRefusesOtherSubcommands(t *testing.T) {
	repo := gittest.Repo(t, filepath.Join(t.TempDir(), "seat-repo"))
	marker := plantFsmonitor(t, repo)
	for _, args := range [][]string{{"status"}, {"log"}, {"config", "--list"}, {}} {
		out, err := gitOut(repo, args...)
		if !errors.Is(err, errGitSubcommand) || out != "" {
			t.Errorf("gitOut(%v) = %q, %v, want errGitSubcommand", args, out, err)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a refused subcommand still ran the repository's core.fsmonitor")
	}
}
