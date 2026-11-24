package session

import (
	"os"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

func prepareManager(t *testing.T) (m *Manager, fakeExec string) {
	t.Helper()
	fakeExec = t.TempDir() + "/sandbox-exec"
	if err := os.WriteFile(fakeExec, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m = NewManager(api.NewStore(), nil)
	m.GOOS, m.SandboxExec = "darwin", fakeExec
	m.SandboxDir, m.RealHome = t.TempDir(), t.TempDir()
	m.NetMounts = func() []string { return nil }
	m.TCCDeny = true
	return m, fakeExec
}

// V2: the composed profile is handed to sandbox-exec inline, so there is no file
// a seat of the same uid could rewrite between the write and the read.
func TestPrepareRunPassesTheProfileInlineAndStagesNoFile(t *testing.T) {
	m, fakeExec := prepareManager(t)
	sess := api.Session{Name: "squad-builder-g1-0", Workspace: "ws", Team: "squad", Role: "builder", WorkDir: "/work/tree", HarnessHome: "/work/home"}
	role := "(version 1)\n(allow default)\n(allow file-read* (subpath (param \"SESSION_DIR\")))\n"
	plan, err := m.PrepareRun(sess, role, api.RunSandboxedRequest{Profile: "(deny network*)\n"})
	if err != nil {
		t.Fatalf("PrepareRun: %v", err)
	}
	defer plan.Remove()
	if len(plan.Prefix) < 3 || plan.Prefix[0] != fakeExec || plan.Prefix[1] != "-p" || plan.Prefix[2] != plan.Composed {
		t.Fatalf("prefix = %q, want [sandbox-exec -p <the composed text> ...]", plan.Prefix)
	}
	if strings.Join(plan.Prefix[3:], " ") != "-D SESSION_DIR=/work/home" {
		t.Errorf("params after the profile = %q, want -D SESSION_DIR=/work/home", plan.Prefix[3:])
	}
	if !strings.Contains(plan.Composed, "(deny network*)") || !strings.Contains(plan.Composed, "/Volumes") {
		t.Errorf("composed text lacks the request or the deny block:\n%s", plan.Composed)
	}
	if ents, err := os.ReadDir(m.SandboxDir); err != nil || len(ents) != 0 {
		t.Errorf("the profile directory holds %d entries (%v), want none: no staged file", len(ents), err)
	}
}

// V3: RunChild is the one run route. It puts the plan's prefix in front of the
// argv, and a missing plan or an empty prefix is refused, so a caller cannot
// run a command without a composed profile.
func TestRunChildPrependsThePlanPrefix(t *testing.T) {
	res, err := RunChild(t.Context(), &RunPlan{Prefix: []string{"/bin/echo", "PREFIX"}}, []string{"arg"},
		t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 0, 4096)
	if err != nil {
		t.Fatalf("RunChild: %v", err)
	}
	if res.Stdout != "PREFIX arg\n" {
		t.Fatalf("stdout = %q, want the prefix then the argv", res.Stdout)
	}
}

func TestRunChildRefusesAMissingPlan(t *testing.T) {
	for name, plan := range map[string]*RunPlan{"nil plan": nil, "empty prefix": {}} {
		if _, err := RunChild(t.Context(), plan, []string{"/usr/bin/true"}, t.TempDir(), nil, 0, 4096); err == nil {
			t.Errorf("%s: RunChild ran the command with no composed profile", name)
		}
	}
}
