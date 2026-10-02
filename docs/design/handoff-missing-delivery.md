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
- A director shim reads its role inbox,
  `agent.<ws>.<team>.role.<role>.inbox`, only when it was started with
  `DIRECTOR_ROLE` (director `bus.go` `selfSubjects`). marvel's `baseEnv` does
  not set it today (`internal/runtime/adapter.go`); `cast-launch.sh` sets it
  for wrapped seats only, and `docs/design/seat-bootstrap.md` records that
  `role://` delivery breaks until SB-4 adds it to `baseEnv`.
- A new shim durable starts with `DeliverAllPolicy`, so a holder that starts
  later reads role mail already stored. AGENT_INBOX keeps messages 24h and
  dedupes on `Nats-Msg-Id` for 2 minutes (`internal/bus/declared.go`).
- The shim refuses its own `role://` send when no live session holds the role
  (R-92). marvel publishes on the subject directly, so that check does not
  apply to it; H1 states what marvel does instead.
- The canonical schema allows `[a-z0-9][a-z0-9-]{0,31}` in an address
  segment; marvel accepts team and workspace names in `[A-Za-z0-9_-]`, so
  some valid marvel teams cannot be addressed.
- An escalated request persists with the team and ends when its session is
  gone or replaced (`advanceShiftRequest`, `dropShiftRequest`), which is
  what a shift does.

## 3. Design

**Precondition P1.** Delivery ships off, and is switched on only in a build
that also carries SB-4 (`DIRECTOR_ROLE` in `baseEnv`). Without it a
marvel-spawned supervisor seat has no role subject in its durable, so a
publish would be stored and read by no one, which is the silent hold again.
A supervisor spawned before SB-4 without the wrapper receives once it
respawns; until then the step-A event is the floor, as today.

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
  waits in the stream, and the next holder's new durable reads it
  (`DeliverAllPolicy`), which is the point of a durable channel. The event
  says `delivered: queued, no live holder`.
- **The escalated seat is a supervisor.** When the escalated session's own
  role is `supervisor`, marvel does not deliver: a role address would reach
  every holder's durable, the escalated seat's included, which this section
  forbids. The event says `delivered: none, escalated seat holds the
  supervisor role`, one log line says the same, and the decision falls to the
  operator through the event and `get sessions`. marvel cannot reach director
  from here (it holds no global address, and marvel#457 keeps `global://`
  out of the schema), so escalating to director is ruling 4.
- **A team or workspace name the schema cannot address** (upper case, `_`,
  or over 32 characters): no delivery, `delivered: none, team name not
  addressable`, and one warning at `marvel work` naming the team. Not
  refused at apply, since refusing would stop running teams.

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
| `expires_at` | the next repeat time plus one window: a queued message lives 24h and a shift mints a new holder, so a stale escalation must not be read as current; every repeat supersedes it |

No new performative or content type is needed: `REQUEST` with a `signal`
content fits the schema as it stands. The build validates one sent envelope
against `contracts/go/envelope` in a test.

`marvel` as a sender agent id must not collide with a seat: the build checks
that no team declares an agent id `marvel`, the same way `director` is
reserved (`config.ReservedBusUsers`).

**The sender is a label, not authentication.** Any team principal that can
publish to a role inbox can write an envelope that says it is from `marvel`.
`authority: none` limits the harm: the message asks for a decision and
grants nothing, and the action it names (`marvel shift`) is checked at the
daemon against the caller's scope, not against the message.

### H3. The reply

marvel does not read a reply. The supervisor's decision is an action, and
marvel observes the action:

- **Shift:** the supervisor (or the operator) runs `marvel shift <ws/team>
  --role <role>`. The shift replaces the seat, the request is dropped, and
  delivery stops.
- **Leave it running:** the supervisor does nothing, and the repeats
  continue once per window until the request ends some other way, or until
  the operator uses one of the controls in H6.

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

### H6. Operator controls: mute, cancel, abort

