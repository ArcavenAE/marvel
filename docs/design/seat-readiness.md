# Seat readiness: per-duty checks, withdrawal and routing

**Date:** 2026-10-09
**Status:** design, ruled 2026-10-10 (section 9). Nothing here is built.
**Builds on:** `docs/design/seat-register.md` (#802, the register's cells,
reason codes and routing rule) and `docs/design/marvel-watcher.md` (#800, the
watcher's blocker names, report only). Director R-200 (`sim/requirements.md`
in the director repository) reports a seat that took no turn or no ack, with
its blocker.
**Depends on:** the supervisor control-plane design (in progress), for a
per-seat caller identity on daemon calls.

## 0. Why

A seat can be alive and still unable to do its job. A reviewer whose sandbox
cannot reach GitHub, a seat denied the tool it needs to acknowledge, a seat
held at a trust prompt or a consent dialog since spawn, a successor that never
took a first turn: each was routed work it could not do, and someone found out
by waiting. R-200 reports these after the fact. Nothing today records what a
seat can do per duty, nothing keeps an unready seat off the free list, and a
seat has no way to say "not this duty right now".

This design adds three things: a per-duty readiness record in the register,
per-role checks that prove a duty without reading a credential, and a routing
rule that reads them. It adds no new transport and no new report path.

## 1. Premises, verified at origin/main

| # | Premise | Evidence |
|---|---|---|
| 1 | Presence carries no duty state | director's `set_presence` takes `idle`, `busy` or `away` and expires if not repeated (`probe/nats-phase-0/director-mcp/tools.go:83-100`) |
| 2 | marvel's harness state knows one state | `HarnessStateLoggedOut` (`internal/api/types.go:1088`) |
| 3 | The one live pane pattern covers claude 2.1.285 to 2.1.293 | `internal/panestate/patterns/claude/2.1.290/logged-out.yaml:22-24`; a newer build reads uncovered |
| 4 | The watcher visits only seats with old unread mail and a known turn source | `docs/design/marvel-watcher.md:38`, `:123`. A seat held since spawn with an empty queue is never visited |
| 5 | The register bars `ok`, `free` and `ready` in any cell | `docs/design/seat-register.md`, plan row R1 |
| 6 | A zero `ValidUntil` means a cell does not expire | `internal/asof/asof.go:37-40` |
| 7 | codex hooks leave no turn stamp | `docs/design/marvel-watcher.md:24`; codex first turn reads `-` with the code `unsupported` |
| 8 | A bus message does not say which seat of a team sent it | every seat of a team shares one broker user with publish on `agent.<ws>.<team>.>` (`internal/bus/declared.go:207`) |
| 9 | marvel has no readiness check | `HealthCheckType` has two values, `heartbeat` and `process-alive`; `Readycheck` is model-only (`CLAUDE.md`, Resource Model). Health is liveness, not productivity |
| 10 | A read-only codex sandbox blocks gh with `error connecting to api.github.com` | `docs/design/codex-reviewer-forge-access.md`, check P1. The reviewer-codex case may be this, not a bad token; the two-call check in section 4 separates them |

## 2. The record

Readiness lives in the register, per duty, as two cell families. Both are
unchanged `asof.Cell`.

**`duty.<name>`, observed.**

| value | meaning |
|---|---|
| `proven` | a check passed at `observed_at`, from the vantage in `Source`; it says nothing about the next minute |
| `fail` | a check ran and failed; `Source` carries a code |
| `unproven` | the duty has no harmless proof, or no check has run yet |
| `-`, `?` | as the register defines them (`docs/design/seat-register.md:50`) |

`Source` carries a vantage word and a stage from the register's fixed
vocabulary: `seat:reach:<code>`, `seat:auth:<code>`, `pane:...`, `host:...`,
`peer:...`. A `seat` value was run in the seat and reported by it, and says so.
A `pane` value is the daemon's reading of the seat's own screen, as
`pane.alive` and the watcher's `blocker_class` are. A pass from
another vantage never stands in for the seat's: the reviewer case above passed
from its supervisor's seat and failed in its own.

**`withdrawn.<duty>`, declared.** Set by the seat, its supervisor or director,
named in `Source` (`by:seat`, `by:supervisor`, `by:director`). `ValidUntil` is
zero, so it never expires into ready. A `recheck_by` sell-by in the store asks
the named role to re-check it; it never lifts it.

**Behind both,** a store record `(seat, duty, state, seq, observed_at,
set_by)`, `seq` assigned by the daemon. It is written only through a daemon
call. No bus message changes it (premise 8). A router pulls the register row
at dispatch; a pushed notice only says something changed.

The seat view is computed at read time as counts per value. It is never
stored and never a score.

