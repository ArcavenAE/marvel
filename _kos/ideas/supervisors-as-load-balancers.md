# Supervisors as load balancers: fan out workers when work piles up

- **Status:** idea (pre-hypothesis, no commitment). Captured, not designed.
- **Date:** 2026-10-22
- **Origin:** the operator, relayed by director.
- **Subject:** marvel (who changes a team's agent counts, and on what signal). Filed in the marvel graph per the subject test.

## The operator's words

> "marvel supervisor agents as loadbalancers, faning out select members of their teams, ie workers, etc when work queue depth increases or the operator is expressing interst in ramping work, or suggesting priority work; this was part of the origianl intent with supervisors having the ability to request changes to the team, increasing agent counts but has never been realized"

## Prior art in marvel

Each pointer was found in the tree or its history at `origin/main` 740115f.

- Original intent: `docs/spec/requirements.md` FR-11 (Supervisor Role), added in bed9be3, which also gave the Lua supervisor scripts a `scale_team` call. I found no text in marvel that states the supervisor-requests-team-changes intent in so many words.
- Scale: `marvel scale <workspace/team> --replicas N` (`scaleCmd` in `cmd/marvel/main.go`); `docs/design/replica-bounds.md` for the bounds (negative, zero, parked).
- Admission check: `internal/admission` and `internal/daemon/admission.go`; `_kos/findings/finding-009-admission-declared-budget.md` (refuse the declaration, not the spawn).
- Idle scheduler and marry: `internal/api/readiness_marry.go` (the marrying step pairs ready items with free seats; shadow only, sends nothing) and `cmd/marvel/marry.go`, with golden vectors in `contracts/readiness/marry-vectors.json`. The idle-scheduler design v2 itself is not found in marvel.
- Freebusy: `_kos/ideas/agent-duty-cycle-as-a-busy-measure.md` and `docs/design/team-load-rollup.md` (which teams are free, busy, blocked or overloaded).
- Idle-shadow: lives in the public director repo, `skills/director/scripts/idle-shadow` (director#379, director#381); marvel holds no copy. See also the shadow-only marrying step above.
- Declared cadence: `docs/design/declared-cadence.md`.
- Scheduled runs: `docs/design/scheduled-runs.md`.
- Usage limits: `docs/design/usage-limit-pause.md` and `_kos/findings/finding-050-backend-usage-limit-observability.md`.
- Supervisors' RPC reach: `_kos/ideas/approve-by-keyboard-over-the-bus.md` quotes the planned role-based grant ("Supervisors get `inject` + `scale` + `shift` + `capture`"), which does not exist yet.
- Orchestrator ADR-003 (Kubernetes-like resource model for marvel) and ADR-007 (Bainbridge automation boundary: automate reminding, checking and proposing, not judging). A supervisor may propose a scale change; who confirms it is the automation-boundary question.

## Open questions

- What signals count: queue depth, the operator's expressed interest in ramping, a priority request, or something else?
- Who confirms a scale-up, a scale-down and a retirement: the supervisor, the operator, or marvel?
- What about quota and cost: how does a fan-out meet usage limits and declared budgets?
- How does this differ from marvel's own reconciler, which already drives replicas toward a declared count?

## See also

- [seat-modes-headless-standby-standing.md](seat-modes-headless-standby-standing.md): what kind of seat each worker is (headless, hot standby, standing), once the count is set.
