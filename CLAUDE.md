# Marvel — Agent Orchestration Control Plane

@.claude/rules/_index.md

## What This Is

A control plane for AI agent workloads. Written in Go. Manages the
lifecycle of agent sessions running in tmux panes: scheduling,
configuration, process management, health monitoring, persistence, and
remote access over `mrvl://`.

The governing frame is the **agentic resource matrix**. The resources of
agentic work are context, spend, cache locality, access, and authority;
compute is one row of seventeen, not the frame. Kubernetes schedules
containers by CPU and memory. Marvel governs agents by this matrix.
Ratified 2026-08-01: `_kos/nodes/bedrock/elem-agentic-resource-matrix.yaml`.

Enforcement has three loci, in maturity order:

1. **Environment construction at spawn** (BUILT). Adapters construct the
   process environment; permission-through-environment.
2. **Runtime admission and metering** (FIRST BRICK SHIPPED). Manifest-
   declared team budgets refuse over-budget work at the operator verbs
   (`internal/admission`, PR #101); full metering remains open.
3. **Mid-flight revocation** (MISSING). The M1 authority model is its
   prerequisite.

## Build / Run / Test

Requires: Go 1.25+, `just`, `tmux`.

```sh
just build          # go build ./cmd/...
just test           # go test ./...
just lint           # golangci-lint run ./...
just fmt            # gofumpt formatting
```

## Resource Model

**Model-only, not implemented:** Pack (ConfigMap), Vault (Secret), Volume
(PVC), Schedule (CronJob), Gateway (Ingress), Readycheck (readiness
Probe). No type, no code, no CLI. They survive in
`elem-k8s-resource-model` as design prose only. `HealthCheckType` has
exactly two values, `heartbeat` and `process-alive`, so there is no
readiness check to configure.

The k8s mapping below is a **mechanics footnote**, not the frame:
declarative desired state, the reconciliation loop, and typed resources
are the whole inheritance (`elem-k8s-resource-model`, demoted to footnote
2026-08-01). Design attention starts at the resource matrix above.

### Primitives (implemented)

Declared in TOML or YAML manifests, applied with `marvel work`.

| K8s | Marvel | What it is |
|---|---|---|
| Namespace | **Workspace** | Isolation boundary: a project, team, or environment. Scopes every other resource. |
| Pod | **Session** | The atomic unit. One tmux pane running one harness process. Lifecycle pending → running → succeeded/failed, plus crashed and crashloop-backoff. Restartable. |
| Container | **Runtime** | The harness binary, args, and mode, plus `context_window` to override the model-to-window table for CTX%, and `context_feed = "statusline"` to give interactive claude sessions a cooperative CTX% feed via projected statusline hooks + `marvel ctx-forward` (finding-011, `examples/context-feed.toml`). `runtime` names the HARNESS (claude, codex, opencode), never the agent: `elem-runtime-names-harness`. |
| Deployment | **Team** | Heterogeneous roles, each with its own runtime and replica count. Per-role scaling, shifts. Binds a supervisor to its agents. |
| (none) | **Role** | One kind of agent within a team: name, replicas, runtime, restart policy, `max_restarts`, `permissions`, `dangerous_permissions`, `policy`, persona, identity. |
| Service | **Endpoint** | A named record of `{name, workspace, team}` and nothing else. Created from a manifest `[[endpoint]]` section, read with `marvel get endpoints` and `marvel describe endpoint`. No role field, and no code resolves an endpoint to a session, so it is a name in the store rather than a routing target. Role-based routing waits on director (roadmap M2). |
| ConfigMap | **Policy** | Named Claude Code settings fragment marvel projects into a per-session file the harness reads. Workspace-scoped, referenced by `Role.Policy`. Marvel writes it verbatim and does not interpret it. See orc finding-024 and `examples/policy-projection.toml`. |
| Probe (liveness) | **Healthcheck** | `heartbeat` (staleness) or `process-alive`. Failures feed the restart policy. |
| Node | **Host** | A machine running marvel. Local only today; multi-host is roadmap M5. |

### Resource Manifests (TOML)

```toml
# example: a review team with supervisor + reviewers

[workspace]
name = "acme-project"

[[team]]
name = "review-squad"

  [[team.role]]
  name = "supervisor"
  replicas = 1
  permissions = "plan"

    [team.role.runtime]
    image = "claude"
    command = "claude"

  [[team.role]]
  name = "reviewer"
  replicas = 3
  permissions = "plan"

    [team.role.runtime]
    image = "claude"
    command = "claude"
```

Healthchecks, policies, endpoints, and headless launches: see `examples/`.

## Architecture

This tree reflects the packages that exist today.

```
cmd/marvel/                 CLI entry point (cobra commands) + table rendering

internal/
  api/                      Resource types + store + persistence
    types.go                Core type definitions (Workspace, Session, Team, Role, Endpoint, Policy, Host)
    manifest.go             Manifest parsing and validation (TOML + YAML)
    store.go                Store struct (L1 in-memory synchronization
                            boundary; reads return value snapshots)
    bolt.go                 L2 durable record: bbolt write-ahead behind the
                            in-memory store. Buckets workspaces/teams/sessions/
                            endpoints/policies/role_health/meta, schema version 1
                            with refuse-on-higher, rehydrate on OpenBolt. A nil
                            bolt handle means in-memory only. Rehydrate zeroes
                            stream-derived SessionContext (an adopted pane has no
                            attached stream to refresh it), so CTX% renders "-"
                            for adopted sessions; heartbeat readings survive.

  daemon/                   Daemon process + RPC surface
    daemon.go               Reconciliation loop, lifecycle, adopt-on-restart
    sshserver.go            SSH transport for remote clients
    metrics.go              Daemon-level metrics

  team/                     Team (deployment) controller
    controller.go           Reconcile desired replicas vs actual; shifts, health

  session/                  Session lifecycle
    manager.go              Create, monitor, restart, teardown sessions
    bridge.go               Stream bridge between agent runtime and daemon
    projection.go           Policy projection writer (per-session settings file,
                            live re-projection, policy.projected event)

  runtime/                  Harness runtime adapters (the adapter framework)
    adapter.go              Adapter interface (Name, Prepare, ProjectionFor) + registry
    claude.go               Claude Code adapter
    codex.go                Codex adapter
    opencode.go             opencode adapter
    forestage.go            forestage adapter (frozen deep-integration reference)
    simulator.go            Simulator adapter (injects the identity flags its
                            heartbeat path needs, so it does not restart-loop
                            under the generic fallback)
    generic.go              Fallback adapter (any CLI) + GenericScraper, a
                            capture-pane history scrape with no production
                            call site yet
    instance.go             Running-instance abstraction
    instance_tmux.go        tmux-backed instance
    fifo.go                 Named pipe carrying a harness's structured stdout to the daemon
    stream.go               Stream observation + SupportsStream
    claudecode/             Claude Code stream-json parser + event mapping
    codex/                  Codex JSONL event-stream parser + event mapping
    opencode/               opencode JSONL event-stream parser + event mapping
    events/                 Runtime → director event envelopes

  tmux/                     tmux substrate
    driver.go               tmux session/pane CRUD, send-keys, capture-pane

  procstat/                 Per-session process stats (CPU%, RSS) via pid-subtree sampler
    procstat.go, proc.go, ps.go
    read_darwin.go, read_linux.go, read_other.go   platform samplers

  usage/                    Context-window and token accountant, the stream-fed
                            CTX% producer. Occupancy is a per-request level,
                            never a sum; unresolved windows report absence over
                            a guessed denominator (internal/usage/doc.go)

  events/                   Structured event ring (control-plane + agent.* events)
  keys/                     SSH client keypair management (~/.marvel/keys)
  knownhosts/               Host-key trust (~/.marvel/known_hosts)
  config/                   Named-cluster config (~/.marvel/config.yaml)
  paths/                    ~/.marvel/ path resolution
  logbuf/                   In-memory log buffer (serves `marvel daemon logs`)
  rlog/                     Structured request/run logging
  otel/                     34-line producer stub (stdout meter + one gauge).
                            Imported only by cmd/simulator; see Observability below.
  upgrade/                  Self-upgrade (Homebrew / direct binary)
  simulator/                Context-pressure simulation for testing without real agents
```

## Process Management

Marvel manages agent processes through the tmux substrate:

**Start:** create tmux pane → set environment → exec harness binary.
**Stop:** send SIGTERM → wait grace period → SIGKILL → destroy pane.
**Restart:** mark failed, delete the session, let the reconciler respawn it
on a later tick. Names are `<team>-<role>-g<generation>-<index>` and the
index is max(existing)+1 with no gap filling, so a single-replica role gets
its key back while a crashed middle replica of a multi-replica role returns
under a new index.
**Health:** periodic healthcheck, `heartbeat` staleness or `process-alive`.
**Auto-restart:** if healthcheck fails and restart policy allows, restart.

Restart policies: `always`, `on-failure`, `never`.

Recovery behavior, all shipped:

- **Crash-loop backoff.** Repeated restarts move the session to
  `crashloop-backoff` with the pane left alive, so the state is visible
  while the reconciler holds off. Per-role tracking lives in
  `team.RoleHealth`, written through to the bolt `role_health` bucket and
  rehydrated at daemon start.
- **`Role.MaxRestarts`** caps restarts per replica slot; on saturation the
  session is left `failed` rather than respawned. Zero means unlimited.
- **`crashed`** is the transition state `ReapDead` sets when a pane is
  gone, distinguishing a dead process from a drained one.
- **Adopt-on-restart.** A restarted daemon rehydrates the store and runs
  `AdoptOrKill` against the live panes; agents survive the daemon.
- **SIGTERM is detach, not teardown.** `marvel stop` leaves agents
  running; `marvel stop --teardown` is the destructive form.
- **Shift timeout with rollback.** A shift that cannot finish inside the
  timeout (default 10m) is aborted and rolled back rather than left
  half-drained.

## Agent Communication

**UNBUILT.** There is no agent message protocol in this repo. Earlier
prose here described three transports (named pipes, tmux send-keys,
switchboard relay) carrying task/result/heartbeat/signal messages. None of
that shipped: `internal/runtime/fifo.go` is a one-way sink for a harness's
structured stdout, tmux send-keys exists only as operator keystrokes via
`marvel inject`, and switchboard appears nowhere in the code.

What exists today:

- **The events ring** (`internal/events`, 12 `agent.*` kinds plus
  control-plane kinds). The stream-capable harnesses (headless claude,
  codex, opencode) have parsers that emit the unprefixed vocabulary in
  `internal/runtime/events`; `internal/session/bridge.go` lifts each kind
  into its `agent.*` ring kind. Read it with `marvel events`.
- **The heartbeat RPC** (`handleHeartbeat` → `UpdateSessionHeartbeat`), a
  daemon method carrying `session_key` and `context_percent`. Cooperative,
  and one of the CTX% column's two producers (the other is
  `internal/usage`, fed by adapter streams).
