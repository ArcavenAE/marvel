package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/spf13/cobra"
)

// ctx-forward is the statusline side of the cooperative context feed
// (finding-011). Claude Code invokes the configured statusline command
// with a JSON payload on stdin; the projection layer points that hook at
// this subcommand. It forwards the harness's own context figure to the
// daemon's heartbeat RPC and prints a compact status string for the
// human attached to the pane.
//
// Two payload shapes arrive here, distinguished by their keys:
//   - the main statusLine payload (context_window object, cost object)
//   - the subagentStatusLine payload (tasks array)
//
// The harness's percentage is not taken on faith. The same payload
// carries the token classes and the window the harness derived it from,
// so the forwarder recomputes occupancy and refuses to forward a figure
// its own arithmetic contradicts. That is what keeps this channel, the
// only interactive CTX% source marvel has, from reporting a confidently
// wrong number if the field's meaning ever shifts under it.
//
// Failure posture: this runs on every statusline tick inside an agent's
// pane, so it never exits nonzero and never prints errors — a broken
// feed shows as a silent gap in CTX%, diagnosed via `marvel describe
// session`, not as red text inside the agent's terminal. The one
// deliberate exception is a failed cross-check, which prints both
// figures into the pane; see renderForward.

// statuslinePayload is the subset of Claude Code's statusline JSON that
// the forwarder reads. Fields the forwarder does not use are omitted —
// the harness owns this schema, marvel just picks out context and cost.
//
// Shape verified against Claude Code 2.1.226 on 2026-08-08, three ways:
// the payload builder inside the shipped binary, the statusline schema
// the same binary documents, and one payload captured live from a
// throwaway session (testdata/statusline-2.1.226-empty.json). The token
// classes and the window live INSIDE context_window, not at the top
// level; transcript_path is at the top level and is deliberately not
// parsed here, having no consumer yet.
type statuslinePayload struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Cost struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow *contextWindow `json:"context_window"`
	Tasks         []struct {
		Status            string `json:"status"`
		TokenCount        int    `json:"tokenCount"`
		ContextWindowSize int    `json:"contextWindowSize"`
	} `json:"tasks"`
}

