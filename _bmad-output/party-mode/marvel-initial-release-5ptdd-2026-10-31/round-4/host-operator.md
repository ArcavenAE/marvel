# Host operator (SRE), round 4

Lens unchanged. Read-only on fa228925.

## 1. Final class, and the best case against it

| row | my final | strongest argument against my choice |
|---|---|---|
| C2 (#756) | blocker | the binary still runs, no store is touched, and the fix is one branch (Skeptic), so it costs a user one surprise, not data |
| C6 (#337) | should | it is the second command a stranger types, and it fails with a name they cannot guess (First adopter) |
| C7a (#541) | should, under C14 option (a) | it is the one place marvel alone loses finished work, and the fix is one assignment in a path whose shape the shift code already proves |
| C8 | should, before C1b | the rc runs on ephemeral CI runners, so the leak never touches the rehearsal; "before C1b" buys hygiene, not evidence |
| C15 | should | a common Linux first run (tmux 3.4) re-runs and re-pays each headless run, which a stranger reads as marvel wasting money |
| C14-scope | (a), single-host core only | this fleet uses the bus every day, so a promise that excludes it covers the part least exercised in practice |
| C1-name | alpha keeps `marvel` for the rc; decide before `v0.1.0` | a stranger who types `brew install arcavenae/tap/marvel` after `v0.1.0` gets the alpha unless the decision is made |

C2 stays a blocker for me because it is my round 1 "must not" in product form: `marvel upgrade` rewrites a file at a path marvel did not install and did not record (`internal/upgrade/store.go:258-262`). A stable release should not change what it does not own, on the host, by default.

**Is "should, before C1b" a class of its own?** Yes, and it should be written as one: "rc-window". It means land before the rc tag when it is ready, and cut the tag anyway if it is not. It never becomes an edge into C1b. Without the name, the outcome either drops these items or turns them into blockers by accretion. C8, C9, C10, C11 and C15 belong in it; so does C7a if C14 rules (a).

## 2. Can the ruling be predicted?

**C14-scope: partly.** ADR-005 (component independence) and SOUL section 2 say every component works without the others, and marvel's own CLAUDE.md says marvel requires only Go, tmux and a harness. That predicts that the core must stand on its own inside the promise; it does not predict whether the managed bus also goes inside. No recorded ruling covers a stable promise. The card must hold:
- what each option freezes: under (b) the bus config keys, the services entry, the declared principals and the role-inbox subject become compatibility surface for all of `v0.1.x`;
- what (a) costs this fleet: bus and shift changes stay free to break, so the fleet pins and upgrades on its own schedule;
- that (c) makes C7a, C7b and the C13 bus rows blockers, and roughly what that adds to the rc;
- a default of (a), valid until a date, re-checked by the architect seat.

**C1-name: no.** No ruling covers formula names. The card must hold:
- the silent move under option A: every host on the tap, this fleet's included, goes from the alpha head to an older stable on its next `brew upgrade`, unless a one-time note ships;
- what option B leaves: `marvel` stays alpha, and stable needs a name a stranger can find from the README;
- that `internal/upgrade/upgrade.go:121` picks the formula by channel, so either option needs that code to match;
- a default of B for the rc only, valid until `v0.1.0` or a date, whichever comes first.

## 3. (Skeptic only.)

## 4. One wrong edge

**C4 blocks C13 is too wide.** It is right for the known-gaps section, which must be read off the board at the tag's sha. It is wrong for #1131's advisory, which is code with its own failing test (`TestUnnamedModelAdvisoriesNamesARoleWithNoModel`) and needs nothing from the triage. Split C13 into C13-code (#1131, no edges, rc-window) and C13-notes (blocked by C4). Otherwise the one cheap code fix the party wants in the rc waits on a full board triage.

On my own items, I accept Skeptic's correction to C9 (round 3, test 4): a growth count over a shared `/tmp` is flaky under parallel packages, so the check asserts its own layout's tag has no `/tmp/marvel-h-<tag>` after the run, by name.
