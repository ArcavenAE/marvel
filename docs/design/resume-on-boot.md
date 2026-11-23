# Resume on boot: teams that were running come back after a host reboot

- **Status:** design, rulings made 2026-11-23 (section 5). Nothing here is
  built; the build goes to a marvel builder, red tests first.
- **Author:** the arcaven team's architect seat.
- **Checked at:** marvel `origin/main` a7e5c202. Every line cite below is at
  that commit.
- **Builds on:** `_kos/findings/finding-034-convergence-posture-hold-at-start-line.md`
  (the hold at the start line), `fleet-hibernate.md`, `fleet-suspend-resume.md`
  (ruling SR-C), and the open question `question-convergence-posture`
  ("how do hold, the go-line, and resume-on-power compose").

## 0. Why

After a host reboot, marvel's daemon comes back with every team held and no
seats, and stays that way until someone runs `marvel converge`. That hold is
deliberate: it stops a stale store from starting a fleet nobody asked for. But
it also means a host that rebooted overnight is idle until a person notices,
even for teams that were running a minute before the reboot.

This note adds a setting, on by default with a 7-day window (ruled, section
5), that brings back the teams that were running before the reboot, and only
those. A team or a cluster can turn it off. A team held on purpose,
a hibernated team, a suspended cluster, or a store too old to trust stays
held. Seats come back as fresh casts through the normal spawn gate. How a seat
recovers its own conversation is a separate problem (`fleet-hibernate.md`
section 5, the resume journal) and is out of scope.

## 1. What exists today

### 1.1 The start path overwrites the posture before anything reads it

- A team's `ConvergencePosture` is `hold` or `converge`
  (`internal/api/types.go:1370-1383`). It is status, not spec (`toml:"-"`), and
  JSON-persisted with the team (`types.go:1360-1369`).
- At daemon start, after adoption, `RefreshLiveness` reaps rows whose panes did
  not survive (`internal/daemon/daemon.go:839-847`,
  `internal/team/controller.go:686-695`). Then `InitConvergencePosture`
  (`controller.go:715-735`) sets every team with no live session to `hold` and
  every team with one to `converge`. Its comment: "It OVERRIDES whatever
  posture the store rehydrated, so a stale bolt that persisted PostureConverge
  cannot cold-spawn a fleet nobody asked for" (`controller.go:704-706`).
- `postureWithholds` (`controller.go:1105-1110`) then blocks cold spawns for a
  held team with no live session.
- The comment names this work: "tu2e (resume-on-power) will let an operator opt
  a team out of the hold default" (`controller.go:712-713`).

### 1.2 The posture records no source and no time

Four sites write it, and none records who or when:

| writer | sets | cite |
|---|---|---|
| `marvel apply` | `converge` | `daemon.go:1744-1757` |
| `marvel converge` and `converge --hold` | either | `daemon.go:2125-2138`, `controller.go:745-775` |
| `marvel wake` | `converge` | `internal/team/hibernate.go:523` |
| daemon start | derived from live panes | `controller.go:728` |

So a team the operator held and a team the last cold start held both read
`hold`, and a team that was running reads `converge` in bolt only until the
start path overwrites it.

### 1.3 What else a start can read

- **Session heartbeats are in bolt, for roles that beat.** `UpdateSessionHeartbeat` sets
  `LastHeartbeat` and writes the row (`internal/api/store.go:1080`, then
  `persistPut` on `bucketSessions`), and only the heartbeat call reaches it
  (`daemon.go:2210`). A role that does not beat, such as a headless run,
  keeps a zero `LastHeartbeat`. Every row also persists its `CreatedAt`
  (`types.go:385`), and a scheduled role's run history, with each run's
  `StartedAt` and `EndedAt`, persists in the bbolt bucket `schedule_status`
  (`internal/api/bolt.go:61`, `internal/api/schedule_status.go:25-45`).
