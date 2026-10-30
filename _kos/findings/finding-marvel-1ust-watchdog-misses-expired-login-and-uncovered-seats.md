# finding-marvel-1ust: the watchdog sees the first-start login screen, not a login that expires mid-session, and an uncovered seat reads alive

**Date:** 2026-10-30
**Confidence:** frontier. Code read at marvel main 04c258f and PR #1143 head
1710bc23; live panes read on one fleet workstation, read-only, no key sent.
The expired screen's layout rests on one pane.
**Placement:** marvel. The subject is marvel's harness-state watchdog and its
surfaces; claude is the object it reads.
**Source:** a research pass for aae-orc-fxmi1 (watchdog Phase 1), started
from the report that seats showed "Login expired · Please run /login" while
`marvel get sessions` read them running and alive.
**Related:** #276, #554, #703, #1079, #1142, #1143; aae-orc-jgb4e,
aae-orc-trtbm; director#431.

## Why this matters

A seat whose login expired is a live process in a live pane, so it reads
alive and keeps receiving work it cannot do. The watchdog exists to catch
exactly this, and on the measured host it could not have seen any of the
seats in the report.

## The finding

1. **The one pattern is a different screen.** The shipped set
   (`internal/panestate/patterns/claude/2.1.290/logged-out.yaml`) is the
   first-start login-method picker. Its own comment says a login that expires
   mid-session may draw a different screen and reads unknown (#1079).
2. **The expired notice is not at the bottom.** On the one pane that showed
   it (claude 2.1.310, 80x24), the notice sat 18 rows above the bottom, more
   transcript followed it, and an empty composer box sat below. The pattern
   model matches a block only at the bottom of the screen
   (`docs/design/harness-state-watchdog-p1.md` section 4), so no new pattern
   in that model can match this screen. The composer reader reads that
   composer as empty, so the PANE column in #1143 would likely show `idle`
   (read from `internal/daemon/pane_column.go`, not run).
3. **Most seats are uncovered, and uncovered is silent.** The set covers
   claude 2.1.285 to 2.1.293. On the measured host, 20 claude panes ran
   2.1.289 to 2.1.314, one of them in range. An uncovered version is stamped
   and never captured (`internal/daemon/watchdog.go:290-298`), and
   `get sessions` suffixes HEALTH only for logged-out and parked
   (`cmd/marvel/main.go:3364-3369`). An uncovered seat reads bare `alive`.
4. **Release churn outpaces capture.** About 21 claude patch releases landed
   in the 23 days since the range was set (2026-10-07). Capture per release
   is the maintenance cost the design chose; at this rate it does not keep up.
5. **The pure core is sound and composes.** `panestate.Classify` is a pure
   function over rows, a harness name and a version (`panestate.go:263`); the
   daemon loop is one consumer and PANE another. Capture cost is not the
   limit: #1143 timed about 1.5 ms per pane for two tmux calls.

## What this suggests (recommendations, each for a ruling)

- Detect a mid-session expiry from the claude transcript's typed API error
  (an `isApiErrorMessage` entry with `error` `authentication_failed`), read as
  typed fields only. Its trigger cannot be today's quiet gate: every
  heartbeat stamps `ContextAt` (`internal/api/store.go:1015`), the gate
  measures from it (`internal/daemon/watchdog.go:271-277`), and a logged-out
  seat was observed to keep beating, so the gate never opens for it. Key the
  check on the transcript itself (no new transcript entry since the typed
  auth failure), or on beats that carry new token totals. A pane string
  match alone stays low confidence, since a transcript can quote the notice.
- Let a full bottom-aligned picker match on a claude version above the
  verified maximum set `logged-out`, marked unverified in `describe`, with the
  uncovered-version event still firing until a sample lands.
- In PANE, give a seat whose watchdog coverage is `uncovered` or
  `control-failed` a GAP line naming the version, so an unread seat does not
  read as a healthy `idle`.
- Keep every count here (uncovered seats, covered versions, sample age)
  diagnostic. Pattern controls, schema checks and the masking test may gate.

## Open questions

A. Answered in code: yes. Every heartbeat stamps `ContextAt` with the beat
time (`internal/api/store.go:1015`), and the did-work clear
(`internal/daemon/watchdog.go:262`) drops a screen-describing state once
`ContextAt` moves past it. marvel#801 records the mechanism on an idle
seat: the statusline beat re-stamps every 15 s whether or not the seat
works. That a logged-out seat keeps beating is inferred from it and from
the seats in this finding's source report, which read running and alive
while their panes showed the expired notice; no beat log of a logged-out
seat was read. So for a seat that keeps beating, the quiet gate is closed
by design.

B. The watchdog captures client-workspace panes while COMPOSER and PANE never
do. Is the asymmetry intended?

## Evidence

- marvel main 04c258f: `internal/panestate/panestate.go:61`, `:263`;
  `internal/panestate/control.go:30`; `internal/daemon/watchdog.go:26`,
  `:251-320`, `:408-421`, `:466`; `internal/runtime/adapter.go:786`;
  `internal/runtime/claude.go:352`; `internal/api/types.go:1288-1340`.
- PR #1143 head 1710bc23: `internal/daemon/pane_column.go`.
- Pane versions: `tmux list-panes -a -F '#{pane_current_command}'`, 2026-10-30
  20:59Z. The notice's position: row tags only (hit, rule, blank, other), no
  row text kept.
