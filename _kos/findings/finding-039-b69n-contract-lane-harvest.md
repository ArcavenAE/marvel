# finding-039: b69n contract-lane harvest, eight load-bearing lessons

Work: aae-orc-b69n (the contract lane). Date: 2026-09-13.
PRs: marvel #248 @415d65e (envelope schema, frozen), #249 @19efa39 (Go
codegen), #250 @28eeca6 (event-vocabulary twin). Related: director#13
(the shim emitter refresh), beadle#62 (the Rust envelope half), aae-orc
PR #313 (`_kos/ideas/agent-roles-recommended-shape.md`, the role party).

This records the lessons from the b69n contract lane that a session stall
would otherwise lose. Four of them lived only in coordination messages until
now (sections 1, 2, 3, 4).

**Placement.** Contract-lane mechanics live here in marvel/_kos. Three items
are shared-envelope semantics whose canonical home is director's requirements
register and design corpus (`director/sim/requirements.md`, and
`director/sim/design/` brief 5, which already carries R-87 and the
import-not-vendor pattern): sections 3, 5, and the naming principle in 7.
Director has no `_kos` (confirmed 2026-09-13), so the register-side home is
that corpus, not a director/_kos node, and it is the director's to place. They
are captured here because the schema and its implementation live in the marvel
repo (`contracts/`); those sections are marked CANONICAL HOME: DIRECTOR so the
mirror is not forgotten. This finding-039 is the durable marvel-side record
regardless.

---

## 1. Event-kind producer inventory: 8 of 12 kinds are produced, 4 are not

Verified from the adapter code, not from the vocabulary definition. Of the
twelve frozen kinds in `internal/runtime/events/events.go`, eight have a
producer and four do not.

Produced: `session.started`, `session.ended`, `turn.started`,
`turn.completed`, `message.completed`, `tool.call`, `tool.result`, `error`.

Per-adapter asymmetry worth knowing:
- claudecode emits `session.ended` but not `turn.started`.
- codex emits `turn.started` but not `session.ended`.
- `instance_tmux` emits `error` only.

Unproduced (a consumer `case` exists in `internal/session/bridge.go`, but no
adapter constructs them): `health.heartbeat`, `message.delta`,
`permission.requested`, `auth.required`.

Consequence for the role party's backlog: a `health.heartbeat` producer is
small and self-contained, because the consumer path already exists and only
the adapter emission is missing. `message.delta` is different, net-new work:
the adapters emit `message.completed` only, there is no streaming-delta
producer to extend.

## 2. M2 bus consumer model: JetStream (designed as, not yet frozen)

The M2 bus (the bga arc) is held for operator ratification; this is the
recommendation staged for that decision, filed in the role party as "designed
as, not yet frozen."

Choose JetStream consumers. The reasoning is forced by R-10 (mailbox depth),
the only signal that separates a working target from a heartbeating-but-not-
reading one from a dropped message. Mailbox depth needs per-consumer pending
state.
- Core NATS has no per-consumer pending state and no retention (fire and
  forget), so R-10 cannot exist against it. Ruled out by the requirement.
- JetStream consumers expose `num_pending` / `num_ack_pending`, which is
  mailbox depth, and durable streams give the store-and-forward survival
  across restarts the vision requires.
- An explicit mailbox store also yields depth but duplicates JetStream and
  adds a store to own. The multiclaude mailbox is prior art to retire, not
  rebuild.

Three groundings that make this not a fresh choice:
1. director#4's phase-0 broker authz already allows `$JS.API.>`, `$JS.ACK.>`,
   and `$KV.AGENT_STATE.>` for the confined ops user. Core NATS would not need
   the `$JS.*` subjects; the transport authz is already provisioned for
   JetStream.
2. The vision's director section requires a store-and-forward bus that
   survives agent restarts, which is JetStream persistence.
3. marvel has zero NATS code today (verified), so bga builds the consumer from
   scratch and should build it on JetStream.

Mailbox depth on the route record (role-party ratified shape): `depth_at_route`
and `depth_observed`, an integer or UNKNOWN when the bus is unreachable, never
a default zero. This follows the context.limit-unresolved precedent: a missing
reading is UNKNOWN, not a false zero.

## 3. CANONICAL HOME: DIRECTOR. seat.epoch must be monotone, never sourced from the shift generation

R-55 makes `authority.seat.epoch` a monotone fencing token. The hazard: do NOT
source it from the shift generation. `abortStuckShift` rolls the shift
generation backward, which would make the fencing token non-monotone and break
the guarantee a receiver relies on to reject a stale epoch (R-69). Source the
epoch from a separate monotone counter; a NATS KV revision serves, as the
schema `$comment` on `authority.seat` already hints. This is a constraint on
the identity-lane seat model, which populates the epoch. Nothing populates
`authority.seat.epoch` today (verified: nothing in marvel emits an authority
block at all).

## 4. Stacked-PR merge-order gotcha, and the local check that settles it

