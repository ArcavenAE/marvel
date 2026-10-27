# Operator steps as marvel runs: a one-off script on a full tty, followed by a supervisor

**Status: idea. Pre-hypothesis. No design, no ticket and no frontier node.**

Captured: 2026-10-27
Source: operator, relayed by director.

## The operator's words

> "kos idea marvel today we're running scripts by hand on different clusters, but marvel is capable of organizing and running one off scripts using ... itself as a job scheduler and they have a fully tty, and marvel capture/inject can be used to interactively answer inputs, we may need to analyze/ensure that we do not repeatly run the same script over and over but it's basically a single run tui harness shape with a supervisor using marvel capture/inject to follow the latest questions, look for errors, approve or reject the run, collect the logs/status. we should study what changes to the system are needed for this use of marvel as system automation/maintenance/steps, and what improvements to marvel are important to streamline this use case"

## The idea, in one sentence

Run a one-off maintenance script as a marvel session, so that a person no
longer carries it from cluster to cluster by hand, and let a supervisor
session follow it through `marvel capture` and `marvel inject`.

## The shape

- **A single-run role with a full tty.** The script runs in a pane as an
  interactive program, so it can prompt, wait for an answer and print as it
  goes. It is not a headless print-mode role. It starts once and ends.
- **A follower.** A supervisor session reads the pane with `capture`, finds
  the latest question, and answers it with `inject`. It watches the output
  for errors.
- **A verdict.** The supervisor approves or rejects the run, or hands the
  decision to a person. The run ends in one of those states, not in a restart.
- **A record.** The run's logs and its final status are collected and kept
  where a later reader can find them, whether or not the run succeeded.

The follower does not need to be an agent. Whatever follows must be able to
read a prompt and answer it, and to say whether the run went wrong.

## Run-once safety

The operator names the risk: the same script must not run again and again.
Three places this can happen, stated as questions:

- **A run that finishes and is refilled.** A role is a service, so a finished
  one is spawned again unless something says otherwise. ADR-010 solves this
  for a headless role by letting a finished run hold its replica slot. Does a
  tty role need the same completion rule, and is it the same rule?
- **A firing that fires twice.** The schedule design
  (`docs/design/scheduled-runs.md`) already refuses overlap and runs a missed
  firing at most once. That covers a clock. A one-off has no clock, so what
  names "this run" so that a second request for it can be recognized?
- **A run repeated by a person.** If two operators, or an operator and a
  supervisor, start the same script on the same cluster, what stops the
  second? The identity of a run might be the script's digest plus its target,
  but that is a guess, not a design.

## How it relates to what exists

- **Operator-steps scripts.** Today an operator step is a file under
  `tools/operator-steps/` in the orchestrator repository. A seat writes it and
  runs `--check`, which only reads. A person runs `--apply <sha256>`, which
  takes the digest that `--check` printed and refuses when the plan has
  changed since. The idea would move who runs the script, not what the script
  checks. The open tension is that those scripts say a seat never runs
  `--apply`. A supervisor that injects "yes" into an apply prompt is a seat
  approving the step, which the scripts and the no-control-bypass rule
  currently reserve for a person.
- **The schedule block (#650).** A schedule fires a headless role on a clock,
  once per firing, with a run history and a stale alarm. It supplies the
  organizing and the at-most-once idea this request leans on. It does not
  supply a tty, and it has no operator-started run with no clock.
- **Headless completion (ADR-010).** A finished headless run holds its slot
  and is not refilled. This is the completion rule a one-off run needs. The
  rule is written for print-mode runs, and whether it applies to a tty run is
  one of the things to study.
- **The retrospector's scheduled run.** The retrospector is moving to a
  headless scheduled run with a constrained write surface
  (`docs/design/codex-roles-reviewer-and-retrospector.md`, finding-048). It is
  the nearest existing case of a run that does bounded work and leaves
  records behind. It needs no one to answer a prompt, which is the part this
  idea adds.
- **Capture and inject.** `inject` appends to what is already in a pane and
  is not a dispatch channel (finding-040). Answering a prompt with it is a
  different use from dispatching work, and the same weaknesses apply: text
  that does not submit, and text that joins what was already typed.

## What would have to be studied

Not answered here. The operator asks for a study of what the system needs
and which improvements matter most.

- Whether a role can be started once, by hand or by a supervisor, with no
  clock and no restart.
- What completion, status and exit code a tty run reports, and where its
  output is kept after the pane is gone.
- Whether `capture` can reliably find the latest question in a script's
  output, and whether `inject` can reliably answer it.
- Whether the run's authority is fixed by its role, as a scheduled run's is,
  and how a supervisor's approval is recorded.
- Which cluster the run is on, and how one supervisor follows runs on several.

## Left open on purpose

- Is this a mode of an existing role type, or a new kind of run?
- Who may approve: a supervisor session, a person, or either according to the
  step?
- What is the smallest first case that would show whether the shape works?
