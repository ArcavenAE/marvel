# Usage limits: read the account, mark the seat, pause with an end

Design for review. No code lands until this doc is reviewed.

- Author: arcaven-architect-g5-0.
- Issue: #417. Tracks: aae-orc-1p9z2, which carries recommendations 1 to 3 of
  marvel finding-056 (on #408), approved by the operator on 2026-09-30 as one
  design.
- Related: finding-050 (what each backend exposes); #415 (scheduled runs).
- Checked against marvel `origin/main` 3cb6101.

## 1. The problem, verified

| Premise | Where | Result |
|---|---|---|
| The harness reports the account's windows | `cmd/marvel/ctxforward.go:111-124` | `rate_limits` with `five_hour` and `seven_day`, each `used_percentage` and `resets_at` |
| marvel parses them and then drops them | `ctxforward.go:30-38`, `:430-440` | shown in the pane, deliberately not sent: the heartbeat RPC is keyed to a session, and headroom belongs to the account |
| The populated shape is not yet observed live | `ctxforward.go:104-109` | "NOT VERIFIED: a populated payload observed in the wild" |
| codex writes the same data to disk | finding-050 section 2a | `payload.rate_limits` in the rollout log: `primary` and `secondary` windows with `used_percent`, `resets_at` |
| marvel has no account identifier | `internal/api/backend.go:196-215`; `Session.BackendResolved`, `BackendCredentialSource` | a subscription session resolves to `default`; nothing names the account |
| A restart-neutral advisory has a precedent | `api.ActivityState` (`internal/api/types.go:69-101`), shown as a `(stalled)` suffix | a condition beside health, never a restart trigger |
| No pause record exists | `git grep -i pause internal/api` | none |

What finding-056 measured, summarized: a fleet ran into an account limit, the
harness resumed on its own at the reset, and the seats then stayed idle for
hours because the pause a human declared had no end a machine could read.

## 2. Part 1: an account-scoped reading

**The account key.** marvel cannot see which account a session uses: a
subscription resolves to `default`, and reading the credential to find out
would cross the custody boundary (ADR-009). So the key is derived from what
marvel already records, and labelled as derived:

```
account = (harness, resolved backend, credential source, config home)
```

`config home` is the home the session's login comes from: the operator's
harness home, or the `Source` a private home links its credentials from
(`SessionHomeSpec.Source`). It is never the per-session private home itself,
which differs for every session (`internal/session/sessionhome_test.go:91`) and
would give each codex session a key of its own. Every
session with the same key is taken to share a limit. The operator may name a
key in the cluster config (`accounts: [{name, match: {...}}]`) so the views
show a word instead of a tuple. **Stated limit:** two sessions on the same key
with different logins would be merged. That is a configuration error to
report, not a case to design around.

**The home.** A new daemon RPC, `account.limits`, takes one reading: the key,
the windows (`name`, `used_percent`, `resets_at`), the reporting session and
the time. It is not the heartbeat RPC and carries no session-keyed fields, which
is the separation `ctxforward.go` asked for.

- **Producers.** `ctx-forward` sends it on the statusline tick, beside, not
  inside, the heartbeat. A codex reader sends it from the newest
  `rate_limits` record in the rollout log (finding-050). No refusal text is
  parsed.
- **Storage.** In memory, per key: the newest reading and its provenance. A
  reading older than 15 minutes shows as `stale`, never as a number. The
  provenance a condition relies on is copied onto the session with the
  condition (section 3), so it survives a daemon restart without the reading.
- **Where it shows.** The budget view already renders one row per dimension
  (`cmd/marvel/main.go:2885`). It gains one row per account and window:
  dimension `account_window`, observed `used_percent`, the window's reset as a
  short remaining time, and the provenance in the note. An account belongs to
  no workspace or team, so those two cells read `-`. Registered, not
  enforced: it informs, it does not gate (diagnostic-not-gate).
- **Custody.** The reading is a number the harness already computed from
  response headers. No credential moves (finding-050 section 1).

## 3. Part 2: the `limited` seat condition

A session is `limited` when either holds:

1. its account's reading has a window at `used_percent >= 100` with `resets_at`
   in the future; or
2. its own harness reported a limit refusal. For an interactive seat the
   statusline reading is that report; there is no pane-text scraping. For a
   headless run, neither stream parser recognizes a limit error today
   (`internal/runtime/claudecode`, `internal/runtime/codex`: no match for rate
   limit or usage limit). UL-3 adds it only after capturing a real sample, so
   until then a headless run is `limited` through its account's reading only.

It clears at the window's `resets_at`, or earlier when a newer reading for its
account is below 100.

The session stores the condition with its provenance: the window, `resets_at`,
the reporting session and the reading time. After a daemon restart the
condition still clears at `resets_at`, and `describe` still names where it
came from, though the in-memory reading is gone.

`limited` is a condition, not a state: `State` stays `running`, and
process-alive health is untouched. It is stored beside `ActivityState`, with
the same restart-neutral rule: it never triggers a restart, a kill or a
shift.

**How it shows (operator constraint).** The columnar view must stay narrow:

- **`get sessions`:** the `STATE` cell reads `limited` in place of `running`
  while the condition holds. Seven characters, the same width as `running`. No
  until-time, no reason. Stated cost: a script that filters the table on
  `running` misses a limited seat. The JSON form keeps `state` (`running`) and
  `condition` (`limited`) as separate fields, and scripts should read that.
- **`describe session`:** `Condition: limited until 2026-09-30T21:00Z (five_hour
  window at 100%, account <name>, reading from <session> at <time>)`.
- **The event ring:** `session.limited` and `session.unlimited`, once per
  transition, carrying the until-time and the reason.

## 4. Part 3: a declared pause with an end

A **Pause** is a record in the store, not a manifest resource:

| Field | Meaning |
|---|---|
| `scope` | `fleet`, or a `workspace/team` |
| `by` | who declared it (the RPC caller's key fingerprint) |
| `reason` | free text |
| `until` | `reset`, an explicit time, or empty. `reset` is resolved once, at declaration, to the latest `resets_at` among the scoped accounts' fresh readings, and stored as a time. With no fresh reading, `--until reset` is refused, so a pause never waits on a reading that has gone stale |
| `hold` | true means it does not end by itself |

Verbs: `marvel pause <scope> --reason ... [--until reset|<time>] [--hold]`,
`marvel pause lift <scope>`, `marvel get pauses`.

**While a pause holds:**

- admission refuses growth in its scope (new spawns from apply, scale, run,
  shift and reconcile), except repair of an interactive seat's slot a crash
  emptied; the refusal names the pause. Headless repair is held (section 4a);
- automatic shifts in its scope do not start;
- scheduled firings in its scope are skipped with reason `paused` (#415
  section 3, which then allows at most one catch-up run after the lift);
- running seats are not stopped. marvel does not tell an interactive seat to
  stop: it holds no agent address, and a seat is told by its supervisor or by
  director. The pause is readable by every seat through `marvel get pauses`
  (seats carry `MARVEL_SOCKET`), and mirrored to the marvel events stream when
  that exists (aae-orc-aubd6).

**How it ends.** With `until` set and `hold` false, the daemon lifts it at that
time and emits `pause.lifted` with reason `until-reached`. That is the
auto-lift the operator approved. With `hold` true, marvel emits
`pause.lift-proposed` at the reset time and waits for `marvel pause lift`
(ADR-007: marvel proposes, the operator decides). A lift always emits one
event, so a director relay can tell seats "lifted" from a record rather than
from memory, which is the gap finding-056 measured.

### 4a. A headless run that ends under a limit or a pause

A headless run already in flight when its account becomes `limited` either
exits non-zero or is killed at its `active_deadline`. Today the reap path
would mark it `crashed`, refill it through `noteReapedCrash`
(`internal/team/controller.go:638`), and charge a restart, and the refill
fails the same way on the same account until `max_restarts` freezes the role
(with `restart_policy = never`, on the first). Two rules close that:

1. **Held repair.** While a headless role's account is `limited`, or its scope
   is paused, the reconciler spawns no repair for it. The slot stays empty and
   the ended run keeps its record. When the condition clears or the pause
   lifts, one repair spawn is admitted as usual. The pause's repair exemption
   is for interactive seats only.
2. **No charge.** A headless run that ends while its account is `limited` is
   recorded with outcome `limited`, emits `run.limited`, and is not charged:
   it adds nothing to the restart count, cannot saturate `max_restarts`, and
   never freezes the role, whatever `restart_policy` says. This holds for every
   run marvel records as `limited`. A non-scheduled role on an account with no
   reporter is charged at reap until UL-3 (below), so it can still freeze under
   `max_restarts` until then; only scheduled roles are never frozen by a limit
   today (#415).

**How marvel decides "ended while limited".** At reap, a claude headless run
usually cannot say: its stream has no limit report until UL-3, it produces no
reading of its own, and the reconcile loop reaps within about 2 seconds
(`internal/daemon/daemon.go:52`) while the next reading may be a statusline
tick minutes away. So the decision is:

- **The run reported a limit itself** (UL-3, once built): `limited` at reap.
- **The account is already `limited` at reap:** `limited` at reap.
- **The account has a reporter** (a fresh reading for its key exists): the
  exit is held as `pending` until the account's next fresh reading or 15
  minutes, whichever comes first. While pending, no repair spawns and nothing
  is charged. A reading that shows the account limited, with a reset after the
  run ended, settles it as `limited`; a reading below 100, or the 15 minutes
  running out, settles it as an ordinary failure, charged as today.

  **Pending survives a daemon restart.** The hold is persisted on the session
  record, as section 3 persists a condition's provenance: `pending` with its
  reap time and its reap deadline (reap time plus 15 minutes). An in-memory
  hold would be dropped by a restart, and the run would be repaired with no
  hold and no charge; a persisted hold with no start time has no anchor and
  could hold the slot forever. On startup, before the first reconcile, marvel
  settles every pending hold whose deadline has passed as one charged
  ordinary failure, and an open hold keeps its original deadline, never a
  fresh 15 minutes. A reading that arrives after the restart settles an open
  hold as it would have before.
- **The account has no reporter** (no fresh reading for its key, as for an
  account only headless claude runs use): charged at reap as today, with no
  hold. Rule 2 depends on UL-3 for these accounts, and until UL-3 ships they
  keep today's charging.

Stated costs. A real crash on an account with a reporter is repaired up to 15
minutes late. A headless-only account can still be charged for a limit until
UL-3, and a non-scheduled role there can still freeze. For a scheduled role
that charge cannot freeze it: #415 takes scheduled
roles off the restart policy, and its default `on_failure = "wait"` records a
failed firing and runs the next one.

For a scheduled role, #415 applies the same two rules to its firing record: a
run that ends while limited spends no retry and never triggers `on_failure =
"freeze"`; the firing waits, and section 5's catch-up rule decides whether it
runs after the clear.

**Automatic pauses: not in this design.** marvel does not declare a pause on
its own when a reading reaches 100%. Whether to pause at a threshold is the
operator's call (finding-056 says so). A `limited` seat already stops making
progress; the pause is for the operator's fleet-wide intent.

## 5. How it fits scheduled runs (#415)

- A firing whose role's account is `limited`, or whose scope is paused, is
  skipped with reason `limited` or `paused`.
- When the condition clears or the pause lifts, #415's recovery rule gives at
  most one catch-up run, if it is still inside `starting_deadline`.
- Scheduled work is the first to shed: nothing here waits for it.

#415 section 4 already names these two reasons as waiting on this design; this
doc supplies them.

## 6. Tests (red first on 3cb6101)

1. `account.limits` with a window at 100% and a future reset: every session on
   that key reads `limited` in `get sessions`, and `describe` shows the
   until-time, window and provenance.
2. The column cell is exactly `limited`; no until-time appears in the table.
3. At `resets_at` (fake clock) the condition clears and `session.unlimited` is
   emitted once.
4. A reading older than 15 minutes renders `stale` in the budget view, and
   does not set or hold `limited`.
5. `limited` never triggers a restart, a kill or a shift.
6. A pause with `--until reset` refuses a scale-up in scope, admits repair,
   and lifts itself at the reset with `pause.lifted`.
7. A pause with `--hold` emits `pause.lift-proposed` at the reset and stays
   until `marvel pause lift`.
8. A scheduled firing in a paused scope is skipped with reason `paused`
   (joins #415's tests).
9. The heartbeat RPC still carries no account fields (a guard on the
   separation).
10. A headless role with `max_restarts = 1` whose run exits non-zero while its
    account is `limited`: no repair spawns until the clear, the restart count
    stays 0, the role is not frozen, and `run.limited` is emitted once; after
    the clear exactly one repair spawns.
11. The same with `restart_policy = never`: not frozen.
12. A paused scope admits repair of an interactive seat and holds repair of a
    headless role.
13. `--until reset` with no fresh reading is refused; with one, the stored
    `until` does not move when a later reading arrives.
14. A reading that arrives after the reap: a headless run on an account with a
    reporter exits non-zero while the account still reads 90%; the exit is
    held `pending`, no repair spawns, and the restart count stays 0; a reading
    at 100% with a later reset arrives 3 minutes after the reap (fake clock);
    the run settles `limited`, is never charged, and repair waits for the
    clear.
15. The same, but the next reading is below 100: the run settles as an
    ordinary failure and is charged once, at settlement.
16. An account with no interactive reporter: the exit is charged at reap
    exactly as today, with no hold. With UL-3 recognition stubbed to report a
    limit, the same run settles `limited` and is not charged.
17. Two codex sessions on the same operator login share one account key.
18. A daemon restart during `pending`: a run is held at minute 0 (fake clock);
    the daemon restarts at minute 5; no reading arrives. After the restart the
    hold is still `pending` with its original deadline, no repair spawns, and
    the restart count stays 0; at minute 15 it settles as one charged ordinary
    failure and repair proceeds. A variant restarts at minute 20: startup
    settles the expired hold as one charged failure before the first
    reconcile.

## 7. Edits, in order (none made by this PR)

| # | Edit | Depends on |
|---|---|---|
| UL-1 | `account.limits` RPC, in-memory store, derived key, the budget-view rows | none |
| UL-2 | `ctx-forward` sends the reading; the codex rollout reader | UL-1 |
| UL-3 | The `limited` condition, its column cell, describe line and events | UL-1 |
| UL-4 | The Pause record, verbs, admission refusal, shift suppression, lift and proposal events | none |
| UL-5 | Scheduled-run skip reasons | UL-3, UL-4, #415's S-3 |
| UL-6 | Held headless repair, the pending hold (persisted, settled at startup), and the uncharged `limited` outcome (section 4a). The hold is a gate in `planRole` (`internal/team/controller.go:1043` on main), which already folds admission without side effects: a role whose ended run is `pending`, or whose account is `limited`, plans no repair spawn, so `applyRolePlan` has nothing to act on. The no-reporter case waits on UL-3's recognition | UL-1, UL-3, UL-4 |

## 8. Open questions

- **The populated statusline shape** is read from the binary, not observed
  (`ctxforward.go:104`). UL-2 should capture one live payload as a fixture
  before relying on it.
- **Account naming.** Derived keys work without configuration. Whether to
  require operator names for accounts, so a merge of two logins cannot happen
  silently, is the operator's choice; default is optional names.
- **The 15-minute staleness bound** is a guess sized to the statusline cadence
  of an idle seat. It is a constant to tune, not a finding.
