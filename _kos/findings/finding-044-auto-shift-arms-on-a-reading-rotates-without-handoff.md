# finding-044: auto-shift arms only with a context reading, and rotates without a handoff

Two halves of the automatic context-pressure shift on f601b6e: what it takes to
arm the trigger, and what it does when it fires. The first half is a defect in
my own example manifests, fixed here. The second half is executed evidence for
question-shift-triggers, a capability-state fact rather than a defect against
ourselves.

## Part A: a resolved window alone does not arm auto-shift

The remainder trigger fires when a live session crosses
`ContextTokens > ContextLimit - HeadroomTokens` on a resolved window
(`evaluateShiftTriggers`, controller.go:805). Both operands must be populated,
and they arrive by different paths:

- `ContextLimit` resolves from the window ladder. A `claude [1m]` session
  resolves 1000000 on its own; any session resolves it from an explicit
  `runtime.context_window`.
- `ContextTokens` is filled only for a session marvel has an activity channel
  for: a headless stream (accountant.go:287), or the statusline feed
  (store.go:714), which an interactive session opts into with
  `runtime.context_feed = "statusline"`.

`RuntimeModeInteractive` is the default (`Mode = ""`, types.go:146). An
interactive claude emits no parseable stream, so without `context_feed` the
accountant never fills `ContextTokens`. It stays 0, the remainder never crosses
headroom, and the trigger never arms while looking correctly configured. A
window sets the denominator; it does not supply the numerator.

Defect in my own examples: `examples/auto-shift.toml` and `.yaml` (shipped in
#292) set `context_window = 1000000` but omit `context_feed`. On an interactive
claude, which is what they declare, the reading never populates, so the example
as written never fires. Fixed in this harvest: both examples add
`context_feed = "statusline"`, and CLAUDE.md's automatic-shift section now
states that a window alone does not arm the trigger, and that an interactive
role needs a reading (a headless stream or the statusline feed) before the
policy can act.

## Part B: the trigger rotates cleanly and hands off nothing (executed)

With a reading present, the trigger works end to end. Measured on f601b6e
against a live interactive claude (the Jabberwocky auto-shift test).

Event trail:

```
team.shift-autotriggered  "context pressure: session at 206225/1000000 tokens,
                           remainder 793775 <= headroom 799000"
team.shift-started        gen 1 -> 2
session.created           gen-2, pane %24
team.shift-role-ready     gate=running, draining gen 1
session.deleted           gen-1
team.shift-completed       gen 2 active
```

The remainder math is exactly the design. It rotates as a clean rolling shift.

But the successor came up cold: an idle claude REPL at the splash screen, empty
prompt, CTX 0, no task and no commander's intent. There is no
handoff-artifact event in the trail, and there is no handoff `Kind` in
events.go (confirmed). Gen-1's completed worklist survived only as files on
disk that gen-2 had no knowledge of. On this binary, auto-shift ROTATES but
does not HAND OFF.

A follow-up prompted the cold successor with the same neutral launch message
and watched it. The three-part capability fact:

1. The trigger works.
2. marvel delivers no handoff. The successor comes up cold and idle and does
   not self-resume; nothing at spawn tells it it is a successor or what its
   predecessor was doing.
3. Filesystem recovery worked, but only because three conditions held that
   marvel did not supply: the work was file-based in a shared cwd, the
   successor was externally prompted to start, and the brief carried
   successor-aware instructions. Given those, the successor correctly detected
   it was a successor and resumed at item 9 (from PROGRESS.md's last "item 8
   done" line and ANALYSIS.md's last heading), with no restart and no clobber.
   marvel supplied none of the three automatically.

This is the first executed confirmation of what question-shift-triggers
deduced from code: finding-018 found no drain (shiftDrain kill-panes the
predecessor with no signal-then-wait) and no handoff artifact anywhere in the
repository. The handoff content path (commander's intent plus a resume pointer
at spawn) is designed in aae-orc-7opc but is not in f601b6e.

This is capability-state, not a defect to file against ourselves. The handoff
work is roadmap (vision M-2a, matrix row 15); the 7opc artifact is what would
close the gap Part B measures. Node capture is the right layer.

Related: question-shift-triggers (the node this updates), finding-016 (the
auto-compact window the trigger fires ahead of), finding-018 (no drain, no
handoff artifact in code), aae-orc-7opc (the designed handoff artifact),
aae-orc-hfc0 (per-fleet actuator caution the trigger already respects via the
per-tick budget).
