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

## Addendum (2026-10-04): a majordomo to moderate it

The operator, in a second message:

> "this might be a key advantage with marvel, as a service, generally, and
> something that our future majordomo might moderate - look up the vision
> for the majordomo or perhaps he has a seargent at arms for stuff like
> this"

Two framings follow from it, both recorded and neither settled.

- **A general advantage of marvel as a service.** If marvel can carry an
  operator's authority to a seat safely, that is a capability of marvel as a
  service provider, not only a fix for Item 1. See
  question-marvel-service-provider-shape (in both the aae-orc and marvel
  graphs).
- **The majordomo as moderator.** Prior art to read, not restated here:
  - aae-orc `question-akey-majordomo-local-llm-authorization`, "Reading B"
    (2026-07-05): the majordomo as a delegated signer inside a scope the
    operator ratifies. The same shape applied to seat approvals instead of
    akey signatures is the closest match to this idea.
  - aae-orc `question-marvel-service-provider-shape` and
    `ideas/marvel-service-provider-architecture.md`: the caretaker
    (majordomo) behind the wall, where it proposes and a deterministic layer
    enforces.
  - marvel `question-convergence-posture` and finding-034: the majordomo as
    a marvel-native agent whose levers are the same RPC verbs the CLI uses.
    `marvel inject` as the deterministic delivery lever fits that pattern.
  - director `ideas/cluster-majordomo-role.md`: a cluster majordomo, one per
    cluster, a supervisor of supervisors concerned with the cluster rather
    than the work.

**A second open tension.** The akey reading lets the majordomo act inside
its ratified scope. The service-provider reading lets it only propose, and
it ruled against pressure for it to act. An approval-by-keyboard majordomo
would need one of the two.

**Sergeant at arms.** The operator's proposal, and an open question: a
possible majordomo sub-role for enforcement and order, as distinct from
stewardship. No graph mentions it yet, and it is not defined here.

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
  came from director.** It is a requirement, not yet a probe: director's
  `sim/requirements.md` R-05, "Addressed sessions must be able to validate
  that a message is in fact from director, with nonrepudiation", the
  identity plane. R-53 ties it to the seat's nonrepudiation gap ("R-55
  proves recency, not identity"), and director's skill calls it research
  ("That is research, not something to solve in prose here"). An
  operator-originated directive would need the same property, extended from
  director to the operator.
