# The seat register: what each seat can do now, with its age

Proposal, 2026-10-09. Not ruled; section 8 holds the decisions for the operator.

- Author: the architect seat, team arcaven.
- Code read at marvel `ff9d210`, director `origin/main` the same day.
- Sibling design: the built-in watcher (#800), whose per-seat state this register reads.

## 0. Why

A supervisor routing work cannot tell, per seat, whether it is in scope, alive, held at a dialog, or how long its oldest mail has waited. Director rebuilt that by hand more than once: no per-seat queue or wait (director R-115), a misroute to an idle seat outside its cast scope (R-116), and 63 panes captured to find seven seats held at dialogs (R-195). marvel's own activity cells read idle claude seats as active (#801), so a register built on them would be the overstating table director R-27 warns about.

This register prints only checked facts with their age, and prints `-` where it has none. Its first build answers "who exists, is in scope, and is alive". It does not yet answer "who is idle", and it says so.

## 1. Premises, verified

| Premise | Where | Read |
|---|---|---|
| marvel has one as-of cell: value, `observed_at`, `valid_until`, source; zero `ObservedAt` means never observed | `internal/asof/asof.go:41-47` | read |
| The statusline heartbeat re-stamps `ContextAt` on every beat, so quiet, ACTIVE%, LAST-ACTIVE and `(stalled)` read idle claude seats as active | #801 | read, and a unit test |
| **The watchdog visits only quiet panes and classifies only `logged-out`.** By #801 a statusline-fed claude seat is never visited, so it has no logged-out verdict and no recorded harness version | `internal/daemon/watchdog.go:28-31`, `:36`, `:280-328`; `internal/api/types.go:1072` | read |
| The composer read is not passive: it "nudges the pane's size" | `cmd/marvel/main.go:1916` | read |
| The claude statusline payload carries `version`; the forwarder does not read it | `cmd/marvel/testdata/statusline-2.1.226-empty.json:17`; `cmd/marvel/ctxforward.go` (no match) | read at the 2.1.226 fixture; a current build is unchecked |
| `LastHeartbeat` persists across a daemon restart | `internal/api/bolt.go:332` (`json.Marshal`; the field has no `json:"-"`) | read |
| A rendered broker principal carries its password beside `Publish` and `Subscribe` | `internal/bus/declared.go:55-66` | read |
| Any team user may publish to `agent.audit` | `internal/bus/declared.go:208` | read |
| The tmux driver has per-pane calls | `internal/tmux/driver.go:547` (`HasPane`), `:1096` (`ListPanes`) | read |
| marvel writes private files by temp file at 0600 and rename | `internal/api/bolt.go:672-694`, `cmd/marvel/accountstamp.go:70-76` | read |
| Asks are never counted by marvel | `docs/design/team-load-rollup.md:37` | read |
| A director ask row carries the ask's text (`Line`) and links | director `probe/nats-phase-0/director-mcp/askledger.go:66-77` | read |
| The ask reader's own principal is the operator's grant | director `sim/design/ask-ledger.md:339` (part A5) | read; whether it is granted is unchecked |
| No cast-scope field exists in marvel | `git grep -niE 'castscope\|cast_scope' origin/main` (one hit, an env var named in a finding) | read |

## 2. Meaning

A capability is a checked fact about what a named seat can do now, printed with the check that produced it and its age. Two neighbours are not this register:

- **Authority**, which RPCs a role may call (inject, scale, shift), is a separate design.
- **Single events**, such as one tool denial or one network failure, are not standing capabilities.

## 3. Shape

- `internal/register` builds one row per seat from values it only reads: the session store, the watcher's records (#800), and an optional `QueueSource`. It imports no NATS, no `internal/bus`, and never the `Principal` type. The watcher's import test extends to it.
- The daemon computes rows on its own. No bus subject in the first build.
- **Read paths:** a `register` resource type on the existing `get` RPC, through the same dispatch as `get sessions`, which keeps its columns. Rendered by `marvel get register` (one line per role, `--seats` for rows) and a `describe session` block. A pull file follows late (section 7, R15) for supervisors with no marvel CLI (#581).
- **Independence:** without director, queue cells print `-`; director reads the same response and its ledger needs nothing from marvel.

## 4. Cells

Every cell is `asof.Cell`, unchanged: `-` never observed, `?` expired, an old value never shown. `observed_at` is when the check saw the world, never when marvel stored it. The reason for a `-` is a code from a fixed vocabulary in `Source`, including vantage words (`host:unapproved`, `no-turn-reader`, `unmeasured`). A test rejects any code outside the list, so no raw error reaches a reader.

Build-derived cells (`harness.reader`, the interrupt table, `scope`, `bus.publish_scope`) print `declared`, distinct from a checked value.

## 5. Fields

**First build**

| field | kind | source | clock | valid_until |
|---|---|---|---|---|
| `role`, `team`, `harness`, `host` | standing | session store | spawn or respawn | next respawn |
| `alive` | checked | heartbeat age; never activity | `LastHeartbeat` | 45 s |
| `pane.alive` | checked | `ListPanes`, once per tmux session | the call | 30 s |
| `harness.version` | checked | statusline (seat-reported, pattern-gated) for claude; the watchdog verdict for others | the read | next read |
| `harness.covered` | checked | the version against the pattern ranges, at read time | the read | next read |
| `harness.reader` | declared | `composer.ReaderFor` | marvel build | |
| `interrupt.key`, `empty_c_c_exits` | declared | the measured table in finding-064, with build and range; `-` for claude and outside the range | measurement build | |
| `scope` | declared | the manifest, only if a seat cannot write its own value | apply time | next apply |
| `bus.publish_scope` | declared | `Publish` and `Subscribe` copied into a new value; `describe` only | render time | next render |
| `asks.open`, `asks.unacked`, `asks.oldest_open_s`, `asks.coverage` | checked | `QueueSource`, read through per team and role; a reported gap prints `?` | the ledger's pass | 2 x its cadence |
| `working`, `free` | | printed as `-` with a code, never omitted | | |

**Later, each behind a named dependency**

| field | waits on |
|---|---|
| `mail.oldest_age`, `mail.last_ack_at`, `mail.source` | the watcher's mail reader (#800, A1 to A3) |
| `turn.*`, `activity` | the watcher's turn reader (#800, B and C) and #801 |
| `login.state` | #801 |
| `held` / `blocker_class` | the watcher's record (#800, D), class only |
| `bus.last_ack_sent_at` | a per-seat key the seat writes into its own subtree |
| `egress.seat` | a ruling on an unauthenticated reachability check from the seat's sandbox |

**Never:** pane or dialog text; credentials, key ids or the `Principal` type; `gh auth status` output; mail bodies or subjects; an ask's `line` or `links`; environment variables, config contents or credential-store paths; raw error strings; a single score; a rollup fresher than its oldest input; per-instance ask counts; ring claims; mail depth as health.

## 6. The routing rule

1. Candidate: `role` matches and the declared `scope` covers the job's paths.
2. `alive` is necessary and never sufficient. Every other cell the job needs must be a fresh value; `-` or `?` means ask first, never "can".
3. A cell with a host vantage proves the host, not the seat.
4. Once `mail.oldest_age` exists, order by it; `asks.open` is shown and never sorts. Until then, no ordering.
5. With no free candidate, say so with the reasons. Never borrow an idle seat outside its scope.

## 7. Plan (filed as flat tickets after the operator's pick)

| id | ticket | blocked by |
|---|---|---|
| R1 | `internal/register`: row type, pure builder, `declared` word; golden render of `-`, `?` and `declared`; no cell ever prints `ok`, `free` or `ready` | none |
| R2 | Import test: fails on `github.com/nats-io/`, `internal/bus` and `Principal`, and on a nonzero `go list` exit; a planted control by path and a transitive control; a reflect test that no row field is named like a secret | R1 |
| R3 | `register` resource on the `get` RPC, same dispatch as `get sessions` | R1 |
| R4 | `marvel get register`: one line per role with its oldest `observed_at` and its count of `-` seats, `--seats` for rows; `working -` shown, not omitted | R3, R14 |
| R5 | `describe session` register block | R3 |
| R6a | Identity cells | R1 |
| R6b | `alive` and `pane.alive`; a stale beat and a persisted pre-restart beat both render `?` | R1 |
| R7 | Forward the statusline `version`: pattern `^\d+\.\d+\.\d+$`, its own `observed_at`, a missing key leaves the stored value untouched; first step captures a current-build fixture | none |
| R8a | `harness.version` cell | R1, R7 |
| R8b | `harness.covered`, with edge tests at the range bound | R8a |
| R9a | `harness.reader` | R1 |
| R9b | Interrupt table with build and range; claude `-` | R1 |
| R10 | `bus.publish_scope`; a sentinel password never appears on any surface | R5, R2 |
| R11a | Finding: the manifest schema and who can write `scope` | none |
| R11b | `scope` cell | R1, R11a |
| R12a | `QueueSource` and `asks.*` from a fixture double; a planted `line` and `links` never appear | R1 |
| R12b | A reported gap prints `?`; `observed_at` is the ledger's pass | R12a |
| R13a | Director: the ledger output carries `observed_at` and the reader's cadence | (director) |
| R13 | `QueueSource` over director's ledger output; a stopped-reader fixture prints `?` | R12a, R13a, director A5 |
| R14 | The reason vocabulary and its test | R1 |
| R15 | Pull file: temp file created 0600 in the destination, a directory the daemon user owns, rename, the same keys as the RPC and no more, `not-run since T` when stale | R3, R14 |
| R16 | Per-team rollup over rows for #601; passes ask counts through | R4, R6a, R12a |
| R17 | `mail.*`, clock from the broker read | R1, #800 A3, W1 |
| R18 | `turn.*`, `activity` | R1, #800 B1, B2, C5, #801 |
| R19 | `login.state` | R1, #801 |
| R20 | `held` / `blocker_class` from the watcher's record; a missing record prints `-`, never "no dialog" | R1, #800 D3 |
| R21 | claude interrupt-key probe, an operator-run script with coded PASS/STOP checks on a scratch claude | none |

R1 and R2 open first, so the import fence exists before any field that touches the grant. R7, R11a and R21 open the same day; they share no files with R1.

## 8. Decisions for the operator

1. **The design.** The panel recommends it, 6 of 6. Alternatives:
   - wait to build until #801 and the watcher's readers land;
   - put the fields in `get sessions` columns, beside the ACTIVE% and LAST-ACTIVE cells #801 breaks;
   - director-side only, which would leave director holding per-seat facts it cannot back without marvel.
2. **Default view (4-2).** One line per role with `--seats` (recommended), or a row per seat under a role header. The dissent's concern, that a role line hides the one stale seat, is met by R4's role line printing its oldest `observed_at` and its count of `-` seats.
3. **The director ask reader's grant (A5).** It decides whether the queue cells ever leave `-`.

The recommendations in this section are valid until 2026-10-23 or the operator's ruling, whichever comes first; the architect re-checks them then.

## 9. Open premise

Whether the daemon socket authorizes anything per RPC is unchecked. The register adds no authorization of its own and inherits whatever `get sessions` has; the supervisor control plane design owns that question.

## 10. Prior art

- #800 (the watcher), #801, #581, #601 and `docs/design/team-load-rollup.md`, `docs/design/get-sessions-output.md`.
- director `sim/requirements.md` R-11, R-27, R-115, R-116, R-195; `sim/design/ask-ledger.md`.
- marvel `_kos/findings/finding-064-composer-contract-three-harnesses.md`.
