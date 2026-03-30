# Marvel — Agent Orchestration Control Plane

## What This Is

A kubernetes-like control plane for AI agent workloads. Written in Go.
Manages the full lifecycle of BYOA agent sessions: scheduling, configuration,
process management, health monitoring, storage, networking, and observability.

Where kubernetes orchestrates containers across nodes, marvel orchestrates
agent sessions across tmux panes — local or remote via switchboard.

## Build / Run / Test

Requires: Go 1.22+, `just`, `tmux`.

```sh
just build          # go build ./cmd/...
just test           # go test ./...
just lint           # golangci-lint run ./...
just fmt            # gofumpt formatting
```

## Resource Model

Marvel's resource model maps kubernetes concepts to agent orchestration.
Resources are declared in TOML manifests, applied via `marvel apply`.

### Primitives

```
Kubernetes          Marvel                  What It Is
──────────          ──────                  ──────────

Namespace           Workspace               Isolation boundary. A project, team,
                                            or environment. Scopes all resources.

Pod                 Session                 The atomic unit. A tmux pane running
                                            one BYOA console process. Has a
                                            lifecycle: pending → running →
                                            succeeded/failed. Restartable.

Container           Runtime                 The BYOA console binary. aclaude,
                                            zclaude, dclaude, pennyfarthing,
                                            bare `claude` CLI, or any agent
                                            that accepts a prompt on stdin.
                                            Runtime images are just paths to
                                            executables + their config.

Deployment          Team                    Desired state: "run N sessions of
                                            this runtime with this config."
                                            Handles scaling, rolling updates,
                                            shift changes. A team of 3 reviewers
                                            is a Team with replicas: 3.

Service             Endpoint                A stable name for an agent capability.
                                            "the-reviewer" resolves to whichever
                                            session currently holds that role.
                                            Enables director to route by role,
                                            not by session ID.

CronJob             Schedule                Timed agent tasks. "Run a code review
                                            agent every 2 hours." "Shift change
                                            at 06:00 UTC." Creates Sessions on
                                            the cron schedule.

ConfigMap           Pack                    Content packs (spectacle, bmad, etc.)
                                            mounted into sessions. Packs provide
                                            commands, templates, themes, workflows.
                                            4-scope resolution: repo → shared →
                                            user → system.

Secret              Vault                   Auth delegation references. Marvel
                                            never stores credentials. Vaults
                                            point to where auth lives (Claude
                                            Code OAuth, API keys in keychain,
                                            Bedrock/Vertex config). Sessions
                                            inherit vault references at launch.

PVC                 Volume                  Workspace storage for a session.
                                            Git worktrees, sandboxes, shared
                                            directories. A volume can be:
                                            - worktree: git worktree (isolated
                                              branch, auto-cleaned or kept)
                                            - sandbox: temp directory (destroyed
                                              on session teardown)
                                            - shared: mounted read-write across
                                              sessions (coordination artifacts)
                                            - host: bind-mount of a host path

Probe (liveness)    Healthcheck             Is the session alive? Is the process
                                            running? Is the tmux pane responsive?
                                            Marvel restarts sessions that fail
                                            health checks.

Probe (readiness)   Readycheck              Is the session ready to accept work?
                                            Has the agent loaded its context,
                                            packs, and persona? Marvel doesn't
                                            route work until readycheck passes.

Ingress             Gateway                 External access to the agent cluster.
                                            Three types:
                                            - switchboard: remote tmux access
                                              (KVM for agent sessions)
                                            - director: inter-agent supervisor
                                              protocol (internal routing)
                                            - gateway: external API/webhook
                                              interface (not yet designed)

Node                Host                    A machine running marvel. Local host
                                            by default. Remote hosts reachable
                                            via switchboard. Marvel schedules
                                            sessions to available hosts.
```

### Resource Manifests (TOML)

```toml
# example: a team of 3 reviewer agents

[workspace]
name = "acme-project"

[[team]]
name = "reviewers"
replicas = 3

  [team.runtime]
  image = "aclaude"                # or path: "/usr/local/bin/aclaude"
  args = ["--persona", "dune/reviewer"]

  [team.readycheck]
  type = "prompt-response"         # agent responds to a ping
  interval = "30s"
  timeout = "10s"

  [team.healthcheck]
  type = "process-alive"           # tmux pane has a running process
  interval = "15s"

  [[team.pack]]
  name = "spectacle"
  scope = "user"

  [[team.volume]]
  name = "code"
  type = "worktree"
  repo = "."
  branch = "review/{{.session.id}}"  # each reviewer gets its own branch

  [[team.volume]]
  name = "artifacts"
  type = "shared"
  path = ".marvel/artifacts/reviewers/"
```

