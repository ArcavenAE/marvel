# Heartbeat token lifetime, interim (B2, aae-orc-83xu)

Design for review. No code lands until this doc is reviewed.

**INTERIM.** Everything here is replaced when the session certificate becomes
the bus and heartbeat credential (aae-orc-5yqw3; the 2026-09-16 ruling in the
orc's `docs/design/fleet-ca-and-bus-auth.md` section 3.1: "two live
credentials on one session is a defect"). Section 7 lists what is thrown away
then and what carries over.

**Status: ruled 2026-09-30** (operator via director 01M3RBGXEW, relayed by
arcaven-supervisor-g5-0 01M3RBMMW2), verbatim "B-Full": B-min first, then a
TTL with renewal through a pane-scoped file. This reverses the 2026-09-27
choice of option A (wait for 5yqw3) recorded in 83xu's notes.

- Author: arcaven-architect-g5-0.
- Tracks: aae-orc-83xu. Refs: aae-orc-5yqw3, aae-orc-k58k, aae-orc-ww33y,
  marvel#186, ADR-009.

## 1. The problem, verified

Against marvel `origin/main` b1f4953:

| Premise | Where | Result |
|---|---|---|
| Minted once per session, never again | `internal/session/manager.go:543-548` | `NewHeartbeatToken()` at `Create`; plaintext on the pointer, hash stored |
| Plaintext only in memory and pane env | `internal/api/types.go:280-306` | `HeartbeatToken json:"-"`; `HeartbeatTokenHash` persists |
| No expiry, rotation or revocation | `internal/api/heartbeat.go:59-78` | `authenticateHeartbeat` compares one hash; no time, no generation, no revoked set |
| Refusal already splits by cause, not by kind | `internal/daemon/daemon.go:1650-1668` | one kind `heartbeat.refused` with cause `no-token` or `stale-token` |
| The holder is told only an error string and keeps retrying | same, `Response{Error: err.Error()}` | observed today: `marvel orphans` on kinu lists a refusal for `aae/arcaven-architect-g5-0` at 18:52:36Z |
| No revoke verb exists | `git grep -n revoke origin/main -- cmd/marvel` | only `marvel keys revoke <fingerprint>` (clients, not sessions) |

## 2. Scope

Two phases, each its own PR, B-min first.

- **B-min:** an operator can revoke a session's token; the daemon and the
  holder can tell revoked, stale, and forged apart; `describe` shows when and
  for which generation the token was issued.
- **B-TTL:** tokens expire, and a live session renews from a file only its own
  pane generation can find, so an orphan's credential dies within one TTL
  without anyone reaping it.

Out of scope: confidentiality against a same-uid sibling (aae-orc-ww33y). A
0600 file is no harder for a same-uid process to read than a pane's
environment. This design bounds a token's lifetime; it does not hide it.

## 3. B-min

### 3.1 Session record (additive, json omitempty)

- `HeartbeatIssuedAt time.Time`: set at every mint.
- `HeartbeatGeneration int64`: the session's `Generation` at mint.
- `HeartbeatRevoked []RevokedToken`: `{Hash, RevokedAt, By}`, most recent
  first, capped at 8. Only hashes; the plaintext was never stored. The cap
  keeps the record small; a hash older than the cap reads as stale, which is
  still a refusal. Operator revocations only; rotations do not use it.
- `HeartbeatPrevious string` (B-TTL): the one hash a rotation replaced. A
  single slot, overwritten by the next rotation. Kept apart from
  `HeartbeatRevoked` so rotations never crowd out revocations, and so
  `describe` does not count a rotation as a revocation.

An older binary reading the store ignores these fields, so rollback is the
previous binary.

### 3.2 `marvel session revoke <workspace/name>`

1. Moves the current hash onto `HeartbeatRevoked`.
2. Mints a new token and hash, sets `HeartbeatIssuedAt`.
3. Emits `heartbeat.revoked.issued` with the session coordinates and `By`
   (the RPC caller's client fingerprint).
4. Prints the new `issued_at` and generation. Never the token.

In B-min the live session is not handed the new token: its pane environment
cannot change. So revoke in B-min ends that session's heartbeats too, and the
CLI says so and suggests `marvel restart`. B-TTL's renewal file is what lets
the live session pick up the new token (section 4.3). The verb takes
`--restart` to do both in one step.

The RPC is admin scope only: `methodAllowedForScope` (`internal/daemon/scope.go:71`)
confines `credential-push` keys to their own set, and revoke is not in it.

### 3.3 Distinct outcomes

`authenticateHeartbeat` returns one of:

| Presented | Result | Event kind | Error to holder |
|---|---|---|---|
| matches current hash | admitted | none | ok |
| empty, record has a hash | refused | `heartbeat.refused` cause `no-token` (unchanged) | `ErrHeartbeatUnauthorized` |
| matches a hash in `HeartbeatRevoked` | refused | `heartbeat.revoked` | `ErrHeartbeatRevoked` |
| matches `HeartbeatPrevious` (B-TTL) | refused | none; not an orphan sighting | `ErrHeartbeatRotated` |
| matches nothing known | refused | `heartbeat.stale` (was `heartbeat.refused` cause `stale-token`) | `ErrHeartbeatStale` |
| record has no hash | admitted unbound | `heartbeat.unbound` (unchanged) | ok |

`heartbeat.stale` keeps the current orphan text and still feeds
`d.orphans.observe`. `heartbeat.refused` stays registered in the event kinds
so filters written against it still match the `no-token` case; the change
note in the PR says that `stale-token` moved to its own kind.

Today the holder gets only `Response{Error: err.Error()}`, a string; nothing
on the client side maps it back to a sentinel (`git grep
ErrHeartbeatUnauthorized` finds no client use). So the heartbeat response
gains a stable `code` field (`unauthorized`, `revoked`, `rotated`, `stale`,
`expired`) beside the message, and producers branch on the code, never on the
text.

### 3.4 The holder stops

The three producers (`cmd/marvel/ctxforward.go:446`,
`cmd/marvel/codexctx.go:130`, `cmd/simulator/main.go:81`) treat
`ErrHeartbeatRevoked` and `ErrHeartbeatStale` as terminal for the feed: log
once to stderr, stop sending. This answers 83xu question 3 ("nothing stops it
retrying forever") for every producer marvel ships. A process marvel does not
ship keeps retrying and keeps being refused, throttled, which is today's
behavior.

In B-TTL the producer reads the renewal file before every beat (section 4.3),
and one rule covers every refusal: on `revoked`, `rotated`, `expired` or `stale`, re-read
the file once. If it yields a different token, retry with it; if it yields the
same token, or the file is gone, stop as above. So a live seat survives an
operator revoke and a rotation race, and a holder whose file is gone stops.
In B-min there is no file, so `revoked` and `stale` stop at once.

### 3.5 `marvel describe session`

Adds, under a `Heartbeat` block: `issued_at`, `generation`, `expires_at`
(B-TTL only), and `revoked` as a count with the most recent `revoked_at`.
Never the token or any hash.

## 4. B-TTL

### 4.1 Expiry

- `HeartbeatTTL` resolves: role manifest field `heartbeat_ttl`, then the
  cluster default, then 24h. Minimum 10m, so a slow feed cannot expire
  between beats.
- `authenticateHeartbeat` refuses a matching hash when
  `now > HeartbeatIssuedAt + TTL`: event `heartbeat.expired`, error
  `ErrHeartbeatExpired`.
- `describe` shows `expires_at`.

### 4.2 Rotation

The daemon re-mints each live session's token at half its TTL. It runs as its
own ticker (every minute is enough), not inside the team controller's 2s
reconcile (`internal/daemon/daemon.go:52`), which reads the whole process
table and should not also write the store and the file tree.

**The order, and why it is this order.** Writing the file before the hash
swap lets a beat that already read the new token reach the daemon while the
store still holds the old hash; it is refused as stale, its re-read returns
the same token, and a live seat stops (and is recorded as an orphan). Writing
it after lets a beat carry the old token past the swap with no new file to
find. So the two happen as one step under the store lock, the same lock
`UpdateSessionHeartbeat` holds around `authenticateHeartbeat`:

1. Outside the lock: mint the token and write it to a temp file beside the
   renewal file.
2. Under `s.mu`: move the current hash to `HeartbeatPrevious`, set the new
   hash, persist the record, then rename the temp file into place. If the
   persist fails, nothing is renamed and the in-memory record is restored. If
   the rename fails, the persisted record is restored.
3. Release the lock.

No beat is authenticated between the swap and the rename, so a beat presents
either the old token (refused `rotated`: the producer re-reads and finds the
new one) or the new token (admitted). A crash between the persist and the
rename leaves the file on the old token and the store on the new: the restart
reconcile (4.3) compares the hash of each file's contents with the record and
rotates again on any mismatch, before it admits beats.

### 4.3 The pane-scoped renewal file

- Path: `<marvel run dir>/sessions/<workspace>/<name>/<spawn-id>/heartbeat.token`,
  where `<spawn-id>` is a random nonce minted at each spawn and stored on the
  session record as `HeartbeatDir`. Directory 0700, file 0600, written
  atomically (temp plus rename) at every mint and rotation. A daemon restart
  that adopts a live pane keeps its `HeartbeatDir`; a respawn of the same
  session key gets a new one.
- The pane gets the path, not only the token:
  `MARVEL_HEARTBEAT_TOKEN_FILE` beside `MARVEL_HEARTBEAT_TOKEN`. Producers read
  the file before every beat (a small local read per beat) and fall back to the
  environment when the variable is unset, so a session spawned before B-TTL
  keeps working until its token expires.
- A spawn directory is removed when its session exits or is reaped, and when
  the record's `HeartbeatDir` no longer names it. **This is the property that
  matters:** an orphan points at its own spawn directory, and the only
  directory that survives is the one the live record names. That holds for an
  orphan of an earlier generation and for the k58k case, an orphan of an
  earlier daemon at the same generation: the respawn that replaced it minted a
  new spawn id, so the orphan's directory is gone, it cannot renew, and its
  token dies within one TTL without a reaper.
- The bound is against orphans that follow their own environment, which every
  producer marvel ships does. It is not against a same-uid process that lists
  the run directory and reads another session's file; that is aae-orc-ww33y,
  out of scope here (section 2).
- A daemon restart reconciles the directory tree against live sessions and
  removes every directory the live records do not name, and for every live
  session compares the hash of its file's contents with the record, rotating
  on any mismatch (the crash case in 4.2). This is the same pass 5yqw3
  plans for its user table. Before it admits any beat, the same pass rotates
  every live token already past half its TTL (or expired) and rewrites its
  file, so a daemon that was down longer than a token's remaining life does
  not come back to a fleet of expired tokens.

### 4.4 Operator surface after B-TTL

`marvel session revoke` now also rewrites the renewal file, in the same
locked step as 4.2, except that the old hash goes to `HeartbeatRevoked`
rather than `HeartbeatPrevious`. The live session reads the file before its
next beat and presents the new token. A beat that read the old file just
before the step is refused `revoked`, re-reads, finds the new token (the rename
finished inside the lock) and retries. A holder without the file (an orphan
whose directory is gone) is refused `revoked`, re-reads, finds nothing new, and
stops. `--restart` stays available for the case where the operator does not
trust the pane itself.

### 4.5 Renewal end to end, and a missed renewal

- **Who renews, and when.** Only the daemon. At half the TTL it swaps the hash
  and renames the new token into the spawn's renewal file as one locked step
  (4.2). The seat never
  mints; its producer reads the file before every beat. No human step.
- **A failed write.** If the temp write, the persist or the rename fails, the
  step is undone as a whole (4.2). The daemon keeps the old hash, emits
  `heartbeat.rotation-failed` (throttled like the other auth notices), and
  retries on the next rotation tick. The seat keeps beating on the old token
  until it expires.
- **Expiry without a renewal.** The beat is refused `heartbeat.expired` (an
  event on the ring). The producer re-reads, finds the same token and stops
  sending (3.4). The session gains a visible condition, `heartbeat expired`, in
  `get sessions` and `describe session`. The seat's process keeps working;
  what it loses is its heartbeat feed (liveness readings, CTX%).
- **The restart path.** A role with a `heartbeat` healthcheck goes stale and
  its restart policy restarts it with a fresh token, automatically. A role
  with `process-alive` health is not restarted by marvel: the condition and
  the event name the fix, `marvel session revoke --restart`. marvel does not
  restart a working seat on its own for a lost heartbeat (automation proposes,
  it does not judge).
- **Never silent.** Every path above leaves an event and, for expiry, a
  visible condition.

## 5. Tests (red first on b1f4953)

1. Revoke: a token presented after `session revoke` is refused with
   `heartbeat.revoked`; the new token is admitted.
2. Stale: a token from nowhere is refused with `heartbeat.stale`, and
   `marvel orphans` records it.
3. `no-token` still emits `heartbeat.refused`.
4. `describe session` output contains `issued_at` and `generation` and does
   not contain the token or its hash (grep the output for both).
5. Producers stop after `revoked` or `stale` (one log line, no further RPCs in
   a fake-clock loop).
6. B-TTL: an expired token is refused with `heartbeat.expired`; after the file
   is rewritten, the producer renews and is admitted.
7. B-TTL: a holder whose spawn directory is gone cannot renew and is refused
   once the TTL passes, in two cases: an orphan of an earlier generation, and
   an orphan of an earlier daemon at the same generation (the respawn minted a
   new spawn id). The second is the k58k case.
8. B-TTL: a beat racing rotation is admitted after one file re-read, not
   stopped, and is not recorded as an orphan. Both interleavings: the beat
   read the old file before the locked step (refused `rotated`, then
   admitted), and the beat arrives after it (admitted). No interleaving
   produces `stale` for a live seat.
9. B-TTL: after `session revoke`, the live pane's next beat (reading the
   rewritten file) is admitted, and a holder of the old token without the file
   is refused `revoked` and stops.
10. B-TTL: a daemon down past a token's remaining life rotates it at restart
    before admitting beats, so the live pane's first beat is admitted.
11. B-TTL: a crash injected between the persist and the rename leaves the
    file on the old token; the restart reconcile finds the hash mismatch and
    rotates before admitting beats, and the live pane's next beat is
    admitted.
12. B-TTL: a rotation whose file write fails keeps the old hash, emits
    `heartbeat.rotation-failed`, and succeeds on a later tick; if the token
    expires first, the beat is refused `heartbeat.expired` and the session
    shows the `heartbeat expired` condition.

## 6. Migration

- B-min is additive: the new fields are empty on existing records, which read
  as issued-at-unknown and nothing revoked. No running session changes
  behavior until someone runs `revoke`.
- B-TTL: records with no `HeartbeatIssuedAt` are treated as issued at daemon
  start, so the fleet does not expire all at once on upgrade. Existing panes
  have no `MARVEL_HEARTBEAT_TOKEN_FILE`, so they expire one TTL after the
  upgrade and must be restarted or shifted within that window. The PR states
  the window, and the rollout goes one cluster at a time with the seats'
  handoffs written first.
- Rollback: the previous binary; every field is omitempty.

## 7. What 5yqw3 throws away, and what it keeps

When the session certificate becomes the credential (5yqw3, then 83xu's
section 3.1 ruling), the heartbeat verifies the certificate key and the token
retires.

| Part | Fate |
|---|---|
| The token, its hash, `MARVEL_HEARTBEAT_TOKEN` | thrown away |
| `HeartbeatTTL`, rotation at half-life | thrown away: the certificate carries its own not-after, and removal-and-reload is its lifetime enforcement |
| The renewal file, `MARVEL_HEARTBEAT_TOKEN_FILE`, and `HeartbeatPrevious` | thrown away |
| `HeartbeatRevoked` list of hashes | **reshaped**: removing the verify_and_map user line is the revocation, but on its own it makes a revoked certificate look the same as an unknown one. A revoked-serial list (certificate serials, not hashes, same cap) is kept so `revoked` stays distinct from `stale` |
| `marvel session revoke` verb and its RPC scope | **kept**, re-pointed at removing the session's certificate user line and reloading |
| Distinct outcomes `revoked`, `stale`, `expired`, `no-token`, and the 3.4 producer rule | **kept**, as certificate-verification outcomes; `revoked` depends on the revoked-serial list above |
| `describe` `issued_at`, `generation`, `expires_at` | **kept**, filled from the certificate's not-before, serial generation, and not-after |
| Spawn-directory reconcile on daemon restart | **kept**, as the same pass 5yqw3 runs on its user table |

So roughly the operator surface and the event vocabulary carry over, and the
bearer mechanics do not.

## 8. Edits, in order (none made by this PR)

| # | Edit | Depends on |
|---|---|---|
| HB-1 | Session fields; distinct outcomes, errors, event kinds, and the response `code`; tests 2 and 3 | none |
| HB-2 | `marvel session revoke` (with `--restart`) and its RPC; test 1 | HB-1 |
| HB-3 | `describe session` heartbeat block; test 4 | HB-1 |
| HB-4 | Producers stop on terminal outcomes; test 5 | HB-1 |
| HB-5 | B-TTL: expiry, rotation, renewal file, restart reconcile, the missed-renewal path; tests 6 to 12 | HB-2, HB-4 |

marvel-builder builds only after this design is reviewed.

## 9. Open questions

- **ADR-009.** 83xu's notes say closing this makes ADR-009's canonical
  permitted example meet its own "revoke or re-mint" clause. B-min does that
  for revocation. Whether that note should be recorded in ADR-009 is not this
  design's call.
- **Default TTL.** 24h is the options doc's number, not a measured one. A
  seat that runs for days renews through the file, so the TTL only bounds
  orphans. A shorter default (for example 4h) bounds them harder at the cost
  of more rotation writes. Operator's choice; 24h stands unless ruled.
