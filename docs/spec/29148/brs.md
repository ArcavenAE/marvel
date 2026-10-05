---
standard: ISO/IEC/IEEE 29148:2018
document_type: Business Requirements Specification (BRS)
description: Business goals, market context, and the case for building the system
---

# Business Requirements Specification

## 1. Introduction

### 1.1 Purpose

This brief states the business case for marvel as marvel's own records show it. Each statement of fact is one claim line with a class and a source in the evidence index. Where the records are silent, the gap is an open question under 8.2, not a guess.

- **C-001** [OBSERVED] marvel is a control plane for AI agent workloads, written in Go. It manages the lifecycle of BYOA (Bring Your Own Agent) sessions: scheduling, configuration, process management, health monitoring, and rolling shifts. {src: README, CHARTER}

### 1.2 Scope

- **C-002** [OBSERVED] The resource hierarchy is Workspace (an isolation boundary), then Team (a cohesive unit of agents), then Role (one kind of agent, with a runtime, a replica count, a restart policy and a healthcheck), then Session (one agent process in a tmux pane). {src: README}
- **C-003** [OBSERVED] The reconciler and scheduler assume a local host today. A Host resource type exists for future multi-host scheduling, and whether switchboard is sufficient as the transport for that is an open question. {src: CHARTER, NODE:question-multi-host}

### 1.3 References

The only sources this brief cites are the rows of evidence-index.md: the charter, the README, the kos nodes under `_kos/nodes/`, and the findings under `_kos/findings/`.

## 2. Business Purpose

- **C-004** [RULED] The governing frame for marvel's resource model is the agentic resource matrix. The resources of agentic work are context, spend, cache locality, access, and authority. Kubernetes schedules containers by CPU and memory; marvel governs agents by this matrix. {src: NODE:elem-agentic-resource-matrix}
- **C-005** [OBSERVED] Agent sessions accumulate context, drift, and stale mental models. A shift starts fresh sessions, verifies they are running, then drains the old ones, preserving team identity. {src: README}
- **C-006** [OBSERVED] marvel infers a session's state from whether its process is alive, not from what the agent did. In finding-027's truth table a finished one-shot run was reported `crashed` and a run blocked at a modal dialog was reported `running`; the finished run was respawned and paid for again each cycle. {src: FINDING:finding-027}

## 3. Business Scope

### 3.1 Business Domain

- **C-007** [RULED] Agents are processes in tmux panes. Start, stop, restart, health check and output capture all go through tmux. {src: CHARTER, NODE:elem-tmux-session-substrate}
- **C-008** [RULED] marvel orchestrates and does not require a specific console. It works with forestage, zclaude, dclaude, the bare claude CLI, or any agent that accepts a prompt on stdin, with a generic adapter where deep integration is not possible. {src: NODE:elem-console-agnostic}
- **C-009** [RULED] The manifest field `runtime` names the harness (claude, codex, opencode and so on), never the agent. {src: NODE:elem-runtime-names-harness}
- **C-010** [OBSERVED] Six runtime adapters sit behind one event vocabulary. {src: NODE:elem-runtime-adapter-framework}

### 3.2 Business Processes

The processes below are the ones the records describe as shipped. What each is meant to become is in 4.3 and in the open questions.

- **C-011** [OBSERVED] An operator declares desired state in a TOML or YAML manifest, and `marvel work` reconciles it. The daemon is started with `marvel daemon`. {src: README, NODE:elem-yaml-manifest-support}
- **C-012** [OBSERVED] A shift is a rolling replacement driven by the reconciliation loop: launching new-generation sessions, draining old-generation ones one per tick, then complete. Roles shift sequentially, workers first and the supervisor last. {src: CHARTER, README, NODE:question-shifts}
- **C-013** [RULED] For the shift handoff artifact, marvel owns the schema and the departing agent owns the content. {src: NODE:elem-handoff-schema-ownership}
- **C-014** [OBSERVED] Health is evaluated in the reconciliation loop from heartbeat staleness or process liveness. Restart policies are `always`, `on-failure` and `never`. A session with no healthcheck configured stays `unknown` and is never restarted by health evaluation. {src: README, CHARTER, NODE:question-healthchecks}
- **C-015** [OBSERVED] `marvel stop` detaches by default: the daemon checkpoints state and exits while agents keep running, and the next daemon adopts the live panes. `--teardown` ends every agent. {src: README, NODE:question-marvel-transaction-log}
- **C-016** [OBSERVED] A finished headless run holds its replica slot and is not refilled when its pane exits 0. A non-zero exit, a signal death, an unknown status and every interactive role are still treated as crashes. {src: FINDING:finding-038}

