# Rate-limit headroom: channel catalog (claude, codex, opencode, Crush, gemini)

**Status: CANDIDATE CATALOG, 2026-08-08. Not a finding.**

This is desk research with on-disk and in-binary verification. No probe was
run to pre-stated success signals, no live rig was built, no rate limit was
approached or tripped, and no model API call was made for this work. Per the
kos rule that desk research produces candidates rather than results, nothing
below has cleared the bar for a finding. A verdict of `candidate` here means
"worth building a rig for," and nothing more.

**Question:** `question-interactive-context-pressure` supplies the ruled model
(consent gates, fidelity orders), but the subject is different and the
difference is the first result. **bd:** `aae-orc-reif`.
**Method precedent:** `probe-interactive-ctx-remainder-sweep.md`.
**Discipline precedent:** `internal/usage/doc.go` on levels versus counters.

---

## Why this exists, stated as structure rather than as a feature request

Rate limits (input and output TPM, TPD, request rate, plan quota) bind per
ACCOUNT and are shared across the whole fleet. They recover on a clock, and a
shift does not remedy them. Every context-pressure actuator marvel has built
consumes exactly this resource without modelling it.

Two consequences make it structural.

1. A context-pressure trigger that fires on several agents at once produces a
   burst of the one thing it cannot see. The remedy for one session degrades
   all of them. `question-shift-triggers` already records this as the reason
   its own "how does context pressure aggregate?" sub-question is posed
   wrongly.
2. Occupancy is a per-fleet CONCURRENCY limit, not only a per-session cost. An
   agent at 400k spends roughly four times the input TPM per turn of one at
   100k, so the same account supports fewer simultaneous agents when they run
   hot.

And when the account IS limited, a fleet with no degradation policy degrades by
whoever retries hardest, which is the worst available allocation.

---

## Evidence grades used below

- **VERIFIED-ON-DISK**: measured against a real artifact on kinu (macOS 26).
- **VERIFIED-IN-BINARY**: read out of an installed binary at a pinned version.
- **VERIFIED-IN-TYPES**: read out of an installed type declaration.
- **INFERRED**: follows from something verified, but was not observed.
- **NOT ASSESSED**: I did not look, or could not.

Consent grades are the ruled three: CONTRACTED (the harness publishes it for
consumers), CONCEDED (the harness hands over the pointer), APPROPRIATED (I went
looking). Appropriated is a probe instrument only, never a production path.

Versions: Claude Code 2.1.226 (build 2026-08-08, git sha e140b32), codex-cli
0.146.0, opencode 1.18.15 with `@opencode-ai/plugin` and `@opencode-ai/sdk`
installed, Crush v0.88.1 darwin/arm64. gemini CLI is not installed on this host.

---

## Result 0: this is not the CTX% question with a different noun

The catalog method transfers. The subject does not, in three ways that change
what a good answer looks like.

**Occupancy is harness-owned; headroom is provider-owned.** A harness computes
occupancy itself, so "does the harness expose it" is a real question about the
harness. No harness computes rate-limit headroom. The provider computes it and
puts it in a response header. So the question is narrower and more brittle:
does this harness happen to RELAY a number it merely received? A harness can be
richly instrumented and still relay nothing, because relaying is not on its own
critical path.

**Per-session attribution is wrong here, not merely hard.** The whole binding
problem that dominates the CTX% catalog (map a pane to the file or session
carrying its usage) inverts. Every agent on one account reports the SAME
headroom. N sessions are N reporters of one account-scoped quantity, not N
quantities. Summing them, averaging them, or rendering a per-session column of
them are all category errors, and the last is the one a naive implementation
would ship, because the existing CTX% column makes it look natural.

**The shape is a third one.** `internal/usage/doc.go` distinguishes a LEVEL
(occupancy, per-request, latest-wins, never summed) from a COUNTER (cumulative
tokens, monotone). A rate limit is neither. It is a **recovering level**: it
rises with use and falls with time, and a reading is meaningless without the
instant it recovers at. So the sample is a TRIPLE, not a scalar:

> `(utilization, window_length, resets_at)`

