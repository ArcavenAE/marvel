# finding-012: the daemon isolation fixes hold under two live daemons

Date: 2026-08-07
Probe: `aae-orc-3st1`
Verifies: marvel #118, #122, #123
Bears on: `aae-orc-t6da`, `aae-orc-kvcs`, `aae-orc-mh6g`,
`question-daemon-isolation-boundary`
Rig: `scripts/rig-daemon-isolation.sh`

## Why this exists

Everything shipped on 2026-08-07 had unit-test evidence and one no-daemon
CLI check. None of it had been exercised with two daemons running at
once, which is the only condition under which the original bugs appear.
The bugs were found in a rig; the fixes had not been confirmed in one.

That is the shape orc finding-002 named: development-scoped validation
produces false confidence. Three tickets were reading as effectively
closed on the strength of tests that cannot, by construction, reproduce
the failure.

## Result

Eight scenarios, all pass. The load-bearing one first.

**A second daemon no longer destroys the first's fleet.** Daemon A
running a two-replica simulator team on a shared tmux server, 3 panes and
2 live simulator processes. Daemon B started with a different HOME, its
own socket, and the SAME tmux server: the exact configuration that killed
everything in about five seconds on 2026-08-06.

```
before: 3 pane(s), 2 simulator process(es)
after:  3 pane(s), 2 simulator process(es)
```

B logged and evented what it declined to touch, naming itself:

```
AdoptOrLeave[pid=26518 socket=/tmp/mrig-b/.marvel/run/marvel.sock]:
  left tmux session marvel-alpha running: workspace not in this daemon's records

warning  reconcile.left  alpha  left tmux session marvel-alpha running:
  workspace not in this daemon's records. Reclaim with `marvel reap` if it
  is stale [by pid=26518 socket=/tmp/mrig-b/.marvel/run/marvel.sock]
```

**The socket lock refuses rather than stranding.** Two daemons, same
explicit `--socket`. B refuses with the holder named; A stays reachable
and its socket file survives B's exit, which is the stranding case that
previously left a live daemon unreachable with nothing in its log.

```
Error: another marvel daemon is already using socket /tmp/mrig-shared.sock
(lock held on /tmp/mrig-shared.sock.lock). Stop it, or start this one with
a different --socket
```

**Sockets follow HOME.** Two daemons, no `--socket` on either, each got
its own layout-derived socket. A saw its 2 sessions; B saw none of them.
No cross-talk.

**The path assertion fires on a real path, not a synthetic one.** A
layout socket under this session's scratchpad measured 138 bytes and was
refused with the number named. This is not hypothetical: the session
scratchpad prefix alone is 109 bytes against a 104-byte ceiling, so the
rig itself had to live in `/tmp`. The assertion caught its own author.

**The deliberate destructive paths still destroy.** `--reclaim` took the
fleet from 2 simulators to 0 and logged the kill with its own identity.
`marvel reap` listed one candidate and destroyed nothing; `reap --confirm`
destroyed it. The listing warns that candidates may belong to another
running daemon.

**Restart-without-agent-loss survived the policy change.** This was the
regression most likely to have been caused by flipping the default, since
kill-on-start was the behavior adoption was built around. Agents survived
the daemon stop (2 alive), the restarted daemon adopted 2 panes, and no
agent was lost. The restart used the leave policy.

## The methodology error, recorded because it nearly banked two results

The first run used `pgrep -fc` to count simulator processes. `-c` is not
a valid `pgrep` flag on macOS, so every count returned garbage. Two
consequences, in opposite directions:

- Scenario 3's process comparison became `0 == 0`, vacuously true. It
  reported PASS on the single most important claim in this probe while
  measuring nothing. The pane count beside it was real, which is the only
  reason the result was recoverable rather than merely wrong.
- Scenario 5 reported FAIL for "agents survived daemon stop" against a
  broken counter, which would have read as a genuine regression in M3.

Both were caught by reading the raw output rather than the PASS/FAIL
tally, and re-run with `pgrep -f ... | wc -l`. The checked-in rig carries
the corrected helper and a comment naming the trap.

