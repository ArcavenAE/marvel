# Account failover: a written policy that moves a team to another account, and back

Design only. Arcaven architect seat, 2026-11-18. Status: draft for the operator. Nothing here is built, and no ticket is filed.

Amended 2026-11-19: section 2.1 says where each switch sits and who may flip it, section 2.2 says what alerting only means, section 4.1 says how dry-run proposals surface, section 5.2 uses hibernate and wake in place of a new replica hold, and section 1 has three new rows and one corrected line reference. Sections 10 and 12 follow.

## Why

When an account nears its seven-day limit today, a person reads a statusline and moves the team by hand. The operator wants marvel to do that move from a policy the user writes, with failback that is automatic, an alert, or nothing. This design gives that policy a shape in marvel's manifest, says when it may act and how it acts without losing work, and names the two pieces marvel must build first, because without them the policy would act on numbers it cannot trust.

The operator, verbatim: "I think this should be a configurable option for marvel to automatically switch, and it should be flexible so that failover between accounts can be written as policy in some way, with automatic failover and also policy definiton on failback automated or alerting or none".

What is ruled: the operator's own written policy is the ratification SOUL section 8 asks for. Failover may act automatically when a user's policy says so. It is opt-in, and with no policy, nothing changes.

The thresholds come from a study of team moves between an Ultra account and an Enterprise login (2026-11-18: a replay of twelve days of hourly spend on one account, checked against the first real readings), and from a move runbook written beside it. Both are kept outside this repository. The study's stage 1 and stage 2 become this policy's `dry-run` and `enabled` modes.

Guidance for leaving `dry-run`, not a rule: the study set a bar of at least three proposed moves, carried out by hand, with no lost mail and no team held over 3 hours, before acting without a person. `marvel get failovers` could show a count of clean dry-run proposals to help a user judge it.

## 1. What marvel has today, checked on origin/main cf01437f

