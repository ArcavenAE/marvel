# Hibernate: ordered sign-off, scoped pause, ordered resume

**Status: idea. Pre-hypothesis. Nothing here is designed or measured.**

Captured: 2026-10-10
Source: operator, relayed by director.

## The operator's words

> "kos idea marvel could we have a hybernate feature that when called
> prepared handoff/restart materials and caused each of the worker agents to
> sign off, then the supervisors to do the same, and cleanly paused
> operations by team or optionally excluded local inferance and only paused
> teams running on paid services, and also have some kind of controlled
> resume, that probably unfreezes supervisors first, brings in a new shift
> and bootstraps them in an orderly fashion?"

## Where this lives, and why

Filed in marvel's graph by the subject test. marvel holds every seat's pane,
role, shift and handoff contract, so an ordered stop and an ordered restart
are marvel verbs. Director and the harnesses are objects it acts on.

## How it differs from what exists

Hibernate is not the fleet suspend. Suspend (`_kos/ideas/fleet-pause-resume.md`,
#677; design `docs/design/fleet-suspend-resume.md`) freezes every process in
place and resumes the same seats with their context intact. Hibernate ends
the seats on purpose, after each has written its handoff, and resumes with a
fresh shift that reads those handoffs. Suspend keeps context; hibernate
trades it for a clean restart and stops spending entirely while it lasts.

It reuses three things marvel already has rather than adding a parallel path:

- **Shift replacement.** A shift replaces a role's sessions with fresh ones
  (rolling shift; triggers in `docs/design/shift-trigger-list.md`).
- **The max-age handoff request.** Past its max age, marvel types a handoff
  request into a quiet seat and waits for the role's declared `handoff` file
  to end in its `handoff_marker` (`_kos/ideas/max-session-age.md`, the
  shift-trigger design). Hibernate would send the same request on demand.
- **The handoff file and marker contract**, including handoffs for
  read-only seats (`docs/design/read-only-seat-handoff.md`) and the
  crash-safe options (`docs/design/handoff-crash-options.md`).

## The new parts

1. **Ordered sign-off.** Workers first, then supervisors. Each seat gets a
   handoff request and signs off only once its handoff file carries the
   marker. A supervisor goes last so it can record what its workers left.
   A seat that writes no handoff is not ended; it is reported, as max age
   already does.
2. **Scope by team.** Hibernate one team, several, or a whole cluster, the
   same way a shift names its team.
3. **A paid-backend selector.** Optionally skip seats on local inference and
   hibernate only seats on paid services. marvel already records a role's
   intended backend label (`Role.Runtime.Backend`, `internal/api/types.go`),
   which also decides whether a spawn gets a backend overlay
   (`internal/session/backend.go`), but nothing yet says which labels are
   paid; that may belong to the
   backend registry idea (`_kos/ideas/inference-backend-registry-and-quota-estimation.md`).
4. **An ordered resume.** Supervisors first, then a fresh shift of workers,
   each bootstrapped from the handoff its predecessor wrote
   (`MARVEL_PREDECESSOR` already names the old seat to a successor).

## Rulings it would inherit

These are recorded in public design documents, and a hibernate design would
start from them rather than reopen them:

- From `docs/design/fleet-suspend-resume.md`: nothing resumes except an
  explicit resume; a resume from the watch view asks a one-line confirm,
  default no; there is no nudge or stagger after resume in the first phase;
  remote use needs a dedicated scope or admin.
- From `docs/design/read-only-seat-handoff.md`: a handoff file marvel writes
  expires, configurable, default 7 days, and the first scope is max-age
  rotations.

One more input is relayed and not yet recorded in a design document: the
operator has given a direction on the crash-safe handoff options in
`docs/design/handoff-crash-options.md`. That document still carries them as
recommendations, so a hibernate design should confirm the ruling before
relying on it.

## Tensions

- An ordered sign-off can stall on one seat that never writes its handoff.
  The succession contract says never shift unwatched, so a stall waits for
  a person; how long the rest of the team waits is open.
- Workers first assumes workers need nothing from their supervisor while
  signing off. A worker mid-ask may need an answer first.
- "Paid" is not a property marvel holds today. A label set the operator
  declares is the simplest form; the backend registry is the fuller one.
- Resume by fresh shift loses whatever a handoff did not capture. That is
  the price of hibernate over suspend, and the operator chooses per use.
- Suspend and hibernate could share one verb with a mode, or stay two verbs.

## Addendum, 2026-11-02: hibernate across a reboot, resuming the same sessions

Source: operator, relayed by director on 2026-11-02. Still an idea,
pre-hypothesis; nothing below is designed or measured.

> "kos idea marvel today we have a pause/resume, but that works only while
> the os continues to run, does not survive a reboot. what would it look like
> to have a full hibernate, where agents are exited, the full state of marvel
> is written to a file, work manifest including resume strings, and there is
> some method post-reboot to start everything up, do a preflight check, then
> resume each session?"

### How it differs from the two ideas above

- **Suspend** (`_kos/ideas/fleet-pause-resume.md`) keeps every process alive
  and frozen. It keeps context, and it does not survive a reboot, because
  the processes do not.
- **Hibernate as captured on 2026-10-10** (the sections above) ends the
  seats and resumes with a fresh shift that reads their handoffs. It
  survives a reboot, and it gives up context.
- **This one** ends the seats, and after the reboot it resumes the same
  harness sessions through each harness's resume string: the Claude Code
  session id, and whatever codex and opencode offer in its place. Context
  survives the reboot, because each conversation is reopened rather than
  replaced.

It is closest to `_kos/ideas/harness-update-pending-exit-and-resume.md`,
which exits and resumes one seat for a pending update. This idea does the
same for every seat, with a reboot in between.

### What marvel holds today (read at origin/main c815c13e)

- For Claude Code, marvel already names the session: `Session.HarnessSessionID`
  (`internal/api/types.go`) holds the id marvel assigned at launch through
  `--session-id`. That is the resume string this idea needs for Claude seats.
  It is re-minted on every launch, so it names the current conversation,
  not the first one.
- Claude is the only adapter that implements `SessionIDAssigner`
  (`internal/runtime/claude.go`). codex, opencode and the other adapters
  record no session id, so their resume strings would have to be discovered
  or added.

### Recommended direction: one verb, two modes

The operator, 2026-11-02, verbatim: "perhaps we should support the 10-10 or
resume approach?" Director recommends supporting both, as one verb with a
mode. This is the idea's direction, not a design.

- **Resume mode.** Exit each seat cleanly and record its harness resume id,
  working directory and launch args in a state file. After the reboot, run
  a preflight, then start each seat with its resume id, so its context is
  kept.
- **Handoff mode.** The 10-10 path above: ordered sign-off, then a fresh
  shift that reads the handoffs. It is for a harness with no resume id, and
  for a seat near its max age, where keeping the context is the wrong
  choice.
- **Default per seat:** resume where the harness gives an id, handoff
  otherwise.

Each harness's help text names a resume-by-id form (versions read
2026-11-02; whether each works after a reboot is not measured):

