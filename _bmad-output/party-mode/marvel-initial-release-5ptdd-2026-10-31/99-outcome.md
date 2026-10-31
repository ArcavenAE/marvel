# Outcome: what to improve in marvel before the initial release (5ptdd)

Party closed 2026-10-31 after five rounds, against marvel main fa228925. Seats: Release engineer, Host operator, First adopter, Seat lifecycle, Skeptic. The chair recommends; director rules on product scope. Nothing here is built, filed or merged. The supervisor routes the builds.

## The answer in short

Make the release honest before making it bigger. Five things block the first stable cut, and none is a new feature:

1. **Say what "stable" covers** (C14), and mark the rest experimental.
2. **Make the stable pipeline real and rehearse it** on `v0.1.0-rc.1` (C1). The goreleaser path has never run, and as configured it would overwrite the alpha formula.
3. **Write the compatibility contract** (C3): the store schema is frozen at 2 within `v0.1.x`, so a rollback inside the line is a binary swap.
4. **Make the board true** (C4): close the issues already fixed on main, give every open bug a verdict, and ship a changelog.
5. **A quick start that works from an installed binary** (C5). Today it needs a source tree (`just build`).

The host memory incident is mostly not marvel's. The party's reading, not a finding, is that the file-event growth tracks harness git polling over a large tree: another host that runs marvel's tests holds a 7.5 MB file-event daemon after 48 days. marvel's share is real and cheap to fix, though: test homes that never delete, test layouts that fall back to `/tmp`, test brokers that outlive their tests, and orphaned helper processes no verb can see. Those are shoulds, landing before the rc if ready.

No host watch goes into the daemon in this release (5-0). A spawn cap on low memory is a gate on a vital sign (SOUL section 8, `diagnostic-not-gate.md`), and needs a ratified decision before it is built (5-0).

## Settled without a vote

| id | item | class |
|---|---|---|
| C1 | stable pipeline, split into C1a (separate formula files, pinned goreleaser, signed tap commits) and C1b (the rc tag) | blocker |
| C3 | compatibility contract with the schema frozen inside `v0.1.x` | blocker |
| C4 | split into C4a (close the fixed set now) and C4b (triage ledger, changelog) | blocker |
| C5 | split into C5-code, C5-docs and C5-install | blocker |
| C14 | stability fence | blocker |
| C9, C10, C11 | test layouts fall back to `/tmp`; orphaned codex app-servers invisible; test brokers and tmux servers outlive a run | should |
| C12 | no host watch in the daemon; no spawn cap | after release |
| C12b | a footprint summary in `marvel reap --homes`, no thresholds, no new verb | should |
| C13 | silent defaults and false health named as known gaps in the notes; #1131 per harness | should (C13-notes blocks the rc) |
| C15 | a tmux 3.5 floor in README, a NOTE at daemon start that never refuses (its raiser dropped it from blocker) | should |

## The vote (round 5)

| # | item | result | tally | dissent |
|---|---|---|---|---|
| V1 | C2, #756: upgrade overwrites a mise or pinned binary | B should | 3-2 | Release engineer and Host operator hold it a blocker: a stable upgrade should not rewrite a binary at a path marvel did not install |
| V2 | C2 fix scope | A refuse a mise install only | 4-1 | Release engineer: also refuse any path marvel did not install unless `--replace-pinned` |
| V3 | #523 | A close it in C4a; the help already names mise | 5-0 | |
| V4 | C6, #337 | B should | 5-0 | |
| V5 | C7a, #541 | B should, if card 1 rules option 1 | 5-0 | Seat lifecycle: crash repair and shift record succession differently, whatever the fence says |
| V6 | how a "land before the rc if ready" item is recorded | A a should with a sequencing note and no edge | 5-0 | |
| V7 | quick start test before a tag | C both a PR-time `goreleaser --snapshot` job and the post-tag check | 5-0 | |
| V8 | C14-scope | a single-host core | 5-0 | (card 1) |
| V9 | C1-name | B stable gets its own name | 3-2 | Release engineer and First adopter: stable should take the plain name at `v0.1.0`; Skeptic: if A wins, its tap note blocks `v0.1.0` (card 2) |
| V10 | the order | A accept | 4-1 | First adopter: split C5-docs so the rc does not wait on card 2. The Release engineer's amendment depended on V1 going A, so it fell |

