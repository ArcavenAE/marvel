---
id: finding-004-shim-in-pane-spike
title: "Shim-in-pane substrate candidate: prototype measured against five pre-declared signals"
probe: scripts/shim-spike.sh
question: question-substrate
confidence: frontier
tags: [marvel, substrate, pty, tmux, shim]
bd: aae-orc-e35c
addressed_to: aae-orc-kxce
provenance:
  created_by: agent
  session: marvel-wave-1-lane-s1
  created_at: "2026-07-31"
  host: kinu
---

# Shim-in-pane substrate candidate: prototype measured against five signals

Marvel is choosing what owns the terminal, and the leading candidate has never
been built, so its costs have been argued rather than measured. This spike
builds it: a small binary that runs inside a tmux pane, gives the harness its
own PTY, tees the harness's bytes to marvel over a Unix socket, and takes
control commands on a second socket. The result that matters for the decision
is a boundary, not a verdict: the shim is transparent enough that Claude Code
and Codex render byte-identically under it, and it survives losing its pane, so
tmux turns out to be load-bearing for terminal emulation but not for process
supervision.

Prototype quality. Nothing here is production code; the protocol is the
smallest thing that answers the five signals, and error handling is
spike-grade. This finding supplies input to `aae-orc-kxce`; the decision stays
with the operator per ADR-007.

Companion input: `finding-005-stream-attachment-probe.md` measured the
attachment paths and left the shim-PTY-tee row blank because this spike had not
landed. Signal 2 below fills that row.

## What was built

`cmd/marvel-shim` (~380 lines) plus `cmd/shimprobe` (~470 lines of test
children and test clients) and `scripts/shim-spike.sh` (the driver, one
subcommand per signal). New dependency: `github.com/creack/pty` v1.1.24.

The shim is the OS parent of one harness process. It allocates a PTY for that
child, inherits its own stdio from the tmux pane, seeds the child's window size
from the pane, and then runs one loop: read the child's PTY master, write those
bytes to the pane, and hand the same bytes to every stream subscriber. The
control socket takes newline-delimited JSON (`status`, `signal`, `stop`,
`inject`); `inject` writes to the PTY master, so injected bytes are
indistinguishable at the child from bytes typed in the pane.

Launch shape, as pre-declared:

```
tmux new-window 'marvel-shim --control C.sock --stream S.sock -- claude'
```

## Method

Host kinu, macOS 26.5.2 (arm64), tmux 3.7b, Go 1.25.4, Claude Code 2.1.220,
codex-cli 0.146.0. Every tmux session in the driver runs on a private server
socket (`tmux -L shimspike`) so the operator's sessions are untouched.
Reproduce with `scripts/shim-spike.sh all`.

Where a signal could fail for a reason that is not the shim's, the driver runs
the same test without the shim as a control. That is what turned two of the
five results from "the shim hangs this" into "this hangs either way."

## Signal results

### Signal 1 (PASS): render, input, and resize through the double PTY

The size-aware child (`shimprobe winsize`) reported `rows=30 cols=100` at
launch, then `rows=40 cols=120` and `rows=20 cols=70` after two
`resize-window` calls. SIGWINCH is not chained by the kernel between two
independent PTYs; the shim re-reads its own tty on SIGWINCH and pushes the size
down, and that path works in both directions of resize.

vim under the shim reported `COLS=100 LINES=30`, then `COLS=132 LINES=43` after
resize, having redrawn in between.

The stronger evidence is the two real harnesses. Launching `claude` in a plain
tmux pane and under the shim at the same geometry, then capturing both panes
with `capture-pane -e` (escape sequences included), produced byte-identical
captures: 1906 bytes each for Claude Code, 609 bytes each for Codex. That is a
claim about what the terminal was told to draw, not only about visible glyphs.
Both reflowed after a resize to 140x44.

Limitation: I verified render through `capture-pane` and through each program's
own reported geometry, not by a human eye on an attached client.

### Signal 2 (PASS, after fixing a defect): stream fidelity under fast output

50,000 numbered ANSI lines (2,250,032 bytes): 0 missing, 0 out of order,
0.149s. 300,000 lines (13,500,115 bytes): 0 missing, 0 out of order, 0.533s,
about 25 MB/s through the double PTY and the Unix socket. A consumer
deliberately slowed to 5ms per line also lost nothing.

