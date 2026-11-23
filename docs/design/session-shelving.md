# Session shelving: finished sessions leave the default view over time

- **Status:** design, rulings made 2026-11-23 (section 5). Nothing here is
  built; the build goes to a marvel builder, red tests first.
- **Author:** the arcaven team's architect seat.
- **Checked at:** marvel `origin/main` a7e5c202. Every line cite below is at
  that commit unless it names another.
- **Builds on:** ADR-010 and its amendments (completion semantics, the
  scheduled-role amendment, exit 78), `scheduled-runs.md`, `fleet-hibernate.md`,
  `work-prune.md` section 2 (every delete call site).
- **The word:** this note calls the act *shelving* (`ShelvedAt`,
  `session.shelved`). The first draft called it reclaim, but marvel already
  uses that word for a kill: `marvel daemon --reclaim`
  (`cmd/marvel/main.go:551`, `internal/daemon/daemon.go:189`) destroys tmux
  state the daemon does not own. A shelved row is never killed or deleted.

## 0. Why

A fleet that runs one-shot scripts, probes and scheduled jobs keeps every
finished row in `marvel get sessions` until something deletes it, and for
several kinds of row nothing ever does. Three rows of the kind seen on a live
cluster today:

- a crashed one-shot script, ended 1d22h ago;
- a crashed seat of a role that runs once a day, ended 19h ago;
- a reader that succeeded 3d5h ago, in a team that is now hibernated.

Each one is true and each one is old. Read together with the live seats, they
make the view longer and the live state harder to see. This note gives each
kind of finished row a retention time, after which it leaves the default view
and stays in history. Nothing is deleted, and nothing gates on it. Doctor
reports every crashed or failed row, of every kind, before and after it is
shelved (section 2.4), so leaving the view does not hide a failure.

## 1. What exists today

### 1.1 The store keeps every finished row

- Sessions live in memory (`internal/api/store.go:35-39`) and in the bbolt
  bucket `sessions` (`internal/api/bolt.go:55`). Create and update write
  through, and `DeleteSession` (`store.go:514`) removes the row.
- The states are `pending`, `running`, `succeeded`, `failed`,
  `crashloop-backoff` and `crashed` (`internal/api/types.go:18-38`).
  `CountsAsAlive` is true for the first, second and fifth only.
- A session records `CreatedAt` (`types.go:385`), `ExitStatus` (`:419`) and
  its restart and failure counts (`:382-383`). **It has no end time.** Only a
  scheduled run's `RunRecord.EndedAt` (`internal/api/schedule_status.go:34`)
  has one.
- No retention, TTL or prune exists for sessions. The only bound is the
  crashed marker: "at most one Crashed marker exists per role at a time"
  (`internal/session/manager.go:2443-2447`), and the controller clears the
  marker when it spawns a replacement (`internal/team/controller.go:1533`).

### 1.2 Who deletes a row, and who never does

Rows leave the store on `marvel delete`, on scale-down, when a role leaves the
manifest, on a health restart, during a shift, on a failed spawn, and when a
scheduled role's next firing starts (`work-prune.md` section 2 tables them).
Three kinds of finished row have no path out:

- **A `marvel run` row** (generation 0) that has ended. The manifest-drop path
  skips generation 0 on purpose (`controller.go:1057`).
- **A finished run of an unscheduled headless role.** It holds its replica
  slot by design: `OccupiesReplicaSlot` (`internal/api/budget.go:252-256`) is
  true for a headless `succeeded` row and for exit 78. Deleting that row would
  free the slot, and the reconciler would run the job again. This is the
  reason shelving can never be a delete.
- **The crashed marker of a role that will not respawn**, for example a role
  frozen by its restart policy, or one whose last replica ended as `failed`.

### 1.3 Where `failed` is written

`ReapDead` (`internal/session/manager.go:2448`; at `origin/main` 2bc58fa0 it is
at `:2483`) is not the only writer of `failed`:

- the controller writes it at `internal/team/controller.go:866` (freezeRole),
  `:2041` and `:2066` (the restart policies), `:2147` (max_restarts) and
  `:2167` (the restart backoff). Most of these follow a crash that `ReapDead`
  already reaped;
- `keepAfterFailedKill` (`manager.go:2330-2345`) marks a row `failed` with
  `KillError` set when a kill did not take. Its comment says the process "may
  still be running". `ReapDead` finishes the delete once the pane is gone
  (`manager.go:2491-2500`), so such a row is not finished until then.