// contextWindow is the harness's own context accounting: the classes it
// measured, the window it measured them against, and the percentage it
// derived from both. Carrying all three is what makes the reading
// checkable rather than merely asserted.
//
// current_usage and used_percentage are null together on a session that
// has not made an API call yet.
type contextWindow struct {
	ContextWindowSize int `json:"context_window_size"`
	CurrentUsage      *struct {
		InputTokens int `json:"input_tokens"`
		// OutputTokens is parsed and then not summed. It is here so the
		// class set is recorded whole and its exclusion from occupancy
		// reads as a decision rather than a missed field.
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"current_usage"`
	UsedPercentage *float64 `json:"used_percentage"`
}

// harnessPercentTolerance is the largest gap, in percentage points,
// between the harness's used_percentage and marvel's recomputation of it
// that still reads as agreement.
//
// It absorbs a rounding-mode difference, not a change of meaning. The
// failure worth catching is used_percentage moving from raw occupancy to
// the percent-until-auto-compaction figure the harness DISPLAYS to its
// own user; those two differ by a reserved buffer of tens of thousands
// of tokens and a normalized threshold, so they diverge by far more than
// a point (internal/usage/doc.go). One point is wide enough that no
// arithmetically honest payload trips it and narrow enough that a
// redefinition cannot hide inside it.
const harnessPercentTolerance = 1.0

// recomputeUsedPercent derives occupancy from the classes the payload
// shipped, mirroring the harness's own arithmetic: input plus
// cache-creation plus cache-read over the window, rounded then clamped,
// with output tokens excluded. Excluding output is not an oversight: it
// is what the harness does, and summing it in would put every reading a
// little over and make the cross-check fire on healthy sessions.
//
// ok is false when the payload carries nothing to compute from, which is
// the normal state of a session that has not called the API yet.
func recomputeUsedPercent(w *contextWindow) (pct float64, ok bool) {
	if w == nil || w.CurrentUsage == nil || w.ContextWindowSize <= 0 {
		return 0, false
	}
	u := w.CurrentUsage
	occupancy := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	raw := math.Round(float64(occupancy) / float64(w.ContextWindowSize) * 100)
	return math.Min(100, math.Max(0, raw)), true
}

// renderForward parses one statusline payload and returns the status text
// to print, the context percentage and model name to forward (send=false
// when the payload carries no forwardable figure). Pure so it is
// table-testable.
func renderForward(raw []byte) (line string, pct float64, model string, send bool) {
	var p statuslinePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "marvel ctx-forward: unreadable payload", 0, "", false
	}

	// Subagent shape: summarize the task rows. No RPC — the daemon has
	// no per-subagent surface yet (tracked in aae-orc-7hzb).
	if len(p.Tasks) > 0 {
		running := 0
		maxPct := 0.0
		for _, t := range p.Tasks {
			if t.Status == "running" {
				running++
			}
			if t.ContextWindowSize > 0 {
				if pc := float64(t.TokenCount) / float64(t.ContextWindowSize) * 100; pc > maxPct {
					maxPct = pc
				}
			}
		}
		return fmt.Sprintf("agents %d/%d running · max CTX %.0f%%", running, len(p.Tasks), maxPct), 0, "", false
	}

	if p.ContextWindow == nil || p.ContextWindow.UsedPercentage == nil {
		// Session too young to have a measurement. Show something
		// stable rather than flickering an error.
		return fmt.Sprintf("%s · CTX –", orUnknown(p.Model.DisplayName)), 0, "", false
	}
	pct = *p.ContextWindow.UsedPercentage

	// The payload carries the numerator and the denominator behind its own
	// percentage, so marvel can check the harness's arithmetic instead of
	// taking it on faith. When the classes contradict the percentage, the
	// field's meaning has moved and marvel can no longer explain the
	// number it would forward: refuse it, exactly as the accountant
	// reports an unresolved window absent rather than guessing one
	// (internal/usage/doc.go).
	//
	// The check lives here rather than in the daemon because this is the
	// only place the classes exist without widening the heartbeat RPC,
	// and refusing at the edge keeps a suspect reading out of the store
	// entirely rather than storing it and grading it later.
	//
	// A mismatch is printed rather than logged: ctx-forward has no event
	// path and its failure posture is otherwise silent, so the pane is
	// the only place a human will see that the cross-check tripped. A
	// silent blank would look like an ordinary gap in the feed.
	if mine, ok := recomputeUsedPercent(p.ContextWindow); ok && math.Abs(mine-pct) > harnessPercentTolerance {
		return fmt.Sprintf("%s · CTX ? harness %.0f%% vs recomputed %.0f%% · $%.2f",
			orUnknown(p.Model.DisplayName), pct, mine, p.Cost.TotalCostUSD), 0, "", false
	}
	// A payload with no classes to check against is unverified, not
	// suspect. Older harnesses, and any other producer pointed at this
	// hook, still get their declared percentage forwarded; refusing an
	// uncorroborated reading would blank the feed for everyone who
	// predates the classes rather than catch anything.

	line = fmt.Sprintf("%s · CTX %.0f%% · $%.2f", orUnknown(p.Model.DisplayName), pct, p.Cost.TotalCostUSD)
	return line, pct, p.Model.DisplayName, true
}

func orUnknown(s string) string {
	if s == "" {
		return "agent"
	}
	return s
}

func newCtxForwardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "ctx-forward",
		Short:  "Forward a statusline payload's context figure to the daemon (internal)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readAllStdin()
			if err != nil {
				fmt.Println("marvel ctx-forward")
				return nil
			}
			line, pct, model, send := renderForward(raw)
			fmt.Println(line)

			socket := os.Getenv("MARVEL_SOCKET")
			workspace := os.Getenv("MARVEL_WORKSPACE")
			session := os.Getenv("MARVEL_SESSION")
			if !send || socket == "" || workspace == "" || session == "" {
				return nil
			}
			params, _ := json.Marshal(map[string]any{
				"session_key":     workspace + "/" + session,
				"context_percent": pct,
				"model":           model,
			})
			// Best-effort by design; see the failure posture above.
			_, _ = daemon.SendRequest(socket, daemon.Request{
				Method: "heartbeat",
				Params: params,
			})
			return nil
		},
	}
	return cmd
}

func readAllStdin() ([]byte, error) {
	return io.ReadAll(os.Stdin)
}
