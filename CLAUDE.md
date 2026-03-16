# Marvel — Multiagent Orchestration System

## What This Is

Multiagent orchestration for BYOA agents. Spawns, coordinates, and supervises
multiple agent sessions — with or without director, switchboard, kos, and aclaude.

Comparable to multiclaude/gastown but built on the aae-orc platform principles:
tmux as session substrate, auth delegation, parallel safety, composability.

## Build / Run / Test

Requires: Go 1.22+, `just`, `tmux`.

```sh
just build          # go build ./cmd/...
just test           # go test ./...
just lint           # golangci-lint run ./...
just fmt            # gofumpt formatting
```

## Architecture

```
cmd/marvel/       Entry point
internal/
  agent/          Agent lifecycle (spawn, monitor, teardown)
  session/        tmux session and pane management
  plan/           Execution plan parsing and validation
  coordinator/    Fan-out work distribution and result collection
```

### Integration Points

- **aclaude / BYOA consoles** — spawns agents in tmux panes via CLI
- **switchboard** — optional remote access to agent sessions
- **director** — optional supervisor protocol for inter-agent comms
- **kos** — optional spec projection to seed agent context

All integrations are optional. Marvel works standalone with just tmux + a BYOA console.

## Conventions

- **Config format:** TOML for plan files; Go flags/env for runtime config.
- **Auth:** Delegates to the BYOA console (which delegates to Claude Code). No direct credential handling.
- **No file deletion:** Never delete user files. Overwrite only with explicit intent.
- **Parallel-safe:** Each orchestration run gets a UUID. Agent sessions are isolated.
- **Session substrate:** tmux. Each agent gets its own pane within a marvel-managed session.

## Design Principles

1. **Agents are processes, not threads** — each agent is a separate tmux pane running a BYOA console
2. **Plans are data** — orchestration plans are TOML files, not code
3. **Fail observable** — every agent's output is visible in its pane; marvel logs coordination events
4. **Gradual elaboration** — starts as a simple fan-out spawner, grows coordination as needed
5. **Console-agnostic** — works with aclaude, zclaude, dclaude, or any CLI that accepts a prompt
