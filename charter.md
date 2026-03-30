# marvel Charter

Agent orchestration control plane — kubernetes-like resource model for
AI agent workloads. Written in Go.

Follows the kos process. Authoritative graph: `_kos/nodes/`.
Cross-repo questions belong in the orchestrator's charter.

Last updated: 2026-03-30

---

## Bedrock

### B1: K8s-Inspired Resource Model
Resource types map k8s concepts to agent orchestration: Workspace (namespace),
Session (pod), Runtime (container image), Team (deployment), Endpoint (service),
Pack (configmap), Host (node). TOML manifests, reconciliation loop.
Evidence: implemented and working in MVP. ADR: aae-orc decisions/adr-003.

### B2: tmux as Session Substrate
Agents are processes in tmux panes. Start, stop, restart, health check, capture
output — all via tmux. Simple, observable, already works.
Evidence: MVP functional with tmux driver.

### B3: Console-Agnostic
Works with aclaude, zclaude, dclaude, bare claude CLI, or any agent that accepts
a prompt on stdin. Marvel orchestrates; it does not require a specific console.
Evidence: tested with both aclaude and bare claude CLI.

### B4: MVP Complete
Resource types, tmux driver, session manager, team reconciler, daemon, CLI,
simulator with context pressure monitoring and Lua supervisor scripting. Single-host,
in-memory state.
Evidence: marvel-mvp-probe (aae-orc sprint/rd/), 2 probe cycles complete.

---

## Frontier

### F1: Organizational Model
Workspace / Group / Team / Shift hierarchy needs refinement. Current model has
Workspace and Team. Gaps: what groups a supervisor with its agents? What scopes
shared resources? What replaces stale sessions? Hypothesis: Group > Team > Session
hierarchy + Workspace as sandbox.
Cross-ref: orchestrator F9.

### F2: Persistence
MVP is in-memory. What needs persistence? Session state? Team config? Manifest history?
What store (SQLite, files, etcd-like)?

### F3: Content Pack Integration
pack.yaml manifest sketched. How do packs resolve? How do they route artifacts
to the right runtime? 4-scope chain (repo → shared → user → system).
Cross-ref: orchestrator F6.

### F4: Healthchecks and Readychecks
Designed in resource model (process-alive, prompt-response) but not implemented
beyond basic process detection.

### F5: Multi-Host via Switchboard
Host resource type exists for future multi-host scheduling. How does marvel
discover remote hosts? Is switchboard sufficient as the transport?
Cross-ref: orchestrator F7.

---

## Graveyard

### G1: Standalone Pack Manager
Evaluated building pack management as a separate tool. Ruled out — packs are
agent configuration, marvel already manages agent lifecycle, these are control
plane concerns.
Evidence: ADR-002 in orchestrator.
