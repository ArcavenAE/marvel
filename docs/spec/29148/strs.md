---
standard: ISO/IEC/IEEE 29148:2018
document_type: Stakeholder Requirements Specification (StRS)
description: What marvel's stakeholders need it to do, drafted from the ratified BRS by spectacle reverse-prd
---

# Stakeholder Requirements Specification

## 1. Introduction

### 1.1 Purpose

This StRS states what marvel's stakeholders need marvel to do, in "shall" form, one row per need. It is drafted from the ratified business brief (`brs.md`, ratified 2026-10-05), the evidence index and the operator's review of the brief (`brs-review.md`), and from nothing else. Every row is `proposed` and owned by the operator until the operator moves it. A need whose acceptance cannot be measured yet says so, and the gap is a proposed open question under 5.3.

### 1.2 Scope

The scope is marvel as the brief records it: a control plane for AI agent workloads that manages the lifecycle of sessions run by harnesses the user brings (C-001, C-036). It covers what stakeholders need from marvel, not how marvel is built. System requirements are a later step.

### 1.3 References

- `docs/spec/29148/brs.md`, claims C-001 to C-046 and questions Q-001 to Q-010.
- `docs/spec/29148/evidence-index.md`, the sources a row may cite by id.
- `docs/spec/29148/brs-review.md`, the operator's verdicts and answers of 2026-10-05.
- Template: `.spectacle/templates/29148/strs.md` from spectacle (ISO/IEC/IEEE 29148:2018 outline).

### 1.4 Definitions and Acronyms

| Term | Definition |
|------|-----------|
| BYOH | Bring Your Own Harness. The user brings the harness and marvel manages what they bring. BYOA (Bring Your Own Agent) stays valid (C-036). |
| Harness | The agent program a session runs, such as claude, codex or opencode, named by a role's `runtime` field (C-038). |
| Shift | A rolling replacement of agent sessions with fresh ones (C-037). |
| StRS | Stakeholder Requirements Specification. |
| BRS | Business Requirements Specification, here `brs.md`. |

## 2. Stakeholders

| Stakeholder | Role | Interest in System |
|------------|------|-------------------|
| The operator | Ratifies marvel's governing decisions and is the owner of every row until reassigned (C-024) | Stable services for its author for the next two months; a cloud deployment for long-term team agents (C-040, C-042) |
| Individuals operating major harnesses today | Users | Keep the tooling they already use while gaining what marvel adds (C-043, C-046) |
| Individuals who want to work better at scale | Users | Orchestrate agents beyond what one person can manage by hand (C-043, C-046) |
| Teams who want to learn to work together with AI | Users | Achieve more with their AI than they can operate without relying on SaaS for this function (C-046) |
| Other users who choose marvel | Adopters | Within four months, run some of their own agent systems on marvel (C-041) |

Influence and key interests per stakeholder are not recorded in the brief; see the open question `question-stakeholder-influence-and-interests`.

## 3. Stakeholder Requirements

Each table has the eight columns the command fixes. A row's trace back names claims from the brief (C-nnn) or ids from the evidence index. Trace forward is `none` on every row: this command never creates bd tickets.

### 3.1 Capabilities stakeholders need

