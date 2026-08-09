# finding-018: a marvel shift is fast because it does no handoff, and the number that beats compaction is not measuring the same thing

**Date:** 2026-08-08
**Ticket:** `aae-orc-r4q6`
**Probe:** `_kos/probes/probe-cold-shift-wall-clock.md` (pre-registered
before the rig started; hypotheses, arms, success signals, and the
cache-identity trap were all written down first)
**Question:** `question-shift-triggers`; bears on
`question-interactive-context-pressure`.
**Raw data:** `finding-018-data-shift-timings.jsonl` (26 shifts),
`finding-018-data-harness-runs.jsonl` (5 harness runs)
**Status:** MEASURED for the control plane, against pre-stated success
signals, so this is a finding rather than a candidate catalog. The
harness arm deviated from its pre-registered form and is reported
separately and more weakly, for the reason recorded under "Deviation".

## The result in one line

A marvel shift of a one-role team completes in a median of **8.92 s**,
and the cost scales at **7.99 s per role** with a residual under 10 ms
across three role counts. Compaction's median is 154 s (finding-016).
On wall clock the shift wins by an order for any team under about
nineteen roles.

**And the comparison is not between two ways of doing the same thing.**
A compaction preserves the session's working context, compressed. A
marvel shift preserves nothing: there is no handoff artifact, no drain,
and no notice to the departing agent. The shift is fast in large part
because it does not do the work that makes a handoff a handoff.

## What was measured

26 shifts, four arms, one daemon under an isolated HOME and a dedicated
tmux server. Every arm ran clean: 26 of 26 reached
`team.shift-completed` with no timeout and no abort.

Stage timestamps come from the daemon's own event ring (RFC3339,
sub-second) except the readiness stamp, which is a 50 Hz poll of the
session table. That exception is itself a result and is discussed below.

All times are seconds from `team.shift-started`.

| arm | n | successor created | successor running | first heartbeat | predecessor deleted | **shift completed** |
|---|---|---|---|---|---|---|
| A: 1 role, simulator, heartbeat gate | 7 | 0.097 | 0.097 | 1.090 | 4.957 | **8.922** |
| B: 1 role, shell, pane-only gate | 7 | 0.114 | 0.122 | n/a | 0.959 | **4.925** |
| C: 2 roles, simulator, heartbeat gate | 7 | 0.163 | 0.163 | 1.153 | 4.928 | **16.906** |
| E: 4 roles, simulator, heartbeat gate | 5 | 0.240 | 0.240 | 1.232 | 4.957 | **32.893** |

Medians. Spread on the headline column, as min / max / IQR:

| arm | min | max | IQR |
|---|---|---|---|
| A | 7.240 | 8.928 | 0.005 |
| B | 4.821 | 5.225 | 0.059 |
| C | 15.913 | 16.955 | 0.040 |
| E | 31.600 | 32.967 | 0.100 |

The IQR is milliseconds because the thing being measured is a clock, not
work. Each arm has one low outlier exactly one reconcile tick below the
median, which is the sampling phase of the run relative to the ticker.

### The scaling law

Three role counts, fitted on the two extremes and checked against the
middle:

```
t = 0.93 + 7.99 * roles

roles=1: predicted  8.92   measured  8.92   residual +0.000 s
roles=2: predicted 16.91   measured 16.91   residual +0.006 s
roles=4: predicted 32.89   measured 32.89   residual +0.000 s
```

This is not a curve fit that happens to work. `reconcileShift` advances
at most one phase per reconcile tick, the ticker is 2 s
(`daemon.ReconcileInterval`), roles shift strictly in series
(supervisor last, `shiftOrder`), and each role costs four ticks:
launch, observe readiness, drain, observe drained. 7.99 s per role is
four ticks per role, and the arithmetic is visible in the code before
it is visible in the data.

Extrapolating the same law: 8 roles is 64.9 s, and **the crossover with
compaction's 154 s median falls at about 19 roles**.

### Where the time goes, and it is not work

Arm A's 8.92 s decomposes as roughly 1.1 s of anything happening and
7.8 s of waiting for the ticker. The successor pane exists 97 ms after
the shift starts (the shift RPC calls `ReconcileOnce` directly, so the
first tick is free) and heartbeats at 1.09 s. Everything after that is
quantization. Arm B, which has no heartbeat gate, costs 4.93 s: two
fewer ticks, same shape.

