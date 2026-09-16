# The bus as a first-class Service

- **Status:** design, 2026-09-16. Recommendations for marvel-builder; nothing
  here is code. The kinu flip (section 6) is operator-timed and is not part
  of this document's delivery.
- **Author seat:** architect (fleet, workspace ops2), subagent, on the
  operator's workstream C relayed by the director.
- **Evidence:** party J report and grounding under
  `aae-orc/_bmad-output/party-mode/bus-as-service-2026-09-16/` (delta table,
  ranked shortfalls, ordered changes, three transcripts with inline checks).
- **Scope:** the NATS broker marvel supervises or consumes, described in the
  vocabulary of `docs/design/service-provider/02-architecture.md`. Not
  addressing (`aae-orc/docs/design/bus-address-hierarchy.md`, adopted), not
  marvel's in-process bus (marvel-builder's thread; the `internal` mode
  below is a reserved word for it), not hub operations (`aae-orc-qu88n`,
  `aae-orc-4vx98` stay director's).
- **Tickets:** `aae-orc-wzexa` (the Service record and the declared set),
  `aae-orc-vy6k7` (the structural-health contract), `aae-orc-bxg5f`
  (rewritten: the flip). Plan and edges in section 9.

---

## 1. Why this document

Brief 10 shipped a managed broker in five PRs (#265 through #269,
`internal/bus/`, 1973 lines): a `Cluster.Bus` section, a rendered
`nats-server.conf` with a 0600 `authorization.conf`, a supervisor that
starts, adopts, backs off, reloads, and polls the leaf link, one-shot
provisioning of three JetStream objects, credential injection into the
session environment, a spawn gate, nine `bus.*` events. On every host it is
inert: `~/.marvel/config.yaml` carries no `bus:` section, and the running
kinu broker is hand-started, anonymous, plaintext, and unknown to marvel.

The service-provider vision (2026-08-15) describes what the broker should be
to marvel: one Service resource with a class, a provider, and a registration
mode; a health endpoint that is a contract, not a liveness bit; provisioning
as a capability distinct from running. Party J measured the distance in two
legs. Running-to-shipped is a config edit plus a coordinated relaunch that
`aae-orc-bxg5f` already owns. Shipped-to-vision is three design moves, and
this document makes them: the structural-health contract (section 3), the
Service fields on `Cluster.Bus` (section 2), and the relaunch rewritten on
top of both (section 6). It also resolves the party's open questions
(section 8), including the one the flip cannot proceed without: what
credential the human director seat presents once the broker requires one.

---

## 2. The record: `Cluster.Bus` under the three axes

The vision's three axes are lifecycle (runtime service versus provisioning
capability), registration mode (how much of the service marvel runs), and
capability class (the interface the agent depends on). The bus record
carries all three.

```yaml
clusters:
  - name: kinu
    socket: ~/.marvel/run/marvel.sock
    bus:
      class: message-bus        # the only accepted value today
      provider: nats-server     # the only driver today
      mode: managed             # managed | adopted | external
      listen: 127.0.0.1:4222
      url: nats://127.0.0.1:4222
      store_dir: ~/.director/nats
      seat:                     # the human director seat's home subtree (section 8.2)
        workspace: aae-orc
        team: ops
      hub:                      # a Service record of its own, mode external
        url: nats-leaf://127.0.0.1:7442
        ca_file: ~/.director/nats-global/ca.pem
```

**Class and provider.** `class: message-bus` and `provider: nats-server` are
the vision's fields. Each accepts one value today and any other value is
`ErrInvalidBus`, so the fields are a contract from the first day rather than
decoration that later grows a validator. The class contract itself (which
coordination patterns and delivery semantics the shim may depend on) is not
written here; consumer hygiene under `aae-orc-oxy50` and `aae-orc-8mcnf` is
where the first pieces of it are being forced, and they stay director's.

**Mode.** Three values, one reserved word.

