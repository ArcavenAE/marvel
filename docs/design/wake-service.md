# Wake as a marvel service: the states marvel tracks and the actions it takes (marvel#580)

- **Status:** design direction, draft for review, 2026-10-05. No code. Checked
  at marvel main 7d1e5ef and director main 33ed203; every line reference
  below is at those commits.
- **Scope:** records the operator's direction and the questions it opens.
  What ships under #580 and what follows is the operator's call (section 9).

## 1. Why

A cluster can lose its leaf to the hub for hours, and today nothing tells
anyone: marvel logs one line and publishes nothing (#580). The same gap
covers other states marvel already reads, such as a seat stopped at a menu
or a handoff that never came. The operator's direction is to fix this once,
as a marvel service, rather than case by case.

## 2. The direction

The operator, verbatim, from an interview on 2026-10-05. This is the
operator's ruling of that date, relayed word for word by director to the
arcaven supervisor (bus message `01M469BEJA`, item 5):

> "this is a service that marvel can expose to authorized systems, marvel bus
> can be configured to use nats, and director is a user of those. It might
> not be the only user, but marvel should have the mechanical parts to do
> these things for services that are allowed to do it, and marvel should
> know about these states and should be able to take these actions on it's
> own or on behalf of services like director"

Read plainly, it says four things:

- marvel owns the mechanics;
- director is one user of them, not the only one;
- only services that are allowed may use them;
- marvel may act on its own or on a service's behalf.

The rest of this document works out those four.

This refines one earlier line. `docs/design/scheduled-runs.md:383-384` says "A
cue into a seat is a message, and it belongs to director". Under this
direction, the message (what it says and whom it is for) stays with director.
The mechanics of reaching the seat are marvel's.

## 3. What exists today

| Fact | Where |
|---|---|
| The leaf check polls `/leafz` every 30 s and emits only on a change | `internal/bus/supervisor.go:152`, `:701`, `:726-728` |
| A leaf already down at the first poll after a start or reexec emits nothing (`down` needs `prev != nil`). The same guard keeps an unenrolled hub from ever emitting `down`, which a test pins | `supervisor.go:731-732`; `internal/bus/supervisor_test.go:279-283` |
| Leaf states `Status()` reports: `n/a`, `unenrolled`, `detached`, `unknown`, `up`, `down` | `supervisor.go:678-692` |
| A reexec drops the transient leaf seed, and the cluster runs local-only until the seed is pushed again (#339) | `docs/admin-guide.md:509-510`; `credential.transient-dropped`, `internal/events/events.go:258` |
| The hub URL is read once, at start (#514) | `docs/admin-guide.md:468-472` |
| Seat states marvel reads: the composer (`empty`, `holds_text`, `mid_turn`, `menu_unsafe`, `shell`, `unknown`), the activity advisory (`active`, `stalled`), and a usage-limit menu | `internal/composer/composer.go:21-34`; `internal/api/types.go:97-106`; `internal/daemon/limits.go:53-65` |
| marvel already types into a pane on its own, twice: the max-age handoff request and the limit-menu key | `internal/daemon/daemon.go:433-450` (`injector=marvel:max-age`); `internal/limitact/action.go:166` (`injector=marvel:limit-wait`) |
| Events live in an in-memory ring, read by `events.watch` and `marvel events --follow`. No Go code publishes to NATS | `events.go:535`; `daemon.go:1130`; `docs/design/handoff-missing-delivery.md:17-19` |
| A ring-to-bus tap (`events.marvel.<cluster>.>`) is planned and gated | `docs/design/internal-bus.md:264-266` |
| Callers have two scopes, `admin` and `credential-push`; the local socket is always admin; the operator grants keys with `marvel keys authorize --scope` | `internal/daemon/scope.go:15-27`, `:108`; `cmd/marvel/main.go:2013` |
| An inject's origin records a session and user the caller declares, which nothing checks | `daemon.go:3450-3459` |
| director's channel cue wakes an idle Claude seat from inside the harness, with metadata only. For projected seats it waits on #422 | director `sim/design/channel-cue.md:9-16`, `:47-55` |

No principal called "an authorized service" exists in the code today.

## 4. The states marvel tracks

**Link states.** These are the leaf states above, each with the time it
was entered, so a reader can tell "down" from "down for two hours". There
are two cases #580 needs:

- **Down and staying down.** It is reported when it starts, and again while
  it lasts.
- **Unenrolled after a restart.** The cluster has quietly gone local-only
  because the seed was dropped. This is the case aae-orc#461 hit on a third
  cluster.

There is also one gap to close whatever else is decided: an **enrolled** leaf
found down at the first poll must still be reported. Today it is silent until
it comes up. An unenrolled hub stays out of it: no link was ever promised, it
already has its own state (`unenrolled`, `leafEnrolled`,
`supervisor.go:682`), and `TestSupervisorReportsUnenrolledHubOnce` pins that it
never gets `bus.leaf.down` (`supervisor_test.go:279-283`).

**Seat states.** No new reading is needed. These are the composer states,
the stalled advisory, a held usage-limit menu, and a pending or missed
handoff. The service makes them available to authorized callers; it does
not add new ones.

**Questions:**

- **Q1.** After what duration does a persisting state get reported again,
  and how often after that? Is the bound one fleet value, or per cluster?

  **Answered 2026-10-06.** The options as put to the operator:
  "(a) Rule Q1 now, and #600 ships with that value."
  "(b) Ship #600 without the repeat; the other three section 8 items still
  land, and the repeat follows the ruling."
  "(c) Accept 30 minutes, one fleet value, as the starting setting,
  changeable as one constant."
  The operator's words, relayed by director in message
  `01M48QDPXS16M1Q43C6XF968TV`: "4 c". Option (c) means 30 minutes, one fleet
  value, changeable as one constant; the arcaven supervisor, who wrote the
  options, confirms it.
- **Q2.** Are the time-in-state readings stored with the record, so they
  survive a daemon restart, or kept in memory, so a restart starts the clock
  over?

  **Answered 2026-10-06.** The operator's words, relayed by director in
  message `01M492CZRX41N4E276G6J6D3Z9`: "it restarts at zero". A leaf's down-for duration is kept in the daemon's
  memory and starts again from zero when the daemon restarts. That is what
  #600 ships and what the admin guide says (the duration is not stored with
  the record).

## 5. Who is authorized

**Direction.** An authorized service is a principal that holds a grant
naming the actions it may ask for. The operator grants it through the path
that exists today, a key authorized with a scope. A service never grants
itself, and a caller's declared identity is never enough (`daemon.go:3450-3459`
is the gap). director is one such principal. A future one, such as a
scheduler or another supervisor tool, gets its own grant.

Two consequences follow:

- **Actions taken for a service are attributed to the authenticated
  principal**, not to what the caller says it is. The inject origin records
  that principal.
- **A grant names actions, not just reach.** Reading seat state, waking a
  seat, and subscribing to state changes are separate, so a service can hold
  one without the others.

**Questions:**

- **Q3.** What does a grant look like? New scopes beside `admin` and
  `credential-push` (for example a read scope and a wake scope)? Or the role
  model that is still unbuilt (aae-orc-bs3x, `question-permission-model`)?
- **Q4.** Is a grant per cluster, per team, or per seat? Can director wake
  any seat, or only seats on teams that opted in?
- **Q5.** How does a service that reaches marvel only over the NATS bus,
  with no SSH key, prove its grant? Is a broker user enough, or does it need
  something marvel can check itself?
- **Q6.** Seats carry `MARVEL_SOCKET`, and the local socket is admin. Does a
  seat count as a principal under this model, or as the daemon owner? This
  is an inference from `scope.go:108`, not something measured. One read-only
  measurement settles it: `marvel --socket "$MARVEL_SOCKET" get sessions`
  from a seat's shell. It has not been run.

## 6. Actions: on its own, or on a service's behalf

**On its own.** marvel already acts on its own authority in two places
(`usage-limit-pause.md:504`, "B is the daemon acting on its own
authority"). Under this direction, its own actions on these states are:

- record the state and its duration;
- emit an event when a state starts, and again while it persists (Q1);
- keep the actions it already takes: the handoff request and the
  limit-menu key, each under its existing rule.

**On a service's behalf**, for a principal whose grant covers the action:

- **Read** a seat's state and a cluster's link state.
- **Subscribe** to state changes.
- **Wake** a seat. marvel puts a fixed cue into the pane, chosen by the
  service and kept short, through the same send path as its own injects.
  This is the mechanism for seats the channel cue cannot reach today:
  projected seats until #422, and harnesses other than Claude.

**Guards on a wake.** These belong to marvel, whoever asks. Two exist today
and three are proposed:

- **Existing:** a usage-limit menu refuses the send (`daemon.go:2211-2217`).
- **Existing:** a text that starts with a menu digit is refused on a pane the
  capture cannot place, the bare-digit rule (`daemon.go:441-448`, as the
  max-age handoff uses it).
- **Proposed:** the composer must read `empty`.
- **Proposed:** the wake is rate-limited per seat.
- **Proposed:** every wake is an event naming its principal.

marvel does not decide that a seat should be woken for a service. The
service decides, and marvel checks and carries it out.

**Questions:**

- **Q7.** Should any link state lead marvel to act on the link by itself?
  For example, re-reading a changed hub URL (#514), or holding new
  global-mode spawns. Or are reporting and the existing reconnects enough?
- **Q8.** Should a wake ever go to a seat whose composer holds text, with
  the cue staged after it? Or is `empty` the only state that may be woken?

## 7. How a supervisor hears

Today, a supervisor hears nothing (section 3). There are three ways it
could, and they layer rather than compete:

1. **An event on the bus.** The planned tap publishes the ring to
   `events.marvel.<cluster>.>`. A subscribed service (director first) routes
   it. This is the general path, and the one the direction asks for, since
   the bus is NATS and director is its user.
2. **Direct delivery to the team's supervisor role**, as
   `docs/design/handoff-missing-delivery.md` H1 designs it for one event:
   `role://<team>/supervisor` on the director bus. This is useful where no
   service is subscribed, at the cost of marvel holding an agent address,
   which `usage-limit-pause.md:133-134` notes it does not do today.
3. **A wake of the supervisor seat**, so a supervisor idle at an empty
   composer reads its inbox. This happens only when a service asks for it
   (section 6), never as the first signal.

**Questions:**

- **Q9.** Is the bus tap (1) the first step, with 2 and 3 after it? Or does
  #580 need 2 so that one cluster with no service subscribed is still heard?
- **Q10.** Which events cross to the bus? All of them, or a named set
  (leaf, unenrolled, unprovisioned, handoff-missing, limit), so the global
  tier is not flooded?

## 8. #580 under this direction

The smallest step that answers #580 and fits the direction:

- record when the leaf state was entered;
- report an enrolled leaf that is down at the first poll;
- emit a repeated event while it stays down past the Q1 bound;
- show the duration in the bus status.

Delivery is whichever path section 7 settles. Until the tap lands, the event
stays readable on the ring, as now.

## 9. Scope (the operator's)

These are listed, not decided:

- whether #580 is the section 8 step alone;
- whether the authorization model (section 5) is designed before any wake
  action ships;
- whether the wake action (section 6) is a separate issue;
- the order of the three delivery paths (section 7).

Q1 and Q2 are answered (section 4). Q3 to Q10 are the operator's to answer, or to send to a party.