The practical reading: **marvel's shift latency is a tunable, not a
cost.** Nearly all of it is the reconcile interval, and a shift-aware
fast path would reduce a one-role shift to about a second without
changing the state machine's semantics.

## The three stages that do not exist

The ticket asked for five stages. Three of them have no referent in this
codebase, and I checked the code rather than inferring from the timings.

**Drain time for the departing session: there is no drain.**
`shiftDrain` calls `sessMgr.Delete`, and `Delete` calls `tmux kill-pane`
on the predecessor's pane. There is no signal-then-wait, no request to
finish the current turn, no flush. The 3 s `instanceTeardownGrace` in
`retireInstance` bounds marvel's own stream-reader teardown, not the
agent: the pane is already being killed. A departing agent mid-turn
loses that turn. MEASURED as a code fact (grep and read of
`internal/session/manager.go` and `internal/team/controller.go`), and
consistent with arm B's 0.96 s predecessor-deleted stamp, which is one
tick and leaves no room for a grace period.

**Handoff artifact read: there is no handoff artifact.** `grep -ri
handoff --include="*.go"` over the whole repository returns zero hits.
vision.md records a 2026-08-01 ruling assigning ownership of the handoff
schema to marvel and its content to the departing agent. That ruling has
no code behind it. The successor starts with no record of what its
predecessor was doing.

**Configuration and pack resolution: not a shift-time stage.** Policy
projection happens at session spawn, inside the 97 ms
create-to-running window in arms A and B; per ADR-008 the current phase
has sideshow install into the workspace ahead of time and marvel consume
the host filesystem as-is. There is nothing for a shift to resolve.

A fourth gap surfaced from instrumenting rather than reading. **Marvel
emits no event when a successor becomes ready.** The ring carries
`session.created`, `session.deleted`, `team.shift-started` and
`team.shift-completed`, and the readiness decision in `allReady` is made
between two of them with nothing recorded. The single most load-bearing
instant in a handoff, the moment the control plane decides the successor
can take over, is not observable from the event ring. I had to poll the
session table at 50 Hz to timestamp it, which is why that one column in
the table above comes from a different clock than the rest.

## The harness arm, and the deviation

### Deviation from pre-registration

The pre-registered arm D was a real harness shifted through marvel. It
did not run in that form. The isolated HOME the rig requires carries no
credentials, and a harness launched under it fails at
`authentication_failed` before any model call. Running the daemon under
the real HOME instead was refused: that is where the operator's live
fleet state lives and four other agents were working on this machine at
the time.

So the handoff was decomposed. Arms A/B/C/E measure marvel's control
plane under full isolation. Arm D measures the harness standalone under
the real HOME, with no daemon and no tmux. The two compose additively
because in marvel's shift path they are strictly sequential: the pane is
spawned, and only then does the harness inside it start. **This is a
weaker instrument than the pre-registered one, and the composed total
below is INFERRED, not measured.**

### What arm D measured

Five headless one-shot runs, same ~95k-token input and same prompt,
back to back, claude-haiku-4-5.

| run | cache creation | cache read | output | wall s | ttft_stream ms | cost USD |
|---|---|---|---|---|---|---|
| 1 | 77,555 | 17,873 | 681 | 11.48 | 2255 | 0.1741 |
| 2 | 0 | 95,428 | 867 | 12.20 | 1234 | 0.0277 |
| 3 | 71,415 | 22,245 | 744 | 12.93 | 2228 | 0.1626 |
| 4 | 0 | 95,428 | 328 | 7.13 | 905 | 0.0250 |
| 5 | 0 | 95,428 | 1,328 | 17.01 | 2243 | 0.0300 |

Harness boot to first API call (`time_to_request_ms`) was 17 to 22 ms in
every run. That is the headless path; an interactive TUI successor's
boot is NOT measured here.

**The price classes, derived from these rows rather than a published
table.** Three warm runs give a linear solve for the output price and
the cache-read price; the cold rows then give the cache-write price:

```
output       $5.011 / MTok
cache read   $0.245 / MTok
cache write  $2.145 / MTok   (tier: ephemeral_1h, per the usage row)
write:read ratio = 8.77x
```

