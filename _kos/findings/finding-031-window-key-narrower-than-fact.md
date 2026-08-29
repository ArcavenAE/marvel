# finding-031 — the window resolver's key is narrower than the fact it stores, so a wrong hit is silent

Probe: `_kos/probes/brief-window-key-provenance.md` (the aae-orc-l8v1 study),
commissioned by the fanout lead on the operator's instruction, 2026-08-28.
Consolidates and costs `study-router-and-backend-layering.md` §24–27 (the eooi
eighth pass).
Author: Winston (marvel architect persona), no human at the keyboard.

Repo pin: marvel `main` @ `60b994b`, clean tree. **Design study only — no code
changed, a fix is tasked out, not applied** (per aae-orc-l8v1).

Related: finding-016 (the six-axis denominator argument), marvel PR #179,
ArcavenAE/marvel#181 (the layer-2 public record). Source ticket: aae-orc-l8v1.
The tasked-out work and its ticket shaping are in §6.

---

## 1. Headline

**`internal/usage` resolves the CTX% denominator on a key narrower than the
fact it stores, and the failure is silent.** The context window depends on at
least `(model, provider, entitlement)`; the resolver keys on a model string
carrying only the first — `NormalizeModel` strips the provider, and `[1m]` is a
vendor-stamped proxy for the entitlement beta. A **miss is loud** (renders
absence, emits `context.limit-unresolved`); a **wrong hit is silent** (renders a
number with a normal `LimitSource`, indistinguishable from a correct hit).

This is a `question-silent-success-instruments` instance: an instrument whose
wrong-key hit reads exactly like its right answer at the point of use.

## 2. Two sites, and the worse one is not the table

The ticket was filed for the **table** (rung 5). The study found the same
key-narrowness one rung from the top, on the **learned** rung (rung 2), and it
is strictly worse:

- **Fleet-wide.** One Resolver per daemon (`daemon.go:271`), so `learned` is
  shared across every session that daemon runs.
- **Beats the operator.** `LimitLearned` is rung 2; in `Resolve` the learned
  branch returns before the manifest branch, so a learned window silently
  overrules an explicit `runtime.context_window`.
- **Silent where its neighbours warn.** The stream branch warns on
  manifest-conflict (limits.go:339); the feed/manifest branch warns (:356); the
  learned branch has no `warnOnce`. The one rung that can silently beat the
  operator is the only rung that says nothing.

`Learn` is reached today only by claude. **Mechanism, not incident:** it is not
established that anyone has run a mixed-backend fleet on one daemon.

## 3. Premises verified independently at `60b994b`

| Premise | Result |
|---|---|
| One Resolver per daemon | one `NewResolver` site, daemon.go:271 — confirmed |
| The redirection discriminator does not exist yet | the finding-016 axis-4 env strings (`CLAUDE_CODE_USE_*`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_BEDROCK_SERVICE_TIER`) appear **nowhere** in the Go tree — confirmed |
| Learned returns before manifest | limits.go:345–360 — confirmed |
| Learned branch is silent | no `warnOnce` in the learned branch — confirmed |
| 11 table keys → 7 base ids | confirmed |

The claim stands on marvel's own code plus finding-016, **harness-independent**.
The Crush-catalog evidence (141 of 249 multi-provider ids disagree on window, 52
by ≥1.5×) is a *demonstrated mechanism* — third-party curation, no live API
called, no adapter shipped for the divergent providers — not a live defect.

## 4. Why the obvious repair fails, and the answerable question

Widening the key to `(model, provider, entitlement)` is **blocked**: marvel
cannot observe which provider served a request, and a key naming a value marvel
cannot supply never matches. The answerable question is narrower and
conservative: **"is this session on the vendor default, or has something
redirected it?"** The table's shipped values *are* the vendor's direct-API
windows, so a guard need only detect *departure from default*, and may treat
"cannot tell" as departure. The discriminator that answers it does not exist —
it is a *build* (a spawn-time read of the backend-selecting environment marvel
constructs), the KNOW step this arc has recommended since its first pass.

## 5. The repair (tasked out, not applied)

Provenance grade (Option A) as the vocabulary + refuse-on-known-ambiguous-key
(Option C) as the core, both riding a new discriminator. Two refinements the
study adds to the eooi §26 costing:

- **Option D is two things the board already split.** D-warn (one `warnOnce`) is
  independently shippable and self-contained. D-key (key the learned rung on the
  same discriminator the table gains) is **not** independent — learned and table
  already share `NormalizeModel`, so it folds into C.
- **Option A has a cheap static half.** A-static (tag table entries
  `exact|narrow`, grade narrow hits soft) ships now with no discriminator and is
  the cheapest honest early win; A-dynamic (per-session upgrade) rides with the
  discriminator. Concrete shape: a graded enum on the reading —
  `KeyExact | KeyNarrow | KeyRedirected | KeyUndeterminable` — orthogonal to
  `LimitSource`. C is then the rule
  `KeyNarrow ∧ (redirected ∨ undeterminable) ⇒ resolve LimitUnresolved`.
- **Option B** (provider in the key) is deferred behind the study's standing
  trigger.

**Sequence (smallest-first, corrected for the splits):** yfn2 (D-warn) →
A-static → the discriminator (KNOW step) → {C + D-key + A-dynamic} → B deferred.

**The residue, stated plainly:** the discriminator closes only the
environment-mediated part of the provider and entitlement axes. A `.crushrc`, a
project-scope setting, or account-side beta enablement stays silent-wrong.
Smaller than today's residue, not zero — and the provenance grade is what makes
it honest, telling the reader which of `fully-keyed / redirection-detected /
cannot-tell` they are in.

## 6. Follow-on work (one pointer)

The fix is tasked out, not applied. Shaping, per `bd-hierarchy.md` / orc
finding-118 (task-typed umbrellas close at ~9%): do **not** file a new umbrella
— re-scope `aae-orc-bv7m` down to the refuse-guard step and file the provenance
grade and the discriminator as flat predecessors it depends on; the D-warn is
already split out, and `aae-orc-wyza` / `aae-orc-rm6o` stay unparented with their
work absorbed into the grade and discriminator steps. The full
dependency-sequenced ticket list belongs to the kinu work queue and the probe
brief (`brief-window-key-provenance.md`), not here. No new GH issue —
ArcavenAE/marvel#181 already carries the claim and both sites.

## 7. Harvest

- **`question-router-and-backend-layering`** (marvel — direct parent): the l8v1
  sub-probe of that arc. Ruling (no new primitive, 2026-08-09) unchanged; the
  repair is internal to `internal/usage`. This finding is edged from that node
  in the same change.
- **`question-silent-success-instruments`** (orc — cross-cutting family): a new
  instance. The provenance grade (Option A) directly answers that node's
  sub-question C, "should an instrument be required to report its own coverage?"
  — the CTX% resolver reporting its own key-confidence is exactly that. **A
  cross-graph harvest is owed:** an orc-side change should add an edge from
  `question-silent-success-instruments` to this finding (marvel/finding-031).
  Recorded here because the marvel PR cannot edit the orc node.
- **finding-016**: this finding is the concrete `internal/usage` consequence of
  its six-axis argument, on the provider and entitlement axes.
