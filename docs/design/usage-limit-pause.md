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

## 9. Revision, 2026-10-04: what the 2026-10-03 limit showed

The account limit this design was written for happened on kinu on
2026-10-03. Nothing in sections 2 to 4a is built yet (`git grep
account.limits origin/main` finds nothing at b003cd3), so the fleet met it
with today's marvel. This section records what it showed and revises the
design where the evidence disagrees with it. Sections 1 to 8 stand except
where this section says otherwise.

- Author: the arcaven architect role. Checked against marvel `origin/main`
  b003cd3.
- Evidence home: `_kos/probes/brief-seat-account-switch-on-quota.md` (open,
  not yet committed when this was written) and the operator's relay through
  director.

### 9.1 What was observed

| Observation | Source | Bears on |
|---|---|---|
| 8 of 30 Claude seats sat on the weekly-limit menu for hours, unseen | the probe brief, item 1 (`marvel capture` on each seat, grep for the menu text) | sections 3 and 4 |
| The menu reads "Stop and wait for limit to reset / Wait here, then continue automatically at <time> / Switch to usage credits" | the same | 9.3 |
| Statuslines showed `acct 7d 100%` | relayed by the supervisor; `formatRateLimits`, `cmd/marvel/ctxforward.go:268` | section 8, first open question |
| `marvel get sessions` showed every stuck seat as `running` and healthy | relayed by the supervisor | section 3 |
| All 30 seats share the operator's default config directory, so one `/login` changes the store for all of them | the probe brief, items 2 and 3 | section 2, the account key |
| After the move to Claude Enterprise, 0 of 30 live statuslines carry an `acct` segment | `tmux capture-pane` on every pane, 2026-10-04 about 03:25Z: 30 statuslines matched `CTX n%`, none contained `acct` | 9.2 |

### 9.2 The reading may not exist on every plan

`formatRateLimits` prints whenever the payload carries a window with a
percentage, so an absent `acct` segment on all 30 seats is consistent with
the Enterprise payload carrying no `rate_limits` block. This is inferred from
the pane, not read from a payload; UL-2's fixture capture should record one
Enterprise payload as well as one subscription payload.

If it holds, Part 1 has no source on kinu today, and `limited` (section 3)
can only come from a harness refusal, which UL-3 does not recognize yet. Two
consequences for the build:

1. **The no-reading case is the common case, not the edge.** `get sessions`
   must not read as "not limited" when there is no reading. Where the
   account has no fresh reading, `describe` says `limit reading: none` and
   the budget view row says `none`, never `0%`.
2. **UL-3's refusal recognition moves up.** For an interactive seat, section
   3 ruled out pane-text scraping and relied on the statusline. With no
   statusline reading, the seat's own refusal is the only signal left. UL-3
   should capture a real refusal sample from each plan type before choosing
   its match; the limit menu of 9.1 is one such sample for the subscription
   plan, captured by hand.

### 9.3 Seen is not survived: the menu waits for a key

Section 1 summarized finding-056 as "the harness resumed on its own at the
reset". The 2026-10-03 limit did not behave that way. The menu blocks until
someone picks an option; a seat left on it does not resume at the reset. Only
the second option ("Wait here, then continue automatically") resumes by
itself, and nobody was there to pick it.

Every seat on the account was limited at once, supervisors and director
included. After the reset, the event `session.unlimited` (section 3) has no
awake reader on that account. The only actor not subject to the limit is the
marvel daemon, which already injects into panes on its own authority
(`injector=marvel:max-age`, `internal/daemon/daemon.go:326`).

So "survive" needs one more part, and it is a ruling, not a default this
design can take:

**Ruling UL-R1. May the marvel daemon answer the limit menu?**

| Option | What marvel does | Cost |
|---|---|---|
| A | Never touches the menu. At the clear it emits `session.unlimited` and `seat.resume-proposed`; a human or an awake seat resumes each one | Survives only if someone off the account is awake. On 2026-10-03, nobody was |
| B | Answers the menu with the second option only, "Wait here, then continue automatically", once per limit, and logs `injector=marvel:limit-wait`. It acts only after `marvel capture` matches the three option lines byte for byte against a captured sample. It refuses on any mismatch, never sends the key for the third option, and never answers a menu it did not just match | The seat resumes at the reset with no one present. It needs a captured sample per harness version, and a harness change makes it refuse, not misfire |
| C | Waits for the seat to return to a prompt, then injects a fixed resume line | Does not help a seat that stays on the menu, which is the observed case |

**Default: B**, gated on a byte-for-byte captured sample and the refusals
above. The third option spends the operator's money, and choosing it stays a
human act (ADR-007); B is written so it cannot reach it. Two things argue
against B and belong with the ruling. B reads pane text to decide an action,
which section 3 refused for setting a condition. And on 2026-10-03 the
auto-mode classifier refused a seat's raw Escape key to another seat's pane
("Interfere With Workloads"; the probe brief, "Constraint met"). B is the
daemon acting on its own authority, not one agent typing into another
agent's pane. Whether the operator wants that authority to exist is the
question. Expiry: the default holds until the build of UL-7 starts; with no
ruling by then, the builder builds A.

### 9.4 Revised edits

| # | Edit | Depends on |
|---|---|---|
| UL-2 | As before, plus one captured payload per plan type (subscription and Enterprise) as fixtures; a missing `rate_limits` block is stored as `none` | UL-1 |
| UL-3 | As before, with the captured limit menu as the first interactive refusal sample, and `none` shown as `none` (9.2, item 1) | UL-1 |
| UL-7 | The resume part of UL-R1: A's events always; B's guarded menu answer only if the operator rules B | UL-3, UL-R1 |

Build order for the builder's red/green PR: UL-1, UL-3 and UL-2 first, so
the next limit is seen. UL-7 follows its ruling. UL-4 to UL-6 are unchanged.

### 9.5 Tests added

19. A seat whose account has no reading shows `none` in the budget view and
    `limit reading: none` in `describe`; it is never shown as below the limit.
20. (UL-7, option B) A pane whose capture matches the sample receives exactly
    one key, the second option's, and the event carries
    `injector=marvel:limit-wait`. A pane that differs by one byte receives
    nothing and emits `limit-menu.refused` with the reason.
21. (UL-7) No path through UL-7 sends the third option's key; a guard test
    asserts it over every branch.
22. (UL-7, option A) At the clear, `seat.resume-proposed` is emitted once per
    limited seat and nothing is injected.

### 9.6 Open questions added

- **The Enterprise payload.** Is `rate_limits` absent on Enterprise, or
  present only near a limit? One captured payload answers it (UL-2).
- **The account switch.** Whether a running seat's requests follow a new
  `/login` in the shared store is the probe brief's open decisive test. If they
  do, the account key in section 2 can change under a live session, and the
  key must be re-read on each reading, not cached per session.
