# Probe brief: the window resolver's key is narrower than the fact it stores

**Status:** RUN 2026-08-28. Result: firm conclusion, captured in
`_kos/findings/finding-031-window-key-narrower-than-fact.md`. Design study
only — no production code, per the commissioning ticket.
**Question:** `question-router-and-backend-layering` (marvel, the direct
parent arc), harvesting up to `question-silent-success-instruments`
(orc, the cross-cutting "a wrong hit is silent" family).
**Medium:** static reading of `internal/usage` at marvel `main` @ `60b994b`,
plus five independent premise checks against the tree. No model calls, no
harness sessions, no daemon.
**bd:** aae-orc-l8v1 (this study), aae-orc-bv7m (the implementation it tasks
out), aae-orc-yfn2 (the split-out warn, in flight), aae-orc-wyza + aae-orc-rm6o
(the entitlement instance and a miss instance).
**Prior work it follows:** `study-router-and-backend-layering.md` §24–27 (the
eooi eighth pass, which this brief consolidates and costs), finding-016 (the
six-axis denominator argument), marvel PR #179.
**Layer-2 public record:** ArcavenAE/marvel#181 (already filed; covers both
sites).

---

## Why this probe exists

The router-and-backend study (`question-router-and-backend-layering`) ruled
2026-08-09 that no new primitive is warranted, and in its eighth pass found the
same key-narrowness the arc keeps ruling against biting inside `internal/usage`
itself — not only at the table it was first filed for, but one rung from the
top of the resolution ladder. The operator's instruction was explicit: **study
it and task out a fix; not a fix in this ticket** (aae-orc-l8v1). This brief is
that study.

## Scope discipline (held, per the ticket)

The defect is studied as **THE KEY IS NARROWER THAN THE FACT** — fully
supported by marvel's own code today. It is deliberately **not** studied as
"the table returns wrong numbers," which is conditional on a router/backend
adapter marvel does not ship. Every recommendation below is sound whether or
not such an adapter ever exists. The severity rests on one sentence:

> **A miss is loud. A wrong hit is silent.**

A miss renders absence and emits `context.limit-unresolved` — the discipline
`internal/usage/doc.go` exists to enforce. A hit whose key was too narrow to be
right renders a number, with a normal `LimitSource`, and nothing anywhere
distinguishes it from a hit that is correct. This is the
`question-silent-success-instruments` shape, inverted: there the failure path
and the empty-result path print the same string; here the correct-hit path and
the wrong-key-hit path print the same number.

## The claim, and the premises verified at HEAD

The context window is a function of at least **(model, provider, entitlement)**
— finding-016 makes the stronger six-axis case. The resolver is keyed on a
model string that reliably carries only the first axis:

- `NormalizeModel` **actively strips** the provider dimension (limits.go:212–219):
  the Bedrock region prefixes `us.`/`eu.`/`apac.` and a leading `anthropic.`.
  The reason given (pricing differs by region, the direct-API window does not)
  is sound, and silently assumes the direct API.
- The `[1m]` suffix encodes an **account-and-backend-scoped beta**
  (`context-1m-2025-08-07`, finding-016 axis 5) as though it were part of a
  model name. It is marvel's only proxy for the entitlement axis — a proxy the
  vendor stamps, not one marvel controls.

So the key is narrower than the fact it stores, and marvel has no way to detect
a mismatch.

**Verified independently at `60b994b` (not read off the study):**

| Premise | Check | Result |
|---|---|---|
| One Resolver per daemon (learned is fleet-wide) | `grep NewResolver internal/daemon/daemon.go` | one site, daemon.go:271 — confirmed |
| The discriminator does not exist yet | `grep -rE 'CLAUDE_CODE_USE_*\|ANTHROPIC_BASE_URL\|ANTHROPIC_BEDROCK_SERVICE_TIER' **/*.go` | **zero matches** — confirmed |
| Learned rung returns before manifest | Resolve() limits.go:345–360 | learned returns at 350; manifest begins 354 — confirmed |
| Learned rung is silent where neighbours warn | limits.go:337–360 | stream warns (339), manifest-over-feed warns (356), learned branch has no `warnOnce` — confirmed |
| Table arithmetic: 11 keys → 7 base ids | count DefaultTable() | 11 keys, 7 distinct after stripping `[1m]` — confirmed |

