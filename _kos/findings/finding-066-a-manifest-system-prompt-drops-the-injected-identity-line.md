# finding-066: a claude role that supplies its own system prompt loses marvel's injected identity line, and nothing says so

Source: the ops team's report for the 2026-10-04 harvest, item 2, relayed
there from that team's architect seat. I verified the claim against marvel
origin/main b003cd3 before writing it, and re-checked the line cites
after merging main at 4219bca, and narrowed it to what the code
does.

**Placement.** marvel is the subject: what the claude adapter puts on a
seat's command line.

## The claim, as it holds at main

The claude adapter appends a one-line identity prompt, `You are <session>
(role: <role>, team: <team>, workspace: <workspace>).`, as
`--append-system-prompt`, but only when the role's args do not already carry
`--append-system-prompt` or `--append-system-prompt-file`, and only for the
bare harness, not a wrapper command (`internal/runtime/claude.go:138-142`).
Claude Code keeps only the last `--append-system-prompt` it is given, so the
code steps aside rather than have one prompt silently replace the other
(the comment at `:129-137`).

So a role that adds its own system prompt through `runtime.args` launches
with that prompt and without the identity line. The identity still reaches
the seat through the environment: `MARVEL_SESSION`, `MARVEL_ROLE`,
`MARVEL_TEAM` and `MARVEL_WORKSPACE` are set for every launch
(`internal/runtime/adapter.go:383-386`). What the seat loses is the
statement of who it is in its own prompt.

## Where the relayed claim was broader than the code

The report said "adding a manifest prompt" drops the identity line. The
manifest's `runtime.prompt` field is the headless request, passed as the
positional argument (`claude.go:161-164`), and it does not suppress the
identity line. Only a system prompt supplied as one of the two flags in
`runtime.args` does.

## What is new, and what was already written down

The precedence is already recorded: `elem-runtime-adapter-framework` says
the claude adapter adds the identity prompt "unless the manifest supplies
one or the command wraps the harness" (aae-orc-1vq6z). What that node does
not say is the consequence for an operator editing a manifest:

- adding a system prompt to a role is also a removal, of the identity line;
- nothing reports it. No event, log line or `describe` field marks that the
  identity line was skipped, so the change is visible only by reading the
  launch command.

A seat whose own prompt does not restate its role, team and workspace then
depends on reading its environment to know them.

## Open

Whether marvel should prepend the identity to a manifest-supplied prompt
(through a prompt file it writes), emit an event when it skips the line, or
leave this as documented precedence is a design call. I record it as an
open question and do not decide it.
