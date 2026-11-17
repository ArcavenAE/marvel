# Cluster totals above the sessions table

**Status:** proposed design, view only. It is promoted from the idea `_kos/ideas/cluster-rollup-header.md`. Issue: marvel#1477.

- **Author:** the architect seat, team arcaven.
- **Checked against:** marvel `origin/main` 182fadf0 (2026-11-17), after a three-seat design party. The first version was checked at ba0ea659.
- **Valid until:** 2026-11-24, or until the first PR for it opens, whichever comes first. The author re-checks it then.

## 0. Why

To tell how loaded a cluster is, a reader of `marvel get sessions` adds the mCPU, RSS, TOUT, RATE and COST columns by eye. Every input for a total is already on the rows the CLI receives, so a header line that sums them costs no daemon change, no new RPC and no new state.

It is a view: nothing reads the totals but a person.

## 1. The premises, verified

Each line was checked at 182fadf0 with `git show` or `git grep` at that commit.

| Claim | Where |
|---|---|
| CPU shows as mCPU, millicpu as Kubernetes reads it (1000 is one core), an integer | header `columns.go:41`; cell `fmt.Sprintf("%d", api.MillicpuFromPercent(s.CPUPercent))`, `cmd/marvel/main.go:3598` |
| A row with no sample prints `-` for mCPU and RSS, and so does a crashed, failed or succeeded row, whose last reading is frozen | `cmd/marvel/main.go:3591-3595` |
| TOUT is cumulative output tokens since the seat's own start; nil is never metered, not zero; `+` marks a lower bound (the sum began mid-session) | `toutMarked`, `cmd/marvel/tout.go:25-31`; `SpendOut`, `internal/api/types.go:666` |
| TIN, CR, CW and PROMPT use the same cell and the same `+` flag | `cmd/marvel/cachesplit.go:17,20,23`; `main.go:3684` |
| RATE is tokens a second: a value while fresh, `?` once expired, `-` never sampled, each judged under its own seat's quiet window | `rateCell`, `cmd/marvel/rate.go:29-55` |
| COST is opt-in: the harness's running list-price total, `~$`, or `-` when the feed never carried one | `costCell`, `cmd/marvel/cost.go:21-26`; `columns.go:71` |
| mCPU, RSS, TOUT, RATE, TIN, CR, CW, PROMPT and COST are opt-in columns, shown by the width tier or by `--columns` | `optInColumns`, `cmd/marvel/columns.go:76` |
| HIT% is a ratio, in the same wide set | `columns.go:57` |
| The daemon header prints above the table on a TTY or with `--header`, never into a pipe | `printSessions`, `main.go:3910-3920`; `renderSessionsParts`, `main.go:3938` |
| `-w` cuts the table to the screen height around a cursor, counting `used`, `pre` and `post` lines, and prints `showing rows %d-%d of %d` when it cuts | `ws.sessionsView`, `cmd/marvel/watchselect.go:181-237` (budget at `:201`) |
| Single-cluster `-w` keeps the last rows when the daemon is late (`data as of`) or disconnected (`last known state:`) | `main.go:3810-3826` |
| `get sessions` takes no row filter; the scope is the cluster set (`--cluster a,b`, `--all-clusters`) | flags `main.go:1385-1415`; `clusterSet`, `cmd/marvel/fanout.go:327` |
| A fan-out read prints one stderr status line per cluster, then one merged, sorted table with an empty header | `statusLine`, `fanout.go:100`; `runFanOutGet`, `fanout.go:418-464` |
| `answered()` is true for answered, starting ("rows may be incomplete") and same_daemon | `fanout.go:92-95` |
| Two cluster names that reach one daemon are folded: the member keeps only the rows the leader lacks and is marked same_daemon | `foldSameDaemon`, `cmd/marvel/samedaemon.go:174`; called at `samedaemon.go:166` and `watchfan.go:294` |
| The fan-out watch frame keeps a failed cluster's last good rows, skips a repeat address's rows, and renders through `ws.sessionsView` with the status block as `used` | `watchfan.go:232-236`, `:284-311`, `:323`; `data as of` at `:331` |
| `api.TeamLoad` carries `ActiveMeanPct` and `ActiveMeasured` per team, and the mean is the per-seat sum over `ActiveMeasured` | `internal/api/teamload.go:24-28`; `RollTeamLoad`, `:41` |

## 2. The line

One line, `totals`, directly under the daemon header line. It follows the header's stream rules exactly:
- it prints on stdout when stdout is a TTY, or with `--header`;
- a pipe gets the plain table;
- `-o json|yaml` documents are unchanged, because a document consumer sums the rows itself.

Example, for illustration only (a wide terminal, COST selected):

