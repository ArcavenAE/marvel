# probe-backend-usage-limit-observability

Brief. What usage-limit or quota state can marvel observe per auth backend, so
it can surface a real remaining-budget signal where one exists and fall back to
a marvel-defined virtual budget where none does. Feeds the agentic resource
matrix (rows 1 and 2) and vision Gap 4 (credential custody plus spend budgets).

## Scope

Two backends now: the Claude Code subscription (Max/Pro OAuth) and codex on a
ChatGPT/OpenAI subscription login. Channel checklist per backend: response
headers; an account usage endpoint; the CLI or TUI limit source; local on-disk
cache. Plus the custody question the whole thing turns on: which channels are
reachable within ADR-009 / SOUL section 3.

Leave room for (not researched now, kept in the model): the marvel virtual
budget (a manifest ceiling metered against the accountant); and Claude Code via
API key, Enterprise, or Bedrock (rate-limit headers, a management usage API, and
AWS quotas or CloudWatch, each a real-observed candidate with its own reader).

## Method

Read and observe only, secret-safe: field names and shapes, never secret values,
never the operator's actual usage numbers, and no authenticated call on the
operator's credentials. Two sub-researchers fanned out, one per backend;
disk-truth on this machine plus public-documentation corroboration. Load-bearing
disk claims confirmed by direct field-name inspection.

## Result

Harvested to finding-050. Codex is observable on disk (the rollout JSONL
`rate_limits` snapshot). Claude Code has no live number on disk, but the number
is reachable within the custody boundary through agent self-query and
vault-brokered channels, so it is observable-via-channel, not virtual-only (the
custody boundary is brokering-vs-custody, corrected mid-probe by the operator).
The recommendation extends marvel's existing `Dimension` registry with a
windowed Shape and an observed-limit reader carrying a provenance grade, virtual
budget as the fallback. The build is held for the operator as one flat
marvel-scoped bd ticket; the external vault is separate work.

Status: complete.
