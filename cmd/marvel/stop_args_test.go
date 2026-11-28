package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

func runStop(t *testing.T, socket string, args ...string) (code int, stderr string) {
	t.Helper()
	root := newRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(append([]string{"stop", "--socket", socket}, args...))
	code = execute(root, &errOut)
	return code, errOut.String()
}

// A positional argument to `marvel stop` is refused before any dial, and the
// refusal names the verb that stops one session. Without this, `marvel stop
// ops/some-session --cluster mokuzai` stopped the whole daemon.
func TestStopRefusesPositionalArgumentsWithoutContactingTheDaemon(t *testing.T) {
	resolveFixture(t, false)
	for _, args := range [][]string{
		{"ops/i867-read"},
		{"ops/i867-read", "--teardown"},
		{"--keep-bus", "ops/i867-read"},
		{"one", "two"},
	} {
		socket, requests := auditFakeDaemon(t, func(daemonrpc.Request) daemonrpc.Response {
			return daemonrpc.Response{}
		})
		code, stderr := runStop(t, socket, args...)
		if code == 0 {
			t.Errorf("%v: exit = 0, want nonzero", args)
		}
		if n := len(requests()); n != 0 {
			t.Errorf("%v: the daemon received %d requests, want 0", args, n)
		}
		if !strings.Contains(stderr, "marvel delete session") {
			t.Errorf("%v: stderr = %q, want a hint naming marvel delete session", args, stderr)
		}
	}
}

// The fake daemon in the test above does answer: a bare `marvel stop` still
// reaches it, so a zero count there is the refusal and not a broken fixture.
func TestStopWithoutArgumentsStillContactsTheDaemon(t *testing.T) {
	resolveFixture(t, false)
	socket, requests := auditFakeDaemon(t, func(daemonrpc.Request) daemonrpc.Response {
		return daemonrpc.Response{}
	})
	code, stderr := runStop(t, socket)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q, want 0", code, stderr)
	}
	reqs := requests()
	if len(reqs) != 1 || reqs[0].Method != "stop" {
		t.Errorf("requests = %v, want one stop", reqs)
	}
}
