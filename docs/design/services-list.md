# The cluster Services list

- **Status:** Design, ratified direction 2026-09-16; implementation not started.
- **Author seat:** architect (fleet, workspace ops2), on the operator's
  ruling relayed by the director seat.
- **Date:** 2026-09-16
- **Extends:** `docs/design/bus-as-service.md` (PR #281, the `Cluster.Bus`
  record and its section 9 plan). First committed slice of the shape
  sketched in `docs/design/service-provider/` (README, 01, 02, 03); that set
  stays speculative and this document supersedes nothing in it.
- **Evidence:** `aae-orc/_bmad-output/party-mode/bus-as-service-2026-09-16/`
  (party J, move 2) and
  `aae-orc/_bmad-output/party-mode/marvel-services-rerun-2026-09-16/{litellm,pair,switchyard}/report.md`
  (the three re-runs whose first moves converged on one generalization).

**What this decides.** Marvel gets one cluster-level list of the services
it runs, adopts, or reaches, each entry carrying `class`, `provider`, `mode`,
and `caller_identity`. The `Cluster.Bus` record designed under
`aae-orc-wzexa` becomes entry one of that list rather than a bespoke field
beside it. `caller_identity` is defined by the class contract, not by the
provider or the instance. Before any second managed instance exists, the one
bespoke supervisor in `internal/bus/supervisor.go` is extracted into a
workload kind whose child receives an allowlisted environment, closing the
inherit-the-daemon-environment hazard at `supervisor.go:196`. The
secret-manager target is an open sub-question; this document lays out the
candidates and does not choose.

---

## 1. The Services list

### 1.1 The record

```go
// internal/config (sketch: the provider body is decoded by the driver from the remaining keys, not by a yaml inline tag)
type Cluster struct {
    Name, Socket, Server, Identity string
    // Bus is sugar: the loader lifts it into Services as the entry named
    // "bus". Declaring both a bus: block and a services: entry of class
    // message-bus is ErrInvalidService.
    Bus      *Bus      `yaml:"bus,omitempty"`
    Services []Service `yaml:"services,omitempty"`
}

type Service struct {
    Name           string   `yaml:"name"`             // unique per cluster
    Class          string   `yaml:"class"`            // message-bus (registered); others refused
    Provider       string   `yaml:"provider"`         // nats-server (registered); others refused
    Mode           string   `yaml:"mode"`             // managed | adopted | external; internal reserved
    CallerIdentity string   `yaml:"caller_identity"`  // from the class contract's set (section 2)
    URL            string   `yaml:"url,omitempty"`    // what consumers receive
    CAFile         string   `yaml:"ca_file,omitempty"`
    Spec           yaml.Node                          // provider body: listen, store_dir, seat, hub for nats-server
}
```

`health` is not a config field. It is a `Status` the driver reports, and
for the bus it is the structural-health contract of `bus-as-service.md`
section 3 (`aae-orc-vy6k7`), unchanged.

```yaml
clusters:
  - name: kinu
    socket: ~/.marvel/run/marvel.sock
    services:
      - name: bus
        class: message-bus
        provider: nats-server
        mode: managed
        caller_identity: api-key          # session-cert once aae-orc-5yqw3 lands
        listen: 127.0.0.1:4222
        url: nats://127.0.0.1:4222
        store_dir: ~/.director/nats
        seat: { workspace: aae-orc, team: ops }
        hub:                              # nested external record, as bus-as-service.md has it
          url: nats-leaf://127.0.0.1:7442
          ca_file: ~/.director/nats-global/ca.pem
```

### 1.2 The four fields

