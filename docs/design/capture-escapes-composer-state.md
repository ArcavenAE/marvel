# Capture with escapes, a read-only composer state, and the dim suggestion (marvel#571)

- **Status:** design for review, 2026-10-04. Tracks marvel#571 and
  aae-orc-4tueh. Checked at marvel main 57be868; every line reference below is
  at that commit.
- **Scope:** small and additive. Existing capture output does not change.

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
| F4 | `readComposer` captures with escapes when the reader asks (claude does) | `daemon.go:2257-2271`; `claude.go:27` |
| F5 | Composer state reaches a caller only through `verifyInject`, after an inject | `daemon.go:2292-2304` |
| F6 | The claude reader treats only the placeholder as dim; dim text beside a done line reads `HoldsText` | `claude.go:106-132` |
| F7 | No fixture holds a suggestion: the claude fixtures are empty-idle, staged-draft, three mid-turn cases, idle-after-submit | `internal/composer/testdata/claude/` |

F6 is inferred from the code, not reproduced. It needs a real capture (D1).

## 3. Decisions

### D1. The reader: text dim from its first visible character is not a draft

A typed draft is never drawn dim. So when every visible character of the
composer's text is dim (SGR 2):

- beside a finished turn's done line it reads `Empty`, whatever its words;
- with no done line it reads `Unknown`, as a draft does today.

Text whose first visible character is not dim keeps today's reading
(`HoldsText` beside a done line), even if a dim tail follows it: a real draft
is present. The placeholder path is unchanged.

The rule is checked against a real capture of a live suggestion. Probe P1
(read-only): on a pane showing a dim suggestion, run
`tmux capture-pane -p -e -t <pane>` and `tmux capture-pane -p -t <pane>`, and
save both as fixture `5-idle-suggestion` beside the others. Until the fixture
exists, test 1 fails for want of it, which is the point: the reader's shapes
come from real captures (`claude.go:8-11`).

### D2. `marvel capture --escapes`

A new flag, `-e`/`--escapes`, sets `escapes` in the capture params, and
`handleCapture` reads through `CapturePaneEscapes`, or a new
`CapturePaneRangeEscapes` (`capture-pane -p -e -S -E`) when ranged. The
content goes to stdout byte for byte. Without the flag, the params and the
output are what they are today.

### D3. A read-only composer state, as a capture field, not a new verb

A new flag, `--composer`, returns `readComposer`'s state beside the content as
`composer: <state>` on stderr, the way `repaint:` is reported. It types
nothing. Like `--repaint`, it nudges the pane's size to get a fresh paint,
because `readComposer` does (`daemon.go:2258`). A runtime with no reader reads
`unknown`. A new verb was the alternative. A field reuses the capture path and
its output contract, and adds one flag instead of a command.

## 4. Tests (red first)

| # | Test | Fails today because | tmux |
|---|---|---|---|
| 1 | Fixture `5-idle-suggestion`: escapes reads `Empty`, plain reads `Unknown` | the reader returns `HoldsText` (or the fixture is missing) | no |
| 2 | Dim-only composer text with no done line reads `Unknown` | the rule is absent | no |
| 3 | `CanClear` refuses on the suggestion fixture's state; `Confirms(Stage, ...)` is false | `HoldsText` allows both | no |
| 4 | A plain first character with a dim tail, beside a done line, still reads `HoldsText` (synthetic, marked as such) | guards D1 against over-reach | no |
| 5 | The six existing fixtures keep their readings (`TestClaudeReaderReadsTheCapturedStates`, `claude_test.go:22`) | regression guard | no |
| 6 | `handleCapture` with `escapes` calls the escapes capture and returns content with ESC; without it, the plain call and identical output | the flag does not exist | no (fake driver) |
| 7 | Ranged with `escapes` calls `CapturePaneRangeEscapes` with the same bounds and `-e` | no ranged escapes variant | no |
| 8 | The CLI sends `escapes: true` only with `--escapes`, and `composer: true` only with `--composer` | the flags do not exist | no |
| 9 | `capture --composer` returns the state `readComposer` gives for the session, sends no keys, and reads `unknown` for a runtime with no reader | the field does not exist | no (fake driver) |

## 5. Plan

One ticket, aae-orc-4tueh. D1 is the safety fix and could land on its own
first. D2 and D3 are additive and independent of it.

## 6. Rulings needed

None. All three changes are additive, and D1 only narrows what can be cleared.