## 3. Duties per role

A role declares its duties beside `global_role`, read through one resolver as
`config.ResolvedGlobalRole` is. Each names a check from a fixed library; a
role cannot invent a check.

| role | duties |
|---|---|
| every supervised seat | `first-turn`, `ack` |
| reviewer | `gh.read`, `gh.comment` |
| envoy | `gh.read`, `gh.post` |
| a seat that works in Jira | `jira.read`, `jira.write` |

`first-turn` and `ack` belong to every job's duty set, so a `fail` on either
counts for any job the router places.

## 4. Checks

A check proves a duty. It never reads or prints a credential.

| duty | check | values |
|---|---|---|
| a read (`gh.read`, `jira.read`) | A: an unauthenticated TLS reach to the API host. B: one harmless authenticated read of the caller's own record or rate limit (`GET /rate_limit`; Jira `GET /rest/api/3/myself`), through the seat's own tool | A fails: reach `fail`, read `unproven`. A passes, B rejected: read `fail` |
| a write (`gh.post`, `gh.comment`, `jira.write`) | none harmless; proving a post means posting. The last successful write and its time, from the event the seat already produces | `proven` with that time, else `unproven`. A seat's own report never makes a write `proven` |
| `ack` | the director ledger's last ack from the seat; a denial seen by the watcher | observed only; never a test send |
| `first-turn` | turn evidence by the `ready_by` deadline (section 5) | observed only; `-` with the reason code `unsupported` where a harness gives no turn evidence (`docs/design/seat-register.md:50`), which routes as ask first |
| a held dialog | one plain capture, matched to a version-ranged pattern | a match writes `fail` to `first-turn` with `Source` `pane:dialog:<class>`, and the watcher's record carries `blocker_class` `dialog` (`docs/design/marvel-watcher.md:95`). No match writes no duty value; the watcher's record carries `unknown` with `no-dialog-pattern`. A dialog caught after `first-turn` was `proven` overwrites it with `fail`, by design: the newest observation wins, and the next observed turn restores it. See the note below |

Rules for every check:

- The check runs in the seat, through the same execution path as the duty, so
  the sandbox tested is the sandbox the duty uses. It returns a code. marvel
  never holds the credential; it receives a code (the audience test of SOUL
  section 3).
- No retry after `auth-rejected` or `forbidden`; one retry after a transient
  code. After a rejection the check reruns only on an event (a respawn, a
  re-check request), never on a clock.
- One circuit per bot identity label: the first rejection suspends
  authenticated checks for that identity across its seats until an event.
- The wrapper keeps no body, header or raw error; no `gh auth status`, no
  verbose flags, no credential in argv. A sentinel-secret test guards it.
- No static read of permission files: they hold the operator's settings, and a
  classifier denial is in no file.

**Structural codes.** Only these `fail` codes are structural, and only they
can exclude a seat (section 6 rule 1): `seat:auth:auth-rejected` and
`seat:auth:forbidden` (the far side refused the credential), a `denial` the
watcher saw for a tool the duty needs, and a matched dialog
(`pane:dialog:<class>` on `first-turn`). `pane` is a reading of the seat's own
pane, so the register's host-vantage rule does not discount it. Every
`seat:reach` code (`dns`, `connect`, `tls`) and `rate-limited` is transient:
after its one retry it still prints `fail` with its code, and it routes as ask
first, never excluded. A reach that keeps failing is reported to the
supervisor, who may withdraw the duty.

**Dialog text.** The register shows the watcher's `blocker_class` and the
pattern class as a code, and no pane text, because pane and dialog text are on
the register's Never list (`docs/design/seat-register.md:83`). The watcher's
own record keeps truncated, masked text for `dialog` and `denial`
(`docs/design/marvel-watcher.md:98`) for whoever reads that record; this
design adds no text retention to either.

## 5. Spawn and timing

- **Default-deny.** At spawn the daemon writes every declared duty as
  `unproven`. A seat held at a dialog or with no first turn is therefore never
  routable, without a new visitor to find it.
- **`ready_by`.** A deadline after spawn. When it passes with no first turn or
  no proven duty, the daemon reports to the seat's parent if the parent took a
  turn, else to director (the watcher's push rule). It reports; it never
  withdraws, because it is a time threshold.
- **The visit.** Past `ready_by`, one plain capture (the watchdog's
  `CapturePaneJoined`, `internal/daemon/watchdog.go:528`, not the resizing
  composer read), classified, never pressing a key. The deadline only
  schedules the capture. What the capture finds is evidence: a matched dialog
  is a structural `fail` and excludes (section 6 rule 1); no match changes no
  duty value.
- **Cadence.** Per check, set by its cost as `valid_until`. No global interval.

## 6. Routing

This extends the register's routing rule (`docs/design/seat-register.md`
section 6, `:87-89`), whose rule 2 (`-` or `?` means ask first) and rule 3 (a
host vantage proves the host) apply unchanged to duty cells. The router
applies the rule to the cells; marvel never disables a seat.