A consumer that keeps only the utilization has a number it cannot act on, since
94 percent with four hours to run and 94 percent with four minutes to run are
opposite situations. This is the direct analogue of the denominator discipline:
`internal/usage` refuses a numerator without a window, and a headroom feed must
refuse a level without a reset instant. It is also why a monotone-counter check
is the wrong integrity test here; the series is SUPPOSED to decrease, and the
useful invariant is that a decrease is accompanied either by a reset-instant
change (window rollover) or by elapsed time (aging), never by neither.

---

## Candidate table

| Runtime | Verdict | Best channel | Consent | Carries the triple? | Marginal cost to observe |
|---|---|---|---|---|---|
| claude | **candidate, strongest** | `statusLine` payload `rate_limits` | CONTRACTED | 2 of 4 windows, with `resets_at`; no window length | none (rides a hook marvel already installs) |
| claude | candidate, richer and dearer | `get_usage` control request | CONTRACTED, vendor-marked Experimental | 5 windows plus per-model and overage | **a network call to the claude.ai usage endpoint** |
| codex | **candidate, strong** | rollout JSONL `payload.rate_limits` | CONCEDED via `notify`/`hooks`; APPROPRIATED if globbed | yes, all three terms | a tail read of a file already being written |
| opencode | no headroom channel | `RetryPart.error.responseHeaders` | CONTRACTED | no: refusal only, after the fact | none, but only fires once throttled |
| Crush | `no channel` | none | n/a | no | n/a |
| gemini | NOT ASSESSED | not installed on this host | n/a | n/a | n/a |

**The headline: claude's headroom already arrives on a channel marvel owns and
is being thrown away in flight.** See Result 1.

---

## claude

### The statusline payload carries `rate_limits`. VERIFIED-IN-BINARY.

The statusline payload constructor in 2.1.226 builds the object conditionally:

```js
let C = kun();
let A = {
  ...C.five_hour && { five_hour: { used_percentage: C.five_hour.utilization*100,
                                   resets_at: C.five_hour.resets_at } },
  ...C.seven_day && { seven_day: { used_percentage: C.seven_day.utilization*100,
                                   resets_at: C.seven_day.resets_at } }
};
return { ...,
  context_window: ilv(S, b),
  exceeds_200k_tokens: t,
  ...(A.five_hour || A.seven_day) && { rate_limits: A },
  ... };
```

`kun()` returns a module-level cache populated straight from response headers:

```js
function g7u(e) {                       // e is a Headers object
  let t = {};
  for (let [r, n] of [["five_hour","5h"], ["seven_day","7d"],
                      ["seven_day_overage_included","7d_oi"], ["overage","overage"]]) {
    let o = e.get(`anthropic-ratelimit-unified-${n}-utilization`);
    let i = e.get(`anthropic-ratelimit-unified-${n}-reset`);
    if (o !== null && i !== null)
      t[r] = { utilization: Number(o), resets_at: Math.round(Number(i)) };
  }
  return t;
}
```

Four things follow, and each matters to an implementation.

- **The source is a response header, so observing costs nothing.** The number
  was already received on a request the fleet was already making. This is the
  only channel in the catalog with genuinely zero marginal cost, and it is the
  reason it leads.
- **The key is ABSENT until at least one API response has been parsed.** The
  spread is guarded on `A.five_hour || A.seven_day`, and the cache starts empty
  (`h7u()` sets `T_t = {}`). Corroborated on disk: marvel's own captured
  fixture `cmd/marvel/testdata/statusline-2.1.226-empty.json`, taken from a
  throwaway session that made no API call, has thirteen top-level keys and
  `rate_limits` is not among them. VERIFIED-ON-DISK. So absence of the key is
  the normal cold state and must not be read as "no limits apply."
- **The statusline is a lossy projection of what the harness already holds.**
  The cache collects four windows; the statusline emits two. `overage` and
  `seven_day_overage_included` are dropped at the boundary. If marvel wants
  them, the ask upstream is one line in a spread, which is about as small an
  upstream ask as exists.
- **The window LENGTH is not on the payload.** `five_hour` and `seven_day` are
  window names, not durations, so the third term of the triple is carried by
  convention in the key rather than as data. Workable, and worth naming,
  because the moment a window is renamed or added the convention breaks
  silently.

