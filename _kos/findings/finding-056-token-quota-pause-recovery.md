# finding-056: the fleet survives a quota pause, but nothing in marvel sees it, so the pause ends only when a human says so

Probe: `probe-token-quota-pause-recovery.md` (this PR). Specimen: kinu,
2026-09-28 23:58Z to 2026-09-30 03:48Z. Evidence gathered by
arcaven-research-supervisor-g5-0 (bus, hub monitor, daemon log, marvel read
verbs, handoffs, transcripts; working notes in the orc's
`.session/research/2026-09-30-token-pause-recovery/`, not committed) and by
arcaven-supervisor-g5-0 (the E8 restarts, 2026-09-30 04:00 to 04:07Z). Code
claims verified by this seat against marvel origin/main b1f4953.

**Placement.** marvel is the subject: the question is what the control plane
can see and do about a backend limit. Director's own relay gap is recorded
here only as an impact; its remedy belongs in director's graph.

## Two pauses, not one

The main result corrects the probe's premise.

| pause | from | to | length |
|---|---|---|---|
| operator order (human inject) | 09-28 23:58:41Z | 09-30 03:48:28Z | 27h50m |
| account limit (harness refusals) | 09-29 15:32:10Z | about 09-30 02:00Z | 10h28m |

Only four sessions reached the limit, the ones with scheduled tasks
(director, arcaven-envoy, product-supervisor, arcaven-supervisor). The rest
idled on the order and made no calls. The harness did detect the reset: it
printed "continuing automatically at 9pm", and the limited seats resumed at
02:01Z. The order then held them, because it had no end a machine could read.
The envoy declined work because "your directive to start nothing new was
never lifted on the bus".

The codex reviewer seats hit no limit and resumed 13 to 19 s after the lift,
which fits a separate account (seat mapping inferred).

## Hypotheses

- **H1, recovery is human-driven: SUPPORTED, with a correction.** No marvel
  component recorded the pause or the resume. The harness did detect the
  reset on its own; what marvel lacks is the reading, not the harness.
- **H2, losses are time and messages, not sessions: SUPPORTED.** No seat
  died, restarted or crash-looped (marvel events and daemon.log, the whole
  window). The losses were wall-clock time, beats, sweeps, director's
  forwarding, and handoff accuracy (below).
- **H3, protect orders collide with pre-push test hooks: ONE INSTANCE,
  not tested fleet-wide.** This seat's #335 commit, a deliberately failing
  test, was refused by lefthook `test-race` and stayed local. No other seat's
  push was checked.
- **H4, the inbox is the restart bottleneck: SUPPORTED.** On resume this
  seat drained 37 messages, the oldest from 09-27, many already handled.
  After the E8 shim restarts every successor got a fresh durable and
  replayed its whole inbox history: maintainer 15, envoy 20,
  sideshow-builder 30, marvel-builder 37, author 53.

## 1. How well do we recover today: C overall

Graded per phase. MEASURED unless marked.

- **Survival: A.** Sessions and their conversation context survived because
  the harness waited at its prompt. 25 of 31 seats handed off within about
  90 s of the order. The bus kept every message. Branch state was unchanged.
- **Detection: F.** Nothing noticed the stall while it happened. Bus seqs
  4073 and 4074 are consecutive across 10h39m. Health is process-alive, so a
  seat whose every turn is refused reads healthy by design. `SessionState`
  has no paused or limited value (`internal/api/types.go`). The one limit
  reader, `marvel ctx-forward`, parses `rate_limits` but deliberately stops
  at the pane (`cmd/marvel/ctxforward.go`, the comment above the heartbeat
  send): account headroom is not a property of any session, and the daemon
  has no account-scoped place to receive it.
- **Resume: D.** Resume took a second human inject, 1h47m after the reset.
  The E8 shim restarts stalled at 3 of 10, and director's forwarding did not
  restart on its own.

Grades differ by seat class. Supervisors and the envoy (scheduled work)
met the limit and lost beats and sweeps. Builders and reviewers only idled.
Codex seats, on another account, lost only the order's time.

## 2. Impacts

- **Time.** About 27h50m of fleet time against 10h28m of real limit:
  15h33m idle before the limit with budget left, 1h47m after the reset.
- **Director's relay.** Director stopped reading at the order, not at the
  limit (bus silence 3995 to 4096, 27h54m). Five GATEs waited 27h28m to
  28h04m; director#160 was not forwarded until re-asked.
- **Beats and sweeps.** Missed beats were reported only afterwards, by the
  seats themselves. About 27 hourly e98-envoy sweeps and several arcaven
  sweeps were skipped with no catch-up.
- **Handoffs.**
  - Keyed to names, not slots. builder-g5-0 wrote its handoff at 23:55:35Z;
    its replacement registered as builder-g5-2 at 23:55:57Z. A kill on
    09-30 did the same again (g5-0 respawned as g5-2), orphaning a
    name-keyed handoff.
  - Inaccurate. This seat's own stop note (86ffd66) said the sibling
    branch had "no remote branch" when `origin/fix/313` existed. I had
    checked for unpushed commits, not for the remote branch.
  - Missing or overwritten. Five seats wrote no handoff for the order;
    three were overwritten after the lift. Timestamps written inside
    handoffs disagree with file mtimes; mtimes are the truth.
- **Respawns.** Respawned seats idle at the start screen until nudged
  (finding-055, #391). Each killed predecessor leaves a `marvel orphans` row
  with one `heartbeat.refused` (token does not match the session claimed).
- **Codex limit record at risk (INFERRED).** The only on-disk limit record
  for codex seats is their rollout under a `/private/tmp/marvel-h-*`
  directory, exposed to the temp reaping that already emptied marvel temp
  dirs once (#314).

## 3. The restart procedure, and what marvel should do

**As practiced:**

1. The operator watches usage by hand.
2. A human inject stops the fleet.
3. The harness waits at its limit and resumes at the reset.
4. Seats stay held until a second human inject.
5. Each seat rereads its handoff and inbox, and supervisors re-chase what
   stalled.

**What marvel should do, cheapest first (JUDGMENT; none built):**

1. **Read the limit.** Give the daemon an account-scoped home for
   the reading `ctx-forward` already parses, and forward it as a `Dimension`
   reading with provenance (finding-050). Codex writes the same data to disk
   as structured fields (`rate_limit_reached_type`, windows with
   `resets_at`), so no refusal text needs parsing.
2. **A `limited` seat condition.** Set it from the reading or from the
   harness refusal, beside `running`, so a seat shows "limited until
   <resets_at>" while process-alive stays true. Show it in `get sessions`
   and on the event ring.
3. **A declared pause with an end.** A fleet or team pause that marvel
   holds as a record (who, why, until `resets_at` or until lifted) and
   projects to seats, auto-lifted at the reset unless the operator marked
   it hold-until-lifted. marvel proposes the lift and a hold overrides it
   (ADR-007). This removes the 1h47m tail and the "is it lifted?" asks.
4. **Shed, do not stop.** A virtual budget could drop low-priority
   scheduled work (sweeps, beat rate) and keep reviews and GATE traffic,
   in place of idling 15h33m with budget left.
5. **Detect the stall.** Treat missed beats as a dead-man signal, and add a
   GATE-age watcher that does not depend on the one interactive director
   session.
6. **Key handoffs to the role slot**, and write restart plans (E8-style) as
   resumable records rather than prose.

Out of scope: whether to pause at 99% is the operator's call. This finding
only prices it.

## Filed

Nothing filed from this finding yet. Items 1 to 3 are one design question
(an account-scoped reading, a seat condition, a pause record) and are
proposed to the supervisor for a ticket decision.
