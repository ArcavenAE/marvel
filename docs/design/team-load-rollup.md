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
| The daemon holds no global identity | `internal/bus/declared.go:146-148`, `:212-247` | the daemon's own user (`marvel_admin`) is local to its broker; only team and role users carry global grants, and only `global.director.inbox`, `global.*.supervisor.inbox` and `$JS.global.API.>` |
| Asks are director's | director `sim/design/ask-ledger.md`, `docs/shim-reference.md:61-77` | a reader per broker writes `ASK_LEDGER` and a JSON file; `asks --json` gives rollups per owner and asker keyed `<team>/<role>` or `global:<cluster>/<role>`; rows from each broker merge by `message_id` |
| The ask reader is not yet granted | director `sim/design/ask-ledger.md` section 6 | its broker principal is the operator's open decision |
| Blocked is a ledger state | director `sim/design/ask-ledger.md` section 5 | a row is `blocked` when its owner sent a status message `blocked-on <address or ref>` |

So the per-team arithmetic is local to the cluster that owns the team. What is cross-host is the fleet view of all teams, the asks (an ask's row can sit in the sender's broker), and stale claims (a claim names a seat, not a cluster).

## 2. Where the rollup is computed

**Each cluster's daemon computes its own teams' rows.** It already holds the inputs: the sessions, their states, and the tick counts ACTIVE% reads. Per team it computes:

- seats declared, and seats running;
- mean ACTIVE% over the seats that have a value, with the count measured (`62% (5 of 7)`). A seat reading `-` is left out of the mean and counted in neither the numerator nor the denominator;
- seats `limited`;
- the live agent names, for the claim join in section 4.

