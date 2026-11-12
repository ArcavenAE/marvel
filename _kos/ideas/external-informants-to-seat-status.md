# External informants: a watcher that follows marvel and tells it what it found

- **Status:** idea (pre-hypothesis, no commitment). Exploration only: no
  frontier node, probe or ticket, and it may sit in tension with the nodes
  and designs it links.
- **Date:** 2026-11-12
- **Seed (operator's words, via director; one private project name redacted in brackets):** "the health and status of an
  agent might be known by marvel, or informed by special services like
  [the watcher service] or the majordomo function which has not been fully defined
  yet. but imagine that we need some way for [the watcher service] to update or inform
  marvel about discoveries it makes when it's watching or fixing agents"
- **Subject:** marvel (what marvel knows and shares about a seat's health and status,
  and who may add to it). The watcher service and the majordomo are objects here:
  they are informants, not owners of the status. Filed in the marvel graph
  per the subject test.
- **Related:** [[health-signal-taxonomy]] (the signals marvel reads today),
  [[non-llm-bus-publishers-and-watchdog]] (non-model members that publish,
  and a watchdog that acts on silence), [[tmux-harness-state-watchdog]] and
  [[deaf-seat-status]] (status marvel derives itself), [[services-catalog]]
  (the watcher service has no service class yet), and
  `docs/design/service-provider/01-brief-prd.md` (the caretaker, or
  majordomo, as a boundary). The watcher service's own read contract (kept
  in its private repo) covers the other direction: what it takes from
  marvel's event ring.

## The idea, in two sentences

Today marvel's view of a seat comes only from what marvel itself observes
(process, pane, heartbeat, bus beat, harness state). A watcher such as
the watcher service, or a future majordomo, learns things marvel cannot see while it
watches or repairs a seat, and needs a way to hand those discoveries back so
that `get sessions`, `describe` and the event ring reflect them.

The channel runs both ways. Operator addendum, same day: "and it needs to be
able to participate in getting updates for events and status from marvel
also". An informant is first a subscriber: it follows marvel's event ring
and session status as they change, so it acts on what marvel already knows,
and then reports what it found. One identity, one channel, two directions.

## What a discovery might be

- A diagnosis: "this seat is stuck on a startup dialog", "this seat's inbox
  is behind a replayed backlog", "this seat is live but deaf".
- An action taken: "I answered the dialog", "I advanced the durable",
  "I restarted the harness", with the result.
- A caution: "do not shift this seat for ten minutes, I am mid-repair".
- A retraction: "my earlier diagnosis was wrong".

## Questions worth a probe later

1. **Where does it land?** A new event kind in the ring, an annotation on
   the session resource, a condition with a reason (k8s-style), or all
   three. Each has a different lifetime and a different reader.
2. **Who may write it?** An informant needs an identity marvel issued and
   can revoke, scoped to the seats it watches. This is the audience test in
   aae-orc SOUL.md section 3 applied to a new caller, not a new credential class.
3. **Observed versus reported.** marvel's own readings and an informant's
   reports must stay distinguishable on every surface, so a wrong informant
   cannot overwrite what marvel measured. A report carries its source and
   its time; marvel's own reading wins where both exist, or both show.
4. **Expiry.** A report goes stale like a quota reading does. What is the
   default sell-by, and who refreshes or withdraws it?
5. **Text.** The watcher service's read contract drops free text because pane rows
   can reach it. Reports flowing the other way need the same rule: kinds and
   fields from an allowlist, no pane text, no credential material.
6. **The read side.** What does an informant subscribe to: `events.watch`
   (the existing ring subscription, bounded queue, drop-oldest, gap
   reported), a session-status stream that does not exist yet, or both? A
   gap the ring reports must reach the informant as a gap, never as quiet.
   Scope matches the write side: an informant reads the seats it may report
   on, and its read grant and its write grant are one record.
7. **Independence.** marvel must work with no informant at all, and an
   informant must degrade to its own records when marvel is absent
   (component independence, aae-orc decisions/adr-005-component-independence.md). The channel is optional on both ends.

## What this is not

Not a decision that the watcher service or the majordomo exists in a given shape;
both are undefined or partly defined. Not a request that marvel act on a
report. Acting stays with whoever acts today; this idea is only about marvel
knowing.
