package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

const remoteSSHLine = "ssh -t box.example tmux -L marvel-ff00ff00 attach -t %23"

// A remote desk without --print is run over the mrvl:// channel. The plan
// carries the desk, and keeps the ssh line only as the older-daemon fallback.
func TestPlanAttachRemoteRunsOverMrvl(t *testing.T) {
	plan, err := planAttach("23", deskRows("%23"), remoteTarget, "", false)
	if err != nil {
		t.Fatalf("planAttach: %v", err)
	}
	if !plan.Mrvl || plan.Desk != "23" || plan.Exec {
		t.Errorf("plan = %+v, want Mrvl for desk 23 and no local exec", plan)
	}
	want := []string{"ssh", "-t", "box.example", "tmux", "-L", "marvel-ff00ff00", "attach", "-t", "%23"}
	if !reflect.DeepEqual(plan.Argv, want) {
		t.Errorf("fallback argv = %v, want %v", plan.Argv, want)
	}
}

// A success writes nothing: the terminal was the output.
func TestRunRemoteAttachRunsTheChannel(t *testing.T) {
	var gotAddr, gotDesk string
	calls := 0
	env := attachEnv{Mrvl: func(addr, desk string) error {
		calls++
		gotAddr, gotDesk = addr, desk
		return nil
	}}
	var out, errw bytes.Buffer
	plan := attachPlan{Mrvl: true, Desk: "23", Argv: strings.Fields(remoteSSHLine)}
	if err := runRemoteAttach(&out, &errw, "mrvl://box.example", plan, env); err != nil {
		t.Fatalf("runRemoteAttach: %v", err)
	}
	if calls != 1 || gotAddr != "mrvl://box.example" || gotDesk != "23" {
		t.Errorf("channel run %d times for %q desk %q, want once for mrvl://box.example desk 23", calls, gotAddr, gotDesk)
	}
	if out.Len() != 0 || errw.Len() != 0 {
		t.Errorf("wrote stdout %q stderr %q, want nothing", out.String(), errw.String())
	}
}

// Against a daemon without the channel, the readable failure: the ssh line on
// stdout, a note that names the upgrade on stderr, and a nonzero exit.
func TestRunRemoteAttachOlderDaemonPrintsSSHAndFails(t *testing.T) {
	env := attachEnv{Mrvl: func(string, string) error { return daemonrpc.ErrAttachUnsupported }}
	var out, errw bytes.Buffer
	plan := attachPlan{Mrvl: true, Desk: "23", Argv: strings.Fields(remoteSSHLine)}
	err := runRemoteAttach(&out, &errw, "mrvl://box.example", plan, env)
	if err == nil {
		t.Fatal("an older daemon gave exit 0, want a nonzero exit")
	}
	if got := strings.TrimSpace(out.String()); got != remoteSSHLine {
		t.Errorf("stdout = %q, want the ssh line %q", got, remoteSSHLine)
	}
	if !strings.Contains(errw.String(), "upgrade") {
		t.Errorf("stderr = %q, want a note that names the upgrade", errw.String())
	}
}

// Any other failure (a refused key, an unreachable host) is the error itself.
// The ssh line is not printed in its place, so a revoked key never reads as
// a working fallback.
func TestRunRemoteAttachOtherFailuresDoNotFallBack(t *testing.T) {
	refused := &daemonrpc.AuthRefusedError{User: "op", Addr: "box.example:6785"}
	env := attachEnv{Mrvl: func(string, string) error { return refused }}
	var out, errw bytes.Buffer
	plan := attachPlan{Mrvl: true, Desk: "23", Argv: strings.Fields(remoteSSHLine)}
	err := runRemoteAttach(&out, &errw, "mrvl://box.example", plan, env)
	if !errors.Is(err, refused) {
		t.Fatalf("error = %v, want the auth refusal in its chain", err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing: no silent fallback", out.String())
	}
}

// The help no longer says marvel has no remote terminal, and names the run
// form's transport beside the printed ssh form.
func TestAttachHelpDescribesTheRemoteRunForm(t *testing.T) {
	long := attachCmd().Long
	if strings.Contains(long, "adds no remote") {
		t.Errorf("help still says marvel adds no remote terminal:\n%s", long)
	}
	if !strings.Contains(long, "mrvl://") {
		t.Errorf("help does not name mrvl:// as the run form's transport:\n%s", long)
	}
	if !strings.Contains(long, "ssh -t <host>") {
		t.Errorf("help dropped the printed ssh form:\n%s", long)
	}
}

// The process default runs the channel through daemonrpc.Attach; a nil Mrvl
// would make the verb print nothing and succeed on a remote desk.
func TestProcessAttachEnvWiresTheChannel(t *testing.T) {
	if processAttachEnv().Mrvl == nil {
		t.Error("processAttachEnv().Mrvl is nil, want the daemonrpc.Attach runner")
	}
}
