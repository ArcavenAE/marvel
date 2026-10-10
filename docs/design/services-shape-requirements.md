# The shape of marvel services: candidate requirements

- **Status:** Proposal, 2026-10-09. The operator ruled every section 6
  decision on 2026-10-09 (section 6, "Operator rulings"). The requirements
  themselves stay provisional until a ticket takes each one.
- **Author seat:** architect, team arcaven, chairing an eight-round,
  ten-seat panel on the operator's request relayed by director.
- **Code read at:** marvel `7f1c81e`. Between it and `b2fa4ff`, the only
  cited file that changed is `team-load-rollup.md`, and its cited lines were
  re-read at `b2fa4ff`.
- **Builds on:** `services-list.md`, `bus-as-service.md`,
  `adopted-services-2026-09-16.md`, `wake-service.md`,
  `bus-hub-hot-reload.md`, `team-load-rollup.md`, and the speculative
  `service-provider/` set. Supersedes none of them.
- **Evidence:** the panel's record (commission, a brief and the chair's
  checks per round, replies by seat, the R8 ballot and the outcome) is a
  gitignored party record in a private repository; the tallies and
  reasons that matter are carried in section 6. The code-seams study is
  finding-marvel-4m2m (#795, merged as `d9ef67a`).

## 0. Why

The registry in `internal/service` can already describe any class, but
everything after spawn is still bus-specific. Stop, status, restart and
reload are reachable only through `d.busSup`, and `attachServices` has one
provider arm. A second managed service written today would copy the bus
supervisor (4m2m:103). These requirements state what every service
declares and what marvel may and may not do to it, so that the second
service is an instance of a contract rather than a copy. The panel also
found a defect (#794) along the way, and confirmed the leaf-seed drop on
reexec (#339, open) is still current.

How to read an item:
- Each item names its consumer and a failing case a reviewer can run.
- Source classes, per clause where they differ:
  - CODE: checked by one command at `7f1c81e`;
  - DESIGN: merged design text;
  - RULED: a written ruling with an author;
  - PARTY: a panel agreement;
  - STUDY: 4m2m;
  - GITHUB: issue or PR state.
- DEFERRED-SEAM: no code seam exists to assert the item against yet.
- DEPENDS-ON: waits on that ticket.
- DECISION: waits on section 6.

## 1. Shape

**MS-0 marvel requires no service; an enhanced service works without
marvel.**
- A cluster with no Services entry starts, and its sessions run.
- Every marvel extension of a service degrades to the plain service: with
  marvel absent the service works, and with the extension down it works.
- A declared managed service that cannot start is a start failure, not a
  silent skip.

Consumer: an operator with no bus; the bd seats.
Fails if the daemon refuses to start for lack of a service, or if a bd
write path requires a marvel call.
CODE `daemon.go:3326-3328`; RULED SOUL section 2, ADR-005.

**MS-1 One record, structural validation.**
- Every operator-declared service is a Services entry carrying class,
  provider, mode and caller_identity, plus a protocol and transport where
  the class's callers need one (for MCP: stdio or HTTP).
- In-process capabilities are registered in code and shown in the same
  read-only catalog view, not declared in config (panel recommendation,
  section 6 V1). The event ring is not a catalog entry.
- A malformed entry is refused at config load, naming the field. No health,
  throughput or age reading enters validation.

Consumer: the operator writing config.
Fails if a validation path reads a health or age value; if a class,
provider or mode violation is accepted; or if a capability appears in two
lists or in none.
CODE `config.go:384-404`, `service.go:47-53`; DESIGN
`services-list.md:89-91`. Cardinality today is one bus per cluster (CODE
`daemon.go:3344`).

**MS-1a A consumer contract per class.** A class is registered only with a
written consumer contract: what a caller relies on (a trigger and an
observable result), its caller_identity set, and its pass condition.
Consumer: the driver reviewer and the seat author.
Fails if `classes` gains an entry with no contract text.
CODE `service.go:108-114`; DESIGN `bus-as-service.md:77-80` (the bus
contract is unwritten and is director's). DEFERRED-SEAM.

**MS-2 Owner relations.**
- Each entry has one owner relation: managed, adopted, external, or
  provided in-process.
- "marvel's own child, re-attached across a reexec" is derived from mode
  plus how the child was obtained, and is shown apart from mode `adopted`.
- It may be signalled only after the MS-9 proof.

Consumer: the operator.
Fails if the two render the same, or if either is signalled unproven.
CODE `supervisor.go:243-252`. DEFERRED-SEAM: the status field.

**MS-3 (retired).** The panel folded it into MS-1: in-process capabilities
are registered in code and shown in the catalog view (section 6 V1). The
number is kept so later items keep their ids.

**MS-4 The hub stays a field of the bus record** (panel recommendation,
section 6 V2), until something other than the bus consumes it.
Consumer: the operator.
Fails if the hub is both a record and a bus field.
DESIGN `services-list.md:399-401`. `bus-as-service.md:98` says otherwise,
and whichever way V2 is ruled, one of the two texts is corrected.

**MS-5 No foreign credential field, enforced by decode.**
- The record has no field that can carry a third-party credential.
- Each provider's body decoder refuses any key it does not name, with a test
  per driver.
- The informational field V3 (b) adds carries no value that can be a
  token, key or refresh value.

Consumer: the operator, judging what a compromised daemon host exposes.
Fails if an entry with an undeclared key loads or round-trips through
`Save`.
CODE `service.go:30-45`; CODE `config.go:236-240, 304-308` (the provider
body is kept and round-tripped; a strict decode is not shown); RULED
ADR-009. DEFERRED-SEAM: strict decode.

## 2. Lifecycle

**MS-6 Every verb declared.** For each (provider, mode), the driver declares
every verb:
- declare, catalog entry, directory, discover, health reading, version;
- reload: which changes it takes by signal, which by restart, and which
  need a daemon reexec;
- restart: its policy, as a backoff curve, a cap, or "unbounded";
- stop: its grace;
- state ownership.

The class names which verbs must be declared. "n/a" is an answer and
carries a reason code: not applicable by owner relation; not observable by
marvel; not implemented; lost on restart (only where no persisted store
exists, otherwise "restored", with its source). Status shows the config
generation the daemon holds.

Consumer: the operator and director reading the catalog.
Fails if a verb is absent, an "n/a" has no reason code, or a stale config
reads as a wrong one.
CODE `service.go:90-100` (`ProviderDriver` carries `Modes`),
`daemon.go:3603` (reload by restart, the precedent), `supervisor.go:450-483`
(restart is unbounded and undeclared today). DEFERRED-SEAM.

**MS-7 No lifecycle verb on what marvel did not start.** For mode `adopted`
or `external`, marvel never:
- starts, stops, restarts or reloads it, or renders its config;
- edits its route table, or upgrades it;
- mints or reads a credential its owner issues, or any vendor grant;
- signals a pid it has not proven (MS-9).

Two things are not lifecycle verbs:
- a read-only reachability observation (connect, or GET without a
  credential);
- distributing, to the managed child that needs it, a credential the
  service's own operator minted, where declared (the hub leaf seed;
  allowed, V5).

Consumer: the owner of that service.
Fails if a recording lifecycle fake bound to an adopted or external entry
sees any call through a full start, reexec and stop.
DESIGN `adopted-services-2026-09-16.md` 1.3 and 1.6, `bus-as-service.md:112-115`.

**MS-7a No spawn waits on an adopted entry's probe.** A down or unbound probe
is a reading. It never refuses or delays a session start.
Consumer: the operator.
Fails if a down probe holds a spawn.
DESIGN `adopted-services-2026-09-16.md` 1.3; RULED ADR-007.

**MS-8 The extraction gate.** A second managed provider merges only when:

- (a) The daemon reaches stop, status, restart and reload through one
  lifecycle interface. It is instantiated per entry and implemented by the
  provider's driver. Its signatures take plain types, because
  `internal/service` imports nothing and `config` imports `service`. No
  `d.busSup` method call is left (`daemon.go:847, 3452, 3556, 3604, 3611,
  3649` at `b2fa4ff`; the last two are in `leafSeedNeedsRestart`).
  DEFERRED-SEAM.
- (b) The bus supervisor is one implementation, and every assertion in its
  ten tests exists by test name after the move. Whether those tests may be
  edited is DEPENDS-ON aae-orc-oo62t (STUDY 4m2m:102, :224). They reach
  `s.backoff`, `s.logPath`, `s.mgr` and `s.Env`.
- (c) `attachServices` dispatches through a provider map in
  `internal/daemon`. A test checks that every registered provider has an
  arm, and the loud `default` stays. An adopted provider gets a no-op arm.
  DEFERRED-SEAM: an exported provider list.
- (d) A fake-provider test drives every verb without importing
  `internal/bus`. DEFERRED-SEAM.
- (e) Spawn stays only through `workload.Start`, with 0 `os.Environ()` hits
  in `internal/bus` and `internal/workload`. CODE, checkable now.
- (f) MS-13, MS-14 and MS-17 hold for the new driver.
- (g) `computeBackoff` has one definition. CODE: two today,
  `bus/supervisor.go:190` and `team/controller.go:183`.
- (h) Each verb returns an as-of cell (MS-18), and status is served through
  the interface. Moving `*bus.Status` out of the RPC type is a wire change
  (CODE `daemon/status.go:46`, `cmd/marvel/header.go:163`).
- (i) A managed `inference-gateway` or `model-endpoint` driver also waits
  on the aae-orc-w2bmo review (DESIGN `services-list.md` section 3.5).

The generic loop lives in `workload.Process`. The interface and status type
live in `internal/service` (PARTY; STUDY 4m2m:208).
Consumer: the reviewer of a driver PR.
Fails on any one clause.
Order: the interface lands first. The #794 fix is the one change allowed
ahead of it.

**MS-9 The pidfile proves the child.**
- Before adopting a process, deciding it has exited, or sending it any
  signal, marvel proves the pid is the child it started, by the start time
  and executable recorded at spawn.
- Every signal goes through one injectable function that takes a proven
  child.
- A mismatch neither adopts nor signals. It produces one reading and one
  event, and the operator sees "port held, child unproven", not a crash
  loop.
- The legacy case (a number-only pidfile at the first upgrade) is ruled
  section 6 V11 (b): neither adopt nor signal; report "port held, child
  unproven". It ships in the same PR as the proof.
- The daemon's own guard (`checkPidFileFree`, `daemon.go:890-904`) gets the
  same fix as its own item.

Consumer: the operator after a reexec or reboot.
Fails if, with the pidfile naming a live unrelated process, the injected
signal function is called, or if a reused pid makes a dead broker read
alive.
CODE `supervisor.go:243-261, 417, 825-839` (signal 0 is the only proof; the
signals are direct `syscall.Kill` calls); `process.go:88-92` (number only,
0644, directory 0700); GITHUB #794. DEFERRED-SEAM: the injectable signal
function.

**MS-10 A stateful managed class declares its state duties.** A class whose
data dir is the product declares:
- a graceful stop with its own grace, after which it reports "stop
  incomplete" and does not kill the group;
- what a kill may lose, or "unknown, probe pending";
- no kill on pid evidence alone;
- a pre-change backup hook, or "backup: none" naming who holds the only
  copy;
- a schema pin;
- whether downgrade after a migration is possible;
- what `keep` holds (a data-dir lock).

Its credentials pass MS-17.
Consumer: the operator running the service.
Fails if a stateful driver ships without any one duty.
CODE `supervisor.go:517-527` (the group kill after one grace). DEFERRED-SEAM:
no driver. For bd, whether a dolt kill drops uncommitted working-set rows
is unchecked; no managed bd claim ships before a driver probe answers it.

**MS-11 Version readings.**
- An entry carries the provider binary version, read at each spawn and on
  demand from disk, as as-of cells.
- It also carries the protocol version and the class-contract version, plus
  a schema version for a stateful class.
- A binary or protocol mismatch is a reading (running against on-disk or
  declared), and never refuses a start (section 6 V4). For an adopted or
  external service it is never a refusal at all.
- A class-contract mismatch is structural.
- Where marvel cannot see a version (an adopted gateway), the cell says
  "not observable by marvel".

Consumer: the operator reading status.
Fails if status prints one version string when running and on-disk differ.
CODE `supervisor.go:200-214` (resolved and read once, compared nowhere).
The class-contract check is DEPENDS-ON aae-orc-chwhk: `config.go:254-257`
says it lands there, and no code performs it yet. DEFERRED-SEAM: protocol
and contract versions.

**MS-12 A Binding is authorization with one lifecycle edge.**
- A Binding is declared as a field on Workspace, Team or Role. It resolves
  downward, the narrowest scope wins, and a scope may withhold.
- It refers to a catalog record by name. A directory serves records only,
  never a seat, scope or identity.
- It has two visible states, delivered and withdrawn, keyed by seat and
  scope. It is never stored in the transient credential map, and it has no
  health, version or stop of its own.
- A withdrawal that had delivered a minted credential runs that
  credential's revoking verb, or says "not revoked".
- Status shows whether the front enforces the withdrawal ("withdrawn, not
  enforced" when the front checks no marvel token, or a running seat keeps
  delivered config).

Consumer: the operator and the audit reader.
Fails if a withdrawal does not appear as a status line and an audit row
within one second, if enforcement is not shown, or if a directory response
carries a seat or identity.
CODE `api/types.go:136, 602, 894` (the carriers); STUDY 4m2m:49 (no new
import edge). DEFERRED-SEAM: no Binding type exists.

## 3. Custody and identity

**MS-13 Deliver only what is declared.** What marvel delivers, by mode:
- adopted entry: the URL and the CA path only;
- external entry: the same, plus a credential marvel mints where the class's
  caller_identity is one marvel mints and the driver `Delivers` it;
- managed entry: also what marvel minted for it.

marvel may also deliver:
- an operator-declared launch command (command and args), naming
  environment variables but never their values;
- the declared leaf seed, to the managed broker child only, never to a seat
  and never into a log (allowed, V5).

A value the operator placed in the environment the daemon inherits, passed
through unread, is not marvel delivering it.

Consumer: the operator and the driver reviewer.
Fails if:
- (a) the config accepts a credential-bearing key;
- (b) a marvel-written file, API response, event or log carries a foreign
  value, or marvel reads or persists one;
- (c) a driver's allowlist names anything outside what MS-14 declares;
- (d) a delivered entry carries an environment value.

Today (b) is partly tested: `workload/process_test.go:33` covers a
daemon-held canary, and `api/credential_test.go:122`. (a) and (c) have no
test.
CODE `service.go:37-40`, `runtime/codex_home.go:36-45, 139`,
`api/backend.go:96-100`; RULED ADR-009.

**MS-14 Every secret declares its revoke and its reexec fate.**
- A secret marvel mints declares its revoking verb and whether it survives
  a reexec, with a test for each "yes". It is refused if it has no revoking
  verb.
- A secret its operator minted, which marvel only distributes, declares who
  revokes it.
- Loss on restart uses the MS-6 reason code.
- A registry test iterates every driver's declared secrets.

Consumer: the driver reviewer, and the operator after a reexec.
Fails if any secret lacks its declarations.
Today:
- passwords are recovered across a reexec (CODE `manager.go:181-221`);
- stored values are lost on restart (CODE `credential_test.go:161`);
- re-mint after restart has no test;
- the leaf seed is dropped (DESIGN `admin-guide.md:483-485`,
  `bus-hub-hot-reload.md:18`); the drop is current and tracked in #339
  (GITHUB, open).

DEFERRED-SEAM.

**MS-14a Mode change is checked.** Moving a managed service to adopted or
external while marvel holds its signing key is refused unless the ADR-009
tripwire check passes.
Consumer: the operator.
Fails if a mode change is accepted with no custody check.
RULED ADR-009 (the tripwire, noted at `service.go:30-36`). No check exists.

**MS-14b A credential kind needs its consumer.** A kind is admitted only
with a cited use and its spawn-time consumer.
Consumer: the driver reviewer.
Fails if a credential name is stored and read by nothing (GITHUB #344).

**MS-15 Nothing durable for an OAuth service.** marvel holds no refresh
token or other vendor grant. It holds nothing, or a short-lived token from
a vault it does not run. The record is a separate informational field
(section 6 V3 (b)), not a `caller_identity` value.
Consumer: the operator.
Fails if the record schema, the store types or a harness seed struct gains
a field typed as a vendor grant, or if the closed `CredentialKind` set
(`api/types.go:1009-1019`; test `credential_test.go:137`) gains a
vendor-grant member.
RULED ADR-009 and SOUL section 3. The vault case rests on a party vote of
the ADR-009 revisit, pending aae-orc-p4fzv.

**MS-16 Identity is per seat.**
- A caller's identity claim names the seat. A token may be minted per
  instance for revocation.
- Self-asserted strings such as `BEADS_ACTOR` are attribution, never
  identity.
- A front that checks no marvel token says "no audit available", never an
  empty audit.

Consumer: the audit reader.
Fails if an attribution string derives from an instance id, or if an audit
row keys on one.
Today `BEADS_ACTOR` is built from the session name (CODE
`runtime/adapter.go:422`); whether that name is stable across a respawn is
unchecked. DEFERRED-SEAM: an audit record.

**MS-17 Secret canary.** A canary set in the daemon environment appears in
none of these:
- a child's environment;
- the daemon log;
- the lines marvel appends to a child log;
- rendered config.

Consumer: the driver author.
Fails if the canary is found in any of them.
CODE `workload/process_test.go:33` covers the environment half; the rest
is new.

## 4. Readings, absence, failure and metering

**MS-18 One reading shape.**
- Every service reading is an as-of cell (value, observed_at, valid_until,
  source).
- A measured zero is legal. An unmeasured value prints `-`. A restored
  value is never printed as fresh.
- A spawn-time reading is a cell whose valid_until is the spawn.
- Every emitted kind names its consumer (or "display only") and its wire.
- otel is an optional forwarder.

Consumer: the operator, director and supervisors.
Fails if a reading uses zero for absence, or prints a restored or
spawn-time value as current.
CODE `asof.go:41-63`.

**MS-19 Liveness, inform only.** There are two readings: the daemon's, and
each managed child's from its `workload.Process` loop.
- The daemon reading comes from the supervising loop: the
  `Controller.Run` tick after `ReconcileOnce`, by injected callback. It
  carries the bus supervisor's state.
- Each reading carries a sequence number, a generation id, its declared
  interval, and counts of minted values by state (never a value or hash),
  as an as-of cell.

Rules for the observer:
- a new generation is a restart, never a gap;
- a sequence regression reports a replay; signing, not the sequence,
  catches a forgery;
- absence prints "no reading since T", never "down" and never the last
  value;
- the absence threshold is a stated multiple of the interval;
- the observer does not ride the broker it watches. A second path is
  required: a probe of the daemon socket, and a pidfile probe that reads
  the MS-9 identity, never signal 0 alone.

Alarms inform and gate nothing. Adopted entries are not polled unless an
opt-in is ratified.
Consumer: an observer off-host or on the global tier.
Fails if a stuck reconcile loop keeps the reading fresh, if a reexec reads
as a gap, or if a hung broker under a live loop reads healthy.
CODE `team/controller.go:2549-2556`; DESIGN `wake-service.md:55` (nothing
publishes to NATS). DEFERRED-SEAM: the publisher.

**MS-20 Not reporting is said aloud.**
- When marvel cannot observe a service, or a reading is past its
  valid_until, status says "not reporting since <time>" and never shows the
  last value as current.
- A Binding whose delivery was not recorded reads "not reporting".
- Where a purged record and a never-seen one read alike, the header names
  the clusters that were expected.

Consumer: the operator, and director on the operator's behalf.
Fails if, with the daemon stopped, a value prints with no age, or if
missing and healthy render the same.
DESIGN `team-load-rollup.md:56-67`.

**MS-21 A skipped list is visible.**
- At each of the five skip sites in `attachServices` (`daemon.go:3307,
  3318, 3323, 3330, 3335`), the skip produces one operator-visible reading
  and one event per failed load, not one per tick.
- The cluster still starts (section 6 V7). A cluster with no entry is not a
  skip.
- A class is registered before any doc tells an operator to declare it.
  Today an unregistered class invalidates the whole list, bus included.

Consumer: the operator.
Fails if a declared list ends with no managed service and no reading.
CODE `daemon.go:3316-3332` (logged and skipped today, against
`services-list.md:89-91`).

**MS-21a A managed bus's structural health.**
- A managed bus reports provisioned, authorized and tls on its tick.
- An adopted or external bus declares "unknown", never up.
- When a declared object is missing on a managed bus: new spawns hold, one
  event is emitted per change, running sessions are not marked degraded,
  and nothing restarts.
- This hold applies to the managed bus only. Leaf down is a reading, never
  a hold.

Consumer: the operator.
Fails if a miss restarts the broker or fans out an event per session, or
if leaf down holds anything.
CODE `controller.go:1188-1192`; DESIGN `bus-as-service.md:139-195`,
`bus-hub-hot-reload.md:42`.

**MS-21b A lost store refuses, naming the credential.** A spawn whose
config needs a credential the store no longer holds is refused, naming it.
It never starts with a partial environment.
Consumer: the operator.
Fails if a child starts without a credential its config references.
CODE `supervisor.go:322-330`.

**MS-22 Metering is diagnostic.** marvel meters what it runs:
- team-role restarts (persisted in `role_health`, `api/bolt.go:373-386`);
- the bus supervisor's restarts and leaf-down duration (in memory);
- CPU and memory (`daemon/metrics.go:39-60`);
- token spend.

It counts no asks. No count gates without a ratified definition. A declared
ceiling (admission's team budget) is not a count. bd counts and the gateway
spawn count (aae-orc-r1itg) are diagnostic, and nothing opens on a clock.
Consumer: the operator.
Fails if a count blocks a spawn, admit or merge without a cited ruling, or
if a counter keys on an ask.
CODE `internal/admission/admission.go:61-83`; DESIGN
`team-load-rollup.md:37`; RULED ADR-007.

**MS-22a The rollup's own rules.**
- Every hub-sourced fleet number carries "load keys are unauthenticated"
  until rollup D1 is built.
- With no hub, the view falls back to local.
- A stale claim is made only when every cluster is fresh.
- No value gates.

Consumer: the operator and supervisors.
Fails if a hub number prints without the header.
DESIGN `team-load-rollup.md:65-71`.

**MS-23 What a gateway entry may claim.**
- An inference-gateway entry may claim: its url, class, provider and mode;
  the last spawn probe, with its observed-at, as of spawn and never as
  current health; and the count of live sessions whose base URL matched at
  spawn.
- It never claims which model served, a route list as current, or
  per-request routing.

Consumer: the operator.
Fails if any field asserts the served model.
DESIGN `adopted-services-2026-09-16.md` 1.3; node
`question-router-and-backend-layering`. DEFERRED-SEAM: the class is
unregistered (`config/services_test.go:124`).

## 5. Corrections to existing design text

These are for each file's owner. None is made by this PR.

- `services-list.md:216` cites `supervisor.go:190` for the spawn.
- `services-list.md:317` says `spawnLocked` "no longer exists". It is at
  `supervisor.go:306` and delegates to `workload.Start`.
- `services-list.md:189-192` states a refusal no code performs yet
  (aae-orc-chwhk).
- `services-list.md:361` scores a self-hosted vault "Passes", which a 5-0
  party vote of the ADR-009 revisit reads as custody. Ruled V9 (c), and
  applied in this revision: the token passes, the stored grant is open.
- `bus-as-service.md:98` makes the hub its own record, against
  `services-list.md:399-401` (V2).
- The comment at `supervisor.go:247-248` ("rendered fresh ones") reads
  against `manager.go:181-221` (recovered).

## 6. Decisions the operator holds

Each is the panel's recommendation from the R8 forced vote; the tallies
include every seat. An item is an "open split" when no option reached six
of ten. Each recommendation
is valid until 2026-10-23, and the architect seat re-checks it then, unless
a row names another date.

| id | question | options | panel recommendation | tally |
|---|---|---|---|---|
| V1 | Where are in-process capabilities listed? | (a) one list, `internal` as listing-only; (b) hosted list only, in-process registered in code and shown in one read-only catalog view; (c) a separate list or flag | **(b)** | b 7, a 3 |
| V2 | Is the hub its own record? | (a) a field of the bus; (b) its own record, `mode: external` | **(a)** | a 6, b 3, abstain 1 |
| V3 | How is an OAuth-protected service recorded? marvel holds nothing either way (MS-15) | (a) a fourth `caller_identity` value with nothing delivered; (b) a separate informational field; (c) reserved and refused for now | **open split**; plurality (a) | a 5, b 3, c 2 |
| V4 | May a declared minimum version refuse a managed child's start? | (a) yes, managed only, through a ratified gate; (b) no, a reading only | **(b)** | b 8, a 2 |
| V5 | Where does the hub leaf seed sit on the custody line? | (a) issuance inside the operator's trust domain (the ruled text says the hub operator mints and marvel distributes); (b) the edge: a copy marvel cannot revoke needs its own ADR-009 ruling first | **open split**; read with ADR-009 open item O1 (Amendment 1, tracked in aae-orc-p4fzv) | b 4, a 3, abstain 3 |
| V6 | The presence grant per cluster (aae-orc-93xy1) | (a) scope per cluster once part 2 exists (costs a global hub restart); (b) note the intended breadth now; (c) both | **(c)** | c 8, abstain 2 |
| V7 | An invalid Services list | (a) skip, visible reading and event, cluster starts; (b) drop only the bad entry; (c) refuse the cluster's start | **(a)** | a 7, b 3 |
| V8 | "State since" across a daemon restart | (a) zero; (b) restore all; (c) restore new readings only, wake stays at zero | **(c)** | c 9, abstain 1 |
| V9 | The form of the `services-list.md:361` fix | (a) annotate "contested, see aae-orc-p4fzv"; (b) replace with "open, pending the ruling"; (c) split into token (brokering) and stored grant (custody) | **open split**; plurality (a); read with ADR-009 open item O1 (aae-orc-p4fzv) | a 5, b 4, c 1 |
| V10 | Run the `x-litellm-model-id` curl loop before any requirement speaks of "the backend of this session"? | (a) yes; (b) not needed | **(a)**, valid until 2026-10-14 (the w2bmo review) or the loop is run, whichever comes first | a 6, b 1, abstain 3 |
| V11 | A number-only pidfile at the first upgrade | (a) adopt once if the listener answers and the executable matches; never signal the group; rewrite at once; (b) unproven: neither adopt nor signal; report "port held, child unproven" | **open split**; plurality (b) | b 5, a 4, abstain 1 |

The four open splits, with the deciding consideration each side gave:

- **V3.** Side (a) says a named value with `Delivers` empty is visible and
  checkable. Side (b) says `caller_identity` should keep meaning a proof
  marvel mints or checks. Side (c) says a value with no consumer has no
  failing case yet.
- **V5.** Side (a) relies on the ruled text that hub NKeys are issuance
  inside the operator's trust domain. Side (b) says the SOUL section 3 test
  asks whether marvel can revoke the copy, and it cannot. MS-7 and MS-13
  carried the seed as "pending V5" until the ruling below.
- **V9.** Side (a) says an annotation asserts no verdict before the revisit
  rules. Side (b) says a "Passes" left standing beside an annotation still
  reads as a verdict to a driver author.
- **V11.** Side (a) says (b) costs one manual stop per upgraded cluster.
  Side (b) says (a) adopts on evidence that is not yet the MS-9 proof, and
  that a wrong adopt of a stateful child risks its data.

### Operator rulings (2026-10-09)

Relayed by director from the operator's decision desk; recorded by the
architect seat that chaired the panel.

| id | ruling | operator note, verbatim |
|---|---|---|
| V1, V2, V4, V6, V7, V8, V10 | accepted as the panel recommended | |
| V3 | **(b)** a separate informational field | "point of clarification: marvel should not be the storage for secrets, that does not mean it can't handle, distribute, or coordinate them." |
| V5 | "it's allowed" (no letter given; the recorder maps it to (a), issuance allowed as is) | "it's allowed" |
| V9 | **(c)** split: the token (brokering) passes; the stored grant (custody) is open | "marvel isn't the vault. it's a service orchestrator, it will probably manage a vault, be able to access the vault, provide identifies and help agents access the vault" |
| V11 | **(b)** unproven: neither adopt nor signal; report "port held, child unproven" | "we need improved handling for this, route 3ptdd with casting call to figure out how to handle this more reliably" (it ran as the held-port 3ptdd, closed 2026-10-09) |

Party release ruling: **rework**. The operator's note, verbatim: "four rounds,
if converging two more, if diverging, stop and ask, one card per question".
The recorder reads this as the panel not being released, pending
director's confirmation.

Recorded and not voted here:
- aae-orc-oo62t: may the supervisor tests be edited? This is 4m2m's own
  decision. MS-8(b) depends on it.
- Rollup D1 (signing).
- The managed gateway's master key (the router node's boundary case).
- `question-event-ring-as-signal-bus`.