| harness | resume by id |
|---|---|
| Claude Code 2.1.316 | `claude --resume <id>` |
| codex 0.160.1 | `codex resume <id>` |
| opencode 1.18.15 | `opencode --session <id>` |
| crush 0.88.1 | `crush --session <id>` |

### A resume journal written all the time, not only at hibernate

A recent unplanned crash showed the record is needed even when nobody
called hibernate. So marvel would write the resume journal continuously:
on each spawn, and again whenever a seat's resume id changes. An unplanned
reboot or crash can then use the same post-reboot start as a planned one,
reading the last journal entry for each seat instead of a file written at
exit.

### The parts

1. **The journal** (above). One entry per seat: its mode, its resume id
   where it has one, its working directory, role, workdir and launch args.
2. **A planned exit.** For a resume-mode seat, the harness exits cleanly and
   the journal entry is brought up to date, with any director messages
   still pending in its inbox. A handoff-mode seat signs off as in the 10-10
   idea. The order can follow the 10-10 sign-off (workers first,
   supervisors last), so a supervisor's record includes what its workers
   left.
3. **One state file.** It holds:
   - the daemon's state;
   - the seats, with their journal entries;
   - the teams;
   - the bus subjects in use;
   - messages held for delivery.

   It is written atomically, with a terminal marker last, so a partial file
   reads as incomplete rather than as a smaller fleet. It goes where a host
   backup can carry it. After an unplanned stop the journal stands in for
   the parts the exit would have written.
4. **A post-boot start and a preflight.** The start is launchd or an
   operator command. The preflight checks each of these before anything
   resumes, and stops with a named failure:
   - the daemon is up;
   - the hub and leaf bus links are up;
   - credentials are present and valid;
   - worktree locks are intact;
   - there is enough free disk;
   - each harness binary is present, at a version it can resume from.
5. **A start per seat.** Supervisors go first, then their workers.
   - A resume-mode seat relaunches in its recorded directory with its
     resume id.
   - A handoff-mode seat starts as a fresh shift that reads its handoff.
   - A resume that fails falls back to handoff mode, so a failed resume
     costs context and never the seat.

### Tensions

- **A resumed context may be stale.** The repos, branches and tickets a
  seat was reading can change while it is down, and a resumed seat does not
  know that unless it is told.
- **The harness may have pruned the id.** A harness that cleans old
  sessions leaves a resume id that names nothing, so the fallback to
  handoff mode is the common path for a long stop, not a rare one.
- **Resume support differs across harnesses.** codex, opencode and crush
  each have a resume form, but how each behaves after a reboot is
  unmeasured.

### Open questions

- **Harness resume reliability.** Does each harness reopen a session by id
  after its process, and the host, have restarted? Does a version upgrade
  in between break the resume?
- **Transcript retention.** A resume needs the harness's own transcript on
  disk. Where each harness keeps it, how long it keeps it, and whether a
  per-seat harness home (`HarnessHome`) moves or is cleaned at reboot are all
  open.
- **Bus durables.** Which director and marvel subjects keep their messages
  across a hub restart, and which are lost unless the state file carries
  them.
- **Auth after reboot.** Whether harness logins, keychain access and marvel
  peer keys come back without a person at the console. If they do not, the
  preflight is where that shows, before any seat resumes.
- **Recording the id for every harness.** Today marvel records an id only
  for Claude Code. Whether the others can be assigned an id at launch or
  only discovered after it is open.