### marvel is already receiving this and discarding it. VERIFIED-ON-DISK.

`cmd/marvel/ctxforward.go` reads the statusline payload from stdin and
unmarshals it into `statuslinePayload`, which declares `model`, `cost`,
`context_window`, and `tasks`. Go's `encoding/json` drops unknown fields
silently, so `rate_limits` lands on the daemon's doorstep on every statusline
tick of every `context_feed = "statusline"` session and is thrown away without
a trace. The file's own comment says the omission is deliberate for
`transcript_path` ("deliberately not parsed here, having no consumer yet"); for
`rate_limits` there is no comment, because at the time it was written the field
had not been looked for.

This is the same class as marvel#141 (ctx-forward discards the classes, the
window, and `transcript_path`), and it is the strongest argument in the catalog
for reif being cheap: the transport, the projection, the hook installation, and
the heartbeat RPC all already exist and are already carrying the payload.

### The `get_usage` control request. VERIFIED-IN-BINARY.

`get_usage` is a subtype in the control-request roster alongside `is_repl`,
`set_permission_mode`, `rename_session`, `set_model`, `read_file`,
`mcp_authenticate`, `get_context_usage`, `file_suggestions`, `mcp_status`,
`mcp_reconnect`, `set_color`, `set_max_thinking_tokens`, and
`mcp_oauth_callback_url`. Its request and response both carry zod `.describe()`
text, which is a published contract rather than an internal shape:

> Requests the structured /usage data: session cost/usage totals plus claude.ai
> plan rate-limit utilization when available. Experimental, the response shape
> may change.

The response is richer than the statusline by a wide margin: `five_hour`,
`seven_day`, `seven_day_oauth_apps`, `seven_day_opus`, `seven_day_sonnet`, an
optional `model_scoped[]` array of per-model weekly windows with server-supplied
display names, and an `extra_usage` object (`is_enabled`, `monthly_limit`,
`used_credits`, `utilization`, `currency`). It also carries
`rate_limits_available`, described as:

> False when plan rate limits do not apply (API key, Bedrock, Vertex, or
> missing profile scope), rate_limits will be null.

That single field answers a question the statusline path leaves ambiguous, and
it is the reason this channel is worth keeping in the catalog despite its cost.

**But observing it is not free, and this is the sharper instance of the
perturbation constraint than Crush supplied.** The construction site fetches
from the claude.ai usage endpoint over the network and has its own 429 handling
(`r.response?.status === 429` becomes `rateLimitedVia: "http_429"`, with a
seeded fallback on failure). So the act of measuring rate-limit headroom
consumes a rate-limited resource, and a fleet that polls it once per agent per
tick will throttle its own instrument first. The constraint that
`question-interactive-context-pressure` records as "a channel can be fully
CONTRACTED and still not free" applies here in its most literal form: this one
is contracted, documented, schema'd, and self-throttling.

### Clean negatives for claude

- **The transcript does not carry it.** Scanned 255,639 lines across 1,572
  files under `~/.claude/projects`. Zero JSON KEYS matching
  rate-limit/quota/reset/utilization. The only key hits were file paths in a
  `file-history-snapshot` (a memory file about a Bedrock quota appeal) and one
  `toolUseResult.ghRateLimitHint`, which is the GitHub CLI. VERIFIED-ON-DISK.
  This matters because `transcript_path` is the CONCEDED pointer that solves
  several other problems; it does not solve this one.
- **OTEL does not carry it.** The full `claude_code.*` instrument roster in the
  binary is: `active_time.total`, `bash.subprocess`, `code_edit_tool.decision`,
  `commit.count`, `compaction`, `cost.usage`, `events`, `hook`, `interaction`,
  `lines_of_code.count`, `llm_request`, `mcp.rpc`, `pull_request.count`,
  `session.count`, `subagent.spawn`, `token.usage`, `tool`,
  `tool.blocked_on_user`, `tool.execution`, `tracing`. Nothing named for rate
  limits, quota, or utilization. VERIFIED-IN-BINARY. This extends the sweep's
  OTEL ruling: OTEL is a dead end for headroom as well as for pressure.

### The refusal signal exists but is not on either channel

