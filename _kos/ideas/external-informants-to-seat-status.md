# External informants: how a watcher tells marvel what it found about a seat

- **Status:** idea (pre-hypothesis, no commitment). Exploration only: no
  frontier node, probe or ticket, and it may sit in tension with the nodes
  and designs it links.
- **Date:** 2026-11-12
- **Seed (operator's words, via director; one private project name redacted in brackets):** "the health and status of an
  agent might be known by marvel, or informed by special services like
  [the watcher service] or the majordomo function which has not been fully defined
  yet. but imagine that we need some way for [the watcher service] to update or inform
  marvel about discoveries it makes when it's watching or fixing agents"
- **Subject:** marvel (what marvel knows about a seat's health and status,
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
   SOUL section 3 applied to a new caller, not a new credential class.
3. **Observed versus reported.** marvel's own readings and an informant's
   reports must stay distinguishable on every surface, so a wrong informant
   cannot overwrite what marvel measured. A report carries its source and
   its time; marvel's own reading wins where both exist, or both show.
4. **Expiry.** A report goes stale like a quota reading does. What is the
   default sell-by, and who refreshes or withdraws it?
5. **Text.** The watcher service's read contract drops free text because pane rows
   can reach it. Reports flowing the other way need the same rule: kinds and
   fields from an allowlist, no pane text, no credential material.
6. **Independence.** marvel must work with no informant at all, and an
   informant must degrade to its own records when marvel is absent
   (component independence, ADR-005). The channel is optional on both ends.

## What this is not

Not a decision that the watcher service or the majordomo exists in a given shape;
both are undefined or partly defined. Not a request that marvel act on a
report. Acting stays with whoever acts today; this idea is only about marvel
knowing.
