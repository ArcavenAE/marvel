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

## Epic 7: Context Pressure and Heartbeat

### Story 7.1: Session heartbeat tracking
As the system, I need sessions to report context pressure so operators
can monitor agent utilization.

Tasks:
- [x] Add ContextPercent, LastHeartbeat to Session type
- [x] Add UpdateSessionHeartbeat to store
- [x] Add "heartbeat" RPC to daemon
- [x] Add CONTEXT% column to `marvel get sessions` output
- [x] Tests for heartbeat update and RPC

### Story 7.2: One-off session creation
As an operator, I want to create ad-hoc sessions without a team manifest.

Tasks:
- [x] Add "run" RPC to daemon
- [x] `marvel run <command> [args...]` CLI command
- [x] `marvel kill <session-key>` CLI command
- [x] Tests for run RPC

## Epic 8: Simulator

### Story 8.1: Simulator engine
As a developer, I need a simulated agent that grows context pressure
so I can test orchestration without real Claude Code sessions.

Tasks:
- [x] Engine with tick loop, random 1-5% growth, wrap at 100%
- [x] Deterministic with seed for testing
- [x] Status line output format
- [x] OnTick, OnHeartbeat, OnRecord callbacks
- [x] Tests for tick growth, bounds, wrap, determinism

### Story 8.2: Simulator binary
As an operator, I want a standalone simulator binary that marvel
launches as a runtime.

Tasks:
- [x] cmd/simulator/main.go with flag parsing
- [x] OTEL stdout export via --otel-stdout
- [x] Heartbeat to daemon via --socket
- [x] Lua script loading via --script
- [x] Signal handling for graceful shutdown

## Epic 9: OTEL Metrics

### Story 9.1: Stdout meter provider
As a developer, I need OTEL metrics export so agent context pressure
is observable.

Tasks:
- [x] NewStdoutMeterProvider wrapping OTEL SDK
- [x] NewContextGauge for marvel.agent.context_window_percent
- [x] Tests for provider creation and gauge recording

## Epic 10: Lua Scripting

### Story 10.1: Lua environment with marvel module
As a supervisor, I want Lua scripts to programmatically control agents.

Tasks:
- [x] LuaEnv struct wrapping gopher-lua LState
- [x] marvel module: create_agent, kill_agent, list_agents, scale_team, log
- [x] LoadScript, CallOnTick, Close methods
- [x] Tests for Lua state, module registration, on_tick callback, RPC errors

### Story 10.2: Example supervisor scripts
As an operator, I want example Lua scripts demonstrating supervisor patterns.

Tasks:
- [x] scripts/chaos.lua — random create/kill every ~60s
- [x] scripts/scaler.lua — scale based on context pressure

## Epic 11: Identity Injection

### Story 11.1: Team controller identity flags
As the system, I need to inject identity information into simulator
sessions so they can heartbeat and run Lua scripts.

Tasks:
- [x] Team Role field (agent/supervisor)
- [x] Runtime Script field
- [x] Controller injects --name, --workspace, --team, --socket, --script
- [x] Tmux driver accepts env var map for pane creation
- [x] Session manager passes MARVEL_SESSION and MARVEL_SOCKET env vars
- [x] Tests for identity injection

## Epic 12: Updated Demo

### Story 12.1: Simulator demo manifest
As a developer, I want the demo to use simulators showing context
pressure and a Lua supervisor.

Tasks:
- [x] Updated examples/demo.toml: 3 simulator agents + 1 chaos supervisor
- [x] Updated justfile: build both binaries, build-sim recipe