A separate module-level object carries `status` (`allowed`, `allowed_warning`,
`rejected`), `rateLimitType`, `isUsingOverage`, `resetsAt`, `overageStatus`,
`overageResetsAt`, `overageDisabledReason`, and
`unifiedRateLimitFallbackAvailable`, populated from
`anthropic-ratelimit-unified-status` and its siblings on 429 responses.
VERIFIED-IN-BINARY. Neither the statusline nor `get_usage` carries it. So on
claude, headroom and refusal are also disjoint, which is the same split codex
shows and is stated as a cross-cutting result below.

---

## codex

### `payload.rate_limits` is a sibling of the occupancy block on the same record. VERIFIED-ON-DISK.

Measured across 209 rollout files under `~/.codex/sessions`. Every one of the
2,098 `event_msg` / `token_count` records carries a `rate_limits` object, at
`payload.rate_limits`, and **2,097 of 2,098 carry `last_token_usage`,
`total_token_usage`, and `model_context_window` on the SAME record.** The one
exception is the first record of a session, where `info` is absent entirely,
which matches what the CTX% sweep found independently.

**This is the correction to make loudly.** finding-007 located this field as
"rollout `rate_limits.primary.used_percent`" and the sweep did not revisit it.
It is not inside `info`; it is beside it. Anyone who writes
`info.rate_limits` gets nothing and no error, which I confirmed by doing exactly
that on the first pass of this probe. The consequence is the good news: one
read of one record yields occupancy, its denominator, AND account headroom,
which makes codex the only runtime in the catalog where a single sample carries
all three.

Observed shape (values from a real record, content-free):

```json
{
  "limit_id": "codex",
  "limit_name": null,
  "primary": { "used_percent": 0.0, "window_minutes": 10080, "resets_at": 1785409893 },
  "secondary": null,
  "credits": { "has_credits": false, "unlimited": false, "balance": null },
  "individual_limit": null,
  "spend_control_reached": null,
  "plan_type": "team",
  "rate_limit_reached_type": null
}
```

`primary` carries the complete triple: level, window length in minutes, and
reset instant as a unix epoch. `window_minutes` was 10080 (seven days) in all
2,097 records that carried a `primary`. `plan_type` was `team` in all 2,098.

### The series is a recovering level, and I can show both recovery modes. VERIFIED-ON-DISK.

Across 1,890 consecutive within-file pairs, `primary.used_percent` decreased 8
times. The decreases split cleanly into two kinds:

- **Two large drops to zero, each with a CHANGED `resets_at`**: 57 to 0 as
  `resets_at` moved from 2026-07-30T11:11:37Z to 2026-08-01T19:48:50Z, and 94
  to 0 as it moved from 2026-08-05T13:53:09Z to 2026-08-08T04:02:57Z. That is
  window rollover, and it is unambiguous.
- **Six small decrements of one or two points with `resets_at` UNCHANGED**: 6
  to 5, 21 to 19, 2 to 1, 10 to 9, 41 to 40, 86 to 85, across three different
  files.

The first kind proves the quantity is not a counter. The second kind is the
interesting one, and **I am naming the alternative rather than asserting the
identity**, because this is exactly the shape of error the graph has been
burned by.

- Hypothesis A, rolling-window aging: the seven-day window is a sliding one, so
  the oldest usage falls out continuously and the level drifts down between
  requests even while resets_at is fixed.
- Hypothesis B, denominator change: the account's limit was raised, so the same
  numerator over a larger limit rounds down a point.

The data I have cannot separate these, because codex publishes only a percent
and never the numerator or the limit. A is better supported by frequency (six
independent occurrences across three files, all small, all with the reset
anchor fixed) and B would require six limit changes in a two-week corpus. But
"better supported" is not "established," and the thing that would settle it is
a numerator, which this channel does not carry. **An adapter must therefore
tolerate a decreasing series without a reset change, and must not treat that as
a parse error or a schema break.** That is the actionable consequence and it
holds under either hypothesis.

### The refusal record is disjoint from the headroom record. VERIFIED-ON-DISK, n=1.

Exactly one record in 2,098 carried a non-null `rate_limit_reached_type`:

```json
{ "limit_id": "premium", "primary": null, "secondary": null,
  "plan_type": "team", "rate_limit_reached_type": "workspace_owner_credits_depleted",
  "credits": { "has_credits": false, "unlimited": false, "balance": null } }
```

Two things changed together on that record: `limit_id` flipped from `codex` to
`premium` (a different limit pool), and `primary` went NULL. So on the single
record that reports the limit was actually reached, the headroom fields are
absent. A consumer keyed only on `primary.used_percent` sees nothing at the one
moment that matters most, and a consumer that treats a missing `primary` as
"stale, keep the last value" would report healthy headroom during a refusal.

This is one specimen. I am not generalizing it into a schema rule. What it
justifies is a defensive read, not a claim about codex's design.

### Codex negatives and gaps

- **`secondary` is null in all 2,098 records.** The short window is not
  populated on this account, so on this host codex offers a weekly figure and
  nothing faster. A weekly percent is close to useless as a burst-control
  signal, which is the actual use case in the reif argument. NOT ESTABLISHED
  whether this is a `plan_type: team` property, an account property, or a
  version property, and it is the single most important open question for
  codex.
- **`credits.balance`, `individual_limit`, `spend_control_reached`, and
  `limit_name` were null in every record.** VERIFIED-ON-DISK.
- **No execution point carries it.** Consistent with the sweep: `notify` fires
  on `agent-turn-complete` and the ten hook events carry a thread id and a
  transcript path, not tokens and not limits. So codex is POINTER-tier here for
  the same reason it is POINTER-tier for occupancy, and the pointer is the same
  pointer, which is a real economy.
- **Cadence.** Median gap between `token_count` records within a session is
  9.0s, p90 77.2s. Written live during interactive TUI sessions. So the feed is
  fresh enough for a control loop but is driven by the agent's own request
  rate, which means a QUIET agent's headroom reading goes stale precisely when
  other agents are busy consuming the shared resource. That is a structural
  staleness hazard specific to this channel shape and it does not exist for
  claude's statusline, which ticks on the TUI's own cadence.

---

## opencode

**Verdict: no headroom channel. A refusal channel exists.**

- The plugin API has fifteen hooks (`chat.headers`, `chat.message`,
  `chat.params`, `command.execute.before`, four `experimental.*`,
  `permission.ask`, `shell.env`, `tool.definition`,
  `tool.execute.before`/`after`, `experimental.text.complete`). **None receives
  a response or its headers.** `chat.headers` is the near miss and it is
  outbound only: its `output` is `{ headers: Record<string,string> }`, which the
  plugin WRITES into the request. VERIFIED-IN-TYPES.
- No `fetch` override appears in the plugin type surface, so the standard
  escape hatch for seeing response headers is not offered. VERIFIED-IN-TYPES.
- **The one real channel is a refusal channel.** `RetryPart` is a message part
  type carrying `attempt: number` and `error: ApiError`, and `ApiError.data`
  declares `statusCode?`, `isRetryable`, `responseBody?`, and
  **`responseHeaders?: { [key: string]: string }`**. VERIFIED-IN-TYPES. Raw
  response headers, on a message part, reachable through three transports the
  sweep already mapped (the plugin `event` hook, the HTTP SSE `/event` stream,
  and the persisted store). If a provider returns `retry-after` or
  `anthropic-ratelimit-*` on a 429, this is where they would land.

**Whether it is ever populated is NOT ESTABLISHED, and the local corpus leans
against it.** A read-only byte scan of `~/.local/share/opencode/opencode.db`
(4.3 MB) found the string `responseHeaders` four times and **zero occurrences
of any of `anthropic-ratelimit-*`, `x-ratelimit-*`, or `retry-after`.**
VERIFIED-ON-DISK. Two hypotheses, and I cannot separate them: no throttled
request has occurred on this host, or the headers are declared optional and
dropped in practice. What would settle it is a synthetic 429 from a mock
endpoint, which is cheap and safe and is the right phase-2 step for opencode.

Note the boundary, which is unchanged from the CTX% sweep: `opencode.db` holds
`credential` and `account` tables in the same file. Nothing in this section
requires opening a sqlite handle on it, and nothing recommended here should.
I used a byte scan for exactly that reason.

