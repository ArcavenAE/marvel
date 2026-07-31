# Marvel — Agent Orchestration Control Plane

@.claude/rules/_index.md

## What This Is

> Note: the BYOA console formerly known as "aclaude" is now named "forestage".
> References below have been updated. Historical GitHub issue references
> (ArcavenAE/aclaude#N) are preserved as-is.

A kubernetes-like control plane for AI agent workloads. Written in Go.
Manages the full lifecycle of BYOA agent sessions: scheduling, configuration,
process management, health monitoring, storage, networking, and observability.

Where kubernetes orchestrates containers across nodes, marvel orchestrates
agent sessions across tmux panes — local or remote via switchboard.

## Build / Run / Test

Requires: Go 1.25+, `just`, `tmux`.

```sh
just build          # go build ./cmd/...
just test           # go test ./...
just lint           # golangci-lint run ./...
just fmt            # gofumpt formatting
```

## Resource Model

Marvel's resource model maps kubernetes concepts to agent orchestration.
Resources are declared in TOML manifests, loaded via `marvel work`.

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

Container           Runtime                 The BYOA console binary. forestage,
                                            zclaude, dclaude, pennyfarthing,
                                            bare `claude` CLI, or any agent
                                            that accepts a prompt on stdin.
                                            Runtime images are just paths to
                                            executables + their config.

Deployment          Team                    A cohesive unit of agents with
                                            heterogeneous roles. Each role has
                                            its own runtime and replica count.
                                            A team binds a supervisor to its
                                            agents. Handles per-role scaling,
                                            rolling updates, shift changes.

(none)              Role                    One kind of agent within a team.
                                            Has a name, replica count, and
                                            runtime. "supervisor" (replicas: 1)
                                            and "worker" (replicas: 5) are
                                            roles within the same team.

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
# example: a review team with supervisor + reviewers

[workspace]
name = "acme-project"

[[team]]
name = "review-squad"

  [[team.role]]
  name = "supervisor"
  replicas = 1

    [team.role.runtime]
    image = "forestage"
    args = ["--persona", "dune/supervisor"]

  [[team.role]]
  name = "reviewer"
  replicas = 3

    [team.role.runtime]
    image = "forestage"
    args = ["--persona", "dune/reviewer"]

# future: readychecks, healthchecks, packs, volumes per role
```

## Architecture

This tree reflects the packages that exist today. Several resource
types in the model above (Pack, Volume, Gateway, Schedule) are not yet
implemented as their own packages; they live in the model, not the code.

```
cmd/marvel/                 CLI entry point (cobra commands) + table rendering

internal/
  api/                      Resource types + store
    types.go                Core type definitions (Workspace, Session, Team, ...)
    manifest.go             Manifest parsing and validation (TOML + YAML)
    store.go                Resource store interface
    bolt.go                 BoltDB-backed store (persistence across restarts)

  daemon/                   Daemon process + RPC surface
    daemon.go               Reconciliation loop, lifecycle, adopt-on-restart
    sshserver.go            SSH transport for remote clients
    metrics.go              Daemon-level metrics

  team/                     Team (deployment) controller
    controller.go           Reconcile desired replicas vs actual; shifts, health

  session/                  Session lifecycle
    manager.go              Create, monitor, restart, teardown sessions
    bridge.go               Stream bridge between agent runtime and daemon

  runtime/                  BYOA console runtime adapters (the adapter framework)
    adapter.go              Runtime adapter interface + Instance layer
    forestage.go            forestage adapter (deep integration)
    claude.go               Bare claude CLI adapter
    generic.go              Generic adapter (any CLI that accepts stdin)
    instance.go             Running-instance abstraction
    instance_tmux.go        tmux-backed instance
    fifo.go                 Named-pipe cooperative stream
    stream.go               Stream observation
    claudecode/             Claude Code stream-json parser + event mapping
    codex/                  Codex adapter notes
    opencode/               opencode adapter notes
    events/                 Runtime → director event envelopes

  tmux/                     tmux substrate
    driver.go               tmux session/pane CRUD, send-keys, capture-pane

  procstat/                 Per-session process stats (CPU%, RSS) via pid-subtree sampler
    procstat.go, proc.go, ps.go
    read_darwin.go, read_linux.go, read_other.go   platform samplers

  events/                   Structured event ring (control-plane + agent.* events)
  keys/                     SSH client keypair management (~/.marvel/keys)
  knownhosts/               Host-key trust (~/.marvel/known_hosts)
  config/                   Named-cluster config (~/.marvel/config.yaml)
  paths/                    ~/.marvel/ path resolution
  logbuf/                   In-memory log buffer
  rlog/                     Structured request/run logging
  otel/                     Observability metrics
  upgrade/                  Self-upgrade (Homebrew / direct binary)
  simulator/                Context-pressure simulation for testing without real agents
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
marvel work <manifest.toml>                          # load manifest, reconcile desired state
marvel get sessions                                  # list sessions (add -w for watch mode)
marvel get teams                                     # list teams and roles
marvel get workspaces                                # list workspaces
marvel describe session <id>                         # detailed session info
marvel scale <workspace/team> --role <r> --replicas N  # scale a role within a team
marvel shift <workspace/team> [--role <r>]             # rolling shift (replace sessions with fresh ones)
marvel run <command> [args...] --role <r>             # run a one-off agent session
marvel kill <session-key>                            # kill a session
marvel stop                                          # stop daemon, agents keep running (restart adopts them)
marvel stop --teardown                               # stop daemon, delete sessions, kill panes
marvel daemon                                        # start the daemon (foreground)
marvel events                                        # structured event ring (control-plane + agent.*)
marvel capture <session-key>                         # capture a session's pane content
marvel inject <session-key> <keys>                   # send keystrokes to a pane
marvel config <add-cluster|list|current|use-cluster|remove-cluster>   # named-cluster config
marvel keys <generate|show|list|trust|authorize|authorized|revoke|doctor>  # SSH client keys
marvel version                                       # print version and channel
marvel upgrade                                        # self-upgrade

# future: logs, attach, exec, drain, top, pack management
```

The `logs` verb is not yet implemented; `marvel events` covers the agent
observability surface today (see charter B8 for the runtime adapter
framework that feeds it).

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
| forestage | Marvel uses bare `claude` or any CLI. Process management only. | Deep integration: personas, OTEL, packs, hooks, full metrics. |
| switchboard | Local-only scheduling. All sessions on one host. | Remote hosts. Distributed fleet. Cross-machine attach. |
| director | No inter-agent comms. Fan-out/collect only. | Supervisor patterns, agent-to-agent routing, role-based endpoints. |
| spectacle | No spec commands in packs. User loads manually. | IEEE-based spec templates available as a pack. |
| kos | No knowledge projection. Specs are manual. | Specs projected from knowledge graph into sessions. |

**Marvel is also optional to everything else:**
- forestage runs standalone without marvel (single-agent, own config chain)
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
8. **Console-agnostic** — works with forestage, zclaude, dclaude, or any CLI that accepts a prompt
9. **No conscription** — marvel orchestrates, it does not require. Every integration is optional.

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
