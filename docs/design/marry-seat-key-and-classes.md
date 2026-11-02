# marry inputs: the seat key, the class names, and seat-eligibility.json

Proposal, 2026-11-02. Code read at marvel `940cc335` and director `b9bbd56`. Nothing here is built.

- **Author:** the architect seat, team arcaven.
- **Asked by:** the operator, relayed by director, 2026-11-02.
- **Status:** for review. Each of the three choices is the operator's. Each has a recommended default (section 4).

## 0. Why

The marry pilot has 10 eligible items and pairs none of them. Three inputs block it, and none is a fault in the matcher:

- a seat name that exists on two clusters makes `marvel marry` refuse the whole run;
- no seat lists a class an item carries;
- the file that would give seats their classes does not exist.

Each fix is small. Settling them in one note lets director's shadow run (idle-shadow) and `marvel marry` change once, together, instead of trading errors.

## 1. The seat key

**What happens.** `Marry` refuses any input that lists one seat address twice (`internal/api/readiness_marry.go`, "seat %s is listed twice"). idle-shadow builds each address as `agent://<team>/<name>` from freebusy's rows, and carries the cluster in a separate field. When one seat name runs on two clusters, both rows get the same address, and marry refuses the run before it pairs anything. The pilot hit this with a builder seat present on two clusters.

`MarrySeat` already has a `Cluster` field. Placement reads it (a seat is placed only in the sender's cluster or a ruled one). The duplicate check, the lease and the send re-check key on the address alone.

**Options.**

1. **marry keys a seat by cluster and address.** The duplicate check, `Lease`, `MarryPair` and `RecheckBeforeSend` use the pair (cluster, address), and a pair names its cluster. `MarryAddress` is unchanged, so the no-fan-out rule (`agent://{team}/{id}` only) holds. The change is in marvel, plus one vector in `contracts/readiness/marry-vectors.json` for "one name, two clusters". idle-shadow already sends the cluster and does not change.
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

**Who writes it.**

- **Classes and scopes** describe what a role does. They change rarely and belong to whoever assigns work. Proposed: each team's supervisor keeps a small role table (role to classes and scopes), and director writes the file from those tables and freebusy's live seats.
- **Duty cells** are marvel's readiness records (`DutyRecord`, `internal/api/readiness.go`). I found no daemon method or CLI that reads them today. Until one exists, `cells` stays empty. `duties_by_class` lists no duties for the shadow pilot, so a seat's eligibility rests on class and scope only.
  - This is safe while idle-shadow only shadows and sends nothing.
  - A read path for duty cells is needed before any live send.

**Recommended default:** the shape above; director writes it from the supervisors' role tables; empty duties and cells while the pilot is shadow only.

## 4. Decisions

| choice | recommended default | changes |
|---|---|---|
| seat key | cluster and address in marry; idle-shadow drops duplicates as a GAP until then | marvel (key, lease, pair, re-check, one vector); idle-shadow (the stopgap only) |
| class names | marvel vocabulary-free; the six stay in director; one vector with `pr-review` | the shared vectors |
| seat-eligibility.json | the shape in 3; director writes it from supervisor role tables; no duties while shadow | idle-shadow (reads the new fields); director (writes the file) |

These recommendations are valid until 2026-11-09; the arcaven architect re-checks them then.
