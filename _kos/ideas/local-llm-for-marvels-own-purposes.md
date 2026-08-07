# A local LLM the cluster can call for its own purposes

Raised by the operator 2026-08-07, alongside
`operator-console-repl-and-lua.md`. Pre-hypothesis.

## The seed

> How could we extend to the marvel cluster an interface out to ollama,
> mlx or another local LLM for "AI scripting" for its own purposes?
> Besides native Lua with a rich marvel-specific control surface, I'd also
> like to be able to call out to an LLM (perhaps a Rust configurable LLM
> router? against which maybe the default is ollama with qwen 2B or
> something like that) for basic logic, tests, self-analysis.

## What exists today, measured

- **Marvel has no model client.** Nothing in `internal/` or `cmd/` opens
  a connection to any inference endpoint.
- What it does have is a model-name-to-context-window TABLE
  (`internal/usage/limits.go`), used to turn token counts into CTX%.
  Marvel knows model *names* and has never called one.
- **The fleet already has the tool.** `aae-orc/ai` (Go, Ollama) is a
  Unix-philosophy CLI: stdin plus a prompt in, model output out. It was
  built to compose with pipes.

That third point is the cheap path, and it is worth noticing before
designing a subsystem.

## The line this crosses

Today marvel is a control plane and the agents do the thinking. An LLM
inside marvel means **the control plane thinks**. That is a category
change, not a feature, and it lands on ADR-007 and SOUL §8:

> Automate reminding, checking, and proposing, not judging. Promotion,
> meaning, dissent, and closure remain human acts; automation computes,
> surfaces, and drafts.

Applied here that is a usable line, not an obstacle:

**Inside the boundary (propose, draft, check):**
- Summarize an event ring window into a suspected cause.
- Draft a scaling change for a human or a supervisor agent to apply.
- Generate test cases and fixtures (the seed's "tests").
- Flag an anomaly for attention.
- Screen inbound tool output for injection (vision Gap 8 names "fast
  local oracles" as the natural mechanism for exactly this).

**Outside it (judge, decide, act):**
- Deciding a session is unhealthy and restarting it.
- Deciding to scale, kill, or reclaim.
- Closing or promoting anything.

This is the same gate as the Lua write-half in the console idea, for the
same reason, and it should be one ruling covering both rather than two.

## The router is required, not a nicety

vision.md value 10's corollary: *everything we own must front multiple
external providers; no seam may bind to one vendor.* An inference seam in
marvel is therefore a router by construction. Ollama with qwen 2B is a
DEFAULT, not a binding, and mlx/vLLM/llama.cpp/a cloud endpoint have to
be reachable through the same interface from day one or the seam is
already wrong.

The 2026-08-01 language ruling permits the Rust shape the seed suggests:
marvel stays Go, and Rust enters as satellite processes where its
ecosystem wins. An inference router is a plausible satellite alongside
VFS/slotefs. Not required, though: the cheap probe below needs no new
process at all.

## Why this is the bet's first concrete instance

vision.md's bet (horizon ≈ 2027-04) is fleets on local hardware
coordinating with cloud frontier models, making **"placement across the
cost/privacy/latency/capability/cache-locality gradient"** load-bearing.
A router choosing between a local 2B and a cloud model IS that placement
decision, in miniature, for marvel's own traffic rather than the fleet's.

That makes this a cheaper test of the bet than M6 is. M6 is "run agent
workloads on local runtimes" and is gated by the tripwire (≤ 2026-10).
This is "run marvel's own small jobs locally", which needs no fleet, no
scheduler change, and no tripwire, and it exercises the same gradient.

Resource-matrix framing: a local model is a distinct point on the spend
row (near-zero marginal cost), the latency row, and the custody row
(prompt never leaves the host). Marvel *scheduling by* those rows and
marvel *using* a model are different problems that share one gradient.

## The failure mode to design against, and it is specific

"Self-analysis" is the most seductive and least verifiable item in the
seed. A 2B model asked to explain why a fleet degraded will produce
fluent, plausible, confidently-wrong prose, and it will do so at exactly
the moment an operator is stressed and least able to check it.

This is orc finding-114's lesson in a new place: a green check that means
nothing. There, stable rustfmt exited 0 on config it could not apply.
Here, a small model returns an answer whether or not it has one.

So any generated diagnosis has to arrive marked as generated and, where
possible, carrying the evidence it was derived from (event IDs, session
keys, log line numbers) so a reader can check rather than trust. An
unmarked LLM summary in an operator surface is worse than no summary.

The verifiable jobs are the honest starting set: generating test fixtures
(a test either passes or fails), screening content (a decision that can
be sampled and scored), and classification with a known answer key.
"Explain what happened" comes last, if at all.

## Cheapest first probe, which builds nothing

Do NOT build a router first. Shell out to `ai` from a read-only surface
and answer the only question that gates everything else:

**Is a 2B-class local model actually good enough for the named jobs?**

Concretely: take a real event-ring window and a real daemon log from a
degraded fleet, hand them to qwen 2B via `ai`, and score the output
against what actually happened (today's session alone has several: the
base-pane defect, the socket collision, the headless-crashed confusion).
If a 2B cannot classify those, the router is moot and the answer is a
bigger model or a different job. If it can, the router question becomes
real and well-posed.

This costs one afternoon, needs no marvel change, and its result is
useful whichever way it falls.

## Open questions

- **Which jobs, ranked by verifiability?** Test generation and screening
  are checkable; diagnosis is not. Start where you can grade.
- **Who is the caller?** Lua script, console command, the daemon itself
  on a timer, or a supervisor agent. The daemon calling a model on its
  own initiative is the version that most needs the ADR-007 ruling.
- **Where does the prompt come from?** A prompt that ships with marvel is
  behavior marvel is responsible for; a prompt from a pack is
  configuration. Pack provenance (sideshow) already has machinery for
  the second.
- **Does this reuse `ai`, absorb it, or ignore it?** SOUL §2 says compose
  with small tools. `ai` is a small tool that already does this. The
  argument for a router is multi-provider policy, which `ai` may or may
  not want to carry.
- **Custody.** A local model means the prompt never leaves the host,
  which is a real privacy property worth stating and not accidentally
  losing the first time the router falls back to a cloud provider.
  Fallback policy is a custody decision, not a performance one.

## Related

- `operator-console-repl-and-lua.md`: the Lua surface is the obvious
  caller, and both need one ADR-007 ruling rather than two.
- vision.md M6 (local model runtimes), the Bet, Gap 8 (adversarial
  screening via fast local oracles), value 10 and its multi-provider
  corollary, innovation 1 (the resource matrix).
- orc finding-114: fluent output from a tool that cannot do the job is
  the failure mode; mark generated content and carry its evidence.
- `aae-orc/ai`: fleet prior art, Go plus Ollama, already composable.
