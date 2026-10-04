# Harness-state watchdog, Phase 1: a logged-out seat, seen

Design for review. No code lands until this doc is reviewed.

- Author: the arcaven architect seat.
- Ticket: aae-orc-fxmi1. Issue: #276 (detect, classify and remediate frozen
  sessions). Idea: `_kos/ideas/tmux-harness-state-watchdog.md` (#288).
  Commission: the operator ("yes", via director), through the team
  supervisor. The marvel builder builds it after review.
- Checked against marvel `origin/main` on 2026-10-04.

## 1. The problem, verified

A seat at a login prompt is a live process in a live pane, so HEALTH reads
`healthy` (finding-025), and it does no work. #276 records the fleet-wide
case: every seat froze at its next call and was found by eye, one pane at a
time. Phase 1 makes that one state visible: **logged-out**. It classifies and
surfaces; it never acts on the pane.

| Premise | Command | Result |
|---|---|---|
| Nothing detects it today | `git grep -n GenericScraper origin/main -- '*.go'` outside tests | defined in `internal/runtime/generic.go`, no call site |
| The ticket's named primitive fits | read `generic.go:31-75` | **it does not.** `GenericScraper.Scrape` returns only history lines past a high-water mark and never re-emits one. A login prompt already on screen when the watchdog first looks, or seen once, is never returned again. The watchdog needs the **visible screen**, the `capture-pane -p` form (`Driver.CapturePane`), which is also what `usage-limit-pause.md` 9.4's matcher reads |
| The quiet signal exists | `evaluateActivity`, `internal/team/controller.go:1529-1552` | `ActivityStalled` from `SessionContext.ContextAt` against the role's `activity_timeout` |
| Any role opts in to it | `grep -rl activity_timeout` over the orc's manifests, with a positive control (`restart_policy` or `replicas` finds 4 manifests) | **none**, and `marvel get sessions` shows no `(stalled)` row. A gate that waits for `ActivityStalled` would never fire |
| The token-rate trigger exists | `aq show aae-orc-o16q2` | open, not built. Phase 1 cannot consume it |
| A captured logged-out screen exists | the fixtures tree; #276's text | none. #276 quotes "401 ... token expired / please run /login" from memory of the incident, not a capture. Patterns are not guessed (section 4) |

## 2. The shape

One state, one adapter (claude), the whole path:

```
gate (cheap, no capture)  ->  visible capture  ->  normalize  ->  match a
versioned pattern set  ->  state + confidence + evidence  ->  surface
```

The watchdog never sends a key, never injects text, never restarts a
session, and never changes a session's `State` or `HealthState`. A
logged-out seat restarted by marvel comes back logged out and burns the
restart budget; the value is the state becoming visible. That also keeps it
clear of the open tension in `_kos/ideas/approve-by-keyboard-over-the-bus.md`
(#542): nothing here types into a pane, so nothing here can carry, or
counterfeit, an approval.

## 3. The gate

A running session with a live pane and a live process is a candidate only
when the pane's **foreground process is the harness marvel spawned there**
(option (a) of the review). Two facts decide it:

- **Which harness:** marvel's own record. The session's runtime name selects
  its adapter from the registry (`Registry.Resolve`, `internal/runtime/adapter.go:361-368`), the
  same lookup the spawn used. Nothing is read from the pane for this.
- **Whether it is in front:** `#{pane_current_command}` (already read by
  `ListPanes`, `internal/tmux/driver.go:890`) is checked by that adapter's
  foreground rule, an optional interface in the existing pattern
  (`SessionIDAssigner`, `adapter.go:197-215`); an adapter without one is never
  a candidate. claude's rule: the command is a semver-shaped string
  (`^[0-9]+\.[0-9]+\.[0-9]+$`) or `claude`. Measured read-only on one fleet
  workstation, every claude pane reports its **version** there: 2.1.285 (7
  panes), 2.1.283 (6) and 2.1.284 (1), all live at once. A rule keyed on the
  version recorded with a sample would drop every seat on another version,
  exactly when an update is likeliest to log seats out.

The same field then supplies the session's harness version for choosing a
pattern set, which is what makes the `low` tier reachable: a seat on 2.1.285
matched by a 2.1.283 pattern set is a candidate, matches, and reads `low`.
A seat with a pager or an editor in front (`less` showing the sample or
incident text, the block at the bottom) fails the foreground rule and is not
a candidate, however quiet. Then **either**:

1. **Quiet after work:** `ContextAt` is non-zero and older than the
   watchdog window (default 10 minutes); or
2. **Never worked:** `ContextAt` is zero and the session is older than the
   window. This is the fleet case: a seat spawned into a logged-out harness
   never produces its first sample.

The window is the watchdog's own setting (a new key, `watchdog.window` in
`~/.marvel/config.yaml`), not the role's `activity_timeout`, so the gate does
not depend on an opt-in that no role uses. A role that does set
`activity_timeout` gets its own window instead. When o16q2's rate ships, a
rate above zero removes a session from the candidates; until then the two
rules above are the whole gate.

A candidate is captured at most once per window per session. A session that
leaves the gate (its `ContextAt` advances) is not captured again. The capture
costs no model tokens; the gate bounds CPU and keeps the matcher away from
busy panes whose output is full of error-looking text.

## 4. Matching

**Input.** The visible screen from `Driver.CapturePaneJoined`
(`capture-pane -p -J`, `internal/tmux/driver.go:636`), no scrollback.
Joining wrapped lines removes the pane width from the match, so a sample
taken at one width matches a seat at another; samples are captured the same
way, and the pane width is recorded beside each.

**Normalize.** Strip trailing spaces per row and drop trailing blank rows.
`capture-pane -p` without `-e` emits no escape sequences, so there is no ANSI
to strip; the normalizer refuses input that contains an ESC byte rather
than guessing. The refusal's error names the pane and the byte offset only,
never the row's text, since that row may hold a span to be masked.

**Patterns are data,** in `internal/panestate/patterns/claude/<harness
version>/logged-out.yaml`, built from captured samples stored beside them as
fixtures. A pattern is a block of consecutive rows, matched whole at the
**bottom** of the normalized screen, with at most one marked variable span
per row (as 9.4 of `usage-limit-pause.md` does for the menu's time). No
substring match inside a row, no match above the last block.

**Confidence.**

- `high`: the whole block matches at the bottom, for a pattern set whose
  harness version equals the session's.
- `low`: the whole block matches, but the pattern set is for a different
  harness version, or only some rows match. Reported as `unknown`, with the
  evidence kept for `describe`. That evidence is **rendered from the
  pattern** too: its fixed rows, and `<masked>` for each variable span. A
  partial match shows only the pattern rows that matched, never a captured
  row that did not.
- A capture error, an empty screen, or no pattern set for the harness: no
  state, `unknown`.

Only `high` sets `logged-out`. A confident wrong answer is the injury; an
`unknown` is a fine answer.

**Samples come first.** Probe P-WD1 (section 7) captures them. Until a
sample exists for a harness version, that version has no pattern set and the
watchdog reports nothing for it.

**Shared with the usage-limit menu.** The pane-menu source of
`usage-limit-pause.md` 9.3 (held until UL-R2 is answered) needs the same
pieces: the visible capture, the normalizer, row-block matching, versioned
samples and the "does this still match" test. Phase 1 builds them once, as
`internal/panestate`. If UL-R2 is answered in favor of the pane-menu source,
it becomes a second pattern set in the same package, not a second matcher.

## 5. Surfaces

- **`get sessions`:** HEALTH carries `(logged-out)`, the same suffix idiom as
  `(stalled)` (`cmd/marvel/main.go:2638-2646`). No new column, no change to
  `State` or `HealthState`.
- **No captured text leaves the matcher unmasked.** A login screen's
  variable span is exactly where an auth URL or a device code sits. Every
  surface carries the pattern's id and version and the pattern's **fixed**
  rows; each variable span is replaced by `<masked>`. The raw capture is
  never stored, logged or published.
- **`describe session`:** `Harness state: logged-out (high, claude
  2.1.x, pattern logged-out@1, captured 04:12Z)` and the fixed rows, masked
  as above.
- **Events:** `session.harness-state` with the state, confidence, pattern
  id and version, and the masked rows, on the event ring at warning
  severity, and `session.harness-state-cleared` when it ends. At warning, the
  zhx6x tap carries them to the bus once it ships. **No bus consumer may act
  on these events before Phase 3**: they inform an operator, and acting on a
  pane from them is the inject-as-approval shape #542 leaves open.
- **Fleet roll-up:** when three or more seats on one account read
  `logged-out` within one window, one `account.logged-out` event names them,
  so a fleet-wide expiry reads as one fact, not thirty.

**Clear:** at the first of: `ContextAt` advances; a capture in which the
block no longer matches; the session ends.

## 6. Tests (red first)

1. A fake driver whose visible screen is the P-WD1 sample, session quiet past
   the window: `logged-out`, `high`, the evidence equals the sample's fixed rows, with each variable span masked.
2. A pager in front: the P-WD1 sample displayed in `less` at the bottom of
   the pane, `pane_current_command` = `less`, session quiet past the window:
   not a candidate, never captured, no state. The same screen with the
   harness in front (`pane_current_command` = the sample's version):
   `logged-out`.
3. The sample's block with a busy session's output below it: no match (the
   block is not at the bottom).
4. The sample's rows quoted inside a tool result in the middle of the
   screen: no match.
5. A session whose `ContextAt` is 2 minutes old: never captured.
6. A session with `ContextAt` zero, created 11 minutes ago: captured once;
   created 9 minutes ago: not captured.
7. A pattern set for another harness version that matches: `unknown`, low
   confidence kept in `describe`, nothing in HEALTH.
8. A capture error: `unknown`, no event.
9. `ContextAt` advancing after `logged-out`: cleared, one cleared event.
10. A guard over every code path in `internal/panestate` and its caller:
    nothing calls `Driver.SendKeys` (`driver.go:573`), `TmuxInstance.Inject`
    (`internal/runtime/instance_tmux.go:162`), a restart path, or any store
    write other than the harness-state fields.
11. Three seats on one account `logged-out` in one window: one
    `account.logged-out` event naming all three.
12. The rot test: each stored sample is matched by its own pattern set; a
    sample edited by one character in a fixed span is not.
13. Masking: a sample whose variable span holds
    `https://example.invalid/device?code=ABCD-1234`: the event payload, the
    `describe` output and the daemon log contain neither the URL nor the
    code, and do contain `<masked>` in that row. The same with the URL long
    enough to wrap at the pane edge (joined by `-J`), with a `low` partial
    match, and with an ESC byte in that row (the refusal path): in none of
    them does the URL or code appear anywhere.
14. Cross-version: a pattern set captured on 2.1.283; three seats in front
    with `pane_current_command` 2.1.283, 2.1.285 and `claude`. The first is a
    candidate and reads `high`; the second and third are candidates, match,
    and read `low` (`unknown` in HEALTH, masked evidence in `describe`). A
    seat whose runtime is not claude, with `2.1.283` in front, is not judged
    by claude's rule and is never captured (zero captures).

## 7. Probe P-WD1 (builder-run, scratch only)

The aim is a logged-out screen produced without touching the fleet: on a
scratch tmux server (`tmux -L wd1-scratch`), start the harness with an empty
config directory (`CLAUDE_CONFIG_DIR=$(mktemp -d)`) and no credentials in
the environment, wait for it to settle, and capture the visible screen, the
harness version and `pane_current_command`.

- **Open question: where the harness keeps its login, per OS.** On macOS it
  may use the system keychain, so an empty config directory may not isolate
  the scratch session from the operator's login. This is not answered by
  probing: no seat inspects the harness binary or the keychain to find out
  (a seat that tried was refused as credential exploration, and the refusal
  is the design). It is asked of the operator or of the harness's own
  documentation.
- **Stop condition.** If the first capture shows a logged-in prompt, stop:
  the scratch session is not isolated. Kill the scratch server
  (`tmux -L wd1-scratch kill-server`), record that, and capture nothing
  further.
- **The positive control is operator-run.** A logged-in capture needs a real
  credential, so the operator runs it, with their own login, and hands over
  only the capture. No seat runs it.

That gives the "never logged in" screen.

The "token expired mid-session" screen (#276 cause 2) cannot be produced on
purpose without spending or breaking a real credential. It is captured at the
next real occurrence, by the operator or with the operator's word, and added
as a second sample. Until then Phase 1 detects only what P-WD1 captured, and
the design says so.

## 8. Rulings needed

1. **The window.** Default 10 minutes. Alternative: reuse each role's
   `activity_timeout` and stay silent where it is unset. Expiry: the default
   holds when the build starts.
2. **Sessions whose role has no `activity_timeout`.** Default: watched (the
   advisory changes nothing about the session). Alternative: opt-in only,
   which today means no session is watched.
3. **One package with the usage-limit menu** (section 4). Default: yes,
   `internal/panestate`, built here first. It does not decide UL-R2.

Out of scope, each its own ticket when committed: the wider taxonomy
(waiting-approval, waiting-input, rate-limited, stuck), other harnesses'
patterns, any action on a classified state (Phase 3), and `deaf`
(`_kos/ideas/deaf-seat-status.md`, which reads the broker, not the pane).