The operator ruled on 2026-10-02: "let's add a mute button option, a cancel
autoshift change for that agent session or a general abort option". Each
control is an explicit operator act through one CLI verb, `marvel autoshift`.
None is ever set by a seat or by automation, and each one is visible where
the operator already looks, so none can become the #453 silent hold by
accident.

**Who may run them.** The verbs require the admin scope at the daemon
(`internal/daemon/scope.go`), local socket or an admin key over mrvl. Each
requires `--reason`, and each records the actor (the caller's key
fingerprint, or `local socket`), the time and the reason. A seat runs as the
same OS user and could reach the local socket, so the boundary is the rule
and the record, not the kernel: the verbs are not in any seat's allowed
command set, and every use is printed in `marvel get sessions` and the
event ring.

| control | verb | scope | effect | persists across a restart | undone by | expires |
|---|---|---|---|---|---|---|
| (a) mute | `marvel autoshift mute <session> --reason R [--for D]` | one escalated request | supervisor delivery and the repeating event pause | yes, on the persisted request | `marvel autoshift unmute <session>`, the expiry, or the request ending | yes: default 4h, `--for` up to 24h, no "forever" |
| (b) cancel | `marvel autoshift cancel <session> --reason R` | one session | that session is never auto-shifted, by either trigger, for its life; any pending request for it is dropped | yes, on the session record | `marvel autoshift resume <session>` | no; it ends with the session, and a successor starts with auto-shift on |
| (c) abort | `marvel autoshift abort <ws/team> --reason R`, or `abort --all` | a team, or the whole daemon | no new auto-shift is initiated for that scope; pending requests in the scope are dropped | yes: team-scope on the team record, daemon-scope in the store's meta bucket | `marvel autoshift resume <ws/team>` or `resume --all` | no |

**Why both scopes for abort.** Team scope is for one team behaving badly
(a manifest error, a role that shifts in a loop). Daemon scope is the
incident switch: a bad release, or correlated shifts across teams spending
the shared rate limit at once. Abort persists across a restart because an
abort that a restart quietly lifts would resume shifting in the middle of the
incident it was called for. Abort stops initiation only: a shift already in
progress finishes or rolls back on its own timeout, and the operator's manual
`marvel shift` still works under an abort.

**What the supervisor delivery says.**
- On mute: one message, `muted by <actor> until <time>: <reason>`, then no
  more deliveries for that request until the mute ends. On expiry: one
  message, `mute expired`, and the normal cadence resumes. The ring event is
  not silent while muted: it repeats once per window marked `muted until
  <time>`, so the ring never shows a gap with no reason.
- On cancel: one message, `auto-shift canceled for <session> by <actor>:
  <reason>`, and the request ends.
- On abort: one message per dropped request in the scope, `auto-shift
  aborted for <scope> by <actor>: <reason>`, and one per team when it is
  resumed.

**How `marvel get sessions` shows it.**
- A per-session suffix on the HEALTH cell, beside `(stalled)`: `(muted 3h12m)`
  or `(autoshift off)`.
- A banner line above the table while any abort stands: `auto-shift aborted:
  <scope> by <actor> at <time>: <reason>`, one line per abort.
- `marvel describe session` and `describe team` print the actor, time and
  reason.

## 4. Builder changes

- `internal/team/shift_handoff.go`: a `deliverHandoffMissing` beside
  `emitHandoffMissing`, called at escalation and from
  `repeatHandoffMissing`; the delivered counter in `api.ShiftRequest`.
- A small publisher on the daemon's existing admin connection, behind an
  interface the controller holds (nil when no managed bus, which means
  event-only, as today). The interface is narrow because the admin
  principal can publish to `>`: one method,
  `PublishRoleInbox(ctx, ws, team, role string, env envelope.Envelope, msgID string) error`,
  which builds the subject itself from validated tokens and can publish
  nowhere else. It is the controller's only bus capability.
