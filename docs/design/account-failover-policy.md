# Account failover: a written policy that moves a team to another account, and back

Design only. Arcaven architect seat, 2026-11-18, ask I-565 (director 01M7R3TK5J, to the arcaven supervisor). Status: draft for the operator. Nothing here is built, and no ticket is filed.

## Why

When an account nears its seven-day limit today, a person reads a statusline and moves the team by hand. The operator wants marvel to do that move from a policy the user writes, with failback that is automatic, an alert, or nothing. This design gives that policy a shape in marvel's manifest, says when it may act and how it acts without losing work, and names the two pieces marvel must build first, because without them the policy would act on numbers it cannot trust.

The operator, verbatim: "I think this should be a configurable option for marvel to automatically switch, and it should be flexible so that failover between accounts can be written as policy in some way, with automatic failover and also policy definiton on failback automated or alerting or none".

What that rules (director, relayed by the supervisor): a user's own written failover policy is the ratification SOUL section 8 asks for, so marvel may act automatically where the policy says so. It is opt-in. With no policy, nothing changes.

The thresholds come from a study of team moves between an Ultra account and an Enterprise login (ask I-563, 2026-11-18: a replay of twelve days of hourly spend on one account, checked against the first real readings), and from the move runbook written beside it (ask I-562). Both are kept outside this repository. The study's stage 1 and stage 2 become this policy's `dry-run` and `enabled` modes.

## 1. What marvel has today, checked on origin/main cf01437f

