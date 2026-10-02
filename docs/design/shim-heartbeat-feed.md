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
marvel's existing heartbeat health check turns a dead bus into an unhealthy
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
  `LastHeartbeat`, and leave `SessionContext` and `ContextAt` untouched. A
  bus beat is not a context reading and not evidence of work, so it must not
  clear CTX% or reset the stalled advisory.
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

- `revoked`, `rotated`, `expired`, `stale`: re-read the #414 renewal file
  once and retry on the next tick; if it is gone or refused again, stop the
  feed and log once. The shim keeps serving the bus.
- `unauthorized` with no token sent: a producer bug, logged once, feed stops.
- **A daemon without C2:** an older daemon ignores the unknown `kind` field
  and treats the beat as a reading with `context_percent` 0, which wipes
  CTX%. Its response has no `kind` echo. On a success with no echo, the
  producer stops the feed and logs `daemon does not support liveness beats`
  once. At most one beat can clobber a reading, and section 4's order makes
  even that rare.

### C7. What marvel does on a miss

Nothing new. A role that wants bus liveness to count declares the existing
check:

```toml
    [team.role.healthcheck]
    type = "heartbeat"
    timeout = "90s"          # three missed 30s beats
    failure_threshold = 2
```

A shim that never connects never beats, so after one `timeout` from
creation the seat is unhealthy, and the restart policy and crash-loop backoff
make it loud in `get sessions` and the events ring. That is the
"never report running" half of z63a4. A role that does not declare
`heartbeat` is not restarted on a miss; `describe session` shows the last
liveness beat and its source either way.

`LastHeartbeat` also feeds shift readiness, so a successor counts as ready
once its shim has reached the bus. That is the intended meaning here, and
ruling 2 confirms it.

## 4. Rollout order (no live seat restarts by surprise)

| step | change | risk to a running seat |
|---|---|---|
| R1 | marvel accepts `kind: liveness` (C2, C3) and shows the last liveness beat in `describe` | none: no producer sends it yet |
| R2 | The shim sends liveness beats (C4 to C6) | none for a role on `process-alive`; for a statusline-fed role, the beat is liveness-only, so CTX% is untouched |
| R3 | A role declares `healthcheck.type = "heartbeat"` | **yes, if its seats run a shim without R2**: they never beat and would be restarted after one timeout. So R3 is per role, and only after `describe` shows a liveness beat on every live seat of that role |

R1 must merge and reach a host before R2 runs there (C6 covers the gap).
`marvel work` warns when a role declares a `heartbeat` check and any live
seat of that role has never sent a liveness beat.

## 5. Tests (red first)

Receiver (marvel):
1. A `liveness` beat with a valid token stamps `LastHeartbeat` and leaves
   `SessionContext`, `ContextAt` and CTX% exactly as a prior statusline
   reading left them; the response echoes `kind`.
2. A `liveness` beat with a wrong, missing, revoked or expired token is
   refused with the #414 code, and `LastHeartbeat` does not move.
3. A role on `heartbeat` with a seat that never beats goes unhealthy one
   `timeout` after creation and enters the restart policy; with beats every
   30s it stays healthy across 10 minutes.
4. A beat with no `kind` behaves exactly as today (regression).
5. `marvel work` warns on R3 while a live seat has no liveness beat.

Producer (director shim):
6. With all three env values set and a fake daemon socket: one request per
   tick, after a successful presence write, with the C2 body.
7. A failed presence write sends no beat on that tick.
8. A refusal stops the feed after one renewal-file retry; the bus keeps
   serving.
9. A success with no `kind` echo stops the feed and logs once.
10. With any env value missing, no socket is dialed and one start log line
    appears.
11. A daemon that never answers: the tick returns within 2s and presence
    still renews.

End to end:
12. The finding-166 shape: a broker with no streams, a role on `heartbeat`.
    The seat shows unhealthy within one `timeout` and the event names the
    heartbeat miss; on a provisioned broker the same seat stays healthy.

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
| 2 | A successor is shift-ready once its shim has reached the bus (`LastHeartbeat` from a liveness beat) | yes |
| 3 | The recommended check for director-enabled roles: `timeout` 90s, `failure_threshold` 2 | yes, declared per role, never defaulted |
| 4 | A global tier failure does not suppress the marvel beat | yes |
