# Egress credential front: marvel runs agent-vault so seats never hold provider keys

Design for review. No code lands until this doc is reviewed.

- Author: the arcaven team's architect seat, 2026-10-19.
- From the arcaven research-supervisor's plan (2026-10-16) and its spec of the
  operator's seven rulings (2026-10-19), applied here.
- Checked against marvel `origin/main` 69461c4.
- The rule it applies: ADR-009 as amended 2026-10-19, the fleet's credential
  custody decision. Third-party bearer material may be held in memory while
  running. At rest it, or the key that decrypts it, lives only in an
  operator-owned store marvel cannot write. marvel may keep ciphertext whose
  key lives only there.

## 1. Why

A seat that calls a model or a vendor API today needs the provider key in its
own environment, where anything the seat runs can read it and anything it
prints can leak it. agent-vault is a local proxy that adds the key to a
seat's outbound request, so the seat holds only a token that agent-vault can
revoke. marvel already supervises child services (the bus is one), so marvel
runs agent-vault as a managed service: it starts it, loads the keys from the
operator's keychain, gives each seat a token, and stops it.

The cost is that marvel holds provider keys in memory while it runs. The
amended ADR-009 allows that, and nothing else: no key rests in anything
marvel owns.

## 2. The rulings this design applies

| # | Ruled 2026-10-19 | What it means here |
|---|---|---|
| 1 | Amend ADR-009 now | Section 1's rule; section 9's tripwires |
| 2 | A dedicated keychain whose items trust only the marvel binary | Section 4 |
| 3 | Kept state: master password in the keychain, database kept | Sections 3 and 5 |
| 4 | The vendor's signed release; telemetry off by environment; document and warn on the analytics key | Section 6 |
| 5 | `NO_PROXY` for the harness's own model and login hosts | Section 7 |
| 6 | First seat: a builder seat with a provider API key | Section 10, step 4 |
| 7 | Seat token: a scoped session of up to 7 days | Section 7 |

## 3. The shape

**Mode: managed.** `internal/service/service.go` has three modes. `adopted`
is a service someone else started, and its record has no field for a foreign
credential on purpose. `external` reaches a service elsewhere. Only `managed`
(`:36`) covers marvel starting the process, loading the keys and minting seat
tokens. The comment above it (`:32-34`) already records that what a managed
service mints is issuance under ADR-009.

**Record.** Class `egress-credential-front`, provider `agent-vault`,
`caller_identity: api-key`, both registered in code.

**What agent-vault keeps, and where.** Everything resolves from the child's
`HOME`, which marvel sets through `MintedEnv` (`internal/workload/process.go:37-39`;
a minted key overrides an allowed one in `childEnv`, `:123`). The home is a
fixed directory in marvel's run directory, mode 0700, kept across restarts.

| Under the home | Holds | At rest |
|---|---|---|
| the database | provider keys sealed under a data key; the data key wrapped by the master password; users, agents, token hashes, a 7-day request log | ciphertext; readable only with the master password |
| the CA key and certificate | the proxy's TLS root key, sealed under the master key; the public certificate | ciphertext, plus a public file |
| the CLI's session files | the CLI's login token | never written: marvel uses the HTTP API, never the CLI |
| the detached-mode log | only with `--detach` | never written: marvel runs it in the foreground |

The child environment allowlist keeps out `DATABASE_URL`, which would move
the store to a database server, and anything else not named in reviewed code.

Never called: agent-vault's own `run` command. In its container isolation
mode it copies the harness login from the keychain into a plaintext file.

## 4. The operator's keychain

A dedicated keychain file, owned by the operator's macOS account, with its
own password that only the operator types. It locks on sleep and after a
timeout the operator picks. Its items trust only the marvel binary, so while
it is unlocked marvel reads them without a prompt and other programs get one.

Items, all service `marvel.agent-vault`, account as named:

| account | holds | written by |
|---|---|---|
| `master-password` | agent-vault's master password, random | the operator's setup |
| `owner` | agent-vault's owner credential | the operator's setup, before first start |
| `<credential-name>` | one provider key each | the operator's setup |

marvel never writes the keychain. No code path in marvel creates, updates or
deletes an item. A design or PR that adds one re-opens ADR-009 (section 9).

**The setup, by the operator, on the host that runs the service.** A
keychain password prompt can only be answered by its owner, so this is the
one step that needs the operator's hand. The commands, with `<seconds>` and
`<marvel>` (the resolved marvel binary, not the brew link) filled in:

