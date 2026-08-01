# Architecture Overview

Marvel is a control plane for AI agent workloads. It manages the lifecycle
of agent sessions running in tmux panes — starting, stopping, health-checking,
shifting, and scaling them according to declarative manifests.

## Design principles

1. **Agents are processes.** Each agent is a tmux pane running a harness
   (claude, codex, or opencode are the live targets; forestage is retired to
   reference; the generic fallback takes any command that accepts input on
   stdin). The manifest field `runtime` names the harness, never the agent.
2. **Declarative desired state.** You write a manifest declaring what you want
   running. Marvel reconciles actual state to match.
3. **Harness-agnostic.** Marvel orchestrates any agent CLI. The runtime
   adapter framework handles the differences.
4. **Infrastructure, not workflow.** Marvel manages processes and the substrate
   around them. It does not understand what agents are working on, decompose
   tasks, or know what "done" means. Supervisors (which are themselves agents)
   make those decisions.

## Layers

```
┌──────────────────────────────────────┐
│ agents (claude, codex, opencode,     │
│   any CLI via the generic adapter)   │
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
| Container image | **Runtime** | The harness binary + args. Names the harness, never the agent. |
| Deployment | **Team** | A group of agents with heterogeneous roles. |
| (none) | **Role** | One kind of agent within a team (supervisor, worker). |
| Service | **Endpoint** | Stable name for a session capability. |
| ConfigMap | **Policy** | Named Claude Code settings fragment marvel projects into a per-session file the harness reads. Scoped to a workspace, referenced by `Role.Policy`. |
| Node | **Host** | A machine running marvel (local by default). |

Resources are declared in YAML or TOML manifests and applied with `marvel work`.

## Runtime adapters

When marvel launches a session, it resolves a runtime adapter based on the
`image` field in the manifest. Each adapter knows how to construct the
execution environment for its runtime type.

Six adapters are registered. `claude`, `codex`, and `opencode` are the live
harness targets; `generic` is the fallback; `simulator` serves load tests;
`forestage` is frozen as the deep-integration reference.

| Adapter | Triggered by | What it does | Stream | Projection |
|---------|-------------|--------------|--------|------------|
| **claude** | `image: claude` | Injects --settings for the projected policy, --permission-mode from the role, and --append-system-prompt with the session identity unless the manifest already supplies one. Headless adds --print --output-format stream-json --verbose and the prompt as the positional argument. | headless only | yes |
| **codex** | `image: codex` | Env-var identity only. Headless becomes `codex exec --json --skip-git-repo-check <prompt>` with stdin from /dev/null, since codex appends piped stdin to its prompt and would otherwise hang on the pane tty. | headless only | no |
| **opencode** | `image: opencode` | Env-var identity only. Headless becomes `opencode run --format json <message>`, stdin likewise closed. | headless only | no |
| **forestage** | `image: forestage` | The deepest surface: --persona, --identity, --role, --name, --workspace, --team, --socket, --permission-mode, --dangerously-skip-permissions, --script, then --settings and --append-system-prompt after the `--` claude passthrough. Retired to reference; the adapter stays as the deep-integration contract's reference implementation. | no | yes |
| **simulator** | `image: simulator` | Injects the identity flags the simulator's heartbeat path needs (--name, --workspace, --team, --role, --socket, --script). Without them a simulator launched through the generic fallback never heartbeats and restart-loops under `restart_policy = always`. | no | no |
| **generic** | Any other image | Passes command + args through unchanged. Env vars only (MARVEL_SESSION, MARVEL_ROLE, and the rest). Pane content is read on demand with `marvel capture`; `GenericScraper` (a capture-pane history scrape with a per-session high-water mark) is written and unit-tested but has no production call site yet. | no | no |

Stream capability is `SupportsStream`, which returns true only when
`Runtime.Mode` is headless: an interactive harness renders a TUI to the pane
and has no structured output to redirect. A stream-capable session's output
is redirected into a named pipe that the daemon parses (finding-005).
Projection is `ProjectionFor(ctx, dir).Supported`.

### Three permission mechanisms, not one

1. **`Role.Permissions`** maps to the harness's `--permission-mode` and
   selects how it prompts within its cooperative permission model. The
   claude and forestage adapters inject it; codex, opencode, simulator,
   and generic ignore it.
2. **`Role.DangerousPermissions`** appends
   `--dangerously-skip-permissions`, removing that model. Orthogonal to
   the first: an operator picks one, the other, or both. Only the
   forestage adapter honors it today, so on a claude session the field is
   currently inert.
3. **Policy projection** is the file-based mechanism.
   `Adapter.ProjectionFor` reports where the file goes,
   `internal/session/projection.go` writes the settings JSON, and
   `LaunchContext.PolicyProjectionPath` is what the adapter points the
   harness at via `--settings`. Re-projection happens live on re-apply,
   emitting a `policy.projected` event, so a running agent's contract can
   change without a restart. Harnesses that support no projection (codex,
   opencode, simulator, generic) have the referenced policy logged as
   advisory rather than silently dropped.

Enforcement is spawn-time environment construction. Real containment
belongs to curtain.

## Reconciliation loop

The team controller runs a reconciliation loop every 2 seconds:

1. **Reap dead sessions.** Remove sessions whose tmux panes no longer exist.
   `ReapDead` marks the transition state `crashed` first, so a dead process
   is distinguishable from a drained one.
2. **Evaluate health.** Check heartbeat staleness, apply restart policies.
   Repeated restarts move a session to `crashloop-backoff` with its pane
   left alive, so the state stays visible while the reconciler holds off.
   At `Role.MaxRestarts` the reconciler stops and leaves the session
   `failed`.
3. **Reconcile each team.** For each role, compare desired replicas vs
   actual, create or delete sessions to match.
4. **Process shifts.** If a shift is in progress, manage the rolling
   replacement. A shift that cannot finish inside `ShiftTimeout` (default
   10 minutes) is aborted and rolled back rather than left half-drained.
5. **Write crash-loop state through.** Every crash the reap path or the
   health path records goes to the bolt `role_health` bucket, the one bucket
   with no in-memory mirror, so the restart counter and the backoff deadline
   are durable as they change.

Recovery is separate: it runs once at daemon start, not on the loop.
`RehydrateRoleHealth` loads the persisted crash-loop state before the
reconciler starts, so a role frozen at `MaxRestarts` stays held back across a
restart instead of getting a free respawn. `AdoptOrKill` then reconciles the
live tmux panes against the rehydrated store, which is what lets agents
survive the daemon.

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
  api/                      Resource types + manifest parsing (YAML/TOML) +
                            Store (L1 in-memory snapshot boundary) +
                            bolt.go (L2 bbolt durable record, optional via OpenBolt)
  config/                   Client cluster configuration (~/.marvel/config.yaml)
  daemon/                   Daemon, RPC handlers, embedded SSH server
  runtime/                  Runtime adapter framework (six adapters + parsers)
  session/                  Session lifecycle (create, delete, reap, policy projection)
  team/                     Team controller (reconciler, shifts, health, crash-loop)
  tmux/                     tmux driver (subprocess, capture, send-keys)
  procstat/                 Per-session CPU/memory rollup over a pid subtree
  usage/                    Context-window/token accountant (stream-fed CTX% producer)
  events/                   Bounded event ring (control-plane + 12 agent.* kinds)
  logbuf/                   Daemon log ring behind `marvel daemon logs`
  rlog/                     Structured request/run logging
  keys/                     SSH client keypair management (~/.marvel/keys)
  knownhosts/               Host-key trust (~/.marvel/known_hosts)
  paths/                    ~/.marvel/ path resolution
  otel/                     34-line OTEL producer stub, imported only by cmd/simulator
  simulator/                Context pressure simulator + Lua scripting
  upgrade/                  Self-update (Homebrew detection, GitHub releases)
```