### 1.4 Scheduled roles already keep only the latest run

When a scheduled role's next firing spawns, the controller deletes the
finished rows of earlier firings (`controller.go:1534-1546`: "their finished
rows are in the run history, so they go when the next firing's run starts").
The run history lives on `ScheduleStatus.History`
(`schedule_status.go:118-120`), trimmed per outcome by `AddRun` (`:158-191`),
3 succeeded and 3 failed by default (`internal/api/schedule.go:285-286`). Its
ceiling is `DefaultScheduleHistoryMax`, 50 (`schedule_status.go:293`), when the
cluster sets no `schedule_history_max` (`internal/config/config.go:317`).
ADR-010's scheduled-role amendment scopes the slot to the current firing
(`OccupiesFiringSlot`, `internal/api/schedule_clock.go:308-313`).

### 1.5 What `get sessions`, events, doctor and hibernate do

- `get sessions` has no state filter (`cmd/marvel/main.go:1536-1547`). It
  has `--all-clusters`, and `hibernate --all` and `wake --all` mean every team.
  The daemon's `handleGet` (`internal/daemon/daemon.go:1804-1844`) returns
  every row.
- Events are a bounded in-memory ring of 2000 entries
  (`internal/events/events.go:820`), lost when the daemon restarts.
  `marvel events --session <key>` filters by string and never consults the
  store, so it shows a deleted session's events while they are in the ring.
- `marvel doctor` reports no crashed or failed session. `checkSeats`
  (`cmd/marvel/doctor.go:461-501`) reads only the pane word, which is empty
  for a row that is not running (`internal/daemon/pane_column.go:90`), so such
  a row prints SKIP. Doctor's contract: it "writes nothing but to w and
  changes no setting" (`doctor.go:100-102`).
- No `introspect` command exists in marvel code.
- Hibernate ends only running, interactive seats (`internal/team/hibernate.go:53`),
  deletes their rows and records their keys on the team's mark
  (`hibernate.go:365-378`). A finished row already in the team stays, so a
  hibernated team can still show an old finished reader.
- **No wake time is stored.** The team's `HibernateMark`
  (`internal/api/hibernate.go:38-50`) carries `Since`, the hibernate start.
  `advanceWake` (`internal/team/hibernate.go:557`) clears the mark once the
  covered roles are up, and nothing on the team records when that happened.

## 2. The rule: a retention time per kind, then history

### 2.1 Kinds, decided by the row's fields, never by a role's name

| kind | how the code knows | default retention |
|---|---|---|
| one-shot or probe | generation 0 (`marvel run`), or a headless role with no `schedule` block | 24h after it ended |
| scheduled run | the row has a `Firing` | latest run: none (section 2.3); earlier runs: already deleted at the next firing |
| standing role, crashed or failed | an interactive role's row in `crashed` or `failed` | 24h after it ended |
| headless exit 78 | `IsConfigError` (`internal/api/budget.go`) | never shelved; it waits for a person (ADR-010, exit 78 amendment) |
| a failed kill | `KillError` is set | never shelved; its process may still be running (section 1.3) |

A role named for one-shot work that runs as an interactive role is a standing
role under this table. The fields decide, so a name never hides a failure.

Retention is a cluster setting with these defaults, for example
`session_retention = { oneshot = "24h", standing = "24h" }`, beside the
cluster's other switches in `internal/config/config.go`. `"0"` turns shelving
off for that kind.

### 2.2 Shelved means hidden from the default view, never deleted

- A session gains two proposed fields, neither of which exists today:
  - `EndedAt`, set where a row first stops: by `ReapDead` when it marks a row
    `succeeded`, `failed` or `crashed`, and by any later writer of `failed`
    (section 1.3) only when `EndedAt` is still zero. A later transition, for
    example a reaped crash the controller then marks `failed`, keeps the
    first `EndedAt`. `keepAfterFailedKill` never sets it;
  - `ShelvedAt`, set by the shelving pass when the row's retention has passed.
- A row with a zero `EndedAt` is never due, whatever its state.
- The default `get sessions` view, table and `-o json`, leaves out rows with
  `ShelvedAt` set. A new `--shelved` flag shows them, with the state cell read
  as `crashed (exit 1, shelved)`. The flag is not named `--all`, which
  `--all-clusters`, `hibernate --all` and `wake --all` already give other
  meanings. `describe session <key>` always shows a shelved row.
- Shelving emits a `session.shelved` event, so `marvel events --session`
  shows when it happened while the event is in the ring.
- The row stays in the store, so `OccupiesReplicaSlot`, budgets, shift
  readiness and the run history read exactly what they read today. Since the
  event ring does not survive a restart, the store row is the record that
  makes "nothing is lost" true.
- A row leaves the store only by the delete paths of section 1.2, as today.
  No cap on shelved rows per role in this build (section 5).

### 2.3 A scheduled role keeps its latest run

ADR-010 and `scheduled-runs.md` keep the latest firing's row until the next
firing replaces it, and the run history keeps earlier outcomes. Shelving adds
nothing for earlier firings, which are already gone, and never shelves the
latest run. A crashed latest run therefore stays visible until the next firing,
at most one schedule period. The daily role in section 0 is that case: it reads
as crashed for up to a day, which is the signal working, not clutter.

### 2.4 Doctor reports every crashed or failed row

A crashed or failed row is a failure someone may need to act on, whatever its
kind. Its retention clock starts at `EndedAt`, like every other kind (ruled,
section 5), and doctor reports it, so leaving the default view never hides it:

- Doctor gains a `sessions` check: one WARN line per crashed, failed or
  exit-78 row that is not shelved, with its age since `EndedAt`, and one line
  counting the shelved crashed and failed rows of every kind (one-shot, probe,
  headless and standing), pointing at `get sessions --shelved`. It counts as
  a warning, not a failure, so doctor's exit code is unchanged.
- Doctor stays a reader: the check writes nothing, and doctor's contract text
  is unchanged.
- When an `introspect` command exists, it lists the same rows the same way.

### 2.5 A hibernated team's finished rows stay until wake or delete

The shelving pass skips every row of a team whose hibernate mark is set
(hibernated or waking). A team gains a proposed field, `WokeAt`, which does
not exist today: `advanceWake` (`hibernate.go:557`) sets it on the team, in
the same store update that clears the mark, so it persists with the team in
the bbolt bucket `teams` (`bolt.go:54`). A row's retention is counted from the
later of its `EndedAt` and the team's `WokeAt`, so an old reader does not
vanish in the same moment the team comes back. `marvel delete team` removes
the rows as it does today (`daemon.go:1955-1962`).

## 3. Diagnostic housekeeping only

Shelving decides what the default view shows and nothing else. Under the
fleet's rule that a vital sign stays diagnostic and never becomes a gate by
accretion:

- no spawn, slot, budget, admission, shift or wake decision reads
  `ShelvedAt`;
- no command's exit code depends on whether a row was shelved;
- doctor's new check warns and never fails;
- shelving never deletes, so turning it off (`"0"`) restores today's view.

## 4. Where it sits in the code, and the red tests to start with

Flat build tickets, in order. `after:` names what lands first. Line cites are
at a7e5c202; `internal/session/manager.go` has moved since, so at `origin/main`
2bc58fa0 the `ReapDead` cites sit about 35 lines lower.

- **B1. `EndedAt` on the session.** `internal/api/types.go`, set in
  `ReapDead` (`internal/session/manager.go`, the succeeded, failed and crashed
  branches near `:2503-2565`) and, only when still zero, at the controller's
  `failed` writes (section 1.3). after: none. Red, in
  `internal/session/manager_test.go` beside `TestReapDeadCapsCrashedMarkers`
  (`:576`): a reaped headless exit 0, a reaped exit 78 and a reaped crash each
  have a zero `EndedAt`. Green:
  - each carries the reap time;
  - a reaped crash later marked `failed` by the controller keeps its first
    `EndedAt`;
  - a row marked `failed` by `keepAfterFailedKill` has a zero `EndedAt`.
- **B2. The kind and due rule, as a pure function.** `internal/api`, for example
  `ShelfDue(s Session, team Team, policy Retention, now time.Time) bool`.
  after: B1. Red: a table test with the three rows of section 0 and the exit 78
  row returns nothing, because the function does not exist. Green:
  - the one-shot crash at 1d22h is due;
  - the latest scheduled run at 19h is not due;
  - the hibernated reader at 3d5h is not due while the mark is set, and is
    not due at 23h after `WokeAt`;
  - exit 78 is never due;
  - a row with `KillError` set is never due;
  - a row with a zero `EndedAt` is never due, in every state;
  - a standing crash that ended 23h ago is not due, and one that ended 25h
    ago is due.
- **B3. The shelving pass, in the controller's reconcile.** `internal/team`.
  after: B2. Red, in `internal/team/headless_completion_test.go` beside
  `TestHeadlessCompletionHoldsSlotAndIsNotRefilled` (`:21`): a shelved
  headless `succeeded` row is refilled, or its row is gone. Green: the row is
  still in the store with `ShelvedAt` set, it still occupies its slot, and no
  new session spawns. A second test: the pass emits one `session.shelved`
  event per row and none on the next pass.
- **B4. The default view and `--shelved`.** `cmd/marvel/main.go` (`getCmd`,
  the flags at `:1536-1547`, the state cell at `:3827-3829`) and
  `internal/daemonrpc/params.go` (`GetParams`). after: B3. Red, in
  `cmd/marvel/render_test.go` and `cmd/marvel/getsessions_json_test.go`: a
  shelved row appears in the default table and JSON. Green: it is absent from
  both, present under `--shelved` with `shelved` in its state cell, and
  `describe session` shows it. Keep `TestGetRegisterLeavesGetSessionsUnchanged`
  (`internal/daemon/register_get_test.go:138`) green.
- **B5. Doctor's `sessions` check.** `cmd/marvel/doctor.go` beside
  `checkSeats`. after: B1, B3. Red, in `cmd/marvel/doctor_test.go` beside
  `TestDoctorSeatPaneWords` (`:261`): a crashed standing row prints SKIP "pane
  state not read" and nothing else. Green:
  - it prints a WARN line naming the row and its age;
  - a crashed one-shot row prints the same WARN line;
  - a shelved crashed row, standing or one-shot, is counted on the shelved
    line;
  - doctor's exit code is unchanged, and doctor writes nothing.
- **B6. Hibernate and wake.** `internal/team/hibernate.go` and the team type
  in `internal/api/types.go` (`WokeAt`). after: B3. Red: a finished row in a
  hibernated team, past its retention, is shelved. Green:
  - it is not, while the mark is set;
  - `advanceWake` sets `WokeAt` in the update that clears the mark, and it
    survives a store reopen;
  - after wake the row is shelved only once its retention has run from
    `WokeAt`.
- **B7. The cluster setting.** `internal/config/config.go`. after: B2. Red: a
  cluster with `session_retention.oneshot = "0"` still shelves. Green: it
  does not, and an unset key reads the default.

## 5. Ruled 2026-11-23

The operator ruled on this note on 2026-11-23, relayed by director. The
operator's words on these items, verbatim: "As for all the reset, yes, you're
right update them as you describe and route".

- **S1. When a crashed standing seat's clock starts.** Ruled: at `EndedAt`, as
  for every kind, not at doctor's first report. Doctor, and introspect once it
  exists, still report the row (section 2.4). No `ReportedAt` field is added.
- **S2. The retention defaults.** Ruled: 24h for each kind.
- **S3. A cap on shelved rows kept per role.** Ruled: no cap in this build;
  measure store growth after B3 lands, and propose a cap only if it is needed.

The rulings were made on the first draft, which called the act reclaim. The
rename (the header's "The word") and the review fixes after it change no
ruled item.

## 6. Out of scope

- Deleting any existing row. Whether a given finished seat on any cluster is
  deleted stays with its owner.
- The run history's own bounds (`history`), which `scheduled-runs.md` sets.
- Persisting the event ring.

## 7. Candidate requirements (provisional, not ratified)

- **SR-A (DESIGN):** a finished session leaves the default `get sessions` view
  after its kind's retention, and stays visible under `--shelved` and in
  `describe session`.
- **SR-B (DESIGN):** shelving never deletes a row and never changes
  replica-slot, firing-slot, budget or readiness decisions.
- **SR-C (DESIGN):** a crashed or failed row of any kind is reported by
  doctor, before and after it is shelved.
- **SR-D (DESIGN):** a headless exit 78 row, and a row whose kill failed, is
  never shelved; nor is a row with no end time.
- **SR-E (DESIGN):** no row of a hibernated or waking team is shelved, and
  retention after a wake counts from the stored wake time.
- **SR-F (DESIGN):** a scheduled role's latest run is never shelved.