The claim holds on marvel's own code plus finding-016, harness-independent.

## Two sites of one root cause — and the worse one is not the table

| | Table (rung 5) | Learned (rung 2) |
|---|---|---|
| Populated | shipped, diff-reviewable, versioned with the binary | at runtime, from the first session to finish |
| Scope | per lookup | **fleet-wide** — one Resolver per daemon, `learned` shared |
| Beats the operator's `runtime.context_window`? | no (manifest is rung 3, above the table) | **yes** — learned is rung 2, returns before manifest |
| Announces a conflict? | n/a | **no** — the one rung that can silently beat the operator is the only rung that says nothing |

On one daemon running claude across two backends, the first session to finish
`Learn`s a window that then outranks the operator's own override for every later
session naming that model id, with no log line. `Learn` is reached today only by
claude. This is a **mechanism, not an incident** — it is not established that
anyone has run a mixed-backend fleet on one daemon.

## Why the obvious repair fails

Widening the key to `(model, provider, entitlement)` does not work: **marvel
cannot observe which provider served a request** (the arc's central finding). A
key naming a value marvel cannot supply never matches, so it degrades every
lookup to a miss. The answerable question is narrower and conservative:

> Not "which provider is this?" but **"is this session on the vendor default,
> or has something redirected it?"**

The asymmetry that makes this tractable: the table's shipped values **are** the
vendor's direct-API windows. When nothing has redirected the session, they are
right and today's behaviour is correct. When something has, they are unkeyed for
that path. So a guard need only detect *departure from default*, and may treat
"cannot tell" as departure. The redirection mechanisms are enumerated
(finding-016 axis 4) and **absent from the Go tree today** (confirmed above),
so the discriminator is a *build*, a KNOW-step deliverable: read, at spawn, the
backend-selecting environment marvel itself constructs.

## The four options, costed

Carrying the eooi §26 costing forward, with two refinements that change the
sequence: **Option D is really two things, and the board has already split
them; Option A has a cheap static half that ships before the expensive work.**

### Option A — a provenance grade beside `LimitSource`
`LimitSource` grades *which rung* produced the value; this new grade is
orthogonal — *whether the key was discriminating enough*. A table hit can be
exact (`claude-haiku-4-5`, one window) or narrow (`claude-opus-4-8`,
provider-variable at exact spelling); the same is true of a learned value.

- **Cost:** small. One field on the reading, riding the plumbing `LimitSource`
  already has through the store, the API and the renderer.
- **Refinement — A splits:**
  - **A-static** (ships now, no discriminator): tag each table entry
    `exact | narrow` at ship time (the same diff-reviewable-in-Go argument the
    table rests on), and grade a narrow hit soft. The renderer can then say
    "this number is soft because its key cannot exclude a provider that would
    change it" *today*.
  - **A-dynamic** (rides with the discriminator): upgrade the grade per-session,
    so `narrow + redirected` and `narrow + cannot-tell` become distinct states.
- **Recommended concrete shape:** a graded enum on the reading, not a bool:
  `KeyExact`, `KeyNarrow`, `KeyRedirected`, `KeyUndeterminable`. The static
  table tag is `exact|narrow`; the per-session grade combines that tag with the
  discriminator's verdict. Option C is then exactly the rule
  `KeyNarrow ∧ (redirected ∨ undeterminable) ⇒ resolve LimitUnresolved`.
- **Verdict:** necessary, not sufficient — the vocabulary the other options
  speak, and (as A-static) the cheapest honest early win in the arc. It is the
  countermeasure `question-silent-success-instruments` sub-question C keeps
  circling: **an instrument that reports its own coverage.**

### Option B — put the provider in the key
- **Cost:** high, and **blocked.** Requires provider observation, which the
  study concludes is unavailable and which a repo-supplied `.crushrc` defeats
  even when marvel constructs the environment.
- **Failure mode if built anyway:** every lookup misses until a provider is
  known — Option C with more machinery and a worse name.
- **Verdict:** do not build now; revisit only on the study's standing trigger.

### Option C — refuse on a known-ambiguous key (the recommended core)
Mark the table entries whose window is not determined by the model id alone;
when the discriminator says redirected (or cannot be determined), resolve
`LimitUnresolved` rather than the default-path value.

- **Cost:** moderate. The guard in `Resolve` is small; its **input is the real
  work** — the discriminator. `TestResolveAgreesWithTheLadder` constrains it:
  the guard must refuse *within* a rung, never reorder rungs.
- **Regression, smaller than it looks:** no change for a session on the vendor
  default; for a redirected session, cold-start CTX% renders `?` until the
  harness teaches a window — for claude, one session, because `Learn` fills the
  learned rung from the terminal line.
- **Buys:** converts the silent-wrong hit into a loud-absent miss — the
  behaviour `rm6o` already gets for free by *missing*.

### Option D — fix the learned key and warn when it overrules the manifest
- **Refinement — D is two things the board already split:**
  - **D-warn** = one `warnOnce` in the learned branch comparing
    `req.ManifestLimit` and naming which won, matching limits.go:339 and :356.
    **This is aae-orc-yfn2, filed and in flight** (branch
    `fix/yfn2-learned-window-warn`). Self-contained, cheapest item on the arc,
    and it converts the one silent operator-override into a visible one — which
    is what the larger repair buys everywhere else.
  - **D-key** = key the learned rung on the *same discriminator the table
    gains*. **Not** independent of the discriminator: today learned and table
    already share `NormalizeModel` (limits.go:347 vs :367), so "the same key" is
    only meaningful once that key grows a discriminator dimension. D-key
    **folds into C**.
- **Verdict:** ship **D-warn (yfn2) first** — correct regardless of what the
  rest becomes, and every later step assumes the failure is visible. Carry
  **D-key inside C.** The study's "D is small and self-contained" is true of the
  warn half only, which is exactly why the board split it out.

## Recommended repair and corrected sequence

A (provenance grade) as the vocabulary + C (refuse on known-ambiguous key) as
the core, both riding a new **discriminator** (a spawn-time record of the
backend-selecting environment marvel constructs). D's warn half (yfn2) lands
first and is moving; D's key half folds into C; B is deferred.

1. **yfn2 (D-warn)** — one `warnOnce`. In flight. Makes every downstream
   failure visible.
2. **A-static** — mark table entries `exact|narrow`; grade narrow hits soft;
   fold in `wyza`'s doc half (the default-vs-maximum convention in the
   `DefaultTable` comment). No discriminator needed; ships value early.
