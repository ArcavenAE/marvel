# Supervisor succession: seats, leases, assistants, and an election ceremony

- **Status:** idea (pre-hypothesis, no commitment). A design study with
  recommendations only. Nothing here changes a manifest, a seat file, or code.
  Output of a four-voice party (distributed systems, test and risk, operator
  sovereignty, marvel implementation) over an architect's draft, one argue
  round, conditioned unanimous vote. Commissioned as STUDY 1 through the
  migrated team's supervisor on behalf of director, 2026-09-26.
- **Subject:** marvel (slot identity and the seat record at spawn, the shift state machine, the
  supervisor predicate, the lease record when no bus is present). Director is
  an OBJECT for everything on the bus side (durables, custody, fencing checks,
  named refusals), and each recommendation below is labelled with its owner so
  the director half can be lifted into director's register without
  re-derivation. Filed in marvel per the subject test because the succession
  mechanism is marvel's.
- **Reconciles with, does not replace:** [[shift-change-succession-protocol]]
  (the handoff slot, marker gate, `MARVEL_HANDOFF_IN`; this study adds seat,
  lease, and election on top of it) and wardrobe
  `contents/fragments/succession.md` (the authored content contract, unchanged
  here). Also [[identity-at-launch-and-managed-nats]] (identity projected at
  spawn), director design briefs `director-seat-lease.md` (SEAT-A..G) and
  `continuous-custody-succession.md` (CUST-A..H), director requirements R-50
  (amended), R-54/R-55, R-106, R-140..R-145, R-151, and the
  supervisor-development synthesis (tolerance bands, decision rights).
- **Specimen:** bd `aae-orc-nzh7c`.
- **Reconciled with:** the agent-identifiers party recommendation
  (2026-09-26, seven seats; bd `aae-orc-ep8n3`), received after the first
  draft as director's summary, and orc `docs/design/bus-address-hierarchy.md`
  (the fleet, cluster, slot, inst tokens adopted 2026-09-16). This doc now uses
  their names; see "Reconciliation with the agent-identifiers recommendation"
  below. Two items still wait on the operator's rulings and stay marked
  **[R1]** (pet name as key or label) and **[R2]** (scale-down retires the
  seat record or hands it back).

## The problem, in one sentence

Marvel can replace a supervisor's process, but nothing names the seat the
process was filling, so the successor inherits neither the predecessor's inbox
position nor its authority, and the only guard against doing acknowledged work
twice is a human checking GitHub by hand.

## The specimen, and its two mechanisms (verified against code)

At 04:50Z to 04:52Z on 2026-09-26 marvel shifted all five teams on one cluster
in about two minutes, gated on process-alive, with no handoff artifacts. The
g3 supervisor's first poll reported "no earlier durable for this seat" and
replayed global seqs 51 to 78 (27 messages), including REQUESTs the g2
supervisor had already handled. Separately, a supervisor-to-supervisor send
across workspaces timed out, and director had to relay.

Checked at director `origin/main` and marvel `origin/main` (e360c58):

1. **The seat key carries the generation.** director-mcp names a durable
   `mcp_<AgentID>_<instance>` and computes the resume floor over durables
   matching the prefix `mcp_<AgentID>_` (`director-mcp/bus.go`, `seatAckFloor`).
   marvel sets `DIRECTOR_AGENT_ID` to the session name
   (`internal/runtime/adapter.go:325`), which is
   `<team>-<role>-g<generation>-<index>`. The comment there says so: a shift or
   restart mints a new id by design. So g3 never matches g2's durables. This is
   not a shim defect; it is marvel projecting an instance name where a slot id
   belongs. R-151 (RULED) already says the instance name only reaches a live
   process.
2. **A live predecessor's floor is skipped.** `seatAckFloor` ignores any
   durable whose instance still has a live presence row, and a crashed
   session's row lingers up to the 90s bucket TTL. During any overlap in which
   the predecessor is draining, the successor ignores its position and replays.
   Fixing mechanism 1 alone does not fix the specimen.

