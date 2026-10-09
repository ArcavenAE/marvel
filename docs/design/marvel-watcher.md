# The marvel watcher: unread mail against the last turn

Proposal, 2026-10-09. Code read at marvel `ff9d210`.

- **Author:** the architect seat, team arcaven, from a six-round design panel. The seats were messaging, observability, runtime and harness; the panel's record is kept outside this repository.
- **The operator's ask:** "marvel builtin watcher, bearing in mind that while marvel bus is native, nats is not, and other agent message queues may be swapped in".
- **Status:** the operator picks the design (section 8) before any ticket is filed. Builders then work red tests first.

## 0. Why

A seat can sit idle while holding work mail, and nothing marvel prints says so. On 2026-10-09 a supervisor took no turn for about 17 hours while holding an ask, and a successor inherited 83 unread messages. Director's requirements already name the gap: R-117 (unread depth and age; an idle seat with mail gets a doorbell), R-194 (a doorbell is a checked action), and R-195 (activity state), all in `sim/requirements.md`. R-200 (a ring followed to a first turn and an acknowledgement, otherwise the blocker) is proposed in director#273.

The subject test for what belongs in marvel is: a feature belongs here only if, without it, a surface marvel already prints would claim something marvel cannot back. HEALTH is liveness, so a seat that has ignored its mail for hours prints healthy. The watcher is the part that lets marvel stop making that claim.

## 1. Premises, checked at `ff9d210`

| claim | where | checked |
|---|---|---|
| marvel reads no mail today | `git grep -i 'unread\|NumPending'` over non-test Go | read: no mail or consumer-state read |
| The message bus is a registry entry with no read contract: class `message-bus`, one provider `nats-server` | `internal/service/service.go:104-119` | read |
| **Interactive seats produce no turn events.** The stream parsers emit `turn.started` and `turn.completed`, but only headless launches have a stream | `internal/runtime/claude.go:27-31`, `codex.go:28-33` (`SupportsStream` is `Mode == headless`) | read |
| claude's stream emits `turn.completed` and no `turn.started` | `internal/runtime/claudecode/mapping.md:39` | read |
| **`ContextAt` cannot be an idle signal for a claude seat on the statusline feed.** The statusline refreshes every 15 s while idle; each tick sends a heartbeat, and each heartbeat stamps `ContextAt` | `internal/runtime/claude.go` (`refreshInterval: 15`), `cmd/marvel/ctxforward.go:453`, `internal/api/store.go:811-823` | read |
| codex's projected hooks (`SessionStart`, `Stop`, `PostToolUse`) leave no turn stamp: the event name is parsed and unused, and a payload with no occupancy sample is not sent | `internal/runtime/codex_home.go:27`, `cmd/marvel/codexctx.go:63-66`, `:83`, `:135` | read |
| The codex composer reader returns only `MenuUnsafe` or `Unknown`; any runtime other than claude and codex gets a reader that is always `Unknown` | `internal/composer/composer.go:54-66`, `:95-112` (aae-orc-g88i1, open) | read |
| An empty composer does not mean idle: it can read empty while text streams | `internal/composer/composer.go:24-26` | read |
| The inject path checks before sending and verifies after | `internal/daemon/daemon.go:2257` (`handleInjectAs`), `:2460` (`verifyInject`) | read |
| Sibling loops run beside the reconcile tick | `internal/daemon/limits.go:70-73` (`RunLimits`), `daemon.go:668`, `:676` | read |
| marvel has no in-process NATS server module | `go.mod` (`nats.go`, `nkeys`, `nuid` only) | read |
| claude 2.1.295 declares `UserPromptSubmit`, `Stop`, `StopFailure`, `PermissionDenied` and `Notification` hooks | strings in the installed binary | read, never observed firing; the probe in section 6 measures it |

## 2. What the watcher is

