# probe: how a fleet behaves when the host carrying bd and the global hub leaves for under an hour

**Probe id:** `probe-temporary-host-disconnect`
**Ticket:** none of its own. Related: bd `aae-orc-5aum0` (director bus:
leaf drop and reconnect timing, and fail-fast back-pressure on the live
two-cluster deployment), marvel#434, FR-2026-10-04e.
**Date opened:** 2026-10-04
**Question:** when the host that carries the bd server and the global NATS
hub leaves the network for under an hour, what does each seat notice and
when, what fails and how loudly, what reconnects on its own, what work is
parked, lost or duplicated, and what it takes to come back.
**Status:** OPEN. The cut came between 19:59:15Z and 20:04:57Z on
2026-10-04 (about 20:02Z, C3); 19:57:32Z is only the first GitHub failure
seen from the host that left (C6). The return is marked two ways: the
mokuzai leaf's first clean global poll at 22:19:08Z (on mokuzai), and the
first message from mokuzai received on the host that came back at
22:19:29Z, 21 seconds later. Seat logs are collected; the finding follows
once director rules whether the window evidence is enough.

**Pre-cut record.** The questions and hypotheses were written before the
cut, but the file stayed uncommitted until 22:25Z and no hash was taken
then. The only record from before the cut is this seat's bus message to its
supervisor at 19:58:10Z (message 01M447XKF4WJV9A1TPH5MHYP8R), which says
the brief was written with "six questions, four pre-registered
hypotheses". Read the hypotheses as pre-registered on that message's
word, not on a commit.

## Why

The operator, verbatim: "we'll do a kos probe to see what we can learn while
doing this disconnect ... it will not be the last time we do this kind of
thing". Taking a host off the network for maintenance is routine, and the
fleet has never been watched through one. Each such window costs seat time
and risks parked or duplicated work. Measuring one planned cut, with notice,
sets the baseline the next one is judged against.

## Overlap check

`aae-orc-5aum0` asks for leaf drop and reconnect timing as a deliberate test
on the bus. This probe is the uncontrolled version: a real cut, with every
seat working, the bd server gone too, and the measure taken from what seats
did, not from a test harness. It feeds 5aum0's timing question and does not
replace it.

## The event

On 2026-10-04 the operator announced that the host carrying the bd server,
the local bus (port 4222) and the global hub (port 4242) would leave the
network "temporarily in a few minutes", returning "in less than an hour
probably". Director relayed the notice to every seat (STANSFIELD) at
19:42Z, and the probe ask at 19:56Z. marvel, tmux and local git stay up on
each host; bd, the bus, the hub, and network access from that host do not.

## Questions

1. **Notice.** Which seats read the notice before the cut, and when? A seat
   that did not read it is a seat that met the cut cold.
2. **Detection.** What does each seat notice first, and when? Which
   command, and what error text?
3. **Loudness.** Which operations fail, and how: an error at once, a hang
   until a timeout, or silent success that did not happen (a bus send
   reported "accepted" that is never delivered)?
4. **Recovery.** Do leaf links and bd clients reconnect on their own when the
   host returns? How long after?
5. **Work.** What was parked, lost, duplicated or blocked?
6. **By hand.** What had to be done by a person or a seat to recover?

## Hypotheses (pre-registered)

- **H1.** A bd command fails loudly (a connection error) rather than
  hanging, on every host.
- **H2.** A bus send from a leaf during the cut returns "accepted for
  delivery" and is not delivered; nothing on the sending side says so.
  (Recorded memory: accepted is not delivered.)
- **H3.** Leaf links to the hub reconnect on their own when the host
  returns, within a few minutes and without a seat acting.
- **H4.** Seats that read the notice parked cleanly; any loss or
  duplication comes from a seat that did not, or from a send that read as
  accepted.

A hypothesis the logs cannot settle is reported as unsettled, not as
confirmed.

## Method

Observational only. Nothing is changed to provoke a failure.

- **Seat logs.** Every seat keeps a short local log while the host is out,
  in its own scratch space or handoff, never in a repo or `.beads`, in UTC:
  whether and when it read the notice; its last successful bus send or
  read and its last bd command; the first failure, with its command and
  error text byte for byte; what it did (parked, retried and how often,
  switched work, waited); work parked, lost, duplicated or blocked; and
  when things came back, on their own or after what step by hand.
  Supervisors roll up their team's logs or name the seats that kept their
  own. Logs reach director after the return.
- **Bus and leaf timestamps.** The leaf's own connect and disconnect
  records, and stream sequence numbers either side of the cut, read after
  the return.
