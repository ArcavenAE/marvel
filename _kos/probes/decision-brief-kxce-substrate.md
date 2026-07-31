# Decision brief: marvel substrate + attachment (aae-orc-kxce)

**Status:** RATIFIED by the operator 2026-07-31 (marvel roadmap-triage session).
Recorded as a marvel-native decision (ratified brief + bedrock node
`elem-tmux-session-substrate`); marvel records load-bearing decisions in the
charter/graph rather than a separate ADR dir, which the orc uses.
**Date:** 2026-07-31
**Inputs:** finding-004-shim-in-pane-spike (aae-orc-e35c),
finding-005-stream-attachment-probe (aae-orc-3cp). Both measured on kinu,
macOS 26.5.2, tmux 3.7b, against Claude Code 2.1.220, codex 0.146.0,
crush 0.67.0, opencode 1.18.5.
**Decides:** aae-orc-kxce (substrate) and the attachment half of
question-stream-attachment.

## The question, split

kxce asked "is tmux the right substrate, or just the first?" The two findings
show the question was really two:

1. **Attachment** — how marvel observes agent output. Answered by measurement,
   and the answer does not constrain the substrate.
2. **Substrate** — what owns the terminal and the process. Answered by a
   boundary: tmux is load-bearing for terminal emulation and the human view,
   and not load-bearing for process lifetime or supervision.

## What the evidence settled

**Observation never required owning the PTY.** The old stream-attachment node
ruled out the PTY-owned strategy on the circular ground that tmux was bedrock.
finding-005 measured the paths instead: a FIFO carries a harness's own bytes at
100% coverage, zero loss, ANSI intact, at 2.6M lines/s, and every target
harness except Crush emits newline-delimited JSON in headless mode. The FIFO
path already shipped in wave 1 (PR #76, the Instance stitch), and a real
claude -p stream-json turn was observed end to end through it. So marvel can
already observe agents today, on the current tmux substrate, with no shim.

**The shim-in-pane candidate has no blocker.** finding-004 built candidate (e)
and passed all five pre-declared signals: byte-identical render of real claude
and codex under the shim, 25 MB/s stream tee with zero loss, race-free
concurrent inject with no send-keys, survival of pane loss with the child alive
and controllable, and the TTY-hang falsification. Cost: 25-40µs added round
trip, one dependency (creack/pty), and two named bounds (drain grace, lag
ceiling) that want surfacing before real agents ride it.

**Replacing tmux and owning the PTY directly (candidates b/g) has direct
evidence against it, cheaply.** Signal 5h: a shim running headless with pipe
stdio gives its child a real TTY, and a terminal-capability query still hangs,
because a PTY is necessary but not sufficient. Something must answer DA1-class
queries, and that responder is open-ended per harness and per harness version
(tmux 3.7b answers DA1, declines the kitty query). Whoever drops tmux takes on
being a terminal emulator, and the falsification set is where that cost turns
from arguable to visible. finding-065's Cursor TTY-hang is the same class in
the wild.

**In-house multiplexing (candidate d) wins nothing the shim does not** and
costs more; recommend graveyarding it with this reasoning.

## Recommendation

**Substrate: tmux stays, as the terminal-emulation and human-view layer.
Adopt the shim-in-pane (candidate e) as the process-supervision and
injectable-tee layer, phased in when a concrete need pulls it — not now.**

The near-term demo does not need the shim: the FIFO attachment path already
observes agents on plain tmux. The shim earns its place when one of these
becomes load-bearing:

- race-free inject into a live agent (the send-keys race, aae-orc-qtkj, is the
  cheap stopgap until then),
- exact per-child process metrics and lifetime control without pane-pid
  guessing (it hands marvel the child PID directly, wait4/rusage for free),
- carrying structured and rendered output from one process (the one path that
  could beat the FIFO; unmeasured until the shim lands in the daemon).

So: keep (a) status quo for the demo, commit to (e) as the direction, reject
(b)/(g), graveyard (d), and keep (f) Go-control-mode as a reference-only note
(tmux-cmc is Rust; unusable from Go without breaking the zero-CGo posture).

**Attachment binding, per harness** (none requires owning the PTY):

| Harness | Binding | Confidence |
|---|---|---|
| Claude Code | structured stream over FIFO (`-p --output-format stream-json`) | verified end to end |
| Codex | structured stream over FIFO (`codex exec --json`) | flags + TTY verified; live turn not exercised |
| OpenCode | structured stream, server-first (`serve`+`attach`, or `run --format json`) | flags verified |
| Crush | FIFO capture of opaque text + `.crush/crush.db` for tokens; probe its unix socket first | no structured mode exists |
| Gemini CLI | structured stream over FIFO, GATED on re-verifying #9009/#13561 | graph-only, not installed |
| unknown / non-cooperative | `capture-pane` history scrape, `-S -` + high-water mark, publish ceiling = visible_rows × poll_hz | measured capacity rule |
| adopted (marvel didn't launch it) | `pipe-pane` (byte-faithful, strip CR) | measured |

## If ratified, the harvest

- Promote the boundary ("tmux: terminal yes, supervision no") into marvel
  bedrock; supersede the circular PTY rule-out in question-stream-attachment
  with the measured result.
- Write an ADR (substrate direction) since this is a load-bearing architecture
  decision, per ADR-007's "ratified, written decision" bar.
- Close kxce; open a follow-on for the shim's daemon integration (promote the
  spike's cmd/marvel-shim from draft PR #75 when a puller need lands).
- Two shipped-code bugs finding-005 flagged become tickets: `Driver.CapturePane`
  is the visible-region variant (0.48% coverage) where the generic adapter
  wants `CapturePaneRange` + high-water mark; nothing sets `history-limit`, so
  scrapes inherit the 2000-line default (20% coverage vs 100%).

## What would change the read

A Linux re-run (B13: PTY and signal behavior differ) and one shim inject
exercised under the alternate screen + bracketed paste, neither of which the
spike covered.

(Cursor CLI verification was named as a gap in finding-004; it is void as of
this ratification. Cursor is out of the target set entirely following its
SpaceX acquisition, so its TTY-hang, cited as the falsification class, remains
a valid mechanism example but is no longer a harness marvel supports. The
target set is Claude Code, Codex, Gemini CLI, Crush, OpenCode.)
