# finding-028 — event-ring inventory: a well-observed, well-typed ledger that nothing reads

Probe: recon task [A], commissioned in
[ArcavenAE/aae-orc#242](https://github.com/ArcavenAE/aae-orc/issues/242).
Operator: Claude (Opus 5), no human at the keyboard.
Date: 2026-08-25 (13:33Z). **Spend: $0.00.**

Binary: **`marvel 0.1.0-alpha.20260823.211648.2f76ccf`** (brew-installed,
notarized, `Identifier=com.arcavenae.marvel`, `TeamIdentifier=XBGSVSJPD8`).
Operator directive as of this task: prefer the signed install over the tree
build, which is unsigned (`codesign: Internal requirements=none`).

**Comparability with finding-027**, which used the tree build at `0c59baa`:
the entire delta between that SHA and this binary's `2f76ccf` is one renovate
commit touching `go.mod`, `go.sum`, `mise.toml` — dependency bumps, zero
source changes. Verified with `git show --stat` rather than assumed. Prior
results carry forward.

**Measurement only.** No code changed, no fix proposed, no patch sketched.
The comms/bus layer is explicitly the conductor's, not this probe's.

Related: finding-027 (the truth table this instruments), ArcavenAE/marvel#201,
#203.

---

## 1. The headline

Marvel's event vocabulary is **larger and better-typed than expected**, and
**structurally unable to inform the state machine**. Three separable facts,
each measured:

1. **35 event kinds**, every one with a live emit site. No dead vocabulary.
2. **The ring's `Event` struct has no payload field.** Every kind-specific
   fact survives only as prose in a 160-char `Message` string.
3. **Nothing reads the ring.** Its single non-test reader serves
   `marvel events` to a human terminal. No controller, no reconciler, no
   health evaluator consults it.

The gap finding-027 found is therefore not an observation gap. Marvel *sees*
the distinction between a completed agent and a dead one. It types it
correctly upstream, inspects it in passing, renders it to prose, and throws
the structure away — then never reads the result.

## 2. The ledger

`marvel events --list-kinds`: **23 control-plane + 12 agent-stream = 35.**

### Control plane (23) — what marvel did to a session

| kind | emitted by |
|---|---|
| `session.created`, `session.deleted`, `session.crashed` | `internal/session` |
| `session.restarted`, `session.failed` | `internal/team` |
| `health.failed` (`KindHealthCheckFailed`), `health.crashloop-backoff` | `internal/team` |
| `team.shift-started`, `team.shift-completed`, `team.shift-timed-out`, `team.shift-role-ready` | `internal/team` |
| `role.saturated`, `role.removed` | `internal/team` |
| `policy.projected` | `internal/session` |
| `context.limit-unresolved` | `internal/usage` |
| `admission.refused`, `admission.cleared` | `internal/team` |
| `admission.unmeasured` | `internal/daemon` |
| `reconcile.adopted`, `reconcile.killed`, `reconcile.left` | `internal/session` |
| `heartbeat.refused`, `heartbeat.unbound` | `internal/daemon` |

### Agent stream (12) — what the agent inside a session did

`agent.session.started`, `agent.session.ended`, `agent.turn.started`,
`agent.turn.completed`, `agent.message.delta`, `agent.message.completed`,
`agent.tool.call`, `agent.tool.result`, `agent.permission.requested`,
`agent.auth.required`, `agent.health.heartbeat`, `agent.error`.

All lifted by `internal/session/bridge.go` from the adapter vocabulary in
`internal/runtime/events`. They fire only for stream-capable launches; an
interactive pane publishes no stream to parse.

**Checked for dead vocabulary and found none.** Every kind above resolves to
a non-test emit site. This is a ledger that is actually written.

### The fields — for all 35 kinds, this is the entire set

`internal/events/events.go:227`:

```go
type Event struct {
    Seq        uint64    // ring-assigned monotonic, for --follow cursors
    Timestamp  time.Time
    Kind       Kind
    Severity   Severity
    Workspace  string
    Team       string
    Role       string
    Session    string
    Actor      string    // "pid=N socket=PATH", for two-daemon disambiguation
    Generation int64     // when the event's subject is a whole generation
    Message    string    // one line, capped at 160 chars (maxRingMessage)
}
```

**There is no payload field.** Grepped for `Fields`, `Attrs`, `Data`,
`Detail`, `map[string]` — none present. So exit codes, token counts, costs,
tool names, pane ids, backoff durations all live in `Message` as prose or not
at all.

The 160-char cap (`maxRingMessage`) exists so one event renders on one
terminal line, which tells you what the ring was designed for: **operator
scanning.** That is a coherent design for a log. It is not one for a signal
bus, and nothing in the code claims otherwise — the gap is that the state
machine has no other source.

## 3. The indictment

### 3a. Upstream, the data is properly typed

`internal/runtime/events/events.go` defines ten payload structs:
`SessionStartedData{Model, Cwd, Resumed, Tools}`,
**`SessionEndedData{Reason, ExitCode, Usage, Metering}`**, `TurnData`,
`MessageData`, `ToolCallData`, `ToolResultData{OK}`,
`PermissionRequestedData`, `AuthRequiredData`, `HealthHeartbeatData`,
`ErrorData`.

`SessionEndedData.ExitCode` is precisely the field that separates a completed
one-shot from a death, and its own doc comment says so: *"ExitCode 0 =
success; non-zero maps to Lift() → INFORM(alert)."* `Metering` carries
`DurationMS`, `APIDurationMS`, `TTFTMS`, turn counts, cache accounting, and
per-model breakdown, with an explicit discipline that a harness reporting
none of it leaves the pointer nil *"so consumers can distinguish 'not
reported' from 'zero'."*

That is careful, deliberate typing. It reaches the bridge intact.

### 3b. It dies at one assignment

`internal/session/bridge.go:36`:

```go
return events.Event{
    Kind: kind, Severity: sev,
    Workspace: c.Workspace, Team: c.Team, Role: c.Role, Session: c.Session,
    Message:   summarize(ev),      // <-- typed Data flattened to a string
}
```

`summarize()` (`:83`) switches on the payload type and renders it:
`SessionEndedData` becomes
`"exit 0 (end_turn) tokens in=3 out=4 cost=$0.2076 turns=1 dur=2500ms ..."`.
The struct is not stored anywhere. There is no field on `events.Event` that
could hold it.

**The sharpest detail: the bridge reads `ExitCode` twelve lines earlier and
uses it for colour.** `ringKind()` at `:48`:

```go
case rtevents.KindSessionEnded:
    if d, ok := ev.Data.(rtevents.SessionEndedData); ok && d.ExitCode != 0 {
        return events.KindAgentSessionEnded, events.SeverityWarning
    }
    return events.KindAgentSessionEnded, events.SeverityInfo
```

So marvel inspects the exact field that answers "did this agent succeed or
die," uses it to pick `info` vs `warning`, and then discards it. The severity
is the only surviving trace, and severity is not consulted by the reap path.

### 3c. And the ring is write-only

Read surface: `Ring.Snapshot(Filter, n)` at `events.go:367`. Its only
non-test caller is `internal/daemon/daemon.go:746`, inside the `events` RPC
handler, which marshals the snapshot straight to the CLI.

`internal/team/` and `internal/session/` call `Emit` and never read.
Confirmed by grep across both packages.

**So the path is: adapter → typed payload → prose string → ring → RPC →
human's terminal.** There is no branch off that path into any decision.

The ring is not a bus that the state machine is failing to use. It is a
display log, and there is no bus.

## 4. The three outcomes, measured live

One team, two roles, generic adapter, **$0.00**: `/usr/bin/true` (completes
immediately) and `sleep 3600` killed with `SIGKILL` (dies). Same reap path a
headless claude one-shot takes — established equivalent in finding-027.

```
TIME      SEV      KIND             SESSION               MESSAGE
13:33:20  info     session.created  ring/led-done-g1-0    pane %1
13:33:20  info     session.created  ring/led-victim-g1-0  pane %2
13:33:21  warning  session.crashed  ring/led-done-g1-0    pane %1 gone      <- SUCCEEDED
13:33:37  warning  session.crashed  ring/led-victim-g1-0  pane %2 gone      <- KILLED
13:33:37  warning  session.failed   ring/led-victim-g1-0  restart_policy=never, pane gone; role frozen
```

Filtered to the pair, which is the whole finding in two lines:

```
13:33:21  warning  session.crashed  ring/led-done-g1-0    pane %1 gone
13:33:37  warning  session.crashed  ring/led-victim-g1-0  pane %2 gone
```

**Same kind, same severity, same message shape, opposite underlying truth.**
The only differing field is which pane number vanished.

### The indictment table

| outcome | signal that COULD distinguish it | present on the ring? | reaches the state machine? |
|---|---|---|---|
| **completed** | `SessionEndedData.ExitCode=0`, `Reason="end_turn"` | prose in `Message` only; structure dropped at `bridge.go:36` | **no** — read at `:48` for severity, then discarded |
| **died** | absence of any `agent.session.ended` before the reap | both facts present, but only as **two events needing correlation over time** | **no** — nothing correlates, because nothing reads |
| **blocked** | nothing fires at all | **absent entirely** | n/a — the information does not exist in marvel |

### The `died` row deserves emphasis

Marvel could distinguish died from completed **today, with no new
instrumentation**, by asking: *did an `agent.session.ended` arrive for this
session before its pane vanished?* Both facts are already in the ring,
already timestamped, already sequenced (`Seq` exists precisely to order
them).

Nothing joins them — not because the join is hard, but because **no code
reads the ring at all.** That reframes finding-027's cell-1 gap: it is not
"wire one field through," it is "there is no reader to wire it to."

### A candidate for `blocked` that I had not seen before

`agent.permission.requested` and `agent.auth.required` both exist, both are
`warning` severity, and both mean *"the harness is waiting on a human."* So
the vocabulary for blocked-on-a-human is **already half-built.**

The limit: they fire only for stream-capable launches. An interactive TUI's
first-run consent dialog — finding-027's cell 2 — publishes no stream, so
neither fires. But this narrows the gap usefully: the concept exists on the
side of the fence that has a stream, and cell 2 is the *interactive* case
specifically, not a general absence of the idea.

## 5. The instrument has no machine interface

#242 frames this ledger as *"the instrument that makes every later metering
run possible."* As it stands, the instrument is human-only.

`marvel events` flags: `--follow`, `--kind`, `--lines`, `--list-kinds`,
`--role`, `--session`, `--team`, `--warnings`, `--workspace`. **No
`--output json`, no `-o`.**

The daemon marshals `Event` structs to JSON over the RPC, then the CLI
renders them into a fixed-width table. So an external consumer — a
supervising agent, a metering script, tasks [C]/[D]/[B] of this campaign —
must scrape padded columns. Fields the ring *does* carry (`Seq`, `Actor`,
`Generation`) are not all rendered, so they are unreachable from outside the
daemon without writing a client.

For this campaign's own purposes that is the binding constraint: every later
task's measurements come out of `grep` against a table, which is how I have
been counting spawns and costs since run-01.

## 6. What this changes about finding-027's conclusion

finding-027 said the requirement is that state derive from what the agent
reports, with pane liveness as fallback, and split the work into two gaps:
cell 1's signal "exists and is unrouted," cell 2's "does not exist."

This inventory **sharpens the first half and confirms the second**:

- Cell 1 is not one unrouted field. It is a **typed payload with no
  destination and no reader**. Two things are missing: somewhere on
  `events.Event` to keep structure, and any consumer of the ring at all.
  Adding `ExitCode` to a prose string would not help, because no code reads
  the string either.
- Cell 2 is confirmed absent, with the qualification above: the *concept*
  ships for streamed sessions (`agent.permission.requested`,
  `agent.auth.required`); only the interactive path lacks it.

## 7. Method notes

- Every ledger claim is from source (`internal/events`,
  `internal/runtime/events`, `internal/session/bridge.go`,
  `internal/daemon/daemon.go`) rather than from what one run happened to
  emit, so kinds a trivial run never fires are still inventoried.
- Live cells used the generic adapter deliberately: `/usr/bin/true` and a
  killed `sleep` traverse the same reap path as a real harness at $0.00,
  established in finding-027.
- **The one paid turn I flagged as possible was cancelled.** I had budgeted a
  trivial headless turn to capture `agent.*` payloads with cost fields
  populated. Unnecessary: the payload structs are authoritative in source,
  and finding-027 plus run-01 already recorded real `agent.session.ended`
  lines with cost, tokens, turns, duration, api time and ttft. Re-observing
  recorded facts would have been waste.
- Ground truth for the live cells came from `tmux list-panes`, `ps`, and pane
  capture, never marvel's rolled-up status.
- Teardown verified: daemon stopped, no marvel tmux servers, scratch removed.

## 8. Out of scope

No fix, no patch, no proposed schema for a payload field, and specifically no
comms-layer proposal — the bus is the conductor's. §6's restatement of the
gap is a measurement consequence, not a design.

**Not produced: the run-registry line** #242 asks for. critic is design-only
— `charter.md`, `docs/run-marker-schema.md`, analysis prose; no code, no
table, `critic/_kos` holds zero nodes. Inventing a registry format would
pre-empt a live design decision (roadmap E1). Flagged on the issue for the
third time; needs either a concrete interim path or the clause dropped until
E1 ships.
