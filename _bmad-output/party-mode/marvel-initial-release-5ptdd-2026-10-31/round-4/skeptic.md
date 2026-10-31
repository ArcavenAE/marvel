# Skeptic, round 4

Read-only on fa228925; new checks in brackets.

## 1. Contested rows: my final call, and the best case against it

| id | final | strongest argument against my own choice |
|---|---|---|
| C2 | should | under mise, an in-place swap leaves mise's version directory holding a build it did not install, and `mise ls` then reports a version that is no longer on disk |
| C6 | should | it is the second command a stranger types, and first impressions are what a first release is for |
| C7a | should, given C14 option (a) | one path, marvel-only cause, and it loses finished work; a stable promise that keeps it reads as indifference to that |
| C8 | should | the suite is what every contributor and every release CI run executes, so "not user facing" understates it for an open repo |
| C15 | should | a headless run paid twice on Ubuntu 24.04 is a real cost to a stranger, and the stranger has no way to learn why |
| C14-scope | (a) single-host core | this fleet, the heaviest user, runs mostly on bus and fleet verbs, so a promise that excludes them tells the operator little |
| C1-name | rc writes its own file now; stable takes a dedicated name; alpha keeps `marvel` | `brew install arcavenae/tap/marvel` then keeps giving strangers the alpha after `v0.1.0`, unless README says otherwise |

On C8 and "should, before C1b": that is not a class. A should with a hard `blocks` edge to the tag is a blocker by another name. Record it as should with a sequencing note in the outcome, and no edge.

## 2. Can the rulings be predicted?

- **C1-name: partly.** README line 48 already states the plan: "A dedicated stable formula will be added when the first `v*` release ships", so alpha keeps the plain name and stable gets a new one. It was committed under the operator's identity in cccc651 [`git blame -L 48,48 README.md`], but seats commit under that identity too, so git cannot show it was the operator's own word. Recommend applying README:48 and putting it on a card only if director does not treat it as ruled.
- **C14-scope: no.** No recorded ruling says which features a stable promise covers. Component independence (ADR-005) says marvel must work without the bus, which supports (a) but does not decide it. The card needs:
  - the three options, each with the verbs it covers;
  - what the operator's fleet gives up under each (bus, shift and max-age stay experimental under (a));
  - what each costs C3 (under (b) or (c), the bus's broker config and subjects become a compatibility surface);
  - a recommendation, and a valid-until date.

## 3. Tests that cannot fail first on fa228925, and their replacements

1. **Rehearsal check on the rc tag (Release engineer) and `quickstart-from-archive` on the rc tag (First adopter).** Neither can run before a `v*` tag exists. Replace both with a PR-time job that runs `goreleaser release --snapshot --clean` and then the README Quick Start against the snapshot archive in a clean `MARVEL_HOME`. It fails today at `just build`. Keep the tag-time runs as acceptance checks, not as the tests that prove the fix.
2. **`TestStoreOpensPreviousStableFixture`.** A fixture written by the rc build and opened by the rc build passes by construction. Generate a schema-2 fixture from main now with an `-update` flag and label the test a regression guard. The real check is the next tag opening the previous tag's store, which stays vacuous until `v0.1.1`.
3. **`TestRunWithRemovesTheScratchHomeAfterAGoSubprocess` (Host operator).** The child writes the read-only tree itself, so the proposed fix (point `GOMODCACHE` outside the scratch HOME) does not change what the child does. The test stays red after the fix, or goes green only if the remover also restores write permission first. Replace it with a test of the fix itself: the child runs `go env GOMODCACHE` under `RunWith` and asserts the path is outside the scratch HOME. That fails today. Keep the second test (a failed removal makes the binary exit nonzero), which is sound.
4. **`homeguard.CountTempBases`, a before and after count of `/tmp/marvel-h-*` (Host operator).** It is not a fail-first problem; it is a false-red problem. `go test ./...` runs package binaries in parallel against one `/tmp`, so a count grows from other packages' layouts. Assert that this package's own layout tags left no base, which `TestScratchLayoutUsesTheDurableHarnessBase` already covers.
5. **`TestReexecHelpNamesTheMisePath` (Release engineer).** The help already names mise and the binary the command runs from: "an install that keeps each version in its own directory (mise) adopts the new build" (`cmd/marvel/main.go:519-536`). A test for the strings `exec_path` and "Under mise" would fail today on wording, not on the defect. Drop it, and close #523 in C4 with the sha that changed the help, after one read of the issue's last comment against that text.
6. **`TestUpgradeRefusesUnownedPathWithoutFlag`.** It can fail first, but its premise is wrong. `channelOf` returns `direct` for any path outside brew and mise (`internal/upgrade/store.go:110-119`), and README offers a direct download from Releases (line 83). Refusing an unowned path refuses every user who installed the way README says. Limit the refusal to mise, and leave direct installs to the existing swap with its kept prior.

One caution, not a fail-first problem: `TestUnnamedModelAdvisoriesNamesARoleWithNoModel` needs a per-harness answer. A role names its model only through `Runtime.Args` (#1131 body; no `model` field on the runtime type [`grep -n -E 'Model +string' internal/api/*.go`: only heartbeat.go:135]), and the flag differs by harness. The advisory belongs on the adapter, and the generic adapter says "unknown", never "no model".

## 4. An edge in the chair's order that is wrong

**C14 blocks C4** is too wide. Only the ledger's `fenced` verdict needs the fence. Closing the fixed set (365, 364, 348, 341, 289, 202, 201, 119, 390) needs nothing and can start now. Split C4 into C4a (close the fixed set with shas, no edges) and C4b (the ledger and the changelog, blocked by C14).
