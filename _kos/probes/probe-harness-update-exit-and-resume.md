# probe: can a seat with a pending harness update be exited and resumed, and can marvel see the pending state

**Probe id:** `probe-harness-update-exit-and-resume`
**Proposed path:** `marvel/_kos/probes/probe-harness-update-exit-and-resume.md`
**Ticket:** none. Open question for the operator: file one, or attach to an
existing ticket (`aae-orc-wffc9` is the closest; see "Overlap check").
**Date opened:** drafted 2026-10-24, not yet filed.
**Ideas merged:** `_kos/ideas/harness-update-pending-exit-and-resume.md`
(2026-10-04) and `_kos/ideas/harness-update-visible-and-idle-restart.md`
(2026-10-11).
**Question:** for a seat whose harness has installed an update it is not yet
running, can marvel (a) tell that the update is pending without typing into
the pane, and (b) exit the harness and resume the same session so the resumed
process runs the new version with its conversation kept?
**Status:** BRIEF (architect exploration). Nothing run. Pre-registration
below is a draft for the architect seat to accept, amend or decline.
**Read at:** marvel `origin/main` 9672ca5. The local `marvel/` checkout's
`main` was at 740115f, 22 commits behind; every citation below was read with
`git show origin/main:<path>`.

## Why

Two operator asks, a week apart, describe one gap. A seat that shows
"Update installed / Restart to update" keeps running the old harness until
something restarts it, and every restart marvel has today starts a fresh
session: `marvel kill` is an alias for delete session
(cmd/marvel/main.go:1777-1797) and the controller refills the missing replica
(`reconcileRoleAt`, internal/team/controller.go:1010; `planRole`, :1144).
If marvel can see the pending state and a resume keeps the conversation, the
update lands without a shift change and without a person visiting each pane.
Both halves rest on facts no one has measured, so the first step is a probe,
not a design.

## Overlap check

- **`aae-orc-wffc9`** (open, P1): "does a mid-turn hard kill truncate the
  harness transcript JSONL". The T6 party outcome of 2026-10-20 widened it to
  include "does a killed Claude session resume with the same id"
  (row 5 of that outcome). That is the crash form of this probe's H1. This probe is the
  planned form: a clean exit at a quiet point, followed by a version change.
  If `wffc9` runs first, its resume result answers H1a here, and this probe
  keeps only the version half.
- **`docs/design/handoff-crash-options.md` option O8**, "resume is the
  handoff", for crash respawn only, launched with `--resume <predecessor id>`
  (:52, :76). Its "fails when" column names three limits that apply here too:
  the harness session id is unknown outside a fresh Claude launch, the
  transcript carries a secret, and the restored permission mode is not the
  one the role declares. Rulings 2 and 3 in its section 6 (a 30-day
  transcript as a second copy; may a crash respawn start in the mode
  `--resume` restores) are unanswered (T6 grounding, same seat record,
  `00-grounding.md`:13). This probe does not answer them; it measures what a
  ruling on them would rest on.
- **`brief-marvel-upgrade-intelligence.md`** studies marvel's own upgrade
  (`marvel upgrade`, `daemon reexec`), not the harness a seat runs. No
  overlap in method; idea 1 asks whether a pending harness update belongs to
  that work (open question 7 below).
- **`fleet-suspend-resume.md`** "resume" means continuing a stopped process,
  not a harness session resume. Same word, different act.

## What exists, at origin/main 9672ca5

Built:
- **Running version of a claude seat.** tmux reports claude's version as the
  pane command, and `Claude.Foreground` returns it
  (internal/runtime/claude.go:305-310) [read]. The watchdog reads it before
  classifying a screen (internal/daemon/watchdog.go:285) and passes it to
  `panestate.Classify` (internal/panestate/panestate.go:263) [read].
- **The version is recorded on a watchdog verdict.**
  `HarnessState.HarnessVersion` (internal/api/types.go:1153) [read]. No
  `get sessions` column shows it: the registry has no version entry
  (cmd/marvel/columns.go:29-54) [read]. `describe` prints the record as JSON
  (cmd/marvel/main.go:1422-1425) [read], so the value likely shows there when
  a verdict exists [inferred].
