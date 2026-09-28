# Deaf-seat status: a watchdog state for a seat that is alive, working, and not hearing its bus

- **Status:** idea (pre-hypothesis, no commitment). Item 7 of the
  loss-reduction party (2026-09-27), tracked as an idea by operator ruling.
- **Date:** 2026-09-27
- **Author seat:** arcaven-architect-g5-0
- **Subject:** marvel (the harness-state watchdog). Filed in the marvel graph
  per the subject test; director is the object whose channel is observed.
- **Extends:** [[tmux-harness-state-watchdog]], whose state table has no row
  for this. **Related:** [[health-signal-taxonomy]];
  `question-session-state-observation`; director finding-007 (a deaf session
  reports silence as normal) and director#66; the loss-reduction build brief
  LR-3 (an unread-age reader from consumer ack floors).

## The gap, in one sentence

A seat whose bus consumer is gone keeps working, keeps advertising itself as
live, and truthfully reports "no message within the window", so every
surface marvel reads today calls it healthy while its inbox fills.

## Why marvel, and why from outside

Director finding-007 measured that this state is indistinguishable from
working **from inside the seat**: the seat has no observation that differs
from a quiet channel, and it can be wrong about which end of its own channel
is broken. So detection has to come from outside the seat, and marvel is
the component already outside every seat, reading its pane and its process.

## The proposed state

Add one row to the watchdog's table, earned by a distinct operator
response:

| State | Signal source | Operator response |
|---|---|---|
| **deaf** | broker: the seat's presence row is live while it has no inbox consumer, or its oldest unread message ages past a bound; pane: corroboration only | re-attach the channel (restart the shim or the seat), then re-send what it missed; never treat its "quiet" reports as evidence |

## Signal tiers

Pane text alone is a weak signal here, which is the main thing this idea
has to get right.

1. **Decisive, from the broker (cheap, no scraping).**
   - Presence live, and a tier's durable is missing. There are two tiers,
     and each is checked on its own:
     - local: no `mcp_<agent>_*` durable on `AGENT_INBOX` (the cluster
       broker);
     - global: no `mcp_global_<agent>_*` durable on `GLOBAL_TO_<cluster>`
       (the hub, for a seat that holds a global address).
     finding-007's measured specimens are the GLOBAL case: after the hub
     restart of 2026-09-25, no live supervisor on one cluster held a consumer
     on its `GLOBAL_TO_<cluster>` stream while the seats kept polling
     silence. A seat can be healthy on one tier and deaf on the other.
   - A durable exists on either tier, and its oldest unread message is older
     than a bound while the pane shows activity. LR-3's reader computes this from
     `AckFloor` and the stream; the watchdog consumes it, never recomputes
     it.
2. **Corroborating, from the pane (scraping; the watchdog's second tier).**
   - The harness's own MCP-server status indicator showing the director
     server failed or disconnected. The exact strings per harness are
     **unmeasured**; this idea does not guess them.
   - Repeated `no message within the window` results in the pane while
     tier 1 shows pending mail.

   Pane text never raises `deaf` on its own. It only explains a tier-1 hit
   ("the MCP server is down" versus "the consumer expired while the server
   ran").
3. **Not a signal:** the seat's own statement that nothing arrived. By
   finding-007, that is the symptom, not evidence.

## The probe that would make this a question

On a scratch daemon with a scratch broker and a scratch hub (never the live fleet):

- run one claude seat and one codex seat;
- delete the seat's global durable (`mcp_global_<agent>_*` on
  `GLOBAL_TO_<cluster>`), the case finding-007 measured, and record the result;
- separately, delete the seat's local durable (`mcp_<agent>_*` on
  `AGENT_INBOX`), and record that result on its own;
- separately, kill the seat's director-mcp process.

For each, record what the pane shows, byte for byte, and what the broker
shows. The result answers four things:
- whether tier 2 has any reliable string per harness;
- how long tier 1 takes to show pending age;
- whether the mechanisms are distinguishable at all;
- whether a seat deaf on one tier still looks healthy on the other.

Positive: a deaf seat is classified `deaf` within one watchdog interval
after its first unread message. Negative: a seat that is genuinely quiet
(no mail sent) is never classified `deaf`.

## Boundaries

- **Diagnostic, not a gate** (ADR-007). `deaf` informs the operator and the
  supervisor; nothing auto-restarts on it until a ruling says so.
- **Credentials.** Tier 1 needs a read of consumer info on the inbox stream.
  finding-007 section 5 notes a cluster credential cannot read it. Which
  credential marvel reads it with is part of the bus-as-service design, not
  settled here.
