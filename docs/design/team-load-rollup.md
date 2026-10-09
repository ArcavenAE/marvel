# The per-team load rollup across clusters

Proposal, 2026-10-08. Ruled in part (section 6): D2, D3, D5 and D6 on 2026-10-08, D4 and the D3 amendment question on 2026-10-09. D1 is not ruled; the transport in sections 3 and 7 is the recommended one, conditional on it.

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
| Asks are director's | director `sim/design/ask-ledger.md` sections 4 and 8, `docs/shim-reference.md:61-77` | a reader per broker writes `ASK_LEDGER` and a JSON file. Each rollup row (`rollupRow`, director `probe/nats-phase-0/director-mcp/askledger.go:738-744`) carries `open`, `unacked`, `blocked` and `oldest_open_seconds`, per owner role and per asker role, keyed `<team>/<role>` or `global:<cluster>/<role>`. A `global:` key carries no team. Merging rows across brokers by `message_id` is part A2, not built; until then each broker's ledger holds its own copy of a cross-broker ask, so summing ledgers double-counts it |
| The ask reader is not yet granted | director `sim/design/ask-ledger.md` section 6 | its broker principal is the operator's open decision |
| Blocked is a ledger state, and counted | director `sim/design/ask-ledger.md` section 5; `askledger.go:341`, `:882-883`; `status.go:42-49`, `tools.go:92` | a row is `blocked` when its owner sent a status message `blocked-on <address or ref>`, which the shim's `report_status` tool sends; each rollup row counts its blocked rows |

So the per-team arithmetic is local to the cluster that owns the team. What is cross-host is the fleet view of all teams, the asks (an ask's row can sit in the sender's broker), and stale claims (a claim names a seat, not a cluster).

## 2. Where the rollup is computed

**Each cluster's daemon computes its own teams' rows.** It already holds the inputs: the sessions, their states, and the tick counts ACTIVE% reads. Per team it computes:

- seats declared, and seats running;
- mean ACTIVE% over the seats that have a value, with the count measured (`62% (5 of 7)`). A seat reading `-` is left out of the mean and counted in neither the numerator nor the denominator;
- seats `limited`;
- the live agent names, for the claim join in section 4.

**Asks are never counted by marvel.** Marvel reads only fields the ledger's rollups already carry, joined by the team key, and never recounts rows or bus traffic (the distsys seat's point, quoted in `get-sessions-output.md` section 6). That rule decides what the view can show:

- **Which ledger.** The merged ledger, once A2 builds it. Before A2 the view reads no ledger at all, because summing per-broker ledgers double-counts cross-broker asks, and one broker's ledger alone is a partial count that would read as whole. Ask cells print `-` until then.
- **What it shows from a rollup:** from `rollup_by_owner` keyed `<team>/<role>`, over the team's roles: open, unacked and blocked, each summed, and the oldest open age, the maximum.
- **`global:<cluster>/<role>` owner keys** carry no team, and several teams can hold the same global role on one cluster. They are shown on the cluster's line, never assigned to a team.
- **Waiting on the operator** is in neither rollup today: the owner rollup has director's key but no asker team, and the asker rollup has the team but not who owns the ask. The view cannot show it without recounting rows, so it prints `-` until director's rollup carries it (D6).

**The view is assembled by the reader.** `marvel get teams --load` (the name is open to the column selector) reads every cluster's rows, the ledger and, optionally, bd. It renders one table, one row per team, with the cluster as a column.

## 3. What crosses the global tier, and how often

Only each cluster's summary crosses. No pane content, no per-tick samples and no ask text.

- **The record:** one value per cluster, holding the cluster name, marvel version, `observed_at`, `valid_until`, and the per-team rows from section 2.
- **Where it lands, if D1 is ruled (a) or (d):** a key-value bucket on the hub, with one key per cluster. D1 is not ruled; this line and the next describe the recommended transport, not a ruled one. A bucket keeps the last value, so a reader started late sees every cluster at once, and no subscriber has to be up when a cluster publishes.
- **How it gets there, under the same condition:** the daemon puts through its own broker as `marvel_admin`, over the leaf, into the hub's JetStream domain, the route team users already take for `$JS.global.API.>`. That needs no hub credential for the daemon. INFERRED: that route is granted to team users and not measured for `marvel_admin`; a red test in plan item 3 settles it. Write access is unchanged by the D2 ruling, so any seat holding the global tier can write any cluster's key (section 4).
- **Cadence:** every 60 s, with `valid_until` at `observed_at` + 180 s, and a bucket max age of 1 h (decision D4, ruled (a)). ACTIVE% is a 15-minute share, so one minute of lag moves it little. A record is stale once a third publish is late: two missed, and the third later than 180 s. Clock skew between the publishing host and the reader moves that edge by the skew.
- **Size:** about 100 bytes per team plus the agent names. Well under one message for any fleet today.

