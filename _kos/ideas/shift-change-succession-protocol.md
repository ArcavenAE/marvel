# Shift-change succession protocol: from cold successor to authored intent

- **Status:** idea (pre-hypothesis, no commitment). A design for the missing
  half of marvel's shift mechanism: the handoff. Output of a bmad party-mode
  session (understanding, independent research, question, six argue rounds,
  conditioned unanimous vote), commissioned via director on behalf of the
  operator, 2026-09-17.
- **Subject:** marvel (the shift state machine, the runtime adapters, the
  daemon). The succession MECHANISM is marvel's; wardrobe is an OBJECT it
  consumes for the handoff CONTENT. Filed in the marvel graph per the subject
  test, cross-linked to wardrobe for the content contract.
- **Related:** [[tmux-harness-state-watchdog]] (classified state and the
  advisory-only boundary; a shift is one operator response a classified state
  can motivate), [[token-rate-column-and-configurable-columns]] (the decaying
  rate is a cheap "went quiet" signal adjacent to context pressure). Vision row
  15 (marvel owns handoff SCHEMA; departing agent owns CONTENT). marvel
  ShiftPolicy / ShiftState (the trigger and state machine this extends).
  wardrobe `contents/fragments/succession.md` (the authored content contract,
  included by 8 roles). orc finding-016 (fire on the level, not the
  post-compaction event; F-02 context-pressure monitoring).

## The problem, in one sentence

Marvel can fire a clean shift, but the successor comes up blind: gen-2 is a cold
idle REPL with no task, no intent, and no pointer to what gen-1 was doing, so the
only thing standing between a context-pressure shift and lost work is luck.

## What the live test showed (Jabberwocky, 2026-09-17)

I am grounding this in the operator's own context-pressure shift test run today
against the auto-shift binary, not in theory.

- **The trigger works.** At `ContextTokens > ContextLimit - HeadroomTokens` the
  daemon fired a clean rolling shift: launch gen-2, gate on running, drain gen-1,
  complete. Arming it needs `context_feed=statusline`; `context_window` alone
  sets the denominator and meters no occupancy.
- **There is no handoff.** Gen-2 came up a cold idle REPL. No task, no
  commander's intent, no self-resume.
- **Recovery worked only by luck.** Three things happened to hold: the work was
  file-based in a shared cwd; the successor was externally prompted to start; and
  the brief carried successor-aware instructions. When prompted, gen-2 correctly
  detected it was a successor, recorded "came up cold," and resumed at the right
  item without clobbering. Marvel supplied none of those three.

The code confirms the gap is structural, not incidental (survey against marvel
`main`): there is no handoff schema, struct, field, artifact, or store anywhere
in marvel; `ShiftState` carries no handoff pointer; a gen-2 session is launched
with the identical static one-sentence system prompt as gen-1; and marvel
controls no agent scratch or working directory (panes inherit the tmux server
cwd, a workspace is a name). Vision row 15 is unbuilt.

## The ownership line, and why it resolves the hardest tension

Vision row 15: marvel owns the handoff SCHEMA (location, lifecycle, generation
binding); the departing agent owns the CONTENT. The gen-1 multiclaude protocol,
which I harvested as prior art we own, learned this the hard way and it is the
release valve for the obvious objection.

The objection: a context-exhausted agent is a bad author, degraded at exactly the
moment I ask it to write the most important paragraph of the shift. The valve:
in gen-1 the authored handoff was declared an OPTIMIZATION, not a requirement
(`supervisor.md:239`), and the load-bearing state was the daemon-observable
snapshot rebuilt from durable stores (git, the issue tracker). So a garbage
intent from a fried agent degrades to "the successor reconciles from ground
truth," not "the successor loses the work." The authored intent is the fast path;
reconcile-from-records is the floor.

## Prior art harvested

**Gen-1 multiclaude (ours), the shape and the scars.** Two levels: a per-agent
handoff triggered by a `SESSION_HANDOFF_PREPARE` message (departing agent writes
`handoff.md` under five fixed headings plus a machine `context.json`, replies
`SESSION_HANDOFF_READY`), and a supervisor shift handover where the successor is
spawned with `SHIFT_HANDOVER` plus a path to `shift-state.yaml` and drains the
message queue and reconciles before taking new work. The most reusable idea is
the anti-bloat mechanism: a checkpoint captures DERIVED state, the successor
reads only the delta after a watermark, and the handoff is overwritten, not
appended, so successive shifts do not accumulate context
(`retrospector.md:44-63`). Two scars matter for us:

- **INC-004:** all eleven agents reached for the harness's own built-in send
  tool, which "silently accepted and silently dropped" their messages; 33+ were
  lost with no error. The lesson: the handoff signal and transport must not ride
  a same-process harness primitive. They must fail loud and observable.
- **INC-001:** a routine `git checkout` in a shared working tree destroyed the
  supervisor's uncommitted edits. The lesson: the handoff artifact lives outside
  any shared checkout, single-writer.

Gen-1 also scattered its state across four `~/.multiclaude/` locations plus
committed files in the git tree, which is exactly the sprawl the one-clean-slot
constraint forbids.

**External research (cited inline).** Commander's intent doctrine (US Army ADP
6-0, AFDP 1-1): a handoff carries purpose, key tasks, and desired end state, plus
constraints; it is deliberately short so it survives transmission, and it is
end-state-oriented, not step-oriented, because the successor must act correctly
in situations the predecessor did not foresee. Agent-succession practice splits
on the axis I care about: OpenAI's Agents SDK handoff passes the entire prior
conversation forward (lossless but unbounded, the wrong primitive for a shift
that fired because context was full), while Anthropic's context-engineering
guidance is clean-context successors plus an external memory store (the right
shape, and the shape gen-1 already had). Non-lossy compaction: CogCanvas (arXiv
2601.00821, verified against its Table 1) reports summarization at 19.0% recall
against 92.0 to 92.5% for verbatim chunks and 97.5% for its full method; the
mechanism is a timing argument, an extractor commits at write time before the
questions exist and silently deletes the quantifier (the worked case turns "use
type hints everywhere" into "prefers type hints"), while verbatim storage defers
the decision to read time when the successor's live question guides selection.
This is the charter's F25 backdrop/props distinction: the handoff is an authored
intent (an honest lossy backdrop) plus pointers into an untouched verbatim store,
never a summary of the predecessor's context. Fire-on-level (finding-016) has
control-theory prior art: hysteresis and dual-threshold switching (the Schmitt
trigger), where firing on a measured level crossing with headroom is anticipatory
control and the harness compaction event is the failure boundary you stay away
from, not the trigger you react to.

One honest gap the research flagged: CogCanvas measures RETENTION recall, not
agent TASK success after a succession. The recall win is strong evidence for
"the successor retained the decisions and constraints," but the transfer to
task-continuity is unmeasured. That is why a test task below measures task
continuity directly, not file-read.

## The design

Marvel builds the half it lacks. The departing agent keeps the half wardrobe
already authored. Neither owns the seam today; this places it.

### 1. The handoff slot (marvel owns location and lifecycle)

Marvel creates one generation-bound handoff directory per shift lineage under its
own state root, for example `handoff/<team>/<lineage>/`. It is single-writer (the
departing generation), it lives OUTSIDE any shared git checkout (INC-001), and it
is wiped on shift completion, with at most a bounded retain-for-audit that also
expires. It is a handoff drop box, NOT the agent's working directory: the worker
role does its real work in a git checkout, and setting the pane cwd to a scratch
dir would break the one role that already had a durable handoff store. The slot
path is recorded in `ShiftState`, which is the handoff pointer field the schema
lacks today. This one slot is the whole answer to the "one clean wipeable area,
no sensitive data scattered" constraint: it is the only place marvel tells the
agent to write handoff bytes, so wiping it is wiping the handoff.

### 2. The handoff phase (marvel owns the lifecycle and the marker gate)

Marvel inserts a handoff step into the shift state machine BEFORE the drain.
Today the phases are None, Launching, Draining; this adds a prepare/handoff beat.
On that beat marvel: creates the slot; injects a bounded write-request over the
adapter INPUT path (the same mechanical path that starts a session), never the
harness's own message tool (INC-004); then waits for the departing agent's
terminal marker or a timeout, and only then drains. `HeadroomTokens` is already
documented to budget the departing agent's handoff turn, so the timeout has a
home. The self-initiated path stays valid: where the harness exposes its own
pressure, the agent may write before being asked (wardrobe already says so), and
both paths converge on the same file and the same marker.

### 3. Delivery to the successor (marvel owns the generation binding)

At gen-2 spawn marvel stamps one more environment variable, `MARVEL_HANDOFF_IN`
set to the slot path, alongside the five it already stamps (`MARVEL_SESSION`,
`MARVEL_ROLE`, and the rest). The successor reads it as its first act, which is
exactly wardrobe's ordered first-acts. The binding is recorded in `ShiftState`,
so a daemon restart mid-shift does not lose the pointer.

### 4. The marker contract (the seam marvel owns, and only this)

Marvel gates the drain on a terminal marker, so marvel must parse that one token.
Therefore the marker contract, the token and the file location, belongs to
marvel's schema, together with location, lifecycle, and generation binding. That
is the minimal schema marvel parses and not a byte more. Everything inside the
file, the four things and the record shapes, is wardrobe's content, which only
the writer and the successor read; marvel never parses the prose. The marker
written last is also the partial-write defense: if the agent dies mid-write, the
marker is absent, the successor treats the file as not-there, and it reconciles.

### 5. No-loss by reconcile-from-records (the floor under the fast path)

The pointers are the load-bearing part of the handoff; the prose intent is the
fast path. The guarantee is three layers: a durable record store (git for the
worker, the issue tracker and kos for the rest), plus marvel's guaranteed slot
and delivery so the pointers reach the successor, plus the successor's
reconcile-from-records fallback (wardrobe's singleton query and
compute-what-changed first-acts) when the handoff is absent or unmarked. The
residual risk is a role whose in-flight work is neither in a durable store nor
written before death; that is thinner for non-worker roles and is a NAMED
limitation with a test, not a paper-over.

### The content the agent writes (wardrobe owns this, already authored)

Per commander's-intent doctrine and wardrobe's existing contract: purpose, key
tasks, desired end state, and constraints, capped in length (brevity is a
feature, not a nicety), plus the four things wardrobe already specifies (pointers
to every record held, what is in flight, what was ruled out and why, what is
being dropped). The pointers are addresses, not payloads. The schema FORBIDS any
full-context or prior-transcript field, because that is the door context-bloat
walks through; the successor resolves pointers at its own read time, so fidelity
stays in the store and shift forty starts with the same access as shift one.

## Where the shift-change instructions live (recommendation and tradeoffs)

**Recommendation: split, along the seam the code and content already imply.**
Marvel owns location, lifecycle, generation binding, and the marker contract
(schema and mechanism). Wardrobe owns the content shape (what to write, the write
discipline, the successor's first-acts). The binary owns the mechanical delivery
(slot creation, the env stamp, the wipe) and encodes NO editable content.

The four candidates and what each already owns, so the split is a ratification of
reality, not a preference:

- **Marvel graph/schema.** Already owns the shift lifecycle, generation identity,
  and per-session persistence that survives a daemon restart. It is the natural
  owner of location, lifecycle, and generation binding precisely because it
  already owns generation identity. It owns nothing agent-authored today.
- **Marvel binary.** Already owns everything that reaches a live harness at spawn:
  env stamps, the settings projection, the input path. It is the only place that
  can inject the slot pointer into gen-2 or create a marvel-owned directory. If a
  handoff is delivered at all, the delivery originates here.
- **Wardrobe role content.** Already owns the entire handoff CONTENT contract,
  authored, versioned, digest-pinned, included by 8 roles, including a concrete
  store for the worker (git branch and PR). It can only reach the agent as slice
  text in the system prompt, and it defers the write trigger and the "handoff
  bytes" location to "the spawner," which is the marvel machinery this design
  builds.
- **The binary as content home.** Rejected. Baking the handoff format or content
  into the compiled binary makes every wording change a release. The binary owns
  the marker token (it gates on it) and the delivery, nothing an operator should
  edit without a release.

Tradeoff of the split: it requires the marker contract to be agreed by both
sides, so a change to the marker is a two-repo change (marvel schema and
wardrobe content). That is the cost, and it is the right cost: the marker is the
one thing both the writer and the reader must agree on, so it should be the one
thing that cannot drift silently. The de-facto arrangement is already this split
on the content side (merge-queue's prompt has moved to wardrobe, marked do-not-
load from multiclaude); this design builds the marvel side that has been missing.

## Harness-independence (protocol yes, trigger separately)

The PROTOCOL, create slot, request write, gate on marker, deliver path, wipe, is
harness-independent and trigger-agnostic by construction: it rides marvel's
generation machinery and a plain file, with no harness-specific branch, and it
works on an operator-initiated shift as well as an auto shift. The TRIGGER is a
different matter: codex and opencode run unresolved windows (`ContextLimit==0`)
and never trip the pressure inequality, and interactive Claude needs the
statusline feed. That auto-trigger gap is a SEPARATE root cause; solving it here
would be filing a solution ahead of its problem. It is filed as its own ticket
and is explicitly out of this design. Harness-independence of the protocol is
proven the honest way: an operator-initiated shift on a codex session must
round-trip the identical handoff with zero harness-specific code.

## The boundary (advisory, per SOUL section 8)

The handoff mechanism classifies and carries; it does not judge or destroy.
Nothing here auto-kills a session, and the existing `(stalled)` advisory and the
[[tmux-harness-state-watchdog]] boundary hold: the value is that the successor
comes up KNOWING, not that the machine takes the wheel. A missing or unmarked
handoff is an honest degrade to reconcile, never an escalation to a restart loop.
Custody never moves during a shift (SOUL section 3 / ADR-009): the handoff
carries pointers and intent, never credentials, and the slot must not tempt any
credential-handling path.

## Phased plan

- **Phase 1:** the slot, the handoff phase, and delivery for ONE role on the
  claude adapter, end to end: marvel creates the slot, requests the write, gates
  the drain on the marker, stamps `MARVEL_HANDOFF_IN`, and the successor reads it
  as its first act. Wardrobe's content shape aligns to the marker contract.
  Chosen because it runs the whole path once and proves the architecture; the
  rest is more roles and more adapters, not more plumbing. Verified by re-running
  the Jabberwocky context-pressure test against the built handoff.
- **Phase 2:** the reconcile-from-records fallback made load-bearing and tested
  (kill mid-write, prove degrade not loss); extend to the other roles and to the
  codex and opencode adapters via operator-initiated shifts.
- **Phase 3:** the auto-trigger gap for unresolved-window harnesses, tracked
  separately, and any confidence-to-action policy, which stays advisory by
  default per the boundary above.

Standing rule across phases: the marker contract is the one thing both repos
agree on and the one thing that may not drift silently; the schema carries no
full-context field.

## Open questions

- Does marvel keep a continuously-fresh snapshot so a force-killed predecessor
  still hands off, or is the snapshot only as fresh as the last marked write? Gen-1
  kept a daemon snapshot; marvel's observable state is thinner today.
- The write-request injection over the adapter input path is the least-proven
  mechanical step; is there a session whose input path marvel cannot reach, and
  what is the fallback there?
- How long is the bounded audit-retain before the slot is wiped, and is retain-
  then-wipe worth the risk to constraint 2, or should completion wipe
  immediately?
- Is the marker a fixed token or a generation-stamped token, and does the
  successor verify the marker's generation matches its own predecessor?
- Does `describe session` surface the handoff state (written / delivered /
  reconciled) for operator visibility, the way it will carry the watchdog's
  classified state?

## bd tasks filed

A flat set with depends-on edges, no invented umbrella parent (per bd-hierarchy).
If the operator wants to ratify "marvel shift-change succession protocol" as an
epic, these are its natural children; I did not invent the container. Ids and the
edge map are recorded in the party output and reported to the director.

## Crystallization signal

Already crystallizing: a live incident (Jabberwocky) this would have caught, a
fully authored content contract waiting on the mechanism, and a code survey that
located the exact seam. When Phase 1 is committed, extract a probe brief (the
one-role handoff on the claude adapter behind the marker gate) and a frontier
question for the reconcile-from-records floor and the non-worker residual.