- **A "parked: update" HEALTH suffix is renderable.** The parked state is "a
  trust, permission or update dialog" (internal/api/types.go:1174-1178), a
  parked pattern may name reason `update` (internal/panestate/load.go:119),
  and HEALTH appends `(parked: <reason>)` (cmd/marvel/main.go:3250-3251)
  [read]. No shipped pattern carries reason `update`: the shipped set is one
  claude 2.1.290 logged-out pattern, and the only parked pattern is opencode
  test data (`git ls-tree -r origin/main internal/panestate`) [measured].
- **Session id binding for claude.** marvel mints a fresh UUID per launch and
  passes `--session-id` (internal/runtime/claude.go:125-126) [read]. Reusing
  an id fails: `--session-id <reused uuid>` exit 1, "Session ID <uuid> is
  already in use", measured on Claude Code 2.1.271
  (internal/api/harnessid.go:20-28) [read]. So a resume must be `--resume`,
  not a re-sent `--session-id` [inferred].
- **A resume launch records no id.** `AssignsSessionID` declines when the
  launch args carry `--session-id`, `--resume`, `-r`, `--continue` or `-c`,
  leaving `Session.HarnessSessionID` empty (internal/runtime/claude.go:84-93)
  [read]. A resumed seat would lose the binding marvel relies on for its
  transcript unless that rule changes [inferred].
