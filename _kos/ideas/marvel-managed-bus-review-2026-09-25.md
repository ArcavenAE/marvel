# Design review: is "marvel runs its own bus" the right shape?

Status: design review (idea), 2026-09-25, operator request. Reviews bd
aae-orc-qu88n before anyone builds it. Nothing is built here.

Placement: marvel's graph, by the subject test. The subject is marvel
supervising a bus per cluster: its lifecycle, ports, discovery and health.
Director is the main consumer and appears here as an object. The hub's
operations are director's today (`docs/design/bus-as-service.md` §7), and this
review proposes moving them.

Citations are against origin/main (marvel `1df684c`, director `d458f30`, orc
`8844089`).

## Verdict

**Yes, and it is already the ruled shape, and mostly built.** The operator's
framing agrees with the 2026-08-01 ruling: "EXTERNAL NATS, marvel-supervised
(declared workload ...). Marvel stays thin; nats-server becomes a runtime
dependency via mise/brew" (orc `docs/marvel-remap-2026-08.md:167-172`,
`docs/roadmap.md:82-86`).

Managed mode does most of it today:

- the broker runs as a child of the daemon, in its own process group, with
  crash backoff (`internal/bus/supervisor.go:285-288`, `:146-160`). Citations
  are pinned at 1df684c; since #351 (4ef7222) the spawn itself lives in
  `internal/workload/process.go` (`exec.Command` at :79, `Setpgid` at :82)
- config rendering (`internal/bus/render.go:128-160`)
- credential minting (`internal/bus/manager.go:32-46`)
- provisioning of the declared streams and KV (`internal/bus/declared.go:96-132`)
- a structural health check (`internal/bus/health.go:64-118`)

The operator's framing departs from what exists in three places:

1. **Several clusters per host.** Bus state is shared by every cluster under
   one HOME: `StateDir/nats`, `RunDir/nats-server.pid` and `LogDir`
   (`internal/daemon/daemon.go:2619,2626`). Today, several clusters per host
   means several HOMEs.
2. **No launchd.** qu88n plans a launchd unit for the hub. The ruling and the
   operator both say the bus is marvel's to supervise. The hub is the one bus
   still outside that.
3. **Broker neutrality.** The ruling pinned NATS. The operator wants a seam
   that another broker could fill. Section 4 says that seam should be a
   document now, not code.

Managed mode has a live user: mokuzai (192.168.100.196, the `skippy`
cluster). The reviewer's `marvel bus status` there reports managed, ready,
leaf up, provisioned and authorized, and the hub's `/leafz` on kinu shows a
leaf from 192.168.100.196 in account FLEET. The known live defect on that path
is marvel#339 (a reexec drops the leaf).

kinu is the other shape. Its `~/.marvel/config.yaml` has no `bus:` or
`services:` entry, and its phase-0 broker (127.0.0.1:4222, no auth, no domain)
and the global hub (4242/7442/8242) are both started by hand. Every
observation below names its host; nothing here rests on managed mode being
untried.

## 1. What marvel owns, and what the broker keeps

| Concern | marvel | broker binary |
|---|---|---|
| Lifecycle: start, adopt by pidfile, restart under backoff, stop, SIGHUP reload | owns (built) | runs |
| Config: listen, monitor, JetStream store and limits, domain, auth, leaf remote | renders (built; store limits missing) | reads |
| Ports | allocates and persists (new, section 2) | binds |
| Credentials: admin, seat, per-team; leaf NKey in the bolt store | mints and recovers (built). Per-seat JWT per brief 11 §2.4 is designed, not built | enforces |
| Provisioning: streams, KV, principals | creates the absent, never alters (built) | stores |
| Health | structural (built) plus a write probe (new, section 5) | serves /varz, /jsz, /leafz |
| Upgrades | activation through the staged-upgrade design (bus-as-service.md §5, open); binary pinned by mise | the version |
| Protocol, storage, replication, leaf routing | none | owns |

**The hub.** Make it a marvel-managed bus in a small cluster of its own on the
hub host, with a `hub` role. Its config adds a leaf listener, the FLEET
account and NKey users, all rendered by marvel from the same declared set.
That puts the hub under the same supervision and health checks as every
cluster bus, and needs no launchd.

The cloud move (aae-orc-b0fzk) is the other end of that. The b0fzk notes
record an infra EKS NATS hub, TLS, domain `global`. Local clusters then
reach the hub in `external` mode, which marvel already models
(`internal/service/service.go:29-44`). Either way, launchd is not needed.

## 2. Several clusters on one host

