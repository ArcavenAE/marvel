# Adopted services: liteLLM, PAIR, Switchyard

- **Status:** Design, ratified direction 2026-09-16; implementation not
  started.
- **Author seat:** architect (fleet, workspace ops2).
- **Date:** 2026-09-16.
- **Evidence:** the three re-run party reports under
  `aae-orc/_bmad-output/party-mode/marvel-services-rerun-2026-09-16/`
  (`litellm/`, `pair/`, `switchyard/`, each with `grounding.md` and four
  round transcripts) and the earlier packet under
  `aae-orc/_bmad-output/party-mode/marvel-services-2026-09-16/`
  (`tech-litellm.md`, `tech-pair.md`, `tech-switchyard.md`, `report.md`).
  Panelist names below are the party personas as the reports name them.
- **Filed:** pending (section 7).

This document turns three ratified decisions about specific services into
entries a builder can implement. It does not define the cluster-level
Services list: the record shape (`class`, `provider`, `mode: managed |
adopted | external`, `caller_identity` on the class contract) and the
supervisor extraction that gates any second managed workload are
`docs/design/services-list.md`, written beside this one. Field names in the
examples follow `docs/design/bus-as-service.md` (marvel PR #281), whose bus
record these entries sit next to. Where this document and
`services-list.md` disagree on a field, `services-list.md` wins.

The three decisions, verbatim from the director's relay:

> 2. liteLLM: adopt-and-count now (adopt the operator's existing gateway,
>    spawn-time probe, "bound but down" projection, count four weeks).
>    Managed phase 0 is gated on the counts, no supervisor code before two
>    counts. Class inference-gateway, provider litellm.
> 3. PAIR: adopted design note only, class model-endpoint, caller_identity
>    none. No PAIR-specific code until an OpenAI-dialect harness runs here
>    at volume. Write Marek's readiness gates now.
> 4. Switchyard: external, adopted by URL. Standing rule: CredentialKind
>    never gains a third-party-issued kind (doc comment citing ADR-009).
>    Revisit only on Quinn's condition (session-cert verification +
>    SAN-to-route mapping).

---

## 1. liteLLM: class `inference-gateway`, provider `litellm`, mode `adopted`

### 1.1 What is being adopted

The operator's existing gateway is one `litellm --config ... --port 4100`
process on the daemon's host: hand-started, unsupervised, no database, no
virtual keys, one static master key (`tech-litellm.md` sections 2 and 4).
Sessions reach it through a profile env file: `ANTHROPIC_BASE_URL=
http://localhost:4100`, `ANTHROPIC_AUTH_TOKEN` set to the master key
(bearer), `ANTHROPIC_API_KEY` blanked, and the three
`ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL` variables naming gateway
routes the harness sees as model names. The port is 4100 because another
process holds 4000 on that host and liteLLM falls back to a random port
without error when its port is taken. Checked 2026-09-16 on this machine:
no liteLLM process is running and `:4100/health/liveliness` does not
answer, so 1.3's projection is what a spawn today would record. Nothing
under `~/.claude/settings.json`, `~/.config`, or `~/.marvel/config.yaml`
names the gateway; the binding is the profile env the daemon inherits.

Marvel holds nothing for an adopted gateway. The master key stays the
operator's, passed through the environment marvel never reads by name; the
upstream vendor key stays in the gateway's own environment. ADR-009
(`aae-orc/decisions/adr-009-credential-custody-boundary.md`): both are
bearer authority at a third party or at a process marvel does not
supervise, so both are custody, and the record has no field for either. The
bus record makes the same choice for `mode: adopted`
(`bus-as-service.md` section 2).

### 1.2 The Services entry

```yaml
clusters:
  - name: kinu
    services:
      - name: gateway
        class: inference-gateway   # contract on marvel's admin wire (1.6)
        provider: litellm
        mode: adopted              # on this host, started by someone else
        url: http://localhost:4100
        health: /health/liveliness # no model call, no token spent
        # caller_identity: api-key  (class contract, services-list.md)
```

`url` is the base URL the harness is handed; `health` is the path the probe
appends to it. No `listen`, `store_dir`, or config path: those are `managed`
fields and their absence is what `adopted` means.

### 1.3 The spawn-time probe and the "bound but down" projection

