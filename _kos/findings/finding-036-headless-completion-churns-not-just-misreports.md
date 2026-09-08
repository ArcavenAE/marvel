# finding-036: the headless-completion ticket and its probe brief still say "diagnostic" three weeks after finding-027 measured the spend

Date: 2026-09-08
Probe: `probe-headless-terminal-state.md` (SP5, partial SP1)
Serves: `aae-orc-vsju`; blocks-of-record `aae-orc-bxeh`
Measured on: mokuzai, macOS arm64, tmux 3.7c, marvel `d0c9f85`

## What this finding is, and what it is not

It is NOT the discovery that a finished headless role is re-executed and
re-billed. **finding-027 established that on 2026-08-24** and
`question-session-state-observation` carries it in the graph: the one-shot cell
reads `crashed / exited 0 (end_turn) / DONE / RE-PAYS`, the respawn floor was
measured with `/usr/bin/true` at ~12 respawns/hour indefinitely, and the spend
floor at $0.1377/cycle → $39.66/day for a seven-token prompt.

This session re-derived that result from scratch before finding the node. That
is the finding.

**The artifacts a worker is told to read still carry the superseded claim.**
`aae-orc-bxeh` says "THE CONSEQUENCE IS A DIAGNOSTIC ONE." finding-015 says
"The established cost is diagnostic." `probe-headless-terminal-state.md` — the
brief `aae-orc-vsju` exists to execute — inherits it, and frames SP3 around
avoiding a respawn-forever loop as a HAZARD OF THE PROPOSED FIX. It is not a
hazard of the fix; it is the current shipped behaviour, and finding-027 priced
it three weeks before the brief was written.

A worker who does what `vsju` says — read the brief, run the sub-probes — is
designing against a premise the graph had already corrected. Orienting from the
ticket and the brief, as instructed, routed around the node that had the answer.
This is a concrete instance of charter F19/F25 (props existed; no backdrop
pulled them into the path of the work), and of finding-046's shape: the
diagnosed failure recapitulated by the diagnosing session.

## Independently confirmed on d0c9f85

The churn reproduces on current code, isolated daemon, one role,
`sh -c 'echo ok; exit 0'`, `mode = "headless"`, no `restart_policy` declared
(so `always`, exactly as `mixed-adapters.toml` leaves it):

```
19:28:23  info     session.created           pane %1
19:28:24  warning  session.crashed           pane %1 gone
19:28:24  warning  health.crashloop-backoff  restart #1, backoff until 19:29:24Z
19:29:26  info     session.created           pane %2      <- ran again
19:29:28  warning  session.crashed           pane %2 gone
19:29:28  warning  health.crashloop-backoff  restart #2, backoff until 19:31:28Z
19:31:30  info     session.created           pane %3      <- and again
19:31:32  warning  session.crashed           pane %3 gone
19:31:32  warning  health.crashloop-backoff  restart #3, backoff until 19:35:32Z
```

Three re-executions of finished work in three minutes, window doubling
60s → 120s → 240s toward the 5m ceiling (`restartBackoffMax`,
`controller.go:101`). Corroborates finding-027's floor on a second host, a
second tmux version, and two weeks of intervening commits.

## SP5: answered, and its premise is void

SP5 asked whether the reap path reaches the reconciler at all, because if not,
"SP3's mapping is being designed against a path that does not run."

**It runs.** `ReconcileOnce` → `reapDeadLocked` (`controller.go:458`) →
`noteReapedCrash` → `noteCrashAndBackoff`; `planRole` then sees
`actual(0) < desired(1)`, waits out `BackoffUntil`, and spawns. Every link is
exercised in the trace above.

finding-015's unexplained non-respawn — three roles `crashed` for ~4 minutes
with no replacement — **does not reproduce**. It is not a property of the
reap→reconcile wiring. finding-015's own provenance records that the daemon was
stopped by the operator before follow-up, which is the cheapest explanation and
needs no defect to carry it. Recommend closing that thread as
environment-specific rather than routing it to `aae-orc-4bz2`, unless 4bz2's
own work turns up a suppression path.

## Two premises in the brief that do not hold

1. **"the first backoff is 30s."** It is 60s. `noteCrashAndBackoff` calls
   `computeBackoff(rh.RestartCount + 1)`, so the first charge computes
   `computeBackoff(2)` = `30s << 1`. The 30s constant is never the first
   window. Does not change SP5's conclusion (4 min > 60s either way), but it is
   the number the brief reasons from.

2. **Consequence 3 — `new-window -t <session>` fails with "index 1 in use"
   when a dead window is current — did not reproduce on tmux 3.7c.** Tested
   with the exact `NewPane` argument vector (`new-window -t <session> -d -P -F
   '#{pane_id}' -n <title> <cmd>`, `driver.go:246`), `base-index 0`,
   `renumber-windows off`, `remain-on-exit on`, with a dead window present and
   again with the dead window *current*: four consecutive spawns, all rc=0,
   tmux allocating the next free index each time.

   This is the most dangerous of the brief's four costs — it takes down
   spawning for the whole tmux session — so it should not be carried forward as
   established. The pre-probe measurement was tmux 3.7b on kinu; this is 3.7c.
   Either the behaviour changed across the patch release or the original
   measurement had an unstated precondition. **This raises the value of SP1
   (version floor):** a cost that varies across a patch release is exactly what
   a documented floor exists to pin down.

## What replicated cleanly

The dead-pane status table, re-measured on 3.7c (brief measured 3.7b):