| fact | how checked | what it means here |
|---|---|---|
| An account reading arrives only from a running seat: claude's statusline, codex's rollout log | `cmd/marvel/accountlimits.go`; `internal/runtime/codex/ratelimits.go` | An account with no seat has no fresh reading. |
| The newest reading per account is held in memory and is stale after 15 minutes | `internal/api/account.go`, `ReadingMaxAge`, `ReadingFresh/Stale/None` | The three states the policy reads already exist. |
| A time series of readings is kept, in memory, for 8 days, behind the `account.series` and `account.delta` RPCs (#1380) | `internal/api/account_series.go`; `internal/daemon/daemon.go` case list | Half of MR1 is built. Not on disk, gone on a daemon restart, and no CLI verb reads it. |
| The account key is (harness, backend, credential source, config home) | `AccountKeyOf` | A policy account can be resolved to the same key the readings use. |
| Which login a claude role runs on is one env value, `CLAUDE_CONFIG_DIR` | `ClaudeConfigDirEnv`; a diff of a live team's manifest against its stand-by mirror's | The two manifests differ in three things: the team name, that env key on every role, and replicas 0 on two roles. The account is one key, not a second team. |
| `marvel scale` writes the stored team, so a reconcile keeps it; a later `marvel apply` of the manifest resets it | `handleScale`, `internal/daemon/daemon.go:2146-2200` | A team scaled down by failover comes back up on the next apply unless something holds it. |
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

## 3. Two ways to move: `relogin` and `swap-team`

**`relogin` (recommended).** The team stays the same team. marvel records an account override for the scope, then shifts each role with its handoff. The successor seat spawns with the next account's `env` over its own. Seat names keep their team and role; only the generation moves. Mail named for the seat still reaches it; the successor resumes from its predecessor as every shift does; posting identity comes from the role's launch, so it does not change.

**`swap-team`.** Two teams, as the runbook runs them: the primary team is wound down and scaled to 0, a mirror team is scaled up. It is kept for a mirror that must differ in more than its login (another host, another runtime, another cast). The policy names the mirror: `action: swap-team` with `mirror: alpha-ent`, and per-account `env_on_move` overlays (section 7).

Why `relogin` is the default: the three problems the runbook needs extra work for come from there being two teams. Mail named for the ultra seat is dropped by the mirror (director ticket A), a returning seat replays mail the mirror already handled (director ticket B), and the mirror posts under another identity (the study's ticket ID1). In `relogin` there is one line of seats, so none of the three arises. It also needs no stand-by seats idling on the other account.

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

## 5. Carrying out a move safely

### 5.1 `relogin`

1. **Before record.** marvel writes `failover/<policy>/<UTC time>-before.json` under its home: the decision and its reason, the reading it acted on (account, window, percent, reset, observed time, reporting session), the scope's roles and seats, and the reset to fail back at.
2. **Override.** It stores the override (scope, from account, to account, reset recorded, policy, record path) in the daemon's persisted store, not in the manifest. A later `apply` of the team's manifest does not clear it.
3. **Shift with handoff.** It shifts each role in scope, one role at a time, through the existing shift path, with its handoff. The successor spawns with the override's `env`. A role that declares no handoff is not shifted by a policy: apply refuses an `enabled` `relogin` policy whose scope holds such a role, unless the policy sets `allow_no_handoff: true` for it.
4. **Stop on a missing handoff.** If the marker does not arrive in the window (`team.shift-handoff-missing`), marvel stops the move there. The roles already moved stay moved, the rest stay where they were, and the policy enters the fault state "part done" with the record pointing at both sets. It never retries with `--no-handoff`.
5. **After record.** `...-after.json`: the same fields, the seats now running, and each role's account as resolved from its new session.

### 5.2 `swap-team`

1. Before record, as above, covering both teams.
2. Hold: the override record marks the primary team held at 0 and the mirror at its manifest replicas. While it holds, `apply` of either team's manifest keeps the held replicas and says so, rather than restoring them; `marvel failover clear` is the way out. This closes the hazard in section 1 (apply restores a scaled-down team).
3. Wind down the primary: each role with a handoff is asked for it (the shift handoff request, with the successor count set to 0). Wait for every marker; a miss stops the move as in 5.1, step 4.
4. Scale the primary to 0 and confirm no seat runs.
5. Scale the mirror up with the account's `env_on_move` overlay applied at spawn (section 7).
6. After record.

### 5.3 What every move emits

Events in the daemon's ring, so `marvel events` and anything that follows it see them: `failover.proposed`, `failover.started`, `failover.role-moved`, `failover.completed`, `failover.stopped` (with the reason and the record path), `failover.fault`, and the same for `failback.*`. A record file is written once and never edited after the move.

## 6. Failback

| `failback.mode` | what happens |
|---|---|
| `auto` | marvel moves the scope back to the primary, through the same steps as 5.1 or 5.2, when `when` is met and the scope is at a cut point (the shift's handoff is the cut point). `when: reset` means the reset recorded at failover has passed. `when: below` means a fresh reading of the primary is below `below`; with no seat on the primary, that needs MP1. |
| `alert` | When `when` is met, marvel emits `failback.proposed` once, shows it in `marvel get failovers`, and does nothing. A person or another tool acts, with `marvel failover back <policy>`. |
| `none` | marvel never proposes failback. The scope stays where it is until a person runs `marvel failover back <policy>` or `marvel failover clear <policy>`. |

`cooldown` and the fault checks apply to failback as to failover. `when: reset` is safe on a missing reading, because the recorded reset does not move; it is the default for that reason. A failback never starts inside `cooldown` of the failover, so a reading taken just after a move cannot bounce the team back.

## 7. Director: where tickets A and B fit, and the no-director case

marvel knows nothing of director. Where a move needs a seat to be told something at spawn, the policy carries it as an env overlay, `env_on_move`, set per account in the policy and applied only to seats spawned by the move:

```yaml
  action: swap-team
  mirror: alpha-ent
  env_on_move:
    enterprise: { DIRECTOR_STANDS_IN_FOR: alpha }   # ticket A
    ultra-a: { DIRECTOR_RETURN_FROM: alpha-ent }    # ticket B
```

- **Ticket A** (a mirror seat answers mail named for the ultra seat it stands in for): set on the mirror's seats at failover, cleared by the failback, which spawns without it.
- **Ticket B** (a returning seat starts at the stream head and reads the mirror's handoff): set on the primary's seats at failback only.
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
| FO4 | Evaluator in `dry-run`: decisions and reasons, `get failovers` status, `failover.proposed` events | FO1, FO3 | Fixture readings give each row of section 4's table; a stale 92% gives `hold: stale`, never `propose`. |
| FO5 | Account override record and spawn-time env, persisted, kept across `apply`; `marvel failover clear` | FO3 | A role with an override spawns with the account's env; `apply` of the manifest does not clear it; `clear` does. |
| FO6 | `relogin` executor in `enabled` mode: records, role-by-role handoff shift, stop on a missing marker | FO4, FO5 | A fake seat that never writes its marker leaves the move `part done` with both role sets in the record, and no `--no-handoff` shift is issued. |
| FO7 | Failback `auto`, `alert`, `none`, and `marvel failover back` | FO6 | With `when: reset`, a recorded reset in the past and the scope at a cut point gives a failback under `auto`, one `failback.proposed` under `alert`, and nothing under `none`. |
| FO8 | `swap-team` executor, replica hold across `apply`, and `env_on_move` | FO6 | `apply` of the primary's manifest during a hold keeps it at 0 and says why; mirror seats spawned by the move carry the overlay and later seats do not. |

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
