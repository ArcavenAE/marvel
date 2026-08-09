# finding-021: AWS AgentCore and the Gemini Enterprise Agent Platform are peers, not deployment targets

- **Date:** 2026-08-09
- **Status:** captured. Ruling made; the central premise of the probe brief was checked and partly refuted; the vendor arms diverge and are split.
- **Probe:** documentation study (AWS Bedrock AgentCore devguide, Google Cloud Gemini Enterprise Agent Platform docs, both read 2026-08-09) plus a code read of this repository at `0886801`. No cloud resources were provisioned and nothing was spent, so every vendor capability below is documented-only unless it says otherwise.
- **bd:** aae-orc-1s7y
- **Prior art extended, not re-derived:** orc `_kos/ideas/agentcore-crosswalk.md` (2026-07-17), which ran AgentCore against the platform vision as an external audit. This is the inverse reading: marvel as the thing deployed or composed.

## The ruling

**Peer.** A managed agent runtime is one more `Host` under marvel's
scheduler, reached through a second `Instance` implementation, and marvel
stays a local process that owns its own reconciliation loop. **Target is
refused on a mechanical ground, not a philosophical one: an agent running
inside AgentCore has no primitive that creates a sibling session.** Only
`InvokeAgentRuntime`, called from outside, brings a session into
existence. A reconciler whose entire job is to converge the set of running
sessions cannot run as one of the things it converges.

The rest of this document is the evidence for that sentence, the premise
corrections that changed the shape of the question, and the partition of
marvel that a peer adapter would actually touch.

## Premise check: three claims in the brief are now stale

The brief was written 2026-08-01 and rests on the crosswalk of 2026-07-17.
Both describe AgentCore Runtime as a managed microVM with "no tmux, no
pane, and no persistent host marvel owns." As of the documentation read on
2026-08-09, each third of that is wrong or has an exception.

**There is a terminal.** `InvokeAgentRuntimeCommandShell` opens a
persistent interactive shell inside a running session over WebSocket,
using binary frames that the documentation says carry "raw terminal
control sequences, colors, cursor movement, and full-screen applications."
State (environment variables, working directory, history) survives across
inputs; reconnecting with the same `shellId` replays up to 256 KB of
buffered output and the shell process keeps running while disconnected.
The documentation names Claude Code, Kiro and Codex as consumers of this
channel. Agents created after 2026-06-05 support it automatically. Caps:
10 concurrent shells per runtime, 1 hour per connection, 64 KB frames, 250
frames/sec. A one-shot sibling, `InvokeAgentRuntimeCommand`, streams
stdout/stderr and an exit code for a single command (1 byte to 64 KB
command, 1 to 3600 second timeout, 300 default).

**There is a persistent host, and it is in your account.** The Instances
compute type reached general availability 2026-08-06. Agents run on EC2
managed instances that AgentCore provisions inside the customer's own
account: sessions up to 14 days rather than 8 hours, `x86_64` as well as
`arm64`, GPU families supported, EBS volumes that detach on session stop
and reattach on resume, and multiple agents landing on one instance
sharing a filesystem when invoked with the same `runtimeSessionId`. Data
stays in the account and existing Savings Plans and ODCRs apply. The
instances are not yours to operate: you do not launch, patch or terminate
them, they are hidden from EC2 console listings by default, and an
infrastructure IAM role grants AgentCore the ability to manage compute on
your behalf.

**Filesystem state persists even without Instances.** Managed session
storage (preview) mounts a per-session directory that survives stop and
resume, up to 1 GB, reset after 14 days idle or on any runtime version
update. `git`, `npm`, `pip` and `cargo` are documented as working
unmodified. Hard links, device files, FIFOs, UNIX sockets and extended
attributes are not supported, which matters directly: marvel's stream
attachment path is a FIFO (`internal/runtime/fifo.go`), so that path does
not survive a naive lift into managed session storage. It would work on an
Instances volume or on the container's own writable layer.

The correction is not that AgentCore is friendlier than the brief assumed.
It is that the shape of the question changed. The choice is no longer
local-with-a-terminal against cloud-without-one. It is a terminal you rent
by the connection under a 10-at-a-time cap, and a host you pay for but may
not touch.