## Architecture

```
cmd/marvel/                 Entry point

internal/
  api/                      Resource types (Workspace, Session, Team, etc.)
    types.go                Core type definitions
    manifest.go             TOML manifest parsing and validation

  scheduler/                Session scheduling and placement
    scheduler.go            Assign sessions to hosts/panes
    reconciler.go           Desired state → actual state reconciliation loop

  runtime/                  BYOA console runtime management
    runtime.go              Runtime interface (start, stop, attach)
    aclaude.go              aclaude-specific runtime
    claude.go               Bare claude CLI runtime
    generic.go              Generic runtime (any CLI that accepts stdin)

  session/                  Session lifecycle
    manager.go              Create, monitor, restart, teardown sessions
    health.go               Healthcheck and readycheck execution
    state.go                Session state machine (pending → running → done)

  tmux/                     tmux substrate
    session.go              tmux session/pane CRUD
    attach.go               Attach/detach/send-keys
    capture.go              Capture pane output for monitoring

  team/                     Team (deployment) controller
    controller.go           Reconcile desired replicas vs actual
    scaling.go              Scale up/down, shift changes
    rolling.go              Rolling updates (new config without downtime)

  pack/                     Content pack management
    registry.go             Pack discovery, versioning
    router.go               Artifact type → filesystem routing
    scope.go                4-scope resolution (repo → shared → user → system)
    manifest.go             pack.yaml parsing

  volume/                   Workspace storage
    worktree.go             Git worktree create/cleanup
    sandbox.go              Temp directory lifecycle
    shared.go               Shared volume management

  config/                   Configuration resolution
    resolve.go              Merge chain: defaults → pack → scope → override
    vault.go                Auth delegation references

  gateway/                  External access
    switchboard.go          Switchboard integration (remote tmux)
    director.go             Director integration (inter-agent comms)

  otel/                     Observability
    collector.go            OTEL span/metric collection from sessions
    export.go               Forward to self-hosted collector or stdout
    metrics.go              Marvel-level metrics (sessions, restarts, health)

  protocol/                 Agent communication protocol
    message.go              Message types (task, result, heartbeat, signal)
    transport.go            Transport interface
    fifo.go                 Named pipe transport (local, simple)
    tmux.go                 tmux send-keys transport (fallback)
    switchboard.go          Switchboard relay transport (remote)
```

## Process Management

Marvel manages agent processes through the tmux substrate:

**Start:** create tmux pane → set environment → exec runtime binary.
**Stop:** send SIGTERM → wait grace period → SIGKILL → destroy pane.
**Restart:** stop + start. Preserves the session ID and volume mounts.
**Health:** periodic healthcheck (process alive) + readycheck (agent responsive).
**Auto-restart:** if healthcheck fails and restart policy allows, restart the session.

Restart policies: `always`, `on-failure`, `never`.

## Agent Communication Protocol

Sessions communicate via a message protocol. Three transports, same messages:

| Transport | Use Case | Latency | Reliability |
|-----------|----------|---------|-------------|
| Named pipes (FIFO) | Local sessions, same host | Low | High |
| tmux send-keys | Fallback, any tmux pane | Medium | Medium |
| Switchboard relay | Remote sessions, cross-host | Higher | High |

Message types:
- **task** — work assignment from coordinator to session
- **result** — work product from session to coordinator
- **heartbeat** — periodic liveness signal
- **signal** — control messages (pause, resume, shutdown, reconfigure)

Director provides higher-level supervisor patterns on top of this protocol.

## Observability

Sessions export OTEL telemetry. Marvel collects and routes it.

**Session-level signals:**
- Traces: tool executions, API calls, agent reasoning spans
- Metrics: token usage, tool counts, session duration, error rates
- Logs: agent output, structured events

**Cluster-level signals (marvel itself):**
- Sessions running/pending/failed per workspace
- Restart counts, health check failures
- Pack resolution times, volume mount times
- Scheduling decisions and queue depth

