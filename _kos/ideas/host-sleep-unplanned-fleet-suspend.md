# Host sleep: an unplanned fleet suspend that nobody records

- **Status:** idea (pre-hypothesis, no commitment). An open topic for
  observations from both ends of a hub-and-spoke cluster.
- **Date:** 2026-10-11
- **Origin:** the operator, after a laptop that hosts a spoke cluster slept
  for most of a day while its seats read healthy. Observed from the spoke by
  its management seat; the hub's observations are invited below.
- **Subject:** marvel (operating a cluster whose host sleeps: health, the bus
  link, seat timeouts). Filed in the marvel graph per the subject test.
- **Related:** [[fleet-pause-resume]] and `docs/design/fleet-suspend-resume.md`
  (the deliberate suspend; host sleep is the same freeze with no record, no
  stop order and no resume step), [[tmux-harness-state-watchdog]] (a frozen
  seat is a state the watchdog cannot see from inside the freeze),
  [[health-signal-taxonomy]], and `docs/admin-guide.md` (the operator rule
  that a cluster host must not idle-sleep; this idea is about what happens
  when it does anyway).

## The problem, in one sentence

When the host sleeps, every seat, the daemon and the leaf link to the hub
freeze together, and on each brief wake everything reports healthy, so the
only party that can see the outage is the hub, and today it does not say.

## What was observed (spoke side, macOS on AC power)

- The host idle-slept on AC with the AC `sleep` setting at 0. Why is not
  known; a managed profile is one candidate, unverified.
- While asleep, macOS woke the host for 45 to 60 s about every 15 to 17
  minutes (DarkWake, then Maintenance Sleep). Over roughly 22 hours the
  cluster was awake perhaps 5% of the time.
- Each DarkWake matched a bus leaf close to within about a second ("Read
  Error - Remote: global-hub"), then a reconnect in 1 to 3 s. Read from the
  spoke's broker log, this looked like a flapping hub on a 16-minute cycle,
  and was first misattributed to the hub.
- Health checks run during a wake, so every one passed. A check that started
  one second before sleep finished 17 minutes later, at the next wake.
- Seats were frozen between wakes: a message or ring waited up to about 16
  minutes, and a seat then had under a minute of wall-clock time. This may
  explain or contribute to some earlier missed-wake reports.
- A user-run `caffeinate -dimsu` ended it.

## Questions this raises for marvel

1. **Who can see it?** The spoke cannot observe its own sleep while asleep.
   The hub sees a leaf that is absent most of the time and present briefly.
   Should the hub (or director's roster) mark a spoke as *asleep* or
   *intermittent* rather than present?
2. **Should the daemon read the host's power state?** On macOS the power log
   and assertions are readable without root. A daemon that notes "the host
   slept from T1 to T2" on wake could annotate health, the event ring and the
   bus, so a reader sees sleep, not a network fault.
3. **Should the daemon hold the host awake?** A power assertion while seats
   run (and on AC only, perhaps) is one line on macOS. It is also a policy
   choice the operator owns: battery, heat, a laptop in a bag. An operator
   setting, not a default, until ruled.
4. **Timeouts and stalls across a sleep.** Wall-clock timers (stall
   advisories, heartbeats, shift max-age, request timeouts) all jump on wake.
   Which should count wall time and which should count awake time?
5. **The relation to deliberate suspend.** The suspend design freezes seats
   on purpose and keeps the daemon answering. Host sleep freezes the daemon
   too. Is there anything the suspend design's record (pids, start times)
   should also capture for an unplanned freeze, or is detection on wake
   enough?

## Observations invited

- **Hub side:** what the hub's broker and roster showed for this spoke over
  the same window (leaf connects and disconnects, presence rows, any wake
  that a seat asked for and that never arrived).
- **Other spokes:** does any other cluster host sleep, and on what cycle?
- **Linux:** suspend behavior differs (no DarkWake by default); untested.

Each observation should carry its time window and the command that produced
it, so the two ends can be lined up.