## 4. Staleness and UNKNOWN

The three-state grammar applies per cluster, and every team row inherits its cluster's state:

- **fresh:** the reader's clock is at or before the key's `valid_until`. A reading is still fresh at exactly `valid_until`, as `asof.Cell.State` decides (`internal/asof/asof.go:48-58`). The row prints values.
- **stale:** the key exists and the reader's clock is after `valid_until`. Every derived cell prints `?`, and the row shows the key's age (`cluster-b ?  last 7m ago`). It never prints the old numbers as if current.
- **never seen:** a cluster in the expected set with no key. Its teams cannot be listed, so the table prints one line for the cluster, `cluster-b  -  no load published`.
- **The header** says how many answered: `clusters: 2 of 3 fresh, 1 stale`. Who is in the expected set is decision D3.
- **Ledger cells** carry their own as-of: the merged ledger's pass time. With no merged ledger (before A2, or with no reader), they print `-`. While a broker's reader is down, they print `?`. The ledger lists reader downtime under its gaps, and the view repeats that line.
- **A stale claim is only claimed when every cluster is fresh.** A claim is stale when no fresh cluster lists the seat it names. If any cluster in the set is stale or never seen, the claim count prints `?` with the reason, because the missing seat may be on the cluster that did not answer. This guard exists because one claim held by a seat that no longer exists is the specimen #601 names.
- **Every load key is unauthenticated.** Under the D2 ruling, any seat holding the global tier can write any cluster's key, so a key's cluster name is the writer's claim, not a proof. When the view reads hub keys, its header says so on every render: `load keys are unauthenticated: any global-tier seat can write them`. It never prints a key as verified.
- **No hub at all:** the view falls back to the local cluster, with the header `clusters: local only (no hub)`. Marvel without the global tier, or without director, still prints the local rows (component independence).

## 5. Every number is diagnostic

No value gates a spawn, a merge, a scale or a shift (SOUL section 8, ADR-007, `diagnostic-not-gate`). "Overloaded" and "unloaded" are not defined here. They need a ceiling per team that no design yet sets, and setting one would be the first step toward a gate. The view shows the numbers, and the operator and the supervisors judge.

## 6. Decisions for the operator

Each has options, a recommendation and an expiry. Nothing here is built before the rulings.

**D1. How should each cluster's summary reach the readers?**

| | option | gives | costs |
|---|---|---|---|
| a | a hub key-value bucket, one key per cluster, put by each daemon over its leaf | any seat with hub read sees the fleet; a late reader sees every cluster at once | under the D2 ruling (c) there is no grant work, so every key stays unauthenticated: any seat holding the global tier can write any cluster's key (section 4) |
| b | the reader fans out over `mrvl://` to each configured cluster | no global tier change; works today | only a reader holding every cluster's key sees the fleet, so supervisors on other hosts do not |
| c | director computes and serves the rollup | one place joins asks and seats | director learns marvel's session model, which crosses the boundary the ledger design kept |
| d | (a)'s bucket and route, with each record signed by its cluster's own key and verified by the reader against pinned public keys | the numbers are evidence again although D2 (c) left the keys writable by any global seat | a signing key per cluster (issuance under ADR-009: marvel mints it and only marvel readers rely on it), a pin step, and sign and verify code |

Recommended at first: (a), as #601 asks. (b) is a valid interim for the operator's own laptop, with no grant needed.

**Status, 2026-10-09:** not ruled. The operator asked for a simulation of (a), (b) and (c) by two bmad agents first, hosted by another architect seat. That simulation reported on 2026-10-08 and voted for (d), which this table did not have before; its record is not committed. The architect's recommendation is now (d), as revised below. Director is confirming the ruling with the operator.

**D2. What stops a seat or a cluster from writing another cluster's load?**

Today, nothing would. Every team and role user that holds the global tier may publish `$JS.global.API.>` (`declared.go:212`, given at `:220` and `:245`). That covers a key put into any hub bucket (`$JS.global.API.$KV.<bucket>.<key>` is under it), so any such seat could write any cluster's key. A narrower grant for the daemon alone changes none of that. Every option below therefore starts with the same precondition: narrow the seats' grant so it excludes the load bucket.

