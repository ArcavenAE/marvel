# Skeptic, round 3: C4, and the tests hardest to make fail first

Read-only on fa228925; new checks in brackets.

## C4. Board reconcile at one sha, and a changelog

**Fix (no code change):**
- Pick the rc sha. For every open `type.bug` and unlabeled `bug` issue, record one verdict against that sha: `fixed` (with the merge PR and sha; then close it citing both), `holds` (with the one command that shows it still holds), `rescoped` (retitle to what remains, as #523 becomes "help text" and #186 its unbuilt half), or `fenced` (outside the C14 promise, kept open).
- The verdicts live in one tracked ledger, `docs/release/v0.1.0-triage.tsv` (issue, verdict, sha or command, PR). The release notes' known-issues section is the `holds` rows; nothing else feeds it.
- The fixed set to close first, from the round 1 and 2 checks: 365, 364, 348, 341, 289, 202, 201, 119, 390. #391 is fixed for claude only (#1046), so it is `rescoped` to the codex and opencode harnesses, not closed.
- `CHANGELOG.md`, one section per `v*` tag, with Fixed, Changed, Known issues. The rc section is written from the ledger.
- What does not change: no issue is closed on a "looks fixed" comment alone; each close names a sha that a reader can check.

**Failing tests first:**
1. `release-verify: every open bug has a verdict` (a script in `release-verify.yml`, run on a `v*` tag and by hand). It lists open issues labeled `type.bug` or `bug`, reads the ledger, and fails naming each issue with no row, and each `fixed` row whose issue is still open. Fails today: there is no ledger, so all 73 open bugs are missing.
2. `release-verify: the tag has a changelog section`. Fails if `CHANGELOG.md` has no `## <tag>` heading for the tag being verified. Fails today: there is no CHANGELOG file [`ls CHANGELOG*`: no match].
3. `release-verify: a fixed row names a reachable sha`. For each `fixed` row, `git merge-base --is-ancestor <sha> <tag>` must hold. Here the sha is the squash merge commit, so the ancestry check is valid; the branch-commit false negative (finding-175) does not apply. Fails today only through test 1.

These are structural checks on a release artifact (is the ledger well formed and complete), not a health number, so they may gate the tag; they never gate a merge to main.

**Ticket line:** "Triage every open bug against the rc sha into a ledger, close the fixed set, and add CHANGELOG.md with a verify check." Edges: blocks C1's rc tag (the notes come from it); blocked by C14 (`fenced` needs the fence). Independent of C3 and C5.

## Tests I expect to be hardest to make fail first

1. **C1, the rc rehearsal, has a hole that may make the rehearsal itself the collision.** `.goreleaser.yml` sets `prerelease: auto` (line 65), and the `brews` block (lines 80 to 96) sets no `skip_upload` and no formula name. If goreleaser's documented default (`skip_upload: false`, which I have not checked against the pinned version, since the action runs `version: latest`) holds, `v0.1.0-rc.1` uploads `Formula/marvel.rb` and replaces the alpha formula, the same collision C1 names. So the formula rename must land before the rc, and the C1 check should assert the tap diff after the rc touches only the stable formula's file.
2. **C3's schema freeze cannot fail first.** A test that pins the schema constant, or opens a committed schema-1 fixture, passes today. Its value is regression only. Say so in the ticket, and make the real check a release-verify step: the tag's binary opens a store written by the previous `v0.1.x` tag. For `v0.1.0` there is no previous tag, so it is vacuous until `v0.1.1`.
3. **C5, install from the tap in a clean HOME and run the quick start.** It fails today trivially (no stable formula), so a red tells you nothing about the quick start itself. Split it: a test that runs the quick start's commands against the built binary with no source tree on the path (fails today on `examples/` and `../bin/simulator`), and the tap install as a separate C1 check.
4. **C9, assert `/tmp/marvel-h-*` did not grow during a package run.** Other packages run in parallel and share `/tmp`, so a growth check is flaky under `go test ./...`. Make it fail first by name instead: the test asserts its own layout's tag has no `/tmp/marvel-h-<tag>` after cleanup.
5. **C14, a fence nobody checks drifts.** A doc fails no test. Make it checkable: walk the cobra command tree and fail if a verb is in neither the stable nor the experimental list. Fails today (no list).

The rest look easy to make fail first: C8 (run `go list` under `RunWith`, assert the scratch home is gone), C7a (assert a crash replacement's env holds `MARVEL_PREDECESSOR`), C2 (a mise-channel path must refuse), C13 (each NOTE asserted on a fixture).

## Blocker order for the rc (edges)

- C14 blocks C4 and C3 (the fence decides `fenced` and what the contract covers).
- C3 and C4 block the rc tag; C5's docs half blocks the rc tag.
- C1's formula rename blocks the rc tag (test 1 above); the rc tag blocks C5's tap half and `v0.1.0`.
