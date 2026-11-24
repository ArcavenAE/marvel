# `get sessions` output: the daemon that answered, columns, and load

Accepted by the operator on 2026-10-06, with the rulings in section 9.

- Author: the architect seat, team arcaven.
- Issue: #601 (the team rollup, judged a separate design below). Tracks:
  aae-orc-f08m0 (column selector), aae-orc-o16q2 (token rate),
  aae-orc-vdcwm (WORKDIR column).
- Checked against marvel `origin/main` 6bc4961.
- Built by a four-round design party (seats: ux, observability, distsys,
  finops, substrate). Its record is outside this repo, so the lines this doc
  relies on are quoted here.

## 0. Why

`get sessions` cannot tell the operator which daemon answered, whether the
bus behind it is up, or whether that daemon is listening on the network. It
also cannot say which seats are working. On 2026-10-06 director counted 63
seats, all "healthy", while a pane snapshot showed none busy. The table
looked the same for a stalled fleet as for a working one.

The operator asked for these, verbatim: "displaying cluster name, nats
connection status, mrvl:// info (add an indicator for local only vs network
listening) customized columns, Tin/Tout and other token rate and other
proposed columns". A second question came the same day: "can we tell what
the work queue/load/duty cycle is across the fleet? who is free/busy/idle,
blocked, overloaded, unloaded?"

This design answers the first ask in full and the second in part. Section 6
says which part is left.

## 1. The premises, verified

