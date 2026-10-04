package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

// The synthetic payload (not a live capture; see the rateLimits type) carries
// both windows as integer epochs.
func TestClaudeAccountWindowsFromThePayload(t *testing.T) {
	t.Parallel()
	got := claudeAccountWindows(readTestdata(t, "statusline-2.1.226-rate-limits-synthetic.json"))
	if len(got) != 2 {
		t.Fatalf("windows = %+v, want two", got)
	}
	five, seven := got[0], got[1]
	if five.Name != "five_hour" || *five.UsedPercent != 41 || !five.ResetsAt.Equal(time.Unix(1786000000, 0)) {
		t.Errorf("five_hour = %+v", five)
	}
	if seven.Name != "seven_day" || *seven.UsedPercent != 63 || !seven.ResetsAt.Equal(time.Unix(1786400000, 0)) {
		t.Errorf("seven_day = %+v", seven)
	}
}

// Test 20's sender half: a payload with no rate_limits block (a session that
// has made no API call) sends nothing, so nothing arrives as 0%.
func TestClaudeAccountWindowsAbsentBlockSendsNothing(t *testing.T) {
	t.Parallel()
	if got := claudeAccountWindows(readTestdata(t, "statusline-2.1.226-empty.json")); got != nil {
		t.Fatalf("windows = %+v from a payload with no rate_limits", got)
	}
	if got := claudeAccountWindows([]byte("not json")); got != nil {
		t.Fatalf("windows = %+v from garbage", got)
	}
}

func TestClaudeAccountWindowsDropsWhatIsNotAReading(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body string
		want       []string
	}{
		{"no percentage", `{"rate_limits":{"five_hour":{"resets_at":1786000000}}}`, nil},
		{"null percentage", `{"rate_limits":{"seven_day":{"used_percentage":null,"resets_at":1786000000}}}`, nil},
		{"null window", `{"rate_limits":{"five_hour":null,"seven_day":null}}`, nil},
		{"over 100 is the scaling trap", `{"rate_limits":{"seven_day":{"used_percentage":4100}}}`, nil},
		{"negative", `{"rate_limits":{"seven_day":{"used_percentage":-1}}}`, nil},
		{"one good one bad", `{"rate_limits":{"five_hour":{"used_percentage":4100},"seven_day":{"used_percentage":50}}}`, []string{"seven_day"}},
		{"exactly 100 is a reading", `{"rate_limits":{"seven_day":{"used_percentage":100,"resets_at":1786400000}}}`, []string{"seven_day"}},
		{"no reset is carried as zero", `{"rate_limits":{"seven_day":{"used_percentage":5}}}`, []string{"seven_day"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var names []string
			for _, w := range claudeAccountWindows([]byte(tc.body)) {
				names = append(names, w.Name)
			}
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("windows = %v, want %v", names, tc.want)
			}
		})
	}
	// An unreadable reset instant is recorded as absent, not as a failure.
	w := claudeAccountWindows([]byte(`{"rate_limits":{"seven_day":{"used_percentage":5,"resets_at":"someday"}}}`))
	if len(w) != 1 || !w[0].ResetsAt.IsZero() {
		t.Fatalf("windows = %+v, want one with no reset", w)
	}
}

// The codex hook points at a rollout; the real one in the codex testdata
// carries a seven-day window.
func TestCodexAccountWindowsFromARealRollout(t *testing.T) {
	t.Parallel()
	path, err := filepath.Abs("../../internal/runtime/codex/testdata/rollout-compaction.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"transcript_path": path})
	got := codexAccountWindows(raw)
	if len(got) != 1 || got[0].Name != "seven_day" || *got[0].UsedPercent != 22 {
		t.Fatalf("windows = %+v, want seven_day at 22", got)
	}
}

func TestCodexAccountWindowsHoldsOnAnyFailure(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"no transcript_path": `{}`,
		"empty path":         `{"transcript_path":""}`,
		"missing file":       `{"transcript_path":"/nonexistent/rollout.jsonl"}`,
		"garbage":            `not json`,
	} {
		if got := codexAccountWindows([]byte(raw)); got != nil {
			t.Errorf("%s: windows = %+v, want none", name, got)
		}
	}
}

// The request names the reporting session as workspace/session, the key the
// daemon resolves to an account, and carries the windows.
func TestAccountLimitsRequest(t *testing.T) {
	t.Parallel()
	pct := 87.0
	req, ok := accountLimitsRequest("ws", "seat-0", []api.AccountWindow{{Name: "seven_day", UsedPercent: &pct}})
	if !ok || req.Method != "account.limits" {
		t.Fatalf("req = %+v ok=%v", req, ok)
	}
	var body api.AccountLimitsRequest
	if err := json.Unmarshal(req.Params, &body); err != nil {
		t.Fatal(err)
	}
	if body.Session != "ws/seat-0" || len(body.Windows) != 1 || *body.Windows[0].UsedPercent != 87 {
		t.Fatalf("body = %+v", body)
	}
	if _, ok := accountLimitsRequest("ws", "seat-0", nil); ok {
		t.Fatal("a request was built with nothing to send")
	}
}