- **`marvel inject`**, operator keystrokes into a pane.

The adopted shape (roadmap M2): an **external NATS bus, supervised by
marvel as a declared workload**. Marvel stays thin and nats-server becomes
a runtime dependency; embedded NATS was declined. Marvel owns the events,
director owns the envelope. Ruling: orc `docs/roadmap.md` M2 and
`docs/marvel-remap-2026-08.md` sections 2 and 4. Envelope draft v1: orc
`docs/design/director-envelope-and-adapter-events.md`. Open question:
`_kos/nodes/frontier/question-agent-communication-broker.yaml`.

## Observability

What exists:

- **`internal/events`**: bounded ring, severity-tagged, filterable by
  workspace/team/role/session/kind. `marvel events`.
- **`internal/usage`**: the context-window and token accountant, the
  stream-fed CTX% producer. Occupancy is a per-request level, never a
  sum; an unresolved window renders `?` and emits
  `context.limit-unresolved` rather than guessing a denominator. The
  discipline and its measured basis: `internal/usage/doc.go`.
- **`internal/logbuf`**: the daemon's in-memory log ring. `marvel daemon
  logs`, over `mrvl://` too.
- **`internal/rlog`**: structured request/run logging.
- **`internal/procstat`**: per-session CPU and RSS over a pid subtree.
- **`internal/otel`** (34 lines): a stdout meter provider plus one gauge,
  `marvel.agent.context_window_percent`, that nothing in the daemon
  populates. Its only importer is `cmd/simulator`. Treat it as a stub.

