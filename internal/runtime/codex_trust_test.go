package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// gitRepo makes a repository with one commit under a temp dir and returns its
// top-level with symlinks resolved. The commit is made with plumbing so the
// fixture needs no signing and no config.
func gitRepo(t *testing.T, dir string) string {
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
	empty := run("mktree")
	commit := run("commit-tree", empty, "-m", "fixture")
	run("update-ref", "refs/heads/main", commit)
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func addWorktree(t *testing.T, repo, name string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), name)
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", wt, "-b", name).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	real, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestCodexTrustRootAtAListedRoot(t *testing.T) {
	repo := gitRepo(t, filepath.Join(t.TempDir(), "aae-orc"))
	got := resolveCodexTrust(repo, []string{repo}, "", "")
	if got.Root != repo || got.Reason != "" {
		t.Fatalf("trust = %+v, want trusted at %s", got, repo)
	}
}

// One key at the root covers every subdirectory and every worktree of it.
func TestCodexTrustRootFromASubdirectoryAndAWorktree(t *testing.T) {
	repo := gitRepo(t, filepath.Join(t.TempDir(), "aae-orc"))
	sub := filepath.Join(repo, "docs", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := addWorktree(t, repo, "feat")
	for name, start := range map[string]string{"subdirectory": sub, "worktree": wt} {
		if got := resolveCodexTrust(start, []string{repo}, "", ""); got.Root != repo {
			t.Errorf("%s: trust = %+v, want trusted at the main root %s", name, got, repo)
		}
	}
}

// A subrepo with its own .git resolves to its own root and does not inherit the
// parent's trust, as in codex.
func TestCodexTrustSubrepoDoesNotInheritItsParent(t *testing.T) {
	parent := gitRepo(t, filepath.Join(t.TempDir(), "aae-orc"))
	subrepo := gitRepo(t, filepath.Join(parent, "marvel"))
	got := resolveCodexTrust(subrepo, []string{parent}, "", "")
	if got.Root != "" || got.Reason != "not listed" {
		t.Fatalf("trust = %+v, want untrusted, not listed", got)
	}
}

func TestCodexTrustUnlistedFolderStaysUntrusted(t *testing.T) {
	other := gitRepo(t, filepath.Join(t.TempDir(), "other"))
	listed := gitRepo(t, filepath.Join(t.TempDir(), "aae-orc"))
	got := resolveCodexTrust(other, []string{listed}, "", "")
	if got.Root != "" || got.Reason != "not listed" {
		t.Fatalf("trust = %+v, want untrusted, not listed", got)
	}
}

// Every way git can fail leaves the seat untrusted, each with its own reason.
func TestCodexTrustFailsClosed(t *testing.T) {
	listed := gitRepo(t, filepath.Join(t.TempDir(), "aae-orc"))

	noGit := t.TempDir()
	if got := resolveCodexTrust(noGit, []string{listed}, "", ""); got.Root != "" || got.Reason != "no git" {
		t.Errorf("a folder with no repository: %+v, want untrusted, no git", got)
	}
	if got := resolveCodexTrust(filepath.Join(noGit, "missing"), []string{listed}, "", ""); got.Root != "" || got.Reason != "git error" {
		t.Errorf("a missing start directory: %+v, want untrusted, git error", got)
	}

	bare := filepath.Join(t.TempDir(), "bare.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	if got := resolveCodexTrust(bare, []string{bare}, "", ""); got.Root != "" || got.Reason != "bare repo" {
		t.Errorf("a bare repository: %+v, want untrusted, bare repo", got)
	}

	if got := resolveCodexTrust("", []string{listed}, "", ""); got.Root != "" || got.Reason == "" {
		t.Errorf("an empty start directory: %+v, want untrusted with a reason", got)
	}
}

// A listed path that is a symlink to the root, and a start directory reached
// through one, both match: the comparison is after EvalSymlinks.
func TestCodexTrustMatchesThroughSymlinks(t *testing.T) {
	repo := gitRepo(t, filepath.Join(t.TempDir(), "aae-orc"))
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	if got := resolveCodexTrust(repo, []string{alias}, "", ""); got.Root != repo {
		t.Errorf("a listed alias: %+v, want trusted at %s", got, repo)
	}
	if got := resolveCodexTrust(alias, []string{repo}, "", ""); got.Root != repo {
		t.Errorf("a start directory through an alias: %+v, want trusted at %s", got, repo)
	}
}

func TestCodexTrustExpandsHomeAndResolvesRelativeAgainstTheRoot(t *testing.T) {
	home := t.TempDir()
	inHome := gitRepo(t, filepath.Join(home, "work", "aae-orc"))
	if got := resolveCodexTrust(inHome, []string{"~/work/aae-orc"}, home, ""); got.Root != inHome {
		t.Errorf("a ~/ path: %+v, want trusted at %s", got, inHome)
	}
	wsRoot := t.TempDir()
	rel := gitRepo(t, filepath.Join(wsRoot, "i-orc"))
	if got := resolveCodexTrust(rel, []string{"i-orc"}, "", wsRoot); got.Root != rel {
		t.Errorf("a relative path: %+v, want trusted at %s", got, rel)
	}
	if got := resolveCodexTrust(rel, []string{"i-orc"}, "", ""); got.Root != "" {
		t.Errorf("a relative path with no workspace root must not match: %+v", got)
	}
}

// seedFor runs the real seeder for a seat starting in workDir and returns the
// config it wrote and the events it recorded.
func seedFor(t *testing.T, workDir string, listed []string) (map[string]any, []events.Event) {
	t.Helper()
	t.Setenv("PATH", t.TempDir()) // no codex to ask: the file is still written
	ring := events.NewRing(16)
	ctx := &LaunchContext{
		Session:   &api.Session{Name: "seat", Workspace: "aae", Team: "t", Role: "r", WorkDir: workDir},
		Role:      &api.Role{Name: "r", Runtime: api.Runtime{Name: "codex", Command: "codex"}},
		Team:      &api.Team{Name: "t"},
		Workspace: &api.Workspace{Name: "aae", TrustedFolders: listed},
		Events:    ring,
	}
	dir := t.TempDir()
	seeder := codexSeeder(ctx, "")
	if seeder == nil {
		t.Fatal("no seeder for a full launch context")
	}
	_ = seeder(dir) // returns the no-codex reason; the file is what matters
	var cfg map[string]any
	if _, err := toml.DecodeFile(filepath.Join(dir, codexConfigFile), &cfg); err != nil {
		t.Fatalf("read seeded config: %v", err)
	}
	return cfg, ring.Snapshot(events.Filter{}, 0)
}

func projectsOf(t *testing.T, cfg map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	if raw, ok := cfg["projects"].(map[string]any); ok {
		for k, v := range raw {
			out[k] = v.(map[string]any)
		}
	}
	return out
}

// A listed root is seeded trusted with ONE entry, the root, and no untrusted
// entry for the start directory: an exact key beats the root key in codex.
func TestSeederWritesOneTrustedRootAndNoUntrustedEntry(t *testing.T) {
	repo := gitRepo(t, filepath.Join(t.TempDir(), "aae-orc"))
	wt := addWorktree(t, repo, "feat")
	cfg, evs := seedFor(t, wt, []string{repo})
	projects := projectsOf(t, cfg)
	if len(projects) != 1 || projects[repo]["trust_level"] != "trusted" {
		t.Fatalf("projects = %v, want exactly %s trusted", projects, repo)
	}
	if len(evs) != 1 || evs[0].Kind != events.KindCodexTrust || !strings.Contains(evs[0].Message, "trusted") {
		t.Fatalf("events = %+v, want one codex.trust naming outcome trusted", evs)
	}
}

// A seat in a subrepo of a listed root stays untrusted, as today, and the event
// says why.
func TestSeederKeepsASubrepoUntrusted(t *testing.T) {
	parent := gitRepo(t, filepath.Join(t.TempDir(), "aae-orc"))
	subrepo := gitRepo(t, filepath.Join(parent, "marvel"))
	cfg, evs := seedFor(t, subrepo, []string{parent})
	projects := projectsOf(t, cfg)
	if len(projects) != 1 || projects[subrepo]["trust_level"] != "untrusted" {
		t.Fatalf("projects = %v, want only %s untrusted", projects, subrepo)
	}
	if len(evs) != 1 || evs[0].Kind != events.KindCodexTrust || !strings.Contains(evs[0].Message, "untrusted") || !strings.Contains(evs[0].Message, "not listed") {
		t.Fatalf("events = %+v, want one codex.trust, untrusted, not listed", evs)
	}
}

// A git failure fails closed to the seed marvel writes today.
func TestSeederFailsClosedOnAGitError(t *testing.T) {
	listed := gitRepo(t, filepath.Join(t.TempDir(), "aae-orc"))
	noGit := t.TempDir()
	cfg, evs := seedFor(t, noGit, []string{listed})
	projects := projectsOf(t, cfg)
	if len(projects) != 1 || projects[noGit]["trust_level"] != "untrusted" {
		t.Fatalf("projects = %v, want only %s untrusted", projects, noGit)
	}
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "no git") {
		t.Fatalf("events = %+v, want one codex.trust naming no git", evs)
	}
}