| Field | Meaning | Allowed today | Refusal |
|---|---|---|---|
| `class` | The interface a consumer depends on: coordination pattern plus delivery semantics, never a provider's API (`service-provider/02-architecture.md`, "Capability class, provider, service") | `message-bus`. Names the party reports put on record and this document reserves by doc comment, unregistered: `inference-gateway` (liteLLM), `model-endpoint` (PAIR, Switchyard) | An unregistered class is `ErrInvalidService` |
| `provider` | The driver of a class: what renders, spawns, health-checks, and mints for it | `nats-server` | An unregistered provider, or a provider registered under a different class, is `ErrInvalidService` |
| `mode` | How much of the service marvel runs. Same table as `bus-as-service.md` section 2: `managed` renders, starts, supervises, provisions, mints; `adopted` hands out a URL to something on this host started by someone else; `external` reaches a service elsewhere over a link marvel can observe. `internal` is reserved by comment for marvel-builder's in-process thread | `managed`, `adopted`, `external` | `internal`, or anything else, is `ErrInvalidService` |
| `caller_identity` | What a consumer presents to be recognized by the service. Defined by the class contract (section 2) | Set by the class; for `message-bus`: `api-key`, `session-cert` | A value outside the class set, or one the provider cannot deliver, is `ErrInvalidService` |

Every refusal is structural (a malformed record) and may gate the daemon's
start on that cluster, as `ErrInvalidBus` does today (ADR-007 clause 3). No
health, throughput, or age reading enters validation.

### 1.3 The bus becomes entry one

Nothing in the `wzexa` record moves. Its fields (`class`, `provider`,
`mode`, `listen`, `url`, `store_dir`, `seat`, `hub` with `url` and
`ca_file`) are the nats-server provider's `Spec`, plus the four common
fields above. What changes:

- **`bus:` is sugar.** The loader lifts a `bus:` block into `Services` as
  the entry named `bus`. `internal/config/config.go:185` keeps the field;
  `ValidateBus` (`config.go:219`) becomes the nats-server driver's `Spec`
  validator, called from the Services validator. The `managed` bool keeps
  parsing exactly as `wzexa` specifies (`managed: true` is `mode: managed`;
  `managed: false` with `url` is `mode: adopted`; `mode` wins; disagreement
  is an error).
- **`caller_identity` is the one field the bus record did not have.** It
  defaults to `api-key` for a `managed` message-bus today (the team
  password of brief 10 D3, minted at `internal/bus/manager.go:26` and
  injected at `internal/runtime/adapter.go:319`). An `adopted` or
  `external` bus declares what the foreign broker requires, and marvel
  mints nothing for it (the ADR-009 reading `wzexa` already carries).
- **The daemon reads the list, not the field.** `attachBus`
  (`internal/daemon/daemon.go:2329`) becomes `attachServices`, a loop over
  entries dispatching on `provider`. One arm today; the second arm joins a
  loop rather than a second bespoke function.
- **`marvel bus status` stays** as a projection of the `bus` entry; a
  `marvel service status` verb over every entry is a follow-on.

- **The declared set rides in.** `wzexa`'s one Go value replaces three
  literals with three readers: the stream configs at `provision.go:69` and
  the KV bucket at `:114` (provisioner), the grant lists at
  `render.go:176` through `:185` (renderer), and nothing yet for health.
  `Objects` (name, kind, scope, origin, `StreamConfig` or `KeyValueConfig`)
  and `Principals` (name, scope `service` or `binding(team)`, publish,
  subscribe, origin) cover all three. `RenderAuth`'s ordering (admin first,
  teams sorted by workspace then team, `render.go:147`) and its
  duplicate-team refusal (`:164`) carry over unchanged.