| id | requirement | trace back | trace forward | acceptance | status | owner | evidence |
|---|---|---|---|---|---|---|---|
| STREQ-001 | marvel shall manage the lifecycle (scheduling, configuration, process management, health monitoring and rolling shifts) of agent sessions run by a harness the user brings. | C-001, C-036, README | none | Apply a manifest naming one team and read marvel's session listing. Pass: every declared replica is listed and its tmux pane exists. | proposed | operator | |
| STREQ-002 | marvel shall let an operator declare desired state in a TOML or YAML manifest and shall reconcile live sessions to it with `marvel work`. | C-011, NODE:elem-yaml-manifest-support | none | Apply one manifest written as YAML and again as TOML. Pass: both produce the same declared replica set. | proposed | operator | |
| STREQ-003 | marvel shall group agents in teams of heterogeneous roles, each role with its own runtime, replica count, restart policy and healthcheck. | C-002, C-025, NODE:question-org-model | none | Apply a manifest with one team of three roles on different runtimes. Pass: each role runs its own runtime at its own replica count with its own restart policy. | proposed | operator | |
| STREQ-004 | marvel shall work with the harness a user brings, naming the harness (never the agent) in the manifest field `runtime`, and shall offer a generic adapter where deep integration is not possible. | C-008, C-009, C-010, C-038, NODE:elem-runtime-names-harness | none | Run one role under each registered runtime and one under the generic adapter. Pass: each launches, and the six registered adapters report through one event vocabulary. | proposed | operator | |
| STREQ-005 | marvel shall replace aging sessions with fresh ones by a rolling shift that starts the new sessions, verifies them running, then drains the old ones one per tick, keeping team identity. | C-005, C-012, FINDING:finding-002 | none | Shift a team of three roles and record the order of events in the log. Pass: the new generation is running before any old session drains, one drains per tick, and workers shift before the supervisor. | proposed | operator | |
| STREQ-006 | marvel shall own the schema of the shift handoff artifact and shall leave its content to the departing agent. | C-013, NODE:elem-handoff-schema-ownership | none | Take a handoff produced in a shift and validate it against the schema marvel defines. Pass: it validates, and marvel wrote none of its content. | proposed | operator | |
| STREQ-007 | marvel shall, on `marvel stop`, detach by default so the daemon checkpoints state and exits while agents keep running, and the next daemon shall adopt the live panes; only `--teardown` shall end every agent. | C-015 | none | Start a team, run `marvel stop`, confirm the panes live, start a daemon. Pass: the same sessions are listed and adopted. Repeat with `--teardown`; pass: no agent pane remains. | proposed | operator | |
| STREQ-008 | marvel shall hold the replica slot of a finished headless run and not refill it when its pane exits 0, and shall still treat a non-zero exit, a signal death, an unknown status and every interactive role as crashes. | C-016, FINDING:finding-038, FINDING:finding-015 | none | Run a headless role that exits 0 and another that exits 1. Pass: the first is not respawned and keeps its slot; the second is handled as a crash. | proposed | operator | |
| STREQ-009 | marvel shall evaluate health in the reconciliation loop from heartbeat staleness or process liveness, with the restart policies `always`, `on-failure` and `never`, and shall leave a session with no healthcheck at `unknown` and never restart it by health evaluation. | C-014, NODE:question-healthchecks | none | Run roles with a stale heartbeat under each policy, and one with no healthcheck. Pass: each restarts or not as its policy says, and the one with no healthcheck is never restarted by health evaluation. | proposed | operator | |
| STREQ-010 | marvel shall report a session's state from what the agent did and not only from whether its pane exists, so that a finished run and a run blocked at a modal dialog are not reported as their opposites. | C-006, C-027, FINDING:finding-027, FINDING:finding-015 | none | Reproduce the two cases in finding-027's truth table. Pass: the finished run is not reported `crashed` and the blocked run is not reported `running`. The state names to report instead are not recorded: see `question-reported-state-names`. | proposed | operator | |
| STREQ-011 | marvel shall make the running daemon's build observable, so that its version and revision can be read from the socket or the log. | C-028, FINDING:finding-061 | none | Ask the running daemon for its build. Pass: version and revision are shown and match the binary it was started from. | proposed | operator | |
| STREQ-012 | marvel shall make `marvel upgrade` verify and report truthfully: it shall not delete the prior binary on the direct path and shall report failure when a brew upgrade fails. | C-029, FINDING:finding-062 | none | Run `marvel upgrade` with a failing brew upgrade. Pass: the exit status is non-zero, the failure is reported, and the prior binary is still present. Then run it on the direct-binary path and list the install directory before and after. Pass: the prior binary is still present after the upgrade. | proposed | operator | |
| STREQ-013 | marvel shall upgrade in two planes, a bootstrap plane that installs and stages and a cluster plane that runs pre-flight checks, activates and rolls back with a version store, and `marvel daemon reexec` shall perform an upgrade while sessions keep running. | C-021, NODE:elem-staged-activation-upgrades | none | Long-term test: upgrade the daemon under a live fleet. Pass: no session restarts and none is lost (zero-downtime upgrade). A design study precedes implementation, so this is not runnable today. | proposed | operator | |
| STREQ-014 | marvel shall bound a team's spend at declaration time: admission control shall refuse a manifest declaration whose team budget would be exceeded, rather than refuse a later spawn. | C-030, FINDING:finding-009 | none | Apply a manifest whose declared team budget cannot be met. Pass: the apply is refused and no session was spawned. | proposed | operator | |
| STREQ-015 | marvel shall govern agents by the agentic resource matrix (context, spend, cache locality, access and authority), enforced at environment construction, at runtime admission and metering, and by mid-flight revocation. | C-004, C-022, NODE:elem-agentic-resource-matrix | none | For each of the five resources, name the enforcement locus and show one observed instance. Environment construction is built and testable now. Full metering is open and mid-flight revocation is missing, so those two parts are not measurable yet: see `question-mid-flight-revocation-acceptance`. | proposed | operator | |
| STREQ-016 | marvel shall turn automatic shifts on only where a handoff is good enough to replace a working seat. | C-023, NODE:question-auto-shift-handoff-quality, NODE:question-shift-triggers | none | Not yet measurable: no measure of a handoff good enough to replace a seat is recorded. See `question-handoff-quality-measure`. | proposed | operator | |
| STREQ-017 | marvel shall let a remote client reach a daemon over the mrvl:// protocol, using the daemon's own SSH server, authenticating the client by an ed25519 key listed in the daemon's authorized keys, and recording the daemon's host key on first use. | C-026, NODE:elem-mrvl-protocol-ssh, NODE:question-mrvl-exposure-and-trust-bootstrap, FINDING:finding-029 | none | Connect with a listed key, then an unlisted key, then reconnect. Pass: the listed key connects, the unlisted key is refused, and the second connect checks the host key recorded on the first. Whether the client-side trust bootstrap can work as documented is open (NODE:question-mrvl-exposure-and-trust-bootstrap, finding-029), so the first-use step may not pass as described. | proposed | operator | |
| STREQ-018 | marvel shall name the services it fronts or manages as resources, starting with switchboard (remote tmux access) and director (the inter-agent supervisor protocol), and shall decide the external API or webhook interface. | C-020, NODE:question-gateway-external-api, OPERATOR:2026-10-05 | none | Not yet measurable: the operator says Gateway is probably not the right classification and the set is wider than three. See `question-gateway-service-classification`. | proposed | operator | |
| STREQ-019 | marvel shall offer bd/dolt as a marvel-managed service option, not as a native marvel feature. | OPERATOR:2026-10-05, NODE:question-bd-managed-extended-service | none | Not yet measurable: the operator says the service is scheduled, and its shape is the frontier question NODE:question-bd-managed-extended-service, which is not repeated as a proposed node. | proposed | operator | |
| STREQ-020 | marvel shall provide stable services for its author for the next two months. | C-040 | none | Not yet measurable: the brief gives no measure of "stable". See `question-objective-measures`. | proposed | operator | |
| STREQ-021 | Within four months, other users shall have chosen marvel to run some of their agent systems. This is a stakeholder outcome that marvel enables, written as the brief states it (C-041), not a behaviour of marvel; the operator ratifies it as an objective. | C-041 | none | Not yet measurable: the brief gives no count and no instrument for "chosen". See `question-objective-measures`. | proposed | operator | |
| STREQ-022 | marvel shall be deployable on cloud computers for long-term team agents. | C-042 | none | Not yet measurable: no date was given and no deployment target is recorded. See `question-cloud-deployment-date`. | proposed | operator | |
| STREQ-023 | marvel shall, within six months, improve the new-user experience and introduce majordomo, leveraging AI for cluster-internal operations. | C-044 | none | Not yet measurable: no measure of an improved new-user experience is recorded. See `question-objective-measures`. | proposed | operator | |
| STREQ-024 | marvel shall orchestrate agents at a scale beyond what an individual can manage while working with, not against, the harnesses a solo developer already uses. | C-043, C-046, C-007 | none | Run a harness as a marvel role and attach to its pane. Pass: the attached session shows the harness's own prompt and accepts input as it does outside marvel. | proposed | operator | |
| STREQ-025 | marvel shall let a team run multi-agent work under its own control, without relying on a SaaS service for orchestration. | C-046 | none | Run a three-role team with the daemon on the team's own machines. Pass: orchestration needs no third-party service beyond the inference provider the harness itself uses. | proposed | operator | |

