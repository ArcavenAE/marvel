---
id: finding-006-three-act-demo-and-recovery-hardening
title: "Three-act demo build: recovery, observation, and control-plane beats made runnable, plus the gaps that surfaced"
question: question-permission-model
confidence: frontier
tags: [marvel, demo, recovery, observability, policy, harness]
bd: [aae-orc-k3a, aae-orc-96st, aae-orc-qkfl, aae-orc-69i2, aae-orc-22sz, aae-orc-6spa, aae-orc-vaja, aae-orc-yvja, aae-orc-sape, aae-orc-w5su]
addressed_to: aae-orc-vaja
provenance:
  created_by: agent
  session: marvel-demo-waves-2026-07-31
  created_at: "2026-08-01"
  host: kinu
---

# Three-act demo: what marvel can demonstrably do, and what it still cannot

Marvel accumulated a set of shipped capabilities across several waves this
session (recovery, per-session metrics, harness adapters, self-update, a
Policy resource) but had no single runnable sequence that proved the pieces
work together, and two of the oldest shipped example manifests did not run at
all. This finding records the demo that was built to close that gap, the fixes
the build forced, and the honest boundary of what has been verified.

## Load-bearing caveat: no human has run the demo yet

Every beat below was exercised by automated agent runs against a live marvel
daemon on kinu (the Opus lanes that authored and fixed each piece started a
daemon, ran the commands, read `marvel get sessions` and `marvel events`, and
tore the daemon down). That is stronger than manifest-parse-only or
code-reading, and it is not the same as an operator watching the demo. As of
this harvest, `docs/demo.md` has not been run top to bottom by a person. The
confidence tier here is frontier for that reason: the beats are agent-verified,
not operator-witnessed. First operator run is the promotion signal.

## The three acts, and how each maps to real commands

The demo lives in `docs/demo.md` with `just demo-act1`, `demo-act2`,
`demo-act3` recipes and Act-1 manifests under `examples/demo-act1-*`.

- **Act 1 (recover).** Kill a pane out of band and marvel emits
  `session.crashed` then re-creates the session after crash-loop backoff; an
  operator `marvel kill` emits `session.deleted` then `session.created` with no
  backoff; removing a role from the manifest and re-applying drains the orphans
  with `role.removed` + `session.deleted`; a health manifest with
  `failure_threshold = 1` shows `health.failed`, `session.restarted` (always
  policy), `session.failed` (never policy), and `role.saturated` +
  `session.failed` at `max_restarts = 1`.
- **Act 2 (observe).** The mixed `{claude, codex, opencode}` team shows CPU and
  RSS populating uniformly across all three harnesses, and agent-stream events
  (`agent.session.started`, `agent.turn.completed`, `agent.message.completed`,
  `agent.session.ended`) carrying tokens, cost, and duration, streamed from all
  three under live harness auth.
- **Act 3 (control plane).** `marvel work` on a Policy manifest fires
  `policy.projected` at spawn; editing the policy and re-applying fires a second
  `policy.projected` and rewrites the running session's projected settings file
  in place with no restart; `marvel get policies` shows the version flip.

## What the demo build forced into existence or repair

Shipped and merged this session (PR numbers are marvel):

- Recovery correctness (PR #83): `session.failed` now emitted on the
  restart-policy-never and max-restarts-saturation paths; shift timeout with
  rollback of the stuck generation; orphaned-role teardown. Closes the three
  recovery-correctness bugs (96st, qkfl, 69i2).
- Policy resource + live projection (PR #84): `ProjectionFor` is a method on the
  runtime Adapter interface; claude and forestage consume a Claude Code settings
  fragment via `--settings` and hot-reload it; codex, opencode, and generic log
  the policy as advisory rather than dropping it. This is layer 1 (environment
  construction) of `question-permission-model`. Closes k3a.
- tmux history-limit and manifest permission-mode validation (PR #85): sessions
  no longer inherit the 2000-line default that starved the capture-pane scrape
  (100000 now, session-scoped); permission modes are validated against a
  canonical allowlist at parse time. Closes 22sz, 6spa.
- Simulator adapter (PR #89): `image = "simulator"` now resolves to a dedicated
  adapter that injects the heartbeat flags, instead of falling through to the
  generic adapter which does not. The shipped `examples/demo.toml` went from
  four sessions in crash-loop to all healthy with context pressure populated.
  Closes yvja (GH ArcavenAE/marvel#87).
- Configurable shift timeout (PR #89): `--shift-timeout` flag and
  `MARVEL_SHIFT_TIMEOUT` env, defaulting to the prior 10 minutes. Demonstrated
  `team.shift-timed-out` live at a 15-second override. Closes sape
  (GH ArcavenAE/marvel#88).

## The gaps the verification surfaced

Six places where the code did not match a naive reading. Three were
behavior-naming clarifications recorded directly in `docs/demo.md` (kill
recovery is crashed-or-deleted then created, not `restarted`; `health.failed`
is suppressed when `failure_threshold >= 2` because the sub-threshold miss
consumes the transition; `health.crashloop-backoff` does not surface
interactively on the always-restart path because the session is deleted during
its backoff window). No defect there; the runbook now states the real behavior.

Three were real and are tracked:

- Simulator heartbeat dead under the generic adapter (fixed, PR #89, yvja / GH #87).
- Shift timeout not operable outside code (fixed, PR #89, sape / GH #88).
- Context pressure (CTX%) is not extracted from the claude, codex, or opencode
  adapters. The single producer is the heartbeat RPC, so CTX% renders absent for
  every real harness even though usage data is present in each harness's own
  stream. This is not a one-off bug; it is the Lens-1 harness-first requirement
  (extract context pressure and selected values from the harness process marvel
  manages). It is tracked as `aae-orc-w5su`, folded into the harness-first arc
  alongside `question-stream-attachment`, and left open. The simulator does
  populate CTX% because it is the one image that sends the heartbeat, which is
  precisely why the real-harness gap is visible: the plumbing works, the
  producers are missing.

## Bearing on the graph

- `question-permission-model`: layer 1 (environment construction via the
  projected settings file) is now implemented and demoable. Layer 2 (role-based
  RPC authorization) is still unbuilt (`aae-orc-bs3x`, `aae-orc-ukz`), so the
  node stays frontier.
- `question-stream-attachment`: the CTX% gap is a concrete instance of the
  per-harness binding still needing to produce context pressure from the parsed
  stream, not just carry bytes. The direction is settled; this is verification
  work plus a missing producer.
