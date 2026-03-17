# Marvel MVP — Software Design (IEEE 1016, abbreviated)

Probe: marvel-mvp-probe | Confidence: frontier

## 1. Architecture Overview

```
┌─────────────────────────────────────────┐
│  marvel CLI                              │
│  (cobra commands: apply, get, delete...) │
└──────────────┬──────────────────────────┘
               │ unix socket (JSON-RPC)
┌──────────────▼──────────────────────────┐
│  marvel daemon                           │
│  ┌──────────┐ ┌───────────┐ ┌────────┐ │
│  │ API/Store│ │ Team Ctrl │ │ Tmux   │ │
│  │ (in-mem) │ │ (reconcile)│ │ Driver │ │
│  └──────────┘ └───────────┘ └────────┘ │
└─────────────────────────────────────────┘
               │
       ┌───────▼────────┐
       │  tmux server    │
       │  (panes = pods) │
       └────────────────┘
```

## 2. Component Design

### 2.1 Resource Types (internal/api)
Go structs for each resource type. In-memory store with CRUD operations.
Resources are keyed by `workspace/name`.

### 2.2 Tmux Driver (internal/tmux)
Shell-out to `tmux` binary. Operations:
- `NewSession(name)` — create tmux session
- `NewPane(session, command)` — split-window, exec command
- `KillPane(paneID)` — kill a specific pane
- `KillSession(name)` — kill entire tmux session
- `ListPanes(session)` — list panes and their PIDs
- `HasSession(name)` — check if session exists

### 2.3 Session Manager (internal/session)
Creates/destroys sessions by coordinating API store and tmux driver.
Tracks session state (pending → running → succeeded/failed).

### 2.4 Team Controller (internal/team)
Reconciliation loop: compare desired replicas to actual running sessions.
Create or destroy sessions to match. Runs on a ticker (e.g., every 2s).

### 2.5 Runtime (internal/runtime)
Maps runtime names to executable commands. MVP has two built-in runtimes:
- `top` — runs `top` (visible, interactive, proves tmux works)
- `shell` — runs `bash` (interactive shell, proves session access)

### 2.6 Daemon
Listens on Unix socket (`/tmp/marvel.sock`). Serves JSON-RPC for CLI.
Starts team reconciliation loop. Manages tmux sessions.

### 2.7 CLI
Cobra commands that serialize requests to the daemon via Unix socket.

### 2.8 OTEL Metrics (internal/otel)
Thin wrapper over OpenTelemetry SDK. Stdout exporter only — no collector.
`NewStdoutMeterProvider()` creates provider, `NewContextGauge(meter)` creates
the `marvel.agent.context_window_percent` gauge.

### 2.9 Simulator Engine (internal/simulator)
Context pressure simulation: `Engine` struct with configurable tick rate.
Each tick grows context by random 1-5%, wraps at 100%. Callbacks:
`OnTick`, `OnHeartbeat`, `OnRecord` for Lua, daemon, and OTEL integration.

### 2.10 Lua Environment (internal/simulator)
`LuaEnv` wraps gopher-lua with `marvel` module exposing daemon RPC calls.
`LoadScript(path)` loads a Lua file. `CallOnTick(pct, tick)` invokes the
global `on_tick` function each simulator tick. Functions:
- `marvel.create_agent(cmd, args...)` — create one-off session
- `marvel.kill_agent(key)` — delete session
- `marvel.list_agents()` — list all sessions
- `marvel.scale_team(key, n)` — scale team replicas
- `marvel.log(msg)` — print to stdout

### 2.11 Simulator Binary (cmd/simulator)
Standalone binary wiring engine + OTEL + Lua + daemon heartbeat.
Flags: `--name`, `--socket`, `--script`, `--otel-stdout`, `--tick`,
`--workspace`, `--team`. Marvel launches it as a runtime — it doesn't
know simulator internals.

## 3. Key Decisions

- **Shell-out for tmux** — no Go tmux library worth using (probe finding)
- **Unix socket** — simpler than HTTP for single-host daemon
- **In-memory store** — no persistence in MVP, rebuild state from tmux on restart
- **JSON-RPC** — simple request/response, no streaming needed for MVP
- **Cobra** — placeholder CLI framework, expedient not committed
- **Simulator as separate binary** — marvel just launches it as any runtime
- **OTEL stdout export per agent** — no daemon-side collection in MVP
- **Supervisor = convention** — no ACL boundary, just socket access + Lua script
- **Lua via gopher-lua** — embeddable, no CGo, good enough for supervisor scripts
- **Context pressure measured, not acted on** — infrastructure before automation