- **Namespace every bus path by cluster**: `StateDir/nats/<cluster>/`, with the
  pidfile and log under that. This is what makes two managed buses under one
  HOME possible. The config test already models a twin cluster on 4223
  (`internal/config/config_test.go:299`).
- **Ports: allocate once, persist, reuse.**
  - An explicit `listen` still wins.
  - When it is unset, marvel takes a free port from a configured range (for
    example 4222 to 4299) at first start and writes it into the cluster's
    rendered conf. Later starts reuse it, so seats' stamped `NATS_URL` stays
    valid across restarts.
  - A port the OS assigns afresh at each start (nats-server's random-port
    option, `-p -1`; the reviewer verified it on nats-server 2.14.6, where
    `-p -1 -a 127.0.0.1` listened on 127.0.0.1:59448) is rejected, because it
    changes on every restart.
  - Unix sockets are not an option: nats-server has no client listener on a
    unix socket (UNVERIFIED; no such option found in the rendered config).
- **Monitor port: allocate it too, instead of listen+4000**
  (`render.go:35-39`). The offset already overflowed once (finding-047), and it
  clashes across clusters: a cluster on 4222 derives 8222, which a cluster that
  chose 8222 as its client port would also want.
- **Collisions.** The allocator checks a host-level registry of allocations
  and does a bind test. The only guard today refuses to adopt an unclaimed
  listener on the client port (`supervisor.go:214-216`). It does not look at
  the monitor port.

## 3. Registration and discovery

- **Keep the env stamped at spawn as the primary path.** It already works and
  is harness-agnostic. `baseEnv` sets `NATS_URL`, `DIRECTOR_NATS_USER` and
  `DIRECTOR_NATS_PASS` (`internal/runtime/adapter.go:318-325`), a role cannot
  override the credential keys (`:387-388`), and a manifest never names a
  broker.
- **Add a per-cluster bus record file** that marvel writes: endpoint, domain,
  and the paths to the credential files. Stamp its path as one more env var.
  The shim reads the file on reconnect, so a port or credential change reaches
  a running seat without a relaunch. That closes a doc-code gap:
  `bus-as-service.md:438-452` says the shim reads `DIRECTOR_NATS_PASS_FILE`,
  but no such read exists in director-mcp.
- **A daemon query** (`marvel bus endpoint`) for people and tools, not for
  seats. That keeps seats free of a marvel dependency (SOUL §2).
- **Restarts.** With persisted ports and recovered passwords
  (`manager.go:39-46`), a restart changes nothing a seat holds. A deliberate
  port change goes through the record file. One exception after #351: a broker
  the daemon adopts rather than spawns (`supervisor.go:199-207`, SIGHUP only)
  keeps its old process environment until it next restarts. mokuzai is the
  first host where that case is live. The phase-0 default of
  `nats://127.0.0.1:4222` (director-mcp `main.go:73`) should become an error
  when neither env nor record file names a bus, since a silent default is how
  a seat ends up on another cluster's broker.
- **Prior art:** `question-agent-service-directory` (orc) is the broader
  directory question. This record file is its smallest slice.

## 4. Broker neutrality: a seam, written down, not an interface in code

director and marvel depend on NATS well beyond pub/sub:

- JetStream streams with durable pull consumers and explicit ack (director-mcp
  `bus.go:173-177`)
- dedupe by Msg-Id (`bus.go:531,571`)
- KV with TTL as the presence and liveness signal (`AGENT_STATE`,
  `GLOBAL_PRESENCE`; `bus.go:768`, `global.go:380`)
- JetStream domains
- leaf nodes with NKey auth
- accounts and subject grants
- SIGHUP reload, and the /varz and /leafz monitoring endpoints
- brief 11 adds operator/JWT scoped signing keys and KV sourcing

Standing in RabbitMQ would mean rebuilding presence-with-expiry, dedupe and
cross-cluster store-and-forward on top of it. That is a port of director's
semantics, not an adapter.

Today's registry (`internal/service/service.go:76-128`) validates class,
provider and mode; it is not a Go interface. `attachServices` switches on the
provider name (`daemon.go:2587-2601`).

**Recommendation:** write the message-bus class contract as a document now.
It is bus-as-service.md open question 4 ("has no document"). It states what
any broker must provide:

- a durable per-seat inbox with ack
- dedupe by message id
- presence with expiry
- a store-and-forward link between clusters
- per-seat credentials that fail loudly
- health: ready plus a write probe

Keep the code NATS-only until a second broker has a real user (SOUL §7).
Writing the contract is cheap; writing the adapter now would build for a
tenant that does not exist.