**Asks are never counted by marvel.** QUEUE, the oldest open ask, blocked, and asks waiting on the operator come from director's ledger, joined by the team key. Marvel reads the ledger's counts and never recounts bus traffic (the distsys seat's point, quoted in `get-sessions-output.md` section 6).

**The view is assembled by the reader.** `marvel get teams --load` (the name is open to the column selector) reads every cluster's rows, the ledger and, optionally, bd. It renders one table, one row per team, with the cluster as a column.

## 3. What crosses the global tier, and how often

Only each cluster's summary crosses. No pane content, no per-tick samples and no ask text.

- **The record:** one value per cluster, holding the cluster name, marvel version, `observed_at`, `valid_until`, and the per-team rows from section 2.
- **Where it lands:** a key-value bucket on the hub, with one key per cluster (decision D1). A bucket keeps the last value, so a reader started late sees every cluster at once, and no subscriber has to be up when a cluster publishes.
- **Cadence:** every 60 s, with `valid_until` at `observed_at` + 180 s (decision D4). ACTIVE% is a 15-minute share, so one minute of lag moves it little. Three missed publishes mark the cluster stale.
- **Size:** about 100 bytes per team plus the agent names. Well under one message for any fleet today.

## 4. Staleness and UNKNOWN

The three-state grammar applies per cluster, and every team row inherits its cluster's state:

- **fresh:** the key's `valid_until` is in the future. The row prints values.
- **stale:** the key exists and `valid_until` has passed. Every derived cell prints `?`, and the row shows the key's age (`cluster-b ?  last 7m ago`). It never prints the old numbers as if current.
- **never seen:** a cluster in the expected set with no key. Its teams cannot be listed, so the table prints one line for the cluster, `cluster-b  -  no load published`.
- **The header** says how many answered: `clusters: 2 of 3 fresh, 1 stale`. Who is in the expected set is decision D3.
- **Ledger cells** carry their own as-of: the ledger JSON's pass time. With no ledger, they print `-`. While a broker's reader is down, they print `?`. The ledger lists reader downtime under its gaps, and the view repeats that line.
- **A stale claim is only claimed when every cluster is fresh.** A claim is stale when no fresh cluster lists the seat it names. If any cluster in the set is stale or never seen, the claim count prints `?` with the reason, because the missing seat may be on the cluster that did not answer. This guard exists because one claim held by a seat that no longer exists is the specimen #601 names.
- **No hub at all:** the view falls back to the local cluster, with the header `clusters: local only (no hub)`. Marvel without the global tier, or without director, still prints the local rows (component independence).

## 5. Every number is diagnostic

No value gates a spawn, a merge, a scale or a shift (SOUL section 8, ADR-007, `diagnostic-not-gate`). "Overloaded" and "unloaded" are not defined here. They need a ceiling per team that no design yet sets, and setting one would be the first step toward a gate. The view shows the numbers, and the operator and the supervisors judge.

## 6. Decisions for the operator

Each has options, a recommendation and an expiry. Nothing here is built before the rulings.

**D1. The transport.**

| | option | gives | costs |
|---|---|---|---|
| a | a hub key-value bucket, one key per cluster, written by each daemon | any seat with hub read sees the fleet; a late reader sees every cluster at once | a new hub grant per cluster daemon (D2) |
| b | the reader fans out over `mrvl://` to each configured cluster | no global tier change; works today | only a reader holding every cluster's key sees the fleet, so supervisors on other hosts do not |
| c | director computes and serves the rollup | one place joins asks and seats | director learns marvel's session model, which crosses the boundary the ledger design kept |

Recommended: (a), as #601 asks. (b) is a valid interim for the operator's own laptop, with no grant needed.

**D2. Who may write a cluster's key.**

| | option | gives | costs |
|---|---|---|---|
| a | a hub-side user per cluster daemon, allowed to put only its own key | a cluster cannot overwrite another's load | one more hub user per cluster, which is the operator's to grant (R-95) |
| b | reuse the team users' existing `$JS.global.API.>` grant | no new user | any supervisor could write any cluster's load, so the numbers stop being evidence |

Recommended: (a).

**D3. The expected set behind "n of m".**

| | option | gives | costs |
|---|---|---|---|
| a | every cluster that ever wrote a key, kept until the bucket's max age (1 h proposed) | no new list | a cluster that never published is invisible |
| b | the clusters in the reader's own `~/.marvel/config.yaml` | the reader's own view | differs per reader |
| c | (a) plus every cluster live in the hub's `GLOBAL_PRESENCE` | catches a cluster whose supervisors are up but whose daemon is not publishing | reads one more hub bucket |

Recommended: (c).

**D4. Cadence and validity.**

The recommendation: publish every 60 s, `valid_until` at +180 s, bucket max age 1 h. A tighter cadence costs hub writes for little gain on a 15-minute share.

**D5. Stale claims and bd.**

| | option | gives | costs |
|---|---|---|---|
| a | the reader runs `bd` when it is on `PATH` and reachable, and joins its open claims against the fresh clusters' agent names | the column #601 asks for, optional | marvel's CLI calls bd; absent bd, the cell prints `-` |
| b | leave stale claims to the bd side (`aq stale`) and keep marvel bd-free | no new coupling | the operator reads two views |

Recommended: (a), as an optional integration that marvel never requires and that never writes bd.

**D6. Where blocked and operator-waiting come from.** The asks are director's (the ask ledger), so this is the operator's routing to confirm, not a marvel choice. The recommendation:
- blocked is the count of the team's owner rows in the ledger state `blocked`;
- waiting on the operator is the count of the team's open asks whose owner is director (`global:global/director`).

Neither adds an ask class or a role. Both print `-` until the ledger reader runs on each broker, which waits on the reader's principal (director's open decision).

These recommendations are valid until 2026-10-22, or until the operator grants the ask reader's principal, whichever comes first. The architect re-checks them then.

## 7. The plan, once ruled

Flat tickets with dependency edges, each with red tests first:

1. daemon: compute per-team rows on the reconcile tick (seats, ACTIVE% mean with count, `limited`, agent names). Local only; no transport.
2. CLI: `get teams --load` for the local cluster, with the as-of grammar. Depends on 1.
3. daemon: publish the record to the hub bucket on D4's cadence. Depends on 1 and on D1 and D2.
4. CLI: read every cluster's key, apply section 4's states and the header. Depends on 2 and 3.
5. CLI: join the ask ledger's rollup by team key. Depends on 2 and on the ledger reader running.
6. CLI: the optional bd claim join with the all-fresh guard. Depends on 4 and on D5.
