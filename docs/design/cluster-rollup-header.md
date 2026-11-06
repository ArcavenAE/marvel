# Cluster totals above the sessions table

**Status:** proposed design, view only. It is promoted from the idea `_kos/ideas/cluster-rollup-header.md`.

- **Author:** the architect seat, team arcaven.
- **Checked against:** marvel `origin/main` ba0ea659.
- **Valid until:** 2026-11-13, or until the first PR for it opens, whichever comes first. The author re-checks it then.

## 0. Why

To tell how loaded a cluster is, a reader of `marvel get sessions` adds the CPU, RSS, TOUT and RATE columns by eye. Every input for a total is already on the rows the CLI receives, so one header line that sums them costs no daemon change, no new RPC and no new state.

It is a view: nothing reads the totals but a person.

## 1. The premises, verified

| Claim | Where |
|---|---|
| CPU% and RSS are per session, from the process-table sampler | `internal/api/types.go:661-662`, filled by `internal/daemon/metrics.go:115-117` |
| A row with no sample prints `-` for CPU% and RSS. So does a crashed, failed or succeeded row, whose last reading is frozen | `cmd/marvel/main.go:3460-3470` (`MetricsAt` zero, or the gone states) |
| TOUT is `SpendOut`, cumulative output tokens; nil means never metered, not zero | `internal/api/types.go:590-597` |
| A statusline seat with no token stream shows the harness's running cost total in TOUT (`~$4.12`) and its change per minute in RATE (`~$0.11/m`). A seat never shows both units | `cmd/marvel/cost.go:20` (`costTout`), `:33` (`costRate`); `CostTotalUSD` at `internal/api/types.go:304` |
| The `~` marks the harness's list-price estimate, not a bill; under a subscription it measures usage, not money | `get` help text, `cmd/marvel/main.go:1266-1269` |
| The daemon header block prints above the table on a TTY, or with `--header`, and never into a pipe | `docs/design/get-sessions-output.md` section 2; `renderSessionsView`, `cmd/marvel/main.go:3772-3795` |
| `-w` redraws through the same `renderSessionsView` | `cmd/marvel/main.go:3772` (shared by get sessions and its watch mode) |
| `get teams --load` is one row per team, one cluster, with seats, mean ACTIVE% and limited seats | `cmd/marvel/teamload.go:15-40`; help at `cmd/marvel/main.go:1282-1287` |
| `get sessions` takes no row filter today. Its scope is the cluster set (`--cluster a,b`, `--all-clusters`) | `marvel get --help` flags; `clusterSet`, `cmd/marvel/fanout.go:318` |
| A fan-out read prints one stderr status line per cluster | `clusterRecord.statusLine`, `cmd/marvel/fanout.go:98-110` |

## 2. The line

One line, `totals`, printed directly under the daemon header line. It follows the header's stream rules exactly:
- it prints on stdout when stdout is a TTY, or with `--header`;
- a pipe gets the plain table;
- `-o json|yaml` documents are unchanged, because a document consumer sums the rows itself.

Example, for illustration only:

```
cluster  local (rung: default)   mrvl://  off   bus  link up, 12s ago
totals   37 sessions   cpu 146.0% (35)   rss 24.1G (35)   tout 1.9M tok (9); ~$4,893 (16)   rate 2.1k tok/s (3); ~$0.43/m (6)
```

The `;` sets the token and dollar figures side by side; the two are never summed (2.2). The builder may pick another ASCII separator that does not read as arithmetic; it is not part of the contract.

### 2.1 What each field sums

The rule for every field: **a total counts exactly the cells the rows show as values, and nothing else.** The line reuses the row code's own predicates (the CPU and RSS gone rule, `costTout`, `costRate`, the `OutRate` as-of state), so a total and its column can never disagree about which rows count.

- **sessions:** the number of rows the table prints.
- **cpu:** the sum of CPU% over rows that print a CPU value.
  - The sampler reads each session's process subtree, so the sum can pass 100% on a multicore host. It is printed as the plain sum, not divided by core count.
  - Converting to a share of the host is a separate question, left out here (section 6).
- **rss:** the sum of RSS over rows that print a value.
  - Shared pages are counted once per process, so the sum overstates real memory use.
  - The help text says "sum of resident set sizes", never "memory used".
- **tout:** two figures, never one. See 2.2.
- **rate:** two figures, never one, from the cells RATE prints as values.
  - A `?` (expired) or `-` (never measured) cell adds nothing and is not counted.

### 2.2 TOUT and RATE: tokens and dollars stay apart

