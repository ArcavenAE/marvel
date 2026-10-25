# The marvel watcher: unread mail against the last turn

Proposal, 2026-10-09. Code read at marvel `ff9d210`.

- **Author:** the architect seat, team arcaven, from a six-round design panel. The seats were messaging, observability, runtime and harness; the panel's record is kept outside this repository.
- **The operator's ask:** "marvel builtin watcher, bearing in mind that while marvel bus is native, nats is not, and other agent message queues may be swapped in".
- **Status:** decisions 2 to 4 were ruled on 2026-10-10 (section 8, "Operator rulings"). On the same day the operator ordered the report-only build ("kick off 2"), which is read as settling decision 1, pending the operator's confirmation (section 8). The section 7 plan is filed as flat tickets, with the ring rows held. Builders work red tests first. Revised 2026-10-24 (section 11): the watcher becomes the fleet's one watchdog for unread mail, and section 11 holds the re-check, one definition each of unread, age and ringer, and the mail reader's spec.

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
| **`ContextAt` cannot be an idle signal for a claude seat on the statusline feed.** The statusline refreshes every 15 s while idle. Each tick whose payload carries a context figure sends a heartbeat (a tick without one returns at `cmd/marvel/ctxforward.go:444`, after the account-limits send), and each heartbeat stamps `ContextAt` | `internal/runtime/claude.go` (`refreshInterval: 15`), `cmd/marvel/ctxforward.go:444`, `:453`, `internal/api/store.go:811-823` | read |
| codex's projected hooks (`SessionStart`, `Stop`, `PostToolUse`) leave no turn stamp: the event name is parsed and unused, and a payload with no occupancy sample sends no heartbeat. The account-limits RPC is still sent for every hook, but it carries account windows, not a turn | `internal/runtime/codex_home.go:27`, `cmd/marvel/codexctx.go:63-66`, `:83`, `:133-135` | read |
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

**Packages, and the import boundary.** Today only `internal/bus` imports `github.com/nats-io/...`. The watcher keeps it that way:

- `internal/mail`: `MailState`, `MailReader`, `SeatMail`. No NATS import.
- `internal/mail/mailtest`: the file-spool double.
- `internal/mail/jetstream`: the JetStream adapter. The only new package that imports NATS.
- `internal/turn`: `TurnState`, `TurnReader` and the per-harness dispatch. No NATS import.
- `internal/watcher`: `RunWatcher`, the recorder and the surfaces' data. No NATS import, and it never imports `internal/mail/jetstream`.

The adapter is constructed only in the daemon's start-up wiring (`internal/daemon`, beside where `RunLimits` starts), which already imports `internal/bus`, and handed to `RunWatcher` as a `MailReader`. A1 adds a source test: it lists the dependencies of `internal/mail`, `internal/turn` and `internal/watcher` with `go list -deps` and fails on any `github.com/nats-io/` path or on `internal/mail/jetstream`. Its control is a planted package under `testdata` that imports NATS, which the same check must flag, so a check that reads nothing cannot pass.

