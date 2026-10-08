# The per-team load rollup across clusters

Proposal, 2026-10-08. Not ratified.

- Author: the architect seat, team arcaven.
- Issue: #601. Builds on `docs/design/get-sessions-output.md`, which judged this rollup a separate design (its section 6).
- Code read at marvel `a45eb0b` and director `987fa0f`.

## 0. Why

The operator asked which teams are free, busy, blocked or overloaded. `get sessions` now answers part of that per seat on one cluster (ACTIVE%, LAST-ACTIVE, `limited`). It does not answer it for the fleet: teams live on several clusters, and asks cross between them. #601 asks for one view per team, collected over the global tier. The issue does not decide where the rollup is computed, what crosses the tier, or what the view says when a cluster does not answer. This design proposes those, and lists the decisions only the operator can make.

## 1. The premises, verified

| Premise | Where | What it says |
|---|---|---|
| A team lives on one cluster | `internal/api/store.go` (teams are records in one daemon's store), `internal/bus/declared.go:203-205` | `marvel work` applies a team to the daemon it reaches; that daemon's broker refuses the same team name in a second workspace. Nothing replicates a team to another daemon |
| Per-seat activity exists | `cmd/marvel/activepct.go`, `cmd/marvel/main.go:1037` | ACTIVE% is the share of the last 15 minutes of daemon ticks that found the seat not quiet; the tick is `ReconcileInterval`, 2 s (`internal/daemon/daemon.go:61`) |
| The request was renamed | `docs/design/get-sessions-output.md` section 4.4 | the party renamed BUSY% to ACTIVE%, because it measures "not quiet", not "working" |
| One as-of shape | `internal/asof/asof.go:41-63` | value, `observed_at`, `valid_until`, source; `-` never measured, `?` expired, never `0` for no sample |
| The daemon holds no global identity | `internal/bus/declared.go:146-148`, `:212-247`; `internal/bus/render.go:175` | the daemon's own user (`marvel_admin`) is local to its broker. Only team and role users carry global grants: publish on `global.director.inbox`, `global.*.supervisor.inbox` and `$JS.global.API.>` (`:212`, given at `:220` and `:245`), and the team user also subscribes `global.<domain>.>` (`:221`). The hub is reached only through the broker's leaf remote, which logs in with the cluster's leaf nkey; the daemon has no hub connection of its own |
| Asks are director's | director `sim/design/ask-ledger.md` sections 4 and 8, `docs/shim-reference.md:61-77` | a reader per broker writes `ASK_LEDGER` and a JSON file. The rollups are open asks, unacked asks and the oldest open age, per owner role and per asker role, keyed `<team>/<role>` or `global:<cluster>/<role>`. A `global:` key carries no team. Merging rows across brokers by `message_id` is part A2, not built; until then each broker's ledger holds its own copy of a cross-broker ask, so summing ledgers double-counts it |
| The ask reader is not yet granted | director `sim/design/ask-ledger.md` section 6 | its broker principal is the operator's open decision |
| Blocked is a ledger state | director `sim/design/ask-ledger.md` section 5 | a row is `blocked` when its owner sent a status message `blocked-on <address or ref>` |

So the per-team arithmetic is local to the cluster that owns the team. What is cross-host is the fleet view of all teams, the asks (an ask's row can sit in the sender's broker), and stale claims (a claim names a seat, not a cluster).

## 2. Where the rollup is computed

**Each cluster's daemon computes its own teams' rows.** It already holds the inputs: the sessions, their states, and the tick counts ACTIVE% reads. Per team it computes:

- seats declared, and seats running;
- mean ACTIVE% over the seats that have a value, with the count measured (`62% (5 of 7)`). A seat reading `-` is left out of the mean and counted in neither the numerator nor the denominator;
- seats `limited`;
- the live agent names, for the claim join in section 4.

**Asks are never counted by marvel.** Marvel reads only fields the ledger's rollups already carry, joined by the team key, and never recounts rows or bus traffic (the distsys seat's point, quoted in `get-sessions-output.md` section 6). That rule decides what the view can show:

- **Which ledger.** The merged ledger, once A2 builds it. Before A2 the view reads no ledger at all, because summing per-broker ledgers double-counts cross-broker asks, and one broker's ledger alone is a partial count that would read as whole. Ask cells print `-` until then.
- **What it shows from a rollup:** open asks, unacked asks and the oldest open age, from `rollup_by_owner` keyed `<team>/<role>`, summed over the team's roles.
- **`global:<cluster>/<role>` owner keys** carry no team, and several teams can hold the same global role on one cluster. They are shown on the cluster's line, never assigned to a team.
- **Blocked and waiting on the operator** are in neither rollup today, so the view cannot show them without recounting rows. They print `-` until director's rollup carries them (D6).

