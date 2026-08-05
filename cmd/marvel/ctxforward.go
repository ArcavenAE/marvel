package main

import (
	"encoding/json"
	"fmt"
	"io"
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
// Failure posture: this runs on every statusline tick inside an agent's
// pane, so it never exits nonzero and never prints errors — a broken
// feed shows as a silent gap in CTX%, diagnosed via `marvel describe
// session`, not as red text inside the agent's terminal.

// statuslinePayload is the subset of Claude Code's statusline JSON that
// the forwarder reads. Fields the forwarder does not use are omitted —
// the harness owns this schema, marvel just picks out context and cost.
type statuslinePayload struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Cost struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow *struct {
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
	Tasks []struct {
		Status            string `json:"status"`
		TokenCount        int    `json:"tokenCount"`
		ContextWindowSize int    `json:"contextWindowSize"`
	} `json:"tasks"`
}

// renderForward parses one statusline payload and returns the status text
// to print plus the context percentage to forward (send=false when the
// payload carries no forwardable figure). Pure so it is table-testable.
func renderForward(raw []byte) (line string, pct float64, send bool) {
	var p statuslinePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "marvel ctx-forward: unreadable payload", 0, false
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
		return fmt.Sprintf("agents %d/%d running · max CTX %.0f%%", running, len(p.Tasks), maxPct), 0, false
	}

	if p.ContextWindow == nil || p.ContextWindow.UsedPercentage == nil {
		// Session too young to have a measurement. Show something
		// stable rather than flickering an error.
		return fmt.Sprintf("%s · CTX –", orUnknown(p.Model.DisplayName)), 0, false
	}
	pct = *p.ContextWindow.UsedPercentage
	line = fmt.Sprintf("%s · CTX %.0f%% · $%.2f", orUnknown(p.Model.DisplayName), pct, p.Cost.TotalCostUSD)
	return line, pct, true
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
			line, pct, send := renderForward(raw)
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
