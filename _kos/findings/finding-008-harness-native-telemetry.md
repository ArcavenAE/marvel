---
id: finding-008-harness-native-telemetry
title: "Harness-native telemetry as a third observation channel (OTEL / Prometheus / metrics export)"
question: question-stream-attachment
confidence: frontier
tags: [marvel, observability, otel, telemetry, harness]
bd: [aae-orc-mki1, aae-orc-w5su, aae-orc-dc1j]
provenance:
  created_by: agent
  session: marvel-mki1-otel-2026-08-01
  host: kinu
  created_at: "2026-08-01"
---

# Harness-native telemetry as a third observation channel

Three of the five target harnesses (Claude Code, Codex, Gemini CLI) emit
OpenTelemetry natively, and Claude Code's feed is live-verified on this host at
version 2.1.220. This matters because a native OTEL feed is emitted regardless
of UI mode: it flows in interactive/TUI sessions as well as headless ones,
which is the exact case the stream channel (headless-only) cannot reach. So
native telemetry is a genuine third channel, orthogonal to the FIFO-stream and
capture-pane strategies in finding-005, and it is the first viable path to
interactive-session context pressure (aae-orc-dc1j).

The load-bearing negative result: none of the five harnesses exports a
context-window occupancy gauge or a percentage-used value. Every one that emits
telemetry emits token counts, not occupancy. So native telemetry does not hand
marvel CTX% for free. It hands marvel the same numerator the stream does (the
per-request token classes) and marvel must still derive occupancy with the
finding-007 accountant. Native telemetry therefore re-scopes aae-orc-w5su rather
than resolving it, and gives dc1j a path where none existed rather than closing
it outright.

No harness exposes a Prometheus scrape endpoint that marvel could poll. Where
"Prometheus" appears it is either a metrics-exporter option that still pushes
via OTLP (Claude Code), a residual/transitive string in a bundled runtime
(opencode), or a Prometheus-compatible backend reached through a collector
(Gemini). The delivery model for all OTEL-capable harnesses is OTLP push, not
scrape.

## Method

Host kinu, macOS (arm64). Installed and inspected: Claude Code 2.1.220
(`~/.local/share/claude/versions/2.1.220`, a Mach-O-wrapped JS bundle), Codex
0.146.0 (`~/.codex/packages/standalone/releases/0.146.0-aarch64-apple-darwin`),
opencode 1.18.5 (Homebrew, Node/bun `.exe` bundle), Crush 0.67.0 (Homebrew Go
binary). Gemini CLI is not installed here and is covered from docs and the graph
only.

Per harness I inspected `--help`, config files, and env-var/metric-name literals
in the installed binary, and cross-checked each web claim against those literals.
For Claude Code I ran one live headless turn (`claude -p`, model haiku, one short
prompt) with `CLAUDE_CODE_ENABLE_TELEMETRY=1`, `OTEL_METRICS_EXPORTER=console`,
`OTEL_LOGS_EXPORTER=console`, and short export intervals, and captured the real
metric and event records the SDK printed. That is the only model call spent on
this probe. Codex/opencode/crush OTEL surfaces are docs-cited and binary-confirmed
but were not driven against a live model turn (their token/usage stream shape is
already live-verified in finding-007; the OTEL wiring is the new part and was not
exercised end to end).

Every web claim carries its source URL inline. Marvel's own OTEL shape was read
from `internal/otel/metrics.go` in this repo.

## Per-harness table

