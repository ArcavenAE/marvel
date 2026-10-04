# finding-067: max age can only end in a shift for a seat that can write a file, so a seat with no write surface escalates for as long as it runs

Source: the ops team's report for the 2026-10-04 harvest, item 3, and the
operator's ruling in that report excluding such seats. I verified the
mechanism against marvel origin/main b003cd3.

**Placement.** marvel is the subject: the max-age trigger's design. Which
wardrobe roles can write is context, not the subject.

## The mechanism

Max age never shifts a seat by itself (`internal/team/shift_handoff.go:19-25`,
design `docs/design/shift-trigger-list.md`, D5). On the threshold
marvel sends the seat a notice and records the request
(`requestHandoff`, `shift_handoff.go:80-126`). It shifts only when the role
declares a handoff file and that file's last line equals the role's
`handoff_marker` (`handoffComplete`, `:314`). With no marker by the end of
the window, or no declared file, it escalates `shift.handoff-missing` to the
team's supervisor, leaves the seat running, and repeats the escalation once
per handoff window while it stands (`:239-244`; marvel#468). The escalation
is sticky (marvel#453).

So the only path from "past max age" to a shift is the seat itself writing
a file. Neither the code nor the design names that as a precondition.

## Who cannot satisfy it

The ops team's report lists seats with no write surface: a codex seat run
read-only, crush, opencode with no prompt surface, and a supervisor under a
coordination contract that forbids it to write. For any of these, max age
on a role is an alarm that cannot be answered, and it repeats once per
window until a human shifts or stops the seat. The operator excluded such
seats from max age, which settles the deployment; the design gap stays.

The report said such seats "would escalate on every tick". At main the
repeat is once per handoff window (default 5m), not per tick.

## The design note

Max age assumes a writable seat. A role that enables it should be one whose
seat can write the declared handoff path, and the trigger has no way to know
that today. Two options I see, neither decided: validate at apply that a
role with max age declares a handoff path (it cannot check writability,
only declaration), or document the precondition beside `handoff` in the
manifest reference. Recorded as an open question.

Related, and not this finding: the three max-age gaps (marvel#451, #452,
#453), being placed as finding-065 in marvel#528, and the orc's open
question on the five forms of terminal marker seats write
(`question-handoff-terminal-marker`, in review).