| | option | gives | costs |
|---|---|---|---|
| a | narrow the team and role users to deny `$JS.global.API.$KV.<bucket>.>` (a marvel change); and on the hub, limit each cluster's leaf user so it can put only its own key (the operator's hub-side grant, R-95; how, below) | if the rules below hold as written, a cluster can write only its own key, and no seat can write any key | a marvel change to every seat user, and one hub-side rule per cluster's leaf. Within a cluster, a key is then as trustworthy as that cluster's `marvel_admin` |
| b | (a)'s narrowing in marvel only, with no hub-side rule | no seat can write a key | one cluster's daemon could still write another cluster's key, because the hub sees only the leaf user |
| c | no narrowing; reuse the existing grant | nothing to change | any seat holding the global tier could write any cluster's load, so the numbers stop being evidence |

How each rule could be written, all INFERRED from nats-server's documented permission model (deny takes precedence over allow) and unmeasured here:

- **The put subject.** A key put into the hub's domain is assumed to travel as `$JS.global.API.$KV.<bucket>.<key>`, under the `$JS.global.API.>` the seats hold today. The narrowing item, dropped by the D2 ruling, would have started with a red test measuring the actual subject a domain put uses, before any rule was written against it.
- **The seats' rule (marvel).** Add `deny: [ "$JS.global.API.$KV.<bucket>.>" ]` to each team and role user's publish permissions. marvel's `Principal` has no deny field today, and the renderer writes only `allow` (`internal/bus/render.go:204`), so the dropped narrowing item would have added a deny list to `Principal` and to the render.
- **The leaf rule (hub).** The leaf user for cluster X also carries `$JS.global.API.>`, and because deny wins over allow, "allow my key, deny the rest of the bucket" cannot be one wildcard pair. Two ways it could be written:
  1. deny every other cluster's key by name: `deny: [ "$JS.global.API.$KV.<bucket>.<cluster-a>", "$JS.global.API.$KV.<bucket>.<cluster-b>", ... ]`, which must be updated on every leaf whenever a cluster joins;
  2. replace the leaf's `$JS.global.API.>` with an enumerated allow list of the API subjects its seats use, plus `$JS.global.API.$KV.<bucket>.<X>`, which changes what every seat on that cluster can reach and needs that list measured first. The list must keep director's put to the hub's `GLOBAL_PRESENCE` bucket, or presence breaks for every seat on that leaf.
- **Red tests these rules would have needed:** a seat user's put to any load key is refused; cluster X's daemon put to X's key succeeds; cluster X's put to cluster Y's key is refused; each against a scratch hub and two scratch leaves. Under the ruling, only the own-key-accepted test stays, on plan item 3; the two refusal tests were dropped with the narrowing item.

Recommended: (a), with the leaf rule written as (1) while the fleet is a few clusters. (b) is the smallest safe start if the hub-side rule waits. (c) is listed to name what the current grant allows, not as a choice.

**Ruling, relayed by director on 2026-10-08:** (c), no change to write access. This is against the recommendation of (a). Its consequence is carried into section 4: the view says its fleet numbers come from unauthenticated keys. The narrowing plan item and the refusal red tests are dropped from section 7. The rule-writing notes above apply only to (a) and (b) and stay as the record.

**D3. Which clusters count as expected in "n of m"?**

| | option | gives | costs |
|---|---|---|---|
| a | every cluster that ever wrote a key, kept until the bucket's max age (1 h, ruled under D4) | no new list | a cluster that never published is invisible |
| b | the clusters in the reader's own `~/.marvel/config.yaml` | the reader's own view | differs per reader |
| c | (a) plus every cluster live in the hub's `GLOBAL_PRESENCE` | catches a cluster whose supervisors are up but whose daemon is not publishing | reads one more hub bucket |

Recommended: (c).

**Ruling, relayed by director on 2026-10-08:** (c).

**Amendment question, ruled 2026-10-09 (relayed by director):** the D1 simulation proposed counting pinned clusters as expected too, which would amend (c). The operator ruled (b): "No, D3 (c) stands as ruled". The expected set stays (a) plus every cluster live in `GLOBAL_PRESENCE`.

**D4. How often should a cluster publish, and how long is a record good for?**

