# finding-019: the Crush context-pressure channel, and what perturbation turned out to be

- **Date:** 2026-08-09
- **Status:** captured. Channel measured; four recorded claims refuted; the
  perturbation question ruled; no adapter written, and §7 says why that is
  the right outcome.
- **Probe:** crush v0.88.1 (`build_id` `dkivfqpur08w`, go1.26.5,
  darwin/arm64) against ollama `qwen3:0.6b`, 40960 window. Isolated rig:
  own `CRUSH_GLOBAL_CONFIG`, `CRUSH_GLOBAL_DATA`, `CRUSH_CACHE_DIR`,
  project data dir, and socket. Live SSE capture, live REST, live CLI,
  live tmux TUI, one forced compaction.
- **bd:** aae-orc-k2mi (this work), aae-orc-dc1j (the option (c) ruling it
  serves), aae-orc-6c2r (the desk research it checks)

## Summary

Nine claims were checked. Four were refuted, four held with the mechanism
corrected, and one gap the desk research named as open is now closed by
measurement. Two channels and one hazard were found that the research did
not list.

The refutation that matters is the first one, and it inverts the ticket's
framing. Attaching to Crush's event stream does **not** register marvel as
a client of any session. The counter the research cited is bumped by a
separate write that a reader never makes. What the read *does* do is keep
a workspace alive past the point where Crush would reap it. So the
perturbation is real, but it is lifecycle coupling rather than
participation, and it points the other way: marvel's observer prevents the
harness's garbage collection instead of paying to be tolerated by it.

That decomposition is the ruling in §6. Perturbation is not a third
grading axis and not a general eligibility cost. It splits into a per-call
property and a spawn-time environment obligation, and marvel already has a
home for both. A genuinely third property did appear, but it is not
perturbation: enabling the channel publishes marvel's constructed spawn
environment, secrets included, to every other client of a host-shared
socket.

## 1. The premise check, and the correction that unblocks the design

**Refuted: "the interactive TUI is itself a client of that server."**

At v0.88.1 neither the TUI nor `crush run` registers a workspace by
default. I ran both in the rig against a live server and polled
`GET /v1/workspaces` throughout: empty for the whole of two headless runs,
and empty for a TUI session that sat at its ready prompt.

Both register when `CRUSH_CLIENT_SERVER=1` is in the process environment.
Measured for the TUI (workspace appeared within 8s of launch and carried
the session through four turns) and for headless (`crush run` in a fresh
directory took `/v1/workspaces` from 0 to 1 for the duration of the run
and back to 0 about two seconds after it exited).

This is better news than the claim it replaces. A channel that every
running crush turns on would have made marvel a reader of the operator's
own sessions. A channel gated on one environment variable is marvel
enforcement locus 1 verbatim: the adapter constructs the environment at
spawn, so marvel observes exactly the sessions it provisioned and nothing
else. It also answers, for this harness, the sweep's ruling 11 question of
who is entitled to decide. Marvel is not discovering a system of record.
It is turning on a feed in a subordinate process it launched.

**Held, with the version qualified:** the socket default is unchanged.
`crush --help` prints `unix://$TMPDIR/crush-<uid>.sock` at v0.88.1 as it
did at v0.88.0, the `/v1` prefix is intact, and `GET /v1/version` answers.
One caveat found by accident: the socket path is built from `TMPDIR`, and
when I pointed `TMPDIR` at a path long enough to exceed the ~104-byte
`sun_path` limit the server fell back to `/tmp/crush-501.sock` without
saying so. A marvel that derives the socket path rather than passing
`-H` explicitly will be wrong on long paths.

## 2. Occupancy is a level, including inside a multi-request turn

The research proved `prompt_tokens` a level from the series 28593, 28611,
28639. I re-ran the discriminator rather than inheriting it, and extended
it to the case the series could not reach.

Four consecutive single-request turns in one session, read off the SSE
`session` frames:

| turn | messages | requests in turn | `prompt_tokens` | `completion_tokens` |
|---|---|---|---|---|
| 1 | 2 | 1 | 28674 | 77 |
| 2 | 4 | 1 | 28692 | 82 |
| 3 | 6 | 1 | 28710 | 107 |
| 4 | 8 | 1 | 28728 | 180 |
| 5 | 10 | 1 | 28756 | 966 |
| 6 | 12 | 1 | 29438 | 179 |

A cumulative reading predicts about 57000 at turn 2 and about 114000 by
turn 4, both past the 40960 window this model runs in, so cumulative is
excluded by the second sample rather than by the fourth.