| Harness | Native OTEL? (mechanism) | Prometheus scrape? | Metrics exported (incl. context-occupancy?) | Per-session attribution | Collection shape for marvel | Evidence |
|---|---|---|---|---|---|---|
| Claude Code 2.1.220 | Yes. `CLAUDE_CODE_ENABLE_TELEMETRY=1` plus standard OTEL env (`OTEL_METRICS_EXPORTER`, `OTEL_LOGS_EXPORTER` = `otlp`/`prometheus`/`console`/`none`; full `OTEL_EXPORTER_OTLP_*` set incl. per-signal endpoint/protocol/headers, temporality preference, export intervals). OTLP grpc, http/protobuf, http/json; console; prometheus (metrics only, pull). Meter `com.anthropic.claude_code`, events logger `com.anthropic.claude_code.events`. | No native scrape endpoint. `prometheus` is a metrics-exporter option, still OTLP-family push/pull config, not a marvel-pollable `/metrics` route in the CLI. | Metrics: `session.count`, `token.usage` (attrs `type`, `model`), `cost.usage`, `active_time.total`, `lines_of_code.count`, `commit.count`, `pull_request.count`, `code_edit_tool.decision`, plus newer `hook`/`interaction`/`llm_request`/`subagent.spawn`/`tool.execution`/`tool.blocked_on_user`. Events: `api_request` (carries `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens`, `cost_usd`, `model`, `duration_ms`), `user_prompt`, `assistant_response`, `tool_result`, `tool_decision`, `api_error`. **No context-occupancy metric; no percentage; no `contextWindow` field in the OTEL feed at all** (denominator is in the stream, not here). | Yes. `session.id` on datapoints (`OTEL_METRICS_INCLUDE_SESSION_ID` default true), plus resource `service.name=claude-code`, `service.version`, `os.type`, `host.arch`, `terminal.type`, `user.id`, `app.version`. Marvel can inject `OTEL_RESOURCE_ATTRIBUTES=marvel.agent_id=...` per session. | Point session exporter at one shared marvel OTLP receiver, correlate by `session.id` + injected `marvel.agent_id`. Console/outfile exporter tailed by marvel is the no-network variant. | **live-verified** on kinu. Docs: [1] |
| Codex 0.146.0 | Yes. `[otel]` in `~/.codex/config.toml`: `otel.environment`, `otel.exporter` (`none`/`otlp-http`/`otlp-grpc`, default `none`), `otel.log_user_prompt` (default false), and in 0.146-era `otel.trace_exporter`, `otel.metrics_exporter` (default `statsig`, OpenAI's own channel, not your collector). OTLP http/grpc. **Config is enforced user-level; project-level `.codex/config.toml` `otel.*` keys are ignored** (finding-005, orc finding-087). | No native scrape endpoint documented; OTLP push. | Metrics: `codex.api_request`, `codex.sse_event`, `codex.tool.call` (each with a `*.duration_ms` histogram), `turn.token_usage` histogram (`token_type` = total/input/cached_input/output/reasoning_output). Default dims `auth_mode`/`originator`/`session_source`/`model`/`app.version`. Logs: `conversation_starts` (**carries `context_window`, `auto_compact_token_limit`, `max_output_tokens`** = the limit), `api_request`, `sse_event`, `user_prompt`, `tool_decision`, `tool_result`. **No occupancy gauge/percentage**; token counts only, but the LIMIT is in the log feed (unlike Claude). | Logs: yes (`conversation.id` on every record). Metrics: weak (no `conversation.id` on default metric dims, only `session_source`/`originator`). | Set user-level `otel.exporter`=`otlp-http/grpc` + `otel.metrics_exporter` off `statsig` to a marvel receiver. Note: user-level config is shared across all codex sessions on the host, so per-session isolation via config is not clean; env-based per-process override is unverified. | docs-cited + binary-confirmed (`otlp-http`, `otlp-grpc`, `statsig`, `turn.token_usage`, `context_window`, `conversation.id` in the 0.146.0 binary). Docs: [2][3][4] |
| opencode 1.18.5 | No usable native OTEL. The OTEL SDK init (NodeSDK + OTLP trace exporter hard-coded to `http://localhost:4318/v1/traces`) was removed as unused in PR sst/opencode#1738 (Aug 2025). Binary strings `OTEL_EXPORTER_OTLP_ENDPOINT`, `Prometheus`, `/metrics` are residual/transitive from the bundled runtime, not a wired exporter. | No. Server routes are health/OpenAPI/SSE only; no `/metrics`. | None via OTEL. Structured surface is its own JSON event stream (`run --format json`, `serve` SSE) and `opencode.db` sqlite (finding-005/007). No occupancy metric. | Via stream/db only (finding-007 path). | Use the finding-007 stream accountant; no telemetry channel to add. | docs-cited + binary cross-checked (strings present, feature removed per PR #1738). Docs: [5][6] |
| Crush 0.67.0 | No. No operator-controllable OTLP exporter and no collector target. Ships PostHog product-analytics to `data.charm.land` (v0.10.0+), disable with `CRUSH_DISABLE_METRICS=1` / `DO_NOT_TRACK=1`. That stream is Charm's, not marvel-addressable. | No. | None marvel can consume. Local sources are `crush.db` sqlite and `.crush/logs/crush.log` (finding-005: no structured stdout stream at all). No occupancy metric. | Via `crush.db` only. | Reconstruct usage from `crush.db` message tokens against a limit table (finding-007); no telemetry channel. | docs-cited + binary cross-checked (`posthog`, `data.charm.land` in the 0.67.0 binary). Docs: [7][8] |
| Gemini CLI (not installed) | Yes (docs). Enable `gemini --telemetry`, or `.gemini/settings.json` `telemetry.enabled=true`, `target` `local`/`gcp`, `otlpEndpoint` (default `http://localhost:4317`, OTLP/gRPC; later builds add http). Flags `--telemetry-target`, `--telemetry-otlp-endpoint`, `--telemetry-log-prompts`, `--telemetry-outfile`. Env `GEMINI_TELEMETRY_*` and `OTEL_EXPORTER_OTLP_ENDPOINT`. Default disabled. | No native scrape; Prometheus only via a collector/backend. | Metrics: `gemini_cli.session.count`, tool-call count/latency, api-request count/latency, token usage (`input`/`output`/`thought`/`cache`/`tool`), file-op counts. Events: startup config, user prompt, tool calls, api request/response/error, token counts. **No context-occupancy metric.** | Yes (session id on records; per-session settings/flags). | Same as Claude: shared OTLP receiver, per-session correlation. `--telemetry-outfile` gives a per-session file sink. | docs-cited only, NOT installed on kinu. Re-verify against a current build (finding-005 gates on #9009/#13561). Docs: [9][10] |

Sources:
- [1] Claude Code monitoring/telemetry: https://docs.anthropic.com/en/docs/claude-code/monitoring-usage and https://code.claude.com/docs/en/monitoring-usage ; env vars https://code.claude.com/docs/en/env-vars ; context-window/statusline (separate from OTEL) https://code.claude.com/docs/en/context-window , https://code.claude.com/docs/en/statusline
- [2] Codex config/otel: https://github.com/openai/codex/blob/main/docs/config.md
- [3] Codex config reference (0.146-era logs/traces/metrics + statsig default): https://learn.chatgpt.com/docs/config-file/config-reference and https://learn.chatgpt.com/docs/config-file/config-advanced
- [4] Codex otel crate README: https://fossies.org/linux/codex-rust/codex-rs/otel/README.md
- [5] opencode server routes (no metrics endpoint): https://opencode.ai/docs/server/
- [6] opencode OTEL removal PR: https://github.com/sst/opencode/pull/1738
- [7] Crush metrics (PostHog) guide: https://charmbracelet-crush.mintlify.app/guides/metrics
- [8] Crush v0.10.0 analytics note: https://newreleases.io/project/github/charmbracelet/crush/release/v0.10.0
- [9] Gemini CLI telemetry: https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/telemetry.md
- [10] Gemini CLI configuration reference: https://github.com/google-gemini/gemini-cli/blob/main/docs/reference/configuration.md

## What the live Claude Code capture showed (2.1.220)

Enable and exporter env vars, confirmed as literals in the 2.1.220 binary and
exercised live: `CLAUDE_CODE_ENABLE_TELEMETRY`, `OTEL_METRICS_EXPORTER`,
`OTEL_LOGS_EXPORTER`, `OTEL_EXPORTER_OTLP_ENDPOINT`/`_PROTOCOL`/`_HEADERS` with
per-signal (`_METRICS_`/`_LOGS_`/`_TRACES_`) overrides,
`OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE`,
`OTEL_METRIC_EXPORT_INTERVAL`, `OTEL_LOGS_EXPORT_INTERVAL`,
`OTEL_RESOURCE_ATTRIBUTES`, `OTEL_LOG_USER_PROMPTS`, and the
`OTEL_METRICS_INCLUDE_SESSION_ID`/`_VERSION`/`_ACCOUNT_UUID`/`_ENTRYPOINT`/`_RESOURCE_ATTRIBUTES`
toggles.

One `claude -p` haiku turn with the console exporter emitted, verbatim: the
`claude_code.session.count` metric (dataPointType 3), the `claude_code.token.usage`
metric (attrs `type`, `model`, unit `tokens`), `claude_code.cost.usage`,
`claude_code.active_time.total`, and log events `claude_code.user_prompt`,
`claude_code.api_request`, `claude_code.assistant_response`,
`claude_code.tool_result`-class records, plus lifecycle events
(`hook_registered`, `mcp_server_connection`, `permission_mode_changed`). The
`api_request` event carried `input_tokens: 10`, `output_tokens: 43`,
`cache_read_tokens: 16514`, `cache_creation_tokens: 13397`, `cost_usd`,
`cost_usd_micros`, and `model`. Resource block: `service.name=claude-code`,
`service.version=2.1.220`, `os.type=darwin`, `host.arch=arm64`; datapoints
carried `session.id`, `user.id`, `terminal.type`, `app.version`.

Context-window occupancy: a grep of the entire emitted telemetry for
`contextWindow`/`context_window`/`used_percent`/`occupancy`/`remaining` returned
zero. The occupancy denominator that finding-007 reads from the stream
(`result.modelUsage.<model>.contextWindow = 200000`) is not present anywhere in
the OTEL feed. The numerator is: `api_request` carries exactly the token classes
finding-007 sums (`input_tokens + cache_read_tokens + cache_creation_tokens`).
So CTX% is derivable from the OTEL feed only if marvel supplies the limit from a
model-to-limit table (the stream would give it inline; OTEL does not).

Note on capture mechanics: with `--output-format json` the console exporter's
output is swallowed by the JSON redirect; the metric/event records print to the
same stream as text output, so a merged-stream capture in text mode is required
to see them.

## How this bears on the stream and capture-pane channels

**UI-mode independence is the whole point.** The finding-005/007 stream exists
only in headless mode; capture-pane scraping the rendered TUI indicator
(finding-005 case c/d, 0.24 to 0.48 percent coverage on a burst) is the fragile
fallback dc1j is stuck with today. Native OTEL is emitted by the harness itself
whether it is drawing a TUI or streaming NDJSON, so for Claude Code, Codex, and
Gemini it delivers per-session usage in interactive sessions with no terminal
scraping at all.

**It is coarser than the stream for structure.** OTEL gives aggregate metrics at
the export interval (60s default for metrics, 5s for logs, delta temporality) and
discrete events, not the stream's real-time per-turn envelope. The stream carries
`tool_use` detail, thinking tokens, per-message assistant content, and the
`result` envelope that OTEL flattens into counters and a few event fields. For
deep per-turn structure the stream still wins; for UI-independent metering
(tokens, cost, tool counts, errors) OTEL wins.

**It does not own the terminal**, so like the stream it does not constrain the
substrate decision (aae-orc-kxce). It is a sidecar signal, independent of what
owns the pane.

## Collection shape for marvel

Marvel today is an OTEL metric *producer*, not a *receiver*:
`internal/otel/metrics.go` builds a stdout meter provider and already declares a
`marvel.agent.context_window_percent` gauge that marvel computes and emits. There
is no OTLP receiver in the tree. Consuming harness-native OTEL needs one of:

- **One shared in-process OTLP receiver** (recommended). Point every OTEL-capable
  session's exporter at it (`OTEL_EXPORTER_OTLP_ENDPOINT` for Claude/Gemini;
  user-level `otel.exporter` for Codex), and correlate by `session.id` plus an
  injected `OTEL_RESOURCE_ATTRIBUTES=marvel.agent_id=<id>`. One receiver for all
  sessions; cheapest and matches the existing "marvel collects and routes"
  design in marvel CLAUDE.md.
- **Per-session file/console exporter that marvel tails.** No network, strongest
  isolation, but N sinks and file-tail latency. Good for the console/outfile
  variants (`OTEL_METRICS_EXPORTER=console`, Gemini `--telemetry-outfile`).
- **Per-session collector process.** Rejected as default: a full collector per
  session is heavy relative to a shared receiver and buys little when the
  harness already speaks OTLP.

**Auth/isolation (SOUL.md section 3).** OTEL carries no credentials; each session
runs under its own harness auth, and a marvel receiver ingesting metrics routes
no one's credentials, so the channel is clean against the multi-user boundary.
Two cautions: keep prompt logging off (`OTEL_LOG_USER_PROMPTS` /
`otel.log_user_prompt` default off) so prompt text is never shipped into marvel's
receiver; and Codex's user-level-only otel config means enabling it touches a
host-global file shared by every codex session, which is a real isolation wrinkle
for per-session routing on that harness.

## Recommendation

**Native telemetry is a complement to stream-parsing, not a replacement, and per
harness it splits three ways.**

- **Claude Code, Gemini CLI: complement, and the primary metering channel for
  interactive/TUI sessions.** Use the stream for headless deep structure; use
  OTEL for UI-independent metering (the only path that survives TUI mode). Both
  read the same token numerator; pick per session mode.
- **Codex: complement, with a caveat.** OTEL is real and its `conversation_starts`
  log even carries the context-window limit the stream withholds, but the
  user-level-only config and `statsig`-default metrics exporter make per-session
  routing awkward. Prefer the stream for headless; treat OTEL as the TUI-mode
  metering path, accepting the shared-config wrinkle.
- **opencode, Crush: not viable.** opencode removed its OTEL exporter (PR #1738);
  Crush never had one (PostHog only). For both, the finding-007 stream/sqlite
  accountant stays the only path. There is no telemetry channel to add.

**Does it resolve w5su (context pressure)?** No; it re-scopes it. No harness
exports occupancy, so marvel still runs the finding-007 usage-accountant. Native
telemetry changes the *feed* into that accountant (OTEL `api_request` token
classes keyed on `session.id`, instead of the stream `result.usage`), and for
Codex it actually improves the denominator story (limit present in
`conversation_starts`). The Claude OTEL feed lacks the denominator the stream
carries, so the model-to-limit table finding-007 needed for Codex/OpenCode now
also applies to Claude if OTEL is the feed. Net: same derivation, additional
UI-mode-independent feed, one shared accountant.

**Does it resolve dc1j (interactive/TUI context pressure)?** Partially, and it is
the first clean path. For Claude, Codex, and Gemini, enabling native OTEL plus a
per-session resource attribute yields per-session token usage in interactive
sessions, retiring the fragile capture-pane scrape of the rendered indicator. It
does not fully close dc1j because (a) the same occupancy derivation still applies,
and (b) opencode and Crush have no OTEL, so their interactive CTX% still has no
telemetry path. Recommend re-scoping dc1j to "collect native OTEL and run the
finding-007 accountant for OTEL-capable harnesses; leave opencode/Crush
interactive CTX% as a separate, lower-priority gap."

## Gaps

- **Only Claude Code was driven live for OTEL.** Codex/Gemini OTEL wiring is
  docs-cited and (for Codex) binary-confirmed but not exercised end to end
  against a live model turn. One short OTLP-collector smoke per harness would
  settle the metric/event shape as finding-007 settled the stream shape.
- **Codex per-session env override is unverified.** Whether
  `OTEL_EXPORTER_OTLP_ENDPOINT` overrides the user-level `otel.*` config
  per-process (enabling per-session routing without touching global config) was
  not tested. This is the load-bearing unknown for using Codex OTEL under marvel
  multi-session isolation.
- **Gemini CLI is not installed here.** Entire row is docs/graph, gated on
  re-verifying #9009/#13561 against a current build (finding-005).
- **No live compaction crossing.** As in finding-007, the downward-step behavior
  of the token numerator across an auto-compaction was not measured; whether OTEL
  emits any compaction marker (Claude has `pre_tokens`/`post_tokens` literals in
  the binary) is unconfirmed live.
- **macOS only** (B13). Re-measure on Linux before treating the exporter behavior
  as the platform contract.
- **Version-pinned.** Claude 2.1.220, Codex 0.146.0, opencode 1.18.5, Crush
  0.67.0. A harness bump that changes the telemetry schema changes the read.
