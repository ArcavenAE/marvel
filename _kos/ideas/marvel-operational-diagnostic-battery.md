# An operational diagnostic battery for marvel: intended, working, broken

- **Status:** idea (pre-hypothesis, no commitment). Operator ask, relayed via
  director to arcaven-supervisor, 2026-09-27.
- **Subject:** marvel (spawn, restart, shifts, inject, reexec and the managed
  bus's credentials are marvel's). director is an object here; its half of
  the battery is filed in its own graph.
- **Sibling:** `director::director-operational-diagnostic-battery` (the same
  battery for reach, addressing, presence, replay, grants and the launcher).
  One ask, two subjects, filed once in each graph per the subject test.
- **Related:** [[health-signal-taxonomy]] (what signals exist per harness;
  the battery asks whether designed behavior holds, which is a different
  question), [[tmux-harness-state-watchdog]] (detecting a stuck or
  logged-out harness from its pane).

## The operator's words

> "we should have operational tests, diagnostics, that verify designed,
> intended functionality is working, because the design is complex, and
> instances wont know what is intended vs what is working vs what is broken,
> and it's very challenging without a battery of positive and negative tests
> to know when something is broken or not working correctly"

## Operator ruling (RULED 2026-09-27)

Relayed by arcaven-supervisor, 2026-09-27T18:34:43Z:

> "the operator ruled on the diagnostics battery: YES to the shape, and YES
> to the default that a shim-only refusal is not enough and the broker must
> refuse before a third cluster joins."

- The shape above (named behavior with an anchor, a positive case, a
  negative case, and PASS, FAIL or NOT-APPLICABLE with evidence) is
  accepted.
- A negative case that only the shim refuses does not count as the control
  holding. The broker must refuse too, and that has to be in place before a
  third cluster joins.

## The observation

A runnable battery that any instance (a seat, a supervisor, director, the
operator) can call to learn three things: what is intended, what is working,
and what is broken. Each check:

- names the designed behavior it verifies, with a design-doc or finding
  anchor;
- has a positive case: the thing works;
- has a negative case: the control refuses what it should refuse;
- reports PASS, FAIL or NOT-APPLICABLE, with the evidence.

Marvel's areas, each with the anchor a check would cite:

- **Spawn environment construction:** a managed child gets its allowlist
  plus minted secrets and nothing inherited (question-permission-model;
  marvel#351). Negative: a canary set in the daemon's environment is absent
  in the child.
- **Restart:** restart policy and crash-loop backoff (question-healthchecks).
- **Shifts:** the shift state machine and succession (question-shifts,
  [[shift-change-succession-protocol]]).
- **Inject submit:** a long injected message submits rather than stages
  (finding-040 addendum; marvel#357). Negative: nothing the check injects
  concatenates onto text already in the composer.
- **Reexec:** the daemon adopts a new binary without losing running agents
  (elem-staged-activation-upgrades).
- **Bus auth regeneration:** a team password minted before a restart still
  authenticates after it (docs/design/services-list.md, plan row 3). Negative:
  a credential for another team is refused.

## Evidence that it is needed (2026-09-27)

- The peer-routing ruling went live, but no kinu supervisor held a global
  address, and finding that out took a manual survey.
- A supervisor sat on "Login expired" and swallowed six messages while every
  send returned accepted. marvel ran the pane the whole time and had no
  check that would have said the harness behind it was no longer working.

## Prior art to build on, not duplicate

- `marvel keys doctor`: audits and optionally fixes permissions under
  `~/.marvel`. One structural check; the battery would call it rather than
  re-implement it. (The installed binary predates the command; it is on
  main at `cmd/marvel/main.go`.)
- director's `sim/twin/verify-cast-launch.sh` (26 broker-free launcher
  checks), the director-mcp `--preflight`, and the hub acceptance steps in
  director's `sim/design/global-bus-tier.md`, on the director side.
- The orc's warn-only hygiene check for shared-checkout drift (aae-orc#376).

## Tensions

- **Diagnostic, not gate.** The battery informs; it does not gate CI
  (`.claude/rules/diagnostic-not-gate.md`, ADR-007). A structural check (an
  invalid services record, a key file with the wrong mode) may gate; a
  behavioral probe of a live cluster only reports.
- **Negative cases touch real controls.** A restart or reexec check against
  a live daemon disturbs running agents; those checks likely need a scratch
  daemon on its own socket (question-daemon-isolation-boundary).
- **NOT-APPLICABLE must be honest.** A cluster with no managed bus reports
  bus checks as not applicable, never as passed.
- **Who may call what.** A seat can read results; running a disruptive check
  is a different permission.
