# Probe brief: which deaf-seat mechanisms survive the shim's durable repair, and can the planned watcher read tell each one from a quiet seat?

**Status:** DRAFT 2026-10-24, not filed, not run. Written for the architect
from the idea `_kos/ideas/deaf-seat-status.md` (2026-09-27). Premises read at
marvel `origin/main` 9672ca5 and director `origin/main` 4dc2d0a (both local
checkouts were behind origin and were not used for reads).
**Question asked:** the idea proposes a `deaf` row for the harness-state
watchdog: a seat that is alive, working, and not hearing its bus. Is that row
still needed, and if so, what signal raises it?
**Medium:** reading marvel and director; a scratch broker and scratch hub
under a scratch HOME; `director-mcp` builds run as plain processes driven by a
scripted MCP client. No fleet broker, leaf or hub. No model calls in the tier-1
half.
**bd:** none filed for this idea. Related open items: aae-orc-jjhza (director:
a session that loses its global durable goes deaf) [measured] `bd sql --json`;
director#66 and director#269, both open [measured] `gh api`.

## 1. Question

With the shim now rebuilding a lost durable on both tiers, which mechanisms
still leave a seat that presents as live while it receives nothing, and does
the read the marvel watcher plans (JetStream consumer state per seat, both
tiers) distinguish each of them from a seat that is quiet because no mail was
sent?

## 2. Hypothesis

The idea's measured mechanism, a deleted durable, no longer produces a
permanent deaf seat on a current shim: the shim recreates the durable and
reports it. What remains is three cases: a seat on a shim built before the
repair, a seat whose shim is alive but whose harness has stopped polling, and
a seat whose `director-mcp` process is gone. The first is invisible to
director's unread reader, which lists durables and so has no row for a live
presence without one. The second is director's `durable-idle` state. The
third ends in presence expiry, so it reads as not live rather than as deaf. If
that holds, a separate `deaf` watchdog row is needed only for the first case,
and the watcher's planned rule that a missing durable reads `unknown` would
hide it.

## 3. Already built or designed