Checked back against every row: exact for four of five, and 0.15% off on
run 3. Derived rather than looked up because the point of the exercise
is the ratio, and because finding-016's cost falsifier had to ASSUME a
price ratio it could not measure. This measures it. Note that the
derived output price matches the published haiku-4.5 figure exactly
while the two cache figures do not match the published 5-minute tier;
the usage row says the tier is 1-hour. I did not establish which
published schedule applies, and the ratio does not depend on resolving
that.

**The cold-successor premium, measured.** A cold and a warm run consume
the SAME 95,428 input tokens. The entire difference is which price class
they land in. At the derived rates, the 77,555 tokens a cold successor
must re-create cost $0.1663 instead of $0.0190: a **8.8x premium on the
re-warmed portion**, and 6.3x on the run's total cost despite the cold
run producing fewer output tokens than two of the warm ones.

**Run 3 is the interesting row.** It is a partial re-warm: 71,415
created, 22,245 read, three runs into a back-to-back sequence. Cache
retention is not a clean binary, and a successor can pay most of the
premium without being cold in any sense an operator declared.

**A negative result worth keeping: the cold cache did not cost wall
clock.** Cold `ttft_stream_ms` was 2255 and 2228; warm was 1234, 905,
and 2243. The cold runs sit inside the warm range. At n=2 cold and n=3
warm I cannot detect a latency penalty for a cold cache at all. The
re-warm shows up in dollars, not in seconds. Anyone arguing cache
locality on latency grounds should not cite this data.

### The cache-identity trap, which fired live

The probe pre-registered a commitment not to call a successor cold on
`cache_read_input_tokens = 0` alone, because a row with both cache
classes at zero is the same arithmetic under two different identities.

That trap then fired unplanned, in the failed first attempt to run a
harness under the isolated HOME:

```
"total_cost_usd": 0,
"usage": {"input_tokens": 0, "cache_creation_input_tokens": 0,
          "cache_read_input_tokens": 0, "output_tokens": 0}
```

Read as a usage row, that is a maximally cold successor. The true
identity is `"is_error": true`, `"error": "authentication_failed"`,
`"duration_api_ms": 0`: no model call happened at all. The usage row
cannot tell those apart. The discriminators are `is_error` and
`terminal_reason`, which live outside `usage`.

**Consequence to carry: a cold-cache determination requires
`cache_creation > 0` AND `cache_read == 0` AND `is_error == false`.**
Both-zero is undetermined and must be reported as such, never as cold.

## Does a cold shift beat a 154 s compaction

**On the control plane, yes, and the evidence could have said otherwise.**
If a shift cost 154 s or more through marvel's own machinery, arms A
through E would have shown it; a one-role shift would have had to be
17x slower than measured, and the four-role arm 5x. The IQR is under
0.1 s across 26 runs. H2-via-control-plane is excluded for teams up to
four roles, and the measured scaling law puts the crossover near 19.

**Composed with the harness, still yes, but this number is inferred.**
8.9 s of control plane plus about 12 s for a successor's first
substantial turn is roughly 21 s against 154 s. The composition
assumption (that the two stages are sequential and additive) follows
from the code, but I did not measure them together.

**What my evidence cannot exclude, and this is the real limit.** The
comparison silently assumes a successor is useful after one turn. My
arm D turn was a single 95k-token question with the context handed to
it in the prompt. A real successor has no handoff artifact to read, so
it must rediscover its predecessor's working state by exploring. If
that takes fifteen turns at roughly 12 s each, the handoff costs 180 s
and loses to compaction. **My measurement cannot distinguish "one turn
suffices" from "fifteen turns needed", because the artifact that would
define sufficiency does not exist.** A per-turn cost multiplied by an
unmeasured turn count is not a handoff cost, and I decline to report
one.

So the honest verdict is narrower than the headline: **marvel's shift
machinery is not what would make a shift expensive, and the intuition is
not inverted at the control plane.** Whether a shift beats a compaction
end to end is undetermined, and it will stay undetermined until there is
a handoff artifact to measure a successor against.

## Per claim: the alternative the evidence could have excluded

