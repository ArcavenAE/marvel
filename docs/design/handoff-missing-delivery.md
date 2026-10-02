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
- A `supervisor` role with no running seat: publish ONCE, and hold. The
  message waits in the stream, and the next holder's new durable reads it
  (`DeliverAllPolicy`). While no holder is live, marvel does not repeat the
  delivery: AGENT_INBOX keeps 24h, the shim does not honor `expires_at`
  (director `envelope.go`), and a late holder would otherwise read up to one
  envelope per window for a day. marvel knows a holder is live from its own
  session records; when one appears, the normal cadence resumes with an
  immediate send. The event says `delivered: queued once, no live holder`.
- **The escalated seat is a supervisor** (ruled 2026-10-02: "Deliver to
  other supervisors and director"). marvel never uses the role address here,
  since it would reach the escalated seat's own durable. Instead it sends the
  same envelope to:
  1. every other live replica of the same team's `supervisor` role, each by
     `agent://<team>/<session>` (its own inbox, which the shim always reads,
     so P1 does not gate this leg);
  2. the `supervisor` role of every other team in the SAME WORKSPACE on this
     cluster that has one,
     by `role://<team>/supervisor` (gated by P1, as in the normal case);
  3. the director, by `global://director`, published on
     `global.director.inbox` (gated by P2 below).
  Supervisors on other clusters are not included; the director is the
  cross-cluster recipient. The event lists the legs and what each got
  (`sent`, `queued`, `off`, `undelivered`). If every leg is unavailable, the
  event says `delivered: none, no other supervisor and no global tier`, and
  the operator decides from the event and `get sessions`.

**Precondition P2, the director leg.** In this order:
1. marvel#457 lands: the canonical schema accepts `global://` recipients.
   Until then a `global://director` envelope fails the schema, so the leg
   stays off.
2. Then this leg ships. It is off on a cluster with no hub.

Where the global send comes from, checked against the hub conf and the
role-users design (`docs/design/bus-role-users.md`, marvel#461):
- marvel publishes on its own broker with its existing admin connection
  (ruling 3). It holds no global address and gains none: the subject crosses
  to the hub over the cluster's existing leaf connection. The hub's
  per-cluster leaf user already allows publish on `global.director.inbox`
  and `global.*.supervisor.inbox` (checked on the running hub by permission
  lines only), so no hub grant changes. That the hub's director stream
  captures a core publish arriving over the leaf is INFERRED, and test 14
  proves it.
- marvel#461 narrows the team and role users and leaves the admin principal
  unchanged, so it does not affect this path. The admin principal is never
  handed to a session (`declared.go`), so no seat gains the global send.
- SOUL section 3 and ADR-009: no new credential. The admin password is
  issuance on marvel's own broker; the leaf seed stays where the leaf design
  put it, and marvel does not read it for this.
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
(`internal/daemon/scope.go`): an admin key over mrvl, or the local socket,
which is admin scope for any process of the daemon's OS user. Seats run as
that user, so a seat can reach the socket. Three layers keep a seat from
setting a control on itself, and the record shows which one held:

1. **A rendered deny rule.** marvel adds `Bash(marvel autoshift:*)` to the
   `permissions.deny` list of every claude seat's projected settings,
   writing a projection file even for a role with no policy. It stops a
   claude seat's Bash tool from running the verb by its plain name. It does
   NOT stop a shell indirection (`sh -c`, a script, an alias), another
   harness (codex, crush and opencode have no such rule), or a process
   writing to the socket directly. It is a harness rule, not a kernel
   boundary.
2. **Peer attribution on the local socket.** The daemon reads the caller's
   pid from the socket (`LOCAL_PEERPID` on macOS, `SO_PEERCRED` on Linux)
   and that process's controlling terminal. If the terminal is a marvel
   pane's pty, the caller is that seat: the verb is refused and the refusal
   names the session. If the caller has no controlling terminal (a
   `setsid` or double-forked process, cron, launchd), the verb is refused
   with "run it from a terminal, or over mrvl with an admin key", because
   such a caller cannot be told apart from an escaped seat. Otherwise the
   caller is recorded as `operator, tty <name>, pid <n>`.
3. **The record.** Every use prints in `get sessions`, in `describe`, and in
   the event ring, with the actor as attributed above, or the key
   fingerprint over mrvl.

Limit, stated plainly: under one OS user none of this is a kernel boundary.
A seat that drives a terminal it does not own (for example by typing into
the operator's own tmux server) is attributed to that terminal. Closing that
needs a separate OS user per seat or a sandbox profile (aae-orc-ww33y); the
attribution makes such a use visible, not impossible.

| control | verb | scope | effect | persists across a restart | undone by | expires |
|---|---|---|---|---|---|---|
| (a) mute | `marvel autoshift mute <session> --reason R [--for D \| --forever]` | one escalated request | supervisor delivery and the repeating event pause | yes, on the persisted request | `marvel autoshift unmute <session>`, the expiry, or the request ending | timed: default 4h, `--for` up to 24h. `--forever` (ruled 2026-10-02, "allow forever mute"): no expiry, but it still ends with the request, and a daily reminder keeps it visible |
| (b) cancel | `marvel autoshift cancel <session> --reason R` | one session | that session is never auto-shifted, by either trigger, for its life; any pending request for it is dropped | yes, on the session record | `marvel autoshift resume <session>` | no; it ends with the session, and a successor starts with auto-shift on |
| (c) abort | `marvel autoshift abort <ws/team> --reason R`, or `abort --all` | a team, or the whole daemon | no new auto-shift is initiated for that scope; pending requests in the scope are dropped | yes: team-scope on the team record, daemon-scope in the store's meta bucket | `marvel autoshift resume <ws/team>` or `resume --all` | no |

**Why both scopes for abort.** Team scope is for one team behaving badly
(a manifest error, a role that shifts in a loop). Daemon scope is the
incident switch: a bad release, or correlated shifts across teams spending
the shared rate limit at once. Abort persists across a restart because an
abort that a restart quietly lifts would resume shifting in the middle of the
incident it was called for. Abort stops initiation only: a shift already in
progress finishes or rolls back on its own timeout, and the operator's manual
`marvel shift` still works under an abort. So the ring has no gap while an
abort stands, marvel emits `autoshift.aborted-standing` once an hour per
aborted scope, listing the seats it would have asked for a handoff, so a
seat running far past its max age stays visible.

**What the supervisor delivery says.**
- On mute: one message, `muted by <actor> until <time>: <reason>`, then no
  more deliveries for that request until the mute ends. On expiry: one
  message, `mute expired`, and the normal cadence resumes. The ring event is
  not silent while muted: it repeats once per window marked `muted until
  <time>`, so the ring never shows a gap with no reason.
- On a forever mute: one message, `muted forever by <actor>: <reason>`, then
  one line every 24h to the same recipients, `still muted forever since
  <time> by <actor>: <reason>; unmute with marvel autoshift unmute
  <session>`, and the ring event marked `muted forever`. A forever mute is
  never silent for more than a day.
- On cancel: one message, `auto-shift canceled for <session> by <actor>:
  <reason>`, and the request ends.
- On abort: one message per dropped request in the scope, `auto-shift
  aborted for <scope> by <actor>: <reason>`, and one per team when it is
  resumed.

**How `marvel get sessions` shows it.**
- A per-session suffix on the HEALTH cell, beside `(stalled)`: `(muted 3h12m)`,
  `(muted forever)` or `(autoshift off)`. A forever mute is always shown;
  no flag hides it.
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
4. A supervisor role with no live seat, across six windows: exactly one
   envelope is published. A seat started later reads that one, then
   receives an immediate send and the normal cadence after it.
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
8. **Who can set a control.** A projected claude seat's settings carry the
   `Bash(marvel autoshift:*)` deny rule, including for a role with no
   policy. A call on the local socket from a process whose controlling
   terminal is a marvel pane is refused and names that session; a call from
   a process with no controlling terminal is refused; a call from another
   terminal is accepted and recorded with its tty and pid; a call over mrvl
   with a non-admin key is refused. **Mute.** `--forever`: one `muted forever` message; after a simulated 24h,
   exactly one reminder line (and one per further 24h); `(muted forever)`
   shown in `get sessions` and in `describe` with actor and reason; it
   survives a restart; it ends when the request ends; `--forever` without
   `--reason` is refused, as is `--forever` together with `--for`. Timed:
   mute an escalated request for 1h: one muted message, no further
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
    shown, `autoshift.aborted-standing` once per hour naming the over-age
    seats, and it survives a restart; `abort --all` does the same for every
    team; a shift in progress is not interrupted; manual `marvel shift`
    still works; `resume` lifts each scope separately.
11. **Supervisor escalated.** The escalated session's role is `supervisor`,
    on a cluster with two other teams that have supervisors and a hub. Nothing
    reaches the escalated seat's durable. The other replica of its own role
    (with two replicas) receives by `agent://`. Each other team's supervisor
    receives by `role://`. `global.director.inbox` receives one envelope that
    validates against the schema after #457. The inject spy records zero
    calls. With no other supervisor and no hub, nothing is published and the
    event says so.
12. **Unaddressable team.** A fixture team `Team_A`: `marvel work` warns
    once, escalation publishes nothing, and the event says `team name not
    addressable`.
13. **Precondition.** With delivery switched off (a build without SB-4),
    escalation publishes nothing and the event says `delivered: off`.
14. **The director leg crosses the leaf.** On a test hub that mirrors the
    running hub's ACCOUNT layout (which account the leaf user binds to, and
    which account the director stream lives in) as well as the leaf user's
    permissions, an envelope marvel publishes
    on the leaf's `global.director.inbox` with its admin connection is
    stored in the hub's director stream. With #457 absent, the leg reports
    `off` and publishes nothing.

## 6. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | RULED 2026-10-02 yes: the recipient is the role named `supervisor` in the same team (a convention, not a declared field) | yes |
| 2 | RULED 2026-10-02: add a mute, a per-session cancel and a general abort (H6); "allow forever mute". The timed mute keeps a 4h default and a 24h cap; a forever mute sends a daily reminder | as written |
| 3 | RULED 2026-10-02 yes: marvel publishes as `marvel` on its existing admin connection, not a new scoped principal (a scoped principal held by the same daemon adds no boundary) | yes |
| 4 | RULED 2026-10-02: "Deliver to other supervisors and director". Other replicas of the same role, every other team's supervisor on this cluster, and `global://director` after #457 (H1, P2) | as written |
