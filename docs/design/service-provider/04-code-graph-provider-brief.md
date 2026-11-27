# Code-graph service provider: design brief

Status: proposal for review. No code accompanies it and nothing here is
committed. Written for the architect to rule on the open questions in section 7.

Last updated: 2026-11-27

---

## Why this exists

Seats ask symbol and reference questions of code they did not write: who calls
this function, what else changes if this signature does, which files does this
type reach. An agent that answers by grepping spends its context on text it then
has to interpret. kos already turns a compiler-grade SCIP index into facts
(`kos facts ingest-scip`); what is missing is a way for marvel to keep that
ingest fresh and say so honestly. This brief proposes the smallest class and
provider that does it, so a reviewer can say yes or no to the shape before any
code is written.

The brief is also the first real use of the provider map from the extraction
gate (MS-8 in [services-shape-requirements.md](../services-shape-requirements.md)).
It tests whether the class, provider and lifecycle seams hold for a service that
is not a long-running daemon.

## 1. What this is, and what it is not

It is an ingest pipeline into existing stores. marvel starts an indexer, hands
the index to `kos facts ingest-scip`, and reports how fresh and how complete the
result is.

It is not a graph database that marvel operates. marvel never runs, hosts or
upgrades the store the facts land in. It is not a query language or a query
server: consumers read the facts through kos. It does not manage Serena. Serena
is adopted-only (operator, 2026-10-16): if an operator already runs one, marvel
may observe it as an adopted entry, and marvel never starts, stops or configures
one. Nothing in this brief changes that.

This matches the earlier framing in [01-brief-prd.md](01-brief-prd.md): the code
graph "should not be a new graph database marvel operates; it is an ingest
pipeline into existing stores."

## 2. The class

Proposed class name: `code-graph` (the name 02-architecture.md already uses for
this slot).

**Consumer contract (MS-1a).** A caller relies on this: after a trigger naming a
repository and a commit, the facts for that pair are in the store, and the class
reports when, with which tools, and whether the index is complete. The pass
condition is a status reading that says `complete` for that commit, or says why
not.

**Modes.**

- `managed`: marvel runs the index job and nothing else. It does not run or host
  the store. Say it plainly: marvel starts an indexer process and the ingest
  command, watches them, and stops them. The store belongs to whoever owns it.
- `adopted`: an index job the operator runs on their own schedule. marvel reads
  how fresh it is and mints nothing (MS-7: no lifecycle verb on what marvel did
  not start).
- `external`: the same, reached over a link marvel can observe.

**Lifecycle fit.** `service.Lifecycle` (Start, Stop, Restart, Reload, Status) was
built around a process that stays up, and an index pass runs to completion. So
the verbs act on the provider's supervision and not on a pass:

- Start arms the provider for its declared repositories. It runs no pass.
- Stop with `keep=false` ends a running pass by its proven pid (MS-9). With
  `keep=true` a running pass is left to finish, and the successor adopts the
  pass's result, not its pid.
- Reload re-reads which repositories are declared. Restart is Stop then Start.
- Status reports the last pass and any running one.
- A pass is its own request verb, outside the Lifecycle interface (section 7,
  question 4 says who may ask).