- `config.ReservedBusUsers` (or the agent-id equivalent) reserves `marvel`.
- The `marvel autoshift` verb (mute, unmute, cancel, resume, abort) with
  admin scope and a required `--reason`; fields on `api.ShiftRequest`
  (mute), `api.Session` (cancel), `api.Team` and the meta bucket (abort);
  the HEALTH suffixes and the abort banner.
- Events `team.shift-handoff-undelivered`, `autoshift.muted`,
  `autoshift.unmuted`, `autoshift.canceled`, `autoshift.aborted`,
  `autoshift.resumed`; the existing event gains a
  `delivered:` field (`sent`, `queued, no live holder`, `no recipient`,
  `undelivered: <error>`).

## 5. Tests (red first)

1. A team with a running supervisor whose seat was started with
   `DIRECTOR_ROLE`: at escalation, the envelope is RECEIVED through a
   role-filtered durable of the same shape the shim creates (not only
   published), and it is valid against the canonical schema with the
   fields in H2, `expires_at` included.
2. A repeat after one window carries the same `correlation_id` and a new
   `message_id`; a shift ends the request and no further envelope follows.
3. A team without a `supervisor` role: no publish, one log line, events
   continue; an inject spy on `Notify` records zero calls for any pane.
4. A supervisor role with no live seat: the envelope is stored and a seat
   started later reads it.
5. Restart: escalate, send, restart the daemon before the next boundary; no
   second envelope until the boundary. Kill between publish and store write,
   through a fault seam between the two: the re-send inside the stream's
   2-minute dedupe window is dropped; one after it is received once more with
   the same `correlation_id`.
6. Bus down: the reconcile tick returns within the publish timeout (2s),
   `team.shift-handoff-undelivered` fires once, the event repeats; after bus
   ready a send follows within 35s (one 30s health tick plus slack), not a
   full window.
7. A manifest declaring an agent id `marvel` is refused.
8. **Mute.** Mute an escalated request for 1h: one muted message, no further
   delivery, ring events marked muted, `(muted 59m)` shown; restart the
   daemon and the mute holds; at expiry one `mute expired` message and the
   cadence resumes; `unmute` resumes it at once; mute without `--reason`, or
   with `--for 25h`, is refused; mute from a non-admin key is refused.
9. **Cancel.** Cancel a session past max age with a pending request: the
   request is dropped with one message, no auto-shift by either trigger
   fires for it, `(autoshift off)` shown, and it survives a restart; after a
   manual shift the successor is auto-shifted normally; `resume` restores it.
10. **Abort.** `abort <ws/team>`: no new auto-shift in that team, others
    unaffected, pending requests dropped with one message each, the banner
    shown, and it survives a restart; `abort --all` does the same for every
    team; a shift in progress is not interrupted; manual `marvel shift`
    still works; `resume` lifts each scope separately.
11. **Supervisor escalated.** The escalated session's role is `supervisor`:
    no publish (with one and with two supervisor replicas), the event says
    why, and the inject spy records zero calls.
12. **Unaddressable team.** A fixture team `Team_A`: `marvel work` warns
    once, escalation publishes nothing, and the event says `team name not
    addressable`.
13. **Precondition.** With delivery switched off (a build without SB-4),
    escalation publishes nothing and the event says `delivered: off`.

## 6. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | RULED 2026-10-02 yes: the recipient is the role named `supervisor` in the same team (a convention, not a declared field) | yes |
| 2 | RULED 2026-10-02: add a mute, a per-session cancel and a general abort (H6). Open within it: the mute's default and cap | 4h default, 24h cap |
| 3 | RULED 2026-10-02 yes: marvel publishes as `marvel` on its existing admin connection, not a new scoped principal (a scoped principal held by the same daemon adds no boundary) | yes |
| 4 | When the escalated seat is the supervisor, deliver nowhere (operator decides from the event), or later escalate to director once marvel can address it | nowhere in this slice |