```
cluster  local (rung: default)   mrvl://  off   bus  link up, 12s ago
totals   37 sessions   mCPU 14600 (35)   RSS 24.1G (35)   TOUT 1.9M+ (31, 2 partial)   RATE 2.1k/s (9 fresh)   COST ~$4,893.10 (16)
```

### 2.1 Which fields print (ruled)

**A total appears only for a column that is shown** (director, 2026-11-17, relayed by the supervisor). The line reads the same column list the fit drew, so a column hidden at this width has no field, and `--columns` brings it back. `sessions` always prints.

### 2.2 What each field sums

The rule for every field: **a total counts exactly the cells the rows show as values, and nothing else.** The line reuses the row code's own predicates (the gone rule, `toutMarked`, `rateCell`'s state, `costCell`), so a total and its column never disagree about which rows count. Every field prints the number of rows it summed, in parentheses, and the help text names each field as below.

- **sessions:** the number of rows the daemon returned for this line's scope (2.5).
- **mCPU:** the integer sum of the mCPU cells that print a value, under the column's own label. A crashed, failed or succeeded row, or one never sampled, adds nothing and is not counted. Help: "sum of the seats' millicpu".
- **RSS:** the sum of RSS over rows that print a value, in the cell's byte format. Shared pages count once per process, so the sum overstates real memory. Help: "sum of resident set sizes", never "memory used".
- **TOUT, TIN, CR, CW, PROMPT:** each the sum of its column's non-nil cells. Help: "sum of cumulative counts since each seat started", never a per-period figure.
- **RATE:** the sum of fresh cells only; `?` and `-` add nothing and are not counted, and the count reads `(N fresh)`. Help: "sum of fresh rates", never "cluster rate", because each seat's rate is judged under its own window.
- **COST** (ruled): the sum of non-nil `CostTotalUSD`, printed `~$` with two decimals, as the cell is. A `-` cell adds nothing and is not counted. Help: "sum of the seats' running estimates at list price; not a bill". It covers statusline seats only, which is why its count matters.
- **HIT%** and every other column: never summed.

TOUT and COST are separate fields. Neither feeds the other, and no total converts tokens to dollars.

### 2.3 Rows that report nothing, and lower bounds

- **A row with no value is left out of a sum and is never counted as zero.** The reader can compare each count with `sessions`.
- When no row has a value for a shown field, the field prints `-`, never `0` or `~$0.00`.
- **Partial (`+`) cells:** a token total that summed any `+` cell carries `+` itself, and its count names the partial rows: `TOUT 1.9M+ (31, 2 partial)`. Leaving `+` cells out would drop tokens that were measured.
- **A starting cluster** (fan-out, `outcomeStarting`) may not have returned every row, so its whole line is a lower bound and says so with a word: `totals c (starting)`. A word, not a second `+`, so it never reads as the token mark.

### 2.4 Watch mode

- **The totals are over every row the daemon returned, not the rows the cut leaves on screen.** A total that moved as the cursor scrolled would describe the viewport, not the cluster.
- When the table is cut, the line ends `of M`, in the cut line's own words, so a reader knows the totals cover rows off screen.
- The totals line counts against the screen height: in single-cluster `-w` it is part of `pre` (the header); in fan-out it is written into `buf` before `ws.sessionsView`, so it is counted as `used`. Either way the selected row stays on screen.
- **Held rows:** when the table shows rows from an earlier read (single-cluster `ws.late` or `ws.disconnected`; a fan-out cluster whose poll failed), the totals line still prints and carries that read's time: `totals (as of 15:02:11)`. A fresh read drops the stamp. The RATE count follows the table as `now` advances: a held row's fresh rate expires in the total exactly when its cell turns `?`.
- The line reads the same clock the row cells read (`rateClock`), never one of its own.

### 2.5 Several clusters

- One `totals <cluster>` line per cluster whose rows the table shows, printed as a block above the merged table, in the order of the status lines. Each is computed from the rows whose `Cluster` names it, before the merge sort.
- **same_daemon:** a member folded into a leader gets no line. Its kept rows count on the leader's line, which names it: `totals a (+ b, same daemon)`. One daemon is one load, so it reads as one cluster, not as a busy one and a nearly idle one.
- **A repeat address** in the watch frame gets no line, as its rows are already skipped.
- **A cluster that did not answer and shows no rows** prints no line; its status line says why. A cluster showing held rows prints a line with its as-of (2.4).
- **starting:** the line is marked `(starting)` (2.3).
- There is no grand total across clusters. Each line is one daemon at one moment, and a sum across them needs the "n of m clusters answered" and per-cluster as-of grammar of the team rollup design (`docs/design/team-load-rollup.md`, section 4). A grand total can follow once that grammar is shared; it is a separate PR.

