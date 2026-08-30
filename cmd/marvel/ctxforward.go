package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/arcavenae/marvel/internal/api"
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
// The same payload also carries the ACCOUNT's rate-limit headroom, which
// the harness relays from response headers it already received. That
// figure is captured and shown in the pane but never forwarded, because
// it belongs to the account rather than to the session whose heartbeat
// this is; see the rateLimits type for why a per-session column of it
// would be a category error.
//
// Failure posture: this runs on every statusline tick inside an agent's
// pane, so it never exits nonzero and never prints errors — a broken
// feed shows as a silent gap in CTX%, diagnosed via `marvel describe
// session`, not as red text inside the agent's terminal. The one
// deliberate exception is a failed cross-check, which prints both
// figures into the pane; see renderForward.

// statuslinePayload is the subset of Claude Code's statusline JSON that
// the forwarder reads. Fields the forwarder does not use are omitted; the
// harness owns this schema, marvel picks out context, cost, and the
// account's rate-limit headroom.
//
// Omitting a field here is not free, and rate_limits is the demonstration:
// encoding/json drops unknown keys without complaint, so a field the
// harness sends and this struct does not declare is discarded in flight
// with no error, no log line, and nothing to notice. rate_limits arrived
// on every tick of every statusline-fed session and was thrown away that
// way until it was declared. Treat any addition to the payload as data
// this file is silently refusing until proven otherwise.
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
	RateLimits    *rateLimits    `json:"rate_limits"`
	Tasks         []struct {
		Status            string `json:"status"`
		TokenCount        int    `json:"tokenCount"`
		ContextWindowSize int    `json:"contextWindowSize"`
	} `json:"tasks"`
}

// rateLimits is the account's subscription headroom, which the harness
// relays from the response headers it already received. Reading it here
// costs nothing: the number arrived on a request the fleet was already
// making, on a hook marvel already installs.
//
// Scope is the point. Occupancy is per-session; headroom is per-ACCOUNT.
// Every session on one account reports the SAME two windows, so N
// sessions are N reporters of one quantity rather than N quantities.
// Summing them, averaging them, or rendering a per-session column of
// them are all category errors, and the last is the one that looks
// natural next to the existing CTX% column. That is why this block is
// captured and displayed but deliberately NOT sent over the heartbeat
// RPC, whose every field is keyed to a session.
//
// Shape verified against Claude Code 2.1.226 (build 2026-08-08, git sha
// e140b32) three ways: the payload constructor, the statusline schema
// the same binary documents for statusline authors, and the header
// parser feeding both. The documented contract is
// `used_percentage` as a number in 0-100 and `resets_at` as unix epoch
// seconds, both optional, with the whole block absent until the session
// has parsed at least one API response.
//
// NOT VERIFIED: a populated payload observed in the wild. The captured
// fixture beside this file is from a session that made no API call and
// correctly lacks the key, which corroborates the absent case and
// observes nothing about the present one. Everything here about the
// populated shape is read out of the binary, not measured from traffic.
type rateLimits struct {
	FiveHour *rateLimitWindow `json:"five_hour"`
	SevenDay *rateLimitWindow `json:"seven_day"`
}

// rateLimitWindow is one recovering level: it rises with use and falls
// with time. That makes it a third thing beside the level and the
// counter internal/usage/doc.go distinguishes, and it is why the reset
// instant is carried rather than dropped. 94% with four hours to run and
// 94% with four minutes to run are opposite situations, so a consumer
// holding only the percentage has a number it cannot act on.
type rateLimitWindow struct {
	UsedPercentage *float64     `json:"used_percentage"`
	ResetsAt       resetInstant `json:"resets_at"`
}

// resetInstant parses a reset instant from either shape the SAME claude
// binary emits, because 2.1.226 uses two and they disagree:
//
//	statusline payload   resets_at is an INTEGER unix epoch
//	                     (Math.round(Number(header)))
//	get_usage response   resets_at is an ISO 8601 STRING
//	                     (new Date(x*1000).toISOString())
//
// The level field diverges the same way and worse: the statusline calls
// it used_percentage and pre-scales it (utilization*100, from a header
// carrying a 0-to-1 fraction), while get_usage calls it utilization and
// documents it as already 0-100. A consumer that reads one channel's
// documentation and parses the other gets a type error on the instant
// and a 100x error on the level. Hence both shapes here, and the range
// check in formatRateLimits for the other half of the trap.
//
// UnmarshalJSON never returns an error, which is the load-bearing part.
// This type sits inside the same struct as the context figure, so a
// strict parse would let an unfamiliar reset-instant shape fail the
// WHOLE payload and take the CTX% feed down with it. An unreadable
// instant is recorded as absent instead, exactly as an unresolved window
// reports absence rather than a guess.
type resetInstant struct {
	Time  time.Time
	Valid bool
}

