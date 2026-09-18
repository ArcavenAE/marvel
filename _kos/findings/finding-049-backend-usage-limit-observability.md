# finding-049: what usage-limit state marvel can observe per auth backend

Work: director research dispatch (operator-authorized), 2026-09-17. Feeds the
agentic resource matrix (token-spend / spend-budget rows 1 and 2) and vision
Gap 4 (credential custody plus spend budgets). Verified against
`internal/api/budget.go`, `internal/api/backend.go`, `internal/usage/limits.go`.
Governing decisions: ADR-009 (credential custody boundary), ADR-007 (automation
boundary), SOUL section 3.

**Placement.** The subject is marvel's budget awareness: what marvel can read to
surface a real remaining-budget signal where one exists and fall back to a
marvel-defined virtual budget where none does. That is a marvel concern, so the
finding lives here. The per-backend disk facts describe the harnesses marvel
observes (Claude Code, codex) and are objects, not co-owners; without marvel's
budget question they would not be filed. The custody rule it turns on is
aae-orc-level (SOUL section 3 / ADR-009), applied here to marvel's observation
path.

## The question

For each auth backend a marvel-managed agent runs under, what usage-limit or
quota state can marvel read programmatically, so it can (a) surface a real
remaining-budget signal where the backend exposes one, and (b) fall back to a
marvel-defined virtual budget where it does not. Researched in scope now: the
Claude Code subscription backend (Max/Pro OAuth) and codex on a ChatGPT/OpenAI
subscription login.

## 1. The custody boundary is custody-vs-brokering, not a bar on touching the bearer (load-bearing)

The boundary that governs which usage numbers marvel may reach is ADR-009 /
SOUL section 3, and it is an AUDIENCE test, not a prohibition on marvel knowing
or moving a credential. ADR-009 states it plainly: a component may hold an
artifact it can itself revoke or re-mint without a human at a third party's
console; it must not hold the artifact that is bearer authority at a third
party (the most durable one). And: "Brokering is permitted; custody is not.
Asking a vault to mint a short-lived scoped credential and handing it on is
inside the line, because the vault can revoke it and the component can ask
again."