```go
// MailState: each field is a value, unknown (tried and failed) or
// unsupported (the queue cannot answer). Never a zero by default.
type MailState struct {
	Depth      Count    // shown; never triggers (redelivery inflates it)
	OldestAge  Duration // the trigger, on the broker's clock (section 11.2)
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

**Ringers.** One ringer per seat, through the shared claim, which every ring writes through `marvel inject --ring` (section 11.2): director's doorbell and team supervisors do not ring inside the window. The claim is per daemon; a seat watched from two daemons is out of scope.

## 6. Measuring claude before trusting it

A probe kit runs on a scratch claude:
- its own tmux socket;
- an empty `HOME` and `CLAUDE_CONFIG_DIR`;
- a throwaway repository;
- a credential supplied for the run, never a fleet seat's.

Hook loggers and a statusline logger append monotonic-stamped lines. A checker writes `probe_result.json` with thirteen keys, `c1` to `c13`, each carrying a name field. The measures column is what the party asked each key to measure, and the probe kit measures each as asked:

| key | name | measures |
|---|---|---|
| `c1` | `pre_session_hooks` | hook events logged at the trust dialog and setup menu (expected none) |
| `c2` | `idle_cost_moves` | whether the statusline cost moved and whether its context moved while idle, with the sample count; the idle window is five minutes. |
| `c3` | `one_submit_one_stop` | every prompt shows exactly one `UserPromptSubmit` and one `Stop` or `StopFailure` |
| `c4` | `ring_form` | for the exact ring form, the `source` value and the submit-to-hook latency in ms (one key, two fields) |
| `c5` | `denial_reason` | the `PermissionDenied.reason` text, and the `Notification.notification_type` seen |
| `c6` | `interrupt_event` | `stop`, `stopfailure` or `none` after Esc mid-turn |
| `c7` | `nonprompt_moves` | with no prompt sent, the count of turn hooks (any is a fail) and the count of statusline changes; the statusline fallback is trusted only while that change count is zero |
| `c8` | `idle_notification_s` | seconds until the `idle_prompt` notification, or `never` |
| `c9` | `hook_stdout_reaches_context` | whether a line a hook prints reaches the session's context, read from the transcript. |
| `c10` | `hook_exit2_effect` | what a hook's exit 2 does to the prompt |
| `c11` | `subagent_stamps` | whether subagent hooks carry `agent_id`, and whether a main turn's `Stop` ever carries one. |
| `c12` | `stop_background` | whether a `Stop` after a backgrounded task carries a non-empty `background_tasks`, and whether a turn then resumes with no prompt. |
| `c13` | `version_visible_to_hook` | whether the hook process can learn the claude version |

The names are the party's (round 4, with `c13` added in round 5). An earlier revision of this section listed twelve measurements for thirteen keys, dropping `c8`.

Hooks become the claude source only if every prompt shows exactly one `UserPromptSubmit` and one `Stop` or `StopFailure`. The result carries the build stamp and is re-run on a newer claude before the stamp is reused.

## 7. Plan (filed as flat tickets on 2026-10-10, after the order to build report-only; G1 to G4 are held until ringing is turned on)

| id | ticket | blocked by |
|---|---|---|
| A1 | `internal/mail`: `MailState` with `Note`, `MailReader`; `internal/mail/mailtest` spool double with error injection; the import-boundary source test with its planted-import control (section 3) | none |
| A2 | Per-seat addresses on both tiers; `SeatMail`, itself a `MailReader`, with the aggregate rules | A1 |
| A3 | `internal/mail/jetstream` adapter: Go read, broker time for age, missing durable is `unknown`; parity fixture against director's reader rows; broker tests exec a scratch `nats-server` and say where CI gets it | A2 |
| B1 | `internal/turn`: `TurnState`, `TurnReader`, and a per-harness dispatch that can say `unsupported` | none |
| B2 | Turn-stamp store on the session record and a daemon RPC for hook commands (event, source, agent id, notification type, background flag), daemon receive time; includes the `codex-ctx` send change | B1 |
| B3 | codex prompt-submit hook and its trust entry, or `started_at: unsupported` | B2, C6 |
| C1 | claude probe kit and checker | none |
| C2 | claude probe run, as an operator-run script with coded PASS/STOP checks; a finding with the build stamp and the ring-to-submit latency | C1 |
| C3 | Project claude hooks beside the statusline feed, keeping the operator's own hooks | C4 |
| C4 | claude hook command: prints nothing, exits zero, bounded timeout | B2, C2 |
| C5 | claude `TurnReader`: open turn, open-turn ceiling, subagent stamps, background tasks, version gate | B1, C3, C4 |
| C6 | codex scratch probe: hook names, `Stop` on interrupt, trust hashes | none |
| D1 | `internal/watcher`: `RunWatcher` core: eligibility, `turn-no-ack`, `turn-ack-unknown`, re-arm on mail movement, capture only for eligible seats | A1, B1 |
| D2 | Watcher liveness: pass stamp, `not-run`, panic recovery, stale mail reads `unknown` | D1 |
| D3 | Edge-triggered recorder keyed `(seat, class)` with `since`; one `resumed` record per held seat after a restart | D1 |
| E1 | Surfaces: event, `describe session`, the pull file; no surface merges before D2 | D2, D3 |
| W1 | Start the watcher from the daemon's start-up wiring, which alone constructs the `internal/mail/jetstream` adapter; mode `off \| report`, printed in `get sessions` | D1, A2, A3 |
| F0 | Where a seat's parent comes from (no lookup exists today) | none |
| F1 | Decision: the director-facing sink for the push | none |
| F2 | Push target selection, director only | D3, B1 |
| F3 | Push delivery with its own state, one push per edge | F2, E1, F1 |
| F4 | Push to the seat's parent when it took a turn in the window, else director | F2, F0 |
| G1 | Ring claim, written before the send; read side for another ringer | D1 |
| G2 | Internal inject entry point recording the watcher as origin | none |
| G3 | The ring, off by default; codex stays unrung while its reader cannot return `Empty` | G1, G2, C5 |
| G4 | codex ring | G3, B3, aae-orc-g88i1 |

The first PRs open the same day, with C1 merged first, because it feeds the longest chain: C1, A1, B1, G2, B2.

**Separate issue, not this design (H1, #801):** the watchdog's quiet test and its did-work clear (`internal/daemon/watchdog.go:262-271`) read `ContextAt`, and so do LAST-ACTIVE (`cmd/marvel/lastactive.go:25`) and ACTIVE% (`internal/team/tickring.go:54`). A claude statusline heartbeat that carries a context figure re-stamps `ContextAt`, every 15 s while the seat idles.

## 8. Decisions for the operator

1. **The design.** The panel recommends, 4 of 4, a report-only first build with the ring held until C2 passes and aae-orc-g88i1 lands. Alternatives:
   - a turn-age-only first step, a strict subset of the plan;
   - director stays the ringer, and marvel's ring is built only if director's doorbell cannot confirm a turn;
   - marvel never rings.

   Picking this design does not turn the ring on. Turning it on is a later operator decision, put up after C2 passes and aae-orc-g88i1 lands; until then G3 ships with the ring off.
2. **claude's fallback turn source, for reports only (split 2-2).** Either the statusline cost or token delta, used only if the probe shows it flat while idle and moving on every turn, or none, reported as `unknown`. Recommendation: decide after C2. Both sides agree it never carries ring authority and falls to `unknown` if the probe fails.
3. **Starting pass interval (split 2-2), 30 s or 60 s.** Recommendation: 60 s, because a JetStream read per seat per pass has a cost and the threshold is minutes.
4. **Starting age threshold (split 2-2), 10 min or 15 min.** Recommendation: 10 min, the single default quiet window `internal/api/quiet.go:5-10` chose on purpose.

Every number here is unmeasured. Report mode measures the interval and the threshold, and C2 measures the confirm window: `max(60 s, 3 x p95 ring-to-submit latency)`, and 120 s until then.

The recommendations in this section were valid until 2026-10-23 or the operator's ruling, whichever came first. They were re-checked on 2026-10-24, in section 11.1.

### Operator rulings (2026-10-10)

Relayed by director from the operator's decision desk. Each ruling quotes the option the operator chose, as it was written on the card.

| # | ruling | the chosen option, as written |
|---|---|---|
| 2, claude's fallback turn source | **probe first** | "Run a short test first: watch an idle Claude seat and see whether the cost and token numbers in its status line stay flat. Decide between the two options below on the result." |
| 2, after the probe (2026-10-10) | **statusline** | "a statusline cost or context change counts as a turn, for reports only, never to restart, ring or route; fall back to unknown if a later test shows misses." The probe result is finding-marvel-so6h (FLAT). |
| 3, starting pass interval | **60 s**, as recommended | "Every 60 seconds." |
| 4, starting age threshold | **10 min**, as recommended | "10 minutes. Matches the quiet window marvel already uses to decide a seat is idle." |

Decision 2 was first ruled probe first; the probe found the figures flat while idle (finding-marvel-so6h), and the operator then ruled "statusline", in the second row. The probe observes a live idle seat by capture only. It is separate from the C2 scratch kit in section 6, which still gates any ring authority. Turning the ring on remains a later operator decision, put up after C2 passes and aae-orc-g88i1 lands.

**Decision 1, read from an order (pending the operator's confirmation).** On 2026-10-10 the operator ordered the report-only build: "kick off 2". That order is read as settling decision 1, pending the operator's confirmation through director. The build it starts is report-only, with no ringing yet. Checks run every 60 s, unread mail older than 10 min is reported, and a statusline cost or context change counts as a turn for reports only, never to restart, ring or route. The section 7 plan was filed from it.

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

## 11. Revision of 2026-10-24: one fleet watchdog for unread mail

Code re-read at marvel `7c4a3d3e` and director `1b31eba`; director#399 read at `b7f8a187`. The operator, relayed by director on 2026-10-24: the watcher becomes the one fleet watchdog for unread mail, and the architect owns the design. The 10-10 rulings stand: a 60 s pass, a 10 min threshold, the statusline as claude's turn source, report-only, ring held.

### 11.1 Re-check of the recommendations

The section 8 recommendations were valid until 2026-10-23. Each is re-checked against what changed since 10-09.

| recommendation | condition then | condition now (checked) | action |
|---|---|---|---|
| Decision 1: report-only first build, ring held until C2 passes and aae-orc-g88i1 lands | C2 not run; codex reader cannot return `Empty` | C2 (aae-orc-f8nut) still open, never run; aae-orc-g88i1 still open (updated 10-20); C1 is done, marvel#844 and #849 (aae-orc-2f0c8 closed today with that evidence) | **renew** |
| Decision 2: decide claude's fallback after C2 | no statusline turn stamp existed | ruled "statusline" on 10-10; built as `TotalsChangedAt`, the stamp LAST-TURN reads (`cmd/marvel/lastturn.go`, #979), which a heartbeat or a context reading never moves | **superseded by the ruling**. B1 and C5 read `TotalsChangedAt` as the report-mode claude source, `turn.source = statusline`; the hook source stays gated on C2 |
| Confirm window 120 s until C2 measures it | no measurement | no measurement | **renew** |
| Section 5, "Ringers": director's doorbell reads the claim | one other ringer, director | three ringers: director's doorbell (R-194, R-200), each team supervisor ringing its own team (the 2026-10-24 multi-cluster outcome), and the held watcher ring | **revise**, as 11.2 |
| Section 5, "the claim is per daemon" | one daemon per seat | unchanged: a seat runs under one daemon, and a ringer on another cluster reaches it through that daemon | **renew** |
| H1 (#801), `ContextAt` re-stamped while idle | open | open | **renew**, still a separate issue |
| A claude seat can be read by the composer reader | labelled rule not matched (finding of 2026-10-24) | `ruleLine` now accepts one label in the rule (`internal/composer/claude.go`) | **no longer a limit** for the claude reader; verify the kinu panes after the next deploy |

Still true at `7c4a3d3e`: marvel reads no mail (no `NumPending` or ack floor read in non-test Go), only `internal/bus` imports `github.com/nats-io/`, interactive seats have no stream (`SupportsStream`), the statusline refreshes every 15 s (`internal/runtime/claude.go:64`), and inject verifies after the send (`internal/daemon/daemon.go:2571`). No ring claim exists in the inject path (`injectParams`, `daemon.go:2345`).

These recommendations are valid until 2026-11-07, or until C2 runs, whichever comes first. The arcaven architect re-checks them then.

### 11.2 One definition each

The fleet has three readings of "unread" and three ringers today. Each gets one definition.

- **Unread** is mail not acked, read per durable. Each live durable is read from its own `AckFloor.Stream`: its unread mail is every message on its filter subjects above its floor that it has not acked, and its depth is `NumPending + NumAckPending`. A message read in handled mode and not yet acked is unread. Its oldest unread message is the first one after its floor on its filter subjects. That message is never acked, because on nats-server the floor moves past other seats' sequences up to the oldest held message (the arcaven reviewer measured this on nats-server v2.15.0 for marvel#1056: floor 2 with the held message at 3). So an ack out of stream order moves the depth and leaves the oldest where it is, and the two statements agree. A seat's depth and oldest age are the maximum over its live durables. Live durables are never merged into one floor: two live sessions of one seat each get their own copy of the mail (R-50), and one session's acks say nothing about the other's.
- **The inherited floor** is the highest `AckFloor.Stream` over the seat's departed durables with the same filter subjects, which is where a successor resumes (director `bus.go`, `seatAckFloor`, which skips live instances for the reason above). It is read only for a seat with no live durable, and it is what such a seat reports as unread. `LastAckAt` is the latest `AckFloor.Last` over the durables that were read.
- **Age** is on the broker's clock: the consumer info's `TimeStamp`, which the server stamps when it answers, minus the stored time of the oldest unread message. Both times come from the server, so no host clock enters. A server that leaves `TimeStamp` zero gives an age on the reader's clock, and the state's `Note` says so.
- **The ringer** is marvel's ring claim, one per seat. Anyone may ring, but every ring goes through `marvel inject --ring`, which writes the claim before the send and refuses a second ring inside the window with the claim's holder and time. The watcher's own ring stays held. Building the claim rings nothing new; it makes the existing rings visible to each other.

What changes for each source today:

| source | reads today | change |
|---|---|---|
| marvel watcher (A1 to A3) | nothing | built to 11.2 from the start (11.3) |
| `director-mcp unread` (`cmd/director-mcp/unread.go`) | ack floor and `NumPending + NumAckPending` (matches), age as reader-host `now` minus stored time | age from the consumer info's `TimeStamp`; until then the parity fixture compares ages within a tolerance |
| director#399, presence `oldest_pending_s` and `stalled` | `NumPending` only, aged by samples on the shim's clock | the count follows the ack floor; the clock may stay the shim's (11.5) |
| director's doorbell (R-194, R-200) | its own captures, then inject | rings through `marvel inject --ring`; R-194's checks and R-200's follow-up are unchanged |
| team supervisors ringing their own team | `marvel inject` with no claim | ring through `marvel inject --ring`; a refusal inside the window means the seat was just rung, and the supervisor does not retry |
| lane-ledger's unread column | nothing; always GAP | reads 11.4's file |

### 11.3 The mail reader (A1 to A3)

All three are built on the mokuzai marvel lane, because marvel is not pushed from kinu. Broker tests run against a scratch loopback `nats-server` only, never a fleet broker.

**A1, `internal/mail`.**
- `Count`, `Duration`, `Time` and `Seq` each carry a value or a state: `unknown` (tried and failed), `unsupported` (the queue cannot answer), and, for `Time` only, `never`. The zero value of each is `unknown`, so an unset field never reads as zero.
- `MailState` holds the section 3 fields, plus `AckPending Count`, `AckFloor Seq` and `OldestSeq Seq`, so the parity fixture can compare them.
- `MailReader` is as in section 3.
- The spool double in `internal/mail/mailtest` keeps one directory per address, with one file per message (sequence and stored time), a floor file and an ack-time file. It can inject an error per call or `unknown` and `unsupported` per field.
- The import-boundary test is as in section 3, with its planted-import control.
- Red tests first:
  - the zero `MailState` reads `unknown` in every field;
  - the spool reports depth, oldest age and last ack;
  - an injected error returns an error and the state reads `unknown`;
  - the boundary test flags the planted package.

**A2, seat addresses and `SeatMail`.**
- **The local tier.** The seat's durables on `AGENT_INBOX` are the ones named `mcp_<agent id>_<instance>`. The agent id is split from the instance at the last `_`, as director does (`unread.go`). Their filter subjects are `agent.<workspace>.<team>.<id>.inbox`, plus `agent.<workspace>.<team>.role.<role>.inbox` when a role is held.
- **The global tier.** It exists only for a seat with a global address. That is a cluster supervisor (`global.<cluster>.supervisor.inbox` on `GLOBAL_TO_<cluster>`) or director (`GLOBAL_TO_DIRECTOR`), with durables named `mcp_global_<agent id>_<instance>`. Any other seat's global tier reads `unsupported`, with the note "no global address", and is not counted.
- **The aggregate.** Each live durable is read from its own floor and reported on its own, as `unread.go` reports per durable. Within a tier, the seat's depth and oldest age are the maximum over its live durables, never a sum and never one merged floor (11.2). Across tiers, depth is the sum and the oldest age is the maximum. `LastAckAt` is the latest. Any `unknown` tier makes the whole `unknown`.
- **A durable with a different filter set** belongs to another seat sharing the id prefix and is skipped, as `seatAckFloor` skips it.
- **A seat with no live durable.** Its unread mail is still counted from 11.2's inherited floor, because that is what a successor inherits (the 83-message case in section 0). The departed durable that holds that floor supplies the depth and the oldest message. A seat with no durable at all inherits floor 0: a successor would read everything the inbox holds on its subjects, so depth is the stream's message count on those subjects (`STREAM.INFO` with a subjects filter) and the oldest is the first message on them. The note says "no live durable" or "no durable".
- Red tests (on the spool):
  - a successor whose departed predecessor acked further reads from the predecessor's floor;
  - two live durables of one seat, one acked ahead: the other's mail still counts, and the seat reports the larger depth and the older age;
  - a live durable and a departed one acked further: the live durable is read from its own floor, not the departed one's;
  - role mail on the same durable is counted once;
  - a durable with other filters is skipped;
  - one unknown tier makes the seat unknown;
  - a seat with no global address reads that tier `unsupported` and still reports the local tier.

**A3, `internal/mail/jetstream`.**
- **Reads.** Each tier is read with `ListConsumers` info on the stream, filtered to the seat's durables. For each durable read (each live one, or the one holding the inherited floor), the oldest unread message is read with `GetMsg(start, WithGetMsgSubject(subj))` per filter subject. `start` is that durable's own floor plus one, and never before a resumed durable's `OptStartSeq`: the same reads `unread.go` makes. Age is 11.2's broker-clock age.
- **Bounds.** Each tier's reads are bounded (500 ms, as director#399 bounds its own). A denied or timed-out read makes that tier `unknown`, with the error in `Note`, and never zero.
- **The credential this needs** has not been checked here. On each inbox stream it needs `STREAM.INFO`, `CONSUMER.LIST` and `CONSUMER.INFO`, plus `STREAM.MSG.GET` or `DIRECT.GET`. Director#399 records that the hub template grants a leaf `STREAM.MSG.GET` only on the presence bucket. So the global tier's oldest message may need a grant. The builder lists the subjects the daemon's credential is denied in its scratch broker, and the PR names any grant the fleet needs. Until it is granted, the global tier reads `unknown`, never a guess.
- **The parity fixture.** One script builds a scratch broker state, and the same state is read twice:
  - once by `director-mcp unread --json` (and `--global`), which writes a golden kept in marvel `testdata` with the director commit it came from;
  - once by the adapter.

  The cases:
  - one live durable;
  - two live durables of one seat, one acked ahead;
  - a successor resumed past a departed durable;
  - a seat with no live durable;
  - role mail;
  - an ack-pending message (handled mode);
  - an ack out of stream order: a later message acked while an earlier one is held, so depth falls and the oldest stays the held message;
  - a Nak'd redelivery;
  - the global tier;
  - nothing unread.

  For each durable, these fields compare exactly: pending, ack pending, ack floor, oldest sequence and oldest stored time. The seat's maximum is then compared with the watcher's seat state. Age compares within 2 s until the director ticket in 11.6 moves `unread.go` to the broker clock, then exactly. Director's per-durable states (`reading`, `behind`, `durable-idle`, `reads-outside-durable`) are not compared; the watcher has no such classes.

### 11.4 lane-ledger's unread column (S11)

`skills/director/scripts/lane-ledger:55` prints GAP with "marvel exposes consumer pending per seat (S11)". No surviving record defines S11. The label appears only there and in the test that pins it. By its words, it names a per-seat count of JetStream consumer pending, with its age. That is 11.2's unread.

Director's reader already ships (`director-mcp unread --json`, slice M), so it is the reader that ships first. The spec:
- freebusy, on its sweep, writes `$DIRECTOR_STATE/freebusy/unread.jsonl` from `director-mcp unread --json` and `--global`, one row per seat. A row carries `seat`, `tier`, `pending`, `oldest_unread_age_s`, `ack_floor`, `source` (`director-mcp unread <rev>`) and `observed_at`.
- lane-ledger reads that file, as it reads `seats.jsonl`. A lane's cell is the sum of pending and the oldest age over the lane's seats, with the file's `observed_at` as its as-of.
- A missing or unreadable file prints GAP with a new fix text naming the file, never 0. A seat row with an unknown read prints `?`.
- When the watcher's pull file (E1) ships, freebusy writes the same rows from it, with `source = marvel watcher`. lane-ledger does not change.
- `verify-lane-ledger.sh` gets a fixture with the file present, one with it absent, and one row unknown.

### 11.5 director#399 against 11.2

#399 counts `NumPending` only, so a message read in handled mode and held does not age toward `stalled`. Its body calls that deliberate: "Stalled means not drained." Under 11.2, that held message is unread. Two numbers named for the oldest pending mail of one seat would then disagree exactly for handled-mode seats. And `oldest_pending_s` is the number the multi-cluster outcome tells senders and supervisors to read.

- **The count should align before merge.** The tracker counts `NumPending + NumAckPending`. Both are in the consumer info #399 already reads, so no new grant is needed. The bus test that an ack-pending and a Nak'd message are not counted flips, and the reason text says "unacked" where the oldest message is ack-pending.
- **The tracker pops by the ack floor, never by a fall in the count.** With the aligned count, a fall can be an ack out of stream order (handled mode acks the message the seat names), and popping the front batch on a fall hands a held message a younger stamp: a message held 3000 s read 3m20s in the reviewer's probe on #399 (review 5640723021). A batch leaves only once `AckFloor.Stream` has passed it, which matches 11.2's oldest unread, the first message after the floor. Undelivered mail still leaves in stream order, so while nothing is held ack-pending the front may be popped to the count.
- **The clock need not align.** #399's age is inferred from its own samples on the shim's clock, and never compares two hosts' clocks. Reading the broker's stamp of the oldest message would need `STREAM.MSG.GET` on the inbox streams, which the hub template does not grant a leaf (#399's own round 2). Its bound, with batches stamped at the previous sample and popped by the floor: the age does not read low after a tier's first sample. It reads high by at most one sample while nothing is held. While mail is held it can read high for longer, because a batch stays until the floor passes all of it. The doc comment states that bound, and the watcher's broker-clock age is the reference.

### 11.6 Tickets

- **A1 to A3** (aae-orc-f3usp, aae-orc-dehyh, aae-orc-i9tw9) carry 11.3 as their spec.
- **G1a (aae-orc-svz6j), the ring claim in the inject path.** `marvel inject --ring` writes `last_ring_at`, the ringer and `ring_result = pending` before the send. Inside the window it refuses, with the holder and time. The claim reads in `describe session`. G1 (aae-orc-2y4mi) now depends on it and becomes the watcher's use of it. Building G1a turns on no watcher ring, so it sits outside the ring hold, pending director's confirmation of 11.2.
- **Director: `unread.go` ages on the broker clock** (aae-orc-f7fq8, 11.2).
- **Director: lane-ledger's unread column** (aae-orc-p9vdn, 11.4).
- **Director: the doorbell and supervisor rings use `marvel inject --ring`** (aae-orc-a2pk9), blocked by G1a.
- **The #399 changes** (the aligned count, and popping by the ack floor) are not filed as tickets. They went to #399 as review notes, and #399 carries both.