## Why target fails

Four reasons, ordered by how hard they are to argue with.

**1. No create primitive inside.** AgentCore's control plane owns session
creation. `InvokeAgentRuntime` with a new `runtimeSessionId` is what
provisions compute. Marvel's reconciliation loop converges observed
sessions toward a declared team, which means creating and destroying
sessions is the loop's entire output. Marvel-inside could call the control
plane back out through its execution role, but then it is a workload of
the control plane issuing instructions to the control plane about
workloads, and its own existence depends on a session that service can
stop. The reconciler must sit outside the thing it reconciles.

**2. Session lifetime is shorter than the fleet.** A microVM session caps
at 8 hours (`maxLifetime`, adjustable within 60 to 28,800 seconds) and
idles out after 15 minutes by default. Instances raise the ceiling to 14
days. Marvel's daemon is meant to outlive the agents it supervises;
`finding-018` measured cold-shift wall clock precisely because rotation is
an event in a longer-lived process. A supervisor that is itself evicted on
a timer is not a supervisor.

**3. The duplication is nearly total at the session layer.** AgentCore
already provides health checks, restart of unhealthy compute, session
isolation, and lifecycle. Running marvel's session manager inside buys
nothing there and loses the parts that make marvel's version different,
which are team-shaped rather than session-shaped.

**4. BYOA survives, and that is the surprise.** The container is arbitrary
(`arm64` for microVMs, either architecture on Instances, 2 GB image limit),
"Models and frameworks: Any" per the compute-type comparison, and the
shell channel is documented as the execution environment for third-party
coding harnesses. So the honest statement is that target does not die on
BYOA grounds. It dies on the control-plane inversion above. Refusing it
for the wrong reason would leave the question open to reopening the first
time someone reads the container spec.

## Why peer works

The interface that matters already exists and is not tmux-shaped.
`internal/runtime/instance.go` declares `Instance` with `Spawn`, `Kill`,
`Inject`, `Capture` and `Events`. `TmuxInstance` is one implementation,
and `internal/runtime/instance_tmux.go` deliberately declares a narrow
`PaneController` interface with the comment that it "keeps package runtime
free of a tmux dependency." The AgentCore verbs land on those five
methods without strain:

| `Instance` method | AgentCore |
|---|---|
| `Spawn` | `InvokeAgentRuntime` with a fresh `runtimeSessionId` |
| `Kill` | `StopRuntimeSession` |
| `Inject` | `InvokeAgentRuntimeCommandShell` frame write |
| `Capture` | shell reconnect replay buffer, or `InvokeAgentRuntimeCommand` |
| `Events` | the invoke response stream, plus the agent's own log group |

The `Adapter` interface does **not** stretch to cover this, and saying so
is half the value of the probe. `Adapter.Prepare` returns
`LaunchResult{Command, Env}`, and the comment on it says the command
string "is passed to tmux new-window." An AgentCore session is an API
call, not a command line. So the seam is `Instance`, one level below
`Adapter`, and `elem-runtime-adapter-framework` is the wrong node to
stretch. A cloud runtime is a *placement*, which is what the `Host`
resource in `internal/api/types.go` is for, and today `Host` is two fields
(`Name`, `Status`) with the comment "represents the local machine."

Two supporting facts. The Go SDK exists
(`github.com/aws/aws-sdk-go-v2/service/bedrockagentcore`), so this is a
plain module dependency rather than a language problem for a Go codebase
holding a zero-CGo posture. And telemetry can leave: setting
`DISABLE_ADOT_OBSERVABILITY=true` on the runtime unsets AgentCore's
default ADOT configuration so another platform can collect it, which is
what `question-marvel-otel-architecture` needs to stay open rather than be
decided by a vendor default.

## The partition

