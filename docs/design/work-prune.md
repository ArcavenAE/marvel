# Opt-in prune for `marvel work`, with a handoff before every delete

- **Status:** design for review, 2026-10-27. No code lands until this is
  reviewed. Commissioned by the operator (ruling mw1027-1, "let's design and
  build 3"), relayed by the arcaven supervisor as ask ws:marvel-work-prune.
- **Author:** the arcaven team's architect seat.
- **Checked at:** marvel `origin/main` 25bdccf. Every line cite below is at
  that commit.
- **Related:** finding-043 (the "upsert-no-team-prune shape"),
  `manifest-plan.md` (the `marvel plan -f` preview, designed, not built:
  aae-orc-qawkf), `shift-trigger-list.md` and `per-seat-replace.md` (the
  handoff request this design reuses).

## 1. Why

A manifest is the record of what should run, but `marvel work` only reaches
the teams the file names. Drop a team from the file and it keeps running and
spending until someone notices it and deletes it by hand. Separately, every
path that removes a seat today kills its pane with no warning, so a seat
shed by a scale-down or a dropped role loses whatever it had not written
down.

This design makes removal by manifest possible, opt-in, limited to teams the
same file put there, and never silent: a seat is asked for its handoff and
given a bounded window before its pane goes.

## 2. Today, verified

| Premise | Where (at 25bdccf) | Result |
|---|---|---|
| Fewer replicas deletes the excess, oldest first | `internal/team/controller.go:1318-1335` (`case actual > desired`) | true |
| `replicas = 0` is legal and parks a role | `internal/api/manifest.go:449-456` (marvel#335, fixed by #406) | true |
| A role dropped from the file is drained | `reconcileOrphanedSessions`, `controller.go:930-950`; it skips `marvel run` sessions (`Generation == 0`, :943) | true |
| `Apply` loops only over the file's teams | `manifest.go:967` (`Apply`), team loop at :1033, upsert at :1141-1162 | true; no loop removes a team the file stopped naming |
| A file names one workspace; a dropped workspace is untouched | `manifest.go:970` (`m.Workspace`, singular) | true |
| Re-apply sets posture to converge, so a held team spawns | `internal/daemon/daemon.go:1598-1607` | true. Noted, not changed by this design |
| The daemon never saw the manifest's path | before step 1, `cmd/marvel/main.go` sent only `manifest_data` and `workspace_root`; step 1 adds `manifest_source` (`main.go:1056-1064`) | true when written; changed by step 1 |
| No owner record exists | `api.Team`, `internal/api/types.go:1015-1052` | true |
| Every delete kills the pane with no handoff | `Manager.Delete`, `internal/session/manager.go:1562` | true |

The delete call sites, and whether step 4 covers each:

| Call site | Path | Step 4 |
|---|---|---|
| `controller.go:946` | role dropped from the file | covered |
| `controller.go:1431` | scale-down (`plan.Delete`) | covered |
| (new) | prune, step 3 | covered |
| `controller.go:1932` | health restart of a failed seat | not covered: the seat already failed |
| `controller.go:2337` | shift abort deletes the new generation | not covered: those seats just launched |
| `controller.go:2577` | shift drain of the old seat | not covered; see section 9, item 2 |
| `schedule_clock.go:66` | scheduled firing replaced | not covered: headless runs use completion semantics (ADR-010) |
| `daemon.go:1792-1823`, `manager.go:1927` | explicit `marvel delete` and teardown | not covered: an operator's direct act |

One thing found in passing: the scale-down list at `controller.go:1171` is
every session of the role, so a `marvel run` session placed in a manifest
role counts toward its replicas and can be shed, while the role-drop path
skips it. This design does not change that; section 9 lists it.

## 3. Step 1: the owner record

### 3.1 Identity of a manifest

A manifest is identified by **where it was applied from**: the client's host
name and the file's absolute path with symlinks resolved, written
`<host>:<path>`. `marvel work` computes it and sends it as a new apply param,
`manifest_source`.

`<host>` is `os.Hostname()` cut at the first dot and lowercased, so
`host-a.local` and `host-a.lan` are one host. That still moves when the name
itself changes (on macOS with no `HostName` set, the name can come from DNS
and follow the network). A host whose name moves becomes a new source: its
old claims are never released, so that file can no longer prune the teams it
applied under the old name. This fails safe, since the teams stay. `describe
team` shows the stale source, and the teams can be deleted by hand.

If `marvel work` cannot compute the source (the host name or the path cannot
be read), it sends none and prints `warning: could not compute manifest source
(<reason>); teams applied now are never pruned`. The teams that apply names
then take nil claims, as from an older client (rule 3).

A declared id inside the file was weighed and rejected. A file copied as a
template keeps its id, and two files with one id would each prune the
other's teams. A path fails the other way: a moved or copied file is a new
source, its old teams are no longer its own, and nothing is deleted. Every
mistake with a path leaves a team running; every mistake with a declared id
can delete one.

### 3.2 Format

A new status field on `api.Team`, persisted with the team in the store and
never declared in a manifest (the same treatment as `Shift` and
`ConvergencePosture`):

```go
// Claims records which manifest sources have applied this team. nil means
// the team predates claims, or was last applied by a client that sent no
// source: it is never pruned.
Claims *TeamClaims `toml:"-"`

type TeamClaims struct {
	// Sources maps a manifest source ("<host>:<path>") to the time it last
	// applied this team.
	Sources map[string]time.Time
	// ReleasedBy is the source whose apply removed the last claim, and
	// ReleasedAt when. Set only when Sources becomes empty.
	ReleasedBy string
	ReleasedAt time.Time
}
```

Rules 1 and 3 run in each team's upsert under the store lock; rule 2 is one
`ReleaseClaims` sweep under one lock hold, after the team loop succeeds; rules
1 and 2 are not one atomic step, and the per-key argument below is why that is
safe.

1. An apply with a source adds or refreshes that source's claim on every
   team the file names, and clears `ReleasedBy` and `ReleasedAt` in the same
   lock hold, since the team is claimed again.
2. The same apply removes that source's claim from every team, in any
   workspace, that the file does not name. A team whose `Sources` empties
   this way records `ReleasedBy` and `ReleasedAt`. Nothing is deleted; this
   is bookkeeping.
3. An apply with no source (an older client) sets `Claims = nil` on every
   team it names. Such a team is never pruned until a client with a source
   applies it again.

`describe team` prints the claims.

Two applies can run at once (each request has its own goroutine, and `Apply`
holds no lock across the whole file). The rules stay safe because a claim is
one key, `Sources[source]`, on one team. Rule 1 for source B and rule 2 for
source A touch different keys, so they end with the same `Sources` in either
order. Two versions of one file applied at once can leave the older version's
claim on a team the newer one dropped; that claim holds until the file's next
apply, which keeps the team rather than releasing it. A sourceless apply racing
a claimed one leaves whichever ran last on each team; nil is never pruned.

### 3.3 Migration

The field is additive JSON in the store, so no store version moves. Every
team that exists when the daemon upgrades reads `Claims == nil` and is never
pruned. A team picks up a claim the next time a file that names it is
applied from a new client.

That leaves one gap on purpose: a team no file names any more, which is the
case that motivates this work, stays unclaimed forever. The dry-run lists it
with the exact `marvel delete team <ws>/<name>` command for the operator to
run by hand. The rule "an unowned team is never pruned" holds without
exception.

## 4. Step 2: the dry-run

`manifest-plan.md` section 2, point 2, already ruled out `marvel work
--dry-run`: a dropped flag turns a preview into an apply. With prune the cost
of that mistake is a deleted team, so the rule holds here more strongly.

The preview is the verb `marvel plan -f <manifest> --prune`, served by a new
read-only daemon method, `prune_plan`, which takes `apply`'s params plus
`manifest_source`. An older daemon answers "unknown method", so the client
fails closed. Until aae-orc-qawkf lands the full manifest plan, `marvel plan
-f` without `--prune` is refused with a pointer to that ticket. When qawkf
lands, the prune section becomes one section of its output and `prune_plan`
becomes a part of its `evaluate`.

**The prune set.** A team is in this file's prune set only when all four
hold:

1. the file does not name it (by full key, `workspace/name`), and the apply
   would leave it released by this file: its `Claims` is non-nil, its
   `Sources` less this file's source is empty, and either this source is in
   `Sources` or `ReleasedBy` is this source. The plan runs before the apply,
   so it predicts rule 2 of section 3.2 rather than reading its result; step
   3 recomputes after its apply and prunes the set it finds then;
2. it holds no `marvel run` session (`Generation == 0`). Such a session
   belongs to the operator, as the role-drop path already says
   (`controller.go:940-942`), and scale-down would otherwise shed it
   (`controller.go:1171`);
3. it is not mid-shift (`Shift.Phase` is none). A shift's drain and abort
   (`controller.go:2577`, `:2337`) call `Delete` directly, so a prune waits
   for the shift to end;
4. it has no running headless seat, since headless seats are left alone by
   operator ruling 2026-10-28: no
   session of a headless role in an alive state (pending, running or
   crashloop-backoff, `SessionState.CountsAsAlive`), section 5. A live
   interactive seat does not hold the team back; step 4 retires it
   (section 6).

A team that meets rule 1 and fails 2, 3 or 4 is listed as kept or deferred,
with the rule it failed, and is not deleted.

The other groups:

- **Kept, claimed by another file too:** the file does not name it, this
  source is in its `Sources`, and `Sources` less this source is non-empty.
- **Kept, unclaimed:** every team with nil claims, in any workspace, that the
  file does not name.
- **Held:** a team the file names, that exists, and whose posture is hold. It
  will spawn the sum over the file's roles of declared replicas less that
  role's alive sessions, and is listed only when that sum is above zero. A team
  the apply would create is left to the full manifest plan (aae-orc-qawkf).
- A team another source released (`Sources` empty, `ReleasedBy` another
  source), or one only other sources claim, is not listed: it belongs to that
  file's preview.

`prune_plan` is parse only: it parses the manifest as `apply` does and runs
none of `apply`'s other pre-flights (admission, the name clash, `LookPath`);
those are the full manifest plan's. It skips the `workspace_root` transform,
which is a plain assignment that cannot fail and plays no part in the prune
set. It refuses an empty `manifest_source`, as the client does, so a parse
error and a missing source are its only exit 1.

It writes nothing: no store, no events, no tmux. It prints:

```
prune plan for workspace/aae from host-a:/srv/manifests/main.toml
pre-flight checks not evaluated (aae-orc-qawkf)

would delete (claimed only by this file, which no longer names them):
  aae/old-research   3 seats  1 failed, 2 running   handoff: declared, window 5m
  aae/ops-trial      0 seats
  aae/nightly-sync   1 seat  1 running   headless: waits up to 5m

kept, claimed by another file too:
  aae/shared-ops     also host-a:/srv/manifests/ops.toml (applied 2026-10-27T18:02Z)

kept, unclaimed (applied before claims existed, or by an older client):
  aae/fossil-a       delete by hand: marvel delete team aae/fossil-a
  other/fossil-b     delete by hand: marvel delete team other/fossil-b

kept, holds marvel run sessions:
  aae/scratch        1 run session (not the manifest's to remove)

deferred:
  aae/night-batch    shift in progress; re-run the plan when it ends

named by this file and held (re-apply converges them):
  aae/builders       will spawn 2
```

A team with a running headless seat is listed under deferred as 'live
headless seat, left alone by operator ruling 2026-10-28' (section 5).

Each "would delete" line names its seats and their state, the states in
alphabetical order. Counting only the interactive roles that hold an alive
seat, it says `handoff: declared, window W`
when every one declares `shift.handoff` (W is the longest `handoff_window`,
default 5m), `handoff: none` when none does, and `handoff: some roles`
otherwise. A team with a headless role holding an alive seat adds `headless: waits
up to W`, the role's `handoff_window` (section 6.2). The notes are text
output only.

The "held" group reports the posture behaviour section 2 records; it is
printed, not changed.

Exit codes follow `manifest-plan.md` section 6, as ruled 2026-10-19: 0
evaluated, 1 could not evaluate (an older daemon included), and 2 when
something would be deleted, returned only under `--detailed-exitcode`.

## 5. Step 3: `marvel work --prune`

```
marvel work <manifest> --prune
```

`--prune` takes no value. A digest from the preview was required until the
operator ruled it out on 2026-10-28: "no it's needless hurdles and doesnt
protect what is important, it's a false protection". `marvel plan -f
<manifest> --prune` stays as an optional preview of the set, never a
precondition. A client from after the ruling, against a daemon from before
it, is refused for the missing digest, and nothing is applied.

1. The client sends `manifest_source` and `prune: true`.
2. The daemon runs the normal apply, then recomputes the prune set exactly as
   `prune_plan` does, and prunes that set.
3. Each team in the set is marked `Pruning` (by source, at a time) and every
   role's effective desired count becomes 0. The prune re-checks each team
   against the four rules and marks it `Pruning` in one lock hold, and an apply that claims a
   `Pruning` team clears `Pruning` in that same hold, so a claim and a prune
   never interleave on one team. A pruning team starts no
   shift: the automatic triggers skip it and `marvel shift` refuses it, so
   no drain can bypass the ask once the prune has begun. The team's
   sessions then leave through the scale-down path, which asks each one
   for its handoff (section 6). Once a pruning team holds no live session, its
   record is deleted
   exactly as `marvel delete team` does (`Controller.DeleteTeam`,
   `internal/team/prune.go`): health
   state cleared and the broker user revoked. Endpoints and policies stay,
   as they do for `delete team` today. A marked team with no live seat is
   deleted at once, in the same request; one with live interactive seats is
   listed in `draining`, and the reconcile deletes it once its last seat has
   been retired.
   The last re-check and the record delete are one store-lock hold, so an
   apply that claims the team first keeps it; the team's finished sessions
   are deleted after the record.
4. The response lists the teams deleted in `pruned`, always an array, and
   the teams kept or deferred with their reasons. From step 4 it also lists,
   in `draining`, always an array, the teams marked and left to drain, each
   with its seat count; the reconcile deletes each once its last seat has
   gone. The client errors if the response has no `pruned` array, or a null
   one, which is how an older daemon that dropped `prune` is caught: it
   applied, and it pruned nothing. A response with no `draining` key reads as
   none.

**Headless seats are left alone by operator ruling 2026-10-28:** step 4
retires interactive seats (section 6), so a team whose live seats are all
interactive is pruned once each has been asked and has left. A team with a
running headless seat is listed as "deferred: live headless seat, left alone
by operator ruling 2026-10-28" and kept. So no prune kills a running seat
without a handoff ask. Scale-down and a role drop delete a running headless
seat at once, as they did before this design.

A team being pruned that a later apply names again stops pruning: the apply
clears `Pruning`, reclaims it, and the reconciler converges it back. Seats
already asked for their handoff are not un-asked (section 6.4).

### 5.1 Several manifests in one workspace

- A prune by file A reaches only teams whose last claim A removed. B's teams,
  and teams both files claim, are never in A's set.
- A team both files name is kept until both stop naming it. The second file
  to drop it is the one whose `--prune` removes it.
- Prune reaches across workspaces by claim, so a file whose workspace name
  changed can prune its teams in the old workspace. It never deletes a
  workspace.
- The same path on two hosts is two sources. Applying one file from two
  clones gives two sources too; each claim has to go before either prunes.

## 6. Step 4: a handoff before every delete

### 6.1 The ask

At each covered call site (section 2), `Delete` is replaced by `Retire`.
`Retire` reuses the shift handoff request (`internal/team/shift_handoff.go:109`,
`requestHandoff`) for an interactive seat, with a new notice (a running
headless seat is left alone by operator ruling 2026-10-28, section 6.2):

```
retire: this seat is being removed; write your handoff now to <path>, ending with the line "<marker>"; you have <window>
```

The tail from "write your handoff now" is byte-identical to the shift
notices (`shift_handoff.go:26-32`), so a seat that recognises the shift ask
recognises this one. The request is stored on the team, keyed by session,
beside `ShiftRequests`, so a daemon restart inside the window resumes it.

A retiring seat no longer counts as actual for its role, so the next tick
neither asks again nor sheds a second seat for the same deficit. If desired
rises again, a fresh seat spawns; the retiring one is not recalled.

### 6.2 The bound

- **The role declares `shift.handoff`:** the window is the role's
  `handoff_window`, default 5 minutes (`api.DefaultShiftHandoffWindow`,
  `types.go:790`). The pane is killed one tick after the marker is seen
  (`handoffComplete`, `shift_handoff.go:362`), or at the end of the window.
- **The role declares no handoff path:** the notice goes without a path, and
  the seat gets the same default window as a plain grace. There is nothing
  to watch, so the pane goes at the end of the window.
- **A running headless seat:** left alone by operator ruling 2026-10-28.
  It is not asked, because a headless run reads no keystrokes, so a typed
  notice would reach nothing, and marvel does not retire it. Scale-down and
  a role drop delete it at once, as before this design, and a prune defers
  its team (section 4, rule 4).
- **Not waited for, deleted at once:** a seat not in the running state (a
  pending seat, and a finished headless run, included), and a team-wide
  delete by an operator (out of scope, section 2).
- **A seat of a dropped role** gets the plain grace, because its role's
  handoff declaration went with the apply that dropped it.

A prune runs all its seats' windows at once, so a prune ends within the
longest window plus a tick.

### 6.3 A seat that does not answer

At the end of the window the pane is killed. That is the ruling as given:
"wait, bounded, before killing its pane". Before the kill, marvel saves the
pane's last 500 lines, scrollback included, to
`<marvel home>/retired/<workspace>/<name>-<stamp>.txt`, where the stamp is
basic ISO 8601 UTC with no colons (`20261027T024915Z`). The directory is mode
0700 and the file 0600, created new and never overwritten, so what the seat
had on screen survives. A screen that cannot be saved is named in the event
and does not hold the kill back.

Every retirement emits `session.retired` with one outcome:
`handoff-written`, `handoff-missing` (with the reason: window passed,
notice undelivered, or the directory could not be made), or `not-asked`
(with the reason). A running headless seat is left alone by operator ruling
2026-10-28, so no retirement ends `run-finished` or `run-cancelled`.
`handoff-missing` is a warning, and goes to the team's supervisor seat by
the existing missing-handoff delivery (`handoff-missing-delivery.md`) when
that seat is not itself retiring.

A handoff file is never deleted by marvel. It stays at its path for the next
seat or a person to read.

This differs, on purpose, from context pressure, where the operator ruled
that no handoff is worse than a forced replacement and the seat is left
running (`per-seat-replace.md`, section 7, ruling 1). A pressure shift wants
the work to go on; a retirement is a removal someone asked for.

### 6.4 Once asked, final

A retirement is not cancelled. The seat was told to stop and write down its
state; telling it to carry on is a second message it may not read in time.
A re-apply that wants the seat back gets a new one.

## 7. Risks to live seats

| Risk | Mitigation | What is left |
|---|---|---|
| A prune from the wrong file | Path identity; the optional preview shows the set | An operator who prunes from the wrong file |
| A prune deletes a team another file still names | Claims: a team any other source claims is never in the set | A file that names the team but has not been applied since claims existed holds no claim, so it does not protect the team. Its next apply recreates the team, with fresh seats |
| A flag added by habit | none: the operator ruled the digest out on 2026-10-28 (section 5) | A copied command line prunes the set as it stands |
| An older daemon drops `prune` | The client requires `pruned` in the response | none known |
| A pane killed mid-turn | Step 4: the ask, the window, the saved screen | A seat that ignores the notice loses unsaved work after the window |
| A notice into a pane parked at a dialog (marvel#1063) | The window bounds it | The seat never sees the ask; the screen is saved |
| Scale-down and role drops change timing | Seats leave up to one window later than today | Spend during the window; it is bounded and reported |
| A pruning team re-applied mid-prune | `Pruning` cleared, the team converges back | Seats already asked leave anyway (6.4) |
| Retired screens accumulate | Each is small and named by workspace, seat and time | They collect under `<marvel home>/retired`, and marvel deletes none |

## 8. Build plan (flat tickets after this merges; a rule-(d) chain)

One PR per step, each based on the one before, merged in order.

1. **Claims.** `manifest_source` from `marvel work`; `TeamClaims` and the
   three rules in `Apply`; `describe team` prints claims. Red tests: a
   source claims what it names and releases what it does not, across
   workspaces; an apply with no source sets nil; a nil-claims team survives
   every later rule.
2. **`prune_plan` and `marvel plan -f --prune`.** Read-only; the groups of
   section 4. Red tests: each group, including a team kept for a
   run session and one deferred mid-shift; nothing is written (store,
   events, tmux); an older daemon fails the client closed. Blocked by 1.
   Built with a digest, removed by the 2026-10-28 ruling (section 5).
3. **`marvel work --prune`.** `Pruning`, the
   team delete once empty, the `pruned` response field, and the deferral of
   teams with a running seat. Red tests: a
   team claimed by two sources is kept; a team holding a `marvel run`
   session is kept and the run session survives; a team mid-shift is
   deferred; a re-apply clears `Pruning`; a team
   with a running interactive seat is deferred, and so is one with a running
   headless seat. Blocked by 2.
4. **`Retire`.** The notice, the stored request, the windows, the saved
   screen, `session.retired`, and the switch at the three covered call
   sites; lifts step 3's deferral. Red tests: marker kills one tick after;
   no marker kills at the window; no path uses the plain grace; a running
   headless seat gets no notice, is killed when its run ends or at the
   window, and the kill at the window records `cancelled`; a restart inside
   the window resumes it; a retiring seat is not counted as actual.
   Blocked by 3.

## 9. Open items

1. **Ruled 2026-10-28:** plain `--prune` deletes what the apply computes;
   the preview is optional (section 5).
2. Operator `marvel shift` drains the old seat without a handoff
   (`controller.go:2577`). Step 4's `Retire` could cover it; that changes
   shift timing and belongs with marvel#1063, not here.
3. Scale-down can shed a `marvel run` session placed in a manifest role
   (`controller.go:1171`), where the role drop skips one. Separate issue.
4. Re-apply converges a held team (`daemon.go:1598`). The dry-run reports
   it; whether it should is a separate question.

Valid until 2026-11-10; the arcaven architect re-checks it then.