```sh
security create-keychain -P ~/Library/Keychains/marvel-agent-vault.keychain-db
security set-keychain-settings -l -u -t <seconds> ~/Library/Keychains/marvel-agent-vault.keychain-db
security add-generic-password -s marvel.agent-vault -a master-password \
  -T <marvel> -U ~/Library/Keychains/marvel-agent-vault.keychain-db -w
security add-generic-password -s marvel.agent-vault -a owner \
  -T <marvel> -U ~/Library/Keychains/marvel-agent-vault.keychain-db -w
security add-generic-password -s marvel.agent-vault -a <credential-name> \
  -T <marvel> -U ~/Library/Keychains/marvel-agent-vault.keychain-db -w
```

`-P` and a trailing `-w` make `security` prompt, so no value reaches argv or
shell history. Not verified: whether `security` still reads options after
the keychain path, which is a positional argument; if it stops there, the
trailing `-w` is lost. Step 1 runs these exact lines against the scratch
keychain, and the setup names the keychain another way if they fail. Never `-A` (it trusts every program). The master password and
owner credential are random: the operator generates each and enters it at the
prompt. How to get a random value into the prompt without it passing through
the clipboard is checked in step 1 (whether `security` reads a trailing `-w`
from a pipe is not verified).

After a marvel upgrade the binary's identity changes, so the keychain may
prompt once for access (inferred, measured in step 1). That prompt is the
operator's.

## 5. Start, restart and stop

**Start**, with no secret on disk outside the keychain, in argv, in a child
environment or in a log:

1. marvel reads `master-password` from the keychain into a buffer.
2. It starts `agent-vault server --password-stdin`, writes the password to
   the child's stdin, closes it, and zeroes the buffer. Not the environment
   route: agent-vault unsets the variable after reading it, but on macOS a
   process's starting environment stays readable by the same user with
   `ps -E` (that this survives the unset is inferred; M9 measures it).
   `ProcessSpec` has no stdin field today (`process.go:26-53`), so this needs
   build item 2.
3. **First start only**, against an empty database: marvel registers the
   owner with the `owner` item. An uninitialised server makes its first
   registrant the owner, which is a race against any local process, so
   marvel registers before it reports the service ready and checks that the
   owner is its own (M4). Every later start logs in with the same item.
