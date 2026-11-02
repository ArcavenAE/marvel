# marry inputs: the seat key, the class names, seat-eligibility.json, and duty cells

Proposal, 2026-11-02. Code read at marvel `940cc335` and director `b9bbd56`. Nothing here is built.

- **Author:** the architect seat, team arcaven.
- **Asked by:** the operator, relayed by director, 2026-11-02.
- **Status:** for review. Each of the four choices is the operator's. Each has a recommended default (section 5).

## 0. Why

The marry pilot has 10 eligible items and pairs none of them. Four inputs block it, and none is a fault in the matcher:

- a seat name that exists on two clusters makes `marvel marry` refuse the whole run;
- no seat lists a class an item carries;
- the file that would give seats their classes does not exist;
- no seat has a duty cell, and marry pairs no seat without a proven one (section 4).

A fifth cause can also give zero pairs: with `IDLE_SHADOW_SENDER_CLUSTER` unset, idle-shadow names no sender cluster, so marry places no candidate and reports each as "unplaced: no sender ruled" (`readiness_marry.go`, `UnplacedNoSender`). That is a setting, not a design gap. The pilot should set it to the cluster its shadow run speaks for, so that a pair can be placed at all.

Each fix is small. Settling them in one note lets director's shadow run (idle-shadow) and `marvel marry` change once, together, instead of trading errors.

## 1. The seat key

**What happens.** `Marry` refuses any input that lists one seat address twice (`internal/api/readiness_marry.go`, "seat %s is listed twice"). idle-shadow builds each address as `agent://<team>/<name>` from freebusy's rows, and carries the cluster in a separate field. When one seat name runs on two clusters, both rows get the same address, and marry refuses the run before it pairs anything. The pilot hit this with a builder seat present on two clusters.

