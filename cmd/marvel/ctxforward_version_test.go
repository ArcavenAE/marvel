package main

import (
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The 2.1.226 live capture carries a top-level "version", which the
// forwarder used to discard along with every other key it did not declare.
func TestStatuslineVersionFromLiveCapture(t *testing.T) {
	raw, err := os.ReadFile("testdata/statusline-2.1.226-empty.json")
	if err != nil {
		t.Fatal(err)
	}
	got := statuslineVersion(raw)
	if got == nil || *got != "2.1.226" {
		t.Fatalf("statuslineVersion = %v, want 2.1.226", got)
	}
}

// PROVENANCE, stated because it is weaker than the live capture above. For
// Claude Code 2.1.319 (build 2026-11-05) the key is READ FROM THE BINARY, NOT
// CAPTURED: the statusline payload builder in the shipped binary returns an
// object whose siblings are model, workspace, version, output_style, cost and
// context_window, and its version member is the build's VERSION string
// "2.1.319". So the key is top level in that build by construction. No 2.1.319
// payload was observed in traffic.
//
// Only a bare x.y.z is forwarded. A missing key, a non-string and anything
// else is nil, which the daemon reads as "leave the stored value".
func TestStatuslineVersion(t *testing.T) {
	for _, tc := range []struct {
		name, payload, want string
	}{
		// 2.1.319 is the leading token of what `claude --version` printed on the
		// day this was written ("2.1.319 (Claude Code)"). It is the binary's own
		// version flag, not a statusline value; the full line is not a version.
		{"claude --version token", `{"version":"2.1.319"}`, "2.1.319"},
		{"claude --version full line", `{"version":"2.1.319 (Claude Code)"}`, ""},
		{"missing key", `{"model":{"display_name":"m"}}`, ""},
		{"empty", `{"version":""}`, ""},
		{"null", `{"version":null}`, ""},
		{"number", `{"version":2.1}`, ""},
		{"two parts", `{"version":"2.1"}`, ""},
		{"prerelease", `{"version":"2.1.319-beta.1"}`, ""},
		{"v prefix", `{"version":"v2.1.319"}`, ""},
		{"four parts", `{"version":"1.2.3.4"}`, ""},
		{"trailing newline", "{\"version\":\"2.1.319\\n\"}", ""},
		{"padded", `{"version":" 2.1.319"}`, ""},
		{"non-ascii digits", `{"version":"２.１.３"}`, ""},
		{"nested only", `{"x":{"version":"2.1.319"}}`, ""},
		{"unreadable", `not json`, ""},
	} {
		got := statuslineVersion([]byte(tc.payload))
		switch {
		case tc.want == "" && got != nil:
			t.Errorf("%s: got %q, want nil", tc.name, *got)
		case tc.want != "" && (got == nil || *got != tc.want):
			t.Errorf("%s: got %v, want %q", tc.name, got, tc.want)
		}
	}
}

// withVersion returns the 2.1.226 rate-limits fixture with its top-level
// "version" set to v, or removed when v is nil. The fixture is the one payload
// here that carries a context figure, so it is the one that sends a beat.
func withVersion(t *testing.T, v any, present bool) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(readTestdata(t, "statusline-2.1.226-rate-limits-synthetic.json"), &m); err != nil {
		t.Fatal(err)
	}
	if present {
		m["version"] = v
	} else {
		delete(m, "version")
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The command puts the version on the heartbeat it sends the daemon. This is
// the wiring statuslineVersion's own tests cannot see: without the forward the
// parsing is correct and nothing arrives.
func TestCtxForwardSendsTheHarnessVersionOnTheHeartbeat(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload func(*testing.T) []byte
		want    string
	}{
		{"valid", func(t *testing.T) []byte { return withVersion(t, "2.1.319", true) }, "2.1.319"},
		{"outside the pattern", func(t *testing.T) []byte { return withVersion(t, "2.1.319-beta", true) }, ""},
		{"key absent", func(t *testing.T) []byte { return withVersion(t, nil, false) }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lastHeartbeat = atomic.Value{}
			socket, methods, _ := fakeDaemon(t)
			seatEnv(t, socket)
			withStdin(t, tc.payload(t), func() {
				if err := newCtxForwardCmd().RunE(newCtxForwardCmd(), nil); err != nil {
					t.Fatalf("hook: %v", err)
				}
			})
			waitForMethods(t, methods, 2)
			hb, ok := lastHeartbeat.Load().(api.HeartbeatRequest)
			if !ok {
				t.Fatalf("no heartbeat reached the daemon; methods = %v", methods())
			}
			switch {
			case tc.want == "" && hb.HarnessVersion != nil:
				t.Errorf("heartbeat carried version %q, want none", *hb.HarnessVersion)
			case tc.want != "" && (hb.HarnessVersion == nil || *hb.HarnessVersion != tc.want):
				t.Errorf("heartbeat version = %v, want %q", hb.HarnessVersion, tc.want)
			}
		})
	}
}