**The multi-request case, which the desk series could not decide.** Turn 7
made a tool call, so it issued two model requests, and the stream carried
one usage-bearing frame per request:

| frame | messages | `prompt_tokens` | `completion_tokens` |
|---|---|---|---|
| request 1 | 15 | 29465 | 216 |
| request 2 | 16 | 29582 | 185 |

Under a within-turn accumulator request 2 would read about 59047. It reads
29582, which is request 1 plus the tool result. Codex's defect (finding-017
§4) does not have a Crush counterpart: this is a level at both scopes.

**`completion_tokens` is a level too**, which the research recorded as
unproven and consistent with either reading. Turn 5 produced a long answer
at 966 and turn 6 a one-word answer at 179. A running total cannot fall.
The same fall appears inside the tool-calling turn, 216 then 185.

That second result is load-bearing beyond bookkeeping, because Crush's own
displayed ratio is `(CompletionTokens + PromptTokens) / ContextWindow`. If
completion had been cumulative, Crush's own actuation point would drift
upward over a session for a reason unrelated to occupancy. It does not.

## 3. The denominator, and the case the research said was written nowhere

`GET /v1/workspaces/{id}/agent` answers with `model.context_window` 40960
for `qwen3:0.6b`, alongside `is_busy`, `is_ready`, and the model id. The
research called locally discovered providers the gap, since ollama models
get their window at runtime and it lands in no on-disk catalog. The route
returns it anyway, so the gap is closed for exactly the case that motivated
it, and this host's daily driver is one of those models.

The rung is `stream`, not `table`: it is the harness's own declaration
about the session in front of it, read live, the same argument
finding-017 §2 made for codex. It is not on the SSE feed, so a Crush
reader is a feed plus a one-shot lookup rather than a FEED-2. Refetch on
model change, not per turn.

Nothing here needs multiplying. Crush publishes one window term and
applies its own compaction thresholds to it internally.

## 4. Compaction reports empty at the moment of maximum pressure

`POST /v1/workspaces/{id}/agent/sessions/{sid}/summarize` on a session
holding 28673 prompt tokens produced, on the next `session` frame,
`prompt_tokens` 0 with `summary_message_id` set. That confirms the
research's signature.

The part that is worse than recorded, and worse than the codex sentinel:

- **The zero persists.** It stayed 0 through the entire next turn while
  `is_busy` was true, and only became 29117 when that request completed.
  Between a compaction and the next completed request, which can be an
  idle overnight gap, the channel reports zero occupancy. Codex writes one
  all-zero record and its next record is real; Crush's zero is durable
  session state.
- **`completion_tokens` is not reset.** It read 698 across the whole zero
  window, so Crush's own ratio reports 698/40960, about 1.7%, for a
  session that in fact holds roughly 28k of system prompt plus a summary.
  The harness's displayed figure is wrong in the same direction and by the
  same mechanism.
- **`summary_message_id` is a has-ever-compacted marker, not an event.**
  It stays set afterward. Detecting a compaction crossing needs the
  `prompt_tokens` transition to zero or a change in the id's value.

Any Crush reader must discard the zero rather than fold it, exactly as
finding-017 required for codex, and must hold the previous reading rather
than emit zero. Both failure modes report LOW pressure at HIGH pressure,
which is the direction that silently disables rotation.

## 5. No token classes exist on this channel

The SSE stream carries exactly two token numbers, `prompt_tokens` and
`completion_tokens`. I grepped a full session capture for any cache,
input, output, or reasoning breakdown and found zero occurrences. The CLI
channel adds `total_tokens`, and on a live row it is exactly
`prompt + completion` (29117 + 606 = 29723).

Two consequences.

**The reasoning-subset trap has no Crush instance.** `RequestUsage.TotalMismatch`
sums In, Out and ReasoningOut; Crush publishes no reasoning term at all, so
a Crush `Total` would check `In + Out` against a vendor total computed the
same way. The trap that bit codex cannot arise here. Establishing that is
the point; assuming it would have been the error.

**Sweep ruling 1 cannot be satisfied for this harness.** That ruling says
the numerator is marvel's, computed from the token classes, never
harness-supplied. Crush supplies no classes. Its `prompt_tokens` is a
composition its source performs as `InputTokens + CacheReadTokens`, which
excludes cache-creation where marvel's additive layout includes it. So the
choice for Crush is Crush's composition or no reading, and the ruling needs
an explicit carve-out rather than a quiet exception. I did not measure the
composition itself: ollama returns no cache terms, so both readings predict
the same numbers here, and the window-bound discriminator that decided the
codex layout (finding-017 §3) cannot fire when the cache terms are zero.