| mode | marvel does | sessions get | health | today's spelling |
|---|---|---|---|---|
| `managed` | renders, starts, supervises, provisions, mints team credentials | `NATS_URL` and a team credential | full structural contract (section 3) | `managed: true` |
| `adopted` | nothing but hand out the URL; the broker is on this host, started by someone else, with authorization marvel did not write | `NATS_URL`, no credential | unknown, reported honestly as such | `managed: false` with `url` |
| `external` | reaches a broker elsewhere over a link it can observe | nothing directly | whatever the link reports | `hub.url` |
| `internal` | reserved by doc comment: an in-process broker or library, marvel-builder's Q1 thread | | | none |

`managed: false` with only a `url` keeps working unchanged. The bool keeps
parsing: `managed: true` is `mode: managed`, `managed: false` plus `url` is
`mode: adopted`; when both are present `mode` wins, and a disagreement is
`ErrInvalidBus`. Nothing on any host has a `bus:` section, so there is no
migration to protect (Winston's concession, party J section 5); the bool
survives only so brief 10's examples keep parsing.

**The hub as the first external Service.** `hub` today is a URL in the leaf
block. It becomes a record with `mode: external` implied, `url`, `ca_file`
(`aae-orc-i9i23`, rendered into the leaf remote's `tls {}` block), and a
health that is the leaf link: the `/leafz` poll the supervisor already runs
every 30s and reports as `bus.leaf.up` and `bus.leaf.down`. Quinn's round-3
reversal stands: the adopted phase-0 broker under `managed: false` is
already an external service to marvel, so the external mode is not a
comment awaiting a second instance.

**ADR-009 on the mode field (party J open question 4).** The `mode` doc
comment carries this reading. Marvel-held secrets for a bus are issuance only
while the broker is marvel-supervised, which is `mode: managed`: the daemon
mints the passwords, can rewrite and reload them, and no third party's
console is involved. An `adopted` or `external` record carries no
marvel-minted secret. The one credential marvel holds for an external broker
is the hub leaf seed, and that is already ruled: the hub operator mints,
marvel distributes (`aae-orc/docs/design/fleet-ca-and-bus-auth.md` section
4). Handing marvel a credential for an adopted broker it did not configure
would be custody, and the record has no field for it on purpose. Moving a
`managed` broker out of marvel's supervision is the ADR-009 tripwire, and
the comment says so.

**Status.** `marvel bus status` prints `class`, `provider`, `mode`, and the
`nats-server` version the supervisor started, beside the fields it prints
today.

---

## 3. The structural-health contract

Today `Ready` is listener-answers plus pid-alive. A managed broker that is
bare (no streams), anonymous (the authorization include failed to load), or
plaintext reports `Ready: true`, and provisioning is asserted once at start
and never re-checked. finding-166 measured what a bare broker does to a
fleet: nine sessions reported running with zero presence.

### 3.1 The fields

`bus.Status` gains three fields, each checked against the running broker and
never inferred from the rendered files:

- `provisioned`: every service-scope object in the declared set (section 4)
  exists. Existence only; parameters are not compared. A hand-altered
  stream is drift, and drift is a diagnostic, not a gate.
- `authorized`: `/varz` on the loopback monitor reports `auth_required:
  true`. On a managed broker the rendered file always carries the include,
  so `false` means a reload did not land.
- `tls`: `/varz` reports `tls_required: true`. Reported on every managed
  broker from day one; it joins the ready predicate only once the client
  listener is rendered with TLS (`aae-orc-5yqw3`). Until then it reads
  `false` on every managed broker and that is the truth of the interim LAN
  posture, not a failure.

`Ready` for a managed broker becomes: listener, pid, `provisioned`,
`authorized`, and `tls` once rendered. For `adopted` the three fields are
absent or `unknown`: marvel holds no admin credential there and does not
guess.

### 3.2 When the check runs

At start, after `AfterReady`. After every `Regenerate` that reloaded the
broker. And on the existing 30s tick in the supervisor's watch loop, the
same ticker that polls `/leafz`.

One cadence, not two (party J open question 5). The check is one admin
connection and a handful of lookups (five objects today, seven with the
event plane), against a broker on loopback. A second ticker is a second
schedule to reason about and to test, for no measured saving. If the admin
connection per tick is ever measured to matter, run the provisioning check
on every Nth tick of the one ticker rather than on a new timer.

### 3.3 On a miss

- Re-provision: run the idempotent `Provision` again as the admin identity.
  "Exists is success" means a hand-provisioned object is never rewritten; a
  missing one is created with its declared parameters.
- Hold new spawns: set `ready` false so the controller's `BusGate` returns
  `HoldBus`, the reason that already exists. The hold lasts until the
  re-provision succeeds, in the common case inside the same tick.
- Emit `bus.unprovisioned` (warning) naming the missing objects, once per
  change in the missing set. Not once per tick: a broker that stays broken
  for an hour produces one event and one hold reason, not 120 events.
- Never restart on a miss. A missing stream is metadata; the process is
  fine; a restart drops every live connection to fix a lookup. `authorized`
  false is a reload that did not land: SIGHUP once, re-check, then hold and
  emit. Still never restart.

### 3.4 Running sessions on a miss (party J open question 1)

Hold new spawns is agreed. Running sessions are **not** marked degraded by
marvel on a provisioning miss. Two reasons. One cluster fact would fan out
to N session-level state changes and N events for one cause, which is noise
in the ring and a lie in the roster (the session process is alive; its bus
is what is hurt). And the per-session signal already has an owner: the
shim-fed heartbeat (`aae-orc-z63a4` shape 2) carries bus liveness into the
health column, so a session whose shim lost its stream shows up there, once,
as itself. The operator sees the hold reason on the cluster and the one
event; the roster shows which sessions actually noticed.

### 3.5 Structural only

ADR-007 clause 3 and `.claude/rules/diagnostic-not-gate.md`: structural
validity may gate, vital signs never do. Existence of declared objects, auth
loaded, TLS on: structural, gate. Consumer count against presence,
never-delivered durables, stream age against retention: vital signs. They
go on `marvel bus status` and the ring as diagnostics under `aae-orc-oxy50`
and `aae-orc-8mcnf`, and no code path turns one into a hold. The version
string is a diagnostic too.

### 3.6 What "provisioned" is a property of (party J open question 3)

The vision splits Endpoint (registered once) from Binding (bound to many
scopes). The bus has both kinds of thing:

- **Service-scope**: `AGENT_INBOX`, `AGENT_AUDIT`, `AGENT_STATE`, and the
  event-plane streams `EVENTS_GH` and `EVENTS_MARVEL`. Each exists once per
  broker, is created by the admin identity, and its absence strands every
  session on the cluster. `provisioned` is a property of the Service and is
  what the health check reads.
- **Binding-scope**: the user line and grant rows for one applied team,
  including the event-plane rows of `aae-orc-lgthv`. Each is the team's
  attachment to the Service, rendered per team by `Regenerate`, and its
  absence hurts that team alone.

The health contract in `aae-orc-vy6k7` checks service-scope objects. A
binding-scope check (did this team's user line land, can it connect) is
verified today at render (a reload error surfaces) and at spawn (the shim's
`--preflight`, finding-166 shape 1). Making it a per-team hold is a
follow-on named in the ticket as out of scope, because marvel has no
per-team hold today and inventing one for a case nobody has hit is
scaffolding.

---

## 4. Provisioning as a declared set

Today the three objects live as literals in `provision.go`, the grant rows
as literals in `render.go`, and the health check (once it exists) would need
a third copy. One list, read by three consumers:

```
Declared {
  Objects:    []Object    // name, kind (stream | kv), scope (service), config, origin
  Principals: []Principal // name, scope (service | binding(team)), publish, subscribe, origin
}
```

- The renderer reads `Principals` to write `authorization.conf`.
- The provisioner iterates `Objects` with exactly today's semantics: look
  first, create only what is absent, alter nothing.
- The health check iterates `Objects` of scope `service`.

The existing three objects keep their exact parameters (`AGENT_INBOX`: two
inbox subjects, file, limits, 24h, 64 KiB, 2m dedupe; `AGENT_AUDIT`: 720h;
`AGENT_STATE`: 90s TTL). `EVENTS_GH` and `EVENTS_MARVEL` join under
`aae-orc-aubd6` with the event-plane parameters (limits, 24h, dedupe equal to
retention, 200 per subject, `allow_msg_schedules` on marvel's), and the
event-plane grant rows join under `aae-orc-lgthv`. Both tickets become "add
entries to the set" rather than "edit two files and hope the third copy
follows." The `$JS.API` narrowing by stream name in `lgthv` is derived from
the same list, so a stream cannot be added without its grant, or the other
way around.

The `origin` field on each entry names the brief or design that declared it
(brief 10 section 5, utility-event-plane section 4). It is documentation
carried in the code, so a reader of `provision.go` a year from now knows why
`EVENTS_MARVEL` has schedules on.

---

## 5. Cadence, pin, and the supervisor

- **Cadence:** one 30s tick for leaf polling and structural checks (section
  3.2).
- **Pin:** `nats-server` is pinned at 2.14.6 in `mise.toml` (PR #279, merged
  2026-09-16 under `aae-orc-k28s`). The daemon still resolves a bare
  `nats-server` from PATH; the pin is what makes the repo and the fleet
  resolve the same one. The supervisor logs and reports the version it
  started, so a host whose PATH disagrees with the pin is visible in
  `marvel bus status` rather than discovered at an upgrade.
- **Upgrade:** the vision says rolling a broker upgrade is the Shift
  primitive applied to a part. Marvel's staged-activation model
  (`aae-orc/_kos/ideas/marvel-staged-activation-upgrades.md`) is the frame:
  mise as the image plane that pre-stages a version, `marvel update` as the
  cluster plane that activates it. For the broker the activation primitive
  is what the supervisor already half-has: adopt across reexec, `--keep-bus`
  on stop, and `nats-server --signal ldm` (lame duck) for a drain. The
  design study is `aae-orc-k28s`'s remainder and is not done here.

---

## 6. The relaunch: `aae-orc-bxg5f` rewritten

Brief 10 section 8 item 8 said what the relaunch becomes once the managed
path exists: `managed: true` with `hub.url` in the kinu cluster's config and
a `credential put bus/leaf`, run at a coordinated window because the
authorization block goes live with it. This section is the rewrite; the
ticket carries the same content.

### 6.1 The config edit

On kinu, `~/.marvel/config.yaml`, the cluster that owns the twin socket:

```yaml
bus:
  class: message-bus
  provider: nats-server
  mode: managed
  listen: 127.0.0.1:4222
  url: nats://127.0.0.1:4222
  store_dir: ~/.director/nats        # the phase-0 store root; marvel appends /store
  seat: { workspace: aae-orc, team: ops }
  hub:
    url: nats-leaf://127.0.0.1:7442
```

`store_dir` points at the phase-0 store so `AGENT_AUDIT`'s 720h history and
the live `AGENT_STATE` bucket survive; adding a JetStream domain to an
existing store keeps streams, KV, and default-prefix clients (verified
2026-09-14, in the current ticket text). A fresh store is acceptable only if
the operator rules the audit history disposable. The monitor port becomes
8222 (listen plus 4000), which is the port the phase-0 broker uses today.

### 6.2 Preconditions

1. The daemon on kinu is built from a `main` that includes `aae-orc-wzexa`
   and `aae-orc-vy6k7`, and #279 (the pin; done).
2. The seat credential (section 8.2): `seat:` in the config so the
   `director` user renders; the shim reads `DIRECTOR_NATS_PASS_FILE`; the
   operator has set `DIRECTOR_NATS_USER=director` and
   `DIRECTOR_NATS_PASS_FILE=<StateDir>/nats/director.pass` once in the
   `director-mcp` entry of the aae-orc project in `~/.claude.json`, at a
   terminal. Without this the flip locks the human out of the bus it is
   turning authorization on for.
3. `marvel credential put bus/leaf` with the `leaf-kinu` NKey seed, so the
   leaf block renders and the hub link comes up in the same window. Without
   it the broker starts local-only and emits `bus.leaf.unenrolled`, which is
   survivable but is a second window later.
4. Every marvel-managed session on kinu is one marvel can respawn. A
   credential arrives only at spawn (`baseEnv`), so a running session with
   no credential cannot reconnect once authorization is on. The "17
   reconnects" the party priced are 17 respawns, and that is the design
   working, not a defect.

### 6.3 The window

The operator's timing. In order: stop the hand-started phase-0 broker (the
supervisor refuses to adopt a stranger on the port); reexec or restart the
daemon so it reads the new `bus:` section, renders, starts `nats-server` on
4222 with the authorization include, provisions (finds the existing objects
in the inherited store), and comes up as a leaf of the hub; roll every
kinu session so each spawns with `NATS_URL` and its team credential; let the
operator's seat shim restart and dial with the `director` user from the
file. Verify: `marvel bus status` reads `ready`, `provisioned`,
`authorized`, `leaf: up`; the hub's `/leafz` lists the kinu link; a global
publish from a kinu supervisor lands in `GLOBAL_TO_mokuzai`; an anonymous
`nats sub '>'` on 4222 is refused.

### 6.4 What it closes and what it must not break

Closes `aae-orc-umw8p` for kinu: the authorization block is active on 4222
by construction, and every session presents a credential the bus enforces.
`umw8p` stays open as the cross-cutting record until every cluster is
managed. `cast-launch.sh:38` (the hardcoded `NATS_URL` default) becomes dead
code for marvel-launched sessions; the director decides when to remove it.

Must not break: the `AGENT_AUDIT` history (inherit the store); the hub's
`director` and `leaf-mokuzai` users and mokuzai's cluster, which this window
does not touch; the seat's ability to reach every kinu session (the
`director` user's publish grants are the R-95 row); `managed: false` on any
other cluster.

---

## 7. Out of scope

- Addressing: `bus-address-hierarchy.md` is adopted and untouched.
- Marvel's internal bus and the embedded-library lead (Q1): marvel-builder's;
  `internal` is reserved here and defined nowhere.
- The auth form: the session cert is the ruled credential (`aae-orc-5yqw3`);
  every credential in this document is brief 10's interim password or the
  ruled target by ticket id.
- Hub operations: `aae-orc-qu88n` (launchd) and `aae-orc-4vx98` (TLS) stay
  director's. A record pointing at the hub does not move its operation.
- The record projected as a file the shim watches (party J move 7): deferred
  until the env form has been exercised; noted on `aae-orc-fcgpf`.

---

## 8. The open questions, resolved

### 8.1 HoldBus on a mid-flight provisioning miss (J open question 1)

Hold new spawns; do not mark running sessions degraded. Section 3.4.

### 8.2 The human director seat's credential when the flip turns auth on (J open question 2)

**Finding first.** The prompt for this design said the grant table already
has a director-only local user; checked, that is half true. The R-95 grant
table (`director/sim/design/global-bus-tier.md` section 4.1) has a row for
it: "local, director seat credential (its own, not a team's)", publish
`agent.*.*.*.inbox`, `agent.*.*.role.*.inbox`, `agent.*.*.broadcast`,
`agent.*.broadcast`, `agent.audit`, plumbing, publish-only across
workspaces; subscribe its own inbox, its own team subtree, KV. But no file
renders it. `director/probe/nats-phase-0/authorization.conf` has
`director_admin` (break-glass, `>`, "NOT an agent credential and is never
handed to a session") and `ops` (a team). `internal/bus/render.go` renders
`marvel_admin` and one user per applied team. The hub has a `director` NKey
user; the local tier has none. So the row exists and the user does not, and
`aae-orc-wzexa` renders it.

**Decision: a dedicated local `director` user, rendered by marvel from the
R-95 row.** Not the team credential, for three reasons. The team credential
is confined to `agent.<ws>.<team>.>`, and the seat's job is the one
cross-workspace publish the asymmetry permits: the fleet's sessions live in
workspace `ops2` while the seat's home is `aae-orc/ops`, so a team
credential cannot do the seat's job at all. The team credential rides into
every session of that team, so giving it to the human erases the line
between the human and the agents on the audit trail, and R-82 says the
credential is the only authority. And a team password is dropped when the
team is unapplied, which the seat must survive. Not `marvel_admin` or
`director_admin`: break-glass, `>` on everything, and the file's own comment
forbids it. Not an NKey or `.creds` on the local tier: brief 10 D3 chose
user and password for the local broker, a second mechanism buys nothing, and
the ruled successor (the session cert) covers agents while humans are
deferred under `aae-orc-k3ihd`. This is the interim that `k3ihd`'s deferral
assumed and never named; it retires when `k3ihd`'s trigger fires or the seat
moves to the hub's `director` NKey.

The seat's home subtree is not something marvel knows (the seat is not a
marvel session), so the record declares it: `bus.seat: {workspace, team}`,
R-76 tokens, optional. Absent, no `director` user renders and the flip
refuses to proceed with a human locked out, which is the honest failure.
`director` and `marvel_admin` are reserved names; a team by either name is
refused in `RenderAuth`.

**Placement: the seat's MCP env, by path, set once.** The seat is the
`director-mcp` entry of the aae-orc project in `~/.claude.json` (checked
2026-09-16: `DIRECTOR_AGENT_ID`, `DIRECTOR_TEAM`, `DIRECTOR_WORKSPACE`,
`NATS_URL`, no credential; the file is 0600). The operator adds two
variables at a terminal, once: `DIRECTOR_NATS_USER=director` and
`DIRECTOR_NATS_PASS_FILE=<StateDir>/nats/director.pass`. No password literal
enters the JSON.

**Rotation: marvel rotates, nobody re-pastes.** The daemon writes the
`director` password to `<StateDir>/nats/director.pass`, 0600, atomically
(temp plus rename), on every render that mints it. The shim gains
`DIRECTOR_NATS_PASS_FILE`, read through `nats.UserInfoHandler` (present in
nats.go v1.53.1, the version marvel already depends on) so the file is read
on every connect and reconnect, not once at startup. When the daemon
re-renders and reloads, the broker keeps the seat's live connection (a
reload does not drop authenticated clients), and the seat's next reconnect
presents the new value. No human step, no stale copy.

The alternative, a literal `DIRECTOR_NATS_PASS` in the MCP env set by the
operator, was rejected because it is a second copy of a secret in a file
marvel does not own, and because the copy goes stale at the first daemon
restart: `NewManager` mints every password fresh at start (`passwords` is
empty), so a literal would need a re-paste after every daemon restart, and
the operator would learn that by being locked out.

**A residual this surfaced, for marvel-builder.** The same fresh mint at
daemon start means a running session's team password is invalid at its next
reconnect after a daemon restart or reexec: the adopted broker is reloaded
with new passwords, connected clients are kept, and the first shim to
reconnect is refused. The seat file sidesteps this for the seat. For
sessions the fix is either persisting passwords across reexec (a 0600 file
the new daemon reads before it renders) or party J move 7 (the record as a
file the shim watches). It is named here so it is not rediscovered at the
flip; it is not designed here.

### 8.3 What "provisioned" belongs to (J open question 3)

Service for cluster-scope objects, Binding for per-team grants. Section 3.6.

### 8.4 ADR-009 on the mode field (J open question 4)

The doc comment carries the reading. Section 2.

### 8.5 One tick or two (J open question 5)

One. Section 3.2.

---

## 9. Plan, edges, and the critical path

Every ticket is flat; edges are `depends on`, no parents
(`.claude/rules/bd-hierarchy.md`). Labels `aae-orc, marvel, source:session`
or `aae-orc, director, source:session` by owner.

| # | Work | Owner | Ticket | Depends on |
|---|---|---|---|---|
| 1 | Pin `nats-server` 2.14.6 in `mise.toml` | marvel | `aae-orc-k28s` (REMAINING) | done, PR #279 |
| 2 | Service fields on `Cluster.Bus`; hub as external with `ca_file`; the declared set; the `director` seat user and its 0600 file | marvel | `aae-orc-wzexa` | none |
| 3 | Structural-health contract | marvel | `aae-orc-vy6k7` | `wzexa` |
| 4 | Shim: `DIRECTOR_NATS_PASS_FILE` via `UserInfoHandler` | director | noted on `aae-orc-fcgpf` | none |
| 5 | Operator: `seat:` in config, the two MCP env variables, `credential put bus/leaf` | operator | inside `aae-orc-bxg5f` | 2, 4 |
| 6 | The flip at a coordinated window | operator, director | `aae-orc-bxg5f` | `wzexa`, `vy6k7` |
| 7 | `EVENTS_GH`, `EVENTS_MARVEL` join the declared set | director (code lands in marvel's set) | `aae-orc-aubd6` | `umw8p`, `wzexa` |
| 8 | Event-plane grant rows join the declared set | marvel | `aae-orc-lgthv` | `umw8p`, `wzexa` |
| 9 | Vital signs on `marvel bus status` (diagnostic only) | director, marvel | `aae-orc-oxy50`, `aae-orc-8mcnf` | none; parallel |
| 10 | `ca_file` rendered into the leaf remote | marvel | `aae-orc-i9i23` | rides `wzexa` |
| 11 | Staged activation of the broker (design study) | marvel | `aae-orc-k28s` | none; parallel |
| 12 | The record as a file the shim watches | director, marvel | noted on `aae-orc-fcgpf` | after the env form is exercised |

**Critical path**, in order, with what each step unblocks:

1. `aae-orc-k28s`: the pin. Done (#279).
2. `aae-orc-wzexa`: the Service record, the declared set, the `director`
   seat user and its password file. Unblocks 3, 7, 8, 10.
3. `aae-orc-vy6k7`: the structural-health contract on the 30s tick. With 2,
   unblocks the flip.
4. Seat credential set: the shim's `DIRECTOR_NATS_PASS_FILE` (director; the
   note on `aae-orc-fcgpf`) and the operator's two MCP env lines. Without
   this the flip locks the human out.
5. `aae-orc-bxg5f`: the flip at the operator's window. Closes
   `aae-orc-umw8p` for kinu.
6. `aae-orc-aubd6` and `aae-orc-lgthv`: the event plane joins the declared
   set. Then `aae-orc-7vw44`, `aae-orc-bstdv`, `aae-orc-zhx6x`, and the
   rest of the event-plane build.

---

## 10. Open questions after this document

1. Per-team hold: should a binding-scope miss (a team's user line failed to
   land) hold that team's spawns? Marvel has no per-team hold today. Decide
   when a case is hit.
2. Team password validity across reexec (section 8.2, the residual): persist
   across restarts, or move 7? marvel-builder's call before the flip if the
   kinu daemon is expected to restart under the fleet.
3. `adopted` health: should marvel at least dial an adopted broker and hold
   spawns when nothing answers? It would change `managed: false` behavior
   on a path no cluster uses; left as-is until someone wants it.
4. The class contract for `message-bus` (patterns and semantics the shim may
   rely on) has no document. `oxy50` and `8mcnf` are forcing its first
   clauses; when a third arrives, write it.