The first run of the slow-consumer case lost 89% of the stream: 223 of 2,000
lines. Cause: child exit signalled subscribers and then let the process exit,
which killed the pump goroutines mid-queue. A fast consumer never saw it
because it drained before the child finished. Fixed by having close wait for
each subscriber to drain, bounded by a 10s grace. `cmd/marvel-shim/stream_test.go`
holds the regression test.

Two bounds remain, both by design and both worth naming for the decision: a
consumer slower than the 10s drain grace still loses the tail, and a subscriber
that falls more than 64 MiB behind gets its chunks dropped. Drops are counted
but the count is not yet reported on the control socket, so today that loss is
countable but not visible.

### Signal 3 (PASS, with a size bound): inject with no send-keys race

Two supervisor clients injecting concurrently on separate control connections,
300 lines each of 120 bytes: 300 clean A-lines, 300 clean B-lines, zero mixed
lines, zero wrong-length lines. Against a child in raw mode (what a real
harness does) the same test passed at 120, 900, 4,096, and 16,384 bytes per
line, twice at each of the larger widths. No interleaving corruption appeared
at any width.

Against a child in cooked mode the failure mode is truncation, not
interleaving: at 900 bytes 9 of 150 lines arrived short, and at 2,048 bytes
nothing arrived at all. That is Darwin's canonical-mode input limit
(MAX_CANON, 1024 bytes) discarding an over-long line, a property of the child's
termios rather than of the shim. Real harnesses set raw mode, so the practical
inject bound is generous, but marvel should not assume it is unbounded.

Inject also drove a real harness. Sending `2` and Enter through the control
socket answered Codex's and Claude Code's trust prompt with "No, quit" and both
harnesses exited, which is end-to-end proof that injected bytes reach a real
TUI's input handling.

One flake, undiagnosed: a single 900-byte raw-mode run reported zero echoed
lines; two immediate repeats and four later runs at larger widths all passed.

### Signal 4 (PASS, behavior documented): pane kill and supervisor disconnect

With the default `--on-hup=kill`, killing the tmux session forwards SIGHUP to
the child, the shim exits, and the control socket stops answering. Deliberate
and observable.

With `--on-hup=detach`, killing the tmux session leaves both the shim and the
child alive. The control socket kept answering at +2s and +7s while the child
went on writing to its PTY, because the shim ignores write errors on its own
now-dead stdout. The shim becomes a headless PTY parent that marvel can still
control and stream. This is the result with the most weight for the substrate
decision: the shim does not need its pane in order to keep supervising.

SIGKILLing both supervisor connections did not disturb the shim. A fresh
connection worked immediately and an injected line arrived on the new stream
connection while the pane kept rendering. One caveat: `status` still reported
`clients: 1` for the dead subscriber, because a dead subscriber is noticed only
on the next write. Client counts are stale until traffic flows.

### Signal 5 (PASS with a boundary): falsification, the Cursor-class TTY hang

The emulation is a prober that writes a terminal capability query and then
reads its reply with a timeout, reporting hang or reply. Seven cases:

| Case | Setup | Result |
|---|---|---|
| 5a | pipe, nothing behind it | HANG at 3s |
| 5b | plain tmux pane, raw stdin | reply in 34µs, `ESC[?1;2;4c` |
| 5c | shim in tmux pane, raw stdin | reply in 94µs, same bytes |
| 5d | plain tmux pane, cooked stdin | HANG at 3s |
| 5e | shim in tmux pane, cooked stdin | HANG at 3s |
| 5f | plain tmux pane, kitty query | HANG at 3s |
| 5g | shim in tmux pane, kitty query | HANG at 3s |
| 5h | shim headless, stdio are pipes, raw | HANG at 3s |

5a establishes the failure class is real on this host. 5b against 5c is the
central result: the shim passes the query up and the reply back down, so tmux
answers through it, at a cost of 60µs. 5d against 5e and 5f against 5g are the
controls that keep me honest: cooked stdin hangs either way (the child's own
line discipline holds a reply that never contains a newline), and tmux 3.7b
does not answer the kitty keyboard-protocol query either way. Neither is a
shim regression.