- **Scheduled firings stop while a team holds.** `schedule_clock.go:62` and
  `cadence_clock.go:27` both read `postureWithholds`, so a held team's
  schedule does not fire.
- **The daemon has no persisted heartbeat of its own.** No field records when
  the daemon was last alive (searched `internal` and `cmd` for a daemon tick,
  alive or shutdown stamp; none found). The staleness bound below needs one.
- **The suspend record is in bolt** (`internal/api/suspend_record.go:10-60`,
  bucket `suspend`), so a cluster suspended at the reboot is still marked
  suspended after it.
- **The hibernate mark is on the team** (`internal/api/hibernate.go:14-18`).
  `SetConvergencePosture` refuses a team only when one team is named and its
  mark reads `hibernated` (`controller.go:752-762`), not while it is
  `waking`.

### 1.4 The boot reap charges every lost seat as a crash

The start-time reap runs `noteReapedCrash` for each row whose pane is gone
(`controller.go:677-681`, `:831-900`):

- a role with `restart_policy = never` is frozen and its row set `failed`
  ("restart_policy=never, pane gone; role frozen", `controller.go:858-880`);
- any other role is charged one restart per tick and given a backoff
  (`controller.go:882-895`).

A scheduled role is exempt (`controller.go:854-856`). Today this charge is
mostly unseen, because the team then holds. With resume on boot it would
matter: a reboot would come back with frozen roles and backoff delays, as if
every seat had crashed.

## 2. The design

### 2.1 Record where each posture came from

The team gains three status fields:

- `PostureSource`: `operator` (apply, converge, hold, wake), `start` (the
  start-time derivation) or `boot` (this setting);
- `PostureSetAt`: when it was set;
- `PriorPosture`: the posture, source and time that a `start` derivation
  overwrote, kept until a non-`start` writer sets the posture again.

`PriorPosture` is what lets a reboot followed by a second restart keep the
fact that the team was running: the second start reads the carried posture,
not the `hold` the first start wrote.

### 2.2 Decide before the boot reap, from a snapshot

The order matters, because the boot reap charges before the posture is set.
`RefreshLiveness` runs first (`daemon.go:843`), and its reap calls
`noteReapedCrash` for each lost row (`controller.go:677-681`), which freezes
or charges at once (section 1.4). Only then does `InitConvergencePosture` run
(`daemon.go:844`). A decision taken after it would find the charges already
made. So the decision comes first:

1. After adoption and before `RefreshLiveness`, daemon start reads a
   snapshot: each team's persisted posture, source, prior posture and
   hibernate mark, the suspend record, the daemon's last alive stamp, each
   team's latest presence (condition 6), and which of the team's rows still
   have a pane after adoption.
2. The decision runs on the snapshot. A team is **boot-eligible** when
   conditions 1 to 6 of section 2.3 hold and none of its rows' panes survived
   adoption, so the reap will leave it cold.
3. `RefreshLiveness` runs with the set of boot-eligible teams, and its reap
   skips the charge and the freeze for their rows (section 2.4). It still
   removes the dead rows, as today. The skip is scoped to this one call.
   `reapDeadLocked` is shared: `ReconcileOnce` calls it on every tick
   (`controller.go:607`) and `RefreshLiveness` at start (`:694`). So the set
   is an argument, never controller state: `reapDeadLocked(skip)` takes it,
   `RefreshLiveness` passes the boot-eligible set, and `ReconcileOnce` passes
   none. Nothing is stored, so from the first reconcile tick on, a crash in a
   resumed team is charged exactly as today.
4. `InitConvergencePosture` is unchanged: every cold team still goes to `hold`
   first.
5. Each boot-eligible team is then set to `converge` with source `boot`,
   before the reconcile loop starts.

A daemon restart that finds every pane of a team gone, with no host reboot (a
killed tmux server, for one), is treated the same way: the seats were lost
together, not crashed one by one.

### 2.3 Which teams converge on boot

A cold team (no live session after the reap) converges on boot only when all
of these hold:

1. **The setting is on** for at least one of the team's roles (section 2.5).
2. **The cluster is not suspended**: no suspend record exists.
3. **The team is not hibernated**: no hibernate mark, in either state.
4. **It was running before**: the effective posture is `converge`. The
   effective posture is the persisted one, or `PriorPosture` when the persisted
   source is `start`. A `hold` set by `operator` stays held.
5. **The store is fresh**: the daemon's last alive stamp is within the bound
   of now (default 7 days).
6. **The team was live near the end**: its latest presence is within the
   bound of the daemon's last alive stamp. A team's latest presence is the
   latest of:
   - any row's `LastHeartbeat`;
   - any row's `CreatedAt`;
   - for a scheduled role, its latest run's `StartedAt` or `EndedAt` from the
     run history;
   - the daemon's last alive stamp itself, for any row the snapshot finds
     `running` with a pane recorded. The reconcile loop reaps such a row on
     the tick its pane is gone (`controller.go:607`), so a row the store
     still holds as `running` had a pane at the last tick before the daemon
     stopped, within a tick of the alive stamp. Only `running` counts. The
     reap skips a row with no pane (`internal/session/manager.go:2462-2465`),
     so the claim does not hold for the other states that `CountsAsAlive`. A
     `crashloop-backoff` row is set after its crash was already reaped
     (`controller.go:2111-2115`), and a `pending` row may not have a pane
     yet. Neither proves the role was up at the stamp, so both count only
     by their `LastHeartbeat` and `CreatedAt`.

   A role that never beats is present by its spawns, its runs and its live
   rows. This covers a scheduled headless role and an unscheduled, non-beating
   role that was spawned long before the reboot and was still running at it.
   A role whose only row is a finished run (a headless `succeeded` or exit 78
   row) has nothing to resume: it holds its slot and is not relaunched
   (ADR-010). A team of only such rows that fails this condition loses
   nothing by staying held. One gap is left and named. Condition 6 is
   team-wide, so the gap is a team, not a role: a team whose only roles are
   scheduled with a period longer than the bound (a monthly job under a
   7-day bound), with no `running` row at the reboot, fails this condition.
   The team stays held, and the log line names condition 6. A monthly role in
   a team that has any other recent presence is resumed with its team. Once a session end time exists
   (`session-shelving.md` proposes `EndedAt`), it joins this list.

The daemon then sets the team's posture to `converge` with source `boot`, and
emits a `team.resumed-on-boot` event. A team that fails a condition stays
held, and the daemon logs one line per team naming the first condition that
failed, for example `resume on boot: team ws/b held: operator hold since
2026-11-20T09:00Z`.

Conditions 5 and 6 carry the money-safety guarantee of the cold-start hold.
A bolt rehydrated from weeks ago fails condition 5. A team that was declared
but had no live seat for days before the reboot fails condition 6. Neither
can start a fleet.

### 2.4 Seats come back as fresh casts, uncharged

- A resumed team spawns through the normal reconcile path, so admission,
  budgets, bus health and every other spawn check apply unchanged. A refused
  spawn is refused as it is today.
- For a boot-eligible team, the boot reap charges nothing and freezes
  nothing, because the decision ran before the reap (section 2.2). A seat
  lost to a reboot is not a crash of that seat. Teams that are not
  boot-eligible keep today's reap accounting, so the change reaches no team
  the decision holds. Nothing is charged and then undone.
- The daemon's alive stamp is written at most once a minute from the
  reconcile loop, and once more on a clean shutdown, in a small record of its
  own. One write a minute bounds the cost.

### 2.5 The setting: on by default; a cluster, a team or a role opts out

- Cluster config: `resume_on_boot` (default `true`) and
  `resume_on_boot_within` (default `7d`), beside the cluster's other switches
  in `internal/config/config.go` (`type Cluster struct`, `:295`). A cluster
  that sets `resume_on_boot: false` keeps today's start path for every team
  and role on it, whatever they set.
