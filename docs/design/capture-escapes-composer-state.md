# Capture with escapes, a read-only composer state, and the dim suggestion (marvel#571)

- **Status:** design for review, 2026-10-04, revision 2 (after review
  5407804684). Tracks marvel#571 and aae-orc-4tueh. Checked at marvel main
  57be868; every line reference below is at that commit.
- **Scope:** small. D1 narrows what the claude reader accepts; D2 and D3 are
  additive, and capture output without the new flags does not change.

## 1. Why

A reader of another seat's pane cannot tell a dim composer suggestion from a
typed draft, and marvel itself misreads one. After a finished turn, the claude
reader takes any composer text that is not the `Try "..."` placeholder as a
staged draft (`internal/composer/claude.go:106-132`). That reading permits a
clear (`CanClear` allows `HoldsText`, `internal/composer/composer.go:147-155`),
and on claude the clear key on a composer that is really empty arms exit
(the `CanClear` comment, `composer.go:141-146`). A stage that did not land can
also verify as confirmed (`Confirms(Stage, HoldsText)`, `composer.go:131-139`).

## 2. Today

| # | Fact | Where |
|---|---|---|
| F1 | `marvel capture` has `-S`, `-E`, `--repaint`, `--settle` and nothing else | `cmd/marvel/main.go:1697-1700` |
| F2 | `handleCapture` always captures plain, ranged or visible | `internal/daemon/daemon.go:2384-2388` |
| F3 | `CapturePaneEscapes` exists (`capture-pane -p -e`); there is no ranged escapes variant | `internal/tmux/driver.go:644-652`, `:866` (`CapturePaneRange`) |
| F4 | The daemon hands the claude reader only escapes captures: `readComposer` and `bareDigitRefusal` | `daemon.go:2257-2271`, `:3622`; `claude.go:27` |
| F5 | Composer state reaches a caller only through `verifyInject`, after an inject | `daemon.go:2292-2304` |
| F6 | The claude reader treats only the placeholder as dim; dim text beside a done line reads `HoldsText` | `claude.go:106-132` |
| F7 | No fixture holds a suggestion: the claude fixtures are empty-idle, staged-draft, three mid-turn cases, idle-after-submit | `internal/composer/testdata/claude/` |
| F8 | Three consumers act on a reading: `Confirms` (Submit accepts `Empty` or `MidTurn`, Stage accepts `HoldsText`), `CanClear` (`HoldsText` only), `bareDigitRefusal` (`Empty` or `MidTurn` only) | `composer.go:131-155`; `daemon.go:3624-3625` |

F6 is confirmed by reading and not yet reproduced live. Probe P1 (D1) captures
it.

## 3. Decisions

### D1. Composer text that starts dim reads Unknown

One rule, decided by the first visible character of the composer's first text
line, which is what `dimAtText` already reads (`claude.go:142`):

- **Dim first character:** `Unknown`, whatever the words, however many lines,
  whatever follows it. A dim head with a plain tail is `Unknown` too.
- **Plain first character:** today's reading (`HoldsText` beside a done line,
  `Unknown` without one), even if a dim tail follows.
- **The placeholder** (dim, one line, `Try "..."`) is checked first and still
  reads `Empty`. Nothing about it changes.
- **A plain capture** (no SGR at all) cannot show dim, so it reads as today.
  This rule does not reach it, and it does not need to: the daemon hands the
  claude reader only escapes captures (F4). Plain text beside a done line
  stays `HoldsText`, which existing tests pin (`claude_test.go:84-85`,
  `internal/daemon/inject_bare_digit_test.go:201`).

Why `Unknown` and not `Empty`: `Empty` is permissive in two consumers
(`Confirms(Submit)` and `bareDigitRefusal`, F8). Suppose claude draws a
collapsed paste chip (`[Pasted text #N +M lines]`) or an image chip dim, and a
bracketed-paste submit (`driver.go:550-608`) leaves the chip in the composer
because its Enter did not land. Reading `Empty` there would verify a submit
that never sent. `Unknown` confirms nothing and permits nothing in all three
consumers, so the change only narrows:

| Consumer | On main (suggestion reads `HoldsText`) | After D1 (`Unknown`) |
|---|---|---|
| `CanClear` | allows a clear | refuses |
| `Confirms(Stage)` | confirms | does not confirm |
| `Confirms(Submit)` | does not confirm | does not confirm (unchanged) |
| `bareDigitRefusal` | refuses | refuses (unchanged) |

The cost is on the safe side. A stage whose text claude draws dim would no
longer verify, and its clear would be refused; P1 shows the paste chip is not
such a case.
The caller sees "unconfirmed" where it saw "confirmed". It never sees the
reverse.

Probe P1 (read-only; the builder is running it): on panes showing (a) a dim
suggestion and (b) a collapsed paste chip, run
`tmux capture-pane -p -e -t <pane>` and `tmux capture-pane -p -t <pane>`, and
save both as fixtures `5-idle-suggestion` and `6-paste-chip`. Result (claude 2.1.289, read-only, private socket, from the marvel builder):

- **Suggestion:** dim, SGR 2 on the text only, not the prompt:
  `ESC[39m ❯ U+00A0 ESC[2m start with step 1 ESC[0m`. It appeared only after a
  turn that ended in a question; a plain "ok" turn left the composer empty. D1
  reads it `Unknown`.