**Where it hooks.** `internal/session/manager.go:485` already classifies
the spawn environment at `Create` (`sess.BackendRedirection =
api.ClassifyBackendRedirection(backendEnvLookup(plan.env))`), reading the
effective `ANTHROPIC_BASE_URL` (`internal/api/backend.go:80-81`). The probe
runs at the same point on the same lookup, after `planLaunch` and before
the pane exists. A session is **bound** to an entry when its effective base
URL at spawn equals the entry's `url`. No manifest field is added: the
environment is the binding, which is how the operator's sessions reach the
gateway today. A per-team opt-in Binding is `services-list.md`'s field, and
when it lands the probe reads it instead.

**What it checks**, in order, each with a short timeout (the bus dial uses
10s; 2s per step is enough on loopback):

1. Listener: TCP connect to the entry's host and port. Failure here is the
   silent-rebind case the deployment picked 4100 to avoid; it records as
   `down` with detail `no listener`.
2. Liveness: `GET <url>/health/liveliness`. This endpoint makes no model
   call and spends nothing. Non-2xx records `down` with the status.
3. Route list: `GET <url>/v1/models` **without** a token. `200` yields the
   route names, and each of the session's three `ANTHROPIC_DEFAULT_*_MODEL`
   values is checked against them; a missing name is recorded as
   `route_missing: <name>`. `401` records `auth: enforced, routes unknown`,
   which is the expected answer on the operator's gateway and still proves
   a liteLLM-shaped responder.

The probe never reads `ANTHROPIC_AUTH_TOKEN` or any other credential, so
"auth accepted" is not something it establishes; the harness's first request
is that test, and its failure is loud in the pane. Reading the token to
probe with it would be one line, and the line is what the custody rule
forbids (`marvel-services-2026-09-16/report.md` section 5 item 3).

**What it records.** A sibling of `BackendRedirection` on the session
record (`internal/api/types.go:295`):

```go
// BaseURLProbe is the spawn-time observation of the Services entry this
// session's base URL points at. Zero value: no entry matched (unbound).
type BaseURLProbe struct {
    Service    string    // entry name, "" when unbound
    URL        string    // the effective base URL at spawn
    Outcome    string    // up | down | unbound
    Detail     string    // "no listener", "liveliness 503", "auth: enforced", "route_missing: x"
    ObservedAt time.Time
}
```

Graded observed-at-spawn, as the redirection verdict is: true of the instant
before the pane existed and nothing later.

**The projection.** The entry's status line (the sibling of
`handleBusStatus`, `internal/daemon/daemon.go:2425`, in whatever verb
`services-list.md` gives the list) shows `mode: adopted`, the last probe
outcome and time, and the number of live sessions whose `BaseURLProbe`
names the entry. "Bound but down" is the state where that count is above
zero and the last probe was `down`: sessions exist that point at a gateway
nothing answers on, and the operator can see it without opening a pane.
One event per state change, `service.unavailable` (warning) and
`service.recovered` (info), carrying the entry name, never one per spawn or
per tick; the bus emits `bus.unavailable` the same way
(`internal/events/events.go:166`).

**Sessions still start.** In adopted mode the probe does not hold the
spawn. The bus precedent gates only `managed` brokers (`BusGate` is
installed only when a supervisor exists, `daemon.go:2409`), and the rerun's
first count (1.4) is "teams spawned into a dead or wrong-port gateway",
which presupposes the spawn happens. The earlier packet's "instead of
spawning a team into it" described the managed shape's `HoldGateway`, which
arrives with phase 0 and not before; a held spawn would also hide the very
number the four weeks exist to produce.

### 1.4 The count

