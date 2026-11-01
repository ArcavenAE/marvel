# Release engineer, round 1

Lens: what "stable" promises, and whether the path that ships it has ever run. Every check is read-only on fa228925.

## 1. Items

| # | item | check | class |
|---|---|---|---|
| R1 | The stable pipeline has never run. `release.yml` fires on `v*` tags only, and there are 0 `v*` tags against 749 `alpha-*`. The alpha path is a different pipeline (a hand-built job in `ci.yml:252-660`); the stable path is goreleaser. The first stable tag would be the first run of its signing, notarizing, attesting and tap steps. | `git tag -l 'v*' \| wc -l`; `sed -n 1,12p .github/workflows/release.yml` | blocker |
| R2 | Both pipelines write the same tap file. The alpha job writes `Formula/marvel.rb` (`ci.yml:533-576`); goreleaser's `brews` block (project `marvel`, directory `Formula`) writes the same name. README promises "a dedicated stable formula ... when the first `v*` release ships". As written, the stable tag replaces the alpha formula, and the next push to main puts alpha back over stable. | `sed -n 78,100p .goreleaser.yml`; `grep -n marvel.rb .github/workflows/ci.yml` | blocker |
| R3 | The goreleaser tap commit is authored as `goreleaserbot` through a token, so it lands unsigned in the tap. The fleet rule is signed commits only. | `.goreleaser.yml:84-87` | should |
| R4 | `goreleaser-action` runs `version: latest` (`release.yml:37`), so the stable build tool is unpinned while every other tool in CI is pinned. | `sed -n 35,38p .github/workflows/release.yml` | should |
| R5 | #523, still OPEN: `daemon reexec` under mise restarts the old binary while the version output says it adopted the new one. An upgrade that reports the wrong version is the first thing a stable user hits. | `gh issue view 523 --json state` | blocker |
| R6 | #756, still OPEN: `marvel upgrade` overwrites a mise or hand-pinned binary in place with no warning. Stable users pin. | `gh issue view 756 --json state` | blocker |
| R7 | Store upgrades have no contract. `internal/api/bolt.go:150-152` refuses any older non-zero schema with "no migration from it"; #524 (the v1 to v2 migration logs nothing) and #636 (it stamps one directory on every undeclared team) are OPEN. Stable must say which on-disk schema it starts from and promise that the next stable opens it. | `sed -n 135,155p internal/api/bolt.go` | blocker for the promise, should for #636 |
| R8 | No changelog. There is no CHANGELOG file, and the alpha release body is "Automated alpha release from push to main". A stable user has nothing that says what changed or what breaks. | `ls CHANGELOG*` (no match); `ci.yml:478` | should |

## 2. Not in scope

A host-health watcher inside the daemon. It is a new long-running subsystem, and the release candidate would ship it untested on the one path (R1) that has never run. The operator verbs that already exist (`marvel reap --homes`, `--empty-bases`) plus a fix for test-home leaks cover the first release; the watcher can land in a later stable release.

## 3. What a first release promises

A `v*` build installs, reports and upgrades to exactly the version it names, through one signed, notarized and attested pipeline that has been rehearsed. The next stable opens its store and its manifests with no hand steps, and a changelog says what changed. An alpha promises none of that.

## Proposal for round 2

Cut `v0.1.0-rc.1` before `v0.1.0` (`prerelease: auto` makes it a prerelease), so R1 and R2 run for the first time on a tag nobody depends on.
