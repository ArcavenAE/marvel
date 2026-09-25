# Inference backend registry and quota estimation: know what serves each agent, and see the wall before the fleet hits it

**Status: idea. Pre-hypothesis. One live registry sample measured (2026-09-25);
nothing else here is measured.**

Captured: 2026-09-25
Source: operator request, relayed by director; research-supervisor synthesis.
Demand-side sibling: orc `_kos/ideas/workload-queue-depth-and-capacity.md`
(work and queues). This file is the supply side: backend capacity and quota.
Neither duplicates the other; a scale or move decision needs both.
Related: `router-and-backend-as-first-class-concepts.md`,
`token-rate-column-and-configurable-columns.md`,
`question-router-and-backend-layering`, `question-model-runtime-posture`,
`question-managed-runtime-placement`, `study-router-and-backend-layering.md`,
`probe-rate-limit-headroom-channel-catalog.md` (bd aae-orc-reif),
`finding-050-backend-usage-limit-observability.md`,
`memory-pressure-and-emergency-messages.md` (marvel#349),
`fleet-throughput-as-the-objective-function.md`, orc
`marvel-agentic-resource-matrix.md` (rows 2, 4, 17), orc `finding-175`
(cross-cluster work migration), ADR-007, ADR-009.

## The operator's words

> kos idea marvel we need to study each inference service and backend, figure out how we can identify which is in use across marvel, for every agent, a living registry, and like an inertia nagiation system that uses sensor fusion, we need a system to estimate token use and periodically true that up, and sample against limits reported by the backend (claude max plan's 4h-session, weekly queue, however codex/openai does it, some kind of ollama/vLLM/LMStudio throughput or contention algrythm or something, some measure of what a PAIR-connected system will accept, how many streams) so that we can inform the system about impending rate limiting, budgeting, quota-based service outages approaching and take appropriate action, moving work to other marvel clusters, moving agents to other backends, slowing the work in some areas, protecting work in flight so it's not frozen in time

A note on "4h-session": Anthropic's help center states the subscription
session window as five hours. The file uses five hours.

## What this adds to what exists

The pieces are mostly already studied, one at a time. The router idea and its
frontier node ruled KNOW yes, PRESENT graded, MANAGE no. The headroom catalog
(reif) found the channels. finding-050 designed a windowed budget shape with
provenance grades. The token-rate idea designed the per-session EMA. What
nothing does is join them into one loop: **registry, then estimate, then
forecast, then act, with work in flight protected.** Today the only instance
of that loop ran through a person: in finding-175 the operator read one
host's status line (7d at 78%), saw another account at 8%, and moved the work
by hand. "Budget is invisible to marvel."

## 1. The registry

### What marvel knows per agent today (measured 2026-09-25, `marvel backend verify`, `marvel describe session`)

| Field | Coverage | Source | Grade |
|---|---|---|---|
| Harness | 26 of 26 | manifest `runtime` | declared |
| Model (display name) | 24 of 26 (claude seats; "-" for codex) | statusline context feed | client-echoed |
| Backend | 0 of 26 named. All 26 read RESOLVED `default`, CREDENTIALS `ambient`, INTENDED `-` | spawn environment (`CLAUDE_CODE_USE_*`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_BEDROCK_SERVICE_TIER`) | assignment, not observation |
| Plan / tier | 0 of 26 | none | none |
| Account identity (which quota pool) | 0 of 26 | none | none |

"Default, ambient" means no role declared a backend and marvel did not
redirect one. On this fleet that is almost certainly a subscription login, but
marvel cannot say so, and `backend verify` notes why: macOS does not let one
process read another's environment, so it verifies only artifacts marvel
wrote. Backend detection exists for Claude Code only. There are no selectors
for OpenAI, Ollama, LM Studio, vLLM or PAIR in the Go tree (the local four are
the unbuilt BT13 placeholder, aae-orc-jmaed).

### What each backend would need, and how marvel could detect it

| Backend | Detect at spawn | Confirm while running |
|---|---|---|
| Anthropic subscription (Pro/Max/Team) | ambient login, no redirect env | statusline `rate_limits` present (Anthropic: the field appears "only for claude.ai Pro and Max subscribers") |
| Anthropic API key | `ANTHROPIC_API_KEY` present in the constructed env (name only, never the value) | statusline `rate_limits` absent; `get_usage` reports `rate_limits_available` false (catalog) |
| Bedrock / Vertex | `CLAUDE_CODE_USE_BEDROCK` / `_VERTEX` | same absence; region and model from env |
| Router (liteLLM) | `ANTHROPIC_BASE_URL` to the router | response headers name the backend, but marvel cannot see them (router study) |
| OpenAI / Codex plan | codex `CODEX_HOME` config | rollout `payload.rate_limits.plan_type` |
| Local (Ollama, vLLM, LM Studio) | base URL to a local port | the server's own `/api/ps`, `/metrics`, `/api/v0/models` |
| PAIR | unknown (see open question 1) | unknown |

### Keeping it living, not a snapshot

- **Declare first.** A role that declares its backend gives the registry a
  trustworthy INTENDED value. `backend verify` already reports disagreement
  between intended, resolved and overlay. Ambient stays shown as unknown,
  never guessed (the router node's rule: "PRESENT yes, graded, never a
  synthesized guess").
- **Confirm by presence of signal.** The running fixes above double as
  classifiers: a claude seat whose statusline carries `rate_limits` is on a
  subscription; one that never does after its first response is not. That
  turns the assignment into an observation.
- **Key by quota pool, not by session.** The catalog's framing holds: "N
  sessions are N reporters of one account-scoped quantity." The registry's
  second table is pools (account x backend x window), with sessions as members.
  Telling two logins apart without holding their identifiers is open question 3.
- **Re-read on events**: spawn, shift, a backend overlay change, and a
  fix whose shape changes (a window appears or vanishes).

## 2. Estimation: dead reckoning corrected by fixes

The operator's analogy is exact enough to use. An inertial system integrates
its own sensors between satellite fixes, and its error grows between fixes.
Here:

- **Dead reckoning (the prediction step):** per-session token burn from
  harness events. The token-rate idea already designs the integrator (a
  per-session EMA with a half-life near the poll interval) on the usage
  accountant's timestamped samples. Summed per quota pool.
- **Fixes (the correction step):** backend-reported usage, sampled when
  available (section 3). Mostly percent-used per window, with a reset time.
- **The state per pool:** percent used per window, plus one extra unknown the
  filter must learn: the gain from tokens to percent. No subscription
  publishes its allowance in tokens, so "how much percent does a million
  tokens cost on this plan" is itself estimated, and it drifts with model mix
  and cache hit rate.

**The named method.** A Kalman filter alternates a predict step
(x⁻ = A·x + B·u; P⁻ = A·P·Aᵀ + Q) and a correct step that blends in a
measurement weighted by its noise R (Welch and Bishop). With no fix, repeated
predicts add Q each step, so the uncertainty P grows: the formal statement of
"confidence decays between fixes". A complementary filter is the simpler
cousin, and Higgins shows it is a steady-state Kalman filter for this class of
problem: integrate the fast sensor, correct with the slow one. His worked
example integrates acceleration and corrects with barometric velocity, the
same shape as integrating token events and correcting with a percent meter.
**Start with the complementary filter**; move to a Kalman filter only if the
probe shows the gain drifts enough to need estimating online.

**Where the analogy breaks.**

- *Fixes arrive late and out of order.* A statusline fix is stamped after the
  response that produced it; CloudWatch minutes arrive minutes late. The
  simple remedy is to buffer events and replay them after a delayed fix
  (the out-of-sequence literature is cited but not read here).
- *Fixes are coarse.* Percent used is an integer; API `remaining` tokens are
  rounded to the nearest thousand. Large R.
- *Fixes stop when the agent idles.* The statusline fires only after an API
  response and "can go quiet when the main session is idle"; the catalog
  measured that "a QUIET agent's headroom reading goes stale precisely when
  other agents are busy." A pool with one busy member still gets fixes; an
  all-idle pool gets none, which is fine because it is not burning.
- *Unobserved consumers move the fix.* Subscription usage is "shared across
  Claude and Claude Code". A person using claude.ai in a browser, or another
  laptop on the same login, moves the percent with no fleet event. The
  filter's residual carries them. A residual that stays positive is a finding
  in itself: someone outside the fleet is spending the pool.
- *Some backends never fix.* Vertex dynamic shared quota has "no predefined
  quota limits" (search snippet, unverified); the only signal is the 429 rate.
- *The quota ledger is not the bill.* Bedrock reserves input plus
  `max_tokens` up front and counts Claude output at 5x to 15x toward quota;
  Anthropic excludes cache reads from input-token limits on most models.
  Each backend needs its own conversion from harness events.

## 3. Limit signals per backend

| Backend | Signal | How to sample | Verified? |
|---|---|---|---|
| Claude subscription | statusline stdin `rate_limits.five_hour` and `seven_day`: `used_percentage`, `resets_at` (epoch s). Present only for Pro/Max, only after the first response; a window drops once `resets_at` passes. Weekly resets "at a fixed time each week that is assigned to your account." | **marvel already receives it**: its own `marvel ctx-forward` statusline hook reads these fields every 15 s (`cmd/marvel/ctxforward.go:76, 111-125`) and renders the pane's "acct 5h / 7d", but deliberately does not forward them to the daemon (`:435-439`). No scraping needed; what is missing is an account-scoped home in the daemon (catalog Phase 2, P2-b). Claude Code fills the fields from `anthropic-ratelimit-unified-{5h,7d}-*` response headers (catalog, read from the binary). | VERIFIED (Anthropic statusline docs; marvel code) |
| Claude subscription, richer | `get_usage` control request: five windows plus model-scoped windows | a network call to the usage endpoint, itself rate-limited ("the act of measuring rate-limit headroom consumes a rate-limited resource") | catalog, from the binary |
| Anthropic API | `anthropic-ratelimit-{requests,tokens,input-tokens,output-tokens}-{limit,remaining,reset}`, `retry-after`. Token bucket, "continuously replenished". Spend-cap 429 carries `enforced_spend_limit_reached` and no `retry-after`. | every response, but only the harness sees the headers; marvel needs a hook, an app-server stream, or a proxy | VERIFIED (Anthropic rate-limits docs) |
| OpenAI API | `x-ratelimit-{limit,remaining,reset}-{requests,tokens}` (+ project-scoped), `Retry-After`; resets are durations; a 429 `slow_down` can fire under the per-minute limits | same visibility problem | VERIFIED (OpenAI docs) |
| Codex on a ChatGPT plan | rollout JSONL `payload.rate_limits`: `primary`/`secondary` with `used_percent`, `window_minutes`, `resets_at`, `plan_type`. The codex source parses `x-codex-primary-used-percent` / `-window-minutes` / `-reset-at` headers into a `RateLimitSnapshot`. | passive tail of the rollout file under marvel's private per-session `CODEX_HOME` (`internal/runtime/codex.go:61-63`); marvel reads that file today for context only. Key on `window_minutes`, not on the slot: finding-050 reads primary as 5-hour, the catalog measured primary at 10080 (weekly) in all 2,097 records. Never open the `auth.json` beside it. | rollout: measured (catalog); headers: codex source, not a documented contract; plan policy: OpenAI pricing page VERIFIED, recent 5-hour change UNVERIFIED |
| Bedrock | Service Quotas per model per region (tokens/min on-demand and cross-region, requests/min for some models, tokens/day per account); CloudWatch `AWS/Bedrock` `InvocationThrottles`, token counts, `EstimatedTPMQuotaUsage` ("does not reflect the reservation-based token consumption that drives throttling") | CloudWatch poll at one-minute resolution, account-wide, lagged | VERIFIED (AWS docs) |
| Vertex | per-region per-model QPM/TPM for Claude; dynamic shared quota for Gemini (no fixed limit; a 429 means the pool is busy) | 429 rate only under DSQ | UNVERIFIED (pages returned navigation only) |
| Ollama | `OLLAMA_NUM_PARALLEL` (default 1), `OLLAMA_MAX_QUEUE` (default 512, then 503), `OLLAMA_MAX_LOADED_MODELS`; `GET /api/ps` (loaded models, size, VRAM, expiry); per-response `eval_count` | poll `/api/ps`; no queue-depth endpoint is documented | VERIFIED (Ollama docs) |
| vLLM | Prometheus `/metrics`: `vllm:num_requests_running`, `vllm:num_requests_waiting`, `vllm:kv_cache_usage_perc`, time to first token, token counters | scrape | VERIFIED (vLLM docs) |
| LM Studio | continuous batching, "Max Concurrent Predictions" default 4 | no metrics endpoint documented | VERIFIED (partial) |
| PAIR | open question 1 | | |

**Local servers are a different kind of limit.** They expose contention
(running, waiting, KV cache full, latency), not a quota. For them the
forecast target changes from "time to exhaustion" to "expected queue wait and
time to first token", and the useful action is placement, not budgeting. A
router such as PAIR (below) also detaches the endpoint an agent calls from the
machine whose metrics describe it.

## 4. Forecasting

Per quota pool and window:

- Burn rate r (from dead reckoning, as percent per hour via the learned gain)
  with standard deviation σ_r; remaining capacity C with filter uncertainty P.
- Time to exhaustion T ≈ C / r, with σ_T² ≈ (P + T²·σ_r²) / r² (a first-order
  propagation, derived here, not sourced).
- **A fixed-reset window** (the subscription windows): compare T to the time
  until `resets_at`. If T is longer, the window resets first and there is no
  risk. The risk measure is P(T < time to reset).
- **A token bucket** (API): exhaustion is only reachable while r exceeds the
  refill rate; net drain is r minus refill.
- **Per cluster:** the minimum over the pools its seats draw from, since one
  exhausted pool stalls every seat on it.

Honest uncertainty: the gain is learned, the fixes are coarse, and unobserved
consumers exist. Show a band (for example T from the 10th to 90th percentile),
never a single time. An estimate that has not seen a fix for a stated interval
shows as stale, per finding-050's downgrade-when-stale rule.

## 5. Actions, gentlest first, with work in flight protected

| Action | What it does | How work in flight is protected | ADR-007 |
|---|---|---|---|
| **Warn** | NOTICE / WARN to the supervisor and director with the forecast band | nothing moves | automatic (reminding) |
| **Slow a team** | pause intake for the pool's seats (the demand-side idea's actuator); no new work admitted | running work finishes | proposal, confirmed by the supervisor |
| **Route new work elsewhere** | new items go to seats on a pool with headroom | nothing in flight moves | proposal |
| **Move an agent to another backend** | a shift whose successor spawns with a different declared backend | a shift with handoff, started while headroom remains: the predecessor writes DONE / REMAINING / RULED OUT / OPEN / RESUME (the finding-175 shape) and pushes its branch before it stops | proposal; the operator's backend choice is a custody decision ("Fallback policy is a custody decision, not a performance one", model-runtime node) |
| **Move work to another marvel cluster** | finding-175, made routine | the handoff above, plus what finding-175 found missing: the spec travels in git (never in gitignored `.session/`), the role definition travels, a durable cross-cluster mailbox | proposal; operator-confirmed |
| **Pause** | stop a seat before the wall | only at a turn boundary after a checkpoint, never mid-call | proposal |

**The load-bearing rule: act on the forecast, not the refusal.** A seat that
hits a 429 mid-turn is frozen with its work half done, which is the outcome
the operator named. The trigger for a handoff is "the lower bound of T falls
below the handoff lead time", where lead time is the measured wrap-up cost
(about eight minutes in finding-175) plus a margin. The context-pressure shift
works the same way: fire before the boundary, not at it.

**Emergency vocabulary.** The memory-pressure idea's NOTICE / WARN /
CRITICAL / FINAL scale has no quota row. It should gain one, with the forecast
band as the situation and the handoff as the action. A FINAL ("marvel will act
at the stated deadline") requires the ratified decision below.

**Automation boundary.** Warning is reminding, which automation may do.
Slowing, routing, moving and pausing are acts, and ADR-007 clause 2 says "No
automation acts on its own proposal." The precedents for a defined exception
are the admission budget and the per-role `shift` block: declared by the
operator, bounded, visible. An automatic handoff-before-the-wall on a declared
per-role quota policy fits that pattern, and **needs a written ratified
decision before it acts**. Until then marvel proposes and a supervisor or the
operator confirms. Marvel's context-pressure auto-shift already caps
simultaneous shifts per tick for exactly the reason that matters here: a burst
of shifts spends the shared rate limit it is trying to protect (throughput
idea, mechanism 1).

## 6. Composition

- **marvel** holds the registry (declared, resolved, confirmed-by-signal) and
  the pool table; runs the estimator on the usage accountant; stores fixes on
  the budget snapshot with provenance and staleness (finding-050's design:
  `ShapeWindowed`, grades `on-disk-harness` / `agent-reported` /
  `service-brokered`); proposes actions; executes confirmed ones through
  existing verbs (`shift`, `scale`, admission).
- **Adapters** must report, per harness: tokens per turn (claude interactive
  does not today: token and cost counting comes only from headless streams,
  and the heartbeat carries no totals), the rate-limit fix if the harness
  relays one (claude via ctx-forward; codex via rollout), and the 429 or
  refusal event with its type.
- **Director** consumes the forecast and the proposals (the board shows the
  pool bands beside the queue depth from the demand-side idea) and routes
  confirmations. Director never reads quotas itself.
- **critic** consumes spend per selected outcome (vision Gap 5); the estimator
  gives it cost on a timeline rather than a cumulative number.
- **Custody (SOUL §3, ADR-009).** Every fix listed above is read without
  holding a bearer credential: the statusline payload and the rollout file are
  outputs the harness already writes, local server metrics are
  unauthenticated or operator-owned, and CloudWatch reads use a role the
  operator grants (brokered, not held). A reader that would need the account's
  OAuth token (for example calling the usage endpoint directly rather than
  through the harness) is custody and stays out. One gap found while
  reading: marvel's literal-bearer refusal list (`internal/api/backend.go:92-97`:
  `ANTHROPIC_API_KEY`, `AWS_BEARER_TOKEN_BEDROCK`, `AWS_SECRET_ACCESS_KEY`,
  `AWS_SESSION_TOKEN`) does not include `ANTHROPIC_AUTH_TOKEN`, the bearer a
  router such as liteLLM expects. Reported separately; a registry that detects
  a router backend should not ship before that list covers it.

## 7. Open questions and a first probe

1. **What is a "PAIR-connected system"?** The operator's term, undefined in
   every kos graph. Marvel's code reserves a `pair` provider for NVIDIA
   Personal AI Router (`internal/service/service.go:11-15`), a router across
   machines on one network in front of Ollama and LM Studio, where each
   request runs on one machine. "PAIR-connected" appears in none of its
   material. **Operator: is that what you mean, and is "how many streams" the
   pool's concurrency across its machines?**
2. Does the statusline payload identify the account, or must pools be told
   apart some other way? Without that, two logins on two hosts merge.
3. How to key a pool without holding an account identifier (a salted local
   hash of something the harness already shows, or an operator-declared pool
   name per host).
4. Do sessions not on a subscription ever get `rate_limits`? (The docs say no.)
5. How stable is the token-to-percent gain across model mix and cache hit
   rate?
6. What is the handoff lead time per role, measured, not assumed?
7. Should the quota ladder be a per-role policy, and who ratifies it?

**Proposed question node (not filed):**
`question-quota-dead-reckoning-tracks-the-meter`: *Does a dead-reckoning
estimate from harness token events track the subscription 5-hour meter closely
enough to forecast exhaustion?*

**Smallest falsifying probe.** Build nothing new beyond a log line. For one
day on one host, record every ctx-forward statusline tick for each claude
seat: session, time, context tokens, `five_hour.used_percentage`,
`resets_at`. Offline, fit the token-to-percent gain on the first half of the
window and predict the second half from tokens alone, correcting only every
30 minutes.

- **Falsified** if the 30-minute-corrected prediction misses the meter by more
  than 10 percentage points at any correction (the placeholder N; set it from
  the data).
- **Also informative:** whether the residual drifts one way (an unobserved
  consumer), and whether any hour produced no fix at all (idle pools).

The capture is also the populated statusline fixture the ctx-forward code
comment says has never been observed (catalog P2-a).

## Sources

Read for this idea (2026-09-25) unless marked.

- Anthropic, "Rate limits", https://platform.claude.com/docs/en/api/rate-limits
- Anthropic Help Center, "What is the Max plan?", https://support.claude.com/en/articles/11049741-what-is-the-max-plan ; "Using Claude Code with your Pro or Max plan", https://support.claude.com/en/articles/11145838-using-claude-code-with-your-pro-or-max-plan ; "Usage limit best practices", https://support.claude.com/en/articles/9797557-usage-limit-best-practices
- Anthropic, Claude Code docs, "Customize your status line", https://code.claude.com/docs/en/statusline ; "Manage costs effectively", https://code.claude.com/docs/en/costs
- OpenAI, "Rate limits", https://developers.openai.com/api/docs/guides/rate-limits ; "Codex pricing", https://learn.chatgpt.com/docs/pricing
- openai/codex source, `codex-rs/codex-api/src/rate_limits.rs` (default branch; commit not pinned)
- AWS, Bedrock User Guide: "Quotas for the bedrock-runtime endpoint", https://docs.aws.amazon.com/bedrock/latest/userguide/quotas-runtime.html ; "How tokens are counted in Amazon Bedrock", https://docs.aws.amazon.com/bedrock/latest/userguide/quotas-token-burndown.html ; "Monitor bedrock-runtime inference using CloudWatch metrics", https://docs.aws.amazon.com/bedrock/latest/userguide/monitoring-runtime-metrics.html
- Google Cloud, Vertex quotas for Claude and dynamic shared quota (UNVERIFIED: pages returned navigation only; search snippets)
- Ollama, "FAQ", https://docs.ollama.com/faq ; "List running models", https://docs.ollama.com/api/ps
- vLLM, "Metrics", https://docs.vllm.ai/en/latest/design/metrics.html
- LM Studio, "Parallel Requests", https://lmstudio.ai/docs/app/advanced/parallel-requests
- NVIDIA, Personal AI Router README, https://github.com/NVIDIA/Personal-AI-Router
- Welch, G. and Bishop, G., "An Introduction to the Kalman Filter", UNC-Chapel Hill TR 95-041 (2004 update), https://www.cs.utexas.edu/~pstone/Courses/393Rfall15/readings/Welch+Bishop-TR-95.pdf
- Higgins, W. T. (1975), "A Comparison of Complementary and Kalman Filtering", IEEE Trans. Aerospace and Electronic Systems AES-11(3), https://www.allaboutcircuits.com/uploads/articles/A_comparison_of_complementary_and_kalman_filtering.pdf
- Heinanen, J. and Guerin, R. (1999), RFC 2697, "A Single Rate Three Color Marker", https://www.rfc-editor.org/rfc/rfc2697
- Bar-Shalom, Y. (2002), "Update with out-of-sequence measurements in tracking: exact solution", IEEE TAES 38(3) (UNVERIFIED: bibliographic data only)
