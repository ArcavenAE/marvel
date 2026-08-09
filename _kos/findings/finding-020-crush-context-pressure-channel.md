# finding-020: the Crush context-pressure channel, and what perturbation turned out to be

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

**Both discriminators the mining arm names are satisfied here, and I am
stating that rather than leaving it implied.** A session accumulator can
never decrease, and `completion_tokens` decreases twice (966 to 179, 216 to
185). A level must stay under the window while a running total over N
requests cannot, and every `prompt_tokens` sample across the seven requests
sits between 28674 and 29582 against a 40960 window, where a running total
would have passed it at request 2 and reached roughly 200k by request 7.
The one place `prompt_tokens` falls is the compaction in §4, which is a
reset rather than an ordinary sample, so the window bound is what carries
that term and the observed decrease is what carries the other.

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

**The rung is NOT settled, and it should stop being settled between two
arms.** The codex arm and I have now taken three positions across three
exchanges: transport (theirs, withdrawn), governance (mine, conceded,
concession declined), attributability (theirs, current). Each move was made
by deferring to the other's evidence. That is not convergence, and one more
round of it would produce a fourth position rather than an answer.

So I read `limitLadder` and `doc.go` rather than each other. Both texts are
in `internal/usage/limits.go` and both are quoted here because the outcome
turns on them.

Rung 1 is defined by a sentence with two conjuncts: "the harness stating
the window it is currently enforcing compaction against, **in the same
channel as the token counts it is stating it about**." Crush satisfies the
first (its auto-summarize StopCondition actuates against exactly this
number) and fails the second (separate route). The codex arm is right that
the second conjunct exists and right that I had been reading the
paragraph's closing summary, "rung 1 is for the channel that governs the
session," which states only the first.

But rung 4 does not fit either, and this is the part neither of us checked.
It is defined as "a side channel read opportunistically off a human-facing
status hook, with no version handle and no statement of which of the six
effective-window axes it reflects." Against Crush's route, measured:

| rung-4 property | Crush `GET /v1/workspaces/{id}/agent` |
|---|---|
| side channel, read opportunistically | no: a documented first-class API route |
| off a human-facing status hook | no: no hook involved, no human-facing string |
| no version handle | no: `/v1` route prefix, and `GET /v1/version` returns `v0.88.1` with a build id |
| names no effective-window axis | partial: it names the model, not the entitlement or threshold axes |

Three of four fail outright. So the ladder's text was written with two
channels in view, a harness stream and a statusline hook, and Crush's route
is a third shape it does not describe. Forcing it onto either rung imports
reasoning that does not apply, which is exactly what both of us did.

**The consequence is decidable on evidence even though the rung is not.**
Rung 4 sits below `LimitFromManifest`, so placing Crush there means an
operator's hand-written `runtime.context_window` outranks the live route.
The stated reason manifest outranks feed is that the feed's number varies
on axes the operator may know about and the payload does not name. Here
that argument runs backwards: the router study measured the window as a
provider-plus-model property with 141 of 249 shared model ids disagreeing
across providers, and an operator writing a window by hand will write the
model's headline number, which is the value that is wrong by up to 3.8x.
On this harness the manifest is the more likely error, not the correction.

**The same defect is in marvel's own shipped table, and it does NOT change
the rung ask.** Stating that plainly because a new cross-reference inside a
decision document invites the reader to assume it moves the decision. The
router study reports Crush's catalog ASSIGNING `claude-opus-5` a window of
1000000 under anthropic against 264000 under copilot, so `LimitFromTable` can
resolve wrong by 3.8x with no signal (`aae-orc-eooi`; that study's catalog
measurement, not mine). Two precisions from that arm, both of which narrow
the claim and are worth carrying: no live API was called, so the catalog
assigns the number rather than a provider being observed to serve it; and
marvel ships no copilot adapter, so the 3.8x is a demonstrated MECHANISM
rather than a live defect anyone is currently hitting. I checked the marvel
half directly: `defaultTable` in
`internal/usage/limits.go` carries eleven keys over seven distinct model ids,
every one keyed on the model id alone with no provider dimension, and
`claude-opus-5` is 1_000_000 there. `table` already sits below `feed` in
`limitLadder`, so placing Crush's route at either candidate rung already
outranks it and nothing here needs a different answer. What it does is
promote the paragraph above from a claim about one harness to a claim about
a class: a provider-blind static number outranking a provider-aware live one
is wrong in the same direction wherever it appears, and it appears in our
code, not only in a hypothetical operator's manifest. The table's own defect
is the router study's to file, not this finding's.

