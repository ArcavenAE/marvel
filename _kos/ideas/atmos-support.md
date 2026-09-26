# Atmos support for marvel

- **Status:** idea (pre-hypothesis, no commitment). Operator request relayed
  via director, 2026-09-26. Kept open on purpose: the seed names a direction,
  not a reading.
- **Seed (operator's words):** "Atmos support for marvel."
- **Subject:** marvel (what marvel would read, or how it would behave, when
  Atmos is present). Atmos is an OBJECT here. Filed in the marvel graph per
  the subject test.
- **Related:** [[sweetops-platform-team]] (a SweetOps platform team marvel can
  run), [[declared-cluster-config]] (the config surface Atmos would most
  likely manage).
- **Optional by construction.** marvel keeps working without Atmos, and
  nothing here may make Atmos a dependency (SOUL §2).

## Plausible readings, as questions

1. **Atmos manages marvel's configuration.** Could marvel manifests and
   cluster config be generated from Atmos stacks, so that inheritance,
   imports and per-environment overlays apply to teams and clusters the way
   they apply to infrastructure components? Would marvel then consume the
   rendered output only, with no Atmos code in marvel?
2. **marvel is Atmos-aware as a runtime for platform teams.** Could a team
   marvel runs (the SweetOps platform team is the first candidate) know the
   stack and component it works on, so that the stack is part of the team's
   context rather than something each seat discovers?
3. **Both.** Configuration from stacks, and teams that act on stacks. Does
   either reading require the other?

## Further questions for any reading

- Where does the boundary sit: a converter outside marvel, a marvel input
  format, or a documented convention with no code?
- What does an adopting org already have in its stacks that marvel should
  read instead of duplicating?
- How does this compare with other configuration layerings (plain overlays,
  other stack tools), so the seam stays generic rather than bound to one
  tool?
