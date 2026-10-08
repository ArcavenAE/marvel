# A lasting leaf-down reaches the cluster's supervisor as a bus event (#580)

Status: design, for review. Ruled direction: a bus event (operator, 2026-10-08,
on `wake-service.md` section 7 Q9, path 1). Nothing here is built.

## 1. What is already true

#600 (merged 2026-10-06) did the marvel half of `wake-service.md` section 8:

- `observeLeaf` (`internal/bus/supervisor.go`) reports a drop once, reports an
  enrolled and attached leaf found down at the first poll, and repeats
  `bus.leaf.down` every `defaultLeafDownRepeat` (30 minutes, the operator's
  2026-10-06 ruling on Q1) while an enrolled, attached leaf stays down;
- `marvel bus status` shows `leaf_since` and `leaf_for`.

The event still stops at the ring. No supervisor hears it unless it runs
`marvel events`. What is left is delivery, and this note covers only that.

## 2. The event

**Subject.** `events.marvel.<cluster>.bus.leaf.down` and
`events.marvel.<cluster>.bus.leaf.up`, on the **local** broker. This is the
tap's subject shape (`internal-bus.md` section 6): the kind is the subject
tail, so there is no second catalog.

The local broker is the only broker that can carry the event. A leaf-down
means the link to the hub is gone, so an event that needs the hub to reach
its reader cannot arrive while the condition holds. The subscriber has to be
on the same cluster, and lifting the event to the hub is out of scope.

**Payload.** The `events.Event` JSON as the tap projects it, unchanged, plus
one optional field:

| field | value |
|---|---|
| `kind` | `bus.leaf.down` or `bus.leaf.up` |
| `severity` | `warning` for down; see section 4 for up |
| `ts` | when this report was taken |
| `since` (new, optional, RFC3339) | when the current up or down state was entered (`leafSince`) |
| `message` | as today, with the hub URL and "down for 2h9m" |

`since` makes "how long" a field a reader can compare against its own bound.
Today the duration exists only inside `message` prose. The field is generic
(any state-entered event may set it), so the tap needs no special case.

## 3. Who emits, and when

- **Emitter:** the marvel daemon on the cluster whose leaf is down. The bus
  supervisor's `observeLeaf` writes the ring, and the tap publishes from the
  ring. The hub side emits nothing; it would need a per-leaf registry it does
  not have.
- **When:** at #600's report moments, unchanged. That means a drop; a first
  poll that finds a promised leaf down; a down leaf that becomes enrolled or
  attached without having been reported; and every 30 minutes while a
  promised leaf stays down. `bus.leaf.up` fires once, when the link recovers.

## 4. The bound, and one gap in it

**The bound is the 30-minute repeat that is already ruled.** Every report
crosses the tap. A reader tells a fresh drop from a lasting one by
`now - since`, so no second constant is needed. A supervisor that wants to
act only past the bound acts on the first report whose `since` is 30 or more
minutes old.

**The gap:** the tap filters at `MinSeverity: SeverityWarning`
(`internal-bus.md` section 3), and `bus.leaf.up` is emitted at
`SeverityInfo`. Under the planned tap, a supervisor would hear the outage and
never the recovery. Recommendation: emit `bus.leaf.up` at `warning` when
the down it ends was reported (`leafReportedAt` set), and at `info`
otherwise. A recovery the supervisor was told about then closes on the same
channel. Changing the tap's filter would also work, but it widens the tap
for every kind, so this note does not recommend it.

## 5. Who listens

The cluster's supervisor, through a subscriber on the local broker to
`events.marvel.<own cluster>.bus.leaf.>` that turns each event into an inbox
item for `role://<team>/supervisor`. That subscriber is director-side, not
marvel. marvel publishes and holds no agent address, which keeps the
constraint `usage-limit-pause.md` records. It runs on the same cluster as the
event, for the reason in section 2. Which director component carries it is
open (Q-b).

## 6. Build order

1. marvel (aae-orc-x2wz4), small and needing no tap: the `since` field, and `bus.leaf.up` at
   `warning` after a reported down (section 4). Tests first: an up after a
   reported down is `warning`; an up after an unreported down is `info`; a
   repeat carries the `since` of the original drop.
2. The tap (`aae-orc-zhx6x`), which already waits on `aae-orc-31or7`,
   `aae-orc-lgthv` and `aae-orc-umw8p`. Delivery to a supervisor depends on
   it.
3. The director-side subscriber (section 5), aae-orc-mn10f.

Step 1 can be built now, and on its own it changes nothing a reader sees on
the ring except a field and a severity.

## 7. Open questions

- **Q-a.** Should a cluster with no subscriber still get a direct delivery
  (path 2 in `wake-service.md` section 7)? Open; nothing has ruled it.
  Tracked with Q-b on the subscriber ticket, aae-orc-mn10f.
- **Q-b.** Which director component subscribes on the local broker, and how
  it maps `<cluster>` to the right team's supervisor.
