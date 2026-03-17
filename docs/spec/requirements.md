# Marvel MVP — Software Requirements (ISO/IEC/IEEE 29148, abbreviated)

Probe: marvel-mvp-probe | Confidence: frontier

## 1. Purpose

Validate marvel's resource model by building a working prototype that
orchestrates real tmux processes using k8s-like primitives.

## 2. Scope

MVP only. Single-host, single-binary, in-memory state. No persistence,
no packs, no probes, no gateway, no auth delegation.

## 3. Functional Requirements

### FR-01: Resource Types
The system shall support these resource types:
- **Workspace** — isolation boundary (namespace equivalent)
- **Session** — atomic unit: a tmux pane running one process (pod equivalent)
- **Runtime** — the program to execute (container image equivalent)
- **Team** — desired state: N sessions of a runtime (deployment equivalent)
- **Endpoint** — stable name for a session role (service equivalent)
- **Host** — the local machine (node equivalent, single-host only)

### FR-02: Manifest Application
The system shall accept TOML manifests declaring desired state and
reconcile actual state to match.

### FR-03: Session Lifecycle
Sessions shall transition: pending → running → succeeded/failed.
Creating a session creates a tmux pane and launches the runtime binary.
Deleting a session destroys the tmux pane.

### FR-04: Team Reconciliation
Teams shall maintain desired replica count. If a session dies or is
deleted, the team controller shall create a replacement.

### FR-05: CLI Operations
- `marvel apply <manifest.toml>` — apply desired state
- `marvel get <resource-type>` — list resources
- `marvel describe <resource-type> <name>` — detail view
- `marvel delete <resource-type> <name>` — remove resource
- `marvel scale <team> --replicas N` — adjust team size

### FR-06: Tmux Substrate
All sessions run in tmux panes within a marvel-managed tmux session.
The tmux session name is `marvel-<workspace>`.

## 4. Non-Functional Requirements

### NFR-01: In-Memory State
All state lives in memory. No database, no file persistence. State is
lost on restart. (Persistence is a future concern.)

### NFR-02: Single Binary
One `marvel` binary serves as both daemon and CLI. Commands that need
the daemon connect via Unix socket.

### NFR-03: Observability
Stdout logging of state transitions. OTEL stdout metric export available
via simulator (`--otel-stdout` flag).

## 5. Out of Scope

Healthchecks, readychecks, schedules, packs, vaults, volumes, gateways,
multi-host, persistence, auth, director integration, switchboard
integration.

## 6. Expansion Requirements (Post-MVP)

### FR-07: Context Pressure Monitoring
Sessions shall track context window usage (`ContextPercent`) and heartbeat
timestamps. Agents report via `"heartbeat"` RPC. Measurement only — no
automated actions.

### FR-08: Simulator Runtime
A separate `simulator` binary simulates agent context pressure. Accepts
`--name`, `--workspace`, `--team`, `--socket`, `--tick`, `--script`,
`--otel-stdout` flags. Grows context 1-5% per tick, wraps at 100%.

### FR-09: OTEL Metrics
Each agent exports `marvel.agent.context_window_percent` gauge via stdout
OTEL exporter. Attributes: workspace, team, session.

### FR-10: Lua Scripting
Simulator supports Lua scripts via `--script` flag. Scripts define
`on_tick(pct, tick)` called each tick. Lua environment exposes `marvel`
module: `create_agent`, `kill_agent`, `list_agents`, `scale_team`, `log`.

### FR-11: Supervisor Role
Teams may have a `role` field (`"agent"` | `"supervisor"`). Supervisors
are convention-based — any process with daemon socket access can call RPCs.

### FR-12: One-Off Sessions
- `marvel run <command> [args...]` — create ad-hoc session via `"run"` RPC
- `marvel kill <session-key>` — destroy session (alias for delete session)

### FR-13: Identity Injection
Team controller appends `--name`, `--workspace`, `--team`, `--socket`,
`--script` flags to sessions whose team has a `role` or `script` set.

### FR-14: Session State Accuracy
The system shall accurately reflect the running state of all sessions.
When an agent process exits (normally or abnormally), the session must
be removed from the store so that `get sessions` shows only live
sessions. The reconciler shall detect dead sessions (tmux pane gone)
and reap them before counting replicas. Dead sessions must not persist
as static entries. This is a prerequisite for reliable reconciliation,
shift triggers, and any future healthcheck or restart policy.
