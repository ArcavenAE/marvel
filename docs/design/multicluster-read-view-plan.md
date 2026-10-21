# Multicluster read view: build plan and red tests

- **Status:** Proposal, 2026-10-21, for review. It changes no code. Ticket
  edges and bodies change only after this plan is reviewed.
- **Design it builds:** #816 (design, ruled by the operator on 2026-10-10;
  the ruling is the comment on #816). Seed: `_kos/ideas/multicluster-view.md`
  and `question-multicluster-view`.
- **How it was made:** a three-round design panel (distributed systems,
  credential custody, service operability, AI security, operator CLI UX, test
  architecture) saw the existing plan and improved it. Each code claim a
  panelist made was checked with one command; the results are in section 9.
- **Author seat:** architect, team migrated.
- **Code read at:** marvel `d0616f9`; `golang.org/x/crypto` v0.58.0.

## 1. Why this plan exists

The design in #816 is ruled and its fifteen slices are filed, but no slice
names the tests that prove it, and two of the facts the slices rest on have
moved. This plan gives the build an order, a dependency edge for each step,
and the red tests each ticket turns green, so a builder can start at the head
of the chain and a reviewer can check each PR against a written list.

Three findings shaped it:

1. **The per-call timer is not wired on SSH today.**
   `sshSessionConn.SetDeadline` returns nil (`internal/daemonrpc/client.go:372`),
   so the deadline `SendRequestWith` sets (`client.go:90-91`) does nothing on
   `mrvl://` or direct `ssh://`. The deadline is also set only after the dial
   returns (`client.go:86-91`), and `ClientConfig.Timeout` bounds only the TCP
   connect (x/crypto `ssh/client.go:204`, documented at `:321`). One cluster
   that accepts TCP and then goes quiet would hold a fan-out open with no
   bound. #761 is therefore inside this chain, and it is a timer armed before
   the dial that closes the connection, not a deadline.
2. **The client prompts for unknown host keys by default.** `sshClientConfig`
   uses `ModePrompt` (`client.go:341`) and reads `os.Stdin`
   (`internal/knownhosts/knownhosts.go:50-56`). Nothing in `cmd/marvel` sets
   strict mode. Parallel dials must never prompt; #830 owns that.
