# tmux harness-state watchdog: from liveness to classified state

- **Status:** idea (pre-hypothesis, no commitment). A core expansion of
  marvel health-and-state monitoring.
- **Date:** 2026-09-16
- **Origin:** commission via director, three rounds of party. Motivated by a
  fleet-wide "not logged in" failure: multiple sessions sat at a login
  prompt, all reading `healthy`, all doing nothing, until a human looked.
  This watchdog is the signal that would have caught it automatically.
- **Subject:** marvel (health-and-state monitoring, the tmux substrate, the
  runtime adapters). Filed in the marvel graph per the subject test.
- **Related:** [[health-signal-taxonomy]] (the signals inventory this builds
  on), [[token-rate-column-and-configurable-columns]] (its decaying rate is
  this watchdog's cheapest trigger; consume it, do not recompute it),
  [[bound-context-instead-of-measuring-it]] (the "event, not level" instinct
  applies to state too). aae-orc-9box + finding-025 (HEALTH is liveness; a
  login prompt reads healthy: the exact gap). aae-orc-dc1j (scrape rendered
  indicators, an adjacent scraping question).

## The problem, in one sentence

Marvel can tell that a session is quiet, but not *why*. HEALTH answers only
"is the process alive and does its pane exist" (finding-025): a harness at a
login prompt is a live process in a live pane, so health calls it healthy
while it does no work. The `(stalled)` activity advisory catches "no
activity" but cannot distinguish logged-out from crashed from waiting-on-you
from thinking-hard. This watchdog turns the one bit "quiet" into a
classified state.

## The state taxonomy (anchored to operator response)

The enumeration is part of the work, and the discipline is: a state earns a
place only if an operator would respond to it differently. States that share
a response are one state.

| State | Signal source | Operator response |
|---|---|---|
| **working** | tokens flowing / rate > 0 | nothing |
| **idle / done** | clean finish, holding slot (ADR-010 `succeeded`) | nothing, or reap on policy |
| **waiting-approval** | pane: a permission prompt | unblock (operator/director) |
| **waiting-input** | pane: a question to a human | answer (operator/director) |
| **logged-out / unauthenticated** | pane: a login prompt | supply credentials |
| **rate-limited / quota** | event/error kind; pane | back off, do NOT restart into it |
| **error-transient** | error event mid-turn | usually self-heals |
| **crashed** | `pane_dead` / process gone (already a state) | replace on policy |
| **stuck / hung** | alive + pane alive + no output + no tokens + no recognizable prompt | the residual; investigate |

Two states pay the rent: **logged-out** and **waiting-approval/input**.
Those are where a machine noticing minutes earlier than a human saves an
overnight run, and both are invisible today.

## The two-tier signal model (the design spine)

Most states are readable from **cheap** signals with no scraping:

- crashed = `pane_dead` / `pane_dead_status`;
- rate-limited = often an error event/kind;
- working = rate > 0 (the token-rate EMA from the sibling idea).

Only the ambiguous middle needs pane **text**: logged-out,
waiting-approval, waiting-input, stuck. That is the expensive tier, and it
runs only when the cheap tier says "this one is quiet."

**The gate:** do NOT scrape every pane every tick. Scrape only when cheap
indicators fire together: no tokens in/out over a window, CTX flat (the
decayed rate near zero, from [[token-rate-column-and-configurable-columns]]),
process alive, pane alive. Cheap signals say "quiet"; only then does the
pane-scrape answer "quiet *how*."

### Why the gate exists (correcting "without burning tokens")

`tmux capture-pane` burns **zero model tokens**; it reads the terminal, it
never asks the agent anything. So token cost is not the reason to gate. The
gate exists for:

1. **Load** — do not pattern-match hundreds of panes every tick.
2. **False positives** — a busy healthy session's output is full of strings
   that look like errors; keep the classifier away from sessions that are
   visibly fine.

The scrape is token-free. The *classifier* is the cost, in CPU and in wrong
answers. This must be in writing before someone "optimizes" by scraping
constantly.

## The detection mechanism, and how it rots

Home: `internal/runtime/generic.go` already has a `GenericScraper`
(capture-pane history scrape) with no production call site yet. This is its
call site.

Pipeline, once the gate fires:

1. capture the **last N lines** (the tail is the signal: what is on screen
   *now*, not the scrollback);
2. **normalize** (strip ANSI, collapse whitespace) before matching, because
   a login prompt wrapped in color codes is a different byte string than the
   same prompt plain, and that difference is where a regex silently stops
   matching;
3. match against a **per-adapter** pattern set (claude/codex/opencode each
   know their harness; the generic fallback gets a weak, conservative set);
4. emit a **classified state with a confidence and the matched evidence**;
5. surface it; never auto-destroy (see boundary below).

### Brittleness defenses (patterns rot, per harness, per version)

Learned patterns rot: a harness release changes its login prompt and a
regex that matched yesterday silently misclassifies today. Three defenses:

- **Patterns are data, not compiled-in regexes**, so a correction ships
  without a release.
- **Every classification carries confidence + raw evidence.** Low
  confidence is `unknown`, not a guess. `unknown` is a fine answer; a
  confident wrong answer is the injury.
- **A "does this still match the current harness" test per pattern set.**
  The day a harness changes its login prompt, a test goes red *here*, before
  the operator's pager does. This is the line between a watchdog and a
  liability.

Classifier failure mode: capture-pane error or garbage content yields
`unknown`, never a state. A session the gate never fires on stays `unknown`,
not `working` by omission. Absence of a verdict is an honest value, the same
lesson as CTX%'s `-`.

## The boundary: classify and surface, do not judge or destroy

The watchdog must NOT auto-restart anything off a scrape. SOUL section 8 and
`.claude/rules/diagnostic-not-gate.md`: automate checking and surfacing, not
judging. The existing `(stalled)` advisory is deliberately restart-neutral;
a pane-scrape classification is *softer* evidence than a staleness timer,
not harder. It informs; it does not fire the destructive path. This also
matches the daemon's ratified "err on accumulation rather than destruction"
posture.

The tension is real and resolved: the incident wanted *action*, but the fix
is the state becoming **visible**, not the machine taking the wheel.
Auto-killing a logged-out session does not log it back in; it crash-loops
faster and burns the restart budget. The value is surfacing.

Output rides surfaces that already exist:

- `SessionContext` carries the classified activity-state, the same way
  `(stalled)` rides today;
- `get sessions` shows it (logged-out is a red cell, impossible to miss);
- `marvel events` emits it with evidence (e.g. `agent.state.classified`);
- `describe session` carries the matched lines and the confidence.

## High-level plan (the three rounds' output)

- **Phase 1 (the starter bead, filed to bd today):** the full pipeline for
  *one* state, **logged-out**, on the claude adapter. Cheap gate → capture
  last N lines → normalize → per-adapter match → classified state on
  `SessionContext` → red cell + event + `describe` evidence. Advisory only.
  Chosen because it would have caught the motivating incident, and because
  running one state through the whole path proves the architecture; the rest
  are more pattern data, not more plumbing.
- **Phase 2:** widen the taxonomy (waiting-approval, waiting-input,
  rate-limited, stuck) and extend patterns to the codex and opencode
  adapters. Each new state is pattern data plus a test, not new plumbing.
- **Phase 3:** the confidence-to-action policy. If an operator opts a role
  in, a high-confidence terminal verdict (crashed, or logged-out persisting
  past a threshold) *may* escalate to a notice or to director, and only by
  explicit policy ever toward a restart. Default stays advisory.

Standing rule across all phases: patterns are versioned data with tests,
and every set has a currency check against the live harness.

## Open questions

- Where does the gate live in the reconcile loop, and at what cadence
  relative to the healthcheck tick?
- Is the classified state a new field on `SessionContext`, or a richer
  `ActivityState` enum extending the current stalled/active axis?
- How many tail lines is N, and does it vary per adapter?
- Confidence: a scalar with a threshold, or a small ordered enum
  (`confident` / `weak` / `unknown`)?
- Does `stuck` (the residual) need a distinct pattern, or is it simply
  "gate fired, nothing matched, still quiet"?
- Interaction with the auth boundary: a "logged-out" verdict must not tempt
  any credential-handling code path (SOUL section 3 / ADR-009); the watchdog
  surfaces the state and stops there.

## Crystallization signal

Already crystallizing: a real fleet-wide incident this would have caught.
When Phase 1 is committed, extract a probe brief (the logged-out pipeline on
the claude adapter behind the cheap gate, with a pattern-currency test) and
a frontier question for the taxonomy and confidence model.
