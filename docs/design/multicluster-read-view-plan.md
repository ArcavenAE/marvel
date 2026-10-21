# Multicluster read view: build plan and red tests

- **Status:** Proposal, 2026-10-21, for review. It changes no code. Ticket
  edges and bodies change only after this plan is reviewed. All four ruling
  requests were ruled by the operator on 2026-10-21 (R1 to R4, section 8),
  and so were #761's two questions (Q1, Q2, section 8); no link waits on a
  ruling.
- **Design it builds:** #816 (design, ruled by the operator on 2026-10-10;
  the ruling is the comment on #816). Seed: `_kos/ideas/multicluster-view.md`
  and `question-multicluster-view`.
- **How it was made:** a three-round design panel (distributed systems,
  credential custody, service operability, AI security, operator CLI UX, test
  architecture) saw the existing plan and improved it. Each code claim a
  panelist made was checked with one command; the results are in section 9.
- **Author seat:** architect, team migrated.
- **Code read at:** marvel `d0616f9`; `golang.org/x/crypto` v0.58.0. Every
  line anchor in this plan is at `d0616f9` (section 5 lists the ones that
  have since moved on main).

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
  reply, set per cluster in the fan-out. #761 builds the timer and sets no
  deadline of its own; #830 arms it at 10 s for each cluster, and a
  single-cluster command keeps today's behavior with no deadline (Q1,
  section 8). One exception: on `events --watch` the timer bounds the dial and
  handshake only, since the stream is meant to stay open
  (`TestEventsWatchTimerBoundsHandshakeOnly`, #833). Against the
  multi-cluster watch (#832), the only watch that arms the timer under Q1
  (a): interval 2 s (`cmd/marvel/main.go:1152`) < stale banner 5 s
  (`cmd/marvel/watchloop.go:8`) < timer 10 s, so the operator sees "late"
  before "gave up". A single-cluster watch has no timer; its 5 s view
  deadline only raises the stale banner.
- **Writes and the timer.** Under Q1 (a) the timer never cuts a write.
  Every write names one cluster (section 2), single-cluster commands keep no
  deadline, and today no write sets `DialOptions.Timeout`: its only caller
  that sets one is `cmd/marvel/accountlimits.go:120` (at `363e7bb`). The
  rule below waits on a write getting a deadline, which Q1 (a) does not give
  and which would need its own ruling: if the timer fires before the request
  is written, nothing was sent: exit 7 with "nothing sent; safe to retry". If
  it fires after, the result is unknown: exit 7 with "may have applied; check
  before retrying", never retried automatically.
- **A byte bound on every reply**, per cluster, in `internal/daemonrpc`
  beside the timer, single-cluster commands included. It is a trial: one cap
  for every reply, to see how it goes (Q2, section 8). Over the bound the
  outcome is `bad_reply` and `cause` names the limit. The value comes from
  the link 3 (#761) build; its basis is still open with the operator, and
  this plan names no number.
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
| 7 | `no_answer_in_time`. Under Q1 (a) only a fan-out read arms the timer, so a write never exits 7 from it. The two write messages wait on a write getting a deadline (section 3) |
| 8 | `auth_refused` |
| 9 | `host_key_unknown` or `host_key_changed` |
| 10 | partial: at least one cluster answered and at least one did not, or, under `--cluster` with two or more clusters or `--all-clusters`, any `starting` (R4). Because any `starting` gives 10 there, a set with a `starting` cluster exits 10, never 11 |
| 11 | no cluster answered, and the causes differ |

One cluster, or every cluster failing for the same reason, exits with that
reason's code. A host-key failure is never 0. Single-cluster use keeps
today's exit when the daemon is starting: `get sessions` with no cluster
flag, and `get sessions --cluster a` naming one cluster (R4).

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

This replaces the criterion ruled on 2026-10-10. RULED 2026-10-21 by the
operator (R2, section 8).

### 3.4 The filter (#823)

One function handles every daemon-supplied string drawn to a terminal (table
cells, status lines, stderr errors and notes, events, the watch frame), before
width is measured. It replaces each C0 (U+0000 to U+001F, including tab, LF
and CR), DEL, C1 (U+0080 to U+009F), each Unicode format character (category
Cf, which covers the bidi controls and the zero-width characters) and each
invalid UTF-8 byte. In KEY's workspace and name parts it also replaces the
separator lookalikes U+FF1A, U+2236, U+A789, U+FE55, U+2215, U+2044, U+FF0F
and U+29F8. Nothing is dropped silently. JSON output and `capture` stay raw.
The placeholder is U+FFFD (R3, section 8). Each replaced code point and each
invalid UTF-8 byte becomes exactly one U+FFFD, which is also what Go's own
decoding gives for an invalid byte, so filtering never changes a cell's rune
count. When anything was replaced, one line follows the output naming the
code points, for example `2 fields held undrawable characters (U+001B,
U+202E); -o json shows them`, so the operator still learns which characters
were there. A key holding U+FFFD, whether the filter put it there or the
daemon sent it, is refused as a handle and points at `-o json`.

Known cost, on the table when R3 was ruled: U+FFFD's East Asian Width is
"ambiguous" (Unicode 16.0), so a terminal set to draw ambiguous characters
wide draws it two columns, while the table counts one (`tableWidth` counts
runes, `cmd/marvel/fit.go:156-162`). On such a terminal a filtered cell draws
one column wider than measured.

## 4. The chain

All thirteen tickets are one chain, in this order. #812's typed errors, #761's
timer and #826's agent close all edit `internal/daemonrpc/client.go`, and
nearly every other ticket edits `cmd/marvel/main.go`, so parallel PRs off
main would conflict (rule (d), `code-restructure-plan.md` section 4). Each
link is based on the one before and merges in order. When another feature's
PR lands on main first, the current link merges main in; nothing is rebased
or force-pushed. Three open PRs from other features touch
`cmd/marvel/main.go` as of 2026-10-21: #970, #977, #979.

In the table, (r) is a real dependency and (c) is file order under rule (d).
Both are filed as bd `blocks`, so the ready list shows only the chain's head.

| link | issue | bd | work | depends on |
|---|---|---|---|---|
| 1 | #812 | aae-orc-lsa0u | no usage text on connection errors; typed errors; the exit table | none |
| 2 | #828 | aae-orc-fyhx1 | resolver takes the cluster (and its identity) as an argument | lsa0u (c) |
| 3 | #761 | aae-orc-9mo6w | timer armed before the dial that closes everything, with no default deadline (Q1); byte bound on every reply, a trial (Q2) | lsa0u (r: the timeout and bad-reply types), fyhx1 (c) |
| 4 | #826 | aae-orc-u2bvg | close the SSH agent connection after the handshake | 9mo6w (r: the timer also closes it) |
| 5 | #823 | aae-orc-qeuth | the filter | u2bvg (c) |
| 6 | #827 | aae-orc-73704 | stable sort with a full tiebreak | qeuth (c) |
| 7 | #830 | aae-orc-jkb6u | `get sessions` across clusters; arms the timer at 10 s per cluster (Q1) | lsa0u, fyhx1, 9mo6w, u2bvg, qeuth, 73704 (r) |
| 8 | #824 | aae-orc-ioqwd | write result lines name the cluster | jkb6u (c) |
| 9 | #829 | aae-orc-z885x | qualified keys, typed `--cluster` on writes | fyhx1, ioqwd (r) |
| 10 | #825 | aae-orc-nprld | a future stamp reads `?` with a reason | z885x (c) |
| 11 | #831 | aae-orc-lfb76 | same-daemon merge | jkb6u (r), nprld (c) |
| 12 | #832 | aae-orc-w5ik7 | `get sessions -w` across clusters, with #822's sequence discard folded in | jkb6u, lfb76 (r) |
| 13 | #833 | aae-orc-xj69u | `get teams` and `events` across clusters | jkb6u, w5ik7 (r) |

Not in the chain: #834 (read-only permission level; unscheduled, its own
ruling), #835 (workspace name rule; its own change), and #822 (aae-orc-2ihk2),
folded into #832 (director, 2026-10-21, section 8). Director closes #822
after this plan merges.

**Edge changes against today's tracker.** Remove: `jkb6u` on `z885x`,
`jkb6u` on `nprld`, `2ihk2` on `u2bvg`, `2ihk2` on `9mo6w`, `w5ik7` on
`2ihk2` (the last three because #822 leaves the chain). Add every edge in the table that
does not exist yet. Today's tracker has no edges into `lsa0u`, `9mo6w`,
`u2bvg`, `qeuth`, `73704` or `fyhx1`.

**Ticket bodies to correct when the edges change.**

- #826 cites `internal/daemon/daemon.go:3253`; the agent dial is now
  `internal/daemonrpc/client.go:430-433` (the client moved in #908 and #909).
- #828 cites `cmd/marvel/main.go:91`; the resolver is `resolveDaemonRung` at
  `cmd/marvel/main.go:92`.
- #822 asks for one fetch in flight per cluster, with the per-call timer
  freeing the slot. Under Q1 (a) only the fan-out arms that timer, so its
  whole job moves to #832, and #832's body gains #822's sequence discard.
- #827 sorts on cluster, workspace and name. Before #830 a row has no
  cluster, so #827 sorts on workspace and name with a full tiebreak, and #830
  puts cluster first.
- #761 builds the timer only and sets no default deadline: #830 arms it at
  10 s per cluster in the fan-out, and single-cluster commands keep today's
  behavior with no deadline (Q1). #761 also gains the byte bound on every
  reply, as a trial (Q2), and the rule that the timer starts before the
  dial. Its ask for a default `Timeout` in `send()` is answered by Q1 (a),
  not adopted.

Anchors here and in section 5 are at `d0616f9`. Four have moved on main at
`b9f33a8` (#1004 merged): `cmd/marvel/columns.go:165-183` is now `:178-196`;
`cmd/marvel/main.go:3296` is now `:3317`; `cmd/marvel/main.go:1152` is now
`:1153`; `internal/daemon/daemon.go:1115-1117` is now `:1118-1122`. The
`internal/daemonrpc/client.go` anchors have not moved. A ticket body cites
the anchor at the commit it is edited against.

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
| 5 | #823 | `TestFilterReplacesC0C1DELFormat`. `TestNewlineCannotForgeRow`: a name holding `x\nother:ws/evil\trunning` draws exactly one row. `TestPlaceholderAddsNoSeparator`: for any input, the output holds no more `:`, `/` or `?` than the input. `TestSeparatorLookalikesReplacedInKeyParts`. `TestDaemonErrorFiltered`: an error text holding ESC prints filtered. `TestEachReplacementIsOneFFFD`: each replaced code point and each invalid byte becomes exactly one U+FFFD, and the rune count is unchanged. `TestUndrawableNoteNamesCodePoints`: a name holding ESC and U+202E gives one note line naming `U+001B, U+202E` and pointing at `-o json`; clean input gives no note. | U, F |
| 6 | #827 | `TestSortStableOnWorkspaceName`: tied rows keep their order over 100 shuffled inputs. | U |
| 7 | #830 | `TestFanOutOneOutcomePerCluster`: three fakes (answers, absent, hung) give three status lines and three records, exit 10. `TestFanOutNoneAnswered`: mixed causes, exit 11. `TestAllFailSameCauseExitsThatCode`: one subtest each for 5, 6, 7, 8 and 9. `TestFanOutOneHungClusterBounded`. `TestFanOutIsParallel`: three hung clusters finish under twice the timer. `TestFanOutNeverReadsStdin`: an unknown host key, stdin a pipe holding `y`; the pipe is unread and known_hosts is byte-identical. `TestFanOutChangedKeyDistinct`. `TestFanOutRefusesIdentitySocketSeat`: zero connections. `TestFanOutCallsOnlyReadMethods`: the methods called are within a written allowlist. `TestColonKeyRefusedUntil829`. `TestStartingReplyIsPartial`: under `--cluster a,b` and under `--all-clusters`, rows shown, exit 10. `TestPlainGetSessionsStartingKeepsExit`: no cluster flag, today's exit (R4). `TestSingleClusterFlagStartingKeepsExit`: `--cluster a` naming one cluster, today's exit (R4). `TestFanOutSortsClusterFirst`. `TestKeyColumnSurvivesNarrowWidth`, `TestKeyColumnWithExplicitColumns`, `TestKeyColumnInPipe`. `TestOutcomeEnumIsClosed`. `TestFanOutOffersOnlyEntryKey`: two in-process listeners, a scratch `SSH_AUTH_SOCK` fake and a key at `~/.ssh/id_ed25519` under the scratch HOME; each listener sees one key, the entry's own, the agent gets zero connections and `~/.ssh/id_ed25519` is never offered (R1). `TestFanOutEntryWithoutIdentityOffersOnlyClientKey`: an entry that names no identity, with marvel's client key in the scratch marvel layout (`layout.DefaultClientKey()`, `internal/daemonrpc/client.go:418`), an agent on a scratch `SSH_AUTH_SOCK` and a key at `~/.ssh/id_ed25519`; the listener sees only the client key and the agent gets zero connections (R1). `TestFanOutNoIdentityIsAuthRefused`: an entry with no loadable key is `auth_refused` before any dial (R1). | F, L, U |
| 8 | #824 | `TestWriteResultNamesCluster`: kill, delete, inject, scale and shift name the cluster, or the socket on the socket rung (`cmd/marvel/header.go:44`). Held, not in #824's red set: `TestWriteCutByTimerSaysMayHaveApplied` (the fake receives exactly one request, then goes quiet; exit 7 and "may have applied") and `TestWriteCutBeforeSendSaysNothingSent` (the timer fires before the request is written; the fake receives zero requests; exit 7 and "nothing sent; safe to retry") wait on a write getting a deadline (section 3) and join the ticket that adds one. | F |
| 9 | #829 | `TestQualifiedKeySplitsAtFirstColon`. `TestFullwidthColonIsBare`. `TestQualifiedWriteNeedsMatchingCluster`. `TestKeyHoldingPlaceholderRefused`: a key holding U+FFFD, filtered or sent literally by the daemon, is refused and the refusal points at `-o json`. | U, F |
| 10 | #825 | `TestFutureStampReadsQuestion`: a pinned clock gives `?` and the reason; a missing stamp reads `-`. | U |
| 11 | #831 | `TestSameAddressDialledOnce`. `TestSameInodeMerges`: two paths, one a symlink to one fake. `TestEchoedVersionNotMerged`: a fake echoes another's pid and start time from a different inode. `TestSharedHostKeyDifferentSocketNotMerged`. `TestSameHostKeyAndSocketMerges`: two `ssh://` entries to one S server, same verified fingerprint, socket path, pid and start time, merge into one `same_daemon` line. `TestMergeNeverHidesStatusLine`. | F, S |
| 12 | #832 | `TestStaleReplyDiscardedPerCluster`, which absorbs #822's `TestStaleReplyDiscarded`: under `--cluster a,b`, cluster a's fetch 1 blocks, the timer fires and frees a's slot, fetch 2 shows, a's late fetch 1 is dropped, and b's rows and as-of are untouched. `TestOneSlowClusterDoesNotBlankWatch`: today the first-answer wait blanks the whole frame (`cmd/marvel/main.go:3296`). `TestEachClusterAgesOnItsOwnAsOf`. | U, F |
| 13 | #833 | `TestTeamsFanOutOutcomes`. `TestEventsSeatTextFiltered`. `TestEventsWatchTimerBoundsHandshakeOnly`. | F, S |

Timer and race tests take their order from channels, never sleeps; a timeout
test asserts an upper bound only; the merge gate for these packages is
`-race -count=20`. New test files in `cmd/marvel` run under its existing home
guard `TestMain`.

## 6. Reachability check

Each test was checked against its ticket and that ticket's dependencies:

- **#822 as filed was not reachable.** A per-cluster test needs a fan-out
  that #822 does not depend on, and under Q1 (a) a single-cluster watch has
  no timer to free a slot: today one fetch holds it until it returns
  (`watchLoop`, `cmd/marvel/watchloop.go`), so no stale reply can exist.
  Fixed by folding #822 into #832 (section 8).
- **#827 as filed was not reachable.** Sorting on cluster needs a cluster
  field that rows gain only in #830. Fixed by moving cluster-first to #830.
- **The byte bound had no owner.** Assigned to #761, which already owns the
  client exchange; #830 maps the error to `bad_reply`.
- **#812's host-key and auth types** need the S rig and nothing else.
- **#824's two write-cut tests are not reachable under Q1 (a).** No write
  arms the timer, so they could pass only through a test seam that proves a
  message no operator sees. They are held, named in #824's row, until a
  ticket gives a write a deadline.
- **`TestFanOutOffersOnlyEntryKey`**, **`TestFanOutEntryWithoutIdentityOffersOnlyClientKey`**
  and **`TestFanOutNoIdentityIsAuthRefused`** belong to #830 (R1 is ruled, both
  branches: the entry's key, or marvel's client key when the entry names
  none); all three need only the L rig and a scratch HOME.
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
- #829's check of workspace names on every live cluster is an operator step
  whose output goes into the PR. The reply byte bound's value comes from the
  link 3 (#761) build and goes into that PR with its basis; the basis is
  still open with the operator (Q2). Neither is a test.

## 8. Rulings

R1 to R4, and #761's Q1 and Q2, were RULED 2026-10-21 by the operator. No
link waits on a ruling.

- **R1. Fan-out credentials. RULED 2026-10-21 by the operator: yes.** During
  a fan-out, each cluster's dial offers exactly one key: the identity in that
  cluster's entry, or marvel's own client key when the entry names none. It
  never uses the SSH agent or the user's SSH directory keys. A cluster with
  neither key loadable gets `auth_refused` before any dial. Today a fan-out
  would offer every agent key and default key file to every cluster
  (`client.go:404-449`). Cost: a cluster whose key lives only in an agent (a
  hardware key) reads `auth_refused` in a fan-out. The follow-on, where an
  `identity` that names a `.pub` file selects only the matching agent signer,
  is its own ticket after #830; it is filed after this plan is reviewed.
- **R2. Same-daemon merge criterion. RULED 2026-10-21 by the operator: yes.**
  Merge two entries only on the same socket inode, or on a strict-verified host
  fingerprint (plus the socket path for `ssh://`), and a matching pid and start
  time. Show the union and keep both status lines (section 3.3).
- **R3. The placeholder. RULED 2026-10-21 by the operator, verbatim: "V4 A
  U+FFFD".** The panel had split 2-2-2 on vote V4: U+FFFD (Option A;
  Test-architecture, Security) against a visible ASCII escape such as `\x1b`
  (Option B; Operability, UX), with Custody and Distributed-systems
  abstaining. The operator had the full details of both options, including
  the East Asian Width cost, before ruling. The placeholder is U+FFFD, #823
  carries Option A's tests and the note line naming the code points (section
  3.4), and the chain order voted in V1 stands.
- **R4. Scope of the `starting` exit. RULED 2026-10-21 by the operator:
  yes.** Exit 10 for `starting` applies only under `--cluster a,b` or
  `--all-clusters`. A plain single-cluster `get sessions` keeps today's exit.
  The follow-up question, whether a single `--cluster a` counts as a cluster
  set, was RULED 2026-10-21 by the operator, verbatim: "re R4 (a)
  single-cluster behavoir for single clusters". So `--cluster a` naming one
  cluster keeps today's exit, and exit 10 on `starting` needs `--cluster`
  with two or more clusters, or `--all-clusters`.
- **#761 Q1 and Q2. RULED 2026-10-21 by the operator, verbatim:
  "#761 Q1 (a), Q2 apply to every reply but I may regret this so let's keep some kind of hint about the other options and we're just trying a single cap to see how it goes".**
  - **Q1 (a). Where the timer is set.** #761 builds the timer mechanism only.
    #830 sets 10 s per cluster in the fan-out. Single-cluster commands keep
    today's behavior with no deadline.
  - **Q2. Which replies the byte cap covers: every reply, as a trial.** One
    cap on every reply, single-cluster commands included. The options not
    taken, kept here as the hint the ruling asks for: a cap only on the
    fan-out path, where the timer is set; and a cap per verb. If the trial
    goes badly, those are the next options to put back on the table.
  - **The cap's value.** It comes from the link 3 (#761) build. Its basis is
    still open with the operator, so this plan names no number.
- **#822 folded into #832. RULED 2026-10-21 by director, applying the
  operator's Q1 (a).** Only the fan-out arms the timer, so only a fan-out
  watch can free a slot early and receive a stale reply. #822 leaves the
  chain, its test merges into #832's `TestStaleReplyDiscardedPerCluster`,
  and the 2 s < 5 s < 10 s watch line is scoped to #832. No new operator
  ruling was needed.
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