### 3.3 Out of Scope

- **C-017** [RULED] Ruled out: building pack management as a separate tool outside marvel. Packs are agent configuration, and marvel already manages agent lifecycle, so pack management is a control plane concern. The ruling would be reopened if marvel becomes too large, or a non-marvel orchestration path needs pack resolution. {src: NODE:grv-standalone-pack-manager}
- **C-018** [RULED] Ruled out: a private Claude Code config directory per interactive claude seat. Measured on macOS, where the seat loses the operator's login. The cited node and finding limit the ruling to macOS; the operator's verdict of 2026-10-05 widens it: the macOS specifics reflect the platform in use at development time, and the ruling should not be limited to macOS (see Q-009). marvel manages the needed keys in the operator's own config instead. {src: NODE:grv-private-claude-config-home-macos, FINDING:finding-063}

## 4. Business Overview

### 4.1 Business Environment

- **C-019** [OBSERVED] marvel is distributed as macOS binaries (arm64, amd64), code-signed and Apple-notarized; as Linux binaries (amd64, arm64); and through a Homebrew tap and mise. Alpha releases are cut on push to main and stable releases on `v*` tags. {src: README, NODE:elem-release-pipeline}
- **C-020** [OBSERVED] The Gateway resource type names at least three services as sub-types: switchboard (remote tmux access), director (the inter-agent supervisor protocol), and an external API or webhook interface. The first two are designed; the external one is not. Whether each is a Gateway sub-type, and whether the set stops at three, is open (Q-008). {src: CHARTER, NODE:question-gateway-external-api}

- **C-045** [RULED] marvel is open source. Inference service providers' terms of service for the use of their harnesses and services are important non-regulatory restrictions to be aware of (operator, 2026-10-05, Q-004). {src: OPERATOR:2026-10-05}

### 4.2 Mission and Vision

- **C-043** [RULED] marvel orchestrates and organizes agents on a scale beyond what an individual can manage, and enables people to advance with the tooling used by heroic solo developers, because it works with rather than against the industry momentum in current REPL and harness tooling (operator, 2026-10-05, Q-002). {src: OPERATOR:2026-10-05}

### 4.3 Business Goals and Objectives

- **C-021** [FORWARD] Upgrades are to separate fetch from activation across two planes: a bootstrap plane that installs and stages, and a cluster plane that runs pre-flight checks, activates and rolls back, with a version store holding staged versions. `marvel daemon reexec` performs an upgrade while keeping running processes: sessions keep running across the reexec. Zero-downtime upgrade is the long-term acceptance test. A design study precedes implementation. {src: NODE:elem-staged-activation-upgrades}
- **C-022** [OBSERVED] Enforcement has three loci. Environment construction at spawn is built. Runtime admission and metering has its first component shipped, and full metering across the matrix rows is open. Mid-flight revocation is missing, and the M1 authority model is its prerequisite. {src: NODE:elem-agentic-resource-matrix}
- **C-023** [FORWARD] Whether automatic shifts may be turned on depends on whether a handoff is good enough to replace a working seat. This is recorded as an open question about automatic shift triggers. {src: NODE:question-auto-shift-handoff-quality, NODE:question-shift-triggers}

