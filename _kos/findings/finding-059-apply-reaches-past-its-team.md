# finding-059: applying one team's manifest reaches every live session and the shared broker, so a probe brief scopes its blast radius by team set, not by daemon

Source: the paper review of the model-per-role probe brief, marvel#407
(merged as 0245681). Code cites are at marvel origin/main ee7666d; the brief
cited the same calls at earlier line numbers, before main moved.

**Placement.** marvel is the subject: what `marvel work` and `marvel delete
team` do beyond the team they name.

## What the brief claimed, and what the code does

The first draft of the brief said applying its scratch manifest "cannot
touch a live team", because `handleApply` iterates only the manifest's
teams. That is true of the team loop and false of the daemon. A paper review
caught it before anything ran. Three effects reach past the scratch team:

1. **Re-projection over every live session.** After the team loop,
   `handleApply` calls `sessMgr.Reproject()`
   (`internal/daemon/daemon.go:1242`). `Reproject` walks every session in
   the store that counts as alive, in every team, and rewrites any settings
   file whose projection differs (`internal/session/projection.go:51-80`).
   It logs `apply: re-projected policy for N running session(s)` only when N
   is greater than zero (`daemon.go:1242-1244`).
2. **A broker reload on apply.** `regenerateBus("apply")` (`daemon.go:1246`)
   re-renders the shared managed broker's files and reloads it when they
   changed. A new team's broker user is such a change.
3. **A broker reload on teardown.** Deleting a team calls
   `regenerateBus("delete team " + p.Name)` (`daemon.go:1412`), which removes
   that user and reloads the shared broker again. `p.Name` is the key as the
   CLI received it, so the reason reads `delete team probe/probe-model`, not
   the bare team name.

Every render is recorded as `bus.rendered`, with an info form `bus config
rendered (<reason>)` and a warning form `bus config not rendered (<reason>)`
(`emitBusRender`, `daemon.go:2880-2891`).

## What the run confirmed

The probe later ran under the corrected brief, which made any unexpected
render or any re-projection line a stop condition. The supervisor's
read-only check of the event ring found exactly two `bus.rendered` events
in the window, `(apply)` at 00:40:51Z and `(delete team probe/probe-model)`
at 00:55:03Z. The director reported no re-projection line and no warning
form in the daemon log. So the two reloads happened as predicted, nothing
else rendered, and no live session's projection changed. Every live team on
that daemon still took two broker reloads from a probe that named none of
them.

## The lesson for probe briefs

Scope a "this cannot touch X" claim by what the action creates, changes, or
deletes, not by the daemon or broker it runs on. In marvel, a manifest apply
or a team delete is scoped by team set for manifests, sessions, and roles,
and is daemon-wide for re-projection and the shared broker. A brief that
applies anything should:

- say which of those two scopes its no-touch claim covers;
- name the daemon-wide effects it will cause;
- turn any effect beyond the predicted ones into a stop condition, with the
  exact event or log text to watch for and where to read it (`marvel events
  --kind bus.rendered`, `marvel daemon logs`).

The wrong claim survived drafting because the code it cited was correct.
`handleApply`'s team loop really is scoped to the manifest; the daemon-wide
calls sit after the loop, in the same function, outside the lines the claim
was checked against. Reading to the end of the function is the cheap check.