// The always-nil error is the point, not an oversight: json.Unmarshaler
// fixes this signature, and returning a real error here would reintroduce
// the whole-payload failure the type exists to prevent.
//
//nolint:unparam // json.Unmarshaler mandates the error return; never non-nil by design
func (r *resetInstant) UnmarshalJSON(b []byte) error {
	// null must be checked before the number, not after: unmarshalling
	// null into a float64 succeeds and leaves it zero, so the fallthrough
	// would silently report 1970 as a valid reset instant. The get_usage
	// schema declares every window nullable, so this is a shape the
	// harness family really emits rather than a hypothetical.
	if string(b) == "null" {
		return nil
	}
	var epoch float64
	if err := json.Unmarshal(b, &epoch); err == nil {
		r.Time = time.Unix(int64(epoch), 0).UTC()
		r.Valid = true
		return nil
	}
	var iso string
	if err := json.Unmarshal(b, &iso); err == nil {
		if t, err := time.Parse(time.RFC3339, iso); err == nil {
			r.Time = t.UTC()
			r.Valid = true
		}
	}
	return nil
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

// occupancyTokens is the NUMERATOR: input plus cache-creation plus
// cache-read, with output tokens excluded. Excluding output is not an
// oversight — it is what the harness does, and it is the same sum
// recomputeUsedPercent divides, so the token count marvel forwards on the
// heartbeat is exactly the one it checked the harness's percentage
// against. ok is false when the payload carries no classes to sum, the
// normal state of a session that has not called the API yet.
func occupancyTokens(w *contextWindow) (tokens int, ok bool) {
	if w == nil || w.CurrentUsage == nil {
		return 0, false
	}
	u := w.CurrentUsage
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens, true
}

// recomputeUsedPercent derives occupancy from the classes the payload
// shipped, mirroring the harness's own arithmetic: input plus
// cache-creation plus cache-read over the window, rounded then clamped,
// with output tokens excluded. Summing output in would put every reading a
// little over and make the cross-check fire on healthy sessions.
//
// ok is false when the payload carries nothing to compute from, which is
// the normal state of a session that has not called the API yet.
func recomputeUsedPercent(w *contextWindow) (pct float64, ok bool) {
	occupancy, ok := occupancyTokens(w)
	if !ok || w.ContextWindowSize <= 0 {
		return 0, false
	}
	raw := math.Round(float64(occupancy) / float64(w.ContextWindowSize) * 100)
	return math.Min(100, math.Max(0, raw)), true
}

// formatRateLimits renders the account's headroom for the pane, or "" when
// the payload carries none. Both windows are labelled `acct` because the
// figure describes the account rather than the session whose pane it is
// printed in; every pane on the account shows the same two numbers, and
// the label is what keeps a reader from taking it for a per-session
// measurement.
//
// The pane is the one destination that cannot commit the attribution
// error. It is a string a human reads, not a record keyed to a session,
// so displaying an account-scoped figure there is honest in a way that
// storing it against a session key would not be.
//
// A percentage outside 0-100 is refused rather than printed, in the same
// posture as the context cross-check above: out of range is the signature
// of the scaling trap in resetInstant's comment having actually
// materialized, and a confidently wrong 4100% is worse than a question
// mark with the raw value beside it.
func formatRateLimits(rl *rateLimits, now time.Time) string {
	if rl == nil {
		return ""
	}
	out := ""
	for _, w := range []struct {
		label  string
		window *rateLimitWindow
	}{
		{"5h", rl.FiveHour},
		{"7d", rl.SevenDay},
	} {
		if w.window == nil || w.window.UsedPercentage == nil {
			continue
		}
		pct := *w.window.UsedPercentage
		if out != "" {
			out += " "
		}
		if pct < 0 || pct > 100 {
			out += fmt.Sprintf("%s ? (raw %.0f)", w.label, pct)
			continue
		}
		out += fmt.Sprintf("%s %.0f%% (%s)", w.label, pct, untilReset(w.window.ResetsAt, now))
	}
	if out == "" {
		return ""
	}
	return "acct " + out
}

// untilReset renders how long the window has left to run, which is the
// term that makes the level actionable. An instant already past means the
// window rolled over and no payload has been seen since, so the reading
// is stale rather than merely old; saying so beats printing a negative
// duration or silently showing a number that no longer applies.
func untilReset(r resetInstant, now time.Time) string {
	if !r.Valid {
		return "resets ?"
	}
	d := r.Time.Sub(now)
	switch {
	case d <= 0:
		return "stale"
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

// renderForward parses one statusline payload and returns the status text
// to print, plus the context percentage, model name, the harness-measured
// occupancy (the NUMERATOR) and the harness-declared window (the
// DENOMINATOR) to forward (send=false when the payload carries no
// forwardable figure). tokens and window are 0 when the payload declares
// none — the daemon then keeps the percentage-only reading.
//
// Pure apart from the clock formatRateLimits needs, so it stays
// table-testable.
func renderForward(raw []byte) (line string, pct float64, model string, tokens int, window int, send bool) {
	var p statuslinePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "marvel ctx-forward: unreadable payload", 0, "", 0, 0, false
	}

	// Subagent shape: summarize the task rows. No RPC — the daemon has
	// no per-subagent surface yet (tracked in aae-orc-7hzb).
	// The account's headroom rides every main-shape line. It is appended
	// rather than woven in so the context half of the line keeps the exact
	// format it had, including on the cross-check refusal path.
	withAcct := func(line string) string {
		if acct := formatRateLimits(p.RateLimits, time.Now().UTC()); acct != "" {
			return line + " · " + acct
		}
		return line
	}

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
		return fmt.Sprintf("agents %d/%d running · max CTX %.0f%%", running, len(p.Tasks), maxPct), 0, "", 0, 0, false
	}

	if p.ContextWindow == nil || p.ContextWindow.UsedPercentage == nil {
		// Session too young to have a measurement. Show something
		// stable rather than flickering an error.
		return withAcct(fmt.Sprintf("%s · CTX –", orUnknown(p.Model.DisplayName))), 0, "", 0, 0, false
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
		return withAcct(fmt.Sprintf("%s · CTX ? harness %.0f%% vs recomputed %.0f%% · $%.2f",
			orUnknown(p.Model.DisplayName), pct, mine, p.Cost.TotalCostUSD)), 0, "", 0, 0, false
	}
	// A payload with no classes to check against is unverified, not
	// suspect. Older harnesses, and any other producer pointed at this
	// hook, still get their declared percentage forwarded; refusing an
	// uncorroborated reading would blank the feed for everyone who
	// predates the classes rather than catch anything.

	line = withAcct(fmt.Sprintf("%s · CTX %.0f%% · $%.2f", orUnknown(p.Model.DisplayName), pct, p.Cost.TotalCostUSD))
	// The numerator rides out only when the payload carried the classes to
	// sum; a payload with a percentage but no current_usage forwards a bare
	// percentage still (occupancyTokens ok=false), which the daemon keeps
	// as a percentage-only reading. tokens is 0 in that case, never a
	// declared-empty-context zero.
	tokens, _ = occupancyTokens(p.ContextWindow)
	return line, pct, p.Model.DisplayName, tokens, p.ContextWindow.ContextWindowSize, true
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
			line, pct, model, tokens, window, send := renderForward(raw)
			fmt.Println(line)

			socket := os.Getenv("MARVEL_SOCKET")
			workspace := os.Getenv("MARVEL_WORKSPACE")
			session := os.Getenv("MARVEL_SESSION")
			if !send || socket == "" || workspace == "" || session == "" {
				return nil
			}
			// Deliberately no rate-limit fields here. Every parameter of
			// this RPC is keyed to one session, and the account's headroom
			// is not a property of any session; see the rateLimits type.
			// It reaches the pane and stops there until an account-scoped
			// home exists to send it to.
			// The constructor reads the token marvel minted for this
			// session at spawn. It is what lets the daemon tell this
			// session reporting itself from any other process on the host
			// reporting on its behalf. Absent (a session spawned before
			// tokens existed) the daemon admits the beat and says so on
			// the ring.
			p := api.NewHeartbeatRequest(workspace+"/"+session, pct, model)
			// The numerator and the denominator behind the harness's own
			// percentage. Forwarding both turns this into a GRADED reading:
			// UpdateSessionHeartbeat routes the window through usage.Resolve
			// (as a LimitFromFeed rung, below an operator's manifest
			// override) and derives the percentage from occupancy over the
			// window it resolves — so an operator's runtime.context_window
			// wins over the harness's own, and CTX% is against the
			// denominator marvel trusts. The bare percentage above stands
			// only as the fallback when there is nothing to derive from.
			// This was the seam ctxforward opened and aae-orc-38yr closed;
			// the daemon no longer drops these fields.
			//
			// Zero means the payload declared none, and an undeclared figure
			// must not arrive as a declared zero: tokens omitted keeps the
			// percentage-only reading, a window of zero resolves to nothing.
			if tokens > 0 {
				p.ContextTokens = tokens
			}
			if window > 0 {
				p.ContextWindow = window
			}
			params, _ := json.Marshal(p)
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
