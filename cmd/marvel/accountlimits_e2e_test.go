package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemon"
)

// fakeDaemon answers every request with an empty response and records the
// methods it was sent, in order. The socket lives in its own short directory:
// a unix socket path is capped near 104 bytes and t.TempDir can exceed it.
func fakeDaemon(t *testing.T) (socket string, methods func() []string, tokens func() []string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "mx")
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
		mu   sync.Mutex
		got  []string
		toks []string
	)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemon.Request
			if err := json.NewDecoder(c).Decode(&req); err == nil {
				mu.Lock()
				got = append(got, req.Method)
				if req.Method == "account.limits" {
					var body api.AccountLimitsRequest
					if json.Unmarshal(req.Params, &body) == nil {
						toks = append(toks, body.SessionToken)
					}
				}
				mu.Unlock()
			}
			_ = json.NewEncoder(c).Encode(daemon.Response{})
			_ = c.Close()
		}
	}()
	return socket, func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), got...)
		}, func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), toks...)
		}
}

// withStdin runs fn with os.Stdin reading payload and the seat environment
// set by the caller. Not parallel: it swaps os.Stdin.
func withStdin(t *testing.T, payload []byte, fn func()) {
	t.Helper()
	f, err := os.CreateTemp("", "stdin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	if _, err := f.Write(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = old; _ = f.Close() }()
	fn()
}

func waitForMethods(t *testing.T, methods func() []string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := methods(); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return methods()
}

func seatEnv(t *testing.T, socket string) {
	t.Helper()
	t.Setenv("MARVEL_SOCKET", socket)
	t.Setenv("MARVEL_WORKSPACE", "ws")
	t.Setenv("MARVEL_SESSION", "seat-0")
}

func TestCtxForwardPostsTheAccountReadingBeforeTheHeartbeat(t *testing.T) {
	socket, methods, _ := fakeDaemon(t)
	seatEnv(t, socket)
	withStdin(t, readTestdata(t, "statusline-2.1.226-rate-limits-synthetic.json"), func() {
		if err := newCtxForwardCmd().RunE(newCtxForwardCmd(), nil); err != nil {
			t.Fatalf("hook: %v", err)
		}
	})
	got := waitForMethods(t, methods, 2)
	if len(got) != 2 || got[0] != "account.limits" || got[1] != "heartbeat" {
		t.Fatalf("methods = %v, want [account.limits heartbeat]", got)
	}
}

// A payload with a rate_limits block but no context figure yet still reports
// the account; before this the whole hook returned early.
func TestCtxForwardPostsTheAccountReadingWithNoContextFigure(t *testing.T) {
	socket, methods, _ := fakeDaemon(t)
	seatEnv(t, socket)
	payload := []byte(`{"model":{"display_name":"x"},"rate_limits":{"seven_day":{"used_percentage":50,"resets_at":1786400000}}}`)
	withStdin(t, payload, func() {
		if err := newCtxForwardCmd().RunE(newCtxForwardCmd(), nil); err != nil {
			t.Fatalf("hook: %v", err)
		}
	})
	waitForMethods(t, methods, 1)
	time.Sleep(50 * time.Millisecond) // room for an unwanted heartbeat to arrive
	if got := methods(); len(got) != 1 || got[0] != "account.limits" {
		t.Fatalf("methods = %v, want [account.limits] only", got)
	}
}

// A payload from a session that has made no API call sends nothing at all.
func TestCtxForwardSendsNothingForAnEmptyPayload(t *testing.T) {
	socket, methods, _ := fakeDaemon(t)
	seatEnv(t, socket)
	withStdin(t, readTestdata(t, "statusline-2.1.226-empty.json"), func() {
		if err := newCtxForwardCmd().RunE(newCtxForwardCmd(), nil); err != nil {
			t.Fatalf("hook: %v", err)
		}
	})
	time.Sleep(100 * time.Millisecond)
	if got := methods(); len(got) != 0 {
		t.Fatalf("methods = %v, want none", got)
	}
}

// With no socket in the environment the hook is silent and exits clean.
func TestCtxForwardWithoutASeatEnvironmentIsSilent(t *testing.T) {
	t.Setenv("MARVEL_SOCKET", "")
	withStdin(t, readTestdata(t, "statusline-2.1.226-rate-limits-synthetic.json"), func() {
		if err := newCtxForwardCmd().RunE(newCtxForwardCmd(), nil); err != nil {
			t.Fatalf("hook: %v", err)
		}
	})
}

func TestCodexCtxPostsTheAccountReading(t *testing.T) {
	socket, methods, _ := fakeDaemon(t)
	seatEnv(t, socket)
	payload, _ := json.Marshal(map[string]string{"transcript_path": liveRollout(t)})
	withStdin(t, payload, func() {
		if err := newCodexCtxCmd().RunE(newCodexCtxCmd(), nil); err != nil {
			t.Fatalf("hook: %v", err)
		}
	})
	got := waitForMethods(t, methods, 1)
	if len(got) == 0 || got[0] != "account.limits" {
		t.Fatalf("methods = %v, want account.limits first", got)
	}
}

// The hook presents the token marvel minted for the session, read from the
// environment, so the daemon can bind the reading to it. Without one it sends an
// empty token and the daemon refuses.
func TestCtxForwardPresentsTheSessionToken(t *testing.T) {
	socket, methods, tokens := fakeDaemon(t)
	seatEnv(t, socket)
	t.Setenv(api.HeartbeatTokenEnv, "minted-at-spawn")
	withStdin(t, readTestdata(t, "statusline-2.1.226-rate-limits-synthetic.json"), func() {
		if err := newCtxForwardCmd().RunE(newCtxForwardCmd(), nil); err != nil {
			t.Fatalf("hook: %v", err)
		}
	})
	waitForMethods(t, methods, 2)
	if got := tokens(); len(got) != 1 || got[0] != "minted-at-spawn" {
		t.Fatalf("tokens presented = %v, want [minted-at-spawn]", got)
	}
}

func TestCodexCtxPresentsTheSessionToken(t *testing.T) {
	socket, methods, tokens := fakeDaemon(t)
	seatEnv(t, socket)
	t.Setenv(api.HeartbeatTokenEnv, "minted-at-spawn")
	payload, _ := json.Marshal(map[string]string{"transcript_path": liveRollout(t)})
	withStdin(t, payload, func() {
		if err := newCodexCtxCmd().RunE(newCodexCtxCmd(), nil); err != nil {
			t.Fatalf("hook: %v", err)
		}
	})
	waitForMethods(t, methods, 1)
	if got := tokens(); len(got) != 1 || got[0] != "minted-at-spawn" {
		t.Fatalf("tokens presented = %v, want [minted-at-spawn]", got)
	}
}

// liveRollout writes a synthetic codex rollout whose rate_limits block was
// written a minute ago and resets in the future, so the sender treats it as a
// current observation. The checked-in rollout is real but its reset has passed.
func liveRollout(t *testing.T) string {
	t.Helper()
	now := time.Now().UTC()
	line := fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex","primary":{"used_percent":22.0,"window_minutes":10080,"resets_at":%d}}}}`+"\n",
		now.Add(-time.Minute).Format("2006-01-02T15:04:05.000Z"), now.Add(48*time.Hour).Unix())
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
