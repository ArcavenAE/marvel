# Probe brief: does an idle claude seat's statusline cost and token figure stay flat?

- **Status:** complete. It passed the team supervisor's check against the
  observe-only rule before it ran, and it ran on 2026-10-10. Result:
  `_kos/findings/finding-marvel-so6h-idle-claude-statusline-stays-flat.md`.
- **Author seat:** architect, team arcaven.
- **Commission:** the operator's ruling on marvel watcher decision 2
  (2026-10-10), "probe first": "Run a short test first: watch an idle Claude
  seat and see whether the cost and token numbers in its status line stay
  flat. Decide between the two options below on the result."
- **Design it serves:** `docs/design/marvel-watcher.md` section 8,
  decision 2. The two options are the statusline cost or token delta as a
  turn source for reports only, or none, reported as `unknown`.
- **Recorded data:** no pane text beyond the statusline figures, because
  this repository is public.

## Question

While a claude seat takes no turn, do the cost and token figures in its
statusline stay exactly the same from sample to sample?

The other half of the option is whether the figures move on every turn. This
probe answers that half only if a turn happens on its own during the window.
It never causes one (see the rules below).

## The one rule

**Observe only.**
- No `marvel inject`, no `tmux send-keys`, no keystroke of any kind.
- No `marvel capture --repaint`: it resizes the seat's pane to force a
  redraw, which acts on the seat.
- No `--composer` flag. No `tmux attach`, which can resize the pane.
- No director message to the seat, because a message can start a turn.
- No reading of credential, key or harness auth files, and no printing of
  environment values.

Every command below reads state that marvel or the seat already produced.

## The seat

- One marvel-managed interactive claude seat, with the statusline context
  feed on (`runtime.context_feed = "statusline"`), so its statusline runs
  while it idles.
- Chosen by the supervisor, not by this seat: one with no work dispatched
  for the window, an empty director inbox, and no turn in progress at the
  start.
- Not this architect seat, and not the supervisor itself.
- The supervisor may tell the seat's own supervisor not to dispatch to it
  for the window. That is a scheduling choice, the supervisor's to make. The
  probe does not need it, because it records any turn it sees.

## Window and cadence

- 30 minutes, one sample every 60 s, so 31 samples. 60 s matches the
  watcher's ruled pass interval.
- Total cost: 31 captures, 31 `get sessions` rows and 31 `describe` reads of
  one seat. All of these are local daemon reads.

## Each sample

1. **The statusline line.** Run `marvel capture <session-key>` (plain,
   visible area only) and keep only the statusline line, found by a pattern
   fixed before the run from one capture of that seat. Record the cost string
   and the token string exactly as printed. All other pane text is discarded
   at the moment of the read and never written to disk.
2. **The freshness stamp.** Read the session's context `observed_at` from
   `marvel describe session <session-key>`. A capture shows what the TUI last
   painted, and this stamp shows whether the statusline command ran since the
   last sample. The design notes that it re-stamps about every 15 s while
   idle (watcher section 6, H1).
3. **The activity row.** Run `marvel get sessions --columns
   name,state,last-active,context,active` for that seat, which gives
   LAST-ACTIVE and ACTIVE%.
4. **The turn indicator.** From the same capture, record a yes or no flag:
   does the pane show claude's working spinner or "esc to interrupt"?
   Record only the flag, not the text.

After the window, read `marvel events --session <session-key>` once for
`agent.*` kinds in the window, to cross-check the turn flags.

Each sample becomes one CSV row: UTC time, cost string, token string,
`observed_at`, LAST-ACTIVE, ACTIVE%, turn flag. The CSV and the fixed pattern
stay in the probe's scratch directory, outside the repository. The finding
reports the figures by field.

## Checks

- **STOP before sample 1** if any of these fails, and report which one:
  - the seat is mid-turn;
  - the statusline line cannot be found by the pattern;
  - the pattern matches more than one line.
- **STOP during the run** if a capture or a read fails twice in a row.
  Record how many samples were taken and report; do not retry around the
  failure.
- **Valid sample:** `observed_at` advanced since the previous sample. A
  sample whose stamp did not move is kept but marked stale, because the
  painted figure may be old.

## Verdicts

- **FLAT:**
  - no turn flag set in the window, and no `agent.*` event;
  - at least 25 valid samples;
  - the cost string and the token string identical across all of them.
- **MOVED:** with no turn flag and no `agent.*` event, the cost or token
  string changed. Record the samples where it changed.
- **INCONCLUSIVE:** fewer than 25 valid samples, or a turn interrupted the
  idle stretch so that less than 20 minutes of it remain.

If a turn happens on its own, its samples are reported separately: whether
the figures moved across it, and by how much. One turn is a single
observation, not "every turn".

## What the result decides, and what it does not

- FLAT makes the statusline delta a candidate for decision 2, for reports
  only. It goes back to the operator as that choice, with this probe's
  limits stated.
- MOVED points to `unknown`.
- In both cases decision 2 is the operator's, not this probe's.
- It does not show that the figures move on every turn. That needs either a
  natural turn in the window, reported as one observation, or the C2 scratch
  kit (watcher section 6), which drives prompts on a scratch claude and is
  the gate for any ring authority.

## Limits to state in the finding

- One seat, one claude version (the version as printed in the statusline or
  pane header, if shown), and one host.
- A capture is the last painted frame. The freshness stamp shows the
  statusline command ran, not that the frame was repainted after it. If the
  stamp advances but a figure stays the same, it may be either flat or stale
  paint. The finding says so. It does not rule this out with `--repaint`,
  which acts on the seat.
