---
id: finding-005-stream-attachment-probe
title: "Stream attachment: harness telemetry inventory plus byte-path fidelity measurement"
probe: _kos/probes/stream-attachment-bytepath
question: question-stream-attachment
confidence: frontier
tags: [marvel, streaming, observability, harness]
bd: aae-orc-3cp
addressed_to: aae-orc-kxce
provenance:
  created_by: agent
  session: marvel-wave-1-lane-s2
  created_at: "2026-07-31"
  host: kinu
---

# Stream attachment: harness inventory plus byte-path measurement

Marvel has to decide how it observes agent output before it can decide what
owns the terminal, and the two questions have been answered by preference
rather than measurement. This probe supplies the missing numbers: what each
target harness offers as a structured surface, and what each candidate byte
path actually delivers under fast output. The short version is that every
harness marvel is targeting except Crush emits newline-delimited JSON in
headless mode, a FIFO carries those bytes without alteration, and
`capture-pane` scraping loses data at a rate that is predictable from two
numbers, so it can be scoped rather than guessed at.

Scope: part (i) telemetry-surface inventory, part (ii) byte-path fidelity.
This finding does not settle the substrate decision (`aae-orc-kxce`); it
supplies the attachment half of the input. The shim-PTY-tee path cannot be
measured until the `aae-orc-e35c` spike lands, so it is absent from the
byte-path table by necessity, not by judgement.

## Method

Host kinu, macOS 26.5.2 (arm64), tmux 3.7b, Go 1.25.4. Harnesses as installed:
Claude Code 2.1.220, codex-cli 0.146.0, Crush v0.67.0, OpenCode 1.18.5. Gemini
CLI is not installed on this host and is covered from the graph only.

Part (i) is `--help` inspection, filesystem inspection of each harness's
session store, literal-string inspection of each binary for telemetry env-var
names, and a stdin-closed invocation per harness to check for a TTY gate.
Binary string inspection used a known-absent control name to confirm the grep
discriminates rather than matching everything.

Part (ii) is a synthetic producer, so no model tokens were spent on it. The
producer emits numbered lines wrapped in SGR escapes, one line per newline,
plus a terminal `DONE:<n>` marker, so loss, reordering, and escape mangling are
all recoverable from the sink's bytes alone. Scoring is on the sink file only.
Every pane command carries one second of lead time so the sink is attached
before the first byte is written; without it the measured loss is the harness's
race, not the path's. Scripts: `_kos/probes/stream-attachment-bytepath/`
(`producer.go`, `run.sh`, `run-paced.sh`).

One real model call was spent, on the smoke test in the last section.

## Part (i): per-harness inventory

