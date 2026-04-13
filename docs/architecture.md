# Architecture Overview

Marvel is a control plane for AI agent workloads. It manages the lifecycle
of agent sessions running in tmux panes — starting, stopping, health-checking,
shifting, and scaling them according to declarative manifests.

## Design principles

1. **Agents are processes.** Each agent is a tmux pane running a BYOA console
   (forestage, bare claude CLI, or any command that accepts input on stdin).
2. **Declarative desired state.** You write a manifest declaring what you want
   running. Marvel reconciles actual state to match.
3. **Console-agnostic.** Marvel orchestrates any BYOA console. The runtime
   adapter framework handles the differences.
4. **Infrastructure, not workflow.** Marvel manages processes and the substrate
   around them. It does not understand what agents are working on, decompose
   tasks, or know what "done" means. Supervisors (which are themselves agents)
   make those decisions.

## Layers

```
┌──────────────────────────────────────┐
│ agents (forestage, claude, any CLI)  │
│   personas, work, decisions, prompts │
└─────────────────┬────────────────────┘
                  │
┌─────────────────▼────────────────────┐
│ marvel control plane                 │
│   identity (workspaces, teams,       │
│     roles, sessions)                 │
│   scheduler / reconciler             │
│   runtime adapters                   │
│   healthcheck / shift lifecycle      │
│   mrvl:// remote access              │
│   capture / inject primitives        │
└─────────────────┬────────────────────┘
                  │
┌─────────────────▼────────────────────┐
│ substrate                            │
│   tmux (panes), processes,           │
│   filesystem, network                │
└──────────────────────────────────────┘
```

## Resource model

Marvel's resource model maps kubernetes concepts to agent orchestration.

| K8s concept | Marvel resource | What it is |
|-------------|-----------------|------------|
| Namespace | **Workspace** | Isolation boundary. A project or environment. |
| Pod | **Session** | Atomic unit. A tmux pane running one agent process. |
| Container image | **Runtime** | The BYOA console binary + args. |
| Deployment | **Team** | A group of agents with heterogeneous roles. |
| (none) | **Role** | One kind of agent within a team (supervisor, worker). |
| Service | **Endpoint** | Stable name for a session capability. |
| Node | **Host** | A machine running marvel (local by default). |

Resources are declared in YAML or TOML manifests and applied with `marvel work`.

## Runtime adapters

When marvel launches a session, it resolves a runtime adapter based on the
`image` field in the manifest. Each adapter knows how to construct the
execution environment for its runtime type.

| Adapter | Triggered by | What it does |
|---------|-------------|--------------|
| **forestage** | `image: forestage` | Injects identity flags (--name, --workspace, --team, --role, --socket), permission mode, script path. Deep integration. |
| **claude** | `image: claude` | Injects --permission-mode and --append-system-prompt with identity. Medium integration. |
| **generic** | Any other image | Passes command + args through unchanged. Env vars only (MARVEL_SESSION, MARVEL_ROLE, etc.). |

The adapter also implements permission-through-environment: it injects
`--permission-mode` from the role's `permissions` field in the manifest.
The agent reads permissions from files that marvel wrote.

## Reconciliation loop

The team controller runs a reconciliation loop every 2 seconds:

1. **Reap dead sessions** — remove sessions whose tmux panes no longer exist
2. **Evaluate health** — check heartbeat staleness, apply restart policies
3. **Reconcile each team** — for each role, compare desired replicas vs actual,
   create or delete sessions to match
4. **Process shifts** — if a shift is in progress, manage the rolling replacement

## Shift mechanics

A shift is a rolling replacement of agent sessions. State machine:

```
idle → ShiftLaunching (create new-gen sessions)
     → ShiftDraining (remove old-gen sessions one per tick)
     → idle (complete)
```

Roles shift sequentially, supervisor last. Each role's new-gen sessions must
be ready (running + heartbeat received if healthcheck configured) before
old-gen sessions are drained.

## Connection model

Marvel supports three connection modes for CLI-to-daemon communication:

```
/tmp/marvel.sock             Unix socket (local, default)
mrvl://host                  Embedded SSH server (remote, port 6785)
ssh://host/path/to/socket    Tunnel through system sshd (fallback)
```

The `mrvl://` protocol is the primary remote access mode. The daemon runs
its own SSH server, generates its own host key, and manages its own
authorized keys. No dependency on sshd.

### Cluster configuration

Named clusters are stored in `~/.marvel/config.yaml`:

```yaml
clusters:
  - name: local
    socket: /tmp/marvel.sock
  - name: kinu
    server: mrvl://michael@kinu
  - name: staging
    server: mrvl://deploy@staging.example.com:7000

current_cluster: local
```

The CLI resolves connections in priority order:
`--socket` (explicit) > `--cluster` (named) > `current_cluster` > local default.

## Daemon architecture

```
cmd/marvel/                 CLI entry point (cobra)
internal/
  api/                      Resource types + manifest parsing (YAML/TOML)
  config/                   Client cluster configuration (~/.marvel/config.yaml)
  daemon/                   Daemon, RPC handlers, embedded SSH server
  runtime/                  Runtime adapter framework (forestage/claude/generic)
  session/                  Session lifecycle (create, delete, reap)
  team/                     Team controller (reconciler, shifts, health)
  tmux/                     tmux driver (subprocess, capture, send-keys)
  otel/                     Observability (OTEL metrics)
  simulator/                Context pressure simulator + Lua scripting
  upgrade/                  Self-update (Homebrew detection, GitHub releases)
```

## Manifest formats

Marvel accepts both YAML (default) and TOML manifests. YAML uses natural
plural keys (`teams`, `roles`, `endpoints`). TOML uses singular keys
(`team`, `role`, `endpoint`) per its array-of-tables syntax.

File extension determines the parser: `.yaml`/`.yml` → YAML, `.toml` → TOML.