- **Clock.** Every time is read from `date -u` when written. A time that
  was estimated says so.

## Success signal

The probe succeeds when the finding can answer each of the six questions
with a timestamp or an error string from at least one log, and names which
of H1 to H4 held, failed, or stayed unsettled. A question no log answers is
reported as a gap, not filled in.

## Candidate findings (to test against the logs)

Recorded during recovery on 2026-10-04, before the logs are read. Each is a
candidate until the seat logs and leaf timestamps back it.

- **C1. One hub URL means an address change needs a config edit, unless
  the name still resolves.** A cluster's config carries the hub as one
  string (`hub.url`, `internal/config/config.go:473-474` at main b355df7),
  so a leaf has no second address to try. This is the structural point; how
  each cluster actually fared differed:
  - mokuzai's leaf recovered with no manual step: its first clean global
    poll was at 22:19:08Z, with no re-push or restart on its side (mokuzai
    reviewer's probe log);
  - on the other remote cluster, director re-trusted host keys and
    re-seeded its side by hand.

  A list of hub addresses is the near-term fix (marvel#575, bd
  aae-orc-9lapl); a directory is the longer one ([[service-directory]]).
- **C2. Discovery and trust may have been bound to addresses** (unverified).
  The verified part: the hub host came back on a different address. The
  rest is a lead to test against the logs:
  - name resolution: a remote leaf dialed the hub host by a `.local` name.
    Whether that stopped resolving is not established. mDNS answers per
    link, not per IPv4 subnet, and the remote leaf returned at 22:19:29Z
    over an IPv6 link-local address (22:19:29Z is the first message received
    on the returning host; mokuzai's own first clean poll was 22:19:08Z),
    which may contradict the lead;
  - trust: host keys pinned to addresses refused a moved cluster in
    marvel#434's earlier event. Nothing yet records that it recurred in
    this window.

  The operator expects the address to move between subnets again and wants
  every node to tolerate it.

- **C3. Seats on the host that left cannot see the cut on the bus.** The
  local bus and the hub run on that host, so for its own seats the bus
  stayed up. The cut showed only as GitHub failures and as R-92 refusals
  for sends to remote clusters, from about 20:02Z (bounded 19:59:15Z to
  20:04:57Z) until the first message from mokuzai arrived at 22:19:29Z
  (supervisor roll-up).
- **C4. On the host that left, a wait for messages reports silence, not
  failure.** For an idle seat on that host, `wait_for_message` on its
  local tier returned "no message within the window" through the outage,
  because the local bus never went down there; it could not tell an empty
  inbox from a cut-off fleet (the director#66 class; inferred, not
  measured). This does not hold on the far side: on mokuzai the
  reviewer-supervisor's global drain failed loudly at 20:04:57Z ("no
  responders"), and so did its send at 20:05:09Z.
- **C5. Coverage depends on who was polling.** Idle seats, prompt-driven
  seats and seats on a two-hour cadence saw nothing. A 90-minute watch that
  started early ended before the cut. Polling once per turn cost 11 turns
  and observed nothing (supervisor roll-up). This seat's own log has the
  same gap: it never saw the return.
- **C6. GitHub activity times do not mark the cut.** GitHub failed from
  the host that left (19:57:32Z here) while bd and the bus still answered,
  so the first GitHub error and the network cut are different events.
- **C7. Long waits overran their own deadline.** On three seats a
  120-second wait ran one to two minutes past its window; on one of them
  the harness moved the call to the background, and it later returned
  silence. The roll-up reads this as a harness edge, not the bus
  (supervisor roll-up).
- **C8. Capture the whole error before trimming it.** One seat's push
  failed at 19:58:22Z and its retry succeeded three seconds later, but the
  error was cut to its last line by `tail -1`, so the cause of the failure
  is not recorded (supervisor roll-up). The byte-for-byte rule in the
  method above exists for this.

Sources for C3 to C8: the arcaven supervisor's team roll-up of seat logs,
sent to director at 22:24Z and kept outside any repo; seats are cited by
role there.

## Recorded context: a cluster-local fallback, not a marvel feature

Operator ruling, 2026-10-04, verbatim: "mokuzai has a local self-recovery
method for the credentials, outside of marvel, just in case. Corporate does
not." That method explains the bus and leaf credential re-put seen on
mokuzai at 22:12:58Z. It is local to that cluster and outside marvel.
Nothing is to be built in marvel for it, and the finding should not count
it as marvel recovering on its own.

## Not in this probe

Designing a fix (fail-fast back-pressure, a sender-side "undelivered"
signal, a second bd server). Those are design work for after the finding.
No substitute relay or server is stood up during the cut.
