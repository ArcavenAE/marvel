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
  still a refusal.

An older binary reading the store ignores all three, so rollback is the
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

In B-TTL, `ErrHeartbeatExpired` is not terminal: the producer re-reads the
renewal file once and retries (section 4.3).

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
table and should not also write the store and the file tree. Old hash goes to `HeartbeatRevoked` with
`By: "rotation"`, so a beat racing the rotation reads `revoked` and the
producer re-reads the file (below) instead of stopping. That is one exception
to 3.4: a revoked hash whose `By` is `rotation` is non-terminal, and the error
says so (`ErrHeartbeatRotated`, a wrapped `ErrHeartbeatRevoked`).

### 4.3 The pane-scoped renewal file

- Path: `<marvel run dir>/sessions/<workspace>/<name>/g<generation>/heartbeat.token`,
  directory 0700, file 0600, written atomically (temp plus rename) at every
  mint and rotation.
- The pane gets the path, not only the token:
  `MARVEL_HEARTBEAT_TOKEN_FILE` beside `MARVEL_HEARTBEAT_TOKEN`. Producers read
  the file first and fall back to the environment, so a session spawned before
  B-TTL keeps working until its token expires.
- The generation directory is removed when the session exits, is reaped, or is
  replaced by a new generation. **This is the property that matters:** an
  orphan from an earlier daemon or generation points at a directory that no
  longer exists, so it cannot renew, and its token dies within one TTL. That
  turns k58k's permanent refusal into a bounded one without a reaper.
- A daemon restart reconciles the directory tree against live sessions and
  removes directories with no live session, the same pass 5yqw3 plans for its
  user table.

### 4.4 Operator surface after B-TTL

`marvel session revoke` now also rewrites the renewal file, so the live
session renews on its next beat and only other holders of the old token are
cut off. `--restart` stays available for the case where the operator does not
trust the pane itself.

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
7. B-TTL: after the generation directory is removed, a holder of the old token
   cannot renew and is refused once the TTL passes.
8. B-TTL: a beat racing rotation is admitted after one file re-read, not
   stopped.

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
| The renewal file and `MARVEL_HEARTBEAT_TOKEN_FILE` | thrown away |
| `HeartbeatRevoked` list | thrown away: removing the verify_and_map user line is the revocation |
| `marvel session revoke` verb and its RPC scope | **kept**, re-pointed at removing the session's certificate user line and reloading |
| Distinct outcomes `revoked`, `stale`, `expired`, `no-token`, and the producers stopping on terminal ones | **kept**, as certificate-verification outcomes |
| `describe` `issued_at`, `generation`, `expires_at` | **kept**, filled from the certificate's not-before, serial generation, and not-after |
| Generation-directory reconcile on daemon restart | **kept**, as the same pass 5yqw3 runs on its user table |

So roughly the operator surface and the event vocabulary carry over, and the
bearer mechanics do not.

## 8. Edits, in order (none made by this PR)

| # | Edit | Depends on |
|---|---|---|
| HB-1 | Session fields; distinct outcomes, errors, event kinds, and the response `code`; tests 2 and 3 | none |
| HB-2 | `marvel session revoke` (with `--restart`) and its RPC; test 1 | HB-1 |
| HB-3 | `describe session` heartbeat block; test 4 | HB-1 |
| HB-4 | Producers stop on terminal outcomes; test 5 | HB-1 |
| HB-5 | B-TTL: expiry, rotation, renewal file, restart reconcile; tests 6 to 8 | HB-2, HB-4 |

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