**This belongs to the operator.** It is a change to a ruled ladder, the
ruling was the operator's on 2026-08-08, and the evidence for revisiting it
did not exist then. What I would put in front of them: either Crush's route
is rung 1 on the governance conjunct with the same-channel conjunct
relaxed, or the ladder gains a rung between manifest and feed for a
contracted, versioned, live query that is neither the stream nor a status
hook. I am not choosing between those in a finding.

**Both arms have since converged on the first candidate, and the reason it
is not a fourth swap is that neither arm argued from the other.** The codex
arm's case is from the ladder's own stated harm: overruling a rung-1
declaration with a manifest value "would make marvel's denominator disagree
with the one that actually governs the session's behavior", and Crush's
auto-summarize actuates against the number this route returns, so a
manifest override produces that harm here identically to codex. Set beside
the rung-4 table above, both texts point the same way: rung 4's description
does not fit, and rung 1's stated harm does apply.

One overstatement in that case, and it does not change the conclusion. It
says the rung decides "exactly one thing", whether the manifest outranks
the channel. It decides two: rung 1 also sits above `LimitLearned`, so a
rung-1 Crush window would outrank a learned one and a rung-4 window would
lose to it. For this harness that is unlikely to bind, since anything
learned for Crush would be learned from the same route, but "exactly one
thing" is not the mechanism.

So the operator ask narrows from "which rung" to "ratify relaxing the
same-channel conjunct, or add the intermediate rung". The ladder is still
theirs to amend, and two arms agreeing is not the same as it being ruled.

**The two candidates differ in blast radius, which is a property of the
options rather than an argument for either** (the codex arm's observation,
and it is the one thing an operator needs that neither arm's evidence
supplies). Relaxing the same-channel conjunct changes what rung 1 MEANS for
every future channel, including ones nobody has surveyed. Adding a rung
between manifest and feed changes only where one new shape sits. If the
smaller commitment is wanted, the second is smaller. Neither arm states a
preference.

**One thing IS settled and it survives either answer.** A window not
re-read with its level goes stale on model change with no signal, so a
fetched window carries **refetch on model change, and a window fetched
under a different model is unresolved rather than stale**. Both arms agree
on this and it is the part that actually protects a reading.

**The table is not a fallback here, and the router study measured why.**
`~/.local/share/crush/providers.json` is keyed provider first and model
second: 40 providers, 948 model ids, 249 of them offered by more than one
provider, and **141 of those 249 disagree with themselves on
`context_window`**, 52 by a factor of 1.5 or more. All seven model ids in
marvel's shipped table are provider-variable at exact spelling.
The catalog assigns `claude-opus-5` 1000000 under anthropic and 264000 under
copilot, and marvel's table returns 1000000, so a `LimitFromTable` resolution
would be wrong by 3.8x with no signal that anything happened. Two limits on
that number: no live API was called, so this is the catalog's assignment
rather than an observation of what a provider serves, and marvel ships no
copilot adapter, so it is a demonstrated mechanism rather than a defect
anyone is hitting today. (Measured by the router and backend study,
aae-orc-eooi, not by me.)

