# finding-034 — convergence posture: hold at the start line, with a control-plane go-line

Work: aae-orc-rwiw (the fix), discharging aae-orc-cxdf (the LIVE P1
money-spender). Builds on aae-orc-nf0w's plan/apply split (marvel #220).

Related: finding-030 (the empirical grounding — the mechanism confirmed
"cold convergence from zero" as the exact dangerous case), the frontier node
`question-convergence-posture`.

**This is a design + implementation finding, not a measurement.** It records
the load-bearing decision so a future reader (or majordomo author) does not
re-derive it.

---

## 1. The defect, restated

`marvel daemon` against a populated `~/.marvel` rehydrates durable desired
state and the reconciler spawns replicas to reach it — spawning real
claude/codex/opencode processes that spend real tokens — from one unflagged
command. Reproduced live 2026-08-09: 6 roles, 20 sessions, three vendors, in
~3s, against a torn-down demo's leftover bolt. A stale bolt is a standing
instruction to spend money.

## 2. The discriminator (the whole fix in one line)

The dangerous spawn is **cold convergence from zero**: a team with
`desired > 0` and **no live presence**. Every safe recovery path — detach,
reexec, SIGTERM — leaves panes alive, so adoption satisfies desired and
nothing spawns (confirmed in finding-030). So the daemon *can* tell "restarted
under an operator, wants its fleet back" (panes survived → adopted) from
"started fresh/stale, wants nothing" (no panes) — by **the panes**, the fact it
can observe, not a guess.

## 3. What shipped

**Per-team posture** (`api.Team.ConvergencePosture`, persisted; read through
`Team.Posture()` which normalizes the zero value to `hold`):

- `PostureHold` (default) — a team with no live presence does NOT spawn toward
  desired.
- `PostureConverge` — spawns toward desired (the go-line), and the stance a
  team with surviving panes is given at start so its steady-state maintenance
  is never suppressed.

**The gate** (`team.Controller.reconcileRole`, steady-state path only, never the
shift path):

```
withhold a spawn  ⇔  plan.Action == RoleSpawn
                     AND team posture == hold
                     AND the team has zero live sessions
```

The zero-live clause is the load-bearing exception: a team with even one live
replica is being *maintained*, not cold-started, so its top-ups and crash-loop
restarts proceed regardless of posture. A single-replica team that crashes to
zero still restarts, because posture is a latch set at start (converge for a
live team) and a crash does not flip it. `planRole` / `PlanConvergence` are left
RAW (posture-blind) — the dry-run seam (nrk1) is untouched.

**Posture decided at start from ground truth** (`Start` →
`RefreshLiveness` → `InitConvergencePosture`): after adoption, reap records
whose panes did not survive (a host reboot leaves stale `Running` rows), then
set each team's posture from live presence — any live session → converge, none
→ hold. This **overrides** whatever the bolt rehydrated, so a stale bolt that
persisted `converge` cannot cold-spawn: only surviving panes yield converge.
This is the money-safety guarantee, and it is why a persisted posture never has
to be trusted at start.

**The go-line is a control-plane operation** (`SetConvergencePosture` on the
controller; `converge` RPC on the daemon; `marvel converge [workspace/team]` as
one thin client), so a future in-process majordomo pulls the same lever. Empty
team key = every team. `apply` sets applied teams to `converge` (an explicit
"make it so"), so apply's spawn-on-apply behavior is unchanged.

## 4. What it does NOT do (deliberate scope)

- No CLI `hold` verb (the RPC supports the hold direction for the majordomo).
- No `resume-on-power` / start-policy config — that is tu2e, and the start path
  is written so tu2e can make the hold default conditional per team.
- No cost dimension. finding-030 showed the cold-convergence *cost* depends on
  role kind (a headless role with `replicas > 1` re-pays; an interactive role
  is ~free). The hold gate discharges cxdf for both; pricing the posture is a
  future refinement, not part of this fix.
- Scaling a cold held team records the new desired count but spawns nothing
  until `converge` — consistent, and the operator's spawn intent is the
  converge verb.

## 5. Non-regression, tested

- **reexec/detach auto-resume**: a fully-adopted team (panes survived) is set to
  converge at start and reconciles to steady — no spawn (adoption satisfied
  desired). `TestReexecAutoResumeAdoptedTeamDoesNotSpawn`.
- **cannot silently re-arm the spender**: a cold held team run through the
  reconcile boundary spawns nothing, asserted with a nil session manager so any
  spawn attempt panics. `TestReconcileRoleWithholdsColdHeldTeam`.
- **maintenance is never suppressed**: a held team with a live replica is topped
  up to strength. `TestReconcileRoleHoldMaintainsLiveTeam`.
- **host-reboot money-safety**: a rehydrated `Running` record whose pane is gone
  is reaped before posture-init, so the team reads cold and holds.
  `TestRefreshLivenessReapsDeadPaneRecordsBeforePosture`.
- **the go-line**: cold held team spawns only after converge, end-to-end through
  the daemon dispatch. `TestConvergeRPCSpawnsHeldTeam`, plus the empty-key
  all-teams variant and the persisted-converge-override money-safety case.