### 2.6 Width

- The line is never truncated mid-field. When it is wider than the terminal, it breaks between fields onto a second `totals` line, so the table keeps its own width budget (`docs/design/get-sessions-output.md` section 4.2). A broken line counts against the `-w` height like any other.

## 3. Which views carry it

| View | Carries it? | Why |
|---|---|---|
| `get sessions` on a TTY or with `--header` | **yes** | the view the operator asked about |
| `get sessions -w` | **yes** | 2.4 |
| `get sessions --cluster a,b` / `--all-clusters`, one-shot and `-w` | **yes**, one line per cluster | 2.5 |
| `get sessions` into a pipe, or `-o json\|yaml` | no | the plain-table and document contracts stay as they are |
| `get teams --load` | **a totals row**, not the header line | below |
| other `get` views | no | none lists per-session numbers to sum |

**`get teams --load`** reads the daemon's `team-load` resource, not session rows. Its totals are a last row, `all teams`, from its own columns: seats running of declared, and limited seats. ACTIVE% is the seat-weighted mean, sum(`ActiveMeanPct` x `ActiveMeasured`) / sum(`ActiveMeasured`), which equals the mean over all measured seats because `RollTeamLoad` computes each team's mean as its per-seat sum over `ActiveMeasured`. The row prints the seat count it averaged, and `-` when no seat was measured.

## 4. Every number is diagnostic

No total gates anything. No threshold, colour or mark acts on a total, and nothing reads one to block a spawn, a scale or a merge (SOUL section 8, ADR-007, `docs/design/get-sessions-output.md` section 5). The help text says so in one sentence, as the `--load` help already does.

## 5. The plan

Flat tickets with dependency edges; no umbrella. The supervisor files them. Each names the red tests it turns green, as table tests on pure functions unless it says otherwise.

**T1. The totals renderer and the single-cluster one-shot line.** Files: `cmd/marvel/totals.go` (new), `main.go` (`printSessions` path and the `get` help paragraph).
- A pure `renderTotals(rows []api.Session, shown []sessionColumn, now time.Time, opts totalsOpts) string` that calls the existing cell predicates and never re-derives which rows have a value. `opts` carries the label, the `of M` count, the as-of stamp and the starting mark, so T2 to T4 only pass values.
- Printed into the header for `get sessions` on a TTY or with `--header`.
- Red tests:
  - `TestTotalsOnlyForShownColumns`: a column hidden by the fit has no field; `--columns tout` brings it back.
  - `TestTotalsMillicpuIntegerSum` and `TestTotalsGoneRowsAddNothing`: a crashed row with a frozen reading and a never-sampled row add nothing to mCPU and RSS and are not counted.
  - `TestTotalsPartialCarriesPlusAndCount`: one `+` TOUT cell gives `+` and `1 partial`.
  - `TestTotalsTokenFieldsFollowToutRule`: TIN, CR, CW and PROMPT carry `+` the same way.
  - `TestTotalsHitNeverSummed`.
  - `TestTotalsRateFreshOnly`: `?` and `-` add nothing; the count is `(N fresh)`.
  - `TestTotalsRateClockSeam`: with `rateClock` pinned and one reading at its window edge, the total's fresh count equals the number of non-`?` RATE cells.
  - `TestTotalsCostNilLeftOut`: one nil and one value row count 1; all nil prints `-`, never `~$0.00`.
  - `TestTotalsToutAndCostSeparate`: neither field changes when the other's input does.
  - `TestTotalsDashWhenNoValue`: every shown field `-`, never `0`.
  - `TestTotalsNotIntoPipe`: no line without a TTY or `--header`; `-o json` output byte-identical to before.
  - `TestTotalsBreaksBetweenFields`: a line wider than the terminal breaks onto a second `totals` line, never mid-field.

**T2. Single-cluster `-w`.** Files: `main.go` (the watch frame), `watchselect.go`. Depends on T1.
- The totals line joins `pre`, so the cut counts it; `of M` when cut; held rows carry their as-of.
- Red tests:
  - `TestWatchTotalsFitHeightBudget`: a frame of height h with a totals line fits in h lines and keeps the selected row visible.
  - `TestWatchTotalsCursorStable`: the totals do not change as the cursor moves, and the line says `of M` when the table is cut.
  - `TestWatchTotalsHeldRowsCarryAsOf`: late and disconnected frames print the stamp; a fresh read drops it.
  - `TestWatchTotalsHeldRateExpires`: with `rateClock` past the window, the fresh count falls with the table's `?` cells.

