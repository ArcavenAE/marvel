# marvel replica bounds: negative, zero, parked

**Date:** 2026-09-26
**Author seat:** arcaven-architect-g5-0, on the director brief
`2026-09-26-marvel-replica-bounds`.
**Status:** recommendation. One bug is filed (marvel#365). Nothing else is
filed or changed.
**Update 2026-09-27:** the negative-count fix merged as #375 and the
delete-on-failed-kill fix as #376 (issue #364); issues #365 and #364 are
still open. Whether 0 is legal desired state (section 5, #335) is not
ruled. Moved here from the author's local notes so the recommendation has
a git home. The identifiers recommendation it cites (R1, R2) is at
aae-orc `docs/design/agent-identifiers-and-work-queues.md`.

## 1. Negative counts: confirmed and filed as ArcavenAE/marvel#365

Reproduced in three scratch unit tests on marvel main e360c58. They ran on the
daemon package's own tmux socket with no live cluster, and the scratch test is
not committed.

- **The scale panics.** `handleScale` stores `-1`, then reconciles
  synchronously. `planRole` indexes `current[-1]` (`controller.go:1166`):
  `panic: runtime error: index out of range [-1]`.
- **The daemon exits.** No `recover()` exists outside tests, and the handler
  runs in the per-connection goroutine (`daemon.go:501`).
- **The value survives a restart.** The count is stored before the panic, and a
  daemon restarted from the default state file loads `-1` and panics on its
  first reconcile. That is a crash loop until the state file is edited.

The CLI path itself was not run. The repro drives the RPC handler, which the
CLI and the simulator's `scale_team` call with the value unchanged.

## 2. What is known about replicas 0 (collected, not re-derived)

- **Parser and `scale` disagree:** the manifest parser refuses `< 1`, while
  `marvel scale` accepts 0.
  - The store has held 0 live for `ops/director` and `ops/supervisor`, while
    the manifest said 1 (marvel#335, bd `aae-orc-5ebrf`, aae-orc finding-178).
  - `marvel plan` compares desired with actual, never with the manifest, so the
    drift is invisible until `marvel work` resurrects the role (#335; #334 is
    the same verb family).
- **The reconciler already handles 0:** it treats it as an ordinary scale-down.
  This is confirmed by reading (the research supervisor's triage of #335).
- **Parked roles are already wanted declaratively:**
  - aae-orc finding-168 lists `merge-queue` and `retrospector` as "declared, replicas 0";
  - aae-orc finding-178 notes that design brief 7's target manifest declares two roles
    at 0 and so cannot be loaded.
  - The workaround today is commenting roles out, which drops their runtime and
    policy from review (#335).
- **Neighbors that bear on the count's meaning:**
  - ADR-010 and #201: a finished headless run holds its replica slot, so a slot
    is not the same as a live process.
  - #107 (closed): `restart_policy never` must hold a terminal slot, not
    replace it.
  - #345 and #348: shift and scale-down picked the wrong session to drain.
    The triage reads #350 as fixing both on main, pending a daemon on that
    build.
  - #363: the index a replacement gets depends on the code path.
- **The triage's alignment note:** option 1 of #335 ("zero is legal desired
  state") fits the declarative model and needs no new verb, and "parity ...
  should come from one validator, not two copies of the bound."

## 3. Recommendation

1. **The contract: `replicas` is a non-negative integer, and 0 is legal desired
   state ("parked").** This is #335 option 1. A parked role stays declared,
   with its runtime, policy and healthcheck, under review in the manifest.
   Negative is refused everywhere.
2. **One validator, called at every door.** A single `api` function checks the
   count. Callers:
   - the manifest parser (the bound becomes `< 0`);
   - `handleScale`, before `admitGrowth` and before `UpdateTeam`, so the CLI,
     the raw RPC and the simulator are all covered;
   - any future writer.

   The error names the team and the role, not `team[0].role[0]` (#335's minor
   item).
3. **Defense behind the doors, so a bad stored value cannot crash-loop.**
   - `planRole` refuses a negative desired count for that role: it emits an
     event, skips the role, and never indexes.
   - Loading the state file validates stored counts and reports any it refuses.
   - A per-connection `recover` turns a handler panic into an error response
     and an event instead of a dead daemon.

   These are the fix shape for #365. The validator alone does not protect a
   state file that already holds `-1`.
4. **Upper bound: leave it to the budget.** `admitGrowth` and
   `admitDeclaration` already refuse growth past `max_sessions` when a budget is
   declared. A hard-coded ceiling would be a second copy of that policy. With no
   budget declared there is no ceiling, and that is a manifest choice.
5. **Drift is a report, not a gate.** `marvel plan` (or `work --dry-run`) gains a
   desired-vs-manifest view, so a `scale` override is visible before the next
   `work` overwrites it. #335's suggested fix and #334's fuller answer point
   the same way. Per SOUL section 8 and `diagnostic-not-gate.md`, it never
   blocks.
6. **Parked versus the seat record (the identifiers party, R2).**
   - With R2's default, a scale to 0 is a scale-down like any other: it retires
     every seat record of the role. Scaling back up reuses the addresses
     (`<role>-0`, per the adopted index reuse) with fresh epochs and empty work
     pointers.
   - The harvest gate `aae-orc-5xa4a` covers scale-down, so parking a live role
     harvests before it deletes.
   - A role declared at 0 from the start has no records to retire.
   - If the operator wants parking as a pause that keeps its seats, that is a
     different state and a ruling (R2 option "never reuse a seat" or a
     separate pause). Do not overload 0 with it.
7. **Parked is not terminal.** A parked role holds zero slots. A
   `restart_policy never` role that failed holds a terminal slot (#107), and a
   finished headless run holds a succeeded slot (ADR-010). Status output should
   show the three differently; today `get teams` shows only desired.

## 4. Tickets to file (named, not filed; only #365 was filed per the brief)

- **Extend `aae-orc-5ebrf` / #335 rather than filing new:** adopt option 1 and
  the shared validator (items 1 and 2). One comment on #335 records the
  decision once the operator rules.
- **#365's fix covers items 2 and 3.** A bd anchor is warranted only if the
  director commits to closing it (the three-layer rule).
- **Desired-vs-manifest drift report** (item 5): a flat marvel issue, or fold
  it into #334. It is related to #335, not a child of it.
- **Status that distinguishes parked, terminal and succeeded slots** (item 7):
  a flat marvel issue.
- **Unverified; check before filing:** what a shift does to a role at 0.
  `shiftLaunch` loops up to `desired`, so it probably launches nothing, but the
  drain and readiness paths were not read.

## 5. Decision needed

Whether 0 is legal desired state is #335's contract question, and nobody has
ruled on it. Default option 1: legal, parked, one validator. Expiry: before
#365's fix lands, since that fix writes the shared validator and must pick
`< 0` or `< 1`.