Combined with §8's result that the database carries no window at all, that
closes the denominator question for this harness: the REST route or the
provider-keyed catalog, never a model-keyed table. The study also notes
that marvel and Crush disagree about which axis carries a 1M window, marvel
putting it in the model name (`claude-opus-4-8` beside
`claude-opus-4-8[1m]`, the entitlement axis) and Crush putting it in the
provider row. At least one of the two is modelling it wrong, so keying by
provider relocates that entanglement rather than resolving it.

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
- **`summary_message_id` is a has-ever-compacted marker, not an event, and
  that is categorical rather than a hit rate.** It stays set afterward, so
  it cannot distinguish this compaction from a previous one at any
  reliability. It is not a weak discriminator; it is not a discriminator.
  Detecting a crossing needs the `prompt_tokens` transition to zero or a
  change in the id's value.

Any Crush reader must discard the zero rather than fold it, exactly as
finding-017 required for codex. What it does INSTEAD splits in two, and
the second branch is the one this harness makes ordinary:

- **A reader that has seen a good level holds it.** Emitting zero reports
  LOW pressure at HIGH pressure, the direction that silently disables
  rotation.
- **A reader that has NOT seen one reports ABSENCE.** It has nothing to
  hold, so `internal/usage`'s stated discipline applies unchanged: a wrong
  number is worse than absence, and that specifically rules out zero and
  rules out a neighbouring session's level.

The second branch is not an edge case here. Because Crush's zero is
durable state rather than a passing record, every attach to a session that
compacted while nothing was watching lands in it: daemon restart, adopted
pane, a session idle since last night. On a harness whose sentinel lasts
one record the no-prior-reading case is rare; on this one it is the
ordinary startup path. (The branch was named by the compaction-mining arm
against its own corpus, and it corrects what this section said in its first
pass, which asserted the hold without its precondition.)

**How the codex forwarder satisfies this, and why copying its shape is not
the same as copying its property.** The codex arm verified absence two ways,
in source and end to end: its reading path writes nothing when it has no
usable sample, so a session that never had a good level keeps a zero
`ContextAt` and the renderer's first switch arm leaves the cell at `-`. That
is satisfied by DECLINING TO SEND rather than by holding correctly. A reader
that instead holds a value in-process across fires loses the property, on
either harness. Worth stating because the forwarder shape is the obvious
thing to port and the property is not in the shape.

**Discard on the token values, not on the companion field.** The
compaction-mining arm measured the same artifact class on Claude Code (68
non-sidechain all-zero usage records over 117,493, 67 of them naming model
`<synthetic>` and one naming the session's real model beside a
467,121-token boundary), which makes the zero-valued record a cross-harness
class with at least three members rather than a Crush quirk. Their
transferable rule applies here directly and I am adopting it: key the
refusal on the zero itself, never on `summary_message_id` being set. My
corpus contains no counterexample, but I never looked for one, and a Crush
row with `prompt_tokens` 0 and no `summary_message_id` would defeat a
companion-keyed discard silently. Their measurement is not mine; it is
recorded here because it changes what a Crush reader should be written to
do.

Two further results from that arm bear on this section and neither is
measured on Crush. On Claude the first post-compaction sample runs a median
61,658 tokens above the boundary's own `postTokens`, because the harness
re-primes system prompt, tools and memory that the summary figure does not
count, which is the measured reason not to seed a reader's level from a
summary row. And two drops of 16 to 20 percent in that corpus carry no
compaction record at all, so "the level fell" and "a compaction happened"
are separate claims. A Crush reader inferring compaction from a fall rather
than from the zero would inherit that error.

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

**This inverts marvel's permission model rather than taxing it, and that
is the reason it outranks everything else on this arm.** Environment
construction at spawn is marvel's one built enforcement locus; loci 2 and
3 do not exist. Permission-through-environment is not one mechanism among
several, it is the whole of what marvel currently enforces. A channel that
serializes the constructed environment to any client of a shared socket
turns the enforcement surface into the disclosure surface: the same act by
which marvel constrains an agent is the act that publishes the constraint's
contents. That is a different kind of finding from a cost to be priced. A
cost can be accepted; an inversion has to be designed around, and the
design that survives it is to keep secrets out of the constructed
environment rather than to weigh the channel's benefits against it.

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

