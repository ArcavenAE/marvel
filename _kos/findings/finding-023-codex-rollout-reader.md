# finding-023: the codex rollout reader, and the hook that speaks to the model

- **Date:** 2026-08-09
- **Status:** captured. Reader built and verified end to end. Three of
  finding-017's open questions moved and one closed; the ladder-rung
  question it touched is open with the operator (marvel PR #172).
- **Probe:** codex-cli 0.146.0 on macOS arm64. Independent re-census of
  209 rollout files (2098 `token_count` records); live hook capture under
  an isolated `CODEX_HOME`; app-server `hooks/list` interrogation;
  end-to-end run against a marvel daemon on an isolated socket and bolt.
- **bd:** aae-orc-pt8k (the channel), aae-orc-mob4 (the two failure modes
  this had to land with)
- **Builds on:** finding-017 (the channel verified and the exec stream
  refuted), finding-011 (the claude statusline feed this is modeled on)

## Summary

The reader is built and codex CTX% resolves. Six things came out of
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
5. **The reader orders by the record's own timestamp, not by position in
   the file.** Codex has never been seen writing them out of order, but
   Claude Code does, and the absence measured here is too thin to lean
   on for one comparison's worth of cost.
6. **The accumulator is session-scoped, settled from data already on
   disk.** finding-017 said this needed an authenticated multi-turn
   run. It needed a discriminator, which probe-0tnf supplied: 159 turn
   boundaries crossed with zero decreases.

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

## Position is not time

probe-0tnf measured a property of the Claude Code transcript corpus that
bears directly on any reader taking "the last record in the file": 26
records across 2 of 422 sessions carry a timestamp OLDER than a record
physically before them, worst case 90,895 seconds (25.2 hours), and they
cluster immediately before a compaction boundary. One such record read
134,037 where the session's true level was 967,915. That is the same
failure direction as the compaction sentinel, arriving through a
different door.

I tested codex for it rather than assuming either way. Over 210 rollout
files, across every record type and not only samples: **zero
inversions**, and every timestamp parsed. In no file does the last
usable sample in file order differ from the newest usable sample by
timestamp.

The reader orders by timestamp anyway. The absence measured here does
not carry much: a rate like Claude's 2-in-422 would produce no inversion
across 210 files roughly a third of the time, so this corpus cannot
distinguish "codex does not do this" from "codex does it rarely and I
did not catch it". Against that, the fix is one comparison in a loop
that already holds both records. A record whose timestamp does not parse
falls back to file order, so an undatable record neither wins nor loses
silently; that branch has never fired on codex.

Re-run over the corpus after the change: every reading identical, max
level still 218,755.

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

**The general rule, which is crush-channel's and belongs here because
codex supplies the trap.** A hook's stdout is a per-harness contract, so
a hook body does not port across harnesses. Crush's is the inverse of
codex's and worse: exit 0 means stdout is parsed as a JSON envelope, and
`decision: "allow"` is affirmative pre-approval that bypasses the
permission prompt and beats no-opinion when hooks aggregate. A reporter
hook must omit that field on Crush and print nothing on codex, and the
two failure modes are opposite, one polluting context and one granting
permissions.

The clause that makes it operational is the collision that invites the
port in the first place: **this holds even when both harnesses call the
thing a hook and share event names.** Codex's vocabulary is
`PreToolUse`, `PostToolUse`, `SessionStart`, `SessionEnd`, `PreCompact`,
`PostCompact`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop`,
`Stop`, `PermissionRequest`. Read that list beside Claude Code's and a
shared hook body looks obviously correct. The event names match; the
stdout contracts do not.

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

## Two things settled with the sibling arms

**The rung: codex is `stream` and settled. Crush is neither rung, and
goes to the operator.** This took five exchanges, the answer moved three
times, and twice it moved by one arm deferring to the other rather than
to evidence. The route matters more than the destination, so it is
recorded whole.

**Codex first, because it is the part that is actually decided.**
`limitLadder`'s rung-1 sentence is two conjuncts: the harness stating the
window it is currently enforcing compaction against, AND doing so "in the
same channel as the token counts it is stating it about". Codex satisfies
both. `model_context_window` rides the record carrying the level, in the
artifact codex compacts against. It is the case the ladder was written
to describe, and nothing below disturbs it.

I first read limitLadder's asymmetry as turning partly on transport, and
said so. crush-channel refuted that: Crush's window comes from a separate
REST call (`GET /v1/workspaces/{id}/agent`, measured 40960 for a locally
discovered model) whose number Crush's own auto-summarize condition
actuates against, so a separate round trip can still be the window the
harness enforces. Transport is not the test, and I withdrew it.

They then measured the thing that decides it. Crush's model catalog is
keyed provider-first: 141 of 249 multi-provider model ids disagree with
themselves on `context_window`, 52 of them by 1.5x or more, with
`claude-opus-5` at 1000000 under anthropic against 264000 under copilot.
The REST route reports the workspace's CURRENT agent, so it cannot say
which window applied to a past message even in principle. On that
evidence they offered `feed` and I take it.

I proposed attributability next, from the ladder's clause about the
harness stating its window "in the same channel as the token counts it is
stating it about", and read Crush's separately fetched window as failing
it. crush-channel then withdrew their own concession, correctly: they had
imported historical answerability from a different question, and CTX% is
a live reading that nothing in the ladder asks to answer about past
messages. Codex's window would fail that test too if asked about a
message from a session that has since changed models.

I then argued Crush up to rung 1 on the ladder's stated PURPOSE:
overruling a rung-1 declaration with a manifest value "would make
marvel's denominator disagree with the one that actually governs the
session's behavior", and Crush's auto-summarize actuates against the
number its route returns, so the harm is identical. crush-channel
declined to accept that, correctly, and did the check neither of us had
done: they tested rung 4's definition against their channel instead of
only rung 1's. It fails on three of four properties. Rung 4 is written
about "a side channel read opportunistically off a human-facing status
hook, with no version handle and no statement of which of the six
effective-window axes it reflects". Crush's route is a documented
first-class API, no hook and nothing human-facing, carrying a `/v1`
prefix and a version endpoint. Only the axes clause partly holds.

So the ladder's text was drafted with two channels in view, a harness
stream and a statusline hook, and Crush's route is a third shape it does
not describe. Each of us had been quoting the clause that fit our own
conclusion: I took the conjunct rung 1 fails, they took the paragraph's
closing summary that rung 1 passes, and neither of us tested the
destination.

**That makes it a ruling, not a derivation.** The ladder is a ratified
artifact and the ruling was the operator's on 2026-08-08; the evidence
for revisiting it did not exist then, and two arms are not the authority
to amend it. crush-channel routes it in marvel PR #172 with two
candidates and no preference: relax the same-channel conjunct so a
contracted live query qualifies for rung 1, or add a rung between
manifest and feed for a contracted, versioned, live query that is
neither the stream nor a status hook. I support that and add no third
candidate.

Two things are decidable meanwhile and both are recorded there. **`feed`
is the worse of the two placements on consequence**: rung 4 sits below
`LimitFromManifest`, so it lets a hand-written window outrank the live
route, and the router study measured the window as a provider-plus-model
property where the hand-written number is the model's headline value and
wrong by up to 3.8x. Demoting promotes the more likely error. And **the
residue of my own objection is not fixed by any rung**: the race between
fetching a window and reading a level under a changed model is treated
only by the refetch rule, in crush-channel's strong form, where a window
fetched under a different model is **unresolved** rather than stale. That
rule holds at whichever rung the operator picks, and codex needs none of
it, because its window is re-read with every level.

**Codex's model name cannot reach the accountant's primary-model latch,
and that is now checked rather than assumed.** probe-0tnf measured the
latch's cost on claude: `fold` latches the first model it sees and never
re-latches, so after a permanent model switch 3,548 samples across 19
sessions route to spend while the occupancy level FREEZES, worst case
frozen at 79,246 while the session reached 751,169. `codex-ctx` forwards
a model name, so the question is live rather than academic. It does not
reach it: `handleHeartbeat` calls `store.UpdateSessionHeartbeat`, which
writes `sess.ContextModel` and nothing else; the accountant is fed only
by `sampleFromEvent` off the adapter event stream, and codex's stream
names no model. Inert by two independent facts rather than by luck.

## The accumulator is session-scoped, and the data was already on disk

finding-017 left open whether codex's accumulator resets at a turn
boundary or runs for the session, and said it needed an authenticated
multi-turn `codex exec resume`. It did not. probe-0tnf supplied the
discriminator while reporting a Gemini result: a session accumulator can
never decrease, so one decrease anywhere in the series settles it.

Applied to the rollout corpus, which carries `total_token_usage` beside
every level and is the same accumulator `turn.completed` mirrors field
for field:

| measurement | result |
|---|---|
| consecutive-pair comparisons | 1890 |
| decreases in `total_token_usage.total_tokens` | **0** |
| turn boundaries with a sample on both sides | 159 |
| decreases at a turn boundary | **0** |
| later-sample records where total equals last (the reset signature) | 1, and it is a duplicate |

A per-turn accumulator drops at every turn boundary. This one crosses
159 of them, across 9 multi-turn sessions carrying up to 45 turns each,
without dropping once. **The accumulator is session-scoped**, which is
the worse of the two cases finding-017 named and the one
`internal/usage/profiles.go` already declares as `CumulationSession`.

probe-0tnf reproduced this against the same 208 files and reported one
`total == last` where my first pass reported none, which is worth
stating because the reconciliation is the definition rather than the
data. Every session's first sample has `total == last` trivially (208 of
them), so only later samples can carry the reset signature. Exactly one
does, and it is a duplicate: two byte-identical records 3.7 seconds
apart, both `total` 18690 and `last` 18690. My pass excluded it through
a `prev != v` guard that happens to filter duplicates; theirs counted it
and then characterized it. Same data, no reset either way.

The honest limit: this measures the rollout's own accumulator. That
`turn.completed` mirrors it was established on a single-turn fixture, so
whether the mirroring survives a turn boundary is the one step still
needing an authenticated multi-turn `exec`. The declaration in
profiles.go is now supported by measurement rather than by an
unverified note, which is a different thing from proven.

Worth naming because it is the second instance in this arc: finding-017
closed its own refutation with a fixture already sitting in the repo,
after the question had been framed in a way that made the fixture look
unable to answer it. The same thing happened here. The framing "needs an
authenticated multi-turn resume" was true of the exec stream and false of
the corpus beside it.

## What was not established

- **Whether hook trust can be pre-seeded at all**, and where an accepted
  review persists. Three candidate mechanisms refuted, the granting path
  identified, the storage not found. Needs an authenticated interactive
  codex.
- **Whether `turn.completed` keeps mirroring `total_token_usage` across
  a turn boundary.** The accumulator's scope is settled above; this
  remaining step is the only part that still needs an authenticated
  multi-turn `codex exec resume`. It does not change the treatment,
  since neither scope is a level.
- **Whether the window varies by model.** Untouched, and the corpus still
  cannot separate "keyed by model" from "one number for this account and
  plan". No table entry was added.
- **Whether `.zst` compression replaces a live session's file**, and
  whether a compaction opens a new rollout. Both removed from this
  design's critical path, neither answered.
- **The seam, not the rung.** The rung is settled (see below); what is
  not is that the heartbeat RPC carries neither rung nor window, so
  `context_window` is emitted into a parameter the daemon drops, exactly
  as ctx-forward's is. Finishing that seam is the contract decision
  already named in `ctxforward.go`, and a second producer sharpens it
  rather than settling it.
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
- finding-020 (the Crush channel, merged as 16add0c): the per-feed
  cumulation point both arms reached independently, and the hook-stdout
  contract whose codex half is above. The rung question is marvel PR
  #172, open with the operator
- finding-024 (the compaction corpus, probe-0tnf): the ordering property
  and the primary-model latch measurement this reader is checked against