| fact | how checked | what it means here |
|---|---|---|
| An account reading arrives only from a running seat: claude's statusline, codex's rollout log | `cmd/marvel/accountlimits.go`; `internal/runtime/codex/ratelimits.go` | An account with no seat has no fresh reading. |
| The newest reading per account is held in memory and is stale after 15 minutes | `internal/api/account.go`, `ReadingMaxAge`, `ReadingFresh/Stale/None` | The three states the policy reads already exist. |
| A time series of readings is kept, in memory, for 8 days, behind the `account.series` and `account.delta` RPCs (#1380) | `internal/api/account_series.go`; `internal/daemon/daemon.go` case list | Half of MR1 is built. Not on disk, gone on a daemon restart, and no CLI verb reads it. |
| The account key is (harness, backend, credential source, config home) | `AccountKeyOf` | A policy account can be resolved to the same key the readings use. |
| Which login a claude role runs on is one env value, `CLAUDE_CONFIG_DIR` | `ClaudeConfigDirEnv`; a diff of a live team's manifest against its stand-by mirror's | The two manifests differ in three things: the team name, that env key on every role, and replicas 0 on two roles. The account is one key, not a second team. |
| `marvel scale` writes the stored team, so a reconcile keeps it; a later `marvel apply` of the manifest resets it | `handleScale`, `internal/daemon/daemon.go` (line 2140 at cf01437f; line 1936 at bfccaca2, re-checked 2026-11-19) | A team scaled down by failover comes back up on the next apply unless something holds it. |
| Scaling a role down retires its seats: an interactive seat is asked for its handoff and its pane is killed when the handoff lands or when its role's handoff window ends; a headless seat is not asked. Re-checked 2026-11-19 at bfccaca2 | `internal/team/retire.go`; `docs/design/work-prune.md` section 6 | A scale to 0 is not an immediate stop, and a seat that misses its window loses its context. A move must not use it to stop a team. |
| A hibernated team spawns nothing until `wake`, whatever reconcile, converge, scale or apply would do. In handoff mode every seat writes its handoff first, and a seat with no handoff is reported and left running, never ended. Re-checked 2026-11-19 at bfccaca2 | `docs/design/fleet-hibernate.md` sections 4 and 6, HB-B; `MethodTeamHibernate` | The hold section 5.2 needs already exists, and it is safer than a scale to 0. |
| `team.hibernate` and `team.wake` are local only: the daemon's own socket, never the `mrvl://` tunnel, until the remote scope is ruled. Re-checked 2026-11-19 at bfccaca2 | `internal/daemon/methods.go`, `LocalOnly` on both | A person on another host cannot hibernate a team by hand. A policy's executor runs inside the daemon, so the limit does not bind it. |
| A rolling shift asks each role for its handoff and waits for the marker; a missing marker stops the shift and emits `team.shift-handoff-missing` | `marvel shift --help`; event kinds `team.shift-*` | The safe cut point already exists. |
| The declared pause of `usage-limit-pause.md` section 4 is not built | `marvel get pauses`: `unknown resource type: pauses` | Failover cannot lean on a pause record. |
| The manifest key `policies` is taken: it projects harness settings | `ManifestPolicy`, `internal/api/manifest.go:64` | The new section needs its own name. |
| No failover or failback work exists in marvel | grep of docs, internal, cmd, `_kos`; issue search | Nothing to supersede. |

## 2. The policy, as manifest resources

Two new top-level manifest sections, beside `teams`: `accounts`, which names logins, and `failovers`, which says when a team moves between them. Both are structural data: `marvel apply` validates them, and nothing acts on them until a policy is enabled.

```yaml
accounts:
- name: ultra-a
  harness: claude
  env:
    CLAUDE_CONFIG_DIR: /path/to/ultra-login
- name: enterprise
  harness: claude
  env: {}            # the host's ambient login
  metered: false     # sends no seven-day reading; may be a target, never a trigger

failovers:
- name: alpha-off-ultra
  enabled: true
  mode: dry-run      # dry-run | enabled
  scope:
    team: alpha      # one team; roles: [supervisor, builder] narrows it
  accounts: [ultra-a, enterprise]   # ordered: primary first
  action: relogin    # relogin | swap-team
  trigger:
    window: seven_day
    at_or_above: 90
    reset_more_than: 12h
    fresh_within: 15m
  failback:
    mode: auto       # auto | alert | none
    when: reset      # reset | below
    below: 50        # only with when: below
  cooldown: 12h
  max_moves_per_window: 1
```

| field | meaning |
|---|---|
| `accounts[].name` | A word the policy and the views use. Unique in the cluster. |
| `accounts[].harness` | Which harness the login is for. A policy may only list accounts of the harness its roles run. |
| `accounts[].env` | The env values that put a role on this login, applied over the role's own at spawn. For claude, `CLAUDE_CONFIG_DIR` or nothing. marvel names a path; it holds no credential (ADR-009). |
| `accounts[].metered` | Default true: the account sends readings, and a missing one blocks action. False: it sends none (Enterprise today), so it may be a target but never a trigger. |
| `enabled` | Per-policy switch. False keeps the policy on file and inert. |
| `mode` | `dry-run`: evaluate and report what would happen, change nothing. `enabled`: act. A new policy should start in `dry-run`. |
| `scope` | A team, optionally narrowed to roles. One team per policy, so two policies can never move the same role. Apply refuses a second policy whose scope overlaps. |
| `accounts` (in the policy) | Ordered list, primary first. Failover goes to the next eligible account after the current one; failback goes to the primary. A pair is a list of two. |
| `action` | How the move is made; section 3. |
| `trigger` | Failover fires when a reading of the current account that is fresh by `fresh_within` shows `window` at or above `at_or_above` and its reset is more than `reset_more_than` away. |
| `failback` | Section 6. |
| `cooldown` | Minimum time between any two moves of this scope. |
| `max_moves_per_window` | Moves away from the primary allowed per primary window, counted from the reset recorded at the move. |

Validation (structural, so it may gate `apply`): every listed account exists and matches the scope's harness; the first account is metered; `failback.below` is lower than `trigger.at_or_above` (hysteresis cannot be zero); `cooldown` is at least 1h; `fresh_within` is at most `ReadingMaxAge`; scopes do not overlap; `swap-team` names its second team (section 3). An account list is only checked for shape: whether a path is a real login is not something marvel can see without reading the credential.

The defaults are the study's rule: 90%, reset more than 12h away, back at the recorded reset. The replay found that rule held a team 3 hours or less in every run with the fewest moves, and that a "reset more than two days away" guard left teams held up to 66 hours.

### 2.1 Turning failover and failback on and off

A user turns automatic moves on and off in the manifest, and can stop them for a while on the host without editing it. The rule that ties them together: **a switch on the host can only make a policy less active, never more.** Turning a policy on, or moving it from `dry-run` to `enabled`, is always a manifest change through `apply`, so the written policy stays the ratification.

| switch | where it lives | what it does | how it changes |
|---|---|---|---|
| no `failovers` entry for the team | manifest | Nothing runs for the team. | `apply` |
| `enabled: false` | the policy, in the manifest | The policy stays on file. It is not evaluated, and `get failovers` shows it `disabled`. | `apply` |
| `mode: dry-run` | the policy | Both directions are proposals only. Nothing moves, whatever `failback.mode` says (section 2.2). | `apply` |
| `mode: enabled` | the policy | Failover acts on its own. Failback does what `failback.mode` says. | `apply` |
| `failback.mode` | the policy | Failback alone: `auto`, `alert` or `none` (section 6). | `apply` |
| `marvel failover hold <policy>` and `marvel failover release <policy>` | the daemon's store, kept across `apply` and restart | While held, the policy is evaluated and shown, but it starts no move in either direction. A move already running finishes the step it is on (one role's shift, or one hibernate) and stops there, with the record marked held, not faulted. | a person on the host |
| `marvel failover hold --account <name>` and `release --account <name>` | the daemon's store, kept across `apply` and restart | No policy moves a scope onto that account. Moves off it, failback included, go ahead. | a person on the host |