**The view is assembled by the reader.** `marvel get teams --load` (the name is open to the column selector) reads every cluster's rows, the ledger and, optionally, bd. It renders one table, one row per team, with the cluster as a column.

## 3. What crosses the global tier, and how often

Only each cluster's summary crosses. No pane content, no per-tick samples and no ask text.

- **The record:** one value per cluster, holding the cluster name, marvel version, `observed_at`, `valid_until`, and the per-team rows from section 2.
- **Where it lands:** a key-value bucket on the hub, with one key per cluster (decision D1). A bucket keeps the last value, so a reader started late sees every cluster at once, and no subscriber has to be up when a cluster publishes.
- **How it gets there:** the daemon puts through its own broker as `marvel_admin`, over the leaf, into the hub's JetStream domain, the route team users already take for `$JS.global.API.>`. That needs no hub credential for the daemon. INFERRED: that route is granted to team users and not measured for `marvel_admin`; a red test in plan item 4 settles it. Who may write which key is then decided by grants, D2.
- **Cadence:** every 60 s, with `valid_until` at `observed_at` + 180 s (decision D4). ACTIVE% is a 15-minute share, so one minute of lag moves it little. A record is stale once a third publish is late: two missed, and the third later than 180 s. Clock skew between the publishing host and the reader moves that edge by the skew.
- **Size:** about 100 bytes per team plus the agent names. Well under one message for any fleet today.

## 4. Staleness and UNKNOWN

The three-state grammar applies per cluster, and every team row inherits its cluster's state:

- **fresh:** the reader's clock is at or before the key's `valid_until`. A reading is still fresh at exactly `valid_until`, as `asof.Cell.State` decides (`internal/asof/asof.go:48-58`). The row prints values.
- **stale:** the key exists and the reader's clock is after `valid_until`. Every derived cell prints `?`, and the row shows the key's age (`cluster-b ?  last 7m ago`). It never prints the old numbers as if current.
- **never seen:** a cluster in the expected set with no key. Its teams cannot be listed, so the table prints one line for the cluster, `cluster-b  -  no load published`.
- **The header** says how many answered: `clusters: 2 of 3 fresh, 1 stale`. Who is in the expected set is decision D3.
- **Ledger cells** carry their own as-of: the merged ledger's pass time. With no merged ledger (before A2, or with no reader), they print `-`. While a broker's reader is down, they print `?`. The ledger lists reader downtime under its gaps, and the view repeats that line.
- **A stale claim is only claimed when every cluster is fresh.** A claim is stale when no fresh cluster lists the seat it names. If any cluster in the set is stale or never seen, the claim count prints `?` with the reason, because the missing seat may be on the cluster that did not answer. This guard exists because one claim held by a seat that no longer exists is the specimen #601 names.
- **No hub at all:** the view falls back to the local cluster, with the header `clusters: local only (no hub)`. Marvel without the global tier, or without director, still prints the local rows (component independence).

## 5. Every number is diagnostic

No value gates a spawn, a merge, a scale or a shift (SOUL section 8, ADR-007, `diagnostic-not-gate`). "Overloaded" and "unloaded" are not defined here. They need a ceiling per team that no design yet sets, and setting one would be the first step toward a gate. The view shows the numbers, and the operator and the supervisors judge.

## 6. Decisions for the operator

Each has options, a recommendation and an expiry. Nothing here is built before the rulings.

**D1. How should each cluster's summary reach the readers?**

| | option | gives | costs |
|---|---|---|---|
| a | a hub key-value bucket, one key per cluster, put by each daemon over its leaf | any seat with hub read sees the fleet; a late reader sees every cluster at once | the grant work in D2, before the numbers can be trusted |
| b | the reader fans out over `mrvl://` to each configured cluster | no global tier change; works today | only a reader holding every cluster's key sees the fleet, so supervisors on other hosts do not |
| c | director computes and serves the rollup | one place joins asks and seats | director learns marvel's session model, which crosses the boundary the ledger design kept |

Recommended: (a), as #601 asks. (b) is a valid interim for the operator's own laptop, with no grant needed.

**D2. What stops a seat or a cluster from writing another cluster's load?**

