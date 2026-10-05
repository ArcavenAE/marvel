# finding-069: a planned host disconnect, read from the seats' logs

**Date:** 2026-10-05
**Probe:** `_kos/probes/probe-temporary-host-disconnect.md` (marvel#577)
**Placement:** marvel, where the probe was filed. Its subject is how a cluster
and its leaf behave when the hub host leaves; bd and director are objects.
**Number:** 069, the next free id after finding-068 on main and in open PRs.
**Sources:** none committed, and none in a repo. The first three are on kinu;
the last is on mokuzai:
- the arcaven supervisor's roll-up of 13 seat logs (sent to director 22:24Z);
- the separate logs of the envoy, maintainer and both architect seats;
- director's own log;
- mokuzai's daemon and nats logs, read on that host by the reviewer
  (review 5418470362 on this PR). Those times are relayed, not re-measured.

Seats are cited by role. Times are UTC on 2026-10-04. Where a time comes from
one seat's report and was not re-measured, it says so. Host addresses in
director's log are left out on purpose.

## What happened

The operator took kinu off the network for maintenance. kinu carries the bd
server, the local bus and the global hub. Director relayed the notice at
19:42Z and the probe ask at 19:56Z.

Two things happened, and the logs separate them:
- **About 20:00Z, kinu joined a different network.** kinu's own seats kept
  GitHub, the local bus and the hub. They lost mokuzai and corporate (no ping,
  marvel dial timeouts; director, about 20:01Z).
- **GitHub blipped from kinu as the network changed.** Each seat's estimate
  of the outage differs:

| Seat | First failure | Back by |
|---|---|---|
| this architect | 19:57:32Z | not observed |
| supervisor | 19:58:55Z | 20:01:53Z |
| builder | 19:58:22Z (push failed, retry ok 3s later) | 19:58:25Z |

The window closed at 22:21Z (director), about 2h18m after the cut. A second,
shorter event followed, from 23:43:56Z (mokuzai's first leaf drop) to about
00:03Z: global tier down from 23:46Z and GitHub intermittent from kinu
(supervisor roll-up). On mokuzai the leaf dropped twice in that span and
reconnected each time with no restart or put (mokuzai's logs). The mokuzai
reviewer's relay of a review it held through that span was sent at 00:05:21Z,
while GitHub showed the review at 23:43:20Z, so a relayed time can lag the
event it reports. Its cause is not established.

## The six questions

1. **Notice.** All 13 arcaven seats read it before the cut (supervisor
   roll-up, 13/13 logged). Corporate's two reviewer seats did not: they were
   idle, never prompted, and could not be doorbelled in time (director).
2. **Detection.**
   - On kinu, the first sign was a GitHub error, for example `Post
     "https://api.github.com/graphql": dial tcp <github-ip>:443: i/o timeout`.
   - The cross-host loss showed only as R-92 refusals. The first was at
     20:04:38Z: `nothing is registered under "presence.mokuzai." in
     GLOBAL_PRESENCE for global://mokuzai/supervisor ... (R-92 liveness)`.
3. **Loudness.**
   - On mokuzai, the reviewer-supervisor's global drain failed loudly at
     20:04:57Z ("no responders"), and so did its send at 20:05:09Z (probe
     brief, from that seat's log).
   - On kinu, nothing failed on the bus. `wait_for_message` returned "silence,
     not failure" throughout, because the bus never left (maintainer, filer).
4. **Recovery.**
   - mokuzai came back twice, each time after its local fallback ran a full
     cycle (mokuzai's logs):
     - 22:12:52Z daemon restart with the hub set to a literal address,
       22:12:58Z credential put, 22:13:03Z nats restart, and 22:13:03.6Z leaf
       up over IPv4;
     - 22:17:55Z a second daemon restart with the hub back to its name,
       22:18:01Z a second put, 22:18:06Z nats restart, and 22:18:09.9Z leaf up
       over IPv6.
     mokuzai's first clean global poll was at 22:19:08Z (probe brief, from the
     mokuzai reviewer's log). The first mokuzai message reached kinu at
     22:19:29Z.
   - corporate: director re-pushed the seed by hand. Its leaf came back over
     IPv6 after kinu changed networks again (director).
   - The return was not clean: kinu's address flipped again at 22:2xZ, and
     corporate's leaf dropped again at 00:01Z because `.local` did not cross
     subnets (director). mokuzai had no 00:01Z drop.
5. **Work.**
   - arcaven lost nothing and duplicated nothing. Every send to mokuzai was
     refused loudly and parked, not dropped.
   - Another team's asks to the mokuzai reviewer-supervisor were not
     delivered (that seat, 22:21:51Z). Whether they were accepted and dropped
     or refused is not recorded here.
   - One push failed and succeeded on retry, but its stderr was cut to one line
     by `tail -1`, so the cause is lost (builder).
6. **By hand.** Director re-pushed corporate's seed. On mokuzai, the operator's
   local fallback ran twice: 2 credential puts (22:12:58Z, 22:18:01Z), 2
   daemon restarts and 2 nats restarts. Under the
   operator's ruling, that fallback is outside marvel and is not counted as
   marvel recovering. No arcaven seat took a manual step.

## The hypotheses

| | Result | Why |
|---|---|---|
| H1, bd fails loudly on every host | **unsettled** | bd stayed up for kinu's seats (claude-reviewer correction: reachable throughout). No log records a bd call from mokuzai or corporate in the window |
| H2, a send is accepted and not delivered | **failed for arcaven, unsettled overall** | arcaven's sends were refused under R-92, not accepted. Another team's undelivered asks may be H2, unconfirmed |
| H3, leaves reconnect on their own within minutes | **failed for the cut, held for mokuzai's later blips** | the cut's return took about 2h18m and came only after kinu changed networks again. Corporate needed a seed re-push, and its leaf dropped again at 00:01Z. mokuzai's return followed two fallback cycles outside marvel. In the second event mokuzai's leaf did reconnect unaided: down 23:43:56Z, back 23:47:43Z, down 23:52:22Z, back 23:56:00Z, with no restart or put (mokuzai's logs). No log read here records corporate's leaf in that span |
| H4, seats that read the notice parked cleanly | **held** | 13/13 arcaven seats parked, nothing lost. The only unread notices were corporate's two idle seats, which had no work at risk on record |

## The candidate findings

- **C1, one hub URL: held, with one correction.** The probe says mokuzai
  recovered "with no re-push or restart on its side". That is wrong. The
  operator's mokuzai fallback ran its cycle twice: 2 credential puts, 2
  daemon restarts and 2 nats restarts. The first leaf came up over IPv4 at
  22:13:03.6Z with the hub set to a literal address; the second came up over
  IPv6 at 22:18:09.9Z with the hub back to its name. The
  structural point stands: a single `hub.url` gives a leaf no second address
  (marvel#575).
- **C2, addresses in discovery and trust: partly confirmed.** The `.local`
  name did not cross subnets at 00:01Z, dropping corporate's leaf (director).
  On host keys, mokuzai's logs show 5 ssh failures with "no common algorithm
  for host key" at 22:10:40Z to 22:10:42Z. That is a host-key negotiation
  failure in the window, though not the address-pinned refusal of marvel#434.
- **C3, kinu's seats cannot see the cut on the bus: confirmed.** The
  maintainer measured it after a GitHub failure at 21:30:38Z: `wait_for_message`
  and `set_presence` still answered at 21:31:15Z.
- **C4, silence, not failure, on the host that left: confirmed on kinu.** The
  maintainer and filer both read silence. The far side failed loudly, as
  above.
- **C5, coverage depends on who polled: confirmed.** None of these seats saw
  the return as it happened:
  - the maintainer's per-turn polls cost 11 turns and saw one 40-second
    GitHub blip;
  - the second architect's 20-second watch was capped at 90 minutes and ran
    out before the return;
  - the envoy runs on a 2-hour cadence;
  - this architect was idle.
- **C6, GitHub activity times do not mark the cut: confirmed.** The envoy
  first read the last kinu-side GitHub activity (20:23Z) as a bound on the
  cut, then withdrew it. The 20:16Z merges and the 20:23Z activity came after the
  network switch. GitHub kept carrying cross-host review traffic while the bus to
  mokuzai was down: a mokuzai verdict reached GitHub at 20:16Z.
- **C7, long waits overran their deadline: confirmed on three seats.**
  - filer: one 120s wait at about 19:57Z, backgrounded by the harness ("still
    running after 120s ... moved to the background"), later returned silence;
  - second architect: two, at 19:57Z and 20:03:51Z;
  - claude-reviewer: two, at about 21:18Z and 21:22Z.
  The 60s and 90s waits returned on time. This reads as a harness edge near
  120s, not the bus. The mechanism is not measured.
- **C8, capture the whole error: confirmed.** The builder's `tail -1` lost the
  push error.

## Lessons for the next planned cut

- **Mark the cut and the return from the host that stays put.** Seats on the
  host that leaves keep their bus. GitHub activity times are not a usable cut
  marker (envoy).
- **A watch has to outlast the window.** Start it at the cut, not at the
  notice, and keep it running until the return.
- **Doorbell idle seats before the cut.** Two corporate seats met it cold.
- **R-92 refusals did their job.** Nothing was silently dropped from arcaven.
  The refusal text blames the name ("the cluster name is likely wrong") when
  the cause is liveness (supervisor).
- **Count manual steps honestly.** mokuzai's return took two fallback cycles
  (two puts, two daemon and two nats restarts); corporate's took a seed
  re-push.
