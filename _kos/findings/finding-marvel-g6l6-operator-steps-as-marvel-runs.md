# finding-marvel-g6l6: operator steps can run as marvel runs for reading, but answering their prompts needs authority and records marvel does not have yet

**Date:** 2026-10-27
**Confidence:** frontier. This is design read from code. Nothing here was run against a daemon, and no test ran.
**Placement:** marvel. The subject is how marvel would run and follow one-off maintenance steps. The scripts, the follower seats and director are objects.
**Source:** a research study requested by the operator, relayed by director, on the idea `_kos/ideas/operator-steps-as-marvel-runs.md` (#1101). Every code cite below was re-read at marvel main c7ca936c.
**Depends on:** seat RPC authorization (`aae-orc-bs3x`). This is a prerequisite, not a part: until it lands, marvel cannot tell an operator's answer from a seat's.

## Why this matters

Operators carry one-off maintenance scripts from cluster to cluster by hand today. Running them as marvel sessions followed by a supervisor would remove that hand-carrying. It only holds if a follower can never answer a prompt that belongs to the operator, and if each run leaves a record a later reader can trust.

Reading a run works now. Answering one does not.

## Short answer

The reading half of the shape is sound. A step can run in a marvel pane, and a follower can watch it, collect its output, and report errors and verdicts.

The answering half needs work in two places:

1. **Authority is not in marvel today, and marvel cannot enforce it yet.** Every caller on the local socket is admin, and every seat holds the socket. So marvel cannot tell an operator's "yes" from a seat's. Until a principal check exists, an approval must come from the operator by a path no seat can type into.
2. **A tty run leaves almost nothing behind.** An interactive `marvel run`:
   - gets no exit code;
   - keeps no output once its pane closes;
   - is marked crashed even when it exits 0.

   There is no run-once key and no enforced timeout.

Recommended first case: a check-only step run by marvel, followed by a supervisor that only reads, with the transcript and exit code kept. The apply stays with the operator at their own terminal. It needs improvements 1 to 3 below, not all of them.

## The facts it rests on

**`marvel run` is the starting point, and it is one-shot.**
- The command is `cmd/marvel/main.go:1743`, `Use: "run <command> [args...]"`.
- The daemon side is `internal/daemon/daemon.go:2187` (`handleRun`):
  - it defaults team and role to `adhoc` (2196-2201);
  - it names the session `run-<unixms>` (2251);
  - interactive is the default mode, and headless needs a prompt (2203-2213).
- Nothing respawns it: the reconciler plans from team records, and the default adhoc team has none (`daemon.go:2242`).
- Arguments are quoted on this path: `internal/session/manager.go:1526`, `cmd += " " + shellQuote(arg)`, fixed in #728.
- The direct path still stamps `MARVEL_SESSION`, `MARVEL_ROLE` and `MARVEL_SOCKET`, but no team or workspace (`manager.go:1527-1557`).
- There is no timeout flag (`main.go:1800-1809`).

**A tty run keeps no exit code and no output, and it reads as crashed.**
- An interactive pane is not kept on exit: `KeepOnExit: sess.Runtime.Mode == api.RuntimeModeHeadless` (`manager.go:781`).
- `Session.ExitStatus` is "empty ... on sessions whose window closed on exit (interactive roles, where tmux keeps nothing)" (`internal/api/types.go:334-341`).
- Completion is headless only (`manager.go:1844-1846`). Every other exit takes the crash path, `live.State = api.SessionCrashed` (`manager.go:1782-1795`). So a step that ends with PASS and exit 0 is recorded as a crash with no status.
- `clearStaleCrashed` runs per role (`manager.go:1783`), and every one-off shares role `adhoc`. So a later run's marker may replace an earlier one's. That is inferred from the call, not traced.
- Run records exist only for scheduled roles: "A session whose role is not scheduled records nothing" (`internal/session/run_record.go:57`).
- A scheduled run keeps at most 4 KiB of its final message (`internal/api/schedule_status.go:20`).
- `marvel capture` reads only a live pane.

**Schedules give at-most-once on a clock, for headless roles only.**
- "schedule is only valid on a headless role" (`internal/api/schedule.go:91`).
- Implemented in `internal/team/schedule_clock.go`, `internal/api/schedule_clock.go` and `internal/team/schedule_freshness.go`:
  - concurrency forbid;
  - the starting deadline;
  - retries and freeze;
  - run history;
  - `stale_after`.
- `active_deadline` is required (`api/schedule.go:134-135`) and parsed (:193), but nothing reads it. The kill is listed as future work (`docs/design/scheduled-runs.md:402`).
- No run-once, dedupe or digest key exists. The nearest thing is the store refusing a duplicate name, and that holds only while the row exists.

**Following a pane by capture and inject is unguarded for a plain script.**
- Composer readers exist for codex and claude only. Every other program gets `unknownReader` (`internal/composer/composer.go:57-66`).
- `panestate` ships one pattern: claude's logged-out screen.
- No code detects a y/N, a "type yes" or a menu in an arbitrary program.
- inject pastes, then sends Enter whenever `--enter` is given, whether or not the program is waiting.
- The only pre-flight refuses two harness menus, and it says of itself "It is a check, not a lock" (`daemon.go:2516-2525`).
- Text joins any draft already typed, unless `--clear` is used, and that is claude only (`main.go:1964-1971`).
- A step produces no prompt event. `agent.permission.requested` (`internal/events/events.go:446`) is bridged from harness adapters only. The push signals a follower gets from a script are its exit events.

**Authority, audit and reach.**
- "The local unix socket is the daemon owner, so it is admin." (`daemon.go:977`; `internal/daemon/scope.go:108`).
- "Every seat is admin today. That is the larger exposure: a seat can also scale, kill and apply." (`docs/design/describe-redaction.md:150`).
- A principal model is deferred (`scope.go:56`).
- `inject` is labelled "executive privilege" (`main.go:1941`). Any admin caller may inject into any session.
- `session.injected` records a byte count and an origin, and never the text. The declared parts of the origin are "a claim, not a proof" (`daemon.go:2367-2369`, `:2412`, `:3550-3560`).
- The `apply` and `run` handlers receive no caller (`daemon.go:1174-1175`, `:1196`).
- Events live in "a bounded in-memory ring" (`events.go:1`). No durable record names the caller of a spawn or an apply.
- Fan-out is reads only, `fanOutMethods = []string{"get", "version"}` (`cmd/marvel/fanout.go:32`), and every write names one cluster (`main.go:95-98`).
- A remote admin key may still call run, apply and inject. Only four methods are local-socket only (`scope.go:115-130`).
- A plain command resolves to the generic adapter (`internal/runtime/adapter.go:404`, `:430`). It projects no policy (`internal/runtime/generic.go:101`) and gets marvel's base environment.
- Redaction covers marvel's RPC responses, "not the seat" (`describe-redaction.md:157`). A secret a step prints is in its capture.

## The cases from 2026-10-27

1. **A schedule dry run stopped on `command "": empty`.**
   - The first apply of a schedule dry-run step stopped at the daemon's runtime pre-flight, because the roles set `image = "claude"` and no command.
   - The step had to repeat `ValidateRuntimes` by hand. `marvel work` has no dry-run or validate flag, and `marvel plan` previews only teams the daemon already holds; it takes no manifest (`internal/team/controller.go:1437-1440`).
   - Lesson: any step that ends in `marvel work` needs a validate-only check, or the check and the apply disagree.
2. **An upgrade hit a stale pin and a false leaf STOP (#1102, fix in #1103).**
   - The upgrade step's default pin named a commit the tap no longer installed.
   - Its post-check waited 90s and exited nonzero on a cluster with no bus, after a re-exec that worked.
   - Lesson: run from a pinned, digest-checked script that reads its inputs at run time.
   - Lesson: keep the transcript beside the exit code. Here the transcript showed the re-exec had worked while the exit code said failure.
3. **A retire step printed its next command and did not run it.**
   - The step removes a role from a manifest and ends with `next: marvel work <manifest>`. By design it does not run marvel.
   - The retirement was not in effect until someone ran that line.
   - Lesson: a step's next command is part of the step. The record needs a "passed, follow-up pending" state, and the follow-up is a second gated step.
4. **Steps read the operator's "type yes".**
   - Upgrade and retire steps read the operator's "yes" from the terminal.
   - Driven through marvel, that terminal is the pane. Any seat could inject "yes", and marvel would record only "3 bytes literal=true" with a self-declared origin.
5. **A seat reran a denied command after a supervisor's pane message.**
   - At a builder seat, an auto mode classifier denied a `gh issue create` with no stated reason. The seat stopped.
   - After a supervisor's message typed into its pane, the seat ran the same command unchanged, and it succeeded.
   - Director's view, which is not a ruling: a supervisor's pane message is not the authority the classifier checks for, so the rerun is bypass-shaped. It is with the operator.
   - Capture and inject is the same path.

## What the system needs: authority and gates

- **Two classes of prompt, declared by the step and not guessed by the follower.**
  - An informational prompt ("press Enter", "which replica") is one a seat may answer. The step names these in its header.
  - Everything else is reserved: "type yes", an apply, a production change, and anything after a classifier or permission denial.
  - A prompt the follower does not recognize is reserved.
- **A reserved prompt reaches the operator, and only the operator answers it.**
  - The follower stops and escalates to director with the prompt text, the transcript tail, the script digest and the target.
  - The operator answers at a terminal no seat can type into: their own shell running the apply, or `marvel attach` by hand.
  - Until the principal check lands, a marvel "approve" verb on the local socket proves nothing.
- **Nothing executes on silence.** A reserved prompt that waits past its timeout ends the run as rejected. No default answer is ever typed.
- **Classifier boundaries carry through.**
  - A refused inject is a control that fired. The follower reports the stated reason verbatim and stops. It never asks another seat to inject for it.
  - After a policy denial, any retry is a bypass.
- **Credentials.**
  - A step reads its secret by name from the operator's store at run time and never prints it.
  - No credential goes through `--env` on `marvel run`, because env reaches the pane through `new-window -e` and is visible to `ps` (`describe-redaction.md:94-96`).
- **Run records and audit.** The record lives in a durable file on the target host. It keeps:
  - the script digest, the target, the arguments, and the start and end times;
  - the exit code and the full transcript;
  - each prompt and who answered it;
  - the verdict.
  It also says that the inject origin is self-declared.
- **Cross-cluster targeting.**
  - Run each step on its target cluster, started there.
  - Starting runs on another cluster over a remote admin key works, but it widens what any holder of that key can do.

## Marvel improvements, ranked

The ranking puts first what blocks the first case, then counts how many of the cases each improvement would have changed.

1. **A run record for interactive runs** (cases 2 and 3).
   - Keep the pane on exit for runs, or pipe it to a file from spawn.
   - Record the exit code, and mark exit 0 as succeeded.
   - Write a run row with the full transcript in a file.
   - Small to medium.
2. **Run identity.** Stamp team and workspace on `marvel run`, under its own team rather than `adhoc`. Small.
3. **Timeouts that end in reject** (case 4).
   - A `--timeout` on run: small.
   - The missing `active_deadline` consumer for schedules: already designed.
4. **A run-once ledger keyed by script sha256 plus target plus mode.**
   - It refuses a second apply of a digest that passed, unless the operator names the run it supersedes. Medium.
   - Interim: a deterministic `--name`, which the store refuses only while the row exists.
5. **Carry the check's digest to the apply.**
   - The apply refuses a digest that no recorded check produced.
   - The approval stays the operator's.
   - Medium, and only after 1 and 4.
6. **A validate-only `marvel work --check`** (case 1).
   - It exposes `ValidateRuntimes` and the manifest validator without applying.
   - Small to medium. `docs/design/manifest-plan.md` was closed on 2026-10-25, so read its successor first.
7. **Pinned script fetch** (case 2). Run from `<repo>@<sha>:<path>` after checking the blob digest. Medium.
8. **A prompt channel instead of screen scraping.**
   - A helper the step calls emits a `run.prompt` event with the prompt's class and text.
   - The answer path for a reserved prompt is out of band by construction.
   - Medium to large.
   - It ranks below the record because a read-only follower needs no detection.
9. **Cleanup.** Delete a finished run's row only after its record is written. marvel makes no worktrees for runs (`internal/daemon/run_workdir.go:15-38`). Small.

## Open questions for the operator

1. **Who may answer an informational prompt?** A supervisor seat by inject, or no seat at all in the first case (read and report only)? The recommendation is no seat in the first case. This recommendation is valid until 2026-11-10, or until aae-orc-bs3x lands, whichever comes first. The research seat that wrote it re-checks it then.
2. **Is `marvel attach` by the operator an acceptable approval path for a reserved prompt?** Or does every `--apply` stay a command the operator types in their own shell, as today?
3. **The classifier case (01M5YSZDEZ) is with you.** Does the ruling there also cover a follower's inject into a running step? This study assumes it does.
4. **Should a run start on a remote cluster over a remote admin key?** Or must each cluster start its own steps?
5. **Where does the durable run record live?** In the seat holding area, under `~/.marvel` on the target host, or in a tracked repo?
