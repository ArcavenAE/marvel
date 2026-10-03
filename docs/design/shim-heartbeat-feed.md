# The shim-fed heartbeat: a contract between the director shim and marvel

Status: design for review, 2026-10-02. Owner: the architect role. Tracks
aae-orc-z63a4, shape 2 (finding-166; R-93 ratified). Shape 1, the shim's
`--preflight` gate, shipped as director#27. This is the contract a
director-side producer and a marvel-side receiver are built against. Design
only.

## 1. Why

finding-166: marvel reported nine sessions running while their shims had
exited on a bus with no streams, because nothing marvel reads knows whether a
seat reached its bus. The shim already renews presence on its own 30s timer.
If that same tick tells marvel "this seat is alive and its bus is writable",
a health check that reads that beat turns a dead bus into an unhealthy
seat, with the restart and crash-loop backoff that already make failure loud.

## 2. Today (checked on marvel main and director main)

- **marvel's heartbeat RPC.** Method `heartbeat` on the daemon's Unix socket,
  one JSON request per connection (`daemon.SendRequest`), params
  `api.HeartbeatRequest`: `session_key`, `session_token`,
  `context_percent`, and optional `model`, `context_tokens`,
  `context_window`. Three producers send it: `marvel ctx-forward` (the
  statusline feed), `marvel codex-ctx`, and the simulator.
- **Auth.** `session_token` is the per-session `MARVEL_HEARTBEAT_TOKEN`
  minted at spawn; the store keeps its SHA-256 and compares in constant time
  (`internal/api/heartbeat.go`). A missing or wrong token is refused and
  emits `heartbeat.refused`. marvel#414 (merged design) adds revocation, a
  TTL with renewal from a pane-scoped file, and a stable refusal `code`
  (`unauthorized`, `revoked`, `rotated`, `stale`, `expired`).
- **What a beat writes.** `UpdateSessionHeartbeat` REPLACES the session's
  context reading with the beat's, stamps `LastHeartbeat`, and stamps
  `ContextAt` (`internal/api/store.go`). So a beat that carries no context
  figures today would wipe a statusline-fed CTX% to zero.
- **Who reads `LastHeartbeat`.** The team controller's `heartbeat` health
  check (staleness against `healthcheck.timeout`, with a grace of one
  timeout from creation for the first beat; `FailureCount` against
  `failure_threshold`, then the restart policy), and shift readiness.
  `ContextAt` feeds the opt-in `(stalled)` activity advisory.
- **The shim's timer.** `bus.heartbeat` ticks every 30s (`main.go`) and
  writes the local presence row, then the global row when the global tier is
  on (director `bus.go`). It holds no marvel socket or token today.
- **The env a seat has.** marvel's `baseEnv` sets `MARVEL_SESSION`,
  `MARVEL_SOCKET` and `MARVEL_HEARTBEAT_TOKEN` when the daemon has a socket
  (`internal/runtime/adapter.go`).

## 3. The contract

### C1. Endpoint and framing

The producer sends method `heartbeat` to the socket at `MARVEL_SOCKET`, one
request per connection, with a 2s deadline for dial, write and read
together. No new method and no new transport.

### C2. The request

```json
{"session_key": "<MARVEL_SESSION>",
 "session_token": "<MARVEL_HEARTBEAT_TOKEN, or the renewal file's token under #414>",
 "kind": "liveness",
 "source": "director-shim"}
```

- `kind` is new on `api.HeartbeatRequest`. Absent or `"reading"` means
  today's behavior. `"liveness"` means: authenticate exactly as today, stamp
  a NEW field, `Session.LastLivenessAt`, and touch nothing else:
  `LastHeartbeat`, `SessionContext` and `ContextAt` are left as they were.
  A bus beat is not a context reading and not evidence that the harness is
  working. If it stamped `LastHeartbeat`, every role already on
  `type = "heartbeat"` (the README's and `examples/review-team.toml`'s
  claude roles among them) would read a hung harness as healthy as long as
  its shim kept beating, and shift readiness (`controller.go`) would count a
  successor ready on its shim alone. Keeping the two signals in two fields
  means existing heartbeat roles keep their meaning exactly.
- A liveness beat for a session record with no token hash (one written
  before tokens existed) is admitted as unbound, exactly as a reading is
  today, and emits `heartbeat.unbound`; that exemption drains as those
  sessions end, and a session spawned now always carries a hash.
- `source` is a label for events and `describe`; it is not authorization.
- `context_percent` is omitted. The struct field is a plain float, so the
  receiver must branch on `kind` before reading it.

### C3. The response

Success returns a result that echoes `{"kind": "liveness"}`. A refusal
returns the #414 `code`. The producer keys its behavior on these, never on
message text.

### C4. Cadence and meaning

- The producer sends on the shim's existing 30s presence tick, AFTER the
  local presence write, and ONLY if that write succeeded. So a marvel beat
  means "the shim is running and its local bus accepts writes". A global
  tier failure does not suppress it; the global tier has its own warning
  path and is off for most roles.
- A failed send is logged on the transition, as presence failures are. It
  never delays the presence write or the next tick.

### C5. When the producer is off

The feed runs only when `MARVEL_SOCKET`, `MARVEL_SESSION` and a token are
all present. Otherwise it is off, with one log line at start, and the shim
behaves as today. A seat outside marvel is unaffected.

### C6. Refusals and an old daemon

- **Interim, before #414's codes land on main** (today's daemon returns only
  an error string): any refusal stops the feed and logs once. The shim keeps
  serving the bus. A stopped feed shows as a liveness miss, which is the
  honest reading.
- **After #414:** `revoked`, `rotated`, `expired`, `stale`: re-read the
  renewal file once and retry on the next tick; if it is gone or refused
  again, stop the feed and log once. `unauthorized` with no token sent is a
  producer bug, logged once, and the feed stops. The producer branches on
  the code, never on the message text.