## 6. The ruling: perturbation decomposes, and exposure is the real third property

The ticket asked whether perturbation becomes a third per-channel property
alongside consent grade and fidelity, or an eligibility cost inside
arbitration. Measurement says neither, because the thing being named is
three different things that behave differently.

**Measurement A: registration is a property of the call, not the channel.**

`attached_clients` sat at 1, the TUI's own count, while I held
`GET /v1/workspaces/{id}/events` open with a fresh `client_id`. Killing my
stream left it at 1. It went to 2 only when that same client also issued
`POST /v1/workspaces/{id}/current-session`, and fell back to 1 when the
stream was torn down. The POST refuses with `client not attached` unless
the stream is already open, so the two calls are ordered but distinct, and
a reader has no reason to make the second one.

The claim in the graph is that attaching registers marvel as a real client.
It does not. Registration is what declaring a current session does. Grading
the channel would have recorded the hazard against a set of calls only one
of which carries it.

**Measurement B: lifecycle coupling is real, and points the opposite way.**

With no observer, the workspace was reaped 10 to 15 seconds after its last
client exited (`/v1/workspaces` length 1, 1, 0 on a 5-second poll). With a
marvel-shaped observer holding only the events stream, the same teardown
left the workspace alive for the full 90 seconds I watched, with zero real
clients.

So the hazard is not that marvel must pay to keep its reading alive. It is
that marvel's reading keeps alive a workspace Crush had decided to collect,
holding that workspace's config, its environment, and whatever LSP and MCP
clients it owns. For marvel-spawned agents, an observer that outlives its
agent is a leak marvel creates. That is a per-channel correctness bug
needing a test, which is precisely where the sweep's ruling 2 carve-out put
it before anyone had measured it.

**Measurement C: the host-global side effect is a spawn-time obligation.**

Starting a server refreshed `~/.local/share/crush/hyper.json` (content
changed) and touched `providers.json` (mtime moved, content identical).
Reproduced. `CRUSH_GLOBAL_DATA` does **not** relocate it: with that
variable pointed at the rig and auto-update left on, the write still landed
on the host path and the rig data directory got no providers file at all.
`CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1` does prevent it; on the control run
both hashes and both mtimes held.

So this is not a property of the channel either. It is one variable in the
environment the adapter constructs, at the cost of a provider catalog that
stops refreshing, which for a marvel-pinned local model is not a cost.

**The ruling.** Perturbation is not a third grading axis and not a general
eligibility cost. It decomposes into:

1. A per-call property, declared by the adapter alongside the ordered
   channel list it already owns, and tested per channel where the
   harness's lifecycle keys on observation.
2. A spawn-time environment obligation, which belongs to enforcement locus
   1 and needs no new machinery at all.

**And ruling 6's proposed boolean is refuted by the same measurement.**
The sweep suggested one field, `ObservationRegisters`, so the fallback
chain could prefer a non-registering channel. For Crush that boolean is
unanswerable: false for the read, true for a write no reader makes, and
either way it names the wrong hazard, since what needs handling is
workspace liveness rather than a counter. A boolean whose first real
instance cannot be set is not a field, it is a guess with a type.

**What is genuinely third, and it is not perturbation.**
`GET /v1/workspaces` serves the registering client's full process
environment as `env`. Seventy-six entries on my run, including
`BEADS_DOLT_PASSWORD` and both `SSH_AUTH_SOCK` paths. In a marvel
deployment that block is the environment marvel constructed at spawn, so
turning the channel on publishes marvel's own credential material to every
client of a socket that is shared across all workspaces on the host.

Consent asks who permitted the read. Fidelity asks how true the number is.
This asks what enabling the channel makes readable by someone else, and
none of the three predicts it. It is the sweep's blast-radius property
landing on a surface nobody anticipated: not "can this channel hang the
harness" but "does enabling this channel widen the credential surface."
SOUL §3 makes that a boundary rather than a risk to manage, and the
consent axis had placed the contracted socket as far from that boundary as
it is possible to be.

The measured mitigation is partial. Socket mode is `srwxr-xr-x`, and BSD
semantics require write permission to connect, so other users cannot reach
it. Every process running as the operator can.

## 7. Two channels the research did not list, and why no adapter is written

**`crush session show <id> --json` and `crush session last --json`.**
`crush session --help` states that agents can use `--json` for
machine-readable output. `session last --json` returns the newest session
with a `meta` block carrying `prompt_tokens`, `completion_tokens` and
`total_tokens`, so it needs no session id at all; `session show` takes one.