- **Precedence, the one rule.** A cluster set off is a hard lock: no team or
  role setting lifts it, because turning a whole host off is a deliberate
  host-wide act, often for maintenance. On a cluster that is on, the most
  specific level wins: a role's own value over its team's, and a level that
  sets nothing reads the next one out. A team set off is a default its roles
  may override. Today's start path is kept exactly for a team when its
  cluster is off, or when the effective setting of every one of its roles is
  off. (Director ruling, 2026-11-23, recorded as S5 in section 5.)
- A team opts out in its manifest settings with `resume_on_boot = false`, or
  sets `true` to state the default for its roles. Under a cluster that is off,
  neither has any effect. This is spec, so it belongs in the manifest, unlike
  the posture, which stays status.
- **A role may override its team** with `resume_on_boot` in its own settings
  (a new field on the role, `internal/api/types.go`, `type Role struct`,
  `:823`). The team's value is the default for each of its roles, and a role
  that sets nothing inherits it. On a cluster that is on, a role's effective
  setting is its own value, else its team's, else the cluster's; on a cluster
  that is off, it is off.
- **How a role override acts.** The posture is per team, so a role is held
  back by a list on the team, not by a posture of its own. When the decision
  resumes a team, it also records `BootWithheld`, a new status field on the
  team: the roles whose effective setting is off. A role on that list is
  withheld from cold spawn and from firing, the way `postureWithholds`
  (`controller.go:1105-1110`) withholds a held team, while the rest of the
  team converges. The next posture write by an `operator` (apply, converge or
  wake) clears the list, so a withheld role starts on the next explicit
  command, as it does today. A team whose roles are all off fails condition 1
  and stays held.
- **Worked example: a scheduled role.** A team is on by default and holds two
  standing seats and a nightly scheduled role that sends a report. The
  nightly role sets `resume_on_boot = false`, because its owner wants a
  person to look before it fires after an unattended restart. After a reboot,
  the two standing seats come back as fresh casts; the nightly role is on
  `BootWithheld` and does not fire, and `describe team` says so, until the
  operator runs `marvel converge`. The reverse works the same way, on a
  cluster that is on: under a team set off, a one-shot or scheduled role that
  sets `true` makes the team
  boot-eligible, and only that role spawns or fires.
- The boot reap's skip (section 2.4) covers every row of a boot-eligible
  team, withheld roles included: their seats were lost to the reboot, not
  crashed, and a `restart_policy = never` role on the list is not frozen.
- The default is on, so a host needs no config change to get it.
- `describe team` shows the posture, its source and set-at time, and whether
  resume on boot applies, so a held team says why it is held.

### 2.6 How this fits "nothing resumes except an explicit command"

`fleet-suspend-resume.md` rules SR-C: "nothing resumes a suspended cluster
except an explicit `resume`", and `fleet-hibernate.md` section 11 inherits it
as "Nothing resumes except an explicit command". This setting keeps both:

- it never acts on a suspended cluster (condition 2) or a hibernated team
  (condition 3), so `resume` and `wake` stay the only ways out of those states;
- the setting is the operator's explicit, standing instruction: the operator
  ruled that a standing setting counts as the explicit resume (section 5), and
  a team or a cluster can turn it off.

## 3. Red tests, as flat build tickets

In order; `after:` names what lands first.

- **RB1. Posture source and set-at time.** `internal/api/types.go` (the team),
  and each writer in section 1.2. after: none. Red, in
  `internal/team/posture_test.go` beside `TestSetConvergencePostureFlipsAndReports`
  (`:145`): after `SetConvergencePosture(hold)`, `apply`, `wake` and
  `InitConvergencePosture`, the team has no source and no set-at time. Green:
  `operator`, `operator`, `operator` and `start`, each with its time.
