# finding-062: marvel upgrade is a single-plane replace-in-place with no record of what runs, and the staged binary is the natural source of its own pre-flight rules

**Date:** 2026-10-03
**Study:** `_kos/probes/brief-marvel-upgrade-intelligence.md`
**Ticket:** `aae-orc-k28s`
**Bedrock tested against:** `elem-staged-activation-upgrades`
**Code:** marvel origin/main bc327df. Every code claim below is path:line in that tree.

## The finding

1. **Today's upgrade is one plane, not two.** `marvel upgrade` fetches and replaces the binary in the same step, and `--daemon` activates it at once (`cmd/marvel/main.go:1665-1677`). Nothing stages a version apart from activating it, and nothing records which version is active.
   - The direct path writes `<bin>.new`, renames the current binary to `.old`, renames `.new` into place, and then deletes `.old` (`internal/upgrade/upgrade.go:287-328`). The prior binary is gone after every successful upgrade.
   - The brew path runs `brew update` and `brew upgrade arcavenae/tap/marvel`, and nothing else (`upgrade.go:107-121`).

2. **The channel is detected for brew and Linux packages only, and an exact version is honored on the direct path only.**
   - Detection (`upgrade.go:62-96`):
     - brew if the resolved path contains `/Cellar/` or `/homebrew/`;
     - a Linux package if dpkg, rpm or apk owns it;
     - "direct" otherwise.
   - A mise install is classified as direct, so marvel would rewrite files inside mise's install directory, behind mise's back.
   - A `dev` build is treated as alpha (`upgrade.go:197-200`).
   - `--version` is dropped on the brew path (`upgrade.go:52`). This is marvel#485, observed on kinu: it asked for `de9e409`, installed `b533ca5`, and exited 0.
   - On the direct path, an exact tag match is tried first, then the first substring match from one unpaginated listing (`upgrade.go:173`, `:202-213`), so a partial tag can resolve to a different release.