Today, nothing would. Every team and role user that holds the global tier may publish `$JS.global.API.>` (`declared.go:212`, given at `:220` and `:245`). That covers a key put into any hub bucket (`$JS.global.API.$KV.<bucket>.<key>` is under it), so any such seat could write any cluster's key. A narrower grant for the daemon alone changes none of that. Every option below therefore starts with the same precondition: narrow the seats' grant so it excludes the load bucket.

| | option | gives | costs |
|---|---|---|---|
| a | narrow the team and role users to deny `$JS.global.API.$KV.<bucket>.>` (a marvel change); and on the hub, permit each cluster's leaf user to put only its own key (the operator's hub-side grant, R-95) | a cluster can write only its own key, and no seat can write any key | a marvel change to every seat user, and one hub-side rule per cluster's leaf. Within a cluster, a key is then as trustworthy as that cluster's `marvel_admin` |
| b | (a)'s narrowing in marvel only, with no hub-side rule | no seat can write a key | one cluster's daemon could still write another cluster's key, because the hub sees only the leaf user |
| c | no narrowing; reuse the existing grant | nothing to change | any seat holding the global tier could write any cluster's load, so the numbers stop being evidence |

Recommended: (a). (b) is the smallest safe start if the hub-side rule waits. (c) is listed to name what the current grant allows, not as a choice.

**D3. Which clusters count as expected in "n of m"?**

| | option | gives | costs |
|---|---|---|---|
| a | every cluster that ever wrote a key, kept until the bucket's max age (1 h proposed) | no new list | a cluster that never published is invisible |
| b | the clusters in the reader's own `~/.marvel/config.yaml` | the reader's own view | differs per reader |
| c | (a) plus every cluster live in the hub's `GLOBAL_PRESENCE` | catches a cluster whose supervisors are up but whose daemon is not publishing | reads one more hub bucket |

Recommended: (c).

**D4. How often should a cluster publish, and how long is a record good for?**

| | option | gives | costs |
|---|---|---|---|
| a | every 60 s, valid for 180 s, bucket max age 1 h | a stale cluster shows within about 3 minutes | one small put per cluster per minute |
| b | every 15 s, valid for 45 s | a stale cluster shows within a minute | four times the hub writes, for a 15-minute share that moves little in 15 s |
| c | every 300 s, valid for 900 s | the fewest writes | a dead cluster reads fresh for up to 15 minutes |

Recommended: (a).

**D5. Should the view read bd for stale claims?**

| | option | gives | costs |
|---|---|---|---|
| a | the reader runs `bd` when it is on `PATH` and reachable, and joins its open claims against the fresh clusters' agent names | the column #601 asks for, optional | marvel's CLI calls bd; absent bd, the cell prints `-` |
| b | leave stale claims to the bd side (`aq stale`) and keep marvel bd-free | no new coupling | the operator reads two views |

Recommended: (a), as an optional integration that marvel never requires and that never writes bd.

**D6. Where should the blocked and waiting-on-operator counts come from?**

Neither is in the ledger's rollups today, and marvel does not recount rows (section 2). The asks are director's, so this is a request to director, routed by the operator:

| | option | gives | costs |
|---|---|---|---|
| a | director's rollup gains two fields: blocked per owner key (once A3's status message exists), and open asks per asker key whose owner is director | marvel reads two more counts, as it reads the others | two fields in director's rollup; blocked waits on A3 |
| b | marvel counts ledger rows itself | no director change | marvel recounts, against section 2, and two counts can disagree |
| c | leave both out of the rollup view | nothing to build | the operator's "blocked" and "waiting on me" stay unanswered here |

Recommended: (a). Neither adds an ask class or a role. Until it lands, both cells print `-`.

These recommendations are valid until 2026-10-22, or until the operator grants the ask reader's principal or director builds A2, whichever comes first. The architect re-checks them then.

## 7. The plan, once ruled

Flat tickets with dependency edges, each with red tests first:

1. daemon: compute per-team rows on the reconcile tick (seats, ACTIVE% mean with count, `limited`, agent names). Local only; no transport.
2. CLI: `get teams --load` for the local cluster, with the as-of grammar. Depends on 1.
3. bus: narrow the team and role users' `$JS.global.API.>` to exclude the load bucket (D2's precondition). Depends on D2.
4. daemon: put the record into the hub bucket over the leaf on D4's cadence. Depends on 1 and 3, on D1, and on the hub-side rule if D2 (a) is ruled.
5. CLI: read every cluster's key, apply section 4's states and the header. Depends on 2 and 4.
6. CLI: join the merged ledger's rollup by team key, with `global:` keys on the cluster line. Depends on 2 and on director's A2.
7. CLI: the optional bd claim join with the all-fresh guard. Depends on 5 and on D5.
