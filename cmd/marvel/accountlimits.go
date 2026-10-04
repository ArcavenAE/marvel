package main

import (
	"encoding/json"
	"math"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/runtime/codex"
)

// The senders for the account.limits RPC. A statusline or hook tick carries
// the account's rate-limit windows beside the context figure; they go to their
// own RPC because they belong to the account and not to the session whose hook
// ran. Failure posture is the hooks': best effort, never an error in the pane,
// never a nonzero exit.

// validPercent reports whether a harness-declared percentage is in the range
// the contract documents. Out of range is the signature of a scaling trap
// (see resetInstant) and is refused, as formatRateLimits refuses to print it.
func validPercent(p float64) bool {
	return !math.IsNaN(p) && p >= 0 && p <= 100
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
		if w.window == nil || w.window.UsedPercentage == nil || !validPercent(*w.window.UsedPercentage) {
			continue
		}
		pct := *w.window.UsedPercentage
		aw := api.AccountWindow{Name: w.name, UsedPercent: &pct}
		if w.window.ResetsAt.Valid {
			aw.ResetsAt = w.window.ResetsAt.Time
		}
		out = append(out, aw)
	}
	return out
}

// codexAccountWindows lifts the usable windows out of the rollout a codex hook
// payload points at. Any failure to read it means no windows: the daemon holds
// what it had.
func codexAccountWindows(raw []byte) []api.AccountWindow {
	var p codexHookPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.TranscriptPath == nil || *p.TranscriptPath == "" {
		return nil
	}
	rl, err := codex.ReadRateLimits(*p.TranscriptPath)
	if err != nil {
		return nil
	}
	var out []api.AccountWindow
	for _, w := range rl.Windows {
		if !validPercent(w.UsedPercent) {
			continue
		}
		pct := w.UsedPercent
		out = append(out, api.AccountWindow{Name: w.Name, UsedPercent: &pct, ResetsAt: w.ResetsAt})
	}
	return out
}

// accountLimitsRequest builds the account.limits request for a session. It
// reports false when there is nothing to send.
func accountLimitsRequest(workspace, session string, windows []api.AccountWindow) (daemon.Request, bool) {
	if len(windows) == 0 {
		return daemon.Request{}, false
	}
	params, err := json.Marshal(api.AccountLimitsRequest{Session: workspace + "/" + session, Windows: windows})
	if err != nil {
		return daemon.Request{}, false
	}
	return daemon.Request{Method: "account.limits", Params: params}, true
}

// sendAccountLimits posts the windows, best effort.
func sendAccountLimits(socket, workspace, session string, windows []api.AccountWindow) {
	req, ok := accountLimitsRequest(workspace, session, windows)
	if !ok {
		return
	}
	_, _ = daemon.SendRequest(socket, req)
}
