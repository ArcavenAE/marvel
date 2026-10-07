# A first prompt for interactive seats

Status: design proposal, 2026-10-07, for #391. Proposal only; the rulings in
section 7 are the operator's. Code references are pinned to marvel `5a972a4`.

## 1. Why

An interactive seat marvel spawns starts at an empty prompt and waits for a
human. A successor from a shift or a rotation does the same: its predecessor's
handoff is written, `MARVEL_PREDECESSOR` names the predecessor, and nothing
tells the new seat to read either. On 2026-10-07 this idled every successor
on two hosts (6 of 6 on mokuzai for about 50 minutes, 7 of 7 on corporate),
as reported on #391. The handoffs read fine once a person typed into each
pane. The only missing piece is the trigger.

marvel already decides what a seat's first turn should be about; it just does
not say it. This design has marvel hand the harness that first turn at launch.

## 2. What is true today

- The manifest carries `runtime.prompt` for any mode (`internal/api/manifest.go`,
  `ManifestRuntime.Prompt`). Only headless launches read it:
  - claude appends it as the positional argument `if headless`
    (`internal/runtime/claude.go:162-164`);
  - codex's interactive branch returns before it reads the prompt
    (`internal/runtime/codex.go:114-120`);
  - opencode's interactive branch does the same (`internal/runtime/opencode.go`,
    `Prepare`).
  Nothing warns at apply.
- Every harness marvel drives takes a first message at an interactive launch,
  by its own help text on this host: `claude [options] [command] [prompt]`
  (2.1.292, "starts an interactive session by default"); `codex [OPTIONS]
  [PROMPT]` (codex-cli 0.160.1, options "forwarded to the interactive CLI");
  opencode's TUI takes `--prompt <string>` (1.18.15), and its positional is a
  project path, not a message. Whether each one submits that message, or only
  fills the input box, and whether it does so after a first-run or consent
  dialog, is not measured yet (P1 to P3).
