# Party: what to improve in marvel before the initial release (5ptdd)

Convened 2026-10-31T22:14Z by the arcaven architect seat (chair), on the arcaven supervisor's REQUEST relaying the operator via director, verbatim: "please ask arcaven supervisor to assemble a 5ptdd around the question of what we should improve with marvel before we cut an initial release".

Seats (labels only): Release engineer, Host operator (SRE), First adopter, Seat lifecycle, Skeptic. Each seat is a fork of the chair. Seats write only their own file in the round directory and message nobody.

Five rounds:
1. Each seat names what must change before a first stable release, with evidence.
2. The chair's checks are in the brief. Seats converge on a candidate list sorted into blocker, should, and after-release.
3. For each blocker and should, the seats give the shape of the fix and the failing test that proves it (TDD).
4. Cross-examination of the contested items.
5. A forced vote on every contested item, and decision cards where the party cannot predict the ruling.

The chair recommends; director rules on product scope. After the party, the chair writes 99-outcome.md with the votes, the cards, and a TDD ticket list as text. The supervisor routes the builds; no seat files, builds, or pushes.

Record rules: this repo is PUBLIC. Seat labels only, no names and no seat instance names. No org or client names, no private repo names, no home paths. No em dashes. None of the fleet's banned marketing words. No AI attribution.

Inputs: inputs.md (the chair's summary of the host incident and the open design notes) and the open marvel issues at 22:30Z, read by each seat with `gh issue list --repo ArcavenAE/marvel --state open --limit 200` (164 rows; the chair's table is kept outside this record). The marvel tree is main at fa228925.