---

## Crush

**Verdict: `no channel`.**

The v0.88.1 binary contains `Retry-After` and `retry-after` (three occurrences
each) and a set of vendor SDK identifiers (`RateLimitError`, `QuotaFailure`,
`RateLimits`, `RateLimitUpdated`, `RateLimitDeleted`, `QuotaExceededError`,
`RateLimiter`), which are types reachable through the OpenAI and Google client
libraries rather than evidence that Crush does anything with them.
VERIFIED-IN-BINARY. There is **no `x-ratelimit-*` or `anthropic-ratelimit-*`
header name anywhere in the binary**, so Crush does not parse the header family
that carries the number.

This is consistent with what the sweep established about Crush's persistence:
the `sessions` table carries `prompt_tokens`, `completion_tokens`, `cost`,
`message_count`, and `summary_message_id`, and no limit or quota column. Crush
tracks what it spent and nothing about what it is allowed to spend.

The framework prediction from the sweep survives here too, in a second domain.
Charm renders in-process and delegates nothing, so there is no chrome callout
to carry a figure, and there is no export path to carry one either.

---

## gemini

**NOT ASSESSED.** gemini CLI is not installed on this host, so every claim I
could make would be from documentation alone, at the weakest grade, about a
runtime marvel cannot launch (`aae-orc-6c2r`, no adapter). The sweep's own rule
applies unchanged: a channel for a runtime marvel cannot launch is shelf
inventory. Recording the gap rather than filling it badly.

One thing the sweep already established that bears here: gemini's OTEL surface
carries token counts and a compaction event, with nothing named for windows or
limits. That is weak evidence against a headroom channel existing on the OTEL
path, and no evidence at all about its hooks.

---

## Cross-cutting results

### 1. Headroom and refusal are disjoint on both harnesses that carry either

codex nulls `primary` on the record that sets `rate_limit_reached_type`.
claude keeps the refusal object (`status`, `rateLimitType`, `isUsingOverage`)
entirely off both the statusline payload and the `get_usage` response. So on
neither runtime does one subscription give you both "how much room is left" and
"I was just refused." Any degradation policy needs two feeds, and the second
one is the one neither harness volunteers.

### 2. The same field name means different things on two channels of the same binary

Within Claude Code 2.1.226:

| | statusline path | `get_usage` path |
|---|---|---|
| level field | `used_percentage`, computed as `utilization * 100` | `utilization`, described as "0-100" |
| header/source value | fraction, 0 to 1 | server-supplied |
| `resets_at` type | **integer, unix epoch seconds** (`Math.round(Number(header))`) | **ISO 8601 string** (the code converts a numeric epoch with `new Date(x*1000).toISOString()`) |

Both VERIFIED-IN-BINARY. A consumer that reads one channel's documentation and
parses the other gets a 100x scaling error on the level and a type error on the
reset instant. This is precisely the class of trap `internal/usage/doc.go`
exists to encode, and it argues that a headroom sample type should carry its
source channel the way `LimitSource` grades a window today.

### 3. The chrome-callout rule from the CTX% sweep predicted this correctly

The sweep's framework says a harness that DELEGATES its chrome rendering has to
serialize what it draws, and one that draws in-process is under no such
pressure. Applied to headroom rather than to occupancy, it predicted the whole
table in advance: claude delegates its footer and therefore serializes the
windows it draws; Crush draws in-process and serializes nothing; opencode's
chrome is its own TUI and its only serialized surfaces are the message parts.
The framework survived an attempt to use it on a different quantity, which is
worth more than a confirmation on the quantity it was built from.

The one runtime it does NOT explain is codex, whose channel is a file it writes
for its own resume path rather than chrome it delegates. So the search list for
a new harness needs a second entry alongside "what does it draw with someone
else's command": "what does it write down in order to resume."

### 4. None of the recommended channels is credential-adjacent

The categorical constraint from `question-interactive-context-pressure` is
satisfied without effort here: claude's statusline payload arrives on stdin,
codex's rollout is a session transcript file, and neither requires a handle on
a store that also holds credentials. The one credential-adjacent artifact in
the catalog (opencode.db) is also the one with no headroom in it. Recording
this as a clean result rather than as a mitigation.

