package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/shorttmp"
)

func stampOf(t *testing.T, b daemon.Build) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestBuildMismatchWarning: a stamped build that differs warns with the same
// words marvel version uses; a match, a missing stamp (an older daemon) and an
// unreadable one say nothing. A difference marvel version reports only as a
// note (a dirty tree at the same commit) is not a warning here, so a verb
// does not print it.
func TestBuildMismatchWarning(t *testing.T) {
	t.Parallel()
	dirty := clientBuild
	dirty.Commit, dirty.Dirty = "bc327df", true
	clean := dirty
	clean.Dirty = false
	tests := []struct {
		name   string
		client daemon.Build
		stamp  json.RawMessage
		want   string
	}{
		{"a different version warns", clientBuild, stampOf(t, daemonBuild), "the running daemon is a different build"},
		{"the same build is quiet", clientBuild, stampOf(t, clientBuild), ""},
		{"an older daemon with no stamp is quiet", clientBuild, nil, ""},
		{"an unreadable stamp is quiet", clientBuild, json.RawMessage(`"not a build"`), ""},
		{"a dirty tree at the same commit is a note, not a warning", clean, stampOf(t, dirty), ""},
	}
	for _, tc := range tests {
		got := buildMismatchWarning(tc.client, tc.stamp)
		switch {
		case tc.want == "" && got != "":
			t.Errorf("%s: warned %q, want nothing", tc.name, got)
		case tc.want != "" && !strings.Contains(got, tc.want):
			t.Errorf("%s: warning %q, want it to contain %q", tc.name, got, tc.want)
		}
	}
}

// mismatchSocket is a daemon that answers every method with an empty result,
// stamped with a build no test binary has.
func mismatchSocket(t *testing.T) {
	t.Helper()
	resolveFixture(t, false)
	t.Setenv("MARVEL_HOME", shorttmp.Dir(t))
	other := stampOf(t, daemon.Build{Version: "9.9.9", Channel: "stable", Commit: "abcdef0"})
	socket := headerSocket(t, func(req daemonrpc.Request) daemonrpc.Response {
		return daemonrpc.Response{Result: json.RawMessage(`{}`), DaemonBuild: other}
	})
	t.Setenv(config.SocketEnv, socket)
	buildWarned.Store(false)
	t.Cleanup(func() { buildWarned.Store(false) })
}

// TestEveryVerbWarnsOnceOnABuildMismatch: any verb that reaches the daemon
// warns when the builds differ, and a command that calls the daemon twice
// (as get -w does on every poll) prints the warning once.
func TestEveryVerbWarnsOnceOnABuildMismatch(t *testing.T) {
	mismatchSocket(t)
	errOut := captureStderr(t, func() {
		for _, method := range []string{"get", "describe"} {
			if _, err := send(daemonrpc.Request{Method: method}); err != nil {
				t.Errorf("send %s: %v", method, err)
			}
		}
	})
	if n := strings.Count(errOut, "warning: the running daemon is a different build (9.9.9, stable)"); n != 1 {
		t.Errorf("the build warning should print once for two calls, printed %d times:\n%s", n, errOut)
	}
}

// TestVersionVerbDoesNotRepeatTheBuildWarning: marvel version prints its own
// comparison on stdout, so its call to the daemon adds nothing on stderr.
func TestVersionVerbDoesNotRepeatTheBuildWarning(t *testing.T) {
	mismatchSocket(t)
	errOut := captureStderr(t, func() {
		if _, err := send(daemonrpc.Request{Method: daemonrpc.MethodVersion}); err != nil {
			t.Errorf("send version: %v", err)
		}
	})
	if strings.Contains(errOut, "different build") {
		t.Errorf("the version call printed the verb warning too:\n%s", errOut)
	}
}