A daemon goroutine, `RunWatcher`, runs beside `RunLimits` and never inside the reconcile pass. Each pass handles each interactive seat:

1. Read mail through `MailReader`.
2. Read turn evidence through `TurnReader`.
3. Decide whether the seat is **eligible**: its oldest unread is older than the threshold, it has a known turn source, and no turn has started since the later of that mail's arrival and the last ring.
4. For an eligible seat only, capture the pane and read the composer.
5. Write a record when a `(seat, class)` condition first holds, and another when it clears.
6. In a later phase only, ring (section 5).

When a pass has not completed within three intervals, every output reads `not-run`. A stale mail read reads `unknown`, never its last value. `get sessions` prints the watcher's mode.

## 3. Interfaces

```go
// MailState: each field is a value, unknown (tried and failed) or
// unsupported (the queue cannot answer). Never a zero by default.
type MailState struct {
	Depth      Count    // shown; never triggers (redelivery inflates it)
	OldestAge  Duration // the trigger, measured on the broker's clock
	LastAckAt  Time     // a value, never, unknown or unsupported
	ObservedAt time.Time
	Source     string   // the adapter
	Note       string   // the adapter's own note, e.g. "reads outside durable"
}
type MailReader interface {
	Read(ctx context.Context, seat api.Session) (MailState, error)
}

type TurnState struct {
	StartedAt Time // a value, unknown or unsupported
	EndedAt   Time
	Source    string // "hook:UserPromptSubmit", "hook:Stop", "none", ...
}
type TurnReader interface {
	LastTurn(ctx context.Context, s api.Session) (TurnState, error)
}
```

- **The mail half sits behind an adapter.** A per-seat aggregate covers every address on both tiers: the oldest age is the maximum, the depth is the sum, and any `unknown` makes the whole `unknown`. The first adapter is a Go read of JetStream consumer state, the same read director's `probe/nats-phase-0/director-mcp/unread.go` performs. It is not a call into director, so marvel keeps working without director installed. A file-spool double in `internal/mail/mailtest` shows that a non-NATS queue fits the contract. Any queue needs three reads: the unread count for an address, the oldest unread time and the last ack time.
- **The turn half is per harness.** It is never derived from the queue: a queue can say what was delivered and acknowledged, never what was acted on. `last_ack_at` is never turn evidence.
  - **claude:** hooks marvel would project, `UserPromptSubmit` and `Stop`, through a hook command that prints nothing. They are trusted only after the section 6 probe passes, and only inside the measured version range. Outside that range the seat reads `unobserved (version-unmeasured)`. Feed liveness comes from `LastHeartbeat`, never from `ContextAt`.
  - **codex:** a prompt-submit hook, if codex has one (to be measured). Until then `started_at` is `unsupported`.
  - **opencode:** `unsupported`.

## 4. The report

A record holds:
- the seat;
- `observed_at`;
- the mail and turn states;
- the composer state;
- `blocker_class`: `dialog`, `denial`, `no-tool`, `no-network`, `no-turn`, `turn-no-ack`, `turn-ack-unknown` or `unknown`;
- `since`.

Captured pane text is kept only for `dialog` and `denial`, truncated and masked.