4. For each provider item named in the class config, marvel reads it into a
   buffer, loads or updates it through agent-vault's credentials API, and
   zeroes the buffer. It loads the host-to-credential entries, sets the
   unmatched-host policy to deny (agent-vault's default is passthrough), and
   reads the policy back.
5. Telemetry off, section 6.

A locked keychain or a missing item makes the start refuse, with one event
naming the item and no retry loop (M7).

**Restart** (a crash, a stop and start, or a marvel reexec): the database is
kept, so every seat token stays valid and bound seats keep working once the
service is back. A marvel reexec leaves the successor without the owner
session; it logs in again from the keychain.

**Stop** ends the process. The database stays, as ciphertext. Nothing marvel
owns holds a key or the means to decrypt one.

**Recovery from a compromised database or host:**

1. Stop the service.
2. Rotate every provider key at its vendor's console, and update its
   keychain item.
3. Replace the `master-password` and `owner` items with new random values.
4. Delete agent-vault's home directory.
5. Start the service. It comes up empty, registers the owner again (first
   start), and loads the new keys. Every old seat token is gone, so every
   bound seat restarts with a new one.

The order matters. A database deleted before the keys are rotated still
leaves old keys valid at the vendors.

## 6. The signed release and its analytics key

marvel installs the vendor's signed release at a pinned version, verifying
its checksum and signature at install. That release carries an analytics key,
so telemetry is off only while `AGENT_VAULT_TELEMETRY=false` is set (and any
install-time opt-out the release documents). One missing variable turns it on.

What marvel does about it:

- The driver sets `AGENT_VAULT_TELEMETRY=false` as a code constant in the
  child environment. No config can unset it.
- If agent-vault exposes its telemetry state (through its status or config),
  the driver reads it after start and refuses to keep the service running if
  telemetry shows enabled. If it does not, marvel raises a warning event on
  every start naming the risk.
- `marvel get service` and `describe` show a warning line for this service:
  the release carries an analytics key, and telemetry is off only by
  environment.

M3 (no non-local connection) is measured against the signed release.

## 7. How seats reach it

For each bound seat, marvel creates an agent-vault agent with the proxy role.
That role adds keys to requests and cannot read a value back. The seat's
environment gets:

- `HTTPS_PROXY=http://<token>@127.0.0.1:<port>`;
- `NODE_EXTRA_CA_CERTS` and `SSL_CERT_FILE` pointing at the CA certificate,
  a public file in marvel's run directory;
- placeholder values for the keys the seat used to hold;
- `NO_PROXY` for the harness's own model and login hosts, so the harness
  authenticates as it does today and only the seat's tool traffic goes
  through the proxy.

The CA never enters the System keychain. A seat can unset the proxy;
enforcing it is the agent sandbox tool's job, not this design's.

**The seat token is a scoped session of up to 7 days.** A pane's environment
cannot be refreshed, so a seat that outlives its session loses egress
mid-run. So:

- marvel records each seat's session expiry beside the binding;
- it raises an event a set time before expiry, so the seat's supervisor can
  shift or restart it. The lead time is a class config value; this design
  proposes 12 hours, open in review;
- a seat whose session has expired gets a proxy 407. That blocks the request;
  it does not leak anything.

**Seat end:** marvel deletes the seat's agent, and its token returns 407
from then on (M5).

**Kill switch:** stop the service. Seats' proxy requests then fail, which
blocks rather than leaks.

**Not loaded in phase 1:** OAuth refresh tokens, harness subscription tokens
included. Static API keys only.

## 8. What marvel must never do

- Write any key, or the means to decrypt one, to a marvel-owned file, store
  bucket, log line or `api.Credential` entry. `CredentialKind` stays a closed
  set (`internal/api/types.go:1032-1046`); no kind is added for these.
- Write to the keychain.
- Put a secret in argv, in a child environment or in a log. The child's
  stdout and stderr go to `LogPath` (`process.go:43-44`), so M1 checks that
  log too.
- Call agent-vault's CLI or its `run` command.

## 9. ADR-009 tripwires

Each re-opens ADR-009:

- moving agent-vault's master password out of the operator's keychain;
- adding any marvel write path to that keychain;
- moving agent-vault's home onto storage outside marvel's run directory or
  off the host.

## 10. Order of work

**Step 1. Scratch trial, before any live seat.** Run under the fleet's four
run conditions for commands that write files: a scratch `HOME` with a short
`TMPDIR`; no `MARVEL_SOCKET`; a sandbox profile that allows localhost only,
shown by a refused connection to a non-local address with the profile's own
error; and proof the real marvel home was not written (on a host with a live
daemon, the sandbox write-deny proof). Material: a throwaway keychain inside
scratch with a throwaway password and a canary value, never a real item; the
pinned signed release; a local HTTPS fake upstream that records the headers
it receives. `security create-keychain` may add the file to the user's
keychain search list, which is a write outside scratch (not verified): compare
the list of keychain files, not items, before and after, and restore it.

Measured, each with a positive control:

- **M1 Leak canary.** The canary must not appear in the child's argv, its
  environment, its log, marvel's log or `~/.marvel`. Control: the canary
  planted in a decoy file is found by the same search.
- **M2 Injection and deny.** The fake upstream receives the added header for
  a matched host, and an unmatched host is refused. Control: under
  passthrough the same request is forwarded.
- **M3 Telemetry.** No non-local connection attempt in the sandbox log.
  Control: the refused-connection probe appears there.
- **M4 Owner race.** marvel is the only owner, and a second registration does
  not get owner. The time from start to registration is recorded.
- **M5 Revocation.** After a seat's agent is deleted, its token returns 407.
- **M6 Restart.** Kill the child. It restarts, old tokens stay valid, and the
  database cannot be opened without the keychain item.
- **M7 Keychain read per launch context.** A login terminal, a detached tmux
  server and launchd; and a locked keychain, where the service refuses with
  one event.
- **M8 Harness unaffected.** A seat with `NO_PROXY` set still authenticates
  and answers.
- **M9 Environment residual.** A scratch child started with a value in its
  environment, then unset: does `ps -E` still show it?
- Also: whether `security` accepts a trailing `-w` from a pipe, and whether a
  marvel upgrade triggers a keychain prompt.

**Step 2. The operator's keychain setup**, section 4, once step 1 passes.

**Step 3. marvel PRs, as one chain** (each based on the one before, merged
in order):

1. this design doc;
2. `ProcessSpec.Stdin`: bytes written once, then closed;
3. `internal/keystore` (darwin): reads one named item into a buffer the
   caller zeroes; no write, no list; logs names only; refuses cleanly when
   the keychain is locked or the item is missing;
4. the agent-vault driver and the class and provider registration: install
   check of the signed release, start steps 1 to 5, health, `Delivers:
   [api-key]`, the telemetry constant, check and warning;
5. the Binding: per-seat agent create, seat environment, session expiry
   record and event, revoke at seat end.

**Step 4. First live seat:** a builder seat with a provider API key, per the
ruling. Use the lowest-scope key the vendor offers, with a spend limit at the
vendor where one exists, and only after M1 passes. Watch it for a working
day, then add one seat at a time.

## 11. Risks measurement does not remove

- agent-vault's maintenance is thin: pin the release and read each upgrade's
  changes.
- Whoever holds the proxy's CA key can read seat TLS.
- A token's scope is a whole vault.
- Any process running as the operator can try the keychain while it is
  unlocked. The item access list narrows that, and the lock closes it.
