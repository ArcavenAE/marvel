# finding-039: two release runs race on the tap push; the loser leaves the tap behind

**Date:** 2026-09-15
**Probe:** merging the brief 10 stack (#264 through #269) into main in one sitting
**Confidence:** bedrock for the mechanism (read from the run logs); frontier for the fix until one merge cycle proves it

## Symptom

Six squash merges landed on main within a few minutes. Each push cut its own
alpha release. The CI run for the last commit (135015d) published its release
with all eight assets, then failed in the `Create Release` job at the
`Update Homebrew formula` step:

```
[main c62f9e5] chore(formula): update marvel to alpha-20260915-162659-135015d
 ! [rejected]        main -> main (fetch first)
error: failed to push some refs to 'https://github.com/ArcavenAE/homebrew-tap.git'
```

The run for the previous commit (1ff8176) had pushed the tap between this
run's `git clone` and its `git push`. Outcome: the tap serves 1ff8176 (five of
the six PRs), the newest release exists but is not what `brew upgrade`
installs, and `Attest Release Provenance` was skipped for 135015d because it
depends on the failed job.

## Mechanism

The tap step is clone, edit, commit, push, with no retry and no rebase. Two
release jobs for two pushes to main run concurrently (the `release` job has no
concurrency group), and whichever pushes the tap second is rejected as a
non-fast-forward. The step treats that as a hard failure, so the job fails
after the release is already public, and the downstream attestation is
skipped.

Re-running the failed job is not a repair: `gh release create "$TAG"` runs
first in that job and refuses because the release already exists.

## Fix shape

Two changes, both in `.github/workflows/ci.yml`:

1. The tap push rebases onto the tap's current main and retries a bounded
   number of times. The clone is the bot's own fresh checkout, so a rebase
   there rewrites nothing anyone holds.
2. The `release` job carries a concurrency group with `cancel-in-progress:
   false`, so release jobs for successive pushes queue rather than interleave.

Either alone closes the observed race; together they also cover the case
where something other than a marvel release pushes the tap in the window
(the `jr-d` bump at 16:29:26 sat between two marvel bumps in this very
sequence).

## Repair for the observed instance

The next merge to main cuts a release whose tap push has no competitor and
moves the tap to head. No hand edit of the tap is needed, and none was made.

## Relevant paths

`.github/workflows/ci.yml` (`release` job, `Update Homebrew formula` step);
tap commits cf3dedf, 94e9fc1 in ArcavenAE/homebrew-tap; marvel CI run
34994829897.