`MarrySeat` already has a `Cluster` field. Placement reads it (a seat is placed only in the sender's cluster or a ruled one). The duplicate check, the lease and the send re-check key on the address alone.

**Options.**

1. **marry keys a seat by cluster and address.** Everything that keys on a seat today uses the pair (cluster, address) instead:
   - the duplicate check;
   - `heldSeat`, the seats a lease in the ledger holds;
   - `used`, the seats already paired this run;
   - `Lease`, `MarryPair` and `RecheckBeforeSend`, with a pair naming its cluster.

   `Stop.NotMatchable` stays a list of addresses, and an address on it stops that seat name on every cluster. A stop entry should fail safe, never miss a cluster. `MarryAddress` is unchanged, so the no-fan-out rule (`agent://{team}/{id}` only) holds. The change is in marvel, plus one vector in `contracts/readiness/marry-vectors.json` for "one name, two clusters". idle-shadow already sends the cluster and does not change.
2. **idle-shadow drops a duplicated address.** Every address listed on more than one cluster is left out of the run, with a GAP line naming it, and marry stays as it is. idle-shadow never picks one of the rows, since a guess could pair work to the wrong cluster's seat. This unblocks the run at once, and costs the pilot those seats until option 1 lands.
3. **Qualify the address string itself** (`agent://<cluster>.<team>/<name>` or similar). Not recommended: it changes the envelope address grammar that the bus and `MarryAddress` share, for a problem the existing `Cluster` field already solves.

**Recommended default:** 1 as the key, with 2 as a stopgap in idle-shadow until 1 merges. The stopgap is removed when 1 lands.

## 2. The class names

**What the code shows.**

- marvel has no class vocabulary. `eligibleSeats` matches an item's class to a seat's `classes` by plain string equality, and no list of allowed names exists in marvel's code.
- `review` in `marry-vectors.json` is a fixture value, not a vocabulary.
- idle-shadow reads exactly one bd label `class:<id>`, where `<id>` is one of the six in director's filing view: story-implementation, design-brief, human-decision, pr-review, research-local and gate-definition. It strips the `class:` prefix and sends the bare id under `classes.label`, with `work_class_source: "label"`. So marry already receives, say, `pr-review`, never `class:pr-review`.

So there is no mismatch in the code path. What is missing is a seat that lists any of the six (section 3), and a vector that uses the real form.

**Options.**

1. **marvel stays vocabulary-free.** The six live in director's filing view, as now. The `class:` prefix stays a bd label convention that idle-shadow strips and never sends. The shared vectors gain one row that uses a real id (`pr-review`) under `classes.label`, so the contract exercises the form in use. `review` stays in the existing rows as an opaque fixture.
2. **marvel adopts the six as a closed list** and refuses any other class. That puts a director vocabulary into marvel, which then changes whenever the filing view does.
3. **Rename the fixture to `pr-review` everywhere.** It is harmless, but it implies a vocabulary that marvel does not enforce.

**Recommended default:** 1.

## 3. seat-eligibility.json

**What exists.** idle-shadow reads the file from `$IDLE_SHADOW_SEAT_ELIGIBILITY` or director's state directory. Its header gives the shape:

```json
{"duties_by_class": {"<class>": ["<duty>"]},
 "seats": [{"address": "agent://team/name", "classes": [], "scopes": [], "cells": {}}]}
```

It joins the file to freebusy's rows by address. An absent or malformed file is a GAP: the seats then carry no classes, and every eligible item reads "no eligible seat". That is today's state, since nobody writes the file.

**Proposed shape**, the same plus four fields:

```json
{"version": 1,
 "written_at": "2026-11-02T00:00:00Z",
 "valid_until": "2026-11-03T00:00:00Z",
 "writer": "<role>",
 "duties_by_class": {"pr-review": []},
 "seats": [{"cluster": "<cluster>", "address": "agent://team/name",
            "classes": ["pr-review"], "scopes": ["marvel"], "cells": {}}]}
```

- `cluster` joins with section 1, so the join key is (cluster, address).
- `written_at` and `valid_until` let idle-shadow treat a stale file as a GAP rather than as truth.
- `writer` names the role that wrote it.
- `classes` use the six ids, bare. `scopes` are component names from the bd label convention.
- idle-shadow checks each seat class against the six and reports any other as a GAP line naming the seat and the value. Today an unknown class (a typo, for one) silently reads "no eligible seat".

**Who writes it.**

- **Classes and scopes** describe what a role does. They change rarely and belong to whoever assigns work. Proposed: each team's supervisor keeps a small role table (role to classes and scopes), and director writes the file from those tables and freebusy's live seats.
- **Duty cells** are section 4's question.

**Recommended default:** the shape above, with director writing it from the supervisors' role tables.

## 4. Duty cells for a shadow pilot

**What the code shows.** An item with no duties never pairs:

- `RouteSeat` returns ask-first when it is given no duties (`internal/api/readiness_route.go`, `ReasonNoDuties`).
- `eligibleSeats` keeps only a seat whose verdict is eligible (`readiness_marry.go`).
- idle-shadow sends `duties_by_class[class]`, or `[]` when the class has none.

A review measured it in a sandboxed scratch test: no duties gave 0 pairs, and one duty with a current proven cell gave 1.

No source of proven cells exists. A duty is eligible only when its cell is proven, current, and observed from the seat, pane or peer vantage; `routeDuty` sends a host-vantage proof to ask-first. At `940cc335`, nothing outside tests calls `Store.RecordDuty`, and nothing outside tests calls `Store.SeatReadiness`. So there are no cells to read, and no read path for any. Director observes seats from the host, so it cannot write a seat-vantage proof honestly.

**What a pair needs, exactly.** For an item of class `C` to pair with a seat, `seat-eligibility.json` must carry all of the following. These are `routeDuty`'s rules (`internal/api/readiness_route.go`) and the cell type `asof.Cell` (`internal/asof/asof.go`).

- `duties_by_class` names at least one duty for `C`, for example `{"pr-review": ["first-turn"]}`. An empty list routes ask-first.
- For every duty `D` in that list, the seat's `cells` holds a cell named `duty.D` (`DutyCellName`) with:
  - `value`: `"proven"`. `"fail"` and `"unproven"` route ask-first; a structural fail excludes the seat.
  - `observed_at`: set. A zero time reads as never observed.
  - `valid_until`: at or after the run's `now`, so the cell is fresh. A past time reads as expired. A zero time never expires; the file should always set one.
  - `source`: `<vantage>:<stage>` from the closed vocabulary (`internal/api/readiness.go`), with the vantage `seat`, `pane` or `peer`, never `host`, for example `"pane:turn"`. A `host` proof routes ask-first.
- The seat has no `withdrawn.D` cell whose value is `"withdrawn"`. A withdrawal excludes the seat, and it never expires.

An example seat row that pairs:

```json
{"cluster": "<cluster>", "address": "agent://team/name",
 "classes": ["pr-review"], "scopes": ["marvel"],
 "cells": {"duty.first-turn": {"value": "proven",
           "observed_at": "2026-11-02T05:00:00Z",
           "valid_until": "2026-11-02T05:30:00Z",
           "source": "pane:turn"}}}
```

What is missing today is every such cell: nothing observes a seat's first turn from its pane and records it, and nothing reads such a record into the file.

**Options.**

1. **marry reports would-pair-if-proven.** For an item whose class and scope match a free seat that `RouteSeat` sends to ask-first, the output counts the seat as `ask_first` with its route reasons, beside today's `no-eligible-seat`. It is read-only, it writes no lease, and the shadow pilot learns how many pairs proven duties would give, without any cell being invented. idle-shadow sets `duties_by_class` to one duty per class, so the reasons name what is missing.
2. **Build the producer and the read path now.** The daemon records `duty.first-turn` from `pane:turn` when it sees a seat take a turn, and a read (an RPC, or a `marvel` command) returns `SeatReadiness` for a seat, which idle-shadow puts into `cells`. This is the real path, and every live send needs it. It is more work than the shadow pilot needs to learn.
3. **Director writes the cells itself.** Not recommended. Its observations are host vantage, so they would either route ask-first anyway or claim a vantage they do not have.
4. **A shadow switch in marry that skips duties.** Not recommended. It makes the shadow run measure a matcher that the live run will not be.

**Recommended default:** 1 for the shadow pilot, and 2 before any live send. The pilot then shows real pairs where proofs exist and an honest ask-first count where they do not.

## 5. Decisions

| choice | recommended default | changes |
|---|---|---|
| seat key | cluster and address in marry; idle-shadow drops duplicates as a GAP until then | marvel (key, lease, pair, re-check, one vector); idle-shadow (the stopgap only) |
| class names | marvel vocabulary-free; the six stay in director; one vector with `pr-review` | the shared vectors |
| seat-eligibility.json | the shape in 3; director writes it from supervisor role tables; unknown classes are a GAP | idle-shadow (reads the new fields, checks classes); director (writes the file) |
| duty cells | marry reports would-pair-if-proven for the shadow pilot; the duty producer and read path before any live send | marvel (one output field now; the producer and read later); idle-shadow (one duty per class) |

These recommendations are valid until 2026-11-09; the arcaven architect re-checks them then.
