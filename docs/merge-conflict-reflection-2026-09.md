# Reflection: marvel merge conflicts, and what they signal

> Commissioned by the director (operator, 2026-09-16). Step 1 reflection and
> step 2 hypothesis are below with the evidence. Step 3, the joint diagnosis
> with the architect and the idiomatic-Go target shape, is folded in once we
> converge. No tickets filed; this reports to the director for routing.

## What actually conflicts

The recent bus work is the sample: #284 (feat/services-list, merged), #285
(feat/bus-service-record, merged), #286 (feat/bus-structural-health, open and
CONFLICTING against main).

The #286 conflict is not a fresh collision. Its branch carries five commits
main lacks, and three of them are the original, un-squashed commits behind
#284 and #285:

```
9e028b9 Merge feat/bus-service-record into feat/bus-structural-health
64481cc Merge origin/main into feat/bus-service-record
7781af4 feat(bus): structural health contract for the managed broker   <- #286's own work
832b5d8 feat(bus): the bus as a Service record ...                      <- #285, already squashed to main
4305478 feat(config): cluster Services list ...                         <- #284, already squashed to main
```

The merge-base against main is e475e9d (#230, a dependency bump), far behind
current main. That is the tell: this is a three-deep stack (services-list ->
bus-service-record -> bus-structural-health). #284 and #285 were squash-merged,
which rewrote their commits into new single commits on main and discarded the
parent linkage. #286 still carries the originals, so merging it replays
4305478 and 832b5d8 against a main that already contains their squashed twins.
Git cannot see they are the same change, so it re-collides on every line those
squashes touched.

The conflicted files confirm the mechanism: CLAUDE.md, cmd/marvel/bus_test.go,
cmd/marvel/main.go, internal/bus/supervisor.go. Each is a file #285 changed and
#286 inherited then changed again. The near-identical +/- counts across #285
and #286 (declared.go +221/-0 in both, manager.go +172/-19 in both,
provision.go +37/-52 in both, render.go +29/-50 in both, config.go +97/-2 in
both) are the stack showing through.

**Character of this conflict: mechanical, and a workflow artifact.** Stacked
feature branches plus squash merges. Not a code-design fault.

## Are the same files hot across many PRs

Yes, and this is the second, separate story. Touch frequency over the last 60
commits on main:

```
15  internal/daemon/daemon.go
11  cmd/marvel/main.go
 8  internal/events/events.go
 5  internal/api/types.go
 4  internal/team/controller.go
 4  internal/session/manager.go
 4  internal/runtime/adapter.go
 4  internal/config/config.go
 4  internal/bus/manager.go
```

And the size of the two hottest:

- `internal/daemon/daemon.go`: 2477 LOC on main (the 2732 first measured was
  on the bus branch). One `Daemon` receiver carries 43 methods. But the
  architect's inspection reframes this and it matters: daemon.go is not a
  business-logic god-struct, it is a request-dispatch hub. `dispatch` calls
  `dispatchAs(req, caller)`, which runs a `switch req.Method` over 25 verb
  cases, each delegating to one `handleX`. Two consequences. The dispatch
  switch is a legitimately shared, one-line-per-verb edit point that rarely
  conflicts semantically; the real collisions come from handler *bodies*
  sharing the one file. And the test suite is already sliced by exactly these
  concerns (bus_test, bus_status_test, bus_leaf_restart_test,
  credential_handlers_test, credential_reveal_test, heartbeat_*, orphans_test,
  plan_test, reset_health_test, 4072 test LOC), which makes the fix in the next
  section unusually safe.
- `cmd/marvel/main.go`: 2775 LOC on main, dispatched through a hand-rolled
  `switch` on subcommand and resource type. It also carries the watch-mode
  column switch and sort logic, which is the same configurable-columns
  substrate the rate-column work (bead aae-orc-o16q2) needs.
- `internal/config/config.go`: 542 non-test LOC, the central `Config` /
  `Cluster` / `Bus` / `Hub` structs. #284, #285, and #286 each extended it.
- `internal/events/events.go`: 494 non-test LOC, the event vocabulary every
  emitter appends to.

