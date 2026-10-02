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
`$JS.global.API.>`, only what a supervisor's shim does: stream info on
`GLOBAL_TO_<cluster>`, consumer create and info scoped to that stream and to
the supervisor inbox filter (nats-server 2.10 and later can carry the filter
in the create subject, `$JS.global.API.CONSUMER.CREATE.<stream>.<consumer>.<filter>`;
INFERRED from the server's API, and test 3 checks it on the version in use),
message next and ack. The exact list is the builder's, against the shim's
calls, and test 3 proves it from both sides.

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
refuses while marvel's records show a live session spawned with that team
user, or while the broker reports any connection as it, and names each one.
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
3. **The supervisor still works.** As `<team>.supervisor`, the shim's global
   start succeeds (stream info, its consumer, receive and ack). A consumer
   with a filter other than its inbox is refused.
4. **Spawn uses the role user.** A seat spawned after S2 carries
   `DIRECTOR_NATS_USER=<team>.<role>`, and its record names it.
5. **A running seat is untouched by S2.** A seat started before S2 keeps its
   team credential and stays connected; after `marvel shift` it is on its
   role user.
6. **The retire verb refuses.** With one live session on the team user it
   refuses and names the session; with a hand-run client connected as the
   team user it refuses and names the broker count; after a shift and the
   client gone, `--global-only` removes only the global grants, and a second
   run removes the user.
7. **Restart.** Role passwords survive a daemon restart and reexec; a seat
   connected as a role user stays connected.
8. **Names.** A role user name never collides with a team user name, and a
   team with no supervisor role renders role users with no global grant
   (the negative test #403 carries).
9. **The seat user.** The rendered file states the seat user's global
   grants, or their absence, in a comment.

## 8. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | Broker users per (team, role), named `<team>.<role>`, as the step before per-session users (R-84) | yes |
| 2 | A worker role in a supervisor team publishes nothing on the global tier (the 2026-09-30 ruling extended from teams to roles), or keeps publish on `global.director.inbox` (sharpened R-94's upward send) | nothing; an upward send goes through the supervisor |
| 3 | The per-cluster director seat user carries no global grant, stated in the rendered file | yes |
| 4 | S4 and S5 run per team, by the operator's verb, after a heads-up to director; never fleet-wide in one command | yes |
