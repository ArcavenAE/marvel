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
Stdout logging of state transitions. No OTEL in MVP.

## 5. Out of Scope

Healthchecks, readychecks, schedules, packs, vaults, volumes, gateways,
multi-host, persistence, auth, OTEL, director integration, switchboard
integration.