This settles the idea's mixed-unit question: **a total never adds tokens to dollars.**
- Token rows (headless seats, and interactive codex seats from their rollout) sum to a token figure.
- Statusline rows sum to a `~$` figure.
- Each figure carries its own row count.
- A cluster with only one kind prints only that one.

The `~` and the meaning ("an estimate at list price, usage under a subscription") carry over from the row cells unchanged. The header never prints a dollar total without its `~`.

Converting tokens to dollars, or dollars to tokens, is out (section 6). Either direction needs a price table marvel does not own.

### 2.3 Rows that report nothing

This settles the idea's missing-rows question. **A row with no value is left out of a sum and is never counted as zero.** Each figure prints the number of rows it summed, in parentheses.
- The reader can always compare that count with `sessions`.
- A total that hid its missing rows would read as a smaller cluster than it is.
- When no row has a value for a field, the field prints `-`, never `0`.

### 2.4 Scope: the rows shown

This settles the idea's filter question. **The totals are over the rows the table prints.**
- Today that is every session the answering daemon returns, because `get sessions` has no row filter.
- If a filter is added later, the totals follow it, and the line says so (`totals (filtered)`), so a filtered total is never read as the cluster's.
- A total that ignores the filter answers a different question. It is not offered.

### 2.5 Several clusters

This settles the idea's per-cluster question. **One `totals` line per cluster that answered, each led by the cluster name.**
- A fan-out read has no per-cluster header today. `runFanOutGet` merges and sorts every cluster's rows into one table (`cmd/marvel/fanout.go:441`) and renders it with an empty header (`:448`). The watch frame does the same (`cmd/marvel/watchfan.go:318`).
- So the totals for a fan-out read are a block of `totals <cluster>` lines printed above the merged table, one per answered cluster, in the cluster order of the status lines. Each line is computed from the rows whose `Cluster` field names that cluster (`internal/api/types.go:288`), before the merge sort.
- The block follows the same stream rule as the single-cluster line: stdout on a TTY, or with `--header`, and never into a pipe or `-o`.
- In the watch frame, a cluster whose rows are dropped as a repeat of another cluster (`repeatOf`, `cmd/marvel/watchfan.go:305-309`) gets no totals line of its own, so the same rows are never totaled twice.
- A cluster that did not answer prints no totals line. Its status line already says why.
- There is no grand total across clusters in this design. Each cluster's figures come from a different daemon at a different moment, and a sum across them needs the "n of m clusters answered" and per-cluster as-of grammar that the team rollup design (`docs/design/team-load-rollup.md`, section 4) defines.
- A grand total can follow once that grammar is shared; it is a separate PR.

### 2.6 Width

- The line is never truncated mid-field. When it is wider than the terminal, it breaks between fields onto a second `totals` line, so the table below keeps its own width budget (`docs/design/get-sessions-output.md` section 4.2).
- With `-w`, the line is part of each redraw and counts against the screen height like the header.

## 3. Which views carry it

| View | Carries it? | Why |
|---|---|---|
| `get sessions` on a TTY or with `--header` | **yes** | the view the operator asked about |
| `get sessions -w` | **yes** | same renderer, so no second path |
| `get sessions --cluster a,b` / `--all-clusters` | **yes**, one line per answered cluster | section 2.5 |
| `get sessions` into a pipe, or `-o json\|yaml` | no | the plain-table and document contracts stay as they are |
| `get teams --load` | **a totals row**, not the header line | see below |
| other `get` views | no | none lists per-session numbers to sum |

**`get teams --load`** fetches the daemon's `team-load` resource, not session rows. So it has no CPU, RSS or spend to sum without a second call.
- Its totals are a last row, `all teams`, from its own columns: seats running of declared, and limited seats.
- ACTIVE% is a mean of means over teams of different sizes, so its cluster figure is the mean over all measured seats, with that count, computed from the same per-seat readings the team rows use. If the `team-load` response does not carry those readings, ACTIVE% prints `-` in that row rather than a mean of team means.
- Adding CPU, RSS or spend to `--load` is left out (section 6).

## 4. Every number is diagnostic

No total gates anything. No threshold, colour or mark acts on a total, and nothing reads one to block a spawn, a scale or a merge (SOUL section 8, ADR-007, `docs/design/get-sessions-output.md` section 5). The help text for the line says so in one sentence, as the `--load` help already does.

## 5. The plan

One PR, in `cmd/marvel` only (files: `totals.go` new, `main.go`, `fanout.go`, `watchfan.go`, `teamload.go`):

1. **A pure renderer,** `renderTotalsLine(rows []api.Session, now time.Time, label string) string`, in a new `cmd/marvel/totals.go`.
   - It calls the existing cell predicates.
   - It never re-derives which rows have a value.
