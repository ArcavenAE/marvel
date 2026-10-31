# Release engineer, round 4

Read-only on fa228925.

## 1. Final classes, each with the strongest case against it

| id | my class or option | strongest argument against me |
|---|---|---|
| C2 | blocker | It loses no state, and the swapped binary works (Skeptic, round 2). I hold it because C3 promises that upgrade installs exactly the version it names, and a binary swapped under mise makes mise report one version while another runs. That breaks the promise on the first upgrade. |
| C6 | should | It is the second command a stranger types (First adopter, round 1), so the first error they meet is one marvel could have avoided. |
| C7a | should under C14 (a); blocker only under (b) or (c) | `MARVEL_PREDECESSOR` also carries the handoff path, not only bus mail, so a crash replacement on one host with no bus loses its predecessor's handoff too. |
| C8 | should, with an edge: it lands before C1b | A leak of 1,800 files per test binary on every dev host is the kind of footprint a stable label should not carry. "Should, before C1b" is not a class of its own: it is a should plus an ordering edge, recorded in the edge list. |
| C15 | should | A headless run below tmux 3.5 is paid for twice (`docs/user-guide.md:383-385`), and the README never says so. |
| C14-scope | (a), the single-host core | The fleet runs the bus, shift and max-age every day, so marking them experimental leaves the most exercised code outside the promise. The answer: the promise is for a stranger's install, and their install runs with no bus (`daemon.go:3149-3160`). |
| C1-name | alpha keeps `marvel` through the rc; the rc writes `marvel-rc`; the plain name is ruled before `v0.1.0` | Homebrew convention puts the stable build on the plain name. Until the ruling, a stranger who types `brew install arcavenae/tap/marvel` gets the alpha. |

## 2. Can the ruling be predicted?

- **C14-scope: no.** Recorded rulings lean towards (a), but none decides it. SOUL section 7 (gradual elaboration) and ADR-005 (a component works alone) favor a small promise. No ruling says which fleet features the operator wants to stand behind. The card must hold:
  - the verb list for each option (First adopter, round 3, C14);
  - what "experimental" means: no compatibility promise, it may change in a patch release, and `--help` says so;
  - the cost of (a): the fleet's own daily features are outside the promise;
  - the cost of (c): every bus and shift bug becomes a stable regression;
  - a recommendation of (a), with an expiry.
- **C1-name: no.** No recorded ruling covers tap names. The card must hold both options:
  - stable takes the plain name at `v0.1.0`, and alpha moves to `marvel-alpha`;
  - stable gets a new name.
  It must also state the cost of the first option: every host on the tap moves from the alpha head to the older stable build on its next `brew upgrade`, a silent downgrade, unless the move ships with a one-time note, and `internal/upgrade/upgrade.go:121` picks the formula by channel. Recommend the plain name for stable at `v0.1.0`, with `marvel-alpha` and the note, expiring before the `v0.1.0` tag. The rc does not wait on it.

## 3. On the Skeptic's round 3 test notes for my items

- The C1 hole is real, and it is the reason C1a lands before C1b. I add one assertion to the rehearsal check: the tap commit the rc makes touches only `Formula/marvel-rc.rb`.
- C3: `TestSchemaVersionMatchesCompatibilityDoc` does fail first (the doc is absent). `TestStoreOpensPreviousStableFixture` does not, and it is vacuous until `v0.1.1`. I move it to a release-verify step that opens a store written by the previous `v0.1.x` tag, marked vacuous for `v0.1.0`.

## 4. Edges

Two are missing from the chair's order:
- **C2 blocks C1b**, if C2 is voted a blocker. The rc is the first build a stranger could pin, and an upgrade from it must already refuse mise.
- **The C1-name ruling blocks `v0.1.0`**, not C1b. Without it the stable formula has no name to publish under.

No proposed edge is wrong.
