package main

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/runtime"
)

// The quick start has to work from an installed binary, with no source tree and
// nothing to build. `marvel example demo` prints a manifest the binary carries,
// and the manifest runs a shell loop, so the only thing it needs is tmux.

func runExample(t *testing.T, args ...string) (string, error) {
	t.Helper()
	// Stdout and stderr stay apart: `marvel example demo > demo.toml` must put
	// the manifest, and only the manifest, in the file.
	var out, errOut bytes.Buffer
	cmd := exampleCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if len(args) > 0 && err == nil && errOut.Len() > 0 {
		t.Errorf("a successful marvel example %v wrote to stderr: %q", args, errOut.String())
	}
	return out.String(), err
}

func TestExampleDemoParsesAndNeedsNoBuildTree(t *testing.T) {
	out, err := runExample(t, "demo")
	if err != nil {
		t.Fatalf("marvel example demo: %v\n%s", err, out)
	}
	m, err := api.ParseManifestBytes([]byte(out))
	if err != nil {
		t.Fatalf("the printed manifest does not parse: %v\n%s", err, out)
	}
	if err := m.Apply(api.NewStore()); err != nil {
		t.Fatalf("the printed manifest does not apply: %v", err)
	}
	if len(m.Teams) == 0 {
		t.Fatal("the manifest names no team")
	}
	for _, team := range m.Teams {
		if team.WorkDir != "" && !filepath.IsAbs(team.WorkDir) {
			t.Errorf("team %s workdir %q is relative to a build tree", team.Name, team.WorkDir)
		}
		for _, role := range team.Roles {
			rt := role.Runtime
			if rt.Command != "sh" {
				t.Errorf("role %s runs %q; the demo may run only sh, so tmux is all it needs", role.Name, rt.Command)
			}
			if rt.Script != "" {
				t.Errorf("role %s names a script file %q; a source tree is not there", role.Name, rt.Script)
			}
			for _, a := range append([]string{rt.Command}, rt.Args...) {
				if strings.Contains(a, "../") || strings.Contains(a, "bin/") {
					t.Errorf("role %s argument %q points into a build tree", role.Name, a)
				}
			}
			if role.WorkDir != "" && !filepath.IsAbs(role.WorkDir) {
				t.Errorf("role %s workdir %q is relative to a build tree", role.Name, role.WorkDir)
			}
			if role.HealthCheck == nil || role.HealthCheck.Type != string(api.HealthCheckProcessAlive) {
				t.Errorf("role %s health check = %+v, want process-alive", role.Name, role.HealthCheck)
			}
		}
	}
}

// The loop is real: it is a shell command that prints and keeps running, so a
// pane that runs it shows activity. It is run here for a moment with only sh.
func TestExampleDemoLoopRunsUnderPlainSh(t *testing.T) {
	out, err := runExample(t, "demo")
	if err != nil {
		t.Fatal(err)
	}
	m, err := api.ParseManifestBytes([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	rt := m.Teams[0].Roles[0].Runtime
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var got bytes.Buffer
	cmd := exec.CommandContext(ctx, rt.Command, rt.Args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "MARVEL_SESSION=demo/squad-agent-g1-0"}
	cmd.Stdout = &got
	cmd.WaitDelay = 500 * time.Millisecond
	_ = cmd.Run()
	if ctx.Err() == nil {
		t.Errorf("the loop ended by itself; a seat that exits is restarted in a loop")
	}
	if strings.TrimSpace(got.String()) == "" {
		t.Error("the loop printed nothing in three seconds")
	}
}

// The adapter builds the pane's command line from the manifest, and quoting is
// where a shell loop in an args list goes wrong. The command it builds is run
// the way the pane runs it.
func TestExampleDemoCommandSurvivesTheAdapterQuoting(t *testing.T) {
	out, err := runExample(t, "demo")
	if err != nil {
		t.Fatal(err)
	}
	m, err := api.ParseManifestBytes([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	mr := m.Teams[0].Roles[0].Runtime
	sess := &api.Session{
		Name: "squad-agent-g1-0", Workspace: "demo", Team: "squad", Role: "agent",
		Runtime: api.Runtime{Name: mr.Image, Command: mr.Command, Args: mr.Args},
	}
	res, err := (&runtime.Generic{}).Prepare(&runtime.LaunchContext{
		Session:   sess,
		Role:      &api.Role{Name: "agent", Replicas: 2},
		Team:      &api.Team{Name: "squad", Workspace: "demo"},
		Workspace: &api.Workspace{Name: "demo"},
	})
	if err != nil {
		t.Fatalf("the generic adapter refused the demo role: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var got bytes.Buffer
	cmd := exec.CommandContext(ctx, "sh", "-c", res.Command)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "MARVEL_SESSION=demo/squad-agent-g1-0"}
	cmd.Stdout = &got
	cmd.WaitDelay = 500 * time.Millisecond
	_ = cmd.Run()
	if !strings.Contains(got.String(), "demo/squad-agent-g1-0 alive at ") {
		t.Errorf("the adapter's command line printed %q, want the loop's line with the session name", got.String())
	}
}

func TestExampleListsItsNamesAndRefusesAnUnknownOne(t *testing.T) {
	list, err := runExample(t)
	if err != nil {
		t.Fatalf("marvel example: %v", err)
	}
	if !strings.Contains(list, "demo") {
		t.Errorf("the list does not name demo: %q", list)
	}
	_, err = runExample(t, "nosuch")
	if err == nil {
		t.Fatal("an unknown example name was accepted")
	}
	if !strings.Contains(err.Error(), "nosuch") || !strings.Contains(err.Error(), "demo") {
		t.Errorf("the refusal does not name the bad name and the known ones: %v", err)
	}
}

// The verb is on the root command, so the installed binary answers it.
func TestExampleIsRegisteredOnTheRootCommand(t *testing.T) {
	found, rest, err := newRootCmd().Find([]string{"example", "demo"})
	if err != nil {
		t.Fatalf("marvel example demo does not resolve: %v", err)
	}
	if found.Name() != "example" || len(rest) != 1 || rest[0] != "demo" {
		t.Errorf("resolved %q with args %v, want the example verb with demo", found.Name(), rest)
	}
}
