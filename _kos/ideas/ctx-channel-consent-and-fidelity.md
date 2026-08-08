# CTX% channels: consent gates, fidelity orders

**Status: idea. Pre-hypothesis. Nothing here has been built or tested.**

Raised during a design review of the CTX% acquisition channels, 2026-08-08,
alongside phase 1 of `probe-interactive-ctx-remainder-sweep.md`. Recorded
because it is the most reusable thing the review produced, and because it
would be expensive to rediscover after a second channel ships.

## The problem it answers

The room kept saying "layered" and meaning four different things: a
fallback chain, a merge, a vote, and a preference order. Meanwhile the
sweep turned up a class of channel nobody had a slot for: reading state a
harness writes for its own purposes, with no cooperation at all
(`~/.codex/sessions/*.jsonl`, `opencode.db`, a per-project `.crush/`,
`~/.claude/projects/**`). Where does that rank?

## The claim

Cooperation is not a rung on a fidelity ladder. It is a second axis, and it
**gates** rather than orders. Consent decides what is admissible; fidelity
still ranks what got in.

**Axis A, fidelity** (orders): proximity to a per-request prompt level with
its token classes intact.

**Axis B, consent** (gates), three grades:

- **contracted** — the harness publishes it for consumers. A FIFO stream,
  an HTTP API, OTEL, a documented hook.
- **conceded** — the harness handed marvel the pointer. The claude
  statusline payload contains `transcript_path`; codex hooks carry
  `agent_transcript_path`; a Crush `PreToolUse` hook exports
  `CRUSH_SESSION_ID`. One field converts a glob into a handoff.
- **appropriated** — marvel went looking. Globbing a projects directory,
  opening someone's sqlite file, guessing a path from a mangled cwd.

An appropriated channel is not a low rung. It is off the ladder: a probe
instrument, whose production form is whatever conceded or contracted
variant can be negotiated.

## Why the gate, and not just a low rank

Three arguments were offered, in increasing severity. The third is the one
that would decide it alone.

1. **Failure mode.** A configured-away channel fails loudly and
   attributably: marvel wrote the config, so marvel knows it is missing,
   and absence renders as absence. A refactored-away channel fails
   silently: the file still exists, the query still succeeds, the number is
   now wrong. That is finding-007's measured defect (a cumulative total
   read as a level) with a new delivery mechanism.
2. **Declaration.** A contracted schema tells you which quantity you hold.
   An appropriated one does not. `Sample.Cumulation` exists precisely
   because level-versus-sum must be declared and never inferred, and an
   appropriated channel cannot declare it. (Crush turned out to be
   determinable anyway, empirically and from source, which weakens this
   argument by exactly one instance and is worth noting honestly.)
3. **Credential adjacency, categorical.** `opencode.db` holds `credential`,
   `account`, and `control_account` tables in the same file as the token
   counts. SOUL §3 says marvel never stores or manages credentials and
   delegates auth to the tool that owns it. A production read path whose
   access surface is the harness's OAuth store is one bug away from
   violating marvel's own advertised auth boundary. A capture-pane scrape
   physically cannot reach a credential; a database handle can.

Consent failure is the user's decision to make. Schema failure is the
vendor's. SOUL §1 says marvel owns the first and must not pretend to own
the second.

## The overfit ruling this exposes

The sweep brief ruled capture-pane scraping out for fragility. That is
right, but the stated reason was slightly wrong, and the wrong reason
generalizes badly.

A TUI's disqualifying defect is not that it is uncooperative. It is that
**there is no version number on the screen**, so it can only fail quietly.
Both candidate databases carry a migration-version table
(`goose_db_version`, `migration`). Uncooperative-with-a-version-handle is a
different animal from uncooperative-without-one, and the graph currently
conflates them.

Counter-argument recorded in the same breath, and it is strong: pinning a
schema version is false comfort where the volatile part is inside an opaque
column. opencode keeps its token classes in a `data TEXT` JSON blob that no
`sqlite_master` fingerprint can see, and it shipped 38 migrations in five
months. The proposed answer is to **fingerprint the data, not the schema**:
assert an arithmetic identity inside the payload (`total == input + output
+ reasoning + cache.write + cache.read`, which holds on live rows) and
refuse when it stops holding.

## Consent as an operator decision, not a vendor one