**T3. Fan-out one-shot.** Files: `fanout.go`. Depends on T1.
- Group rows by `Cluster` before the sort, fold same_daemon members into their leader, mark starting, print the block above the merged table.
- Red tests:
  - `TestFanOutTotalsOneLinePerCluster`: each line from its own cluster's rows; a cluster with no rows prints none.
  - `TestFanOutTotalsSameDaemonFoldsIntoLeader`: two names, one daemon; the member prints no line, its kept rows count once on the leader's line, which names it.
  - `TestFanOutTotalsStartingMarked`.
  - `TestFanOutTotalsNotIntoPipe`.

**T4. Fan-out `-w`.** Files: `watchfan.go`. Depends on T2 and T3.
- The block goes into `buf` before `ws.sessionsView`; a repeat address gets no line; a failed cluster showing held rows gets a line with its as-of.
- Red tests:
  - `TestWatchFanTotalsCountedAsUsed`: the block counts against the height and the last table row still fits.
  - `TestWatchFanTotalsRepeatHasNoLine`.
  - `TestWatchFanTotalsHeldClusterCarriesAsOf`: a failed cluster with held rows prints its line with `as of`; the next good poll drops it.

**T5. `get teams --load` totals row.** Files: `teamload.go`, and the `--load` help sentence in `main.go`. Depends on T1 (the help paragraph's wording).
- Red tests:
  - `TestTeamLoadAllTeamsRow`: seats running of declared and limited, summed.
  - `TestTeamLoadAllTeamsActiveIsSeatWeighted`: two teams of different sizes give the seat-level mean with its seat count, not a mean of means; no measured seat gives `-`.

**Plan check** (architect, 2026-11-17). Every ticket names its red tests. Each test is reachable from its ticket and the tickets it depends on: T1's tests need only T1's renderer; T2's need T1's `opts` and the watch frame it edits; T3's need T1 and `fanout.go`; T4's need T2's as-of and height handling and T3's grouping and fold, both edges present; T5's need only `teamload.go` and T1's help. No test reaches a file outside its ticket and its ancestors.

**Rule d:** T1, T2 and T5 touch `main.go`; T2 and T4 share the watch rendering. Each builder runs the rule-d shared-file check before opening a PR, and PRs that share a file go as a chain in the edge order (T1, T2, T3, T4, T5), never as parallel siblings off main. At 182fadf0 no open PR touches `fanout.go`, `watchfan.go`, `watchselect.go` or `teamload.go`. One open PR touches `main.go`: marvel#1478, the `get --help` fix that says TOUT and RATE are tokens and dollars are in COST (draft, opened 2026-11-17). T1 edits the same help paragraph, so T1 chains on #1478 (`--base` its head), or starts after it merges. #601, the team rollup, is an open issue, not a PR: if an open PR for it touches these files, the chain starts on it.

## 6. Not in this design

- **A grand total across clusters** (2.5).
- **CPU as a share of the host:** this needs core counts per host, and marvel reports none today.
- **A token-to-dollar conversion, either way:** marvel owns no price table.
- **CPU, RSS or spend in `get teams --load`:** this needs the `team-load` resource to carry them.
- **An age gate on mCPU and RSS:** a running row prints any reading with a non-zero `MetricsAt`, however old (`main.go:3591-3598`), so a total mixes readings of different ages. View only, so the line does not imply one moment; fixing it is a daemon or row change.
- **An adopted seat's TOUT:** it reads `-` for life (`tout.go:3-14`), so it never counts.
- **A failed same-daemon member in the watch frame:** it gets no fold key (`watchfan.go:286-290`), so the table shows its held rows beside its leader's, and the totals inherit that duplicate. A table defect, not a totals one; a separate issue.
- **Seats whose login state makes a row's numbers mean something else:** if that question's answer changes what a row means, the totals inherit the change, because they count only what rows show.
- **The team rollup itself (#601):** busy, open asks, blocked and stale claims are its own design.

## 7. Decisions

Ruled:
- **COST is summed, with its `~`, whenever the COST column is shown; only shown columns are summed** (director, 2026-11-17, relayed by the supervisor, verbatim: "yes, sum COST as well whenever the COST column is shown. Give it the same \"~\" mark the column carries, so it reads as an estimate and not a bill. The operator asked for the summed values at the top, and spend is one of them. Sum only the columns that are shown.").

Recommended, for the operator through director. Each carries the default the author recommends; this recommendation is valid until 2026-11-24, and the architect seat re-checks it then.

1. **No grand total across clusters in this work.** Default: yes, one line per cluster; a grand total follows the team-rollup grammar.
2. **A same-daemon member folds into its leader's line.** Default: yes (party, 3-0).

The first version's decisions on two-unit TOUT and RATE and on CPU as a percent sum are withdrawn: dollars left TOUT and RATE (#1444), and CPU is now mCPU (#1418).

The party's record (casting, briefs, replies, votes and checks) is kept outside this repository.