The package layout itself is fine. marvel is decomposed into ~25 packages
(admission, api, bus, config, daemon, events, keys, runtime/*, session, team,
usage, tmux, upgrade ...). The problem is not missing packages. It is that in
the hottest packages, nearly all the work lands in a single file:

| Package | non-test LOC | dominant file | share |
|---|---|---|---|
| internal/daemon | 3608 | daemon.go 2732 | 76% |
| cmd/marvel | 3446 | main.go 2838 | 82% |

## Step 2 hypothesis, answered

**It is both causes, and they compound.**

1. The acute #286 conflict is workflow: stacking plus squash. On its own this
   is fixable without touching the code (restack the child on main after each
   parent merges; or land the stack as one PR; or merge rather than squash
   stacked work).
2. The rising baseline is structural. A few god-files (`daemon.go`,
   `main.go`) and central registries (`config.go`, `events.go`) sit on the
   path of almost every feature. Two concurrent PRs that each add a daemon
   method, a CLI verb, a config field, or an event will collide on the same
   file, and often the same region (the shared `switch`, the struct body, the
   enum block), which is a semantic-adjacent conflict, not a clean two-region
   merge.

Concurrency (3 to 5 sessions on a small repo) is the trigger, but it is not the
whole story. On a repo where features touch different files, concurrent PRs
mostly merge clean. Here the god-files act as an amplifier: every feature is
routed through the same few files, so concurrency turns into collision. Squash
plus stacking then turns each merge into a conflict for the next branch in the
stack.

## Idiomatic-Go target shape (joint diagnosis)

Converged with arcaven-architect-g1-0, whose tree inspection reshaped two of
the five moves and the sequencing. The aim: shrink the conflict surface so
concurrent features touch disjoint files, without a rewrite and without breaking
the package API. The load-bearing correction from the architect: the acute and
chronic tracks are independent in cause but *ordered in execution*. Any
file-move refactor of daemon.go, main.go, or config.go that lands while #286 is
open will itself collide with #286. So the workflow fix is the unblocker, not an
orthogonal afterthought. The list is in execution order.

1. **Resolve #286 first (workflow).** Restack feat/bus-structural-health on
   current main after the #284 and #285 squashes, or land the remaining stack
   as a single PR. Until this is done, the refactor moves below would collide
   with it. Going forward: restack a child on main immediately after its parent
   squash-merges, or land stacks as one PR, or use real merges for stacked work
   so ancestry survives and the merge-base does not drift back to an ancient
   commit as #286's did (its base is #230).

2. **Config split by domain, thin aggregator (chronic, cheapest and
   empirically hottest).** Move the sub-structs into `config_bus.go`,
   `config_services.go`, and keep top-level `Config` as a thin aggregator that
   embeds them. A new field then touches only its sub-struct file; the
   aggregator changes only when a whole new domain appears. This ends the
   confirmed #284/#285/#286 three-way collision at near-zero risk and keeps a
   single point for YAML marshal, validate, and default (full per-package
   config registration would scatter those, so hold it unless the aggregator
   line itself becomes hot, which embedding largely prevents).

3. **Daemon handler-body split, same package (chronic, safe on existing
   coverage).** Move the handler *bodies* into per-concern files whose names
   mirror the existing test files: `daemon_bus.go`, `daemon_credential.go`,
   `daemon_session.go`, `daemon_team.go`. Keep the methods on `*Daemon` and
   keep `dispatch` / `dispatchAs` and the verb switch in daemon.go untouched;
   the switch is a legitimate shared edit point that rarely conflicts. The test
   suite already assumes this slicing, so `go test ./internal/daemon/...` is the
   whole safety net; no separate coverage gate is needed for a same-package body
   move. Carve the bus handler cluster first, since that is the in-flight work.
   Do *not* do blanket interface extraction now: the handlers have direct
   coverage against a real Daemon, so interfaces would add indirection to buy
   testability that already exists. Reserve interface seams for the bus
   supervisor and the credential store only, and only later, where they touch
   external state and a substitution seam genuinely helps.

4. **CLI command registry, hand-rolled, unified with the column registry.**
   Replace the subcommand switch with a registry of command values (name,
   flags, run func) each registered from its own file (`cmd_bus.go`,
   `cmd_credential.go`, ...); main.go becomes the dispatcher plus shared
   helpers. Stay hand-rolled: cobra reverses the dependency-light posture
   (SOUL section 2) to buy help and completion generation marvel does not need.
   main.go also holds the watch-mode column switch and sort, which is the
   configurable-columns substrate the rate-column work needs (bead
   aae-orc-o16q2). Design the command registry and the column registry as one
   typed-registry shape so that work converges with this refactor instead of
   racing it.

5. **Events vocabulary to the emitters (chronic, lowest amplitude).** events.go
   keeps the type and the ring machinery; move the event-kind consts next to
   where they are emitted so each package declares its own kinds. This removes
   the central append-point with less churn than a runtime registry and no
   enum-ordering concerns. Last, because it is the lowest-frequency hot file
   (8 of 60).

The two tracks stay conceptually distinct: the workflow fix stops the next
stacked branch from conflicting on already-merged work; the structural moves
stop two unrelated features from colliding just because both had to edit
daemon.go, main.go, config.go, or events.go. Interface extraction is a
selective later move for bus supervisor and credential store only, not a
blanket seam. If the operator wants the structural work tracked, it is one
refactor arc across four files, not four independent tickets.