| Item | State | Evidence |
|---|---|---|
| Global durable recreate in the shim | merged 2026-09-25, two days before the idea | [read] director `ddf514f` "recreate a lost global durable instead of polling silence (#82)"; `cmd/director-mcp/global.go:710-747` |
| Local durable rebuild in the shim | merged 2026-10-19 | [read] director `dffbd87` (#328, for #269); `cmd/director-mcp/localrecover.go:1-9` |
| Recreate notice surfaced to the seat | built | [read] `cmd/director-mcp/bus.go:1202-1209`, `global.go:654` |
| Unread reader (the idea's "LR-3 reader") | built: slice S, plus slice M states `reading`, `behind`, `durable-idle`, `reads-outside-durable`, and `--global` over `GLOBAL_TO_*` | [read] `cmd/director-mcp/unread.go:1-15`, `:115`, `:287-292`, `:885`; `sim/design/unread-slice-m.md` M2, M3 |
| Unread reader row for a live presence with no durable | not found | [measured] `git grep` over `unread.go` finds only the reverse note, "no presence: this durable belongs to a session that is not live" (`unread.go:419`) [inferred: no such row] |
| marvel watcher (unread mail against the last turn) | designed and partly ruled; tickets A1 to G4 filed; only the claude probe kit (C1, #844) and its measurements (#849) merged; no `internal/mail` or `internal/watcher` on main | [read] `docs/design/marvel-watcher.md` status line, sections 3 and 7; [measured] `git ls-tree origin internal/` |
| Watcher rule for a missing durable | `internal/mail/jetstream` adapter: "missing durable is `unknown`", and any `unknown` makes the per-seat aggregate `unknown` | [read] `marvel-watcher.md` section 3 ("The mail half") and section 7 row A3 |
| Harness-state watchdog | P1 classifies a quiet pane as logged-out only | [read] `internal/daemon/watchdog.go` header comment |

The idea's Related line points at "LR-3 (an unread-age reader from consumer ack
floors)". On director main, LR-3 names the unread reader and also the `rev`
field in presence [read] `unread.go:3`, `cmd/director-mcp/main.go:28`; the
idea's reference resolves to slice S.

## 4. Method

Run conditions (operator ruling, 2026-10-14): `env -i` with a
scratch `HOME` and a short `TMPDIR`; no `MARVEL_SOCKET`; localhost only under a
`sandbox-exec` profile with a refused-connection control showing the
profile's own "Operation not permitted"; and the sandbox write proof (a
blocked write to a fixed path outside scratch, a successful write inside).
One scratch `nats-server` as the cluster broker and one as the hub, with the
`AGENT_INBOX` and `GLOBAL_TO_<cluster>` streams provisioned the way the shim's
own tests do (`startScratchServer`, `provision` in `unread_noconsumer_test.go`).

Two shim builds: director `origin/main` (current) and the parent of `ddf514f`
(before the global repair). Each runs as a plain process under a scripted MCP
client that calls `wait_for_message` on a fixed cadence, so no harness or model
is needed for tier 1.

For each case, send three messages to the seat, then record, at 0, 30, 60 and
120 s: the presence row (local and global), `director-mcp unread --json` and
`unread --global --json`, raw consumer info for the seat's durables, and the
client's poll results byte for byte.

| Case | Setup | What is recorded |
|---|---|---|
| D1 global, current | delete `mcp_global_<agent>_*` on `GLOBAL_TO_<cluster>` | time to recreate notice; whether mail arrives; unread rows |
| D2 local, current | delete `mcp_<agent>_*` on `AGENT_INBOX` | the same |
| D3 global, old build | D1 on the pre-`ddf514f` shim | whether the seat stays deaf; whether any unread row names it |
| D4 not polling | current shim, client stops calling `wait_for_message`, process alive | the state unread gives (`durable-idle` expected after its 10 min window) |
| D5 shim gone | kill the `director-mcp` process | presence expiry time (90 s TTL expected [read] `bus.go:341-342`, `askledger.go:22`); unread rows |
| Q quiet control | current shim, polling, no mail sent | every surface, to prove none of the reads above fire on it |

Tier 2 (pane strings per harness) is a separate half. It needs a real claude
and a real codex seat, and a scratch HOME de-authenticates claude
(finding-025, cited in `brief-multiple-clusters-per-host.md` section 1). It is
not run under this brief without an operator ruling on which run conditions
apply (open question 1).

## 5. Pass and fail

- **Supported** if D1 and D2 show a recreate notice and delivered mail within
  one poll window; D3 stays deaf with no unread row naming the seat while its
  presence stays live; D4 reads `durable-idle`; D5 reads not live once the TTL
  lapses; and Q is never flagged by any read.
- **Disconfirmed** if D1 or D2 stays deaf on the current shim (the repair does
  not hold, and the idea's original case stands), or if D3 is visible in an
  existing unread row (no new signal is needed), or if Q is flagged anywhere.
- The idea's own criteria carry over for the eventual watcher, not for this
  run: a deaf seat classified within one watcher interval after its first
  unread message, and a quiet seat never classified.

## 6. Scope out

- Building the watcher, a `deaf` row, or any restart or re-send on a signal
  (diagnostic only, ADR-007; the idea's own boundary).
- Any fleet broker, leaf, hub or live seat.
- Choosing the credential marvel reads consumer info with (the idea defers it
  to the bus-as-service design; director finding-007 section 5).
- Closing or editing director#66, director#269 or aae-orc-jjhza.

## 7. Open questions

1. Tier 2 needs logged-in harnesses. Is it run under the real HOME with marvel
   state moved by flag (the finding-025 recipe), or deferred? This is an
   operator ruling on run conditions.
2. If D3 is supported: is the fix a watcher rule (presence live and no durable
   is `deaf`, not `unknown`), a row in director's unread reader, or an upgrade
   check that refuses old shim builds by the presence `rev`? The watcher
   design and director's reader each own a candidate; this brief does not pick.
3. The watcher design's A3 rule ("missing durable is `unknown`") and the idea's
   tier-1 rule ("presence live and no durable is decisive") disagree. Which
   document is amended, and by whom?
4. director#66, director#269 and aae-orc-jjhza are open while their fixes are
   merged [measured]. Whether they are done is for their owners to confirm;
   this brief only reports it.
5. Does the subject test still place this in marvel, if the remaining case is
   a director shim version and director's reader can name it? The idea filed
   it in marvel because marvel is outside every seat.
