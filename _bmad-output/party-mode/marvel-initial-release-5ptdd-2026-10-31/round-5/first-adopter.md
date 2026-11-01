# First adopter, round 5: votes

| # | vote | reason |
|---|---|---|
| V1 | B | it changes a stranger's pin without a word but loses no state; the one-branch fix still belongs in the rc window |
| V2 | A | README line 82 tells strangers to download a binary, so refusing every path marvel did not install refuses the users the README sent (Skeptic, round 4, item 6) |
| V3 | A | the help already names mise (`cmd/marvel/main.go:519-536`); close #523 with that sha after one read of its last comment |
| V4 | B | a loud refusal with the key one `describe` away; I still want it before C1b, as a sequencing note |
| V5 | B | under (a) every consumer of the variable is a fleet feature; under (b) or (c) it becomes a blocker |
| V6 | A | a named class or an edge makes the tag wait on non-blockers by accretion |
| V7 | C | the snapshot job proves the fix on a PR and fails today at `just build`; the tag-time archive check accepts what was actually published |
| V8 | a | a stranger's install runs with no bus (`internal/daemon/daemon.go:3149-3160`), and the promise is for that install |
| V9 | A | a stranger types `brew install arcavenae/tap/marvel`, and after `v0.1.0` that name should give them stable |
| V10 | B | amend one edge, below |

**V8, predicted?** No. ADR-005 and SOUL sections 2 and 7 favour (a), but no recorded ruling scopes a stable promise.

**V9, predicted?** No. README line 48 ("A dedicated stable formula will be added") reads as a plan for option B. Git cannot show whose word it is, and a plan in a README is not a ruling. If director treats line 48 as the operator's, B is predicted, and I record dissent: a dedicated stable name leaves strangers on the alpha by default.

**V10 amendment.** In round 4 I put "the C1-name ruling blocks C5-docs". That edge is too wide, and it is mine. Both V9 options keep `marvel` on alpha through the rc, so the README for the rc does not need the ruling, and with that edge the ruling would hold the rc. Split C5-docs:
- C5-docs-rc: the quick start rewrite naming the rc install. It blocks C1b and has no other edge.
- C5-docs-final: the formula name in README. It is blocked by the C1-name ruling and blocks `v0.1.0`.

The chair's other edges stand.