5h is the boundary and the most useful line in the table. Run headless with
pipes for stdio and no tmux, the child's stdin **is** a tty, because the shim
gave it one, and the query still hangs. The shim provides a PTY; it does not
answer terminal queries. Whatever terminal sits above the shim is the answerer.

Real Cursor CLI verification is future work. Cursor is not installed on this
host, so what I measured is the mechanism finding-065 describes, not the
product. The claim "Cursor works because a PTY-providing multiplexer sits
between marvel and the process" is consistent with 5a/5b/5h but is not verified
against the real binary.

## Measurements

Inject-to-observe round trip, same echo child both ways, 100 to 200 samples per
run, three runs:

| Path | p50 | p90 | max |
|---|---|---|---|
| one PTY, no shim, no sockets | 6-12µs | 10-18µs | 6.1-15.8ms |
| shim: control socket in, stream socket out | 34-48µs | 50-79µs | 243-559µs |

The shim adds roughly 25-40µs at p50. The single-PTY control had the larger
outliers, which I read as scheduler noise on a loaded laptop rather than a
property of either path. Throughput: about 25 MB/s with zero loss. Both numbers
are far below the cadence at which a harness paints a TUI, so latency is not a
reason to reject this candidate.

## macOS specifics encountered

- Unix socket paths are capped near 104 bytes on Darwin. `t.TempDir()` paths
  exceed it and `net.Listen` fails with `bind: invalid argument`. Tests use a
  short path in `os.TempDir()`, matching what the daemon tests already do.
- Canonical-mode input is capped at 1024 bytes per line (MAX_CANON) and the
  excess is discarded silently. This bounds inject for any child that has not
  set raw mode.
- tmux runs a pane command through `/bin/sh`, so the shim is not the pane's
  first process. Discovering the shim by `pgrep -f` finds the shell wrapper
  instead. I added `shim_pid` to the `status` reply rather than guessing from
  the process table, which marvel will want anyway.
- Child exit surfaces as EIO on the PTY master read, not EOF. The read loop
  treats any read error as end-of-child.
- Raw mode on the shim's own tty is load-bearing rather than cosmetic. Without
  it the pane's line discipline echoes and line-buffers, which reintroduces the
  5d/5e hang for every child.

## Recommendation, addressed to aae-orc-kxce

**Candidate (e), shim inside the tmux pane: no blocker found.** All five
pre-declared signals pass on this host. It gives marvel a byte-exact tee, a
race-free inject path, per-child control without `send-keys`, and it keeps
tmux as the terminal-query answerer that harnesses depend on. The costs I
measured are 25-40µs of added round trip, one new dependency, and the drain and
lag bounds named under signal 2.

**Candidates (b) and (g), shim or marvel replacing tmux and owning the PTY
directly: 5h is direct evidence against, and it is cheap evidence.** A PTY is
necessary but not sufficient. Something has to answer DA1-class queries, and
that responder is open-ended per harness and per harness version: tmux 3.7b
answers DA1 and declines the kitty query, and a harness that probes for a
capability nobody answers hangs rather than degrading. Choosing (b) or (g)
means marvel takes on being a terminal emulator, and the falsification set is
where that cost becomes visible rather than arguable.

**The detach result narrows what tmux is actually for.** Signal 4b shows the
shim surviving the loss of its pane with the child alive and the control socket
serving. So tmux is load-bearing for terminal emulation and for the human's
view, and it is not load-bearing for process lifetime or supervision. Any
candidate that argues from "we need tmux to keep agents alive" is arguing from
a property this spike shows tmux does not uniquely supply.

**If the operator picks (e), three things want doing before it carries real
agents:** surface the dropped-byte counter and stale client counts on the
control socket (today loss is countable but not visible), decide the drain
grace deliberately rather than inheriting 10s, and re-run this driver on Linux,
where the PTY and signal behavior differs enough that B13 applies.

**What would change my read:** real Cursor CLI verification, a Linux run, and
one harness exercised under inject while it is using the alternate screen and
bracketed paste, which none of these tests covered.

## Scope qualifier

Single host, single OS, single tmux version, development build, no release
verification (B12). Two harnesses of the four marvel targets. The falsification
emulates a mechanism rather than testing the product that named it. Every
number here is from a laptop under other load.