| Harness | Version | Headless one-shot | Structured stream | Telemetry / OTLP | Session store on this host | Hook / statusline surface | TTY required |
|---|---|---|---|---|---|---|---|
| Claude Code | 2.1.220 | `-p` / `--print` | `--output-format text\|json\|stream-json`, `--input-format stream-json`, `--include-partial-messages`, `--json-schema` | `CLAUDE_CODE_ENABLE_TELEMETRY` plus `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_METRICS_EXPORTER`, `OTEL_LOGS_EXPORTER`, `OTEL_LOG_USER_PROMPTS`, `OTEL_RESOURCE_ATTRIBUTES` (all present as literals in the 2.1.220 binary) | `~/.claude/projects/<slugged-cwd>/<session-id>.jsonl` (1077 files here) | hooks plus `statusLine` via `settings.json` | no |
| Codex | 0.146.0 | `codex exec` | `--json` (JSONL events), `--output-schema`, `-o/--output-last-message`; `mcp-server` stdio JSON-RPC; `app-server`, `remote-control`, `exec-server` marked experimental | `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `otel.exporter` literals in binary; `otel.*` keys are ignored in project-level `.codex/config.toml` and enforced user-level (orc finding-087) | `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl` plus `state_5.sqlite`, `logs_2.sqlite`, `memories_1.sqlite` | `statusline` literal present; `notify` is user-level only (finding-087) | no |
| Crush | v0.67.0 | `crush run [prompt]`, accepts piped stdin | none. `-q/--quiet` and `-v/--verbose` only; output is prose text | no OTEL literals in binary | per-project `<repo>/.crush/crush.db` (sqlite: `sessions`, `messages`, `files`) plus `logs/`; registry at `~/.local/share/crush/projects.json` | `crush logs -f`; no statusline literal | no |
| OpenCode | 1.18.5 | `opencode run` | `--format default\|json` (raw JSON events); `serve` headless server, `attach <url>`, `acp` (Agent Client Protocol) server, `export <sessionID>`, `stats` | `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME` literals in binary | `~/.local/share/opencode/opencode.db` (sqlite: `session`, `message`, `part`, `session_message`, `permission`, `todo`) plus `log/` | `--print-logs`, `--log-level`; plugin system; `statusline` literal present | no |
| Gemini CLI | not installed | `--non-interactive` | `--output-format text\|json\|stream-json` | not verified | not present | not verified | not verified |

Notes that matter for the binding decision:

**Every structured mode here is line-delimited JSON, not a bespoke protocol.**
Claude's `stream-json`, Codex's `exec --json`, and OpenCode's `run --format
json` all emit one JSON object per line. A single NDJSON reader covers three of
the five targets, and Gemini's documented `stream-json` would make four.

**Crush is the odd one out and needs its own decision.** It has no structured
output flag at any version installed here. It does have a client/server split
(`-H/--host unix:///tmp/crush-501.sock`), which is a plausible structured
surface, but no socket existed on this host during the probe and the protocol
is undocumented in `--help`. Until someone probes that socket, Crush output is
opaque text and its per-project sqlite store is the only structured record.

**Transcript files are a working passive telemetry surface, including for
headless runs.** The single `-p` smoke below wrote
`~/.claude/projects/-private-tmp/<session-id>.jsonl` containing per-assistant-message
`message.usage` (`input_tokens`, `output_tokens`, `cache_read_input_tokens`,
`cache_creation_input_tokens`, `service_tier`, `speed`, `iterations`). Codex's
rollout files carry the same class of data in an `event_msg` payload of type
`token_count`: `total_token_usage` and `last_token_usage` (input, cached,
cache_write, output, reasoning, total), plus `model_context_window` and
`rate_limits.used_percent`. Orc finding-057's `compactMetadata.postTokens` is
the compaction-event counterpart on the Claude side. A poller of these files
gets token accounting for free, with no attachment at all, at the cost of
file-tail latency.

**Gemini stays flagged.** `docs/research/2026-07-05-harness-survey.md` records
`--output-format stream-json` alongside issues #9009 (JSON output bug) and
#13561 (approval prompts still fire in non-interactive mode despite
`--approval-mode yolo`). Both must be re-verified against a current build
before Gemini is bound to a structured path.

**No harness gated on a TTY.** With stdin at `/dev/null` and no controlling
terminal, `codex exec --json` reached "Reading prompt from stdin... No prompt
provided via stdin", `crush run` and `opencode run` both reached argument
validation, and Claude completed a full headless turn (below). This is entry-path
evidence for three of them and end-to-end evidence for Claude; it does not rule
out a TTY dependence inside a Codex/Crush/OpenCode turn.

## Part (ii): byte-path fidelity

Single burst, N=20000 lines (1.37 MB of producer bytes), pane 200x50.
"redundant" counts re-delivered sequence numbers, "inversions" counts
first-arrival order violations, "intact_ansi" counts lines whose original
`ESC[32m ... ESC[0m` wrapper survived byte-for-byte.

| Path | Coverage | Redundant | Inversions | Intact ANSI | Sink bytes | DONE marker |
|---|---|---|---|---|---|---|
| (a) FIFO redirect | 20000/20000 (100%) | 0 | 0 | 20000 | 1368905 | yes |
| (b) tmux `pipe-pane` | 20000/20000 (100%) | 0 | 0 | 20000 | 1388930 | yes |
| (c) `capture-pane` visible, 1 Hz | 97/20000 (0.48%) | 48 | 0 | 0 | 8803 | yes |
| (d) `capture-pane` visible, 10 Hz | 48/20000 (0.24%) | 960 | 0 | 0 | 61342 | yes |
| (e) `capture-pane -S -` history, 1 Hz, `history-limit 100000` | 20000/20000 (100%) | 32349 | 0 | 0 | 3107689 | yes |
| (f) same, `history-limit 2000` (tmux default) | 4011/20000 (20.05%) | 2000 | 0 | 0 | 360729 | yes |

