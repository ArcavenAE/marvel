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

## Addendum, 2026-08-09: re-verified live, and one test added

`aae-orc-reya` was filed against this defect about thirty minutes before
PR #132 merged, so it outlived its own fix. Re-checking it produced two
things worth keeping.

**The fix holds under a live rig.** Isolated `HOME` and tmux server, a
two-replica generic fleet, ground truth read straight from tmux: the base
pane is present and unmarked (`%0`, `marker=`), both replicas carry
`marker=1`, `marvel reap` says "Nothing to reap", and `reap --confirm`
leaves every pane id in place.

**Marvel is not sensitive to `base-index` or `pane-base-index`.** With
`base-index 3` and `pane-base-index 7` set in the rig's `.tmux.conf`, the
panes come back as `pane_index=7 window_index=3,4,5` and reap still
reports clean. Nothing in the driver targets a pane by index; every
`-t` argument is a `%id`.

**The reason to reject the `%0` guard is sharper than "base-index is
configurable".** Pane ids are allocated per server in creation order and
are unaffected by either index option, so `%0` stays `%0` no matter how a
user configures tmux. What actually breaks an id guard is a second
workspace: two workspaces of two replicas each put the base panes at `%0`
and `%3`, and only the first of those is `%0`. The original text is right
that the number is not a fact, for a reason it does not name.

**Which exposed a gap in the tests above.** Every test in this finding
uses one workspace, so every one of them also passes against the guard
this finding rejects. I confirmed that by replacing the `!p.Created`
fence with `p.ID == "%0"` and running them:
`TestReapReportsNothingOnAHealthyFleet` and
`TestUnrecordedTmuxStateIgnoresPanesMarvelDidNotCreate` both PASS. The
suite proved the defect was gone without proving the fix had the shape
that keeps it gone.

`TestReapReportsNothingWhenTheBasePaneIsNotPaneZero` closes that. It
builds two workspaces, asserts its own preconditions (the later workspace
has an unmarked base pane, and its id is not `%0`, so an id guard and a
provenance guard genuinely disagree), and then asserts reap reports
nothing. Against the `%0` fence it fails with `1 reap candidate(s), want
0: [pane %3 in workspace test-basepane-second]`.

Same lesson as the section above, one level up. The earlier check
confirmed the behavior was right; this one confirms the mechanism is.
