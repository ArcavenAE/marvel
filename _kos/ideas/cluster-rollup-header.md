# A cluster rollup at the top of the views: memory, CPU, rates and "tout"

**Status: idea. Pre-hypothesis. No design, no ticket and no frontier node.**

Captured: 2026-10-30
Source: operator, relayed by director.

## The operator's words

> "kos idea marvel would be nice to have cluster rollup metrics at the top for RSS/memory, CPU, rates, tout, etc"

## Director's reading (a reading, not the operator's text)

A rollup header at the top of `marvel get sessions`, and possibly of the other
`get` views, with one line per cluster. Each line sums the numbers the rows
below it already carry: resident memory, CPU, token rates and "tout".

"tout" is the TOUT column of `get sessions`: cumulative output tokens
(`docs/design/get-sessions-output.md`, line 154). A seat that has no token
stream and a statusline feed can put its running cost total there instead, as
a dollar figure (same file, line 158). The idea does not depend on either
reading: the header would carry whichever totals the operator names.

## The problem it answers

Each row of `get sessions` shows one session's CPU%, RSS and, where the rate
column exists, its token rate. To know how loaded a cluster is, the reader adds
the column by eye. A total at the top would answer "how busy is this cluster"
before the reader scans the rows.

## Questions the idea leaves open

- Which views carry the header: `get sessions` only, or every view that lists
  sessions or teams?
- Which totals, and over what set: all sessions on the cluster, or the rows the
  current filter shows? A rollup that ignores the filter and one that follows it
  answer different questions.
- Rates over what window, and from which meter? The token rate column idea
  ([[token-rate-column-and-configurable-columns]]) settles what a rate is for a
  row; a sum of rates needs the same answer for sessions that report nothing.
- Does a sum mix tokens and dollars? TOUT holds tokens for most seats and a
  cost total for statusline seats, so one cluster-wide figure adds two units
  unless the header splits them or leaves the dollar seats out with a count.
- How does a session that reports no value count: as zero, or left out with a
  count of how many were left out? A total that hides missing rows reads as a
  smaller cluster than it is.
- With several clusters in one client ([[multicluster-view]]), is the header one
  line per cluster, or one line for the whole view?
- Width: a header line shares the display with the row table
  ([[get-sessions-width-and-truncation]]).

## Related

- [[token-rate-column-and-configurable-columns]]: the row-level rate column and
  the column definitions a rollup would sum.
- [[multicluster-view]]: one line per cluster is the multicluster form of the
  header.
- [[get-sessions-width-and-truncation]]: the same view's width limits.
- [[get-views-connection-and-service-context]]: other context that belongs
  above the table, not in it.
- [[health-signal-taxonomy]]: what a total says about a cluster's health, and
  what it does not.
- A research investigation is in flight into what `get sessions` reports for
  seats that are logged out. It touches the same view, so its result may change
  what a row, and so a total, means for those seats. It has no marvel record
  yet, so this note names it without a pointer.