- **RB2. The carried prior posture.** `controller.go`
  (`InitConvergencePosture`). after: RB1. Red: a team persisted at `converge`
  by `operator`, started cold twice, reads `hold` from `start` with nothing
  carried. Green: after both starts `PriorPosture` is `converge` from
  `operator` at its original time; a later `converge --hold` clears it.
- **RB3. The daemon alive stamp.** `internal/api` (a one-key record, like the
  suspend record) and the daemon's reconcile loop. after: none. Red: after a
  reconcile tick and after a clean shutdown, the store has no alive stamp.
  Green: it has one, and two ticks inside a minute write it once.
- **RB4. The setting.** `internal/config/config.go`, the manifest team spec
  and the role spec. after: none. Red: an unset cluster reads off, a team with
  `resume_on_boot = false` under a cluster set on reads on, and a role with
  `resume_on_boot = false` under a team set on reads on. Green: an unset key
  reads on with a 7-day window; each set value reads as written; a role that
  sets nothing reads its team's value, and a team that sets nothing reads the
  cluster's; and under a cluster set off, a role or team set on reads off.
- **RB5. The decision, as a pure function.** `internal/team`, for example
  `resumeOnBoot(snap bootSnapshot, cfg ResumeConfig, now time.Time) (bool, string)`.
  after: RB1, RB2, RB3, RB4. Red: a table test with the cases below returns
  nothing, since the function does not exist. Green, each with its reason:
  - running before the reboot, setting on, store fresh: converge;
  - the same with the setting off: hold;
  - an `operator` hold: hold;
  - a hibernated team: hold;
  - a suspend record present: hold;
  - the alive stamp 30 days old: hold;
  - the team's latest presence 10 days before the alive stamp: hold;
  - a team of scheduled headless roles with zero heartbeats, whose latest
    run ended an hour before the alive stamp: converge;
  - a team with a row whose pane survived adoption: not boot-eligible;
  - an unscheduled, non-beating role created 30 days before the reboot, its
    row still `running` in the store, with a fresh alive stamp: converge;
  - the same role with its row in `crashloop-backoff`, created 30 days
    before the reboot and with no heartbeat: hold, condition 6;
  - a role set on under a cluster set off and a team that sets nothing:
    hold, the cluster lock;
  - a role set on under a team set off, on a cluster that is on: converge
    for that role;
  - the team opted out in its manifest: hold;
  - every role of the team opted out: hold, condition 1;
  - one role opted out under a team set on: converge, with that role on
    `BootWithheld`;
  - one role set on under a team set off: converge, with every other role on
    `BootWithheld`;
  - a `start` hold carrying a prior `operator` converge: converge.
- **RB6. Wire it into daemon start, before the reap.** `internal/daemon/daemon.go:839-847`:
  the snapshot and the decision run before `RefreshLiveness`, and the
  `converge` from `boot` is set after `InitConvergencePosture`. after: RB5. Red, in `internal/team/posture_test.go` beside
  `TestInitConvergencePostureFromLivePresence` (`:106`) and
  `TestRefreshLivenessReapsDeadPaneRecordsBeforePosture` (`:278`): a team
  persisted at `converge`, with stale running rows, a fresh alive stamp and the
  setting on, is still held after start and spawns nothing. Green: it reads
  `converge` from `boot`, one `team.resumed-on-boot` event is emitted, and
  its spawns go through the normal reconcile path. A second case: a budget
  that refuses the spawn leaves the team at `converge` with nothing spawned and
  the refusal reported, as a cold `converge` is today.
- **RB7. The boot reap charges nothing for a resumed team.** `controller.go`
  (`RefreshLiveness` and `reapDeadLocked`, `:670-695`, and `noteReapedCrash`,
  `:831-900`); `reapDeadLocked` takes the skip set as an argument. after:
  RB6. Red: a resumed team with a `restart_policy = never` role comes back
  with that role frozen, and another role's first spawn waits out a backoff.
  Green: neither role is charged or frozen at any point, no `failed` row or
  reap charge event is written for it, and a team that is not boot-eligible is
  charged exactly as today. A second case: after the resume, a seat of the
  resumed team whose pane dies is reaped by `ReconcileOnce` and charged
  exactly as today.
