# Scheduled runs: a headless role on a clock

Design for review. No code lands until this doc is reviewed.

- Author: arcaven-architect-g5-0.
- Commission: operator request via director, relayed by arcaven-supervisor-g5-0
  (msg 01M3REAWWN, 2026-09-30): assess, then design, scheduled work in marvel.
- Answers, in part: `question-scheduled-cues` (marvel frontier).
- Checked against marvel `origin/main` 3cb6101.

## 0. The recommendation in five lines

1. A schedule is a block on a **headless role**, not a new resource. A firing
   advances the role's current firing, and the reconciler that already
   exists spawns the run. ADR-010 completion semantics end it.
2. Overlap defaults to **forbid**. A missed firing runs **at most once** on
   recovery, inside a starting deadline, never as a burst.
3. Every firing passes the same admission check as `apply`, `scale` and
   `run`, under a new trigger. Each run is capped by a wall-clock deadline and
   metered by the headless stream marvel already reads.
4. marvel keeps a bounded run history and reports on its own event ring, plus a
   **freshness alarm** when a schedule has not succeeded within its bound. It
   sends to the director bus only through the planned marvel events stream,
   never into an agent inbox.
5. A run's authority is its role's projected policy, fixed in the manifest. It
   gets nothing from what it reads, and no scheduled role may use
   `dangerous_permissions`.

The first user is the daily beadle board refresh. Today it runs as a session
timer inside a seat's Claude Code session, which expires after seven days and
does not survive a respawn.

## 1. Assessment: what exists

### 1.1 marvel today

| What | Where | State |
|---|---|---|
| Schedule (CronJob) | `CLAUDE.md` "Model-only, not implemented"; `_kos/nodes/bedrock/elem-k8s-resource-model.yaml` row "CronJob / Schedule / Timed agent tasks" | model-only: no type, code or CLI |
| The open question | `_kos/nodes/frontier/question-scheduled-cues.yaml` | frontier. Asks whether marvel owns the clock or only a freshness alarm, what a cue delivers to an interactive seat, and where "last ran" lives |
| Headless completion (ADR-010) | orc `decisions/adr-010-headless-completion-semantics.md`; marvel #244 (`OccupiesReplicaSlot`, `internal/api/budget.go:252`), #246 (writes `succeeded` from the tmux exit status) | **built**. A headless run that exits cleanly holds its replica slot and is not refilled |
| Headless launch | `internal/runtime/claude.go:94-125` | built: `--print --output-format stream-json --verbose`, `--permission-mode` from the role |
| Stream metering | `internal/usage` | built for headless: tokens per run are observable |
| Admission | `internal/admission/admission.go:89-93` | built: triggers `apply`, `scale`, `run`, `shift`, `reconcile`; team budgets refuse over-budget growth |
| One-off run | `marvel run`, `internal/daemon/daemon.go:1765` | built, but it takes a raw command and sets no headless mode, so it does not get ADR-010 completion |
| Reconcile loop | `internal/daemon/daemon.go:52`, `ReconcileInterval = 2s` | built |
| Per-session lifetime | `_kos/ideas/max-session-age.md` | idea; names the scheduled cue as its fleet-wide counterpart |
| A `scheduler` service class | `_kos/ideas/services-catalog.md` line 48 | idea: "a scheduler, class `scheduler` (new), managed, not on record" |
| Services and workloads | `docs/design/services-list.md`, `bus-as-service.md`; `workload.Process` (aae-orc-oo62t, in progress) | built for the bus; the process model for a second managed service is in flight |

### 1.2 Around marvel

