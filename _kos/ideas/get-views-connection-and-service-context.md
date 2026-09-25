# Decorating the `get *` views with cluster, connection, and service context

- **Status:** idea (pre-hypothesis, no commitment). Captured as a GROUP: the
  operator wants several `get *` decorations rounded up and worked together, not
  one column at a time.
- **Date:** 2026-09-18
- **Origin:** commission via director, carrying the operator's intent.
- **Subject:** marvel (the `get sessions` and other `get *` views). Filed in the
  marvel graph per the subject test.
- **Related:** [[token-rate-column-and-configurable-columns]] (a RATE column plus
  first-class configurable columns; the substrate several of these ride),
  [[subagent-session-display]] (the same table, the same layout question),
  [[desk-addressed-tmux-connection]] (an existing DESK column),
  [[identity-at-launch-and-managed-nats]] (where the managed bus and its leaf
  live). bd aae-orc-o16q2 (RATE column), aae-orc-vdcwm (WORKDIR column): two
  in-flight instances of "one more column," which this idea groups a next wave of.

## The intent

The operator wants marvel's `get` views to carry more of the context an operator
needs at a glance, and wants several such changes treated as one body of work
rather than a trickle of single columns. This file captures that grouping intent
and the known items so far, and leaves the design open. The prior column ideas
above are per-item; this one is the round-up.

## The known items so far

Two were named at filing, and a third joined on 2026-09-25. All are examples of the same shape: a `get` view today shows
per-session facts, and the operator wants it to also show the CONTEXT the session
sits in, the cluster it belongs to and the services that cluster depends on.

1. **The cluster we are connected to.** Which marvel cluster this view is
   reading: its profile, and the connection information (a hostname, and whatever
   else identifies the cluster and the connection). Exactly which fields are
   essential is itself part of the exploration.

2. **The state of the cluster's services.** From an earlier operator note:
   decorate `get *` output with the local and global NATS connection URL and
   status, and the status of the other managed or attached services, for example
   a credential vault, a code graph, flyloft, curtain, and any other attached
   service. The list of services and which of their fields matter is open.

3. **The account's budget, reset, and credit state** (added 2026-09-25 by
   operator ruling). Claude Code `rate_limits` (five_hour, seven_day: used
   and resets_at) and codex `rate_limits` (windows keyed on
   `window_minutes`, plus credits), with near-limit, time-to-reset, credit,
   and no-rate_limits-block flags. Same shape as items 1 and 2: context the
   session sits in, not a per-session fact, because every session on an
   account reports the same number. Detail, the account-scope constraint,
   and the recommended presentation (a separate account view, with an
   `ACCT`-labelled column as the in-table fallback, never aggregated) live
   in [[token-rate-column-and-configurable-columns]] Feature C; bd
   aae-orc-f08m0.

## The open questions

- What context does an operator actually need on a `get` view, and why? The value
  test is per field: what decision does seeing it let the operator make faster or
  more safely? A field that answers no question is decoration for its own sake.
- Which fields are essential for the cluster identity and connection, and which
  are noise? A hostname, a profile, a URL, a reachability state: which of these
  earn a place, and which belong only in a detail view or on request?
- For the services (NATS local and global, a vault, a code graph, flyloft,
  curtain, others): what does "status" mean per service, and is a single
  reachability state enough, or does each service have its own essential facts?
- Where does context belong in a per-session table? A header or banner above the
  rows, a set of columns beside them, or a separate `get`-style view for the
  cluster and its services. This is an open layout question, not a decision here.
- Does surfacing service status risk turning a display into a health gate? The
  automation-boundary rule (SOUL section 8, ADR-007) says a vital sign informs,
  it does not gate; a status decoration must stay diagnostic, never a check that
  blocks.
- How does this interact with the first-class configurable-columns substrate the
  RATE-column idea drags in? If columns become user-definable, several of these
  decorations may be column definitions rather than hardcoded additions.

## The tension

Every one of these fields is context an operator plausibly wants, and the sum of
all of them is a view too dense to read. The grouping is the point: worked
together, the items can share one question, which context earns its place on a
glanceable view and which belongs in a detail lookup, rather than each column
being argued and added on its own. This idea does not answer that question; it
holds it open and names the items the operator wants weighed together.

No output format is designed here, and no field name or view name is proposed;
those are the operator's to choose when this leaves the idea stage.
