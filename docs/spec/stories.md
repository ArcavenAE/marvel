# Marvel MVP — Epics, Stories, Tasks

Probe: marvel-mvp-probe | Confidence: frontier

## Epic 1: Resource Model

### Story 1.1: Define core resource types
As a developer, I need Go structs for Workspace, Session, Runtime, Team,
Endpoint, and Host so the system has a typed resource model.

Tasks:
- [x] Define resource structs in internal/api/types.go
- [x] Define resource store interface in internal/api/store.go
- [x] Implement in-memory store
- [x] Tests for store CRUD operations

### Story 1.2: TOML manifest parsing
As an operator, I want to declare desired state in TOML so I can
`marvel apply` it.

Tasks:
- [x] Define manifest TOML structure
- [x] Parse manifest into resource types
- [x] Tests for manifest parsing

## Epic 2: Tmux Substrate

### Story 2.1: Tmux driver
As the system, I need to create/destroy tmux sessions and panes so
sessions have a process substrate.

Tasks:
- [x] Implement tmux shell-out driver
- [x] Tests for tmux operations (integration, requires tmux)

## Epic 3: Session Lifecycle

### Story 3.1: Session creation and destruction
As the system, I need to create a tmux pane for each session and track
its state through pending → running → succeeded/failed.

Tasks:
- [x] Session manager creates pane via tmux driver
- [x] Session manager tracks state transitions
- [x] Session deletion kills tmux pane
- [x] Tests for session lifecycle

## Epic 4: Team Controller

### Story 4.1: Reconciliation loop
As the system, I need to maintain desired replica count by creating or
destroying sessions.

Tasks:
- [x] Team controller compares desired vs actual
- [x] Creates sessions when under-provisioned
- [x] Destroys sessions when over-provisioned
- [x] Periodic reconciliation on ticker
- [x] Tests for reconciliation logic

## Epic 5: Daemon and CLI

### Story 5.1: Daemon with Unix socket
As the system, I need a long-running daemon that serves CLI requests
and runs the reconciliation loop.

Tasks:
- [x] Unix socket listener with JSON-RPC
- [x] Daemon starts/stops cleanly
- [x] Tests for daemon lifecycle

### Story 5.2: CLI commands
As an operator, I want kubectl-like commands to manage the system.

Tasks:
- [x] `marvel daemon` — start the daemon
- [x] `marvel apply <file>` — apply manifest
- [x] `marvel get <type>` — list resources
- [x] `marvel describe <type> <name>` — detail view
- [x] `marvel delete <type> <name>` — remove resource
- [x] `marvel scale <team> --replicas N` — adjust replicas
- [x] `marvel stop` — shut down daemon and clean up tmux

## Epic 6: Just Interface

### Story 6.1: Build and run recipes
As a developer, I want just recipes to build, test, start, and stop marvel.

Tasks:
- [x] `just build` — go build
- [x] `just test` — go test
- [x] `just start` — launch daemon
- [x] `just stop` — stop daemon
- [x] `just demo` — apply sample manifest and show running state
