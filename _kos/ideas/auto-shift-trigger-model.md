# Auto-shift trigger model: requirements and design recommendations

**Status:** idea (pre-hypothesis, no commitment). Output of a casting-call
bmad party-mode session, 2026-09-17. Nothing here is decided or ratified; it
recommends changes for the operator to choose among.

**Grounding:** every claim about current behavior is verified against marvel at
`c7ab41c` (four read-only surveys, notes in the session scratchpad), not
asserted from memory. Where I say "today X," a file:line backs it.

**Subject:** the automatic shift TRIGGER and the manifest layering that governs
it. The succession/handoff mechanism, successor identity, and roster liveness
are OTHER subjects with their own homes; this doc references them and does not
re-derive them (see the boundary below).

## What this does not touch (de-duplication boundary)

- The remainder trigger already shipped (PR #292): per-role
  `[team.role.shift]` `on = "context-pressure"` + `headroom_tokens`, evaluated
  on the reconcile tick, `maxAutoShiftsPerTick`, event `team.shift-autotriggered`.
  This design extends the trigger taxonomy around it; it does not rebuild it.
- The handoff/succession mechanism is designed in
  `_kos/ideas/shift-change-succession-protocol.md` and the #295 bd set (root
  `aae-orc-c3be5`). That design explicitly carves the auto-trigger out as a
  separate root cause left to a separate ticket. This doc is that ticket's
  design. Where an emergency shift needs recovery-with-guard, that is
  succession CONTENT and lands on the #295 set, not here.
- Successor identity, the human-director seat, and roster liveness are the
  director session-identity options doc's subject (`director` PR #47,
  `sim/design/session-identity-and-succession-options.md`, finding-003).
  Anything about a successor not colliding on a live address references that.

## The current implementation, verified

- The one automatic trigger is context-pressure. Firing test
  (`internal/team/controller.go:1884`):
  `s.ContextTokens > s.ContextLimit - role.Shift.HeadroomTokens`, guarded by a
  resolved-window check `ContextLimit > 0` (`:1881-1883`), evaluated in the
  reconciler (`evaluateShiftTriggers`), not a health loop. Manual shifts are the
  only other path (operator `marvel shift`).
- The window is model-derived through a resolution ladder
  (`internal/usage/limits.go:74-82`): harness stream, then learned, then the
  `runtime.context_window` operator override, then the statusline feed, then a
  model->window table keyed by model id. `runtime.context_window` is an optional
  override, not a hardcoded per-role number.
- The manifest is FLAT: shift and runtime attach only at the role level;
  workspace carries a name, team carries a budget; `Apply()` copies each role
  verbatim with no inheritance. `shift.on` is an enum-of-one (`context-pressure`).
  There is no enable/disable field; the presence of the shift block is the switch.
- Sessions on an unresolved window (`ContextLimit == 0`: codex, which reports a
  session total not a per-request level; opencode, which declares no window) are
  NEVER shifted by this trigger and look configured while never firing
  (finding-044).
- `ContextPercent` (`100 * tokens / limit`) is already computed and stored per
  reading (`internal/api/store.go:714-739`). A reading carries no observation
  time, so a frozen latch is indistinguishable from a fresh one.
- There is no cooldown, hysteresis, or debounce keyed to shift firing; the only
  brakes are `maxAutoShiftsPerTick = 1` and the `Phase != ShiftNone` re-entry
  guard. A role can re-fire the tick after its shift completes.
- Health runs every 2s in the reconciler (`evaluateHealth`). `HealthState` is
  `unknown/healthy/unhealthy`; the `ActivityState` (stalled) axis is
  restart-neutral and never drives replacement. There is no runaway/rate/loop
  detector, no inbound peer or supervisor verdict channel (the events ring is
  outbound-only), and no graceful interrupt (every stop is a hard tmux
  `kill-pane`). The one seam that initiates a shift is `initiateShiftLocked`
  (`controller.go:1718-1803`), two callers today.
- ADR-010 gives headless roles completion semantics (a finished run holds its
  slot), but "does not by itself stop the churn" until exit-status work
  (`aae-orc-bxeh`) lands.

## Requirements (captured from the operator's seven-point framing)

RQ-1 (trigger taxonomy). Auto-shift must support triggers beyond context
level: at least a compaction EVENT trigger and an emergency/health trigger,
alongside the shipped remainder trigger. (defects 1, 6, 7)

