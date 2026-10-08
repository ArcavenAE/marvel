package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
)

// writeClientConfig puts raw bytes where config.Load reads them.
func writeClientConfig(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".marvel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A config file that does not parse is not a reason to refuse a listing: the
// table is the default, as an unreadable config already leaves the default
// socket.
func TestUnreadableConfigFallsBackToTheDefaultColumns(t *testing.T) {
	resolveFixture(t, false)
	writeClientConfig(t, "clusters: [this is: not valid yaml\n  - : :\n")
	if _, err := config.Load(); err == nil {
		t.Fatal("setup: the fixture config should not parse")
	}

	cols, err := loadSessionColumns("")
	if err != nil {
		t.Fatalf("loadSessionColumns with an unreadable config: %v, want the default table", err)
	}
	if got := headersOf(cols); !reflect.DeepEqual(got, todaysHeaders) {
		t.Errorf("headers = %v, want %v", got, todaysHeaders)
	}
}

// --columns overrides the preference outright, so a broken preference is not
// even looked at: an operator can fix a bad name in the config by running
// with a flag.
func TestColumnsFlagOverridesABrokenPreference(t *testing.T) {
	resolveFixture(t, false)
	if err := config.Save(&config.Config{Display: config.Display{SessionColumns: []string{"nope"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSessionColumns(""); err == nil {
		t.Fatal("setup: the preference names an unknown column and should be refused on its own")
	}

	cols, err := loadSessionColumns("name")
	if err != nil {
		t.Fatalf("--columns name over a broken preference: %v, want the flag to win", err)
	}
	if got, want := headersOf(cols), []string{"AGENT NAME"}; !reflect.DeepEqual(got, want) {
		t.Errorf("headers = %v, want %v", got, want)
	}

	// And an unreadable config does not stop a flag either.
	writeClientConfig(t, "clusters: [this is: not valid yaml\n")
	if _, err := loadSessionColumns("state"); err != nil {
		t.Errorf("--columns state over an unreadable config: %v, want it to work", err)
	}
}

// fakeSocket serves every request through handler on a short-path unix
// socket and returns its path.
func fakeSocket(t *testing.T, handler func(daemon.Request) daemon.Response) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemon.Request
			if err := json.NewDecoder(c).Decode(&req); err == nil {
				_ = json.NewEncoder(c).Encode(handler(req))
			}
			_ = c.Close()
		}
	}()
	return socket
}

func watchFixtureSessions() []api.Session {
	return []api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3",
	}}
}

// Watch mode renders the selected columns, not the default table: both on a
// live frame and on the last-known frame it shows while the daemon is away.
func TestWatchRendersTheSelectedColumns(t *testing.T) {
	resolveFixture(t, false)
	cols, err := selectSessionColumns("state,name", nil)
	if err != nil {
		t.Fatal(err)
	}

	live := fakeSocket(t, func(req daemon.Request) daemon.Response {
		data, _ := json.Marshal(watchFixtureSessions())
		return daemon.Response{Result: data}
	})
	t.Setenv(config.SocketEnv, live)
	ws := newWatchState(cols)
	frame := renderWatch(ws)
	if got := frameHeader(frame); !reflect.DeepEqual(got, []string{"STATE", "AGENT NAME"}) {
		t.Errorf("live frame header = %v, want only STATE and AGENT NAME:\n%s", got, frame)
	}

	// The daemon is gone: the last-known sessions still render in the chosen columns.
	t.Setenv(config.SocketEnv, filepath.Join(t.TempDir(), "gone.sock"))
	ws.lastSessions = watchFixtureSessions()
	frame = renderWatch(ws)
	if !strings.Contains(frame, "last known state") {
		t.Fatalf("setup: the daemon is gone and the frame should say so:\n%s", frame)
	}
	if got := frameHeader(frame); !reflect.DeepEqual(got, []string{"STATE", "AGENT NAME"}) {
		t.Errorf("last-known frame header = %v, want only STATE and AGENT NAME:\n%s", got, frame)
	}
}

// frameHeader is the table header line of a watch frame: the line that
// carries the AGENT NAME column.
func frameHeader(frame string) []string {
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, "AGENT NAME") {
			return splitColumns(line)
		}
	}
	return nil
}

// The watch screen starts sorted by name, ascending, in the columns it was
// given, so a first frame lists the same order `get sessions` does.
func TestNewWatchStateStartsByNameAscending(t *testing.T) {
	cols, err := selectSessionColumns("state,name", nil)
	if err != nil {
		t.Fatal(err)
	}
	ws := newWatchState(cols)
	if ws.column != "name" || ws.desc {
		t.Errorf("watch starts sorted by %q desc=%v, want name ascending", ws.column, ws.desc)
	}
	if got := headersOf(ws.columns); !reflect.DeepEqual(got, []string{"STATE", "AGENT NAME"}) {
		t.Errorf("watch columns = %v, want the ones passed in", got)
	}
}
