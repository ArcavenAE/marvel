package main

import (
	"encoding/json"
	"os"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/runtime/codex"
	"github.com/spf13/cobra"
)

// codex-ctx is the hook side of codex's context feed, the sibling of
// ctx-forward. Codex invokes a configured hook command with a JSON
// payload on stdin; this subcommand takes the payload's transcript_path,
// reads the newest usable occupancy record out of that rollout file, and
// forwards the figure to the daemon's heartbeat RPC.
//
// It is the only source of codex context pressure. The `codex exec
// --json` stream cannot produce one: its turn.completed usage object is
// a running total, measured at 28110 where the prompt was 14105
// (finding-017 §4). The rollout is not the preferred channel; it is the
// only one.
//
// # This command prints nothing, and that is load-bearing
//
// ctx-forward prints a status line because Claude Code's statusline hook
// exists to render one for the human in the pane. A codex hook's stdout
// goes somewhere else entirely: measured against codex-cli 0.146.0, a
// line printed by a SessionStart hook arrives in the rollout as a
// `developer` role `input_text` message, which is to say it is fed to
// the model. A context-pressure reporter that printed its own reading
// would add context on every fire. So this command writes nothing to
// stdout, and a broken feed shows as a gap in CTX% diagnosed through
// `marvel describe session`.
//
// Failure posture is otherwise ctx-forward's: never exit nonzero, never
// print errors, because this runs inside an agent's session.

// codexHookPayload is the subset of a codex hook payload this command
// reads. Every field codex sends that is not declared here is dropped in
// flight by encoding/json with no error and nothing to notice, so
// omitting one is a decision rather than an oversight.
//
// transcript_path, NOT agent_transcript_path. The latter exists on
// exactly one of codex's eleven hook schemas (subagent-stop) and names a
// SUBAGENT's file; a design built on that name finds the field absent on
// every hook that matters. Both are typed NullableString while sitting
// in `required`, so present is not the same as non-null and the pointer
// here is the shape codex really emits.
//
// Verified live against codex-cli 0.146.0 on 2026-08-09: a SessionStart
// payload carries session_id, transcript_path, cwd, hook_event_name,
// model, permission_mode and source.
type codexHookPayload struct {
	TranscriptPath *string `json:"transcript_path"`
	// Model is codex's own name for the session's model, present on ten
	// of the eleven hook schemas (session-end is the exception). It is
	// forwarded for display; it resolves no window, because marvel's
	// codex table is deliberately empty (internal/usage/limits.go).
	Model *string `json:"model"`
	// HookEventName and SessionID are parsed and not used. They are the
	// two fields a reader reaches for first when this feed misbehaves,
	// and declaring them keeps the payload's shape on the page.
	HookEventName string  `json:"hook_event_name"`
	SessionID     *string `json:"session_id"`
}

// codexReading resolves one hook payload to a forwardable figure. send
// is false whenever the caller must HOLD its previous reading: no
// pointer, an unreadable rollout, no usable sample in the tail, or a
// sample declaring no window. Holding is safe because occupancy is
// monotone within a compaction generation; reporting zero is not, and
// zero is what every one of these cases would otherwise produce.
//
// Pure, so the decision table is testable without a daemon or a hook.
func codexReading(raw []byte) (pct float64, model string, window int, send bool) {
	var p codexHookPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0, "", 0, false
	}
	if p.TranscriptPath == nil || *p.TranscriptPath == "" {
		return 0, "", 0, false
	}
	reading, err := codex.ReadOccupancy(*p.TranscriptPath)
	if err != nil {
		// Every failure reaches the same answer, which is to say
		// nothing. ErrNoSample is the ordinary mid-turn case rather than
		// a fault (a multi-megabyte tool-output record can sit between
		// the newest sample and EOF); an unopenable path is a real
		// fault; neither may become a zero.
		return 0, "", 0, false
	}
	pct, ok := reading.Percent()
	if !ok {
		return 0, "", 0, false
	}
	// The model rides out only with a reading. A hold sends nothing at
	// all, so returning an identity for a figure that will not be sent
	// would be a value with no destination.
	if p.Model != nil {
		model = *p.Model
	}
	return pct, model, reading.Window, true
}

func newCodexCtxCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "codex-ctx",
		Short:  "Forward a codex hook payload's context figure to the daemon (internal)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readAllStdin()
			if err != nil {
				return nil
			}
			pct, model, window, send := codexReading(raw)

			socket := os.Getenv("MARVEL_SOCKET")
			workspace := os.Getenv("MARVEL_WORKSPACE")
			session := os.Getenv("MARVEL_SESSION")
			if !send || socket == "" || workspace == "" || session == "" {
				return nil
			}
			p := codexHeartbeatParams(workspace, session, os.Getenv(api.HeartbeatTokenEnv), model, pct, window)
			params, _ := json.Marshal(p)
			// Best-effort by design; see the failure posture above.
			_, _ = daemon.SendRequest(socket, daemon.Request{
				Method: "heartbeat",
				Params: params,
			})
			return nil
		},
	}
}

// codexHeartbeatParams builds the heartbeat RPC's params. It is a
// separate function so a test can assert what goes on the wire without
// standing up a daemon; the pair of keys below is exactly what the
// merge of #170 and #168 got wrong, and an inline literal inside a
// cobra closure is not reachable by any test that would have caught it.
func codexHeartbeatParams(workspace, session, token, model string, pct float64, window int) map[string]any {
	p := map[string]any{
		"session_key":     workspace + "/" + session,
		"context_percent": pct,
		"model":           model,
	}
	// The token marvel minted for this session at spawn, exactly as
	// ctx-forward sends it. Without it the daemon refuses the beat:
	// authenticateHeartbeat fails open only when the record carries no
	// hash, and Manager.Create now mints one before the record exists, so
	// every session spawned by a current daemon has one. A tokenless codex
	// beat lands as ErrHeartbeatUnauthorized and CTX% renders absence.
	//
	// That is not hypothetical: it is what the merge of #170 and #168
	// produced. codexctx was written against a base where
	// UpdateSessionHeartbeat took no token, and the two landed the same
	// night. Nothing failed to compile, because the payload is a
	// map[string]any on the wire, and no test on either side covered the
	// pair.
	if token != "" {
		p["session_token"] = token
	}
	// context_window is the producer half of a seam whose consumer does
	// not exist: internal/daemon.heartbeatParams has no field for it and
	// drops it. See the long comment in ctxforward.go, which names the two
	// edits that finish it.
	//
	// Codex sharpens that open decision rather than settling it. This
	// window is a `stream` declaration (rung 1), and codex is the clean
	// case: limitLadder's rung-1 sentence has two conjuncts, the harness
	// enforcing compaction against the window AND stating it "in the same
	// channel as the token counts it is stating it about", and
	// model_context_window satisfies both by riding the level's own record.
	//
	// Do NOT generalize the rung from this comment. A window the harness
	// serves over a separate contracted query satisfies the first conjunct
	// and not the second, and rung 4's text (a human-facing status hook,
	// no version handle) does not describe it either. That case is open
	// with the operator, marvel PR #172; see finding-023 and finding-020.
	// Whatever rung it lands on, it needs a refetch rule codex does not:
	// fetched under a different model, a window is unresolved rather than
	// stale.
	//
	// The heartbeat RPC carries no rung and no window, so the distinction
	// is lost at this seam today.
	if window > 0 {
		p["context_window"] = window
	}
	return p
}