RQ-2 (per-model, harness-aware pressure). Context pressure must be expressible
per model, keyed to that model's resolved window, and must have a defined
behavior for harnesses whose window is unresolved today. One per-team absolute
number cannot be correct for a mixed-model, mixed-harness team. (defects 2, 4)

RQ-3 (layered enable/disable and override). Auto-shift must be enable/disable-able
at workspace, team, and per-agent(role) levels, with a per-agent override of
both the on/off state and the trigger parameters. (defect 3)

RQ-4 (trigger and inhibit). Beyond conditions that START a shift, there must be
predicates that PREVENT one, each bounded so a stuck inhibitor cannot strand a
session past its window. (defect 5)

RQ-5 (cooperate with harness autocompaction, with a stopgap). A role may opt to
use the harness's own autocompaction; marvel must detect whether compaction
actually happened and fire a stopgap shift if it did not by a bound. (defect 6)

RQ-6 (emergency shift on health, peer verdict, or runaway). marvel must accept a
health signal or a peer/supervisor verdict that an agent is misbehaving or
runaway (an output-token-rate spike with repetition), attempt to interrupt it,
then replace it, recovering from session remnants while guarding against
replaying the triggering prompt. (defect 7)

RQ-7 (reading age and anti-flapping). A level-based trigger must carry a reading
age (a stale reading neither fires nor inhibits) and must not flap: a role that
just shifted does not immediately re-fire. This is a correctness gap in shipped
code, and it mirrors director R-93 / BEAT-G on the marvel side. (cross-cutting)

RQ-8 (harness-independence with defined fallback). Every trigger and the
emergency path must work across claude, codex, and opencode; where a harness
cannot supply a signal, the policy degrades to a defined fallback, never to a
silent never-fires. (cross-cutting; defect 2 / finding-044)

## Design recommendations, with tradeoffs

Flagged by how critical I judged the shortcoming and whether this addresses it.

### A. Shift-policy resolution ladder (RQ-3, defect 3). CRITICAL, addressed.

Introduce an optional `[shift]` block at the workspace and team levels in
addition to the role level, and resolve per role by override: role wins over
team wins over workspace. Add an explicit `enabled = true|false` field distinct
from the presence-of-block switch, so a team can enable auto-shift for all roles
and one role can turn itself off. This mirrors the context-window ladder marvel
already has (`limits.go`); `Apply()` gains a merge step where today it copies the
role verbatim.

Tradeoff: it adds inheritance to a deliberately flat manifest, so precedence and
"unset vs explicit false" must be defined (nil = inherit, explicit `false` = off
at this level). Back-compat is preserved: a role with a shift block and no
`enabled` field stays on, as today. Alternative (keep flat, make every role
fully specify) is rejected as unusable for real teams and a direct miss of the
operator's ask.

### B. Per-model, window-relative pressure (RQ-2, RQ-8; defects 2, 4). CRITICAL, addressed with a reconciliation.

The operator asks for pressure as a percentage of the window, per model. The
graph already ruled a percentage FILL LEVEL a defect three ways (no stable
denominator, occupancy is a price multiplier, aggregation is a scheduling
hazard; `question-shift-triggers`). Those two are reconcilable, not in conflict.
The ruling is about COMPARING percentages across agents on one shared column.
A percentage of a session's OWN resolved window, evaluated locally, firing only
that session's own shift, is not that column: the denominator is that one
session's window, which marvel already resolves.

Recommendation: add `on = "context-fraction"` as a policy whose parameter is a
fraction of the resolved window, optionally overridable per model id (a small
map mirroring `limits.go` DefaultTable). It computes to the same per-session
headroom the shipped remainder trigger consumes, so a mixed-model team gets a
correct per-session headroom from one policy line: a 1M-window model and a
200k-window model in one team shift at their own proportional points.
`maxAutoShiftsPerTick` stays unchanged as the aggregation-hazard guard.

Tradeoff: two policies (remainder and fraction) is more surface. Reyes'
reservation, recorded: `context-fraction` may be better as sugar that just
computes `headroom_tokens` per session rather than a second `on` value. I
recommend the explicit policy name for clarity in a mixed-model manifest, but
the sugar form is a legitimate lighter alternative. Either way, the fraction is
per-session and never an aggregated column.

### C. Compaction-event trigger and cross-harness backstop (RQ-1, RQ-5, RQ-8; defects 1, 6, and the codex/opencode blind spot). CRITICAL, addressed.