- **RB9. A role on `BootWithheld` is withheld until an operator command.**
  `internal/api/types.go` (the team's `BootWithheld`), `controller.go`
  (`postureWithholds` and its callers in `schedule_clock.go:62` and
  `cadence_clock.go:27`). after: RB4, RB6. Red: in a resumed team whose
  nightly scheduled role sets `resume_on_boot = false`, the nightly role
  fires at its next due time. Green: it does not fire and spawns nothing
  while the team's standing roles spawn; after `marvel converge` the list is
  empty and the role fires at its next due time.
- **RB8. `describe team` shows it.** `cmd/marvel` (describe team). after: RB1,
  RB4, RB9. Red: describe shows the posture with no source, time or resume line.
  Green: it shows all three, a held team's line names the condition that
  holds it, and a resumed team lists its `BootWithheld` roles.

## 4. Diagnostic and safety notes

- The setting adds no gate. Every spawn it allows passes the same checks a
  `marvel converge` spawn passes.
- With the effective setting off for every role of a team, the team gets
  today's start path exactly: RB6 and RB7 act only for a team the decision
  resumes, and the reap skip applies only to the boot call (section 2.2).
- What to measure on a host after the build, at a window the operator names:
  the pre-reboot running teams read `converge` from `boot` with fresh seats; a
  team held before the reboot is still held; a suspended cluster stays
  suspended.

## 5. Ruled 2026-11-23

The operator ruled on this note on 2026-11-23, relayed by director. The
operator's words on these items, verbatim: "As for all the reset, yes, you're
right update them as you describe and route".

- **S1. A standing setting counts as the explicit resume** SR-C asks for.
  Ruled: yes (section 2.6).
- **S2. The bound.** Ruled: 7 days, for both the store's freshness and the
  team's last presence. The setting is on by default.
- **S3. The boot reap's charge.** Ruled: a resumed team pays no reap charge
  (RB7); other teams keep today's accounting.
- **S4. A per-team setting.** Ruled: each team can opt out in its manifest
  settings. Ruled further by the operator, 2026-11-23: any role may override
  its team's setting in its own settings, and the team's setting is the
  default for its roles (section 2.5).
- **S5. A cluster set off.** Ruled by director, 2026-11-23, applying the
  operator's rulings above: a cluster set off is a hard lock that no team or
  role setting lifts, because turning a whole host off is a deliberate
  host-wide act, often for maintenance. Below the cluster, the most specific
  level wins (section 2.5).

## 6. Out of scope

- Starting the daemon at boot (a launch agent marvel installs) and the bus at
  boot: they come before this setting can act, and are separate work.
- Restoring a seat's conversation: the resume journal of `fleet-hibernate.md`
  section 5.
- Any change to hibernate, wake, suspend or resume.

## 7. Candidate requirements (provisional, not ratified)

- **RB-A (DESIGN):** resume on boot is on by default with a 7-day window. A
  cluster set off is a hard lock, and every team on it gets exactly today's
  daemon start. On a cluster that is on, a role's effective setting is its own
  value, else its team's, else the cluster's, and a team whose roles all have
  the setting off gets exactly today's daemon start.
- **RB-F (DESIGN):** a role's own `resume_on_boot` overrides its team's, and a
  role whose effective setting is off neither spawns nor fires after a boot
  resume until the next operator posture command.
- **RB-B (DESIGN):** a team converges on boot only if it was running before the
  reboot, was not held by an operator, is not hibernated, the cluster is not
  suspended, and both staleness bounds hold.
- **RB-C (DESIGN):** every posture change records its source and time.
- **RB-D (DESIGN):** a resumed team's seats are fresh casts through the normal
  spawn checks, and the reboot charges none of them as a crash.
- **RB-E (DESIGN):** a held team's `describe` says which condition holds it.