**Second instance of one shape, same day, different repo.** The
`harden-mr5c` arm established that `api.Session` reaches bbolt and every
`mrvl://` client through the same encoder, so any credential placed on a
store resource is published by default. This is that shape again with the
serialization path owned by a vendor instead of by marvel. Both say the
same thing: a credential adjacent to a value that gets serialized is a
credential that gets served, and the axis that predicts it is where the
bytes go, not who was asked. Two instances in one day is enough to stop
treating either as a local defect.

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

## 8. The other two surfaces, checked on a rebuilt rig

Added 2026-08-09 after the first pass, on a rig rebuilt with
`CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1` from the start. Host-global state
was byte-identical and mtime-identical before and after this run, which is
also the control confirming §6's measurement C a second time.

**`crush stats` is a fourth surface, and it sums a level.** The generated
`.crush/stats/index.html` renders from an embedded `const stats` object
whose `total_prompt_tokens` is exactly `SUM(sessions.prompt_tokens)`.
Since `sessions.prompt_tokens` is a per-request level that each request
overwrites, summing it across sessions counts one request per session and
discards the rest.

Measured against the database that produced the page: a three-turn session
issued requests at 28672, 28712 and 28782, and contributed 28782 to the
report. Its actual prompt tokens over the sequence were about 86k, so the
page undercounts that session by roughly 3x, and the error grows with turn
count.

That is a different defect from the codex `turn.completed` class, and the
distinction is worth keeping. A running total sums something real and
merely answers a different question. A sum of levels answers no question:
it is neither occupancy nor spend. So the surface can be cited for session
and message counts, which are honest, and for nothing token-shaped.

Two smaller facts from the same object. `usage_by_model` entries carry
`{model, provider, message_count}` with no token fields, so per-model token
attribution does not exist there. And generating the page writes 157KB into
the operator's project tree, so it is not the zero-side-effect surface; that
is `crush session last --json`.

**The database carries no window, and its model attribution is partial.**
Five tables (`sessions`, `messages`, `files`, `read_files`,
`goose_db_version`) at `goose_db_version` 20260127000000. Grepping the full
schema for `context`, `window` and `max_token` returns nothing. `provider`
and `model` are two real nullable columns on `messages`, not one namespaced
string, and they sit on the message rather than the session, so nothing
structurally constrains a session to one provider.

The negative is the useful part. Crush runs a `models.small` slot as well
as `models.large`, and title generation uses the small model. With the two
slots configured to different models, a TUI session that generated a title
persisted **no row** for that call: the output lands in `sessions.title` and
`messages` gains nothing. So `messages.provider`/`model` records the model
that served each persisted conversational turn, not every model call the
session made, and no marker distinguishes the two. Anyone reading this
database as a routing record should know that before they aggregate it.

**And the record is arbitrarily partial, not partial along a boundary a
consumer could reason about.** Both title generation and summarization run
on the same `models.small` slot, and they persist differently: title
generation adds no row at all (measured, `message_count` held at 2 while
`sessions.title` changed), while summarization does add one, marked
`is_summary_message=1` (measured, `message_count` went 2 to 3 with
`summary_message_id` set). Same slot, same class of internal call, opposite
persistence. So there is no property exposed by the harness from which a
consumer could predict which model calls leave an artifact. That is a
stronger caution than the two-provider question the router study opened,
and it is the one that bears on a Crush adapter: any per-model or
per-provider aggregate built from this table is over an unknown subset.

Cost is stored rather than computed at render time (`sessions.cost REAL NOT
NULL DEFAULT 0.0`, no per-message column), and reads a literal 0.0 for
ollama because the provider catalog prices it at zero.

**One hazard from a sibling arm that does NOT reach these surfaces.** The
codex arm relayed a measurement that Claude Code writes records physically
later than records carrying much older timestamps, 26 times across 2 of 422
sessions, worst case 25.2 hours, clustered before compactions. That defeats
a reader taking the last line of an append-only log. None of Crush's
surfaces is one. The SSE frames are live pushes consumed in arrival order;
the REST routes are queries against current state; `sessions.prompt_tokens`
is a single mutable column overwritten in place rather than an appended
record, so there is no ordering to get wrong. The `messages` table is
append-shaped, but no channel I measured derives occupancy from it. Stated
as a negative rather than skipped, because the check was cheap and the
reason it does not apply is structural rather than lucky.

