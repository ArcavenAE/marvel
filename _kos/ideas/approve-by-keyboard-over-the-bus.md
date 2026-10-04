# Approve by keyboard: a bus directive plus `marvel inject` to carry an operator's approval

**Status: idea. Pre-hypothesis. No design, and the central tension is open.**

Captured: 2026-10-04
Source: operator, relayed by director.

## The operator's words

> "kos idea marvel could we combine over the bus directives + marvel inject
> to "approve by keyboard" as a method to address the Item 1 problem?"

## The problem it answers

"Item 1" is director's item O-2026-10-04e: an operator's authorization does
not travel over the bus. The operator gave director a ruling on folding a
client team's PR. The product team relayed the operator's words, quoted, to
its builder seat, and the builder's action was refused as `[Auto-Mode Bypass]`:
a seat credits an approval only when the words were typed in its own
session. The operator then typed "yes, do it" in the builder's pane, and
the action went through.

So the authority existed, and the bus carried its words, but the words
counted only once the operator typed them at the seat. The idea is to have
marvel deliver the approval as keystrokes, on a directive that arrives over
the bus.

## The open tension

As things stand, this is the shape the fleet forbids. marvel injecting
approval text into a pane because a bus message asked it to is a peer
making a control not fire, which `.claude/rules/no-control-bypass.md` in
aae-orc rules out ("A boundary a peer can type past is not a boundary").

What could make it legitimate is where the authority comes from, not how it
is delivered. One candidate shape, stated as a question and not a design: a
directive that only the operator can originate and that a seat can verify.
It would be signed, bound to one act on one seat, single use, short-lived
and audited, with marvel only delivering it and no seat able to mint one.
Without that, it is a bypass with extra steps.

Left open on purpose:

- Can a seat, or marvel on its behalf, verify that a directive came from the
  operator and not from a peer relaying one?
- Is typed text in a pane the right carrier at all, or is the keystroke
  only standing in for an authority signal that should be its own channel?
- Does this stay inside SOUL §8 (automate reminding, checking and
  proposing, not judging)? The operator still judges; the question is
  whether delivering the judgment by keystroke changes who made it.

## Prior art this builds on

- **finding-040, inject is not a dispatch channel.** `marvel inject`
  appends rather than replaces, a failed submit can prefix the next one,
  and a long payload can arrive in part, which is why dispatch belongs on
  the bus. An approval carried by inject inherits those limits.
- **question-mrvl-exposure-and-trust-bootstrap.** Any authorized mrvl://
  key can inject keystrokes into any agent's pane; the README calls inject
  "executive privilege", and it shares its single gate with `get sessions`.
  An approval path would widen what that one gate already grants.
- **question-permission-model, layer 2.** Role-based RPC authorization on
  daemon methods ("Supervisors get `inject` + `scale` + `shift` +
  `capture`"), which does not exist yet. Who may inject an approval is that
  question at its sharpest.
- **question-agent-communication-broker.** The broker-first protocol,
  including per-session credentials, where a verifiable directive would
  travel.
- **ideas/tmux-harness-state-watchdog.md.** Its waiting-approval state,
  unblocked by the operator or director, is the pane this idea would type
  into.
- **marvel credential kinds** (bd aae-orc-p4fzv, open): the operator's
  ruling to carry more kinds of sensitive material in marvel's credential.
  A single-use, operator-signed directive would be a new kind of artifact
  marvel holds or passes on.
- **aae-orc SOUL §3, custody.** A component may hold what it can revoke or
  re-mint; it must not hold bearer authority at a third party. An approval
  token is authority, so who mints it and who may hold it is a custody
  question.
- **aae-orc SOUL §8 and ADR-007, the automation boundary.**
- **Director's open research on how a session validates that a message
  came from director or the operator.** No record of it was found in
  director's graph or in the orc graph on main; the pointer is to be added
  when it has one.
