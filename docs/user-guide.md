# User Guide

This guide covers daily use of marvel — writing manifests, managing sessions,
and interacting with running agents.

## Quick start

```bash
# Start the daemon (local only)
marvel daemon &

# Apply a manifest
marvel work examples/claude.yaml

# See what's running
marvel get sessions

# Stop the daemon, leaving agents running (restart adopts them)
marvel stop

# Or end the agents too
marvel stop --teardown
```

## Writing manifests

A manifest declares the desired state: which agents to run, how many,
and how to configure them. Marvel reconciles actual state to match.

### Minimal manifest

```yaml
workspace:
  name: dev

teams:
  - name: squad
    roles:
      - name: worker
        replicas: 2
        runtime:
          image: claude
          command: claude
```

This launches 2 instances of the bare Claude Code CLI in a workspace
called "dev", managed as a team called "squad".

**When to use:** Getting started, single-agent experiments, running
a few Claude instances side by side.

### With permissions and personas

```yaml
workspace:
  name: review

teams:
  - name: reviewers
    roles:
      - name: supervisor
        replicas: 1
        permissions: auto
        runtime:
          image: forestage
          command: forestage
          args: ["--persona", "dune/supervisor"]

      - name: worker
        replicas: 3
        permissions: plan
        runtime:
          image: forestage
          command: forestage
```

The `permissions` field controls what the Claude Code agent can do.
Marvel injects this as `--permission-mode` at launch time. Workers get
`plan` (must approve tool calls), the supervisor gets `auto` (autonomous).

**Valid permission modes.** `permissions` must be one of Claude Code's
`--permission-mode` values, or empty. Marvel rejects anything else at
`marvel apply` time (a typo like `pln` no longer produces a pane that
exits immediately). The canonical set:

| Mode | Meaning |
|------|---------|
| `acceptEdits` | Auto-accept file edits, prompt for other tools |
| `auto` | Run autonomously |
| `bypassPermissions` | Auto-allow within the permission model |
| `default` | Standard interactive prompting |
| `dontAsk` | Suppress prompts |
| `plan` | Plan first, approve before acting |

An empty `permissions` means "unset" — the adapter's own default applies.

**`dangerous_permissions` is separate.** The boolean `dangerous_permissions:
true` appends `--dangerously-skip-permissions`, which removes the permission
model entirely rather than choosing a mode within it. It is orthogonal to
`permissions` and combines with any mode. Pick `permissions: bypassPermissions`
to keep the harness's permission machinery engaged (still auditable, still
hookable) while auto-allowing within it; pick `dangerous_permissions: true`
for autonomous fleet agents where no approver exists and enforcement is
delegated to a sandbox (curtain).

**When to use:** Multi-agent teams where different roles need different
trust levels. The supervisor can execute freely; workers ask before acting.

### With health checks

```yaml
workspace:
  name: prod

teams:
  - name: agents
    roles:
      - name: worker
        replicas: 5
        restart_policy: always
        runtime:
          image: claude
          command: claude
        healthcheck:
          type: heartbeat
          timeout: "30s"
          failure_threshold: 3
```

Agents that don't send a heartbeat within 30 seconds are marked unhealthy.
After 3 consecutive failures, `restart_policy: always` triggers a restart.

**When to use:** Long-running agents that need automatic recovery. The
health check ensures stuck agents get replaced without manual intervention.

### Mixed runtimes

```yaml
workspace:
  name: mixed

teams:
  - name: hybrid
    roles:
      - name: supervisor
        replicas: 1
        permissions: auto
        runtime:
          image: claude
          command: claude

      - name: worker
        replicas: 2
        permissions: plan
        runtime:
          image: forestage
          command: forestage

      - name: monitor
        replicas: 1
        runtime:
          image: shell
          command: sh
```

Different roles can use different runtimes. Each resolves to its own
adapter (claude, forestage, generic). They coexist in the same team.

**When to use:** Heterogeneous teams where the supervisor runs bare
Claude Code, workers run forestage with personas, and a monitor runs
a shell script for health scraping.

## Managing sessions

### List sessions

```bash
marvel get sessions
```

Output:
```
WORKSPACE  TEAM       ROLE    GEN  NAME                   STATE    HEALTH   CTX%  CPU%   RSS   DESK  AGENT
dev        squad      worker  1    squad-worker-g1-0      running  unknown  -     99.9   412M  1     claude
dev        squad      worker  1    squad-worker-g1-1      running  unknown  -     0.4    380M  2     claude
```