- **Paste chip:** not dim. No SGR after the prompt's `ESC[39m`:
  `ESC[39m ❯ U+00A0 [Pasted text #1 +12 lines]`, with a hint line ("paste
  again to expand") under the lower rule. It is a real staged draft and keeps
  `HoldsText` under D1, so a staged paste still verifies and can still be
  cleared.

### D2. `marvel capture --escapes`

A new flag, `-e`/`--escapes`, sets `escapes` in the capture params, and
`handleCapture` reads through `CapturePaneEscapes`, or a new
`CapturePaneRangeEscapes` (`capture-pane -p -e -S -E`) when ranged. The
content goes to stdout byte for byte. Without the flag, the params and the
output are what they are today.

### D3. A read-only composer state, as a capture field, not a new verb

A new flag, `--composer`, reports the composer state beside the content as
`composer: <state>` on stderr, the way `repaint:` is reported. It types
nothing. A new verb was the alternative; a field reuses the capture path and
its output contract and adds one flag instead of a command.

**One nudge.** `readComposer` repaints before it reads (`daemon.go:2258`).
With `--composer`, `handleCapture` nudges once, whether or not `--repaint` is
also given, and `readComposer` is split so the read after that nudge does not
nudge again. `--settle` applies to that one nudge.

**One capture where it can be.** The state is read from a capture in the
reader's mode (escapes for claude). When that capture is also the content
(with `--escapes`, or a reader that reads plain), content and state come from
the same bytes and cannot disagree. Without `--escapes` on claude, the plain
content is a second capture taken straight after, and the two can disagree if
the pane changed between them; the CLI help says so. A runtime with no reader
reads `unknown`.

## 4. Tests

"Red" means it fails on main 57be868; the reason is read from the code, not
run. "Guard" means it passes on main and must keep passing.

| # | Test | Kind | On main, by reading |
|---|---|---|---|
| 1 | Fixture `5-idle-suggestion`: escapes reads `Unknown`; plain reads `HoldsText`, because a plain capture cannot show dim (D1, plain-capture bullet) | red | escapes reads `HoldsText` (`claude.go:131`); the fixture is also missing |
| 2 | Dim first character, with no done line, reads `Unknown` | guard | already `Unknown` (`claude.go:129-131`) |
| 3 | On the suggestion fixture's reading: `CanClear` refuses, `Confirms(Stage)` is false, `Confirms(Submit)` is false | red | `HoldsText` passes `CanClear` and `Confirms(Stage)` |
| 4 | Dim head, plain tail, beside a done line, reads `Unknown` (synthetic, marked as such) | red | reads `HoldsText` |
| 5 | Plain head, dim tail, beside a done line, reads `HoldsText` (synthetic, marked as such) | guard | `HoldsText` |
| 6 | Plain text beside a done line still reads `HoldsText` (`claude_test.go:84-85`), and an escapes capture of unstyled text still does (`inject_bare_digit_test.go:201`) | guard | pass |
| 7 | Fixture `6-paste-chip` reads `HoldsText` with escapes (its first character is not dim, P1) | red | fixture missing |
| 8 | The six existing fixtures keep both readings (`TestClaudeReaderReadsTheCapturedStates`, `claude_test.go:22`) | guard | pass |
| 9 | `handleCapture` with `escapes` returns content containing ESC; without it, content with no ESC, as today | red | `captureParams` has no `escapes`; unknown JSON keys are ignored, so no ESC |
| 10 | Ranged with `escapes` returns ESC in the ranged lines only | red | no ranged escapes variant |
| 11 | The CLI sends `escapes: true` only with `--escapes`, and `composer: true` only with `--composer` | red | the flags do not exist; cobra refuses them |
| 12 | `capture --composer` on a pane showing the suggestion fixture reports `unknown`, sends no keys (the pane's input is unchanged), nudges once with and without `--repaint`, and reports `unknown` for a runtime with no reader | red | the field does not exist |

**The seam for 9, 10 and 12.** There is no fake driver: `*tmux.Driver` runs
the tmux on `PATH` (`driver.go:83-95`). These tests run against real tmux on
the package's private server, which `TestMain` already sets up through
`MARVEL_TMUX_SOCKET` (`internal/daemon/testmain_test.go:13-25`), using the
existing seat helper `verifySeat` (`internal/daemon/inject_verify_test.go:32`),
which runs a script in a pane and waits for it, as
`inject_bare_digit_test.go:184-201` already does for a staged draft. They skip
where tmux is absent, as the package's other tmux tests do. The script takes
the shape of `quietSeat` (`inject_verify_test.go:25-28`): it prints the
fixture's bytes, then echoes each line it reads, so a key sent would show in
the capture. The existing verify tests already read such a seat as a composer,
not as `Shell` (`daemon.go:2267`). Tests 1 to 8 and 11 need no tmux.

## 5. Plan

One ticket, aae-orc-4tueh. D1 is the safety fix and could land on its own
first. D2 and D3 are additive and independent of it.

## 6. Rulings needed

None. D1 widens nothing: every consumer refuses or declines to confirm at
least as often as it does on main (table in D1). The one behaviour that moves
toward refusal, a dim staged text no longer verifying, is a false "unconfirmed",
never a false "confirmed".