3. **The discriminator (KNOW step)** — record the backend-selecting env at
   spawn. The real engineering; the input C needs; its fixture pass answers
   `wyza`-empirical and `rm6o`.
4. **C + D-key + A-dynamic** — the refuse guard; the learned rung keyed on the
   same discriminator so the two rungs cannot diverge; the per-session grade
   upgrade. Depends on 1, 2, 3.
5. **B** — deferred behind the standing trigger. No ticket now.

### How wyza and rm6o sequence INSIDE this arc (not beside it)
- **rm6o** (sonnet-5 likely missing its `[1m]` spelling) is a **miss** —
  loud by construction, and the contrast that shows C's target behaviour. Its
  fix is a one-line data question (does sonnet-5 have a single window? if split
  like opus-4-8, add the `[1m]` key), a rider on **step 3's fixture pass.**
- **wyza** (bare-vs-`[1m]` default-vs-maximum, the **entitlement** axis) splits
  like A: its **doc half** → step 2; its **empirical half** (does a bare-keyed
  split model actually get 200k?) is exactly the fixture **step 3** produces.
- Leave both tickets unparented; absorb their work into steps 2/3.

### The residue, stated so nobody discovers it later
The discriminator closes only the **environment-mediated** part of the provider
and entitlement axes. A `.crushrc`, a project-scope setting, or an account whose
1M beta is enabled server-side without an env var, redirects the backend or the
entitlement without touching the environment marvel constructed — and stays
silent-wrong. The residue is smaller than today's and not zero, and Option A's
grade is what makes it honest: it tells the reader which of `fully-keyed /
redirection-detected / cannot-tell` they are in.

