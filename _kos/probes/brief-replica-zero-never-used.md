# Probe brief: scaling never-used seats to zero replicas to recover their resources

**Status:** answered 2026-10-06, see the finding; mail delivery at 0 was read from code, not measured. Opened 2026-10-06 by the operator: "we have a few agents completely idle across the sessions, let's kos probe marvel can we leverage replica 0 to recover resources for agent sessions that have never been used on kinu?"

Finding: `finding-marvel-l1bh-replica-zero-never-used.md` (minted with `kos id`).

## Why

A seat that has never taken a turn still holds a harness process, its MCP children and a tmux pane. On 2026-10-06 the director counted four such seats on one cluster, about 1.7G RSS together: a supervisor, an author, a filer and a second author, on three teams. If replicas = 0 parks a role cleanly and a scale-up brings it back the same, that memory comes back without losing the seat's place in the fleet.

## Questions

1. **Signal.** How does marvel tell "never used" (no first turn) from "idle", and is that signal reliable?
2. **Round trip.** Does replicas = 0 keep the manifest, the address and the routing, so that a later scale-up restores the seat cleanly? What happens to the seat's bus presence, and to messages sent to it while it is at 0?
3. **Who acts.** Is this an operator act (`marvel scale`, or a manifest edit), or a marvel feature such as scale-to-zero for never-used seats with wake on first message? Under SOUL section 8, marvel may propose; it does not judge.
4. **Interactions.** Shift blocks and `max_age`; `marvel scale` validating against the live spec; the replicas constraint in marvel#452 (decision D5).

## Hypotheses (to be confirmed or refuted)

- H1. marvel has no "never used" field. The nearest signals are the statusline heartbeat (no context reading, no model) and the transcript (no assistant turn). The heartbeat alone cannot tell "never used" from "statusline not fed".
- H2. replicas = 0 keeps the team and role records, because the spec is the manifest. It ends the session, so the seat's bus presence ends, and a message sent meanwhile waits on the bus, or is lost, depending on the stream and consumer. This probe measures that rather than assuming it.
- H3. Scaling is an operator act today. A feature would be a proposal (an event or a `get` column that names never-used seats), not an automatic scale-down.
- H4. A team with a shift block or `max_age` may refuse, or may undo, a replicas change made outside its manifest.

## Method

Measure only. No `marvel scale`, `marvel work` or manifest edit on any live team; the operator applies those.

- **Observational (live, read only):**
  - `marvel get sessions`, `marvel describe` and the statusline fields for the four seats;
  - each seat's transcript, checked for whether any assistant turn exists, by file presence and record type only;
  - `ps` RSS for the harness and its children.
- **Code (read):**
  - how the reconciler treats replicas 0;
  - what `marvel scale` validates;
  - how shift and `max_age` read replicas;
  - what marvel#452 D5 says;
  - how the director shim's presence and durable consumers behave when a session ends.
- **Interventional (scratch only):** a scratch team of my own on a scratch workspace with a cheap runtime. Run scale 1 to 0 to 1, recording before and after the address, routing, presence, and one message sent while it is at 0. Nothing on a live team.

## Pass and fail

- Q1 passes if a signal separates never-used from idle on all four seats and on at least one known-used idle seat, and fails if one seat reads the same both ways.
- Q2 passes if after 0 to 1 the seat has the same address and role, receives the message sent at 0 (or the loss is measured and named), and its manifest and spec are unchanged.
- Q3 and Q4 are answered with file:line, as recommendations to the operator.

## Disclosure

marvel is public. Seats are named by role only, never by workspace or team. No home paths, host-specific counts beyond the four-seat example, instance ids or IP addresses.
