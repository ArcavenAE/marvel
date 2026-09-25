# Token-throughput RATE column, and columns as a user option

- **Status:** idea (pre-hypothesis, no commitment). Two features, one root
  cause: `marvel get sessions` has no token-throughput signal and a rigid,
  hardcoded column layout. Extended 2026-09-25 by operator ruling with a
  third (Feature C, account budget, reset, and credit columns), which rides
  the same substrate.
- **Date:** 2026-09-16
- **Origin:** commission via director. The rate column is the forcing
  function; first-class column definitions are the substrate it drags in.
- **Subject:** marvel (the `get sessions` display and the usage accountant).
  Filed in the marvel graph per the subject test.
- **Related:** [[health-signal-taxonomy]] (rate-near-zero is a cheap liveness
  proxy), [[subagent-session-display]] (the same table, the same
  gaps/alignment problem), [[context-pressure-is-an-operating-point-not-a-fill-level]]
  (why a numeric column must be commensurable and sortable). aae-orc-vdcwm
  (a WORKDIR column, an instance of "one more hardcoded column" this idea
  generalizes). aae-orc-dc1j (CTX% acquisition for interactive sessions,
  the same three-state discipline).

## The two features

### Feature A: a tokens-in / tokens-out RATE column

The operator's standing question about a running session is not "how many
tokens" but "is this thing *moving*." Marvel has no glanceable answer today.
CTX% shows occupancy, not pulse; HEALTH is liveness (process + pane), which
a stalled-but-alive session passes. A token-throughput rate is the cheapest
"it is working / it went quiet" signal marvel can render, and it decays to
zero on its own when a session stops emitting.

Output t/s is the generation pulse ("the model is producing"); input t/s
spikes on large tool results and compactions. If a single column, it is
**output** t/s. If both, they are labelled, and that is two columns, which
is exactly what Feature B exists to accommodate.

### Feature B: displayed columns as a user option

`renderSessionTable` hardcodes thirteen columns in a fixed order
(`WORKSPACE TEAM ROLE GEN AGENT NAME STATE HEALTH CTX% CPU% RSS DESK
RUNTIME LLM`). Adding rate-in and rate-out pushes it to fifteen and the
table wraps on an ordinary terminal. Columns cannot keep being bolted on.
They become a **selection**: which columns, and in what order, chosen by
CLI flag and by user preference.

## Where the rate comes from (the source, honestly)

This is an **accountant** feature with a render on the end, not a render
feature. The `usage.Accountant` already carries per-session cumulative
`Spend{In, Out, ...}` and every `Sample` is timestamped (`Sample.TS`). A
rate is a derivative it does not currently keep.

- **Do not** render the instantaneous delta between the last two samples.
  Samples arrive per-request, so an instantaneous rate spikes into the
  thousands during a large turn and craters to zero between turns: jumpy
  and useless for "is it moving."
- **Do** keep a per-session decaying rate (EMA) updated on each `Observe`,
  half-life on the order of the poll/watch interval (~15-30s). It is cheap,
  it lives in the one place that already holds session state, and it decays
  to zero on its own when samples stop, which *is* the "went quiet" signal.
- The decayed reading rides `SessionContext` next to CTX%, produced by the
  same two channels (adapter streams for headless; the statusline feed +
  heartbeat for interactive claude).

### Cross-harness availability

Rate is available exactly where CTX% is, and absent in the same places: the
accountant is fed by adapter streams (headless claude, codex, opencode) and
by the interactive-claude statusline/heartbeat path. It follows the CTX%
sentinel discipline, but needs only **two** states, not three, because a
rate has no denominator to miss:

- `-` never measured (no stream, adopted pane, non-stream adapter).
- a value otherwise (including a legitimate, decaying `0/s`).

There is no `?` state: `?` exists for CTX% because a percentage against an
unresolved window is a fiction. A rate is tokens over wall-clock, both of
which the sample carries, so it is either measured or not.

## The display trap the director named (units, precision, truncation)

Rates like `2.334 t/s` next to `168 t/s` mis-sort and mislead by width: the
`2.x` reads as larger than the `168` because it is wider on the glyph, and a
lexical or width-based sort ranks it higher. Measured:

```
$ printf '%s\n' '2.334' '168' '1240' '12' | sort
12
1240
168
2.334          # slowest session sorts to the bottom, looks fastest
$ printf '%s\n' '2.334' '168' '1240' '12' | sort -n
2.334
12
168
1240
```

Two distinct harms, both fixed by the same discipline, both permanent if
skipped:

- **Mis-sort:** the column must sort by its numeric *value*, never its
  rendered string.
- **Mis-read:** even sorted correctly, a wider token reads as larger, so the
  cell must be right-aligned with magnitude-quantized precision and a single
  unit.

Rendering rule for the rate cell:

- one unit, `t/s`;
- precision quantized to magnitude: `< 10` one decimal (`2.3/s`), `10-999`
  integer (`168/s`), `>= 1000` SI-ish (`1.2k/s`);
- fixed field width, right-aligned, decimal points aligned or absent;
- a stopped session's cell *decays visibly to* `0/s` rather than freezing at
  its last value (a frozen `168` is a lie; `40, 12, 0` is the truth on
  schedule).

## The architectural payload (why the two features are one idea)

The rate column is a forcing function. The real thing worth building is
**first-class column definitions** with typed alignment and sort keys:
every numeric column (rate, CTX%, CPU%, RSS) declares "I am numeric,
right-align me, sort me by value not string" as a property of the column,
not of the render call. Do that once and the mis-sort bug cannot recur on
any column, present or future. The rigid layout and the mis-sort hazard are
the same defect seen from two sides.

## Configurable columns: the grammar and the precedence

- **Contract is long names**, order-significant: `--columns
  workspace,team,name,ctx,rate`. The order in the flag *is* the render
  order. Single-letter aliases (the watch-mode sort keys `w t R g n s c d r
  l h p m`) are accepted where unambiguous, but they are a convenience, not
  the contract: they collide the moment columns are added (`r` is already
  runtime; rate wants `r` too), so the long names are canonical.
- **Preference home:** a new top-level `display:` block in
  `~/.marvel/config.yaml` (today `Config{Clusters, CurrentCluster}`; no
  display-shaped field exists), e.g. `display.session_columns: [...]`.
- **Precedence, stated once:** explicit `--columns` flag > preference >
  built-in default. The same precedence shape cluster selection already
  uses.
- **The default set is exactly today's thirteen in today's order.** The
  feature is opt-in width relief, not a re-layout; no operator's muscle
  memory breaks on upgrade.
- **Fail soft:** an unknown or misspelled column name (in flag or
  preference) is a warning to stderr and is dropped; the table still
  renders. A display preference that can brick `get sessions` is worse than
  no preference.

## Feature C: account budget, reset, and credit columns (operator ruling 2026-09-25)

The operator ruled that the new columns (the Tin/Tout pair and the
tokens-out rate above) become selectable on and off, and that the harness
budget, reset, and credit metrics join them: any of them should be
displayable. They ride the Feature B substrate; none is a hardcoded
addition.

### The metrics

- **Claude Code**, from the statusline payload's `rate_limits` (already
  captured by `cmd/marvel/ctxforward.go` and shown in the pane, deliberately
  not sent on the heartbeat): `five_hour` (the "current session" window) and
  `seven_day` (the weekly window), each with `used_percentage` and
  `resets_at`. Time to reset matters as much as the percentage: 90% with
  ten minutes left and 90% with four days left call for opposite actions.
  The populated shape is read from the binary, not yet measured from
  traffic (ctxforward.go says so); treat it as unverified until a live
  payload is captured.
- **codex**, from every rollout `token_count` event's `rate_limits`:
  `limit_id`; `primary` and `secondary`, each with `used_percent`,
  `window_minutes`, `resets_at`; and `credits` {`has_credits`, `unlimited`,
  `balance`} (finding-050).
- **Key codex windows on `window_minutes`, never on position.** A live codex
  seat on 2026-09-24 carried primary=300 (5 hours) and secondary=10080
  (weekly); `internal/runtime/codex/mapping.md` item 5 and finding-007
  describe a fixture where primary was the weekly window. Both readings are
  true of their data, so the position carries no meaning; a 5h column reads
  whichever window has `window_minutes` 300.
- **Kept:** Tin/Tout and the tokens-out rate (Features A and B).

### The flags