So the durable at-rest custody of a subscription bearer belongs in a VAULT,
which is a separate project outside marvel (marvel is not a vault, just as it is
not a NATS server). marvel MAY manage, supervise, broker, and push credentials.
This is not hypothetical: marvel already does it. The `internal/api/credential.go`
Credential resource is transient and never persisted; the daemon exposes
`credential.put` / `credential.get` / `credential.reveal` / `credential.delete`
(`internal/daemon/credential.go`); the `marvel credential` CLI pushes a
credential to the daemon (value from `--value-file` or stdin, `put` reachable by
a credential-push key, `reveal` on the local socket only, "transient, never
persisted"); and marvel already delivers a credential into a spawned process
environment (the bus leaf seed rides as `DIRECTOR_LEAF_NKEY` into the broker
environment, `internal/bus/render.go` / `supervisor.go`; a team credential rides
into the session environment). marvel knows and moves credentials; it is simply
not a write-to-disk at-rest vault.

The model is the same one marvel already uses for NATS: marvel does not BE the
service, it supervises an external instance as a declared workload and makes it
available to agents. Applied to secrets: an external vault holds durable
custody; marvel is the vault control-plane and a service provider to agents,
brokering and pushing short-lived scoped credentials through the credential
machinery above. This is the trust plane in `question-marvel-service-provider-shape`
("identity minting and credential brokering"), whose credential-boundary clause
already reads: marvel "brokers access, holds session state on the agent's behalf
(for example the OAuth or Claude-backend session), and connects agents to a
vault that mints short-lived scoped credentials."

Consequence for observability: a subscription usage endpoint IS reachable within
the boundary, through three channels (section 5), so an OAuth subscription
backend is observable, not virtual-only. The one case ADR-009 leaves explicitly
UNRESOLVED is marvel-the-scheduler itself holding the long-lived backend OAuth
session directly; that needs its own decision and is not required by any of the
three channels, so it is not on the recommended path.

## 2. Per-backend results

### 2a. Codex (ChatGPT/OpenAI subscription): observable on disk

codex writes the same rate-limit snapshot its TUI shows into the per-session
rollout log, `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`, as `event_msg`
records whose `payload.rate_limits` object carries:

- `primary`: a 5-hour rolling window: `used_percent` (0 to 100),
  `window_minutes` (about 300), `resets_at` (unix epoch seconds).
- `secondary`: a weekly rolling window, same shape (`window_minutes` about
  10080).
- `plan_type`, `limit_id`, and `credits` (`has_credits`, `unlimited`, balance).

Confirmed on this machine: 254 rollout files, the newest from 2026-09-17, with
3270 `rate_limits` records carrying the full field set (field names read only,
no values or usage numbers). marvel reads the freshest value by taking the last
`rate_limits` record in the most-recently-modified rollout file. No credential,
no request-path access.

Three caveats bound this signal:

- It is percent-consumed plus a reset epoch, not an absolute token ledger.
- It is a passive tail: it refreshes only while a codex session is running
  (a record is appended roughly once per model turn). A stale file means no
  recent session, not zero usage. There is no standing codex daemon or socket
  to poll (verified: no listener, no process at check time).
- The rollout JSONL is a codex-internal format, not a stable API. A reader must
  version-guard and degrade to the virtual budget when the schema shifts.

### 2b. Claude Code (Max/Pro OAuth): not on disk, observable via channel

The live 5-hour and weekly remaining-and-reset numbers that `/usage` shows are
fetched from an OAuth-authenticated account endpoint on demand and are never
cached to disk. What is on disk under `~/.claude`:

- `stats-cache.json`: cumulative and daily per-model consumption spent, not
  remaining and not reset.
- the statusline stdin stream: per-session context-window percent and
  per-session cost/tokens/duration, only if marvel is the statusline command
  or otherwise captures that stream. Per-session, not subscription-wide.
- `backend-profiles.json`: account tier classification
  (`organizationRateLimitTier`, `userRateLimitTier`, `seatTier`, `billingType`,
  `hasExtraUsageEnabled`, trial fields). Which limit bucket the account is in,
  not live counts.

OAuth credentials are held in the macOS login Keychain, not an on-disk file. So
the on-disk artifacts give marvel consumption and tier, not the live
remaining-and-reset. But the live number is NOT out of reach: it lives behind
the OAuth account endpoint, which is reachable within the custody boundary
through the three channels in section 5 (agent self-query, vault-brokered,
utility service). Claude Code is therefore an observable-via-channel backend for
its subscription caps, with the virtual budget as the fallback when no channel
has answered recently, not a virtual-only backend. Codex's on-disk snapshot
stands as the cheapest observed path for codex; agent self-query is the cheapest
for Claude Code.

## 3. Channels that are not marvel channels (framing corrections)

- There is no `anthropic-ratelimit-unified-*` header publicly. The documented
  `anthropic-ratelimit-*` family (`requests`, `tokens`, `input-tokens`,
  `output-tokens`, each with `limit`/`remaining`/`reset`, resets as RFC 3339)
  describes per-minute org limits on the API-key / Bedrock / Claude-platform
  billing path, not the subscription 5-hour or weekly caps.
- The OpenAI `x-ratelimit-*` family is the same story on the codex side: an API
  REST surface, not the ChatGPT-subscription caps.
- In both cases the harness makes the model calls internally, so a supervisor
  off the request path never sees these headers, and neither harness mirrors
  them to disk. Headers are not a marvel channel for either subscription
  backend.

## 4. Limit-shape taxonomy

Any backend maps onto four axes:

- window: rolling-D (codex 5-hour and weekly; Claude Code 5-hour session) or
  anchored-period (Claude Code weekly, a fixed per-account reset time).
- scope: account, model-tier, workspace, or session.
- metric: requests, tokens, cost, opaque units, or percent-of-window.
- source: observed or virtual. Observed has three channels: (a) an on-disk
  harness artifact marvel reads directly (codex rollout JSONL); (b) an
  agent-reported reading, where a marvel-managed agent already authenticated
  under the operator's subscription queries the vendor usage endpoint and reports
  remaining-and-reset back over the harness event stream or the director bus (no
  new custody); (c) a service-brokered reading, where a vault-backed utility
  service marvel supervises reads the endpoint and exposes the number. Virtual is
  the fallback: marvel meters its own event stream against a manifest ceiling.

The source axis is the one this research turns on, and it is per-backend, not a
global property. It is also not binary: a backend can carry more than one
channel, and the provenance grade on the reading (section 5) records which one
answered and how fresh it is.

## 5. Recommendation: extend the existing budget surface

marvel already has the surface; this extends it rather than adding a parallel
interface. Verified in the code:

- The `Dimension` registry (`internal/api/budget.go`, `budgetSpecs`) is the
  resource-matrix-as-budget surface. Registered today: `max_sessions`
  (ShapeCount, implemented), `max_tokens` (ShapeCumulative, implemented),
  `max_cost_usd` (ShapeCumulative, registered but not implemented, owner
  "aae-orc-qiay follow-on"), `max_team_rss_bytes` and `max_session_ctx_percent`
  (not implemented).
- `OnUnmeasured` (`admit` default, `refuse` when the operator ratifies) is the
  already-decided "what to do when the meter cannot answer" knob (ADR-007).
- The limit ladder (`internal/usage/limits.go`, `LimitSource`) and the
  orthogonal `KeyConfidence` (with `KeyRedirected`) plus `BackendRedirection`
  (`default`/`redirected`/`unknown`, `internal/api/backend.go`) are the
  real-vs-virtual discriminator.

The core arithmetic is unaffected: `admission.Check` is pure (no store, meter,
clock, or I/O), the meter reading rides `Snapshot`, and the caller supplies it.
A windowed clause fits that: its `used_percent`, `resets_at`, and as-of are new
`Snapshot` fields the codex-tail reader fills, and `Check` stays pure. (Checked
with marvel-builder against the code, 2026-09-17.)

The recommendation:

1. Virtual budget (universal floor, covers every backend including
   no-visibility ones): implement `max_cost_usd` metered against the
   accountant's layout-normalized cumulative spend, mirroring implemented
   `max_tokens`. This is the fallback for Claude Code. Owned by the existing
   `aae-orc-qiay` follow-on.
2. Observed windowed limit: the Shape vocabulary is `count`/`cumulative`/`level`
   and cannot express "resets every 5 hours" or "resets weekly". Add a fourth
   Shape, `ShapeWindowed`, carrying `{window_minutes or period, used_percent or
   amount, resets_at, as-of}`; Unit is percent for the observed case. Make its
   behavior EXPLICIT per-shape properties rather than extending the
   count-vs-not-count binary, so a fourth shape does not get re-decided at every
   switch:

   | property | ShapeCount | ShapeCumulative | ShapeWindowed |
   |---|---|---|---|
   | over time | falls with live sessions | monotonic within a daemon | resets at the window boundary |
   | overlap | applies | n/a | exempt (Used is observed, not derived from session count) |
   | partial headroom | yes | no | no (already excluded by the `AllowPartial && Shape==ShapeCount` guard) |
   | repair | n/a | skipped (gating repair on a monotonic meter is a permanent outage, R2) | its own decision: a temporary hold, since the gauge self-clears at the boundary, not a permanent skip |
   | reconciler meter | count available | skipped (meterless) | skipped (the async tail is meterless there) |
   | meter source | live sessions | the accountant | the codex-tail reader (observed) |
   | rendering | count detail | "spent X since=" | "account at X% against dim, resets_at, as-of" |

   Three sites today treat "not count" as "cumulative/monotonic" and must gain a
   windowed branch: `check()` (the cumulative-clause skip on `Repair`),
   `CheckSessions()` (generalize the cumulative skip to "any non-count clause the
   reconciler has no meter for"), and `decidingDetail()` (the resetting-window
   rendering). Shift logic beyond admission is untouched: the auto-shift trigger
   reads occupancy, not budget.
3. Observed reader and its provenance. The observed reading carries its own
   provenance grade on the budget `Snapshot` (a rank plus an as-of plus
   downgrade-when-stale), mirroring `LimitSource`'s design but NOT on the usage
   `limitLadder` itself: that ladder resolves the context-window denominator for
   CTX% occupancy, and these `rate_limits` are a rate quota (percent-consumed,
   `resets_at`), a different quantity; a rung there would conflate the occupancy
   denominator with a spend gauge. Reuse the pattern, place it on the budget
   `Snapshot`. The provenance grades are the three observed channels, most
   trusted and freshest winning:
   - `on-disk-harness`: marvel reads the harness's own artifact (codex rollout
     JSONL). No credential.
   - `agent-reported`: a marvel-managed agent already authenticated under the
     operator's subscription queries the vendor usage endpoint and reports the
     reading back over the harness event stream or the director bus. The cheapest
     path for an OAuth subscription backend, and it adds no new custody (the agent
     uses the session auth it already has).
   - `service-brokered`: a vault-backed utility service marvel supervises reads
     the endpoint and exposes the number (section 5b).
   Each grade carries an as-of and downgrades to Unmeasured when stale. The trust
   gate (`BackendDefault` only, `KeyConfidence` not `KeyRedirected` or
   `KeyUndeterminable`) lives at the READER that builds the `Snapshot`, never in
   pure `Check`.
