## marvel capture

Capture a session's pane content

### Synopsis

Capture a session's pane content.

Without flags, captures the visible area. -S with a negative value reaches
into scrollback; an omitted -E defaults to the bottom of the visible area.

Full-screen TUI harnesses (interactive claude and friends) run on the tmux
alternate screen, which has no scrollback — captures of those sessions cap
at the visible screen regardless of -S.

A capture is what the program last painted, not necessarily what is on its
screen now: a TUI may repaint only on input or on a resize. --repaint asks the
program to redraw first: marvel widens the pane's own terminal by one column,
holds for --settle, puts the size back, waits --settle again, and then reads.
The programs repaint on a change of size, not on a bare SIGWINCH, and nothing is
signalled by process id or changed in tmux. It reports "repaint: signalled"
(the size was nudged), "repaint: signalled, restore skipped: size changed
underneath" (something else resized the pane meanwhile, so the original size
was left alone) or "repaint: unavailable: <reason>" on stderr, with the name of
the foreground program. A nudge is not a redraw seen: whether a given harness
repaints is its own behavior (measured for claude, codex and opencode; not for
others). A seat sitting at a shell prompt has nothing to repaint; the result
names the shell. A failed repaint never fails the capture.

-e/--escapes keeps the pane's escape sequences (tmux capture-pane -e), byte for
byte on stdout, so text drawn dim, such as a composer suggestion, can be told
from a typed draft. --composer reports the composer state marvel's own reader
gives the session ("composer: empty", "holds_text", "mid_turn", "unknown", ...)
on stderr. It types nothing. It nudges the pane's size once (with or without
--repaint), because the reader repaints before it reads; --settle applies to
that one nudge. The state is read from a capture in the reader's mode (escapes
for claude). With --escapes the content is that same capture, so content and
state cannot disagree. Without --escapes on claude the plain content is a
second capture taken straight after, and the two can differ if the pane changed
between them. A runtime with no reader reads unknown.

A plain capture drops dim text's attribute. When it did, it writes one line to
stderr ("capture: dim text removed; ...") and leaves stdout as it was; -e and
--composer carry no such line, since they already answer the question. The line
comes from a second read of the pane taken straight after the plain one, so a
pane that changed between the two reads can give a line with no dim text in
what you got, or miss one. Treat it as a hint to ask with -e, not as a verdict.

```
marvel capture <session-key> [flags]
```

### Options

```
      --composer          also report the composer state marvel's reader gives (types nothing; nudges the pane's size)
  -E, --end int           end line (default bottom of visible)
  -e, --escapes           keep the pane's escape sequences (dim text, colours), byte for byte
      --repaint           ask the program to redraw first (widen the pane one column and restore it), then read
      --settle duration   with --repaint or --composer, how long to wait for the redraw before reading (default 300ms)
  -S, --start int         start line (negative for scrollback; default top of visible)
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel](marvel.md)	 - Agent orchestration control plane