### 3.2 Operational Requirements

| id | requirement | trace back | trace forward | acceptance | status | owner | evidence |
|---|---|---|---|---|---|---|---|
| STREQ-OPS-001 | marvel shall be distributed as code-signed, Apple-notarized macOS binaries (arm64 and amd64), as Linux binaries (amd64 and arm64), and through a Homebrew tap and mise. | C-019, NODE:elem-release-pipeline | none | List the artifacts of a release. Pass: all four binaries are present, the macOS ones pass the notarization check, and the tap and mise install the same version. | proposed | operator | |
| STREQ-OPS-002 | marvel shall cut an alpha release on every push to main and a stable release on a `v*` tag. | C-019 | none | Merge a change to main, then push a `v*` tag. Pass: an alpha release follows the merge and a stable release follows the tag. | proposed | operator | |
| STREQ-OPS-003 | The marvel daemon shall start in a keychain-capable session on macOS and on Linux, and on Linux shall not require a GUI desktop or a display server. | C-031, FINDING:finding-041 | none | Start the daemon in a keychain-capable macOS session. Pass: it runs and its seats keep their login. The Linux keychain system is not chosen, so the Linux case is not measurable yet: see `question-linux-keychain-system`. | proposed | operator | |
| STREQ-OPS-004 | marvel shall manage the keys an interactive claude seat needs in the operator's own config, and shall not give each seat a private config directory, on macOS and on Linux alike. | C-018, NODE:grv-private-claude-config-home-macos, FINDING:finding-063 | none | Launch an interactive claude seat under marvel. Pass: the seat shows the operator's login. Measured on macOS only; the Linux or container case needs its own run: see `question-private-claude-config-beyond-macos`. Note: req-drift reports a graveyard trace for this row. That is expected: the row restates the ruling C-018, which the operator widened on 2026-10-05. | proposed | operator | |
| STREQ-OPS-005 | `marvel daemon reexec` shall start the new version when marvel was installed by mise, and sessions shall keep running. | C-021, OPERATOR:2026-10-05 | none | Install a new version with mise, run `marvel daemon reexec`. Pass: the daemon reports the new version and no session restarted. Today it restarts the old version: see `question-reexec-under-mise`. | proposed | operator | |