| What | Where | Bearing |
|---|---|---|
| Resource model | orc ADR-003, amended by ADR-010 | a new role field or resource is a resource-model change, so it is ratified, not emergent (ADR-007) |
| Vision | orc `vision.md` line 410: "Scheduling by what actually constrains agents ... context pressure, token spend" | spend, not CPU, is the budget a schedule must respect |
| Gap 1 | orc `vision.md` line 458, operator attention routing | a scheduled run that ends in a GATE is attention demand; the run should report, not interrupt |
| Director timers | aae-orc-7vw44 (open): shim `set_timer` | a cue into a live seat is director's, on the bus |
| Bus scheduling | aae-orc-aubd6 (open): EVENTS_MARVEL with `allow_msg_schedules` | the planned bus path for marvel's events |
| Quota pause | marvel finding-056 (on marvel#408, not yet merged) | the seats that ran into the account limit were the ones with scheduled tasks; it proposes shedding scheduled work first |
| Usage-limit reading | marvel finding-050 | the future source of a `limited` condition |
| Pane inject is not dispatch | finding-040; an interactive seat takes no first prompt, finding-055 | a schedule cannot target an interactive seat through the pane |

bd, searched for schedule, cron, periodic, timer, recurring, headless and
service (titles, `bd sql`):

- **Closed:** 1p8o and bxeh (headless completion); vsju (terminal-state
  probe); qiay (token and quota scheduling probe); lvzck (shim identity and
  timer heartbeat); lzug and otbb (beadle dashboard-refresh policy and run 14);
  4t1k (scheduled packaging in sideshow-packs); fxfkw (actor stamping at cron
  spawn points); 1yzn8 (Services list).
- **In progress:** oo62t (workload.Process).
- **Open:** 7vw44 (shim timers); aubd6 (EVENTS_MARVEL); 0u4tq (marvel as
  service provider); k28s (service packaging); ety14 (dashboard-refresh builds
  before ingest); v0ymv (a recurring warn-only orc check); 7mz (multi-host
  scheduling).
- **None found:** a ticket for scheduled runs in marvel.

### 1.3 Related art, and what fits marvel's resource matrix

| Art | Worth taking | Not worth taking |
|---|---|---|
| k8s CronJob | `concurrencyPolicy` (Allow, Forbid, Replace); `startingDeadlineSeconds`; `successfulJobsHistoryLimit` 3 and `failedJobsHistoryLimit` 1; `suspend`; `timeZone`; a Job template that is ordinary Job spec | a separate Job object per firing: marvel's session already is the run record |
| systemd timers | `Persistent=true` (run once on boot if a firing was missed); `RandomizedDelaySec` (spread a fleet's starts) | calendar-expression syntax beyond cron |
| launchd | `StartCalendarInterval` coalesces missed firings into one on wake | per-user plist placement |
| GitHub Actions `schedule` | an honest statement that a scheduled run can be delayed or dropped under load, and that it runs from the default branch's definition | its best-effort delivery as a design target |

What k8s never had to price, and marvel does: a firing spends **tokens on a
shared account**, carries **authority** (it acts in someone's name), and ends
in **a report someone must read**. That is why sections 4, 6 and 7 exist.
CPU placement is not a concern at this scale.

## 2. Where a schedule lives: a block on a headless role

```toml
  [[team.role]]
  name = "board-refresh"
  replicas = 1
  permissions = "acceptEdits"
  policy = "board-refresh"

    [team.role.runtime]
    command = "claude"
    mode = "headless"
    prompt = "/dashboard-refresh"

    [team.role.schedule]
    cron = "17 6 * * *"          # five-field cron
    timezone = "Etc/UTC"         # required, no default; an IANA zone or a fixed offset like "-06:00"
    # dst_ack = true             # required only for a zone that observes DST (section 2a)
    concurrency = "forbid"       # forbid | replace | allow
    starting_deadline = "2h"     # a missed firing older than this is skipped
    active_deadline = "45m"      # a run older than this is killed, and the run fails
    jitter = "5m"                # random delay per firing
    stale_after = "30h"          # the freshness alarm (section 7)
    retries = 0                  # retries inside one firing; default 0
    on_failure = "wait"          # wait | freeze, after retries are used up
    history = { succeeded = 3, failed = 3 }
    suspend = false
```

**Why a role block and not a resource.** The role already is the job template:
runtime, prompt, permissions, policy, budget scope, replicas as parallelism.
ADR-010 already made a headless role a Job. A schedule is the one field that
turns a Job into a CronJob. A new resource would duplicate the template, add a
store bucket, a CLI verb set and a reference to keep valid, for no field the
role cannot hold.

**The cost, stated.** ADR-010 warned that mode-dependent semantics on `Role`
may prove awkward, and that the successor is a distinct resource kind. A
schedule adds one more mode dependency. Validation keeps it narrow: `schedule`
is refused on a role that is not `mode = "headless"`. **Promotion trigger:**
if a second schedule-only field lands that does not fit a role (for example,
one schedule firing several roles), promote to a `Schedule` resource that
references roles, and amend ADR-010 then.

**How a firing becomes a run.** A new per-role store record holds the current
firing id and its due time (the role spec at `internal/api/types.go:440` stays
spec; this is status beside it, like `RoleHealth`). Each session of a
scheduled role records the `Firing` it was spawned for.

For a scheduled role, slot occupancy is **firing-scoped**: a session occupies
a slot only if its `Firing` equals the role's current firing, whatever its
state. Today `OccupiesReplicaSlot` (`internal/api/budget.go:252`) counts every
live session plus headless `succeeded`, so a previous firing's run that is
still live would hold the slot and `allow` could never start a second run,
and a failed run holds nothing and is refilled inside the same firing. The
scheduled rule replaces both:

- a live session of the current firing occupies (the run is in progress);
- a `succeeded` session of the current firing occupies (the firing is done);
- a failed or crashed session occupies nothing (operator ruling, section 11);
  whether the firing tries again is decided by the firing record, below, not
  by slot counting;
- no session of an earlier firing occupies, live or not. It still counts as a
  process everywhere else (`CountsAsAlive` is unchanged: budgets and posture
  see two runs when two run).

**The firing record settles failures.** The per-role firing record carries
`attempts` and `settled`. A run that exits non-zero or crashes increments
`attempts` and emits `run.failed`, and the run history records it. While
`attempts <= retries` and the firing is inside `starting_deadline`, the
reconciler spawns a retry after a backoff, through admission like any spawn.
Once retries are used up, the firing is `settled` as failed and the
reconciler spawns nothing more for the role until the next firing. Then
`on_failure` applies: `wait` (the default) does nothing further, and the next
firing runs as declared; `freeze` suspends the schedule until an operator
clears it with `marvel reset-health <ws/team> --role <r>`.

**A run that ends under a usage limit is not a failure** (#419 section 4a,
settled jointly with this one). A run that ends while its account is
`limited` is recorded with outcome `limited` and emits `run.limited`. It does
not increment `attempts`, spends no retry, and never triggers `on_failure =
"freeze"`. The firing waits: no retry spawns while the account is limited or
the scope is paused, and after the clear #419's catch-up rule decides whether
the firing still runs inside `starting_deadline`.

The reap path does not charge a scheduled role's crash to its restart
policy: `noteReapedCrash` and `applyRestartPolicy` skip scheduled roles, so
`max_restarts` and `restart_policy` never turn a failure into a freeze
(`restart_policy = never` freezes on the first crash today,
`internal/team/controller.go:638`). A schedule freezes only when
`on_failure = "freeze"` says so.

So:

1. The scheduler advances the firing (a new id, stamped at due time plus
   jitter).
2. Last firing's succeeded sessions stop occupying slots.
3. The reconciler sees `desired > actual` and spawns, through the same path,
   admission and launch as any role.
4. The run exits. ADR-010 writes `succeeded`, the slot is satisfied until the
   next firing.

The scheduler writes one field; it never spawns. The reconciler stays the only
thing that creates sessions.

**The clock.** A ticker in the daemon, one per minute, beside the reconcile
loop, not inside it. The next-due time per role is computed from `cron` and
`timezone` and kept in the store, so a daemon restart recomputes it rather than
forgetting it.

### 2a. The timezone and daylight saving (RULED, section 11)

`timezone` is required and has no default. It takes an IANA zone name
(`Etc/UTC`, `America/Chicago`) or a fixed UTC offset (`+00:00`, `-06:00`).

**The problem.** A cron in a zone that observes daylight saving is a local
clock time, and local clock time moves against UTC twice a year. The beadle
board timer hit this: a local-time schedule had to be re-armed at each change
(operator report, 2026-09-30). Around a change a local-time cron also has two
edge cases: a time that does not exist (the spring-forward gap, for example
02:30 on the change night) and a time that happens twice (the fall-back
overlap). A fixed offset has neither.

**Detection.** At apply, and again whenever the daemon computes a next-due
time, marvel checks whether the zone observes daylight saving: it loads the
zone and compares its UTC offsets across the next twelve months. A fixed
offset and `Etc/UTC` never do.

**The trap.** A schedule whose zone observes daylight saving is refused at
apply unless it carries `dst_ack = true`. The refusal names the zone and offers
the fixed alternatives, computed for this schedule:

```
schedule for team/role: timezone America/Chicago observes daylight saving;
its firings move by an hour against UTC twice a year.
  fixed alternative: timezone = "-06:00" (keeps 06:17 in winter, 07:17 CDT in summer)
  or UTC:            timezone = "Etc/UTC", cron = "17 12 * * *"
  or keep DST:       add dst_ack = true
```

The offset form is offered rather than an `Etc/GMT+N` name, because those
names invert the sign (`Etc/GMT+6` is UTC-6), which is its own trap.

**With `dst_ack = true`.** The schedule is accepted and runs in local time.
marvel emits `schedule.dst-acknowledged` once at apply. At each change it
emits `schedule.dst-shift` with the old and new UTC offset and what the change
did to the next firing. The edge cases are fixed, not left to a library
default:

- a firing time inside the spring-forward gap fires once, at the first minute
  after the gap;
- a firing time inside the fall-back overlap fires once, at its first
  occurrence.

**At runtime without the ack.** If a zone begins to observe daylight saving
after apply (a tzdata update), marvel does not stop a running schedule. It
emits `schedule.dst-unacknowledged` (warning) once per next-due computation
that finds it, and keeps firing, and `describe team` shows the warning until
the manifest adds `dst_ack` or changes the zone.

**Tests.**

1. Apply with `timezone` missing: refused, naming the field.
2. `America/Chicago` without `dst_ack`: refused; the message offers `-06:00`
   and the UTC cron; `schedule.dst-unacknowledged` is not emitted (apply
   refusals are errors, not events).
3. The same with `dst_ack = true`: accepted; `schedule.dst-acknowledged` once.
4. `Etc/UTC`, `+00:00` and `-06:00` with no ack: accepted, no DST events.
5. A fake clock across the spring change with cron `30 2 * * *`: exactly one
   firing that night, at 03:00 local; `schedule.dst-shift` emitted.
6. A fake clock across the fall change with cron `30 1 * * *`: exactly one
   firing, at the first 01:30.
7. A daemon restart between two changes recomputes the same next-due time.
8. A zone whose tzdata gains DST after apply (a test zone): firings continue,
   `schedule.dst-unacknowledged` is emitted, and `describe team` shows it.

## 3. Missed runs and overlap

| Case | Behavior | Event |
|---|---|---|
| Firing due, previous run still live, `forbid` | not advanced; recorded | `schedule.skipped` reason `overlap` |
| same, `replace` | previous run killed, new firing advanced | `schedule.replaced` |
| same, `allow` | advanced; both run (possible because an earlier firing's run no longer occupies a slot, section 2) | `schedule.fired` |
| Daemon was down, one or more firings due, newest within `starting_deadline` | **one** firing on recovery (launchd coalescing, systemd `Persistent`) | `schedule.fired` with `catch_up: true`, `missed: N` |
| same, newest older than `starting_deadline` | none | `schedule.missed` with `missed: N` |
| `suspend = true` | none | `schedule.suspended` once per change |
| Run passes `active_deadline` | killed; `failed` | `run.deadline` |
| Run exits non-zero or crashes | `attempts` + 1; retried after backoff while `attempts <= retries` and inside `starting_deadline`; else the firing settles failed and `on_failure` applies (default `wait`: next firing runs as declared) | `run.failed`; `schedule.frozen` only under `freeze` |
| Run ends while its account is `limited` (#419) | not a failure: `attempts` unchanged, no retry and no freeze; the firing waits for the clear, then #419's catch-up rule applies | `run.limited` |

There is no backlog and no burst: a recovery produces at most one run. That is
the property that matters for a shared account.

## 4. Spend and admission

- **Every firing is admitted.** It goes through the check `apply`, `scale` and
  `run` already use, under a new trigger, `TriggerSchedule`. A refused firing
  is recorded `schedule.skipped` reason `budget`, and it is not retried until
  the next firing.
- **Every run is bounded.** `active_deadline` is required, since a run with no
  wall-clock bound is how a scheduled job spends overnight. A turn or token
  cap, where a harness offers one, is a later addition; this design does not
  assume one exists.
- **Every run is metered.** Headless runs feed `internal/usage`, so the run
  record carries its token total with the accountant's own flags (metered,
  partial, suspect). No new meter.
- **Usage limit and pauses (finding-056).** Scheduled seats were the ones that
  ran into the account limit. When marvel gains a `limited` reading
  (finding-050) or a declared pause (finding-056's proposal 3), a firing during
  either is skipped with reason `limited` or `paused`, and the recovery rule in
  section 3 applies at the end, so the pause produces at most one catch-up run.
  Until those readings exist, `active_deadline` is the only bound: a run that
  meets the limit waits inside the harness and is killed at its deadline.
  Scheduled work is the first thing to shed, by design.

## 5. Where results go

- **The work product goes where the prompt puts it.** The board refresh posts
  its own issue body. marvel does not collect, copy or forward it.
- **marvel keeps a run record** per session: firing id, due and started times,
  catch-up flag, exit status, duration, token total, and the stream's final
  result message, truncated to 4 KiB. It lives in the local store, bounded by
  `history`. `marvel describe team` (which exists; `describe` takes session,
  team, workspace, endpoint and credential today) gains a schedule block per
  scheduled role: the next due time and the history.
- **What is never forwarded:** the result text. Reports carry pointers and
  status only (section 6).

## 6. How a run reports

- **The event ring, always.** `schedule.fired`, `schedule.skipped`,
  `schedule.missed`, `schedule.replaced`, `run.succeeded`, `run.failed`,
  `run.deadline`, `schedule.stale`. Filterable by workspace, team and role like
  every other kind.
- **The bus, through marvel's own stream, when it exists.** aae-orc-aubd6
  provisions EVENTS_MARVEL. Once it lands, the same kinds are mirrored there,
  and director or a supervisor subscribes (7vw44 `subscribe_events`). marvel
  does not publish into an agent inbox. It holds no agent address (R-94), and
  a control plane writing mail in someone's name is the wrong shape.
- **Optional `report_to`** on the schedule block names who should subscribe.
  It is documentation until EVENTS_MARVEL exists, and a subscription filter
  after.

## 7. The freshness alarm (question-scheduled-cues, first piece)

`stale_after` is required. When a schedule has no `succeeded` run newer than
`stale_after`, marvel emits `schedule.stale` once per transition, and again
when it recovers. It is an event, never a block (diagnostic-not-gate). The
"last succeeded" time lives in the store with the history, so it survives a
daemon restart.

This alone would have surfaced the 19-hour gap the frontier node records, and
the 57-hour envoy stall seen on 2026-09-28. It ships first (section 10).

## 8. Authority and the classifier

A scheduled run acts with no human watching. Its authority has to be fixed
before it starts.

- **Authority comes from the manifest, not the run.** The prompt, the
  permission mode and the projected policy are role fields, applied by the
  operator with `marvel work`. A run gets nothing from what it reads: issue
  text, PR bodies and fetched pages are data (the upstream-claim and ae
  quarantine rules).
- **Outward writes are named.** The role's policy allowlist names the exact
  commands a run may use to write outside the host (for the board refresh:
  one `gh issue edit` on one repository, and a commit to its own fixture
  path). Anything else is denied by the harness.
- **No bypass.** Validation refuses `schedule` on a role with
  `dangerous_permissions`, or with a permission mode that skips checks. A
  scheduled run that needs a check skipped is a design problem to escalate,
  not a flag to set.
- **A refusal is recorded, and nothing retries around it.** A denied action
  does not by itself fail a run: a headless run whose tool call was denied
  usually carries on and exits 0. The run record keeps any permission
  denials the stream reports; whether claude's `--print` stream-json final
  result lists them is not yet verified and is checked on a captured run in
  S-2. Until then a denial is visible only in the result text, and the run
  reads `succeeded`. Either way, the next firing runs as declared, and
  nothing rewords or retries the denied action (no-control-bypass).
- **Identity.** The run acts under whatever identity the role's environment
  carries: a bot account for a public post, the operator's own credentials
  otherwise (SOUL section 3). The schedule adds no credential and holds none.
- **The report is not a GATE.** A run that finds something needing a human
  decision records it in its result and ends. It does not wait for an
  approval that nobody is watching for (vision Gap 1).

## 9. Out of scope

- **Cues into interactive seats.** A schedule starts a headless run; it does
  not wake a live seat. A cue into a seat is a message, and it belongs to
  director (7vw44 `set_timer`, or NATS scheduled messages on the bus).
- **Multi-host placement** (aae-orc-7mz).
- **A `scheduler` service class** (services-catalog idea). The daemon already
  has a clock, a store and a reconciler; a separate service would add a
  process and a credential path for nothing this design needs. Reopen it if
  scheduling must survive the daemon being down.

## 10. Edits, in order (none made by this PR)

| # | Edit | Depends on |
|---|---|---|
| S-1 | `schedule` block parsed and validated (headless only, required fields, no bypass modes); the timezone rules and DST trap in section 2a; refused otherwise | the ADR-010 amendment merged (section 11) |
| S-2 | Run record, history and the `describe team` schedule block; `schedule.stale` from `stale_after` | S-1 |
| S-3 | The clock: next-due in the store, firing advance, `OccupiesReplicaSlot` firing rule, overlap and recovery policy | S-1 |
| S-4 | `TriggerSchedule` in admission; `active_deadline` kill | S-3 |
| S-5 | Move the beadle board refresh onto a scheduled role; retire the session timer | S-4, and the refresh's own permission policy |
| S-6 | Mirror the kinds to EVENTS_MARVEL | aae-orc-aubd6 |
| S-7 | Skip on `limited` or `paused` | finding-050 reading, finding-056 proposal 3 |

S-2 before S-3 is deliberate: the alarm is useful on its own and costs least.

## 11. Rulings (all made)

All four rulings are made. The operator's words for the first three, via
director and arcaven-supervisor (msg 01M3S7M3MP, 2026-09-30), verbatim: "(a)
amend ADR-010 (b) option A (c) required with no default, include the DST issue
in docs and trap/detect DST-based timezones in runtime and offer alternative
fixed time but allow user to ack and use DST anyways".

- **The resource-model change: RULED, amend ADR-010.** The amendment is drafted
  in the orc's `decisions/adr-010-headless-completion-semantics.md` in its own
  orc PR, and S-1 depends on it merging.
- **Interim for the board refresh: RULED, Option A.** Renew the session timer
  by hand on a calendar reminder until S-5, and accept the risk until then.
  (Option B, a host timer outside marvel, is not taken.)
- **Timezone: RULED, required with no default,** with daylight-saving
  detection, a refusal that offers a fixed alternative, and `dst_ack = true`
  to keep a DST zone. Section 2a.
- **A failed run: RULED 2026-09-30** (operator via director, relayed by
  arcaven-supervisor-g5-0 msg 01M3RHVMT1), verbatim: "they should do any of
  those, a setting, but default retry is 0 so it just tries again next time
  unless other options are set". Recorded in section 2 and the schedule
  block:
  - `retries` (default 0): tries inside one firing.
  - `on_failure` (default `wait`): after retries, `wait` for the next firing,
    or `freeze` until `marvel reset-health`.
  - A failed run is recorded as failed (`run.failed` plus the run history),
    holds no slot, and never freezes the role unless `on_failure = "freeze"`.
    `max_restarts` and `restart_policy` do not apply to scheduled roles.
