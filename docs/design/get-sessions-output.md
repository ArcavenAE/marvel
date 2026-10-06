# `get sessions` output: the daemon that answered, columns, and load

Accepted by the operator on 2026-10-06, with the rulings in section 9.

- Author: the architect seat, team arcaven.
- Issue: #601 (the team rollup, judged a separate design below). Tracks:
  aae-orc-f08m0 (column selector), aae-orc-o16q2 (token rate),
  aae-orc-vdcwm (WORKDIR column).
- Checked against marvel `origin/main` 6bc4961.
- Built by a four-round design party (seats: ux, observability, distsys,
  finops, substrate). Its record is outside this repo, so the lines this doc
  relies on are quoted here.

## 0. Why

`get sessions` cannot tell the operator which daemon answered, whether the
bus behind it is up, or whether that daemon is listening on the network. It
also cannot say which seats are working. On 2026-10-06 director counted 63
seats, all "healthy", while a pane snapshot showed none busy. The table
looked the same for a stalled fleet as for a working one.

The operator asked for these, verbatim: "displaying cluster name, nats
connection status, mrvl:// info (add an indicator for local only vs network
listening) customized columns, Tin/Tout and other token rate and other
proposed columns". A second question came the same day: "can we tell what
the work queue/load/duty cycle is across the fleet? who is free/busy/idle,
blocked, overloaded, unloaded?"

This design answers the first ask in full and the second in part. Section 6
says which part is left.

## 1. The premises, verified