Write-side elapsed, which is the sink's backpressure signature, was 0.01 s for
the FIFO (2.6 M lines/s) and 0.04 s through the pane (0.45 M lines/s). Neither
path is anywhere near being the constraint for agent-rate output; the pane
costs roughly 6x the FIFO in write-side throughput and both are orders of
magnitude above what a model emits.

Three mechanism facts, each verified directly rather than inferred:

**FIFO delivers the process's own bytes; `pipe-pane` delivers the cooked PTY
stream.** The 20 KB difference in sink bytes is carriage returns: the FIFO file
holds 20001 LF and 0 CR, the `pipe-pane` file holds 20001 LF and 20025 CR (one
per newline plus a small constant from pane setup). Any consumer of `pipe-pane`
must strip CR before parsing NDJSON. Content is otherwise identical, ordering
intact, escapes intact.

**`capture-pane -e` does not replay the producer's escape sequences; it
re-renders attribute state.** Across 20000 lines each individually wrapped in
`ESC[32m`/`ESC[0m`, the captured output contained three `ESC[32m` and two
`ESC[39m` sequences and zero of the original per-line resets. tmux collapses
equal adjacent attributes into runs and substitutes its own default-foreground
code for the reset. Rendered color survives; escape structure does not. A
consumer that needs the agent's own control sequences (progress redraws, cursor
moves, alternate-screen use) gets nothing usable from a scrape.

**Scrape loss follows a capacity rule: `visible_rows * poll_hz` lines per
second.** The burst above completed in under 0.1 s, so it could not
discriminate poll rates, which is why (d) at 10 Hz scored worse than (c) at
1 Hz: both caught essentially one screenful, and which screenful is noise. A
paced producer settles it. Six seconds of output at fixed rates, 50-row pane,
visible-region scrape:

| Sustained rate | 1 Hz poll (capacity 50/s) | 10 Hz poll (capacity 500/s) |
|---|---|---|
| 20 lines/s | 120/120 (100%) | 120/120 (100%) |
| 40 lines/s | 240/240 (100%) | 240/240 (100%) |
| 200 lines/s | 297/1200 (24.75%) | 1200/1200 (100%) |

The 200 lines/s at 1 Hz case predicted 50/200 = 25% and measured 24.75%. Below
capacity the scrape is lossless; above it, coverage is the ratio. This makes
scraping specifiable rather than merely bad: a `generic` adapter can state its
own ceiling.

Two corollaries for anyone implementing the scrape fallback. Scrape history
(`-S -`), not the visible region, or the ceiling drops to one screen per poll.
And raise `history-limit`: at the tmux default of 2000, case (f) lost 80% of a
20000-line burst that case (e) captured whole. The cost of history scraping is
re-reading: case (e) moved 3.1 MB to deliver 1.37 MB of content, a 2.3x
amplification that grows with scrollback depth, so a real implementation needs
a high-water mark rather than a full re-read each poll.

## Real smoke: Claude stream-json through the FIFO path

One `claude -p` turn, model haiku, one short prompt, launched inside
`tmux new-session -d` with stdin at `/dev/null` and stdout redirected into a
named pipe read from outside the pane:

- 8 lines arrived, all 8 parsed as JSON, 0 invalid.
- Event sequence: `system/init`, four `system/thinking_tokens`, two
  `assistant`, `result/success`.
- Zero carriage returns in the sink, confirming the FIFO bypasses the pane's
  PTY cooking even though the process runs in a pane.
- The `result` event carried `usage` (input/output/cache tokens, `service_tier`,
  `speed`, `iterations`), `session_id`, and `total_cost_usd`.
- The corresponding transcript JSONL was written under `~/.claude/projects/`
  with per-message usage, so the passive surface works for headless runs too.

The structured path works end to end through a FIFO inside tmux. No TTY, no
mangling, no partial lines.

## Recommended attachment binding, per harness