## Reconciliation with the board (the fix is already partly tasked out)

Applying "verify the premise before working the ticket" to this study: l8v1
says "task out a fix," but a fix has **already been tasked out** — `bv7m` (P1,
task, `depends-on yfn2`) carries the four steps as one ticket. So this study
proposes **no new umbrella**. Two observations, one recommendation:

1. `bv7m` bundles four differently-sized steps in one P1 `task`. Per
   `bd-hierarchy.md` / orc finding-118, task-typed umbrellas close at ~9%, and
   the entanglement has a concrete cost here: the two cheap high-value wins
   (A-static, learned-rung consistency) cannot be marked done independently, and
   the P1 stays open until the expensive discriminator lands. Burying the
   discriminator as "step 3" hides its cost.
2. **Recommendation:** decompose into flat tickets sequenced by `--deps`.
   Minimal-churn form: re-scope `bv7m` down to the refuse-guard step (its title,
   "narrow the resolver's key gap," already names it), file the provenance grade
   and the discriminator as flat predecessors it depends on, keep `yfn2`. `bv7m`
   stays the P1 anchor #181 references. Operator's call whether to re-scope or
   close-and-refile; the finding's task list maps either way.

## Layer 2 (GH issue) — no new issue warranted

ArcavenAE/marvel#181 (type.bug/p1, 2026-08-09) already carries the claim, both
sites, the evidence and its limits, and the ask, attributed to the
router-and-backend study. The three-layer rubric is complete: local (this brief
+ finding-031 + study §24–27 + finding-016), GH issue (#181), bd (l8v1 →
bv7m/yfn2/wyza/rm6o). This study is at most a comment reference on #181; do not
file a new issue.

## Harvest

- `question-router-and-backend-layering` (marvel): this brief is the l8v1
  sub-probe of that arc; the ruling (no new primitive) is unchanged and the
  repair is internal to `internal/usage`.
- `question-silent-success-instruments` (orc): a new instance for the family —
  the CTX% denominator resolver is an instrument whose wrong-key hit is
  indistinguishable from a correct hit at the point of use. The provenance grade
  (Option A) is a direct answer to that node's sub-question C ("should an
  instrument be required to report its own coverage?"). Recorded in
  finding-031, which the harvest should edge into that node.
- finding-016: cross-referenced; this brief is the concrete `internal/usage`
  consequence of its six-axis argument, on the provider and entitlement axes.

## Provenance

- Author: Winston (marvel architect persona), commissioned by the fanout lead
  on the operator's instruction, 2026-08-28.
- Repo pin: marvel `main` @ `60b994b`, clean tree.
- Method: static reading + five premise checks (table above). No code changed.

## Sources

- `internal/usage/limits.go`, `internal/usage/doc.go` (marvel @ `60b994b`)
- `_kos/probes/study-router-and-backend-layering.md` §24–27
- `_kos/findings/finding-016-effective-autocompact-window-is-the-predictive-denominator.md`
- marvel PR #179; ArcavenAE/marvel#181
- orc `_kos/nodes/frontier/question-silent-success-instruments.yaml`
- bd: aae-orc-l8v1, aae-orc-bv7m, aae-orc-yfn2, aae-orc-wyza, aae-orc-rm6o