The alternative is not to make this a service but a scheduled headless role
(`docs/design/scheduled-runs.md`). A scheduled role has run status of its own
(`ScheduleStatus`, with `schedule.stale` and `schedule.fresh`, S-2, #529); what
it lacks is the `adopted` and `external` modes and a per-repository as-of cell.
Section 7 asks which.

**Readings (MS-18).** Every reading is an as-of cell. Per repository: the
commit last indexed, when, the indexer and its version, document and fact counts
as the ingest reports them, and a completeness flag. A repository never indexed
prints `-`, never zero.

## 3. The provider

Proposed provider name: `kos-scip`, a driver of class `code-graph`.

The Services entry would carry the repositories to index (path and the indexer
for each language), and the target the ingest writes to. The recipe it follows is
kos's [indexing-recipe.md](https://github.com/ArcavenAE/kos/blob/main/docs/scip-ingest/indexing-recipe.md):

- Go: `scip-go index`, with `GOCACHE` set outside the clone so the build's
  generated test mains are not indexed, plus `go list -json ./...` from the same
  checkout. Each log ends with an `exit: <n>` line the recipe writes, and the
  ingest refuses a list or log that does not end `exit: 0`.
- Rust: `rust-analyzer scip`, on a toolchain that has `rust-src`.
- Load: `kos facts ingest-scip --db <facts.db> --at <n> --repo <name> --commit <sha> --index ... --log ... --checkout ...`.

marvel does not reimplement the ingest. It runs the command and reads its result.
The environment of every child is built from a class allowlist, as for the bus
(`ChildEnvAllow`), never inherited from the daemon.

## 4. Cost and failure

These figures are from kos's finding ndz7, "SCIP indexing cost across the fleet"
(2026-08-16), measured there on a 16-core, 128 GB laptop with rust-analyzer
1.97.1 and scip-go 0.2.7. They are that finding's measurements, not marvel's, and
a different host or version will differ.

- Go is cheap in time. Every Go workspace in that sweep indexed warm in under
  one second.
- Rust is memory-bound. Every Rust run cost between 0.55 and 2.2 GB peak RSS
  regardless of crate size, and warm times ran 1.72 to 8.46 seconds. Running the
  Rust repositories concurrently would need roughly 12 GB resident.
- Cold cost tracks dependency fetching, not source size.
- rust-analyzer can degrade silently. Without `rust-src` it logged "can't load
  standard library", exited 0, and wrote a plausible index with every standard
  library reference unresolved.

What the provider does about it:

- A pass starts only on an explicit request (section 7, question 4). It never
  runs on a timer, and a daemon start, reexec or Reload never triggers one, so
  no one pays for a run they did not ask for.
- Repositories run one at a time by default, so peak memory is one indexer's.
- A zero exit is not completeness. The provider reads the indexer log and marks
  the index `degraded` when it finds the standard-library error, and says so in
  status. `degraded` is never reported as `complete`.
- Each pass runs under a recognizable process name, logs its purpose and pid, and
  has its resident size watched by that pid, with a stated stop threshold. It is
  stopped by pid, never by pattern.
- Metering stays diagnostic (MS-22): pass counts and durations are readings, and
  nothing blocks on them.

## 5. What marvel owns and what it does not

| marvel owns | marvel does not own |
|---|---|
| starting, watching and stopping an index pass | the store the facts land in |
| the Services entry and its validation | the query surface (consumers use kos) |
| status as as-of cells, including `degraded` | the indexers' installation and versions |
| the child's environment and run conditions | Serena, in any mode but adopted |

Because marvel does not host the store, the state duties of MS-10 (graceful stop,
what a kill may lose, backup, schema pin) belong to the store's owner.

**Store owner (decided by director, 2026-11-27).** The owner is kos, the project
whose schema the store holds. Whether kos wants a different arrangement is open
on kos's side. Until kos rules otherwise, the provider refuses a target that does
not exist, and refuses when the store's `user_version` is behind the schema kos
now writes, and it never upgrades a store itself. The reason, as the architect
reads it: `FactStore::open` creates a missing file, and `init` upgrades an older
schema on open (kos `src/facts/mod.rs:130-157`).

**A killed pass (the architect's reading).** The ingest is atomic by SQLite's
guarantee: one transaction with the commit last (`src/facts/mod.rs:177-205`,
`src/facts/scip_ingest.rs:811`), and not by a kos test. The MS-6 verb line for
Stop is therefore "a killed pass leaves the store at its prior version". kos has
no kill-midway test yet, and the brief does not claim one. These line references
are the architect's reading, which the author re-read at kos `69d7b01`; they are
not a marvel measurement.

## 6. Fit with the provider map

#1602 put provider dispatch behind a map in `internal/daemon` with a test that
every registered provider has an arm. This provider would be one more arm and
one more registered class, with the one-per-class cap decided in section 7.

It would be the second managed provider, so MS-8 applies in full, and its clause
(a) has not landed. #1602 records a lifecycle for every entry in `d.services`
(`internal/daemon/services_dispatch.go:61`), but every daemon verb still reaches
only `d.lifecycle`, which is set for the bus class alone (`services_dispatch.go:62-63`):
stop at `daemon.go:3494-3495`, restart at `:3505-3508`, status at `:3376` and
`:3404`. Clause (c), the provider map, has landed. Clauses (a), (d) (a
fake-provider test that drives every verb) and (h) (status through the interface
with as-of cells) are open. Decided by director, 2026-11-27: a second managed
provider does not ship until (a) is done. The brief does not propose a schedule
for that work. The MS-8 list is the one in
[services-shape-requirements.md](../services-shape-requirements.md), where (a)
is already marked as a deferred seam.

## 7. Open questions

Questions 1, 3 and 5 are recommendations with a sell-by date, not defaults that
take effect on silence. Questions 2, 4 and 6 carry the architect's reading or a
director decision, as marked. Items for the architect go through the supervisor.

1. **Service or scheduled role?** Recommend a service of class `code-graph` with
   the verb mapping in section 2, because the `adopted` and `external` modes and
   a per-repository as-of cell come with the class. Valid until 2026-12-11; the
   architect re-checks it then.
2. **Where does the store live, and who owns it?** Decided by director,
   2026-11-27: kos owns it (section 5). The entry names the target, and marvel
   treats the store as an adopted dependency of the job, never a managed one. The
   provider refuses a missing target and a store whose `user_version` is behind,
   and never upgrades. Whether a schema upgrade should instead surface as an
   event is open on kos's side.
3. **One index per repository or per cluster?** Recommend per repository, since
   the recipe and the ingest are per repository and commit. The store's version
   is global to the store: the ingest refuses `--at` below the store's latest
   (`src/facts/mod.rs:322-331`, the architect's reading), so the provider
   allocates `--at`, monotonic per store. Valid until 2026-12-11; the architect
   re-checks it then.
4. **Who may request a pass?** Decided by director, 2026-11-27: explicit
   requests only, with no timer. The requesters are the operator verb, and seats
   whose role declares the permission `code-graph.index`. That name is
   provisional and changes only by an edit to this brief before the class lands.
   A daemon start, a reexec or a Reload never triggers a pass.
5. **Cap per cluster.** Recommend one `code-graph` entry per cluster to start, as
   the bus has, so the cap is a decision to relax later. Also one running pass
   per store target, because the ingest reads earlier facts outside its
   transaction (`src/facts/scip_ingest.rs:801-809`, the architect's reading).
   Valid until 2026-12-11; the architect re-checks it then.
6. **Is the ingest atomic on a kill?** Answered in section 5, as the architect's
   reading: yes, by SQLite's guarantee, with no kos kill-midway test yet.

## 8. Out of scope and follow-ups

- Any code, config schema or registry entry. Those follow review of this brief.
- A query server, and anything that manages Serena.
- Marvel as a consumer of changed-symbol reachability (02-architecture.md notes
  it); a later brief.
- MS-8 (a), (d) and (h), which a second managed provider waits on.

## Opportunities

- kos's recipe could print the indexer log's degraded marker as a field of the
  ingest result, so a consumer need not grep the log.