Two numbers, named by the yes side's concession and Penny's condition
(`litellm/report.md` section 6, `round-4-transcript.md` "Smallest first
move"):

1. **Spawns into a dead or wrong-port gateway.** Every spawn whose probe
   outcome is `down`, by team, over the window.
2. **Teams a single vendor key would have served.** Teams whose sessions
   only ever named frontier routes, so a per-team vendor key with a console
   spend limit would have done the gateway's one job for them. Marvel cannot
   classify this alone (the route table is the operator's), so the record
   carries the inputs and the operator classifies at review.

**Where the counts live.** An append-only JSONL under the daemon's state
directory, `~/.marvel/state/services/<entry>/spawns.jsonl`, one line per
spawn that matched the entry:

```json
{"at":"2026-09-17T14:02:11Z","workspace":"aae-orc","team":"ops","role":"dev",
 "session":"ops-dev-g3-0","outcome":"down","detail":"no listener",
 "default_models":{"opus":"judgment-primary","sonnet":"implementation-local","haiku":"mechanical-local"}}
```

It survives session pruning and daemon restarts, `jq` reads it at review,
and nothing in marvel reads it back. No aggregation verb is built before
the review.

**The window and the gate.** Four weeks from the day the entry first lands
in a cluster config on a host that spawns teams. The review is
`2026-10-14` if adoption lands this week; if it lands later the date moves
with it, and the ticket in section 6 says so. At the review the operator
reads the two counts and decides whether managed phase 0 opens.

A count is a vital sign, and ADR-007 clause 3 keeps vital signs out of
gates except "where the gate is clearly defined and ratified: scoped,
written down, and traceable to a decision"
(`aae-orc/decisions/adr-007-bainbridge-automation-boundary.md`). Decision 2
is that written decision: it scopes the gate to managed phase 0, names the
metric, and rules that no supervisor code is written before the counts
exist. The gate is a human decision point at a dated review (clause 2,
confirm-or-override); no code path turns the number into a hold, a CI
check, or a refusal, which is what `.claude/rules/diagnostic-not-gate.md`
asks.

### 1.5 What managed phase 0 would need, and why the gate sits here

Listed so the reader sees the size of what the count defers, not as work to
start:

- The extracted workload kind from `services-list.md`; the bus supervisor
  is hardwired to `nats-server` (`internal/bus/supervisor.go`) and a second
  copy is the outcome every panel refused.
- An **allowlisted** child environment. The managed child today inherits
  the daemon's entire environment (`supervisor.go:196`, `os.Environ()` plus
  `s.Env()`). A gateway child receives only the variables its config names;
  the vendor key's interim source is a 0600 operator-owned env file read
  into that allowlist, under a written rule because the admissible and
  inadmissible versions are one line apart (Priya, `litellm/report.md`
  section 4).
- A config render touching identity and budget only: the master key
  referenced by env name and minted per daemon start (issuance); the route
  table passed through as the operator's intent, never rewritten.
- Health as listener plus `/health/liveliness`; readiness as marvel's own
  per-route tool-call round trip on a slow tick, never liteLLM's `/health`
  on a tick (it spends tokens per model).
- `HoldGateway` with `HoldBus` semantics for bound teams only
  (`internal/team/controller.go:933-936, 1046-1051` is the template).
- A frozen-composition artifact for a Python service on macOS without a
  container runtime: open.
- Phase 1 (Postgres, per-team `max_budget`, per-session keys expiring with
  the shift) waits on a budget principal existing anywhere in marvel;
  `Team.Budget` is `MaxSessions` only (`internal/api/budget.go:143`).

Every item is supervisor code or its precondition, and decision 2 forbids
all of it before two counts. That is why the gate sits between 1.3 and this
list.

### 1.6 The class, in one paragraph

`inference-gateway` is defined on the wire marvel would speak to a gateway,
not the wire the harness speaks: mint a scoped credential for an identity;
bind it to a budget and a model set; revoke; read spend and refusals by
identity; health. The Messages wire stays outside the class; the route
table is the operator's. In adopted mode none of the verbs are exercised;
the entry carries the class so the field validates from day one.

---

## 2. PAIR: class `model-endpoint`, `caller_identity: none`, mode `adopted`, design note only

### 2.1 What PAIR is

NVIDIA Personal AI Router (`tech-pair.md`): a local-network inference
router that discovers peers over mDNS, pairs them with a PIN and pinned
self-signed certificates, and presents Ollama-dialect (`:11434`) and
OpenAI-dialect (`:1234`) proxies on loopback. Each request goes to one node
for its life; nothing is sharded or pooled. Every agent host runs its own
node; a shared remote PAIR does not exist by design. Beta (v0.1.1), no
health URL, no metrics, workers get liveness only ("a hang is not
detected"), peers get reachability only. No API key, no budgets, no spend
log: every process on the host is the same caller.

### 2.2 Why `caller_identity: none`

PAIR authenticates no caller: its mTLS is node-to-node and its proxies
accept any loopback process. A Binding to it is visibility, not access;
marvel can decide which roles are told the URL, and any process on the host
can find `:11434` without being told. The field is on the class contract so
a manifest cannot claim to gate what it only informs (`pair/report.md`
section 4). Enforcement at the consumer is curtain's, design-only.

### 2.3 The entry as it would look

```yaml
    services:
      - name: pair
        class: model-endpoint
        provider: pair
        mode: adopted                 # the installer's service runs it
        url: http://127.0.0.1:1234    # OpenAI dialect; 11434 is the Ollama dialect
        health: /v1/models            # liveness proxy; PAIR has no health URL
        # caller_identity: none  (class contract; Binding informs, never gates)
        peers: []                     # optional declared peer set, checked by node-info count
        expected_digest: ""           # optional model digest checked across peers at ready
```

Managed mode (the daemon spawns the PAIR broker) is unanimously no today:
no headless entry point, no known service unit name to disable, no drain, no
pin channel (`pair/report.md` sections 3 and 6).

### 2.4 The trigger: no PAIR-specific code until it holds

Decision 3's trigger is "an OpenAI-dialect harness runs here at volume."
The report did not define "at volume"; its grounding checked only that no
codex or opencode runtime is configured in `~/.marvel/config.yaml` on this
host and left "at volume in the fleet" as "the operator's fact to confirm"
(`pair/round-4-transcript.md`, checks table). Proposed observable, to be
confirmed or replaced by the operator: **one applied team whose roles run
the codex or opencode adapter has spawned sessions on 10 or more distinct
days inside a 28-day window on a fleet host**, read from the session store.
Until that holds, nothing in marvel is named after PAIR: no adapter change,
no probe, no hold reason, no dialect variable. The generic base-URL probe
of 1.3 applies to a PAIR entry unchanged the day one exists, and that is not
PAIR-specific code.

The report's other flip conditions, any one of which reopens the question
early: PAIR serving the Anthropic Messages dialect; PAIR naming the served
node in a response header or API; a pairing API; a headless entry point
plus a known service unit, which reopens managed mode for every service.

### 2.5 Marek's readiness gates (written now, built later)

Marek's point (`pair/round-1-transcript.md`, `round-3-transcript.md`): for a
service whose own health is liveness-only, "ready" has to be defined by
structural checks marvel runs, or the axis is decoration. Each row names
the observable that satisfies it and whether ADR-007 lets it gate.

| # | Gate | Observable that satisfies it | May gate? |
|---|---|---|---|
| 1 | Liveness with a timeout | `GET /api/tags` on `:11434` and `GET /v1/models` on `:1234` each answer 2xx inside the readiness timeout | Yes; structural |
| 2 | The pool is real (cluster-of-one) | `GET :14318/v1/node-info` answers on every peer in the declared set; the count equals the declared count. Provenance graded `lan-plaintext` (node-info is unauthenticated) | Yes; structural |
| 3 | Model parity across nodes | For each expected tag, the digest from `/api/tags` is identical on every peer at ready | Yes; structural |
| 4 | Responder is PAIR (port ownership) | `/v1/models` on both ports identifies PAIR. **Not established** that PAIR self-identifies; until it does the record reads `responder: unverified` | No, until established |
| 5 | One owner of the process tree | The installer's service is disabled before marvel supervises. Needs the unit name, **not established**. Managed mode only | Yes for managed, once the name is known |
| 6 | Wedged peer | None. A hang looks like a slow request; a restart drops every in-flight local inference. **Never restart on it** | No; there is no honest check |

A failed gate 1, 2, or 3 sets a hold reason `inference_unavailable` beside
`bus_unavailable` for roles bound to the entry, and only for them. None of
this is built until 2.4 holds; the table is the contract the build will be
tested against.

---

## 3. Switchyard: mode `external`, adopted by URL

### 3.1 The entry

NVIDIA NeMo Switchyard (`tech-switchyard.md`): a Rust routing proxy
(`switchyard-server`, vendor-rated demo grade) that picks a model per
request or turn while preserving OpenAI and Anthropic wire formats. One
`routes.toml`; upstream keys by `api_key_env` or `forward_auth`. No inbound
caller authentication; `GET /health`, `GET /v1/models`; SIGTERM drain.
Nobody runs it on a fleet host today.

```yaml
    services:
      - name: switchyard
        class: model-endpoint         # name reserved on record; contract unwritten (3.4)
        provider: switchyard
        mode: external                # marvel reaches it over a URL and does nothing else
        url: https://127.0.0.1:4000
        ca_file: ~/.marvel/certs/switchyard-ca.pem   # only when it serves TLS
        health: /health
```

The ratified text says "external, adopted by URL." In the bus record's
vocabulary (`bus-as-service.md` section 2) `adopted` is a process on this
host started by someone else and `external` is something reached over a
link; the entry uses `mode: external` and "adopted by URL" describes what
marvel does with it: hand out the URL, probe it at spawn (1.3, unchanged),
and nothing more. If `services-list.md` collapses the two words for
loopback URLs, the entry follows it.

### 3.2 What "adopted by URL" excludes

- **No lifecycle.** Marvel does not start, stop, restart, drain, or upgrade
  it (a restart would also empty its in-memory classifier affinity).
- **No config render.** `routes.toml` is the operator's; marvel writes no
  route, target, or `llm_clients` entry.
- **No credential minting.** Under `api_key_env` the vendor key is in the
  proxy's environment, custody the operator chose; under `forward_auth` the
  harness carries its own key. Marvel holds, mints, and passes nothing; the
  record has no credential field.
- **No hold.** A down external entry projects bound-but-down and emits the
  event; it does not gate spawns.
- **No Binding beyond the URL.** Switchyard has no caller table for marvel
  to write into, so RBAC is by process (one proxy per team) or by caller
  (`forward_auth`), never a table marvel owns.

### 3.3 The standing rule: `CredentialKind` never gains a third-party-issued kind

The enum lives at `internal/api/types.go:566-573`; today's doc comment is
one line and the set has one member, `nats-nkey-seed`. The Credential type
below it (`types.go:579-610`) already states the ADR-009 reading for the
bus seed. The rule goes on the kind, where the next member would be added.
Replace the comment at `types.go:566` with:

```go
// CredentialKind is the closed set of credential kinds marvel can hold.
//
// Standing rule, ratified 2026-09-16: this set never gains a kind whose
// issuer is a third party. Every member is issuance inside the operator's
// trust domain: the daemon, or a service the daemon supervises in that
// domain, minted it and can revoke or re-mint it without a human at anyone
// else's console. A vendor API key, a gateway master key marvel did not
// mint, an OAuth refresh or access token, or any other bearer authority at
// a third party is custody, whatever its format and however briefly it is
// held, and does not belong here. The test is audience, not format, and it
// applies to the most durable artifact held: see
// aae-orc/decisions/adr-009-credential-custody-boundary.md and SOUL.md
// section 3. Wanting a kind here for a service marvel adopts or reaches by
// URL is the tripwire; the answer is a vault the service reads with a token
// marvel can revoke, not a new member of this set.
type CredentialKind string
```

`ValidCredentialKind` (`types.go:576`) and the `mystery` rejection test
(`internal/api/credential_test.go:140`) already enforce closure; the
comment adds the rule for the reviewer who would otherwise widen it. No
kind is added, removed, or renamed.

### 3.4 Quinn's condition: the only revisit trigger

Quinn's root cause (`switchyard/report.md` sections 6 and 8): the property
that keeps Switchyard from being a managed service belongs to Switchyard.
A service that authenticates no caller cannot be managed for RBAC or
identity by any platform; run-and-manage would deliver lifecycle and
discovery only, which the operator's gateway has the higher claim to. The
condition that changes the answer has two parts, both required:

1. **Session-cert verification.** Switchyard, or a proxy in front of it,
   terminates TLS on its inbound listener with client-certificate
   verification against the fleet CA trust bundle (root plus the daemon
   intermediates of `aae-orc/docs/design/fleet-ca-and-bus-auth.md` section
   2). A request then carries a verified URI SAN in the fleet grammar,
   `marvel://<fleet>/<cluster>/<ws>/<team>/<slot>`, issued by the daemon at
   spawn as a per-session-generation leaf. Verification alone establishes
   who is calling and nothing about what they may call.
2. **SAN-to-route mapping.** A table from the verified SAN (or a prefix of
   it, such as the team segment) to the set of route ids that caller may
   request; a request naming a route outside its set is refused at the
   proxy. The daemon writes the row at spawn and removes it at exit, the
   same single-writer pattern the fleet CA design rules for the broker's
   user line (section 3.1, `verify_and_map` reloaded by SIGHUP). This is
   what turns the Binding from a URL into a contract and RBAC from
   process-per-team into a table marvel writes.

When both hold, the commission is re-run; the outcome is a re-vote, not an
automatic promotion to `managed`. A precondition on the harness side rides
with it: the harness must present a client certificate, which is not
established for Claude Code (`litellm/report.md` section 10, Form 3), so
the revisit checks that first.

---

## 4. What the three share

- All three are entries in the Services list of `services-list.md`,
  beside the bus record, each with `class`, `provider`, `mode`, `url`, and
  `health`.
- None needs the supervisor extraction. Only liteLLM managed phase 0 would,
  and it is gated behind the four-week counts.
- The spawn-time base-URL probe (1.3) serves all three unchanged; nothing
  in it is named after a provider.
- The only code before the gates: the entries, the probe and its session
  field, the status projection and two events, the count JSONL, and the
  `CredentialKind` doc comment.
- None moves marvel onto the request path; the route tables are the
  operators'.

---

## 5. What this does not decide

- The secret-manager target for a managed gateway's vendor key, and its
  relation to `aae-orc-z6y7`: open, handled in `services-list.md` section
  4.
- Whether the gateway Binding is a team field or a cluster record, and
  whether it requires an explicit billing acknowledgement (bound sessions
  move from subscription to API billing).
- The store for phase 1 (managed Postgres, adopted database, or a lighter
  provider probe first).
- The frozen-composition artifact form for a Python service on macOS
  without a container runtime.
- Served-model passthrough through the gateway, and whether
  `Runtime.ContextWindow` is enough for route names the harness cannot size.
- Degraded marking of running sessions when an adopted service goes down
  (`bus-as-service.md` section 3.4 says no for the bus).
- The `model-endpoint` class contract itself. The name is reserved on
  record by both the PAIR and Switchyard panels; no contract is written,
  and decision 4 names no class for Switchyard. The entry in 3.1 carries the
  reserved name so the field validates; if `services-list.md` prefers a
  class-less external entry, 3.1 follows it.
- The definition of "at volume" (2.4 proposes one; the operator confirms).
- The PAIR and harness facts marked not established above: pairing API,
  proxy self-identification, per-instance prefix cache, custom request
  headers, client certificates.

---

## 6. Plan

Flat tickets, no parents. The Services list record ticket from
`services-list.md` is the dependency for every entry; today `aae-orc-wzexa`
carries the bus half of that record.

| Proposed title | Pri | New / update | Deps | Acceptance |
|---|---|---|---|---|
| marvel: adopt the operator's liteLLM gateway as a Services entry (class inference-gateway, provider litellm, mode adopted) with the spawn-time base-URL probe and the bound-but-down projection | P2 | NEW | Services list record (`services-list.md`; `aae-orc-wzexa` today) | The entry parses; a spawn against a stopped gateway records `BaseURLProbe.Outcome: down` on the session, the entry's status shows bound-but-down with the live-session count, one `service.unavailable` event fires per state change, and the session still starts |
| marvel: gateway spawn count record, append-only JSONL under the daemon state dir | P2 | NEW | the entry above | Every spawn matching a gateway entry appends one line with the fields of 1.4; a daemon restart does not truncate it; nothing in marvel reads it back |
| marvel: four-week gateway count review, due 2026-10-14 (diagnostic review, not a gate that fires; decides whether managed phase 0 opens) | P3 | NEW | the two above | The two counts of 1.4 written into the ticket with the `jq` that produced them, and a written go or no-go on managed phase 0 from the operator; the date moves with first adoption if adoption lands after this week |
| marvel: CredentialKind doc comment carrying the third-party-issuer rule (ADR-009) | P3 | NEW | none | The comment of 3.3 above `internal/api/types.go:566`; `go vet` and `gofumpt` clean; no kind added or renamed |
| aae-orc-z6y7: note that an adopted gateway leaves the vendor key in the operator's gateway environment, and that a managed gateway's secret-manager target is this ticket's workstream 1 evaluation, referenced from `services-list.md` section 4 | P1 (unchanged) | UPDATE `aae-orc-z6y7` | none | One `REMAINING:`-style note appended; no scope change to the ticket |

No ticket for PAIR or Switchyard: decision 3's deliverable is section 2 of
this document, and decision 4's is the doc comment above plus section 3.
Both reopen only on their stated triggers.

---

## 7. Filed

Filed 2026-09-16: liteLLM entry `aae-orc-r1e3v` (depends on `aae-orc-1yzn8`); count record `aae-orc-r1itg`; four-week review `aae-orc-w2bmo`; CredentialKind comment `aae-orc-vfwye`; `aae-orc-z6y7` note appended. Labels `aae-orc`, `marvel`, `source:session`.