Adding commits to a base branch after a stacked PR has branched off it leaves
the stacked PR's GitHub `mergeable` at UNKNOWN. That is GitHub recomputing the
merge asynchronously, not a conflict. It happened here: #250 branched from an
earlier #249 head, then #249 gained two README commits (the principal note and
the R-87 caveat), and #250 read UNKNOWN.

Do not trust or fear the UNKNOWN. Settle it locally:

```
git merge-tree --write-tree <base-branch> <stacked-branch>
```

Exit 0 with a tree oid means a clean merge; a nonzero exit prints the
conflict. #250 verified clean this way (merged tree f0793be), because the
README additions were in disjoint regions. If a real conflict ever appears,
resolve it with a merge commit, never a rebase or force-push on a pushed
branch.

## 5. CANONICAL HOME: DIRECTOR. R-87 and R-88 semantics; Validate is necessary, not sufficient

R-87: an empty body is refused before any ack, and a bare wake is an explicit
INFORM (it carries content, it is not an empty body).

R-88: the recipient receipt is the recipient's durable write echoing a digest.
It is never the JetStream ack. It is additive and rides an existing
performative, so it is a forward slice, not a change to the frozen envelope.

The implementation caveat that made R-87 necessary (recorded in
`contracts/README.md` at #249): `content.data` is optional by design (a
`signal` carries no body, a `pointer` carries refs), so `envelope.Validate()`
passing does NOT prove a `text`, `task`, or `result` body is present.
Validation is necessary, not sufficient. Every emitter must enforce non-empty
data for those content types on the emit path. The director-mcp shim does this
in `publish()` and validates emit-only, with a lenient receive path so a
migrated emitter never rejects un-migrated in-flight traffic. Any future
marvel producer inherits the same emit-path policy when bga gives marvel one.

## 6. Consumer integration: import the marvel contracts package, do not vendor

Architect ruling: a Go consumer of the envelope contract imports
`github.com/arcavenae/marvel/contracts/go/envelope` directly rather than
vendoring a copy. Canonical stays in `marvel/contracts` as an importable Go
path. The Rust consumer (beadle) keeps a sha-pinned schema copy plus a drift
guard, because Rust cannot import a Go package. No new shared repo is created.

The trigger for extracting a standalone shared-contracts module is a
fleet-wide toolchain need, NOT merely a third consumer. This supersedes the
earlier reading of SOUL section 7 that a third consumer alone would force the
extraction; the director-mcp shim being the third consumer does not trigger it.

Mechanics for a stacked consumer PR before the contract merges: require a
pseudo-version against the contract branch commit so the PR builds now, and
flag a one-line repin to the marvel release version once the contract PR
merges (director#13 does exactly this).

## 7. $id path naming: the subject test picks the path

The `$id` path names the owner of the vocabulary, decided by the subject test.
The envelope is `https://schema.arcaven.com/director/envelope/v1` (the director
protocol family); the event twin is
`https://schema.arcaven.com/marvel/adapter-event/v1` (the marvel-owned adapter
seam). The `director/` segment names the protocol family, `marvel/` names the
owning repo's seam; the shared base `https://schema.arcaven.com` is the
operator ruling D8. (The naming PRINCIPLE is shared; CANONICAL HOME for the
principle as a convention is the director register.)

Decide the path before any consumer pins it. Once beadle or the shim pins an
`$id`, a rename costs one PR per pinning repo; before the pin it costs one
line. The event twin was renamed from `director/event/v1` to
`marvel/adapter-event/v1` at freeze, before beadle pinned it, for exactly this
reason.

## 8. The CAS primitive already exists in the Bolt update closures

For the bus route-record key (bga), there is no need to invent a
compare-and-set primitive. marvel's `internal/api` store already has it:
`UpdateSession`, `UpdateTeam`, and `UpdatePolicy` are lock-guarded
read-modify-write closures (serialized under `s.mu`, which is CAS at the store
level), and `CreateSession` is insert-if-absent (it returns `ErrAlreadyExists`
on a key collision rather than overwriting). Reuse these closures for the route
record.

---

## Pointers

- Shas: #248 @415d65e (frozen), #249 @19efa39, #250 @28eeca6 (verified clean
  merge onto #249, tree f0793be). director#13 (shim emitter). beadle#62 (Rust
  envelope). aae-orc #313 (role party).
- Source of truth and rationale in the marvel repo: `contracts/README.md`
  (decisions, A2A mapping, the event-twin section, the R-87 caveat, the
  principal identity-lane note); `contracts/schema/director-envelope.schema.json`
  and `contracts/schema/director-event.schema.json` (the `$comment`s carry the
  reasoning, and the event schema names `events.go` as source of truth);
  `contracts/a2a/PINNED.md` (A2A provenance and sha256); `internal/runtime/
  events/events.go` (the event vocabulary source of truth, now carrying
  `AllKinds`).
- Register: `director/sim/requirements.md` (R-01 through R-88).
- The first forward-info batch (seven earlier items: JSON-Schema-first, the
  A2A proto-normative premise, the profile-on-A2A-metadata rule, the frozen
  envelope shape, the Go/Rust codegen split, the event-twin schema-as-wire-twin
  ruling, and identity-at-launch) is in the harvest record, not repeated here.
