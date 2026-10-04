# Per-seat replacement for automatic shifts (marvel#452)

- **Status:** design for review, 2026-10-04. No code lands until this is
  reviewed and its rulings are made. Tracks marvel#452. Run as 3ptdd (a
  three-round party, then the standard lifecycle); the party record is in
  section 9.
- **Amended 2026-10-04 on the operator's rulings** (section 7). Ruling 1
  was reversed from its default: context pressure never replaces a seat
  without an observed handoff. D7, D8, D9, section 4, the tests for ticket 4
  and the plan changed to match; ruling 2 stands.
- **Checked at:** marvel main e1dcaa5. Every line reference below is at that
  commit.
- **Scope:** both automatic triggers (`max-age`, `context-pressure`) replace
  only the seat that crossed, on a role of any replica count. Operator
  `marvel shift` keeps its role-scoped meaning.

## 1. Why

On a role with more than one replica, an automatic shift drains seats that
were never asked. One seat crosses, the whole role rotates, and a sibling
with live work is deleted with no handoff. That is the unwatched shift the
succession contract forbids ("never auto-shift unwatched",
`shift-trigger-list.md` D5).

#458 closed the max-age door with a refusal at apply and at scale. Context
pressure has no such guard, and it is live: `aae/arcaven` role `architect`
runs 2 replicas with `context-pressure` (`marvel describe team` on kinu,
2026-10-04, marvel b533ca5). No other multi-replica role in the fleet
declares a shift.

## 2. Today

