package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// recorder is a daemon that answers every method with an empty object and
// keeps each request it saw, so a test can read the key that crossed the wire.
type recorder struct {
	sock string
	mu   sync.Mutex
	reqs []daemonrpc.Request
}

func (r *recorder) seen() []daemonrpc.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]daemonrpc.Request(nil), r.reqs...)
}

func startRecorder(t *testing.T) *recorder {
	t.Helper()
	dir, err := os.MkdirTemp("", "qk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	r := &recorder{sock: filepath.Join(dir, "d.sock")}
	ln, err := net.Listen("unix", r.sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				var req daemonrpc.Request
				if json.NewDecoder(c).Decode(&req) != nil {
					return
				}
				r.mu.Lock()
				r.reqs = append(r.reqs, req)
				r.mu.Unlock()
				_ = json.NewEncoder(c).Encode(daemonrpc.Response{Result: json.RawMessage(`{}`)})
			}()
		}
	}()
	return r
}

// wireName is the session key or name field of the one request a recorder saw.
func wireName(t *testing.T, r *recorder) string {
	t.Helper()
	got := r.seen()
	if len(got) != 1 {
		t.Fatalf("daemon saw %d requests, want 1", len(got))
	}
	var p map[string]any
	if err := json.Unmarshal(got[0].Params, &p); err != nil {
		t.Fatalf("params: %v", err)
	}
	for _, k := range []string{"name", "session_key", "key"} {
		if s, ok := p[k].(string); ok {
			return s
		}
	}
	t.Fatalf("no key field in params %s", got[0].Params)
	return ""
}

func twoRecorders(t *testing.T) (*recorder, *recorder) {
	t.Helper()
	a, b := startRecorder(t), startRecorder(t)
	clusterConfig(t,
		config.Cluster{Name: "alpha", Socket: a.sock},
		config.Cluster{Name: "beta", Socket: b.sock},
	)
	return a, b
}

// A qualified key splits at its first ':'. The cluster before it picks the
// daemon, and the daemon gets the bare workspace/name, so a read copied out
// of a multi-cluster view needs no --cluster (#829, item 10 of #816).
func TestQualifiedKeySplitsAtFirstColon(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"describe", []string{"describe", "session", "beta:w/s1"}, "w/s1"},
		{"capture", []string{"capture", "beta:w/s1"}, "w/s1"},
		{"second colon stays in the name", []string{"describe", "session", "beta:w/s:1"}, "w/s:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b := twoRecorders(t)
			code, _, errOut := runFanGet(t, tc.args...)
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if got := a.seen(); len(got) != 0 {
				t.Fatalf("alpha saw %d requests; the key named beta", len(got))
			}
			if got := wireName(t, b); got != tc.want {
				t.Errorf("beta received key %q, want %q", got, tc.want)
			}
		})
	}
}

// A qualified key with a --cluster that agrees reads the same cluster.
func TestQualifiedReadWithMatchingCluster(t *testing.T) {
	a, b := twoRecorders(t)
	code, _, errOut := runFanGet(t, "capture", "beta:w/s1", "--cluster", "beta")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if len(a.seen()) != 0 || wireName(t, b) != "w/s1" {
		t.Errorf("alpha saw %d, beta key %q", len(a.seen()), wireName(t, b))
	}
}

// The full-width colon U+FF1A is not ':'. Such a key is bare and goes to the
// selected cluster untouched.
func TestFullwidthColonIsBare(t *testing.T) {
	a, _ := twoRecorders(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.CurrentCluster = "alpha"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	key := "beta：w/s1"
	code, _, errOut := runFanGet(t, "capture", key)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if got := wireName(t, a); got != key {
		t.Errorf("alpha received key %q, want the bare %q", got, key)
	}
}

// A write given a qualified key needs a typed --cluster that matches. A
// missing or different one is refused before any connection, and a matching
// one sends the bare key to that cluster.
func TestQualifiedWriteNeedsMatchingCluster(t *testing.T) {
	refused := [][]string{
		{"inject", "beta:w/s1", "hello"},
		{"inject", "beta:w/s1", "hello", "--cluster", "alpha"},
		// A socket names no cluster and outranks --cluster in the resolver, so
		// a qualified key beside one would write to the socket's daemon.
		{"inject", "beta:w/s1", "hello", "--cluster", "beta", "--socket", "{alpha}"},
	}
	for _, args := range refused {
		a, b := twoRecorders(t)
		args = fillSocket(args, a)
		code, _, errOut := runFanGet(t, args...)
		if code != 1 {
			t.Errorf("%v: exit %d, want 1", args, code)
		}
		if !strings.Contains(errOut, "--cluster") {
			t.Errorf("%v: stderr %q does not point at --cluster", args, errOut)
		}
		if strings.Contains(errOut, "not supported yet") {
			t.Errorf("%v: stderr %q still says qualified keys are unsupported", args, errOut)
		}
		if n := len(a.seen()) + len(b.seen()); n != 0 {
			t.Errorf("%v: %d requests before the refusal, want 0", args, n)
		}
	}

	a, b := twoRecorders(t)
	code, _, errOut := runFanGet(t, "inject", "beta:w/s1", "hello", "--cluster", "beta")
	if code != 0 {
		t.Fatalf("matching cluster: exit %d, stderr %q", code, errOut)
	}
	if len(a.seen()) != 0 {
		t.Errorf("alpha saw %d requests", len(a.seen()))
	}
	if got := wireName(t, b); got != "w/s1" {
		t.Errorf("beta received key %q, want w/s1", got)
	}
}

// A key holding U+FFFD, filtered or sent literally by the daemon, is refused,
// and the refusal points at -o json where the raw bytes are.
func TestKeyHoldingPlaceholderRefused(t *testing.T) {
	for _, args := range [][]string{
		{"capture", "beta:w/s�1"},
		{"describe", "session", "beta:w/s�1"},
	} {
		a, b := twoRecorders(t)
		code, _, errOut := runFanGet(t, args...)
		if code != 1 {
			t.Errorf("%v: exit %d, want 1", args, code)
		}
		if !strings.Contains(errOut, "-o json") {
			t.Errorf("%v: stderr %q does not point at -o json", args, errOut)
		}
		if n := len(a.seen()) + len(b.seen()); n != 0 {
			t.Errorf("%v: %d requests before the refusal, want 0", args, n)
		}
	}
}

// fillSocket puts a recorder's socket where an argument reads "{alpha}".
func fillSocket(args []string, r *recorder) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, "{alpha}", r.sock)
	}
	return out
}

// A qualified read given --socket is refused too: the socket names no
// cluster, so the cluster in the key and the address would disagree, and the
// resolver would dial the socket without a word.
func TestQualifiedKeyWithSocketRefused(t *testing.T) {
	for _, args := range [][]string{
		{"capture", "beta:w/s1", "--socket", "{alpha}"},
		{"describe", "session", "beta:w/s1", "--socket", "{alpha}"},
	} {
		a, b := twoRecorders(t)
		code, _, errOut := runFanGet(t, fillSocket(args, a)...)
		if code != 1 {
			t.Errorf("%v: exit %d, want 1", args, code)
		}
		if !strings.Contains(errOut, "socket") || !strings.Contains(errOut, "no cluster") {
			t.Errorf("%v: stderr %q does not say the socket names no cluster", args, errOut)
		}
		if n := len(a.seen()) + len(b.seen()); n != 0 {
			t.Errorf("%v: %d requests before the refusal, want 0", args, n)
		}
	}
}