`CPU%` and `RSS` come from a sampler that runs every 5 seconds and rolls
up each agent's whole process subtree, not just the process tmux started
(that one is a shell). CPU% is per core, so a session using two cores
reads about 200.

A `-` means the value has never been measured, which is not the same as
zero:

- `CTX%` is `-` until the agent sends a heartbeat. No runtime adapter
  implements the heartbeat yet, so today only the bundled simulator
  reports context pressure.
- `CPU%` and `RSS` are `-` before the first sampler pass, and on
  platforms where marvel has no process-table reader (anything other
  than macOS and Linux).

Per-process IO counters are read on Linux only; `marvel describe session`
reports `IOAvailable: false` elsewhere.

### Watch mode

```bash
marvel get sessions -w
```

Live-updating dashboard. Sort by pressing keys: `c` (context %), `p`
(CPU), `m` (memory), `n` (name), `r` (role), `g` (generation), `t`
(team). Press `h` for help, `q` to quit.

**When to use:** Monitoring a running team, watching shifts progress,
observing health state changes in real time.

### Capture pane content

```bash
marvel capture dev/squad-worker-g1-0
```

Returns the current visible content of the agent's tmux pane. This is
how you see what an agent is doing without attaching to its pane.

With scrollback:
```bash
marvel capture dev/squad-worker-g1-0 -S -100 -E 0
```

**When to use:** Checking on a specific agent's progress, debugging a
stuck agent, reviewing output without interrupting the agent.

### Inject keystrokes

```bash
marvel inject dev/squad-worker-g1-0 "review the auth module" -e
```

Sends text to the agent's pane as if typed at the keyboard. The `-e` flag
appends Enter. The `-l` flag (default: true) sends keys literally.

```bash
# Send without Enter (type but don't submit)
marvel inject dev/squad-worker-g1-0 "partial text"

# Send a special key
marvel inject dev/squad-worker-g1-0 "C-c" --literal=false
```

**When to use:** Giving an agent a task, interrupting a stuck agent,
sending Ctrl-C to stop a runaway process. This is the "executive
privilege" operation — you're typing into another agent's terminal.

## Scaling

```bash
marvel scale dev/squad --role worker --replicas 5
```

The reconciler creates or removes sessions to match the new count.
Scale down removes the newest sessions first.

**When to use:** Increasing capacity for a burst of work, scaling
down after a sprint, adjusting team composition.

## Shifts

A shift is a rolling replacement of all sessions with fresh ones.

```bash
# Shift the whole team (workers first, supervisor last)
marvel shift dev/squad

# Shift only one role
marvel shift dev/squad --role worker
```

Watch the shift in progress:
```bash
marvel get sessions -w
```

The GEN column increments. New-gen sessions launch, become ready,
then old-gen sessions drain one per reconciler tick.

**When to use:** Context windows are filling up. Agents have been
running long enough that their context is stale. A configuration
change needs to propagate. You want fresh agents without losing
the team structure.

## Connecting to remote daemons

### Named clusters (recommended)

```bash
# Add a remote cluster
marvel config add-cluster kinu mrvl://kinu
marvel config add-cluster staging mrvl://deploy@staging:7000

# List clusters
marvel config list

# Switch to a remote cluster
marvel config use-cluster kinu

# All commands now go to the remote daemon
marvel get sessions
marvel capture prod/squad-worker-g1-0
```

### Explicit cluster per command

```bash
marvel get sessions --cluster kinu
marvel capture prod/squad-worker-g1-0 --cluster staging
```

### Explicit address (advanced)

```bash
marvel get sessions --socket mrvl://kinu
marvel get sessions --socket /tmp/marvel-dev.sock
```

## Deleting resources

```bash
# Kill a specific session
marvel kill dev/squad-worker-g1-0

# Delete a team and all its sessions
marvel delete team dev/squad

# Delete a workspace and everything in it
marvel delete workspace dev
```

## Version and upgrade

```bash
marvel version                  # show version and channel
marvel upgrade                  # upgrade to latest
marvel upgrade --version v0.2.0 # pin to a specific version
```

If installed via Homebrew, `marvel upgrade` runs `brew upgrade` automatically.