| Class | What | Note |
|---|---|---|
| **Ports unchanged** | manifests as data; the reconciliation loop; Team, Role and replica model (B5); shift state machine with supervisor-last ordering (B6); restart policies and heartbeat health (B7); the twelve-kind event vocabulary; admission and the declared-budget path; the agentic resource matrix | None of these has an AgentCore counterpart. AgentCore has sessions and agents; it has no team, no role, no shift, no supervisor ordering, no context-pressure admission. This is the whole argument for marvel continuing to exist next to it. |
| **Void for cloud sessions, retained for local** | the tmux driver, pane lifecycle, `send-keys` inject, `capture-pane` | Narrower than the brief assumed. Three non-test files import `internal/tmux` (`cmd/marvel/main.go`, `internal/daemon/daemon.go`, `internal/session/manager.go`) across 14 driver call sites. The substrate is already behind a seam, so cloud and local sessions coexist rather than one replacing the other. |
| **Redundant if the session is cloud** | curtain's isolation job; marvel's compute-level restart; switchboard's reach-the-box premise | Each is redundant only for cloud sessions. Local hosts still have no microVM, and the shell channel reaches only AgentCore sessions, 10 at a time. |
| **Becomes an implementation, not an adapter** | the launch path | A second `Instance` implementation plus a real `Host` type. `Adapter` stays command-shaped for local harnesses. |

## Boundary gaps, and the sovereignty price

The crosswalk named Gaps 7 through 12 as clustering at the platform's
edges. AgentCore covers five of the six, each at a stated cost.

| Gap | AgentCore surface (documented-only) | Sovereignty cost |
|---|---|---|
| 7 agent payments | payment credential providers (50 per account), `GetResourcePaymentToken` | the wallet and its spend limits live in AWS |
| 8 adversarial-content defense | Policy engines with Cedar (1,000 per account, 10 KB per policy, 400 KB schema) | screening policy is expressed in their language, in their engine, on their tool path |
| 9 inbound identity and delegation | Identity: 11,000 workload identities, `GetWorkloadAccessTokenForUserId`, OAuth on-behalf-of | the principal model becomes AWS's, which is the exact thing the identity lane wants to own |
| 10 capability registry | AWS Agent Registry: 5 registries per account, publish and approve, `SearchDiscoverableRegistryRecords` | discovery is AWS-scoped and carries no provenance chain, so it is the half sideshow does not have without the half sideshow does |
| 11 failure injection | not covered by the platform (chaos testing lives in the Strands SDK, not the runtime) | none; still unowned |
| 12 live configuration trials | AB Testing (20 active, 2 treatments each, 1 test per gateway) plus Configuration Bundles | collides with ADR-007: statistically gated automatic promotion is exactly the judging this platform reserves for humans |

Gap 12's collision is the one to carry forward. The mechanism is genuinely
the shipped answer to a thing named as missing, and its default behavior
contradicts a ratified decision. Renting the traffic-splitting while
keeping promotion a human act is possible, and it is a design constraint
someone will otherwise discover late.

Costs 7, 8 and 9 all point the same way and agree with the adopted triage
rule: rent capacity, own policy and custody. The credential-custody
question also runs into SOUL section 3 from an unexpected direction. One
operator using their own AWS credentials to reach AgentCore is squarely
inside the permitted case. Storing anyone else's tokens in AgentCore
Identity so marvel can route them is the forbidden case, and the service
makes that easy enough to do by accident that the boundary should be
written into whatever adapter ships.

## The GCP arm, and why it is split off

Google renamed the surface: Vertex AI became the **Gemini Enterprise Agent
Platform**, announced at Cloud Next April 2026 and generally available
2026-04-22. **Agent Engine is now Agent Runtime**, with the API resource
still named `ReasoningEngine` for compatibility. Agents deploy as a Python
package or a container; ADK, LangChain, LangGraph, AG2 and LlamaIndex are
named, along with custom agents and A2A.

Sandboxes are a separate service from the runtime. Custom containers (BYOC)
are supported with three constraints that bear on this question: Linux
image in Artifact Registry, a "compatible runner or entry point that the
platform can use to execute commands," and **no root privileges or access
to restricted system resources**. Templates set CPU and memory
requests and limits, expose ports, and control internet egress. Sandbox
state persists up to 14 days with a configurable TTL, and a sandbox
environment carries a 7-day TTL that each interaction resets. Code
Execution sandboxes support file I/O up to 100 MB per request or response,
have a limited filesystem and no network access, and are available in
`us-central1` only.

