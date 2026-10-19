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
K=~/Library/Keychains/marvel-agent-vault.keychain-db
security create-keychain -P "$K"
security set-keychain-settings -l -u -t <seconds> "$K"
security add-generic-password -s marvel.agent-vault -a master-password \
  -T "" -T <marvel> -U "$K" -w
security add-generic-password -s marvel.agent-vault -a owner \
  -T "" -T <marvel> -U "$K" -w
security add-generic-password -s marvel.agent-vault -a <credential-name> \
  -T "" -T <marvel> -U "$K" -w
security set-generic-password-partition-list -s marvel.agent-vault \
  -a <account> -S <partition-list> "$K"     # once per item; never -k (see below)
security dump-keychain -a "$K"              # access lists only; never -d, which prints data
```

What each part is for:

- **`-P` and a trailing `-w`** make `security` prompt, so no value reaches
  argv or shell history. Never `-A` (it trusts every program).
- **`-T ""` before `-T <marvel>`.** By default the program that creates an
  item, here `/usr/bin/security`, is trusted to read it without a prompt;
  `-T ""` removes that default (`security(1)`, under
  `add-generic-password`). Without it any process running as the operator
  could read the items through `security` while the keychain is unlocked,
  and ruling 2 would not hold.
- **The partition list** is a second part of each item's access control
  that limits access by the reading program's code signature
  (`set-generic-password-partition-list`). Changing it needs the keychain
  password (`security(1)`). Never pass it with `-k`: that puts the keychain
  password in argv, and the man page marks the flag deprecated.
  `<partition-list>` is the value that admits marvel's signature and nothing
  else. Which value that is depends on how the marvel release is signed, so
  step 1 reads it from the scratch items, and the setup fills it in from
  there. The only command that reads it is `security dump-keychain -a`,
  which prints access lists and no data. `find-generic-password -g` or `-w`
  and `dump-keychain -d` print the secret, and are never used to read the
  value.
- **`dump-keychain -a`** prints each item's access list, never its data. The
  setup ends by reading it: each item must list marvel and only marvel, with
  the partition list just set. Anything else is a STOP, and the items are
  fixed before marvel starts the service.

Not verified, so step 1 runs these exact lines against the scratch keychain
first:
- whether `security` still reads options after the keychain path, which is
  a positional argument; if it stops there, the trailing `-w` is lost, and
  the setup names the keychain another way;
- how to get a random master password and owner credential into the prompt
  without the clipboard (whether a trailing `-w` reads from a pipe);
- whether `set-generic-password-partition-list` prompts for the keychain
  password when `-k` is left out. The man page says only that the password
  must be presented. If it does not prompt, the setup stops at that line,
  no real item is created, and the step is redesigned before step 2 (never
  by adding `-k`).

**After a marvel upgrade, re-trust.** `<marvel>` is the resolved binary
(not the brew link), so its path and its signature change with each
release, and the trusted entry names a binary that is gone. marvel then
cannot read the items, and the service refuses to start with the
locked-or-unreadable event (section 5). The re-trust step, by the operator,
from a login terminal on that host:

1. `marvel service check egress-credential-front` reads each item once,
   with the new binary, and discards the values. It writes nothing. The
   keychain prompts for each item; the operator answers **Always Allow**
   with the keychain password, which adds the new binary to that item's
   access list.
2. `security set-generic-password-partition-list ...` for each item, as in
   the setup, with the new release's value.
3. `security dump-keychain -i "$K"` (interactive access-list editing):
   remove each item's entry for the old binary's path.
4. `security dump-keychain -a "$K"`: each item lists the new marvel only,
   with the new partition list. Anything else is a STOP.

The old entry is removed, not left. An empty path is not what keeps an old
entry harmless: the Homebrew prefix is writable by the operator's own user,
so a binary can appear at an old path. What guards the item is the code
signature check in its access list and partition list, and removing the
entry keeps that check the only way in.

That Always Allow adds the new binary, and that the prompt appears from a
login terminal, are inferred, and step 1 measures both (M10). If they fail,
the fallback is to re-add each item with `-U` and its value at the prompt.
That needs the value, so it suits the random items only after a rotation
(section 5, recovery).

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
   - **The check fails** (the registration was refused, or the owner is not
     the one marvel registered): marvel stops agent-vault, emits
     `service.owner-mismatch` with the service name, binds no seat and loads
     no key. It does not retry; the service stays failed until the operator
     acts. Because step 4 runs only after this check, a database with a
     wrong owner holds no provider key.
   - **A later start cannot log in** (the `owner` item was lost, replaced
     or changed): marvel stops agent-vault, emits
     `service.owner-login-failed`, and does not retry. Seats already bound
     keep their tokens until the service stops; no new seat is bound.
4. For each provider item named in the class config, marvel reads it into a
   buffer, loads or updates it through agent-vault's credentials API, and
   zeroes the buffer. It loads the host-to-credential entries, sets the
   unmatched-host policy to deny (agent-vault's default is passthrough), and
   reads the policy back.
5. Telemetry off, section 6.

A locked keychain, a missing item, or an item marvel may not read (after
an upgrade, section 4) makes the start refuse with one
`service.keychain-unreadable` event naming the item, and no retry loop (M7).

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

**Recovery from a wrong owner** (`service.owner-mismatch`). The database
holds no provider key (start step 4 never ran), so nothing has to be
rotated:
1. find what else registered: another local process reached agent-vault's
   port before marvel did, and it is the thing to remove;
2. delete agent-vault's home directory;
3. start the service; it registers the owner again.

**Recovery from a lost or replaced owner** (`service.owner-login-failed`).
The database is still ciphertext under the master password, so losing the
`owner` item alone exposes nothing:
1. stop the service;
2. write a new random `owner` item with the setup's three lines for
   `owner`: the `add-generic-password` line (with `-T ""`, `-T <marvel>`
   and `-U`), its `set-generic-password-partition-list` line, and the
   `dump-keychain -a` check, which must show marvel alone;
3. delete agent-vault's home directory, since its owner can no longer be
   logged in as;
4. start the service; it registers the new owner and loads the keys again.
   Every old seat token is gone, so every bound seat restarts with a new
   one.
If the old `owner` value may have leaked (rather than been lost), this is
the compromised-database recovery above instead, provider keys first.

**Failed states are visible without the ring.** The ring is in memory and
starts empty with each daemon (section 7), so an event alone is not the
record. `service.owner-mismatch`, `service.owner-login-failed` and
`service.keychain-unreadable` each leave the service in state `failed`,
held in the service's store record, with the reason and the time.
`marvel get services` prints the state, and `marvel describe service`
prints the reason, the time and the recovery to follow. It stays failed
until the operator acts; nothing retries it.

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
- the service views show a warning line for this service (marvel has no
  service view today; build item 4 adds `marvel get services` and
  `marvel describe service`):
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
- `NO_PROXY` set from the class config key `no_proxy_hosts`, one list per
  harness. Starting values, which M8 confirms before any live seat: claude
  `api.anthropic.com,claude.ai,console.anthropic.com`; codex
  `api.openai.com,auth.openai.com`. These are the harness's own model and
  login hosts, so the harness
  authenticates as it does today and only the seat's tool traffic goes
  through the proxy.

The CA never enters the System keychain. A seat can unset the proxy;
enforcing it is the agent sandbox tool's job, not this design's.

**The seat token is a scoped session of up to 7 days.** A pane's environment
cannot be refreshed, so a seat that outlives its session loses egress
mid-run. So:

- marvel records each seat's session expiry in the binding's store record,
  so it survives a daemon restart or reexec. That record is the durable
  signal. `marvel describe session` prints the expiry, and `marvel get
  sessions` marks a seat inside the lead with `(egress expiring)` beside its
  health, and an expired one with `(egress expired)`;
- the lead is the class config key `session_expiry_lead`, default 24 hours.
  It must be shorter than the session length (`session_length`, at most 7
  days). A lead equal to or longer than the session would mark every seat
  as expiring from spawn, so the config is refused at load with a message
  naming both values;
- marvel also emits `egress.session-expiring` on entering the lead and
  `egress.session-expired` at expiry, naming the workspace, team, role,
  session and expiry time. These are notices only. On main the ring is the
  only emitter besides a discard (`internal/events/events.go`, `Ring`), an
  in-memory buffer of 2000 events built fresh at each daemon start
  (`:639`; `internal/daemon/daemon.go:328`). So an event can be evicted
  before a once-a-day reader sees it, or lost on a restart. No push to
  supervisors exists;
- so a supervisor learns of an expiring seat by polling: `marvel get
  sessions` in its own sweep, where the mark shows until the seat is
  shifted. A push (a bus message to the team's supervisor) is a later item,
  not this design's, and the mark stays the record when it comes;
- marvel does not shift or restart the seat itself; that stays the
  supervisor's call, as for max age. If nobody acts, the seat's next
  proxied request after expiry gets a 407. That blocks the request; it does
  not leak anything, and the seat's own error says why.

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

Each re-opens ADR-009. They are review rules: the reviewer of any change to
the keystore, the driver or the service record checks them. One is also a
test (below).

- moving agent-vault's master password out of the operator's keychain;
- adding any marvel write path to that keychain;
- moving agent-vault's home onto storage outside marvel's run directory or
  off the host;
- moving a managed service out of marvel's supervision, the tripwire
  `internal/service/service.go:34-35` already records.

The keychain one is backed by a test in build item 3: it fails if any
non-test marvel package calls a keychain write (`SecItemAdd`,
`SecItemUpdate`, `SecItemDelete`, or `security` with `add-`, `delete-` or
`set-`). It is a source scan, so it is a review aid, not a guarantee: a call
built from parts or wrapped under another name passes it. The review rule
above is what holds.

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
  Every seat's proxy token is in its environment by design
  (`HTTPS_PROXY`), so `ps -E` run as the operator reads all of them, and
  with them any process running as the operator can use every seat's
  egress, not one seat's. That is accepted, for two reasons: a token is
  issuance marvel can revoke (section 7, seat end), not a provider key, so
  no key leaves agent-vault and the amended ADR-009 holds; and it is the
  same-user residual that section 11 names for the keychain, applied to the
  tokens, which the agent sandbox tool is meant to close.
- **M10 Keychain trust.** After the setup lines, `dump-keychain -a` lists
  only marvel on each item, and `security find-generic-password` from a
  plain terminal is prompted, not answered. Then, with a second build of
  marvel standing in for an upgrade: the old trust no longer reads; the
  re-trust step (section 4) makes the new build read without a prompt; and
  the partition list value that admits marvel's signature is recorded.
- Also: whether `security` accepts a trailing `-w` from a pipe, and whether a
  marvel upgrade triggers a keychain prompt.

**Step 2. The operator's keychain setup**, section 4, once step 1 passes.

**Step 3. marvel PRs, as one chain** (each based on the one before, merged
in order):

1. this design doc;
2. `ProcessSpec.Stdin`: bytes written once, then closed;
3. `internal/keystore` (darwin), with the no-keychain-write test (section 9): reads one named item into a buffer the
   caller zeroes; no write, no list; logs names only; refuses cleanly when
   the keychain is locked or the item is missing;
4. the agent-vault driver and the class and provider registration: install
   check of the signed release, start steps 1 to 5 with the owner check and
   its two events, health, `Delivers: [api-key]`, the telemetry constant,
   check and warning, `service.keychain-unreadable`, the `failed` state with
   reason and time in the service's store record, `marvel get services`,
   `marvel describe service`, and `marvel service check` for the re-trust
   step (reads only);
5. the Binding: per-seat agent create, seat environment with
   `no_proxy_hosts`, the session expiry in the binding's store record, the
   `(egress expiring)` and `(egress expired)` marks in `get sessions` and
   the expiry in `describe session`, the refusal of a lead not shorter than
   `session_length`, the two notice events, and revoke at seat end.

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
