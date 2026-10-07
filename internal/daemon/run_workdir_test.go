package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An ad-hoc run places explicitly (design decision 8), and what the daemon
// cannot place it refuses when it was asked for and drops when it was a default.
func TestResolveRunWorkDir(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(real, "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(real, "gone")

	cases := []struct {
		name      string
		dir       string
		isDefault bool
		want      string // resolved
		wantErr   []string
		wantWarn  string
	}{
		{name: "nothing asked, nothing placed"},
		{name: "an existing directory", dir: real, want: real},
		{name: "a symlinked directory is stored by its real path", dir: link, want: real},
		{name: "a relative directory is refused", dir: "rel/dir", wantErr: []string{"--workdir", "not absolute"}},
		{name: "a tilde is refused", dir: "~/proj", wantErr: []string{"--workdir", "not absolute"}},
		{name: "an explicit directory that is absent is refused", dir: gone, wantErr: []string{"--workdir", "does not exist"}},
		{name: "an explicit file is refused", dir: file, wantErr: []string{"--workdir", "not a directory"}},
		{name: "a default directory that is absent is dropped with a warning", dir: gone, isDefault: true, wantWarn: "not on the daemon's host"},
		{name: "a default file is dropped with a warning", dir: file, isDefault: true, wantWarn: "not on the daemon's host"},
		{name: "a default that exists is placed", dir: real, isDefault: true, want: real},
		{name: "a relative default is still refused", dir: "rel/dir", isDefault: true, wantErr: []string{"not absolute"}},
	}
	for _, tc := range cases {
		got, warn, err := resolveRunWorkDir(tc.dir, tc.isDefault)
		if len(tc.wantErr) > 0 {
			if err == nil {
				t.Errorf("%s: accepted, want a refusal", tc.name)
				continue
			}
			for _, w := range tc.wantErr {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%s: error = %q, want it to contain %q", tc.name, err, w)
				}
			}
			continue
		}
		if err != nil || got != tc.want || (tc.wantWarn == "") != (warn == "") || !strings.Contains(warn, tc.wantWarn) {
			t.Errorf("%s: = %q, warning %q, %v; want %q, warning containing %q", tc.name, got, warn, err, tc.want, tc.wantWarn)
		}
	}
}

// The session an ad-hoc run creates carries the directory, so the pane starts
// there and get sessions shows it.
func TestRunPlacesTheSessionInTheWorkdir(t *testing.T) {
	d := newHandlerDaemon(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{
		"workspace": "runwd", "team": "adhoc", "role": "adhoc",
		"runtime_command": "sleep", "runtime_args": []string{"300"}, "workdir": dir,
	})
	resp := d.handleRun(params)
	if resp.Error != "" {
		t.Fatalf("run: %s", resp.Error)
	}
	var out map[string]string
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	sess, err := d.store.GetSession(out["session_key"])
	if err != nil {
		t.Fatal(err)
	}
	if sess.WorkDir != dir {
		t.Errorf("session workdir = %q, want %q", sess.WorkDir, dir)
	}
	t.Cleanup(func() { _ = d.sessMgr.CleanupWorkspace("runwd") })
}

// A run whose --workdir is absent is refused before a session exists.
func TestRunRefusesAnAbsentWorkdirBeforeCreatingASession(t *testing.T) {
	d := newHandlerDaemon(t)
	params, _ := json.Marshal(map[string]any{
		"workspace": "runwd2", "runtime_command": "sleep", "runtime_args": []string{"300"},
		"workdir": filepath.Join(t.TempDir(), "gone"),
	})
	resp := d.handleRun(params)
	if resp.Error == "" || !strings.Contains(resp.Error, "does not exist") {
		t.Fatalf("error = %q, want a refusal that the directory does not exist", resp.Error)
	}
	for _, sess := range d.store.ListSessions() {
		if sess.Workspace == "runwd2" {
			t.Errorf("session %s exists after a refused run", sess.Key())
		}
	}
}
