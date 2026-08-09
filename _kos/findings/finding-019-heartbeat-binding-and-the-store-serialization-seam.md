# finding-019: a secret on api.Session is published by the same encoder that persists it

**Date:** 2026-08-09
**Ticket:** `aae-orc-mr5c` (heartbeat RPC unauthenticated and unbound to
its claimed session)
**Question:** bears on `question-agent-identity-authority` (M1 identity
lane) without entering it.
**Status:** SHIPPED for the narrow fix, with two limits stated below that
the narrow fix does not reach.

## The result in one line

Binding the heartbeat needed a secret on the session record, and the
session record is serialized by `encoding/json` twice: once into bbolt,
once into every `get sessions` response. One tag decides which of those
two an added field goes to, and the wrong answer would have published the
secret to exactly the sibling agents it separates.

## The defect, confirmed on `main` before the fix

`handleHeartbeat` unmarshalled `session_key` and `context_percent` off the
wire and called `UpdateSessionHeartbeat` with them. Nothing tied the
request to the session it named. Any process that could reach the daemon
socket could stamp any session's `LastHeartbeat` and write its
`ContextPercent`.

The second half is what makes this more than a liveness bug. The
heartbeat RPC is one of the CTX% column's two producers, so an unbound
key writes a number an operator reads and a shift trigger may act on.
`LastHeartbeat` alone already feeds the heartbeat healthcheck, the
restart policy, and `allReady` shift readiness.

The realistic attacker is not a stranger on the host. It is a marvel
agent: every session gets `MARVEL_SOCKET` by design, sibling session keys
are `<workspace>/<team>-<role>-g<gen>-<index>` and readable from
`marvel get sessions`, and an agent can be talked into things by whatever
it reads.

## The fix

`session.Manager.Create` mints a 256-bit token before the record exists.
The digest goes on the record; the plaintext rides the caller's pointer
into `planLaunch`, where the adapter puts it in the pane environment as
`MARVEL_HEARTBEAT_TOKEN`. `Store.UpdateSessionHeartbeat` takes the token
as a parameter and checks it under the same lock as the write, so the
check is in the write's signature rather than in a caller's discipline.
A mismatch returns `ErrHeartbeatUnauthorized` and the daemon emits
`heartbeat.refused` on the ring and in the log.

Environment, not a flag: an argv is readable from the process table by
every agent on the host.

## The durable part: the serialization seam

`api.Session` records reach bbolt through `persistPut` → `json.Marshal`,
and reach every RPC client through `handleGet` → `ListSessions` →
`json.Marshal`. Same encoder, same struct tags, two destinations with
opposite requirements for a secret.

So the field split is not stylistic:

- `HeartbeatToken string \`json:"-"\`` is plaintext: this process's memory
  and the agent's environment, nowhere else.
- `HeartbeatTokenHash string` is the digest, persisted and published, which is
  safe and is what lets an adopted session keep beating across a daemon
  restart. The agent still holds the plaintext; the rehydrated record
  still holds the digest to check it against.

Anyone adding a credential, a nonce, or a capability to a store resource
inherits this seam. `aae-orc-wbqi` (projected policy file must be
tamper-proof) is in the same family and should read this before choosing
where its integrity material lives.

There is a test for it (`TestHeartbeatTokenNeverSerializes`) because a
future field addition, a struct embed, or a switch to a different encoder
would all break the property silently.

## The one deliberate fail-open, and its drain condition

A session record with an empty hash is admitted, and the daemon emits
`heartbeat.unbound` each time it happens.

Only records written by a binary that minted no token are in that state.
Refusing them would have made the upgrade destructive: adopt-on-restart
brings live agents forward, their heartbeats would be refused, they would
go stale on the healthcheck, and the restart policy would kill and
respawn a working fleet. Every session spawned from here on carries a
hash, so the exemption cannot widen, and it ends when those sessions end.

The event is the price of the exemption. An accepted gap nobody can see
is not an accepted gap.

## Two limits this does not reach

**Same-uid environment reads.** Marvel's agents run as one user, so an
agent that goes looking can read a sibling pane's environment and take
its token. The binding defeats forgery from a guessed key, which is the
confused-deputy case and the one an injected agent falls into; it does
not defeat an agent that deliberately hunts. Closing that is enforcement
locus 3 and curtain, not this ticket.

**Ring pressure.** A process spamming refused heartbeats fills the
bounded event ring and evicts other events. The refusals are visible,
which is the property that was missing; rate-limiting them is not built.

Both are scope boundaries rather than oversights: the M1 principal model
is deliberately on the identity lane, and only the minimal per-session
spawn token was ruled into the main line.

## Falsification

Removing the comparison in `authenticateHeartbeat` and leaving everything
else in place fails six subtests across `internal/api` and
`internal/daemon`: peer-token, no-token, and hash-as-token at both the
store and the RPC layer, plus the assertion that a refused heartbeat
leaves `LastHeartbeat` and `ContextPercent` where the last admitted one
left them. Restoring it passes all of them and the rest of the suite.
