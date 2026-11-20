# Session quick look: Space shows a seat's pane read-only, Esc leaves

- **Status:** idea (pre-hypothesis, no commitment). Notes only: nothing is built,
  and nothing here is a design decision. Open questions are listed as questions.
- **Date:** 2026-11-19
- **Origin:** the operator, in a message routed by director (2026-11-19), quoted
  below as written.
- **Subject:** marvel (the `get sessions` watch view and `marvel capture`). Filed
  in the marvel graph per the subject test.
- **Related:** [[desk-addressed-tmux-connection]] (the attach verb and the
  jump-to-desk keymap this would sit beside), [[tmux-over-mrvl]] (a terminal on
  the `mrvl://` channel, the heavier way to reach a pane).

## The operator's words

```
kos idea marvel keybind for <space> for quick look behavoir to ro jump into tmux session but must be easy to get out so not really tmux, need a tmux viewer with easy exit/esc and spack toggle
```

## One reading of it

This section is a reading of the words above, relayed by director, not the
operator's own text.

A quick-look keybind in marvel's session list, like macOS Quick Look. Space opens
a read-only view of the selected seat's tmux pane. Esc, or Space again, closes
it and returns to the list. It is not an attach: no keystrokes reach the pane,
and there is no tmux prefix to learn in order to leave. "spack" is read as
"space", so Space toggles the view.

## What exists today

Checked against main at dd7a30d8 by reading the code and the help; I did not run
a client.

- **The list it would sit in.** `marvel get sessions -w` is a watch view with a
  raw-mode key reader (`cmd/marvel/watchloop.go`, `cmd/marvel/keyreader.go`).
  `handleWatchKey` takes `q` and Ctrl-C to quit and several single-letter keys
  that change the view (`c` toggles a sort on context, for one); `j`, `k` and
  Enter move and act on a cursor
  (`cmd/marvel/watchselect.go`). That function has no case for Space, so the key
  looks free. The selected row is held by identity (cluster, workspace, name),
  not by row index, so a refresh or re-sort finds it again.
- **Enter is the opposite of a quick look.** Enter resolves the row's desk and
  hands the terminal to a tmux attach (`cmd/marvel/watchhandoff.go`: it parks the
  key reader, restores cooked mode, clears the screen, and puts raw mode back
  afterwards). Only a switch-client or a new-window returns at once; an attach
  takes the terminal, so leaving it means tmux's own detach key. That is the
  friction the words describe: "not really tmux".
- **`marvel capture <session-key>` is a read-only snapshot.** It returns the
  pane content through a daemon RPC (`capture`), the visible area by default, with
  `-S` reaching into scrollback and `-e` keeping escape sequences. Per its help,
  it shows what the program last painted, not necessarily what is on screen now,
  and a full-screen harness on the alternate screen has no scrollback. Its
  `--repaint` flag is not read-only in the strict sense: it widens the pane by
  one column and puts the size back, so a quick look that wants a fresh paint
  would need to decide about that.
- **`marvel view` is not this.** The `view` verb works with a seat's read-only
  git views (`cmd/marvel/view_cmd.go`). The name is close and the subject is
  different.
- **`marvel attach` and the desk keymap** are in
  [[desk-addressed-tmux-connection]]; a terminal over the SSH channel is in
  [[tmux-over-mrvl]].

## Questions

- **Snapshot or live.** One `capture` on open is a still picture of the last
  paint. A refresh on the watch's own tick would follow the pane, at the cost of
  one RPC per tick for the one selected seat. Which is the quick look?
- **What a harness on the alternate screen shows.** A capture of such a pane is
  the visible screen only. Is that enough for a glance, and is a scrollback key
  inside the view wanted or out of scope?
- **Escapes.** Plain text loses dim text's attribute (the capture help says so);
  `-e` keeps escapes and would need the view to render them. Which does a glance
  need?
- **The repaint nudge.** Should a quick look ever resize the pane to force a
  repaint, or should it stay strictly passive and show a stale paint as stale?
- **Size.** The pane and the terminal differ in size. Crop, scale by scrolling, or
  show the pane's own size inside a frame?
- **Leaving.** Esc and Space both leave, per the words. Does `q` also, as it does
  in the list, or does `q` stay the list's quit so a double press cannot exit the
  whole view by mistake?
- **Read-only as a guarantee.** The view must send no keystroke to the pane. Is
  that a property of the code path (it never opens an attach) or does it need a
  test that fails when a key reaches the pane?
- **Remote clusters.** Because `capture` is an RPC, a quick look could work for a
  remote cluster where attach only prints a command. Is that so, and does the
  multicluster view change which cluster a row's capture goes to?
- **Narrow lists and the cursor.** With a filtered or sorted list, the quick look
  follows the cursor's identity. What happens if the seat vanishes while the view
  is open?

## Not now

Capture only. No keymap, no view, no code. This file holds the shape of the
request and what is already in the tree.