3. **No daemon text is filtered before it is drawn.** Session names go
   straight into a tabwriter row (`cmd/marvel/columns.go:165-183`), so a
   newline in a name can draw a row under another cluster's KEY. The filter
   (#823) lands before the first multi-cluster table.

## 2. What this plan does not change

Everything the operator ruled on 2026-10-10 stands: reads may span clusters
and every write names one cluster; `--cluster a,b` and `--all-clusters`; each
cluster resolves from its own config entry; `--identity` and `--socket` are
refused with more than one cluster; the command is refused inside a
marvel-spawned seat; bare keys on writes keep today's behavior, re-checked
when #830 ships; any key holding `:` reads as qualified; only the operator
runs the view until a read-only daemon permission level (#834) exists.

## 3. What the panel added

Agreed by all six seats:

- **Timer.** One constant, 10 s, over connect, handshake, auth, request and
  reply. Against the watch: interval 2 s (`cmd/marvel/main.go:1152`) <
  stale banner 5 s (`cmd/marvel/watchloop.go:8`) < timer 10 s, so the
  operator sees "late" before "gave up".
- **Writes cut by the timer.** If it fires before the request is written,
  nothing was sent. If it fires after, the result is unknown: exit 7 with
  "may have applied; check before retrying", never retried automatically.
- **A byte bound on every reply**, per cluster, in `internal/daemonrpc`
  beside the timer. Over the bound the outcome is `bad_reply` and `cause`
  names the limit. The bound's value is an operator step, set from the
  largest real reply.
- **Strict host keys in every fan-out dial**, never trust, never stdin.
- **Typed errors** for the outcome classes, so nothing is parsed from message
  text: unknown host key, host key changed (today a string,
  `knownhosts.go:86`), auth refused (x/crypto's refusal is a bare string,
  `ssh/client_auth.go:154`, so marvel sets `ClientConfig.AuthCallback`,
  `ssh/client.go:327`), and no identity.
- **#828 takes the identity from the named entry alone**, never the package
  `identityPath` it falls back to today (`cmd/marvel/main.go:146-149`).
- **#829 follows #830.** Until it lands, a key holding `:` is refused with one
  line.

### 3.1 Outcomes

The JSON field `outcome` is a closed set, pinned by a test:

| outcome | meaning |
|---|---|
| `answered` | the sessions call returned a result that parsed, within the timer and the byte bound |
| `starting` | the reply carried `Response.Starting` (`internal/daemonrpc/wire.go:59`); rows are shown and may be incomplete. A refusal sent while starting (`internal/daemon/daemon.go:1115-1117`) is `starting` with no rows |
| `same_daemon` | skipped or merged; `same_daemon_as` names the entry that answered |
| `unreachable` | connect refused, no route, or no such host |
| `no_answer_in_time` | the timer fired; `phase` is `connect`, `handshake`, `auth` or `reply` |
| `auth_refused` | the daemon rejected every key offered |
| `host_key_unknown` | no record; `fingerprint` is set |
| `host_key_changed` | a record exists and differs; `fingerprint` is set |
| `daemon_error` | the daemon answered with `Response.Error`; `cause` holds its filtered text |
| `bad_reply` | the reply did not parse, or passed the byte bound |

Every record also carries `cluster`, `cause`, `next`, `as_of`, and
`sessions` (a count, on `answered` and `starting`). A daemon that does not
report its build is `build: null` on an `answered` record, not an outcome.

The stderr line has one form: `marvel: cluster <name>: <outcome>: <cause>;
<next>`. A changed host key is named on its stderr line and in JSON every
time, including when the exit code is 11.

### 3.2 Exit codes

| code | meaning |
|---|---|
| 0 | every named cluster is `answered` or `same_daemon` |
| 1 | command error before any dial: usage, refused flag, seat guard, unknown cluster |
| 2-4 | `marvel wait` only, unchanged (`cmd/marvel/wait.go:18-26`) |
| 5 | the daemon answered and the answer is unusable (`daemon_error`, `bad_reply`); inside `wait`, 5 keeps its wider meaning |
| 6 | `unreachable` |
| 7 | `no_answer_in_time` (on a write: may have applied) |
| 8 | `auth_refused` |
| 9 | `host_key_unknown` or `host_key_changed` |
| 10 | partial: at least one cluster answered and at least one did not, or any `starting` |
| 11 | no cluster answered, and the causes differ |

One cluster, or every cluster failing for the same reason, exits with that
reason's code. A host-key failure is never 0.

### 3.3 Same-daemon merge (#831)

A second dial to an address already dialled is skipped. Otherwise two entries
merge only if (a) both are unix entries whose sockets resolve to the same
device and inode, or (b) both host keys were verified in strict mode, the
fingerprints match, and for `ssh://` the remote socket paths match too; and
in both cases the daemon-reported pid and start time agree. Daemon-reported
fields never suffice alone, because a hostile daemon can echo them. Equal
session sets are not a criterion, because two reads race any spawn between
them. The rows shown are the union; both entries keep a status line, the
second reading `same_daemon` and naming the first in config order. A missed
merge costs duplicate rows; a false merge would label one cluster's rows with
another's name and send a later write to the wrong place.

This replaces the ruled criterion, so it is ruling request R2 (section 8).

### 3.4 The filter (#823)

One function handles every daemon-supplied string drawn to a terminal (table
cells, status lines, stderr errors and notes, events, the watch frame), before
width is measured. It replaces each C0 (U+0000 to U+001F, including tab, LF
and CR), DEL, C1 (U+0080 to U+009F), each Unicode format character (category
Cf, which covers the bidi controls and the zero-width characters) and each
invalid UTF-8 byte. In KEY's workspace and name parts it also replaces the
separator lookalikes U+FF1A, U+2236, U+A789, U+FE55, U+2215, U+2044, U+FF0F
and U+29F8. Nothing is dropped silently. JSON output and `capture` stay raw.
A key holding the placeholder is refused as a handle and points at `-o json`.
The placeholder is never `:`, `/` or `?`; which character it is remains open
(R3, section 8).

## 4. The chain

All fourteen tickets are one chain, in this order. #812's typed errors, #761's
timer and #826's agent close all edit `internal/daemonrpc/client.go`, and
nearly every other ticket edits `cmd/marvel/main.go`, so parallel PRs off
main would conflict (rule (d), `code-restructure-plan.md` section 4). Each
link is based on the one before and merges in order. When another feature's
PR lands on main first, the current link merges main in; nothing is rebased
or force-pushed. Six open PRs from other features touch `cmd/marvel/main.go`
today: #968, #970, #977, #979, #990, #992.

In the table, (r) is a real dependency and (c) is file order under rule (d).
Both are filed as bd `blocks`, so the ready list shows only the chain's head.

| link | issue | bd | work | depends on |
|---|---|---|---|---|
| 1 | #812 | aae-orc-lsa0u | no usage text on connection errors; typed errors; the exit table | none |
| 2 | #828 | aae-orc-fyhx1 | resolver takes the cluster (and its identity) as an argument | lsa0u (c) |
| 3 | #761 | aae-orc-9mo6w | timer armed before the dial that closes everything; reply byte bound | lsa0u (r: the timeout and bad-reply types), fyhx1 (c) |
| 4 | #826 | aae-orc-u2bvg | close the SSH agent connection after the handshake | 9mo6w (r: the timer also closes it) |
| 5 | #823 | aae-orc-qeuth | the filter | u2bvg (c) |
| 6 | #827 | aae-orc-73704 | stable sort with a full tiebreak | qeuth (c) |
| 7 | #830 | aae-orc-jkb6u | `get sessions` across clusters | lsa0u, fyhx1, 9mo6w, u2bvg, qeuth, 73704 (r) |
| 8 | #824 | aae-orc-ioqwd | write result lines name the cluster | jkb6u (c) |
| 9 | #829 | aae-orc-z885x | qualified keys, typed `--cluster` on writes | fyhx1, ioqwd (r) |
| 10 | #825 | aae-orc-nprld | a future stamp reads `?` with a reason | z885x (c) |
| 11 | #831 | aae-orc-lfb76 | same-daemon merge | jkb6u (r), nprld (c) |
| 12 | #822 | aae-orc-2ihk2 | single-cluster watch: sequence discard of stale replies | 9mo6w (r), lfb76 (c) |
| 13 | #832 | aae-orc-w5ik7 | `get sessions -w` across clusters | 2ihk2, jkb6u, lfb76 (r) |
| 14 | #833 | aae-orc-xj69u | `get teams` and `events` across clusters | jkb6u, w5ik7 (r) |

Not in the chain: #834 (read-only permission level; unscheduled, its own
ruling) and #835 (workspace name rule; its own change).

**Edge changes against today's tracker.** Remove: `jkb6u` on `z885x`,
`jkb6u` on `nprld`, `2ihk2` on `u2bvg`. Add every edge in the table that
does not exist yet. Today's tracker has no edges into `lsa0u`, `9mo6w`,
`u2bvg`, `qeuth`, `73704` or `fyhx1`.

**Ticket bodies to correct when the edges change.**

- #826 cites `internal/daemon/daemon.go:3253`; the agent dial is now
  `internal/daemonrpc/client.go:430-433` (the client moved in #908 and #909).
- #828 cites `cmd/marvel/main.go:91`; the resolver is `resolveDaemonRung` at
  `cmd/marvel/main.go:92`.
- #822 asks for one fetch in flight per cluster. Before #830 there is one
  cluster, so #822 is the single-cluster sequence discard; the per-cluster
  form moves to #832.
- #827 sorts on cluster, workspace and name. Before #830 a row has no
  cluster, so #827 sorts on workspace and name with a full tiebreak, and #830
  puts cluster first.
- #761 gains the reply byte bound and the rule that the timer starts before
  the dial.

## 5. Red tests

Each test fails on main at `d0616f9` (or does not compile), its failure
output goes into its PR, and it needs only its ticket and the tickets that
ticket depends on.

Rigs:

- **U**: a unit test, no socket.
- **F**: a fake daemon, a JSON-RPC responder on a unix socket in a temp
  directory (the pattern in `cmd/marvel/attach_test.go:180-224`). It cannot
  test deadlines or host keys: a unix socket honors `SetDeadline`, so a timer
  test on F passes on main.
- **S**: a minimal SSH server built in the test with x/crypto
  `ssh.NewServerConn` on `127.0.0.1:0`. It imports no daemon code, so it can
  live in `internal/daemonrpc`.
- **L**: the in-process `mrvl://` listener (`StartMRVL`, as in
  `internal/daemon/remote_client_test.go`).

| link | ticket | red tests (what each asserts) | rig |
|---|---|---|---|
| 1 | #812 | `TestConnectErrorPrintsNoUsage`: a dead socket gives one line, no `Usage:`, exit 6. `TestExitCodeTable`: each typed error maps to one code. `TestWaitCodesUnchanged`: `wait` keeps 0 to 5. `TestUnknownHostKeyIsTyped`, `TestChangedHostKeyIsTyped`: `errors.As` finds each through the handshake. `TestAuthRefusedIsTyped`: set through `AuthCallback`; `Offered` is a fingerprint. | F, U, S |
| 2 | #828 | `TestResolveTakesClusterArg`: two clusters resolve side by side, and `MARVEL_SOCKET`, `--socket` and `-i` leak into neither. `TestResolveIdentityFromEntryOnly`: the package `identityPath` never reaches a named cluster. | U |
| 3 | #761 | `TestTimerBoundsSilentBanner`: TCP accepted, no banner; returns within the timer plus 2 s. `TestTimerBoundsSilentSession`: handshake done, no reply; same bound. `TestTimerFreesEverything`: no goroutine or file descriptor left after it fires. `TestOversizedReplyIsBadReply`: a reply past the bound fails with the bad-reply type, and its cause names the limit. | S, F |
| 4 | #826 | `TestAgentConnClosedAfterCall`: a fake agent on a scratch `SSH_AUTH_SOCK` counts zero open connections after the call returns, and zero after the timer fires. | S |
| 5 | #823 | `TestFilterReplacesC0C1DELFormat`. `TestNewlineCannotForgeRow`: a name holding `x\nother:ws/evil\trunning` draws exactly one row. `TestPlaceholderAddsNoSeparator`: for any input, the output holds no more `:`, `/` or `?` than the input. `TestSeparatorLookalikesReplacedInKeyParts`. `TestDaemonErrorFiltered`: an error text holding ESC prints filtered. | U, F |
| 6 | #827 | `TestSortStableOnWorkspaceName`: tied rows keep their order over 100 shuffled inputs. | U |
| 7 | #830 | `TestFanOutOneOutcomePerCluster`: three fakes (answers, absent, hung) give three status lines and three records, exit 10. `TestFanOutNoneAnswered`: mixed causes, exit 11. `TestAllFailSameCauseExitsThatCode`: one subtest each for 5, 6, 7, 8 and 9. `TestFanOutOneHungClusterBounded`. `TestFanOutIsParallel`: three hung clusters finish under twice the timer. `TestFanOutNeverReadsStdin`: an unknown host key, stdin a pipe holding `y`; the pipe is unread and known_hosts is byte-identical. `TestFanOutChangedKeyDistinct`. `TestFanOutRefusesIdentitySocketSeat`: zero connections. `TestFanOutCallsOnlyReadMethods`: the methods called are within a written allowlist. `TestColonKeyRefusedUntil829`. `TestStartingReplyIsPartial`: rows shown, exit 10. `TestFanOutSortsClusterFirst`. `TestKeyColumnSurvivesNarrowWidth`, `TestKeyColumnWithExplicitColumns`, `TestKeyColumnInPipe`. `TestOutcomeEnumIsClosed`. If R1 is ruled: `TestFanOutOffersOnlyEntryKey`. | F, L, U |
| 8 | #824 | `TestWriteResultNamesCluster`: kill, delete, inject, scale and shift name the cluster, or the socket on the socket rung (`cmd/marvel/header.go:44`). `TestWriteCutByTimerSaysMayHaveApplied`: exit 7. | F, S |
| 9 | #829 | `TestQualifiedKeySplitsAtFirstColon`. `TestFullwidthColonIsBare`. `TestQualifiedWriteNeedsMatchingCluster`. `TestKeyHoldingPlaceholderRefused`. | U, F |
| 10 | #825 | `TestFutureStampReadsQuestion`: a pinned clock gives `?` and the reason; a missing stamp reads `-`. | U |
| 11 | #831 | `TestSameAddressDialledOnce`. `TestSameInodeMerges`: two paths, one a symlink to one fake. `TestEchoedVersionNotMerged`: a fake echoes another's pid and start time from a different inode. `TestSharedHostKeyDifferentSocketNotMerged`. `TestMergeNeverHidesStatusLine`. | F, S |
| 12 | #822 | `TestStaleReplyDiscarded`: single cluster; fetch 1 blocks, the timer frees the slot, fetch 2 shows, a late fetch 1 is dropped. | U (the watch loop's seams) |
| 13 | #832 | `TestStaleReplyDiscardedPerCluster`. `TestOneSlowClusterDoesNotBlankWatch`: today the first-answer wait blanks the whole frame (`cmd/marvel/main.go:3296`). `TestEachClusterAgesOnItsOwnAsOf`. | U, F |
| 14 | #833 | `TestTeamsFanOutOutcomes`. `TestEventsSeatTextFiltered`. `TestEventsWatchTimerBoundsHandshakeOnly`. | F, S |

Timer and race tests take their order from channels, never sleeps; a timeout
test asserts an upper bound only; the merge gate for these packages is
`-race -count=20`. New test files in `cmd/marvel` run under its existing home
guard `TestMain`.

## 6. Reachability check

Each test was checked against its ticket and that ticket's dependencies:

- **#822 as filed was not reachable.** A per-cluster test needs a fan-out
  that #822 does not depend on. Fixed by moving the per-cluster form to #832.
- **#827 as filed was not reachable.** Sorting on cluster needs a cluster
  field that rows gain only in #830. Fixed by moving cluster-first to #830.
- **The byte bound had no owner.** Assigned to #761, which already owns the
  client exchange; #830 maps the error to `bad_reply`.
- **#812's host-key and auth types** need the S rig and nothing else.
- **`TestFanOutOffersOnlyEntryKey`** belongs to #830 only if R1 is ruled.
- Every other row is reachable from its ticket and the tickets it depends on.

## 7. Run conditions for every test, script and mutant

- A test that needs a second cluster uses a fake or in-process daemon in a
  scratch HOME. Nothing reaches a live cluster.
- Every run is under a `sandbox-exec` profile that allows writes only in the
  clone and scratch and network only to localhost, and that ends with:

  ```
  (deny network-outbound (remote ip "localhost:4222") (remote ip "localhost:4242")
                         (remote ip "localhost:7422") (remote ip "localhost:7442"))
  ```

  The S and L rigs listen on kernel-chosen ports, which the deny list does
  not touch.
- No `MARVEL_SOCKET` in the environment; socket paths come from a short
  `TMPDIR` to stay under the 104-character limit.
- #829's check of workspace names on every live cluster, and the reply byte
  bound's value, are operator steps whose output goes into the PR. Neither is
  a test.

## 8. Ruling requests

These are recommendations, not decisions. Each is valid until 2026-10-28; the
architect re-checks it then.

- **R1. Fan-out credentials.** Recommended (all six seats): during a fan-out,
  each cluster's dial offers exactly one key, the identity in that cluster's
  entry or marvel's own client key when the entry names none. It never uses
  the SSH agent or the user's SSH directory keys. A cluster with neither key
  loadable gets `auth_refused` before any dial. Today a fan-out would offer
  every agent key and default key file to every cluster (`client.go:404-449`).
  Cost: a cluster whose key lives only in an agent (a hardware key) reads
  `auth_refused` in a fan-out. A follow-on, as its own ticket after #830: an
  `identity` that names a `.pub` file selects only the matching agent signer.
- **R2. Same-daemon merge criterion.** Recommended (all six): the rule in
  section 3.3, replacing "same start time and pid, and the same sessions".
- **R3. The placeholder.** The panel split 2-2-2. U+FFFD: one rune, so
  width stays in the cell's unit, and it cannot be mistaken for typed text.
  A visible ASCII escape such as `\x1b`: it tells the operator which byte was
  there and stays ASCII in any terminal or log. No recommendation; #823 needs
  the answer before it starts.
- **R4. Scope of the `starting` exit.** Recommended: exit 10 for `starting`
  applies under `--cluster a,b` or `--all-clusters` only, and a plain
  single-cluster `get sessions` keeps today's exit so existing scripts do not
  change. (One seat's scoped vote; the other four voting for `starting` did
  not address the plain command.)
- **Carried from 2026-10-10:** the bare-key-on-writes ruling is re-checked
  when #830 ships.

## 9. Claim checks

Panel claims checked with one command each at `d0616f9`. Two did not hold
and were corrected in the panel: `ClientConfig.Timeout` covers the handshake
(false: TCP connect only), and `cmd/marvel/main.go` has one `DialOptions`
literal (false: ten; none sets strict mode, which was the point). One of the
architect's own checks was wrong and was corrected by a panelist: the wire
does carry a starting signal (`Response.Starting`). The rest held, including
every anchor cited in this plan.
