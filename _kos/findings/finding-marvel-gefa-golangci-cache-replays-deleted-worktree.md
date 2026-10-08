# finding-marvel-gefa: the pre-commit lint hook replays errors from a deleted worktree's cache

Date: 2026-10-07
Status: frontier

## Why this matters

A builder's commit is refused by the lint hook for two errors in files that are not in its tree and that carry the `//nolint` the hook would accept. Each builder that hits it either re-investigates or routes around the hook, and the second is the failure the hook exists to stop. The cause and the one-command workaround are recorded once here.

## 1. Symptom

- **MEASURED:** 2026-10-07, committing in a marvel worktree whose change did not touch `internal/runtime/codex`, `golangci-lint run ./...` (golangci-lint 2.12.2, go1.26.2) from lefthook's pre-commit exited 1 with two issues. Verbatim, trimmed to the lines that matter:

```
level=warning msg="[runner/source_code] Failed to get line 52 for file <repo parent>/marvel-wt-builder-exit-gate/internal/runtime/codex/ratelimits.go: failed to get file ..."
../marvel-wt-builder-exit-gate/internal/runtime/codex/ratelimits.go:52:15: Error return value of `f.Close` is not checked (errcheck)
../marvel-wt-builder-exit-gate/internal/runtime/codex/rollout.go:116:15: Error return value of `f.Close` is not checked (errcheck)
2 issues:
* errcheck: 2
```

- **MEASURED:** the worktree `marvel-wt-builder-exit-gate` no longer existed: `git worktree list` and `ls` both showed no such path. The same two lines in the committing tree read `defer f.Close() //nolint:errcheck // read-only handle`. A second `git commit` failed the same way, so it was not a one-off.
- Main's CI lint is green on the same lines.

## 2. Cause, suspected

- **INFERRED, needs probe:** golangci-lint replays cached results keyed to absolute paths. The cached run came from a worktree that has since been removed, so when it tries to read the source line to apply the `nolint` directive ("Failed to get line 52 for file ...") it cannot, and the issue is reported as if unsuppressed.

## 3. Workaround

- `golangci-lint cache clean`. It is local to the host and shared by every worktree there, and the next lint run is slower. The hook still runs on the real code afterwards, so nothing is bypassed.

## 4. Probe result

- **MEASURED:** after `golangci-lint cache clean` (exit 0), the same commit's lint hook ran on the same tree and reported `0 issues.`, and the commit went through with the hook on. The failure was gone with no change to any source file, which fits a stale cache and does not prove why it keyed to a removed path. Not probed: whether the cache is keyed by absolute path or by something else, and whether a removed worktree always poisons it.
- **Ask for a later probe:** whether the hook should clean or bypass the cache when a worktree has been removed, or whether the cache location should be per worktree. This finding proposes nothing further.
