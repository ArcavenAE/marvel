# Release engineer, round 5: votes

| # | vote | reason |
|---|---|---|
| V1 | A | the rc is the first build a stranger can pin; an upgrade from it that swaps a mise binary breaks C3's promise that upgrade installs exactly the version it names |
| V2 | B | a hand-placed path is a pin just as a mise one is; refusing only mise leaves the other half of #756 open |
| V3 | A | the chair's check shows the help already names mise (`cmd/marvel/main.go:519-536`); I withdraw my round 3 help test |
| V4 | B | real and cheap, but nothing is lost: the miss can print the key it wanted |
| V5 | B | under C14 (a) a crash replacement on one host is in the promise only through its handoff path; if director rules (b) or (c), it becomes a blocker |
| V6 | A | an edge into the tag would make a should hold the release; a sequencing note gives the order without the hold |
| V7 | C | the snapshot job catches a broken quick start at PR time, and the post-tag check catches what only the published archive and formula show |
| V8 | a | a stranger's install runs with no bus (`internal/daemon/daemon.go:3149-3160`). Predictable: no. SOUL section 7 and ADR-005 lean to (a), but no ruling names which fleet features the operator stands behind |
| V9 | A | the plain name is where Homebrew users look for stable, and the one-time note covers the downgrade on hosts that move. Predictable: no; no recorded ruling covers tap names |
| V10 | B | amend: add "C2 blocks C1b" if V1 carries A; otherwise accept as written |

Dissent, if V1 goes B: an rc whose upgrade swaps a mise install in place ships a known break in the one promise the release exists to make.