| Premise | Where | Result |
|---|---|---|
| Today's header | `cmd/marvel/main.go:2626` | `WORKSPACE TEAM ROLE GEN AGENT NAME STATE HEALTH CTX% CPU% RSS DESK RUNTIME LLM`; no cluster, bus, token, age or load column |
| A bare `--mrvl` binds all interfaces | `internal/daemon/daemon.go:640-643`, `internal/daemon/sshserver.go:59` | the address defaults to `":" + DefaultMRVLPort`, passed to `net.Listen("tcp", addr)` |
| The bind is not reported | `internal/daemon/sshserver.go:27` | the listener is an unexported field; no API type carries it |
| Address resolution order | `cmd/marvel/main.go:64-82` | `--socket`, then `MARVEL_SOCKET`, then `--cluster`; an unknown cluster warns and dials the local socket (#586, #502) |
| The bus status already has a domain and a leaf state | `internal/bus/supervisor.go:119`, `:124-127` | `Domain`; `Leaf` is up, down, detached, unenrolled, n/a, or unknown ("not polled yet"). No probe age |
| "Leaf up" means a leafnode count above zero | `internal/bus/supervisor.go:721` | `up := body.Leafnodes > 0`; it does not mean the expected subjects are carried |
| Token spend exists but is not on the wire | `internal/usage/reader.go:28`, `internal/usage/accountant.go:708`, `internal/session/manager.go:1123` | `usage.Spend`; `SessionSpend` has one caller; `api.Session` carries no spend field |
| No token rate | `internal/usage/accountant.go` | cumulative sums and sample times only |
| The watchdog visits only quiet seats | `internal/daemon/watchdog.go:122-126` | a session is skipped unless `now - ContextAt` exceeds the window; it also starts no loop while the pattern set is empty (#598) |
| Marvel does not see bus sends | (no source) | the nearest per-seat activity signal is `ContextAt` (`internal/team/controller.go:1497-1522`) |
| Agent names embed team and role | `internal/team/controller.go:1285`, `:2217` | `<team>-<role>-g<gen>-<index>`; colliding team names are refused at apply since #378 |
| No column preference exists | `git grep session_columns` | 0 hits in Go |
| No `-o wide` | `cmd/marvel/main.go:945` | `-w` is `--watch` |

## 2. The daemon that answered: a header, not columns

Cluster, bus state and mrvl:// describe the daemon that answered, not a
session. They print as a header block above the table, built from one
daemon status call. `describe daemon` carries the full record from the
same call.

- **Stream.** On stdout, only when stdout is a TTY. `--header` forces it. A
  pipe gets a plain table, as today.
- **Cluster.** The resolved address, the rung that chose it (flag, env,
  cluster, default), and the daemon's own identity. Both names print when
  they differ. That makes #586 and #502 visible. It also catches the
  2026-10-04 mismatch, where two tools named the same cluster differently.
- **Bus.** The existing `Leaf` value, with its probe age. The word is "link
  up", never "connected": a leafnode count is not proof the subjects are
  carried. `unknown` is printed as `unknown`, never as down. #600's
  leaf-down duration supplies the age.
- **mrvl://.** Three values from the daemon's own `listener.Addr()`, read
  after `net.Listen`:
  - `off`: unix socket only;
  - `loopback`;
  - `network`: a wildcard bind counts as network.

  Neither the flag text nor client config is ever used. If the daemon
  cannot report its bind, the indicator is not built.

Example, for illustration only:

```
cluster  skippy (rung: cluster)   mrvl://  network :6785   bus  link up, 12s ago
```

## 3. One as-of cell

Every derived or status cell uses one shape, defined once:

- **Fields:** value, `observed_at`, `valid_until`, source.
- **States:** fresh, stale or none, the words admission readings already
  use (`internal/admission/admission.go:453`).
- **Rendering:** the existing three-state grammar. `-` means never
  measured. `?` means the reading expired. A value means fresh. A cell with
  no sample never prints `0`. Where a cell's source can be weaker than its
  name claims, the source is rendered as a mark on the value (section 4.4).

Levels that cannot go stale need no as-of: a bind address, an identity, or
a cumulative total. The rate, ACTIVE%, LAST-ACTIVE, the bus state and the
harness state all carry an age.

## 4. Columns

### 4.1 Selection

The selector ruled on aae-orc-f08m0 (2026-09-25):

- `--columns` takes long names, and their order is significant;
- the preference is `display.session_columns`;
- the flag overrides the preference;
- an unknown name is an error.

One addition: a named set `wide`, because no `-o wide` exists.

### 4.2 Width

A fit drops columns by a fixed priority. It never reflows. On a TTY it
prints one line on the header's stream: "N columns hidden at this width".

- AGENT NAME is never truncated: the pane verbs need the printed name
  (#337).
- RUNTIME shows its basename with `…`.
- WORKDIR is cut in the middle and keeps its tail.
- `--no-trunc` restores both.

The fit counts suffixed cells. A row like `healthy (stalled)` (17
characters) next to `failed (saturated)` (18) already reaches 79 columns
before LAST-ACTIVE.

| Width | Default columns, in priority order |
|---|---|
| 80 | AGENT NAME, STATE, HEALTH, CTX%, LAST-ACTIVE |
| 120 | adds LLM, TOUT, RATE |
| 200 | adds TEAM, ROLE, WORKDIR, AGE, ACTIVE% |

TEAM, ROLE and GEN restate the agent name, so they leave the narrow
defaults. Two teams stored before #378 may still share a name; for them,
TEAM stays reachable through `--columns` and `describe`.

### 4.3 Tokens and accounts

- **TOUT:** cumulative output tokens, quantized (`12k`, `1.4M`).
- **RATE:** a decaying output-token rate (aae-orc-o16q2). It is computed
  in `Accountant.Observe` and drawn through the as-of cell.
- **No Tin column.** Raw input is not comparable across harnesses: codex's
  layout counts cached input inside input, while Claude's adds it
  separately. PROMPT (`PromptTokens`, normalized for layout) goes in
  `wide` and `describe` instead.
- **Account budget, reset and credit stay in `get budgets`.** At most there
  is an opt-in ACCT key column. It shows the account's `LimitReading` as a
  key and a reading. It is never summed, averaged or thresholded across
  rows.

### 4.4 Activity

- **LAST-ACTIVE:** time since `ContextAt`. It is not LAST-OUT, because
  marvel cannot see bus sends; true LAST-OUT is a director fact.
- **ACTIVE%:** the share of evaluation ticks (`controller.go:1403`) in the
  last 15 minutes at which the session was not quiet.
  - **The predicate.** A session is quiet at time `now` when its
    `ContextAt` is zero or older than `now - W`. One function holds this
    test, and the rate and ACTIVE% call it. `evaluateActivity` calls it
    only for a session with a non-zero `ContextAt`. Its own path for a zero
    `ContextAt` (the observability check and the startup grace,
    `controller.go:1543-1552`) stays as it is.
  - **The window `W`.** It is the role's `activity_timeout` when declared.
    Otherwise it is the cluster's quiet window. That window is the
    operator's `watchdog.window` when set (`internal/config/config.go:183-196`),
    else `DefaultQuietWindow`, 10 minutes. The watchdog's
    `DefaultWatchdogWindow` (`watchdog.go:28`) reads the same constant.
    So one knob moves both the watchdog and ACTIVE%, and marvel has one
    default quiet window, not two.
  - **The default matters.** `activity_timeout` is opt-in
    (`internal/api/manifest.go:154-158`), and no manifest in this repo sets
    it (`git grep -l activity_timeout -- '*.toml' '*.yaml'` returns none).
    Without the default, ACTIVE% would have no window for any current
    role.
  - **What the default does not change.** `evaluateActivity` keeps its
    opt-in gate (`controller.go:1530-1531`), so the `(stalled)` advisory
    still fires only for roles that declare a timeout. Only ACTIVE% and
    the rate use the default.
  - **Where it reads `-`.** A session marvel has no activity channel for
    (`activityObservable`, `controller.go:1571`) reads `-`. So does a
    session whose 15-minute ring has not filled, including after a daemon
    restart.
  - It is counted per session in an in-memory ring and computed when read.

  What a measurement means depends on its source, and `describe` names the
  source along with `W`:
  - **Headless seats.** `ContextAt` moves when the usage accountant sees a
    token-bearing stream sample, so ACTIVE% means "tokens were moving".
  - **Statusline seats.** Every cooperative heartbeat stamps `ContextAt`
    (`internal/api/store.go:802`), so ACTIVE% means "the statusline
    reported". That is weaker than token flow and can read active while
    no work happens.
  - **Heartbeat seats.** The same as statusline seats.

  The difference shows in the cell, not only in `describe`:
  - an ACTIVE% or LAST-ACTIVE value from a statusline or heartbeat source
    carries a trailing `*` (`80%*`, `3m*`); a value from the token stream
    carries none;
  - when any cell on screen carries the mark, one legend line prints on the
    header's stream (stdout, TTY only, per V1): `* reported by statusline
    or heartbeat, not token flow`;
  - `--help` documents the mark.

  Piped output keeps the mark and drops the legend. The mark is part of
  the value; the legend is part of the header. This is the source field of
  the as-of cell (section 3), rendered.

  Either way, a seat in a long tool call reads quiet, and the number claims
  activity, not "busy". LAST-ACTIVE carries the same caveat per source.

The party renamed the requested BUSY% for this reason. Quoting the finops
seat: "BUSY% from rate measures generating, not working. Name it for what
it measures, or the fleet's '0 busy' finding gets replaced by a different
wrong number."

### 4.5 HEALTH and the watchdog

HEALTH stays liveness. The watchdog's harness state is its own as-of field.
It renders as a HEALTH suffix only when abnormal, the way `(stalled)` does
(`main.go:2694`). The full cell is in `describe` and an optional column.
Two watchdog items, `valid_until` and `uncovered`, are held for that design
party's rulings and are not in this plan.

## 5. Every number is diagnostic

None of these cells gates anything. No value blocks a spawn, a merge or a
scale (SOUL section 8, ADR-007). Ticket text for ACTIVE% says so.

## 6. Not in this design

- **QUEUE** needs director's ask ledger, which director owns. The
  interface it would share is keyed per seat: bus message id plus
  `in_reply_to`, with delivered and acked or answered times. Marvel reads
  the ledger's counts and never recounts bus traffic. Quoting the distsys
  seat: "Marvel must read the ledger, not recount from bus traffic, or two
  counts will disagree after a restart."
- **The team rollup (#601)** is a separate design (5 of 5). It aggregates
  across hosts, so it needs a per-host as-of and "n of m hosts answered".
  It also needs its own review against the diagnostic-not-gate rule.
- **#502's refusal.** Whether an unknown `--cluster` should exit nonzero
  was #502's own ruling, and this design only exposes the rung (vote V3).
  The operator has since ruled that it is refused (section 9, ruling 2).
  That change is its own ticket, P2b, filed against #502 beside this plan,
  not inside it.
- **Credits.** No codex payload fixture exists yet.

So the load question is answered in part. Idle and active are answered
here: ACTIVE%, LAST-ACTIVE, and STATE with `limited`. Blocked and waiting
on the operator need the ask ledger. Overloaded and unloaded need QUEUE,
#601, and a ceiling marvel does not yet define.

## 7. The vote

| Item | Result | Tally | Dissent |
|---|---|---|---|
| V1 header stream | stdout when it is a TTY; `--header` forces | 4-1 | ux: stderr to a TTY, so a piped run still shows which cluster answered |
| V2 first ticket | P1 and P2a in parallel | 4-1 | substrate: P2a first, since it needs no new type |
| V3 #502 refusal | expose the rung only; the refusal stays with #502 | 4-1 | distsys: a silent wrong-cluster read; an own ticket costs nothing |
| V4 `wide` | a named column set | 4-1 | distsys: a new surface for one cell |
| V5 hidden-columns note | one TTY line | 5-0 | none |
| V6 the plan | accepted with five amendments | 5-0 | none |

Two amendments conflicted. One removed the as-of edge from the rate
computation; the other required the rate to carry `valid_until`. The edge
moves to the RATE render (P9c). This reconciliation is the architect's, not
a vote.

**Post-party correction, not a party result; accepted as ruling 4.** The party settled ACTIVE%
5-0 as "a measurement inside the quiet window" but never defined that
window. Review found that the shared predicate returns nothing for any role
without `activity_timeout`, and that no manifest sets one. Section 4.4 now
makes three changes, each the architect's, after the party:
- it defines the window as the role's timeout, else the cluster quiet
  window, which the watchdog shares (`watchdog.window`, else 10 minutes);
- it keeps the `(stalled)` advisory opt-in;
- it states what a measurement means for each source, and renders the
  weaker source as a `*` mark on the cell with a TTY legend line, so a
  statusline 80% and a headless 80% no longer look identical.

P10, P11 and P12b carry tests to match; P11 and P12b gain a P4a edge for
the TTY seam the legend uses. The ruling list in section 9 covers it.

## 8. The plan

The tickets are filed flat in bd, with these edges, now that the operator
has ruled. "Deps" are `blocks` edges. Each ticket writes its red tests
first. Three notes from the review are applied:
- the width fit counts the source mark, so P11 and P12b depend on P6;
- a test names the `--help` line that documents the mark;
- the mark is part of the rendered value, so a JSON output keeps the source
  as its own field.

| # | Ticket | Red tests | Deps |
|---|---|---|---|
| P1 | As-of cell type and renderer | `TestAsOfDashWhenNeverObserved`, `TestAsOfStaleNeverPrintsNumber`, `TestAsOfJSONRoundTrip` | none |
| P2a | Expose the resolution rung | `TestResolveRungEnvBeatsCluster` | none |
| P2b | An unknown `--cluster` refuses nonzero and dials nothing (#502; ruling 2, outside this plan's chain) | `TestUnknownClusterRefuses` | none |
| P3a | Status reports daemon identity and the mrvl:// bind; `SSHServer` publishes its listener | `TestStatusReportsBoundAddrWhenMRVLStarted`, `TestStatusOffWithoutMRVLFlag`, `TestStatusWildcardBindIsNetwork`, `TestStatusReportsDaemonIdentity` | none |
| P3b | Leaf probe age on the bus status | `TestStatusLeafCarriesObservedAt` | P1; #600 merged |
| P4a | Header block, TTY seam, "link up" wording | `TestHeaderShowsBothNamesWhenDomainDiffers`, `TestHeaderRungNamesSocketEnvOverCluster`, `TestHeaderBindThreeValues`, `TestHeaderLinkUpWording`, `TestHeaderOmittedWhenStdoutNotTTY`, `TestHeaderFlagForcesWhenPiped` | P1, P2a, P3a |
| P4b | `describe daemon` | `TestDescribeDaemonCarriesBindAndRung` | P3a |
| P5 | Column selector and the `wide` set (aae-orc-f08m0) | `TestColumnsFlagOverridesPreference`, `TestColumnsOrderSignificant`, `TestUnknownColumnRejected`, `TestColumnsWideIsNamedSet` | none |
| P6 | Width fit, truncation, hidden-columns note | `TestFitDropsByPriorityAt80`, `...At120`, `...At200`, `TestFitCountsSuffixedHealthAndState`, `TestNameNeverTruncated`, `TestRuntimeBasename`, `TestLongWorkdirMiddleEllipsis`, `TestNoTruncRestores`, `TestColumnsOverflowWarns`, `TestHiddenColumnsNoteOnTTY`, `TestHiddenColumnsNoteSilentWhenNothingHidden` | P5 |
| P7 | Spend on the wire (`Out`, `PromptTokens`) | `TestSessionContextCarriesSpend`, `TestInteractiveSeatSpendAbsent` | none |
| P8 | Output-rate EMA in the accountant (aae-orc-o16q2) | `TestOutRateDecaysToZeroWhenSamplesStop`, `TestRateNilWhenNeverSampled`, `TestRateNotInstantaneousDelta` | none |
| P9a | PROMPT in `wide` and `describe` | `TestPromptTokensLayoutNormalized` | P5, P7 |
| P9b | TOUT cell | `TestSpendCellDashWhenAbsent` | P5, P7 |
| P9c | RATE cell | `TestRateCellSortsByValue`, `TestRateCellStaleRendersQuestion` | P1, P5, P8 |
| P10 | One quiet predicate and the cluster quiet window (`watchdog.window`, else `DefaultQuietWindow`), shared with the watchdog | `TestRateSnapsToZeroExactlyWhenQuiet`, `TestQuietWindowDefaultsWhenNoActivityTimeout`, `TestQuietWindowFollowsWatchdogWindowConfig`, `TestWatchdogWindowReadsDefaultQuietWindow`; characterization `TestQuietPredicateMatchesEvaluateActivityForObservedSessions` (non-zero `ContextAt` only, timeout set) | P8 |
| P11 | LAST-ACTIVE, with the source mark | `TestLastActiveFromContextAt`, `TestLastActiveDashWhenUnmeasured`, `TestLastActiveMarksStatuslineSource`, `TestFitCountsSourceMark` | P1, P4a, P5, P6 |
| P12a | Per-session tick ring | `TestTickRingHoldsFifteenMinutes` | P10 |
| P12b | ACTIVE% | `TestActivePctFromTickRing`, `TestActivePctDashBeforeWindowFills`, `TestActivePctDashAfterDaemonRestart`, `TestActivePctDashWithoutActivityChannel`, `TestActivePctUsesRoleTimeoutWhenSet`, `TestActivePctUsesDefaultWindowWhenUnset`, `TestActivePctStatuslineAndStreamRenderDifferently` (same 80%, two sources, two cells), `TestSourceLegendOnTTYWhenAMarkIsShown`, `TestSourceLegendAbsentWhenNoMarkOrPiped`, `TestDescribeNamesActivitySource`, `TestHelpDocumentsSourceMark`, `TestJSONKeepsSourceSeparateFromMark` | P1, P4a, P5, P6, P12a |
| P13 | ACCT key column, opt-in | `TestAcctColumnRepeatsNeverAggregates`, `TestAcctStaleReadingPrintsWordNotNumber` | P5 |

Seven tickets have no dependencies: P1, P2a, P2b, P3a, P5, P7 and P8. The WORKDIR
column (aae-orc-vdcwm) becomes selectable through P5.

## 9. Rulings

The operator ruled on 2026-10-06, verbatim via director: "design accepted
(1) and (2) unknown cluster should be refused (3) accept the 4-1 votes 4
accept the architect recommendation".

Director's mapping, which this doc follows:

1. The design is accepted.
2. V3: an unknown `--cluster` is refused (#502). This is built as P2b.
3. V1 to V4 stand as voted.
4. ACTIVE%'s window is the role's `activity_timeout`, else `watchdog.window`,
   else the 10-minute `DefaultQuietWindow`, and the `(stalled)` advisory
   stays opt-in (section 4.4).