This is the second methodology flaw in this ticket family caught after
the fact, the first being the incomplete artifact set in the 2026-08-06
blind read. Both times the flaw made a result look stronger or weaker
than it was, and both times only re-reading the primary evidence found
it. A green tally is not a result.

## Correction, 2026-08-07: the reap scenario measured the wrong object

The claim above that "`marvel reap` listed one candidate and destroyed
nothing; `reap --confirm` destroyed it" is true as written and does not
mean what it was recorded to mean. Found while building finding-013.

That one candidate was not an orphan. It was the tmux base pane (`%0`,
running a shell) that tmux creates with every session before marvel
splits replicas into it. `UnrecordedTmuxState` builds its recorded set
from `sess.PaneID` over store sessions, and the base pane is never a
marvel session, so it is never recorded and is always reported. A single
healthy daemon on its own fleet reports "1 unrecorded item(s)", and
`reap --confirm` on a healthy fleet destroys a pane inside a live
session.

So the scenario demonstrated that reap's plumbing works, not that reap
correctly identifies orphans. The destructive half is worse than
unproven: it is wrong, and this finding's green result helped it look
right. Filed as ArcavenAE/marvel#129; it predates finding-013's change
and was reproduced against the unmodified `358d882` binary.

**Scenario 4b no longer reproduces**, also verified against unmodified
`358d882`, so it is not a regression from later work. Two causes
compounded: 4b inherited the fleet scenario 4's `reap --confirm` had
already destroyed, and the `sims()` helper counts machine-wide while
`marvel stop` is detach rather than teardown, so both readings were
earlier scenarios' surviving simulators on a tmux server `--reclaim`
never touched. Measured directly, `--reclaim` does take 2 simulators to
0 and logs the kill. The rig has been fixed to start 4b from a clean
slate with a loud precondition assertion, and to kill the prior
scenario's tmux server so counts stop carrying earlier processes.

**This is the third methodology error in this ticket family, and the
first two are in this document.** All three share one shape: a check
passed while measuring something other than what it claimed. The
`pgrep -fc` error made a comparison vacuously true; the exit-code error
in the sibling rustfmt work read `head`'s status instead of cargo's;
this one counted a live shell pane as an orphan. Orc finding-114
generalizes the lesson from a different tool: a green check does not
mean the thing you declared was the thing that got checked. It applies
to this rig as much as to rustfmt, which is why the correction is
appended here rather than filed only downstream.

The original text above is left unaltered. Findings record what was
believed when written; corrections append.

## What this does NOT establish

- Nothing about the tmux namespace being scoped. It is still
  machine-global; two daemons still share one tmux server by default.
  Survivability was verified, isolation was not. That is `aae-orc-by6j`.
- Nothing about the victim daemon's own records. Under the leave policy
  there is no victim to test, and the `--reclaim` and reap paths still
  leave the owner of the destroyed state with no record
  (`aae-orc-tt5e`).
- Nothing about real harnesses. The simulator runtime was used
  throughout, so no model tokens were spent and no claude/codex process
  behavior was exercised.
- Nothing about peer authentication. Every daemon here ran at the same
  uid, which is the condition `aae-orc-sqh0` is about.

## Rig recipe, for the next ticket

- Separate `HOME` per daemon, in a SHORT path. `sun_path` is 104 bytes
  and the session scratchpad prefix is 109, so `/tmp/mrig-a` and
  `/tmp/mrig-b` rather than the scratchpad. This is a technical
  constraint, not a preference.
- `MARVEL_TMUX_SOCKET` is a tmux socket NAME, consumed as `tmux -L`, not
  a path. Short names (`mxA`, `mxB`). A 109-byte value was refused by
  tmux with `File name too long` and left sessions pending rather than
  failing the run.
- Simulator runtime, so no model tokens are spent.
- Never against the real `~/.marvel`: its bolt still holds the
  2026-08-06 demo's desired state, and a daemon started there rehydrates
  it into live agent processes.
