# Delivering team.shift-handoff-missing to the team's supervisor

Status: design for review, 2026-10-02. Owner: the architect role. Tracks
marvel#459 (step B of the operator's ruling "A then B"; step A is the
re-emit in #455). Builds on `docs/design/shift-trigger-list.md` D5. Design
only; the build waits for the #454, #455 and #456 stack.

## 1. Why

When a seat misses its max-age handoff window, D5 step 3 says the team's
supervisor decides, but the only signal is an event in the daemon's
in-memory ring. #455 repeats it so a restart or a ring wrap cannot hide it,
but a supervisor still sees it only by watching the ring. This delivers the
escalation to the supervisor seat itself, over the bus it already reads.

## 2. Today (checked on main and the #455 branch)

- marvel publishes no envelope today. Its only bus connections are
  provisioning and health (`internal/bus/provision.go`, `health.go`), under
  the admin principal, "marvel's own identity, provisioning and break-glass,
  never handed to a session" (`internal/bus/declared.go`).
- The canonical envelope (`contracts/schema/director-envelope.schema.json`)
  accepts `role://{team}/{role}` as a recipient. marvel#457 concerns
  `global://` only; a local role address is valid today.
- A director shim whose seat has a role reads its role inbox,
  `agent.<ws>.<team>.role.<role>.inbox`, through its durable (director
  `bus.go` `selfSubjects`). AGENT_INBOX is a durable stream.
- An escalated request persists with the team and ends when its session is
  gone or replaced (`advanceShiftRequest`, `dropShiftRequest`), which is
  what a shift does.

## 3. Design

### H1. The recipient

The role named `supervisor` in the escalated seat's team, as #459 proposes
(the convention `shiftOrder` and the bus manager already use). Address:
`role://<team>/supervisor`, published on the subject
`agent.<ws>.<team>.role.supervisor.inbox`. A role address reaches whichever
seat holds the role now, so a supervisor that shifted still gets it.

- A team with no `supervisor` role: no delivery. The repeating event
  continues, plus one log line per escalation, `escalation has no
  recipient: team <ws/team> declares no supervisor role`. Never delivered to
  the escalated seat or to any other role.
- A `supervisor` role with no running seat: publish anyway. The message
  waits in the durable stream for the next holder, which is the point of a
  durable channel. The event says `delivered: queued, no live holder`.

### H2. The envelope

| field | value |
|---|---|
| `performative` | `REQUEST` (it asks the supervisor for a decision) |
| `sender` | `{agent_id: "marvel", workspace: <ws>}` |
| `recipient.address` | `role://<team>/supervisor` |
| `authority` | `{strength: "none"}` (marvel states a fact and asks; it carries no one's authority) |
| `content.type` | `signal` |
| `content.data` | one line: `handoff-missing: seat <session> in <ws/team> role <role> passed max age <max_age> at <time>; no handoff by <deadline>. Decide: marvel shift <ws/team> --role <role>, or leave it running.` |
| `content.refs` | none; the text carries the session key |
| `correlation_id` | `handoff-missing/<ws>/<team>/<session>/<requested_at unix>`, the same on every repeat, so a reader can group them and the loop guard keys on it |
| `message_id` | a fresh ULID per send, as the schema requires |
| `reply_by` | the next repeat time |

No new performative or content type is needed: `REQUEST` with a `signal`
content fits the schema as it stands. The build validates one sent envelope
against `contracts/go/envelope` in a test.

`marvel` as a sender agent id must not collide with a seat: the build checks
that no team declares an agent id `marvel`, the same way `director` is
reserved (`config.ReservedBusUsers`).

### H3. The reply

marvel does not read a reply. The supervisor's decision is an action, and
marvel observes the action:

- **Shift:** the supervisor (or the operator) runs `marvel shift <ws/team>
  --role <role>`. The shift replaces the seat, the request is dropped, and
  delivery stops.
- **Leave it running:** the supervisor does nothing, and the repeats
  continue once per window until the request ends some other way. A way to
  quiet the repeats without shifting (for example `marvel shift --dismiss`)
  is ruling 2; the default is none in this slice, because a standing
  escalation that is quiet is the silent hold #453 was about.

Making marvel a bus consumer to read replies would be a larger change
(a subscription, its own durable, parsing intent from text) for no gain over
observing the shift. The supervisor's reply to whoever relayed it, if any,
stays the supervisor's business.

### H4. Cadence and idempotency across a daemon restart

- Deliver at escalation and then once per window, on the same schedule as
  the #455 event, and stop when the request ends.
- The persisted request gains `DeliveredCount` (and `LastDeliveredAt`),
  written in the same store update that records a send. On restart, marvel
  sends again only when the next window boundary passes, so a restart does
  not produce an extra message.
- Each publish sets the JetStream dedupe header `Nats-Msg-Id` to
  `<correlation_id>/<DeliveredCount>`. If the daemon dies between the publish
  and the store write, the re-send after restart carries the same header and
  the stream drops it as a duplicate within its dedupe window. Outside that
  window a reader sees one extra repeat with the same `correlation_id`,
  which is harmless and visible.

### H5. When the bus is down

- The publish uses a short timeout and never blocks the reconcile loop.
- On failure the escalation event still repeats (step A is the floor), and
  marvel emits `team.shift-handoff-undelivered` once per transition from
  delivered to undelivered, with the error, and logs one line.
- It does not count a failed send, so the next window boundary tries again,
  and a send also follows the bus's next ready transition (`BusGate`), so a
  short outage does not wait a whole window.
- It never falls back to keystrokes into any pane.

## 4. Builder changes

- `internal/team/shift_handoff.go`: a `deliverHandoffMissing` beside
  `emitHandoffMissing`, called at escalation and from
  `repeatHandoffMissing`; the delivered counter in `api.ShiftRequest`.
- A small publisher on the daemon's existing admin connection, behind an
  interface the controller holds (nil when no managed bus, which means
  event-only, as today).
- `config.ReservedBusUsers` (or the agent-id equivalent) reserves `marvel`.
- Events `team.shift-handoff-undelivered`; the existing event gains a
  `delivered:` field (`sent`, `queued, no live holder`, `no recipient`,
  `undelivered: <error>`).

## 5. Tests (red first)

1. A team with a running supervisor: one envelope on
   `agent.<ws>.<team>.role.supervisor.inbox` at escalation, valid against
   the canonical schema, with the fields in H2.
2. A repeat after one window carries the same `correlation_id` and a new
   `message_id`; a shift ends the request and no further envelope follows.
3. A team without a `supervisor` role: no publish, one log line, events
   continue; no keystrokes into any pane.
4. A supervisor role with no live seat: the envelope is stored and a seat
   started later reads it.
5. Restart: escalate, send, restart the daemon before the next boundary; no
   second envelope until the boundary. Kill between publish and store write:
   the re-send is dropped by `Nats-Msg-Id` within the dedupe window.
6. Bus down: no blocking, `team.shift-handoff-undelivered` once, the event
   repeats; on bus ready a send follows without waiting a full window.
7. A manifest declaring an agent id `marvel` is refused.

## 6. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | The recipient is the role named `supervisor` in the same team (a convention, not a declared field) | yes |
| 2 | A way to quiet the repeats without shifting | none in this slice |
| 3 | marvel publishes as `marvel` on its existing admin connection, not a new scoped principal (a scoped principal held by the same daemon adds no boundary) | yes |
