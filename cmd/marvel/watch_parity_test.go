package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
)

// parityDaemon serves the sessions and a daemon.status whose cluster name
// the test can change between frames.
func parityDaemon(t *testing.T, cluster *atomic.Value) {
	t.Helper()
	resolveFixture(t, false)
	sessions := []api.Session{
		fitSession("agent-b"), fitSession("agent-a"),
		{
			Name: "agent-c", Workspace: "ws", Team: "squad", Role: "worker",
			State: api.SessionRunning, HealthState: api.HealthHealthy, PaneID: "%4",
			Runtime: api.Runtime{Name: "claude", Command: "/usr/local/bin/some-runtime"}, WorkDir: "/srv/work/a/rather/long/directory/name/here",
		},
	}
	socket := fakeSocket(t, func(req daemon.Request) daemon.Response {
		switch req.Method {
		case "get":
			data, _ := json.Marshal(sessions)
			return daemon.Response{Result: data}
		case "daemon.status":
			data, _ := json.Marshal(daemon.DaemonStatus{
				Cluster: cluster.Load().(string), MRVL: daemon.MRVLStatus{State: daemon.MRVLOff},
			})
			return daemon.Response{Result: data}
		}
		return daemon.Response{Error: "unexpected " + req.Method}
	})
	t.Setenv(config.SocketEnv, socket)
}

func setWidth(t *testing.T, width int) {
	t.Helper()
	old, oldTTY := terminalWidth, stdoutIsTTY
	terminalWidth = func() int { return width }
	stdoutIsTTY = func() bool { return width > 0 }
	t.Cleanup(func() { terminalWidth, stdoutIsTTY = old, oldTTY })
}

func plainSessions(t *testing.T, args ...string) string {
	t.Helper()
	return captureStdout(t, func() {
		cmd := getCmd()
		cmd.SetArgs(append([]string{"sessions"}, args...))
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		if err := cmd.Execute(); err != nil {
			t.Errorf("get sessions %v: %v", args, err)
		}
	})
}

func newCluster() *atomic.Value {
	v := &atomic.Value{}
	v.Store("alpha")
	return v
}

// watchBody is a watch frame without its two title lines and the blank line
// after them: what is left is what plain get sessions prints.
func watchBody(frame string) string {
	lines := strings.SplitN(frame, "\n", 4)
	if len(lines) < 4 {
		return frame
	}
	return lines[3]
}

// Guard, passing on main: plain get sessions output is pinned byte for byte
// at four widths, so the shared render extracted for watch mode provably
// changes nothing on the plain path. Regenerate with UPDATE_GOLDEN=1.
func TestPlainGetSessionsOutputIsUnchanged(t *testing.T) {
	for _, width := range []int{0, 80, 120, 200} {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			parityDaemon(t, newCluster())
			setWidth(t, width)
			got := strings.ReplaceAll(plainSessions(t), os.Getenv(config.SocketEnv), "<socket>")
			path := filepath.Join("testdata", fmt.Sprintf("get-sessions-plain-%d.golden", width))
			if os.Getenv("UPDATE_GOLDEN") != "" {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("plain get sessions at %d columns changed:\n--- got ---\n%s\n--- want ---\n%s", width, got, want)
			}
		})
	}
}

// -w shows what plain get sessions shows at the same width: the daemon
// header, the fitted table and the hidden-columns note (marvel#676).
func TestWatchFrameMatchesPlainGetSessions(t *testing.T) {
	for _, width := range []int{80, 120, 200} {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			parityDaemon(t, newCluster())
			setWidth(t, width)
			cols, explicit, err := loadSessionColumnsSel("")
			if err != nil {
				t.Fatal(err)
			}
			plain := plainSessions(t)
			frame := renderWatch(newWatchScreen(cols, explicit, false), time.Second)
			if got := watchBody(frame); got != plain {
				t.Errorf("watch body differs from plain output at %d columns:\n--- watch ---\n%s\n--- plain ---\n%s", width, got, plain)
			}
		})
	}
}