There is no collector, no trace export, and no ingest path. The OTEL
funnel is explicitly HELD (orc `docs/marvel-remap-2026-08.md` section 2).
Design question:
`_kos/nodes/frontier/question-marvel-otel-architecture.yaml`.

Telemetry stays available, never mandatory (SOUL.md §6).

## Content Packs

Marvel has no pack subsystem: no `marvel pack` command, no `internal/pack`,
no Pack type. Prior prose here documented six commands that were never
built.

Pack management is phased across sideshow and marvel per orc
`decisions/adr-008-pack-management-phased-sideshow-marvel.md` (accepted
2026-08-01, supersedes ADR-002). **Now:** sideshow installs packs; marvel
consumes what is on the host filesystem as-is. **Next:** marvel manifests
declare which packs a workspace needs, and marvel or a marvel agent
prepares the workspace. **Later:** self-contained workspaces over a
subPath-like virtual filesystem, with sideshow gaining a marvel-aware
install target; gated on a workspace-VFS study.

Open question: `_kos/nodes/frontier/question-pack-integration.yaml`.

## CLI

```sh
marvel work <manifest.toml>                          # load manifest, reconcile desired state
marvel get <sessions|teams|workspaces|endpoints|policies>   # list resources (-w watches sessions)
marvel describe session <id>                         # detailed session info
marvel delete <session|team|workspace> <name>        # delete a resource (endpoints and policies are not deletable)
marvel scale <workspace/team> --role <r> --replicas N  # scale a role within a team
marvel shift <workspace/team> [--role <r>]             # rolling shift (replace sessions with fresh ones)
marvel run <command> [args...] --role <r>             # run a one-off agent session
marvel kill <session-key>                            # kill a session
marvel stop                                          # stop daemon, agents keep running (restart adopts them)
marvel stop --teardown                               # stop daemon, delete sessions, kill panes
marvel daemon                                        # start the daemon (foreground)
marvel daemon logs [-n N]                            # daemon log ring (works over mrvl://)
marvel daemon reexec                                 # adopt a freshly installed binary, agents keep running
marvel events                                        # structured event ring (control-plane + agent.*)
marvel capture <session-key>                         # capture a session's pane content
marvel inject <session-key> <keys>                   # send keystrokes to a pane
marvel config <add-cluster|list|current|use-cluster|remove-cluster>   # named-cluster config
marvel keys <generate|show|list|trust|authorize|authorized|revoke|doctor>  # SSH client keys
marvel version                                       # print version and channel
marvel upgrade                                        # self-upgrade

# future: attach, exec, drain, top, per-session logs (`daemon logs` is the
# daemon's own ring, not a session's output)
```