Neither remainder nor fraction can fire on an unresolved window, so codex and
opencode roles are uncovered by any level trigger. Adopt the bound-context
thesis (`aae-orc-i8ern`): add `on = "compaction-event"`, where marvel shifts on
the harness's own compaction signal. This needs no denominator and every runtime
emits one (claude `compact_boundary`; codex `type=compacted` with
PreCompact/PostCompact hooks; opencode `session.compacting`; gemini
`gemini_cli.chat_compression`; Crush `prompt_tokens` dropping to zero). It needs
a per-adapter ingestion path landing the signal as a marvel event, beside the
heartbeat and accountant paths.

This one mechanism closes the codex/opencode blind spot (the hardest part of
defect 2) and delivers defect 6: a role using harness autocompaction sets the
compaction-event policy, and the stopgap is that if the event does not arrive
within a bound after pressure crosses, a normal shift fires.

Tradeoff: per-adapter work, since each harness signals differently, and "about
to compact" versus "just compacted" are different edges. The pre-emptive win
(shift BEFORE compaction degrades the session) wants "about to"; the defect-6
stopgap is satisfied by "just." The policy should name which edge it fires on.

### D. Reading age and anti-flapping (RQ-7). CRITICAL, addressed (a latent bug fix).

Two fixes. First, stamp each stored `SessionContext` reading with an observation
time; a level trigger ignores a reading older than a staleness bound. This is a
correctness fix regardless of the new features, and it is the same reading-age
discipline as director R-93 / BEAT-G, applied on the marvel side (cross-linked,
not duplicated). Second, add a fire-once-per-generation guard on auto-triggers
keyed on the generation in `ShiftState`: a role that just shifted does not
auto-fire again until its new generation has produced a fresh reading and a
short settle interval has passed.

Tradeoff: too long a settle risks missing a successor that fills fast; keep it
short and lean on generous pre-emptive headroom. The pre-emptive design argues a
tight cooldown is less necessary, but the fire-once-per-generation invariant is
cheap insurance and the reading-age fix is a correctness bug either way.

### E. Shift-inhibit predicates (RQ-4, defect 5). ADDRESSED (design), lower urgency.

Add an inhibit set evaluated in `evaluateShiftTriggers` before
`initiateShiftLocked`: a shift that would fire is deferred while any inhibitor
holds. Candidates: an agent-declared critical-section flag (a new heartbeat
field or a marker), an account-rate-limit guard (do not shift INTO a rate limit
already near its ceiling, reusing the `ctxforward` rate read, which is the
aggregation hazard seen from the prevent side), an operator pin, or a
human-attached signal.

Tradeoff: an inhibitor that never clears is a deadlock that strands the session
into a hard compaction, the worse outcome. Every inhibitor needs a bounded
max-hold, after which the shift proceeds (fail open toward shifting), mirroring
the succession marker's timeout.

### F. Emergency shift on health, peer verdict, or runaway (RQ-6, defect 7). PARTIALLY addressed; split marvel/director; some deferred.

Three net-new pieces, because none exist today.

1. Runaway detector (marvel health loop). A new health axis measuring output-
token RATE and repetition (output tokens spike while input barely moves; the
same tool output repeats). Context-pressure is a level not a rate, and the
account rate read is display-only, so this is new. Unlike heartbeat-staleness,
this axis can drive an emergency shift.

2. Inbound verdict channel (splits to director). A supervisor or peer reporting
"agent X is misbehaving" has nowhere to land today; the events ring is
outbound-only. This needs a new inbound daemon method (analogous to heartbeat).
The verdict is authority-bearing (a peer asking to kill an agent is director
R-01, "who authorized this"), so the verdict ENVELOPE and its principal are
director's; marvel consumes the verdict as an emergency-shift input. This piece
is gated on the director identity/nonrepudiation plane, which is itself unbuilt,
so it is DEFERRED behind that plane.

3. Interrupt-then-replace (marvel). Today every stop is a hard `kill-pane`;
there is no graceful interrupt to reuse. Add a graceful interrupt primitive
(attempt the harness's own interrupt first, via send-keys or a real signal to
the harness pid, with a bounded wait) before escalating to the existing
kill-and-shift. The replace half reuses the shift drain. Recovery-with-guard:
the successor recovers from session-file remnants through the #295
reconcile-from-records path, and the guard against replaying the triggering
prompt is succession CONTENT (the handoff marks that prompt poisoned so the
successor does not re-issue it), landing on the #295 set, not here.