**Per team.** One policy per scope (section 2, validation), so a team's switch is its policy's switches.

**Per account pair.** A policy's ordered `accounts` list is its pair, so `enabled`, `mode` and `failback.mode` already apply to one team on one pair. The account hold is the cluster-wide switch: it takes one login out of every policy as a target, for example while that account has a fault or its owner wants it left alone.

**Failover and failback apart.** `mode` governs whether anything moves; `failback.mode` only narrows what failback does. The useful combinations:

| `mode` | `failback.mode` | result |
|---|---|---|
| `dry-run` | any | Alerting only, both directions (section 2.2). |
| `enabled` | `auto` | Fully automatic. |
| `enabled` | `alert` | marvel moves away on its own; a person brings the team back with `marvel failover back`. |
| `enabled` | `none` | marvel moves away; it never proposes the way back. |

A hold is shown in `get failovers` with who set it and when, and emits `failover.held` or `failover.released`. `apply` never clears a hold; only `release` does.

**Reach.** `apply` is served over `mrvl://` as today, so a user on another host turns a policy off by applying `enabled: false`. `hold` and `release` are local only, like `team.hibernate`, and follow whatever the operator rules for hibernate's remote scope (`fleet-hibernate.md` section 10).

### 2.2 Alerting only

Alerting only is `mode: dry-run`. A policy in this mode is evaluated as if enabled, and every decision it would act on is reported instead.

**Through what.** marvel's own surfaces, and nothing else: the `failover.proposed` and `failback.proposed` events in the daemon's ring (`marvel events`, `marvel events --follow`), the policy's status in `marvel get failovers`, and the proposal log of section 4.1. marvel sends nothing out of its process: no mail, no webhook, no bus message (section 9).

**Who is told.** Whoever follows those surfaces. In a fleet with a coordinator such as director, the coordinator follows the events and routes a proposal to the supervisor of the team in scope. Without one, a person reads `marvel get failovers`, or a script follows `marvel events`. marvel does not choose the person.

