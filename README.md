# Marvel — Agent Orchestration Control Plane

A kubernetes-like control plane for AI agent workloads. Manages the full
lifecycle of BYOA (Bring Your Own Agent) sessions: scheduling, configuration,
process management, health monitoring, and rolling shifts.

Where kubernetes orchestrates containers across nodes, marvel orchestrates
agent sessions across tmux panes.

## Quick Start

```sh
# Build
just build

# Start the daemon
marvel daemon &

# Load a manifest
marvel work examples/demo.toml

# Watch sessions
marvel get sessions -w

# Trigger a rolling shift (replace all sessions with fresh ones)
marvel shift demo/squad

# Clean up
marvel stop
```

## Resource Model

```
Workspace     isolation boundary (k8s namespace)
  └─ Team     cohesive unit of agents (k8s deployment)
       └─ Role     one kind of agent: name, replicas, runtime, healthcheck
            └─ Session    one agent process in a tmux pane (k8s pod)
```

A team contains heterogeneous roles. A review team might have 1 supervisor,
3 reviewers, and 1 architect — each with its own runtime, replica count,
restart policy, and healthcheck config.

## Manifests

Resources are declared in TOML manifests:

```toml
[workspace]
name = "my-project"

[[team]]
name = "review-squad"

  [[team.role]]
  name = "reviewer"
  replicas = 3

    [team.role.runtime]
    command = "/usr/local/bin/forestage"
    args = ["--persona", "dune/reviewer"]

    [team.role.healthcheck]
    type = "heartbeat"
    timeout = "30s"
    failure_threshold = 3

  [[team.role]]
  name = "supervisor"
  replicas = 1
  restart_policy = "always"

    [team.role.runtime]
    command = "/usr/local/bin/forestage"
    args = ["--persona", "dune/supervisor"]
```

## CLI

```sh
marvel daemon                                        # start the daemon
marvel work <manifest.toml>                          # load manifest
marvel get sessions                                  # list sessions (-w for watch mode)
marvel get teams                                     # list teams and roles
marvel get workspaces                                # list workspaces
marvel describe session <key>                        # session details
marvel scale <ws/team> --role <r> --replicas N       # scale a role
marvel shift <ws/team> [--role <r>]                  # rolling shift
marvel run <cmd> [args...] --role <r>                # one-off session
marvel kill <session-key>                            # kill a session
marvel stop                                          # stop daemon
```

## Shifts

Shifts are rolling replacements of agent sessions. Agent sessions accumulate
context, drift, and stale mental models. A shift starts fresh sessions,
verifies they're running, then drains the old ones — preserving team identity.

```sh
# Shift all roles (workers first, supervisor last)
marvel shift my-project/review-squad

# Shift only one role
marvel shift my-project/review-squad --role reviewer
```

Sessions track their generation (visible in `marvel get sessions` as the GEN
column). During a shift, old-generation sessions drain one per reconciliation
tick while new-generation sessions are already running.

## Healthchecks

Roles can configure healthchecks. Currently supported: `heartbeat` (agent must
send periodic heartbeats to the daemon) and `process-alive` (tmux pane exists).

```toml
[team.role.healthcheck]
type = "heartbeat"       # "heartbeat" or "process-alive"
timeout = "30s"          # heartbeat staleness threshold
failure_threshold = 3    # consecutive failures before action
```

Restart policies control what happens when a session is unhealthy:
- `always` (default): delete and recreate
- `on-failure`: delete and recreate only if failed
- `never`: mark failed, don't restart

Sessions without a configured healthcheck stay in `unknown` health state
and are never restarted by health evaluation.

## Examples

| Manifest | What it shows |
|----------|---------------|
| `examples/demo.toml` | 3 agents + chaos supervisor with healthchecks |
| `examples/demo2.toml` | 5 agents + chaos supervisor (larger team) |
| `examples/review-team.toml` | Multi-role team: reviewers, architect, supervisor |
| `examples/shift-demo.toml` | Minimal team for demonstrating shifts |
| `examples/claude.toml` | Real Claude Code agents (requires claude CLI) |

## Just Recipes

```sh
just build          # build marvel + simulator
just test           # run all tests
just demo           # load demo manifest, show state
just demo-shift     # demonstrate shift lifecycle
just watch          # watch sessions (interactive)
just shift <team>   # trigger a shift
just scale <t> <r> <n>  # scale a role
just start          # start daemon (foreground)
just stop           # stop daemon
just clean          # kill tmux sessions, remove binaries
```

## Architecture

Written in Go. The daemon manages agent sessions through a tmux substrate:

- **Reconciliation loop** (2s interval): compares desired state (manifests)
  with actual state (running sessions), creates/deletes to match
- **Health evaluation**: checks heartbeat staleness, applies restart policies
- **Shift state machine**: launching → draining → complete, driven by
  the reconciliation loop
- **Simulator**: context pressure simulation for testing without real agents

## BYOA

Marvel works with any BYOA console that accepts a prompt on stdin:
forestage, zclaude, dclaude, bare `claude` CLI, or custom agents.
The runtime is just a command path + args. Marvel doesn't care what
the agent is — it manages the process lifecycle.

## Requirements

- Go 1.22+
- tmux
- `just` (command runner)