## Conventions

- **Language:** Go. Entire codebase.
- **Config format:** TOML or YAML for manifests. Go flags/env for runtime.
- **Auth:** Delegates to the harness → Claude Code. Marvel never stores credentials.
  Auth boundary: one user running their own agents under their own credentials
  (Max, API key, Bedrock, Vertex) is permitted. Orchestrating agents that route
  other people's consumer credentials is not — multi-user distribution requires
  API key auth. See SOUL.md §3.
- **No file deletion:** Never delete user files. Overwrite only with explicit intent.
- **Parallel-safe:** Each session gets a UUID. Volumes provide isolation.
- **Session substrate:** tmux. Panes = sessions. Sessions = agent processes.

## Independence and Coupling

Marvel enhances every other component but requires none of them.

**Marvel requires only:** Go, tmux, and a harness binary (any CLI that
accepts a prompt on stdin). Everything else is optional integration.

**Integration tiers:**

| Component | Without It | With It |
|-----------|-----------|---------|
| forestage (RETIRED, reference only) | Marvel drives claude, codex, or opencode. | Identity flags, permission mode, script, socket. The adapter is frozen as the reference implementation of the deep-integration contract; forestage is not a live target (operator directive 2026-07-31). |
| switchboard | Local-only scheduling. All sessions on one host. | Remote hosts. Distributed fleet. Cross-machine attach. |
| director | No inter-agent comms. Fan-out/collect only. | Supervisor patterns, agent-to-agent routing, role-based endpoints. |
| spectacle | No spec commands in packs. User loads manually. | IEEE-based spec templates available as a pack. |
| kos | No knowledge projection. Specs are manual. | Specs projected from knowledge graph into sessions. |