- **A daemon without C2:** an older daemon ignores the unknown `kind` field
  and treats the beat as a reading with `context_percent` 0, which wipes
  CTX%. Its response has no `kind` echo. On a success with no echo, the
  producer stops the feed and logs `daemon does not support liveness beats`
  once. At most one beat can clobber a reading, and section 4's order makes
  even that rare.

### C7. What marvel does on a miss

A third health check type, `liveness`, reads `LastLivenessAt` with the same
staleness logic the `heartbeat` type applies to `LastHeartbeat` (a grace of
one `timeout` from creation, then `FailureCount` against
`failure_threshold`, then the restart policy):

```toml
    [team.role.healthcheck]
    type = "liveness"
    timeout = "90s"          # three missed 30s beats
    failure_threshold = 2
```

A shim that never connects never beats, so one `timeout` after creation
the seat is unhealthy, and the restart policy and crash-loop backoff make it
loud in `get sessions` and the events ring. That is the "never report
running" half of z63a4. A role on `heartbeat` or `process-alive` is
unchanged; `describe session` shows the last liveness beat and its source
either way.

**The cost, stated.** A shim that wedges on a seat whose harness is working
also stops beating, and that seat is restarted, losing its context. A role
that wants the signal without that risk sets `restart_policy = "never"`:
the seat goes unhealthy and is marked in `get sessions` and the ring, and
nothing restarts it. That is the alert-only option.

Shift readiness for a `liveness` role requires a nonzero `LastLivenessAt`
on each successor, so a successor is ready once its shim reaches the bus
(ruling 2). Readiness for a `heartbeat` role is unchanged.

## 4. Rollout order (no live seat restarts by surprise)

| step | change | risk to a running seat |
|---|---|---|
| R0 | marvel#414's refusal codes land (prerequisite for C6's full form; until then the interim rule in C6 applies) | none |
| R1 | marvel accepts `kind: liveness` (C2, C3), stores `LastLivenessAt`, adds the `liveness` check type, and shows the last liveness beat in `describe` | none: no producer sends it yet, and no role uses the type |
| R2 | The shim sends liveness beats (C4 to C6) | none: the beat touches only `LastLivenessAt`, which nothing reads until R3 |
| R3 | A role declares `healthcheck.type = "liveness"` | **yes, if its seats run a shim without R2**: they never beat and would be restarted after one timeout |

R1 must merge and reach a host before R2 runs there (C6 covers the gap).
R3 is enforced, not advised: `marvel work` REFUSES a manifest that declares
`type = "liveness"` on a role with any live seat that has never sent a
liveness beat, and names the seats. `--allow-no-liveness` overrides it, and
the override is recorded in the apply event. A role with no live seats
applies without the check.

## 5. Tests (red first)

Receiver (marvel):
1. A `liveness` beat with a valid token stamps `LastLivenessAt` and leaves
   `LastHeartbeat`, `SessionContext`, `ContextAt` and CTX% exactly as a
   prior statusline reading left them; the response echoes `kind`.
2. A `liveness` beat with a wrong or missing token is refused and
   `LastLivenessAt` does not move; after R0, revoked and expired tokens are
   refused with their #414 codes.
3. A role on `liveness` with a seat that never beats goes unhealthy one
   `timeout` after creation and enters the restart policy; with beats every
   30s it stays healthy across 10 minutes; with `restart_policy = "never"`
   it goes unhealthy and is not restarted.
4. A role on `heartbeat` whose seat's harness stops its own beats while the
   shim keeps sending liveness beats goes unhealthy as today: hung-harness
   detection is unchanged. A beat with no `kind` behaves exactly as today.
5. `marvel work` refuses R3 while a live seat of the role has no liveness
   beat, naming the seat; `--allow-no-liveness` applies it and the apply
   event records the override.
Producer (director shim):
6. With all three env values set and a fake daemon socket: one request per
   tick, after a successful presence write, with the C2 body.
7. A failed presence write sends no beat on that tick.
8. Before R0, any refusal stops the feed and logs once; after R0, a
   refusal stops it after one renewal-file retry. In both, the bus keeps
   serving.
9. A success with no `kind` echo stops the feed and logs once.
10. With any env value missing, no socket is dialed and one start log line
    appears.
11. A daemon that never answers: the tick returns within 2s and presence
    still renews.

End to end:
12. The finding-166 shape: a broker with no streams, a role on `liveness`.
    The seat shows unhealthy within one `timeout` and the event names the
    liveness miss; on a provisioned broker the same seat stays healthy.
13. Shift readiness: a successor in a `liveness` role is not ready until
    its first liveness beat; a successor in a `heartbeat` role is not made
    ready by a liveness beat alone.

## 6. Custody check (SOUL section 3, ADR-009)

- [ ] The only credential is `MARVEL_HEARTBEAT_TOKEN`: issued by marvel,
  meaningful only to this daemon, revocable under #414. Issuance, not
  custody.
- [ ] The token travels in the request body over the local socket, never in
  argv or a log line; the producer logs refusal codes, never the token.

## 7. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | Extend the existing `heartbeat` method with `kind: liveness` rather than a new method | yes: one auth path, one refusal vocabulary |
| 2 | For a `liveness` role, a successor is shift-ready once its shim has reached the bus (`LastLivenessAt`); `heartbeat` roles keep today's readiness | yes |
| 3 | A new check type `liveness` reading a separate `LastLivenessAt`, recommended at `timeout` 90s and `failure_threshold` 2, declared per role, never defaulted; `restart_policy = "never"` is the alert-only form | yes |
| 4 | A global tier failure does not suppress the marvel beat | yes |
