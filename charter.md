# marvel Charter

Agent orchestration control plane — kubernetes-like resource model for
AI agent workloads. Written in Go.

Follows the kos process. Authoritative graph: `_kos/nodes/`.
Cross-repo questions belong in the orchestrator's charter.

Last updated: 2026-04-12 (session-016: charter management probe — content migration from orchestrator)

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
Works with forestage, zclaude, dclaude, bare claude CLI, or any agent that accepts
a prompt on stdin. Marvel orchestrates; it does not require a specific console.
Evidence: tested with both forestage and bare claude CLI.

### B4: MVP Complete
Resource types, tmux driver, session manager, team reconciler, daemon, CLI,
simulator with context pressure monitoring and Lua supervisor scripting. Single-host,
in-memory state.
Evidence: marvel-mvp-probe (aae-orc sprint/rd/), 2 probe cycles complete.

### B5: Heterogeneous Team Model
Teams contain multiple roles, each with its own runtime and replica count.
The supervisor-agent binding is the team itself — not a separate group resource.
Reconciliation is per-role. Session naming: teamname-rolename-N. Scale requires
specifying a role. Group is a collection of teams (placeholder, future work).
Evidence: probe-org-model-heterogeneous-teams, finding-001. 18 files changed,
TestReconcileMultipleRoles validates independent per-role scaling.

### B6: Shift Mechanics
Shifts are rolling replacements of agent sessions driven by the reconciliation
loop. State machine: launching (create new-gen) → draining (remove old-gen one
per tick) → complete. Generation tracking on Team and Session. Roles shift
sequentially, supervisor last. Manual trigger: `marvel shift <team>`.
Session naming includes generation: teamname-rolename-gN-idx.
Evidence: probe-shift-mechanics, finding-002. 11 files changed, 631 insertions.
TestShiftFullLifecycle, TestShiftMultipleRoles, TestShiftSingleRole validate.

### B7: Healthchecks
Health evaluation in the reconciliation loop. Heartbeat staleness detection
with configurable timeout and failure threshold. Restart policies (always,
on-failure, never) on roles. Opt-in: sessions without healthcheck config
stay unknown and are never failed. Shift preflight checks first heartbeat.
Health signal taxonomy maps the full BYOA spectrum (OS, tmux, heartbeat,
agent SDK) — implementation is deliberately thin, taxonomy is broad.
Evidence: probe-healthchecks, finding-003. 8 files changed, 545 insertions.

---

## Frontier

### F1: Organizational Model — RESOLVED → B5
Resolved by probe-org-model-heterogeneous-teams. Teams contain heterogeneous
roles. Supervisor-agent binding is the team. Group is placeholder.
See B5 and finding-001.

### F2: Persistence
MVP is in-memory. What needs persistence? Session state? Team config? Manifest history?
What store (SQLite, files, etcd-like)?

### F3: Content Pack Integration
pack.yaml manifest sketched. How do packs resolve? How do they route artifacts
to the right runtime? 4-scope chain (repo → shared → user → system).
Cross-ref: orchestrator F6.

### F4: Healthchecks — RESOLVED → B7
Resolved by probe-healthchecks. Heartbeat staleness + restart policy.
Opt-in healthcheck config on roles. See B7 and finding-003.
Prompt-response and richer agent signals are future work.

### F5: Multi-Host via Switchboard
Host resource type exists for future multi-host scheduling. This is the
distributed case — the reconciler and scheduler currently assume local-only.
Questions: what scheduling algorithm places sessions across hosts? How does
marvel discover remote hosts? Is switchboard sufficient as the transport, or
does marvel need its own discovery mechanism (e.g., gossip, static config,
registry service)? How does state sync work when the in-memory state is split
across hosts?
Cross-ref: orchestrator F7.

### F6: Shift Mechanics — RESOLVED → B6
Resolved by probe-shift-mechanics. Manual shifts work. State machine driven
by reconciliation loop. Roles shift sequentially, supervisor last.
See B6 and finding-002.

### F7: Automatic Shift Triggers
Manual shifts work (B6). Automatic triggers are the next question: scheduled,
context pressure, failure detection, login failures, service updates, memory
pressure. Which are team-level vs role-level? How does failure detection work
with current heartbeat data? See question-shift-triggers node.

### F8: Gateway — External API/Webhook Interface
The Gateway resource type has three sub-types: switchboard (remote tmux access),
director (inter-agent supervisor protocol), and an external API/webhook interface.
The first two are designed. The external gateway is mentioned in the resource
model but not designed. Questions: what external access patterns are needed?
API for triggering orchestrations from outside the marvel cluster? Webhook
receivers that dispatch work to running agent teams? Or CLI-only access for now,
deferring external API until there's a concrete use case?
Cross-ref: orchestrator F2.

### F9: Runtime Adapter Framework
`internal/runtime/` is empty. This is the integration keystone — marvel can't
launch real BYOA workloads without runtime adapters. Three needed: forestage
(deep: persona, heartbeat, cooperative stream), bare-claude (medium: env vars,
capture-pane fallback), generic-stdin (minimal: any CLI, capture-pane only).
Each adapter constructs the execution environment (settings.local.json, env vars,
volumes) and exposes capabilities (spawn, kill, inject, capture).
Blocks: forestage+marvel integration, permission model, everything downstream.
Depends on: aae-orc-vpq (tmux driver decision).
kos node: question-runtime-adapter.

### F10: Permission Model — Environment Construction + Internal Capabilities
Two complementary layers: (1) environment construction — marvel writes
settings.local.json with the role's CC permission mode, controls filesystem
mounts, sandbox boundaries; (2) internal capabilities — role-based RPC
authorization on daemon methods. Workers get heartbeat+query. Supervisors get
inject+scale+shift+capture. Both declared in team manifest, enforced by marvel.
Replaces per-machine hooks (DCG), per-user settings, daemon-level checks.
Depends on: F9 (runtime adapter, which constructs the environment).
kos node: question-permission-model.

### F11: Stream Attachment Strategy
How does marvel observe agent output? Four strategies with different tradeoffs:
PTY-owned (conflicts with tmux substrate — likely ruled out), pipe-pane to FIFO
(pragmatic), cooperative socket from forestage (highest structure, marvel-aware
only), periodic capture-pane scrape (lowest fidelity, universal BYOA fallback).
Runtime adapter selects strategy based on runtime capabilities. Needs a probe.
kos node: question-stream-attachment.

### F12: Agent Communication — Broker-First Reframe
Original framing (three transports + four message types) is transports without
a broker. Reframed: topics and subscriptions with auth, transports as
implementation detail. Broker candidates: embedded NATS JetStream, RabbitMQ,
SQLite-backed custom. Tension with gradual elaboration: zero agents communicating
today. Adopt the mental model now, defer implementation until concrete need.
kos node: question-agent-protocol (updated session-017).

---

## Graveyard

### G1: Standalone Pack Manager
Evaluated building pack management as a separate tool. Ruled out — packs are
agent configuration, marvel already manages agent lifecycle, these are control
plane concerns.
Evidence: ADR-002 in orchestrator.
