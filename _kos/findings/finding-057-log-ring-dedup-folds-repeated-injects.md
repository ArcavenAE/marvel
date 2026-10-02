# finding-057: the daemon log ring folds repeated injects, so a later doorbell has no timestamp

Work: harvest placement, 2026-10-02 (reviewer team on mokuzai). Observed
while I supervised three reviewer seats and checked whether my doorbells
landed. Verified by reading marvel at origin/main ee7666d and against the
live daemon (0.1.0-alpha.20260928, b1f4953).

**Placement.** marvel is the subject: the ring, its dedup, and the inject
log line are all marvel's.

## Observed

I doorbell a seat with `marvel inject <seat> "<fixed text>" --enter`. The
doorbell text is the same every time, so each inject logs the same message
body: `inject: aae/<seat> <- 65 bytes (literal=true, enter=true)`. When I
rang the same seat twice with nothing logged in between, `marvel daemon logs`
showed the first line with its timestamp and then `last message repeated
N times`. The later injects had no time on them.

I read that as "my most recent inject never reached the daemon" and
started to diagnose a failed doorbell. The inject had landed; the log was
folding it. A slice of the live ring (2026-10-01):

```
2026/10/01 19:26:10 inject: aae/<seat-a> <- 65 bytes (literal=true, enter=true)
last message repeated 2 times
```

That says three injects reached one seat starting at 19:26:10, but not when the
second and third arrived.

## Why

`internal/logbuf` collapses consecutive lines whose fingerprint (the message
with the Go log timestamp stripped) matches. This is deliberate: it came
from a poll loop that flooded the ring with one identical line
(`_kos/ideas/log-rrd-deduplication.md`), and the package comment says the
operator keeps "the timestamp of the first occurrence". For a polling
heartbeat that trade is right. For an inject, each line is an audit event,
and the question an operator asks is "did *this* one land, and when". Dedup
answers with the first one's time.

## Open questions

A. Should audit-class lines (inject, kill, shift, scale) be exempt from
dedup, or carry a per-call id so consecutive ones never match? The flood
case that motivated dedup was health and connection chatter, not audit
events.

B. If dedup stays for everything, should the summary line carry the last
occurrence's time (`last message repeated 2 times, last at 20:05:12`)? That
keeps the ring bounded and answers the operator's question.

## Evidence

- `internal/logbuf/logbuf.go` lines 11 to 17 and 90 to 93 at ee7666d.
- Live ring on mokuzai, 2026-10-01 18:48Z to 21:12Z: two fold markers (after 19:26:10 on seat A, 2 repeats; after 20:40:23 on seat B, 1 repeat)
  among reviewer-seat injects.
- The one misread, 2026-10-01: resolved by `marvel capture` showing the
  seat had received the text. My handoff log does not record it; this
  finding is the record.