**How often.** One event when a decision enters `propose`, not one per evaluation. While the decision stays at `propose`, the event is sent again once per `cooldown` as a reminder. When it leaves `propose` without a move, marvel emits `failover.proposal-ended` with the new decision as its reason.

**Acting on an alert.** A person who agrees with a proposal runs `marvel failover move <policy>` (or `marvel failover back <policy>` for failback). That carries out the proposed move once, through the same executor, checks and records as an enabled policy. It is refused unless the policy's current decision is that proposal, and refused while a hold is set. A move carried out this way is counted for the guidance on leaving `dry-run` (the "Why" section).

**What never happens under alerting only:** no shift, scale, hibernate or wake; no account override; no change to any seat's env; no handoff asked of any seat; no manifest edit; no move record (only proposal log lines); and no proposal from a stale or missing reading of a metered account.

## 3. Two ways to move: `relogin` and `swap-team`

**`relogin` (recommended).** The team stays the same team. marvel records an account override for the scope, then shifts each role with its handoff. The successor seat spawns with the next account's `env` over its own. Seat names keep their team and role; only the generation moves. Mail named for the seat still reaches it; the successor resumes from its predecessor as every shift does; posting identity comes from the role's launch, so it does not change.

**`swap-team`.** Two teams, as the runbook runs them: the primary team is wound down and scaled to 0, a mirror team is scaled up. It is kept for a mirror that must differ in more than its login (another host, another runtime, another cast). The policy names the mirror: `action: swap-team` with `mirror: alpha-ent`, and per-account `env_on_move` overlays (section 7).

Why `relogin` is the default: the three problems the runbook needs extra work for come from there being two teams. Mail named for the ultra seat is dropped by the mirror unless a stand-in seat answers mail named for the seat it replaces; a returning seat replays mail the mirror already handled unless it starts at the stream head; and the mirror posts under another identity. In `relogin` there is one line of seats, so none of the three arises. It also needs no stand-by seats idling on the other account.

What `relogin` does not remove: the config directory carries the harness's settings, plugins and MCP servers as well as the login. A role moved to another `CLAUDE_CONFIG_DIR` runs with that directory's settings. The same is true of a mirror team, so it is not a reason to prefer `swap-team`, but it is a check for the user: keep the two directories' settings aligned.

## 4. When a policy may act

The daemon evaluates every enabled policy on a timer (default each minute) and on each new reading of an account a policy lists. Each evaluation yields one decision with its reason, kept as the policy's status and shown by `marvel get failovers`:

| decision | when |
|---|---|
| `hold: no-reading` | The current account is metered and its reading is `none`. |
| `hold: stale` | Its reading is `stale`, or older than `fresh_within`. Never act; the last number and age are shown. |
| `hold: below` | Fresh, below `at_or_above`. |
| `hold: near-reset` | Fresh, at or above the bar, reset within `reset_more_than`: ride it out. |
| `hold: cooldown` | The last move was less than `cooldown` ago, or `max_moves_per_window` is used. |
| `hold: no-target` | No later account is eligible: a metered target needs a fresh reading below `failback.below` (or below 80% when failback is not `below`); an unmetered target is eligible unless its last move ended in a fault. |
| `hold: fault` | A fault state holds (below). |
| `hold: busy` | A shift, a move or an apply is in progress in the scope. |
| `propose: failover` / `propose: failback` | All checks pass. In `dry-run` this is the end: the event is emitted and nothing moves. In `enabled`, the move starts. |

Fault states, checked before and after every move: both teams live (`swap-team`); neither live; a role in scope with a seat whose account does not match the override (`relogin`); a move left part done. A fault never resolves itself: it is shown, an event is emitted, and the policy holds until `marvel failover clear <policy>` after a person has looked.

Never: act on a stale or missing reading of a metered account; act while the policy is `dry-run` or disabled; start a second move while one is in progress; pick a target the user did not list.

A hold (section 2.1) is checked before every other row: a held policy shows `hold: held` with who set it, and an account hold makes that account ineligible, so a scope whose only target is held shows `hold: no-target`.