| case | `pane_dead` | `pane_dead_status` | `pane_dead_signal` |
|---|---|---|---|
| `exit 0` | 1 | `0` | |
| `exit 3` | 1 | `3` | |
| `sh -c 'exit 7' > /dev/null < /dev/null` | 1 | `7` | |
| SIGKILL | 1 | (empty) | `kill` |

The redirection row is the launch form marvel uses. Exit status is available
from tmux with no shim, on two tmux versions now. That half of the brief stands.

Source premises re-verified on `d0c9f85` (line numbers had drifted a month):
`SessionSucceeded` is still written nowhere in production code (`types.go:16`,
plus one test fixture at `budget_test.go:185`); `RuntimeModeHeadless` is
populated and in reach (`types.go:141`); `NewPane` still sets `remain-on-exit
off` (`driver.go:271`); the default policy is still `RestartAlways`
(`manifest.go:549`). `MaxRestarts` defaults to 0 and **zero means unlimited**
(`types.go:373`), so the default path never freezes.

## CORRECTION 2026-09-08 (same day, after the operator pressed on it)

**The recommendation in the next section is wrong and is superseded by
ADR-010.** It is left in place rather than edited, because the error is
instructive and the reasoning that produced it is the thing to avoid.

What it says: the restart-policy default is "separable from and more urgent
than the exit-status plumbing," and `mode = "headless"` "wants a restart-policy
default that is not `always`, and that stands on its own merit BEFORE any
`remain-on-exit` work lands."

Why that is wrong, established by reading the control flow rather than the
tickets:

1. **Marvel has exactly one mechanism for "stop refilling this slot":
   `freezeRole`,** which parks `BackoffUntil` at a year-9999 sentinel. There is
   no notion of a replica satisfied by completed work. Restart policy never
   changes `desired`; it only decides whether and how fast to refill. So
   "default to `never`" stops the churn only by triggering that freeze — and the
   freeze is **per-role**, thawed only by `marvel reset-health` or
   delete-and-re-apply. **A shift does not clear it** (verified). A completed
   role strands its remaining replicas and reads `failed`.
2. **`on-failure` is not merely unavailable for lack of an exit code — it is
   not wired into the reap path at all.** `applyRestartPolicy`, which honours
   never/on-failure/always, is reached only from the health path. The reap path
   (`noteReapedCrash`) special-cases `never` alone, and a completed one-shot
   goes down the reap path.
3. **Exit status is necessary but NOT sufficient**, which is the specific thing
   this finding got wrong by calling it "the proper fix, just bigger." It does
   not change the desired-vs-actual arithmetic. Worse: the `never` branch of
   `applyRestartPolicy` records that without a freeze, a terminal state that
   drops out of `CountsAsAlive` makes the reconciler "replace the session every
   tick, uncapped and with no backoff — one live pane leaked per cycle"
   (marvel#107, `aae-orc-pyre`). Writing `succeeded` naively converts a
   5-minute churn into a per-tick pane leak.

The real decision was the one this finding listed third and treated as the slow
option: **is `replicas` on a headless role a concurrency target or a completion
target?** The operator ruled COMPLETION on 2026-09-08 (ADR-010). Underneath it,
`CountsAsAlive` was conflating "occupies a replica slot" with "is a running
process"; a completed job needs yes to the first and no to the second.
`api.OccupiesReplicaSlot` / `CountReplicaSlots` now separate them.

The method lesson, which is this finding's own subject pointed at itself: the
section below reasoned from ticket and brief prose about what restart policy
"means" instead of following `planRole`'s arithmetic to the one predicate that
governs it. That is the same failure the finding documents one layer up.

## What this does to SP3 and SP4

SP3's framing needs inverting. The respawn-forever behaviour is not a hazard to
be avoided when introducing `succeeded` — it is what ships today, priced by
finding-027. So the policy default is separable from and more urgent than the
exit-status plumbing: `mode = "headless"` wants a restart-policy default that
is not `always`, and that stands on its own merit BEFORE any `remain-on-exit`
work lands. The `succeeded`/`failed` mapping then decides how the terminal row
READS, which is the diagnostic half the ticket described.

`question-session-state-observation` is ahead of both the ticket and the brief
here, and its OPEN list already names the gating decision: whether `replicas` on
a headless role is a concurrency target or a completion target
(Deployment vs Job semantics). That is SP4's question, better posed, and it is
a resource-model decision for the operator per ADR-007.

## Not established

- **SP1 is not done.** Docker is installed on this host but its daemon is not
  running and no other Linux is available here, so no Linux measurement and no
  version floor. Given consequence 3 diverged across a tmux patch release, SP1
  is the highest-value remaining sub-probe.
- **SP2 is not done.** Exercising the four consequences under the daemon
  requires the `remain-on-exit` change to exist; that is `bxeh`'s code.
- Whether the 3.7b consequence-3 failure is real and version-specific or was a
  measurement artifact. Only the 3.7c non-reproduction is measured here.
- Nothing about interactive roles, and nothing about cell 2 (blocked sessions),
  which is `question-session-state-observation`'s harder half.

## Reproduction

`oneshot.toml` + isolated daemon (`--socket`, `--state-bolt`, `--log-file`,
`--pidfile`, `MARVEL_TMUX_SOCKET=sp5probe`). One gotcha worth recording: a Unix
socket path under a long scratch directory fails to bind with `connect: invalid
argument` — macOS caps `sun_path` near 104 bytes. Keep the daemon socket short.
