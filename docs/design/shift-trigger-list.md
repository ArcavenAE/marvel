# Automatic shift triggers as a list (context pressure, max age)

- **Status:** design for review, 2026-10-02. No code lands until this is
  reviewed. Tracks marvel#437; source idea #399
  (`_kos/ideas/max-session-age.md`).
- **Scope:** slice 1, a list of one or more conditions. AND and OR
  expressions are left room for, not built.

## 1. Why

A role can declare one automatic shift trigger, and only context pressure is
implemented. A seat that never fills its context, or runs on a window marvel
cannot meter (codex, or opencode with no `context_window`), is never rotated,
however long it has been wedged at a prompt or running a harness that has an
update waiting (#399, "Why"). Max session age is the blunt backstop for that,
and the operator wants triggers to combine:

> "to be clear, our intent is to be able to support zero, one, or several
> triggers for autoshift change; it could be unset, it could be context
> pressure, it could be max age, it could be context pressure above X OR max
> age above Y, it could be max age above Z AND context pressure above W
>
> however a simpler version of this, a basic list of one or more conditions
> would be fine initially"

## 2. Today (checked at main ee7666d)

- `ShiftPolicy` (`internal/api/types.go`) has one `On` and `HeadroomTokens`.
- Manifest validation (`internal/api/manifest.go`) rejects any `shift.on`
  other than `context-pressure` and requires `headroom_tokens > 0`.
- `evaluateShiftTriggers` (`internal/team/controller.go:1865`) skips a role
  whose `Shift.On` is not context pressure, fires on the first live
  current-generation session within the headroom of a resolved window, and
  starts a role-scoped shift (`initiateShiftLocked(team, role)`). One
  automatic shift per team per tick, inside a fleet-wide per-tick budget.
- A shift launches the successor first, then drains old seats one per tick,
  oldest first (`shiftLaunch`, `shiftDrain`). marvel does not read or wait for
  a worker's handoff.
- A session's `CreatedAt` is set at create (`internal/session/manager.go`)
  and persisted with the session record in the Bolt store, so it survives a
  daemon restart. A health restart deletes and recreates the session, so it
  gets a new `CreatedAt`.

## 3. Decisions

### D1. The combinator for a plain list is any-of

A list fires when **any** condition holds. This is the operator's first
example ("unset, context pressure, max age, or pressure above X OR age above
Y") and the only reading under which adding a condition makes a role shift
sooner, never later. All-of is the second example and waits for expressions
(section 5). If a reviewer reads the list as all-of, that is a ruling for the
operator through director, not something to settle in review.

### D2. Manifest shape

```toml
# Unset: no [team.role.shift] table. No automatic shift.

# Today's form, unchanged and still valid: a list of one.
[team.role.shift]
on = "context-pressure"
headroom_tokens = 120000

# Slice 1: a list of one or more conditions, any-of.
[team.role.shift]
any = [
  { on = "context-pressure", headroom_tokens = 120000 },
  { on = "max-age", max_age = "8h" },
]
```

Rules at apply (errors, never silent no-ops, as today):
- `on` and `any` together is an error. `any = []` is an error (unset means
  omit the table).
- Each entry is validated as today's single trigger is: a known `on`, and the
  fields that trigger needs (`headroom_tokens > 0`; `max_age` a positive Go
  duration of at least the minimum in D4).
- Two entries with the same `on` is an error in slice 1.
- An entry key slice 1 does not know (`all`, `any` nested, `action`) is an
  error naming it as not yet supported, so a manifest written for a later
  marvel fails loudly on this one.

Internally the single form is normalized to `any` of one, so the evaluator
has one path.

### D3. How age is measured

- From the session's `CreatedAt`, so from spawn. A successor is a new
  session and starts at zero, which is also "from the last shift".
- It survives a daemon restart: `CreatedAt` is persisted.
- A health restart creates a new session, so the clock starts over. That is
  right for the "harness update waiting" case (a new process) and is called
  out because it means a crash-looping seat never ages out; restart backoff
  already bounds that case.
- Age is wall-clock time. A laptop asleep for six hours counts six hours,
  which for this backstop is the safer error.

### D4. A seat mid-turn or mid-merge at the threshold

marvel cannot see a turn boundary or a merge. What it can see is activity:
the context feed timestamp (`ContextAt`) that the activity advisory already
reads. So `max-age` fires in two stages:
- **Quiet threshold:** once age passes `max_age`, fire on the first tick the
  session has been quiet for `quiet_for` (default 2m: no context update).
- **Hard threshold:** fire regardless at `max_age + max_defer` (default 30m),
  because a wedged seat may never go quiet in the feed's sense, and a
  backstop that can be deferred forever is not one.

`max_age` has a floor of 15m, so a typo like `"8m"` for `"8h"` cannot rotate a
team every few minutes. The successor launches before the old seat drains, so
a shift never leaves the role empty; a merge the old seat had begun and not
pushed is its handoff's to record (D5).

Context pressure keeps firing on the level, with no quiet stage: its headroom
already budgets the shift.

### D5. The handoff

The role contract requires a seat to write its handoff on its own threshold,
before anyone asks. A seat cannot see its own age, so for `max-age` marvel
tells it:
- On the quiet or hard threshold, marvel delivers a notice to the seat
  (`shift: max age reached; write your handoff now`) and records
  `shift.handoff-requested` with the time.
- It waits `handoff_window` (default 5m) before starting the shift.

marvel cannot read a handoff, so "no handoff, no shift" is not enforceable in
marvel today. What happens without one: the shift proceeds after the window,
and the successor's first acts (read the predecessor's handoff, report what
changed under it) report "no handoff" as a named class to its supervisor.
That keeps a wedged seat, which by definition will not write one, from
blocking its own replacement. If the operator wants a hard gate instead, it
needs a handoff-observation seam (a terminal-marker check on a declared
path) and is a separate ruling; see section 7.

Context pressure keeps today's behaviour (no notice; the seat crosses its own
pressure threshold and writes the handoff then).

