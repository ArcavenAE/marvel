# finding-014: the destructive paths could not tell a base pane from an orphan

Date: 2026-08-07
Fixes: ArcavenAE/marvel#129 (PR #132)
Corrects: finding-012
Bears on: `aae-orc-2cms`, `aae-orc-wap0`, `aae-orc-5kfw`,
`question-daemon-isolation-boundary`

## The defect

`marvel reap --confirm` and `marvel daemon --reclaim` destroyed a pane
inside a live session on a **healthy** fleet.

tmux creates one base shell pane per session, from `new-session`, before
marvel adds its replica windows. That pane is not a marvel session, so it
was never in the store, so `reconcilePrefix` and `UnrecordedTmuxState`
both read it as unrecorded. A healthy single daemon on its own fleet
therefore reported exactly one reap candidate, always, and confirming
destroyed it.

Two consequences, and the second is worse than the first:

- The destructive action was wrong on healthy state.
- `reap` could never report clean, so the listing an operator is asked to
  confirm has been wrong every time anyone has seen it. The signal that
  was supposed to make the accepted failure visible was noise.

Bounded honestly: `NewPane` uses `new-window`, so replicas sit in their
own windows and the base pane is alone in window 0. Killing it closes an
empty shell window and agents survive. Only a session with no replicas
yet loses the session itself.

## The fix, and why it is not "skip pane %0"

Marvel now marks every pane it creates with the `@marvel_pane` tmux
option and considers only marked panes for anything destructive. The
property is stronger and simpler to state than the bug it fixes:

> Marvel never destroys a pane it did not create.

That also covers a case nobody had filed: an operator opening a shell by
hand inside a marvel session used to be a reap candidate.

Three shapes were available and two are worse:

- **Exclude `%0`** encodes a tmux numbering assumption. `base-index` is
  configurable, so the number is not a fact.
- **Record the base pane in the store** puts a fact that must outlive the
  daemon into a store the daemon rebuilds from disk at startup.
- **Mark the pane in tmux** puts the fact in the thing that outlives the
  daemon. A restarted daemon reconciles against panes it did not create
  in this process, and the marker is still there.

## The mistake inside the fix, kept because it is the instructive part

The first draft put the marker check **before** the store lookup. That
reads naturally and is wrong: panes created by builds older than the
marker carry none, so the first restart after an upgrade would have
skipped them instead of adopting them, losing exactly the
restart-without-agent-loss property this path exists to preserve.

**The full suite passed on that draft.** No test covered the pane branch
at all, which is also why the original defect shipped. The marker now
gates only the unrecorded branch; adoption still keys off the store
alone.

## Verification, stated as failure rather than passage

Three tests, each confirmed to fail with the fence disabled:

```
--- FAIL: TestReapReportsNothingOnAHealthyFleet
    healthy fleet reports 1 reap candidate(s), want 0:
    [pane %0 in workspace test-reap-healthy]
--- FAIL: TestUnrecordedTmuxStateIgnoresPanesMarvelDidNotCreate
    reported 1 candidate(s) for a workspace whose only pane is tmux's own:
    [pane %0 in workspace test-foreign-pane]
```

`TestReapReportsNothingOnAHealthyFleet` also asserts its own
preconditions: it fails loudly if no unmarked base pane is present, or if
no pane carries the marker, so it cannot pass for the wrong reason.

`TestAdoptOrKillSparesPanesMarvelDidNotCreate` covers the kill policy
directly, because a correct preview beside a wrong action is the failure
mode the preview exists to prevent.

## Why this was reported as verified

finding-012 recorded "`marvel reap` listed one candidate and destroyed
nothing; `reap --confirm` destroyed it" and read it as orphan
identification working. It was measuring this bug. The assertion it
needed was **"reap reports nothing on a healthy fleet"**, which is now a
test.

This is the third methodology error in this ticket family and all three
share one shape: a check passed while measuring something other than what
it claimed. The other two are `pgrep -fc` returning garbage that made a
comparison vacuously true, and reading `head`'s exit status instead of
the command's. Orc finding-114 names the general form from a different
tool: a green check does not mean the thing you declared was the thing
that got checked.

The pattern is not carelessness in three unrelated places. It is that a
passing assertion and a correct assertion look identical in the output,
and only the failing case distinguishes them. Every test in this finding
was therefore checked by breaking the code, not by watching it pass.

## What this does NOT establish

- Nothing about panes created by older builds inside a workspace that IS
  recorded but whose panes are NOT. They carry no marker and are now left
  alone, which is the safe direction and also means an actual orphan from
  an older build is no longer reapable. No live sessions existed at the
  time of the change, so this is untested rather than wrong.
- Nothing about `reap` across tmux servers. It still sees only the
  daemon's own server (`aae-orc-wap0`).
- Nothing about whether reap should refuse when a candidate belongs to a
  live foreign daemon (`aae-orc-2cms`). That question is now narrower:
  per-HOME tmux servers mean a foreign daemon's panes are not visible at
  all.