| | option | gives | costs |
|---|---|---|---|
| a | every 60 s, valid for 180 s, bucket max age 1 h | a stale cluster shows within about 3 minutes | one small put per cluster per minute |
| b | every 15 s, valid for 45 s | a stale cluster shows within a minute | four times the hub writes, for a 15-minute share that moves little in 15 s |
| c | every 300 s, valid for 900 s | the fewest writes | a dead cluster reads fresh for up to 15 minutes |

Recommended: (a).

**Ruling, relayed by director on 2026-10-09:** (a), "Every 60 s, valid 180 s, max age 1 h". Section 3's cadence line carries all three values.

**D5. Should the view read bd for stale claims?**

| | option | gives | costs |
|---|---|---|---|
| a | the reader runs `bd` when it is on `PATH` and reachable, and joins its open claims against the fresh clusters' agent names | the column #601 asks for, optional | marvel's CLI calls bd; absent bd, the cell prints `-` |
| b | leave stale claims to the bd side (`aq stale`) and keep marvel bd-free | no new coupling | the operator reads two views |

Recommended: (a), as an optional integration that marvel never requires and that never writes bd.

**Ruling, relayed by director on 2026-10-08:** (a).

**D6. Where should the waiting-on-operator count come from?**

Blocked needs no decision: the rollup already counts it (section 1), and the view reads it. Waiting on the operator is in neither rollup, and marvel does not recount rows (section 2). The asks are director's, so this is a request to director, routed by the operator:

| | option | gives | costs |
|---|---|---|---|
| a | director's rollup gains one field: per asker key, the open asks whose owner is director | marvel reads one more count, as it reads the others | one field in director's rollup |
| b | marvel counts ledger rows itself | no director change | marvel recounts, against section 2, and two counts can disagree |
| c | leave it out of the rollup view | nothing to build | the operator's "waiting on me" stays unanswered here; `director-mcp asks` still lists the rows |

Recommended: (a). It adds no ask class or role. Until it lands, the cell prints `-`.

**Ruling, relayed by director on 2026-10-08:** (a).

**D1, re-checked 2026-10-09.** The first recommendation, (a), passed its sell-by when the simulation reported. Re-checked against the rulings since: D2 (c) left every key writable by any global seat, so under (a) the fleet numbers are a claim, not evidence; D4 (a) fixes a record's validity at 180 s; and the D3 ruling declined counting pinned clusters as expected. Revised recommendation: (d), with three conditions:

1. freshness is judged by the hub's arrival time for the entry, not by the signed `observed_at` alone, so a held-back record cannot read fresh;
2. pinning a cluster's public key is a local command that does not dial the cluster;
3. a valid signed record can be replayed until its `valid_until`, at most 180 s; the view states that bound and keeps no reader state to prevent it. A key with no pin or a bad signature prints as unverified, and the cluster still counts as expected under D3 (c).

This recommendation is valid until 2026-10-23, or until D1 is ruled, whichever comes first; the architect re-checks it then. D1 is not ruled, and section 7's transport items are conditional on it.

## 7. The plan, once ruled

Flat tickets with dependency edges, each with red tests first:

1. daemon: compute per-team rows on the reconcile tick (seats, ACTIVE% mean with count, `limited`, agent names). Local only; no transport.
2. CLI: `get teams --load` for the local cluster, with the as-of grammar. Depends on 1.
Items 3 and 4 are written for D1 (a) or (d), the hub bucket. They are not filed until D1 is ruled; if D1 is ruled (b) or (c), they are rewritten for that transport, and item 7 follows whichever item 4 results. Under (d), item 3 also signs the record and item 4 verifies it.

3. daemon: put the record into the hub bucket over the leaf on D4's cadence, with an own-key-accepted red test against a scratch hub and a scratch leaf. Depends on 1, and on the D1 ruling. D4 is ruled (a): put every 60 s, `valid_until` at `observed_at` + 180 s, bucket max age 1 h.
4. CLI: read every cluster's key, apply section 4's states, the header and the unauthenticated-keys line, with the expected set from D3 (c). Depends on 2 and 3.
5. CLI: join the merged ledger's rollup by team key (open, unacked, blocked, oldest open), with `global:` keys on the cluster line. Depends on 2 and on director's A2.
6. CLI: the waiting-on-operator cell, read from director's new rollup field (D6 (a)). Depends on 5 and 8.
7. CLI: the optional bd claim join with the all-fresh guard (D5 (a)). Depends on 4.
8. director: the rollup field for D6 (a), open asks per asker key whose owner is director. A director change; no marvel dependency.
