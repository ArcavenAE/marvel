# finding-019: the codex rollout reader, and the hook that speaks to the model

- **Date:** 2026-08-09
- **Status:** captured. Reader built and verified end to end; three of
  finding-017's open questions moved, one of them closed against me.
- **Probe:** codex-cli 0.146.0 on macOS arm64. Independent re-census of
  209 rollout files (2098 `token_count` records); live hook capture under
  an isolated `CODEX_HOME`; app-server `hooks/list` interrogation;
  end-to-end run against a marvel daemon on an isolated socket and bolt.
- **bd:** aae-orc-pt8k (the channel), aae-orc-mob4 (the two failure modes
  this had to land with)
- **Builds on:** finding-017 (the channel verified and the exec stream
  refuted), finding-011 (the claude statusline feed this is modeled on)

## Summary

The reader is built and codex CTX% resolves. Four things came out of
building it that the design did not have going in:

1. **A codex hook's stdout is fed to the model.** A line printed by a
   SessionStart hook arrives in the rollout as a `developer` role
   `input_text` message. Porting ctx-forward's status line across would
   have made the context-pressure reporter a context-pressure source.
   `marvel codex-ctx` prints nothing.
2. **The tail ladder is cheap where it was feared and load-bearing where
   it was not.** At rest every one of the 207 files carrying samples
   resolves inside the first 64KB rung, worst case 9,909 bytes from EOF.
   The ladder earns its place mid-turn, which is when a tailing reader
   actually runs.
3. **Hook trust did not close, and two plausible escapes are refuted.**
   The mechanism is now mapped rather than guessed, and the shipped
   design does not need the bypass flag, but automation still does.
4. **Two of the sweep's tailing questions dissolve rather than resolve.**
   Taking the path from the hook payload on every fire, and holding no
   file handle between fires, removes what `.zst` replacement and a new
   file on compaction would have broken.

## The premise check

`rg rollout --type go` before this change hit comments in
`internal/usage/profiles.go`, `internal/usage/limits.go` and
`cmd/marvel/ctxforward.go` and nothing else. No code read a rollout file.
Codex CTX% rendered `-`. The premise held exactly as stated.

## Re-census: mob4's numbers, independently

I re-measured rather than inherited, over the same 209 files.

| quantity | mob4 | measured here |
|---|---|---|
| `token_count` records | 2098 | 2098 |
| `info == null` | 1 | 1 |
| compaction sentinels (`in==0`, `total>0`) | at every compaction | 16 |
| max `input_tokens` | (93.8% of window) | 242504 = 93.85% |
| records over the window, subsumptive | 0 of 2081 | 0 |
| distinct window values | 258400 only | 258400, 2097 declarations |
| largest single record | 1,776,483 B | 1,776,484 B |
| largest gap between samples | 1,792,874 B | 1,792,084 B |
| max EOF-to-last-sample | 9,107 B | 9,107 B |

The two byte figures differ by a newline and by 790 bytes respectively,
which is a difference in where each measurement puts the record boundary,
not a disagreement. Every rule mob4 asked for is the rule the corpus
supports.

Two additions the corpus gives for free:

- **`input_tokens == 0` with `total_tokens == 0` never occurs.** So
  discarding it costs nothing, and the reader discards it: zero occupancy
  and no measurement are indistinguishable in that record, and absence is
  the safe reading of the two.
- **No file's LAST `token_count` is a sentinel.** That is consistent with
  the sentinel sitting between the compacted record and the first
  post-compaction sample, and it means the sentinel trap is strictly a
  live-session hazard. A census of finished files would have reported it
  harmless.

## The tail ladder, measured

For each file I measured the distance from EOF back to the start of the
newest USABLE record, which is the quantity a tailing reader has to
cover.

| rung | files resolved |
|---|---|
| 64KB | 207 of 207 |
| 256KB, 1MB, 4MB | 0 more |

Worst case 9,909 bytes, 6.6x inside the first rung. Set against that, the
largest gap between consecutive samples is 1,792,084 bytes, which is 27x
the first rung. Both facts are true of the same corpus because they
describe different moments: at rest a sample is always near the end,
mid-turn a tool output can bury it. A fixed window is safe in the state
you can measure afterwards and unsafe in the state you measure from,
which is why the ladder is not optional and why measuring finished files
alone would have concluded that it was.

## The hook speaks to the model

This is the finding that changed the design.

`ctx-forward` prints a status line on every statusline tick, because
that is what a statusline hook is for: the string is rendered in the
pane for a human. Codex's hooks are not that. With a SessionStart hook
that printed one canary line, the line came back in the rollout as:

```json
{"type":"response_item","payload":{"type":"message","role":"developer",
 "content":[{"type":"input_text","text":"MARVEL-STDOUT-CANARY codex CTX 42%"}]}}
```

A `developer` role `input_text` item is model-visible input. A codex
forwarder written in ctx-forward's shape would inject its own reading
into the agent's context on every fire, for the life of the session, to
report on how full that context was getting.

`marvel codex-ctx` therefore writes nothing to stdout at all. A broken
feed shows as a gap in CTX% diagnosed through `marvel describe session`,
which is the same posture ctx-forward takes toward its own errors, one
step further.

## Hook trust: mapped, not closed

finding-017 marked this critical path, on the reasoning that a design
requiring `--dangerously-bypass-hook-trust` is not shippable. I did not
close it. What I established:

- **The gate is silent.** An enabled, untrusted hook does not run and
  nothing says so: no warning on stderr, no line in the rollout, no
  nonzero exit. The positive control is the same run with the bypass
  flag, where the hook fires. A person wiring this up gets no signal
  distinguishing "not trusted" from "misconfigured".
- **The state is observable.** The app-server method `hooks/list` returns
  per-hook `key`, `currentHash` (`sha256:...`), `trustStatus`
  (`untrusted`), `enabled` and `isManaged`. The key is
  `<config path>:<event>:<group index>:<hook index>`.
- **Three ways to pre-seed it do not work.** Writing
  `state = { enabled = true, trusted_hash = "<currentHash>" }` into the
  handler leaves `trustStatus` at `untrusted`, in both the `sha256:`
  and bare-hex spellings. The `state` table is inert in general: setting
  `enabled = false` there still reports `enabled: true`. Neither
  `bypass_hook_trust = true` in `config.toml` nor `-c
  bypass_hook_trust=true` on the command line reaches the flag's
  behavior, though the binary parses the name (it carries the message
  "`bypass_hook_trust` override must be a boolean").
- **The granting path is the TUI**, `startup_hooks_review.rs`, reached
  through the string "hooks need review before they can run". I could
  not exercise it: the probe home has no credentials and the TUI stops
  at its login screen, so where the accepted decision persists is still
  unknown.

**The shipped design does not need the flag**, and that is the part that
matters for pt8k. Marvel cannot install this hook anyway: codex reads
hooks from `$CODEX_HOME/config.toml`, and `auth.json` lives in that same
directory, so relocating `CODEX_HOME` to a marvel-owned tree would take
the operator's credentials away with it. The hook is therefore an
operator's one-time edit to their own config, and an operator can trust
their own hook through codex's own review. The flag remains the only
route for unattended installs, and that is the residue of the question.

## What the design does not have to solve

The sweep listed two hazards for a tailing reader. This reader is not a
tailer, and both dissolve:

- **`.zst` rollout compression.** `ReadOccupancy` opens the path fresh on
  every hook fire and holds no handle. A file replaced by a compressed
  sibling makes the open fail, which is a hold, which is the same answer
  as any other read failure. Whether compression actually replaces a live
  session's file is still unmeasured (no `.zst` exists on this host).
- **`SessionStart source:"compact"` opening a new file.** This would
  defeat `(dev,ino)` tracking. Nothing here tracks `(dev,ino)`; the path
  arrives on the payload each time, so a new file is followed for free.

Neither question is answered. Both are removed from this design's
critical path, which is a different and weaker claim.

## Verification

- **Unit:** 9 discard rows, the ladder's miss-then-grow with an assertion
  that the first rung really does miss, the torn trailing write, the
  no-sample holds, and the reading table. Plus a 10-row decision table
  over the forwarder.
- **Corpus:** the reader run over all 209 real rollout files. It produces
  a reading for 207 and `ErrNoSample` for the 2 that carry no
  `token_count` at all. Every window read is 258400; no percentage
  exceeds 100.
- **End to end:** a marvel daemon on an isolated socket and bolt file,
  one generic session, and a hook payload pointing at the compaction
  fixture. `CTX%` moved from `-` to `94%`, `ContextPercent`
  93.84829721362229, `ContextModel` gpt-5.6-sol, `ContextSource`
  heartbeat, and the command's stdout was empty. Firing again against a
  rollout carrying only a sentinel left the column at `94%`, which is the
  hold working where a zero would have been a lie.

The fixture is the case worth naming: its newest `token_count` IS the
compaction sentinel. A reader that took the newest record would have
reported 0% for a session at 93.8%.

## What was not established

- **Whether hook trust can be pre-seeded at all**, and where an accepted
  review persists. Three candidate mechanisms refuted, the granting path
  identified, the storage not found. Needs an authenticated interactive
  codex.
- **Whether the codex accumulator resets per turn or per session.**
  Untouched here; still needs an authenticated multi-turn
  `codex exec resume`. It does not change the treatment, since neither
  is a level.
- **Whether the window varies by model.** Untouched, and the corpus still
  cannot separate "keyed by model" from "one number for this account and
  plan". No table entry was added.
- **Whether `.zst` compression replaces a live session's file**, and
  whether a compaction opens a new rollout. Both removed from this
  design's critical path, neither answered.
- **The rung this window belongs on, at the seam.** The reader knows the
  window is a `stream` declaration (rung 1): it rides the same record as
  the level, in the artifact codex enforces compaction against. The
  heartbeat RPC carries no rung and no window, so `context_window` is
  emitted into a parameter the daemon drops, exactly as ctx-forward's is.
  Finishing that seam is the contract decision already named in
  `ctxforward.go`, and codex sharpens it rather than settling it.
- **Anything about a codex session that marvel did not spawn.** The
  command reads `MARVEL_SESSION` and friends from the environment, so a
  hand-run codex fires the hook and does nothing. That is intended, and
  it is also untested against a codex started by some other supervisor
  that happens to export those names.

## Cross-references

- `internal/runtime/codex/rollout.go`, `rollout_test.go`,
  `testdata/rollout-compaction.jsonl`
- `cmd/marvel/codexctx.go`, `codexctx_test.go`
- `internal/runtime/codex/mapping.md` (rollout section, gaps)
- `docs/user-guide.md` (Codex context pressure)
- finding-017 (the channel and the refutation this implements),
  finding-011 (the claude feed), finding-016 (effective windows)