**Consolidated inventory, all measured at v0.88.1:**

| surface | needs `CRUSH_CLIENT_SERVER` | occupancy | denominator | measured side effect |
|---|---|---|---|---|
| SSE `/v1/workspaces/{id}/events` | yes | per-request level | no | keeps the workspace alive |
| REST `/v1/workspaces/{id}/agent` | yes | no | `model.context_window` | none observed |
| `crush session last/show --json` | no | level, plus a `total` | no | none observed |
| `.crush/crush.db` direct | no | level | no | not exercised as a read path |
| `crush stats` HTML | no | sum of levels, unusable | no | writes 157KB into the project |

Three of the five carry a numerator. Exactly one carries the denominator,
and it is the one that reports the workspace's CURRENT agent, so it cannot
say which window applied to a historical message. The database has the
per-message model and no window; the route has the window and no history.
Neither surface joins them.

## 9. The hook contract, the config split, and a repo-supplied script that runs before anything

Added 2026-08-09 in response to the codex arm's two questions about hook
surfaces. Both answers came out differently from codex, and chasing the
second one turned up the sharpest exposure result on this arm.

**Hook stdout is not fed to the model, and the real trap is the other
direction.** On codex, a hook's stdout came back in the rollout as a
`developer` role item, so a status line printed by a context reporter would
become a context source once per fire. Crush's contract is stricter: exit 0
means stdout is parsed as a JSON envelope (`version`, `decision`, `halt`,
`reason`, `context`, `updated_input`), exit 2 blocks the call with stderr as
the deny reason, exit 49 halts the turn, and any other code is logged and
ignored. Text reaches the model only through the explicit `context` field.
So a Crush hook that printed a status line would not silently become a
context source.

The hazard on this harness points the opposite way. `decision: "allow"` is
documented as **affirmative pre-approval that bypasses the permission
prompt entirely**, and hooks aggregate across config order with `allow`
beating no opinion. A hook written to report rather than to decide must
omit the field, not set it to something benign. Porting a hook design from
a harness whose stdout is inert into one where stdout can grant permissions
is the failure this pair of measurements exists to prevent.

**Config and data split across two variables, and only one of them holds
credentials.** Codex keeps hooks and `auth.json` in one `CODEX_HOME`, which
is why relocating it takes the operator's credentials along. Crush splits
them. `CRUSH_GLOBAL_CONFIG` points at `~/.config/crush/`, whose `crush.json`
is mode 0600 and carries per-provider `api_key` values.
`CRUSH_GLOBAL_DATA` points at `~/.local/share/crush/`, whose `crush.json` is
mode 0644 and holds model selections and `recent_models`, no secrets; the
embedded docs state outright that data directories hold machine-owned JSON
state and that Crush does not discover or execute a `crushrc` from them.

So the adapter rule is asymmetric and the asymmetry is usable: relocating
`CRUSH_GLOBAL_DATA` is safe, relocating `CRUSH_GLOBAL_CONFIG` moves the
operator's credential store and must not be done casually.

**And marvel does not need to touch either, because project-local config
outranks both.** Crush's documented precedence is `.crushrc` / `crushrc` /
`.crush.json` / `crush.json` in the project directory first, closer-to-cwd
winning, then the global config. Marvel owns the workspace directory, so a
marvel-projected hook or setting is a file marvel writes into its own
workspace. That is a genuine structural advantage over codex, where the
only home for a hook is the credential-bearing directory and the hook is
therefore an operator setup step.

