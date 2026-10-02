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
		{"root that does not exist", workDirManifest(filepath.Join(root, "nope"), "", ""), "workspace.root", "does not exist"},
		{"root that is a file", workDirManifest(file, "", ""), "workspace.root", "not a directory"},
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