- **C-039** [RULED] marvel is an open source project without a specific intent around revenue (operator, 2026-10-05, Q-001). {src: OPERATOR:2026-10-05}
- **C-040** [RULED] Objective: within the next two months, marvel provides stable services for its author (operator, 2026-10-05, Q-001). {src: OPERATOR:2026-10-05}
- **C-041** [RULED] Objective: within four months, other users have chosen to use marvel to run some of their agent systems (operator, 2026-10-05, Q-001). {src: OPERATOR:2026-10-05}
- **C-042** [RULED] Objective: marvel is deployed on cloud computers for long-term team agents. No date was given (operator, 2026-10-05, Q-001). {src: OPERATOR:2026-10-05}
- **C-044** [RULED] Objective: within six months, improve the new user experience and introduce majordomo, leveraging AI for cluster-internal operations (operator, 2026-10-05, Q-001). {src: OPERATOR:2026-10-05}

## 5. Stakeholders

- **C-024** [RULED] The operator ratifies marvel's governing decisions. The resource matrix, the runtime naming rule, staged-activation upgrades and handoff schema ownership were each operator-ratified on 2026-08-01. {src: NODE:elem-agentic-resource-matrix, NODE:elem-runtime-names-harness, NODE:elem-staged-activation-upgrades, NODE:elem-handoff-schema-ownership}
- **C-025** [OBSERVED] The managed workload is agents grouped in teams of heterogeneous roles. The README's example review team has one supervisor, three reviewers and one architect. {src: README}
- **C-026** [OBSERVED] Remote clients reach a daemon over the mrvl:// protocol, which uses the daemon's own SSH server. Clients authenticate with ed25519 keys listed in the daemon's authorized keys, and a client records a daemon's host key on first use. {src: CHARTER, NODE:elem-mrvl-protocol-ssh}

- **C-046** [RULED] The stakeholders are individuals operating major harnesses individually today; individuals who would like to work better at scale; and teams of individuals who would like to learn to work together to achieve more with their AI than they are able to operate without relying on SaaS for this function (operator, 2026-10-05, Q-003). {src: OPERATOR:2026-10-05}

Influence and key interests per stakeholder are not recorded.

## 6. Business Requirements

No requirements register exists (see Q-005). The lines below are inferences from the records, classed JUDGMENT, and are the operator's to accept, correct or reject.

- **C-027** [JUDGMENT] A session's reported state should follow what the agent did, not only whether its pane exists, so that a finished run and a blocked run are not reported as their opposites. {src: FINDING:finding-027, FINDING:finding-015}
- **C-028** [JUDGMENT] The running daemon's build should be observable. As of the 2026-10-02 harvest, `marvel version` printed the client binary only and nothing on the socket or in the log named the daemon's version or revision. {src: FINDING:finding-061, NODE:elem-staged-activation-upgrades}
- **C-029** [JUDGMENT] `marvel upgrade` should verify and report truthfully. As measured on 2026-10-03 it fetches and replaces in one step, deletes the prior binary on the direct path, and reports success on a failed brew upgrade. {src: FINDING:finding-062}
- **C-030** [JUDGMENT] A team's spend should be bounded at declaration time. Admission control against manifest-declared team budgets refuses the declaration, not the spawn. {src: FINDING:finding-009, NODE:elem-agentic-resource-matrix}

## 7. Constraints and Assumptions

### 7.1 Business Constraints

- **C-031** [OBSERVED] The daemon must start in a keychain-capable session. macOS and Linux are both target platforms. The Linux keychain system is not yet chosen, and it will not require a GUI desktop or a display server (no GNOME, KDE, X or Wayland). {src: FINDING:finding-041}
- **C-032** [OBSERVED] Overriding HOME isolates marvel but de-authenticates the harness. {src: FINDING:finding-025}
- **C-033** [OBSERVED] The `~/.marvel/` data directory enforces OpenSSH-style permissions, and marvel refuses to load a private key with weaker permissions. {src: NODE:elem-data-directory-permissions, CHARTER}

### 7.2 Assumptions

This brief treats the kos nodes as authoritative over charter prose where the two differ. The claims below record why.

