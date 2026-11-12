# Seat lifetime cost: marvel measures what a seat costs over its life

- **Status:** idea (pre-hypothesis, no commitment). Operator, 2026-11-12.
- **Subject:** marvel (seat lifecycle and metering: spawn, idle, shift, exit,
  and the context it reads). The routing choice is an OBJECT from director; the
  lifetime a role declares is an OBJECT from the role library.
- **Related:** `naked-seat-launch` (this graph); director idea
  `role-lifetime-economics`; role-library idea `role-lifetime-field`; the
  agentic resource matrix (context, spend and cache locality are its rows).

## The observation

The operator, verbatim: "we should also study which roles merit long term use,
and what the benefit/cost is to using more one off, ad hoc, and ephemeral
agents for certain roles. this is a complex topic as cache may save many
tokens for repeat action, under the right circumstances. but larger contexts
and long sessions or cache idling out are also costly".

One reading from kinu, 2026-11-12, the director session over 24 hours: 99.4M
cache-read tokens against 0.56M cache-write. Its cost was context size times
calls, not cache misses. That is one seat; other roles may differ.

## What marvel could measure

marvel already meters context occupancy (CTX%) and has a shift trigger on it.
To compare lifetimes it would also need, per seat:

- calls, and input tokens split into cache read, cache write and uncached;
- idle gaps longer than the cache lifetime, and the re-write each one caused;
- context at each call, so growth over a session is visible;
- cold-start cost at spawn (the first call's write).

With those, a lifetime choice becomes a number: a one-shot seat's cold start
times its runs, against a standing seat's reads plus its idle re-writes.

## Levers marvel already has, or nearly has

- shift on context pressure and max age, which bound growth;
- headless roles with completion semantics (ADR-010) and scheduled roles, which
  are the one-shot and per-burst shapes;
- scale to 0 between bursts.

## Open questions

1. Where do the token splits come from: the headless stream, the statusline
   feed, or the transcripts on the host?
2. Should marvel act on the numbers (scale an idle standing role to 0 past the
   cache lifetime), or only report them? SOUL section 8 suggests report first.
3. The cache lifetime and price multipliers belong in config read from the
   provider, never hard-coded.
