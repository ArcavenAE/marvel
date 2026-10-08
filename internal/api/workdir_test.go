package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Placement (docs/design/session-working-directory.md, marvel#255 build item 1).

func TestResolveWorkDirPrecedence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                   string
		root, teamDir, roleDir string
		want                   string
	}{
		{"role beats team beats root", "/w", "/w/t", "/w/r", "/w/r"},
		{"team beats root", "/w", "/w/t", "", "/w/t"},
		{"root alone", "/w", "", "", "/w"},
		{"relative role dir resolves against root, not the team", "/w", "/w/t", "r", "/w/r"},
		{"relative team dir resolves against root", "/w", "t", "", "/w/t"},
		{"no root and nothing declared", "", "", "", ""},
		{"absolute dir ignores the root", "/w", "", "/elsewhere", "/elsewhere"},
	}
	for _, tc := range cases {
		if got := ResolveWorkDir(tc.root, tc.teamDir, tc.roleDir); got != tc.want {
			t.Errorf("%s: ResolveWorkDir(%q, %q, %q) = %q, want %q", tc.name, tc.root, tc.teamDir, tc.roleDir, got, tc.want)
		}
	}
}

const workDirTOML = `
[workspace]
name = "wd"
root = "/work/proj"

[[team]]
name = "squad"
workdir = "t"

  [[team.role]]
  name = "worker"
  replicas = 1
  workdir = "sub"

    [team.role.runtime]
    command = "claude"

  [[team.role]]
  name = "other"
  replicas = 1
  workdir = "/abs/other"

    [team.role.runtime]
    command = "claude"
`

const workDirYAML = `
workspace:
  name: wd
  root: /work/proj
teams:
  - name: squad
    workdir: t
    roles:
      - name: worker
        replicas: 1
        workdir: sub
        runtime:
          command: claude
      - name: other
        replicas: 1
        workdir: /abs/other
        runtime:
          command: claude
`

// The keys root and workdir parse in both formats, and Apply carries them to
// the stored workspace, team and roles with relative values resolved against
// the one root. A re-apply with a new root moves them.
func TestManifestWorkDirKeysParseAndApply(t *testing.T) {
	t.Parallel()
	for format, src := range map[string]string{"toml": workDirTOML, "yaml": workDirYAML} {
		m, err := ParseManifestBytes([]byte(src))
		if err != nil {
			t.Fatalf("%s parse: %v", format, err)
		}
		if m.Workspace.Root != "/work/proj" || m.Teams[0].WorkDir != "t" || m.Teams[0].Roles[0].WorkDir != "sub" {
			t.Fatalf("%s parsed root=%q team=%q role=%q, want /work/proj, t, sub", format,
				m.Workspace.Root, m.Teams[0].WorkDir, m.Teams[0].Roles[0].WorkDir)
		}
		store := NewStore()
		if err := m.Apply(store); err != nil {
			t.Fatalf("%s apply: %v", format, err)
		}
		ws, err := store.GetWorkspace("wd")
		if err != nil || ws.Root != "/work/proj" {
			t.Fatalf("%s stored workspace root = %q (%v), want /work/proj", format, ws.Root, err)
		}
		team, err := store.GetTeam("wd/squad")
		if err != nil {
			t.Fatal(err)
		}
		if team.WorkDir != "/work/proj/t" || team.Roles[0].WorkDir != "/work/proj/sub" || team.Roles[1].WorkDir != "/abs/other" {
			t.Fatalf("%s stored team=%q roles=%q,%q, want /work/proj/t, /work/proj/sub, /abs/other", format,
				team.WorkDir, team.Roles[0].WorkDir, team.Roles[1].WorkDir)
		}

		m.Workspace.Root = "/moved"
		if err := m.Apply(store); err != nil {
			t.Fatalf("%s re-apply: %v", format, err)
		}
		ws, _ = store.GetWorkspace("wd")
		team, _ = store.GetTeam("wd/squad")
		if ws.Root != "/moved" || team.WorkDir != "/moved/t" || team.Roles[0].WorkDir != "/moved/sub" {
			t.Fatalf("%s after re-apply root=%q team=%q role=%q, want /moved, /moved/t, /moved/sub", format,
				ws.Root, team.WorkDir, team.Roles[0].WorkDir)
		}
	}
}

func workDirManifest(root, teamDir, roleDir string) *Manifest {
	return &Manifest{
		Workspace: ManifestWorkspace{Name: "wd", Root: root},
		Teams: []ManifestTeam{{
			Name:    "squad",
			WorkDir: teamDir,
			Roles:   []ManifestRole{{Name: "worker", Replicas: 1, WorkDir: roleDir}},
		}},
	}
}