- **codex's update menu is a hazard, and the check is off.** The composer
  refuses an inject while "Update available", "Update now", "Updating Codex"
  or "install.sh" is on screen (internal/composer/composer.go:87-92), and
  every codex seat gets `check_for_update_on_startup = false`
  (internal/runtime/codex_home.go:75-79, marvel#483) [read].
- **Quiet signals.** ACTIVE% claims "not quiet", not "busy"
  (cmd/marvel/activepct.go:63; idea 2 cites :33 at 9bb2cf0) and LAST-ACTIVE
  exists (cmd/marvel/columns.go:47) [read].

Not built [measured, `git grep -F <term> origin/main -- '*.go'`, zero hits each]:
"Restart to update", "Update installed", "Auto-update failed",
"update pending", "pending update", `UpdatePending`, "idle restart",
`IdleRestart`. No `marvel` verb restarts or resumes a seat in place: the
command list has `kill`, `shift`, `reset-health`, `daemon reexec`, and no
`restart` or `resume` (`git grep 'Use: ' origin/main -- cmd/marvel`)
[measured]. Neither idea's premise is false: nothing they propose is built.

Line drift: idea 1 cites main.go:1439, controller.go:965 and :1091, and
claude.go:78-88 at b0a6d67. At 9672ca5 those are main.go:1777,
controller.go:1010 and :1144, and claude.go:84-93. Idea 2's citations at
9bb2cf0 mostly still resolve; activepct.go:33 is now :63.

## Hypotheses

- **H1a (resume).** After a clean exit at an idle composer, `claude --resume
  <id>` in the same working directory restores the same conversation: the
  successor's transcript continues the predecessor's file, or a new file
  links to it, and a nonce from before the exit is recalled.
- **H1b (version).** When the binary on PATH changed between exit and resume,
  the resumed process runs the new binary: the pane command reports the new
  version (`Foreground`) and the transcript records it.
- **H2 (detection, read-only).** The pending state is visible to marvel
  without input to the pane, by at least one of: the banner text in a capture
  (a pattern set, as the watchdog uses), or a running version (pane command)
  that differs from the installed version on disk.
- **H0.** Resume does not restore the conversation, or restores it on the old
  version, or no read-only signal tells pending from current.

## Method (draft, for the architect to amend)

1. **Version pin.** Follow the pinning shape in
   `probe-vsdd-factory-as-marvel-team.md` (:199): `DISABLE_AUTOUPDATER=1`, a
   PATH directory holding one `claude` symlink to an absolute versioned
   binary. Version A and version B are two installed builds; which two is
   open.
2. **H1a.** Start a session on A, with a nonce in an early turn. Exit at an
   idle composer. Resume by id. Ask for the nonce. Record exit text, both
   transcript paths and the recalled answer.
3. **H1b.** Repeat with the symlink moved from A to B between exit and
   resume. Record the pane command and `claude --version` in the pane before
   and after.
4. **H2, banner.** Capture the screen of a session on A after B is installed
   by the harness's own updater, if one can be arranged, and record the
   banner text verbatim for a pattern candidate. Second arm: compare the pane
   command with the version the PATH symlink resolves to.
5. **Control.** One session that is exited and resumed with no version change,
   so H1b has a baseline.
6. Each arm is run for claude first. codex, opencode and crush are later
   rows, each with its own resume command, and are out of this run.

## Pass and fail

- **Pass H1:** nonce recalled after resume, and on the B arm the pane command
  names B. Both recorded verbatim.
- **Fail H1:** nonce not recalled, resume refused, or B arm still reports A.
  Any of these closes exit-and-resume for claude as stated, and the finding
  says which.
- **Pass H2:** at least one read-only signal separates "pending" from
  "current" on both a pending seat and the control.
- **Fail H2:** neither signal separates them; detection would need input to
  the pane, which the composer and inject rules refuse today.
- **Stop:** any classifier or permission denial (report it verbatim); any
  write to the real marvel home (relay run conditions, fourth line); any
  prompt that would need answering in a live seat's pane.

## Scope out

- Building anything: no manifest key, column, indicator, verb or pattern
  file. The design question follows the finding.
- Live fleet seats. No real seat is exited, resumed or captured for this.
- The crash form (SIGKILL mid-turn); that is `aae-orc-wffc9`.
- Ordering restarts across a team (idea 2, open question 6).
- Context-quality thresholds ("the context is good", idea 1); the probe uses
  an idle composer only.
- Any harness other than claude in the first run.

## Where the two ideas disagree

1. **Trigger.** Idea 1 restarts when the update is pending and the context is
   healthy, and leaves a near-limit context to a shift change. Idea 2 restarts
   when the update is pending, the manifest allows it, and the seat is idle;
   it has no context axis.
2. **Who decides.** Idea 1 leaves it open among the seat's manifest, the
   supervisor seat and the daemon. Idea 2 names a manifest opt-in.
3. **What the state is.** Idea 1 makes "update pending" a health condition,
   while asking whether it belongs to the upgrade work instead. Idea 2 makes it
   a displayed state: a version column plus indicators, with "Auto-update
   failed" as a second condition idea 1 does not name.
4. **Detector.** Idea 1 names marvel or the seat-holding component, with the pane reader as
   one candidate. Idea 2 does not mention the seat-holding component, and adds the
   running-versus-installed comparison.
5. **The gain.** Idea 1's gain is faster recovery with no handoff written.
   Idea 2's is fleet visibility and an update landing without a person at
   each pane.

Both agree that resume after exit is unmeasured and is the first thing to
measure. The probe tests that shared premise and both detection candidates,
and takes no side on 1, 2 or 3.

## Open questions (for the architect or the operator; none decided here)

1. A claude session needs a login. The relay run conditions ask for a scratch
   home, and a private claude config loses the login (finding-063). Which
   account and home does the run use, and does a logged-in scratch arm need an
   operator grant?
2. Which two claude builds are A and B, and may a second build be installed
   on the probe host for the run?
3. Should this probe wait for `aae-orc-wffc9`, run with it, or run alone?
4. If H1 passes, a resumed launch leaves `HarnessSessionID` empty
   (claude.go:84-93). Is keeping the predecessor's id on a resume in scope for
   the design that follows, or a separate ticket?
5. O8's permission-mode limit: does a resumed session keep the mode the role
   declares? The probe can record it; whether it matters is ruling 3 of
   handoff-crash-options.
6. Is the claude banner a "parked" state? `HarnessStateParked` describes a
   dialog that waits for a person; a status-line banner does not block input
   [inferred]. If not, the state needs its own name, which is a design choice.
7. Is a pending harness update a seat health condition, or an input to the
   upgrade-intelligence work (idea 1's last question)?
8. What is "idle enough" (idea 2), given that ACTIVE% reads quiet, not idle?
   Not measured here.
9. How detection and recovery split between marvel and the seat-holding component.

## Timebox

One session, about 90 minutes, claude only. [inferred from the comparable
probe-model-per-role-claude timebox]

## Predicted confidence

- H1a: medium. `--resume` is the harness's documented path, but nothing in
  marvel has measured it after an exit.
- H1b: medium. A new process from the PATH symlink should run B; whether the
  resumed transcript accepts a version change is unknown.
- H2: medium for the version comparison, low for the banner until its text is
  captured.