1. A fresh structural `fail` (section 4), or a withdrawal: excluded from the free list,
   with the reason shown. A supervisor may override by naming the seat. This
   is the one place a cell removes a seat from a list.
2. A fresh `proven`: eligible for that duty.
3. `unproven`, `-`, `?`, or a transient `fail` (section 4): ask first; never
   excluded and never assumed.
4. A seat not eligible for a duty is not borrowed from another scope.

Counts, rates and time thresholds never exclude; they stay diagnostic
(ADR-007). A seat may withdraw itself; it may never restore or qualify itself
by assertion. Nothing excludes or withdraws on silence or a clock: the
`ready_by` deadline schedules a report and a capture, and only the evidence
the capture finds can exclude.

## 7. Plan (filed as flat tickets after the rulings)

Ships first, needing no per-seat caller identity: the store record (read
side); both cell families, the vocabulary extension and a leak-fence test; the
role `duties` field and its resolver; the routing function as a pure function
with golden vectors director can share; the render; spawn default-deny; the
`ready_by` report; the deadline capture and the pattern result type; the claude
`logged-out` range extension; the check library and wrapper as inert code; a
codex folder-trust measurement on a scratch folder.

Waits for per-seat caller identity: every value a seat reports, the restore
call's confirm, and withdrawals set by a supervisor or director. Until then a
seat-reported value has no proof of which seat sent it.

Patterns to add: claude `consent`, claude `trust`, codex `folder-trust`.
opencode has no turn, composer or dialog reader; its duties read `-` with
`no-turn-reader` and `no-dialog-pattern`, and only an observed ack serves.

## 8. Decisions for the operator

Each recommendation is valid until 2026-10-23 or the operator's ruling,
whichever comes first; the architect re-checks it then. Nothing takes effect
on silence.

1. **Restore.** (a) By duty: a seat-run reach pass restores; ack and first turn
   restore on observed evidence; writes need evidence plus a supervisor
   confirm. (b) Every restore from a seat-run pass needs a supervisor confirm;
   observed evidence restores alone. The design party split 3-3.
   **Recommendation: (b) to start**, since a seat never raises itself. Once a
   false-pass rate is measured, the architect re-presents (a) for a ruling.
2. **Self-withdraw before per-seat identity.** Self-withdraw waits for
   per-seat identity; the option is with the operator.
3. **Exclusion on a fresh structural `fail`** (section 6 rule 1). (a) Exclude
   with a named override. (b) Ask first only. 4-2 for (a).
   **Recommendation: (a).**
4. **Check rulings.** (i) An unauthenticated reach check from the seat's
   sandbox. (ii) Seat-run authenticated reads as the bot identities, with the
   circuit. (iii) codex folder pre-trust for a named folder set, after the
   scratch measurement, never by a bypass flag. (iv) Dialog patterns harvested
   and masked from the first live detection, after the sentinel-secret test.
   **Recommendation: allow each.**
5. **This design.** **Recommendation: accept**, with tickets filed after 1 to 4.

## 9. Rulings (operator, 2026-10-10, relayed by director)

Each ruling quotes the option the operator chose. They settle section 8, and
section 7's tickets are filed from them.

1. **Restore:** "Every restore from the seat's own check needs its
   supervisor to confirm; evidence the daemon itself observed restores on its
   own. Can be relaxed later once we measure how often a seat's check wrongly
   passes." This is option (b).
2. **Self-withdraw:** "Wait for the per-seat control identity. Meanwhile an
   unready seat is still kept out of routing because it starts unproven and
   the daemon sees its failures."
3. **Exclusion on a fresh structural `fail`:** "Exclude it at once; its
   supervisor can override by name." This is option (a), section 6 rule 1 as
   written.
4. **Reach check from the seat's sandbox:** allow.
5. **Seat-run authenticated reads as the bot identities:** allow.
6. **codex folder pre-trust:** "Allow it, for the named folders only."
7. **Dialog patterns:** allow, with the operator's note, verbatim: "and
   capture for stagekeeper also". The design reads the note as: the deadline
   capture (section 5), masked and truncated as the watcher keeps it, also
   goes to stagekeeper's patterns database: the harness and its version, the matched class or
   `no-dialog-pattern`, and the masked text. An unmatched capture is the
   case a new pattern is built from. No unmasked pane text and no credential
   leaves the seat's host this way.
8. **This design:** accept.