Measured properties: it reads the project database directly, returns
correct current numbers when pointed at a nonexistent socket with `-H`,
spawns no server, creates no workspace, leaves `attached_clients`
unchanged, and needs no `CRUSH_CLIENT_SERVER`. It is contracted by the
vendor's own documented flag, and it is the only channel in this catalog
with no measured side effect of any kind.

It is poll-only and it carries no denominator, so it does not replace the
socket. It does mean that a Crush reader could be built with no server
involvement at all, at the cost of a window marvel would have to get from
the manifest.

**Why the evidence does not support writing a parser yet.** Marvel has no
Crush runtime adapter (`internal/runtime` holds claude, codex, opencode,
forestage, simulator and generic), so there is no place to declare the
channel or construct the environment. The channel is HTTP and SSE over a
unix socket, not the harness stdout that `internal/runtime/fifo.go` and
`stream.go` are built around, so it needs a transport that does not exist.
Adding a `crush` profile to `internal/usage` with no producer would be a
claim about a design that does not exist, which is the discipline
finding-017 followed when it declined to add the `feed` rung. The adapter
belongs to aae-orc-6c2r; this finding is its input.

## What changed in the code

Two shipped statements are now measured false, and both assert the
opposite of what this probe found.

- `internal/usage/doc.go` scope paragraph: "Crush publishes no structured
  stream (its token counts live only in a per-repo sqlite database)."
  Crush publishes a structured SSE stream carrying a per-request occupancy
  level, and two documented JSON CLI surfaces besides. The scope decision
  is unchanged (no Crush profile ships) but the reason is replaced with
  the real one.
- `internal/usage/sample_test.go`: the assertion that crush has no profile
  is kept, since none should ship yet, but its failure message asserted
  the same false reason. It now states the reason that holds.

No behavior changes. No profile is added.

## What was not established

- **Whether `prompt_tokens` composes as input plus cache-read.** Source
  says so; ollama returns no cache terms, so every reading predicts the
  same numbers on this rig and the window-bound discriminator cannot fire.
  A prompt-cached Anthropic or Bedrock provider decides it.
- **Whether hooks fire without a trust step.** Crush's embedded docs
  describe hooks as a plain `hooks` block in config with no approval
  ledger, and the binary carries no trust or bypass flag, which is already
  a contrast with codex's `--dangerously-bypass-hook-trust`. I could not
  confirm firing: `qwen3:0.6b` would not reliably call a tool on demand,
  and I did not separately verify that a hook declared in
  `CRUSH_GLOBAL_CONFIG` is read at all. The pointer the hook would carry
  (`CRUSH_SESSION_ID`) is in any case redundant now that
  `session last --json` needs no id.
- **Whether an observer also prevents the server's idle shutdown.** I set
  `CRUSH_SERVER_IDLE_TIMEOUT=3600` for the rig, so the 60-second default
  never ran. Same mechanism as the workspace reap, so I expect it, and
  expecting is not measuring.
- **The reap timeout's actual constant.** Bracketed between 10 and 15
  seconds on a 5-second poll, not read from source.
- **Whether `attached_clients` on the SSE payload is maintained at all.**
  It read 0 on every `session` frame while the REST endpoint reported 1
  for the same session at the same moment. Either a stale field on the
  event or a different denominator; I did not chase it, and a consumer
  should read the counter from REST rather than the frame.
- **Multi-workspace behavior.** One server served one workspace at a time
  throughout. The research notes that one socket sees every Crush agent on
  the host, which is the case where the `env` exposure above compounds,
  and I did not exercise it.

## Probe hygiene disclosure

Before the control run isolated it, my first server start refreshed the
operator's `~/.local/share/crush/hyper.json` and touched `providers.json`.
Both are Charm's model catalogs fetched from the network, not operator
data, and any `crush` the operator runs rewrites them the same way. Every
other artifact of this probe (project databases, sessions, config, socket,
tmux server) lived in the scratchpad rig and is gone. The operator's
`~/.config/crush/crush.json` and `~/.local/share/crush/{projects,crush}.json`
were byte-identical before and after.

Recorded here rather than as a passing note because
`.claude/rules/tooling-friction.md` requires the capture to precede the
workaround, and the workaround (`CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1`) is
now a spawn-time recommendation in §6.

**Candidate upstream report, not filed:** `CRUSH_GLOBAL_DATA` does not
relocate the provider-catalog write. The variable names where global data
lives and the updater writes elsewhere. Filing that on
`charmbracelet/crush` is a layer-2 defect record under the three-layer
rubric and needs the upstream-claim gate; it is left to the operator.
