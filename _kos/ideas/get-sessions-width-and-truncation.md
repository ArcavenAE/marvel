# Keeping `get sessions` readable: width, truncation, and the RUNTIME cell

- **Status:** idea (pre-hypothesis, no commitment).
- **Date:** 2026-09-25
- **Origin:** operator request via director: "we need some way to handle very
  long field names that make get session -w unsuitable, in particular when
  commands are run, the full path and command are shown in the runtime ... keep
  this view readable and not have it take up 200 characters wide destroying the
  display."
- **Subject:** marvel (the `get sessions` table, plain and `-w`). Filed in the
  marvel graph per the subject test.
- **Related:** [[token-rate-column-and-configurable-columns]] (Feature B, the
  column substrate this rides; Feature C, the budget columns that add width),
  [[get-views-connection-and-service-context]] (context that belongs off the
  table), [[subagent-session-display]] (the same table's limits). bd
  aae-orc-f08m0 (configurable columns).
- **Why a separate file:** the configurable-columns idea answers WHICH columns
  and in what order. This one answers how wide each may be and what happens
  when the chosen set does not fit, which is a policy on column definitions
  rather than another column. It is the display half of the same work and
  lands as properties of the column definitions f08m0 builds, not as a
  parallel design.

## Measured (kinu, 2026-09-25)

`marvel get sessions` on the local cluster, 26 sessions: the widest row is
**164 characters with no path in it**. Widest cell per column:

| column | width | | column | width |
|---|---|---|---|---|
| WORKSPACE | 5 | | CTX% | 3 |
| TEAM | 8 | | CPU% | 3 |
| ROLE | 19 | | RSS | 4 |
| GEN | 1 | | DESK | 2 |
| AGENT NAME | 32 | | RUNTIME | 6 |
| STATE | 18 | | LLM | 21 |
| HEALTH | 7 | | | |

The skippy cluster measures 151. So the table already overflows an ordinary
terminal before RUNTIME carries a path. The width comes from AGENT NAME (the
computed `<team>-<role>-g<gen>-<index>`), ROLE, STATE (`failed (saturated)`),
and LLM (`Opus 5.5 (1M context)`).

**Where the path comes from.** The RUNTIME cell prints `Runtime.Name` and
falls back to `Runtime.Command`. A manifest role with `image = "claude"` shows
`claude`, which is why no path appears on kinu today (every manifest role on
disk declares an image). `marvel run <command>` sets BOTH `Runtime.Name` and
`Runtime.Command` to its first argument (`internal/daemon/daemon.go` run
handler), so a one-off launched through a launcher script shows the whole
absolute path. A 59-character launcher path under the home directory takes the
row to about 217. `-w` uses the same renderer, so it inherits all of this, and
`get teams` has the same fallback.

A side defect worth its own look: for a `marvel run` session, `Runtime.Name` is
a path, not a name, and the adapter registry resolves it to the generic adapter
either way. A name field holding a path is part of why the display has nothing
short to print.

## Options, with prior art

For each: what the operator loses, and how they get the full value back.

1. **Short name in the cell, full command in the detail view.** RUNTIME shows
   the adapter name when `Runtime.Name` is a registered adapter, else the
   basename of the command (`cast-dtu-seat.sh`). The full command lives in
   `describe session` and in a selectable `command` column. Prior art: kubectl
   prints a resource's short fields in `get` and the full spec in `describe`.
   Loses: the directory, which is the part of the path that repeats across the
   fleet's launchers and so distinguishes little. Back: `describe session`,
   or `--columns ...,command`.
2. **Truncation of long text cells.** End ellipsis, middle ellipsis
   (`/Users/…/cast-dtu-seat.sh`, keeps the distinguishing tail), or basename.
   Prior art: `docker ps` truncates COMMAND and IDs by default with
   `--no-trunc` to show them whole; systemctl ellipsizes to the terminal with
   `--full` to stop it; git's `%<(N,mtrunc)` truncates in the middle. Loses:
   the elided characters, and a copy-paste of the cell is no longer a usable
   value. Back: `--no-trunc`, or the detail view.
3. **Terminal-width awareness.** Detect the terminal width and, when the row
   does not fit, shrink or drop the lowest-priority columns first. Prior art:
   `ps` sizes to the terminal and `ps -ww` removes the limit; kubectl keeps its
   default narrow and adds columns only under `-o wide`. Loses: dropped columns
   silently, unless the table says so. Back: widen the terminal, `--no-trunc`,
   or name the columns.
4. **Two fixed sets: narrow default and `-o wide`.** Prior art: kubectl's
   `-o wide`, and `-o custom-columns` for an operator-named set. Loses nothing
   in wide, and in the narrow default the columns wide adds. Back: `-o wide`.
   Feature B's `--columns` is already the custom-columns half.
5. **Wrapping within a cell.** Loses: one row per session, which is what
   makes the table scannable and sortable, and which `-w` redraws in place.
   Poor fit for a watch view. Back: none needed, nothing is hidden.
6. **Per-column max width in config** (`display.max_width.runtime: 24`).
   Loses: whatever the operator's own cap cuts. Back: raise the cap or
   `--no-trunc`. Useful as a later override, not as the first fix.

## Recommendation

**Default: option 1 plus option 2 for text columns, applied only when the
output is a terminal.**

- RUNTIME shows the adapter name, or the command's basename when there is no
  registered adapter name. The full command moves to `describe session` and a
  selectable `command` column.
- Each column definition in the Feature B substrate gains three properties:
  a **priority**, a **soft max width**, and a **truncation mode** (`none`,
  `end`, `middle`, `basename`). AGENT NAME and paths truncate in the middle
  (the tail distinguishes); LLM and STATE at the end. Numeric columns never
  truncate: a clipped number is a wrong number, so under pressure a numeric
  column is dropped by priority rather than cut.
- Truncation and dropping apply only when stdout is a terminal. Piped or
  redirected output is never altered, so scripts and `grep` keep full values
  (the `ps` and `docker` behaviour).
- When a column is dropped to fit, the table says which (one line under it),
  so nothing disappears silently.

**Escape hatch: `--no-trunc`** (the docker name, already familiar) turns off
truncation and dropping for one invocation. `--columns` from Feature B stays
the way to choose exactly what is shown, and an explicitly named column is
never dropped, only truncated, because the operator asked for it.

**Against the configurable-columns design:** this changes no grammar and no
precedence. It adds width, priority, and truncation mode as properties of the
column definitions f08m0 already makes first-class, the same place Feature B
puts numeric alignment and sort keys. The budget columns from Feature C are
account-scoped and would sit in the separate account view anyway, which keeps
them off this width budget.

## Open questions

- Should `marvel run` store a real name (the adapter name, or the basename)
  in `Runtime.Name` and keep the path only in `Runtime.Command`? That fixes
  the source rather than the render, and touches the run RPC.
- `-w` redraws in place; should it re-measure the terminal on resize, or
  measure once at start?
- What are the default priorities? A first cut: identity columns (TEAM, ROLE,
  AGENT NAME, STATE, HEALTH) highest; CPU%, RSS, DESK, LLM lowest.
