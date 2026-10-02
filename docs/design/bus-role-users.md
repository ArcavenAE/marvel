# Broker users per role: closing the global read leak without cutting a live seat

Status: design for review, 2026-10-02. Owner: the architect role. Tracks
aae-orc-6vy9x (finding-179). Design only; the builder works it after review
and the rulings in section 8.

## 1. Why

R-94 says a worker never holds a global address; as sharpened, a session
must not RECEIVE mail addressed to a role it does not hold. It does not hold
today: marvel renders one broker user per team, every seat in the team
connects as it, and in a team that has a supervisor that user can subscribe
to the cluster's whole global tier. finding-179 intercepted a supervisor's
global mail with a worker's credential and a plain NATS client. Narrowing the
team user's grants cannot fix this, because the worker and the supervisor
present the same credential. The grant unit has to become the role.

The hard constraint is that running seats keep working: no step may cut a
live seat's bus access by surprise.

## 2. Today (checked on marvel main)

- `internal/bus/declared.go` renders one `ScopeBinding` user per applied
  team, named by the team, with `agent.<ws>.<team>.>` both ways. A team with
  a role named `supervisor` (`hasSupervisorRole`, `manager.go`) and a hub
  gains publish `global.director.inbox`, `global.*.supervisor.inbox` and
  `$JS.global.API.>`, and subscribe `global.<cluster>.>` (#285, 2026-09-16).
  So 6vy9x's fix item 2 already shipped: a team with no supervisor role holds
  no global grant (the builder's note of 2026-09-25, and the operator's
  ruling of 2026-09-30).
- Every seat's password comes from `TeamCredential(team)` (`manager.go`), and
  the spawn path puts it in the seat's env as `DIRECTOR_NATS_USER` and
  `DIRECTOR_NATS_PASS` (`internal/runtime/adapter.go`).
- Passwords are recovered from the rendered `authorization.conf` across a
  restart, so a running session's credential stays valid (`NewManager`).
  `Regenerate` re-renders and reloads the broker (SIGHUP); a removed team's
  user disappears, which is revocation.
- The director seat user (`director`) carries no global grant, by omission.
- User names are tokens in `[A-Za-z0-9_-]` (`validToken`, `render.go`).

**Not fixed by narrowing alone, and why.** The `$JS.global.API.>` grant lets
its holder create a consumer on `GLOBAL_TO_<cluster>` with any filter and
read it through `_INBOX.>`. A role user that keeps that grant can still read
every global subject on the cluster, however narrow its core subscribe. So
the narrowing in section 4 covers the JetStream API as well as the core
subscribe.

## 3. The shape

A broker user per (team, role), named `<team>.<role>`. The dot is outside the
token class, so a role user can never collide with a team user or with
another team's role user. Each gets its own minted password, recovered
across a restart the same way team passwords are.

| user | publish | subscribe |
|---|---|---|
| `<team>.<role>`, any role | `agent.<ws>.<team>.>`, `agent.audit`, plumbing | `agent.<ws>.<team>.>`, `agent.<ws>.broadcast`, plumbing |
| plus, for `<team>.supervisor` on a cluster with a hub | `global.director.inbox`, `global.*.supervisor.inbox`, the narrowed JetStream API below | `global.<cluster>.supervisor.inbox` only |
| plus, for a worker role in a supervisor team | none global (ruling 2) | none global |

**Narrowed JetStream API for the supervisor role user.** In place of
`$JS.global.API.>`, only what a supervisor's shim does across its whole
global lifecycle (director `global.go`):
- **Its inbox consumer:** stream info on `GLOBAL_TO_<cluster>`; consumer
  create and info scoped to that stream and to the supervisor inbox filter
  (`$JS.global.API.CONSUMER.CREATE.<stream>.<consumer>.<filter>`; VERIFIED
  on a scratch hub plus leaf in review 5396916719: with this grant a
  supervisor can create only its own inbox-filtered consumer); message next
  and ack.
- **Global presence** (`attachGlobal`, `writeGlobalPresence`, deregister,
  the roster scan): bind the `GLOBAL_PRESENCE` bucket (stream info on
  `KV_GLOBAL_PRESENCE`), put and delete on its OWN keys only (publish on
  `$JS.global.API.$KV.GLOBAL_PRESENCE.presence.<cluster>.supervisor.*`: a
  local user reaches a hub bucket through the domain prefix, nats.go
  `kv.go` `useJSPfx`, so the unprefixed `$KV.GLOBAL_PRESENCE.>` denies the
  supervisor's own put), list keys (a consumer create, info and delete
  on `KV_GLOBAL_PRESENCE`), and get (direct get and message get). This is
  the same set the hub's leaf users already allow.
- **Acked publish** on `global.director.inbox` and
  `global.*.supervisor.inbox`, with its reply on `_INBOX.>`.
The exact subject list is the builder's, taken from the shim's calls, and
test 3 runs the whole lifecycle, not a subset.

**The pull residual, and the invariant that closes it.** Pulling from the
inbox consumer needs `$JS.global.API.CONSUMER.MSG.NEXT.GLOBAL_TO_<cluster>.*`,
because a shim's durable name carries its random instance id. That grant
lets a supervisor pull from ANY consumer on the stream it can name, and the
review VERIFIED a supervisor pulling another role's message from a broad
consumer someone else had created. So the design rests on an invariant:
**on `GLOBAL_TO_<cluster>`, every consumer is filtered to a single inbox
subject, except consumers created by admin or hub credentials, whose names
are random and never logged.** Enforcement:
- No role user can create a broad consumer (the create grant above carries
  the filter).
- S3 adds a check, `marvel bus users --global-consumers`, and the same
  check on the 30s health tick where the leaf can list consumers on the hub
  (otherwise it is the hub operator's command, given in the doc): any
  consumer whose filter is broader than one inbox subject, or which has no
  filter, raises `bus.global-broad-consumer` with its filter, creator where
  known, and a short hash of its name, never the name itself, so the event
  cannot leak what makes such a consumer safe. It is a warning, not a gate.
- director#191's `unread --global` and any audit reader create no consumer
  at all: they read with admin or hub credentials through
  `MSG.GET` or `DIRECT.GET` by sequence, so no broad consumer exists for
  anyone to pull from.

**Three reviewer questions, answered where known.**
- *Can one supervisor's KV put or delete touch another's presence key?*
  With a broad grant, yes: measured on a scratch hub plus leaf (review
  5397040779), a broad prefixed grant overwrote another cluster's key and
  deleted the director's. The unprefixed `$KV.GLOBAL_PRESENCE.>` is worse
  in the other direction: it denies the supervisor's own put, so every
  supervisor would read as dead after S2. The shim's key is
  `presence.<cluster>.<role>.<instance>` (director `global.go`
  `presenceKey`), and a local user reaches the hub bucket through the
  domain prefix, so the grant is
  `$JS.global.API.$KV.GLOBAL_PRESENCE.presence.<cluster>.supervisor.*`,
  measured in the same review: its own put works and every other key is
  denied. That keeps a supervisor away from the director's and other
  clusters' rows. It does
  not separate two supervisors on the same cluster, who share the role
  word; that is the replica limit already stated, closed only by
  per-session users (R-84). Test 3, run through a leaf, asserts the own
  put and delete succeed and a put or delete on another cluster's key or
  the director's key is refused.
- *Does 60s exceed the shim's reconnect backoff?* Yes. The shim connects
  with nats.go defaults (director `bus.go` sets no reconnect options), and
  director's pinned nats.go v1.53.1 (the same in v1.54.0) sets `DefaultReconnectWait` 2s and `DefaultMaxReconnect`
  60, so a reconnecting shim retries about every 2s and is visible again
  within seconds. After 60 failed attempts (about two minutes) it closes
  for good and does not come back, so it cannot slip past the second check
  either.
- *Can a role user enumerate the random-name consumers?* Not with the
  grant above: it carries no `CONSUMER.LIST` or `CONSUMER.NAMES`. The
  builder keeps both out of the list, and test 10 asserts each is
  refused for a role user. The cost: the shim's roster floor lookups
  (`seatFloor`) then run without NAMES and can take up to
  `floorListTimeout`, 5s, per attach. That is a slower start, not a
  failure, and test 3 records the attach time.

**Honest limit, restated.** Within a team, every role still shares
`agent.<ws>.<team>.>`, so a worker can read a teammate's local role inbox.
R-94 governs the global tier only; per-role local scoping is out of scope
here. And a role user is still shared by that role's replicas: a broker user
per session (R-84) is the complete form.

## 4. The rollout: additive first, subtractive only when nothing uses it

| stage | change | does it cut a live seat? |
|---|---|---|
| S1 | Render role users ALONGSIDE the team users, and reload. Team users are unchanged. | No. The reload adds users; a live connection whose user and grants are unchanged stays connected (test 1 proves it on the broker version in use). |
| S2 | New spawns get the role credential: `RoleCredential(team, role)` replaces `TeamCredential` at spawn, and the session record stores which bus user it was given. | No. Running seats keep the team credential they were started with; a seat moves to its role user when it next respawns (a shift, a restart, a scale). |
| S3 | Observe. `marvel describe session` shows the bus user; `marvel bus users` lists, per team user, the live sessions marvel spawned with it and the broker's own count of connections as that user (`/connz` with auth). An event fires when a team user's count reaches zero. | No. Read only. |
| S4 | Retire a team user's GLOBAL grants (only on a supervisor team). | **Yes, for any connection still on the team user that uses the global tier.** Staged around below. |
| S5 | Retire the team user itself. | **Yes, for any connection still on the team user.** Staged around below. |

**How S4 and S5 are staged around.** Both are an operator verb, `marvel bus
retire-team-user <ws/team> [--global-only]`, never automatic. The verb
refuses, and names each one, while any of these holds:
- marvel's records show a live session of the team spawned with the team
  user, OR with NO recorded bus user (every session spawned before S2 has
  none, and it holds the team password);
- the broker reports any connection as the team user (`/connz` with auth);
- the second of two such checks, taken one reconnect interval apart (60s
  by default, above the shim's maximum reconnect backoff), is not also
  zero. A seat in reconnect backoff is absent from `/connz` for that time,
  so one zero reading proves nothing.
After it retires the user and reloads, the verb watches the broker's closed
connections (`/connz?state=closed`) for authentication failures as the
retired user for five minutes, and reports each one with its address, so a
client the checks missed is named rather than silently cut.
The operator clears them by shifting the team (`marvel shift`), which
respawns every seat onto its role user, then runs the verb. A heads-up goes
to director before any S4 or S5 run on a live cluster, and a fleet-wide run
(every team at once) is never offered as one command.

Clients marvel did not spawn (a hand-run `nats` client, a wrapper that reads
the team password) are why the verb reads the broker's connection count
rather than marvel's records alone.

## 5. The director seat user

Rendered deliberately: the per-cluster seat user carries no global grant,
since the fleet has one director and it is not per-cluster, and the rendered
file says so in a comment rather than by silence (6vy9x fix item 3, its
named default). Ruling 3.

## 6. Custody check (SOUL section 3, ADR-009)

- [ ] Every artifact this adds is issuance: a broker password marvel mints for
  its own broker, which it can revoke (by re-rendering) and re-mint without a
  human at a third party's console. Its audience is this cluster's broker
  only. Nothing here holds bearer authority at a third party.
- [ ] The leaf seed and the hub credential are untouched; they stay where the
  leaf design put them.
- [ ] Known and unchanged: the password reaches a seat as an env value
  (`DIRECTOR_NATS_PASS`). Moving it to a 0600 file path is a separate change.
  Under the same OS user a file is no stronger than env without a sandbox
  (aae-orc-ww33y), so it is noted, not bundled.

## 7. Tests (red first)

1. **S1 is additive.** With a live connection as a team user holding a
   subscription, render the role users and reload: the connection stays up,
   the subscription still receives, and no message is lost.
2. **The leak is closed for new seats.** As a worker role user in a
   supervisor team, with no shim in the path: core subscribe to
   `global.<cluster>.supervisor.inbox` and to `global.<cluster>.>` is
   refused, and creating a JetStream consumer on `GLOBAL_TO_<cluster>` is
   refused. The finding-179 interception cannot be reproduced.
3. **The supervisor still works, end to end.** As `<team>.supervisor`, the
   shim's full global lifecycle succeeds against a scratch hub plus leaf:
   attach (stream info, presence bucket bind, its inbox consumer), a
   presence put on each beat, a roster scan (list keys, get each), an acked
   publish to `global.director.inbox` and to another cluster's supervisor
   inbox, receive and ack from its inbox, and deregister (presence delete).
   A consumer with a filter other than its inbox is refused.
4. **Spawn uses the role user.** A seat spawned after S2 carries
   `DIRECTOR_NATS_USER=<team>.<role>`, and its record names it.
5. **A running seat is untouched by S2.** A seat started before S2 keeps its
   team credential and stays connected; after `marvel shift` it is on its
   role user.
6. **The retire verb refuses.** It refuses and names: one live session on
   the team user; one live session with no recorded bus user (a pre-S2
   record); a hand-run client connected as the team user; a client that
   disconnects just before the first check and reconnects inside the
   interval (the second check catches it). After a shift and the client
   gone, `--global-only` removes only the global grants and a second run
   removes the user; a client that then reconnects with the old password
   is reported as an authentication failure within five minutes.
7. **Restart.** Role passwords survive a daemon restart and reexec; a seat
   connected as a role user stays connected.
8. **Names.** A role user name never collides with a team user name, and a
   team with no supervisor role renders role users with no global grant
   (the negative test #403 carries).
9. **The seat user.** The rendered file states the seat user's global
   grants, or their absence, in a comment.
10. **The pull residual.** A broad consumer created on `GLOBAL_TO_<cluster>`
    with a guessable name raises `bus.global-broad-consumer`; a consumer
    created by the supervisor role user with a broad filter is refused; the
    S3 check lists only inbox-filtered consumers on a clean stream.

## 8. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | Broker users per (team, role), named `<team>.<role>`, as the step before per-session users (R-84) | yes |
| 2 | A worker role in a supervisor team publishes nothing on the global tier (the 2026-09-30 ruling extended from teams to roles), or keeps publish on `global.director.inbox` (sharpened R-94's upward send). It breaks no current shim path, but once S5 retires the team user it DOES cut: a hand-run client using the team password, and director#191's `unread --global` run by anyone without admin or hub credentials | nothing; an upward send goes through the supervisor |
| 3 | The per-cluster director seat user carries no global grant, stated in the rendered file | yes |
| 4 | S4 and S5 run per team, by the operator's verb, after a heads-up to director; never fleet-wide in one command | yes |