4. No-visibility reuses existing semantics unchanged: staleness ("no recent
   session") maps to the clause going Unmeasured, then `OnUnmeasured` governs
   (`admit` default, `refuse` on ratification, ADR-007), then the virtual budget
   is the floor. The denominator stays absent (`ContextLimit` 0, never
   defaulted). No new decision.
5. Custody constraint on the reader (section 1): the reader never causes marvel
   to hold durable bearer custody. It consumes an on-disk harness artifact, an
   agent-reported reading (the agent's own session auth), or a vault-brokered
   reading (durable custody at the vault); marvel brokers and pushes, it does not
   hold the long-lived bearer at rest. The on-disk reader stays read-only and
   treats the JSONL as untrusted data, version-guarded; on a schema shift or a
   stale or absent reading the path is Unmeasured, then `OnUnmeasured`, then
   virtual, with no new decision.

### 5a. Account-global gauge vs team-scoped clause

The codex 5-hour and weekly quota is ACCOUNT-global: it is the whole ChatGPT
subscription, shared by every codex session on the box. A Budget clause is
team-scoped. So a per-team windowed clause is each team's THRESHOLD against one
shared account gauge, not a per-team quota. Two teams reading it see the same
number with independent tolerances. Model it as a threshold on a shared account
gauge, not as if each team owned a slice. The same holds for any future
account-global observed limit.

### 5b. The vault is a separate project; marvel is its control-plane

The service-brokered channel depends on a vault that does not exist yet. The
architectural requirement, and it is a requirement not a detail: durable at-rest
custody of a subscription bearer lives in an external vault, a separate
integrated project (the credential plane of vision Gap 4). marvel supervises
that vault as a declared workload and makes its services available to agents,
exactly as it supervises external NATS and makes the bus available. marvel
brokers and pushes short-lived scoped credentials through the machinery it
already has (`credential.put` / `reveal` / `delete`, the
leaf-seed-into-environment delivery); it does not hold the durable bearer at
rest. This is the NATS pattern applied to secrets, and it is the trust plane
already named in `question-marvel-service-provider-shape`. Building the vault is
its own work, out of scope for the budget-observability reader, which consumes
whatever the vault-backed service or the agent reports.

## 6. Disposition

This finding lands now (research output). The build it recommends is a roadmap
commitment held for the operator. When the go is relayed it is one flat
marvel-scoped bd ticket covering: `ShapeWindowed` with its explicit per-shape
properties and the three switch-site branches; the observed-limit reader with
the on-disk-harness, agent-reported, and service-brokered provenance grades and
the trust gate; and the codex rollout-JSONL tail as the first reader. It
references the existing `aae-orc-qiay` follow-on for the virtual `max_cost_usd`
floor rather than duplicating it, and it is flat, not a parent (bd-hierarchy).
The external vault is separate work, not part of this ticket; the reader is
built to consume a reported reading whether it comes from an agent or a
vault-backed service, so it does not block on the vault.

## Sources

Per-backend disk investigation on this machine (read-only, secret-safe): the
files and rollout schema named above. Web corroboration for header families and
limit shapes: Anthropic rate-limit documentation (header names, reset format);
OpenAI rate-limit documentation; codex protocol notes on the TokenCount /
RateLimitSnapshot event. marvel code claims verified in the files cited inline.