The cross-workspace timeout is a third, separate fact: marvel renders one
broker user per team (`internal/bus/declared.go:209-212`), publish and
subscribe `agent.<ws>.<team>.>`, plus `global.director.inbox` and
`global.<domain>.>` for a team with a supervisor. A publish to another
workspace's inbox is a permissions violation that the client sees only as a
deadline.

And a fourth, found by the party and decisive for the design: **the shim acks
on receipt**, before the model acts (`bus.go`, `m.Ack()` inside `receive`). An
acked message has been read, not handled. Today that turns a generation change
into loud replay. If the floor were re-keyed to the seat without changing the
ack point, the same crash window would turn into **silent loss**: the successor
would start after messages the predecessor read and never acted on.

## Vocabulary: the adopted names

The first draft of this study used its own three names. They map onto the
adopted hierarchy and the identifiers recommendation as follows, and the rest
of this doc uses the adopted names.

| First draft | Adopted | Meaning |
|---|---|---|
| seat (the address) | **slot**, `<role>-<idx>`, generation-free (`supervisor-0`) | the stable address; the bus id becomes the slot (`aae-orc-s0qzd`); vacated indexes are reused (`aae-orc-8mlw1`) |
| seat state | **seat record**, one stored record per slot (`aae-orc-ep8n3`) | lineage epoch, predecessor, current instance, work pointers (home, checkout, branch, open GATE, bus durable) |
| instance | **inst** (a ULID at the shim) and the marvel session name `gX-Y` | one process; a liveness fact carried in the presence body |
| lease | **slot lease**, `seat.<ws>.<team>.<slot>` (`aae-orc-c0n9d`) | who holds the slot's authority now, with a fencing token issued at acquisition (R-55) |
| (none) | **epoch** | the seat record's lineage counter, bumped by each restart or shift |

Work, custody, queues, board lines, and handoffs bind to the slot and its seat
record (R-151). A slot can be filled by an instance that does not hold the
slot lease (a successor still orienting, or a zombie that lost it).

**Epoch and fencing token must be one number, or one must be derived from the
other under the same compare-and-set.** The recommendation's epoch bumps on
each restart or shift; the slot lease's fencing token is the KV revision at
acquisition. Two independently issued counters for the same question (is this
the current holder?) invite a zombie that holds a current epoch and a stale
token, or the reverse. This is a party condition (one authority per slot), not
a disagreement with the record.

Two kinds of succession follow, and they must not be confused:

- **Refill:** a new instance of the same slot, carrying the seat record and
  bumping its epoch. Mechanical. A restart, a crash
  respawn, or a shift. No authority changes hands between slots, so it is not
  an election and wakes no human.
- **Promotion:** a different slot takes supervisor authority (an assistant, a
  slot on another team, a worker). That is an election, and SOUL section 8
  keeps promotion a human act.

## Q1. No supervisor, or a supervisor gone dark

**Detection has three signals with three owners, and only two may start
succession.**

| Signal | Owner | Effect |
|---|---|---|
| Process verdict (pane gone, crash, crashloop) | marvel | may start a refill |
| Lease renewal lapsed past TTL | the team's lease store (see Q4) | may start succession |
| No first turn (R-143), ack timeout, stale inbox age (R-117) | director | diagnostic only; feeds a proposal |

A busy supervisor must not be replaced because it was slow; the supervisor role
already forbids declaring death from heartbeat absence. A `kill -STOP`ed
supervisor is the case the lease exists for: alive to marvel, silent to
everyone, and caught only by a lapse.

**A team with no supervisor degrades, it does not stop and it does not
self-organize.**

- Workers keep executing their current mission orders inside their declared
  tolerance band (synthesis Q3/Q4). Anything outside the band holds.