**Migration:** none. No host carries a `bus:` section (`bus-as-service.md`
section 2, Winston's concession); the bool survives so brief 10's examples
parse. The `services:` spelling and the `bus:` spelling produce the same
record, and a golden test asserts it.

---

## 2. `caller_identity` on the class contract

### 2.1 What the class contract is

A class contract is a Go value in `internal/service` (new package) stating
what every provider of a class must deliver: the patterns and semantics a
consumer may rely on, the `caller_identity` set with each value's Binding
meaning, and the managed child's environment allowlist (section 3.3). The
`message-bus` patterns and semantics stay deferred to `aae-orc-oxy50` and
`aae-orc-8mcnf` (`bus-as-service.md` section 2); this document adds the two
clauses a second class cannot arrive without.

```go
type ClassContract struct {
    Name             string
    CallerIdentities []CallerIdentity // the defined set, with Binding semantics per value
    ChildEnvAllow    []string         // section 3.3
}

type ProviderDriver struct {
    Name     string
    Class    string
    Modes    []Mode
    Delivers []CallerIdentity          // what this driver can actually enforce today
    // render, spawn spec, health, mint: the surface bus.Manager and bus.Supervisor have now
}
```

### 2.2 Why it sits on the class (ratified 5)

The PAIR panel forced the field into existence, and both architects who
argued it placed it on the class (round-2 and round-4 transcripts): a
Binding to a provider whose callers are anonymous is visibility, not
access, and a manifest must not claim it gates when it only informs. That
is a property of the interface a consumer depends on, so it is a clause of
the class contract. A provider declares which values it delivers; an
instance declares which is in force; neither invents one, or a consumer
written to the class meets a credential form the class never promised.

The Switchyard panel met the same rule from the other side: what keeps that
proxy from the cert-manager bar is a provider property (no inbound caller
verification), and a class field surfaces it as `Delivers: [none]` at parse
instead of as a surprise at spawn.

### 2.3 The values

| Value | Meaning | Binding semantics | On the bus |
|---|---|---|---|
| `none` | The service does not identify callers; network position is the only control | The Binding informs (URL delivered); it cannot gate | Not in the `message-bus` set: a bus with anonymous callers is the finding-166 fleet, and marvel refuses to describe one as a service |
| `api-key` | The caller presents a marvel-minted secret (a user/password line, a virtual key) that the service checks | The Binding gates: marvel mints per team or per session, revokes by rewrite and reload | Today's form: `DIRECTOR_NATS_USER` / `DIRECTOR_NATS_PASS` from `adapter.go:321`; the PAIR panel spelled it `api-key` and this document keeps that spelling |
| `session-cert` | The caller presents the fleet-CA per-generation leaf; the service maps its SAN to an identity (`verify_and_map`) | The Binding gates and the credential is the identity; no separate secret | The ruled target, `aae-orc-5yqw3`. The panel wrote `mtls`; `session-cert` names the artifact rather than the transport |

`Delivers` on the nats-server driver is `[api-key]` today and gains
`session-cert` when `5yqw3` lands. Declaring `session-cert` before that is
`ErrInvalidService` naming the ticket: a declaration against a driver's
stated capability, both in code, no health reading involved.

### 2.4 Defaulting

When a provider delivers exactly one value, the loader defaults
`caller_identity` to it and `marvel bus status` prints it, so a record
written before the field existed reads honestly. With two or more, the
field is required.

---

## 3. The supervisor extraction as gating precondition

### 3.1 What `supervisor.go` does today

`bus.Supervisor` (`internal/bus/supervisor.go:30`) resolves `nats-server`
(`:109`), adopts or spawns (`Start`, `:130`), waits for the listener, runs
`AfterReady` (provisioning, `daemon.go:2394`), restarts a crash under the
role backoff, reloads by SIGHUP (`:371`), polls the leaf link every 30 s,
and reports `Status` (`:68`). Only the binary name, the conf flag, and the
leaf poll are nats-server's; the rest is supervision of one child.

### 3.2 The hazard

`spawnLocked` (`supervisor.go:190`) builds the child environment as:

```go
env := os.Environ()                  // supervisor.go:196
if s.Env != nil {
    env = append(env, s.Env()...)    // the minted leaf seed, daemon.go:2381
}
```

The broker inherits the daemon's whole environment. The daemon is started
from an operator shell, and on this workstation that shell carries
`BEADS_DOLT_PASSWORD` (the bd client env) and `MARVEL_HEARTBEAT_TOKEN`
(names observed in the session environment 2026-09-16; values not read).
Any `ANTHROPIC_*`, `AWS_*`, or `GH_TOKEN` an operator exports for other work
rides along the same way. The broker needs none of them. The liteLLM panel
named the general form: the admissible line (an allowlisted read of an
operator-owned file) and the inadmissible line (`os.Environ()`) are one
line apart, so the rule has to be code, not review.

The block at `:196` through `:198` is the whole exposure: nothing else in
the supervisor reads the environment (the pidfile is written `0o644` at
`:221`, the log `0o600` at `:206`, neither from env). A workload kind with
an allowlisted child environment replaces exactly that block.

The pane path already does this right by construction: `baseEnv`
(`adapter.go:292`) builds a map and the tmux driver passes it key by key
(`internal/tmux/driver.go:287`). The managed child is the one spawn path
that does not.

### 3.3 The workload kind: `workload.Process`

New package `internal/workload`. One kind, one constructor, one spawn path:

```go
type ProcessSpec struct {
    Name      string          // "bus"; names the pidfile and log
    Binary    string          // resolved by the driver (exec.LookPath)
    Args      []string        // "-c", confPath
    EnvAllow  []string        // keys copied from the daemon env; from the class contract
    MintedEnv func() []string // secrets the daemon minted, read fresh at every spawn
    Ready     func(ctx) error // listener answers; driver-supplied
    AfterReady func() error   // provisioning; driver-supplied
    Reload    syscall.Signal  // SIGHUP for nats-server; 0 means restart on reload
}

type Process struct { /* bus.Supervisor's fields, minus mgr and the leaf poll */ }
```

The environment is built once, in one function, and it is the only way a
`Process` spawns:

```go
func childEnv(allow []string, minted []string) []string {
    env := make([]string, 0, len(allow)+len(minted))
    for _, k := range allow {
        if v, ok := os.LookupEnv(k); ok { env = append(env, k+"="+v) }
    }
    return append(env, minted...)
}
```

- **The allowlist is code.** `ChildEnvAllow` for `message-bus` is `PATH`,
  `HOME`, `TMPDIR`. A driver may append keys from a constant reviewed in the
  PR that adds the driver. No config field exists for it: an operator
  cannot widen the child environment from YAML.
- **Minted secrets ride `MintedEnv`,** as `sup.Env` does at
  `daemon.go:2381` (the leaf seed under `DIRECTOR_LEAF_NKEY`); the conf
  guard at `supervisor.go:200` moves with it.
- **The bus is the first instance.** `bus.Supervisor` becomes a thin driver
  building a `ProcessSpec` (`LookPath("nats-server")`, listener dial as
  `Ready`, provisioning as `AfterReady`, SIGHUP as `Reload`) and keeps only
  the 30 s `/leafz` poll, the `bus.leaf.*` events, and the `Leaf` and
  `Domain` status fields. `spawnLocked`, `watch`, backoff, adopt-by-pidfile,
  `Restart`, and `Stop(keep)` move to `workload.Process` with behavior
  unchanged; the nine `bus.*` event kinds stay.
- **`os.Environ()` leaves `internal/bus`.** The daemon's one remaining call
  is `reexec` (`daemon.go:580`), the daemon re-spawning itself; out of scope.

### 3.4 Tests that prove the allowlist

In `internal/workload`, a `ProcessSpec` whose binary is `/usr/bin/env` (or
`sh -c env`) and whose `Ready` returns once the child exits:

1. `t.Setenv("MARVEL_TEST_CANARY", ...)`; spawn with `EnvAllow: ["PATH"]`
   and `MintedEnv` yielding `MINTED=1`; assert the output has `MINTED=1` and
   `PATH=` and lacks `MARVEL_TEST_CANARY`, `HOME=`, and every other key the
   test process carries.
2. The same against a long-running child (`sleep`), read from the kernel
   with `ps -E -o command= -p <pid>`, the pattern
   `TestRestartCarriesTheSeedIntoTheChildEnvironment` uses at
   `supervisor_test.go:323`.
3. In `internal/bus`, the ten existing supervisor tests pass unedited; the
   seed test also asserts `BEADS_DOLT_PASSWORD` and `MARVEL_HEARTBEAT_TOKEN`
   are absent from the broker's environment when set in the test process.

### 3.5 The gate

**No second managed instance until this lands.** Not liteLLM managed phase
0, not bd's dolt sql-server (the Switchyard panel's proposed instance two),
not a `model-endpoint` provider. The gate is structural: after the
extraction, `workload.Process` is the only spawn path a driver has, because
`spawnLocked` no longer exists. Before it, a second driver would copy
`supervisor.go` and copy line 196 with it. The ticket for the extraction is
P1 and is the dependency of every ticket that adds a managed driver; it does
not block the Services list record or `aae-orc-wzexa`, so the bus critical
path in `bus-as-service.md` section 9 gains one step (the record before
`wzexa`) and is otherwise unchanged. The first ticket that would add a
second driver, liteLLM managed phase 0, is not filed until the four-week
count review in `adopted-services-2026-09-16.md` section 6 says go, and it
takes this ticket as a dependency when it is.

