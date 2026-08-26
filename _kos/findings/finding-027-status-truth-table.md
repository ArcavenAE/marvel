# finding-027 — status truth table: what marvel reports vs what is true

Probe: recon measurement commissioned in
[ArcavenAE/aae-orc#239](https://github.com/ArcavenAE/aae-orc/issues/239#issuecomment-5389440000)
(the truth-table brief). Operator: Claude (Opus 5), no human at the keyboard.
Date: 2026-08-24 (00:27–00:49Z). Binary: tree build at `0c59baa` (`marvel dev`),
chosen so results are directly comparable to run-01's evidence.
Machine: MacBookPro17,1, macOS 26.3, tmux 3.6a, claude 2.1.241 on Bedrock.

**This is a measurement, not a fix.** No code changed, no PR opened. Scope was
one row per cell, per-cell evidence, no blurring of cases, and deliberately no
conclusion that names a fix. The one structural claim below (§5) is the
requirements statement the brief asked for, not a proposed patch.

Related: orc `docs/runs/marvel-run-01/run-01.md` and `run-01b.md`;
ArcavenAE/marvel#201, #202, #203.

---

## 1. The question

marvel infers a session's state from whether its process is alive, not from what
the agent did. run-01 (a finished agent reported `crashed`, respawned, re-paid)
and run-01b (a stuck agent reported `running`) looked like two defects. The
hypothesis under test: they are one defect in two coats.

Four columns per cell: reported state and the signal it came from; real process
state; real work state; did spend continue.

## 2. The truth table

| | **Cell 3 — process dies** (control) | **Cell 1 — one-shot completes** | **Cell 2 — blocked on modal dialog** |
|---|---|---|---|
| **reported state** | `failed` / `unhealthy` | `crashed` / `unhealthy` | **`running`** / `unknown` |
| **signal inferred from** | pane absence | **pane absence (identical)** | pane presence |
| **real process state** | dead (SIGKILL) | finished, `exit 0 (end_turn)` | alive, idle at a modal prompt, CPU 0.0% |
| **real work state** | never finished | **done** | **never started** |
| **spend continued** | n/a (no model) | **yes — full re-pay per cycle** | **no — $0.00** |
| **verdict** | **truth** | **inverted** | **inverted** |

Detection latency, cell 3: ~2s. Cell 1: reap ~2s after exit. Cell 2: never — the
state is stable indefinitely.

## 3. Cell 3 (control) — marvel reports truth

`sleep 3600`, generic adapter, `restart_policy = never`, killed `SIGKILL` from
outside marvel.

```
00:27:21  session.created  cell3/control-victim-g1-0  pane %1
00:27:39  session.crashed  cell3/control-victim-g1-0  pane %1 gone
00:27:39  session.failed   restart_policy=never, pane gone; role frozen
```

Ground truth agreed at every 4s sample for 16s: `ps` reported no such pid, tmux
listed no pane, marvel said `failed`/`unhealthy`.

**The control passes, and that is what makes the finding precise.** The
inference mechanism is not broken in general. What is broken is that
pane-absence is the *only* input, so it cannot distinguish outcomes that share
it.

Blemish: the corpse's row kept reporting `RSS 1.1M` for the full 16s with no
pane and no process. Reproduces run-01's 22:52:36 observation.

## 4. Cell 1 — a completed one-shot, measured twice

Run in two variants deliberately: an unpaid one to measure the *cadence* for
free, and a paid one to answer only the question the unpaid variant cannot.

### 4a. Unpaid variant — cadence and terminal state (cost: $0.00)

`/usr/bin/true` under the generic adapter: exits 0 immediately, the cheapest
possible "work done, process gone." Same reap-and-respawn path, no model.

Observed over an 8-minute externally-bounded window:

```
00:28:15  session.created  pane %1     <- initial
00:28:15  session.crashed  pane %1 gone
00:29:17  session.created  pane %2     <- respawn 1, +62s
00:31:21  session.created  pane %3     <- respawn 2, +124s
00:35:25  session.created  pane %4     <- respawn 3, +244s
```

Backoff from the daemon log: `restart #1 → 59.99s`, `#2 → 1m`, `#3 → 3m`,
`#4 → 4m`. **4 spawns in 8 minutes**, doubling toward the documented 5-minute
ceiling.

**`succeeded` never appears.** Grepped the whole ring across the window: zero
occurrences. The state is in the documented lifecycle (`pending → running →
succeeded/failed`, CLAUDE.md Resource Model) and nothing reaches it.

The loop does not converge. It settles at one respawn per 5-minute ceiling —
**~12/hour, ~288/day, indefinitely.**

### 4b. Paid variant — does each respawn re-pay? (cost: $0.4830)

The one question the free variant cannot answer. Real headless claude, `haiku`,
prompt `"Reply with exactly the word: ok"`. External kill-switch armed *before*
starting: a `sleep 420` in a detached subshell that runs `marvel stop
--teardown` regardless of anything marvel or my own logic does. It fired on
schedule at 00:44:22Z.

Positive control first, per the brief: confirmed the agent actually completes
and is reaped before trusting any loop result.

```
00:37:26  agent.session.ended  exit 0 (end_turn) tokens in=3 out=4 cost=$0.2076 turns=1
```

Three completions inside the window, each a *separate billed model turn*:

| cycle | tokens | cost |
|---|---|---|
| 1 | in=3 out=4 | $0.2076 |
| 2 | in=3 out=4 | $0.1377 |
| 3 | in=3 out=4 | $0.1377 |

**Answer: full re-pay, not a cached retry.** Identical token counts (3 in, 4
out) with three separate non-zero charges. Cycle 1 costs more than 2 and 3,
which is consistent with cold-cache overhead on the first turn; cycles 2 and 3
are identical, so **$0.1377 is the steady-state per-cycle cost** of this
trivial prompt.

```
runs=3   necessary=$0.2076   actual=$0.4830   waste=$0.2754   multiple=2.33x
steady-state per cycle = $0.1377
```

Projected at the 5-minute ceiling, for this 7-token prompt:

| window | cycles | cost |
|---|---|---|
| 1 hour | 12 | **$1.65** |
| 24 hours | 288 | **$39.66** |

That is the floor, not a worst case: a real task's prompt and output are far
larger than 7 tokens. run-01's real summarization task cost $0.0661–$0.1151 per
cycle at 476–1289 output tokens.

## 5. What the three cells establish together

The hypothesis holds. **Cells 1 and 3 emit the same event, from the same signal,
with opposite underlying truth:**

```
cell 3 (died):      session.crashed  "pane %1 gone"
cell 1 (succeeded): session.crashed  "pane %1 gone"
```

Nothing downstream of that event can recover the distinction, because the
distinction was discarded at the point of observation. Cell 2 is the same defect
from the other side: pane *presence* is read as work happening.

So the defect is not three bugs and not a missing state. It is **one missing
input channel**: marvel observes the pane and infers the agent. The information
it needs exists — cell 1's `agent.session.ended  exit 0 (end_turn)` lands on
marvel's own ring 2 seconds *before* the reap — but it does not reach the state
machine. Cell 2's information does not exist anywhere in marvel at all.

Stated as the requirement rather than the fix: **a session's state needs to be
derivable from what the agent reports about its own work, with pane liveness as
a fallback rather than the primary signal.** Cell 3 shows the fallback is sound.
Cells 1 and 2 show it is insufficient alone.

Corollary worth recording, because it closes off the obvious cheap answer: a
`process-alive` healthcheck cannot catch cell 2. `internal/team/controller.go:636`
— "process-alive is handled by ReapDead. Pane exists → healthy." The pane *is*
alive. A blocked agent is healthy under every healthcheck marvel currently has,
and `heartbeat` does not help either, since an interactive claude session emits
none unless `context_feed` is configured.

## 6. Method notes, including one thing I got wrong

**Cell 2 needed a second attempt, and the first attempt was a false negative I
caused.** My first cell-2 run pointed the daemon at the aae-orc root and the
dialog never fired: `~/.claude.json` showed `hasTrustDialogAccepted: true` for
that path, because **my own `marvel inject "" -e` during run-01 permanently
granted trust there.** Marvel reported `running` and the pane sat at an empty
prompt — genuinely idle, but not blocked, so the row would have been wrong.

Reproduced honestly by creating `/tmp/mv-truth/virgin/` with an `@`-import
`CLAUDE.md`, verified absent from `~/.claude.json` first. The dialog then fired
as expected (`❯ 1. Yes, I trust this folder`). Recorded because it is a general
hazard for this class of test: **first-run gates are one-shot per scope, so a
prior run can silently disarm the very thing a later run is trying to measure.**

Also of note: the virgin directory is *still* absent from `~/.claude.json`
after the run, because the dialog was never answered. So cell 2 left no trust
residue, and the cell is repeatable as-is.

**Controls that held:**

- Kill-switch external to marvel (detached subshell, wall-clock), armed before
  the paid cell started. Fired on schedule.
- Cheap-first ordering: cell 3 and cell 1a produced the cadence, terminal-state,
  and `succeeded`-never-fires results for $0.00. Only the re-pay question needed
  real spend, bounded to 3 cycles.
- `capture` on a timer, never once. Every "real process state" column is ground
  truth from `ps`/`tmux`/pane content, never marvel's own report.
- Positive control before trusting the loop result.

**No sandbox or mock key exists on this machine** (auth is
`CLAUDE_CODE_USE_BEDROCK` + `AWS_BEARER_TOKEN_BEDROCK`, a live billed path), so
the wall-clock cap was the only available money bound. Stated for the record.

## 7. Spend and wall-clock

| Cell | Wall-clock | Spend |
|---|---|---|
| 3 (control) | ~2 min | $0.00 |
| 1a (unpaid cadence) | ~9 min | $0.00 |
| 1b (paid re-pay) | ~7 min (killswitch-bounded) | **$0.4830** |
| 2 (blocked, incl. false-negative retry) | ~5 min | $0.00 |
| **total** | **~23 min** | **$0.4830** |

Within the run-01 range (~$0.48) as the brief predicted.

## 8. Incidental observations

- **Five dead tmux socket files** left behind, one per daemon, after each
  `stop --teardown` reported success (`no server running on ...` for all five).
  Fifth reproduction of marvel#203 item 6. Cleaned by hand.
- **`$0.2076` for a 3-in / 4-out-token turn** is worth a second look by someone
  who knows the Bedrock pricing path. It is the cold-cache first turn, and the
  number is large relative to the work; run-01's much larger turns cost less.
  Not this probe's question, so recorded rather than pursued.

## 9. Out of scope by construction

No fix, no PR, no severity ranking across cells, no merging one cell's story
into another. Each row above stands on its own evidence. §5 states the
requirement the table implies because the brief asked for the table to serve as
a requirements document; it deliberately stops short of naming an
implementation.