2. **Call it from `renderSessionsView`** (`cmd/marvel/main.go:3772`) when the header is non-empty, so `get sessions` and `-w` share it.
3. **Fan-out:** the gate in step 2 does not reach it, because both fan-out call sites pass an empty header. So the block of section 2.5 is built at the two call sites themselves:
   - `runFanOutGet` (`cmd/marvel/fanout.go:441-448`): group `rows` by `Cluster` before the sort, render one line per answered record, and print the block above `renderSessionsView`.
   - The watch frame (`cmd/marvel/watchfan.go:305-318`): the same, from `rowSets` for each non-repeat state, written into `buf` before the table.
   - Both honour the TTY or `--header` rule.
4. **`get teams --load`:** add the `all teams` row in `renderTeamLoad` (`cmd/marvel/teamload.go:39`).
5. **Help text** for `get`: one paragraph naming the fields, the two-unit rule, the counts, and "diagnostic only".

Tests, as table tests on the pure renderer:
- tokens and dollars never sum together; a mixed cluster prints both, each with its count;
- a never-sampled row and a crashed row add nothing to cpu and rss, and are not counted;
- an expired RATE cell (`?`) and a never-measured one (`-`) add nothing;
- every field `-` when no row has a value, never `0`;
- no totals line into a pipe without `--header`, and `-o json` output byte-identical to before;
- fan-out: one totals line per answered cluster, from that cluster's rows only; an unanswered cluster prints no line; in the watch frame a repeat cluster prints no line;
- `--load`: the `all teams` row's ACTIVE% is the seat-level mean, or `-`.

**Rule d:** this PR touches `cmd/marvel/main.go`, `cmd/marvel/fanout.go` and `cmd/marvel/teamload.go`. Before opening it, the builder runs the rule-d shared-file check against open PRs. This PR also touches `cmd/marvel/watchfan.go`. #601, the team rollup, is an open issue, not a PR: if an open PR for #601 touches those files, this PR chains on it.

## 6. Not in this design

- **A grand total across clusters** (section 2.5).
- **CPU as a share of the host:** this needs core counts per host, and marvel reports none today.
- **A token-to-dollar conversion, either way:** marvel owns no price table.
- **CPU, RSS or spend in `get teams --load`:** this needs the `team-load` resource to carry them.
- **Seats whose login state makes a row's numbers mean something else:** an open question about logged-out seats touches the same view. If its answer changes what a row means, the totals inherit that change, because they count only what rows show.
- **The team rollup itself (#601):** busy, open asks, blocked and stale claims are its own design.

## 7. Decisions for the operator

Each carries a default the author recommends. Valid until 2026-11-13.

1. **No grand total across clusters in the first PR.** Default: yes, one line per cluster; a grand total follows the team-rollup grammar.
2. **Two figures for TOUT and RATE, never one.** Default: yes.
3. **CPU as the plain sum, which can pass 100%.** Default: yes, labelled as a sum. The alternative is a share of the host, deferred for want of core counts.

## 8. As built

The operator ruled the open decisions in section 7. Where this section and the text above differ, this section governs.

- **Two lines across clusters.** A fan-out read prints one `totals <cluster>` line for each answered cluster and, when two or more answered, one `totals (fleet)` line over the rows those clusters gave, marked `(n of m clusters)` when a configured cluster did not answer. The parentheses cannot occur in a cluster name, so the line is never mistaken for a cluster's. Section 2.5's "no grand total" no longer holds. Each line has its own display preference, both shown by default: `display.totals_per_cluster` and `display.totals_fleet` in the client config's `display:` block.
- **Two figures.** tout and rate print tokens and `~$` estimates apart, each with its row count, and never add them (section 2.2 as written).
- **cpu is the plain sum**, printed as `cpu sum 146.0% (35)`, so it can pass 100% on a multicore host. It is not a host share.
- **Position.** The line sits after the daemon header's last line and before the source legend.
- **No totals while disconnected.** The disconnected watch frame prints its last known rows and no totals line.
- **Merged daemons.** A cluster merged into another's daemon gets no line, and the rows it kept count in the line of the cluster it merged into.
- **Failed or late clusters.** The watch frame prints no totals line for a cluster that failed after a good read or is late, and keeps its last rows in the table.
- **`get teams --load`.** The last row, `all teams`, sums seats running of declared and limited seats over the fresh team rows. Its ACTIVE% is the mean over seats. When any team row is expired, the row leaves it out and reads `(n of m teams)` in the TEAM column.
