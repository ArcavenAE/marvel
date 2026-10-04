package main

import (
	"encoding/json"
	"math"
	"os"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/runtime/codex"
)

// The senders for the account.limits RPC. A statusline or hook tick carries
// the account's rate-limit windows beside the context figure; they go to their
// own RPC because they belong to the account and not to the session whose hook
// ran. Failure posture is the hooks': best effort, never an error in the pane,
// never a nonzero exit.

// accountPercent returns the percentage to send and whether it is a reading.
// NaN, infinity and negatives are not. A figure above 100 is a limit reached
// and not a scaling trap, and the daemon binds at 100 or more, so it is sent
// as 100 and not lost (marvel#551 review).
func accountPercent(p float64) (float64, bool) {
	if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 {
		return 0, false
	}
	return math.Min(p, 100), true
}

// claudeAccountWindows lifts the usable windows out of a Claude Code statusline
// payload. A window with no percentage, or one out of range, is left out.
func claudeAccountWindows(raw []byte) []api.AccountWindow {
	var p statuslinePayload
	if err := json.Unmarshal(raw, &p); err != nil || p.RateLimits == nil {
		return nil
	}
	var out []api.AccountWindow
	for _, w := range []struct {
		name   string
		window *rateLimitWindow
	}{
		{"five_hour", p.RateLimits.FiveHour},
		{"seven_day", p.RateLimits.SevenDay},
	} {
		if w.window == nil || w.window.UsedPercentage == nil {
			continue
		}
		pct, ok := accountPercent(*w.window.UsedPercentage)
		if !ok {
			continue
		}
		aw := api.AccountWindow{Name: w.name, UsedPercent: &pct}
		if w.window.ResetsAt.Valid {
			aw.ResetsAt = w.window.ResetsAt.Time
		}
		out = append(out, aw)
	}
	return out
}

// codexAccountWindows lifts the usable windows out of the rollout a codex hook
// payload points at, with the time codex wrote them. Any failure to read it
// means no windows: the daemon holds what it had. The newest block in a rollout
// can be old (the newest record may carry none), so a record with no parseable
// time sends nothing, and a window already past its reset at now is left out:
// either would be posted as current by every tick that follows.
func codexAccountWindows(raw []byte, now time.Time) ([]api.AccountWindow, time.Time) {
	var p codexHookPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.TranscriptPath == nil || *p.TranscriptPath == "" {
		return nil, time.Time{}
	}
	rl, err := codex.ReadRateLimits(*p.TranscriptPath)
	if err != nil || rl.TS.IsZero() {
		return nil, time.Time{}
	}
	var out []api.AccountWindow
	for _, w := range rl.Windows {
		pct, ok := accountPercent(w.UsedPercent)
		if !ok || (!w.ResetsAt.IsZero() && !w.ResetsAt.After(now)) {
			continue
		}
		out = append(out, api.AccountWindow{Name: w.Name, UsedPercent: &pct, ResetsAt: w.ResetsAt})
	}
	if len(out) == 0 {
		return nil, time.Time{}
	}
	return out, rl.TS
}

// accountLimitsRequest builds the account.limits request for a session. The
// token is the one marvel minted for the session at spawn, which the daemon
// requires: a reading can mark every session of an account limited. It reports
// false when there is nothing to send.
func accountLimitsRequest(workspace, session, token string, windows []api.AccountWindow, observedAt time.Time) (daemon.Request, bool) {
	if len(windows) == 0 {
		return daemon.Request{}, false
	}
	params, err := json.Marshal(api.AccountLimitsRequest{Session: workspace + "/" + session, SessionToken: token, Windows: windows, ObservedAt: observedAt})
	if err != nil {
		return daemon.Request{}, false
	}
	return daemon.Request{Method: "account.limits", Params: params}, true
}

// accountLimitsTimeout bounds the whole exchange: a statusline or hook must
// not hang on a daemon that accepted the connection and never answered.
const accountLimitsTimeout = 3 * time.Second

// sendAccountLimits posts the windows, best effort. observedAt is when the
// harness made the observation. Both senders stamp it (the statusline with its
// own tick time); a zero one is read by the daemon as unknown and never
// displaces a stamped reading.
func sendAccountLimits(socket, workspace, session string, windows []api.AccountWindow, observedAt time.Time) {
	req, ok := accountLimitsRequest(workspace, session, os.Getenv(api.HeartbeatTokenEnv), windows, observedAt)
	if !ok {
		return
	}
	// The deadline is set after the dial, so it bounds the exchange and not the
	// connect; connecting to the local socket does not block.
	_, _ = daemon.SendRequestWith(socket, req, daemon.DialOptions{Timeout: accountLimitsTimeout})
}
