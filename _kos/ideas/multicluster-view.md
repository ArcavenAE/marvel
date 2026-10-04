# A multicluster view: one marvel client, several clusters at once

- **Status:** idea (pre-hypothesis, no commitment). Exploration only: no
  frontier node, probe or ticket.
- **Date:** 2026-10-04
- **Seed (operator's words, via director):** "what would it look like to
  have a multicluster view with marvel, to be able to connect to,
  communicate with more than one cluster at the same time with certain
  views, marvel get sessions could display all the agent sessions at the
  same time (which is why you were suggesting an optional column display)
  this might present more problems than we're considering right now and
  would need more exploration"
- **Subject:** marvel (its client, its views and its cluster config). Filed
  in the marvel graph per the subject test; the bus and director are
  objects here, not co-owners.
- **Related:** [[declared-cluster-config]], [[local-cluster-discovery]],
  [[get-views-connection-and-service-context]],
  [[token-rate-column-and-configurable-columns]] (a CLUSTER column would be
  one of its selectable columns), [[get-sessions-width-and-truncation]]
  (one more column competes for width). bd aae-orc-f08m0 carries the
  CLUSTER column and the `-w` header indicator; aae-orc-4hku4 carries the
  name grammar.

## Why look at it

An operator with more than one cluster answers "what is running" today by
switching clusters and asking again, once per cluster, and then joining the
answers by hand. A view that reads several clusters at once would answer
that in one call. The operator expects it to raise more problems than it
first seems, so this file lists the problems and settles none of them.

## Today

- The client talks to exactly one daemon per command. It resolves that
  daemon as `--socket`, then `MARVEL_SOCKET`, then the selected cluster's
  socket or server, then the layout default (`cmd/marvel/main.go:50-56`).
- Clusters are named entries in `~/.marvel/config.yaml`, each with a
  socket or an `mrvl://` server and its own client identity
  (`internal/config/config.go:202-206`). One of them is current
  (`current_cluster`, `:175`), and `--cluster` picks another for one
  command (`cmd/marvel/main.go:134`).
- So "several clusters" already exists as config. What does not exist is
  one command that reads more than one of them.

## What it might look like

A read verb takes a set of clusters instead of one (`--cluster a,b`, or
`--all-clusters`), dials each, and merges the rows, with CLUSTER as a column
and the `-w` header naming the clusters it is watching. `get sessions` is
the motivating case; `get teams` and `events` are the next likely ones.

## Open problems (recorded, not settled)

1. **Cluster naming and identity.** The same cluster can carry two names.
   On 2026-10-04 the marvel cluster named `skippy` was `mokuzai` on the
   global bus, so a send to `global://skippy/supervisor` was refused (R-92).
   A merged view keyed on the client's name would disagree with the bus and
   with director about which cluster a row came from. Which name is the
   identity: the client config entry, the daemon's own name, the host, or
   the bus's? (aae-orc-4hku4 is the grammar question.)
2. **Auth and custody per cluster.** Each cluster has its own client key
   and trust (`identity:`, known hosts). One command now holds several
   live connections under several identities. Does a failure to
   authenticate to one cluster stop the command or mark that cluster?
   Nothing here may widen what any one key can reach (SOUL section 3).
3. **Partial reachability.** Some clusters will be down, slow, or off the
   network (kinu's maintenance window on 2026-10-04 is the case in hand).
   A merged table that silently drops a cluster reads as "nothing running
   there". The view needs a per-cluster status line or row, a timeout per
   cluster, and a rule for how long the whole command waits.
4. **Conflicting names.** Session keys are `<team>-<role>-g<gen>-<idx>`
   and workspaces and teams are named per cluster, so two clusters can
   both have `aae/arcaven` and both have `arcaven-architect-g6-0`. Every
   key in a merged view needs its cluster to be unique, and any verb that
   takes a key back (`describe`, `capture`, `inject`) has to accept the
   qualified form.
5. **Which verbs stay single-cluster.** Reads can merge. Writes are a
   different question: `work`, `scale`, `shift`, `kill`, `inject` and
   `delete` across several clusters at once would multiply the blast radius
   of one typo. A likely line is that every write names exactly one
   cluster, but that is a ruling, not a given.
6. **Watch mode.** `get sessions -w` over several clusters means several
   streams, each with its own lag and reconnects. Is the view one table
   refreshed together, or rows updated as each cluster answers?
7. **Version skew.** Clusters run different daemon builds. A column one
   daemon reports and another lacks needs a defined blank, not a guess.
8. **Ordering and width.** Merged rows need a stable sort that keeps a
   cluster's rows together or interleaves them by a chosen column, and the
   CLUSTER column costs width the table already lacks.

## Not in this idea

How clusters are found (that is [[local-cluster-discovery]]), and any
cross-cluster scheduling or team placement. This is about reading several
clusters, not about operating them as one.