Addressed to `aae-orc-kxce`. Each binding is the observation path; none of them
requires marvel to own the PTY, so none constrains the substrate decision in
the direction the old circular rule-out assumed.

- **Claude Code: structured stream over FIFO.** `-p --output-format stream-json
  --verbose`, stdout redirected to a named pipe in the pane command. Verified
  end to end here.
- **Codex: structured stream over FIFO.** `codex exec --json`, same shape.
  Verified for flag surface and TTY independence, not yet against a live model
  turn.
- **OpenCode: structured stream, server-first.** `serve` plus `attach` is the
  richer surface (it is a supervised server with its own event stream and an
  ACP mode); `run --format json` over a FIFO is the one-shot equivalent and
  matches the other two.
- **Crush: FIFO capture of opaque text, pending a socket probe.** No structured
  output exists. Treat the stream as text, take token accounting from
  `<repo>/.crush/crush.db`, and probe the `unix:///tmp/crush-<uid>.sock` host
  surface before committing to text as the permanent binding.
- **Gemini CLI: structured stream over FIFO, gated.** `--output-format
  stream-json` on paper; do not bind until #9009 and #13561 are re-verified on
  a current build. Not installed on this host, so everything here is graph
  evidence.
- **Unknown or non-cooperative harness: `capture-pane` history scrape.** Set
  `history-limit` high, scrape `-S -` with a high-water mark rather than the
  visible region, and publish the adapter's line-rate ceiling as
  `visible_rows * poll_hz`. Expect no usable escape sequences.
- **Session marvel did not start: `pipe-pane`.** When marvel attaches to a pane
  whose command line it does not control, `pipe-pane` gives byte-faithful,
  in-order output at the cost of CR insertion. It is the right fallback for
  adopt-an-existing-session, not for sessions marvel launches.

## Bearing on code already in the tree

The recommendations above line up with what marvel has already built:
`internal/runtime/claudecode` parses Claude `stream-json`, `internal/runtime/events`
normalizes across "Claude Code stream-json, Codex --json, OpenCode SSE", and
`internal/runtime/generic.go` documents itself as the capture-pane scrape
fallback. Two measured details land on that code directly.

**`Driver.CapturePane` is the visible-region variant** (`capture-pane -t pane
-p`, `internal/tmux/driver.go`), which measured 0.48% coverage on a burst and
caps at `rows * poll_hz` lines per second when paced. `Driver.CapturePaneRange`
already provides the history form. The generic adapter should use the range
form with a high-water mark; the visible form is a display helper, not an
observation path.

**Nothing in the tree sets `history-limit`.** A grep across `internal/`,
`docs/`, and `examples/` finds no occurrence, so marvel-created sessions inherit
the tmux default. Case (f) is what that costs a history scrape: 20% coverage
where case (e) captured everything.

## Gaps

- **Shim PTY tee is unmeasured.** It cannot be tested until `aae-orc-e35c`
  lands a shim. The table has a hole there, and it is the one path that could
  plausibly beat the FIFO by carrying both structured and rendered output from
  one process.
- **Codex, Crush, and OpenCode were not exercised against live model turns.**
  Their structured flags and TTY behavior are verified; their event streams are
  not. Each needs one smoke of the shape Claude got here.
- **Gemini is graph-only** and its two known headless defects are unverified
  against a current build. Re-verify before use.
- **macOS only.** tmux pipe behavior, PTY cooking, and FIFO semantics all differ
  enough across platforms that the numbers should be re-measured on Linux
  before they are treated as the platform contract.
- **The Crush socket surface is unexplored** and is the single cheapest
  remaining lever, since it is the only target with no structured stream today.
- **Capacity rule tested on one pane geometry** (50 rows). The rule is
  dimensionally obvious but has one confirmed operating point per poll rate,
  not a curve.

## Repro

```sh
cd _kos/probes/stream-attachment-bytepath
N=20000 ./run.sh          # six-path burst comparison
DURATION=6 ./run-paced.sh # paced capacity check
```

Both scripts are self-contained, use a private tmux socket, and clean up their
server on exit. Go tooling ignores `_kos/`, and `producer.go` carries
`//go:build ignore`, so neither is part of the module build.
