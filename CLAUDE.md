# Marvel — Multiagent Orchestration System

## What This Is

Multiagent orchestration and agent fleet control plane for BYOA agents.
Spawns, coordinates, supervises, and configures agent sessions — including
content pack management, persona assignment, and configuration resolution.

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
cmd/marvel/           Entry point
internal/
  agent/              Agent lifecycle (spawn, monitor, teardown)
  session/            tmux session and pane management
  plan/               Execution plan parsing and validation
  coordinator/        Fan-out work distribution and result collection
  pack/               Content pack management
    registry/         Pack discovery, versioning, dependency resolution
    router/           Artifact type → filesystem path routing
    scope/            4-scope resolution chain (repo → shared → user → system)
    transform/        Format conversion between pack formats
  config/             Agent configuration resolution
    resolve/          Merge config chain: defaults → pack → scope → override
    override/         Per-agent and per-team config deltas
```

### Core Capabilities

**Orchestration** — start, stop, loop, and scale agent sessions.

**Configuration** — resolve what each agent is loaded with:
- Content packs (specticle, bmad, pennyfarthing, multiclaude, custom)
- Personas (which theme and role per agent)
- Scope chain (system → user → shared → repo, first match wins)
- Overrides (per-agent or per-team config deltas)

**Distribution** — fan-out work via TOML plans, collect results.

**Observation** — fleet-wide visibility into agent state and output.

### Content Pack Management

Marvel is the control plane for content packs. A pack is a git repo (or
directory) containing a `pack.yaml` manifest that declares artifact types.

```yaml
# pack.yaml example
name: specticle
version: 0.1.0
artifacts:
  commands:
    source: commands/
    target_type: commands
    namespace: specticle
  templates:
    source: templates/
    target_type: templates
    optional: true
```

**Artifact routing:** each artifact type maps to a filesystem location per
consumer. Commands → `.claude/commands/<pack>/`. Templates → `docs/spec/`.
Themes → `personas/themes/`. Marvel resolves the mapping.

**4-scope resolution:**

```
REPO        .packs/ or .claude/commands/   (git-tracked, travels with code)
    ↓ fallback
SHARED      /path/to/team-packs/ → symlink (revision-controlled separately)
    ↓ fallback
USER        ~/.config/byoa/packs/          (personal, synced across machines)
    ↓ fallback
SYSTEM      /usr/local/share/byoa/packs/   (org-mandated, admin-managed)
```

First match wins. Each scope supports: copy, symlink, or git-submodule modes.
Packs can be git-tracked or gitignored per project preference.

**Multi-pack coexistence:** when multiple packs provide the same artifact
type (e.g., specticle and bmad both provide architecture commands), marvel
resolves via explicit precedence in the plan or config. No silent merge.

**Pack operations:**
- `marvel pack install <source> [--scope repo|shared|user|system]`
- `marvel pack list [--scope ...]`
- `marvel pack update [--all | <name>]`
- `marvel pack remove <name>`
- `marvel pack link <path> --scope shared --project <path>`
- `marvel pack resolve` — show resolved config for current scope chain

### Agent Session Configuration

An agent session is not just "spawn aclaude in a tmux pane." It is:

> Spawn aclaude in a tmux pane **with** specticle commands loaded, the
> dune/architect persona, this project's SRS as context, and these
> repo-local overrides.

Marvel resolves the full agent configuration before launch:

1. Read the orchestration plan (TOML)
2. Resolve content packs via the scope chain
3. Route artifacts to the right locations for the target console
4. Apply per-agent overrides (persona, model, constraints)
5. Launch the console with the resolved configuration

This means:
- **Single agent (no marvel):** aclaude reads its own config chain. Works as today.
- **Marvel-managed agent:** marvel resolves config + packs, launches aclaude
  with that configuration. aclaude doesn't need pack management logic.
- **Shift change / scale-out:** marvel spins new agents with the same resolved
  config. Consistent fleet.

### Integration Points

- **aclaude / BYOA consoles** — spawns agents in tmux panes via CLI
- **switchboard** — optional remote access to agent sessions
- **director** — optional supervisor protocol for inter-agent comms
- **kos** — optional spec projection to seed agent context
- **specticle** — content pack providing IEEE-based spec templates and commands
- **bmad, pennyfarthing, multiclaude** — importable content packs

All integrations are optional. Marvel works standalone with just tmux + a
BYOA console.

## Conventions

- **Config format:** TOML for plan files and pack config; Go flags/env for runtime.
- **Pack manifest:** `pack.yaml` at pack root. Declares artifacts, types, dependencies.
- **Auth:** Delegates to the BYOA console (which delegates to Claude Code). No direct credential handling.
- **No file deletion:** Never delete user files. Overwrite only with explicit intent.
- **Parallel-safe:** Each orchestration run gets a UUID. Agent sessions are isolated.
- **Session substrate:** tmux. Each agent gets its own pane within a marvel-managed session.

## Design Principles

1. **Agents are processes, not threads** — each agent is a separate tmux pane running a BYOA console
2. **Plans are data** — orchestration plans are TOML files, not code
3. **Configuration is resolved, not assumed** — marvel resolves the full scope chain before launch
4. **Packs are git repos** — versioned, diffable, shareable. No proprietary format.
5. **Fail observable** — every agent's output is visible in its pane; marvel logs coordination events
6. **Gradual elaboration** — starts as a simple fan-out spawner, grows pack management as needed
7. **Console-agnostic** — works with aclaude, zclaude, dclaude, or any CLI that accepts a prompt