### 4.1 How dry-run proposals surface

- **`marvel get failovers`**: one line per policy, with `POLICY`, `SCOPE`, `MODE` (`disabled`, `dry-run` or `enabled`), `HOLD`, `ON` (the account the scope runs on now), `DECISION`, `READING` (percent and age), `PROPOSED` (since when, or `-`) and `MOVES` (moves carried out by hand under `dry-run` that completed with no fault, the count the guidance in "Why" asks for).
- **`marvel describe failover <policy>`**: the last 20 decisions, each with its reason, the reading it used and the time.
- **The proposal log**: `failover/<policy>/proposals.jsonl` under the marvel home, one line each time a decision enters or leaves `propose`, appended and never edited, and read back at start. It keeps the count and the history across a daemon restart, which the in-memory status does not.
- **Events**: section 2.2 gives when they fire.

## 5. Carrying out a move safely

### 5.1 `relogin`

1. **Before record.** marvel writes `failover/<policy>/<UTC time>-before.json` under its home: the decision and its reason, the reading it acted on (account, window, percent, reset, observed time, reporting session), the scope's roles and seats, and the reset to fail back at.
2. **Override.** It stores the override (scope, from account, to account, reset recorded, policy, record path) in the daemon's persisted store, not in the manifest. A later `apply` of the team's manifest does not clear it.
3. **Shift with handoff.** It shifts each role in scope, one role at a time, through the existing shift path, with its handoff. The successor spawns with the override's `env`. A role that declares no handoff is not shifted by a policy: apply refuses an `enabled` `relogin` policy whose scope holds such a role, unless the policy sets `allow_no_handoff: true` for it.
4. **Stop on a missing handoff.** If the marker does not arrive in the window (`team.shift-handoff-missing`), marvel stops the move there. The roles already moved stay moved, the rest stay where they were, and the policy enters the fault state "part done" with the record pointing at both sets. It never retries with `--no-handoff`.
5. **After record.** `...-after.json`: the same fields, the seats now running, and each role's account as resolved from its new session.

### 5.2 `swap-team`

The executor uses the hibernate and wake that marvel already has, not a new replica hold and not a scale to 0. A hibernated team cannot be respawned by `apply`, `scale`, `converge` or reconcile, so the hazard in section 1 is closed by a built mechanism. And hibernate never ends a seat that has not written its handoff, where a scale to 0 retires a seat when its window ends, with or without one.

1. Before record, as above, covering both teams.
2. Hibernate the primary in handoff mode, through the daemon's own call. Workers sign off first and supervisors last (`fleet-hibernate.md` section 4). The hibernated mark is set at the first request, so nothing respawns the primary from then on.
3. Wait until no seat of the primary runs. A seat that hibernate reports without a handoff (`team.shift-handoff-missing`) stops the move as in 5.1, step 4: the primary stays hibernated with that seat still running, the mirror is not started, and the policy enters the fault state "part done". The executor never runs `marvel hibernate --end`.
4. Start the mirror: `wake` it if it is hibernated, or scale it to its manifest replicas if it is at 0, with the account's `env_on_move` overlay applied at spawn (section 7).
5. After record.

Failback runs the same steps the other way: hibernate the mirror, then `wake` the primary, which starts its supervisors before its workers (`fleet-hibernate.md` section 6).

Both teams must be served by the daemon that runs the policy; `apply` refuses a `mirror` that is not a team of the same cluster. A policy never moves a team across clusters. Hibernate refuses a team that is mid-shift, so the policy's `hold: busy` check comes first. `marvel failover clear` clears the fault only; it does not wake or scale either team, and a person does that by hand. If the operator renames hibernate and wake (`fleet-hibernate.md` section 10), this section follows the new names.

### 5.3 What every move emits

Events in the daemon's ring, so `marvel events` and anything that follows it see them: `failover.proposed`, `failover.proposal-ended`, `failover.started`, `failover.role-moved`, `failover.completed`, `failover.stopped` (with the reason and the record path), `failover.fault`, `failover.held`, `failover.released`, and the same for `failback.*` where they apply. A record file is written once and never edited after the move.

