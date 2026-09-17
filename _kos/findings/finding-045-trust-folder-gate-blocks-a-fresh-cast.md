# finding-045: a freshly cast agent stalls at claude's folder-trust gate, which marvel could pre-clear

Names the prevent-side complement to aae-orc-fxmi1 (the detect side). A first
cast into a new workspace directory can stall at claude's "do you trust the
files in this folder" gate, unattended, while `get sessions` reads green.
marvel could clear that gate at cast time.

## The gate

finding-030 §2d recorded this empirically: a new workspace directory hits
claude's folder-trust gate at first launch. Directory trust then persists in
`~/.claude.json`, so every later spawn in that directory (including
reconciler-driven replacements) comes up clean. So the gate bites once per
directory, at first launch, and the failure is worst where it is least
visible: the first launch of a new workspace, unattended, when a green
`get sessions` is most likely to be believed.

## permissions="auto" does not clear it

The permission mode marvel passes (a role's `permissions`, projected to
`--permission-mode`) governs claude's tool-permission prompts. It looks like a
different gate from folder-trust: setting the permission mode does not pre-clear
the folder-trust dialog, so a first cast into a fresh workspace directory can
still stall on trust even with `permissions = "auto"`.

## Detect versus prevent

aae-orc-fxmi1 is the detect side: classify and surface a stalled or blocked
session, advisory only, do not judge or destroy. It makes the stall visible; it
does not stop it happening.

The prevent side is a candidate feature: marvel writes the workspace directory
into the trusted set in `~/.claude.json` at cast time, before launching the
agent, so a first cast comes up working. It composes with fxmi1: pre-trust
removes the common first-launch case, and the watchdog catches whatever slips
through.

Bounds worth stating up front, since this touches the harness's own user-level
config: the write should be claude-adapter-specific, opt-in or clearly logged,
and scoped to the workspace marvel is about to run in, never a broadening of
trust beyond it. Filed as a candidate feature bead: aae-orc-w9s1c.

Related: finding-030 (the empirical first-launch hazard),
question-session-state-observation (a converged team is not a working team; the
same blind spot this gate lives in), aae-orc-fxmi1 (the detect side).