**The open point the chair flags.** Under the accepted order, card 2 blocks C5-docs, and C5-docs blocks the rc tag, so the rc waits on a ruling. First adopter's split would remove that wait. If card 2 is not ruled before C5-code and C1a land, take the split: C5-docs-rc names the rc install and blocks C1b, and C5-docs-final follows the ruling and blocks `v0.1.0`.

## Cards for director (product scope)

Both cards are written in the Desk format and passed the card-ingest parse; the supervisor carries them. Neither ruling can be predicted from a recorded ruling (5-0 on both).

1. **Which parts of marvel does the first stable release promise?** Recommended: the single-host core only (session, team, work, get, describe, capture, inject, upgrade, daemon, reap, with no bus), every bus and fleet feature marked experimental. 5-0.
2. **Which channel owns the plain `marvel` formula?** Recommended: stable gets its own formula from the start, alpha keeps the plain name, so no host moves without asking. 3-2.

## TDD ticket list

Flat tickets with dependency edges, as text for the supervisor to route. Each test is written first and fails on fa228925 for the reason given; no seat ran go test. "rc-window" in the class column means a should with a sequencing note: land before the rc tag if ready, never hold the tag for it (V6).

**Blockers**

| t | title | first failing test, and why it fails today | blocked by |
|---|---|---|---|
| T1 | stability fence: `docs/stability.md`, a stability annotation on every cobra command, a NOTE from `marvel work` for each experimental manifest key (C14) | `TestEveryCommandDeclaresStability` walks the command tree and fails on any command in neither list; no annotation exists. Also `TestStabilityDocMatchesAnnotations`, `TestWorkNotesExperimentalKeys` | card 1 |
| T2 | release channels: stable and rc write their own formula files, goreleaser pinned, tap commits signed (C1a) | `TestStableAndAlphaFormulasAreDifferentFiles` (both write `Formula/marvel.rb` today: `ci.yml:577`, `.goreleaser.yml:80`), `TestGoreleaserVersionIsPinned` (`release.yml:37` says `latest`), `TestTapCommitsAreSigned` | none |
| T3 | compatibility contract `docs/compatibility.md`: schema 2 frozen for `v0.1.x`, upgrade and rollback defined, the migration logs what it ran and where the backup is (C3, #524) | `TestSchemaVersionMatchesCompatibilityDoc` (no doc), `TestMigrationLogsRunAndBackupPath` (it logs nothing). A store fixture generated from main is a regression guard, not a first-failing test; the previous-stable store check lives in release-verify and does nothing until `v0.1.1` | T1 |
| T4 | close the fixed set with their fix shas: 365, 364, 348, 341, 289, 202, 201, 119, 390, 523; rescope 391 (only the claude half is fixed) (C4a) | a board action, not a build; each close cites a sha reachable from main | none |
| T5 | triage ledger `docs/release/v0.1.0-triage.tsv` (fixed, holds, rescoped, fenced for every open bug), `CHANGELOG.md`, and three release-verify checks (C4b) | every open bug has a row; the tag has a changelog section; every fixed row names a sha reachable from the tag. All red: no ledger, no changelog. They check a release artifact, so they may gate the tag and never a merge | T1 |
| T6 | `marvel example demo`: an embedded manifest that runs a shell loop and needs only tmux (C5-code) | `TestExampleDemoParsesAndNeedsNoBuildTree`: the verb does not exist | none |
| T7 | README Quick Start rewritten around the installed binary and T6, with a Requires line naming tmux 3.5 (C5-docs) | the T8 job, which runs the README's commands | T6, card 2 (see the flagged split) |
| T8 | PR-time `goreleaser --snapshot` job that installs the snapshot into a clean HOME and runs the README quick start (V7) | fails today at `just build` | T2, T6 |
| T9 | release notes "Known reading gaps" section (#953, #813, #917, #1079, #703 and the rest of C13), README says HEALTH means liveness, a script checks each listed issue is open at the tag (C13-notes) | the script: no section exists | T5 |
| T10 | cut `v0.1.0-rc.1` (C1b). A release act for the operator via director, not a build | the release workflow runs green and writes only the rc formula | T2, T3, T5, T7, T8, T9 |
| T11 | post-tag check: install from the rc formula and archive in a clean HOME, verify version, attestation and signature, run the quick start (C5-install) | runnable only after T10 | T10 |
| T12 | cut `v0.1.0` | T11 green | T11, card 2 |

**Shoulds**

| t | title | first failing test | class, edges |
|---|---|---|---|
| T13 | `marvel upgrade` refuses a mise install and names the mise command to use instead (C2, #756, V2 A) | `TestUpgradeRefusesMiseInstall`: `internal/upgrade/store.go:258-261` swaps it | rc-window |
| T14 | pane verbs accept the bare name `get sessions` prints when it matches one session, refuse two matches listing both keys, and print a hint on a miss (C6, #337) | `TestSessionArgAcceptsPrintedName` | rc-window |
| T15 | crash repair records the crashed seat's key as the replacement's predecessor (C7a, #541) | `TestCrashRepairReplacementNamesItsPredecessor`: the only assignment is the shift path, `internal/team/controller.go:2472` | rc-window; a blocker if card 1 rules option 2 or 3 |
| T16 | user guide: a role inbox survives a restart; a seat's own address lasts one generation (C7b, #951; the sender-side change belongs to director) | a docs check for the sentence | should; T1 |
| T17 | an advisory at apply for a role whose manifest names no model, per harness (C13-code, #1131) | `TestUnnamedModelAdvisoriesNamesARoleWithNoModel`, beside `FirstPromptAdvisories` (`internal/daemon/daemon.go:1575-1580`) | rc-window |
| T18 | homeguard points Go's module cache outside the scratch HOME and fails the test binary when the HOME cannot be removed (C8) | a test that `go env GOMODCACHE` under homeguard is outside the scratch HOME (Skeptic's replacement; a removal test stays red after the fix because the child writes the read-only tree itself). Today `homeguard.go:51` discards the error | rc-window |
| T19 | homeguard makes a short scratch HOME so a test layout's durable harness base fits the 104-byte socket limit (C9) | `TestScratchLayoutUsesTheDurableHarnessBase`, checking its own layout tag only, never a count of `/tmp` | rc-window; T18 (same file) |
| T20 | `marvel reap --homes` lists live codex daemons under other layouts' bases, and never signals them (C10, #186 remainder) | a reap test with a planted pid file under another base | rc-window; T19 |
| T21 | a footprint summary in `marvel reap --homes`: counts of homes, bases, test homes and helper daemons, no thresholds (C12b) | a test of the summary lines | rc-window; T20 |
| T22 | every test that starts a broker or a tmux server records its pid and stops it on every exit path, with the cleanup registered before the adoption step (C11; closes #1162 and #463, narrows #884) | `TestAdoptionHandoffStopsTheBrokerWhenTheSecondDaemonFailsToStart` | rc-window |
| T23 | a `tmux.below-floor` NOTE at daemon start below tmux 3.5, never a refusal (C15, #714) | `TestTmuxVersionFloor` | rc-window |

**After the release, recorded and not designed:** a host watch (in the daemon or as a scheduled role); a spawn cap on low memory, only through a ratified gate decision; per-seat suspend, which needs a wrapper launch (`docs/design/fleet-suspend-resume.md` section 7); the sender-side change for #951; heartbeat tokens that never expire (#186 remainder).

## What the party ruled out, and why

- A host-health ticker in the daemon for this release: it is host monitoring, and it would add an always-on loop to the one process every seat depends on just before a stability promise (5-0).
- "rc-window" as an edge into the rc tag: that would make shoulds into blockers under another name (V6, 5-0).
- A help-text test for #523: the help already names the mise case, so the test would fail on wording, not on the defect (V3, 5-0).

This recommendation is valid until 2026-11-07, or when the `v0.1.0-rc.1` tag is cut, whichever comes first. The arcaven architect re-checks it then.
