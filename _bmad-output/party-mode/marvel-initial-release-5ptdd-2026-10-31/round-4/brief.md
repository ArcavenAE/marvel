# Round 4 brief: cross-examination of what is still contested

Read every round-3 file first.

## Chair's checks on round 3 claims

| claim | result | command |
|---|---|---|
| no client config means no managed bus | holds: `attachServices` returns nil when the config is nil or names no cluster (`internal/daemon/daemon.go:3149-3160`) | sed |
| the create path is where a predecessor can be set | the spawn loop names seats at `internal/team/controller.go:1462-1468`; the only assignment of `Predecessor` stays `:2472` | sed |
| an advisory list exists to extend for #1131 | holds: `FirstPromptAdvisories` and its siblings at `internal/daemon/daemon.go:1575-1580` | sed |
| the rc would write the alpha formula | holds as far as the config goes: `.goreleaser.yml:65` `prerelease: auto`, `brews` with no name override; `release.yml:37` `version: latest`. goreleaser's own default for a prerelease brew upload was not checked against a pinned version | grep |

## Chair's merge of the order

C1 splits in two. **C1a** (separate formula files, pinned goreleaser, signed tap commits) lands first and has no blockers. **C1b** is the `v0.1.0-rc.1` tag itself. Proposed edges:
- C14 blocks C3, C4 and C7b.
- C4 blocks C13.
- C1a, C3, C4 and C5-docs block C1b.
- C1b blocks C5-install, and C5-install blocks `v0.1.0`.
- The C8, C9, C10, C12b chain and C11 have no edges to the tag; the Host operator asks that they merge before C1b anyway.

## Still contested

| id | question |
|---|---|
| C2 | #756: is an upgrade that overwrites a mise or pinned binary a blocker or a should? |
| C6 | #337: blocker or should? |
| C7a | #541: blocker or should? Its raiser makes it conditional on C14. |
| C8 | test homes leak: should (4) or blocker (1)? The raiser now accepts should, landing before C1b. Say whether "should, before C1b" is a class of its own. |
| C15 | tmux floor: its raiser dropped it to should. Any seat still holding blocker? |
| C14-scope | **Does the stable promise cover the bus, director integration, shift, max-age and suspend?** Options: (a) single-host core only: session, team, work, get, describe, capture, inject, upgrade; every bus and fleet verb marked experimental; (b) core plus the managed bus on one host; (c) everything that ships. |
| C1-name | which channel owns the plain `marvel` formula: alpha keeps it for the rc (the raiser's recommendation), stable takes it at `v0.1.0`, or a new name for stable from the start? |

## Questions

1. For each contested row, give your final class or option, and in one line the strongest argument AGAINST your own choice.
2. C14-scope and C1-name are product scope, so the party recommends and director rules. Say whether the operator's ruling can be predicted from rulings already recorded. If it can, name the ruling. If it cannot, say what the card must contain.
3. Skeptic only: of the tests named in round 3, list the ones that cannot fail first on fa228925, and say what each should be replaced with.
4. Any seat: one edge in the chair's order that is wrong, with the reason.

Same limits. Write round-4/<your seat label>.md, under 60 lines, and run prefile-scan.
