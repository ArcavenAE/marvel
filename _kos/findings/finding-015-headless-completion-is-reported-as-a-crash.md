# finding-015: a headless role that finishes its work is reported as a crash

Date: 2026-08-07
Observed on: the operator's live cluster, `mixed/matrix`, during a rolling
shift of all teams
Bears on: `question-runtime-adapter-framework`, `elem-agentic-resource-matrix`

## What was seen

Three of four roles in `mixed/matrix` read `crashed` after a successful
shift. `team.shift-completed` fired for the team, and the three were the
three `-p` roles. Read in order, the event ring says they did their jobs:

```
17:03:13  info     agent.message.completed   matrix-builder-p-g2-0  assistant: ok
17:03:13  info     agent.turn.completed      matrix-builder-p-g2-0  tokens in=19235 out=5
17:03:15  warning  session.crashed           matrix-builder-p-g2-0  pane %51 gone

17:03:16  info     agent.turn.completed      matrix-scout-p-g2-0    tokens in=1 out=2 ctx=35585
17:03:16  info     agent.message.completed   matrix-scout-p-g2-0    assistant: ok
17:03:16  warning  session.crashed           matrix-scout-p-g2-0    pane %52 gone
```

Completed turn, produced the requested output, exited. `analyst-t`, the
interactive role in the same team, stayed `running`.

The manifest says exactly what these are:

```toml
  [[team.role]]
  name = "builder-p"
    [team.role.runtime]
    image = "codex"
    mode = "headless"
    prompt = "Reply with the single word: ok"
```

A prompt and `mode = "headless"` is a job. It runs once and terminates.

## The gap, which is narrower than it looks

Marvel already has every piece and does not connect them.

- `api.SessionSucceeded` is declared at `internal/api/types.go:16`.
  **Nothing writes it.** Grepping `SessionSucceeded` across `internal/`
  and `cmd/`, excluding tests, returns the declaration and nothing else.
  It is a dead state.
- `api.RuntimeModeHeadless` is declared at `types.go:101` and the
  manifest parser populates it, so the intent is known at spawn.
- `Manager.ReapDead` (`internal/session/manager.go:895`) tests only
  `m.driver.HasPane(sess.PaneID)`. A missing pane becomes
  `SessionCrashed` at line 912 with no reference to the role's mode and
  no exit status.

So the classification is made on one bit, pane present or absent, for a
workload whose declared shape says absence is the success condition.

## Why it is worth fixing rather than explaining away

The established cost is diagnostic. "Completed the turn" and "died before
doing anything" produce an identical row: `crashed`, blank DESK, no
distinguishing field. A healthy fleet reads as three-quarters broken at a
glance, and separating the two cases means leaving the table for the
daemon log or the event ring. That is the same class as finding-014,
where `reap` could never report clean, so the signal an operator was
asked to act on was noise.

A first draft of this finding also asserted that the restart policy acts
on the state, so these roles respawn and converge on
`crashloop-backoff`. **That was not observed and is withdrawn.** The
three roles were watched for roughly four minutes after the shift and
stayed `crashed` with no `session.created` following them.
`mixed-adapters.toml` declares no `restart_policy`, so whatever the
default resolves to did not visibly respawn them. Whether it should is
the open question below, not a measured consequence.

## What this does NOT establish

- Nothing about exit status. `ReapDead` observes a vanished pane, not a
  return code, so distinguishing "exited 0" from "died" needs a source
  of truth that does not exist on this path today. `mode` is the
  cheap discriminator; exit status is the correct one.
- Nothing about whether `succeeded` should suppress restart on its own,
  or whether headless roles want a separate `restart_policy` default.
  A job that should run once and a job that should run on a schedule are
  different, and marvel has no Schedule type (it is model-only).
- Nothing measured about crash-loop backoff engaging, or about whether
  these roles respawn at all. They stayed `crashed` for the window
  watched. The daemon was stopped by the operator before this could be
  followed up, so it remains open.

## Relationship to aae-orc-bxeh

`bxeh` was filed earlier the same day and is the ticket. It found the
mechanism this finding missed: `NewPane` sets `remain-on-exit` off, so an
exiting harness closes its own window, and `ReapDead` also clears
`PaneID` at line 913, which is why DESK goes blank. It also found the one
`SessionSucceeded` reference outside the declaration, a test fixture at
`internal/api/budget_test.go:185`.

What this finding adds is that the behaviour is not confined to the Act 2
demo, which is how `bxeh` frames it. It reproduced on the operator's live
cluster during a rolling shift of all three teams, and
`team.shift-completed` fired for `mixed/matrix` while three of its four
roles read `crashed`. Team-level success and role-level health disagree
in the same breath, on real work.

## Provenance note

This was not found by inspection. It surfaced because a rolling shift was
run across all teams on a live cluster and the result looked wrong. Two
predictions made before the shift were also wrong and are recorded so the
finding is not read as more foresight than it was: that `mixed` would
stall and roll back on the 10m timeout (it completed in 38s), and that
`scout-p` could not spawn because `opencode` was missing (it was missing
from the operator shell used to check, not from the daemon's environment,
and scout-p ran and reported `opencode/deepseek-v4-flash-free`).