## 6. Failback

| `failback.mode` | what happens |
|---|---|
| `auto` | marvel moves the scope back to the primary, through the same steps as 5.1 or 5.2, when `when` is met and the scope is at a cut point (the shift's handoff is the cut point). `when: reset` means the reset recorded at failover has passed. `when: below` means a fresh reading of the primary is below `below`; with no seat on the primary, that needs MP1. |
| `alert` | When `when` is met, marvel emits `failback.proposed` once, shows it in `marvel get failovers`, and does nothing. A person or another tool acts, with `marvel failover back <policy>`. |
| `none` | marvel never proposes failback. The scope stays where it is until a person runs `marvel failover back <policy>` or `marvel failover clear <policy>`. |

`cooldown` and the fault checks apply to failback as to failover. `when: reset` is safe on a missing reading, because the recorded reset does not move; it is the default for that reason. A failback never starts inside `cooldown` of the failover, so a reading taken just after a move cannot bounce the team back.

## 7. Director: the stand-in seat, the returning seat, and the no-director case

marvel knows nothing of director. Where a move needs a seat to be told something at spawn, the policy carries it as an env overlay, `env_on_move`, set per account in the policy and applied only to seats spawned by the move:

```yaml
  action: swap-team
  mirror: alpha-ent
  env_on_move:
    enterprise: { DIRECTOR_STANDS_IN_FOR: alpha }   # stand-in seat
    ultra-a: { DIRECTOR_RETURN_FROM: alpha-ent }    # returning seat
```

- **Stand-in seat** (a mirror seat answers mail named for the ultra seat it stands in for): set on the mirror's seats at failover, cleared by the failback, which spawns without it.
- **Returning seat** (a returning seat starts at the stream head and reads the mirror's handoff): set on the primary's seats at failback only.
- **`relogin` needs neither**, for the reasons in section 3.
- **No director:** the overlay is absent or names variables nothing reads. marvel still moves the seats, writes the records, and emits the events. Mail continuity across a `swap-team` is then not marvel's to give, and the docs say so; `relogin` keeps it without director.

The env overlay is set by the policy the user wrote, never by a seat, which matches the runbook's rule that both values are set only by the operator's or director's manifest edits.

## 8. What marvel needs first

**MR1, readings on disk and a read verb.** Write each `account.limits` reading to an append-only file under the marvel home, reload the series and the newest reading at start, and add `marvel get account-readings [--account] [--since]`. The in-memory series of #1380 is the structure; MR1 adds the file, the reload and the verb. Without it, a daemon restart turns every reading to `none`, the policy holds `no-reading`, and the history the thresholds should be checked against does not exist.

**MP1, an account probe with no seats.** A scheduled role on a metered account that starts one short session so a reading arrives when no seat runs. It is what makes `failback.when: below` usable, and what lets a metered target be judged eligible. First measure whether a headless claude run sends the statusline at all (not measured; the runbook names it). If it does not, the probe needs a short interactive session.

**The rule on a missing or stale reading: never act.** It holds in every mode and every direction; section 4 lists it as `hold: no-reading` and `hold: stale`. The one move that does not need a reading is `failback.when: reset`, because it acts on a recorded time, not on a reading.

## 9. Independence check

- **marvel alone:** the policy, the evaluation, the moves, the records and the events are all marvel's. No bus, no director, no switchboard. `marvel get failovers` and `marvel events` are the alert surface.
- **Any console:** the trigger needs a reading, and today only claude and codex send one. A policy whose primary is a harness with no reading producer fails validation with that reason, rather than holding `no-reading` forever. Unmetered accounts work as targets on any harness that marvel can point at a login by env.
- **No director:** section 7. `relogin` loses nothing; `swap-team` loses mail continuity, which is said in the docs.
- **No policy:** nothing in this design runs. The evaluator only reads `failovers` entries that are present and enabled.

## 10. Tickets, flat, in build order, red first (not filed)

Each lands in its own PR; each check is a test written red before the change.

| id | ticket | depends on | red test it turns green |
|---|---|---|---|
| FO1 | MR1: account readings on disk, reloaded at start, `marvel get account-readings` | none | Report a reading, restart the daemon: `get account-readings --since 1h` shows it and `get budgets` shows it `stale`, not `none`. |
| FO2 | MP1: measure whether headless claude sends a statusline; then a scheduled probe role example | FO1 | With no other seat on the account, a fresh reading appears within one schedule period. |
| FO3 | Manifest sections `accounts` and `failovers`: parse, validate, store; no behavior | none | Each validation rule in section 2 refuses its bad case; a good policy round-trips through `apply` and `get failovers` shows it `disabled` or `dry-run`. |
| FO4 | Evaluator in `dry-run`: decisions and reasons, `get failovers` and `describe failover`, the proposal log, and `failover.proposed` and `failover.proposal-ended` sent once per entry into and out of `propose` (sections 2.2 and 4.1) | FO1, FO3 | Fixture readings give each row of section 4's table; a stale 92% gives `hold: stale`, never `propose`; ten evaluations at `propose` give one event; the proposal log survives a daemon restart. |
| FO5 | Account override record and spawn-time env, persisted, kept across `apply`; `marvel failover clear`; `marvel failover hold` and `release`, per policy and per account, local only (section 2.1) | FO3 | A role with an override spawns with the account's env; `apply` of the manifest does not clear it; `clear` does. A held policy shows `hold: held`, and `apply` does not release it. |
| FO6 | `relogin` executor in `enabled` mode: records, role-by-role handoff shift, stop on a missing marker | FO4, FO5 | A fake seat that never writes its marker leaves the move `part done` with both role sets in the record, and no `--no-handoff` shift is issued. |
| FO7 | Failback `auto`, `alert`, `none`; `marvel failover back`; and `marvel failover move`, which carries out a current proposal once by hand (section 2.2) | FO6 | With `when: reset`, a recorded reset in the past and the scope at a cut point gives a failback under `auto`, one `failback.proposed` under `alert`, and nothing under `none`. `move` on a `dry-run` policy whose decision is not `propose` is refused. |
| FO8 | `swap-team` executor through hibernate and wake (section 5.2), and `env_on_move` | FO6 | `apply` of the primary's manifest during a move leaves it hibernated with no seat; a fake seat that never writes its handoff stops the move as `part done` with the mirror not started; mirror seats spawned by the move carry the overlay and later seats do not. |

FO1 and FO3 have no edges between them and can be built side by side.

## 11. Rulings needed

1. **`relogin` as the default action, `swap-team` kept for mirrors that differ in more than the login.** Recommended: yes, for the reasons in section 3. Valid until 2026-12-02; the arcaven architect re-checks it then, or when FO6 lands, whichever comes first.
2. **Where a policy that marvel enables sits next to the runbook's "only director moves a team".** Recommended: for a scope under an `enabled` policy, marvel's move replaces runbook steps 1 to 9 and director is told by the events; elsewhere the runbook stands. Same expiry.
3. **Whether an Enterprise account is ever metered.** Recommended: model it as `metered: false` until a reading source exists. Not measured: whether Enterprise has a limit that applies (the study, section 9).

## 12. Not measured

- Whether a headless claude run sends a statusline (FO2 measures it first).
- Real seven-day readings over a whole week: none are on disk (FO1 fixes this).
- Whether a role's settings differ between two config directories on a fleet host, which `relogin` would expose.
- The real cost of a role-by-role handoff shift for a whole team; the 40 minutes per move in the study's replay is from one run, on 2026-09-19.
- How long a handoff-mode hibernate of a whole team takes, and whether its report of a seat with no handoff can be read by an executor in the daemon, not only by a person at the CLI. One team was hibernated in handoff mode by hand on 2026-11-19 and reached 0 seats; its timing was not measured.