// Every frame carries the header, and it is read again on every frame, so a
// daemon whose status changed is not shown stale.
func TestWatchHeaderIsReadOnEveryFrame(t *testing.T) {
	cluster := newCluster()
	parityDaemon(t, cluster)
	setWidth(t, 200)
	cols, explicit, _ := loadSessionColumnsSel("")
	ws := newWatchScreen(cols, explicit, false)

	first := renderWatch(ws, time.Second)
	if !strings.Contains(first, "mrvl://") || !strings.Contains(first, "alpha") {
		t.Fatalf("first frame should carry the header with the cluster name:\n%s", first)
	}
	cluster.Store("bravo")
	second := renderWatch(ws, time.Second)
	if !strings.Contains(second, "bravo") || strings.Contains(second, "alpha") {
		t.Errorf("second frame should show the changed status:\n%s", second)
	}
}

// The width is read on every frame, so a resize shows on the next tick.
func TestWatchFramePicksUpAResize(t *testing.T) {
	parityDaemon(t, newCluster())
	width := 200
	old, oldTTY := terminalWidth, stdoutIsTTY
	terminalWidth = func() int { return width }
	stdoutIsTTY = func() bool { return true }
	t.Cleanup(func() { terminalWidth, stdoutIsTTY = old, oldTTY })
	cols, explicit, _ := loadSessionColumnsSel("")
	ws := newWatchScreen(cols, explicit, false)

	wide := renderWatch(ws, time.Second)
	if strings.Contains(wide, "hidden") {
		t.Fatalf("setup: nothing is hidden at 200:\n%s", wide)
	}
	width = 80
	narrow := renderWatch(ws, time.Second)
	if !strings.Contains(narrow, "11 columns hidden at this width") {
		t.Errorf("after the resize the frame should fit 80 columns and say what it hid:\n%s", narrow)
	}
}

// An explicit column list that does not fit warns and prints in full, as
// plain output does; --no-trunc reaches the watch frame too.
func TestWatchFrameHonorsExplicitColumnsAndNoTrunc(t *testing.T) {
	parityDaemon(t, newCluster())
	setWidth(t, 80)
	cols, explicit, err := loadSessionColumnsSel("wide")
	if err != nil {
		t.Fatal(err)
	}
	frame := renderWatch(newWatchScreen(cols, explicit, false), time.Second)
	if !strings.Contains(frame, "wider than the terminal") {
		t.Errorf("an explicit list wider than the terminal should warn, as plain does:\n%s", frame)
	}

	setWidth(t, 120)
	cols, explicit, _ = loadSessionColumnsSel("name,workdir")
	trimmed := renderWatch(newWatchScreen(cols, explicit, false), time.Second)
	full := renderWatch(newWatchScreen(cols, explicit, true), time.Second)
	if !strings.Contains(trimmed, "…") || strings.Contains(full, "…") {
		t.Errorf("--no-trunc should leave WORKDIR uncut in the watch frame:\n--- trimmed ---\n%s\n--- full ---\n%s", trimmed, full)
	}
}

// With the daemon away, the last-known frame still carries the header (which
// says the daemon gave no status) and fits the table.
func TestWatchLastKnownFrameKeepsHeaderAndFit(t *testing.T) {
	resolveFixture(t, false)
	setWidth(t, 80)
	t.Setenv(config.SocketEnv, filepath.Join(t.TempDir(), "gone.sock"))
	cols, explicit, _ := loadSessionColumnsSel("")
	ws := newWatchScreen(cols, explicit, false)
	ws.lastSessions = []api.Session{fitSession("agent-a")}
	frame := renderWatch(ws, time.Second)
	if !strings.Contains(frame, "last known state") || !strings.Contains(frame, "no status") {
		t.Errorf("the frame should say the daemon is away and still print a header:\n%s", frame)
	}
	if !strings.Contains(frame, "columns hidden at this width") {
		t.Errorf("the last-known table should be fitted to 80 columns:\n%s", frame)
	}
}
