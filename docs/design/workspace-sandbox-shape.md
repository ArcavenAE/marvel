# Workspace sandbox shape: where a seat runs, and how marvel grows each harness toward it

- **Status:** proposed, 2026-10-10. It records an operator ruling and plans against it; it ratifies nothing new.
- **Author seat:** architect, team arcaven.
- **Scope:** the full shape the operator asked for, then the first build: a container for pi and mini-swe-agent.

## 1. Why

Two harnesses on the roster have no permission system of their own: pi has none, and mini-swe-agent's headless run confirms nothing. The harness plan recommended that neither run outside a sandbox. Today marvel can only start a seat on the host, so neither can be tried safely.

The operator answered with a shape rather than a patch. A workspace chooses where its agents run. "Sandboxed" also covers the harness's own operating mode and permissions, where the harness has them. Every harness matures toward one marvel surface, as far as it can. This doc writes that shape down so the first build fits inside it, and does not become the shape by accident.

## 2. The rulings this rests on

The operator ruled 2026-10-10 on two decisions from the harness plan. Both are quoted verbatim.

**D1, what "sandboxed only" means:** "we should have the option for any, a workspace choice of localhost file system, container, each can be with or without curtain. Here sandboxed also means controlling the operating mode and permissions of the harness, when it supports it. We do not need to do all of it now, but we should have the concepts and the plan, cross-harness view, we have a common surface/target/concept, and we mature each towards that common marvel experience/interface, to the degree each can/as we improve each. And yes, we can focus on (a) now specifically with these two, in execution, but I want the full shape of my intent understood and roughly planned and documented, then we build immediately around (a) for these two"

**D2, a sandbox as a launch precondition:** "no, it's workspace defaults, and marvel operator can change them by explicitly setting different manifest options. If they don't specificy different options, container is assumed for these two harnesses (possibly others, but this is focused on these two; any harnesses that haven't specifically recommended sandbox will not default to container, but can be set as such with a workspace defined as such. This may raise questions about ... mixed agent teams using the same container, I lean towards saying that container is a function of the workspace, it's container or localhost; agents run in the container. I'm not sure we can do that yet. It's more theory than practice right now and for the moment these two agents get their own container per but we'll need some architecture work around this and we have some problems to work out. Let's not stop the train to work these out just yet. Let's have some working harnesses that it matters for first."

**Which two.** D1 and D2 in the harness plan are both titled "for pi and mini-swe-agent", and option (a) of D1 is "a container now, with the curtain build left to P3". So "these two" are **pi and mini-swe-agent**, and "(a)" is a container now. They are also the two harnesses the plan recommended keeping inside a sandbox, which matches the D2 wording "harnesses that haven't specifically recommended sandbox".

## 3. The three axes of "sandboxed"

A seat's sandbox is three independent choices. Each has a value marvel can record and report.

