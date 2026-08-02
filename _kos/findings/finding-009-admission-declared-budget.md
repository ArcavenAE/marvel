---
id: finding-009-admission-declared-budget
title: "Admission control: refuse the declaration, not the spawn"
question: elem-agentic-resource-matrix
confidence: frontier
tags: [marvel, admission, budgets, resource-matrix, reconciler, governance]
bd: [aae-orc-qiay, aae-orc-166h, aae-orc-z7c3]
provenance:
  created_by: agent
  session: marvel-qiay-admission-2026-08-01
  host: kinu
  created_at: "2026-08-01"
---

# Admission control: refuse the declaration, not the spawn

The qiay build (marvel PR #101) shipped the first brick of resource-matrix
enforcement locus 2: manifest-declared team budgets that refuse over-budget
work. The design knowledge worth keeping is where the gate belongs and what
the governance shape requires of it.

## The placement result

The obvious placement, gating only the reconciler's session `Create`,
produces the worst available operator experience: `marvel scale
--replicas 40` reports success, `get teams` claims 40 replicas while 6
run, and the reconciler re-evaluates an impossible deficit every 2
seconds forever. Desired state becomes permanently unsatisfiable and the
tables lie indefinitely.

The shipped placement gates the operator's verbs instead: `work`,
`scale`, `run`, and `shift` fail with the arithmetic in the error, the
store keeps the number that is still true, and nothing enters an
unsatisfiable state. The reconciler check remains as a backstop with a
hold latch: a refusal is not a crash (no backoff pollution, no restart
counters), it emits once per transition rather than per tick, and the
hold clears when headroom returns. Five gate sites, one arithmetic
(`internal/admission`, pure, stdlib + api only).

## The governance shape

ADR-007 discipline held: the gate exists only where an operator declared
a budget in the manifest (the ratified locus-2 direction), a manifest
with no budget block behaves identically to before the feature existed,
and every refusal is observable (`admission.refused` carries dimension,
measured value, and limit). Two dimensions are enforced (`max_sessions`
counted from the store, `max_tokens` as windowed prompt-token spend from
the finding-007/PR #99 accountant). Three more are registered and
rejected at parse with the owner named (`max_cost_usd`,
`max_team_rss_bytes`, `max_session_ctx_percent`), so a
declared-but-unenforced budget cannot silently pass. An unmeasured token
figure renders `unmetered`, never `ok` with fictional headroom, which is
the same absence-over-guessing discipline `internal/usage` codified.

## Accepted consequences and edges, on the record

- **Repair exemption ceiling leak.** Ad-hoc `marvel run` sessions raise
  live count but not declared count, so a team can sit above its ceiling
  by however many ad-hoc sessions it holds; the `run` gate closes the
  entry point and the backstop does not claw back. Accepted for the MVP.
- **The apply-time count gate is structurally unreachable** for a
  manifest that parses, because the parser already rejects
  `declared > limit`. It stays because the cumulative token clause does
  fire at apply.
- **Scale-down is never refused** (`want <= 0` short-circuits); locus 3
  (mid-flight revocation) lifts that guard when it exists.

## Pre-existing defects measured en route (not fixed in #101)

- `ParseManifestBytes` tries YAML then falls back to TOML, so any
  validation error on a YAML manifest is replaced by a TOML syntax
  complaint. Masks the budget declaration clause, `replicas >= 1`, and
  `context_window` validation alike. Tracked as bd `aae-orc-z7c3`; the
  fix changes parse behavior for every manifest, so it needs its own
  care.
- `handleApply` double-wraps the `parse manifest:` prefix (cosmetic,
  rides z7c3).

## What consumes this next

Demo Act 4 (`aae-orc-166h`, now unblocked) has its fixture and five-beat
runbook shipped in `examples/demo-act4-budget.{toml,yaml}`; the act's
promotion signal is an operator-executed run. M4 shift triggers
(`aae-orc-hpeu`) read the same accountant; locus 3 revocation waits on
the M1 authority model.