| Premise | Where | Result |
|---|---|---|
| Today's header | `cmd/marvel/main.go:2626` | `WORKSPACE TEAM ROLE GEN AGENT NAME STATE HEALTH CTX% CPU% RSS DESK RUNTIME LLM`; no cluster, bus, token, age or load column |
| A bare `--mrvl` binds all interfaces | `internal/daemon/daemon.go:640-643`, `internal/daemon/sshserver.go:59` | the address defaults to `":" + DefaultMRVLPort`, passed to `net.Listen("tcp", addr)` |
| The bind is not reported | `internal/daemon/sshserver.go:27` | the listener is an unexported field; no API type carries it |
| Address resolution order | `cmd/marvel/main.go:87-120` | `--socket`, then `MARVEL_SOCKET`, then `--cluster`. When the design was written, an unknown cluster warned and dialed the local socket (#586, #502); #618 changed that to a refusal, so this row is superseded by section 9, "Later rulings on P2b (#618)" |
| The bus status already has a domain and a leaf state | `internal/bus/supervisor.go:119`, `:124-127` | `Domain`; `Leaf` is up, down, detached, unenrolled, n/a, or unknown ("not polled yet"). No probe age |
| "Leaf up" means a leafnode count above zero | `internal/bus/supervisor.go:721` | `up := body.Leafnodes > 0`; it does not mean the expected subjects are carried |
| Token spend exists but is not on the wire | `internal/usage/reader.go:28`, `internal/usage/accountant.go:708`, `internal/session/manager.go:1123` | `usage.Spend`; `SessionSpend` has one caller; `api.Session` carries no spend field |
| No token rate | `internal/usage/accountant.go` | cumulative sums and sample times only |
| The watchdog visits only quiet seats | `internal/daemon/watchdog.go:122-126` | a session is skipped unless `now - ContextAt` exceeds the window; it also starts no loop while the pattern set is empty (#598) |
| Marvel does not see bus sends | (no source) | the nearest per-seat activity signal is `ContextAt` (`internal/team/controller.go:1497-1522`) |
| Agent names embed team and role | `internal/team/controller.go:1285`, `:2217` | `<team>-<role>-g<gen>-<index>`; colliding team names are refused at apply since #378 |
| No column preference exists | `git grep session_columns` | 0 hits in Go |
| No `-o wide` | `cmd/marvel/main.go:945` | `-w` is `--watch` |

## 2. The daemon that answered: a header, not columns

Cluster, bus state and mrvl:// describe the daemon that answered, not a
session. They print as a header block above the table, built from one
daemon status call. `describe daemon` carries the full record from the
same call.

- **Stream.** On stdout, only when stdout is a TTY. `--header` forces it. A
  pipe gets a plain table, as today.
- **Cluster.** The resolved address, the rung that chose it (flag, env,
  cluster, default), and the daemon's own identity. Both names print when
  they differ. That makes #586 and #502 visible. It also catches the
  2026-10-04 mismatch, where two tools named the same cluster differently.
- **Bus.** The existing `Leaf` value, with its probe age. The word is "link
  up", never "connected": a leafnode count is not proof the subjects are
  carried. `unknown` is printed as `unknown`, never as down. #600's
  leaf-down duration supplies the age.
- **mrvl://.** Three values from the daemon's own `listener.Addr()`, read
  after `net.Listen`:
  - `off`: unix socket only;
  - `loopback`;
  - `network`: a wildcard bind counts as network.

  Neither the flag text nor client config is ever used. If the daemon
  cannot report its bind, the indicator is not built.

Example, for illustration only:

```
cluster  skippy (rung: cluster)   mrvl://  network :6785   bus  link up, 12s ago
```

## 3. One as-of cell

Every derived or status cell uses one shape, defined once:

- **Fields:** value, `observed_at`, `valid_until`, source.
- **States:** fresh, stale or none, the words admission readings already
  use (`internal/admission/admission.go:453`).
- **Rendering:** the existing three-state grammar. `-` means never
  measured. `?` means the reading expired. A value means fresh. A cell with
  no sample never prints `0`. Where a cell's source can be weaker than its
  name claims, the source is rendered as a mark on the value (section 4.4).

Levels that cannot go stale need no as-of: a bind address, an identity, or
a cumulative total. The rate, ACTIVE%, LAST-ACTIVE, the bus state and the
harness state all carry an age.

## 4. Columns

### 4.1 Selection

The selector ruled on aae-orc-f08m0 (2026-09-25):

- `--columns` takes long names, and their order is significant;
- the preference is `display.session_columns`;
- the flag overrides the preference;
- an unknown name is an error.

One addition: a named set `wide`, because no `-o wide` exists.

A second reserved name, `rest` (#1040), stands for every registered column the
list does not name, so a column marvel adds later still shows for someone who
pinned the ones they care about:

- it expands in place, in registry order, and appears at most once;
- it leaves out every column named elsewhere in the list, directly or through
  `wide`, so `rest,wide` and `wide,rest` both work;
- it leaves out `composer` and `pane`, which read every seat's pane and so
  come only by name;
- on a narrow terminal only its columns are cut, the ones outside
  `fitPriority` first and then the others from the tail of `fitPriority`; named
  columns are never cut, nor is the KEY column a set read adds when no list is
  given (below), and the hidden-columns line counts the cut ones. A list of
  `rest` alone is cut to one column at the narrowest.

**Account and service columns, split (I-450).** No composite ACCT or SERVICE
cell remains. Twelve columns are named, each one fact, and each selectable,
movable and droppable like any other column:

| Name | Header | Content |
|---|---|---|
| `account` | ACCOUNT | the account id only (six hex), or its mapped name |
| `5h` | 5H% | the five-hour window's used percent |
| `7d` | 7D% | the seven-day window's used percent |
| `5h-reset` | 5H-RESET | the five-hour reset, as an offset from now |
| `7d-reset` | 7D-RESET | the seven-day reset, as an offset from now |
| `read` | READ | the age of the reading |
| `backend` | BACKEND | the resolved backend; `!intent` on a mismatch stays here |
| `cred` | CRED | the credential source, bearer variable names only |
| `home` | HOME | the account home, short form |
| `login` | LOGIN | the declared `account_label` |
| `cluster` | CLUSTER | the cluster the row came from |
| `key` | KEY | `<cluster>:<workspace>/<name>` |

There is no HARNESS column, because RUNTIME already prints the harness. There
is no "fullest window" column and no window-start column: the harness reports
only the used percent and the reset time per window, so a start would be a
guess.

`acct` and `service` stay as named sets that expand in place, as `wide` does,
so a saved `display.session_columns` keeps working: `acct` expands to
`account,7d,7d-reset,read` (the five-hour columns stay available by name, `5h` and `5h-reset`, and are no longer in the set), and `service` to
`runtime,backend,cred,home,login`. Every new column is opt-in, so the default
table is unchanged. `wide` gains `account`, `7d`, `7d-reset` and `read`, so a
stale figure's age is in view wherever the figure is. `rest` picks up every new
column under the rules above.

**KEY.** `key` is a registered column, and the list alone decides:

- with no `--columns` and no preference, a multi-cluster read still adds KEY
  first, so the default set view stays unambiguous;
- once a list is given, naming `key` places it and leaving it out drops it.

This partly reverses the marvel#816 vote that the fit never drops KEY, on the
operator's ruling in section 9. Before it, `--columns key` was refused as
unknown and a set read forced KEY in as a lead the user could not move or drop.
The cost the operator accepted: a list without KEY can show two rows that look
the same.

### 4.2 Width

A fit drops columns by a fixed priority. It never reflows. On a TTY it
prints one line on the header's stream: "N columns hidden at this width".

- AGENT NAME is never truncated: the pane verbs need the printed name
  (#337).
- RUNTIME shows its basename with `…`.
- WORKDIR shows `~` for the client's home prefix, then is cut in the middle and
  keeps its tail.
- `--no-trunc` restores both.

The fit counts suffixed cells. A row like `healthy (stalled)` (17
characters) next to `failed (saturated)` (18) already reaches 79 columns
before LAST-ACTIVE.

| Width | Default columns, in priority order |
|---|---|
| 80 | AGENT NAME, STATE, HEALTH, CTX%, LAST-ACTIVE |
| 120 | adds LLM, TOUT, RATE |
| 200 | adds TEAM, ROLE, WORKDIR, AGE, ACTIVE% |

TEAM, ROLE and GEN restate the agent name, so they leave the narrow
defaults. Two teams stored before #378 may still share a name; for them,
TEAM stays reachable through `--columns` and `describe`.

### 4.3 Tokens and accounts

- **TOUT:** cumulative output tokens, quantized (`12k`, `1.4M`). TOUT never
  shows dollars. A trailing `+` marks a lower bound (`12k+`): the sum began
  mid-session, or the transcript shrank or changed, or the daemon restarted.
  `-` means never read, and a true zero prints `0`.
  - **Claude seats.** The daemon reads the seat's transcript (the watchdog's
    locator, `findTranscript`, so no path comes from the seat) and sums output
    tokens once per message id, at the largest figure seen for that id, adding
    only the growth. On first sight of a file with history it starts at the end
    and marks the sum `+`; a shrunk file or a changed inode resets the offset,
    never the total, and marks `+`. The per-id figures are rebuilt from the
    transcript when the daemon starts, so a restart does not change the sum. If
    the transcript was replaced while the daemon was down, records only in the
    new file are not counted and the sum is shown with `+`, a lower bound. A
    sum stored without the reader's place (an older daemon wrote it) is also
    rebuilt from the whole file and marked `+`. The sum never counts a record
    twice. The place (`spend_tail`: offset and inode) is stored in the same
    write as the sum. Main transcript only:
    subagents are excluded, and `describe session` says so. Client workspaces
    are not read. A seat the usage accountant meters keeps the accountant's
    figures. The wire carries `spend_partial`, always, false included.
- **RATE:** a decaying output-token rate (aae-orc-o16q2). It is computed
  in `Accountant.Observe` and drawn through the as-of cell. A seat whose sum
  comes from the transcript shows `-` in this release, because the rate is not
  computed from that source; `describe session` says so. The rate follows in
  the activity slice.
- **COST (opt-in).** The harness's running cost total for a statusline seat,
  as `~$4.12`. The `~` marks an estimate: Claude Code's list-price figure, not
  a bill. `-` when the feed never carried one. COST is not in `wide`. The
  change per minute (`~$0.11/m`) is not a column in this release: `describe
  session` carries it as `cost_rate`. It is read from a ring of stored totals
  (`api.CostSample`, `internal/api/cost_samples.go`), one sample per 30
  seconds at most and one hour deep, as the change since the sample at the
  start of the quiet window. A total that goes down starts the ring over, so the
  rate shows `-` until a window of readings exists and is never negative; `?`
  once the feed has been silent past the window, or while a silence of over
  three minutes is inside it (`CostGapLimit`; every beat stamps `CostSeenAt`,
  so a silence tells itself apart from a flat run, and a daemon restart reads
  the same way). This moves the dollar figures out of TOUT and RATE so that
  every column has one unit and no figure disappears (card i477c1113-2, read
  by director as option 1; the operator may overrule it at review).
- **TIN, CR, CW and HIT% (I-477 slice 2, card i477c1113-1).** TIN is fresh
  input, beside CR (cache read) and CW (cache write), so that
  TIN + CR + CW = PROMPT on every harness. This replaces the earlier "no Tin
  column": raw input still never becomes a column, because codex counts cached
  input inside input while claude and opencode report it separately, but fresh
  input is comparable. For codex, TIN is input less cached input, and CW is
  `-` because codex reports no cache write. HIT% is CR / PROMPT and is `-` when
  either is unknown or PROMPT is zero. A class nobody reported prints `-`; a
  metered zero prints `0`. All four are opt-in and in `wide`, with PROMPT. A
  sum that is a lower bound ends in `+`; HIT% carries no mark, because both
  sides cover the same records.
- **Account budget, reset and credit stay in `get budgets`.** The opt-in
  account columns of section 4.1 (`account`, `5h`, `7d`, `5h-reset`,
  `7d-reset`, `read`) show each row's own account reading. They are never
  summed, averaged or thresholded across rows, and each row prints its own
  account's cells.
  - ACCOUNT is the six character fingerprint of the account key, or the mapped
    name. A default-home seat on each cluster hashes to its own id, because the
    cluster is part of the hash and marvel never reads a credential (ADR-009).
  - A figure keeps its number when its reading is stale and takes a trailing
    `?`, as in `44%?`; READ gives the age. A reading is fresh until 15 minutes
    old and stale from 15 minutes. `-` means no reading, and it never appears
    in place of a stale figure.
  - A reset prints as an offset from now in its two largest units of d, h and
    m, with no seconds: `+2d3h`, `+47m`. One already past prints `now`.
  - `-o json` carries the numeric percent, a separate stale boolean and the
    reading time. The `?` never appears in JSON, and the change is additive
    only.
  - A client config map gives display names. Its key is the cluster-free id
    of a plain read, optionally qualified as `cluster/id`; with no entry the
    declared `account_label` is used, then the hex. The map holds display text
    only, never credential material, and loading refuses a token-shaped value.
  - The per-window figures need a daemon change, which is additive: view-only
    fields `LimitWindows` and `LimitReadAt` on `api.Session`, stamped in
    `stampLimitReading` (`internal/daemon/limits.go`). Against an old daemon the
    new columns print `-`, and a new client never parses `limit_reading_text`.
  - No start column exists: the figures shown are the percent used and the
    reset offset, which is all the harness reports.

### 4.4 Activity

- **LAST-ACTIVE:** time since `ContextAt`. It is not LAST-OUT, because
  marvel cannot see bus sends; true LAST-OUT is a director fact.
- **ACTIVE%:** the share of evaluation ticks (`controller.go:1403`) in the
  last 15 minutes at which the session was not quiet.
  - **The predicate.** A session is quiet at time `now` when its
    `ContextAt` is zero or older than `now - W`. One function holds this
    test, and the rate and ACTIVE% call it. `evaluateActivity` calls it
    only for a session with a non-zero `ContextAt`. Its own path for a zero
    `ContextAt` (the observability check and the startup grace,
    `controller.go:1543-1552`) stays as it is.
  - **The window `W`.** It is the role's `activity_timeout` when declared.
    Otherwise it is the cluster's quiet window. That window is the
    operator's `watchdog.window` when set (`internal/config/config.go:183-196`),
    else `DefaultQuietWindow`, 10 minutes. The watchdog's
    `DefaultWatchdogWindow` (`watchdog.go:28`) reads the same constant.
    So one knob moves both the watchdog and ACTIVE%, and marvel has one
    default quiet window, not two.
  - **The default matters.** `activity_timeout` is opt-in
    (`internal/api/manifest.go:154-158`), and no manifest in this repo sets
    it (`git grep -l activity_timeout -- '*.toml' '*.yaml'` returns none).
    Without the default, ACTIVE% would have no window for any current
    role.
  - **What the default does not change.** `evaluateActivity` keeps its
    opt-in gate (`controller.go:1530-1531`), so the `(stalled)` advisory
    still fires only for roles that declare a timeout. Only ACTIVE% and
    the rate use the default.
  - **Where it reads `-`.** A session marvel has no activity channel for
    (`activityObservable`, `controller.go:1571`) reads `-`. So does a
    session whose 15-minute ring has not filled, including after a daemon
    restart.
  - It is counted per session in an in-memory ring and computed when read.

  What a measurement means depends on its source, and `describe` names the
  source along with `W`:
  - **Headless seats.** `ContextAt` moves when the usage accountant sees a
    token-bearing stream sample, so ACTIVE% means "tokens were moving".
  - **Statusline seats.** Every cooperative heartbeat stamps `ContextAt`
    (`internal/api/store.go:802`), so ACTIVE% means "the statusline
    reported". That is weaker than token flow and can read active while
    no work happens.
  - **Heartbeat seats.** The same as statusline seats.

  **Provisional override for statusline seats.** A seat whose statusline feed
  carries the API-time total (`APIDurationMS`) reads ACTIVE% from that total
  instead: the API-time increase over 15 minutes as a share of the window,
  held to 0..100 (`api.APIActivePct`). Tool runs are not API time, so a busy
  seat can read low and a seat in a long build near 0%. It uses the cost
  ring's rules (a lower total starts the ring over, a silence over
  `CostGapLimit` starts it over and stamps a gap), so the cell is `-` until a
  15 minute window of samples exists and `?` once the feed is silent past the
  window or a gap sits inside it. Seats with no such feed keep the tick
  reading, and `--load` averages whatever each seat shows. This is the
  operator's provisional choice and is not a ruling; the definition is still
  being developed.

  The difference shows in the cell, not only in `describe`:
  - an ACTIVE% or LAST-ACTIVE value from a statusline or heartbeat source
    carries a trailing `*` (`80%*`, `3m*`); a value from the token stream
    carries none;
  - when any cell on screen carries the mark, one legend line prints on the
    header's stream (stdout, TTY only, per V1): `* reported by statusline
    or heartbeat, not token flow`;
  - `--help` documents the mark.

  Piped output keeps the mark and drops the legend. The mark is part of
  the value; the legend is part of the header. This is the source field of
  the as-of cell (section 3), rendered.

  Either way, a seat in a long tool call reads quiet, and the number claims
  activity, not "busy". LAST-ACTIVE carries the same caveat per source.

The party renamed the requested BUSY% for this reason. Quoting the finops
seat: "BUSY% from rate measures generating, not working. Name it for what
it measures, or the fleet's '0 busy' finding gets replaced by a different
wrong number."

### 4.5 HEALTH and the watchdog

HEALTH stays liveness. The watchdog's harness state is its own as-of field.
It renders as a HEALTH suffix only when abnormal, the way `(stalled)` does
(`main.go:2694`). The full cell is in `describe` and an optional column.
Two watchdog items, `valid_until` and `uncovered`, are held for that design
party's rulings and are not in this plan.

### 4.6 PANE

PANE is one word for what a seat's pane is doing, so a seat at a login or
startup prompt no longer reads as alive (marvel#1142, part a). The set is the
operator's ruling: `working`, `idle`, `prompt`, `restart`, `logged-out`,
`unknown`, and `fresh` (part d, added by the director's ruling).

| Word | Read from |
|---|---|
| `logged-out` | the harness-state watchdog's stored verdict, logged-out |
| `prompt` | the watchdog's stored verdict, parked; or the composer reader's `menu_unsafe` (codex's update menu) |
| `working` | the composer reader, `mid_turn` |
| `restart` | claude's "Update installed · Restart to update" row, only on the row directly above the composer's top rule, in the two shapes read live (the banner right-aligned, or after a "new task?" hint), with its check mark; a transcript row never matches |
| `idle` | the composer reader, `empty` or `holds_text`, with no banner |
| `fresh` | the composer reader, `empty`, on a seat marvel has an activity channel for, that marvel passed no first message (the session records none), and has seen no use from: no tokens, requests, occupancy or spend. Spend is in the test because it survives a daemon restart when the rest of the reading does not. A seat that was handed a first message (marvel's successor message after a shift, or a role's `runtime.prompt`) reads `idle`, not `fresh`. So `fresh` is a seat marvel gave no first message at all: marvel adds no startup text of its own for it, and a role that should start work declares `runtime.prompt` |
| `unknown` | everything else, with a GAP line where marvel knows why |

- **Precedence.** The stored verdict first (applied at stamp time over the
  screen reading), then
  `working`, then `restart`, then `fresh`, then `idle`. The banner sits on a
  seat that may still be in a turn, so an open turn is shown as `working`.
- **Unknown, never a guess.** A runtime with no reader, a screen the reader
  cannot place, a seat at a shell and a seat in a client workspace read
  `unknown`. Codex's reader grounds no working or idle state yet, so a codex
  seat reads `unknown` unless it is at a prompt.
- **A screen reading, not proof.** `restart` is what the screen shows. A
  reply that reproduces the banner's exact shape directly above the composer
  also reads `restart`, and a banner can be stale. The word tells an operator
  where to look; it does not establish that an update is installed.
- **Coverage.** The banner row was read on claude 2.1.289 to 2.1.314. No new
  login or trust screen was captured for this column; those two words come
  from the watchdog's patterns as they are.
- **Cost.** The same read-only path as COMPOSER: one foreground query and
  one capture per running seat that is not in a client workspace, shared by
  the two columns. A seat with a stored verdict is still captured, like any
  other; the verdict is read from the session at every query and applied at
  stamp time over the cached screen reading, and the tick cache holds screen
  readings only.
- **The tick cache.** The daemon's tick (every `ReconcileInterval`, read at
  most every 10 s) reads the panes while a `get sessions` asked for PANE or
  COMPOSER in the last 5 minutes, and `get sessions` serves the last reading,
  so a warm query makes no tmux call. A reading older than 30 s is not served:
  that seat is read afresh and the entry refilled. A workspace turned client is
  gated on every query, so its cached reading is dropped, not served. A
  daemon nobody queries makes no pane read for these columns. A served word is
  up to 30 s behind the screen, and `restart` is a screen reading, not proof. So is
  `fresh`: it can show for up to 30 s after the first reading lands.

## 5. Every number is diagnostic

None of these cells gates anything. No value blocks a spawn, a merge or a
scale (SOUL section 8, ADR-007). Ticket text for ACTIVE% says so.

## 6. Not in this design

- **QUEUE** needs director's ask ledger, which director owns. The
  interface it would share is keyed per seat: bus message id plus
  `in_reply_to`, with delivered and acked or answered times. Marvel reads
  the ledger's counts and never recounts bus traffic. Quoting the distsys
  seat: "Marvel must read the ledger, not recount from bus traffic, or two
  counts will disagree after a restart."
- **The team rollup (#601)** is a separate design (5 of 5). It aggregates
  across hosts, so it needs a per-host as-of and "n of m hosts answered".
  It also needs its own review against the diagnostic-not-gate rule.
- **#502's refusal.** Whether an unknown `--cluster` should exit nonzero
  was #502's own ruling, and this design only exposes the rung (vote V3).
  The operator has since ruled that it is refused (section 9, ruling 2).
  That change is its own ticket, P2b, filed against #502 beside this plan,
  not inside it.
- **Credits.** No codex payload fixture exists yet.

So the load question is answered in part. Idle and active are answered
here: ACTIVE%, LAST-ACTIVE, and STATE with `limited`. Blocked and waiting
on the operator need the ask ledger. Overloaded and unloaded need QUEUE,
#601, and a ceiling marvel does not yet define.

## 7. The vote

| Item | Result | Tally | Dissent |
|---|---|---|---|
| V1 header stream | stdout when it is a TTY; `--header` forces | 4-1 | ux: stderr to a TTY, so a piped run still shows which cluster answered |
| V2 first ticket | P1 and P2a in parallel | 4-1 | substrate: P2a first, since it needs no new type |
| V3 #502 refusal | expose the rung only; the refusal stays with #502 | 4-1 | distsys: a silent wrong-cluster read; an own ticket costs nothing |
| V4 `wide` | a named column set | 4-1 | distsys: a new surface for one cell |
| V5 hidden-columns note | one TTY line | 5-0 | none |
| V6 the plan | accepted with five amendments | 5-0 | none |

Two amendments conflicted. One removed the as-of edge from the rate
computation; the other required the rate to carry `valid_until`. The edge
moves to the RATE render (P9c). This reconciliation is the architect's, not
a vote.

**Post-party correction, not a party result; accepted as ruling 4.** The party settled ACTIVE%
5-0 as "a measurement inside the quiet window" but never defined that
window. Review found that the shared predicate returns nothing for any role
without `activity_timeout`, and that no manifest sets one. Section 4.4 now
makes three changes, each the architect's, after the party:
- it defines the window as the role's timeout, else the cluster quiet
  window, which the watchdog shares (`watchdog.window`, else 10 minutes);
- it keeps the `(stalled)` advisory opt-in;
- it states what a measurement means for each source, and renders the
  weaker source as a `*` mark on the cell with a TTY legend line, so a
  statusline 80% and a headless 80% no longer look identical.

P10, P11 and P12b carry tests to match; P11 and P12b gain a P4a edge for
the TTY seam the legend uses. The ruling list in section 9 covers it.

## 8. The plan

The tickets are filed flat in bd, with these edges, now that the operator
has ruled. "Deps" are `blocks` edges. Each ticket writes its red tests
first. Three notes from the review are applied:
- the width fit counts the source mark, so P11 and P12b depend on P6;
- a test names the `--help` line that documents the mark;
- the mark is part of the rendered value, so a JSON output keeps the source
  as its own field.

| # | Ticket | Red tests | Deps |
|---|---|---|---|
| P1 | As-of cell type and renderer | `TestAsOfDashWhenNeverObserved`, `TestAsOfStaleNeverPrintsNumber`, `TestAsOfJSONRoundTrip` | none |
| P2a | Expose the resolution rung | `TestResolveRungEnvBeatsCluster` | none |
| P2b | An unknown `--cluster` refuses nonzero and dials nothing (#502; ruling 2, outside this plan's chain) | `TestUnknownClusterRefuses` | none |
| P3a | Status reports daemon identity and the mrvl:// bind; `SSHServer` publishes its listener | `TestStatusReportsBoundAddrWhenMRVLStarted`, `TestStatusOffWithoutMRVLFlag`, `TestStatusWildcardBindIsNetwork`, `TestStatusReportsDaemonIdentity` | none |
| P3b | Leaf probe age on the bus status | `TestStatusLeafCarriesObservedAt` | P1; #600 merged |
| P4a | Header block, TTY seam, "link up" wording | `TestHeaderShowsBothNamesWhenDomainDiffers`, `TestHeaderRungNamesSocketEnvOverCluster`, `TestHeaderBindThreeValues`, `TestHeaderLinkUpWording`, `TestHeaderOmittedWhenStdoutNotTTY`, `TestHeaderFlagForcesWhenPiped` | P1, P2a, P3a |
| P4b | `describe daemon` | `TestDescribeDaemonCarriesBindAndRung` | P3a |
| P5 | Column selector and the `wide` set (aae-orc-f08m0) | `TestColumnsFlagOverridesPreference`, `TestColumnsOrderSignificant`, `TestUnknownColumnRejected`, `TestColumnsWideIsNamedSet` | none |
| P6 | Width fit, truncation, hidden-columns note | `TestFitDropsByPriorityAt80`, `...At120`, `...At200`, `TestFitCountsSuffixedHealthAndState`, `TestNameNeverTruncated`, `TestRuntimeBasename`, `TestLongWorkdirMiddleEllipsis`, `TestNoTruncRestores`, `TestColumnsOverflowWarns`, `TestHiddenColumnsNoteOnTTY`, `TestHiddenColumnsNoteSilentWhenNothingHidden` | P5 |
| P7 | Spend on the wire (`Out`, `PromptTokens`) | `TestSessionContextCarriesSpend`, `TestInteractiveSeatSpendAbsent` | none |
| P8 | Output-rate EMA in the accountant (aae-orc-o16q2) | `TestOutRateDecaysToZeroWhenSamplesStop`, `TestRateNilWhenNeverSampled`, `TestRateNotInstantaneousDelta` | none |
| P9a | PROMPT in `wide` and `describe` | `TestPromptTokensLayoutNormalized` | P5, P7 |
| P9b | TOUT cell | `TestSpendCellDashWhenAbsent` | P5, P7 |
| P9c | RATE cell | `TestRateCellSortsByValue`, `TestRateCellStaleRendersQuestion` | P1, P5, P8 |
| P10 | One quiet predicate and the cluster quiet window (`watchdog.window`, else `DefaultQuietWindow`), shared with the watchdog | `TestRateSnapsToZeroExactlyWhenQuiet`, `TestQuietWindowDefaultsWhenNoActivityTimeout`, `TestQuietWindowFollowsWatchdogWindowConfig`, `TestWatchdogWindowReadsDefaultQuietWindow`; characterization `TestQuietPredicateMatchesEvaluateActivityForObservedSessions` (non-zero `ContextAt` only, timeout set) | P8 |
| P11 | LAST-ACTIVE, with the source mark | `TestLastActiveFromContextAt`, `TestLastActiveDashWhenUnmeasured`, `TestLastActiveMarksStatuslineSource`, `TestFitCountsSourceMark` | P1, P4a, P5, P6 |
| P12a | Per-session tick ring | `TestTickRingHoldsFifteenMinutes` | P10 |
| P12b | ACTIVE% | `TestActivePctFromTickRing`, `TestActivePctDashBeforeWindowFills`, `TestActivePctDashAfterDaemonRestart`, `TestActivePctDashWithoutActivityChannel`, `TestActivePctUsesRoleTimeoutWhenSet`, `TestActivePctUsesDefaultWindowWhenUnset`, `TestActivePctStatuslineAndStreamRenderDifferently` (same 80%, two sources, two cells), `TestSourceLegendOnTTYWhenAMarkIsShown`, `TestSourceLegendAbsentWhenNoMarkOrPiped`, `TestDescribeNamesActivitySource`, `TestHelpDocumentsSourceMark`, `TestJSONKeepsSourceSeparateFromMark` | P1, P4a, P5, P6, P12a |
| P13 | Account and service columns, split (I-450), opt-in | `TestAcctColumnRepeatsNeverAggregates`, `TestStaleFigureKeepsNumberWithMark`, `TestAcctSetExpandsToSplitColumns`, `TestKeyColumnSelectableAndMovable`, `TestAcctKeyDiffersAcrossClusterForEmptyHome` | P5 |

Seven tickets have no dependencies: P1, P2a, P2b, P3a, P5, P7 and P8. The WORKDIR
column (aae-orc-vdcwm) becomes selectable through P5.

## 9. Rulings

The operator ruled on 2026-10-06, verbatim via director: "design accepted
(1) and (2) unknown cluster should be refused (3) accept the 4-1 votes 4
accept the architect recommendation".

Director's mapping, which this doc follows:

1. The design is accepted.
2. V3: an unknown `--cluster` is refused (#502). This is built as P2b.
3. V1 to V4 stand as voted.
4. ACTIVE%'s window is the role's `activity_timeout`, else `watchdog.window`,
   else the 10-minute `DefaultQuietWindow`, and the `(stalled)` advisory
   stays opt-in (section 4.4).

### Later rulings on P2b (#618)

These rulings apply only when neither `--socket` nor `MARVEL_SOCKET` is set. Either one names the address outright, and resolution returns it before the client config is read (`cmd/marvel/main.go:88-93`), so no cluster name is looked up and nothing below is reached.

- Refuse an unknown cluster in both the `--cluster` flag and the config's
  `current_cluster`. A command that defines or configures a cluster may
  name one not yet in the config. The operator's words, verbatim via
  director: "yes, but permit the flag if it's defining/configuring a new
  cluster".
- A client config that does not parse, with no `--cluster` flag, falls back
  to the default socket as before, and is not refused. The operator's
  words, verbatim via director: "fallback to local is fine, do not refuse".
  With `--cluster`, an unreadable config is refused, because the name
  cannot be looked up.

### Later rulings on the account split (I-450)

The operator ruled on 2026-11-14, via director, on four Desk cards. Three
followed the panel's recommendation (i450c1113-1, -2 and -4). The `wide` ruling
(i450c1113-3) took option 2, against the recommendation of option 1. The four
rulings, in the operator's words:

- KEY (i450c1113-1): "The list alone decides. KEY becomes a normal column. With no list, a
  multi-cluster read still adds KEY first. With a list, naming key places it
  and leaving it out drops it." This partly reverses the marvel#816 vote that
  the fit never drops KEY.
- Window start (i450c1113-2): "No start column. Show only the figures the harness reports:
  percent used and the reset offset."
- `wide` (i450c1113-3, option 2): "As voted, plus READ in wide. wide gains account,7d,7d-reset,read, so a
  stale figure's age is in view wherever the figure is."
- Order of work (i450c1113-4): "Doc first, in its own small docs pull request, then the TDD
  build against it."

## 10. Notes from the build

Calls made while P5 to P12 were built, recorded here so the design matches
main. Each was made by the architect inside the accepted design; none is an
operator ruling unless it says so.

### 10.1 Column names

A column's long name is the key the watch view already sorts it by
(`sortSessions`, `cmd/marvel/main.go`) where one exists, otherwise its
header in lower case with `%` dropped. That gives
`workspace`, `team`, `role`, `generation`, `name`, `state`, `health`,
`context`, `cpu`, `rss`, `desk`, `runtime`, `llm`, `workdir`, `tout`,
`rate`, `last-active`, `active` and `prompt` (`cmd/marvel/columns.go`). The
asymmetry is deliberate: `context` and `generation` keep the sort keys the
watch view already uses, and do not become `ctx` and `gen`. Names for
columns not built yet follow the same rule: `age`, and the twelve account and
service names of section 4.1 for P13, with `acct` and `service` kept as named
sets.

### 10.2 The default table, explicitly

Section 4.2's table lists only what each width adds. The full priority, as
built (`cmd/marvel/fit.go`), most important first:

| Width | Columns shown by default |
|---|---|
| 80 | name, state, health, context, last-active |
| 120 | the above, then llm, tout, rate |
| 200 | the above, then team, role, workdir, age, active, then runtime, cpu, rss, desk, generation, workspace |

The six legacy columns close the 200 tier (aae-orc-fmgxd), so a wide
terminal shows what it always showed plus the new columns, and a narrower
one drops them first. All of them stay selectable by name and in `wide` at
any width. `age` is in the priority list but is not a registered column
yet, so the fit skips it until its ticket lands. Off a terminal nothing is
fitted, and a pipe prints the default table, because `workdir`, `tout`,
`rate` and `prompt` are opt-in there.

**Print order, changed 2026-10-30.** The priority list above decides only
what a width drops. What stays prints in the default order: workspace, team,
role, generation, name, state, health, context, active, last-active, age,
llm, then any other kept column in registry order. `active`, `last-active`
and `age` joined the default table, and `cpu`, `rss`, `desk` and `runtime`
became opt-in, so a script that read CPU% from a pipe now names it with
`--columns`.

### 10.3 Spend on the wire

P7's fields are `SpendOut` and `SpendPromptTokens`, with `OutRate` beside
them, all in `SessionContext`. `Session` embeds `SessionContext` and its
fields carry no json tags, so these are also the JSON keys. The `Spend`
prefix keeps `session.Out` from reading as pane output, and it matches
`usage.Spend`, which the accountant sums from (#639).

### 10.4 What TOUT counts

TOUT is the cumulative output since the daemon first observed the session.
A session adopted from a previous daemon has no usage drain
(`internal/session/manager.go`, "supervises them without observing them"),
so its TOUT reads `-` rather than a partial count. The cell therefore never
shows a count that began after the session did, and no "lower bound" mark is
needed (aae-orc-8hkpr, closed). One edge was found while checking that: a
heartbeat-sourced session can keep a spend reading through a reload, frozen
rather than partial. The fix is to clear spend at load, not to mark it
(aae-orc-88bm0).

### 10.5 The quiet window at its edge

`api.Quiet` is strict: a session is quiet when `ContextAt` is zero or older
than `W`, so at exactly `W` it is not quiet yet (`internal/api/quiet.go`).
For RATE (`usage.QuietRate`), a session whose channel is still attached
renders a snapped `0` once it is quiet, a fresh measurement of no flow.
`?` appears when the reading has expired while the session does not read
quiet, and `-` when it was never sampled. The client always passes an
attached channel, because a rate cell exists only for a seat whose stream
the accountant reads (`cmd/marvel/rate.go`). The rate's validity reads the same
constant as the quiet window (`OutRateValidFor = api.DefaultQuietWindow`),
so marvel keeps one default window.

### 10.6 Marks in the sessions table

Each mark keeps one meaning across every column: `*` a weaker source
(statusline or heartbeat, section 4.4), `?` expired or unknown, `-` never
measured. `*` was kept although `get clusters` uses it for the current
cluster, because the two commands share no table. A cell that never
expires, such as LAST-ACTIVE's age, carries a zero `valid_until`, which
`asof` defines as "does not expire" (`internal/asof/asof.go`).

### 10.7 Open: does a codex turn count as activity?

Read from code, not measured on a running seat. Codex's exec stream reports
a running session total, not a per-request level (`internal/usage/profiles.go`),
so its spend reaches the store through `UpdateSessionSpend`, which by design
leaves `ContextAt` alone (`internal/api/store.go`). `ContextAt` is the only
input to `api.Quiet`. So a codex seat that is producing tokens would read
quiet: RATE snaps to `0`, and LAST-ACTIVE and ACTIVE% read `-`.

Recommended, for review: yes, a codex turn counts. `UpdateSessionSpend`
stamps a new `SpendAt`; `api.Quiet` reads the later of `ContextAt` and
`SpendAt`; CTX% freshness keeps reading `ContextAt` alone, so a spend update
never makes an old occupancy reading look current. A codex LAST-ACTIVE then
comes from token flow and carries no `*`. The `(stalled)` advisory stays
opt-in. First step: one test that feeds a codex sample and asserts RATE and
LAST-ACTIVE, to confirm the reading above before anything is built.
This recommendation is valid until 2026-10-22; the architect seat re-checks
it then.