A separate line of argument reached a rule the room could ratify without
resolving the philosophy: the consent that matters here belongs to the
**operator**, who owns both tools, not to the harness vendor. But it must
be given rather than assumed. Concretely: uncooperative reads are opt-in
per runtime in the manifest (a sibling of the shipped `context_feed`
setting), never a default, confined to a named table allowlist excluding
credential-bearing tables, and read-only or not at all.

Also recorded: the room reached for "no conscription" (SOUL §2) to argue
against uncooperative reads, and that was a misuse. §2 is a
dependency-direction rule, and reading a peer's files compels the peer to
do nothing. What is actually at stake is user sovereignty pointed the other
way, and the platform has no rule for it. That gap may be worth its own
node.

## What it would cost in code, if it survives

The claim is that this needs no new architecture, only a field and a
refusal:

- `Source` gains a value per channel class plus a consent grade, reusing
  the pattern `LimitSource` already ratified for the denominator.
- One ingest verb, so transport lifecycle stays outside `internal/usage`.
- A refusal at ingest for the trap the new class introduces: a
  session-cumulative sample that is not terminal has no level in it, and
  folding it would resurrect finding-007's defect through a new door.
- Reach is not a tiebreaker. A channel that cannot reach the host is simply
  not eligible there, handled by the same mechanism as staleness, which
  keeps the ladder one-dimensional and answers multi-host without a special
  case.

## Multi-host, dissolved rather than solved

Every file and sqlite channel dies when the harness runs on another host
(roadmap M5), while OTEL and a projected callback survive. The counter is
that **the per-host collector already exists and is called the marvel
daemon**: a local read is the same class as `internal/procstat` sampling
the host process table. Nobody ships the process table across hosts; the
local daemon samples and the reading travels.

The honest consequence: a channel available exactly where the harness
happens to run cannot be a foundation, because a foundation must be uniform
across the fleet. It is an optimization that raises fidelity where it
exists. That costs no new machinery, only the discipline of never treating
it as the plan.

## A third axis the two above do not predict

The consent axis was built to gate uncooperative reads, and one of its
supporting arguments was that an appropriated read is not passive (a
read-only sqlite connection opens `-wal` and `-shm` read-write, because WAL
readers write read-marks). Round 3 produced a sharper instance of
not-passive on the CONTRACTED side, where that argument was supposed not to
reach.

Crush's event stream is as contracted as a channel gets: a documented socket,
a `/v1` route prefix, a versioned endpoint. And attaching to it registers
marvel as a REAL client. `AttachClient` bumps a stream count,
`attached_clients` is exposed on the session, and `proto.go` carries a
comment that observers use stream count to detect live subscribers. During
the probe an unobserved workspace was reaped before use, so keeping the
reading alive may require holding a stream open, which changes the harness's
own view of whether anyone is watching.

A second instance came from the probe's own hygiene disclosure: merely
starting a Crush server refreshed the shared global caches
`~/.local/share/crush/providers.json` and `hyper.json`, and `--data-dir`
scopes the project database rather than the global config directory. So
instantiating the contracted channel mutated host-global state outside the
probe's sandbox.

Neither is a consent failure. Both are observation-changing-the-system, and
both land on the contracted channel, which means **the consent axis does not
predict perturbation**. Either the model needs a third property per channel
(does observing it alter the observed system, and does the harness's own
behavior key on being observed) or perturbation belongs in the arbitration
logic as an eligibility cost rather than in the grading.

The practical consequence is small and immediate: a channel can be fully
contracted and still not free. That belongs in the arbitration design before
a second channel ships, which is the whole reason this file exists.

## What would kill this idea

- If the arbitration never actually fires because no session ever has two
  eligible channels, the whole apparatus is ceremony and one channel per
  harness plus an honest absent state is correct.
- If the consent grades turn out to collapse in practice (every conceded
  channel is also contracted, or no harness ever concedes a pointer), the
  axis is a distinction without a difference.
- If an operator rules that appropriated reads are simply fine, the gate
  becomes a preference and this is over-thought.

## Provenance

Produced in a party-mode design review, 2026-08-08. The consent grading and
the multi-host dissolution came from the architect's lane; the
fingerprint-the-data counter and the silent-failure taxonomy from the
maintenance lane; the operator-consent rule and the overfit-ruling
observation from the problem-solving lane. Empirical inputs are catalogued
in `probe-interactive-ctx-remainder-sweep.md` phase 1. The eight code
observations the same review produced are `ArcavenAE/marvel#141` to `#148`.

Related: `stream-attachment-strategies.md`, `marvel-channel-design-principles.md`,
`marvel-agentic-resource-matrix.md`, `permission-through-environment.md`.