**The same mechanism is a code-execution surface, and this is the part that
matters most.** A `crushrc` is documented as a plain Bash script executed at
load time with the same embedded shell the `bash` tool uses. Measured: I
wrote a `.crushrc` into a project directory that redirects a line to a file,
and `crush run` in that directory executed it. No trust prompt, no `--yolo`,
and the `allowed_tools` permission list does not apply because the script
runs at config load, before any agent turn exists to be permitted. A
read-only `crush session list --json` in the same directory did not fire it,
so the trigger is starting a session rather than touching the project.

Marvel's workspaces are checkouts of arbitrary repositories. Launching a
Crush session in one executes whatever `.crushrc` that repository ships, as
the marvel user, with the environment marvel constructed in scope. Composed
with §6 that is one finding rather than two: a repo-supplied script runs
with the constructed environment that the same harness also serves over its
socket. This is the third instance today of one shape, and it is the only
one where the payload is executable rather than merely readable.

Any Crush adapter needs a position on this before it launches anything. The
options are to refuse `crushrc` (there is no documented flag for it, so this
would need an upstream ask), to sandbox the launch under curtain, or to
declare it accepted and say so. Naming it as undecided is the honest state;
picking silently is not.

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

## One assumption I inherited from myself

Named because the compaction-mining arm asked for it and because the shape
is worth more than the instance.

I re-derived `prompt_tokens` as a level because the ticket told me to, and I
settled `completion_tokens` because that gap was named as open. What I never
questioned is the claim the whole §2 table rests on: that the SSE `session`
frame is emitted once per model request. I inferred that from frame counts
matching the request counts I expected, which is the same move as reading a
field's meaning off an earlier finding's authority.

It happens to be checkable after the fact and it held. The tool-calling turn
produced exactly two usage-bearing frames and four messages, which is what a
once-per-request emitter predicts and what a once-per-turn emitter does not.
But I checked it because someone asked, not because I had treated it as a
claim, and the reason it was hard to notice is that the evidence I already
had was consistent with it. The failure mode has a name worth keeping:
confirmed by data I did not collect for that purpose.

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
- **Whether one session can actually hold two providers.** The schema
  permits it and nothing constrains it, but every assistant row on this rig
  read `ollama`/`qwen3:0.6b`. Structural permission is not observation.
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

## Candidate upstream report, held

`CRUSH_GLOBAL_DATA` does not relocate the provider-catalog write. The
variable names where global data lives, and the updater writes to the host
path anyway.

Evidence, both runs on crush v0.88.1 darwin/arm64:

| run | `CRUSH_GLOBAL_DATA` | `CRUSH_DISABLE_PROVIDER_AUTO_UPDATE` | host `providers.json` | host `hyper.json` | rig `providers.json` |
|---|---|---|---|---|---|
| trial | rig | unset | mtime moved | content changed | not created |
| control | rig | `1` | unchanged | unchanged | n/a |

The variable is honored elsewhere in the same tree: `projects.json` was
written into the rig data directory on the rebuild run, so this is the
provider and hyper updater specifically, not the variable being ignored
wholesale.

**The upstream-claim gate has NOT been run on this.** No pinned upstream
SHA, no clean-tree reproduction against a build I made, no search of
`charmbracelet/crush` issues for prior art. It is recorded here as a
layer-2 candidate under the three-layer rubric, and who files it (and after
running the gate) is the operator's call.

## For whoever writes the Crush adapter

Three environment entries, all measured, none optional:

- `CRUSH_CLIENT_SERVER=1` if the session is to be observable at all.
  Without it neither the TUI nor `crush run` registers a workspace, and
  the SSE and REST channels have nothing to read.
- `CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1` always, whether or not the
  channel is used. Without it every marvel-spawned Crush session rewrites
  the host-global provider cache. `CRUSH_GLOBAL_DATA` does not substitute.
- `-H` passed explicitly rather than derived. The default socket path is
  built from `TMPDIR`, and a `TMPDIR` long enough to exceed the ~104-byte
  `sun_path` limit makes the server fall back to `/tmp/crush-<uid>.sock`
  silently.

And one thing to keep OUT of that environment: anything secret, per §6.
The socket serves the constructed environment to every client on the host.