### 3.3 Constraints from Stakeholders

| id | requirement | trace back | trace forward | acceptance | status | owner | evidence |
|---|---|---|---|---|---|---|---|
| STREQ-026 | marvel shall remain open source, and its use of each supported harness shall respect the inference provider's terms of service. | C-045, C-039 | none | The repository carries an open source license. The terms of each provider are a non-regulatory restriction the operator reviews; that review is a judgment recorded by the operator, not a measurement. | proposed | operator | |
| STREQ-027 | marvel shall run agents as processes in tmux panes, with start, stop, restart, health check and output capture all going through tmux. | C-007, NODE:elem-tmux-session-substrate | none | Start a session and stop it. Pass: each is one tmux pane, and marvel's lifecycle actions and output capture are tmux operations. | proposed | operator | |
| STREQ-029 | A harness launched by marvel shall keep the authentication it has under the operator's HOME, because overriding HOME isolates marvel but de-authenticates the harness. | C-032, FINDING:finding-025 | none | Launch a seat with and without a HOME override. Pass: the seat marvel launches is authenticated. | proposed | operator | |
| STREQ-030 | marvel shall enforce OpenSSH-style permissions on the `~/.marvel/` data directory and shall refuse to load a private key with weaker permissions. | C-033, NODE:elem-data-directory-permissions | none | Set a private key to mode 0644 and start the daemon, then set it to 0600. Pass: the first is refused and the second loads. | proposed | operator | |
| STREQ-031 | marvel shall schedule sessions on the local host, with a Host resource type kept for future multi-host scheduling. Whether switchboard is a sufficient transport for that is open. | C-003, NODE:question-multi-host | none | Not measurable as a future capability: the transport question is the existing frontier node NODE:question-multi-host, which is not repeated as a proposed node. Today's acceptance: a Host resource exists and no session is scheduled onto another host. | proposed | operator | |

## 4. Rationale

The rows follow the brief. Each is either a claim the records observed (C-001, C-011 and so on), a decision the operator ruled (C-007 to C-009, C-013, C-017, C-018, C-043 and the objectives C-039 to C-046), or a forward intent (C-021, C-023). Claims classed JUDGMENT in the brief (C-027 to C-030) are the brief's inference from findings and became rows STREQ-010, STREQ-011, STREQ-012 and STREQ-014. They stay `proposed` like every other row until the operator ratifies them.