| claim | status | alternative it excludes | could the evidence have come out the other way |
|---|---|---|---|
| one-role shift median 8.92 s | MEASURED, n=7 | any value outside 7.24 to 8.93 | yes; IQR 5 ms would have exposed drift |
| 7.99 s per role, linear | MEASURED, 3 role counts | superlinear or sublinear scaling | yes; a quadratic term would have put the 4-role arm off by seconds, and the residual is 0.000 s |
| shift latency is reconcile quantization, not work | MEASURED | that the work itself is slow | yes; arm B removing two ticks moved the total by exactly two ticks |
| no drain, no handoff artifact | MEASURED (code) | that either exists and I missed it | yes; a single grep hit would have refuted it |
| no readiness event | MEASURED (code + ring) | that readiness is observable | yes; the ring would have carried a kind between created and deleted |
| cache write:read = 8.77x | MEASURED, derived from 5 rows | the assumed 4x-to-10x range in finding-016's falsifier (b) | yes; the derived model reproduces all five rows to 0.15%, so a wrong ratio would not have closed |
| cold cache costs no extra wall clock | MEASURED, weak (n=2 cold) | a large latency penalty | partly; a 5 s penalty would have shown, a 300 ms one would not |
| composed handoff about 21 s | INFERRED | nothing | no; this is arithmetic over two separately measured stages |
| a shift beats compaction end to end | UNDETERMINED | nothing | no; the turn count is unmeasurable without a handoff artifact |

## What this does not establish

- **No real harness was shifted through marvel.** The credential
  constraint is recorded above. The claude adapter's readiness gate in
  particular is untested: with no healthcheck it is pane-Running, which
  fires about 100 ms after spawn and long before Claude Code can do
  anything. Whether a heartbeat healthcheck on the statusline
  side-channel (finding-011) gates on genuine harness readiness is
  plausible and unverified.
- **One replica per role.** `shiftDrain` deletes one old session per
  tick, so a role with N replicas should add N-1 ticks. Not measured.
- **Simulator readiness is not harness readiness.** Arms A/C/E gate on a
  process that heartbeats in about a second. A real harness does not.
  This is why those arms are reported as the control-plane floor.
- **One machine, one daemon, an idle-ish fleet.** Twenty simulators
  belonging to another agent were running throughout. Reconcile-bound
  timings should be insensitive to that, and the sub-10 ms IQR suggests
  they were, but I did not vary load deliberately.
- **The 2 s reconcile interval is a default.** Every number here scales
  with it.

## Consequences to carry

1. **The latency argument for shifting survives, but it was never the
   interesting argument.** finding-016 already concluded that the case
   for firing early is knowledge fidelity rather than cost. This finding
   removes the remaining reason to argue it on latency: at the control
   plane a shift is cheap, so latency does not discriminate between the
   policies, and a policy debate conducted in seconds is measuring the
   reconcile interval.
2. **The handoff artifact is now the blocking measurement, not a design
   nicety.** Until it exists, no one can measure whether a shift is
   cheaper than a compaction end to end, because there is no definition
   of when the successor has caught up. This is the load-bearing item.
3. **Marvel should emit a readiness event.** It is the instant the
   control plane commits to the successor, it is the boundary of the
   only stage that will grow when real harnesses replace simulators,
   and today it is unobservable.
4. **Shift latency is a knob.** 88% of a one-role shift is waiting for
   the ticker. If a shift is ever on a critical path, a shift-aware
   fast path is the fix, and it changes no semantics.
5. **A cold successor's cost is a documented 8.8x on the re-warmed
   portion, and zero on latency.** Both halves matter: the row belongs
   in the resource matrix priced in dollars, and it should not be
   argued in seconds.
6. **Cold-cache determination needs three fields, not one.** Recorded
   above; the both-zero row is undetermined.

## Method note

The rig ran under an isolated HOME with `MARVEL_TMUX_SOCKET` set to a
name verified absent beforehand, and an explicit short `MARVEL_SOCKET`
under `/tmp` (the scratchpad prefix alone is 109 bytes against
`sun_path`'s 104, per finding-013). Teardown was verified by three
negative checks: no process referencing my socket, none in my four
workspaces, none from my worktree's binaries. The twenty simulators
still running afterward were confirmed to belong to another agent by
their workspace and socket arguments and were left alone.

One incident worth recording because it is a hazard of the shared
scratchpad rather than of marvel: a sibling agent overwrote a
generically named `env.sh` in the shared scratchpad mid-probe with its
own HOME and tmux socket. Any of my scripts sourcing it after that point
would have driven another agent's fleet. Nothing had run in the window,
and every rig file was renamed to a per-agent prefix afterwards.
Shared-scratchpad filenames need an agent-unique prefix.
