# A pending harness update as its own health condition, recovered by exit and resume

**Status: idea. Pre-hypothesis. Nothing here is designed or measured.**

Captured: 2026-10-04
Source: operator, typed to director in session (no public source), in director's REQUEST 01M43NG31RH7D5PWPV4JG5SS9P, item 2, sent 2026-10-04 at about 14:20Z, and passed on by arcaven-supervisor (message 01M43TRVF923SCX871Q3RAZEYX, 2026-10-04T16:08Z).

## Why this is worth reading

A seat whose harness has an update pending is not unhealthy the way a seat out
of context is, yet the restarts the code shows all start a new session: `marvel
kill` ends the seat (cmd/marvel/main.go:1439) and the controller refills the
missing replica (internal/team/controller.go:335), both read at origin/main
b0a6d67. Whether any path keeps the session is unverified. If the two conditions are
told apart, a seat with a good context could be restarted and resumed without
discarding its work, and a shift change is kept for the case that needs it.

## The operator's words

> "kos idea marvel stagekeeper some health conditions for harnesses include a pending update of that harness session and if the context is good, may not warrent/prefer a shiftchange. instead, if marvel/stagekeeper detects this condition, it may be possible to exit the harness with marvel-native tmux exit and then resume the session, allowing faster recovery from the health conditions of harness pending update (which should itself be a thing, different health conditions)"

## Director's gloss (director's reading, not the operator's words)

- (a) "Harness update pending" is its own health condition, separate from
  context pressure.
- (b) Its recovery action is to exit the harness and resume the same session,
  chosen instead of a shift change when the context is healthy.

## Where this lives

Filed in marvel's graph by the subject test. The condition is a seat health
state and the recovery is a marvel action on a seat's tmux pane; without
marvel there is nothing to detect or recover. stagekeeper is named by the
operator as a detector, so it is an object here: the idea is that marvel or
stagekeeper observes the condition, and marvel owns the action. Whether the
detector lives in marvel or in stagekeeper is an open question below, not a
decision.

## Sketch, with the unverified parts marked

1. **A new health condition**, "harness update pending", kept apart from the
   context-pressure signal. A harness shows it in different ways (a menu, a
   banner, a status line), so detection is per harness.
2. **A recovery action** chosen by the pair (condition, context state). With
   a pending update and healthy context: exit the harness through marvel's own
   tmux exit, then resume the same session. With a pending update and a
   context near its limit: the existing shift change covers both.
3. **Faster recovery** is the operator's stated gain: resume keeps the
   conversation, so no handoff is written or read.

Nothing above says a harness can be resumed after an exit. That depends on
each harness and is the first thing to measure.

## Open questions

- Does each harness resume its session after an exit, and does the resumed
  session pick up the update? Claude Code, codex and others differ, and this
  has not been measured. One cited fact: for claude, marvel mints a session id
  and declines to when the launch args carry `--resume`, `-r`, `--continue` or
  `-c` (internal/runtime/claude.go:83-91), so a resume path would meet that rule.
- What does "marvel-native tmux exit" mean in code: an existing path, or a
  new one? The inject path refuses menus it can place (marvel#560), so an exit
  key must not become a way to answer an update menu by accident.
- How is "pending update" detected without typing into the pane? The pane
  reader that places a harness screen (`internal/panestate`, which today
  recognizes a logged-out screen) is one candidate; stagekeeper's own
  patterns are another.
- What counts as "the context is good"? The context-pressure idea below says
  pressure is an operating point and not a fill level, so the threshold is not
  a single number.
- Who decides between exit-and-resume and a shift change: a rule in the seat's
  manifest, the supervisor seat, or the daemon? The auto-shift trigger model
  is the closest existing layering.
- What happens to work in flight in the pane when the harness exits, and to
  the seat's presence and heartbeat while it is down?
- Is a pending update a health condition, or an input to the upgrade
  intelligence work, which already owns "what changed under a running seat"?

## Related

- `_kos/ideas/health-signal-taxonomy.md`: what marvel does and does not know
  about a seat's health, and how it degrades by harness.
- `_kos/ideas/auto-shift-trigger-model.md`: the shift trigger and its
  manifest layering; the place a second recovery action would attach.
- `_kos/ideas/context-pressure-is-an-operating-point-not-a-fill-level.md`:
  what "the context is good" can mean.
- `_kos/nodes/frontier/question-shift-triggers.yaml`: the open question on
  automatic shift triggers.
- `_kos/nodes/frontier/question-seat-harness-state.yaml`: marvel preparing a
  seat's harness state (trust, settings, login) before launch.
- `_kos/probes/brief-marvel-upgrade-intelligence.md`: the upgrade study.
- marvel#477: an inject into a fresh codex seat can select "Update now"; the
  detect-and-never-type side of the same update menu.
- marvel#560: the inject refusal on a recognized limit menu, the pattern a
  pane reader follows.
