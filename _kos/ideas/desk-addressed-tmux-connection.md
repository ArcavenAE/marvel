# Desk-addressed tmux connection

- **Status:** idea (pre-hypothesis, no commitment). One feature family, two
  facets: a CLI verb that resolves a desk number to a tmux attach, and a
  jump-to-desk keymap in the get-sessions display that consumes that verb.
- **Date:** 2026-09-17
- **Origin:** I raised this today during the daemon reexec and the jabberwock
  auto-shift test, after hand-assembling `tmux -L <socket> attach` commands and
  hunting for pane %23 by desk number by hand.
- **Subject:** marvel (the `get sessions` display and the tmux driver's
  addressing). Filed in the marvel graph per the subject test.
- **Related:** [[token-rate-column-and-configurable-columns]] (same
  get-sessions surface; the keymap and its return-to-view share that display's
  state). bd: aae-orc-lj44u.

## The gap

`marvel get sessions` already carries a DESK column. Today it is the tmux pane
id with the `%` stripped (`cmd/marvel/main.go`, derived from `s.PaneID`). The
daemon runs a dedicated tmux server (`tmux -L <socket>`, one socket per
cluster), each agent runs in its own window, and the pane is `%N`. So marvel
already holds, or can derive, the whole address a human needs to reach a desk:
the socket, the tmux session, and the pane.

What is missing is a verb that turns a desk number into that attach. Today I
read the DESK column, translate the number back to `%N` in my head, and
hand-assemble `tmux -L <socket> attach -t <session>` followed by a select to
the right window or pane. That is the friction this idea removes.

## Facet 1: a verb that resolves a desk to an attach

Shape to explore: `marvel attach <desk>` or `marvel tmux <desk> [--cluster
<id>]` that resolves the desk to the correct `tmux -L <socket> attach -t
<session>` plus a select to the agent's window or pane, and either execs the
attach or prints the exact command for the operator to run or copy. The verb is
a projection of state marvel already holds (DESK is derived from PaneID), not
new bookkeeping.

Open questions:

- **exec vs print.** Exec is fewer keystrokes. Print composes into scripts and
  is the safe choice when the operator is already inside tmux. A default that
  execs when outside tmux and prints when inside, or an explicit `--print`
  flag, is worth exploring.
- **desk uniqueness is per cluster.** A desk number is a tmux pane id, unique
  only within one `-L` socket, so within one daemon and one cluster. Two
  clusters can each have a desk 23. Hence the optional `--cluster`; without it,
  resolve against the current or default cluster and refuse an ambiguous match
  rather than guess.
- **window vs pane target.** Each agent is its own window, so the select is
  either `select-window` to the window that carries pane `%N` or `select-pane
  -t %N`. Pin the exact target so the operator lands on the agent, not on the
  session's first window.
- **attaching from inside tmux.** A nested attach is a footgun. The verb should
  detect `$TMUX` and `switch-client` rather than `attach`, or fall back to
  print.
- **what "desk" should mean long term.** Today it is the raw pane id. If desk
  numbers should be stable, human-friendly, or scoped per cluster rather than
  raw `%N`, that is a separate decision this verb would consume. Capture only
  here.

## Facet 2: the jump-to-desk keymap (a consumer of facet 1)

In the get-sessions display (watch mode or a later TUI), a keymap opens a
configurable terminal command that launches directly into the session by desk
id. The operator types the desk number or selects the row from the display; on
exit it returns to the get-sessions view it was on. The keymap is the
interactive front end; the facet-1 verb is the resolution underneath it.

Open questions:

- **configurable terminal command.** The operator's terminal and how it is
  invoked to open a new window or tab running the attach, as a template with the
  resolved command substituted in.
- **select vs type.** Pick the desk from the current row under the cursor, or
  type a number. Row-select is fewer keystrokes when the desk is on screen.
- **return-to-view.** On exit, restore the get-sessions view: the same sort, the
  same cluster, the same scroll position. That display-state concern overlaps
  with [[token-rate-column-and-configurable-columns]].

## Why one family

Facet 2 is the interactive surface; facet 1 is the resolution it calls. They
share one root: marvel holds the desk-to-tmux-address mapping but exposes no
ergonomic path from a desk number to a connected terminal. The verb is useful
alone, from any shell and any script, so it comes first; the keymap sits on top
of it.

## Not now

Capture only. No verb, no keymap, no code. The bead (aae-orc-lj44u) tracks the
work; this file holds the shape.