## 5. Health: the check that would have caught 2026-09-24

The incident, from the hub log on kinu
(`~/.director/nats-global/log/nats-server.log`): the first "JetStream failed to store a msg on
stream 'FLEET > KV_GLOBAL_PRESENCE' ... no space left on device" came at
2026-09-24 16:14:57. There were 11,530 of them until a restart at 2026-09-25
16:17:42.

**No existing check would have caught it.**

- Every marvel health check reads: stream and KV lookups plus /varz
  (`health.go:77-117`). A bucket that exists but refuses every write passes.
- The shim logs a presence write failure once, on the transition, and does
  not escalate (director-mcp `bus.go:781-783`).
- The hub on kinu is hand-run, so no marvel check covers it at all. mokuzai's
  managed leaf has the checks above, and they read the same way.

Add three things:

1. **A write probe on the existing 30s tick.** For each declared KV bucket,
   put, get and delete a canary key. For each stream, publish to a canary
   subject with an ack. A failure emits `bus.store.write-failed` and escalates
   to the operator. Held against today's incident, this fires within 30s
   instead of 24h.
2. **Store limits and headroom.** Render `max_file_store` and
   `max_memory_store` into the managed conf. The phase-0 and hub confs set
   them; marvel's renderer does not (`render.go:128-160`). Also read
   /jsz usage against the limits and the free space on the store's filesystem,
   and warn before JetStream refuses writes.
3. **Restart policy for a write-dead store.** Today the rule is to escalate
   and never restart, because a restart drops every live connection
   (`supervisor.go:566-620`). That rule stays. The one addition is a
   restart that runs only after the probe has failed, free space has come
   back, and the operator's policy allows it. That is one decision with a
   named trigger, not a restart loop.

Whether the kinu hub's store would have recovered on its own once space came
back is UNVERIFIED. The probe answers it the next time.

The shim should also escalate a presence write failure instead of logging it
once, as a director follow-up.

## 6. Fate of aae-orc-qu88n

**Supersede it.** A launchd unit builds a second supervisor, outside marvel,
for exactly the component the ruling says marvel supervises. It would also
leave the hub without the write probe that section 5 shows is needed. Keep the
hand-run hub on kinu as it is until the follow-ups land. Do not harden it
with launchd.

This plan has a live user. mokuzai already runs the managed shape against the
kinu hub, so it is the first target for the write probe (follow-up 3,
aae-orc-kjix3) and for the adopt-keeps-old-env case in section 3.

Follow-up tickets I would file (not filed; flat, with edges):

1. **marvel: namespace managed-bus paths by cluster**, so two managed buses
   run under one HOME. Blocks 2 and 4.
2. **marvel: allocate and persist the bus client and monitor ports** from a
   configured range, with a host registry and a bind test. This replaces the
   listen+4000 offset and fixes the finding-047 overflow.
3. **marvel: a bus write probe** on each declared KV bucket and stream,
   emitting `bus.store.write-failed` and escalating, plus rendered
   `max_file_store` and `max_memory_store` and headroom warnings.
4. **marvel: run the global hub as a managed bus with a hub role**: leaf
   listener, FLEET account and NKey users rendered from the declared set.
   Supersedes qu88n; relates to b0fzk, since the cloud hub is the same shape
   reached in `external` mode.
5. **marvel + director: a per-cluster bus record file** stamped by path; the
   shim reads it and the credential files on reconnect. This closes the
   `DIRECTOR_NATS_PASS_FILE` doc-code gap, and a missing bus becomes an error
   rather than a 4222 default.
6. **marvel: write the message-bus class contract document**
   (bus-as-service.md open question 4). Documentation only.
7. **director: the shim escalates a presence write failure** instead of
   logging it once (`bus.go:781-783`).

These already exist and stay: marvel#339 (a reexec drops the leaf), aae-orc-oo62t
(the child env allowlist), aae-orc-4vx98 (TLS trial), aae-orc-b0fzk (cloud hub).

## Prior art read

- orc: finding-166 (a dead bus reports running), finding-178 (bring-up
  traps), `question-agent-service-directory`,
  `marvel-service-provider-architecture`
- marvel: `identity-at-launch-and-managed-nats`, `bus-leaf-status-and-terminology`,
  finding-047, `question-marvel-otel-architecture` (the h6ck sidecar-collector
  pattern the ruling cites)
- director: brief 10 (local broker supervision, "a daemon-owned child
  process"), brief 11 §2.2, §2.4, §2.6, §6 (a JetStream domain per cluster,
  per-seat JWT minted by marvel), finding-004, finding-007, finding-008
