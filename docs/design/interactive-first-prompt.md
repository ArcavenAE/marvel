# A first prompt for interactive seats

Status: design proposal, 2026-10-07, for #391. Proposal only; the rulings in
section 7 are the operator's. Code references are pinned to marvel `5a972a4`.

Implementation: T1 (the claude interactive positional, D1 and D4) is in
marvel#1046, a draft. P1 is not measured, which the plan puts before T1, so
that PR claims the prompt is on the command line and not that the seat gets a
turn. T2 to T7 and probes P1 to P4 are not started.

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
- **D5. Resume keeps working, per harness form.** The resume test is per
  adapter, because the forms differ:
  - **claude** (2.1.292): `-c`/`--continue` or `-r`/`--resume [id]` in the
    args. The first message is the trailing positional, as for a fresh
    launch, and becomes the next turn of the resumed session. Unprobed: P1
    asks.
  - **codex** (0.160.1): resume is the subcommand `codex resume [SESSION_ID]
    [PROMPT]`, not a flag, and `-c` is `--config key=value`, never a resume.
    marvel appends the first message to `codex resume <id>`, where it lands
    in `[PROMPT]`, and to `codex resume --last`, where codex moves a lone
    positional into the prompt (the reviewer's read of `codex-rs/cli/src/main.rs`
    at `rust-v0.160.1`, lines 2568-2575, pinned there by the test
    `resume_last_accepts_prompt_positional`; `--help` does not show it, and
    P2 confirms it on a launch). Only `--last` with two positionals conflicts,
    and marvel appends one. For a bare `codex resume`, an appended positional
    would land in `SESSION_ID`, so marvel passes no first message and records
    `source = skipped-resume` under D7. It does not refuse at apply: that would
    be a new gate, and D6's reasoning holds.
  - **opencode**: `--session` with `--prompt` is P3's question.
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
  `successor+role`, `wrapper-env`, `skipped-resume` or `none`, and the byte
  length. It never carries the text. The event records what marvel passed on
  the command line, not what the harness took. A message that only fills an
  input box (P3) or does not survive a first-run dialog (P1, P2) reads as
  passed, so on its own the event separates "marvel passed nothing" from
  "marvel passed something"; it cannot separate a seat that was never
  prompted from one that was prompted and stalled. P1 to P3 decide how far a
  passed message can be read as submitted, per harness.
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
| T7 | resume composition (D5) | claude: args with `--resume <id>` and with `--continue` each launch with the resume flag and the first message both present. codex: `codex resume <id>` and `codex resume --last` each launch with the message as the one trailing positional; a bare `codex resume` launches with no message and emits `source = skipped-resume`; a role carrying `-c key=value` is not read as resuming and gets the message as on a fresh launch | T1, T2 |

T5 has no dependency and can ship first. T4 waits on T1 because the successor
prompt rides the same channel. #541 is an edge into D8, not a ticket here.

Probes, each recording the harness version it ran against:

| id | question |
|---|---|
| P1 | claude 2.1.292: does an interactive positional prompt submit at once? Does it still run once each screen that can stand in front of the composer is answered: the workspace-trust dialog, the development-channel consent dialog, the "Teach auto mode" setup prompt (Yes preselected, which took a stray Enter on 2026-10-07), and the usage-limit menu? Does it combine with `--resume <id>` and `--continue` (D5)? |
| P2 | codex 0.160.1: the same questions for the interactive `[PROMPT]`, for `codex resume <id> [PROMPT]`, and for `codex resume --last <prompt>` (does the lone positional run as the next turn, as D5 reads from source?) |
| P3 | opencode 1.18.15: does `--prompt` submit, or only fill the input box? Does it combine with `--session`? |
| P4 | read only: how long is the longest declared `runtime.prompt` on the fleet's interactive roles? A command line has a length limit, so a very long prompt may need a file. |

## 5. Security notes

- The successor line is built only from values marvel writes (a session key,
  a resolved path, a fixed sentence). A seat never receives a first prompt
  composed from board state, mail, or any file another seat can write.
- The predecessor key is a session name, already validated as one path
  element before it is used in a handoff path.
- The first message goes on argv, so it shows in `ps` for the seat's user.
  For a wrapper (D4) the same text is in `MARVEL_FIRST_PROMPT`, which `ps eww`
  shows to the same user. It must never carry a secret. A role prompt that
  does is a manifest defect, and this design adds none.

## 6. Out of scope

- Waking a seat that is already running (the declared-cadence design, #668).
- Preparing first-run state so no dialog stands in front of the prompt (#420).
- Setting the predecessor on crash repair (#541).

## 7. Rulings needed (the operator's)

**Re-checked 2026-10-24**, after the 2026-10-21 expiry passed with no
re-check. The checks were read only: each harness's version and `--help` on
the host where the re-check ran, plus marvel main and #1046. No harness was
launched, so none of P1 to P4 has run.

| harness | pinned in this design | on the re-check host | launch surface, by its help |
|---|---|---|---|
| claude | 2.1.292 | 2.1.310 | unchanged: `claude [options] [command] [prompt]`, `-c`/`--continue`, `-r`/`--resume [value]` |
| codex | 0.160.1 | 0.154.0 (older) | unchanged: `codex [OPTIONS] [PROMPT]`; `codex resume [OPTIONS] [SESSION_ID] [PROMPT]` with `--last` |
| opencode | 1.18.15 | 1.18.31 | unchanged: TUI `--prompt`, `-s`/`--session`, `-c`/`--continue` |

So no release seen here changes the command-line form that section 2 and D5
rest on. Help text cannot show whether a first message is submitted, or
whether it survives a dialog; that is still P1 to P3. One pin no longer holds
everywhere: D5's codex `resume --last <prompt>` reading came from the
`rust-v0.160.1` source, and a host on codex 0.154.0 is not covered by it.

The operator's go to build this design (2026-10-24, relayed by director) is
read here as answering ruling 1 only. Rulings 2 and 3 are still the operator's
and are restated below.

1. **Adopt D1 to D8 and the plan.** Renewed, revised. Recommended: adopt,
   with two revisions to the plan's order:
   - T1 is in review ahead of P1 (#1046). It may merge on the claim it makes,
     that marvel puts the role's prompt on the claude command line; it may not
     be read as "the seat takes a turn" until P1 runs on claude 2.1.310 or
     later.
   - P2 records the codex version of the seats it covers, and runs on each
     codex version the fleet's interactive seats use. Until P2 covers a
     version, T7's codex `resume --last` case for that version is unmeasured.

2. **The successor prompt is on by default, with an opt-out (D2, D3).**
   Renewed, unchanged. Recommended: on by default. The failure it fixes, a
   successor idling until a person types, is silent and fleet-wide, and
   nothing in the re-check changes that. Needed before T4 merges.

3. **Apply warns rather than refuses (D6, and D5's skipped resume forms).**
   Renewed, unchanged. Recommended: warn now. A refusal would be a new gate
   and needs its own ratified decision; revisit it once every live manifest
   is clean. Needed before T5 merges.

Each recommendation is valid until 2026-11-07, or until one of these,
whichever comes first:

- P1, P2 or P3 reports;
- a claude, codex or opencode release on a fleet host whose `--help` changes
  the launch forms in the table above.

The architect seat of the team building this design re-checks them then, and
renews, revises or withdraws each. Nothing here executes on silence.