What I could not find in Google's documentation is the thing that changed
the AWS answer: **no interactive terminal or PTY channel is documented for
Agent Runtime or for sandboxes**, and no equivalent of the Instances
compute type where the platform manages compute inside the customer's own
project. The quota pages rendered as navigation without numbers on two
attempts, so I have no GCP figures to set against the AWS ones.

That asymmetry is large enough that forcing one answer would be dishonest.
The ruling above is an AWS ruling. **The GCP arm is peer-by-default and
under-evidenced**: nothing found contradicts a peer reading, and there is
not enough documented surface to design an adapter against. It should be
re-read before any GCP work, not treated as settled by this document.

## The obsolescence test, run explicitly

Three surfaces, run against (a) does a platform feature now cover the
commodity form, (b) is our differentiation stated in the repo, (c) would
we start this today.

- **curtain** (sandboxing): yes / yes / partially. Both vendors ship
  kernel-level isolation for cloud sessions. Local hosts have no microVM
  and both vendors' isolation stops at their own boundary. **Narrow**, do
  not retire: curtain's remaining job is the local host, and its fat-pole
  scope was already extracted to the credential plane.
- **switchboard** (remote session reach): partially / yes / yes. The shell
  WebSocket reaches AgentCore sessions and nothing else, under a
  10-connection cap, with a blind-relay property switchboard has and it
  does not. **Keep.**
- **marvel's team, role and shift layer**: no / yes / yes. **Keep**, and
  this is where the differentiation actually sits.

The measured pattern from the orc holds here without exception. The eaten
surfaces are the ones built closest to a vendor roadmap; the surviving
ones are the between-layer and under-layer.

## What this could not establish without spending money

Named as unanswerable rather than guessed:

1. Whether tmux runs inside an AgentCore container, and whether the
   CommandShell binary frame channel carries a PTY good enough for a
   full-screen TUI. The documentation asserts full-screen applications
   work. That is a vendor claim about the exact property `finding-004`
   signal 5h says is open-ended per harness and version.
2. Session cold-start latency, and therefore whether the shift timings in
   `finding-018` survive a cloud placement at all.
3. Real cost at fleet scale. The unit prices are published ($0.0895 per
   vCPU-hour and $0.00945 per GB-hour for microVMs; EC2 rate plus a 12%
   management fee for Instances, 7.8% for G-series) but our per-agent duty
   cycle has never been measured against them, and the microVM model bills
   active processing rather than wall clock, which cuts toward agents
   specifically.
4. Whether the 10-concurrent-shell cap binds per runtime or per endpoint
   in practice. This decides whether marvel can attach to more than ten
   cloud sessions at once, which is the difference between a fleet and a
   demo.
5. Whether GCP's sandbox custom containers tolerate a supervisor process
   at all, given the no-root requirement and the "compatible runner"
   entry-point contract.

## What was not established

- No claim here is measured. Everything vendor-side is documentation read
  on 2026-08-09 and should be treated as documented-only.
- Both platforms shipped material changes inside the four weeks before
  this reading (Instances GA 2026-08-06; shell execution 2026-03; agents
  after 2026-06-05 get terminals). Anything written here has a short
  shelf life, and the brief this extends went stale in three weeks.
- I did not evaluate AgentCore Memory, Gateway, Browser, Code Interpreter
  or Evaluations against their platform counterparts. The crosswalk did
  that in 2026-07 and the question here was placement, not features.
- No design was produced. The seam is named (`Instance`, plus a real
  `Host`) and nothing was specified, sized or prototyped.

## Sources

All read 2026-08-09.

- https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-service-contract.html
- https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-http-protocol-contract.html
- https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-sessions.html
- https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-get-started-command-shell.html
- https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-filesystem-configurations.html
- https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-instances-how-it-works.html
- https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/bedrock-agentcore-limits.html
- https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/observability-configure.html
- https://aws.amazon.com/about-aws/whats-new/2026/08/aws-bedrock-agentcore-runtime-instances-generally-available/
- https://aws.amazon.com/bedrock/agentcore/pricing/
- https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/bedrockagentcore
- https://docs.cloud.google.com/vertex-ai/generative-ai/docs/agent-engine/overview
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/build/runtime
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/sandbox
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/sandbox/custom-containers
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/sandbox/code-execution-overview
