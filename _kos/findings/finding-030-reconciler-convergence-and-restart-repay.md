# finding-030 — the reconciler converges; a converged team is not a working team, and cxdf's premise is narrower than filed

Probe: recon tasks [D] and [B], commissioned in
[ArcavenAE/aae-orc#244](https://github.com/ArcavenAE/aae-orc/issues/244) and
[#245](https://github.com/ArcavenAE/aae-orc/issues/245). Run back-to-back in one
continuous session by operator ruling, so the live team was never unwatched.
Operator: Claude (Opus 5), no human at the keyboard.
Date: 2026-08-25 (16:29–16:43Z). **Spend: $0.06.**

Binary: **`marvel 0.1.0-alpha.20260823.211648.2f76ccf`** (brew, notarized).

**Measurement only.** No code changed, no fix proposed.

Related: finding-027 (truth table), finding-028 (event-ring inventory),
finding-029 (mrvl first-use), ArcavenAE/marvel#201, #202, #203.

---

## 1. Headline

**The reconcile loop is sound, and that is not the good news it sounds like.**

Desired-vs-actual converged on every sample. It recovered from a killed replica
in ~62 seconds. It never misreported during the gap. It adopted live panes
across a daemon restart without touching them.

**And at the moment of its most perfect convergence, three of three agents were
frozen on a modal dialog, doing nothing, forever.**

The second half: **cxdf's premise — daemon restart respawns a team and re-pays
for all of it — does not reproduce on the path it describes.** It is real, but
narrower, and the discriminator is a manifest field rather than the restart.

## 2. [D] Convergence, and the divergence that is not a convergence failure

Team: 3 interactive claude agents (`alpha` ×2, `beta` ×1), `haiku`,
`acceptEdits`, trivial filler workload. Interactive on purpose so the reconciler
was watching long-lived sessions rather than one-shots.

### 2a. Perfect convergence over a completely non-working team

At T+10s:

```
DESIRED                     ACTUAL (marvel)                    GROUND TRUTH (tmux)
alpha replicas=2            crew-alpha-g1-0  running  unknown   %1 crew-alpha-g1-0
beta  replicas=1            crew-alpha-g1-1  running  unknown   %2 crew-alpha-g1-1
                            crew-beta-g1-0   running  unknown   %3 crew-beta-g1-0
```

Desired 3 = actual 3 = panes 3. Every reconciliation invariant satisfied.

`marvel capture` on each of the three:

```
crew-alpha-g1-0  *** BLOCKED on consent dialog ***
crew-alpha-g1-1  *** BLOCKED on consent dialog ***
crew-beta-g1-0   *** BLOCKED on consent dialog ***
```

The event ring for that window, in full: three `session.created` lines. No
warning, no `agent.*`, nothing. Spend: **$0.00**.

**finding-027's cell 2, reproduced at team scale.** The single-session case was
already known; what this adds is that the *reconciler* is satisfied by it. A
converged team is not a working team, and marvel has no signal that distinguishes
them. For an unattended launch this is the worst available combination: a green
board over a team that will never do anything.

### 2b. Convergence proper, once the agents could work

Cleared the three gates with `inject "" -e` (the marvel#202 two-invocation
workaround), gave them the filler task; all three wrote their notes. Then:

```
16:33:46Z desired=3 marvel_actual=3 ground_truth=3 converged=YES
16:34:04Z desired=3 marvel_actual=3 ground_truth=3 converged=YES
16:34:22Z desired=3 marvel_actual=3 ground_truth=3 converged=YES
16:34:40Z desired=3 marvel_actual=3 ground_truth=3 converged=YES
16:34:58Z desired=3 marvel_actual=3 ground_truth=3 converged=YES
```

Steady. No oscillation, no thrash, no stale-read chasing.

### 2c. Perturbation: one replica of a two-replica role killed from outside

```
16:35:28Z  kill -9 crew-alpha-g1-1 (PID 53651)
16:35:29Z  session.crashed  pane %2 gone           <- detected in 1s
16:35:40Z  desired=3 running=2 ground_truth=2  (agree)
16:35:52Z  desired=3 running=2 ground_truth=2  (agree)
16:36:04Z  desired=3 running=2 ground_truth=2  (agree)
16:36:16Z  desired=3 running=2 ground_truth=2  (agree)
16:36:28Z  desired=3 running=2 ground_truth=2  (agree)
16:36:31Z  session.created  pane %4               <- replacement, +62s
16:36:40Z  desired=3 running=3 ground_truth=3  (agree)
```

**Recovery in ~62s, honest reporting throughout the gap.** Marvel said 2 while 2
were alive, for the entire backoff window, rather than optimistically reporting
its desired count. That matches the documented 60s first crash charge. The
reconciler is doing its job and doing it accurately.

### 2d. An asymmetry worth recording: the blocked-pane hazard is first-launch-only

The replacement came up **clean**:

```
❯                                             <- live prompt, no dialog
📁ws · Haiku 4.5 · 💵0.00 · ⏵⏵ accept edits on
```

Because the earlier injects granted directory trust, which persists in
`~/.claude.json`. So cell 2 bites **once per directory**, at first launch, and
every later spawn in that workspace — including reconciler-driven replacements —
comes up working.

Both directions matter. An established workspace does not re-hit the wall on
every respawn, which is why the campaign's earlier runs saw it once and not
repeatedly. And the failure is worst exactly where it is least visible: first
launch of a new workspace, unattended, when a green `get sessions` is most likely
to be believed.

## 3. [B] The restart re-pay test — cxdf does not reproduce as filed

Positive control first, per the brief: 3 live, ground truth agreeing, desired
state matching, baseline spend `$0.04 / $0.00 / $0.02`, daemon PID 53620. The
team was genuinely running and being reconciled before anything was restarted,
so a null result means "no re-pay" rather than "nothing was there."

### 3a. Restart with panes alive: adoption, not respawn, not re-pay

```
16:38:43Z  marvel stop            -> "daemon detaching, agents keep running"
16:38:46Z  daemon 53620 exited; 3 panes still alive
16:38:58Z  daemon restarted on the SAME state bolt
```

```
16:38:58  reconcile.adopted  conv/crew-alpha-g1-0  adopted pane %1 [by pid=56865 ...]
16:38:58  reconcile.adopted  conv/crew-alpha-g1-1  adopted pane %4 [by pid=56865 ...]
16:38:58  reconcile.adopted  conv/crew-beta-g1-0   adopted pane %3 [by pid=56865 ...]
```

Verified three independent ways that nothing was re-run:

| check | pre-restart | post-restart |
|---|---|---|
| pane IDs | %1, %4, %3 | **%1, %4, %3** |
| PIDs | 53639, 55932, 53663 | **53639, 55932, 53663** |
| in-pane spend | $0.04 / $0.00 / $0.02 | **$0.04 / $0.00 / $0.02** |

Same panes, same processes, same spend. **`reconcile.adopted` is doing exactly
what `docs`/CLAUDE.md claim: agents survive the daemon.** No re-pay is possible
on this path because nothing restarts.

### 3b. Restart with panes gone: whole-team respawn, still no re-pay

The case cxdf actually describes. Detached the daemon, then killed the entire
tmux server so no pane survived, then restarted with desired=3 and actual=0:

```
16:39:48  session.crashed  conv/crew-alpha-g1-0  pane %1 gone
16:39:48  session.crashed  conv/crew-alpha-g1-1  pane %4 gone
16:39:48  session.crashed  conv/crew-beta-g1-0   pane %3 gone
```

Then the respawn wave:

```
16:40:31Z spawns=0 running=0
16:40:51Z spawns=1 running=1
16:41:12Z spawns=1 running=1
16:41:32Z spawns=1 running=1
16:41:52Z spawns=3 running=3     <- whole team back, ~2 min after restart
16:42:12Z spawns=3 running=3
16:42:32Z spawns=3 running=3
```

**The team did respawn.** And spend across all three:

```
crew-alpha-g1-0  💵0.00
crew-alpha-g1-1  💵0.00
crew-beta-g1-0   💵0.00
```

**Zero. Not re-paid — reset.** Fresh sessions, each idle at an empty prompt.

### 3c. The discriminator, and it is a manifest field

`marvel capture` on each respawned agent: idle at `❯`, no dialog, no work.

`grep -c prompt /tmp/mv-conv/team.toml` → **0**. The interactive roles declare no
prompt, and `api.Runtime.Prompt`'s own doc comment says why
(`internal/api/types.go:112`):

> *"Prompt is the request a headless launch carries. Required in headless mode: a
> harness given no prompt reads stdin, and stdin in a detached pane is a tty
> nobody types into."*

So the re-pay condition is not "a restart respawned the team." It is **"a
respawned session carries declared work."** Which splits cleanly:

| role kind | manifest carries work? | respawn outcome | cost per cycle |
|---|---|---|---|
| **headless** (`mode = "headless"`, `prompt = "..."`) | yes, by requirement | re-executes the prompt | **full re-pay** ($0.1377 measured, finding-027) |
| **interactive** (no prompt field) | no | idle agent at a prompt | **$0.00** |

finding-027 measured the headless case and got full re-pay. This run measured
the interactive case and got zero. Both are correct; they are different roles.

### 3d. What this means for cxdf

**The blast radius is not team-size × per-cycle cost across the board.** It is
team-size × per-cycle cost **for headless roles only**, and a headless role is by
construction a one-shot — which is finding-027's bug, now multiplied by replica
count rather than being a new defect.

Restated:

- **cxdf's mechanism is real** but it is finding-027's re-pay loop with a larger
  N, not an independent failure. A restart is one of several triggers; the
  underlying condition is a satisfied one-shot role that can never terminate.
- **An interactive team is not exposed to it.** A daemon restart is cheap:
  adoption if panes live, idle respawn if not.
- **The dangerous manifest is a headless role with `replicas > 1`.** Each
  replica re-executes its declared prompt on every respawn cycle, indefinitely,
  at the 5-minute backoff ceiling. For the measured $0.1377/cycle floor, a
  3-replica headless role is ~$0.41/cycle → ~$5/hour → **~$119/day**, and real
  prompts cost more than seven tokens.

I did not run that configuration. The arithmetic is finding-027's measured
per-cycle cost times a replica count, labelled as arithmetic rather than
measurement.

## 4. A measurement error of my own

My first convergence sample reported `ground_truth=0` for four consecutive
samples while marvel said 3 — which read as a spectacular divergence and would
have been the run's headline.

**It was my bug.** `MARVEL_TMUX_SOCKET` was not exported into the subshell's
`tmux` invocation, so I was querying tmux's default server, which holds no marvel
panes. Raw `tmux -L mv-conv list-panes -a` showed all three alive.

Recorded because the wrong reading was the one that flattered the hypothesis, and
I nearly reported it. The corrected samples are in §2b. General hazard for this
class of measurement: **when ground truth disagrees with the system under test,
check the instrument before believing the result** — particularly when the result
is the one you were hoping for.

## 5. Spend

| phase | amount |
|---|---|
| launch + 3 blocked agents | $0.00 |
| filler workload, 3 agents | $0.06 |
| daemon restart (adoption) | $0.00 — unchanged |
| daemon restart (whole-team respawn) | $0.00 — reset to fresh idle |
| **total, [D] + [B]** | **$0.06** |

Under the $0.30–$0.80 estimate because the filler task was one sentence each.
Campaign meter ~$1.02 of $500.

**All spend readings came off pane statuslines, not from marvel.** Interactive
sessions emitted **zero** `agent.*` events all run — no `context_feed`, no stream
to parse — so the control plane could not see any of it. Confirms finding-028 §5
from the operational side: the entire divergence log and cost accounting for both
tasks came from `capture` and `tmux`.

## 6. Controls

- **External wall-clock kill-switch** armed before launch, in a detached
  subshell, covering both tasks. Never needed to fire; disarmed by hand after
  teardown so it could not act against a later daemon.
- **Positive control before the restart**, per #245: verified live, reconciled,
  and spending before touching anything, so a null re-pay result is meaningful.
- **Ground truth from outside marvel throughout** — `tmux list-panes`, `ps`,
  `marvel capture`, pane statuslines — on a timer, never once.
- **Adoption verified three ways** (pane IDs, PIDs, spend) rather than trusting
  the `reconcile.adopted` event alone.
- No sandbox key on this host; live billed Bedrock path, so the money bound was
  external and the workload deliberately trivial.

## 7. Teardown

`marvel stop --teardown`, killswitch subshell killed, tmux socket file removed
(**7th reproduction of marvel#203 item 6** — teardown reports success and leaves
the socket file), scratch directory removed. No daemons, no marvel tmux servers.

## 8. Out of scope

No fix, no patch. §3d's blast-radius arithmetic is explicitly arithmetic over
finding-027's measured per-cycle cost, not a new measurement — the headless
`replicas > 1` configuration was not run. Per the #242 ruling, no run-registry
line is owed until critic E1 ships.
