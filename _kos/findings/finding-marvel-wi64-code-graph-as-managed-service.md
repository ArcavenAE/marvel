# finding-marvel-wi64: a code graph fits marvel's service frame but cannot be hosted until MS-21 and MS-8 land

**Date:** 2026-10-16
**Confidence:** frontier. This is design read from code, design documents and
public tool documentation. No code-graph service was run under marvel.
**Placement:** marvel. The subject is marvel as the host of a code-graph
service; the code-graph tools are objects. The cost measurements of the tools
themselves live in kos (finding-kos-ndz7, finding-kos-wre7).
**Source:** a research request delivered to director on 2026-10-16. The code
cites below were read at marvel main ac67c07.
**Depends on:** marvel#916 (MS-21) and the spawn extraction that gates a
second managed instance (MS-8). Both are prerequisites, not parts.

## Why this matters

A code graph (symbol and reachability facts) would give the reconciler and the
fleet a queryable view of what a change touches. Before anyone builds toward
it, the useful question is whether marvel's service frame can host one and what
stands in the way. It can, with two blockers, and no candidate tool is a drop-in.

## Result

1. The services frame models a code graph without changing its shape: class
   `code-graph`, a provider, and the modes managed, adopted and external
   (`internal/service/service.go:41-44`, `102-123`).
2. Two things block hosting one today:
   - MS-21 (marvel#916): an unregistered class invalidates the whole services
     list, and the bus goes with it (`internal/daemon/daemon.go:3414-3417`);
   - MS-8: no second managed instance lands before the spawn extraction
     (`docs/design/services-list.md:313-325`).
3. The brief already rules that a code graph is an ingest pipeline into
   existing stores, not a graph database marvel operates
   (`docs/design/service-provider/01-brief-prd.md:126-129`).
4. Field state on 2026-10-16:
   - SCIP indexers are batch tools only;
   - tree-sitter MCP servers, code-review-graph the closest fit, offer health,
     a reindex on commit and per-project stores;
   - Serena relicensed to GPL-3.0-or-later in 2.0.0;
   - Sourcegraph fails the custody boundary.
5. No candidate stores provenance per fact (emitter, commit, time). kos needs
   that, so a loader is the honest invent surface.

## Harvest

- question-marvel-service-provider-shape: add code-graph as a second-class
  candidate blocked by MS-21 and MS-8.
- kos: close sub-question E on question-code-graph-correspondence against
  ADR-009. Correct the 2026-09-29 note on aae-orc-isie.

## Next

Ruled 2026-10-16: trial outside marvel first; Serena adopted-only.