func TestValidateWorkDirs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		m         *Manifest
		wantErr   string // empty means no error
		wantAdvis string // empty means no advisory
	}{
		{"absolute root, nothing else", workDirManifest(root, "", ""), "", ""},
		{"missing root warns and places nothing", workDirManifest("", "", ""), "", "workspace.root"},
		{"relative root is refused", workDirManifest("rel/root", "", ""), "workspace.root", "not absolute"},
		{"tilde root is refused as not absolute", workDirManifest("~/proj", "", ""), "workspace.root", "not absolute"},
		{"root that does not exist here warns and places nothing", workDirManifest(filepath.Join(root, "nope"), "", ""), "", "does not exist on this host"},
		{"root that is a file here warns and places nothing", workDirManifest(file, "", ""), "", "not a directory"},
		{"absent root with a relative team workdir is refused", workDirManifest(filepath.Join(root, "nope"), "sub", ""), "workspace.root", "does not exist"},
		{"absent root with a relative role workdir is refused", workDirManifest(filepath.Join(root, "nope"), "", "sub"), "workspace.root", "does not exist"},
		{"relative team workdir resolves against root", workDirManifest(root, "sub", ""), "", ""},
		{"team workdir that does not exist", workDirManifest(root, "nope", ""), "team squad", "does not exist"},
		{"role workdir with a tilde", workDirManifest(root, "", "~/proj"), "role worker", "not absolute"},
		{"absolute role workdir that exists", workDirManifest(root, "", filepath.Join(root, "sub")), "", ""},
		{"relative workdir with no root", workDirManifest("", "sub", ""), "workspace.root", "relative"},
	}
	for _, tc := range cases {
		advis, err := tc.m.ValidateWorkDirs()
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: error %v, want none", tc.name, err)
			}
			if got := strings.Join(advis, "; "); (tc.wantAdvis == "") != (got == "") || !strings.Contains(got, tc.wantAdvis) {
				t.Errorf("%s: advisories %q, want one containing %q", tc.name, got, tc.wantAdvis)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: no error, want one containing %q and %q", tc.name, tc.wantErr, tc.wantAdvis)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), tc.wantAdvis) {
			t.Errorf("%s: error %q, want it to contain %q and %q", tc.name, err, tc.wantErr, tc.wantAdvis)
		}
	}
}

// A root the daemon's host cannot see is a client-side path (marvel work posts
// its own directory): the apply goes through, the root is dropped, and nothing
// is placed, exactly as for an absent root.
func TestValidateWorkDirsDropsARootThisHostCannotSee(t *testing.T) {
	t.Parallel()
	m := workDirManifest(filepath.Join(t.TempDir(), "client-only"), "", "")
	advis, err := m.ValidateWorkDirs()
	if err != nil {
		t.Fatalf("error %v, want the apply to go through", err)
	}
	if m.Workspace.Root != "" {
		t.Errorf("root = %q after validation, want it dropped", m.Workspace.Root)
	}
	if len(advis) != 1 || !strings.Contains(advis[0], "client-only") {
		t.Errorf("advisories = %q, want one naming the root", advis)
	}
}

// The root is resolved through symlinks once, at apply, so a stored anchor
// does not flip between the link and its target.
func TestValidateWorkDirsResolvesTheRootThroughSymlinks(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	real := filepath.Join(base, "real")
	link := filepath.Join(base, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	m := workDirManifest(link, "", "")
	if _, err := m.ValidateWorkDirs(); err != nil {
		t.Fatal(err)
	}
	if m.Workspace.Root != want {
		t.Errorf("root = %q, want the resolved %q", m.Workspace.Root, want)
	}
}

// realDir returns a fresh temp directory with symlinks resolved, which is the
// form Apply stores (macOS temp directories sit behind a link).
func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func rootedTeamManifest(t *testing.T, team, root string) *Manifest {
	t.Helper()
	m := workDirManifest(root, "", "")
	m.Workspace.Name = "shared"
	m.Teams[0].Name = team
	return m
}

// Workspace.Root is per workspace and every `marvel work` posts its own
// directory. A team applied from another directory must not move a team that
// is already running: each team keeps the root it was applied with.
func TestApplySnapshotsTheRootIntoTheTeam(t *testing.T) {
	t.Parallel()
	store := NewStore()
	rootA, rootB := realDir(t), realDir(t)
	for team, root := range map[string]string{"alpha": rootA, "beta": rootB} {
		if err := rootedTeamManifest(t, team, root).Apply(store); err != nil {
			t.Fatal(err)
		}
	}
	ws, _ := store.GetWorkspace("shared")
	for team, root := range map[string]string{"alpha": rootA, "beta": rootB} {
		got, _ := store.GetTeam("shared/" + team)
		if dir := ResolveWorkDir(ws.Root, got.WorkDir, ""); dir != root {
			t.Errorf("team %s resolves to %q with the workspace root at %q, want its own %q", team, dir, ws.Root, root)
		}
	}
}

// An apply that declares no placement leaves a team's anchor where it was.
func TestApplyWithNoPlacementKeepsTheTeamAnchor(t *testing.T) {
	t.Parallel()
	store := NewStore()
	root := realDir(t)
	if err := rootedTeamManifest(t, "alpha", root).Apply(store); err != nil {
		t.Fatal(err)
	}
	if err := rootedTeamManifest(t, "alpha", "").Apply(store); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetTeam("shared/alpha")
	if got.WorkDir != root {
		t.Errorf("anchor = %q after a root-less re-apply, want %q kept", got.WorkDir, root)
	}
}

// A relative workdir with no root to resolve it against is the case an operator
// posting through the raw API hits. The refusal says what fixes it: marvel work
// sends the manifest's directory, and a raw post sets the root itself.
func TestRelativeWorkdirWithNoRootNamesTheFix(t *testing.T) {
	t.Parallel()
	_, err := workDirManifest("", "sub", "").ValidateWorkDirs()
	if err == nil {
		t.Fatal("a relative workdir with no root was accepted")
	}
	for _, want := range []string{"relative", "workspace.root", "marvel work", "the workspace_root parameter"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}