- A successor already carries its lineage: `MARVEL_PREDECESSOR` (the
  predecessor's session key) and, when marvel asked for a handoff,
  `MARVEL_HANDOFF_REQUESTED_AT` (`internal/runtime/adapter.go`,
  `constructedEnv`; `internal/api/types.go:248-255`). A crash repair sets
  neither (#541).
- A role that declares a handoff file names it with a `{session}`
  placeholder (`shift.handoff`, with `handoff_marker`;
  `internal/api/manifest_shift.go:187-199`). Roles without a max-age arm
  usually declare none.
- marvel knows when a seat is on the bus: `constructedEnv` writes the
  director variables when `ctx.BusURL` is set.
- The launch argument is the safer channel than typing into the pane, because
  a single send-keys of a long task truncates at the head (#317).
- A first-run screen (workspace trust, onboarding) that nobody answers stops
  a seat before any prompt runs; preparing that state is #420's subject.

## 3. Design

- **D1. `runtime.prompt` reaches interactive seats.** For an interactive
  role, the adapter passes the role's declared `runtime.prompt` as the
  harness's first message: the trailing positional for claude and codex, and
  `--prompt` for opencode. Headless launches are unchanged.
- **D2. A successor gets a first prompt by default.** When marvel spawns a
  session with a predecessor, it composes a fixed first message, written only
  from fields marvel holds, and puts it before any declared `runtime.prompt`:
  - "You are a successor to `<predecessor>`."
  - When the role declares `shift.handoff`: "Read its handoff at `<path>`
    first", with `{session}` resolved to the predecessor. Otherwise: "Read
    its handoff first, if your process names one."
  - When the seat is on the bus: "Then drain your inbox."
  - Then the role's own `runtime.prompt`, if it declares one.

  marvel stays independent of director: the inbox line appears only when marvel
  itself attached the seat to a bus.
- **D3. A role can turn the successor prompt off**, with
  `runtime.successor_prompt = false` (default true). This is for a role whose own
  `runtime.prompt` already covers succession. There is no free-text
  successor template: the fixed line carries only marvel-written values, so a
  manifest edit cannot make a successor act on text from writable state.
- **D4. Bare harness only.** marvel adds the first message only when the
  command is the bare harness (the same test the claude adapter uses for
  `--append-system-prompt`, `isBareClaude`; codex and opencode get the
  equivalent). A wrapper owns its own command line, so marvel exports the
  composed text as `MARVEL_FIRST_PROMPT` and the wrapper may pass it on.
- **D5. Resume keeps working.** When the role's args resume a harness session
  (`--resume`, `--continue`, `-c`, opencode's `--session`), the first message
  becomes the next turn of the resumed session. Claude and codex take both on
  one command line; opencode needs P3.
- **D6. Apply says what will not happen.** Apply emits an advisory (not a
  refusal) for:
  - an interactive role declaring `runtime.prompt` on a runtime that cannot
    take a first message (generic, forestage, simulator);
  - an interactive role with neither `runtime.prompt` nor a predecessor path,
    saying that a fresh seat of that role starts idle.

  It is an advisory because existing manifests carry `runtime.prompt` on
  interactive roles today, and a refusal would fail their next re-apply. The
  same judgement applied to the unresolved-window design (#667, ruling 2).
- **D7. Every spawn records which first prompt it got.** An event,
  `session.first-prompt`, with `source` = `role`, `successor`,
  `successor+role`, `wrapper-env` or `none`, and the byte length. It never
  carries the text. A supervisor can then tell an idle seat that was never
  prompted from one that was prompted and stalled.
- **D8. Crash repair is a successor too.** Once #541 sets
  `MARVEL_PREDECESSOR` on crash repair, D2 applies to it unchanged. This
  design does not fold #541 in.

## 4. Plan: flat tickets, red tests, edges

| id | change | red test | after |
|---|---|---|---|
| T1 | claude interactive positional (D1, D4) | an interactive bare-claude role with `runtime.prompt` launches with the prompt as the last argument; a wrapper command gets no positional and gets `MARVEL_FIRST_PROMPT` | P1 |
| T2 | codex interactive positional (D1, D4) | the same for codex; the codex home and env are unchanged | P2 |
| T3 | opencode `--prompt` (D1, D4) | the same for opencode via `--prompt` | P3 |
| T4 | successor first prompt (D2, D3) | a shift successor's first message names the predecessor; with `shift.handoff` declared it names the resolved path; with a bus it ends with the inbox line; without one it does not; `successor_prompt = false` leaves only the role prompt; a first spawn gets only the role prompt | T1 |
| T5 | apply advisories (D6) | the two advisory cases each emit one line at apply and refuse nothing | |
| T6 | `session.first-prompt` event (D7) | each spawn emits exactly one, with the right source, and its message holds no prompt text | T1 |
| T7 | resume composition (D5) | a role whose args carry `--resume <id>` launches with the resume flag and the first message both present | T1 |

T5 has no dependency and can ship first. T4 waits on T1 because the successor
prompt rides the same channel. #541 is an edge into D8, not a ticket here.

Probes, each recording the harness version it ran against:

| id | question |
|---|---|
| P1 | claude 2.1.292: does an interactive positional prompt submit at once? Does it still run after the workspace-trust dialog, and after the development-channel consent dialog, once each is answered? |
| P2 | codex 0.160.1: the same questions for the interactive `[PROMPT]` |
| P3 | opencode 1.18.15: does `--prompt` submit, or only fill the input box? Does it combine with `--session`? |
| P4 | read only: how long is the longest declared `runtime.prompt` on the fleet's interactive roles? A command line has a length limit, so a very long prompt may need a file. |

## 5. Security notes

- The successor line is built only from values marvel writes (a session key,
  a resolved path, a fixed sentence). A seat never receives a first prompt
  composed from board state, mail, or any file another seat can write.
- The predecessor key is a session name, already validated as one path
  element before it is used in a handoff path.
- The first message goes on argv, so it shows in `ps` for the seat's user. It
  must never carry a secret. A role prompt that does is a manifest defect, and
  this design adds none.

## 6. Out of scope

- Waking a seat that is already running (the declared-cadence design, #668).
- Preparing first-run state so no dialog stands in front of the prompt (#420).
- Setting the predecessor on crash repair (#541).

## 7. Rulings needed (the operator's)

1. Adopt D1 to D8 and the plan. Recommended: adopt.
2. The successor prompt is on by default, with an opt-out (D2, D3), rather than
   off by default with an opt-in. Recommended: on by default, because the
   failure it fixes is silent and fleet-wide.
3. Apply warns rather than refuses (D6). Recommended: warn now. Revisit a
   refusal once every live manifest is clean.

Each recommendation is valid until 2026-10-21, or a Claude Code, codex or
opencode release that changes how a first message is taken at launch,
whichever comes first; the architect seat that wrote this re-checks it then.
Nothing here executes on silence.
