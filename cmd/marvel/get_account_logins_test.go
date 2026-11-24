package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// loginsFakeDaemon answers `get` with the given document and records the get
// params it was sent.
func loginsFakeDaemon(t *testing.T, doc api.AccountLogins) (socket string, params func() []daemonrpc.GetParams) {
	t.Helper()
	dir, err := os.MkdirTemp("", "al")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket = filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var (
		mu  sync.Mutex
		got []daemonrpc.GetParams
	)
	result, _ := json.Marshal(doc)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemonrpc.Request
			if err := json.NewDecoder(c).Decode(&req); err == nil && req.Method == "get" {
				var p daemonrpc.GetParams
				_ = json.Unmarshal(req.Params, &p)
				mu.Lock()
				got = append(got, p)
				mu.Unlock()
				_ = json.NewEncoder(c).Encode(daemonrpc.Response{Result: result})
			}
			_ = c.Close()
		}
	}()
	return socket, func() []daemonrpc.GetParams {
		mu.Lock()
		defer mu.Unlock()
		return append([]daemonrpc.GetParams(nil), got...)
	}
}

func loginDoc() api.AccountLogins {
	read := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	exp := read.Add(47*time.Hour + 30*time.Minute)
	hours := 47
	return api.AccountLogins{Enabled: true, Logins: []api.AccountLogin{
		{Account: "claude default - /homes/a", State: "expiring", ExpiresAt: &exp, HoursLeft: &hours, ReadAt: read},
		{Account: "claude default - /homes/b", State: "lapsed", ReadAt: read},
		{Account: "claude default - /homes/c", State: "unreadable", ReadAt: read, Why: "timed out"},
	}}
}

// The verb asks the daemon for account-logins and prints one row per login,
// with a dash where there is no expiry, and the reason of an unreadable one.
func TestGetAccountLoginsPrintsTheRows(t *testing.T) {
	sock, params := loginsFakeDaemon(t, loginDoc())
	code, out, errOut := runFanGet(t, "--socket", sock, "get", "account-logins")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, errOut)
	}
	if got := params(); len(got) != 1 || got[0].ResourceType != "account-logins" {
		t.Fatalf("daemon was sent %+v, want one account-logins get", got)
	}
	for _, want := range []string{
		"ACCOUNT", "STATE", "EXPIRES", "LEFT", "READ", "WHY",
		"claude default - /homes/a", "expiring", "2026-11-25T11:30:00Z", "47h", "2026-11-23T12:00:00Z",
		"lapsed", "unreadable", "timed out",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "/homes/b") && !strings.Contains(l, " - ") {
			t.Errorf("the lapsed row shows no dash for its missing expiry: %q", l)
		}
	}
}

// -o json prints the daemon's document; off and empty are lines that say so.
func TestGetAccountLoginsJSONOffAndEmpty(t *testing.T) {
	sock, _ := loginsFakeDaemon(t, loginDoc())
	code, out, errOut := runFanGet(t, "--socket", sock, "get", "account-logins", "-o", "json")
	if code != 0 {
		t.Fatalf("exit %d; stderr %q", code, errOut)
	}
	var got api.AccountLogins
	if err := json.Unmarshal([]byte(out), &got); err != nil || !got.Enabled || len(got.Logins) != 3 {
		t.Fatalf("-o json = %q (%v), want the document with three logins", out, err)
	}
	off, _ := loginsFakeDaemon(t, api.AccountLogins{Logins: []api.AccountLogin{}})
	_, out, _ = runFanGet(t, "--socket", off, "get", "account-logins")
	if want := "login expiry is off on this daemon: set login_expiry: true on its cluster in the marvel config to turn it on"; !strings.Contains(out, want) {
		t.Errorf("off daemon printed %q, want %q", out, want)
	}
	empty, _ := loginsFakeDaemon(t, api.AccountLogins{Enabled: true, Logins: []api.AccountLogin{}})
	_, out, _ = runFanGet(t, "--socket", empty, "get", "account-logins")
	if !strings.Contains(out, "no logins read yet") {
		t.Errorf("empty list printed %q, want a line saying none was read yet", out)
	}
}

// Refusals come before any dial.
func TestGetAccountLoginsFlagRules(t *testing.T) {
	sock, params := loginsFakeDaemon(t, loginDoc())
	// Positive control: the plain form is accepted, so the refusals below are
	// about the flags and not about the verb.
	if code, _, stderr := runFanGet(t, "--socket", sock, "get", "account-logins"); code != 0 {
		t.Fatalf("get account-logins: exit = %d, want 0\n%s", code, stderr)
	}
	before := len(params())
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"since", []string{"get", "account-logins", "--since", "1h"}, "--account and --since apply to get account-readings only"},
		{"account", []string{"get", "account-logins", "--account", "x"}, "--account and --since apply to get account-readings only"},
		{"yaml", []string{"get", "account-logins", "-o", "yaml"}, "-o yaml is not supported"},
		{"watch", []string{"get", "account-logins", "--watch"}, "get account-logins does not support --watch"},
		{"columns", []string{"get", "account-logins", "--columns", "key"}, "--columns applies to get sessions only"},
	}
	for _, c := range cases {
		code, _, stderr := runFanGet(t, append([]string{"--socket", sock}, c.args...)...)
		if code != 1 {
			t.Errorf("%s: exit = %d, want 1\n%s", c.name, code, stderr)
		}
		if !strings.Contains(stderr, c.want) {
			t.Errorf("%s: stderr lacks %q:\n%s", c.name, c.want, stderr)
		}
	}
	if n := len(params()) - before; n != 0 {
		t.Errorf("%d reads reached the daemon from refused commands, want 0", n)
	}
}

// An unreadable login shows its last good read and when the failures began, so
// "unreadable" is not read as "just now".
func TestGetAccountLoginsShowsWhenAnUnreadableLoginFirstFailed(t *testing.T) {
	good := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	since := good.Add(time.Hour)
	sock, _ := loginsFakeDaemon(t, api.AccountLogins{Enabled: true, Logins: []api.AccountLogin{
		{Account: "claude default - /homes/c", State: "unreadable", ReadAt: good, FailingSince: &since, Why: "timed out"},
	}})
	code, out, errOut := runFanGet(t, "--socket", sock, "get", "account-logins")
	if code != 0 {
		t.Fatalf("exit %d; stderr %q", code, errOut)
	}
	for _, want := range []string{"2026-11-23T12:00:00Z", "timed out since 2026-11-23T13:00:00Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}