- Supervisor role mail is held, store-and-forward (R-141), and delivered to
  whoever next holds the seat. A role send with no live holder is refused
  loud to the sender (R-142, director#86), which is correct.
- Escalations go one level up. Director may relay and summarize a decision to
  the human; it never makes the decision (ADR-007).
- No worker promotes itself, and no order is inferred (see Q5).

## Q2. Shift change, and assistant supervisors

### Shift change (refill across a generation)

Build on the handoff slot idea, and add what the specimen showed it lacks:

1. **marvel: one reuse rule and an explicit succession step** (marvel#363,
   `aae-orc-8mlw1`). Today `nextIndex` returns max+1 over the role's live rows
   in the generation (`internal/team/controller.go:2259`). So a single-replica
   role, or the highest index, gets its number back after a health restart,
   while a crashed middle replica returns under a new, higher index, and
   `shiftLaunch` starts a new generation with no old-to-new mapping at all.
   The first draft said the index "drifts on every crash"; that overstated it,
   and the recommendation's "health restart yes" holds only for the top index.
   The rule to build is the lowest free ordinal in `[0, replicas)`, with the
   shift carrying each old slot to the same new slot. Stamp `MARVEL_SLOT` and
   `MARVEL_GENERATION` at spawn (hierarchy change 1), and move
   `DIRECTOR_AGENT_ID` and `BEADS_ACTOR` to slot form: the recommendation
   settles both, because every durable reference keys off the slot
   (`aae-orc-ep8n3`, `aae-orc-s0qzd`). The first draft held `BEADS_ACTOR` back
   for bd attribution continuity; that cost remains real and is now a
   migration note, not a blocker: the seat record's predecessor chain is what
   keeps old instance-keyed attribution traceable.
2. **director: key the durable on the slot, and transfer the floor at lease
   acquisition, not at spawn** (`aae-orc-oxy50`). R-50 (amended, ruled,
   unimplemented) already says one durable per address, rebound on restart.
   The successor rebinds the slot's durable when it acquires the slot lease; a
   draining predecessor's
   position counts rather than being skipped as a live sibling.
3. **director: move the ack point.** Ack after the custody record is written
   (CUST-E, record before act), or at minimum have the handoff slot list the
   received-but-unhandled seqs. **Condition, recorded as dissent-backed: items
   2 and 3 ship together or not at all.** Re-keying without moving the ack
   swaps a loud failure a human caught for a silent one nobody sees.
4. **marvel: gate the supervisor's drain on the handoff marker or lease
   release, not on liveness alone.** Today's gate is `readinessGate`
   (`internal/team/controller.go:2106`): `running`, or the role's healthcheck
   type when one is declared. Either is liveness; neither says the handoff was
   written.
5. **marvel: stagger supervisor-seat transitions.** At most one supervisor
   seat per cluster in transition at a time, and operator-initiated fleet-wide
   shifts get the per-tick budget auto-triggers already have
   (`maxAutoShiftsPerTick`). A mass shift is surfaced as one coalesced notice.
   The specimen was a storm, and every race multiplies under one.
6. **Overlap window.** The predecessor sets presence to draining and answers
   QUERY and REQUEST about its handoff only; it issues no new directives. The
   successor acquires the lease only when the predecessor releases it or the
   TTL lapses (R-145: harvest and handoff before the kill).

### Assistant supervisors (load, not succession)

- **An assistant is its own role with its own seats,** never a second holder
  of the supervisor seat. On the global tier every holder of the supervisor
  role filters the same inbox, so a second holder receives every message
  (fan-out, R-142). A separate role avoids that by construction.
- **Scaling inside manifest-declared min/max is a mechanism;** outside the
  bounds it is a proposal. marvel does not read director inboxes: director
  (or the operator) submits a desired count through marvel's API, and marvel
  clamps it to the bounds and refuses anything outside as a proposal. That
  keeps the load signal (inbox depth and oldest-unread age, R-117) on
  director's side of the independence line.
- **The delegable set is operator-declared.** The manifest names which
  partitions (per worker, per stream, per ask class) an assistant may hold.
  The supervisor cannot widen it; choosing partitions is scope, which is
  judgment.
- **Assistants are outside the primary succession order** unless the manifest
  names them in it. An assistant that ends up holding the primary seat because
  it happened to be scaled up is promotion by accretion.

## Q3. Each supervisor instance's own state

Three layers. The rule is that anything that must survive is written through to
the seat layer before the instance acts on it (CUST-A, CUST-E).

| Layer | Contents | Lifetime | Owner |
|---|---|---|---|
| Seat record (per slot) | lineage epoch, predecessor, current instance; pointers to home, checkout, branch, open GATE, bus durable (consumer cursor), handoff directory; owned asks and claims (custody); the slot lease | outlives every instance of the slot; retired on scale-down **[R2]** | marvel (record, slot, pointers, lease when no bus), director (cursor, custody, lease on the bus) |
| Instance state | model context, pid, pane, the fencing token it currently holds | disposable | the instance |
| Team records | bd, git, PRs | independent of both | the tools that own them |

A successor rebuilds from the seat record and team records alone. A red test proves
it: `kill -9` mid-task after accepting an ask, discard all instance state, and
require the successor to reconstruct its owned asks. That fails today (no
custody store, no handoff field in `ShiftState`).

## Q4. Negotiating messages among supervisors without double handling

Two layers, because transport alone cannot give exactly-once.

**Transport (director).** Slot-keyed durable plus the deferred ack from Q2, so
acknowledged mail is never redelivered as new and read-but-unhandled mail is
never skipped.

**Semantics (director, enforced in the shim, not by the model).**

- **Claims live in the store, not the transport.** Work that must be handled
  once among several supervisors or assistants is claimed with a create-only
  key on the ask or message id. Role mail stays fan-out (R-142); the pick-one
  mode (aae-orc-hieji) stays unruled, and a store claim makes it unnecessary
  for correctness.
- **Claims move through states and do not expire:** `claimed(token)` to
  `done(result)`. A claim with a TTL lets a later replay run the work again. A
  successor with a higher token that finds claimed-not-done reconciles the
  external effect before re-running anything.
- **Fencing reaches writes, not only directives.** Custody and KV writes are
  conditioned on the current token. Every directive a supervisor sends carries
  its token and the receiving shim rejects a lower one. Worker-side checking by
  an LLM reading tokens is not enforcement, so this is a director envelope and
  shim obligation, not a platform guarantee marvel can make.
- **External systems are unfenced resources.** GitHub, git push, and bd never
  see a token, and the specimen's near-duplicate was on GitHub. Before any
  externally visible act the handler checks its custody claim and checks
  current external state for idempotency. Today's hand-check of PR state is
  exactly this check, done by a person.
- **One lease authority per team,** declared in the manifest: the bus KV when
  a managed bus is up, marvel's store otherwise, never both. Two stores with
  independent TTLs is split-brain built in.
- **Partition behaviour.** The lease lives on the hub for global seats. The
  holder self-fences: it stops issuing directives when its own clock passes
  lease expiry minus a skew margin, reachable hub or not (TTL greater than the
  renewal interval plus maximum skew). Leaf workers that cannot validate a
  token against the hub hold new directives rather than obey them. Otherwise a
  leaf-isolated zombie keeps commanding local workers while the hub seats a
  successor.

**Cross-workspace supervisor negotiation.**

- **director:** turn the permissions violation into a named refusal. The NATS
  server reports a publish permissions violation to the client's async error
  handler; map it to a refusal naming the subject and user, and fail the send
  fast (R-09, synthesis Q3). Waiting for the deadline is what hides it today.
- **marvel, opt-in:** a manifest-declared supervisor peer subject on the global
  tier (for example `global.<domain>.supervisors.<seat>.inbox`) rendered into
  the supervisor principal's grants. Off by default.
- Until then, director relay is the sanctioned path, and it is the escalation
  path, not a workaround.

## Q5. The election or promotion ceremony

**The default for any vacancy is the proposal path: hold and escalate.** An
acting tenure is the exception, opt-in per manifest.

| Step | Actor |
|---|---|
| 1. Detect vacancy on an authoritative signal (lease lapse or process verdict) | machine |
| 2. Nominate from the manifest-declared succession order, traced to a specific apply (who, when) | machine |
| 3. Draft the proposal card with evidence (below) | machine |
| 4a. Default: human confirms, overrides, or re-seats | **human** |
| 4b. Opt-in: if the manifest declares the order AND enables acting tenure AND the lease store offers the four primitives, an acting tenure starts with a narrowed grant | machine, executing frozen operator intent |
| 5. Acquire the lease create-only; a new, higher fencing token | machine |
| 6. Announce the holder and generation change to workers and director as a delivered event, not a discovered one (R-144) | machine |
| 7. Successor first acts per wardrobe `succession.md`, including the singleton query and hold-and-escalate on a live rival | agent |
| 8. Confirm reachable (R-143: first turn, seat file and handoff read) | agent, observed by director |
| 9. Ratify acting to confirmed, or override, or revoke | **human** |
| 10. Close the succession record | **human** |

Rules that keep step 4b from becoming promotion by accretion:

- **Acting never becomes confirmed on a timer.** An unratified acting tenure
  has an expiry. At expiry it narrows further to hold-and-escalate (answer,
  hold, relay; no new directives). It never widens. It re-sends one reminder
  per renewal.
- **The acting grant is a machine-checked deny-list,** not prose. It excludes
  every non-delegable act (merges, scope changes, operator-declared merge
  exclusions under R-153, anything production-facing). marvel spawns and
  stamps; it does not interpret the list. The acting state is status
  (`toml:"-"`), never spec.
- **No declared order means no order.** When a user-defined team declares
  none, or the order is used up, nothing is inferred (not by seniority, not
  the first assistant, not the first worker). Straight to the proposal path.
- **Automation shrinks with the primitives available.** Step 4b needs a
  create-only lease with fencing. With the bus off, on a backend lacking the
  primitives, or under marvel alone with no lease consumers, every vacancy is a
  proposal and a human re-seats.
- **Refill never runs this ceremony.** Only promotion does.

**The human's card (one decision, target under a minute):** vacancy cause and
its authoritative signal with sample ages; the nominee and its basis (manifest
line, apply date, applier); what the acting grant excludes; counts of the
inherited unacknowledged set and custody set; the rival or singleton check
result; handoff present or absent and whether its marker is valid; three keys:
confirm, override (pick another), revoke.

**What does not wake the human:** a refill, scaling inside bounds, a single
shift. An acting tenure starting is a coalesced notice. Two live leases, or a
vacancy with no nominee, is a must-wake through director.

## Marvel's supervisor predicate is a string today

marvel knows which role is the supervisor only by the literal role name
`"supervisor"`: `shiftOrder` (supervisor last, `internal/team/controller.go`)
and `hasSupervisorRole` (`internal/bus/manager.go:361-368`, which sets the
team's global grants). There is no authority field on Role. So a user-defined
team whose lead role is named `lead` neither shifts last nor gets global
grants. Recommendation: a declared role capability (for example
`authority = "supervisor"`), falling back to the name for existing manifests,
with one predicate used by both the shift order and the bus grants. Teams are
user-defined, and a string match is an undeclared contract.

Naming trap for readers: `internal/bus/supervisor.go` supervises the
nats-server process. It has nothing to do with team supervisors.

## Composability: what survives in each composition

| Composition | What works | What does not |
|---|---|---|
| marvel alone | slot and seat record, refill, handoff slot, succession order in the manifest, a lease record in bolt (create-only via a nil check, token from `NextSequence`, renewal on the heartbeat path, rehydrated at daemon start like `RoleHealth`) | nobody consults the lease, so it is a **record, not authority**; every vacancy is a proposal |
| director alone | KV lease, custody, human re-seats (R-139) | no automatic refill |
| bus off | bd notes as the mailbox (observed in nzh7c) | `bd update --claim` is not CAS, so double handling is possible; the supervisor must run the reconcile-from-records check before every externally visible act, not only at shift start |
| bus not NATS | the design, if the backend offers four primitives | anything missing a primitive drops to the proposal path |

The portable contract is those four primitives: a create-only put that returns
a monotonic revision; a per-address durable cursor with explicit ack; TTL
expiry of a key; an ordered append log. A conformance suite for them should
take the specimen replay as its first fixture.

## Red tests (each fails on today's code)

1. **Q1 dark:** `kill -STOP` the supervisor. Expect a vacancy event within the
   lease TTL, role mail held, a proposal to the human. Fails: no lease exists
   to lapse.
2. **Q2 specimen, two variants:** seed seqs 51 to 78, g2 acks through 78,
   spawn g3 (a) while g2 is live, (b) after g2 is gone. Expect g3's first
   delivered seq is 79 and zero REQUESTs redelivered. Fails today in both: (a)
   by the live skip, (b) by the generation-bearing prefix.
3. **Q2 loss guard:** as above, but g2 read seq 78 and crashed before acting.
   Expect 78 is redelivered or listed in the handoff. Guards the re-key from
   turning replay into loss.
4. **Q3 state:** `kill -9` after accepting an ask; successor reconstructs owned
   asks from the seat record alone. Fails: no custody store, no handoff field.
5. **Q4 once:** two holders plus a crash between act and ack, against a mock
   GitHub. Expect exactly one side effect. Sub-test: a cross-workspace send
   returns a named refusal, not a deadline. Both fail.
6. **Q5 election:** vacancy with a declared order and acting enabled. The
   acting holder gets a higher token; its non-delegable act is refused;
   `SIGCONT` the old supervisor and its directive is rejected; leave acting
   unratified past expiry and it narrows to hold-and-escalate. All fail: no
   tokens, grants, or expiry exist.

Tests 2 (overlap variant) and 5 are written red before any implementation; that
is a party condition.

## Ownership summary

- **marvel:** the seat record, `MARVEL_SLOT`, the reuse rule and the explicit succession step; the declared supervisor
  capability; drain gated on marker or lease release; staggered supervisor
  transitions; assistant count clamped to declared bounds; the lease record in
  bolt when no bus; the opt-in supervisor peer subject in rendered grants; the
  acting state as status.
- **director:** seat-keyed durable and floor transfer at acquisition (R-50,
  R-144); deferred ack; custody claims and their states; token checks in the
  shim; named refusal for permission violations; the proposal card; the
  generation-change event.
- **wardrobe:** unchanged; `succession.md` already carries the successor's
  first acts, including the singleton query this design relies on.
- **human:** manifest fields (order, bounds, delegable set, acting grant
  exclusions, acting opt-in); every proposal-path decision; ratifying or
  revoking an acting tenure; ruling on a live rival; any promotion; any change
  outside declared bounds; closing the record.

## Reconciliation with the agent-identifiers recommendation

The recommendation arrived after the party voted, as director's summary of
`50-recommendation.md` (the full file is gitignored and was not read here). The
architect reconciled it against the party's recorded positions; the party was
not reconvened. The two studies overlap on refill and are otherwise
complementary: the recommendation settles identity and what a refill carries;
this study adds what the recommendation does not cover (a dark supervisor,
assistants, double handling among several supervisors, cross-workspace
negotiation, and promotion across slots).

**Agreed, and adopted above.** Slot as the address, generation-free, vacated
indexes reused. A stored seat record per slot rather than a bare counter (the
recommendation's vote was 6-1; nothing this party found argues for the
counter). Reuse the address, never the state. No silent reactivation (7-0):
this study's rule that no succession order is ever inferred is the same stance
at the promotion level. An explicit succession step in marvel (7-0), which is
this study's Q2 item 1. Harvest before teardown (`aae-orc-5xa4a`, R-145), which
this study relies on for the overlap window.

**Where this party's recorded positions disagree with, or add conditions to,
the recommendation as summarized:**

1. **Carrying the bus durable is not enough, and alone it is worse.** The
   recommendation names silent duplicate actuation as the worst break and
   fixes it by carrying the durable in the seat record under a slot key. The
   shim acks on receipt, so that fix turns loud replay into silent loss of
   messages the predecessor read and never handled. Condition: the slot-keyed
   durable ships together with a deferred ack (after the custody record is
   written), or not at all. This is the distributed-systems dissent, and it
   is the one place the party would block the recommendation's build order.
2. **The explicit succession step must transfer the floor at lease
   acquisition.** Today `seatAckFloor` skips a predecessor that still has a
   live presence row. A succession step that carries the record at spawn while
   the predecessor drains still replays. The step's definition should say when
   the floor moves, not only that the record moves.
3. **Epoch and fencing token are one authority.** See the vocabulary section.
   The recommendation's epoch and `aae-orc-c0n9d`'s KV-revision token answer
   the same question and must not be issued independently.
4. **An epoch does not fence an external system.** The seat record points at
   an open GATE, a branch, and a checkout, all of which live in GitHub and git,
   which never see an epoch. The seat record tells a successor where to look;
   it does not stop a zombie predecessor from acting there. The custody claim
   and the idempotency check in Q4 are still required.
5. **Scale-down default [R2].** The party did not vote on it. The
   recommendation's default (retire) is the one this study's succession rules
   assume, because a handed-back record would let a regrown slot inherit open
   claims without any election. If the operator rules hand-back, the
   ceremony's rival check and the custody claims must cover the regrown slot.

## Party record

Four voices, one round over the architect's draft, then a vote on the amended
draft. All four voted **ACCEPT-WITH-CONDITIONS**; every condition is folded in
above.

- **Distributed systems.** Conditions: deferred ack ships with the re-key;
  claims move through states with no TTL; partition self-fencing. Dissent:
  fixing the floor without moving the ack is worse than today.
- **Test and risk.** Conditions: floor transfer at acquisition; acting expiry
  plus a machine-checked deny-list; external systems named unfenced; the
  overlap-variant and mock-GitHub tests written red first. Dissent: acting
  tenure should be off by default and opt-in, with expiry required (adopted:
  the proposal path is the default).
- **Operator sovereignty.** Conditions: acting never becomes confirmed on a
  timer; automation shrinks with the primitives; assistants outside the order
  unless named, delegable set operator-declared. Dissent: step 4 must show the
  proposal path as the default (adopted), and director may relay a decision,
  never make it (adopted in Q1).
- **marvel implementation.** Conditions: the declared supervisor capability,
  a stable slot id (first draft: without touching `DIRECTOR_AGENT_ID` or
  `BEADS_ACTOR`; the recommendation has since settled both), and one
  lease store per cluster all land before any lease code; every item labelled
  marvel or director. Dissent: worker-side fencing is a director envelope
  obligation, not a platform guarantee (adopted); a marvel-alone lease is a
  record, not authority (adopted); acting is status, never spec (adopted).

One disagreement resolved by the architect: operator sovereignty proposed that
an unratified acting tenure stay narrowed indefinitely with reminders; test and
risk proposed an expiry that reverts to hold-and-escalate. Both refuse
auto-confirmation, and the expiry is testable, so the design takes the expiry
and keeps the reminder.

## Open questions

- **[R1]** Whether a pet name is a key or only a label (party split 4-3
  label). Nothing in this study depends on the answer while the slot stays the
  address.
- **[R2]** Whether scale-down retires the seat record or hands it back. This
  study assumes the recommendation's default (retire; a regrown slot starts
  with a fresh epoch and empty pointers), which is also the safer one for
  succession: a regrown slot never inherits a predecessor's open claims.
- How an ADR-010 headless run that holds its slot counts against the seat
  record's current instance.
- The acting-tenure expiry value, and whether it scales with the team's
  tolerance band.
- Whether the deferred ack point is "custody written" or "act completed" for
  asks that have no custody record (plain INFORMs).
- Whether the supervisor peer subject is per workspace pair or one shared
  subject, given R-140 (several director instances and principals).

## Crystallization signal

A live incident with a verified two-part mechanism, a ruled requirement
(R-151) the code contradicts at one line, and a unanimous conditioned vote.
When the seat record lands (`aae-orc-ep8n3`), extract a probe brief for the slot-keyed floor plus the
specimen red test (test 2, both variants) and a frontier question for the
election ceremony's human steps. Follow-on tickets are not filed by this study;
the ownership summary above is their natural flat split.
