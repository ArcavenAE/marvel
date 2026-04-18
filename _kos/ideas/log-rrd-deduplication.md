# idea: RRD-style log deduplication in marvel's log ring

Captured session-026. Not yet a frontier question — brainstorming.

## The Cisco IOS idea

Cisco's syslog suppresses repeated identical messages and emits a
periodic summary line:

```
Apr 17 14:00:02: %LINK-3-UPDOWN: Interface GigabitEthernet0/1 changed state to down
Apr 17 14:00:05: last message repeated 385 times
```

That single line replaces 385 rows. The reader learns two things the
raw stream doesn't convey: the event is *still happening* and the
rate at which it's happening.

## Where it would help marvel

The daemon's in-memory log ring (`internal/logbuf`, default 10k
lines) is vulnerable to noisy repetition in at least three observed
cases:

1. **SSH connect polls** — Skippy's monitor loop hits the daemon at
   0.5 Hz. `ssh: client connected: michael.pursifull (SHA256:…)` dominates
   the ring. 10k lines cover ~5.5 hours before any interesting event
   scrolls off. Filed as `aae-orc-1d2`.
2. **CrashLoopBackOff chatter** — before this PR (#21), a failing
   agent could spam `health: restarting session X (failures=N, restarts=M)`
   every tick. With the fix, the rate is bounded by exponential backoff —
   but the problem class remains for any repeating handler log line.
3. **Reconciler "no change" tracing** — if a debug-level log is ever
   added for the common "actual == desired, no-op" case, the ring
   fills instantly.

Same ring, same problem.

## What RRD-style dedup looks like here

Logical shape per-emit:

- Compute a dedup key from the log line — either a content hash with
  volatile fields blanked (timestamps, session IDs, pane numbers) or
  an explicit `log.With(dedupKey=…)`.
- Track per-key: `{lastSeen, count, firstSeen, template}`.
- On emit of a key that matches the *previous* line: increment count,
  do **not** write to the ring.
- On emit of a different key: if the previous run had count > 1,
  emit a summary line (`… repeated N times over Δ`), then emit the new
  line.
- On a timer (e.g. every 60s), flush any open run so long-running
  repetition still surfaces.

Cisco's original was "previous line only." Modern variants (Docker,
systemd-journald via `Storage=persistent` + `--follow`, Loki's LogQL
unwrap) track *multiple* concurrent keys so interleaved repetition
still compresses. Pick one — "previous line only" is the lightest to
ship and catches the three cases above, which are all monotonic.

## Why this is interesting specifically for agent systems

A couple of structural reasons AI-agent control planes amplify the
win over plain server logging:

1. **Agents can fail identically forever.** A shell with a typo in
   `~/.zshrc` crashes the same way, `N` restarts in a row. A claude
   subprocess with a broken permission-mode config fails the same
   way. A curtain sandbox that denies an access a tool needs fails
   the same way. Dedup captures "305 identical failures in 8 minutes"
   as one informative line and preserves the original error verbatim
   without repeating it.
2. **Reading logs is itself an agent task.** A supervisor agent
   tailing the log ring is a reasonable pattern. If it's digesting
   the same line 1,000 times, it's paying 1,000 tokens and getting
   one bit of information. Dedup lowers the cost of log-consumption
   prompts and keeps context windows clear.
3. **Ring buffers are small.** `DefaultLogBufferLines = 10000`. In
   a multi-agent fleet with a chatty health check, one bad pod's
   restart storm can push every startup line and cross-agent event
   out of the window inside minutes — blinding the next operator
   who runs `marvel daemon logs`.
4. **Metric emission is latent in the log.** "Repeated 385 times
   over 8m30s" *is* a rate metric — just not in the metrics pipeline.
   A dedup layer that also exposes `{dedupKey, count, windowStart,
   windowEnd}` as a structured event is a natural on-ramp to the
   OTEL metric story without requiring all call sites to emit a
   counter manually.

## Things to probe

- **Key extraction.** "Same line except for a pane ID" — regex to
  normalize, or require call sites to opt in with an explicit key?
  Auto-normalization is more useful but more dangerous (false dedup
  collapses real distinct events).
- **Summary cadence.** Emit on key-change only, or also periodically
  while a run continues? Periodic is more informative but noisier.
- **Tail behavior for `marvel daemon logs -f`.** Does `-f` see dedup
  summaries or the raw stream? Probably dedup — the whole point is
  the tail stays legible.
- **Structured form.** Do we expose `repeat_count` as a field in the
  logs RPC result for programmatic tailing, so supervisor agents and
  dashboards can react to rate instead of line count?
- **Interaction with existing work.**
    - `aae-orc-1d2` (ssh poll noise) is the immediate motivator; dedup
      makes that filed-but-undressed concern go away without hiding
      the signal.
    - `aae-orc-k0t` (marvel events — structured state-transition log)
      is the structured-event cousin of this idea. If events exist,
      dedup might live at the *event* layer too.
    - Metrics (`internal/otel`) overlaps: dedup produces rate data
      that could feed a `marvel_log_repeats_total{key=…}` counter.

## Crystallization candidate

If this idea gets picked up, the frontier question would be:

> *What is the right boundary between structured events, OTEL metrics,
> and a dedup-summarizing log ring in marvel's observability story?*

Probe shape: implement a minimal "previous-line dedup" with a
content-hash key strategy in `internal/logbuf`. Wire it into the
daemon. Measure ring utilization over a 1-hour run on desk against
the status quo. Decide whether to promote or graveyard.

See also: `aae-orc-1d2`, `aae-orc-k0t`, Skippy's session-026 remote
log testing.
