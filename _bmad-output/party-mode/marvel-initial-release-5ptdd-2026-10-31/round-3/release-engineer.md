# Release engineer, round 3: C1, C3, C2

Read-only on fa228925. Workflow-shape tests follow the precedent in `internal/toolchain/` (`pin_test.go`, `release_target_test.go`), which already parse `ci.yml` and hold it to a rule.

## C1. The stable pipeline, rehearsed on `v0.1.0-rc.1`

**Fix.**
- Give each channel its own tap file. The rc and stable `brews` entry sets an explicit `name`; the alpha job keeps `Formula/marvel.rb` (`ci.yml:577`) until the C1 card rules which channel owns the plain name.
- A prerelease tag writes `Formula/marvel-rc.rb`, never the stable file, so the rehearsal runs the tap step without touching anyone's install.
- Pin `goreleaser-action`'s `version` (`release.yml:37`), as every other tool in CI is pinned.
- Sign the tap commit. Both writers commit unsigned today: goreleaser as `goreleaserbot` (`.goreleaser.yml:84-87`), the alpha job as `github-actions[bot]` (`ci.yml:579-585`). If goreleaser cannot sign, the stable job hands the formula to the same signed push step the alpha job gets.
- No change to the build, notarize or attest steps: they exist in `release.yml:43-204` and run for the first time on the rc.

**Failing tests first.**
- `TestStableAndAlphaFormulasAreDifferentFiles` (internal/toolchain): parses `.goreleaser.yml` and `ci.yml`, and asserts the alpha formula path differs from every `brews` name, and a prerelease name differs from the stable one. It fails today: both resolve to `Formula/marvel.rb`.
- `TestGoreleaserVersionIsPinned`: asserts `release.yml`'s goreleaser `version` is an exact semver. It fails today on `latest`.
- `TestTapCommitsAreSigned`: asserts each tap-writing step configures commit signing. It fails today on both writers.
- **Rehearsal check (CI, on the rc tag):** a `release-verify` job in a clean HOME installs `arcavenae/tap/marvel-rc`. It asserts `marvel version` prints the tag's version with channel `stable`, `gh attestation verify` passes on each asset, and on macOS `spctl --assess` passes. It cannot pass today: no `v*` tag exists.

**Ticket.** "Release: separate stable and rc formulas, pin goreleaser, sign tap commits, rehearse on v0.1.0-rc.1". It blocks C5. It is blocked by C3, C4 and C14, which must be in the tree the rc tags.

**Card candidate (C1-name).** Which channel owns the plain `marvel` formula?
- Option A: stable takes `marvel`, and alpha moves to `marvel-alpha`. `internal/upgrade/upgrade.go:121` then picks the formula by channel.
- Option B: alpha keeps `marvel`, and stable gets a new name.

A moves every host on the tap from the alpha head to an older rc on its next `brew upgrade`, a silent downgrade, unless the move ships with a one-time note. B leaves every install where it is. Recommend B for the rc and decide A against B before `v0.1.0`. This is a product call, so it goes to director.

## C3. The compatibility contract

**Fix.**
- Write `docs/compatibility.md`. It names the surfaces C14 fences in, and freezes the on-disk schema at `boltSchemaVersion` 2 (`internal/api/bolt.go:77`) for all of `v0.1.x`, so rollback within the line is a binary swap.
- It promises that the next stable line opens a `v0.1.x` store with no hand step, and that any migration logs that it ran and where its backup is (#524).
- No code path changes except #524's log line.

**Failing tests first.**
- `TestSchemaVersionMatchesCompatibilityDoc` (internal/api): reads the frozen schema number from `docs/compatibility.md` and asserts it equals `boltSchemaVersion`. It fails today: the file does not exist. Bumping the schema inside the line then fails CI until the doc and the version line change together.
- `TestStoreOpensPreviousStableFixture`: opens `testdata/store-v0.1.0.bolt`, written by the rc build, and asserts every record reads back. It fails today: no fixture. It joins with the rc.
- `TestMigrationLogsRunAndBackupPath` (#524): runs the v1 to v2 migration on a v1 fixture, and asserts one log line naming the migration and the backup path. It fails today: the migration logs nothing.

**Ticket.** "Compatibility contract for v0.1.x: schema frozen at 2, upgrade and rollback rules, migration logging (#524)". It blocks C1. It is blocked by C14.

## C2. Upgrade must not overwrite what it does not own (#756), and #523's help text

**Fix.**
- `marvel upgrade` refuses a mise install (`internal/upgrade/store.go:114` already detects one) and names the mise command to run instead. It exits nonzero and leaves the binary untouched.
- A binary at a path neither brew nor marvel's store installed is refused the same way, unless the operator passes an explicit `--replace-pinned`.
- `store.go:258-262` ("swapped as it always was") goes.
- #523: the `daemon reexec` help names `exec_path` and the mise stop-and-start path (`cmd/marvel/main.go:378`, per the issue's last comment).

**Failing tests first.**
- `TestUpgradeRefusesMiseInstall`: a fake binary under a `/mise/installs/` path. It asserts an error naming mise, and that the file's bytes and mode are unchanged. It fails today: `installDirect` swaps it.
- `TestUpgradeRefusesUnownedPathWithoutFlag`: the same shape for a hand-placed path, and a second case with `--replace-pinned` that swaps it.
- `TestReexecHelpNamesTheMisePath`: asserts the help text holds `exec_path` and "Under mise". It fails today if the comment on #523 holds; I did not run it.

**Ticket.** "upgrade: refuse mise and hand-pinned installs instead of overwriting them (#756); reexec help names the mise path (#523)". If it is a blocker, it blocks C1. Class still contested (2 blocker, 3 should).

## Order for an rc

C14 blocks C3. C3 and C4 block C1. C2 blocks C1 only if it is voted a blocker. C1 (the rc tag) blocks C5's install check, and C5 blocks `v0.1.0`. The C1-name card is ruled before `v0.1.0`, not before the rc.
