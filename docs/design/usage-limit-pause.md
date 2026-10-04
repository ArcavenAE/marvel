# Usage limits: read the account, mark the seat, pause with an end

Design for review. No code lands until this doc is reviewed.

- Author: the arcaven architect seat.
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
2026-10-03. Nothing in sections 2 to 4a is built yet (`git grep -n
account.limits origin/main -- '*.go'` finds nothing at b003cd3), so the fleet
met it with today's marvel. This section records what it showed and revises
the design where the evidence disagrees with it. Sections 1 to 8 stand except
where this section says otherwise.

- Author: the arcaven architect role. Checked against marvel `origin/main`
  b003cd3.
- Evidence home: `_kos/probes/brief-seat-account-switch-on-quota.md`
  (marvel#532, approved, not yet merged when this was written) and the
  operator's relay through director.

### 9.1 What was observed

| Observation | Source | Bears on |
|---|---|---|
| 8 of 30 Claude seats sat on the weekly-limit menu for hours, unseen | the probe brief, item 1 (`marvel capture` on each seat, grep for the menu text) | sections 3 and 4 |
| The menu's three options read "Stop and wait for limit to reset", "Wait here, then continue automatically at Oct 6 at 9pm", "Switch to usage credits" | the same; only the time span varies between captures | 9.4 |
| Statuslines showed `acct 7d 100%` | relayed by the supervisor; `formatRateLimits`, `cmd/marvel/ctxforward.go:268` | section 8, first open question |
| `marvel get sessions` showed every stuck seat as `running` and healthy | relayed by the supervisor | section 3 |
| All 30 seats share the operator's default config directory, so one `/login` changes the store for all of them | the probe brief, items 2 and 3 | section 2, the account key |
| After the move to Claude Enterprise, 0 of 30 live statuslines carry an `acct` segment | `tmux capture-pane` on every pane, 2026-10-04 about 03:25Z: 30 statuslines matched `CTX n%`, none contained `acct` | 9.2 |

### 9.2 The reading may be absent: three reading states

`formatRateLimits` returns nothing when the payload has no `rate_limits`
block, and also when its windows carry no percentage
(`ctxforward.go:268-296`). An absent `acct` segment on all 30 seats fits
either cause; it was read from the pane, not from a payload. UL-2's fixture
capture records one Enterprise payload and one subscription payload, which
tells the two apart.

Either way, Part 1 has no number on kinu today. The design therefore names
three reading states per account key, so "no number" is never shown as
"below the limit":

| State | When | Shown as |
|---|---|---|
| `fresh` | a reading with a usable window arrived within 15 minutes | the percentage, as section 2 says |
| `stale` | the newest usable reading is older than 15 minutes | `stale` (section 2, test 4) |
| `none` | no usable reading was ever received for the key: no payload, no `rate_limits` block, or windows without a percentage | `none` |

Where each state shows:

- **Budget view.** Section 2 renders one row per account and window. An
  account in `none` has no window to name, so it gets one row: dimension
  `account_window`, window `-`, observed `none`. A `stale` account keeps its
  window rows, each observed `stale`.
- **`describe session`.** It always prints `Limit reading: fresh 87% (seven_day)`,
  `Limit reading: stale (last 2026-10-03T22:10Z)`, or `Limit reading: none`.
- **JSON.** Each session carries `limit_reading`: `fresh`, `stale` or `none`.
- **`get sessions`.** The `STATE` cell makes no claim about the limit. It
  reads `limited` only on evidence (section 3) and otherwise `running`, which
  is process state, as `CLAUDE.md` says of the HEALTH column. The operator's
  width constraint holds, and nothing in the table reads as "not limited".
  The not-limited answer is `describe` or `limit_reading`, never the cell.

Which state kinu reads depends on when UL-1 ships. Shipped today, its keys
read `none`: no reading was ever stored, since nothing is built. Had UL-1 been
running before the move to Enterprise, the keys would read `stale`, holding
the personal plan's last reading. That stale 100% neither sets nor holds
`limited` (section 2, test 4), and whether a `/login` should start a new key is
9.8's open question.

With no fresh reading, `limited` can come only from a refusal, and UL-3 does
not recognize one yet. 9.3 adds the interactive source.

### 9.3 The named exception to "no pane-text scraping"

Section 3 rules out pane text for setting `limited` on an interactive seat
and relies on the statusline. With the reading in `none` (9.2), that leaves
an interactive seat with no way to be seen at all. This revision makes one
exception, and only one:

- **What.** A seat whose account reading is `none` or `stale` is marked
  `limited`, with source `pane-menu`, when `marvel capture` of its pane
  matches the limit-menu sample under the matcher of 9.4. Nothing else in the
  pane is read for meaning.
- **When.** On the health tick for an interactive seat whose activity
  advisory reads `stalled` or `unknown`, at most once a minute per seat. A
  working seat is not captured.
- **What it may do.** Set and clear the condition, with the capture time and
  the sample's version in its provenance. It never sends a key; that is UL-7's
  question (9.4).
- **How it clears.** A `pane-menu` condition has no `resets_at` and no
  reading, so section 3's clear rule cannot end it. While it holds, the seat's
  pane is captured once a minute (the "working seat is not captured" rule
  applies only to setting). It clears at the first of:
  1. a capture that shows neither the menu block nor the post-selection
     screen of the second sample (9.4, matcher item 6);
  2. the seat's activity signal advancing (`SessionContext.ContextAt`, the
     restart-neutral advisory's signal), which means it is working again;
  3. a `fresh` reading for its account below 100.
  Each clear emits `session.unlimited` with which rule cleared it.
- **A clear before the reset is visible.** A seat that switched to usage
  credits or logged in again resumes work, and its activity signal advances
  (`store.go:790-793`), which rule 2 cannot tell from waiting out the reset.
  The menu's row 2 names the reset time, so the condition stores it as
  `until`, parsed from the matched span under three rules. The span has no
  year, so `until` is its first occurrence after the capture time (a
  late-December capture of "Jan 2" is next January, not last). It is read in
  the seat's zone when the seat's environment records one (`TZ`), otherwise
  in the host's zone, and the provenance names which zone was used. A local
  time that occurs twice (a DST fall-back) or never (a spring-forward) is not
  guessed: `until` is empty. A clear by any rule before `until` also emits `limit-menu.resumed-before-reset` at warning
  severity, naming the rule and the time left. It changes nothing; it makes a
  resume that may have cost money, or moved the seat to another account,
  visible (ADR-007). If the span cannot be parsed, `until` is empty, and the
  clear emits `limit-menu.reset-unknown` instead.
- **Why an exception and not a change of rule.** The matcher is narrow,
  versioned with the harness, and refuses on any mismatch. "Scrape the pane"
  stays ruled out everywhere else.

This reverses part of a reviewed section, so it is ruling **UL-R2**:
default, adopt the exception; alternative, keep the ban and accept that an
interactive seat on a plan with no reading is never seen as `limited`.
Expiry, in UL-R1's form: the default holds until the build of UL-3 starts;
with no ruling by then, the builder builds the alternative (no `pane-menu`
source), the conservative choice.

### 9.4 Seen is not survived, if the menu waits

Section 1 summarized finding-056 as "the harness resumed on its own at the
reset". finding-056 recorded that for the limit of 2026-09-29 to 09-30: the
harness printed "continuing automatically at 9pm" and the limited seats
resumed at 02:01Z on 2026-09-30.
What a seat left on the 2026-10-03 weekly menu does at the reset was **not
observed**. The reset is Oct 6 at 21:00, and every seat moved to Enterprise by
`/login` before it. The menu offers "Wait here, then continue automatically"
as one of three choices, which suggests that a seat nobody answers does not
continue. That is inference, not observation. It may also be that the
2026-09-29 limit, a different window, never showed the menu.

The design takes the worse case, because a wrong guess the other way leaves a
fleet stuck until a human looks. And the 2026-10-03 limit showed who would
look: every seat on the account was limited at once, supervisors and director
included. The marvel daemon is the only actor the limit does not stop, and it
already injects into panes on its own authority (`injector=marvel:max-age`,
`internal/daemon/daemon.go:326`).

**Ruling UL-R1. May the marvel daemon answer the limit menu?** Ruled
2026-10-04: build B now, A as the target (9.9).

| Option | What marvel does | Cost |
|---|---|---|
| A | Never touches the menu. At the clear it emits `session.unlimited` and `seat.resume-proposed`; a human or an awake seat resumes each one | Survives only if someone off the account is awake. On 2026-10-03, nobody was |
| B | Selects the second option only, once per limit, under the matcher below, and logs `injector=marvel:limit-wait` | The seat resumes at the reset with no one present. It needs a captured sample per harness version, and a harness change makes it refuse, not misfire |

The option that injected a resume line at a prompt is dropped. It does not
help a seat that stays on the menu, which is the observed case, and a line
typed into a live menu would become keystrokes.

**The matcher (used by B and by 9.3).** It is built from two captured
samples stored as fixtures with their harness version: the menu as shown, and
the screen after a human picked the second option by hand.

1. **Input.** The visible screen from `marvel capture`, no scrollback. Rows
   are compared whole, after trailing spaces are stripped, including the
   option number, its punctuation and the cursor-glyph column. Nothing is
   matched anywhere inside a row.
2. **Location.** The menu block is the sample's run of consecutive rows (the
   title row, the three option rows and the footer rows), found at the
   **last** position in the capture where the whole run matches, and only if
   no non-blank row follows it except the sample's own trailing rows. A menu
   quoted higher in the pane is never the block; a quoted copy below a real
   one fails the trailing-rows check and refuses.