- **C-034** [OBSERVED] The charter's own pointer says two of its sections lag the bedrock nodes: B1 still presents the Kubernetes mapping as the governing frame, and B8 still says three adapters where six are registered. Both are described as pending a charter re-render, not open decisions. {src: CHARTER}
- **C-035** [OBSERVED] At the time of finding-006, the three-act demo beats were verified by automated agent runs and had not been run top to bottom by a person. The operator then drove the runbook live the same day, and that run found a bug. {src: FINDING:finding-006}

## 8. Appendices

### 8.1 Glossary

- **C-036** [OBSERVED] BYOH: Bring Your Own Harness. The user brings the harness, and marvel manages what they bring. BYOA (Bring Your Own Agent) stays valid: the user brings agent definitions, or gets them from sideshow, wardrobe, bmad, vsdd-factory or, in future, gastown. {src: README, NODE:elem-console-agnostic}
- **C-037** [OBSERVED] Shift: a rolling replacement of agent sessions with fresh ones. {src: README}
- **C-038** [OBSERVED] Harness: the agent program a session runs, such as claude, codex or opencode, named by a role's `runtime` field. {src: NODE:elem-runtime-names-harness}

### 8.2 Open Questions

- **Q-001** What are marvel's measurable business objectives and their timelines? The records give design intents but no objectives with a measure or a date. Answered by the operator on 2026-10-05; see brs-review.md.
- **Q-002** Is there a mission or vision statement for marvel beyond its purpose line and the resource matrix? Answered by the operator on 2026-10-05; see brs-review.md.
- **Q-003** Who are marvel's stakeholders beyond the operator, and what are their influence and key interests? The records name roles inside a team, not the people or groups with a stake in marvel. Answered by the operator on 2026-10-05; see brs-review.md.
- **Q-004** What is the business environment: market conditions, competing products, and any regulatory constraints? The records hold none. Answered by the operator on 2026-10-05; see brs-review.md.
- **Q-005** Where are the requirements and decision records that apply to marvel? The gather step found no ADRs, no requirements register and no sim-lesson register in this repository, while the charter cites an orchestrator-level ADR for the resource model. Answered by the operator on 2026-10-05; see brs-review.md.
- **Q-006** Do two bedrock nodes disagree about persistence? The node for the shipped MVP says state is in-memory and persistence is open, while the transaction-log node records a bbolt-backed durable record that is shipped. Which one is current? The two nodes are elem-mvp-complete and question-marvel-transaction-log. Answered by the operator on 2026-10-05: no. bd/dolt is not a native feature of marvel; it is a service scheduled to become a marvel-managed service option. Neither persistence node stands as written. See brs-review.md.
- **Q-007** The evidence index lists two findings under the same id, finding-039 (the b69n contract-lane harvest, and the tap push race). No claim here cites either, because the id cannot say which is meant. Should the finding ids be made unique? Still open: the operator says it is not the only finding conflict, and a fix or renumber is in progress.
- **Q-008** The three Gateway sub-types in C-020 are services. Is "Gateway sub-type" the right classification for each, and what other services belong in the set? The set is not limited to these three, and the classification of each is not clear (operator verdict on C-020). The operator answers that Gateway is probably not the right classification, and names candidate services: vault (credential and secrets manager), bd/dolt (tasks), nats (agent bus and communications), litellm (router), PAIR (backend), and more. The classification stays open. See brs-review.md.
- **Q-009** The operator's verdict on C-018 widens the ruling beyond macOS, but the node `grv-private-claude-config-home-macos` is macOS-only and its reopener names a Linux or container host, and finding-063 line 24 says "Where the login is a file (Linux, a container) the question is open and needs its own run." Should the node and finding be harvested to match the operator's verdict, or does the ruling need a Linux or container run first? The node is not edited here.
- **Q-010** `marvel daemon reexec` under mise restarts the old version, because mise installs each version in its own directory and reexec re-executes the binary at the running daemon's own path (docs/admin-guide.md:666-680 at main 9343a8f, "Under mise: stop and start, not reexec"; marvel#523). The operator wants reexec to work under mise, even if the architecture has to change. Tracked in marvel#592.
