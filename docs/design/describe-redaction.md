# Redaction: what marvel's read surfaces may print

Design for review. No code lands until this doc is reviewed.

- Author: arcaven-architect-g5-0.
- Issue: #421. Split from #418 by the operator, 2026-09-30, verbatim: "we may
  need some kind of redaction support, it must be considered. but it should
  be treated as a separate item".
- Precedents: `HeartbeatToken` (`internal/api/types.go:286-293`) and
  `Credential.Value` (`:680-707`), both kept off every wire surface.
- Checked against marvel `origin/main` 4b2fd5c; citations re-checked on 12b08d0.

## 1. The problem, verified

| Premise | Where | Result |
|---|---|---|
| describe marshals the stored record whole | `handleDescribe`, `internal/daemon/daemon.go:1332` (`json.Marshal(result)` for session, team, workspace, endpoint, credential) | true |
| get does the same for lists | `handleGet`, `daemon.go:1248` (`ListSessions`, `ListTeams`, ...) | true |
| plan does it too | `handlePlan`, `daemon.go:1318`, marshals `RolePlan.Delete []api.Session` (`internal/team/controller.go:993`) | true: a verb-by-verb fix would miss it |
| `Env` and `Runtime` carry no json tag | `types.go:189`, `:214` | true: every declared Env value, every Args entry and the Prompt go out on get and describe, for sessions and for teams' roles |
| Every seat can call those methods | `MARVEL_SOCKET` is in every seat's environment (`internal/runtime/adapter.go:358`, `internal/session/manager.go:1194`), and the local socket caller is admin (`internal/daemon/scope.go:17-19`) | true: any seat can read any other seat's declared Env |
| A json tag is not the fix | the bolt store encodes records with the same `encoding/json` (`internal/api/bolt.go:285`) | true: `json:"-"` on `Env` would drop it from the durable record, and a restarted daemon would respawn roles without their Env |
| The constructed env (bus password, heartbeat token) is not in `Runtime.Env` | `baseEnv` builds it at launch (`adapter.go:340-358`); the declared map is copied from the manifest (`manifest.go:693`) | true: the exposure is the manifest-declared values, not marvel's own secrets |
| Events and the daemon log carry Env values | `git grep` for `Runtime.Env` beside `log.` or `Emit` in `internal/` and `cmd/` | none found; test R5 keeps it that way |
| The store file is private | `bolt.Open(path, 0o600, ...)`, `bolt.go:99` | true: values at rest are readable by the daemon's user only |

## 2. The rule

**The durable record keeps what marvel needs; the wire gets a view.** Redaction
happens daemon-side, once, at the RPC boundary, so no client (local socket,
`mrvl://`, a seat, the CLI) can receive a value it was not meant to have. The
store and bolt are unchanged.

**By record type, not by verb.** Records leave the daemon through more
methods than get and describe: `plan` returns whole sessions in each role's
`Delete` list (`handlePlan`, `internal/daemon/daemon.go:1318`; `RolePlan.Delete
[]api.Session`, `internal/team/controller.go:993`), and a later method could do
the same. So the redaction is not attached to verbs. Every successful response
body is built by one function, `respond` (`internal/daemon/redact.go`), which
every handler returns through and which `events.watch` encodes, and it
redacts every `api.Runtime` it finds, at any depth, whichever method built the
value (`api.Redact`). Handlers hand it values rather than pre-marshalled
bytes. A handler that still marshalled its own bytes would bypass it, so a
source test (`response_source_test.go`) fails if anything else, in any
non-test package of the module, sets `Response.Result`: a keyed or positional
literal, an assignment, or the address taken. The test matches on the
`Response` type, and a planted write of each form proves it catches them.

The same function serves every transport. Nothing after it can reconstruct a
value.

## 3. Which fields, and what shows in their place

| Field | Sensitive? | Wire shows |
|---|---|---|
| `Runtime.Env` values (sessions, and roles inside teams) | yes, by default: marvel cannot know which values are secrets, and the field is where a manifest author puts them | the key, and the value replaced by `(redacted)`. No length, no hash: either narrows a short secret |
| `Runtime.Env` keys | no | as today |
| `Runtime.Args` | not a secret channel | as today. `marvel work` warns (never refuses) when an arg looks like a secret flag (`--api-key`, `--token`, `--password`, `--secret`, and the `=value` forms), naming the role and the flag, not the value |
| `Runtime.Prompt` | not a secret channel | as today |
| `HeartbeatToken`, `Credential.Value` | yes | already off the wire; unchanged |

"Not a secret channel" is a documented contract: the user guide says Args and
Prompt are printed by `describe`, and that a secret goes in `env` or, better,
in an `env_from` reference (section 5).

## 4. Who can ever see a value

Nobody, through marvel's read surfaces. There is no reveal verb in this design.

A reveal path like the credential one (local socket only) would protect
nothing here: every seat is a local socket caller with admin scope (section
1). The operator already has the values, in the manifest they applied. A
reveal verb waits until the socket can tell a seat from the operator, which
is its own issue (section 8).

## 5. Keeping secrets out of the record: `env_from`

Redaction limits who reads a value. It does not stop marvel from holding it at
rest. A manifest can instead name where a value comes from:

```toml
    [team.role.runtime.env_from]
    SERVICE_TOKEN = "file:/Users/me/.config/service/token"
    REGION        = "env:AWS_REGION"
```

- `file:` reads the file at spawn; `env:` reads the daemon's own environment
  at spawn.
- The reference is stored and shown; the value is resolved into the pane's
  environment at launch and is never stored in marvel's store, logged,
  emitted or put on the wire.
- **What `env_from` does not hide.** At launch every Env value, resolved or
  literal, is passed to tmux as `new-window -e K=V`
  (`internal/tmux/driver.go:317-318`). It sits on that tmux client's argv,
  visible to `ps` for the call's lifetime, and in the window's environment
  afterwards. That is the finding-020 class named in section 8. `env_from`
  keeps values out of the record, not out of process listings. The error path
  is clean: `NewPane` reports tmux's output, not its argv (`:322-324`).
- **`env_from` does not widen reach.** Only an admin caller can apply a
  manifest, and an admin caller can already read daemon-host files through
  `inject` and `capture`; a `credential-push` key cannot apply
  (`internal/daemon/scope.go`). A `file:` reference is not a new remote file
  read.
- A reference that cannot be resolved refuses the spawn with
  `session.env-unresolved` naming the key and the reference, never a value,
  and is not charged to the restart policy.
- A key in both `env` and `env_from` is refused at apply.

**Custody (ADR-009).** A file or daemon-env reference means marvel reads a
value the operator already placed on the host, for the one spawn, and holds it
nowhere. It does not make marvel a holder of the credential. Keychain or vault
references would add a fetch from a third-party store; whether that is
brokering (permitted) or custody depends on the artifact, so it is left out of
this design and named as ruling 3.

## 6. Events, logs and errors

The rule for every emitter: an Env value, or an `env_from` resolved value,
never appears in an event, a daemon log line, an RPC error string, or `marvel
work` output. Keys and reference strings may. This is already true on main
(section 1); test R5 makes it a guard.

## 7. Tests (red first on 4b2fd5c)

1. For a role with `env = {K = "canary-1"}`, the raw RPC responses to
   `describe session` and `get sessions` (what any socket caller receives, not
   the CLI table) show key `K` with `(redacted)`; the canary appears nowhere in
   the response bytes.
2. The same for `describe team` and `get teams`.
3. The same over an `mrvl://` client.
4. After a daemon restart the respawned session's pane still has `K=canary-1`
   (the store kept it).
5. A canary sweep over every method in the dispatch table: with a canary
   role applied and running, call each method with valid params (including
   `plan` for a scale-down to 0, whose `Delete` list carries the session) and
   with invalid params; also a failed apply and a spawn refusal. Grep every
   response, the event ring and the daemon log ring for the canary. Zero hits.
   A method added later without a sweep entry fails the test.
6. `env_from` `file:` and `env:` resolve into the pane; the value is absent
   from the bolt file (read the raw bytes) and from every surface in test 5.
7. An unresolvable `env_from` refuses with `session.env-unresolved`, restart
   count 0.
8. `marvel work` with `args = ["--api-key", "x"]` prints a warning naming the
   role and `--api-key`, not `x`, and applies.

## 8. Out of scope, named

- **Per-caller scope on the local socket.** Every seat is admin today. That is
  the larger exposure: a seat can also scale, kill and apply. It needs a seat
  identity on the socket (the heartbeat token is one candidate) and belongs in
  its own issue; this design only avoids depending on it.
- **Redacting Args or Prompt.** Declared not secret channels instead (section
  3). If the operator wants either redacted, the view in section 2 is where it
  goes.
- **Values the seat itself holds.** A value in a pane's environment is visible
  to that seat by design (finding-020); redaction governs marvel's surfaces,
  not the seat.

## 9. Edits, in order (none made by this PR)

| # | Edit | Depends on |
|---|---|---|
| RD-1 | Redaction at the dispatch boundary for every response, by record type (`api.Runtime` at any depth); handlers return values; Env values `(redacted)` | none |
| RD-2 | Canary sweep test over ring, log, errors and responses (test 5) | RD-1 |
| RD-3 | `env_from` with `file:` and `env:`, resolution at spawn, refusal event | RD-1 |
| RD-4 | Apply-time warning for secret-looking Args | none |
| RD-5 | User guide: what describe prints; Args and Prompt are not secret channels | RD-1 |

The socket scope issue in section 8 is filed separately, not as a child here.

## 10. Rulings

1. **Which Env values print as `(redacted)`.** Ruled (c), relayed by director
   on 2026-10-08: redact only Env keys that look like secrets. This replaces
   the default offered (every Env value, no per-key opt-out), and it changes
   the Env row of section 3: a value prints as `(redacted)` when its key
   matches the pattern list in `internal/api/redact.go` (`SecretKey`, matched
   case-insensitively), and every other value prints as declared. The cost: a
   credential stored under a key that matches no pattern prints.
2. **Args and Prompt stay visible, as a documented contract.** Ruled (a),
   relayed by director on 2026-10-08, with the RD-4 warning.
3. **Keychain and vault references in `env_from`.** Default offered: not in
   this design; `file:` and `env:` only, pending an ADR-009 reading per source.
   Not ruled.
4. **No reveal verb until the socket can tell a seat from the operator.**
   Default offered: yes. Not ruled.
