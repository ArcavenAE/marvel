# Named views for `get sessions`: predefined, overridable, switched by key

- **Status:** idea (pre-hypothesis, no commitment).
- **Date:** 2026-11-25
- **Origin:** operator, relayed by director, verbatim: "kos idea marvel could we have display profiles and a hotkey to cycle through them or maybe use numbers for the different profiles to switch to them? views so to speak? Maybe have some predefined views and be able to customize/override them?"
- **Subject:** marvel (the `get sessions` table, plain and `-w`). Filed in the marvel graph per the subject test.
- **Related:** [[token-rate-column-and-configurable-columns]] (columns became a user option: `--columns` and `display.session_columns`), [[get-sessions-width-and-truncation]] (closed), [[get-views-connection-and-service-context]], [[multicluster-view]], [[cluster-rollup-header]]; ArcavenAE/marvel#1598 (the `-w` alignment fix under way when this was raised).

## The gap

Today there is one column set at a time: the built-in default, or one named list in `display.session_columns`, or a `--columns` list typed per call. Each question an operator asks of the fleet (who is busy, what is it costing, which login is each seat on, is anything unhealthy) wants a different set of columns, so the operator either edits the config, retypes `--columns`, or reads a table too wide to hold all of them. The width fix in #1598 hides columns that do not fit; it does not help the operator pick which ones matter for the question in hand.

## The idea

A **view** is a named column set (and possibly a sort), for example:

- `default`: what the table shows now.
- `work`: name, state, health, last-active, last-turn, tout, rate.
- `cost`: name, llm, tin, cr, cw, cost, account, 7d.
- `login`: name, backend, cred, home, login, account.
- `host`: name, cpu, rss, pane, workdir, runtime.

Three parts:

1. **Predefined views** ship with marvel, so a fresh install has them.
2. **Custom and override views** in the client config, for example `display.views.<name>: [columns...]`. A config view with a built-in name replaces that built-in; a new name adds a view. `display.session_columns` stays and keeps meaning what it means today (the starting view), so nothing existing breaks.
3. **Switching:** in `-w`, the number keys 1 to 9 jump to the view in that slot, and one key cycles to the next view. The help line (`?:help`) lists the views with their numbers. Outside watch mode, `--view <name>` picks one for a single read. The current view's name shows in the watch header.

## Questions to settle before a design

- Which views ship built in, and in what order (the number slots)?
- Does a view carry a sort and a filter as well as columns, or columns only?
- Precedence: does `--columns` beat `--view`, and what does `display.session_columns` become once views exist (the slot-1 view, or a view of its own)?
- Does watch mode remember the last view across runs? A per-viewer convenience in local state, never shared.
- Which key cycles, given the keys watch mode already binds?
- How does the operator's width ruling apply (I-794 R1: a list that does not fit uses the existing hide-from-right method and says how many columns are hidden)? Each view is a named list and gets the same treatment.
- Do the cluster-set view and the single-cluster view share the same views, given that KEY and CLUSTER apply only to one of them (I-794 R2: KEY is shown only when asked for)?