---

## 4. Secret-manager target: the OPEN sub-question

Ratified text: "the secret-manager target is undecided ... bring the
operator the candidate targets, their trade-offs, and the relation to
aae-orc-z6y7, rather than presuming one. Do not file a target choice." This
section does that. **No target is chosen here, and no ticket in section 6
names one.**

### 4.1 What marvel holds today

`api.Credential` (`internal/api/types.go:567`) is a closed kind set with one
member, `nats-nkey-seed`: memory-only, never persisted to bolt
(`internal/api/store.go:563`), zeroed on delete, revealed only over the
local socket, dropped at daemon restart so the enroller pushes again. Team
and admin passwords are minted per daemon start in `bus.Manager`
(`manager.go:39`). Everything marvel holds it minted or can re-request:
issuance under ADR-009. The one long-term third-party artifact in the
picture, the operator's AI provider key, is held by no marvel component; it
sits in the operator's shell and harness config, which is what `z6y7` was
filed about.

### 4.2 The candidates

The custody column applies ADR-009's test to the **most durable** artifact
marvel would hold under that candidate. "Fleet reach" is kinu, mokuzai, and
remote hosts reached over `mrvl://`.

| Candidate | What it holds | Custody test (does marvel hold bearer authority at a third party?) | Rotation | Fleet reach | Operator burden | Relation to `z6y7` |
|---|---|---|---|---|---|---|
| **A. Marvel's store as it exists** (`api.Credential`, transient, closed kinds) | Issuance-class artifacts only: seeds, minted passwords | Passes by construction while `CredentialKind` stays closed to kinds marvel can re-mint. Admitting a vendor key as a kind is the failure the Switchyard panel's one-line tripwire guards | Re-put after every daemon restart (transient by design) | Per daemon; an enroller pushes over `mrvl://` | Re-put on restart; no long-term key ever lands | `z6y7` W1's artifact (a long-term key) is exactly what this store must refuse. A is not a candidate for W1; it is the boundary W1's answer must respect |
| **B. Operator-owned 0600 `env_file`** read by the daemon into the custodian child's allowlisted environment (the liteLLM panel's interim) | The vendor key, on disk, owned by the operator | Marvel holds nothing durable; the file is the operator's. In transit the value passes through daemon memory into one child's environment. The audience test does not score blast radius, and this reduces it from every pane to one supervised process | Edit the file, `marvel service restart` | Per host; the file does not travel | One file per host per secret; plaintext at rest under the operator's uid | Continues the plaintext-on-disk shape W2 detects. Acceptable only as the interim the panel called it, and W2's scanner should list the path |
| **C. macOS Keychain** (login keychain; the fleet already keeps the bd server password there, finding-039) | Long-term keys at rest, encrypted, unlocked with the login session | Same as B: marvel reads and pipes, holds nothing durable. A daemon under launchd needs the keychain unlocked; a headless host has no login session | Manual; the Keychain has no rotation primitive | macOS hosts only; Linux would need a secret-service equivalent, a second code path | Low on a workstation; uneven across the fleet | W1 lists it. Note: `akey` (`aae-orc/akey/PRODUCT_BRIEF.md`) is an SSH signing agent proxy that shows the human what they are approving; it is not a keychain front and holds no generic secrets. Its approve-with-context pattern is relevant to a reveal path; akey itself is not a store |
| **D. Self-hosted vault** (OpenBao or HashiCorp Vault) | Long-term keys; issues leased, revocable tokens and, where an upstream supports it, dynamic credentials (AWS STS yes; Anthropic keys no) | Passes: marvel holds a vault token the vault can revoke and marvel can re-request, inside the operator's trust domain. This is the brokering ADR-009 admits explicitly | Leases and TTLs; upstream rotation still manual where the upstream has no API | A network service; needs TLS from the fleet CA and reachability from every host | Highest: one more service to run, unseal, back up. Under this design it would itself be a Services entry (class `secret-store`), which raises the unseal-key question the vault cannot answer for itself | W1 lists HashiCorp Vault. D is the only candidate that turns W1's static key into a brokered one for the AWS Bedrock case; for direct Anthropic keys it is a better-guarded static store |
| **E. SaaS vault with a CLI** (1Password `op`, Keeper; the org already uses Keeper per `z6y7`) | Long-term keys in the SaaS; `op run --env-file` injects at launch | A human session (biometric unlock) can hold the SaaS session; a daemon needs a service-account token, which is bearer authority at a third party: custody. So the operator launches with it and marvel does not hold it | Item versioning; upstream rotation manual | Any host with the CLI and a signed-in human; headless hosts hit the custody problem | Low for a human-launched daemon; awkward for launchd | W1 lists both. E fits the human's own launches and not a fleet daemon |
| **F. `sops` + `age` files** | Encrypted files in git, one age recipient per host | The age private key decrypts every secret in the file: holding it is holding bearer authority by the most-durable-artifact clause. The operator holds it; marvel would need it to decrypt | Re-encrypt and commit; recipient rotation is a re-encrypt of every file | Git; keys distributed by hand per host | Low tooling; manual key ceremony | W1 lists it. F is B with encryption at rest and a key-distribution problem |
| **G. Federated identity, no long-term key** (Okta or IAM SAML for Bedrock; `z6y7` names this as often the correct hardening) | Nothing long-term; short-lived tokens from the IdP | Passes: nothing durable to hold | Silent, by the IdP | Wherever the IdP is reachable | Setup once; then none | `z6y7` W1's own note. Removes the question for the AWS case and does not exist for direct Anthropic API keys |