| # | Fact | Where |
|---|---|---|
| F1 | The handoff request is keyed by role: one pending request per role | `internal/team/shift_handoff.go:104`, `:113` |
| F2 | `autoShift(t, role, sess, msg)` starts a role-scoped shift; `sess` reaches only the event | `internal/team/controller.go:2021-2037` |
| F3 | A shift launches `role.Replicas` successors and pairs each with a live predecessor, oldest first | `controller.go:2196-2232` |
| F4 | `shiftDrain` deletes every seat of the role outside the new generation | `controller.go:2343-2408` |
| F5 | A shift is team-wide state: one `t.Shift`, one generation bump, `maxAutoShiftsPerTick = 1` fleet-wide; the shifting role gets no steady-state repair while it shifts | `controller.go:1809-1899`, `:526`, `:2088-2094` |
| F6 | Context pressure picks its seat at the team generation only, the #451 class on the other trigger | `controller.go:2044` |
| F7 | Context pressure shifts at once with no observed handoff | `controller.go:1984-1996`; `shift-trigger-list.md` D5, last paragraph |
| F8 | Steady-state reconcile counts a role's seats at every generation and sheds the OLDEST on a surplus | `controller.go:1097`, `:1216-1233` |
| F9 | `Create` stores the session row before the pane exists | `internal/session/manager.go:562` |
| F10 | A failed kill keeps the row `Failed` with `KillError`; it holds its index and no slot | `manager.go:1326-1349` |
| F11 | `admitShift` asks for the sum of `Replicas` over the shifted roles | `internal/team/admission.go:189-196` |
| F12 | Events carry Session, Generation (the team's), Message; no cause or peer field | `internal/events/events.go`, `type Event` |
| F13 | #458 refuses max age at apply and at scale when `replicas > 1` | `internal/api/manifest_shift.go:178`; `internal/daemon/daemon.go:1589-1594` |

F8 is the reason this design does not reuse the shift machinery naively: any
path that leaves three live seats on a two-replica role hands the reconciler
a surplus, and it deletes the oldest seat, which can be the sibling.

## 3. Decisions

### D1. Replace one session, outside `t.Shift`

An automatic trigger replaces the seat that crossed. It does not start a
shift. The replacement is not a seat-scoped variant of the shift (a party
proposal voted down): a shift selects by "outside the new generation"
(`controller.go:2209`, `:2350`), which names the sibling by construction,
and it holds team-wide state (F5) to move one process.

A replacement creates a successor at the predecessor's own generation, with
`Predecessor` set and a `nextIndex` index, waits until it is `allReady`, then
deletes the predecessor. No team generation bump. Naming stays
`<team>-<role>-g<gen>-<idx>`; after this design the generation in a name
records the last role shift, not the seat's age, and nothing should parse it
for lineage.

### D2. The entry fences every step

A durable team field holds one entry per replacement:

```
Replacements[predecessorKey] = {successor, cause, startedAt, reason}
```

The order is fixed: write the entry, with the successor name already
chosen, then `Create`, then delete the predecessor once the successor is
ready. A daemon restart re-derives every step from the entry. Without this
order a crash between `Create` (F9) and the entry write leaves a third,
unpaired seat, and F8 deletes the oldest.

- The successor name is computed once and stored. If it collides with a
  `Failed` row kept by a failed kill (F10), choose the next free index and
  store that.
- The entry clears only when the predecessor row is gone. A failed kill is
  retried each tick.
- A write for a key that already has an entry is refused, never an
  overwrite. An overwrite would leave the first successor with no entry: the
  unpaired third seat this section exists to prevent.

### D3. The pair is one slot

While an entry stands, `planRole` collapses the predecessor and its
successor into one replica slot before `actual` is computed, and neither
row is a delete candidate. The pair still counts as one slot when the
successor row is absent, so plain reconcile never spawns for it. The
collapse is per entry: two entries in one role are two slots.

D3 and D5 are one invariant: a standing entry means the role's row count is
not its replica count. Every path that counts or deletes a role's seats
(`planRole`, scale, abort, health restart) consults the entries.

### D4. Mid-replace

- **Predecessor gone** (health restart, crash) with a ready successor: the
  replacement is complete, emitted as `shift-seat-drained` with reason
  `seat-gone`. The predecessor is not recreated.
- **Successor gone before ready:** the replacement tick recreates it with
  `Predecessor` set, under the same deadline (D6). Plain reconcile does not
  spawn for it.

### D5. Mutual exclusion

- No replacement starts while `t.Shift` is in flight.
- No team shift starts while a replacement stands
  (`initiateShiftLocked` would drain the pair, `controller.go:2350`).
- `scale` is refused while a replacement stands. The refusal keys on the
  entries, not on `t.Shift` (`daemon.go:1556-1558` reads only the shift).
- Each refusal names the standing replacement.

### D6. Deadline

A replacement standing past the shift timeout (`defaultShiftTimeout`, 10m,
`controller.go:143-150`) deletes the successor, keeps the predecessor, clears
the entry and emits `shift-replace-aborted` (warning). This also bounds D4's
recreation of a crash-looping successor.

### D7. Concurrency

- At most one replacement in flight per role. A second seat that crosses
  keeps its request, pending and visible in `describe team`.
- **Exception:** a context-pressure replacement (D8) may start while one
  other replacement of the same role stands, a cap of two, with an event
  saying so. Its seat has written its handoff close to auto-compaction, and
  it does not queue behind a healthy one.
- Starts count against `maxAutoShiftsPerTick`. Admission asks for one seat
  (`Want: 1`), not the role's replica count (F11).
- **A seat already in a replacement is never selected again.** Trigger
  selection, for both triggers, skips any seat that is the predecessor or the
  successor of a standing entry. Without this, a predecessor still over
  `headroom_tokens` on a later tick would start a second replacement for
  itself, and its own standing entry would count as the "one other".
- The per-role count check, the selection and the entry write happen in one
  pass under the controller lock (`c.mu`; `evaluateShiftTriggers` already
  runs inside `ReconcileOnce`, which takes it at `controller.go:465-467`). The operator path
  (`--session`, D10) takes the same lock, as `InitiateShift` does
  (`controller.go:1798-1801`), and its replacements count toward the
  per-role limit like automatic ones.

### D8. The triggers

Requests are keyed by session key, and a role may hold several.

**Max age** is unchanged in meaning: notice, observe the seat's own marker,
replace on the marker, otherwise escalate and leave the seat running. It now
works for any replica count, which is what lets #458's refusal go (D11).

**Context pressure** becomes handoff-first, like max age (ruling 1, as
ruled: "no handoff is worse than autocompaction"):

- At `headroom_tokens` (today's threshold, `s.ContextTokens >
  s.ContextLimit-headroom`), the seat gets the max-age notice and a per-seat
  request with cause `context-pressure`. It is replaced when its marker
  appears, with reason `marker`.
- **It is never replaced without the marker.** With no marker by the end of
  `handoff_window`, marvel emits `shift-handoff-missing` with cause
  `context-pressure`, leaves the seat running and repeats once per window,
  as max age does. A seat that crosses into auto-compaction meanwhile
  compacts; that is the outcome the ruling prefers over a replacement with
  no handoff.
- A `context-pressure` condition requires the `handoff` and
  `handoff_marker` pair, checked at apply: without it the trigger could
  never observe a handoff, so it would look configured and never act (the
  finding-044 class). Refused at apply (section 7, ruling 3, ruled yes for
  now), and the refusal names ruling 1, the rule it enforces.
- There is no second threshold. The earlier `request_headroom` soft stage
  existed only to come before a forced hard stage, and ruling 1 removed
  that stage. Set `headroom_tokens` above the cost of writing a handoff plus
  a clean replacement.
- Seat selection lists the role's running seats at every generation (fixes
  F6, as #473 did for age), minus any seat in a standing entry (D7).

Immediately before deleting a predecessor, either trigger re-probes the
marker and records `handoff_present_at_drain`. Every automatic replacement
now starts on an observed marker, so the probe confirms it; a marker gone at
drain is recorded, and does not stop the delete of a seat whose handoff was
already read.

### D9. Events and durable state

D9 lands in two parts, because half of it describes things that exist only
once the replacement path does.

**Part A, on what exists today** (ticket 2):

- New `Event` fields `Cause` (`max-age`, `context-pressure`, `operator`) and
  `Reason` (`marker`, `operator`, `seat-gone`, `window-expired`,
  `policy-removed`), set on today's shift events:
  `shift-handoff-requested`, `shift-handoff-missing`, `shift-auto-triggered`.
- Those events carry the seat's own generation, not the team's
  (`shift_handoff.go:122`, `:232`; `controller.go:2028`).
- New kind `team.shift-hold-released` (info), emitted from
  `dropShiftRequest` (`shift_handoff.go:267`) with reason `seat-gone` (the
  asked seat is gone or replaced, `:132-139`) or `policy-removed` (the role
  no longer declares max age, `controller.go:1962-1965`).
- `EscalatedAt` on `ShiftRequest`, set where `Escalated` is set
  (`shift_handoff.go:178`).
- `describe team` shows pending requests (seat, cause, requested and
  escalated times). Until #453's delivery channel exists it is the
  supervisor's only read path, so it ships with a test.

**Part B, with the replacement path** (ticket 3):

- New `Event` field `Peer` (successor on drained events, predecessor on the
  successor's own).
- New kinds `team.shift-seat-drained` (info) and
  `team.shift-replace-aborted` (warning, D6). `shift-hold-released` also
  fires on an operator replacement.
- `describe team` shows standing replacements (predecessor, successor,
  cause, reason).
- Every seat-gone or aborted outcome is reconstructable from durable team
  state after a restart, not only from the 2000-event ring.
- No per-seat `shift-started` event: nothing reads it.

### D10. Operator shifts

`marvel shift --role` keeps today's role-scoped shift. A new
`marvel shift --session <key>` runs D1 for one seat. It counts toward D7's
per-role limit and is refused for a seat already in a standing entry.

### D11. Lift #458

The apply and scale refusals for max age on `replicas > 1` go in the last
commit, revertable alone, once the tests in section 5 are green.

## 4. Out of scope

- **#453's delivery channel.** Escalations still reach only the event ring
  and `describe team`. Its own design.
- **#451.** Closed by #473.
- **A per-role opt-out for pressure** (`on_no_handoff`). The party voted
  3 to 2 to defer it; ruling 1 made escalation the only behavior, so there
  is nothing left to opt out of.
- **The kill branch** (`shift-trigger-list.md` D8).

## 5. Tests (red first)

Fixture work first: a replicas parameter on `maxAgeRole`
(`controller_shift_list_test.go:26-43`) and `shiftTriggerRole`, a
`seedSeat(idx, gen, age, tokens)` in place of the hard-coded `-g1-0` (`:78`),
and a delete spy.

Every test seats A and B on a 2-replica role and asserts **after every
reconcile pass** on B's row identity (Key and CreatedAt) and on the delete
spy. End-state checks are blind: reconcile respawns a drained seat (F8). This
per-tick check is the acceptance criterion.

| # | Test | Mutant it kills | tmux |
|---|---|---|---|
| 1 | Scale-down hazard: A, B, successor A' with an entry; run both orders, B older than A is the one that matters. `planRole` deletes nothing; B survives | pair counted as two slots | no |
| 2 | Pair released: after the predecessor is deleted, `actual == desired`, no spawn, no delete | slot held after completion | no |
| 3 | Successor absent with the entry standing: one slot, no reconcile spawn | collapse keyed on rows, not the entry | no |
| 4 | Sibling survives a full cycle, both triggers (age with marker; pressure) | a role-wide drain selector reused (`controller.go:2350-2351`) | no |
| 5 | Successor shape: one row, predecessor's generation, `Predecessor == A`, team generation and `t.Shift` unchanged; admission asks 1 | replacement bumps the generation or starts `t.Shift`; admission asks `Replicas` | no |
| 6 | Predecessor dies mid-replace: complete, `seat-gone`, no second successor, B untouched | double spawn, or a fall-through drain | no |
| 7 | Successor dies before ready: A and B stay; the replacement recreates the successor | delete fires on "successor exists", not on ready | no |
| 8 | Deadline: past the timeout the successor goes, A and B stay, `shift-replace-aborted`, entry cleared | timeout sweeps the role's rows, or deletes A | no |
| 9 | Mutual exclusion both ways, with the refusal text | either guard missing | no (refusal); e2e half `skipIfNoTmux` |
| 10 | Restart mid-replace: entry survives, no duplicate successor (shape of `TestMaxAgePendingRequestSurvivesControllerRestart`, `:411`) | in-memory idempotency key | no |
| 11 | Control: `--role` drains all seats; `--session` runs D1 | over-correction of D10 | e2e half `skipIfNoTmux` |
| 12 | Kill between entry write and `Create`; kill between `Create` and delete | entry written after `Create` | no |
| 13 | Failed kill of the predecessor: entry stands, retried, cleared when the row goes | entry cleared on "Delete returned" | no |
| 14 | `scale` refused while an entry stands | refusal keyed on `t.Shift` only | no |
| 15 | Marker lands between successor ready and delete: `handoff_present_at_drain` true, not reported missing | probe skipped or read too early | no |
| 16 | Two entries in one role (a pressure replacement beside a max-age one): both pairs collapse, the delete list names neither successor nor the sibling | collapse not per entry | no |
| 17 | Pressure at an older generation (clone of `TestMaxAgeAsksSeatAtAnOlderGeneration`, `:773`) | the F6 filter survives | no |
| 18 | Double trigger: A under pressure with its entry standing, then more ticks with A still over `headroom_tokens`, and once a `--session` request for A. Exactly one successor exists, the entry is unchanged, and B is untouched after every pass | a seat in an entry is selected again; a second write overwrites the entry; `--session` bypasses the per-role count | no |
| 19 | Today's shift events (`handoff-requested`, `handoff-missing`, `auto-triggered`) carry `Cause` and `Reason` and the seat's own generation, on a team whose counter is higher than the seat's; a request dropped because its seat is gone, and one dropped because the policy no longer declares max age, each emit `shift-hold-released` with that reason | an event omits the fields or carries the team generation; a drop is silent | no |
| 20 | `EscalatedAt` is set when a request escalates and survives a controller restart | escalation time held in memory only | no |
| 21 | `describe team` shows each pending request (seat, cause, requested and escalated times) | describe omits the requests | no |
| 22 | Apply: a `context-pressure` condition without the `handoff` and `handoff_marker` pair is refused, naming ruling 1; with the pair it applies | the check missing, or raised only as a warning | no |
| 23 | Pressure with a marker: at `headroom_tokens` the seat gets the notice and a per-seat request with cause `context-pressure`; its marker starts the replacement, with reason `marker` | the request never fires, or the replacement starts before the marker | no |
| 24 | Pressure with no marker: at `headroom_tokens` and on every tick after, through and past `handoff_window`, the seat is never deleted and no successor is created (asserted after every pass); `shift-handoff-missing` fires once per window with cause `context-pressure`, and the seat stays running | a forced replacement at the threshold or at the window's end, or a silent window | no |
| 25 | Lift: apply and `scale` accept `max-age` on a role with `replicas = 2` (fails today on `manifest_shift.go:178` and `daemon.go:1589-1594`) | either refusal left in place | no |
| 26 | Replacement events: a full replacement emits `shift-seat-drained` with `Peer` (the successor), `Cause` and `Reason`, at the predecessor's own generation; a deadline emits `shift-replace-aborted` | the drain or the abort is silent, or omits the seat fields | no |
| 27 | `describe team` shows each standing replacement (predecessor, successor, cause, reason) | describe omits the replacements | no |

## 6. Plan (flat tickets, dependency edges)

The builder may land these as one PR with commits in this order (the party's
vote), or as separate PRs in the same order.

1. Context pressure lists seats at every generation. Red test: 17.
2. D9 part A: `Cause`, `Reason` and seat generation on today's shift
   events, `shift-hold-released`, `EscalatedAt`, pending requests in
   `describe team`. Red tests: 19, 20, 21.
3. Requests keyed by session, the replacement path D1 to D7 and D10, and D9
   part B. Red tests: 1 to 16, 18, 26, 27. Depends on 1 and 2.
4. Pressure becomes handoff-first (D8, ruling 1 as ruled): the request at
   `headroom_tokens`, replacement on the marker only, the apply check. Also
   amends `shift-trigger-list.md` D5, whose last paragraph describes today's
   pressure shift with no notice. Red tests: 22 to 24. Depends on 3.
5. Lift #458 (D11). Red test: 25. Depends on 3 and 4.

## 7. Rulings needed (operator, via director)

| # | Question | Default | Expiry |
|---|---|---|---|
| 1 | Context pressure replaces the seat at `headroom_tokens` without an observed handoff? | **Ruled no, 2026-10-04, reversing the default.** The operator, verbatim: "1 context pressure: no, it should NOT, no handoff is worse than autocompaction". D8 is written to it: pressure asks, replaces on the marker only, and otherwise escalates and leaves the seat running | ruled |
| 2 | Lift #458's refusal on `replicas > 1` once the tests are green | **Ruled yes, 2026-10-04**, the operator verbatim: "2 yes". In its own commit (D11) | ruled |
| 3 | A `context-pressure` condition without the handoff pair: refuse at apply, or apply with a warning and leave it inert? Refusal breaks applying any manifest that declares pressure without the pair (`examples/auto-shift.toml` and `.yaml` today) until it adds them | **Ruled yes for now, 2026-10-04**, the operator via director, verbatim: "yes for now". Refuse, naming ruling 1, and update the examples in the same change: a trigger that can never act should not look configured | ruled |

The ruling-1 default had rested on an unmeasured claim, that a forced
replacement loses less live work than auto-compaction. The ruling went the
other way, so the claim is no longer load-bearing.

## 8. Rollout note

Nothing changes a running cluster until its daemon is rolled. Until
ticket 4 ships, today's pressure shift replaces the whole role at once with
no handoff (F7), which ruling 1 forbids. The `aae/arcaven` architect role
removed its pressure block on 2026-10-04 for that reason (the manifest's
own comment), and its live `Shift` is `null`, so its seats auto-compact for
now; it is the role to re-arm, with the handoff pair, after the roll.
Before rolling a daemon with ticket 4, check each cluster's manifests for a
`context-pressure` condition without the pair (ruling 3).

## 9. Party record (3ptdd)

Five seats cast from the bmad-extras pool by competency square:
Distributed-systems, Runtime, Observability, Context, Evals. The "Casting
Call" party group is the ceremony that mints new extras, so the panel was cast
from the pool, not from that group. Each round had a brief, with one-command
checks on panelist claims appended. The record is in the orchestrator's
`_bmad-output/party-mode/marvel-452-per-seat-shift-2026-10-04/`.

- **Round 1.** The straw design (a seat-scoped shift) drew three objections
  that held: a seat-scoped shift selects the sibling by generation; a marker
  gate on pressure turns a deadline into a silent hold; and steady-state
  scale-down sheds the oldest seat (found by the architect checking a
  replace path, F8).
- **Round 2.** The design moved to a replacement outside the shift. Two seats
  independently found that the entry must be written before `Create` (D2).
- **Round 3, forced vote on 15 items.** 13 unanimous. D7's pressure exception:
  4 for, 1 abstain. The pressure opt-out: 3 to 2 to defer (section 4). No
  open split. Dissent recorded:
  - Runtime: two concurrent pairs in a 2-replica role double-launch every
    seat. Hence D7's cap and event.
  - Distributed-systems: D3 and D5 are one invariant, to be tested by driving
    every path with an entry standing. Also: the D9 re-probe records and does
    not gate.
  - Context, Distributed-systems: for the opt-out now.
  - Evals: the ruling-1 default rests on an unmeasured claim (section 7).
- **After the party.** The operator reversed ruling 1 (section 7), which
  carried Evals' dissent further than the dissent asked: pressure is now
  handoff-first, and the soft and hard stages are gone.
