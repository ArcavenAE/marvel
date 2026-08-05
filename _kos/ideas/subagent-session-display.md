# Subagent display in the sessions table

- **Status:** idea (pre-hypothesis, no commitment)
- **Date:** 2026-08-05
- **Origin:** operator question during the act-2 demo review, same session
  that shipped the LLM/RUNTIME column shuffle and the statusline context
  feed (finding-011, PRs #110/#112)
- **Related:** aae-orc-7hzb (statusline probe, carries the "no daemon
  surface for per-subagent context" gap), finding-011 (the
  subagentStatusLine payload shape)

## The question

Some harnesses share live subagent information. Claude Code's
subagentStatusLine feed delivers, per refresh tick, a tasks array under
the parent session id: task id, type, status, description, startTime,
model, contextWindowSize, tokenCount, and a tokenSamples time series.
`marvel ctx-forward` already parses this and throws it away, because the
daemon has nowhere to put it.

Should `marvel get sessions` display subagents at all, and if so, how?

## Display sketches (from the operator, verbatim intent)

- A fold indicator on the parent row: an icon signalling "there are more
  sessions here" that expands to child rows. Collapsed by default so the
  fleet view stays one row per session.
- Tree glyphs on expanded children, in the AGENT NAME or RUNTIME column:
  `↳`, `├──`, `├`, `└─` or similar, marking the row as a subprocess of
  the row above.
- Runtime-aware rendering: since marvel knows the parent runtime is
  claude (or whatever), the child row's RUNTIME cell could do something
  interesting rather than repeat the parent, e.g. carry the glyph plus
  the harness name (`└ claude`), or the task type (`local_agent`).

## Column applicability (the gaps problem)

Subagent rows are not Sessions. They have no pane, no lifecycle managed
by marvel, no restart policy, no generation. A child row would populate:

| Column | Child value |
|---|---|
| WORKSPACE / TEAM / ROLE | inherit from parent (or blank to de-emphasize) |
| GEN | n/a |
| AGENT NAME | task description or label ("Count files in directory") |
| STATE | task status (running / completed) |
| HEALTH | n/a (no healthcheck applies) |
| CTX% | tokenCount / contextWindowSize |
| CPU% / RSS | n/a (subagents share the parent process; procstat cannot split them) |
| DESK | n/a (same pane as parent) |
| RUNTIME | glyph + inherited harness, or task type |
| LLM | task model |

Roughly half the columns are inapplicable. Options: render `·` in n/a
cells (visually quieter than `-`, which already means "not measured"),
or give expanded children a compressed layout that does not pretend to
be full rows.

## Sorting (the hard interaction)

Sorting a flat table with attached children breaks naively: children
must stick to their parent or the tree reads as garbage.

Prior art worth stealing:

- **htop tree view:** a mode toggle. In tree mode, sorting applies among
  siblings within each subtree; the hierarchy is never broken. Leaving
  tree mode restores flat global sort.
- **ps f / pstree:** ASCII art forest, no interactive sorting at all.
- **k9s:** hierarchical resources get their own views rather than
  inlined rows.

Candidate rule: children always travel with (immediately below) their
parent; sort keys order parents globally and children within a parent.
A sort on a column that is n/a for children (CPU%, DESK) simply leaves
child order untouched. Expansion state is a watch-mode toggle (say
`x` or Space on the parent row); one-shot `get sessions` prints either
collapsed-with-indicator or a `--subagents` flag expands all.

## What has to exist first (the data plane)

Display is the second half. The first half is a daemon surface:

1. Extend the heartbeat RPC (or add a sibling RPC) so ctx-forward can
   deliver the tasks array it already parses.
2. Store shape: ephemeral child readings hanging off the Session
   (`[]SubagentReading` with a TTL or last-seen timestamp), never
   persisted to bolt (they are gone when the parent's statusline stops
   reporting them).
3. Staleness: subagent rows vanish from the feed when tasks complete;
   the table needs a decay rule (drop after N seconds unseen) so
   finished subagents do not linger as ghosts.

Only claude exposes this today (finding-011). If codex/opencode never
grow an equivalent, the fold indicator is claude-only, which argues for
the indicator being data-driven (rows appear when readings exist)
rather than runtime-driven.

## Open questions

- Is the sessions table even the right surface, vs `marvel describe
  session` (already per-session, no sorting problem) or a dedicated
  `marvel top`-style view?
- Does a subagent row ever warrant an event (`agent.subagent.started`?)
  or is that ring noise?
- tokenSamples is a small time series; is a sparkline in the CTX% cell
  worth the render complexity, or strictly a describe-level detail?
- Does the B14 taxonomy have anything to say about naming these rows
  (they are agents in the B14 sense, running inside a session that is
  itself one agent's pane)?

## Crystallization signal

If a second harness exposes subagent data, or the first fleet workload
routinely runs foreground subagents worth watching, extract a frontier
question and a probe brief (display prototype behind a watch-mode
toggle, fed by a heartbeat extension).