### D6. Interaction with the context-pressure trigger

- Both are evaluated each tick in list order; the first that holds fires.
  One shift per team per tick and the fleet budget are unchanged.
- An in-flight shift suppresses all triggers for the team, as today.
- The event names which condition fired
  (`shift.auto-triggered ... cause=max-age age=8h12m` or
  `cause=context-pressure ...`).
- A session on an unresolved window is skipped by context pressure, as
  today, but **is** evaluated by max age. That is the gap this closes.

### D7. Headless roles (ADR-010)

A headless run finishes and holds its replica slot; there is no successor to
hand to, and a finished session is not running. Slice 1:
- evaluates triggers only on sessions in the running state, so a finished
  headless session never ages out;
- rejects `max-age` on a role with `runtime.mode = "headless"` at apply,
  because a headless run past an age bound is a stuck run, and the answer to
  that is to end it, which is the kill branch (D8), not a shift.

Context pressure on headless roles is unchanged by this design.

### D8. The kill branch

Out of scope for slice 1. #399 names `action = "kill"` for a role that should
not outlive a bound at all. Killing interacts with the restart policy and
with ADR-010's completion semantics (a killed headless run holds or frees its
slot?), and deserves its own design. Slice 1 rejects `action` as not yet
supported (D2), so adding it later breaks nothing.

## 4. Changes, for the builder

- `internal/api/types.go`: `ShiftPolicy` gains `Any []ShiftCondition`;
  `ShiftCondition` holds `On`, `HeadroomTokens`, `MaxAge`, `QuietFor`,
  `MaxDefer`, `HandoffWindow`. The single form normalizes to `Any` of one.
- `internal/api/manifest.go`: the validation rules in D2 and D7.
- `internal/team/controller.go`: `evaluateShiftTriggers` loops over the
  conditions; a `firstSessionOverAge` beside `firstSessionOverRemainder`; the
  handoff notice and window as a pending phase on the role's state, persisted
  so a daemon restart inside the window resumes it.
- Events: `cause` on `shift.auto-triggered`; new
  `shift.handoff-requested`.

## 5. Room for AND and OR later

`any` and a future `all` nest:

```toml
any = [
  { all = [ { on = "max-age", max_age = "12h" },
            { on = "context-pressure", headroom_tokens = 200000 } ] },
  { on = "max-age", max_age = "24h" },
]
```

That is the operator's last example plus a hard cap. Slice 1 rejects `all`
and nested `any` by name (D2), so these manifests fail loudly on a slice 1
daemon and work unchanged once expressions land. No field slice 1 defines
changes meaning when they do.

## 6. Tests (red first)

1. Today's single-trigger manifest parses, normalizes to `any` of one, and
   fires exactly as before (the existing trigger tests pass unchanged).
2. `on` with `any` is an error; `any = []` is an error; a duplicate `on` is an
   error; `all`, nested `any`, and `action` are errors naming the key.
3. `max_age` below 15m is an error; `max-age` on a headless role is an error.
4. A session past `max_age` and quiet for `quiet_for` gets the notice, records
   `shift.handoff-requested`, and a shift starts after `handoff_window`.
5. A session past `max_age` and busy is not shifted until `max_age +
   max_defer`, then is (notice and window as in 4).
6. A session on an unresolved window is shifted by `max-age` and never by
   context pressure.
7. With both conditions, whichever holds first fires; the event's `cause`
   names it; one shift per team per tick holds.
8. A daemon restart keeps a session's age (persisted `CreatedAt`) and resumes
   a pending handoff window.
9. A health restart resets age (new session).
10. A finished headless session is never evaluated.

## 7. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | Any-of for a plain list (D1) | yes |
| 2 | Without a handoff, the shift proceeds after the window and the successor reports it (D5), rather than a hard "no handoff, no shift" gate that marvel cannot enforce today | proceed and report |
| 3 | Defaults: `quiet_for` 2m, `max_defer` 30m, `handoff_window` 5m, `max_age` floor 15m (D4, D5) | as listed |
| 4 | Kill branch out of slice 1 (D8) | yes |

## 8. Rollout note

Both clusters run marvel 0.1.0-alpha.20260926 (7f62729). Nothing here
changes a running cluster until its daemon is rolled, which is the
operator's separate decision. A manifest using `any` or `max-age` is refused
by an older daemon at apply, so manifests should change only after the roll.
