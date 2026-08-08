# Probe brief: interactive CTX% remainder sweep (codex, gemini, opencode, Crush)

**Status:** OPEN (brief only; not started).
**Question:** `question-interactive-context-pressure` (the non-claude remainder)
**Probe medium:** desk research first, then code (live rig per surviving candidate)
**Timebox:** phase 1 one sitting; phase 2 one session per surviving runtime, run only as fleet priority demands
**Prior work it extends:** finding-011 (statusline side channel, solved
interactive claude), finding-008 (native OTEL, capable-but-unverified for
codex and gemini), finding-007 (headless accountant), aae-orc-dc1j (the
decision ticket), aae-orc-7hzb (whose "other harnesses statusline
equivalents" remainder this brief absorbs).

## Why this probe exists

Interactive claude CTX% shipped 2026-08-05 via the statusline side channel:
one day, one brief, because the work ran secondary-first (catalog candidate
channels from docs/source, then one empirical rig on the best candidate).
This brief applies the same shape to the four remaining runtimes instead of
pre-committing to per-runtime probe pairs. The remainder matrix at time of
writing:

| Runtime | Known channel | Actually unknown |
|---|---|---|
| codex | OTEL (capable per finding-008, not live-verified) | occupancy derivation; whether a statusline-like hook exists |
| gemini | OTEL (capable, not live-verified; no marvel adapter yet) | same, plus whether the fleet runs it at all |
| opencode | none verified | its client/server HTTP API and plugin surface are unchecked candidates |
| Crush | none | config and event hooks unchecked |

## Hypothesis

For each runtime, at least one cooperative channel (OTEL, statusline-like
hook, HTTP API, plugin) exports enough to compute raw occupancy with a
denominator, without owning the PTY and without capture-pane scraping. Where
no such channel exists, the honest answer is documented headless-only, not a
scraper.

## Phase 1 — secondary sweep (one pass, all four runtimes)

Desk research only: docs, source, changelogs, issue trackers. No daemons.
Catalog every candidate side channel per runtime:

- codex: OTEL metric/log semantics (does anything carry cumulative context
  tokens plus the window size, or only per-turn usage?); any statusline or
  notify-hook analog; `conversation_starts` as denominator source
  (finding-008 note).
- gemini: OTEL semantics, same questions; also record whether adding a
  marvel adapter is even scheduled, because a channel for a runtime marvel
  cannot launch is shelf inventory.
- opencode: the client/server HTTP API (what does the server expose about a
  session's token state?); the plugin API (can a plugin observe usage and
  call out?); any TUI statusline configurability.
- Crush: config surface, event hooks, anything resembling a status command
  or emit-on-tick facility.

Output: a channel-candidate table with one verdict per runtime, one of:
`candidate: <channel>` (carries occupancy + denominator, or enough to derive
them) or `no channel` (nothing exports the pair).

## Phase 2 — primary verification (surviving candidates only)

The finding-011 rig shape, per surviving runtime: launch the runtime
interactively under marvel, attach the candidate channel, drive turns, and
verify the exported figure against ground truth (the harness's own rendered
indicator, or a token-counted transcript). Run in fleet-priority order:
codex and opencode before gemini and Crush unless operations say otherwise.

Success signal per runtime: a live `marvel get sessions` row showing CTX%
for an interactive session of that runtime, fed by the candidate channel
through the existing heartbeat RPC (or a documented reason the channel
needs a new ingest path).

## Kill rule

A runtime whose phase-1 verdict is `no channel` gets recorded in the
question node as "interactive CTX% not meterable for <runtime>; documented
headless-only" (dc1j option (b)) and phase 2 is not run for it. The
capture-pane scraper path (dc1j option (a)) stays ruled out unless an
operator deliberately reopens it; fragility plus a normalized figure lost to
the statusline precedent.

## Non-goals

- Per-subagent context surfaces (that is the aae-orc-7hzb daemon-surface
  remainder, separate data plane).
- OTEL collector architecture (held per question-marvel-otel-architecture;
  if phase 2 verifies a codex/gemini OTEL channel, ingest design goes to
  that question, not this probe).
- Settings-precedence documentation for projected statuslines (7hzb
  remainder, docs work, not research).

---

# Phase 1 results, 2026-08-08

**Status: phase 1 run for all four runtimes.**
**These are candidates, not results. Nothing below has been through phase 2.**

Produced by a desk sweep with spot verification on kinu (macOS 26, tmux
3.7b). Read-only throughout: no harness session was started for the sweep,
no harness state was modified. Where a claim was checked against a real
artifact on this machine it is marked VERIFIED-ON-DISK; where it comes from
upstream source at a pinned commit, VERIFIED-IN-SOURCE; where only a doc
asserts it, DOCUMENTED.

A candidate verdict here means "worth building a rig for," nothing more.
The brief's phase-2 success signal (a live `marvel get sessions` row showing
CTX% for an interactive session of that runtime) has not been met for any
runtime.

## The question changed during phase 1

The brief asks each runtime what it EXPORTS. That framing generalized the
wrong half of its own precedent: interactive claude was won with an
execution hook (`statusLine` runs a command marvel supplies), not with a
data feed. So phase 1 was run asking both questions, and the second one is
the one that found the most:

> What will this runtime RUN for me?

A runtime with no export but with an execution point is a configuration
problem. A runtime with neither is the honest `no channel` case. Phase 2
and any successor brief should carry both questions, in that order.

## Candidate table

| Runtime | Verdict | Channel | Carries |
|---|---|---|---|
| codex | `candidate` | rollout JSONL on disk | occupancy level AND denominator, same record |
| opencode | `candidate` | HTTP server (port pinned at spawn), or `opencode db`, or a plugin | occupancy; denominator from a separate on-disk catalog |
| Crush | `candidate` | server SSE (TUI is a client of it) | occupancy level; denominator from a 1405-model on-disk catalog |
| gemini | `candidate`, deferred | `AfterModel` hook | occupancy; denominator is client-side only, never exported |

**Every runtime surveyed has an execution point. They differ in what the
payload carries, and that is the useful distinction.**

| Runtime | Execution point | Payload carries |
|---|---|---|
| claude | `statusLine` command | occupancy AND denominator |
| gemini | `AfterModel` hook | occupancy (`promptTokenCount`) |
| opencode | plugin (`chat.params`, `event`) | occupancy AND denominator, in process |
| Crush | `PreToolUse` hook | neither; session id only, so a trigger |
| codex | `notify`, `hooks` (10 events) | neither; a pointer to the file |

So the interactive-claude win was not a Claude Code accident, and the
brief's original export-first question would have missed gemini and
opencode entirely. It also reframes the two that carry nothing: a trigger
plus a separate read is still a cooperative feed, assembled from two
channels rather than one, and both Crush and codex hand over the session
identity needed to join them.

Where a hook exists but carries no tokens, the cheapest move may be
upstream rather than architectural. Crush's own docs invite requests for
further hook events and it already parses Claude Code's hook-output
envelope, so a usage-carrying turn-end hook is a small, well-formed ask
rather than a feature marvel has to work around.

### codex

The strongest candidate found, and the surprise of the sweep: it is a local
file read that carries its own denominator, which the sweep's working
assumption said local reads never do.

- `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`, record
  `event_msg` / `token_count`, carries `last_token_usage` (six token
  classes), `total_token_usage`, and `model_context_window` in one record.
  VERIFIED-ON-DISK.
- **It is a level, not a delta.** In one 414-record interactive session,
  `last_token_usage.input_tokens` drops to zero at record indexes 49, 242,
  and 378 (three auto-compactions) against a peak of 240,736. A running sum
  cannot decrease. VERIFIED-ON-DISK, independently reproduced.
- Denominator present on 413 of 414 records, constant at 258,400. The one
  exception is the first record (~4s in), where `info` is null entirely.
  VERIFIED-ON-DISK.
- Cadence is per model API request, median gap 9.7s, not per user turn.
  Written live during an interactive TUI session. VERIFIED-ON-DISK.
- `state_5.sqlite` `threads.tokens_used` equals `total_token_usage.total_tokens`
  exactly, so that column is spend and NOT occupancy. VERIFIED-ON-DISK.
- Trap: `session_meta.payload.context_window` is a UUID (`window_id`), not a
  size. VERIFIED-ON-DISK.
- No execution point carrying tokens: `notify` fires only on
  `agent-turn-complete`, and `hooks` has ten events; neither payload carries
  tokens or a window. Both DO carry a pointer (`thread-id`,
  `agent_transcript_path`), so the projection a supervisor writes would say
  which file to read rather than reporting the number. VERIFIED-ON-DISK.
- `tui.status_line` is NOT an analog of Claude Code's `statusLine`. It is an
  ordered list of built-in footer item identifiers and executes nothing.
  DOCUMENTED.
- OTEL exists (`codex.conversation_starts` carries the window;
  `codex.turn.token_usage` carries classes) but metrics carry no session
  identifier, so per-session attribution from metrics alone is not
  available. VERIFIED-IN-SOURCE.