3. **The option rows.** Row 1 is the sample's "1." row and row 3 the sample's
   "3." row, byte for byte. Row 2 is the sample's "2." prefix through `Wait
   here, then continue automatically at `, then a time span matching one fixed
   pattern taken from the sample (for 2026-10-03, `Oct 6 at 9pm`, so
   `[A-Z][a-z]{2} [0-9]{1,2} at [0-9]{1,2}(:[0-9]{2})?(am|pm)`), then the
   sample's exact remainder. Only that span is a pattern. A reordered menu,
   whatever its texts, refuses.
4. **The cursor glyph.** Exactly one option row carries the sample's glyph,
   and it must be the "2." row; the other two carry the sample's blank form.
   A cursor on row 1 or row 3 refuses. Nothing yet measures that the digit `2`
   selects option 2 wherever the cursor sits, so the matcher acts only where
   the cursor and the key agree. Probe P-UL7 (9.7) may relax this rule; until
   it does, the rule holds.
5. **The key.** The digit `2` and nothing else: no arrow, no Enter, no
   Escape. If the samples show that `2` alone does not select the second
   option, B is not buildable. The build then ships A and emits
   `limit-menu.unselectable` once, so the downgrade is visible, not silent.
6. **Confirmation.** Within 5 seconds of the key, a re-capture must match the
   post-selection sample under the same rules (the time span as in item 3).
   The post-selection samples come from probe P-UL7 (9.7), each recorded with
   the key pressed and the cursor row it was pressed from, never from a hand
   pick with neither stated. A re-capture that matches P-UL7's option-1
   screen is `limit-menu.unexpected`.
   If it does, `limit-menu.answered` is emitted. If the menu is still there,
   nothing is sent again and `limit-menu.unconfirmed` is emitted. Anything
   else emits `limit-menu.unexpected` at error severity, with the capture,
   and that seat is never answered again until an operator clears it. One
   key per limit per seat, whatever happens.
7. **Refusal.** Any mismatch sends nothing and emits `limit-menu.refused`
   with the reason and the sample version.

**Default: B**, under that matcher. The third option spends the operator's
money, and choosing it stays a human act (ADR-007); B is written so it cannot
reach it. Against B: on 2026-10-03 the auto-mode classifier refused one seat's
raw Escape key to another seat's pane ("Interfere With Workloads"; the probe
brief, "Constraint met"). B is the daemon acting on its own authority, not one
agent typing into another's pane, but whether the operator wants that
authority to exist is the question. Expiry: none; UL-R1 was ruled
2026-10-04 (9.9).

### 9.5 Revised edits

| # | Edit | Depends on |
|---|---|---|
| UL-1 | As before, plus the three reading states and the `none` budget row (9.2) | none |
| UL-2 | As before, plus one captured payload per plan type (subscription and Enterprise) as fixtures | UL-1 |
| UL-3 | As before, plus `limit_reading` in describe and JSON (9.2) and the `pane-menu` source under the matcher (9.3). **The `pane-menu` source is HELD until the operator answers UL-R2 (9.9, Conflict)**; the rest of UL-3 is not | UL-1 |
| UL-7 | UL-R1, ruled (9.9): A's events always; B's guarded selection built now, as the first limit action; the matcher and its fixtures shared with UL-3 | UL-3, UL-R1, and for B the samples from probe P-UL7 (9.7) |

Build order for the builder's red/green PR: UL-1, UL-3 (without its held
`pane-menu` source and tests 22, 22a and 29) and UL-2 first, so the next limit
is seen. UL-7 follows its ruling (9.9). UL-4 to UL-6 are unchanged.

### 9.6 Tests added (red first)

19. An account with no reading: the budget view has one `account_window` row
    with window `-` and observed `none`; `describe` prints `Limit reading:
    none`; the JSON `limit_reading` is `none`; the `get sessions` STATE cell is
    `running`, and no cell in the table carries a percentage or "ok".
20. A payload whose `rate_limits` windows carry no percentage is stored as
    `none`, not as 0%.
21. A reading 16 minutes old (fake clock) shows `stale` in the budget view and
    `describe`, and `limit_reading` is `stale`; section 2's test 4 still holds.
22. (9.3; HELD with the `pane-menu` source, 9.9 Conflict) A stalled interactive seat with reading `none` whose capture matches
    the sample is marked `limited` with source `pane-menu`; a capture differing
    in any byte outside the time span sets nothing; a working seat is never
    captured to set the condition.
22a. (9.3, clear; HELD with the `pane-menu` source, 9.9 Conflict) A `pane-menu` condition clears on a capture showing neither
    the menu nor the post-selection screen, on an advancing activity signal,
    and on a fresh reading below 100, each emitting `session.unlimited` with
    its rule; with none of the three it stays set across 10 captures. With
    `until` at Oct 6 21:00 and the fake clock at Oct 4 05:00, an activity
    clear also emits `limit-menu.resumed-before-reset` once, at warning
    severity; the same clear at Oct 6 21:05 emits none. A span that does not
    parse leaves `until` empty, and the clear emits `limit-menu.reset-unknown`.
23. (Matcher) The 2026-10-03 sample matches with its own time span and with
    `Nov 12 at 10:30am`; it refuses `Oct 6 at 9pm` followed by any other
    remainder, and a first or third row differing by one byte. Hostile
    fixtures, each refused: (i) the options reordered so the "2." row reads
    "Switch to usage credits"; (ii) a quoted copy of the real menu above a
    reordered real menu (the last block is the reordered one); (iii) a real
    menu followed by any non-blank row not in the sample; (iv) the real menu with
    the cursor glyph on row 3; (v) the same with it on row 1. Accepted: the real
    menu with the glyph on row 2.
24. (UL-7, B) A matching pane receives exactly the key `2`, once, and the
    event carries `injector=marvel:limit-wait`. A re-capture matching the
    post-selection sample emits `limit-menu.answered`; the menu still showing
    emits `limit-menu.unconfirmed` and sends nothing more; any other screen
    emits `limit-menu.unexpected` at error severity and the seat is not
    answered again. A non-matching pane receives nothing and emits
    `limit-menu.refused`.
25. (UL-7) A sample that offers no way to reach option 2 without option 3
    builds A and emits `limit-menu.unselectable` once.
26. (UL-7, guard) The only key UL-7 can send is `2`, and UL-3 sends none: a
    guard asserts this over every branch and the shared matcher. Run against
    hostile fixtures (i) to (v), no fixture leads to a key that would select
    "Switch to usage credits", and (iv) and (v) send nothing at all. No
    branch ever sends Enter or an arrow.
27. (UL-7, A) At the clear, `seat.resume-proposed` is emitted once per limited
    seat and nothing is injected.
28. (UL-7, B, missing samples) A pane matching the menu sample, for a harness
    version with no post-selection sample, receives no key; A's events and
    `limit-menu.unsampled` are emitted once, and a second matching capture in
    the same limit emits nothing more.
29. (9.3, `until` parsing, fake clock; HELD with the `pane-menu` source, 9.9 Conflict) A capture on Dec 30 of `Jan 2 at 9pm`
    stores Jan 2 of the next year, and an activity clear on Dec 31 emits
    `limit-menu.resumed-before-reset`. With the host in America/Chicago and
    the seat's `TZ=UTC`, `Oct 6 at 2am` is stored as 02:00Z, and the
    provenance names UTC; with no seat zone it is 07:00Z and names the host
    zone. With the clock in October 2026, `Nov 1 at 1:30am` in America/Chicago
    (occurs twice) and `Mar 14 at 2:30am` (2027, does not occur) store an empty `until`, and the clear emits
    `limit-menu.reset-unknown`.
    Implementation note: Go's `time.Date` does not fail on these. It shifts a
    nonexistent time and picks one instance of an ambiguous one, silently.
    Detection is explicit: build the time, convert it back to wall-clock
    fields in the zone and require them unchanged (a nonexistent time fails),
    and probe both of the zone's offsets around the instant (an ambiguous
    time matches both).

### 9.7 Pre-build probe P-UL7 (operator-run, at the next real limit)

UL-7's option B rests on two facts nobody has measured: what the digit `2`
does from each cursor position, and what the screen shows after each choice.
P-UL7 measures them. It presses keys in a live pane on a real limit menu, so
it is **interventional**: the operator runs it, or ratifies it before a seat
does, at the next real limit event. Nobody schedules it ahead of that, and
nobody forces a limit to get one.

1. On a seat showing the menu, capture it as the menu sample.
2. Move the cursor to row 1 ("Stop and wait for limit to reset"), press `2`
   alone, and capture the result. This is post-selection sample B (key `2`,
   cursor row 1). It goes first because it is the safe press: if the digit
   acts on the cursor row instead of selecting by number, it takes option 1,
   which spends nothing.
3. Only if step 2 showed option 2 taken: on a second limited seat, or the same
   one after it returns to the menu, repeat with the cursor on row 3. This is
   sample A (key `2`, cursor row 3). **The risk:** row 3 is "Switch to usage
   credits". If the digit does not select by number, this press is the spend.
   Step 2 is the evidence that it does; without it, step 3 is not run, and
   item 4 stays as written.
4. On a third, select option 1 the way the operator normally would, and
   capture it. This is the must-not-match fixture.
5. Store each with the harness version, the key and the starting cursor row.

Results: if B and A both show option 2 taken, item 4 may admit any cursor
row, and fixtures (iv) and (v) move to accepted. If either shows anything else,
item 4 stays as written. If `2` does not select option 2 at all, item 5's
"not buildable" path applies.

### 9.8 Open questions added

- **The Enterprise payload.** Is `rate_limits` absent on Enterprise, present
  without percentages, or present only near a limit? One captured payload
  answers it (UL-2).
- **The account switch.** Whether a running seat's requests follow a new
  `/login` in the shared store is the probe brief's open decisive test. If they
  do, the account key in section 2 can change under a live session, and the
  key must be re-read on each reading, not cached per session.
- **What an unanswered weekly menu does at its reset** (9.4). The first reset
  observed with the menu up answers it, and may make UL-7 unnecessary.

### 9.9 Rulings (operator, 2026-10-04, relayed by director)

UL-R1, verbatim:

> "UL-R1 A (put events on the marvel internal bus; which we don't have yet) and
> B, well plan to do A but do B right now. The real point is, yes, marvel can do
> SOMETHING about it, can even interact with the menu, the details of what it
> will do should be flexible (may include triggering a "/login" migration of
> the running session to another backend) and ideally we'll have detected this
> before it got to this point and taken defensive measures already"

UL-R1 follow-up, the operator's answer to which internal bus A means
(2026-10-04, relayed by director), verbatim:

> "yes, that's the one the event ring, the backplane with the ring-to-NATS as a method to signal to agent teams, when appropriate"

Director's reading, a gloss and not part of the quote: option A's internal bus
is marvel's event ring, used as the backplane, with the ring-to-NATS tap
(aae-orc-zhx6x) as a method to signal agent teams when appropriate. This
answers the scoping question of which bus A's events go on.

What it changes in this design:

- **B is built now** (UL-7, under the matcher of 9.4 and probe P-UL7 of 9.7).
  **A is the target**: its events go on marvel's event ring, the internal
  bus the follow-up above names, and reach agent teams through the
  ring-to-NATS tap (aae-orc-zhx6x) when appropriate, once it ships. As filed,
  that tap carries events at warning severity and above only.
- **No samples, no key.** B acts only where a menu sample and a P-UL7
  post-selection sample exist for the seat's harness version. A matching menu
  with no post-selection sample sends nothing and emits A's events plus
  `limit-menu.unsampled`, once per limit per seat. Matcher item 5 covers
  samples that fail; this covers samples that are missing.
- **The action is pluggable.** UL-7 defines a limit action as an interface
  with B's guarded selection as its first implementation. A `/login`
  migration of the running session to another backend is a named future
  action. It is not designed here; the account-switch probe brief (marvel#532)
  is where its facts live.
- **Early detection is the real goal.** Detecting the limit before the menu
  appears, and acting defensively, ranks above answering the menu. The
  account reading (UL-1, UL-2) and its thresholds serve that. A threshold
  action before 100% is a follow-up design, not part of UL-7.
- **UL-R2 was not ruled separately.** It is taken as adopted under "marvel can
  do SOMETHING about it": the `pane-menu` source of 9.3 is built with UL-3 if the Conflict below is answered in its favor, and
  its expiry no longer applies.

  **Conflict, awaiting the operator.** That adoption line and 9.3's expiry
  (build the alternative, no `pane-menu` source, if UL-R2 has no ruling when
  UL-3's build starts) disagree on whether UL-R2 is ruled. The question is
  escalated to the operator. Neither text is edited until the answer comes
  back, and UL-3's `pane-menu` source is not built until it does.