Each is a derived, diagnostic cell (SOUL section 8, ADR-007): it informs,
never gates, and no threshold here blocks a spawn or a shift.

- **near-limit**, per window: `used` at or above a display threshold.
- **time-to-reset**, per window: `resets_at` minus now; once `resets_at` has
  passed with no newer reading, the cell says the reading is stale rather
  than printing a negative duration (the `untilReset` rule in
  ctxforward.go).
- **credit state** (codex): unlimited, has credits with a balance, or none.
- **no rate_limits block**: a candidate signal that a session runs on an API
  backend rather than a subscription. **Unverified.** For Claude Code the
  block is also absent until the session has parsed its first API response,
  so the flag is only meaningful after a response has been seen; it must
  not fire on a fresh session.

### The constraint: these figures belong to the account, not the session

ctxforward.go records the reason: every session on one account reports the
same windows, so N sessions are N reporters of one number. A per-session
column of that number is a category error, and summing or averaging it
across sessions is wrong. Each reading also carries an as-of time, because
the freshest reporter wins and an idle session's copy goes stale.

Three presentations fit a per-session table:

1. **Labelled `acct`**, as the pane does: the cell repeats on every row of
   the account, the header says `ACCT`, and nothing aggregates it.
2. **Grouped by account**: rows sort by an account key, with the account
   figures shown once per group.
3. **A separate account view**: one row per account and harness (window,
   used, resets in, credits, flags, as-of, reporter count), with
   `get sessions` carrying at most an account key per row.

**Recommendation: 3, the separate account view, as the home.** One number
gets one row, so there is nothing to sum, sort, or threshold twice. When an
operator selects an account column in `get sessions` anyway, render it as
option 1 (`ACCT`-prefixed header, value repeated, never aggregated) so that
every metric stays displayable as the ruling asks.

**Open:** marvel has no account key today. The grouping key must come from
something marvel can see without holding an account identifier (for codex,
a seat's rollout home; for Claude Code, nothing yet). That key is a
prerequisite for options 2 and 3 and for de-duplicating reporters.

## Width and truncation (2026-09-25)

When the chosen columns do not fit the terminal, the policy lives in
[[get-sessions-width-and-truncation]]: each column definition gains a
priority, a soft max width, and a truncation mode, applied only on a
terminal, with `--no-trunc` to opt out. It adds properties to the column
definitions above and changes neither the grammar nor the precedence.

## The seam to the state watchdog (do not let these diverge)

A rate that decays to zero is the cheapest trigger for the tmux harness-state
watchdog ([[health-signal-taxonomy]] and its idea): "no rate-of-change in
CTX / no tokens flowing" is "rate near zero." This idea builds that
indicator whether it means to or not. The watchdog should *consume* this
rate rather than compute its own, so there is one definition of "quiet."

## Open questions

- EMA half-life: is one fixed constant right, or does it want to track the
  watch `--interval` so the display and the smoothing agree?
- One rate column (output) as the default, with input available by column
  selection? Or a single combined "throughput" the operator can split?
- Does the rate belong in `get sessions` at all, or is a `marvel top`-style
  view the honest home for fast-moving numerics (same question
  [[subagent-session-display]] raises about the table's limits)?
- Should `describe session` carry the rate history (a short series /
  sparkline) while the table carries only the current decayed value?

## Crystallization signal

When the first fleet workload runs long enough that "which sessions went
quiet" becomes a recurring by-hand check, extract a frontier question and a
probe brief: EMA in the accountant behind `SessionContext`, a numeric
first-class rate column, then the column-selection substrate underneath it.

## Starting point filed to bd

One flat starter ticket: the RATE column (accountant EMA + first-class
numeric column with typed alignment/sort). It is the smallest piece that
pays off alone and pulls the column substrate in behind it. Configurable
columns (Feature B) is a separate bead filed when that work is committed,
not a sub-issue invented alongside the starter (per bd-hierarchy: flat
tickets, real containers only).

2026-09-25: the operator ruling commits Feature B, so its bead is filed
flat as aae-orc-f08m0, carrying Feature C's display half.
Acquisition stays where it already lives: the codex reader in
aae-orc-p577l, the account headroom channel in aae-orc-reif, and the codex
context channel in aae-orc-pt8k.