Plausibility today: A exists; B is one allowlisted read away once section 3
lands; C is a `security find-generic-password` call with a headless gap; D
through G are each a real build or an organizational decision. No panel
measured any of them against a running daemon.

### 4.3 Questions the operator has to answer

1. May marvel ever carry a long-term third-party key through its own memory
   into a custodian child (B, C), or must the custodian read from a source
   marvel only names?
2. Does a secret need to reach mokuzai and remote hosts through marvel, or
   is one secret per host, placed by the operator, the standing model?
3. Is running one more service (D) inside the fleet's budget, and who
   unseals it after a reboot?
4. For the AI keys `z6y7` was filed about, is the answer "no long-term key
   at all" (G) for the AWS case, which shrinks the store question to the
   direct Anthropic keys?
5. Does `CredentialKind` stay closed to issuance-class kinds, with a doc
   comment citing ADR-009 (the Switchyard panel's one-line tripwire)?
6. Where does rotation authority sit for a long-term key: the human, marvel,
   or the vault?

The design ticket in section 6 produces the packet that carries these
questions and the operator's answers. It does not choose.

---

## 5. What this does not decide

- The `message-bus` class contract's patterns and semantics
  (`aae-orc-oxy50`, `aae-orc-8mcnf`).
- Any second class or provider: `inference-gateway`, `model-endpoint`, and
  dolt sql-server are names on record, unregistered.
- Whether the hub becomes a top-level `external` entry or stays nested under
  the bus entry (nested, as `bus-as-service.md` has it, until a second
  external service needs a link field).
- The secret-manager target (section 4).
- Binding as a resource (per-team attachment to a service entry); the bus's
  per-team user line is the only Binding today and it stays inside the
  nats-server driver.
- Whether panes inherit the daemon's environment through the tmux server
  (the liteLLM panel's unmeasured inference); worth one measurement.
- `marvel service status` and `marvel service restart`; anything in
  `docs/design/service-provider/` beyond what section 1 commits.

---

## 6. Plan

Flat tickets, `depends on` edges only (`.claude/rules/bd-hierarchy.md`).
Labels `aae-orc, marvel, source:session`.

| # | Proposed title | Priority | New or update | Depends on | Acceptance |
|---|---|---|---|---|---|
| 1 | marvel: extract `bus.Supervisor` into `workload.Process` with an allowlisted child environment (gating precondition for any second managed service) | P1 | NEW | none | `os.Environ()` has no call in `internal/bus`; the env-printing child test shows a set canary absent and the minted key present; the ten existing supervisor tests pass unedited; `bus.*` events and `Status` unchanged |
| 2 | marvel: cluster-level Services list (`class`, `provider`, `mode`, `caller_identity`) with class-contract and provider registries; `bus:` lifted as the entry named `bus` | P1 | NEW | none (see 3.5) | `bus:` and `services:[class: message-bus]` parse to one record (golden test); unregistered class, provider, or mode is `ErrInvalidService`; both spellings and a bus block are refused together; `attachBus` becomes a loop over entries |
| 3 | `aae-orc-wzexa`: scope widens to "Cluster.Bus becomes entry one of the Services list"; declared set, hub `ca_file`, director seat user unchanged. Also resolves the `bus-as-service.md` 8.2 password residual inside the ticket, by the builder: `NewManager` mints fresh at start (`manager.go:52`) and never reads back the `authorization.conf` it wrote at `0o600` (`render.go:232`); recovering admin, seat, and team passwords from that file keeps running shims valid across restart and reexec, makes the adopted-broker SIGHUP at `supervisor.go:139` a no-op when nothing changed, and adds no material at rest beyond what ADR-009 already covers. Not a new ticket | P1 | UPDATE | #2 | `marvel bus status` prints class, provider, mode, caller_identity, version; `wzexa`'s golden tests extended, not rewritten; a restart test shows a pre-restart team password still authenticates; `aae-orc-vy6k7` and `aae-orc-bxg5f` still hang off it |
| 4 | marvel: `caller_identity` on the class contract: `message-bus` defines `{api-key, session-cert}`, nats-server driver `Delivers` `[api-key]`, structural refusal on mismatch, single-value defaulting | P2 | NEW | #2 | Table-driven tests: `none` refused for `message-bus`; `session-cert` refused with a message naming `aae-orc-5yqw3` until that driver capability lands; omitted field defaults and prints |
| 5 | marvel: secret-manager target decision packet (design task, no implementation): measure candidates A through G against a running daemon on kinu and mokuzai, record the operator's answers to section 4.3, cross-link on `aae-orc-z6y7` | P2 | NEW | none | Packet in `docs/design/` with the section 4.2 table filled from measurement, the six answers recorded, and no target named without an operator ruling; `z6y7` notes carry the link |
| 6 | marvel: `CredentialKind` doc comment cites ADR-009 and states the set is closed to kinds marvel can re-mint | P3 | NEW | none | One-line diff plus a test that `ValidCredentialKind` rejects an unknown kind (exists) with the comment present |

Order: 1 beside 2; 3 and 4 after 2 in either order; 5 and 6 beside all of
it. The `bus-as-service.md` section 9 critical path is unchanged except that
`wzexa` now sits behind #2. Row 6 is the same ticket as the CredentialKind
row in `adopted-services-2026-09-16.md` section 6; it is filed once.

---

## 7. Filed

Filed 2026-09-16: row 1 `aae-orc-oo62t`; row 2 `aae-orc-1yzn8`; row 3 `aae-orc-wzexa` (updated, now depends on 1yzn8); row 4 `aae-orc-chwhk`; row 5 `aae-orc-yuem0`; row 6 `aae-orc-vfwye` (shared with `adopted-services-2026-09-16.md`). Labels `aae-orc`, `marvel`, `source:session`.