Persistence is two levels. The in-memory `Store` is L1 and remains the read
path; `bolt.go` is the L2 durable record behind it, a bbolt write-ahead over
the buckets `workspaces`, `teams`, `sessions`, `endpoints`, `policies`,
`role_health`, and `meta`. `boltSchemaVersion` is 1 and a higher on-disk
version is refused rather than migrated blind. `OpenBolt` rehydrates the
in-memory store; a nil bolt handle means in-memory only, which is still a
supported mode.

`internal/events` is the bounded event ring: severity-tagged records
filterable by workspace, team, role, session, and kind, served to
`marvel events`. It carries both control-plane kinds (including
`policy.projected` and `context.limit-unresolved`) and the 12 `agent.*`
kinds. Those twelve are declared
twice under two names: the stream parsers emit the unprefixed vocabulary in
`internal/runtime/events` (`session.started`, `tool.call`, and the rest),
and `internal/session/bridge.go` lifts each one into its `agent.*` ring
kind, which is what lets the daemon and the CLI see one vocabulary
regardless of harness. The ring is bounded, so it is history for the life
of the daemon process, not durable storage.

## Manifest formats

Marvel accepts both YAML (default) and TOML manifests. YAML uses natural
plural keys (`teams`, `roles`, `endpoints`). TOML uses singular keys
(`team`, `role`, `endpoint`) per its array-of-tables syntax.

File extension determines the parser: `.yaml`/`.yml` → YAML, `.toml` → TOML.
