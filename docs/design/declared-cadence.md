# Declared cadence: a periodic prompt into an interactive seat

Status: design, voted by a 4-round party (ptdd), 2026-10-06. The operator
adopted all three rulings in section 7 on 2026-10-10. Code references are pinned to
marvel `409593c` and director `813c595`. V4's gate was tightened in review
after the vote (a second read before Enter, and copy mode); the panel voted
the single-read form.

## 1. Why

Some interactive seats need a prompt on a schedule: an envoy sweeps the
board every two hours, by an operator ruling. Today that schedule lives in
the seat itself, as a Claude Code session cron. A session cron is
session-only, fires only while the seat is idle, runs up to 10% of its period
late, and expires after seven days (the harness's own tool description). It
dies on every respawn and every shift, and each successor rebuilds it by hand
from a record it reads. It has died this way twice.

A cadence should belong to the role, be fired by something that outlives the
seat, and be visible when a slot is missed. That is what this design does.

## 2. What exists, and why the earlier plan is not used

- marvel's role `schedule` block is for headless roles only, and its clock is
  one tick a minute (`docs/design/scheduled-runs.md`, section 6); its section 9
  put cues into interactive seats out of scope, for director. This design
  takes that scope back into marvel and amends section 9 to point here.
- The earlier plan for interactive cues was a NATS timer (`set_timer`) in the
  director shim, on an events stream with message schedules. It sits behind a
  broker authorization block and a bus cutover that are not live, and the
  shim has no timer today. This design does not need it.
- director's mail wake (`DIRECTOR_CUE`) loads a development channel, and
  every seat that loads one stops at a consent prompt on every launch. A
  respawned seat would wait there for a human, so the cue is not the wake.
- A shift or restart gives a seat a new agent id (`internal/runtime/adapter.go:399`),
  and mail to a role reaches every live seat of the role. Addressing one seat
  loses the cue when that seat dies; addressing the role can sweep twice.
- marvel already types text into seats from the daemon: the max-age handoff
  request goes through `notifyHandoff` with a pre-flight and an inject event
  (`internal/daemon/daemon.go:444-469`). On a Claude pane that pre-flight does
  no composer check (`internal/composer/claude.go:28`: `Preflight()` is
  false); its one menu check is the usage-limit menu, and only when a menu
  sample is configured (`preflightRefusal`, `daemon.go:2227-2228`). So typed
  text with Enter could answer a permission prompt. The pre-flight is, in its
  own words, "a check, not a lock" (`daemon.go:2224-2226`): the pane can change
  between the read and the keystrokes. The composer reader does tell an empty
  idle prompt from a draft or a menu (`internal/composer/composer.go:20-30`).
- A pane in tmux copy mode reads as its normal screen to `capture-pane`, and
  keys sent to it go to copy mode, not the program. Measured on tmux 3.7b with
  a scratch server: with emacs mode-keys a typed line and Enter were swallowed
  and the pane stayed in copy mode; with vi mode-keys Enter left copy mode and
  the line was lost. Either way a wake would be recorded that never arrived.

## 3. Design (voted 5 of 5 on every item)

- **V1. Declaration.** A role carries `[[team.role.cadence]]` with `name`,
  `schedule` (5-field cron), `timezone` (IANA, required), `prompt`,
  `starting_deadline` and `grace`. It is set, changed and cancelled only by
  applying a manifest; no seat can. Apply refuses a cadence unless
  1m <= starting_deadline <= grace < period, where period is the shortest gap
  between two consecutive slots over the next 366 days in the declared
  timezone. marvel serves the cadence it applied, never a file on disk.
- **V2. marvel owns the slot.** The schedule clock, extended to interactive
  roles, opens one slot record per cadence per due time, and only marvel
  writes slot records. A slot missed while the daemon was down fires at most
  once on recovery, within `starting_deadline`; past it the slot is recorded
  `skipped` (the scheduled-runs precedent).
- **V3. A claim is a lease.** The woken seat calls marvel to claim the due
  slot, authenticated by its heartbeat token and checked against that token's
  role. marvel answers with the slot and the prompt, or "nothing due", or
  "claimed". A holder's repeat claim returns the same slot. The claim is a
  lease on the claiming session, released when that session departs
  (deleted, crashed or shifted out), which reopens the slot. A slot not
  `swept` by `grace` is `overdue`, the only overdue rule. The sweep must be
  safe to run twice for one slot.
- **V4. The wake.** marvel wakes an open slot's role by typing one fixed
  line, carrying no slot, no prompt and no other state, through the daemon's
  inject path, only into panes marvel spawned, and only through a two-read
  gate. First, the pane is not in a tmux mode (`#{pane_in_mode}` is 0) and
  the composer reads `Empty`. marvel then types the line without Enter, reads
  again, and sends Enter only if the pane is still not in a mode and the
  composer holds exactly the fixed line. Otherwise it clears only the line it
  typed and records a refusal. The second read closes the gap the pre-flight
  leaves between a read and a keystroke; it narrows the race to the Enter
  itself and does not remove it. Each delivered wake is recorded as
  `injector=marvel:cadence` with a `woken` record. A refused pane is retried
  on later ticks until `grace`; the refusal is recorded when its reason
  changes, with a count of the retries between. The development-channel cue
  is not used.
- **V5. Records and alerts.** `claimed` and `swept` come from the seat;
  `woken`, `skipped`, `fresh` and `overdue` from marvel; each names the seat.
  `overdue` fires once per change of state as a `cadence.overdue` event and
  director mail to the team's supervisor, carrying only fields marvel writes.
  `swept` is self-reported, so this shows a silent seat, not one that lies.
- **V6. Spawn tokens.** marvel sets `DIRECTOR_CLUSTER` and `DIRECTOR_ROLE`
  at spawn, so a marvel-native launch does not depend on a cast script.
- **V7. The bridge, until V2 to V5 ship.** The seat's process text re-arms
  its session cron at bootstrap from the role library's text, types the same
  fixed line as V4, deletes the cron when it writes its handoff, never reads
  a prompt from writable board state, and claims before sweeping once the
  claim exists. The team supervisor checks the last sweep on its rounds.
- **V8. The NATS event plane is off the cadence path.** `set_timer` is
  dropped; the event-subscription tools, the events streams, their grants
  and the broker authorization block keep their own purpose (event senses)
  and their own order.
- **V9. Owners, by role.** marvel's builder role builds the marvel tickets
  and re-measures the claude composer on harness upgrades. The bridge and
  the final process text are in the role library, for the operator to
  ratify. Whoever maintains the director shim re-measures the cue on its
  upgrades.

## 4. Plan: flat tickets, red tests, edges

| id | change | red test | after |
|---|---|---|---|
| T1 | declaration and apply validation (V1) | a cadence with grace >= period, or with no timezone, is refused at apply | |
| T2 | spawn tokens (V6) | a marvel-spawned seat's env carries `DIRECTOR_CLUSTER` and `DIRECTOR_ROLE` | |
| T3 | slot clock for interactive roles (V2) | a due slot opens one record; a slot missed while the daemon was down fires once within `starting_deadline` and is `skipped` past it | T1, the schedule clock (S-3) |
| T4 | claim lease and swept (V3, V5) | two seats claim one slot and one gets "claimed"; a holder's repeat claim returns the same slot; a claiming seat deleted, or shifted out, reopens the slot; a token of another role is refused; no `swept` by grace yields one `overdue` | T3, P2 |
| T5 | the cadence wake (V4) | a pane showing a draft, a dim suggestion, the consent prompt or a permission prompt is not typed into and the refusal is recorded once; a pane in copy mode (emacs and vi mode-keys) is refused and retried; a draft that appears between the first read and Enter gets no Enter, only the typed line is cleared, and a refusal is recorded; an `Empty` composer that still holds exactly the fixed line on the second read gets Enter and a `woken` record | T3, P1 |
| T6 | overdue alerting (V5) | one `cadence.overdue` event and one supervisor mail per change of state | T4 |
| T7 | bridge process text (V7) | (role library; the operator ratifies) | |
| T8 | final process text: claim, sweep, swept | (role library; the operator ratifies) | T4, T5 |

Probes, each recording the harness version it ran against:

| id | question |
|---|---|
| P1 | on a Claude pane (build 2.1.292), do the permission prompt, the consent dialog, the usage-limit menu, the "Teach auto mode" dialog, a dim suggestion and a half-typed draft all read as something other than `Empty`? |
| P2 | does the auto-mode classifier let the seat's claim call through? |
| P3 | what does a session cron do when it fires mid-turn: queue, or drop? |
| P4 | read only: the seat's sweep duration (p95) and its longest busy turn, to set `grace` |

T1 and T2 have no dependencies and can ship first. This PR also amends
`scheduled-runs.md` section 9 to point here, so the slot clock's design no
longer says interactive cues belong elsewhere.

The earlier tickets for the NATS path are re-scoped, not dropped: the
subscription tools lose `set_timer`, the grant rows lose the timer grant,
and the streams and the broker authorization block stay for the event
senses. Their edits are the operator's ruling 3.

## 5. Security notes

- A typed line can approve a dialog, so V4's gate is a hard requirement,
  not a refinement, and P1 proves it before T5 ships. The gate types
  without Enter and re-reads before Enter, because a single read is a
  check, not a lock, and it refuses a pane in a tmux mode, because
  `capture-pane` cannot see one.
- The cue carries nothing the seat trusts. A forged wake can at most make a
  seat ask marvel whether a slot is due.
- The local broker listens on loopback only, so the remaining exposure of an
  unauthenticated broker is any local process, which the broker
  authorization work already covers.

## 6. Record

The party record (casting, four rounds, the checks run on panelist claims,
the ballot) is kept outside this repository, with seat labels only. Panel:
distributed systems, Claude Code internals, runtime substrate, AI security,
agent observability.

## 7. Rulings needed (the operator's)

1. Adopt the design and plan as voted. Recommended: adopt.
2. The bridge this week (V7): the seat re-arms its session cron at bootstrap
   from the role library's text. Recommended: adopt, re-checked by
   2026-10-20.
3. The earlier NATS-path tickets: rewrite the subscription and grant tickets
   without the timer, keep the streams and the authorization block for event
   senses, and rewrite the process-text ticket to its event wake only.
   Recommended: adopt.

**RULED 2026-10-10:** "adopt all three", relayed by director. The design
and the section 4 plan (T1 to T8 and probes P1 to P4) are adopted, the
bridge (V7) is adopted, re-checked by 2026-10-20, and the earlier NATS-path
tickets are rewritten as ruling 3 says.

The bridge stays under its re-check: the architect seat re-checks it by
2026-10-20 and renews, revises or withdraws it. Tickets are filed from
section 4 as their own step.

Each recommendation was valid until 2026-10-20, or a change to the schedule
clock, the inject path, the composer reader or a Claude Code release that
changes the facts in section 2, whichever comes first; the architect seat
that wrote this re-checks it then. Nothing here executes on silence.