---

## What I could not establish

1. **Whether the claude statusline `rate_limits` key is populated in practice,
   and with what.** I read the constructor and I have a captured payload that
   LACKS the key from a session that made no API call. Those two are consistent
   with each other, and neither is an observation of the populated shape. The
   presence claim is a code read. What settles it: one statusline tick captured
   from a session that has made at least one request.
2. **Whether codex's `secondary` window is ever populated**, and if so on which
   plan types. Null in 2,098 of 2,098 records. This is the difference between
   codex offering a usable burst signal and offering a weekly one.
3. **Which mechanism produces codex's small decrements** (rolling-window aging
   versus denominator change). Not separable from a percent-only channel.
4. **Whether opencode's `responseHeaders` is ever populated.** Type-declared,
   zero rate-limit header names in the local store.
5. **Whether `get_usage` is reachable from a marvel-spawned session**, and under
   which run modes. The subtype is in the roster and the transport is a control
   channel with a `[bridge:repl]` implementation, but several sibling subtypes
   carry "not supported in this context (callback not registered)" errors, so
   availability is plainly context-dependent and I did not test it.
6. **What any of this looks like behind a router.** `router-and-backend-as-
   first-class-concepts.md` names reif as having a hard dependency on knowing
   where limits bind. Nothing in this catalog addresses a liteLLM-style router,
   where the limit may bind at the router's pool rather than at the account.
7. **gemini, entirely.**

---

## Phase 2, with pre-stated success signals

Ordered by cost. Each is a real probe; none of them is this document.

**P2-a. claude statusline, populated shape.** Capture one statusline payload
from a session that has made at least one API request, in an isolated HOME, and
record whether `rate_limits` is present and what types its members carry.
Success signal: a captured payload committed as a fixture beside
`statusline-2.1.226-empty.json`, showing `rate_limits.five_hour.used_percentage`
as a number in 0 to 100 and `resets_at` as an integer epoch. Failure signal:
key absent despite a completed request, which would mean the header family is
not sent on this account's auth mode and would demote claude to `no channel`
for subscription-independent deployments.

**P2-b. ctx-forward carries the triple.** Extend `statuslinePayload` with
`rate_limits`, forward it, and give the daemon an account-scoped rather than
session-scoped home for it. Success signal: `marvel describe session` shows the
account's five-hour and seven-day utilization with reset instants, identical
across every session on the account, and explicitly NOT rendered as a
per-session column. This is the one item where the transport already exists and
the work is a struct field plus a store decision.

**P2-c. codex rollout headroom.** Read `payload.rate_limits` from the same tail
read that phase 2 of the CTX% sweep already needs for occupancy. Success
signal: one sample carrying `(used_percent, window_minutes, resets_at)` reaching
the daemon from a marvel-spawned codex pane, with a decreasing series accepted
rather than rejected.

**P2-d. opencode synthetic 429.** Point opencode at a mock endpoint that
returns 429 with `retry-after` and a `x-ratelimit-*` family, and check whether
a `RetryPart` lands with `responseHeaders` populated. Success signal: the
headers appear in the persisted part. This is the only phase-2 item that
requires provoking a limit response, and it must be provoked from a MOCK, never
from a provider. Costs no quota.

**P2-e. `get_usage` reachability.** Establish whether the control request can be
issued to a marvel-spawned claude session and what it returns. Gate this behind
a decision about polling cadence before any of it is wired, because the
endpoint throttles.

---

## Non-goals

- Spending model quota to observe a limit. Nothing in this document did, and
  nothing in phase 2 should beyond the single mock-endpoint case.
- Deliberately approaching or tripping a rate limit, on any account, for any
  reason.
- Designing the degradation policy. Knowing the headroom is a prerequisite for
  that argument, not the argument. The policy question belongs with
  `question-shift-triggers` and the admission work.
- Deciding where headroom lives in the store. P2-b names it as a decision, and
  a decision it should stay until there is a sample to put somewhere.
- The router case. That is `router-and-backend-as-first-class-concepts.md`, and
  it may invalidate the account-scoped model this catalog assumes throughout.
