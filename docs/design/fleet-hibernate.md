# Fleet hibernate: ordered sign-off, a held team, and an ordered wake

Design for review. Nothing here is built. Phase 3 waits on the measurement in section 8.

- Author: the arcaven architect seat.
- Idea: `_kos/ideas/fleet-hibernate.md` (#815), with the reboot addendum (#1216).
- Checked against marvel `origin/main`. Every line marked MEASURED cites its measurement. Every other line is a design default, open to review, unless it is marked RULED or INHERITED.

## 1. Why

A fleet suspend keeps every seat's context, but only while the host keeps running. A reboot, a planned update or an unplanned crash loses the panes. The seats then come back, if at all, as fresh sessions with no context. Today a team with no live panes after a reboot holds at the start line until `marvel converge` (`internal/team/controller.go`, `InitConvergencePosture`), and nothing tells the new seats what the old ones were doing.

Hibernate is the planned form of that stop. Every seat writes its handoff first, then ends. The team stays marked as hibernated so nothing respawns it. A later wake brings the team back in order: supervisors first, then workers. Each seat either reopens its own harness conversation by id, or starts fresh and reads its handoff.

Suspend and hibernate stay two verbs. Suspend freezes processes for minutes or hours on a running host. Hibernate ends them, and survives a reboot.

## 2. What already exists, and what this reuses

- **The handoff request.** Past `max_age`, marvel types a handoff notice into a quiet seat. It records a durable `ShiftRequest` on the team, and waits up to `handoff_window` (default 5m) for the role's `handoff` file to end in its `handoff_marker` (`internal/team/shift_handoff.go`). With no marker, it never ends the seat. It escalates with `team.shift-handoff-missing` and leaves the seat running. Hibernate sends the same request on demand and inherits that rule.
- **Role order.** `shiftOrder` (`controller.go`) puts supervisors last, meaning a role with `global_role = "supervisor"`, or a role named `supervisor`. Otherwise it keeps manifest order. Hibernate signs off in that order and wakes in its reverse.
- **The successor launch.** `MARVEL_PREDECESSOR`, `MARVEL_HANDOFF_PATH` and the successor first prompt (`internal/runtime/firstprompt.go`) already tell a fresh seat where its handoff is.
- **Read-only seats.** They hand off through `handoff.put` (slice 1 of `docs/design/read-only-seat-handoff.md`, built).
- **The harness id.** For Claude Code, marvel assigns the conversation id at launch (`Session.HarnessSessionID`, through `--session-id`). It is re-minted on every launch. No other adapter records an id yet, and no code launches a harness with a resume flag.
- **The store.** Teams and sessions already live in bolt and survive a reboot. What a reboot loses is the panes and the processes.

## 3. The verbs

- `marvel hibernate <workspace/team>...` or `--all`. It signs off the named teams and marks them hibernated.
- `marvel wake <workspace/team>...` or `--all`. It brings hibernated teams back in order.
- `marvel status` shows hibernated teams beside the suspend state.

Both verbs follow suspend's surface: a `[y/N]` confirm on a TTY, a refusal without a TTY unless `--yes`, and exit codes 0 for done, 3 for partial and 1 for error. The verb names are the operator's to change (section 10).

**Refused together.** Hibernate refuses while a suspend record exists, and suspend refuses while a hibernate is in progress. Hibernate also refuses a team already mid-shift, as `marvel shift` does.

## 4. Sign-off, in order

One mode decides how a seat comes back (section 6). The sign-off is the same in both modes, so every hibernated seat leaves a handoff behind, and a failed resume always has one to fall back on.

1. **The worker tier first, all at once.** Every non-supervisor seat of each named team gets the handoff notice together, with a hibernate reason in place of max age: "hibernate: write your handoff now". Teams sign off in parallel.
   - Workers go first so a worker with an open question can still ask its supervisor during its window.
   - Sending them all at once bounds the whole sign-off by one window, not one window per seat.
2. **A seat whose handoff carries the marker is ended,** by the same drain a shift uses, and marked hibernated.
3. **A seat with no marker by the end of its window is not ended.** This is INHERITED from the succession contract. It gets `team.shift-handoff-missing` as today, and `marvel hibernate` reports it by name. The seat keeps running, and so does its team's hibernate.
   - The operator can wait.
   - Or they can run `marvel hibernate --end <session>`, which ends that seat without a handoff. That is the one explicit act that loses a seat's context, and it is never automatic.
4. **Supervisors last,** once every worker of their team has ended or been reported. The supervisor's notice lists any worker still running without a handoff, so the supervisor's handoff records it.
5. **The team is marked hibernated** at its first sign-off request, not at its last seat. A team with no running seats is marked at once.
   - The mark is team intent, stored with the team: the time, the actor, the source and the mode.
   - While it is set, reconcile spawns nothing for the team, and `converge`, `scale` and `apply` refuse to spawn for it with "hibernated; run marvel wake".
   - This is what "paused" means for a team with no seats: it will not be started by anything except `wake`.

**Timeouts.**
- Each seat uses its role's `handoff_window`, default 5m.
- A seat that is mid-turn and not quiet gets the notice when it next goes quiet, under the role's `quiet_for`. It is reported as waiting after `max_defer`, default 30m, as max age does today.
- No timeout ends a seat (INHERITED).

## 5. The resume journal, written all the time

A crash nobody planned needs the same record a hibernate does, so marvel keeps it continuously.

- **When it is written:** on every launch, and again whenever a seat's resume id changes, one entry per session.
- **What an entry holds:** the session key, role, team and workspace; the runtime and the adapter's resume id, if it has one; the work directory; the handoff path; the predecessor; and the launch time.
- **What it never holds:** secrets, the heartbeat token, the environment or prompt text.
- **Where it lives:** in bolt, with a JSON mirror beside the socket, the pattern the suspend record set. A person can then read it with the daemon down, and a host backup can carry it. The mirror is written by rename, with a terminal marker last, so a partial file reads as incomplete and not as a smaller fleet.

bolt already holds the teams and sessions across a reboot, so this is the only new state the design adds. There is no separate whole-fleet state file. The idea's "one state file" is this journal, plus the store marvel already keeps.

## 6. Wake, in order

1. **A preflight** runs before any seat starts. It stops with a named failure on any of these:
   - the daemon cannot be reached;
   - a hub or leaf bus link that a woken team declares is down;
   - a worktree lock a seat's workdir needs is held by another session;
   - free disk is under the cluster's floor;
   - a harness binary is missing, or is below the version its resume form needs.

   The operator may skip a named check with `--skip-check <name>`, which is recorded on the wake event. Credentials are checked only by presence, never by use or by reading them.
2. **Supervisors first, per team.** A supervisor counts as up at its first heartbeat. Its team's workers start once it is up, or after its role's health timeout, in which case the wake reports the supervisor as not up and starts the workers anyway. Teams wake in parallel. `--order a,b,c` wakes the named teams one after another, for an operator who wants it.
3. **Each seat starts in its mode.** The default per seat is resume where its adapter has a recorded resume id, and handoff otherwise. `--mode handoff` forces handoff for every seat.
   - **Resume mode:** relaunch in the recorded work directory with the adapter's resume form and the journal's id. A first prompt says "resumed after hibernate from <time> to <time>; re-read your handoff at <path> before acting, since repos and tickets may have moved". It is part of the launch, not a nudge after it.
   - **Handoff mode:** start a fresh successor, with `MARVEL_PREDECESSOR` naming the hibernated session, as a shift does.
   - **A resume that fails falls back to handoff mode** for that seat. The failure is the harness exiting before its first stream event, or reporting that the id names nothing. A failed resume costs context, never the seat. Each fallback is reported by name.
4. **The hibernated mark clears** per team when its last seat has started, and `team.woken` is emitted with the seats resumed, the seats started fresh and the fallbacks.

**After an unplanned stop**, the same `wake --all` reads the journal's last entries. A seat that wrote no fresh handoff before the crash resumes by id if it can. Otherwise it falls back to the last handoff on disk, and the wake reports that handoff's age.

## 7. Selecting by backend

The operator asked to optionally hibernate only seats on paid services and leave local inference running. marvel holds an intended backend label per role (`Runtime.Backend`), but nothing marks a label as paid.

**Default:** `--backend <label,...>` and `--except-backend <label,...>` select seats by that label, so the operator names what counts. No notion of "paid" enters marvel in this design.

A selected subset is not a whole team, so the mark goes on each selected role, not on the team. Without that, a later converge would respawn the hibernated roles while the rest of the team ran. This is the one place the mark lives below the team.

## 8. The gate for phase 3, and what is unmeasured

Resume mode rests on claims that no one has measured:
- **Reopening:** does each harness reopen a conversation by id after its process exits, after the host reboots, and after a harness version upgrade in between?
- **Transcripts:** where does each harness keep the transcript, and does a reboot or a per-seat harness home clean it?
- **The id after a resume:** does a resumed Claude Code session keep its id or mint a new one?

**The gate:** measure Claude Code first, on a scratch seat, across a process exit and a host reboot, with a positive control that the transcript is on disk. Then codex, opencode and crush, from the forms their help text names. Resume mode is built only for a harness that passes. Handoff mode needs none of this.

Also unmeasured:
- which director and marvel bus subjects keep their messages across a hub restart. A woken seat drains its inbox, as a successor does, and the design does not promise that a message sent during the hibernate is kept;
- whether harness logins and keychain access come back without a person at the console after a reboot. The preflight's presence checks show a missing credential, not an expired one.

## 9. Phases

1. **The journal (section 5).** It is useful alone after any crash, and it needs no new verb.
2. **Handoff-mode hibernate and wake on a running host:** sections 3, 4, 6 and 7, with every seat in handoff mode. It reuses the handoff request, the drain and the successor launch.
3. **Resume mode and the post-boot preflight,** per harness, after the section 8 gate.

## 10. Decisions for the operator

Each has a default above. The design does not rely on any of them being ruled before phase 1.

- **The verb names:** `hibernate` and `wake`.
- **The remote scope:** whether `mrvl://` reaches hibernate and wake under suspend's `suspend` scope word, under a new word, or under `admin` only. Default: the `suspend` scope, since both stop a fleet.
- **`--end <session>`:** whether an operator may end a seat that wrote no handoff. Default: yes, by name only, never in bulk.
- **The backend selector:** label lists, or a declared paid set (section 7).
- **The default mode:** resume where possible, or handoff always, until phase 3 has measured more than one harness.

## 11. Inherited rulings

- Nothing resumes except an explicit command (from fleet suspend, SR-C).
- A wake from the watch view asks a one-line confirm, default no (from fleet suspend).
- A seat without a handoff is never ended automatically (the succession contract, and `shift_handoff.go`).
- A handoff file marvel writes for a read-only seat expires after 7 days by default (read-only seat handoff, section 7.1).

The crash-safe handoff options (`docs/design/handoff-crash-options.md`) are still open, and this design does not rely on any of them.

## 12. Candidate requirements (provisional)

Each is tagged DESIGN (this document, open to review) or INHERITED.

- **HB-A (INHERITED):** hibernate never ends a seat whose handoff lacks its marker, except by an operator's `--end` naming that seat.
- **HB-B (DESIGN):** a hibernated team spawns nothing until `wake`, whatever reconcile, converge, scale or apply would do.
- **HB-C (DESIGN):** every seat a hibernate ends has written a handoff first, in both modes.
- **HB-D (DESIGN):** the resume journal is written on every launch and every resume-id change, and holds no secret.
- **HB-E (DESIGN):** a wake starts a team's supervisors before its workers, and reports any supervisor not up by its health timeout.
- **HB-F (DESIGN):** a failed resume falls back to a fresh start from the handoff, and is reported by name.
