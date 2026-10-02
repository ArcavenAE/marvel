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

A list fires when **any** condition holds. The operator's first examples are
single conditions and "context pressure above X OR max age above Y", and
any-of is the only reading under which adding a condition makes a role shift
sooner, never later. All-of is the operator's last example and waits for
expressions (section 5). If a reviewer reads the list as all-of, that is a
ruling for the operator through director, not something to settle in review.

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

Rules at apply (errors, never silent no-ops):
- **Unknown keys are errors**, at the shift-table level and the entry level.
  Today `ParseManifest` decodes with `toml.Unmarshal` and `yaml.Unmarshal`
  (`internal/api/manifest.go`), which drop unknown keys, and `ManifestShift`
  has only `On` and `HeadroomTokens`. So the builder adds detection: for
  TOML, `toml.MetaData.Undecoded()` from `toml.Decode`, reporting any
  undecoded key under a role's `shift`; for YAML, a `yaml.v3` decoder with
  `KnownFields(true)` for the shift section. The error names the key and
  says "not supported by this marvel". This is what makes `action` (which
  #399 sketches on the shift table itself), `all`, and nested `any` fail
  loudly instead of vanishing; `action = "kill"` is the case that matters.
- A shift table with neither `on` nor `any` is an error, stated as such (it
  fails today only by accident, as `shift.on "" is not valid`).
- `on` and `any` together is an error. `any = []` is an error (unset means
  omit the table).
- Each entry is validated as today's single trigger is: a known `on`, and the
  fields that trigger needs (`headroom_tokens > 0`; `max_age` a positive Go
  duration of at least the floor in D4).
- Two entries with the same `on` is an error in slice 1.

Internally the single form is normalized to `any` of one, so the evaluator
has one path.

### D3. How age is measured

- From the session's `CreatedAt`, so from spawn. A successor is a new
  session and starts at zero, which is also "from the last shift".
- It survives a daemon restart: `CreatedAt` is persisted.
- A health restart creates a new session, so the clock starts over. That is
  right for the "harness update waiting" case (a new process). It also means
  a crash-looping seat never ages out: `max_restarts` defaults to 0
  (unlimited) and backoff caps at 5m, so such a seat restarts indefinitely.
  That is accepted here, because each restart is a fresh process and the
  crash-loop machinery owns that case; max age does not.
- **Exception, stated:** a role whose `runtime.args` carry `--resume` or
  `--continue` comes back from a health restart into the same conversation
  (`internal/runtime/claude.go`), yet its age resets. Slice 1 documents this
  rather than measuring from the slot's first session; a role that resumes
  should rely on context pressure for its conversation length.
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

The written succession contract rules out an automatic shift without a
handoff:
- wardrobe `contents/roles/supervisor.md`, CANNOT: "shift without a handoff
  naming what is dropped (none)";
- `contents/ask-classes.yaml`: `shift-call` is "a shift to be called for a
  worker past its threshold, on a handoff that already exists";
- `contents/fragments/succession.md`: "Where the harness hides the pressure
  reading, the spawner requests the write." and "never auto-shift
  unwatched";
- `contents/processes/team-supervision.md`, step 4: "a partial handoff fails
  its automatic start closed".

A seat cannot see its own age, so marvel is the spawner that requests the
write. The default conforms to the contract:

1. **Request.** On the quiet or hard threshold (D4), marvel delivers a notice
   to the seat (`shift: max age reached; write your handoff now`) and records
   `shift.handoff-requested` with the time.
2. **Observe (the seam).** A role that declares
   `handoff = "<path with {session}>"` and `handoff_marker = "<line>"` gets a
   terminal-marker check: marvel shifts when the file exists and its last
   line is the marker. This is the observation seam; marvel reads one line of
   one declared file and nothing else.
3. **Escalate, never shift unwatched.** If `handoff_window` (default 5m)
   expires with no marker, or the role declares no handoff path, marvel emits
   `shift.handoff-missing` (warning: session, age, requested-at), leaves the
   seat running, and the team's supervisor decides whether to call the shift.
   The supervisor is the watcher the contract requires. For a wedged seat this
   makes max age a reliable alarm, which is most of its value.
4. **Tell the successor what it is.** Today a seat spawns with
   `MARVEL_ROLE`, `MARVEL_SESSION`, `MARVEL_TEAM`, `MARVEL_WORKSPACE` and
   transport variables, and nothing marks it a successor (finding-044 Part B:
   it "came up cold"). Every shift (any trigger, and operator shifts) adds
   `MARVEL_PREDECESSOR` (the predecessor's session key) and, when a request
   was made, `MARVEL_HANDOFF_REQUESTED_AT`. Only then can a successor tell
   "my predecessor wrote nothing" from "I have no predecessor".

**Open, for the operator (conflict stated).** Proceeding with the shift after
the window when no handoff exists would be an automatic shift with no handoff
and no watcher, which the contract above forbids. If it is wanted for some
roles, it would be a per-role opt-in field (`on_no_handoff = "shift"`, default
`"escalate"`), ruled by the operator in writing, following the restart-policy
precedent for a default that consumes state. This design does not include it
in slice 1; it is ruling 2.

Context pressure keeps today's behaviour (no notice; the seat crosses its own
pressure threshold and writes the handoff then), and gains only the successor
variables in item 4.

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

- `internal/api/manifest.go`: strict unknown-key detection for the shift
  section in TOML (`toml.Decode` metadata) and YAML (`KnownFields(true)`);
  `ManifestShift` gains `Any`, `MaxAge`, `QuietFor`, `MaxDefer`,
  `HandoffWindow`, `Handoff`, `HandoffMarker`; the rules in D2 and D7.
- `internal/api/types.go`: `ShiftPolicy` gains `Any []ShiftCondition`; the
  single form normalizes to `Any` of one; handoff fields on the policy.
- `internal/team/controller.go`: `evaluateShiftTriggers` loops over the
  conditions; a `firstSessionOverAge` beside `firstSessionOverRemainder`; the
  request, marker check and escalation as a pending phase on the role's
  state, persisted so a daemon restart inside the window resumes it.
- `internal/runtime/adapter.go`: `MARVEL_PREDECESSOR` and
  `MARVEL_HANDOFF_REQUESTED_AT` in the successor's environment on every
  shift.
- Events: `cause` on `shift.auto-triggered`; new `shift.handoff-requested`
  and `shift.handoff-missing`.

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
2. In both TOML and YAML: table-level `action = "kill"`, table-level
   `all = [...]`, an entry-level unknown key, and nested `any` are each errors
   naming the key; a shift table with neither `on` nor `any` is an error;
   `on` with `any`, `any = []`, and a duplicate `on` are errors.
3. `max_age` below 15m is an error; `max-age` on a headless role is an error.
4. A session past `max_age` and quiet for `quiet_for` gets the notice and
   records `shift.handoff-requested`; when the declared handoff file ends in
   the marker, the shift starts.
5. With no marker by the end of `handoff_window`, or no declared handoff
   path, marvel emits `shift.handoff-missing` and does **not** shift; the
   seat keeps running.
6. A session past `max_age` and busy is not requested until
   `max_age + max_defer`, then is (as in 4 and 5).
7. A session on an unresolved window is handled by `max-age` and never by
   context pressure.
8. With both conditions, whichever holds first fires; the event's `cause`
   names it; one shift per team per tick holds.
9. A daemon restart keeps a session's age (persisted `CreatedAt`) and resumes
   a pending request.
10. A health restart resets age (new session).
11. A finished headless session is never evaluated.
12. A successor of any shift has `MARVEL_PREDECESSOR` set to the
    predecessor's session key, and `MARVEL_HANDOFF_REQUESTED_AT` when a
    request was made; a first spawn has neither.

## 7. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | Any-of for a plain list (D1) | yes |
| 2 | Should any role be allowed to shift after the window with no handoff (`on_no_handoff = "shift"`)? This conflicts with the written succession contract ("never auto-shift unwatched"; supervisor CANNOT "shift without a handoff"), so it would override it for that role (D5) | no: escalate, never shift unwatched; not in slice 1 |
| 3 | Defaults: `quiet_for` 2m, `max_defer` 30m, `handoff_window` 5m, `max_age` floor 15m (D4, D5) | as listed |
| 4 | Kill branch out of slice 1 (D8) | yes |

## 8. Rollout note

Both clusters run marvel 0.1.0-alpha.20260926 (7f62729). Nothing here
changes a running cluster until its daemon is rolled, which is the
operator's separate decision. A manifest using `any` or `max-age` is refused
by an older daemon at apply, so manifests should change only after the roll.