// A workspace that lists nothing behaves as before and records nothing.
func TestSeederWithNoListedFoldersIsTodaysSeedAndNoEvent(t *testing.T) {
	dir := t.TempDir()
	cfg, evs := seedFor(t, dir, nil)
	if projects := projectsOf(t, cfg); len(projects) != 1 || projects[dir]["trust_level"] != "untrusted" {
		t.Fatalf("projects = %v, want only %s untrusted", projects, dir)
	}
	if len(evs) != 0 {
		t.Fatalf("events = %+v, want none when nothing is listed", evs)
	}
}

// #684: the seed names the pane's real start directory, the session's WorkDir,
// not the daemon's; with no WorkDir it falls back to the daemon's directory.
func TestSeederUsesTheSessionWorkDirNotTheDaemonDirectory(t *testing.T) {
	workDir := t.TempDir()
	cfg, _ := seedFor(t, workDir, nil)
	if projects := projectsOf(t, cfg); projects[workDir]["trust_level"] != "untrusted" {
		t.Fatalf("projects = %v, want %s untrusted", projects, workDir)
	}
	daemonDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ = seedFor(t, "", nil)
	if projects := projectsOf(t, cfg); projects[daemonDir]["trust_level"] != "untrusted" {
		t.Fatalf("projects = %v, want the daemon directory %s untrusted when WorkDir is empty", projects, daemonDir)
	}
}
