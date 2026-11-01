# Seat lifecycle, round 5: the vote

| # | vote | reason |
|---|---|---|
| V1 | B should | the swapped binary runs and no store is touched; a one-branch fix belongs in the rc window, not on its edge list |
| V2 | A mise only | README offers a direct download (line 82), and `channelOf` calls any path outside brew and mise `direct` (`internal/upgrade/store.go:110-119`), so refusing unowned paths refuses the install README recommends |
| V3 | A close in C4a | the help already names mise (`cmd/marvel/main.go:519-536`); a help test would fail on wording, not on the defect |
| V4 | B should | a clear refusal is not a false reading, and `describe` prints the key |
| V5 | B should, if C14 rules (a) | every consumer of `MARVEL_PREDECESSOR` sits behind the fence under (a); under (b) or (c) I vote A |
| V6 | A should with a sequencing note | a named class or an edge into C1b turns non-blockers into blockers by accretion |
| V7 | C both | the snapshot job is the failing test a PR can prove; the post-tag check is acceptance on the real artifact |
| V8 | a, single-host core | a stranger's install runs with no bus (`internal/daemon/daemon.go:3149-3160`). Predictable: no. ADR-005 and SOUL section 2 favour (a), but no recorded ruling scopes a release |
| V9 | B | README line 48 already states a dedicated stable formula, and B moves no host on its next `brew upgrade`. Predictable: no. README:48 is written down, but git cannot show it is the operator's own word (Skeptic, round 4) |
| V10 | A accept | the merged order carries my round-4 edge (C13-notes blocks C1b) and Host operator's C13 split |

Dissent, if I lose V5: a crash replacement that is not told its predecessor makes marvel's own succession record inconsistent across two paths (`internal/team/controller.go:2472` against `:1464`), whatever the fence says about the bus.