| axis | values | who acts on it | today in marvel |
|---|---|---|---|
| **placement** | `localhost` (the host's file system) or `container` | marvel, when it launches | localhost only |
| **curtain** | `off` or a named curtain profile | curtain, as a wrapper marvel puts before the command | not built; curtain at origin/main is a stub, and the profile resource is parked with aae-orc-10x (`internal/api/types.go:649-650`, `:989-990`) |
| **posture** | the harness's own operating mode and permission settings | the harness, through the adapter's launch line | Claude Code only: `permissions` and `dangerous_permissions` (`internal/api/types.go:615-637`), plus the projected Policy file (`:982-995`) |

The four combinations of placement and curtain are all valid, as the operator ruled. Posture is set in every combination, wherever the harness offers it.

**A naming clash to clear up first.** marvel's code already says "the container half" for something else: a private state directory per session (`internal/runtime/adapter.go:182`, `internal/session/manager.go:983`). That is a home directory on the host, not an OS container. From here on, this doc and the build use "session home" for that, and "container" only for an OS container. The code comments can be renamed in a later, separate change.

## 4. The common surface

The target is one manifest vocabulary and one report for every harness. A harness that cannot honour an axis says so, and marvel reports it. marvel never treats a missing lever as satisfied.

- **Manifest:** a workspace sets `placement`, and optionally a curtain profile. A role sets posture, in that harness's own terms through its adapter. The existing `permissions` field becomes one adapter's mapping of posture, not the posture vocabulary itself.
- **Adapter capability:** each adapter declares what it can honour. That means whether it can run in a container, whether it has a posture lever, and whether it recommends a sandbox. The same pattern is used today for optional capabilities (`SessionHomeAssigner`, `SessionIDAssigner`, `StreamCapable`): a harness without the capability simply does not implement the interface.
- **Report:** `marvel get sessions` (or `describe`) shows the placement, curtain and posture **in force**, each read back after launch where the harness writes it. The adapter kit checklist already asks for this read-back (line 9).

Where each harness stands now. Each cell is a claim to verify when that harness's adapter work starts, not a finding.

| harness | posture lever | sandbox recommended | adapter |
|---|---|---|---|
| Claude Code | permission mode, skip-permissions | no | yes |
| codex | approval and sandbox modes | no | yes |
| opencode | permission config | no | yes |
| goose | `GOOSE_MODE` (docs/design/goose-adapter-v1.md section 4) | no | designed, not built |
| pi | none (no permission system) | **yes** | none; T6 probe first |
| mini-swe-agent | confirm off in headless (`-y`); cost and step limits | **yes** | none; runs as `generic` |

## 5. Defaults and overrides

Placement resolves in this order, and the first value found wins:

1. **The workspace's explicit `placement` in the manifest.** The operator's choice beats every default, including a harness's recommendation.
2. **The harness's recommendation.** An adapter that recommends a sandbox defaults to `container`. Today that is pi and mini-swe-agent.
3. **`localhost`.** Every other harness starts on the host unless a workspace asks for a container.

Curtain defaults to `off` until curtain is built. Posture keeps each adapter's current default.

**When a container cannot be had.** A launch whose placement resolved to `container` and that cannot get one is **refused**. It is never started on the host instead. A silent drop to localhost would turn the operator's default into a suggestion. It would also go through the `Prepare` error fallback, which launches the raw command (`internal/session/manager.go:995-999`); adapter kit checklist line 8 already rules that path out for a posture fault. The operator's way to run on the host is to set `placement = "localhost"` on the workspace. This is a recommendation, not part of the ruling, so it is listed in section 8.

## 6. Questions named, not solved

The operator asked for these to be recorded and the train kept moving.

- **Mixed teams in one container.** The target is "container is a function of the workspace; agents run in the container". For now each of the two seats gets its own container. Open: how a workspace container holds seats of several harnesses, whose session homes, credentials and posture differ; and whether tmux runs inside or outside the container.
- **Container runtime per host.** On kinu, `docker` and `orb` are present and `podman` is absent (`command -v`, 2026-10-10). Other hosts are unchecked. marvel needs one runtime per cluster, named in config, not detected by guessing.
- **Credentials into a container.** A provider key is linked or brokered, never copied in (ADR-009, kit checklist line 20). The trial-credential decision D3 is still open, and it governs the first paid run.
- **Curtain inside a container.** Whether both layers stack, or the container stands in for curtain on Linux and curtain is for macOS hosts. That is P3's question.
- **The pane.** The seat's pane runs the container client in the foreground. Liveness, kill and reap then act on the client, so the container must stop when the client exits.

## 7. The first build: a container for pi and mini-swe-agent

These are flat tickets with dependency edges, listed here for the supervisor to file. Nothing in this doc files them.

| id | work | blocked by |
|---|---|---|
| S1 | `placement` on the workspace, plus the resolution order in section 5: config, validation and tests. An unknown value is refused. | none |
| S2 | the adapter capability "recommends a sandbox", with pi and mini-swe-agent declaring it | S1 |
| S3 | the container launch wrap: when placement is `container`, the prepared command runs under the cluster's container runtime as a prefix, with the workdir and session home mounted; refused when no runtime is configured or reachable | S1 |
| S4 | read-back: the session record shows placement, curtain and posture in force | S3 |
| S5 | mini-swe-agent under `generic` in a container: the T5 trial kit, ending with one marvel headless run in a container | S3, D3 |
| S6 | pi in a container: the T6 probe (session id format, `agent_settled`, auth and session file locations), then the adapter as its own ticket | S3, D3 |

S1 to S4 spend nothing and can start now. S5 and S6 run a model, so they wait on D3.

**Test for the build:** a workspace with no `placement` and one pi seat starts the seat in a container, and `describe` shows `placement: container`. The same workspace with `placement = "localhost"` starts it on the host. With no container runtime configured, the pi seat is refused with a named reason, and a Claude seat in the same workspace starts on the host.

## 8. Decisions for the operator

Each recommendation is valid until 2026-10-17; the architect role re-checks it then. Nothing executes on silence.

- **The refusal rule (section 5).** (a) A seat whose placement resolved to `container` and that cannot get one is refused, and the operator sets `localhost` to override. (b) It falls back to the host with a warning. Recommended: (a). A fallback would make the default a suggestion, and it would use the raw-launch path the kit rules out.
- **The container runtime on each cluster.** Recommended: name one per cluster in marvel config, starting with docker on kinu. Not detected.
