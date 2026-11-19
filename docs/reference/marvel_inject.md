## marvel inject

Send keystrokes to a session's pane (executive privilege)

### Synopsis

Send keystrokes to a session's pane.

Text is sent literally by default, so a tmux key name given as text types the
characters rather than pressing the key. Use --key for a control key:

  marvel inject <session-key> --key Enter        press Enter
  marvel inject <session-key> '' --enter         submit an existing draft
  marvel inject <session-key> 'hello' --key Enter   type, then press Enter

A codex seat is checked before any input: while it shows its "Update available"
menu, where one Enter or a newline runs the vendor's installer, the inject is
refused and nothing is typed. Escape alone is delivered, and skips the update
once (marvel#477).

--verify reads the composer after the keys went in (a nudge to repaint, a
settle, a capture) and says whether the effect was seen: submitted text should
leave the composer empty or the harness mid-turn, staged text should leave it
holding the text. It prints "confirmed" or exits non-zero with the composer
state ("unconfirmed"), and the daemon records session.inject-unconfirmed. A
harness without a reader reads "unknown", which never confirms, and a shell in
the foreground reads "shell" (no harness is running).

--clear discards a staged draft before the text is sent, with one C-c, and only
when a reader sees text in an idle composer. That works for claude today
(measured on 2.1.288), and only for a draft beside a finished turn's done line:
a pane whose reply has scrolled off carries no marker that shows it idle, so it
reads unknown. It is refused, with nothing typed, whenever the composer reads
empty (C-c would arm the exit prompt), mid-turn or streaming (C-c would
interrupt the turn), or unknown, and for codex and opencode, which exit on C-c
at an empty composer and have no reader that can see a draft yet.

```
marvel inject <session-key> [text] [flags]
```

### Options

```
      --allow-bare-digit   send a bare 1, 2 or 3 to a claude pane that might be showing a usage-limit menu (it never answers a menu marvel can see)
      --clear              clear a staged draft first (refused wherever no reader can vouch for it)
  -e, --enter              append Enter keystroke after text
  -k, --key stringArray    press a tmux key by name (Enter, Escape, C-c); repeatable, never sent as literal text
  -l, --literal            send keys literally (no special key interpretation) (default true)
      --ring               mark this inject as a ring: write the seat's ring claim first, and be refused while another ring holds it
      --settle duration    with --verify or --clear, how long to wait for a redraw (default is the repaint default)
      --verify             read the composer afterwards and exit non-zero if the effect was not seen
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
