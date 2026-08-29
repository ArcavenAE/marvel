# finding-032 — the session listing and the generation count are two truths read as one; join in the read path, count at the caller, and 83m9's premise does not hold

Probe: Lane-2 architecture design over the session-listing / generation-counting
defect cluster — aae-orc-6kgq, 094e, prhx, 83m9, fv3h, m8n0.
Operator: Claude (Opus 4.8), architect role (Winston), no human at the keyboard.
Date: 2026-08-28.

Anchors verified at **`60b994b`** (`git rev-parse HEAD` at investigation time).
The PR base is origin/main **`303d88c`** (#211); its only change from 60b994b is
`internal/usage/limits.go`, so every line anchor below is identical at the base.

**Design + re-measurement. No production code changed here** — a dev agent
follows with the red/green implementation. This finding settles the design shape
and, as its evidence, resolves 83m9's flagged-suspect premise.

Related: finding-019 (one-loss-event-charged-per-replica — the reap-tick charge
this cluster makes visible), finding-027 (status truth table), finding-028
(event-ring inventory), finding-030 (reconciler converges ≠ working team).
Cross-graph: orc `question-silent-success-instruments`,
`question-fanout-composition-safety`, bd `aae-orc-2l26`.

---

## 1. Headline

Marvel keeps **two** truths about a role — its **live session rows**
(`store.sessions`) and its **crash-loop state** (`RoleHealth`, keyed
`ws/team/role`). Every ticket in this cluster is a place where one truth is read
as if it were the other, or where the query that spans them is silently asked to
mean two different things at once.

The coherent shape keeps the two truths **separate in the write path** (the
reconciler stays clean — a deleted session is gone, RoleHealth carries the "held"
fact) and **joins them in the read path** (`get sessions` becomes a projection
over both). Counting bugs are fixed at the specific *caller* that asked the wrong
question; the listing gap is fixed in the read projection. **The store query's
meaning is never changed** — because three of its five callers correctly depend
on the meaning it has today.

And the one premise in the cluster that was flagged suspect is refuted: **a
MaxRestarts-saturated role is not absent from `get sessions`.** It keeps its
failed row. The role that actually goes missing is the crash-looping
`restart_policy=always` one.

## 2. The two truths, and why this is one arc and not six PRs

`RoleHealth` (internal/team/controller.go:77) holds `RestartCount`,
`LastRestartAt`, `BackoffUntil` per role. `store.sessions` holds the live +
terminal session rows. `get sessions` (internal/daemon/daemon.go:850) reads
`store.ListSessions()` **alone**; generation counting reads
`ListSessionsByTeamRoleGeneration` (internal/api/store.go:255), which has **no
State predicate**. The cluster is six symptoms of the same seam.

The load-bearing design decision is invisible from inside any single ticket:
**do not add a `State` predicate to `ListSessionsByTeamRoleGeneration`.** The
"obvious" per-ticket fix for 6kgq — make the store query filter to live — would
silently flip its meaning under the callers that need all states. Two
locally-correct PRs would compose into a silent break. That is the
`question-fanout-composition-safety` / `aae-orc-2l26` hazard exactly, which is
why this arc stays single-owner: the constraint must be decided once and honored
by every step.

## 3. Caller inventory — `ListSessionsByTeamRoleGeneration` (the decisive evidence)

Five non-test callers, all in `internal/team/controller.go`. What each *wants*:

| # | Line | Caller | Uses the result to… | Wants |
|---|------|--------|---------------------|-------|
| 1 | 1051 | `shiftAbort` cleanup | **delete** every new-gen row to roll back a stuck launch | **ALL states** — a Failed/Crashed row still has a pane to delete |
| 2 | 1073 | `shiftLaunch` create-gate | `len < desired` → **create** more new-gen sessions | **LIVE only** ← 6kgq |
| 3 | 1096 | `shiftLaunch` ready-gate | `len >= desired && allReady` → **drain** old gen | **LIVE only** ← 6kgq |
| 4 | 1178 | `shiftDrain` | `len == 0` → old gen **drained** | **ALL states** — must delete dead old-gen panes; 094e is a provenance axis, not a state one |
| 5 | 1206 | `nextIndex` | max index over gen → **name** the next session | **ALL states** — a dead row still reserves its index |

**Three of five want ALL states; only shiftLaunch's two sites want LIVE.** So the
store query keeps its all-states meaning and shiftLaunch filters at the call site
with the **existing** `api.SessionState.CountsAsAlive()` (internal/api/types.go:42)
— identical to what `reconcileRoleAt` already does on the non-generation query
(controller.go:500-503). `CountsAsAlive` is the one shared definition of "live":
`pending`/`running`/`crashloop-backoff` alive; `succeeded`/`failed`/`crashed`
terminal.

## 4. Evidence — 83m9 re-measured: the saturated role keeps its row

83m9 was filed with a note flagging its own premise suspect and asking that it be
re-measured before any work. It is refuted, and the refutation is the load-bearing
evidence for the listing design.

`restartSession` (controller.go:750) has three branches for an unhealthy
always/on-failure session:

- **In backoff window** (`now.Before(BackoffUntil)`): mark `SessionCrashLoopBackOff`,
  **keep the row**, keep the pane. Fires only for *sibling* replicas in a
  multi-replica role — asserted by `TestHealthRestartBackoffSiblingMarked`.
- **Saturated** (`!noteCrashAndBackoff`, i.e. `RestartCount >= MaxRestarts`): mark
  `SessionFailed`, **keep the row**, freeze `BackoffUntil` to the 9999 sentinel
  (`saturationFreezeUntil`, controller.go:358). No delete.
- **Restart now** (past backoff, not saturated): set `SessionFailed`, **DELETE the
  row** (`sessMgr.Delete`), let the reconciler respawn after backoff.

Two evidence sources converge:

1. **Live measurement (prior, docs agent, cited in 83m9's own notes):** on the
   capped role, `role.saturated` and `session.failed` fire together at 08:13:33
   and the row **stays permanently**; the `~60s` absence is the *restart* window,
   *before* saturation, not after; the `failstop` role's row never leaves at all.
2. **Code confirmation (this finding, at 60b994b):** the saturation branch keeps
   the `SessionFailed` row; `TestHealthRestartBackoffHoldsReplacement` **asserts
   `len(got) != 0` is a failure** during the backoff window, encoding the zero-row
   gap as the *restart* window's behavior — which is the always-restart path, not
   saturation.

No live daemon was started for this finding (per `aae-orc-9uzk`: a daemon against
the real `~/.marvel` state rehydrates a live fleet on this shared machine). The
re-measurement stands on the docs agent's prior live capture plus independent
code confirmation; they agree.

**Disposition: 83m9 is restate/close as not-a-defect-as-written.** Its real ask —
"a held-down role needs a row that says so" — is the general invariant the read
join establishes (§5), which is prhx.

**What actually goes missing (prhx, confirmed):** for a **single-replica
crash-looping `restart_policy=always` role**, only the delete branch fires each
cycle. Delete → `reconcileRoleAt` sees `actual<desired` but the backoff gate
(controller.go:527) refuses respawn → **zero rows of any state** for the whole
backoff window (60s → 2m → 4m, growing with `RestartCount`), present only for the
few seconds a replacement lives before failing again. The absence is
indistinguishable from "never declared" and from "deleted."

## 5. Listing decision — read-path join (prhx shape 2), not keep-the-row (shape 1)

**Shape 1** (keep the row on the restart-delete tick) mutates the write path:
it needs a new visible-but-not-alive-and-transient state threaded through the
reap, restart, reconcile, `nextIndex`, and shift paths, and it forces rewriting
`TestHealthRestartBackoffHoldsReplacement`, which encodes today's zero-row window
as correct. High blast radius on the most load-bearing tested core.

**Shape 2** (recommended) mutates only the read path. The daemon holds both
sources (`d.store`, `d.teamCtrl`; RoleHealth is both in-memory and persisted via
`PersistRoleHealth`/`ListRoleHealth`). `handleGet` returns the **join**: live rows
plus synthetic rows for any declared role whose row count is below its replica
count *and* whose RoleHealth explains the gap. The reconciler, its counting, and
its tests are untouched.

**Synthesis rule.** For each declared role (`store.ListTeams()` → `Team.Roles`,
carrying `Replicas`, `Team.Generation`): let `rowCount` = rows of *any* state for
that role. If `rowCount >= Replicas` → no synthesis (saturated/frozen/failstop
roles keep a terminal row, so they are already represented — this is why 83m9's
saturated case needs nothing). If `rowCount < Replicas` **and** RoleHealth has a
record → synthesize `Replicas - rowCount` rows. If `rowCount < Replicas` and there
is **no** RoleHealth record → do **not** synthesize (that is a mid-flight spawn
gap the reconciler fills next tick; a row there would be noise). The projection is
bounded to *explained* gaps.

**State vocabulary.** Future `BackoffUntil` (normal window) → reuse
`SessionCrashLoopBackOff` (the honest k8s term; no new enum, no CLI change),
carrying `RestartCount` and "backoff until T". Sentinel `BackoffUntil` +
`restart_policy=never` → **frozen**; + not-never → **saturated** — but those cases
keep their `SessionFailed` row, so they are essentially never synthesized. Making
"saturated"/"frozen" read differently from a plain "failed" is optional polish via
a projection-only `Reason string` on `Session` (empty for real rows); **do not**
add values to `api.SessionState`, which `CountsAsAlive` and the counting paths
consume. Ship the core with `crashloop-backoff` synthetic rows first.

**Read source.** Snapshot RoleHealth from the controller's **in-memory map**
(authoritative whether or not bolt is enabled), not `store.ListRoleHealth()`
(empty when bolt is off). Home the composition on the controller
(`ProjectHeldRoleRows(store)`), which owns the map and its lock; `handleGet`
appends. Lock order `c.mu` → `store.mu` matches `ReconcileOnce` — no inversion.

**This is an instance of `question-silent-success-instruments`.** A listing that
cannot distinguish "absent" from "held-down" from "saturated" is a status surface
reporting a silent success (the role looks gone / fine) over a live failure — the
same family as finding-030's converged-but-frozen team and finding-027's truth
table. The cross-graph edge to that node is owed and recorded in §9.

## 6. The counting axes — 6kgq (state) and 094e (provenance) are different questions

**6kgq:** filter shiftLaunch's two count sites (controller.go:1073, :1096) with
`CountsAsAlive()`; store query untouched. The create-gate then counts only live
rows (a dead new-gen row no longer suppresses a replacement) and the ready-gate
is fed the live slice (a dead row cannot satisfy "launched"). `nextIndex` stays on
the all-states query so the replacement still gets a fresh index.

**Coherence trap to hand the implementer:** 6kgq's count and the §5 join both use
`CountsAsAlive`, but ask **different questions** — 6kgq counts *alive* rows to
decide whether to *spawn*; the join counts *all* rows to decide whether the role
has *representation*. Do not unify them into one "count of sessions" helper; that
conflation is how this class recurs.

**094e** is provenance, not state: `shiftDrain` correctly wants all states (it
must delete dead old-gen panes), but `len(oldGen) == 0` cannot tell "drained
everything" from "was never populated" (mis-tag / early-delete). Fix with a
per-role **drained counter** on `ShiftState` (internal/api/types.go:321),
incremented on each successful drain delete. When old gen is empty:
`Drained[role] > 0` → real drain, advance; `Drained[role] == 0` → advanced through
a role it never moved → emit a distinct warning (`KindShiftDrainedEmpty`) and
still advance (surface, don't judge — a 0→N scale-up legitimately drains nothing).
**Deterministic repro:** seed `ShiftState{Phase: ShiftDraining, OldGeneration: 1,
Roles: ["worker"], RoleIndex: 0}` with zero gen-1 rows and one ready gen-2
session, tick once, assert the "drained" success path is not taken silently.

## 7. Decay (fv3h) and events (m8n0)

**fv3h** — `RestartCount` is increment-only (controller.go:380, :827, :831); the
only reset is `ClearRoleHealthForTeam` via team delete, so a role's backoff
reflects its lifetime, not recent health. Recommend **success-based decay**
(kubelet's model): a new `RoleHealth.HealthySince` stamp; when a session is
`HealthHealthy` continuously for a window, reset that role's `RestartCount` and
clear `BackoffUntil`. Do **not** auto-thaw the saturation freeze (marvel#107 /
pyre — that re-opens the uncapped-respawn leak). Add the operator verb the ticket
asks for: `ClearRoleHealthForRole` (single-role sibling of
`ClearRoleHealthForTeam`), surfaced as `marvel reset-health <ws>/<team>/<role>` —
also the sanctioned way to thaw a saturated/frozen role short of deleting the
team. Splittable: ship the verb first, decay as follow-on.

**m8n0** — reap-path crash accounting is log-only (controller.go:343/:350/:353);
the health path already emits `KindSessionRestarted`/`KindCrashLoopBackoff`/
`KindRoleSaturated`, but killing a 3-replica role's tmux session yields three
`session.crashed` and no charge/backoff/suppression. The `charged map[string]bool`
already lives in `ReconcileOnce` (controller.go:271) around the reap loop; extend
it to tally per-role charge/suppress counts and, after the loop, emit **one**
`KindCrashLoopBackoff` (or `KindRoleSaturated` on saturation) per role per tick —
"charged 1, suppressed N; restart #R, backoff until T". No new event kind needed
(reuse; if a dedicated `KindCrashCharged` is preferred, add it to `AllKinds`,
events.go:155-167, which backs `--list-kinds`). This is the reap-path counterpart
of finding-019's per-replica-charge fix, made visible on the ring.

## 8. Sequenced harvest (smallest-first; single-owner)

Each step is shippable red→green and names the ticket it closes. The spanning
constraint — *never a State predicate on `ListSessionsByTeamRoleGeneration`; count
at the caller, list in the read path* — is decided here once and honored by every
step.

1. **6kgq** — shiftLaunch filters its two count sites with `CountsAsAlive()`. Test:
   a Failed new-gen session must not suppress a live replacement nor satisfy the
   drain gate.
2. **m8n0** — aggregate the reap loop's `charged` map → one accounting event per
   role per tick. Test: 3 same-role reaps in one tick → exactly one accounting
   event (charged 1, suppressed 2) beside the 3 `session.crashed`.
3. **094e** — `ShiftState.Drained` counter → distinguish drained-from-empty +
   `KindShiftDrainedEmpty`. Repro in §6.
4. **prhx** (**+ close 83m9**) — the read-path join (§5). Test: single-replica
   always role in its backoff window → `get sessions` shows a `crashloop-backoff`
   row instead of nothing.
5. **fv3h** — success-based decay + `reset-health` verb (§7); splittable; last,
   because it changes the RoleHealth lifecycle the join reads.

Disposition: 6kgq→step 1, m8n0→step 2, 094e→step 3, prhx→step 4,
**83m9→restate/close in step 4 (premise refuted, §4)**, fv3h→step 5.

## 9. Harvest pointers

- **`question-silent-success-instruments`** (orc — cross-cutting family): a new
  instance. A `get sessions` that cannot distinguish "absent" from "held-down"
  from "saturated" is an instrument whose failure reads exactly like its healthy
  answer at the point of use — the same shape as this node's other instances
  (finding-027, finding-030, and l8v1's finding-031). **A cross-graph harvest is
  owed:** an orc-side change should add an edge from
  `question-silent-success-instruments` to this finding (marvel/finding-032).
  Recorded here — and flagged OWED — because a marvel PR cannot edit the orc
  node. (Tracked as orc-side harvest debt alongside the same edge owed by
  finding-031.)
- **`question-fanout-composition-safety`** / bd **`aae-orc-2l26`** (orc): this
  cluster is a worked instance of the hazard. The store query
  `ListSessionsByTeamRoleGeneration` is read by five callers with two opposite
  intents (§3); a State predicate added to satisfy one caller silently breaks
  three others. That is why the six tickets stay a single-owner arc rather than
  six parallel PRs. No orc edit is made from here; the reference is the durable
  record of the reasoning.
- **finding-019** (one-loss-event-charged-per-replica): m8n0 (§7) makes that
  per-tick charge visible on the event ring; this finding is its observability
  counterpart.