**Open before this can be built,** and phase 2 exists to settle them:
pane-to-file binding (the sweep used `lsof` on the pane pid, which worked
but is untested under marvel's own spawn); the constant offset between
`last_token_usage / model_context_window` and the footer's rendered
"% context left" (`core/src/session/context_window.rs`, unread); behavior
under `codex exec --ephemeral` and a relocated `CODEX_HOME`, either of
which removes the channel entirely; and whether the cadence holds under
sustained fleet load rather than one operator's session.

### opencode

Two shapes, and the sweep did not settle which marvel should carry.

- **Contracted, and cheap.** `--port` is a global flag on the DEFAULT TUI
  command, not only on `serve`. So marvel could pin a port at spawn and the
  HTTP surface becomes available for an interactive pane.
  `internal/runtime/opencode.go:51-55` passes `Runtime.Args` verbatim and
  nothing else; the file's own comment already anticipates a later adapter
  for this. VERIFIED-ON-DISK (help text at 1.18.11 and 1.18.14).
- `opencode serve` starts a headless server; `/api/model`, `/doc`, and
  `/global/event` all answer 200 at 1.18.14. `/api/model` returned zero
  models against a bare server with no provider context, so the
  `limit.context` payload is NOT yet confirmed live. VERIFIED-ON-DISK for
  reachability; UNVERIFIED for content.
- Server startup warns `OPENCODE_SERVER_PASSWORD is not set; server is
  unsecured`, so an auth posture has to be chosen before marvel pins a port.
  VERIFIED-ON-DISK.
- `GET /api/session/{id}/context` is documented in upstream source as "all
  messages after the last compaction," which would be a compaction-correct
  numerator. VERIFIED-IN-SOURCE at pinned SHA; UNVERIFIED at any installed
  version.
- **Uncooperative alternative:** `opencode db "<sql>" --format json` is a
  shipped subcommand, so the sqlite path needs no sqlite driver in marvel.
  `message.data` JSON carries the token classes; `session` carries
  cumulative per-session columns. VERIFIED-ON-DISK.
- **Denominator is a file:** `~/.cache/opencode/models.json` (3.6 MB,
  refreshed by opencode itself) carries `limit.context` per model.
  VERIFIED-ON-DISK.
- **Execution point exists:** the plugin API's `PluginInput` hands a plugin
  `serverUrl` and a client, and the `chat.params` hook receives the `Model`
  object carrying `limit.context`, while `event` receives the whole event
  feed including token payloads. Numerator and denominator, in process.
  VERIFIED-ON-DISK (the plugin package is installed on this host).
- Dead ends: no OTEL (removed upstream in sst/opencode#1738), no `/metrics`
  route, `opencode mcp` is client-role only, `opencode stats` is cumulative
  spend with no occupancy or window, and the default-level log file carries
  token keys only on session-created records with zero values.
- The `opencode api` subcommand and the `server.json` daemon-discovery file
  are dev-branch: absent at 1.18.11 and at 1.18.14. VERIFIED-ON-DISK.
  A sweep claim built on them was corrected by installing the upgrade.

**Open before this can be built:** whether an installed version serves the
v2 `/api/*` paths at all; what the SSE frames look like on the wire; the
auth posture for a marvel-pinned port; and whether `session_context_epoch`
means what its name suggests (0 rows on this host, so unexercised).

### Crush

The sweep's biggest reversal. Three non-specialist readers examined the
sqlite artifact and returned a provisional `no channel`; the platform
catalog overturned it on every point, and the reversal is verified.

- **`crush server` ships** in v0.88.0, and `-H --host` is a flag on the
  ROOT (TUI) command with the same unix-socket default. **The interactive
  TUI is itself a client of that server**, so an interactive session is
  observable over the same API as a headless one, with no pane scraping.
  VERIFIED-ON-DISK.
- **`sessions.prompt_tokens` is a LEVEL, not a sum.** Live rows on this
  machine: 35,267 at 2 messages, 35,290 at 4, 35,745 at 12. Flat under 6x
  message growth. Both rows carrying a `summary_message_id` read 0, which
  is the compaction reset visible in the data. VERIFIED-ON-DISK,
  independently reproduced.
- Source agrees: `updateSessionTokenCounters` assigns rather than adds
  (`internal/agent/agent.go:1941-1948`), written once per API request.
  VERIFIED-IN-SOURCE.
- **The trap that produced the wrong provisional verdict.**
  `internal/db/sql/sessions.sql` also contains `UpdateSessionTitleAndUsage`
  with `SET prompt_tokens = prompt_tokens + ?`, which has no caller outside
  the generated db layer. Reading the schema or the `.sql` file alone yields
  accumulate semantics, which is wrong. VERIFIED-IN-SOURCE.
- **Denominator is a complete catalog on disk.**
  `~/.local/share/crush/providers.json`: 40 providers, 1405 models, and
  **zero** with a missing or zero `context_window`. VERIFIED-ON-DISK.
- Composition differs from marvel's: Crush computes `input + cache_read`
  and EXCLUDES cache-creation tokens, where marvel's additive layout is
  `input + cache_read + cache_creation`. A Crush layout must encode that or
  CTX% reads low right after a cache write. VERIFIED-IN-SOURCE.
- **The estimated-versus-measured flag is persisted nowhere.** When a
  provider returns zero usage, Crush estimates input tokens at roughly four
  characters per token and sets an `EstimatedUsage` flag that exists only in
  memory. It is not a `sessions` column and not an API field. The TUI shows
  a tilde; the database and the API do not. No channel distinguishes a
  measured level from a character-count guess, which bears directly on
  `internal/usage/doc.go`'s stance on false precision.
- Execution point exists but does not carry tokens: `hooks` implements only
  `PreToolUse`, whose payload has no usage field. It works as a TRIGGER (it
  exports `CRUSH_SESSION_ID`) into one of the channels above, not as a feed.
  Crush parses Claude Code's `hookSpecificOutput` envelope and its docs
  explicitly invite requests for further hook events, which makes a
  usage-carrying turn-end hook the cheapest upstream ask in this survey.
  VERIFIED-IN-SOURCE.
- `projects.json` maps a workspace path to the `.crush` directory holding
  its database, so marvel never has to crawl for it. VERIFIED-ON-DISK.
- Dead ends: no OTEL, no `/metrics`, no MCP server role. PostHog analytics
  exist and are Charm's stream, not marvel-addressable.

**The prior blocker was misread, and the correction matters beyond Crush.**
`charmbracelet/crush#1765` was treated in this graph as evidence that Crush
architecturally refused a serve mode. It was created at 15:35:37Z and closed
at 15:38:39Z **by its own author**, who said they would repost as a
discussion per contributing guidelines. It was never a maintainer design
ruling. VERIFIED via issue state. The client/server split was already
shipping in v0.70.0 (2026-05-18) while the prior survey pinned 0.67.0
(2026-05-11): a one-week version gap produced a three-month-stale verdict.

**Open before this can be built:** no crush server was started for this
sweep, so the SSE frame encoding, the config endpoint's JSON shape, and live
socket behavior are source-read only. The server also idle-shuts-down after
60 seconds by default (`CRUSH_SERVER_IDLE_TIMEOUT`), so a poller must
tolerate the socket vanishing or marvel must set that env var at spawn. The
socket path is macOS-specific per B13. Locally-discovered providers (ollama,
lmstudio) get their window at runtime and it is written nowhere, so those
sessions need the config endpoint or an operator-set `Runtime.context_window`,
and this host's daily driver is exactly such a model.

### gemini

`candidate`, and the recommendation is to record it and defer it.

- **`AfterModel` hook is the candidate.** Gemini CLI runs an
  operator-supplied command and pipes it JSON on stdin;
  `llm_response.usageMetadata` carries `promptTokenCount`,
  `candidatesTokenCount`, `totalTokenCount`. Fires on every model response,
  TUI or headless. Structurally the same shape as the claude win.
  VERIFIED-IN-SOURCE (`packages/core/src/hooks/hookTranslator.ts:379-412`).
- **Per-session projection lever exists.**
  `GEMINI_CLI_SYSTEM_SETTINGS_PATH` is returned verbatim as the system
  settings path, so marvel could project a hook config per process without
  touching the user's own `~/.gemini/settings.json`. That is the isolation
  property Tobias's proposed adapter rule asks for.
  VERIFIED-IN-SOURCE (`packages/cli/src/config/settings.ts:105-106`).
- Hook roster: eleven events, only `AfterModel` carries tokens.
  `PreCompress` marks the compaction crossing without quantifying it. Every
  event carries `session_id` and `transcript_path`.
- **The denominator is never exported.** `tokenLimit(model)` is computed
  client-side (`DEFAULT_TOKEN_LIMIT = 1_048_576`, Gemma variants 256,000),
  and the TUI's context display is nine lines dividing `promptTokenCount`
  by it. marvel would supply the window from its own table, which
  `Runtime.context_window` already accommodates. VERIFIED-IN-SOURCE.
- **OTEL is the weaker channel, and this settles an open graph question.**
  The emitting code carries token counts only: `gemini_cli.token.usage`,
  `gemini_cli.session.count`, `gemini_cli.chat_compression`,
  `gemini_cli.token.efficiency`, plus a GenAI-convention histogram. Nothing
  named for context window or occupancy, and no `conversation_starts`
  analog carrying a limit the way codex has. VERIFIED-IN-SOURCE
  (`packages/core/src/telemetry/metrics.ts`). The `session.id` common
  attribute is DOCUMENTED only; the code attaching it was not found, and
  per-session correlation is the whole point for marvel.
- `gemini_cli.chat_compression` (attrs `tokens_before`, `tokens_after`) is
  the one thing OTEL adds: it quantifies the compaction step both
  finding-007 and finding-008 flag as unmeasured.
- Upstream states marvel's own rule in their words:
  `uiTelemetry.ts:248-250` reads "the total tokens of the last Gemini
  message represents the context size at that point in time." They also
  carry a mild internal inconsistency (restore path uses `tokens.total`,
  live TUI path uses `promptTokenCount`).
- Headless `--output-format json` and `stream-json` both emit cumulative
  sums at completion with no window, so they are the wrong shape.
- Dead ends: no statusline concept, no Prometheus scrape, no server mode
  exposing session state, extensions do not get hooks (#14449 closed).

**Both blockers this graph held gemini behind are stale, one by ten
months.** `gemini-cli#9009` (no JSON output) closed COMPLETED 2025-09-26;
`#13561` (non-interactive asking questions) closed as DUPLICATE 2026-04-09.
VERIFIED via issue state. The 2026-07 note carrying them forward as live
gates should be retired.

**Why defer anyway.** marvel has no gemini adapter (`aae-orc-6c2r`), and
this brief's own rule is that a channel for a runtime marvel cannot launch
is shelf inventory. A phase-2 rig would have to build the adapter first.
Deferring is cheap rather than lossy here: the expensive part is done and
durable (channel named, payload fields source-verified, projection lever
identified), so whoever schedules the adapter starts at implementation.

**One framing correction for the graph:** gemini should stop being
described as the OTEL-capable-but-unverified runtime. Its OTEL is the
weaker of its two channels and carries no denominator. Left under the old
framing, the next reader re-derives all of this.

## What phase 1 also turned up, and where it went

The sweep read enough of marvel's own code to raise eight observations about
shipped behavior. Those are filed as `ArcavenAE/marvel#141` through `#148`
with linked beads, each written as a request to validate rather than a
verdict. Two of them bear directly on this probe: `#141` (ctx-forward
discards the classes, the window, and `transcript_path`) and `#142` (the
numerator carries no provenance, so two producers print identically).

## Revised phase 2

Unchanged in shape, revised in content. Per surviving candidate, the rig
now has named unknowns rather than a general "verify against ground truth":

1. **codex.** Bind a marvel-spawned interactive pane to its rollout file and
   hold the binding across a turn; resolve the offset against the rendered
   footer figure; confirm behavior when the file is absent by configuration.
2. **opencode.** Pin `--port` at spawn on the default TUI command, then
   confirm the HTTP surface answers with real content for that session, and
   settle the auth posture. Compare against the `opencode db` path for the
   same session to see whether the server adds anything the file does not.
3. **Crush.** Do not run phase 2 unless the platform catalog overturns the
   provisional verdict.
4. **gemini.** Run phase 1 first.

Ordering note: both surviving candidates are LOCAL to the host running the
harness. Neither survives multi-host scheduling (roadmap M5) without the
daemon on that host doing the reading. That does not disqualify either, but
it does mean phase 2 should record what it is building as host-local by
construction.

## Two cross-cutting results the per-runtime sections do not carry

### The callout tiers, and why no runtime is a dead end

The execution-point table above is better stated as three tiers that
compose upward:

- **FEED** — the invocation carries the numbers (claude `statusLine`,
  gemini `AfterModel`, opencode plugin).
- **POINTER** — the invocation carries a handle to where the numbers live
  (every claude hook carries `transcript_path`; codex `notify` and hooks
  carry the rollout path).
- **CLOCK** — the invocation carries only identity and timing (Crush
  `PreToolUse`).

A clock plus a derivable path is a pointer; a pointer plus a file carrying
usage is a feed. So the right question for a fifth harness is not "does any
callout carry tokens" (usually no) but "does any callout carry an
identifier I can turn into a path, and does that path hold usage." On that
test no runtime in the roster is unsolved, only differently priced.

Two secondary observations worth keeping: an in-process extension (a
plugin) can read live state and therefore usually gets the denominator,
where an out-of-process command can only read what the harness chose to
serialize; and chrome-rendering callouts are the richest out-of-process
feeds, because a harness that must DRAW a context meter has to serialize
both terms to draw it. That gives a concrete search list for any new
harness: statusline, prompt, window title, notification command, theme
command.

### Following these channels correctly, measured on kinu

The sweep's channels are files and databases, and the cost of reading them
was measured rather than assumed:

- **`PRAGMA data_version` costs 1.72 microseconds** on a persistent
  read-only connection, roughly 140x cheaper than the occupancy query it
  gates. It is per-connection: open-poll-close makes it a silent constant.
- **`stat` on a sqlite file is wrong.** `logs_2.sqlite` held its mtime for
  ten minutes while its WAL grew 218 KB to 750 KB. A poller on the db file
  concludes idle while three quarters of a megabyte lands. WAL size is also
  unreliable, since sqlite resets to offset zero after checkpoint rather
  than truncating.
- **A read-only sqlite connection is not passive.** It opens `-wal` and
  `-shm` read-write, because WAL readers write read-marks. The achievable
  goal is a lock that cannot block the writer, not the absence of one, and
  the rule that follows is to keep the connection alive but never wrap a
  poll in a transaction, since a pinned snapshot grows the writer's WAL
  without bound.
- **`mode=ro` while the harness runs, `immutable=1` only when it does not.**
  `immutable=1` against a live writer skips the WAL and returns stale data
  with no error, which for a freshness metric is the worst available
  failure.
- **`CGO_ENABLED=0` is set at `.goreleaser.yml:15` and `ci.yml:102`**, which
  rules out `mattn/go-sqlite3` entirely. The pure-Go driver is an ADR, not
  an implementation detail, and shelling to `sqlite3` costs ~8 ms per
  invocation and discards the persistent connection change detection needs.
- **Per-pid `lsof` costs 49 ms**, so binding fifty panes that way is 2.45
  seconds per reconcile tick. Worse, **it cannot find the claude transcript
  at all**: the live harness holds 54 fds, none of them the JSONL, because
  it opens, appends, and closes. So `lsof` is serving as a cwd oracle
  rather than a file finder, and the codex binding that worked in this
  sweep does not generalize.
- **Read the tail, never the head.** The thing that actually stops scaling
  is reading a growing JSONL from offset zero every tick. A fixed 64 KB
  tail window with `(dev, ino)` tracking is the difference between hundreds
  of sessions and about six. Never parse a line lacking its terminating
  newline.
- **Linux inotify is the tighter side**: watches count against
  `max_user_watches` and instances against `max_user_instances`, commonly
  128, so one shared watcher with many paths rather than one per session.
  macOS kqueue is four orders of magnitude from its ceiling here.

### The cwd gap decides which channels are even addressable

`internal/api/types.go:84` defines `Workspace` as `Name string` and
`CreatedAt time.Time`. marvel's documented isolation boundary touches the
filesystem nowhere, and the adapters pass the workspace name as a label.

That splits the file channels cleanly:

- **codex is addressable today.** Fixed home (`~/.codex/sessions/**`), plus
  `state_5.sqlite` mapping cwd to rollout path, plus `cwd` in-band in
  `session_meta`. A second independent reason it leads.
- **claude and Crush are not**, until marvel holds a path: their locators
  are functions of cwd (a mangled-cwd directory, and a per-project
  `.crush/`).
- **opencode is in between**: one fixed-home database for every session, so
  addressable without cwd, but `session.directory` is needed to attribute a
  row to a marvel session.

The point is that marvel CHOSE the working directory at spawn and did not
keep it. Discovering it afterward with a 49 ms probe is paying to recover
data the daemon already had. That is a design gap worth filing rather than
a lookup problem worth optimizing.

### One untried claude door worth its own line

`totalTokensReminder: "countdown"` is a settings key that emits remaining
tokens into the conversation itself, reconciled exactly against the raw
occupancy level (200000 minus a 33622 prompt level minus 113 output equals
the emitted 166265). It rides the projected-settings mechanism marvel
already owns, needs no forwarder process, and works identically headless
and interactive. It is marked internal in the binary, so it is revocable
and would need a version pin, and it costs context and is read by the model,
which is an unmeasured behavioral effect. Recorded as a candidate, not a
recommendation.

### And one clean negative

There is no `isSidechain: true` assistant line anywhere in the local
transcript corpus, so transcripts do NOT carry subagent context.
finding-011 stands unamended: `subagentStatusLine` is the only channel
breaking out per-subagent token counts and window sizes.

## Phase 1, round 3: the framework was wrong in a way worth keeping

Round 3 re-ran the per-runtime verdicts as verification rather than survey.
Three results change the shape of the catalog, and one of them corrects the
tiering this brief introduced two sections above.

### Tier is a property of the CHANNEL, not of the harness

The FEED/POINTER/CLOCK tiering was written as though a harness has a tier.
It does not. A harness has a channel INVENTORY, and each channel is tiered
separately.

The decisive counterexample is gemini's `PreCompress` hook. It fires at the
single most informative instant in a session, the moment context pressure
peaks, and its payload is `trigger: "auto" | "manual"` plus base fields. A
CLOCK at exactly the instant you most want a feed. The numbers for that same
event exist and are precise, on a different channel:
`gemini_cli.chat_compression{tokens_before, tokens_after}` on the OTEL path.
One event, two channels, two tiers.

gemini alone spans all three tiers at once: CLOCK at `PreCompress`, POINTER
via `transcript_path` and a derivable project-hash directory, FEED via OTEL.
Cataloguing it as "a FEED harness" because `AfterModel` exists would have
selected the worst of its three channels. The catalog is therefore per
channel, and a runtime's row is a list.

**A second refinement: split FEED by how many terms it carries.**

- **FEED-2** carries occupancy AND window. claude `statusLine`, Copilot CLI
  `statusLine`, Qwen Code `statusLine`.
- **FEED-N** carries occupancy only. gemini `AfterModel`, gemini OTEL
  outfile, gemini local chat files, aider `--analytics-log`.

The distinction is operational, not cosmetic. A FEED-N harness forces marvel
to own a model-to-limit table, and that table goes wrong precisely when the
harness switches models mid-session. gemini has a router that does exactly
that, announced only by a separate `model_routing` event carrying
`decision_model`. A denominator marvel infers from a model name is stale the
moment the router moves.

### "Must draw it, must serialize it" survives, with its mechanism corrected

The claim was that a harness which must DRAW a context meter has to
serialize both terms. Three independent harnesses delegate footer rendering
to an external command, and all three serialize `context_window` with both
terms, with field names converged on Claude Code's schema
(`context_window.context_window_size`, `used_percentage` appear near-verbatim
in all three). A scraper written once ports across them with field aliasing
only.

The negative case sharpens it. Cursor's TUI draws a context meter too and
serialized nothing for months, because it draws IN-PROCESS. So the mechanism
is **delegation, not display**. A harness that renders its own chrome is
under no pressure to serialize anything.

That prediction is falsifiable on the next harness, which is what makes it
worth keeping. Crush is a Charm project and Charm builds TUIs in-process, so
the framework predicts Crush delegates nothing and therefore serializes
nothing to a callout.

### Spawn-time pinning: marvel is entitled to assign identity, not discover it

The binding problem (mapping a tmux pane marvel spawned to the file or
session carrying its usage) was being treated as a discovery problem, priced
at a 49 ms per-pid `lsof` probe that cannot even find the claude transcript.
It is not a discovery problem on most of the roster. marvel constructs the
process environment at spawn (enforcement locus 1, shipped), so anything
settable there is free.

| Runtime | Pin available at spawn | Measured |
|---|---|---|
| claude | `--session-id <uuid>`: marvel ASSIGNS the session id | yes, 2.1.226 help |
| gemini | `GEMINI_TELEMETRY_TARGET=local` + `GEMINI_TELEMETRY_OUTFILE=<path>`: marvel names the usage file. `GEMINI_CLI_HOME` pins the config root, which pins where `tmp/<projectHash>/chats/` lands | docs |
| aider | `--analytics-log <path>`: exact, explicit, per-invocation | docs |
| codex | no id pin (`resume` takes an existing one). `OTEL_RESOURCE_ATTRIBUTES` honored, so stamp identity instead | binary strings |
| Copilot CLI | not needed: `session_id` and `transcript_path` ride the statusLine payload marvel itself installs | docs |

`OTEL_RESOURCE_ATTRIBUTES` is honored by both claude and codex, and claude
promotes resource attributes to datapoint labels by default. So marvel can
stamp its own pane identity into exported data at spawn on both. That is
free, it makes any later OTEL work attributable, and it should be done
whether or not OTEL is ever consumed.

### OTEL: a dead end for pressure, possibly the cheapest actuator signal

Measured across the installed binaries:

- Every OTEL token instrument that exists is a monotonic **Counter** of
  cumulative tokens. `or.tokenCounter=t("claude_code.token.usage",
  {description:"Number of tokens used",unit:n("tokens")})` is verbatim from
  the 2.1.226 binary. Folding an OTLP counter into occupancy reproduces
  finding-007's defect exactly, and it grows with request count, so the
  longest sessions read worst.
- Only codex publishes the denominator, and it publishes it on a logs event
  rather than a metric: `context_window` and `auto_compact_token_limit` sit
  adjacent to `provider_name` and `reasoning_summary` in the codex 0.146.0
  binary. `model_context_window`, `model_auto_compact_token_limit`, and
  `effective_context_window_percent` are also present. codex computes the
  number marvel wants and names it that.
- Crush cannot export at all: it links the otel API plus `metric/noop` and
  `trace/noop` transitively, and carries no SDK or OTLP exporter.
- opencode bundles `@opentelemetry/api` and `sdk-trace` only, with no
  exporter and no metrics SDK, so its `gen_ai.usage.*` strings are Vercel AI
  SDK span attributes that stay inert until a plugin registers a provider.

So OTEL is ruled out as the pressure foundation. But the metrics channel is
not the only channel, and the ruling may not carry to the actuator:
`claude_code.compaction` is a **span**, not a metric, carrying
`attrs:{trigger: "auto"|"manual"}` (verbatim from the binary). gemini emits
`gemini_cli.chat_compression` as both a counter and a log event with
`tokens_before` and `tokens_after`. If the minimum viable shift signal is a
compaction EVENT rather than an occupancy level, that event is cheaply
available over tracing on at least two harnesses and needs no denominator.
That question belongs to `bound-context-instead-of-measuring-it.md`.

Two listener-free receive paths exist and are worth more than a collector:
claude's Prometheus **pull** exporter (`OTEL_EXPORTER_PROMETHEUS_HOST`/`_PORT`,
both present in the binary), where marvel allocates the port per agent and
scrapes on its own cadence, and gemini's genuine file exporter. Both make
attribution a function of a path or port marvel itself assigned, which is
the same move as the session-id pin.

### Two more local corpora, both verified on this machine

The compaction-mining probe was written against the claude transcript
corpus: 77 labelled `compact_boundary` records, all 77 carrying
`compactMetadata`, across two generations differing by one optional field.
An earlier count of "112 matching lines, 34 without metadata" was a grep
artifact and is retracted in that brief, along with the reason it matters:
the 57 non-records were this review's own prose about compaction, landing in
the directory it was mining. Two more corpora exist here:

- **gemini**: 26 files under `~/.gemini/tmp/*/chats/`, **477 messages
  carrying a per-turn token series** shaped
  `{input, output, cached, thoughts, tool, total}` with the model name
  alongside. Richer than the documented `AfterModel` hook payload, which
  promises only `totalTokenCount`. The directory name is a 64-hex hash and
  the file field is literally `projectHash`, so the path is derivable from
  the workspace root.
- **codex**: roughly 209 session files under `~/.codex/sessions/`.

Three independent local corpora is enough that the measurement program's
first phase should be mining rather than running sessions.

### Roster additions worth a row

- **GitHub Copilot CLI**, not previously on the list and the strongest find.
  Its `statusLine` payload is FEED-2 and POINTER at once: `session_id`,
  `transcript_path`, `model.{id,display_name}`, and a `context_window` object
  carrying `current_context_tokens`, `context_window_size`,
  `displayed_context_limit`, `used_percentage`, `last_call_input_tokens`, and
  cumulative totals. Caveat to verify: it was behind a `STATUS_LINE` feature
  flag as of 2026-05.
- **Qwen Code**, a gemini-cli fork that added the statusline gemini lacks,
  with `context_window.{context_window_size, used_percentage,
  remaining_percentage, current_usage}`. Configured under `ui.statusLine`,
  not at settings root, which is a documented footgun. Inheriting gemini's
  hook and OTEL surfaces would make it the best-instrumented harness in the
  catalog.
- **goose**, whose sessions moved to a sqlite `sessions.db` at v1.10.0
  storing token usage and working directory. A queryable database beats a
  log tail. Denominator unverified.

Dismissed on the spawnability test: roo-code and zed's agent are
editor-hosted with no terminal CLI. Deferred pending one check each: cline,
kilo, continue, amp (whose `--stream-json` usage field is the highest-value
unverified claim in the round), cursor-agent (capable via stream-json since
2026-02 but its hook and statusline story is unsettled).

### Counterexample worth keeping: hook richness does not predict tier

cursor-agent has a Claude-Code-compatible hook system with parallel
execution and optimized latency, and its hooks still carry no token usage
while its `stream-json` output gained usage in February. Richness of a hook
system predicts nothing about whether the channel carries what you need. The
search list is right to enumerate channels rather than rank harnesses.

### Crush moves from candidate to measured, and leads on semantics

Phase 1 called Crush a candidate via its server. Round 3 measured it end to
end against v0.88.0 (build_id `dkd26so3zabk`, darwin/arm64), with source read
at the matching tag. The channel is real, and the reason it matters is not
the transport.

**Crush computes context pressure natively and acts on it.** The same
expression appears in two places: `internal/ui/model/header.go:149` renders
`(CompletionTokens + PromptTokens) / model.ContextWindow * 100`, and
`internal/agent/agent.go:1044` uses that identical numerator as the
StopCondition for auto-summarize. The semantics marvel wants are the
semantics Crush already ships and already actuates on. That is a stronger
position than any harness where marvel reconstructs the ratio.

Channel inventory, tiered per channel:

| Channel | Tier | Carries |
|---|---|---|
| Server SSE session events | FEED-N | `prompt_tokens`, `completion_tokens`, `cost`, `message_count`, `summary_message_id`, pushed at every turn boundary |
| Server REST `/v1/workspaces/{id}/agent` | denominator, one-shot | `model.context_window` (measured 40960 for qwen3:0.6b). Static per model, so fetch on model change, not per turn |
| Local sqlite `sessions` table | FEED-N, poll only | identical numbers (read back 28639 / 414 matching the last SSE frame exactly) |
| `PreToolUse` hook | CLOCK | `event`, `session_id`, `cwd`, `tool_name`, `tool_input`. No tokens |
| Chrome callouts | ABSENT | nothing |
| Headless output | none | `crush run` has no `--json` or `--output-format` |
| OTEL | ABSENT for export | API plus noop only, 0 SDK symbols, 0 OTLP exporter |

The denominator does NOT ride the SSE stream. It is a separate call on the
same socket, which splits Crush's feed into FEED-N plus a one-shot lookup
rather than a FEED-2.

**Occupancy is a LEVEL, proven by measurement rather than by field name.**
Three consecutive turns in one session: `prompt_tokens` 28593, 28611, 28639.
Cumulative would have put turn 2 near 57186. Source corroborates
(`agent.go:1942` assigns `session.PromptTokens = usage.InputTokens +
usage.CacheReadTokens`, with `session.Cost += cost` on the line above as the
deliberate contrast), but the series settles it independently.

**The compaction edge is clean and observable.** After
`POST /v1/workspaces/{id}/agent/sessions/{sid}/summarize`, `prompt_tokens`
went 28639 to 0 and `summary_message_id` became non-empty
(`agent.go:1460` sets it to zero post-summary). Phase 1 had inferred this
from "`prompt_tokens = 0` on sessions carrying a summary message"; it is now
measured.

**Nadia's delegation prediction was tested here and held.** Zero hits for
statusline, status_line, or status-line across all Go and JSON files. No
delegated footer, no external chrome command, therefore nothing serialized
and no `context_window.*` field names to alias. The only `exec.Command` under
`internal/ui` is the TUI's run-a-shell-command feature. Charm draws
in-process exactly as predicted. The prediction survived an attempt to
refute it, which is worth more than the confirmations.

**The hook is the framework's other lesson, restated.** Crush has exactly one
hook event, `PreToolUse`. No PreCompact, no Stop, no SessionEnd. It fires on
tool use rather than turn boundary, which is the wrong edge for pressure, and
a consumer holding its `session_id` would have to go hit the server API for
numbers, at which point the hook has added nothing the SSE stream did not
already give with better timing.

**One trap to flag before anyone cites it.** `internal/agent/event.go`
`eventTokensUsed` emits input, output, cache-read, cache-creation, total
tokens, and cost. It looks exactly like the channel you want. It is PostHog
analytics egress to `data.charm.land` (`internal/event/event.go`) and never
touches the local SSE stream.

Discovery is cheap: the default host is
`unix://$TMPDIR/crush-<uid>.sock`, printed verbatim as the `-H/--host` default
in `crush --help`, and clients auto-spawn a detached server when none is
listening, so the socket exists whenever anyone runs crush at all. One server
serves many workspaces, so a single socket sees every Crush agent on the
host. Access control is socket file permissions; the SSE `client_id` need only
parse as a UUID.

Unestablished, and the first two are the ones that would change the read:

- Measured only against the ollama provider. Source says `PromptTokens =
  InputTokens + CacheReadTokens`, so prompt-cached Anthropic or Bedrock
  traffic should still yield a correct level, unconfirmed.
- `completion_tokens` level-versus-cumulative is not proven by measurement;
  the 83/166/414 series is consistent with both. `prompt_tokens` is the
  unambiguous term and dominates the numerator.
- Workspace lifetime. A workspace vanished before use when no client held a
  stream; the reap timeout was not characterized.

### Observer perturbation, which the consent axis did not anticipate

The consent idea file argued that a read-only sqlite connection is not
passive (WAL readers write read-marks). Crush produces a sharper instance on
the CONTRACTED side of the axis, where the argument was supposed not to
apply: `AttachClient` bumps a stream count, `attached_clients` is exposed on
the session, and `proto.go` carries a comment that observers use stream count
to detect live subscribers. **A marvel observer registers as a real client,
not a passive tap**, and an unobserved workspace got reaped during the probe.
So keeping the reading alive may require holding a stream open, which changes
the harness's own view of whether anyone is watching.

That is not a consent problem. It is an observation-changes-the-system
problem, and it lands on the contracted channel rather than the appropriated
one, which means the consent axis does not predict it. Worth its own line in
the arbitration design: a channel can be fully contracted and still not free.

The round produced a second, smaller instance from the probe's own hygiene
disclosure: starting a Crush server refreshed the shared global caches
`~/.local/share/crush/providers.json` and `hyper.json`, and `--data-dir`
scopes the project DB rather than the global config dir. Merely instantiating
the contracted channel mutated host-global state outside the probe's
sandbox.

### The codex denominator: two terms in the catalog, ONE on the wire

An earlier read in this brief treated `effective_context_window_percent` as
codex computing the ratio marvel wants. That was an over-read. It is a field
of `struct ModelInfo with 38 elem` (verbatim), sitting alongside
`context_window`, `max_context_window`, `auto_compact_token_limit`, and
`comp_hash`. Static model config: the fraction of the window codex will
actually use.

**A first attempt at correcting that over-read went wrong in the opposite
direction, and this paragraph is the fix.** The draft said marvel's real
denominator is `context_window` times `effective_context_window_percent` and
that both terms must travel together. They have already travelled: 272000 x
0.95 = 258400, and 258400 is exactly what the rollout emits as
`model_context_window` in 206 of 209 files with no other value corpus-wide.
The number on the wire is already effective. A marvel that multiplies again
lands on 245,480 and runs 5 percent PESSIMISTIC, firing shifts early.

Adapter rule: read `model_context_window` from the same `token_count` record
as the level. Never read `models_cache.json`, and never apply the percentage
yourself.

**Correction to the record types, too.** A draft line here said `session_meta`
and `context_compaction` are both in codex's `rollout_item_type` enum. That
conflated two adjacent enums in the same string region. `context_compaction`
belongs to the telemetry item-kind classifier, beside
`response.compaction_trigger` and `dropped`; it never appears as an on-disk
`.type` value. The census over 209 files gives six type values and it is not
one of them. See the codex section below for the full list.

Both errors are the same shape and worth naming as a method note: a
correction appended in a later section left the wrong statement standing in
an earlier one. In a document this long, fix the original rather than
appending the fix.

### The actuator over OTEL, narrowed

Round 4 re-ran the OTEL verdict against the tracing and logs channels rather
than metrics. The actuator event exists on three of five harnesses; the
listener-free half holds on exactly one.

| Runtime | Actuator event over OTEL | Trigger attr | Tokens on it | Reachable without a listener |
|---|---|---|---|---|
| claude | `claude_code.compaction` span | `trigger: auto\|manual`, `message_count` | no | no |
| codex | `codex.task.compact`, plus `codex.compaction.model_fallback` | fallback carries a four-value reason enum (`user_requested`, `context_limit`, `model_downshift`, `comp_hash_changed`) | yes, same span as the seven turn token levels | no |
| gemini | `gemini_cli.chat_compression`, log event and metric | none documented | yes, `tokens_before` / `tokens_after` | **yes** |
| opencode | plugin only | no | plugin counters | no |
| Crush | none | no | no | n/a |

Two things about the claude span before anything is built on it. It is gated
behind `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA`, which is beta and withdrawable.
And the numbers you would want on a compaction do exist in-process
(`preCompactTokenCount`, `postCompactTokenCount`, `truePostCompactTokenCount`,
`autoCompactThreshold`, `willRetriggerNextTurn`) but they are arguments to
the vendor's internal analytics sink, not to the OTEL span, which gets
trigger and message_count and nothing else.

Claude Code's only file-writing telemetry path is `OTEL_LOG_RAW_API_BODIES`,
and it is not narrowable: three modes, no event filter, and it writes whole
Messages API request and response bodies. A volume and privacy problem rather
than a metrics path.

**gemini's `outfile` is the one genuinely new capability OTEL offers.** It
writes logs and metrics to a file, overrides the OTLP endpoint, and is
recommended by the project's own docs for local use. A per-agent path
assigned at spawn gives marvel the compaction event with both token counts,
read the way the process-table sampler reads anything else: no listener, no
receiver dependency, attribution by a path marvel itself chose. It is the
only harness where OTEL gives marvel something it cannot get more cheaply
elsewhere.

One inference recorded and explicitly not recommended: at auto-compaction
gemini's `tokens_before` is approximately the threshold fraction times the
window, so a window is arithmetically recoverable. That would be a new rung
below `table-alias`, and it would be wrong whenever the threshold moves,
which is the silent-misreport mode `limits.go` refuses. Do not ship it.

Net revision to the OTEL verdict: a narrow optimization with one new
capability, not a foundation. If the shift signal is an actuator event, build
it on the structured stream for claude and codex, and treat gemini's
`outfile` as the cheap way to bring a harness marvel cannot stream into the
same actuator vocabulary. The resource-attribute stamping at spawn stays
worth doing regardless, because it costs nothing and makes any of this
attributable later.

### opencode: five channels, and the specimen that justifies the additive rule

Measured against opencode 1.18.14 with plugin SDK `@opencode-ai/plugin@1.18.5`.
Five real channels, tiered separately.

| Channel | Occupancy | Window | Live | Notes |
|---|---|---|---|---|
| Plugin API | yes | yes | in-process | `chat.params` gives `input.model.limit.context`; `event` on `message.updated` gives `properties.info.tokens` |
| HTTP SSE `/event` | yes | no | out-of-process | same AssistantMessage payload the plugin hook receives |
| HTTP REST | yes | yes | polled | `GET /config/providers` for the window, `GET /session/{id}/message` for tokens |
| sqlite direct | yes | **no** | poll | the window is not in the file at all |
| `opencode db` | yes | no | poll | identical data at roughly 800x the latency (0.81s vs 0.00s); it boots a 140MB binary |

`GET /session/status` carries no tokens. It is `{}` when idle, else a map of
session id to `idle|busy|retry`. If anything in phase 1 hoped that was the
pressure endpoint, it is not.

**The specimen.** Three turns in one session, captured through the plugin
event hook:

```
turn1  total=30177  in=28241  out=2  reasoning=14  cache.read=1920
turn2  total=30189  in=20203  out=2  reasoning=0   cache.read=9984
turn3  total=30199  in=117    out=2  reasoning=0   cache.read=30080
```

`total` is a LEVEL: cumulative would have been 30177 / 60366 / 90565.
Confirmed again on a real six-turn production session in the store (37059,
37229, 41882, 42473, 44022, 46092).

But the level result is not the important half. Across those three turns
`input` collapses from 28241 to 117, a 99.6 percent drop, while `cache.read`
climbs from 1920 to 30080. The sum stays near-constant at 30161 / 30187 /
30197. **A consumer polling `input` alone would report the context emptying
out while it was in fact filling.** That is the most vivid empirical support
the additive rule has: occupancy is `input + cache.read + cache.write`, never
`input` alone, and the failure mode of getting it wrong is not a small
underestimate but an inverted trend line. It belongs in the evidence lineage
behind `internal/usage/doc.go` when this is harvested.

**opencode carries both representations, which is the trap.** Per-message
`tokens` are levels. The `session` table columns are cumulative sums: for one
session `tokens_input = 44634` is exactly the sum of that session's
per-message inputs, and `tokens_cache_read = 202240` is exactly the sum of
its per-message cache reads. Both measured. Reading a session column as
occupancy overstates pressure without bound as the session runs. That is
finding-007's defect available through a new door, in the same file as the
correct numbers, one join away. It is the concrete case the consent idea file
anticipated when it asked for a refusal at ingest for session-cumulative
samples.

Corrections to phase 1, both mine:

- "Token classes live in a `data TEXT` JSON blob" is right for `message` and
  incomplete. The `session` table has typed first-class columns
  (`tokens_input`, `tokens_output`, `tokens_reasoning`, `tokens_cache_read`,
  `tokens_cache_write`, `cost`, plus `model` and `agent`). No JSON extraction
  at session grain, which is exactly why the cumulative trap is easy to fall
  into: the typed columns look more trustworthy than the blob.
- The arithmetic identity holds: 219 assistant rows, 211 satisfy
  `total == input + output + reasoning + cache.read + cache.write`, **zero
  violations**, and 8 carry no `total` key at all (all-zero aborted turns).
  So the fingerprint-the-data proposal is viable here. One caveat: `total` is
  present in the persisted JSON but ABSENT from the SDK's declared
  `AssistantMessage.tokens` type, so the type cannot be trusted alone and a
  missing `total` must be handled.

**The docs are stale in our favor.** opencode's documentation states there is
no chat-params hook and no token hook. Both exist. The installed
`index.d.ts` is authoritative and lists `chat.params`, `chat.headers`,
`chat.message`, `permission.ask`, `tool.execute.before`/`after`,
`experimental.session.compacting`, and `experimental.compaction.autocontinue`.
Anything in this catalog sourced from opencode docs rather than from the
installed types should be re-checked.

Two plugin traps worth carrying into any implementation:

- **Awaiting any `client.*` call during plugin init deadlocks instance
  bootstrap silently.** A probe plugin that called `client.config.providers()`
  at init hung the instance after config load: it served nothing and logged
  nothing at DEBUG. Removing the plugin restored HTTP 200. Defer all client
  calls into hooks.
- **`message.updated` fires three times per assistant message**, the first
  with `total` absent and all token classes zero. Take the last per message
  id, or a naive consumer reads zero occupancy at every turn boundary.

Also measured: both `.opencode/plugin/` and `.opencode/plugins/` load, though
the docs name only the plural. And ollama models report `limit.context: 0`
(all three on this host), so any provider without catalogue metadata yields a
zero denominator. That is a live concern for roadmap M6 local model runtimes,
where the ladder would have to fall through to marvel's own table.

**OTEL is dead on opencode for a second, sharper reason.** Amelia's missing
exporter is confirmed independently (zero hits for exporter-trace-otlp,
exporter-metrics, or sdk-metrics; `NodeTracerProvider` and
`BatchSpanProcessor` present). Two refinements kill the path rather than
narrow it. First, `experimental_telemetry` gates emission per call in the
Vercel AI SDK, so a plugin registering a provider is not sufficient; the
spans are not produced at all, and no config surface exposes the toggle.
Second, and decisively: the standard `gen_ai.usage.*` attributes in this
bundle are exactly `input_tokens` and `output_tokens`. No cache, no
reasoning. Applied to turn 3 above, a gen_ai-based reading would have
reported 117 against a true occupancy of 30199. The richer `ai.usage.*`
namespace is present (`cachedInputTokens`, `reasoningTokens`,
`inputTokenDetails`) but under AI-SDK-proprietary names rather than the
standard ones. So the semantic convention that makes OTEL portable is
precisely the thing that would make it wrong here.

Hazards, both structural rather than incidental:

- `credential`, `account`, and `control_account` live in the same file as the
  telemetry. Row counts are zero on this host, so credentials live elsewhere
  here, but the access surface stands: anything granted read on
  `opencode.db` for token counts is one query from the credential table.
  Give a collector a copy or a view, never the file. This is the categorical
  argument from the consent idea file, now with a row-count check attached.
- `opencode serve` prints `OPENCODE_SERVER_PASSWORD is not set; server is
  unsecured`. Unauthenticated localhost HTTP that can list sessions, read
  transcripts, and submit prompts.

Pinning: the `migration` table has 38 rows with timestamped ids (latest
`20260602002951_lowly_union_jack`); `PRAGMA user_version` is 0 and useless.
`session.version` records the writing opencode version per row (198 rows at
1.18.11, 9 at 1.18.5, 2 at 1.18.10), so **a store is legitimately
mixed-version** and the assertion has to be per row, not per file.

Ranking: plugin API first (both halves, live, in-process, no open port, no
credential-file exposure; costs an in-process extension per harness plus
deferred-init discipline), HTTP SSE plus REST second (both halves, and the
only cross-host option; costs an unauthenticated port and two calls), sqlite
third (fast and dependency-free but numerator only), `opencode db` fourth
(ad-hoc only), OTEL not viable.

### codex: the occupancy formula is per-harness, not shared

Measured against codex-cli 0.146.0 over the 209-file local corpus, plus four
live probes under an isolated `CODEX_HOME`. This section corrects two of my
own claims above, one of which corrects a correction.

**The denominator correction was right about the field and wrong about the
direction. marvel must NOT multiply.**

`~/.codex/models_cache.json` carries `context_window = 272000` and
`effective_context_window_percent = 95` for all eight catalog models.
272000 x 0.95 = 258400, and **258400 is exactly the value the rollout emits
as `.payload.info.model_context_window`, in 206 of 209 files, with no other
value corpus-wide.** The rollout's number is already the effective,
pre-multiplied denominator.

So the earlier instruction in this brief was backwards. Reading the raw
catalog number would run optimistic, which is where the concern came from,
but marvel is not reading the catalog. A marvel that multiplies again lands
on 245,480, runs 5 percent PESSIMISTIC, and fires shifts early.

Adapter rule: read `model_context_window` from the same `token_count` record
as the level. Never read `models_cache.json`. Never apply
`effective_context_window_percent` yourself. Two supporting facts:
`effective_context_window_percent`, `context_window`, `max_context_window`,
and `auto_compact_token_limit` appear in no codex-emitted rollout record (the
only corpus hits sit inside a `function_call_output`, where a tool the
operator ran printed model config); and `max_context_window` diverges from
`context_window` for at least one model (gpt-5.4 at 1000000 vs 272000), which
is a second reason the catalog is the wrong source.

Also: `session_meta.payload.context_window` is a one-key object
`{"window_id": "..."}`. It is compaction-generation identity, not a size.

**`context_compaction` is in a telemetry enum, not the on-disk discriminant.**
An earlier line here said `session_meta` and `context_compaction` are both in
the `rollout_item_type` enum. That conflated two adjacent enums in the same
string region. The on-disk `type` variants are `session_meta`,
`response_item`, `inter_agent_communication`,
`inter_agent_communication_metadata`, `compacted`, `turn_context`,
`world_state`, `event_msg`. Corpus census over all 209 files: 7336
`response_item`, 4237 `event_msg`, 385 `turn_context`, 246 `world_state`, 209
`session_meta`, 16 `compacted`. `context_compaction` never appears as a
`.type` value. SP5 should search for `compacted` (top-level) and
`context_compacted` (an `event_msg` payload type, 16 occurrences, exactly
matching).

**Level versus sum, settled over 2098 records rather than three turns.** Both
shapes live in the same record and only one is usable:
`.payload.info.last_token_usage` is a per-request LEVEL;
`.payload.info.total_token_usage` is a CUMULATIVE SUM that reached 2,653,437
against a 258,400 window in one session and 51.9M in another. The level
series from one file runs 16415, 17971, 24981, 25431 ... 89467, 94749 against
the sum's 16415, 34386, 59367, 84798 ... 2642802 over the same records.
`state_5.sqlite` `threads.tokens_used` is also the cumulative total and is
not an occupancy source.

#### The result that changes the architecture

**codex's `input_tokens` already includes `cached_input_tokens`. Claude
Code's does not.**

Verified two ways across all 209 files: `total_tokens == input_tokens +
output_tokens` holds for every non-zero record with zero violations, so
cached is not a term in that sum; and `cached_input_tokens > input_tokens`
never occurs, so it is a subset.

Correct codex occupancy is `last_token_usage.input_tokens` ALONE. Applying
marvel's `input + cache_read + cache_creation` to codex nearly doubles it
(in=17971 with cached=16128 would report 34,099 instead of 17,971) and at the
top of a window reports over 100 percent pressure.

This is the sharpest cross-harness result of the sweep, and it is
uncomfortable, because the two harnesses disagree in opposite directions from
the same field name. opencode's specimen showed that summing `input` alone
UNDER-reports by 99.6 percent at the extreme; codex's shows that adding cache
OVER-reports by nearly 2x. **The occupancy formula therefore belongs in the
adapter, not in `internal/usage`.** Shipping the shared additive rule against
codex is a guaranteed defect of exactly the class marvel already shipped once.
This belongs in a ticket body, not a code comment.

#### Binding: no id pin, but a container pin that is better

Plainly, as a measured negative: codex 0.146.0 has no equivalent of
`claude --session-id`. `CODEX_THREAD_ID` was tested twice, once malformed and
once with a valid UUIDv7, and codex minted a fresh id both times. In the
binary it sits inside an environment allowlist next to SHELL, TMP, LC_ALL,
LOGNAME, and the `*KEY*` / `*TOKEN*` patterns, so it is a variable codex SETS
for children rather than one it reads. No `--session-id` flag, no
`session_dir`, `rollout_dir`, or `force_session_id` config key.

What exists is better for marvel's purpose, and both are measured live:

- **`CODEX_HOME` relocates the entire state tree** (`sessions/`,
  `state_5.sqlite`, `logs_2.sqlite`, `history.jsonl`, `shell_snapshots/`).
  Set it per spawned pane and that pane's rollouts are the only files in that
  directory: no cwd matching, no sqlite join, no ambiguity when two agents
  share a cwd. Auth carries by symlinking `auth.json`.
- **The `SessionStart` hook pushes the binding at spawn**, Claude-Code-
  compatible shape, carrying `session_id`, an ABSOLUTE `transcript_path`,
  `cwd`, `model`, `permission_mode`, and a `source` enum of
  `startup|resume|clear|compact`.

So marvel does not assign identity here; it assigns the CONTAINER and codex
reports the identity into it. Functionally equivalent for binding, and
strictly weaker only if a stable id across restarts were needed.

Two operational caveats, both measured. Hooks need persisted trust or
`--dangerously-bypass-hook-trust` per invocation, so pre-seeding trust is on
the critical path and marvel cannot ship a design requiring the flag. And
`CODEX_*` variables are NOT propagated to hook subprocesses (a hook running
`env | grep ^CODEX_` produced an empty file), so agent identity has to ride
the hook command line.

Full hook set: PreToolUse, PermissionRequest, PostToolUse, PreCompact,
PostCompact, SessionStart, SessionEnd, UserPromptSubmit, SubagentStart,
SubagentStop, Stop.

#### Compaction, and a live defect vector

Phase 1's "three in one session" is corrected: 16 compactions across 6 files,
largest single session 2. The signature, in order:

```
token_count  input=241665 win=258400          last pre-compaction sample
{"type":"compacted", payload:{message, replacement_history,
   window_number, first_window_id, previous_window_id, window_id}}
token_count  input=0 cached=0 output=0 total=13221   ALL-ZERO SENTINEL
{"type":"event_msg","payload":{"type":"context_compacted"}}   bare
token_count  input=18389 win=258400           first post-compaction sample
```

No before/after counts, unlike Claude Code's `compactMetadata`; they bracket
exactly from the series instead. Observed triggers at 93.5 percent and 86
percent of window.

**The all-zero sentinel is a live defect vector.** A naive "read the newest
token_count" reports occupancy 0 against a valid 258400 denominator at
exactly the moment the session is most stressed. Discard records where
`input_tokens == 0 && total_tokens > 0`, and discard `info == null` (measured
once, at the first token_count of a session).

On the compaction reason enum: the rollout does not carry one. The four
values (`user_requested`, `context_limit`, `model_downshift`,
`comp_hash_changed`) are OTEL-side only. What the rollout-adjacent surface
gives instead is PreCompact and PostCompact hooks carrying
`trigger: manual|auto` plus session_id, transcript_path, model, and turn_id.
Coarser than the enum, but a live push signal WITH CAUSE, which no other
harness offers and which no file-tailing design can produce.

Generation tracking is free: `previous_window_id` of generation N equals
`window_id` of N-1, `first_window_id` equals the session id, seeded by
`session_meta.payload.context_window.window_id`.

#### Tail discipline: one correction and one new risk

Append-only NDJSON confirmed; all 209 files end `0x0a`, line count equals
record count exactly on the two largest, and live growth across a running
session (41937, 46897, 52999, 57429, 64080, 65810 bytes at 5s intervals) was
monotonic with every one of 12 samples ending on a newline. So `(dev, ino)`
tracking is sound.

**But the fixed 64 KB window proposed earlier in this brief is not safe.**
The largest single record corpus-wide is 1,776,483 bytes, and the largest
byte gap between consecutive `token_count` records is 1,792,874. A window can
land entirely inside one tool-output record and yield zero complete records.
At rest it is benign (max distance from EOF back to the last token_count is
9,107 bytes, roughly 7x headroom), but mid-turn a large tool output pushes
the sample out of range. Start at 64 KB, grow on miss (256 KB, 1 MB, 4 MB,
capped), and HOLD the previous reading on a miss rather than emitting zero.
Occupancy is monotone within a generation, so stale is safe and missing is
not the same as low.

**New risk, unestablished and worth a ticket.** The binary carries a metric
family `codex.rollout_compression.{run, materialize, temp_cleanup,
file.source_bytes, file.compressed_bytes, file.compression_ratio,
file.duration_ms}` plus `jsonl.zst` and the string "compressed rollout reader
is busy". Rollout compression is a background JOB, not archive-on-demand. No
`.zst` files exist on this host so it could not be tested, but a tailer must
handle its file being replaced by a `.zst` sibling.

#### Pinning

No `schema_version` field anywhere in the rollout. `session_meta.payload
.cli_version` is the entire pin. Corpus spread: 0.146.0 (203), 0.145.0 (5),
0.42.0 (1).

#### Verdict, and two conditions on it

codex should lead, by a wider margin than phase 1 concluded, but not for the
reason phase 1 gave. The rollout file is good; what makes codex lead is that
it is the only harness offering a PUSH binding (SessionStart with an absolute
path at spawn) over a HARD isolation primitive (`CODEX_HOME`), which
dissolves the binding problem instead of working around it. PreCompact and
PostCompact with trigger attribution is a second capability no file-tailing
design reproduces.

Two conditions, both of which are the difference between leading and
shipping a defect:

1. **The occupancy formula must be per-adapter before the codex adapter
   merges.** Input includes cache on codex and excludes it on Claude Code.
2. **Sentinel-discard and grow-on-miss land in the first commit**, not as
   hardening. Both failure modes report LOW pressure at HIGH pressure, which
   is the direction that silently disables shift rotation.

As a mining corpus for SP5, codex is adequate to cross-validate a threshold
model against Claude Code's larger set, and not adequate as a primary: 16
events across 6 files, with before/after derived by bracketing rather than
read from metadata.

Carried forward as unestablished: `CODEX_ROLLOUT_TRACE_ROOT` and
`CODEX_SQLITE_HOME` behavior; whether zstd compression can fire under a live
consumer; whether hook trust can be pre-seeded without the dangerous flag
(critical path); whether `SessionStart source:"compact"` ever opens a NEW
rollout file, which would defeat `(dev, ino)` tracking and leave PostCompact
as the only recovery; and denominator behavior if two models ever differ in
effective window, which is untestable here because all eight local models are
identical at 272000 and 95 percent.

## The sweep's own best result: marvel already had the hard part right

The strongest cross-harness result above is that the two harnesses disagree
about the same field name in opposite directions. opencode's `input` alone
under-reports occupancy by 99.6 percent at the extreme; codex's `input`
already contains its cache term, so adding cache over-reports by nearly 2x.
The natural conclusion, and the one the codex report drew, is that the
occupancy formula has to move out of `internal/usage` and into the adapters.

That conclusion is wrong, and checking it is the most valuable thing this
round did. **marvel already ships the correct design**, and the sweep's
independent measurements confirm it rather than correcting it.

`internal/runtime/events/events.go:99-110` defines a `Layout` per harness,
declared BY THE PARSER and carried on the payload:

```go
// LayoutAdditive means occupancy is In + CacheReadIn + CacheCreationIn.
// Claude Code. Measured: In alone understated real context roughly
// 3000x on a warm session (finding-007 recorded In 10 against 29903).
LayoutAdditive Layout = "additive"
// LayoutSubsumptive means occupancy is In alone. Codex, whose
// input_tokens already contains cached_input_tokens (measured 13992
// with 11008 cached), so summing double-counts.
LayoutSubsumptive Layout = "subsumptive"
```

`codex/parser.go:179` sets subsumptive; `claudecode/parser.go:280` and
`opencode/parser.go:197` set additive. `Occupancy()` in both `sample.go:63`
and `events.go:162` branches on it. The comment at events.go:94-98 states the
reason the discriminant rides the payload rather than a consumer-side table:
the parser knows its own harness, so a declared Layout cannot drift out of
sync with the fields beside it, and an unknown harness cannot be silently
mis-summed.

So the codex condition "the occupancy formula must be per-adapter before the
codex adapter merges" is already satisfied, and was satisfied on the strength
of a two-number observation (13992 with 11008 cached). What the sweep adds is
scale: the same conclusion now rests on `total == input + output` holding
across every non-zero record in 209 files with zero violations, and on
`cached_input_tokens > input_tokens` never occurring. A reasoned design
decision has become a measured one.

**One live check fired for the first time.** `AdditiveConfirmed()`
(`events.go:198`) is a deliberately one-sided test: `Layout == LayoutAdditive
&& CacheReadIn > 0 && In < CacheReadIn`, true only when `In` is smaller than
the count served from cache, which is impossible if `In` already contained
it. Its comment says it "needs a caching turn to fire" and calls itself "the
only signal marvel has for OpenCode's unverified cache layout." opencode's
turn 3 above is that turn: In 117 against CacheReadIn 30080. The check fires,
and opencode's additive declaration moves from assumption to measurement.

**And one thing it did NOT confirm.** The same parser sets
`TotalExcludesCache: true`, and 211 of 219 live rows satisfy
`total == input + output + reasoning + cache.read + cache.write` with zero
violations, so opencode's total is defined over five classes rather than
three. With the flag set, `TotalMismatch()` reports a phantom mismatch equal
to `CacheReadIn` on every caching turn, which is precisely the failure the
flag was added to prevent. Filed as `ArcavenAE/marvel#145`; the round-3
evidence is in a comment there rather than duplicated here.

The pattern worth carrying out of this: **check the codebase before accepting
an architectural recommendation derived from measurement.** Two of the four
runtime reports proposed a change marvel had already made, and the value was
not in the change but in the evidence. The same move is what turned the
compaction-mining brief from "we should measure this someday" into "the
measurement is on disk," and what turned the accountant's hysteresis from a
reasoned constant into a testable one.

## Synthesis round: the rulings the research did not produce

The per-channel measurements above are the round-3 output. Round 4 put the
lanes against each other and produced four rulings that no single measurement
implied. Each is recorded with the argument, because the argument is what
transfers.

### 1. The numerator is marvel's, always. The THRESHOLD is the open question.

The catalog kept framing the problem as "how does marvel read occupancy."
That half is closed, and closed against trusting any harness: two runtimes
use the same field name with opposite arithmetic (opencode additive, codex
subsumptive), which is not an argument for per-harness Layout but proof of
it. `Total` stays fenced to the mismatch invariant exactly as `doc.go` says.

What is actually open is the threshold. Crush publishes two terms and
actuates at a knowable point: `agent.go:57-59` at v0.88.0 carries
`largeContextWindowThreshold = 200_000`, `largeContextWindowBuffer = 20_000`,
`smallContextWindowRatio = 0.2`, so compaction fires when remaining drops
under 20k for windows above 200k and otherwise under 20 percent. For the
40960 window measured, that is compaction at 32768 occupancy: 80 percent,
derivable, version-pinned.

marvel does not want occupancy. It wants to predict compaction. So a marvel
trigger that disagrees with the harness's own actuation point is wrong no
matter how defensible its arithmetic.

**The ruling splits by blast radius.** A wrong threshold makes marvel early
or late; a wrong numerator makes marvel BLIND. Different failure magnitudes,
so they do not deserve the same rule:

- Numerator: computed by marvel from the token classes, never harness-supplied.
- Threshold: MAY be harness-derived, but only as a declared constant behind a
  version fence, with a conservative fallback on an unrecognized version.
  Never inferred, never followed silently.

Those three Crush constants are unexported, undocumented, and carry no
compatibility promise, which is exactly why the fence and the fallback are
the price of using them. Not generalized: four of five harnesses expose no
actuation point at all. This is a Crush precision bonus, not a mechanism.

### 2. Perturbation is eligibility, not grading. Blast radius is a third thing.

Two separate attempts to fold new properties into the consent axis both
failed, and the reasons differ.

**Perturbation** (does obtaining the reading change the observed system)
belongs in arbitration ELIGIBILITY, not in the grade. Grading asks whether
the number is true; perturbation asks what obtaining it costs. Merging them
corrupts the ladder. The sharper carve-out: where a harness's behavior KEYS
ON BEING OBSERVED, as Crush's workspace reap does, the reading can change the
agent's lifecycle, and that is a per-channel correctness bug needing a test,
not a property needing a slot in the model.

It also does not generalize, which was worth measuring rather than assuming.
opencode was checked directly: 210 seconds idle with no observer, `created=1`
before and after, `disposed=0`, and a touch returning 200 on the same
instance. Observation is free there.

**Blast radius** is a genuinely third property and the consent axis cannot
see it. Consent grades the PROVENANCE of permission; it says nothing about
MAGNITUDE. An in-process plugin is fully CONTRACTED and can hang the harness
(measured: awaiting a client call at init deadlocks instance bootstrap
silently, no error at DEBUG). A read-only sqlite handle is APPROPRIATED and
can at worst be stale. Contracted-and-catastrophic is not a paradox; it is
the axis measuring the wrong dimension. Blast radius belongs in eligibility
cost alongside perturbation, not in the grade.

The practical consequence, conceded by the lane that had ranked the plugin
first: **a marvel plugin never goes in front of an operator's live session.**
Only in front of marvel-spawned agents, where marvel already owns the process
and a hang is marvel's own fault. That distinction does not exist anywhere in
marvel today, and its absence is the actual gap.

### 3. The inventory is a selection instrument, not a build list

Cataloguing per channel is epistemically right and is also an invitation to
build five implementations per harness. One criterion collapses it. **Does
the channel carry occupancy AND denominator in the same record?**

Applied to codex's eight enumerated surfaces, seven die: `state_5.sqlite`
carries a cumulative (51.9M), `models_cache.json` is a static catalog that
already disagrees with the live window on one model, OTEL counters reproduce
finding-007 by construction, and `logs_2.sqlite`, the eleven hooks,
`CODEX_HOME`, and the SessionStart push are not pressure channels at all
(three of them are BINDING, which is a different job). The rollout
`token_count` record survives.

So: one channel per harness plus an honest absent state, with the full
inventory kept in this probe as the EVIDENCE for the choice. Shipping five
implementations per harness confuses the ruler with the thing measured.

### 4. Spawn-time pinning is a REQUIREMENT, stated at the right altitude

The three mechanisms found are not three features to exploit. `claude
--session-id` ASSIGNS an identifier; `CODEX_HOME` NAMES A CONTAINER;
`GEMINI_TELEMETRY_OUTFILE` names a file. One requirement underlies all three:

> At spawn, marvel fixes either the artifact's IDENTITY or its LOCATION, so
> that nothing has to be discovered afterward.

Written that way, a sixth harness is evaluated in one command instead of a
sweep, and the failure mode has a name: a harness offering neither is one
marvel supervises blind. Crush is where that gets tested first.

### The refutation that decided the round

The standing counter-argument was that the whole measurement program may be
optional, because the actuator only needs a compaction EVENT and an event
needs no denominator. The compaction event does exist on all five harnesses,
so the premise held.

It fails anyway, and the refutation came from the lane whose own work it
would have vindicated:

**Shifting ON compaction is shifting AFTER the loss.** The uncontrolled
summarization correctly priced as silent has already been paid by the time
the event lands. What makes an event sufficient is a PRE hook. codex has one
(`PreCompact`, carrying `trigger: manual|auto`). gemini has one
(`PreCompress`). **Three of five have nothing before the fact.** So an
event-only design works on two harnesses and silently degrades to
shift-after-loss on the other three, which is the exact failure it was
designed to avoid.

The corollary, stated against the same lane's own interest: on codex
specifically, if `PreCompact` ships then the rollout tail is the FALLBACK,
not the primary. **The measurement program earns its keep on the three
harnesses where the actuator has no early signal**, not on the one with the
best file.

### One self-inflicted defect the round surfaced

`opencode/parser.go` sets `TotalExcludesCache: true` with a comment saying
opencode's total is "defined over input + output + reasoning". Measured: 211
of 211 assistant rows satisfy `total == input + output + reasoning +
cache.read + cache.write`, zero violations.

The harm is not a wrong number. `events.go:183` gates the layout arbiter on
`!TotalExcludesCache`, so a wrong `true` **silently disables the one
cross-check that would catch a wrong Layout**. The flag was set defensively
because no caching turn had ever been observed. One has now.

Sharper still: `ctx-channel-consent-and-fidelity.md` asserts the correct
five-term identity and proposes fingerprinting it. So the graph states the
identity in the idea file and denies it in the parser. That is a better
specimen of the silent-failure taxonomy than anything the sweep found in a
third-party harness, because it is internal and self-inflicted. Filed with
evidence at `ArcavenAE/marvel#145`.

### 5. The consent axis does not survive as an axis. Three things were hiding in it.

It was inverted on CONTENT and on COST in the same round. A grading scheme
that mispredicts both quality and freedom is not grading anything. What was
load-bearing inside it splits three ways, and none of the three is a ladder:

- **One absolute refusal, one bit.** No read path whose access surface
  includes credential storage. `opencode.db` holds `credential` and
  `control_account` beside the token counts. That is SOUL section 3 and it
  needs no rungs. Note the refinement: this is categorical about a FILE, not
  about a grade. Grade the ARTIFACT, not the manner of arrival, and
  `~/.claude/projects` (a transcript directory with no credential table in
  reach) comes in while `opencode.db` stays out.
- **Failure signature**, which is what consent was proxying for: loud-absent
  versus silent-wrong. The discriminant is not who published the channel, it
  is **whether the channel carries a self-check**. codex has
  `total == input + output` across 209 files with zero violations. Crush
  persists its estimated-versus-measured flag nowhere, so the contracted
  channel is the one that can lie quietly.
- **Provenance recorded on the reading**, not a gate on admission. `Source`
  beside `LimitSource`, the pattern already ratified.

Of the three original grounds, CONTENT defeats exactly one: **declaration**.
The argument was that a contracted schema tells you which quantity you hold.
Claude's `compactMetadata` names `trigger`, `preTokens`, `postTokens`, and
`cumulativeDroppedTokens` as distinct keys; the contracted span names
`trigger` and `message_count`. The appropriated artifact out-declares the
contracted one, so **declaration is a property of the payload, not of the
consent grade**, and grading it by grade will keep producing this inversion.

### 6. The kill condition fires. The apparatus was over-built.

The consent file named its own kill condition: if arbitration never fires
because no session has two eligible channels, the whole thing is ceremony.
Checked per harness, and it fires.

The channels marvel plausibly holds at once are **complementary, not rival**.
Crush is FEED-N plus a one-shot denominator on a separate route. codex is a
push binding pointing at a file. Composition is not arbitration. The three
apparent rivals each collapse: Crush's sqlite read back 28639/414 matching
the last SSE frame exactly, so choosing between them decides nothing;
opencode's sqlite has no window in it at all, so it is not a rival on both
terms; claude statusline versus transcript is the one real pair, and it is
settled ONCE at adapter-authoring time, not per session.

**What varies per session is PRESENCE, not preference.** So the replacement
is the pattern marvel already ships: an ordered channel list declared by the
adapter, the way `Layout` rides the payload because the parser knows its own
harness. First channel that answers wins, `?` when none does, provenance
stamped. A switch and a fallback, not an arbiter.

One amendment carried from the perturbation ruling: a test with no slot has
nowhere to put its result. Give the channel declaration one boolean,
`ObservationRegisters`, so the fallback chain can prefer a non-registering
channel when both answer. One field, not a model.

### 7. Refuse the derived threshold. LEARN it.

The threshold-may-be-harness-derived rule is rejected as a PREDICTOR. Three
unexported constants at one version is not a model, and it is contradicted
next door: codex publishes both its denominator terms and still fired
compaction at 93.5 percent and 86 percent of window in the same corpus.
Predicting a vendor's private policy from constants they did not export,
when the realized value moves 7.5 points on a harness that does export, buys
a number marvel would have to distrust anyway.

What is worth having is the same quantity **observed rather than predicted**.
Every harness leaves a compaction crossing legible: codex's all-zero
sentinel, Crush's `prompt_tokens` going to zero with `summary_message_id`
set, gemini's `PreCompress`. Record occupancy at the last pre-compaction
sample and the realized threshold is MEASURED per session and per model.
That is `LimitLearned`'s sibling and it fits the ratified ladder. Never a
denominator, never a gate. And because the asymmetry argues the operator's
conservative fallback should fire early anyway, the actuator never needs the
harness's threshold predicted at all.

### 8. The stop rule, and the eligibility gate that precedes fidelity

**marvel builds one channel per runtime it can already launch. The catalog
holds the rest.** Eligibility is three requirements, all mandatory:
contracted or conceded; pinnable at spawn; carrying a declarable Cumulation.
Fidelity ranks only what clears all three.

Applied to gemini it overturns the round-3 ranking. Under this rule gemini
builds `AfterModel` and nothing else. The local chat files are appropriated,
so off the ladder. OTEL survives eligibility (its `api_response` log event
carries per-request LEVELS, so the counter objection does not reach it) and
still loses, because `GEMINI_CLI_SYSTEM_SETTINGS_PATH` lets marvel project a
hook config per process without touching the operator's
`~/.gemini/settings.json`, and no OTEL configuration has that isolation.
Same number, less machinery, better blast radius. Three channels catalogued,
one built, and the OTEL row stays alive for a different consumer.

### 9. Two lanes for the ratio, which dissolves the third-FEED-kind question

Crush's computed ratio is not a third FEED kind, because it is not the same
quantity: it computes `input + cache_read` and drops cache-creation where
marvel adds it, and its estimated-versus-measured flag lives in memory and
reaches no channel. Arithmetic over a different definition with unmarked
input quality cannot enter `internal/usage`.

But it is the right number for a different consumer. **For metering you want
terms, because you are answering "how full." For actuation you want the
harness's own ratio, because you are answering "when will it compact," and
the harness will act on its own arithmetic no matter whose is correct.**
Agreeing with the harness about its own threshold beats being right by
marvel's formula. Derived ratios go in the actuator lane, terms go in the
metering lane, and the question of what kind of FEED it is dissolves.

### 10. The concession, and the consumer nobody had named

The standing claim that the measurement program is largely optional is
withdrawn in half, by its own author. Stated plainly: **for shift triggers, a
compaction event on five harnesses is sufficient and CTX% is not required.**
The operator display is a genuine consumer but it does not fund the work, and
leaning on it was a rationalization.

What survives is a consumer nobody had named: **admission and dispatch**.
`internal/admission` refuses over-budget work at the operator verbs and
cannot do that from an edge-triggered event. An event tells you where you
have BEEN; placing the next unit of work needs a LEVEL. So the sequencing
claim stands (`hpeu` was never blocked on `dc1j`) and the stronger claim
(measurement is largely optional) does not.

The inferred compaction detector is ruled a **necessary fallback, demoted to
fallback, and defective today**. Markers do not reach the generic adapter or
any unknown CLI, so the floor stays. But codex writes `input=0 total=13221`
at the moment of compaction and a 2048-token hysteresis fires on that record,
booking post-compaction occupancy as zero: it fires for the wrong reason on
the one harness with measured data. Discard `input==0 && total>0` before the
detector sees it, gate the detector on marker-absent, then let SP4 replace
both constants with measured false-positive and false-negative counts.

### 11. The root cause, which is not a measurement question

Four researchers spent a week of agent-hours on channels. The question was
never "how do we read the number." It is **who is entitled to decide when a
session ends.**

marvel constructs the process environment at spawn and then went looking to
DISCOVER a denominator it may ASSIGN. That is also why consent mislocates the
Crush result: an observer registering as a live client is not a consent
failure, it is what happens when a subordinate process is framed as an
external system of record.

The second face is procedural and cheaper to fix: the edge `hpeu blocked-by
dc1j` was **inherited, never derived**. Nobody was asked to justify it, and a
week of work followed from an unexamined dependency. That is
`task-workflow.md`'s verify-the-premise rule applying to a graph edge rather
than to a ticket body, which is a case the rule does not currently name.

### 12. Every channel here is sand we do not own

Asked whether any harness treats supervisor observability as a stated
contract, the answer is **none**. Every channel that scored well is exhaust
from a human-facing feature: a footer a person looks at, a log a person
debugs. codex comes closest by naming `effective_context_window_percent`, and
still ships it only as telemetry.

There is one path from side effect to contract, and it is cheap. Crush parses
Claude Code's `hookSpecificOutput` envelope, and its documentation invites
hook requests. Three statuslines have already converged on Claude Code's
`context_window.*` field names. So **one accepted upstream ask for a
usage-carrying turn-end hook has fleet leverage**, because the convergence
means a single field shape would port. That is the only move in this catalog
that changes marvel's position rather than working within it, and it belongs
to whoever owns upstream relations, not to the adapter work.

Two roster corrections from the same pass. **amp is dropped**: usage rides
per-message on assistant events, its `result` event carries no usage, and the
documented pattern is accumulate-across-messages, which is finding-007's
defect as the vendor's INTENDED read. **Copilot CLI's `STATUS_LINE` is still
experimental** as of 2026-08, gated behind `feature_flags.enabled` in
`~/.copilot/settings.json`. Not a blocker, because marvel writes that file.
It is the warning.

### What to build first

**Interactive codex.** `CODEX_HOME` per spawned pane, the `SessionStart` hook
pushing the absolute rollout path, and the tail with both refusals in the
first commit (discard `input==0 && total>0`; grow on miss and hold the last
reading rather than emitting zero). Both of those failure modes report LOW
pressure at HIGH pressure, the direction that silently disables rotation.

One build settles three things: it dissolves the binding problem instead of
working around it; it gives `internal/usage` its second interactive producer,
so provenance stops being optional; and `PreCompact` with trigger attribution
falls out of the same wiring, which is simultaneously the instrument for the
learned threshold in ruling 7 and the test of whether the shift trigger ever
needed the channel program at all.

## SCOPE QUALIFICATION: what was ruled out, and what emphatically was not

This brief rules against OTEL for ONE use case: **context pressure**, meaning
the occupancy numerator and the context-window denominator that feed CTX% and
the shift trigger. That ruling is narrow, and it must not be read as a ruling
on OTEL in marvel.

Stated as a single sentence for anyone who reads no further:

> **OTEL cannot carry context pressure. OTEL remains the right substrate for
> spend, throughput, tool access, and attention metering, and the same
> measurements that disqualified it here are positive evidence for those.**

### Why the disqualifier is a qualifier elsewhere

The finding against OTEL is that every harness token instrument that exists
is a **monotonic cumulative Counter**. For occupancy that is fatal, because
occupancy is a per-request LEVEL that RESETS at compaction; a counter folded
into it reproduces finding-007 and grows worse the longer a session runs.

But cumulative is the CORRECT shape for spend. Dollars spent do not reset at
compaction. Neither do tokens billed, tools invoked, subagents spawned, or
seconds of wall clock consumed. A counter is the right instrument for every
one of those and the wrong instrument for exactly one thing, which is the
thing this brief was about.

So the sweep's negative result is a narrow mismatch between an instrument
type and a quantity, not a defect in the channel.

### What the sweep actually found, read affirmatively

The same enumeration that killed the pressure case is an inventory of
resource-matrix instrumentation marvel does not otherwise have. Claude Code
2.1.226 carries 20 distinct `claude_code.*` names (8 metrics plus 12 spans).
Mapped against the agentic resource matrix
(`elem-agentic-resource-matrix`, the governing frame per CLAUDE.md):

| Instrument | Matrix row it serves |
|---|---|
| `cost.usage`, `token.usage` | token spend, and the wave-1 metering already shipped |
| `active_time.total` | time budgets |
| `tool`, `tool.execution`, `bash.subprocess`, `mcp.rpc` | tool and filesystem access |
| **`tool.blocked_on_user`** | **human attention** |
| `subagent.spawn` | fleet composition and per-team accounting |
| `lines_of_code.count`, `commit.count`, `pull_request.count` | output measurement, which is critic's remit (per-selected-outcome cost) |
| `session.count`, `interaction`, `llm_request`, `hook` | lifecycle and throughput |
| `compaction` (span) | the actuator signal, per the rulings above |

`claude_code.tool.blocked_on_user` deserves its own line. Vision Gap 1 is
operator attention routing, described there as the scarcest metered resource
and explicitly recorded as having no owner. A harness is already emitting an
instrument for precisely that quantity, and this review found it while
looking for something else. That is worth carrying to whoever picks up Gap 1
regardless of what happens to CTX%.

codex's OTEL surface is similarly rich on spend
(`codex.turn.token_usage.{input,cached_input,cache_write_input,
non_cached_input,output,reasoning_output,total}_tokens`, plus `gen_ai.usage.*`
on `codex.api_request`), and gemini emits `gemini_cli.token.usage` with a type
dimension plus `gen_ai.client.token.usage` on the standard convention.

### The two mechanisms found here that transfer to any OTEL work

Both were discovered chasing pressure and neither depends on it:

- **`OTEL_RESOURCE_ATTRIBUTES` is honored by claude and codex**, and claude
  promotes resource attributes to datapoint labels by default. So marvel can
  stamp its own pane identity into exported telemetry at spawn. This is free,
  it costs nothing to do now, and it makes ANY later OTEL consumption
  attributable to a marvel session. It should be done whether or not a byte
  of OTEL is ever ingested.
- **Two listener-free receive paths exist**: claude's Prometheus PULL exporter
  (`OTEL_EXPORTER_PROMETHEUS_HOST`/`_PORT`, both present in the binary), where
  marvel allocates the port per agent and scrapes on its own cadence, and
  gemini's genuine file exporter. Both make attribution a function of a port
  or path marvel itself assigned, which is the same move as the session-id
  pin, and both avoid standing up a collector.

### What is NOT ruled on here, and where it belongs

`question-marvel-otel-architecture` holds the real design question: what
marvel advertises, hosts, forwards, and subscribes to, with five candidate
topologies and the kitchen-sink trap named. Nothing in this brief touches it.
Its sub-question B (which signals the control loop actually needs) gains one
answer from this sweep and only one: **context pressure is not among them,
because OTEL cannot carry it.** Every other signal in that sub-question is
untouched.

Two corrections that node should absorb when someone harvests this:

- It cites finding-008 as establishing that three harnesses emit native OTEL
  "and that none of them exports context-window occupancy." The second half
  now has a caveat: codex DOES publish its denominator over OTEL
  (`context_window` and `auto_compact_token_limit` as attributes on
  `codex.conversation_starts`). It publishes the numerator as a counter, so
  the conclusion holds, but the reason is now instrument TYPE rather than
  field absence, and that distinction matters for anyone re-checking it later.
- Crush and opencode should be added as measured negatives: Crush links the
  otel API plus noop only and has zero SDK or exporter symbols, so it cannot
  export at all; opencode ships `@opentelemetry/api` and `sdk-trace` with no
  exporter and no metrics SDK, and gates emission behind the Vercel AI SDK's
  per-call `experimental_telemetry` flag with no config surface exposing it.

### One genuine limitation, stated so it is not overclaimed

For the standard `gen_ai.usage.*` semantic convention specifically, the sweep
found a real defect rather than a mismatch: in opencode's bundle it is exactly
`input_tokens` and `output_tokens`, with no cache and no reasoning term.
Applied to a measured turn where input was 117 and cache read was 30080, a
spec-conformant reading reports 117 against a true occupancy of 30199.

**Conformance is the defect there**, and it cannot be fixed without breaking
portability, which was the convention's whole value proposition. That is a
limitation of one semantic convention for one quantity, not of OTEL as a
transport or of vendor-namespaced instruments, and it should not be
generalized either.
