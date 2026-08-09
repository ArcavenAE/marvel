# study: router and backend as layers, and whether the served model is knowable

**Date:** 2026-08-08
**Ticket:** `aae-orc-eooi`
**Source idea:** `_kos/ideas/router-and-backend-as-first-class-concepts.md`
**Medium:** reading this repo, reading two local inference backends over their
read-only HTTP endpoints, and reading vendor documentation. Zero model calls.
Zero writes to any operator configuration.
**Status:** study. No hypothesis was pre-registered and no success signal was
set, so this is not a probe and its output is not a finding. Three of the five
questions end unresolved on purpose.

## Evidence grades, and the standing rule

Every claim below carries one of three grades.

- **MEASURED.** Observed on this machine at the stated version, or read out of
  this repository's own fixtures and code.
- **INFERRED.** Reasoned from something measured, or read out of a vendor's
  documentation without executing it. Documentary inference is marked as such,
  because a doc is a claim about software rather than the software.
- **HYPOTHESIS.** Neither. A position I could not test.

The standing rule for this document: **before asserting anything, name the
alternative the evidence could have excluded.** Where the evidence excludes
nothing, I say so and keep the claim at HYPOTHESIS. This arc has already paid
once for an identity asserted from a measurement that could not discriminate
it (finding-016's retracted opening claim), and a study is exactly where that
mistake is cheapest to avoid and easiest to make.

I could not run liteLLM. It is not installed on this host, and the operator's
instance runs on `local-ai-mac`, which I was directed to leave untouched.
Everything in section 4 that concerns liteLLM is therefore documentary.

---

## 1. The shape marvel has today, which is narrower than the question assumes

**MEASURED (this repo).** `Runtime` names the harness and nothing below it
(`elem-runtime-names-harness`). A model is a string that `internal/usage`
keys a window table on. There is no type, field, or event anywhere in the tree
for a router or a backend.

**MEASURED (this repo).** `runtime.baseEnv` sets exactly four variables
(`MARVEL_SESSION`, `MARVEL_ROLE`, `MARVEL_TEAM`, `MARVEL_WORKSPACE`) plus
`MARVEL_SOCKET` when a socket path exists. `tmux.Driver.NewPane` passes each
as `tmux new-window -e K=V`, which layers them **on top of** the tmux server's
inherited global environment. Marvel sets no backend selector and reads none.

**MEASURED (kinu, 2026-08-08).** The default tmux server's global environment
carries no `ANTHROPIC_*`, `CLAUDE_CODE_USE_*`, or `OPENAI_*` variable. So on
this host a claude pane would reach the vendor endpoint by default.

*Alternative named:* I read one live tmux server, and it is not one marvel
launched. Every `marvel-*` socket under `/tmp/tmux-501/` is a dead file with
no server behind it. What I measured is therefore the **mechanism**
(inheritance from a server-scoped snapshot, with marvel's four variables
layered on) rather than the contents of a marvel-launched pane. The mechanism
is what the argument below needs; the contents are not.

The reframe this produces is the most useful thing in the study:

> **A router does not create marvel's blind spot. It widens one that is
> already total.** Marvel cannot name the backend in the DIRECT case either,
> because backend selection reaches the harness through an environment marvel
> neither writes nor reads.

**INFERRED,** and worth recording because it is a trap: a tmux server's global
environment is a snapshot taken when the server started. An operator who
exports `ANTHROPIC_BASE_URL` after the server is up does not change what a new
marvel pane inherits. Marvel would report a backend that a shell says is in
force and a pane says is not.

---

## 2. Question 1: is the router a distinct layer, or the backend seen from outside?

**Unresolved, and I decline to resolve it.** Both positions are defensible,
they lead to different manifest surfaces, and the fact that would separate
them has not been measured.

### Position A: the router IS the backend

The harness holds one endpoint. Whatever answers it is, by definition, the
thing that serves inference. Marvel's contract with a harness is a process and
an environment, and in that contract a router and a single backend are the
same object seen at the same distance. Fanning out behind the endpoint is the
operator's business, and modelling it means modelling a topology marvel did
not build and cannot verify.

Cost: the collapse discards exactly the two resource-matrix rows that motivate
the question. **Cache locality is a property of one backend's KV cache**, and
a router that moves a session between deployments destroys it while the
endpoint string never changes. **Rate limits bind at an account at a
backend**, and the router's own limit is a different limit (section 5).
Position A can name neither.

### Position B: the router is a distinct layer marvel sees through

The matrix forces the distinction. A model that cannot name the backend cannot
express cache locality or account-scoped limits at all, so the layer is not
optional decoration; it is where two ratified rows live.

The vocabulary also already exists on the other side of the seam.
**INFERRED (vendor documentation, not executed):** liteLLM's own model is
explicitly two-level. A `model_name` is a *model group* which may hold several
*deployments*, each with a `model_info.id`, and the docs direct a caller to
"check the `model_id` in Response Headers to make sure the requests are being
load balanced." Marvel would not be inventing a layer. It would be adopting
one the router already publishes.

Cost: marvel gains a concept it will frequently be unable to populate, and a
field that is usually empty invites being filled with a guess. That is the
failure `internal/usage` exists to refuse.

### Position C, which I did not expect and cannot test

**HYPOTHESIS.** Both A and B argue about a **session-scoped** fact, and the
thing in dispute may be **request-scoped**. If a router selects per request,
then "the backend of this session" has no referent at all, and the correct
model is neither "router is the backend" nor "backend behind router" but
*the resolved serving identity of one request*, of which the router is one
producer among several. Under C, the manifest surface is not a backend field.
It is a provenance field on every reading.

C is attractive precisely because finding-016 already reached the same shape
from the other direction ("the model is part of the reading's identity, not
context for it"), which is weak corroboration and not evidence.

### The test that would decide it, and it is cheap

**Is routing stable per session, or does it vary per request?** One `curl`
loop of N identical requests against the operator's hybrid endpoint, reading
`x-litellm-model-id` on each, answers it in minutes and requires no marvel
code. If the id is constant, Position A is nearly free and C is over-thought.
If it varies, A is unsound and the layer question is settled against it.

This was out of scope here because it touches the operator's running router.
It is the single most decision-relevant unknown in this study.

---

## 3. Question 2: can marvel learn the served model per request?

**Partly, for one harness of three, and its attestation is already
undetermined before any router enters the picture.**

### What each harness gives marvel today

**MEASURED, from this repo's parsers and captured fixtures:**

| harness | model at session start | model per request | window declared |
|---|---|---|---|
| claude | yes, `system/init.model` | yes, `message.model` on each assistant line | yes, `modelUsage[m].contextWindow` on the terminal result line |
| codex | field is parsed from `thread.started`, and **all three fixtures carry `thread_id` only** | no | no |
| opencode | no | no | no |

The codex row is worth stating precisely, because the code and the data
disagree in a way that matters. `internal/runtime/codex/parser.go` declares a
`Model` field on `thread.started`. Every `thread.started` line in
`internal/runtime/codex/testdata/*.jsonl` is `{"type":"thread.started",
"thread_id":"..."}` and nothing else. `internal/usage/limits.go` records the
same thing in prose: a codex window of 258400 was measured but "the model NAME
was not captured, because thread.started carries only thread_id." So the field
is aspirational at codex 0.146.0, and the table above reports the fixture.

**So the honest baseline is that two of three harnesses cannot tell marvel
what model served a request under any configuration, router or not.** Any
argument that a router destroys this capability presumes a capability that
exists for one harness.

### Claude's per-request model is of undetermined provenance

**MEASURED.** Across `internal/runtime/claudecode/testdata/*.ndjson`,
`message.model` appears 7 times as `claude-fable-5` and twice as
`claude-fable-5[1m]`, while the terminal `modelUsage` map is keyed
`claude-haiku-4-5-20251001`. Two facts ride in that: the session used more
than one model (finding-016's model-slot axis, visible in a nine-line
fixture), and the per-request spelling is undated while the accounting
spelling is dated.

**Two hypotheses my evidence cannot separate:**

- (a) the API returned the undated string, and Claude Code passes it through;
- (b) Claude Code substituted the name it was configured with, and the dated
  form survives only in the accounting map because that map comes from a
  different producer.

Nothing in the fixture discriminates. A response captured at the HTTP layer
would, and I did not capture one.

The consequence is sharper than the router question that prompted it:
**"is marvel's model field server-attested or client-echoed" is already open
in the direct case.** A router does not introduce that ambiguity. It converts
it from a possibility into a likelihood.

### What a router does to it

**INFERRED (vendor issue tracker, not executed).** BerriAI/litellm issue
[#22709](https://github.com/BerriAI/litellm/issues/22709) reports that since
PR #19943 (v1.81.7 onward, still present on `main` as of the report) the proxy
**overwrites the response body `model` field with the client-requested alias**,
where before it returned the resolved deployment model. The reporter notes the
provider's own headers still carry the truth, and quotes the PR author as
saying the intended value is still undecided.

If that behavior holds on the Anthropic-shaped `/v1/messages` path as well as
the OpenAI-shaped one, then under that router `message.model` is *definitively*
client-echoed, and marvel's window table would be keyed on the name the harness
asked for. Marvel would return a confident number for a request it did not
observe. I could not test the `/v1/messages` path; #22709 is filed against the
OpenAI-shaped response.

There is a second, quieter version of the same problem that does not require
any bug. **INFERRED (vendor documentation).** Claude Code discovers gateway
models by calling `GET /v1/models` against `ANTHROPIC_BASE_URL` and lists them
labelled "From gateway." Those names are the router operator's `model_name`
strings. So under a gateway, **the model namespace marvel's table is keyed on
belongs to the router, not to the vendor.** `NormalizeModel` and `aliases` in
`limits.go` encode vendor spellings. A group called `fast` or
`conversation.voice` misses the table, which is the correct outcome, but it
misses for the wrong reason: not "unknown model" but "different namespace."

### Is it structurally unknowable?

**Yes, for a describable class, and no in general.** It is unknowable when all
three of these hold, and they hold together routinely:

1. the harness names no model per request (codex, opencode today), **or** the
   router rewrites the field (#22709);
2. marvel reads only the harness's structured stdout;
3. no out-of-band channel is consulted.

Condition 2 is marvel's own choice, and it is the one marvel can change.
Section 4 is about what that would buy.

### The honest rendering

The rendering is already right and does not need changing. `Occupancy.Limit`
of 0, `Percent` meaningless, `?` on the column, `context.limit-unresolved` on
the ring. Nothing here argues for a new absence.

What is missing is not the render, it is the **reason**. `LimitSource` grades
where a denominator came from. It does not say which quantity it is
(finding-016's consequence 1, still open), and it does not say whether the
**model identity** behind that denominator was attested by a server, echoed by
a client, or read off a launch flag. Those three are different confidences and
today they are one value.

So this study adds a sibling to finding-016's open field rather than a new
mechanism: **model-identity provenance**, graded like `LimitSource` is graded.
Named here, deliberately not designed here.

---

## 4. Question 3: what does liteLLM actually expose, and what can marvel reach?

**INFERRED throughout this section (vendor documentation, not executed).**

Three channels exist, all documented, and **marvel can reach none of them from
where it currently stands.**

### (a) Response headers, which are nearly a complete answer

liteLLM's response-header documentation lists, among others:

- `x-litellm-model-id`, the deployment id from `model_info.id`
- `x-litellm-model-api-base`, the provider's API base URL
- `x-litellm-model-group`, the routed model name the client requested
- `x-litellm-call-id`, `x-litellm-version`
- `x-litellm-response-cost`, `x-litellm-key-spend`
- `x-ratelimit-limit-requests` / `-tokens`, `x-ratelimit-remaining-*`,
  `x-ratelimit-reset-*`
- upstream provider headers, passed through with an `llm_provider-` prefix

Read that list against this study's title. `x-litellm-model-group` is what the
harness asked for, `x-litellm-model-id` is which deployment served it, and
`x-litellm-model-api-base` is where that deployment lives. The router publishes
the router/backend distinction, per request, already.

**And marvel cannot see one byte of it.** The harness owns the HTTP
connection; marvel reads the harness's stdout over a FIFO. The headers are
consumed and discarded inside a process whose only contract with marvel is a
JSON line stream. This is the same wall `aae-orc-reif` names for rate limits,
observed from a different side.

### (b) Admin API and spend logs

liteLLM persists per-request rows to a `LiteLLM_SpendLogs` table with a UI and
query surface over it, and serves `/model/info` carrying `model_info`
including context-window fields (`max_input_tokens`, with `max_output_tokens`
alongside). A `/spend` route exists behind a permission grant.

For marvel this is the interesting one, because it is **pull, not intercept**.
It needs no harness cooperation, no header capture, and no change to the
process model. It also survives multi-host, which no file or sqlite channel in
the CTX% catalog does: it is an HTTP endpoint, reachable from wherever the
daemon runs.

### (c) OTEL

liteLLM ships an `otel` callback emitting GenAI-semconv spans with model,
provider, token usage, and cost; an opt-in v2 mode
(`LITELLM_OTEL_V2=true`) produces one trace per request covering auth,
guardrails, the LLM call, and DB writes.

This lands directly on `question-marvel-otel-architecture`, currently HELD.
Worth recording that the first concrete OTEL producer marvel might consume is
not a harness at all.

### The consent grading needs a cell it does not have

In `ctx-channel-consent-and-fidelity.md` the axis is contracted / conceded /
appropriated, and the counterparty is implicitly the **harness vendor**.

A router channel is **contracted** by that definition (documented HTTP API,
documented headers, versioned). But its counterparty is neither the harness
vendor nor marvel. It is **the operator's own service**, running on the
operator's host, holding the operator's keys.

That is the cleanest consent case in the entire catalog. The file already
reached the rule that "the consent that matters here belongs to the operator,
who owns both tools," and had to add machinery (manifest opt-in, table
allowlists) to make an appropriated sqlite read safe. A router read needs none
of that: the operator configures marvel with the router's URL and a key, and
consent is the act of configuring it.

It also has no perturbation problem, which the same file flags as the axis's
blind spot. Reading `/spend/logs` does not register marvel as a live client the
way attaching to Crush's stream does.

### The backend can publish the denominator too, and two of them do

**MEASURED (kinu, 2026-08-08, read-only GET, no inference and no model load):**

- LM Studio's native `GET /api/v0/models` returns per model: `id`, `type`,
  `publisher`, `arch`, `compatibility_type`, `quantization`, `state`, and
  **`max_context_length`**. Observed: `qwen/qwen3-coder-30b` 262144,
  `google/gemma-4-12b` 262144, `qwen/qwen3.6-35b-a3b` 262144,
  `qwen/qwen3-0.6b` 40960.
- LM Studio's OpenAI-compatible `GET /v1/models` returns `id`, `object`, and
  `owned_by`. **No window field.**
- ollama's `GET /api/tags` returns `details.context_length` per model.
  Observed: `qwen3:1.7b` 40960.

*Alternative named, and it matters:* `max_context_length` is the model's
maximum, not the window the server actually loaded it with. Every model in the
snapshot read `"state": "not-loaded"`, so I could not observe a served window
and cannot claim these two numbers are the same. **INFERRED (vendor
documentation, not executed):** LM Studio's `POST /api/v0/chat/completions`
response carries a `model_info` block including `context_length`, per
response.

Which produces a small, sharp irony worth carrying forward: **the served window
is published on the one route nobody in the chain calls.** A harness or a
router speaks OpenAI-compat `/v1`, which omits it. The native `/api/v0` path
carries it. The information exists and the wiring does not.

---

## 5. Question 4: where do rate limits and spend accrue, and can marvel attribute them?

### There is more than one limit, and `reif` currently assumes one

**INFERRED (vendor documentation).** Behind a liteLLM router at least two
independent limit populations exist:

1. **The router's own**, enforced at key / user / team / org scope
   (`tpm_limit`, `rpm_limit`, budgets), plus per-deployment `rpm`/`tpm` that
   are used for **routing decisions** rather than as hard limits unless
   `enforce_model_rate_limits` is set. Spend accrues to the virtual key and to
   the attached user and team rows.
2. **The provider's**, still binding upstream at the account behind whichever
   deployment served the request. liteLLM standardizes the provider's headers
   into the OpenAI `x-ratelimit-*` shape and returns them, and returns
   unstandardized ones under `llm_provider-`.

Two consequences.

**For `reif`:** "the headroom" is not one number behind a router. A fleet can
be refused by the router while the provider account has capacity, or sail past
the router's accounting while the provider throttles. A single headroom gauge
would be wrong in one direction or the other and would not say which.

**For the routing itself:** a deployment's `rpm`/`tpm` are *routing inputs*
by default. The router will steer around a busy deployment silently. From
marvel's side that is indistinguishable from nothing happening, and it is
exactly the mechanism that moves a session off a warm cache.

### Attribution splits cleanly, and not in the direction I expected

**Spend: yes, and a router makes it better than the status quo.** Marvel
creates sessions and teams. A virtual key per team, or per session, is a
partition marvel can define, and the router then attributes spend to it in its
own store without marvel touching a provider bill. This is the first mechanism
in the whole arc that makes **fleet-wide** spend attributable. It is an
argument for routers that has nothing to do with model selection, and it is
the strongest one I found.

**Limits: no, or only weakly.** A limit binds over whoever shares the key or
the deployment. Marvel owns that partition only for the keys it minted, and
never for the provider account underneath, which is shared with the operator's
interactive use and with anything else pointed at the same credentials.
Attributing a refusal to a session is possible; attributing *scarcity* to one
is not.

**HYPOTHESIS:** the useful marvel-side quantity behind a router is not
headroom but **refusal attribution**: which sessions were refused, by which
layer, over what window. That is a count marvel can get from the router's spend
log without solving the harder measurement.

---

## 6. Question 5: which of KNOW / PRESENT / MANAGE is marvel's?

### KNOW: yes, and the cheapest first step needs no router at all

Marvel already constructs the process environment at spawn (enforcement locus
1, the built one). The cheapest possible step is to **record the ambient
backend-selecting environment as observed at spawn** and attach it to the
session: `ANTHROPIC_BASE_URL`, the `CLAUDE_CODE_USE_*` family finding-016
already enumerated, `ANTHROPIC_BEDROCK_SERVICE_TIER`, and the equivalents for
other harnesses.

Three properties recommend it. It requires no router cooperation, no header
capture, and no new dependency. It converts "unknown" into "observed to be
this value at this time," which is loud-absent rather than silent-wrong. And
**it fixes the direct case too**, which section 1 established is currently
just as blind as the routed one.

*Alternative named:* recording the environment records what marvel handed the
harness, not what the harness used. A config file, a project-scope setting, or
a later override can move it, exactly as finding-016 found for the
auto-compact window. So this is an **assignment record**, and finding-016's own
ruling applies verbatim: assignment without observation is open-loop. Recording
the env is the "expected" half; a router or backend read is the "realized"
half. They ship together or the record is a claim rather than a measurement.

### PRESENT: yes, conditionally, and the discipline already exists

Present what was resolved and where it came from, never a synthesized best
guess. The `?`-rather-than-guess rule generalizes from the denominator to the
serving identity without modification.

The conditional is that a presented backend must be graded, or an operator
will read a recorded env var as an observation. Same failure, one layer over.

### MANAGE: no, and the strong arguments are not the one that first comes to mind

The reflex is SOUL §2, no conscription. I decline to lead with it, because the
CTX% consent review already recorded that reaching for §2 in an adjacent case
was a misuse: §2 is a dependency-direction rule, and marvel configuring a
router the operator installed compels the router to do nothing. Three better
arguments:

1. **SOUL §3, auth delegation.** A router holds provider credentials for every
   backend it fronts. That is a credential store. Marvel managing its config
   puts marvel adjacent to exactly the boundary the `opencode.db` argument was
   built to protect, and with a larger blast radius, since the router's keys
   are live production credentials rather than one harness's OAuth token.

2. **Supply chain, and this one is not hypothetical.** liteLLM's PyPI package
   was compromised on 2026-03-24: versions 1.82.7 and 1.82.8 shipped a
   credential stealer, 1.82.8 via a `litellm_init.pth` that executes on every
   Python process start in the environment. The vendor published an advisory
   and the tracking issues are
   [#24512](https://github.com/BerriAI/litellm/issues/24512) and
   [#24518](https://github.com/BerriAI/litellm/issues/24518). If marvel
   installed or upgraded a router, marvel would be inside that blast radius,
   and the platform's own ruling is the opposite: upstream installers execute
   once in auditable CI, never on a user machine (sideshow's
   frozen-composition rule). Reading a router's API carries none of this;
   installing one carries all of it.

3. **Vision value 10.** Own the policy, the record, and the judgment; rent
   capacity through standard seams. A router is capacity. **Marvel should own
   the record of what routed where, not the routing.**

**The boundary case I will not resolve:** minting a per-team virtual key is
management, and section 5 argues it is the one management act that buys
attribution obtainable no other way. It is also the smallest possible one: a
key is created, scoped, and handed to a spawn, and nothing about routing
policy is touched. It sits exactly on the line, and it belongs to the operator
to place.

---

## 7. What this study does not settle

- **Whether routing is per-session or per-request in the operator's actual
  hybrid configuration.** Not measured; `local-ai-mac` was explicitly out of
  scope. This is the fact that decides section 2.
- **Whether Claude Code's `message.model` is server-attested or client-echoed
  in the direct case.** Two hypotheses, no discriminating evidence.
- **Whether liteLLM's `/v1/messages` path shares the `model`-overwrite
  behavior of #22709**, which is filed against the OpenAI-shaped response.
- **Whether any harness surfaces provider response headers at all.** That is
  `reif`'s scope and this study assumed the answer is no without testing it.
- **Whether LM Studio's `max_context_length` equals the window a loaded model
  actually serves.** Nothing was loaded and I did not load anything.
- **Anything about liteLLM by execution.** Section 4 is entirely documentary.

## 8. What would kill each position

- **Position A dies** if one session is observed changing deployment
  mid-session. The endpoint-is-the-backend collapse cannot survive that.
- **Position B dies** if the operator rules the router a deliberate abstraction
  boundary. Then "the router is the backend" is correct by fiat and section 2
  is over.
- **The whole urgency dies** if routers here are only ever configured one
  backend per model group, in which case the layer is real but constant and a
  spawn-time record answers everything.
- **The KNOW recommendation dies** if the recorded environment turns out to be
  routinely overridden downstream, making the record a claim that reads like a
  measurement. That is finding-016's exact failure and it is the thing to check
  first.

## 9. Relationship to existing artifacts

- **finding-016** named backend service as one of six denominator axes. This
  study says that axis is currently unread even without a router, and adds a
  second missing field beside the one finding-016 asked for: denominator
  identity (which quantity) plus **model-identity provenance** (attested,
  echoed, or flag-derived).
- **`elem-runtime-names-harness`** holds. Router and backend are a third thing
  and must not be crammed into `runtime`; the liteLLM model-group/deployment
  split is documentary corroboration that the layer below the harness is
  itself two-level.
- **`elem-agentic-resource-matrix`.** Cache locality and token spend are the
  two rows a router most directly disturbs, and spend is the one it improves.
- **`ctx-channel-consent-and-fidelity`** gains a channel class whose consent
  counterparty is the operator's own service rather than a harness vendor,
  which is a cell the three grades do not have and the cleanest case in the
  catalog.
- **`aae-orc-reif`** should absorb that headroom is at least two numbers
  behind a router, and that refusal attribution may be the tractable substitute.
- **`aae-orc-2lfg`** remains the likely first manifest surface, and this study
  argues that whatever field it adds should carry a provenance grade rather
  than a bare value.
- **`question-marvel-otel-architecture`** (HELD) gains a data point: the first
  concrete OTEL producer marvel might consume is a router, not a harness.

## Provenance

Written 2026-08-08 against `aae-orc-eooi`, from the operator's idea file of the
same date. In-repo measurements were taken at marvel `9b96e03`. Local backend
measurements were taken on kinu against LM Studio's server on 127.0.0.1:1234
and ollama on 127.0.0.1:11434, by read-only GET only: no inference request was
issued, no model was loaded, and no configuration file of any kind was read or
written. No liteLLM instance was contacted.

## Sources

- [LiteLLM: Response Headers](https://docs.litellm.ai/docs/proxy/response_headers)
- [LiteLLM: Proxy Load Balancing](https://docs.litellm.ai/docs/proxy/load_balancing)
- [LiteLLM: Virtual Keys](https://docs.litellm.ai/docs/proxy/virtual_keys)
- [LiteLLM: Budgets and Rate Limits](https://docs.litellm.ai/docs/proxy/users)
- [LiteLLM: Spend Tracking](https://docs.litellm.ai/docs/proxy/cost_tracking)
- [LiteLLM: /v1/messages (Anthropic format)](https://docs.litellm.ai/docs/anthropic_unified/)
- [LiteLLM: Use Claude Code with Non-Anthropic Models](https://docs.litellm.ai/docs/tutorials/claude_non_anthropic_models)
- [LiteLLM: OpenTelemetry integration](https://docs.litellm.ai/docs/observability/opentelemetry_integration)
- [LiteLLM: OpenTelemetry v2](https://docs.litellm.ai/docs/observability/opentelemetry_v2)
- [LiteLLM issue #22709: response body "model" returns the alias, not the resolved deployment](https://github.com/BerriAI/litellm/issues/22709)
- [LiteLLM issue #24512: malicious litellm_init.pth in 1.82.8](https://github.com/BerriAI/litellm/issues/24512)
- [LiteLLM issue #24518: PyPI package 1.82.7 and 1.82.8 compromised, timeline and status](https://github.com/BerriAI/litellm/issues/24518)
- [LiteLLM: Security Update, Suspected Supply Chain Incident (March 2026)](https://docs.litellm.ai/blog/security-update-march-2026)
- [LM Studio: REST API v0 endpoints](https://lmstudio.ai/docs/developer/rest/endpoints)

---

# Addendum, second pass, 2026-08-09

Written against the same ticket after a premise check. The first pass stands
unedited above; this pass adds what it did not reach and rules on the two
questions it left unasked. Same three evidence grades, same standing rule.

**Premise check.** The five questions the ticket names are answered above, and
the structural claims re-verify at HEAD `0886801`: the word "backend" occurs
once in the Go tree, in `internal/api/store.go` about bolt persistence, and
"router" occurs nowhere. So the concepts are still absent and the first pass
is still accurate. What was missing was a graph node, a ruling on where these
concepts sit in the committed vocabulary, and one case.

**The case.** `gemini` appears zero times in the study and zero times in the
source idea file. It is the sharpest instance of the question, and
`finding-016` already carried it: "router-initiated switch. gemini routes
between models mid-session and announces it only on a separate
`model_routing` event."

## 10. There are two routers, and the first pass modelled one

The study treats a router as a network hop between the harness and a backend,
addressed by an environment variable and observable, in principle, over HTTP.
That is one of two.

- **External router.** liteLLM. A separate process reached over the network.
  Sections 2 through 5 above are about this one.
- **Internal router.** A model-selection decision made inside the harness
  process, over a policy marvel never sees, published only in whatever the
  harness chooses to emit.

The internal router is not hypothetical and not confined to one vendor.
`finding-016` lists three mechanisms: claude runs up to three model slots in
one session (`ANTHROPIC_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`,
`CLAUDE_CODE_SUBAGENT_MODEL`), codex emits
`codex.compaction.model_fallback` with a `model_downshift` reason, and gemini
routes on `model_routing`.

**MEASURED (this repo, HEAD).** Marvel's own code already names the internal
router, and solves it. `internal/usage/sample.go:104` documents why a terminal
sample's window must be indexed by the session's primary model:

> Selecting that entry by anything else (first key, max, a range) is wrong: a
> session routing across models carries several entries with windows differing
> by 5x, and Go map iteration is randomized.

**MEASURED (fixtures).** The 5x is real and it is in two of this repo's own
claude fixtures. `hello.ndjson` and `tool_call.ndjson` each carry a terminal
`modelUsage` map with two entries: `claude-haiku-4-5-20251001` at
`contextWindow` 200000, and `claude-fable-5[1m]` at 1000000. One session, one
terminal record, two denominators five times apart.

So "the session's context window" is not one number in the simplest shipped
fixture, and the reason is a router inside the harness. Marvel handles it by
keying on `primaryRaw`.

**The consequence for any manifest surface.** A `backend:` field beside
`runtime:` describes the external router and cannot describe the internal one,
because the internal one is not configuration marvel supplies. It is harness
behavior. A design that adds the field handles liteLLM and misses the case
that already perturbs a shipped adapter.

## 11. Model identity and denominator identity are independent

The first pass framed section 3 as "can marvel learn the served model", and
treated that as the thing a router threatens. Two of marvel's own harnesses
say the question is the wrong one, and marvel's type system already separates
them: `internal/usage/profiles.go` carries `modelFromStream` and
`limitInStream` as independent booleans on the `profile` struct.

**MEASURED (this repo).** The occupied cells, with the fourth from documentary
research rather than code:

| harness | names its model | declares its window | consequence |
|---|---|---|---|
| claude | yes, per request | yes, per model, on the terminal line | router is announced on the same record as the count |
| codex | no, not in the exec stream | no, not in the exec stream (the rollout declares it twice) | model name would key nothing |
| opencode | no | no | neither term |
| gemini | on a separate event (INFERRED) | nowhere (INFERRED) | the two terms must be joined |

Codex is the case that breaks the intuition, and `finding-017` settled it:
the model name IS captured, in `turn_context.model` and on ten of eleven hook
payloads, and naming it keys nothing, because every model on the host reports
258400 against a catalog giving all eight models identical values. "The window
is keyed by model" and "the window is one number for this account and plan"
predict identical data, so a table entry would be a per-account plan limit
wearing a model name.

Read the two together and the ruling falls out. **Marvel needs the
denominator. It needs the model only to know when to invalidate a learned
denominator.** Codex shows the denominator can be sound while the model key is
worthless. Gemini would show the converse: a model key that changes usefully
often, against no denominator at all.

**The refinement, and it is sharper than "does the harness name its model".**
Claude also routes between models, and it costs marvel nothing, because
`message.model` rides the same record as the token counts it describes. Gemini
splits them: the numerator arrives on `AfterModel`, the model identity on
`model_routing`. A consumer must join two event streams, and every gap in that
join attributes a count to the wrong denominator, silently.

So the harmful property is not the router and not the missing name. It is
**the model identity and the token count arriving on separate records**. That
is a third profile axis the struct does not have, and it is the one worth
adding when a harness forces it.

This is the FEED-2 versus FEED-N split stated in the terms this study was
asked about. A FEED-2 channel carries both terms, so the router is harmless
whether it is internal or external. A FEED-N channel carries the numerator
only, so marvel supplies the denominator from a table keyed on a model the
router is free to change. **A router only hurts on FEED-N.**

## 12. Ruling: no new primitive, and specifically not three of them

The ticket asks whether router and backend are new primitives, refinements of
B14's LLM term, or properties of the runtime. None of the three.

**Not a B14 term.** B14's composition is `Agent = Persona + Identity + Role(s)
+ LLM + Tools`. Its five terms are library items selected when an agent is
composed. A backend is not selected at composition time; it is resolved at
request time, sometimes by a party that is neither the operator nor marvel.
Adding it to a composition of authored artifacts would put a runtime outcome
in a list of design choices.

**Not a runtime property.** `elem-runtime-names-harness` is bedrock and
`runtime` names the harness. The first pass is right that a router is a third
thing. Section 10 adds the reason it cannot be folded in anyway: the internal
router is the harness's own behavior, so a field describing it would describe
the adapter, not the deployment.

**Not a resource-matrix row.** Cache locality and token spend are already rows
of the seventeen. A router changes their values and their attribution. It does
not add a resource.

**What is warranted instead** is the thing the first pass already named from
the other direction, and it is a field on a struct that exists rather than a
type that does not: a **provenance grade on the reading's model identity**,
ranked the way `LimitSource` is ranked, distinguishing server-attested from
client-echoed from launch-flag-derived. Section 11 adds a second candidate,
the same-record axis on `profile`. Both are cheap, both are local to
`internal/usage`, and neither claims marvel owns a router.

The failure mode this avoids is on the record in this repo's own CLAUDE.md:
`Pack`, `Vault`, `Volume`, `Schedule`, `Gateway` and `Readycheck` survive as
model-only prose with no type and no code. A `Router` resource today would be
the seventh.

## 13. The one place marvel has already decided the backend does not matter

Abstract questions about modelling a backend have one concrete site.

**MEASURED (this repo).** `NormalizeModel` in `internal/usage/limits.go`
strips `us.`, `eu.`, `apac.` and a leading `anthropic.` before a window
lookup. Its documented reason is regional: "Pricing differs by region; the
window does not." The rule is stated about regions and also erases the
provider, because `us.anthropic.claude-sonnet-4-6` is a Bedrock model id and
the bare form is not. Both collapse to one key.

`finding-016` axis 4 says the same model id through a different provider can
carry a different window, and axis 5 says the 1M window is gated by a beta
header per account per backend rather than being intrinsic to the model.

I am not claiming the strip is wrong: no measurement here compares a Bedrock
window against a direct one for the same id, and the regional half of the
justification looks sound. I am claiming this is the site. If "backend" ever
has to become a key in marvel, it becomes one here first, and the question is
narrow enough to settle with one measurement rather than a design.

## 14. The trigger

The honest answer to the ticket is that no new primitive is warranted **yet**,
and the trigger is specific enough to recognize without judgment:

> **Marvel adopts a harness that routes between models AND publishes no
> per-model window on the record carrying the token count.**

Gemini is that harness. Marvel ships six adapters (claude, codex, opencode,
forestage, simulator, generic) and gemini is not among them; the string
appears twice in the Go tree, both in a `doc.go` comment saying it was out of
scope and unavailable to measure. So the trigger has not fired.

A second trigger, cheaper and named in section 2 above: the operator's `curl`
loop against the hybrid endpoint reading `x-litellm-model-id` on N identical
requests. If the id varies, Position A is unsound and the external layer is
real. If it is constant, a spawn-time record answers the external case
entirely.

Either one firing moves this from a concept marvel declines to carry into one
with a measured reason to exist.

## 15. What this pass did not establish

- **Anything about gemini by measurement.** Every gemini claim here is
  documentary, from the channel research recorded on `aae-orc-6c2r` (itself
  labelled desk measurement, not a probe run) and from `finding-016`. I did
  not run gemini, read its source, or see a `model_routing` event.
- **Whether the same-record property is the right third profile axis** or a
  special case of something more general. One harness suggests it; one harness
  is not a taxonomy.
- **Whether a Bedrock window differs from a direct window for any id marvel's
  table carries.** Section 13 names the site, not the answer.
- **Whether claude's `message.model` is server-attested or client-echoed.**
  Unchanged from the first pass: two hypotheses, no discriminating evidence.
- **Everything the first pass listed in section 7.** None of it was retested.

## Addendum provenance

Written 2026-08-09 against `aae-orc-eooi`, in an isolated clone at marvel HEAD
`0886801`. Method: reading this repository and its fixtures, plus the graph
artifacts cited. Zero model calls, zero network reads, no harness started, no
liteLLM instance contacted, nothing outside the clone touched.

---

# Third pass, 2026-08-09: the provider is a window key, measured

Prompted by a Crush data point relayed from the `k2mi` rig: `crush stats`
reports Messages by Provider and Usage by Model as separate breakdowns, and
prices per day by provider ($0.00 for a local model). The question that makes
that evidence rather than decoration is whether the split is real in the data
or produced by the renderer, so I went looking for the stored form.

I did not start a Crush server. Starting one refreshes host-global
`providers.json` and `hyper.json`, because `--data-dir` scopes only the
project database.

## 16. What marvel's own research already settled

Two of the four questions were already answered in this repo, in
`probe-interactive-ctx-remainder-sweep.md` round 3, measured against Crush
v0.88.0:

- **Crush is already tiered FEED-N here.** Occupancy rides the server SSE
  session frame; `model.context_window` comes from a separate REST call to
  `/v1/workspaces/{id}/agent`. The brief's own words: this "splits Crush's
  feed into FEED-N plus a one-shot lookup rather than a FEED-2." So Crush sits
  on the side of the section 11 split where a router hurts, and it is the next
  adapter in the queue (`aae-orc-k2mi`, in progress today).
- **The denominator source is a catalog, not a per-message field.**
  `~/.local/share/crush/providers.json`, 40 providers, and every model
  carries a `context_window`.

## 17. The measurement: 141 of 249 shared model ids disagree on the window

**MEASURED (kinu, 2026-08-09, read-only `jq` over a static file; no server
started, no request issued).** Crush's catalog is keyed provider first, model
second, and each model entry carries both `context_window` and per-provider
pricing.

| quantity | value |
|---|---|
| providers | 40 |
| distinct model ids | 948 |
| model ids offered by more than one provider | 249 |
| of those, ids whose `context_window` DISAGREES across providers | **141** |
| of those, disagreeing by 1.5x or more | **52** |

Some disagreement is cosmetic (1000000 against 1048576 is decimal against
binary). The 52 are not: `claude-sonnet-4-6` is 200000 at `anthropic` and
1000000 at `vertexai`; `grok-4.5` spans 328000, 500000 and 1000000.

**Every model id in marvel's shipped table is provider-variable, at exact
spelling.** All seven keys, ignoring the `[1m]` suffixed forms:

| marvel table key | marvel's value | catalog spread across providers |
|---|---|---|
| `claude-haiku-4-5` | 200000 | 200000, 204800 |
| `claude-fable-5` (as `[1m]`) | 1000000 | 264000 (copilot), 1000000 |
| `claude-sonnet-4-6` | 200000 | 200000 (anthropic), 1000000 (vertexai and three others) |
| `claude-sonnet-5` | 1000000 | 264000 (copilot), 1000000 |
| `claude-opus-4-7` | 200000 | 200000 (aihubmix), 1000000 (anthropic) |
| `claude-opus-4-8` | 200000 | 200000 (aihubmix), 1000000 (anthropic) |
| `claude-opus-5` | 1000000 | 264000 (copilot), 1000000 (anthropic) |

`claude-opus-5` is the cleanest collision: marvel's table returns 1000000, and
a copilot-served model of that exact id is catalogued at 264000. Marvel would
resolve `LimitFromTable`, the most confident non-measured rung, and be wrong
by 3.8x with no signal.

**A hypothesis I formed and the data refuted.** Copilot's rows show cost 0 for
every model, which looked like the codex pattern from finding-017: a
per-account plan limit wearing a model name. It is not. Copilot's 30 models
carry nine distinct windows (16384 through 1048576), with 264000 on 11 of
them. So copilot CLAMPS a subset of models below their native window and
leaves the rest alone, which is a third pattern beside "keyed by model" and
"one number per account". The uniform thing at copilot is the price, not the
window, and price and window turn out to be provider effects of different
shapes.

## 18. What this does not establish, and it matters

This is **Crush's catalog**, a third party's curation of 40 vendors. It is not
a measurement of any vendor's served window. Two reasons to hold it loosely:

- A catalog can be stale or wrong, and nothing here checks one against a live
  API.
- **Crush and marvel disagree about which axis carries the 1M window.** Marvel
  puts it in the model name (`claude-opus-4-8` 200000 beside
  `claude-opus-4-8[1m]` 1000000, the entitlement axis, finding-016 axis 5).
  Crush puts it in the provider row (anthropic's `claude-opus-4-8` is
  1000000). At least one of them is modelling the axis wrong, and marvel
  cannot tell which from here. Provider and entitlement are entangled, so
  "key the table by provider" would not resolve the entanglement. It would
  relocate it.

What the measurement DOES establish is narrower and still decisive: **the one
shipping harness in this survey that fronts many providers, and that maintains
a 1405-model catalog precisely to answer "what is this model's window", found
it necessary to key that catalog by (provider, model) rather than by model.**
That is an independent design vote on the exact question of section 13, cast
by someone who had to ship an answer.

## 19. The consequence, which is sharper than the first pass had it

finding-016 argued the table is the rung of last resort because its **miss**
rate is structural. This adds the worse half: **the table's HIT can be wrong.**
For all seven keys marvel ships, a correct-looking exact match returns a
number whose truth depends on a provider marvel does not record, does not
read, and has no field for. A miss renders `?`, which is the designed
behavior. A wrong hit renders a confident number, which is the failure
`internal/usage` exists to prevent.

Two fixes, both inside `internal/usage`, neither a new type:

1. **Strengthen the KNOW step the first pass already recommended.** Recording
   the ambient provider-selecting environment at spawn stops being a nice
   provenance record and becomes the input to (2). Section 6 argued for it on
   the grounds that it fixes the direct case; this argues for it on the
   grounds that the table is unsound without it.
2. **A provider-sensitivity guard on the table.** Where an id's window varies
   by provider and the provider is unknown, resolve `?` rather than the
   default. That is the `?`-rather-than-guess discipline applied one level up:
   today it fires on an unknown model, and it should also fire on a known
   model whose answer depends on something unknown.

`NormalizeModel` is where this lands, and section 13's framing needs
correcting. I called the `anthropic.` strip "the site, not the answer" and
said no measurement compared a Bedrock window against a direct one. The
comparison above is not that measurement either, but it is close enough to
retire the idea that the question is speculative. The strip's stated
justification ("Pricing differs by region; the window does not") is sound
about regions and silent about providers, and providers are where the 5x
lives.

## 20. The ruling is unchanged, and here is why the new evidence does not move it

The provider is a real key. It is not a marvel primitive.

Crush separating provider from model is a stronger version of the same
observation the first pass made about liteLLM's model-group/deployment split:
the layer below the harness is genuinely two-level, and Position A ("the
router IS the backend") is weakened further, since a shipping harness names
provider and model as different things at the same distance. None of that
requires marvel to grow a type. It requires a field marvel already has
somewhere to be keyed on one more thing, and a guard that refuses a lookup it
cannot key.

On the cost half of the data point: **marvel never prices.** There is no
price table anywhere in `internal/usage`; `Sample.CostUSD` is a `*float64`
copied from whatever the harness reported (`sample.go:134` and `:148`), and
opencode's fixtures report `"cost":0`. Crush's provider-priced cost column is
Crush doing a job marvel deliberately does not do. So provider is load-bearing
for the WINDOW and not load-bearing for SPEND, which is the opposite of what
the cost column suggests at a glance.

The trigger from section 14 stands unchanged. This adds a second, independent
consequence on a different axis, and both now point at the same near-term
event: the Crush adapter (`aae-orc-k2mi`, in progress). Crush is FEED-N by
this repo's own tiering, and Crush is the harness whose catalog demonstrates
provider-keyed windows. Whoever ships that adapter meets both.

## 21. Fourth pass: the schema answers, and one negative I did not predict

The `k2mi` rig answered the four schema questions by measurement (crush
v0.88.1, isolated rig, `CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1` to avoid the
global-cache refresh, zero host drift). Full detail in
`finding-020-crush-context-pressure-channel.md`, marvel#166. Three of my open
items close and one framing of mine needs correcting.

**The hedge resolves in favor of the argument.** `provider` is its own
nullable `TEXT` column on `messages`, beside `model`, both added by later
migration; live rows read `provider='ollama'` and `model='qwen3:0.6b'`
separately. It is not liteLLM's `model_group` shape. Section 18's design-vote
reading stands at the stored level, not just the catalog level: Crush keys by
provider in the catalog AND records provider per message in the database.

**Per-message, and unconstrained.** `sessions` carries no provider or model
column at all, so nothing structurally prevents one session from holding rows
with two providers. Not observed on the rig, where every assistant row read
`ollama`.

**No window in the database.** Zero hits for `context`, `window` or
`max_token` across all five tables. My section 11 harmful case is confirmed in
the strongest available form, and the rig's own formulation is sharper than
mine: the DB has per-message model attribution and no window, the REST route
has the window and no history, and neither surface joins them. Worse than
separate records, they are separate surfaces, and the REST route reports the
workspace's CURRENT agent model, so it cannot answer what window applied to a
past message even in principle.

**The negative, and it corrects section 10 rather than confirming it.** I have
been treating a per-message provider column as a routing record. It is not a
complete one. Crush has two model slots, `models.large` and `models.small`.
With the slots configured to different models, a TUI session's title
generation ran on the small model and **no row appeared**; the output landed
in `sessions.title`. Summarization is the same class. So
`messages.provider/model` records the model that served each persisted
conversational turn, not every model call the session made, **and carries no
marker distinguishing the two**.

That makes Crush a fourth instance of the internal router alongside claude,
codex and gemini, and the first to demonstrate a distinct failure: the
harness's own routing record is silently partial. It is the same shape as
finding-016 axis 6 (claude's three model slots), arriving in a different
vendor with a different persistence story.

The design consequence is a caution rather than a new recommendation. A marvel
that reads a harness's provider field to answer "what served this session"
gets a true answer about a subset and no signal about the remainder. Reading
the harness's own record is better than guessing and is still not a
measurement of every call.

**Not relied on here:** the `crush stats` page that prompted this thread. Its
`usage_by_model` entries carry `{model, provider, message_count}` and no token
fields, and its totals sum `sessions.prompt_tokens`, which is a per-request
LEVEL rather than a running total. Measured on the rig: a three-turn session
issuing 28672 / 28712 / 28782 contributed only 28782, undercounting by about
3x. The provider/model split it displays is real and stored; the token
aggregation beside it is not a quantity to price anything off. Cost is stored
per session (`sessions.cost REAL`), not per message, and the 0.0 for ollama is
a stored literal from catalog pricing rather than a render-time derivation.

**The ruling is unchanged by the fourth pass, and the trigger is unchanged.**
Nothing here argues for a primitive. Two of the three fixes already named
absorb it: the spawn-time environment record is what tells marvel which
provider a session was pointed at when the harness's own record is silent, and
the provider-sensitivity guard is unaffected. What is new is a bound on how
much a harness-read can ever deliver, which belongs in whatever design
`aae-orc-k2mi` produces.

## 22. Fifth pass: the workspace is a routing locus, and it defeats my own recommendation

Two further results from the `k2mi` rig, neither of which I asked for, and the
first of which lands on the recommendation this study has been strengthening
for three passes. Both relayed as measured by that rig, not re-measured here.

**Crush executes a project-local `.crushrc` as bash at config load.** Measured
firing on `crush run` with no trust prompt, and outside the `allowed_tools`
permission list, because it runs before any agent turn exists. A `crushrc` is
documented as building configuration by calling builtins including `provider`
and `model`.

That makes the **workspace** a routing locus, and it is a fifth one, distinct
from the four in section 10. Routing can be decided by marvel's constructed
environment, by the operator's global config, by the harness's internal slot
policy, by an external router, and now by a script shipped in the repository
being worked on.

**The consequence for section 6's KNOW recommendation is direct and negative.**
Recording the ambient provider-selecting environment at spawn is what the
fourth pass called load-bearing, on the grounds that it is what tells marvel
which provider a session was pointed at when the harness's own record is
silent. A `.crushrc` overrides that after the record is taken. Marvel would
hold a spawn-time observation that is accurate about what marvel handed over
and wrong about what ran.

This is finding-016's open-loop ruling arriving with a concrete mechanism
rather than as a caution. The first pass already stated it in the abstract:
recording the environment records what marvel handed the harness, not what the
harness used, so the record is an assignment and "assignment without
observation is open-loop". Section 19 then leaned on that record anyway,
because the provider-sensitivity guard needs an input. The guard still needs
one; what changes is that a spawn-time environment read cannot be that input
on its own, and the honest grade for a provider derived that way is
**assigned, not observed**, which is precisely the provenance grading section
12 said was the warranted fix. The two recommendations turn out to be one:
the guard is only sound if the provider it keys on carries its own grade.

Worth naming without overreaching: for an agent fleet, the repository being
worked on is frequently content an agent can write. I am not developing that
into a threat claim, and nothing here was tested from that angle. It is
recorded because a routing locus that is writable by the workload is a
different kind of locus from the other four.

**Config and data are split, and only one half holds credentials.**
`~/.config/crush/crush.json` is mode 0600 and carries per-provider `api_key`;
`~/.local/share/crush/crush.json` is 0644, model selections and
`recent_models`, no secrets. `CRUSH_GLOBAL_DATA` is relocatable,
`CRUSH_GLOBAL_CONFIG` is the operator's credential store, and project-local
config outranks both.

Two things follow. The precedence chain a fleet would have to respect to pin a
per-workspace provider is project-local over data over config, which is the
reverse of where the credentials live. And section 6's MANAGE ruling gains its
cleanest supporting case: the relocatable half is the one marvel could touch
without going near a credential store, so the SOUL section 3 boundary has a
natural seam here rather than needing to be drawn by care.

## 23. Hoisting one claim out of the Crush sections, because it is not about Crush

The rig's closing observation is correct and I am acting on it. This sentence
has been sitting inside a Crush narrative across two passes:

> Marvel and Crush disagree about which axis carries the 1M window. Marvel
> puts it in the model name (`claude-opus-4-8` 200000 beside
> `claude-opus-4-8[1m]` 1000000, the entitlement axis, finding-016 axis 5).
> Crush puts it in the provider row (anthropic's `claude-opus-4-8` is
> 1000000).

**That is a claim about `internal/usage/limits.go`, not about Crush.** It says
marvel's table may be mis-keyed for a reason that has nothing to do with which
harness reads it, and it would be equally true if Crush did not exist. Crush's
catalog is only the instrument that made the disagreement visible.

Stated on its own terms: marvel's table encodes the 1M window as a property of
a model NAME, via the `[1m]` suffix that `NormalizeModel` deliberately
preserves. finding-016 axis 5 says the 1M window is a BETA ENTITLEMENT
(`context-1m-2025-08-07`), gated per account per backend, so the same model id
is a 200k model or a 1M model depending on whether the beta is enabled for
that account on that backend. A suffix in a model name cannot express an
account-and-backend-scoped grant. It can only express what the harness chose
to spell.

So `claude-opus-4-8` and `claude-opus-4-8[1m]` are not two models. They are one
model under two entitlement states, and the table reads them as two keys
because that is how the spelling arrives. Whether that is harmless depends on
whether the harness's spelling tracks the entitlement faithfully in every case,
which is untested here and is the thing to test. Two of the table's values are
fixture-verified, so this is not an assertion that the table is wrong; it is
an assertion that the table's KEY is an axis narrower than the fact it stores,
and that marvel currently has no way to notice a mismatch.

This is the same shape as the provider result in section 19 and it compounds
with it: the window depends on at least model, provider, and entitlement, and
the table is keyed on a string that reliably carries only the first.

## Third-pass provenance

Written 2026-08-09 against `aae-orc-eooi`. Method: read-only `jq` over
`~/.local/share/crush/providers.json` (436KB, mtime 2026-08-09 00:52, refreshed
by another agent's rig rather than by me), plus this repo's
`probe-interactive-ctx-remainder-sweep.md` and `internal/usage`. No Crush
server started, no socket opened, no request issued, no file written outside
the clone. The Crush stats-surface observation is relayed rather than
observed; the catalog numbers are mine.

## Addendum sources

All in-tree: `internal/usage/sample.go`, `internal/usage/profiles.go`,
`internal/usage/limits.go`, `internal/runtime/claudecode/testdata/hello.ndjson`
and `tool_call.ndjson`, `internal/usage/doc.go`,
`_kos/findings/finding-016-effective-autocompact-window-is-the-predictive-denominator.md`,
`_kos/findings/finding-017-codex-context-pressure-channel.md`,
`_kos/nodes/bedrock/elem-runtime-names-harness.yaml`. Gemini and Crush channel
detail from bd `aae-orc-6c2r` notes (2026-08-08).