Where the brief cites no measure, date or classification, the row says "not yet measurable" and points at an open question, so the gap stays visible. Rows STREQ-019 and STREQ-031 point at frontier nodes that already exist.

There is no STREQ-028. The first draft carried a row restating C-017 (pack management is not a separate tool outside marvel). The review of this run found that marvel's own `CLAUDE.md:512-514` at 8029b3ca says pack management is phased across sideshow and marvel under ADR-008, with sideshow installing packs now, which is the reopener the graveyard node names for C-017. This StRS asserts neither side. Whether C-017 reopens is the operator's ruling, carried as `question-pack-management-ruling`. The id is not reused.

Claims C-034 and C-035 (which source is authoritative, and how the demo was verified) describe the records rather than a stakeholder need and have no row. C-024 and C-025 are in section 2.

## 5. Appendices

### 5.1 Traceability to Business Requirements

| Stakeholder Req | Business Req |
|----------------|-------------|
| STREQ-001 | C-001, C-036, README |
| STREQ-002 | C-011, NODE:elem-yaml-manifest-support |
| STREQ-003 | C-002, C-025, NODE:question-org-model |
| STREQ-004 | C-008, C-009, C-010, C-038, NODE:elem-runtime-names-harness |
| STREQ-005 | C-005, C-012, FINDING:finding-002 |
| STREQ-006 | C-013, NODE:elem-handoff-schema-ownership |
| STREQ-007 | C-015 |
| STREQ-008 | C-016, FINDING:finding-038, FINDING:finding-015 |
| STREQ-009 | C-014, NODE:question-healthchecks |
| STREQ-010 | C-006, C-027, FINDING:finding-027, FINDING:finding-015 |
| STREQ-011 | C-028, FINDING:finding-061 |
| STREQ-012 | C-029, FINDING:finding-062 |
| STREQ-013 | C-021, NODE:elem-staged-activation-upgrades |
| STREQ-014 | C-030, FINDING:finding-009 |
| STREQ-015 | C-004, C-022, NODE:elem-agentic-resource-matrix |
| STREQ-016 | C-023, NODE:question-auto-shift-handoff-quality, NODE:question-shift-triggers |
| STREQ-017 | C-026, NODE:elem-mrvl-protocol-ssh, NODE:question-mrvl-exposure-and-trust-bootstrap, FINDING:finding-029 |
| STREQ-018 | C-020, NODE:question-gateway-external-api, OPERATOR:2026-10-05 |
| STREQ-019 | OPERATOR:2026-10-05, NODE:question-bd-managed-extended-service |
| STREQ-020 | C-040 |
| STREQ-021 | C-041 |
| STREQ-022 | C-042 |
| STREQ-023 | C-044 |
| STREQ-024 | C-043, C-046, C-007 |
| STREQ-025 | C-046 |
| STREQ-OPS-001 | C-019, NODE:elem-release-pipeline |
| STREQ-OPS-002 | C-019 |
| STREQ-OPS-003 | C-031, FINDING:finding-041 |
| STREQ-OPS-004 | C-018, NODE:grv-private-claude-config-home-macos, FINDING:finding-063 |
| STREQ-OPS-005 | C-021, OPERATOR:2026-10-05 |
| STREQ-026 | C-045, C-039 |
| STREQ-027 | C-007, NODE:elem-tmux-session-substrate |
| STREQ-029 | C-032, FINDING:finding-025 |
| STREQ-030 | C-033, NODE:elem-data-directory-permissions |
| STREQ-031 | C-003, NODE:question-multi-host |

### 5.2 Glossary

BYOH, harness and shift are defined in section 1.4.

### 5.3 Open Questions

Each is a proposed kos frontier node file under `docs/spec/29148/proposed-nodes/`. The operator files or drops each one.

- question-stakeholder-influence-and-interests
- question-finding-id-uniqueness
- question-gateway-service-classification
- question-private-claude-config-beyond-macos
- question-reexec-under-mise
- question-linux-keychain-system
- question-objective-measures
- question-cloud-deployment-date
- question-reported-state-names
- question-mid-flight-revocation-acceptance
- question-handoff-quality-measure
- question-pack-management-ruling
