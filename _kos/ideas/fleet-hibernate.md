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