**Marvel is also optional to everything else:**
- forestage runs standalone without marvel (single-agent, own config chain)
- switchboard relays any tmux session, not just marvel-managed ones
- spectacle installs with `just install <target>`, no marvel needed
- kos operates its own probe/finding cycle independently

**Graceful degradation, not hard dependencies.** In the table above only
the runtime-adapter row is shipped: the switchboard, director, spectacle,
and kos columns describe intended integrations, and none of them appears in
the code today. What marvel actually probes at startup is tmux, without
which the driver refuses to run, plus the manifest validator's `LookPath`
on each role's runtime command. Everything else is absent rather than
detected, so a session launches with harness defaults.

## Design Principles

1. **Declarative desired state.** You declare what you want running; marvel reconciles.
2. **Agents are processes.** Each agent is a tmux pane running a harness.
3. **Manifests are data.** TOML or YAML, not code. Diffable, reviewable, versionable.
4. **Configuration is resolved, not assumed.** Manifest and adapter resolve
   at launch. Policy projection resolves at launch and re-resolves on
   re-apply: `session.Manager.Reproject` rewrites the projected settings
   file for every live session, so an edited policy reaches running agents
   without a restart.
5. **Fail observable.** Every session's output is visible; marvel logs all state transitions.
6. **Gradual elaboration.** Start with one session in a manifest, grow to fleet management.
7. **Harness-agnostic.** Six adapters, and the generic fallback manages any CLI.
8. **No conscription.** Marvel orchestrates, it does not require. Every integration is optional.

## How to Work Here (kos Process)

### Re-introduction
Read charter.md before any substantive work. It contains:
- Current bedrock (what's committed)
- Current frontier (what's under exploration)
- Current graveyard (what's been ruled out)

### Session Protocol
1. Read charter.md (orient)
2. Identify the highest-value open question — or capture new ideas in _kos/ideas/
3. Write an Exploration Brief in _kos/probes/
4. Do the probe work
5. Write a finding in _kos/findings/
6. Harvest: update affected NODES (`_kos/nodes/{bedrock,frontier,graveyard}/*.yaml`),
   move files if confidence changed. Charter is renderer output (per orc F22,
   `kos charter render`); do NOT hand-edit charter prose outside
   `<!-- backdrop -->` blocks. Subrepo charter renderer extension tracked
   in aae-orc-gezz.

Cross-repo questions belong in the orchestrator's _kos/, not here.

### Ideas (pre-hypothesis brainstorming)
Ideas live in _kos/ideas/ as markdown files. Generative, possibly contradictory,
no commitment. When an idea crystallizes, extract into a frontier question + brief.

### Node Files
Nodes live in _kos/nodes/[confidence]/[id].yaml
Schema follows kos schema v0.3.
One node per file. Filename = node id.

### Confidence Changes
Moving a file between confidence directories IS the promotion.
Always accompany with a commit message explaining the evidence.

### Harvest Verification
Before starting the next cycle, verify:
- [ ] Finding written and committed
- [ ] Bedrock/frontier/graveyard NODES updated if state changed —
      edit `_kos/nodes/{bedrock,frontier,graveyard}/*.yaml`, NOT charter
      prose. Charter is renderer output (per orc F22,
      `brief-charter-as-projection-renderer.md`, `kos charter render`).
      Subrepo extension tracked in aae-orc-gezz; until it ships, treat
      charter sections outside `<!-- backdrop -->` blocks as read-only
      and edit the underlying nodes.
- [ ] Frontier questions updated (closed, opened, or revised)
- [ ] Exploration briefs marked complete or carried forward