Export targets: self-hosted OTEL collector, stdout (dev mode), or disabled.
Telemetry is always available, never mandatory (per SOUL.md §6).

## Content Pack Management

(Unchanged from previous design — see pack/ in architecture above.)

A pack is a git repo with a `pack.yaml` manifest. Marvel resolves packs
via the 4-scope chain and routes artifacts to the right locations for
the target runtime.

Pack operations:
- `marvel pack install <source> [--scope repo|shared|user|system]`
- `marvel pack list [--scope ...]`
- `marvel pack update [--all | <name>]`
- `marvel pack remove <name>`
- `marvel pack link <path> --scope shared --project <path>`
- `marvel pack resolve` — show resolved config for current scope chain

## CLI

```sh
marvel apply <manifest.toml>      # apply desired state
marvel get sessions                # list sessions
marvel get teams                   # list teams
marvel get packs                   # list installed packs
marvel describe session <id>      # detailed session info
marvel logs <session-id>           # stream session output
marvel attach <session-id>         # attach to tmux pane
marvel exec <session-id> <prompt>  # send a prompt to a running session
marvel scale <team> --replicas N   # scale a team
marvel restart <session-id>        # restart a session
marvel drain <host>                # gracefully move sessions off a host
marvel top                         # resource usage across cluster
marvel pack install ...            # pack management (see above)
```

## Conventions

- **Language:** Go. Entire codebase.
- **Config format:** TOML for manifests, plans, and pack config. Go flags/env for runtime.
- **Pack manifest:** `pack.yaml` at pack root (YAML for ecosystem compatibility).
- **Auth:** Delegates to BYOA console → Claude Code. Marvel never stores credentials.
  Auth boundary: one user running their own agents under their own credentials
  (Max, API key, Bedrock, Vertex) is permitted. Orchestrating agents that route
  other people's consumer credentials is not — multi-user distribution requires
  API key auth. See SOUL.md §3.
- **No file deletion:** Never delete user files. Overwrite only with explicit intent.
- **Parallel-safe:** Each session gets a UUID. Volumes provide isolation.
- **Session substrate:** tmux. Panes = sessions. Sessions = agent processes.

## Independence and Coupling

Marvel enhances every other component but requires none of them.

**Marvel requires only:** Go, tmux, and a BYOA console binary (any CLI that
accepts a prompt on stdin). Everything else is optional integration.

**Integration tiers:**

| Component | Without It | With It |
|-----------|-----------|---------|
| aclaude | Marvel uses bare `claude` or any CLI. Process management only. | Deep integration: personas, OTEL, packs, hooks, full metrics. |
| switchboard | Local-only scheduling. All sessions on one host. | Remote hosts. Distributed fleet. Cross-machine attach. |
| director | No inter-agent comms. Fan-out/collect only. | Supervisor patterns, agent-to-agent routing, role-based endpoints. |
| spectacle | No spec commands in packs. User loads manually. | IEEE-based spec templates available as a pack. |
| kos | No knowledge projection. Specs are manual. | Specs projected from knowledge graph into sessions. |

**Marvel is also optional to everything else:**
- aclaude runs standalone without marvel (single-agent, own config chain)
- switchboard relays any tmux session, not just marvel-managed ones
- spectacle installs with `just install <target>`, no marvel needed
- kos operates its own probe/finding cycle independently

**Graceful degradation, not hard dependencies.** Marvel detects what's
available at startup and adjusts its capabilities. No switchboard binary?
Local-only mode. No director? No inter-agent routing. No packs installed?
Sessions launch with console defaults.

## Design Principles

1. **Declarative desired state** — you declare what you want running; marvel reconciles
2. **Agents are processes** — each agent is a tmux pane running a BYOA console
3. **Manifests are data** — TOML files, not code. Diffable, reviewable, versionable.
4. **Configuration is resolved, not assumed** — full scope chain before launch
5. **Packs are git repos** — versioned, diffable, shareable. No proprietary format.
6. **Fail observable** — every session's output is visible; marvel logs all state transitions
7. **Gradual elaboration** — start with `marvel apply` for a single session, grow to fleet management
8. **Console-agnostic** — works with aclaude, zclaude, dclaude, or any CLI that accepts a prompt
9. **No conscription** — marvel orchestrates, it does not require. Every integration is optional.