Tradeoff: this is the largest greenfield and the most safety-critical. Runaway
detection has false positives (a legitimately verbose agent), and an emergency
shift is destructive. Recommend a higher bar than context-pressure (a verdict
plus a detector threshold, or two independent signals), default OFF, opt-in per
role, with interrupt-first to reduce destructiveness. Sequencing: the LOCAL
detector plus interrupt-first needs no inbound channel and can land first; the
peer-verdict path waits on the director plane it depends on.

## Critical shortcomings: addressed vs deferred

Judged critical and addressed here: defect 2 (mixed-model/harness one-number-
wrong) via B and C; defect 3 (layered enable/disable/override) via A; defect 4
(per-model percentage) via B, reconciled with the ruling; defect 1 (context-only
trigger) via C and F broadening the taxonomy; and the anti-flapping/reading-age
latent bug via D.

Addressed as design, lower urgency or partly deferred: defect 5 (trigger/prevent)
via E; defect 6 (autocompaction with stopgap) via C; defect 7 (emergency) via F,
where the local runaway detector plus interrupt-first can land, the peer-verdict
envelope is deferred behind the director identity plane, and the replay-guard is
routed to the #295 succession content.

Referenced, not designed here (owned elsewhere): the handoff/succession
mechanism (#295), successor identity and roster liveness (director session-
identity doc), and the signed-principal verdict envelope (director R-01/R-05).

## Proposed follow-on work (flat bd tasks, for the operator to ratify)

Flat tasks with depends-on edges, no invented epic; they relate to the M4
umbrella `aae-orc-hpeu` (operator-owned) and to `aae-orc-i8ern`. Labels
`aae-orc`, `marvel` (plus `director` or `wardrobe` where noted), `source:session`.

1. marvel: shift-policy resolution ladder across workspace/team/role with a
   per-role override and an explicit `enabled` field (A, defect 3).
2. marvel: `context-fraction` shift policy, a per-session fraction of the
   resolved window computed to per-session headroom (B, defects 2/4).
   [relates 1]
3. marvel: per-model fraction override table keyed by model id (B, defect 2).
   [depends 2]
4. marvel: `compaction-event` shift policy plus per-adapter compaction-signal
   ingestion, the cross-harness backstop (C, defects 1/6). [relates i8ern]
5. marvel: stamp `SessionContext` readings with an observation time; level
   triggers ignore stale readings (D, RQ-7; cross-ref director R-93/BEAT-G).
6. marvel: fire-once-per-generation guard on auto-triggers (D, RQ-7).
   [relates 5]
7. marvel: shift-inhibit predicates evaluated before initiate, each with a
   bounded max-hold (E, defect 5).
8. marvel: output-token-rate and repetition runaway detector as a new health
   axis that can drive an emergency shift (F.1, defect 7).
9. marvel: graceful interrupt primitive attempted before `kill-pane`, used by
   emergency shift (F.3, defect 7).
10. marvel: emergency-shift path routing a health/runaway verdict into
    `initiateShiftLocked`, opt-in per role, higher bar than context-pressure
    (F, defect 7). [depends 8, 9]
11. director + marvel: inbound peer/supervisor verdict channel carrying a signed
    principal, consumed by marvel as an emergency-shift input (F.2, defect 7).
    [depends 10; gated on the director identity plane, relates finding-003]
12. wardrobe + marvel: handoff content marks the runaway-triggering prompt
    poisoned so the successor does not replay it (F, defect 7 recovery-guard).
    [relates aae-orc-8fxj0, aae-orc-at7jm in the #295 set]
13. test: a mixed-model team (a 1M-window model, a 200k-window model, and a
    codex role) each shifts at its correct per-model point or its defined
    fallback (RQ-2/RQ-8).
14. test: a stale reading does not fire a shift, and a shift does not re-fire
    within the settle window (RQ-7).

## Cross-links (note the held/branch state so links do not dangle)

- Succession: `_kos/ideas/shift-change-succession-protocol.md`; #295 set root
  `aae-orc-c3be5`; the S0 harness-hook gate `aae-orc-7opc`.
- Trigger prior art: `_kos/nodes/frontier/question-shift-triggers.yaml`; the
  bound-context idea files; the shipped remainder trigger (PR #292); the M4
  umbrella `aae-orc-hpeu`; the compaction-event backstop `aae-orc-i8ern`.
- Jabberwocky and cross-harness metering: `finding-044`, `finding-046`,
  `finding-018`.
- Identity and liveness: director PR #47,
  `sim/design/session-identity-and-succession-options.md`, finding-003, R-93.