- **Where it goes first:** a marvel event, a `describe session` field, and a pull file written by rename. The file reads `not-run since T` when it is stale, and can be read with no marvel CLI (marvel#581).
- **Push target:** the seat's parent, but only if that parent itself took a turn in the window. A parent whose turn state is `unknown` counts as stalled. Otherwise the record goes to director. It never goes to the stalled seat.
- **Push state:** each push records its own state (`pushed`, `failed` or `pending`), with one push per edge.
- **Scope:** the watcher does nothing past ringing and reporting. It never shifts, kills or reassigns.

## 5. The ring (held in the first build)

**When a ring is allowed:**
- the composer reads `Empty`;
- no turn is open (a turn start later than the last turn end);
- the seat is not covered by an open-turn ceiling;
- no ring claim exists inside the window.

**How it is sent:**
- The claim (`last_ring_at`, `ring_result = pending`) is written before the send.
- The send goes through the inject path, with the watcher recorded as its origin.
- The text is fixed and short. Enter, as `inject <key> '' -e`, is pressed only when a capture shows the composer holds exactly that text.

**After the send:**
- One ring per window.
- A turn with no mail movement writes `turn-no-ack` and never rings again. The seat is re-armed only when its mail moves.
- A `pending` claim older than the confirm window after a restart reads as rung.

**What is never rung:** `HoldsText`, `Unknown`, `MenuUnsafe` and `Shell` never ring, and neither does a seat whose turn source is `unknown`.

Two limits follow from the code as it stands:
- claude is the only harness that can be rung, until codex's composer reader can return `Empty` (aae-orc-g88i1).
- The window between the capture and the Enter is accepted and named. It is not called safe.

**Ringers.** One ringer per seat, through the shared claim: director's doorbell reads it and does not ring inside the window. The claim is per daemon; a seat watched from two daemons is out of scope.

## 6. Measuring claude before trusting it

A probe kit runs on a scratch claude:
- its own tmux socket;
- an empty `HOME` and `CLAUDE_CONFIG_DIR`;
- a throwaway repository;
- a credential supplied for the run, never a fleet seat's.

Hook loggers and a statusline logger append monotonic-stamped lines. A checker writes `probe_result.json` with keys `c1` to `c13`, covering:
- hooks at the trust dialog;
- cost movement while idle;
- one submit and one stop per prompt;
- the ring form's source and latency;
- denial text;
- interrupt behavior;
- movement with no prompt;
- whether hook stdout reaches the context;
- a nonzero exit's effect;
- subagent stamps;
- background tasks;
- whether a hook can see the version.

Hooks become the claude source only if every prompt shows exactly one `UserPromptSubmit` and one `Stop` or `StopFailure`. The result carries the build stamp and is re-run on a newer claude before the stamp is reused.

## 7. Plan (filed as flat tickets after the operator's pick)

| id | ticket | blocked by |
|---|---|---|
| A1 | `internal/mail`: `MailState` with `Note`, `MailReader`; `mailtest` spool double with error injection | none |
| A2 | Per-seat addresses on both tiers; `SeatMail`, itself a `MailReader`, with the aggregate rules | A1 |
| A3 | JetStream adapter: Go read, broker time for age, missing durable is `unknown`; parity fixture against director's reader rows; broker tests exec a scratch `nats-server` and say where CI gets it | A2 |
| B1 | `TurnState`, `TurnReader`, and a per-harness dispatch that can say `unsupported` | none |
| B2 | Turn-stamp store on the session record and a daemon RPC for hook commands (event, source, agent id, notification type, background flag), daemon receive time; includes the `codex-ctx` send change | B1 |
| B3 | codex prompt-submit hook and its trust entry, or `started_at: unsupported` | B2, C6 |
| C1 | claude probe kit and checker | none |
| C2 | claude probe run, as an operator-run script with coded PASS/STOP checks; a finding with the build stamp and the ring-to-submit latency | C1 |
| C3 | Project claude hooks beside the statusline feed, keeping the operator's own hooks | C4 |
| C4 | claude hook command: prints nothing, exits zero, bounded timeout | B2, C2 |
| C5 | claude `TurnReader`: open turn, open-turn ceiling, subagent stamps, background tasks, version gate | B1, C3, C4 |
| C6 | codex scratch probe: hook names, `Stop` on interrupt, trust hashes | none |
| D1 | `RunWatcher` core: eligibility, `turn-no-ack`, `turn-ack-unknown`, re-arm on mail movement, capture only for eligible seats | A1, B1 |
| D2 | Watcher liveness: pass stamp, `not-run`, panic recovery, stale mail reads `unknown` | D1 |
| D3 | Edge-triggered recorder keyed `(seat, class)` with `since`; one `resumed` record per held seat after a restart | D1 |
| E1 | Surfaces: event, `describe session`, the pull file; no surface merges before D2 | D2, D3 |
| W1 | Start the watcher in the daemon with mode `off | report`, printed in `get sessions` | D1, A2, A3 |
| F0 | Where a seat's parent comes from (no lookup exists today) | none |
| F1 | Decision: the director-facing sink for the push | none |
| F2 | Push target selection; director until F0 lands | D3, B1, F0 |
| F3 | Push delivery with its own state, one push per edge | F2, E1, F1 |
| G1 | Ring claim, written before the send; read side for another ringer | D1 |
| G2 | Internal inject entry point recording the watcher as origin | none |
| G3 | The ring, off by default; codex stays unrung while its reader cannot return `Empty` | G1, G2, C5 |
| G4 | codex ring | G3, B3, aae-orc-g88i1 |

The first PRs open the same day, with C1 merged first, because it feeds the longest chain: C1, A1, B1, G2, B2.

**Separate issue, not this design (H1):** the watchdog's quiet test and its did-work clear (`internal/daemon/watchdog.go:262-271`) read `ContextAt`, and so does LAST-ACTIVE (`cmd/marvel/lastactive.go:25`). A claude statusline heartbeat re-stamps `ContextAt` every 15 s.

## 8. Decisions for the operator

1. **The design.** The panel recommends, 4 of 4, a report-only first build with the ring held until C2 passes and aae-orc-g88i1 lands. Alternatives:
   - a turn-age-only first step, a strict subset of the plan;
   - director stays the ringer, and marvel's ring is built only if director's doorbell cannot confirm a turn;
   - marvel never rings.
2. **claude's fallback turn source, for reports only (split 2-2).** Either the statusline cost or token delta, used only if the probe shows it flat while idle and moving on every turn, or none, reported as `unknown`. Recommendation: decide after C2. Both sides agree it never carries ring authority and falls to `unknown` if the probe fails.
3. **Starting pass interval (split 2-2), 30 s or 60 s.** Recommendation: 60 s, because a JetStream read per seat per pass has a cost and the threshold is minutes.
4. **Starting age threshold (split 2-2), 10 min or 15 min.** Recommendation: 10 min, the single default quiet window `internal/api/quiet.go:5-10` chose on purpose.

Every number here is unmeasured. Report mode measures the interval and the threshold, and C2 measures the confirm window: `max(60 s, 3 x p95 ring-to-submit latency)`, and 120 s until then.

The recommendations in this section are valid until 2026-10-23 or the operator's ruling, whichever comes first; the architect re-checks them then.

## 9. For the capability register

A sibling design, the capability register, reads the watcher's per-seat state.

**Fields it may read:**
- `turn.started_at`, `turn.ended_at`, `turn.source`;
- `composer.state` with its reader and version range;
- `mail.oldest_age`, `mail.last_ack_at`, `mail.source` and its note;
- `ring.supported` and `turn.supported`;
- `blocker_class` with `since`;
- the watcher's mode and `last_pass_at`.

Each carries `observed_at`.

**What it must not read:**
- captured text;
- the ring claim;
- `mail.depth` as health;
- `no-tool` or `no-network` as a standing capability.

## 10. Prior art

- marvel#554, `docs/design/harness-state-watchdog-p1.md` and `docs/design/watchdog-control-and-uncovered.md`: the pane classifier, positive control, and the uncovered state.
- `docs/design/capture-escapes-composer-state.md`: the composer reader.
- marvel#341 (a bare Enter sent as literal text), #343 (inject truncation), #371 (trust dialog), #580 (a leaf link down tells no supervisor), #581 (supervisors with no marvel CLI).
- director: `sim/design/unread-slice-m.md`, `probe/nats-phase-0/director-mcp/unread.go`.