3. **Failure is reported as success in two places, and `--daemon` then activates whatever is on disk.**
   - A failed `brew upgrade` returns nil (`upgrade.go:124-128`; #486).
   - The package path returns nil after printing advice (`upgrade.go:54-56`).
   - In both cases `--daemon` still sends `reexec` (`main.go:1665-1677`). A host can report "upgraded and re-executed" while running the old build.

4. **Nothing is verified before activation.**
   - The direct path checks HTTP 200 and a non-empty body (`upgrade.go:299-310`).
   - The release pipeline produces `checksums.txt` and attestations (`.goreleaser.yml:36-38`; `.github/workflows/release.yml:168-206`; `ci.yml:448-458`). upgrade.go checks neither.
   - Nothing runs the new binary before the swap or before the reexec.

5. **Agent continuity across activation already exists. Control-plane continuity and recovery do not.**
   - `daemon reexec` keeps the pid, the panes and the broker (`daemon.go:610-622`, `:643-715`). finding-042 executed it under 18 live panes with 0 orphans. For the agents, zero-downtime is largely here today.
   - The gaps are in the daemon:
     - **Exec fails after detach:** the old process logs and exits 1 with no daemon left (`daemon.go:2271-2273`). Agents survive; the operator must start one by hand.
     - **The new image fails at start** (a store refusal, a bad flag): nothing restarts it and nothing falls back (`daemon.go:263-266`).
     - **Bus leaf:** a reexec drops it silently (marvel#339).
     - **Credentials:** transient credentials are dropped on every reexec (`daemon.go:644-656`).
     - **Working directory:** the cwd is inherited, so a daemon started in a worktree stays there across every reexec. This becomes load-bearing once #482 stamps team directories from it.

6. **What runs is unobservable** (finding-061, re-verified).
   - `marvel version` reports the client (`main.go:1639-1645`). The daemon logs no build (`daemon.go:267`), and no RPC returns one.
   - The only client and daemon comparison is a home-path warning (`daemon.go:94-108`).
   - Without this, no post-upgrade check, skew rule or version store can be written, because "active" has no instrument.

7. **The store has no migration path yet, so the first migration sets the rollback rules.**
   - Schema 1 refuses a newer store and fails fast on an older one (`internal/api/bolt.go:124-133`).
   - marvel#482 (open) would add the first one-way migration, v1 to v2, with a kept `.v1.bak` and refusals for an interrupted migration, a `/` cwd and a newer store.
   - Its rollback is manual: stop, reinstall the prior binary from its tag, move the backup over the store, and re-apply teams created since.
   - That is the first case where "rollback with live agents" has a hard edge: once a one-way migration commits, rolling back loses state.

## Answers to the bedrock's open questions

- **Which pre-flight checks are decidable.** The decidable ones are those where the target version knows what it changes and the host knows what it has:
  - store schema (current against target, and whether the step is one-way);
  - manifest and config fields the target removes or renames, checked against the applied manifests in the store;
  - RPC, shim and CLI skew windows;
  - the minimum daemon for rendered bus users (#481);
  - cwd and placement facts the target will stamp (#482);
  - the integrity of the staged binary.

  Not decidable from the host: whether a vendor harness (claude, codex) behaves the same under the new marvel. That stays a canary question.
- **Where the rules live.** Pluto and kubent embed versioned rule data in the checker, keyed by the version that changes something (DOCUMENTED). marvel has a better source, given the bedrock's own ordering of stage before activate: **the staged binary itself.** A staged target can be asked, read-only, "what do you change relative to the running version and this store", because it carries its own schema version, its own manifest schema and its own migrations. The rules then ship with the code that makes them true, and cannot go stale against it.
- **Version-store layout.** HYPOTHESIS, smallest form: keep the binaries the channels produce where the channels put them, and record the active version and its prior on marvel's side (a small record under `~/.marvel/`, plus the retained prior binary). brew kegs, mise `installs/<tool>/<version>` and release assets are already side by side by version (DOCUMENTED). marvel's direct path is the only one that destroys the prior binary. This is the Android A/B and NixOS-generation shape: activation moves a pointer and never overwrites.
- **Rollback with live agents.** It is allowed and cheap until a one-way store migration commits: reexec into the retained prior binary, as nginx's old master or Android's untouched slot do. After the commit it is a restore from the backup, with the loss #482 states. That makes the migration the commit point, and argues for the try-then-commit pattern of NixOS `test`, Nokia SR Linux `commit confirmed` and Android `markBootSuccessful`:
  1. Activate.
  2. Health-check (the running version equals the target, the adopted pane count equals the pre-upgrade count, the bus leaf is up, sessions are healthy).
  3. Only then run the one-way migration, or fall back automatically if no confirmation arrives in a bounded window.
- **Channel set.**
  - **brew:** honor an exact version only through a versioned formula (`marvel@<version>`, `brew extract`); otherwise refuse (#495 option b).
  - **mise:** detect by path (or `mise where`) and delegate to `mise use github:ArcavenAE/marvel@<version>`. Never rewrite inside mise's directory. mise's github backend verifies checksums and attestations, and `mise.lock` pins per platform (DOCUMENTED).
  - **direct:** verify the checksum or attestation, and keep the prior binary.
  - **dev:** refuse to self-upgrade a dev build.
  - **package:** exit nonzero with advice.

## Recommendations, in order, each a candidate PR

Each item stands alone and lands in its own PR unless marked otherwise. The order puts truthful reporting first, because every later item reads it.

1. **Make failure fail** (#486, plus the `--daemon` follow-on). Return an error when `brew upgrade` fails, telling it apart from "nothing to do" with `brew outdated --json`. Return nonzero on the package path. Never send `reexec` unless the install changed the binary. Small; one PR.
2. **Honor or refuse an exact version** (#485). On brew, refuse `--version` unless it equals the tap's formula version or a versioned formula exists. On direct, exact tag match only (drop the substring fallback) and page the listing. Refuse to self-upgrade a `dev` build. One PR; it closes the kinu case.
3. **Report the running build** (finding-061). A startup log line, a status RPC field, and a CLI warning when the client and daemon builds differ. Small, and a precondition of 6, 7 and 8. One PR, independent of 1 and 2.
4. **Detect mise and delegate.** Classify a binary under mise's install tree, or reported by `mise where marvel`, as mise. Print, or run with consent, `mise use github:ArcavenAE/marvel@<version>`, and never rename inside mise's directory. Runs after open check 2 (does mise resolve marvel's alpha tags and assets). One PR; this is the mise answer the ask wants, and it fits #495's pin case.
5. **Verify before swap and keep the prior binary.** On the direct path, check the release checksum (or attestation) and run the new binary's `version` to confirm the tag before the rename. Keep `.old` as the named prior version instead of deleting it. One PR; the first, smallest piece of the version store.
6. **Pre-flight as a report: `marvel upgrade --check <target>`.**
   - Fetch and stage the target without activating it.
   - Ask the staged binary for its change report against the running build and the live store: schema step and whether it is one-way, removed manifest fields found in applied teams, skew windows, placement facts.
   - Print it with severity-specific exit codes (Pluto's 2 and 3; kubent's report-by-default).
   - Per ADR-007 it surfaces and does not gate. The exception is where a structural rule is already ratified (the store's refusal of a newer schema is one); those refuse as they already do.
   - Two PRs: the report subcommand on the target side, then the `--check` driver.
7. **Activation with a health check and a bounded fallback.**
   - Before detach: pre-check that the successor can start, by running the staged binary in a read-only open of the store.
   - After reexec: assert the reported build equals the target, the adopted count equals the pre-reexec count, and the bus leaf is up (#339).
   - On failure before any one-way migration: reexec into the retained prior binary.
   - On exec failure after detach: try the prior binary before exiting.
   - Two PRs: successor pre-check, then post-activation check plus fallback.
8. **Migrations commit after confirmation.** Once #482 lands, run one-way migrations only after step 7's check passes, so the window where rollback is free covers the activation. Coordinate with #482's author; it may be a follow-on rather than a change to #482.
9. **Placement on activation.** Have upgrade or reexec report the daemon's cwd and refuse, or warn, when it is a linked worktree before a migration that stamps it. #482 warns and does not refuse. A `--cwd` or declared daemon home on reexec is the larger option. One PR, after 8.
10. **Rollout across clusters, last.** Write marvel's skew table (CLI and shim within N builds of the daemon, daemon before CLI, the director and hub order). Then let a staged rollout drive `--check` and activation cluster by cluster, with an EKS-style readiness wait between them. This is the "zero-downtime upgrade" acceptance test at fleet scale, and it needs 3, 6 and 7 first. Design first, no PR yet.

Items 1, 2 and 3 are fixes against filed issues and can go now. Items 4 and 5 need open checks 2 and 1 first. Items 6 to 10 are the k28s design core; they want an architect pass before any builder.

## Limits

- Read-only. No upgrade, reexec, brew or mise command was run. The brew exit-status premise of #486 and mise's handling of marvel's tags are open checks, stated in the study.
- Code claims are at bc327df. #482 is open and was read from its PR, not from code on main.
- The prior-art claims are DOCUMENTED (vendor docs), not executed.
- Kinu facts are from issues and one install read. Mokuzai and the corporate cluster were not read.

## Edges

- tests: `elem-staged-activation-upgrades` (bedrock). The two-plane model is unrealized in code, and this names the order to realize it.
- extends: finding-042 (activation primitive), finding-061 (running build unobservable).
- informs: marvel#485, #486, #495, #339, #482; aae-orc#461 (S4-3, S4-4).

## Review note (architect, 2026-10-03)

I re-read the load-bearing code claims at origin/main, which is still bc327df, and each holds as cited: `upgrade.go:52`, `:124-128`, `:287-330`; `main.go:1665-1677`; `daemon.go:610-622`, `:263-267`, `:2271-2273`; `bolt.go:124-133`; the `dev` build mapping to the alpha prefix; and CI's retention of the newest 30 alphas. #482 supersedes #471, so the references to #482 are current.

One correction to recommendations 6 and 7, measured. The daemon opens the store read-write (`internal/api/bolt.go:103`, a 5s lock timeout), and bbolt takes an exclusive `flock` for that (`bolt_unix.go`, `LOCK_EX`). A staged binary's read-only open needs a shared lock, so while the daemon runs it waits 5s and fails. Neither "ask the staged binary against the live store" (6) nor "the staged binary in a read-only open of the store" before detach (7) works as written. Two shapes that do: the running daemon hands the staged binary the facts it needs over RPC (schema version, applied manifest fields), or it writes a consistent snapshot (`tx.WriteTo`, as #482's backup does) that the staged binary opens instead. Choosing between them is part of 6's design.

A second point for 8. A one-way migration today runs inside `Open`, before the new daemon serves, and the new binary refuses an older store until it has migrated. "Run the migration only after the health check passes" therefore needs the new binary to run, at least read-mostly, on the old schema until it is confirmed, or needs the health check to run against a migrated copy. That is a design question, not a sequencing change.

Of items 6 to 10, I judge 6, 7, 8 and 10 need a design first: 6 and 7 for the store-access shape above and the cross-version report contract; 8 for the point just made; 10 as the finding already says. Item 9's report and warning (the daemon's cwd, a linked worktree before a stamping migration) can go straight to a builder; its larger option, a declared daemon home on reexec, needs design.
