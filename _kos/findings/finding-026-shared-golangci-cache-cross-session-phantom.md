# finding-026: the shared golangci-lint cache reports phantom findings from other sessions' checkouts

**Symptom.** `just lint` / the `lefthook` pre-commit `lint` step failed with
one errcheck issue against a file that does not exist in this checkout:

```
../../../../../private/tmp/.../6cd4c178-.../scratchpad/arms/codex-pt8k/internal/runtime/codex/rollout.go:116:15:
  Error return value of `f.Close` is not checked (errcheck)
```

Two `level=warning` lines above it say golangci-lint could not read that
file at all ("no such file or directory") yet still reported the issue.
The path is a *different* concurrent session's marvel checkout (session
`6cd4c178`, under its own scratchpad `arms/codex-pt8k/`), not this tree.

**Why it happens.** golangci-lint caches results in a machine-global
directory (`~/Library/Caches/golangci-lint`), keyed in a way that is not
isolated per working tree. When two sessions on kinu both lint module
`github.com/arcavenae/marvel` from different checkouts, a finding cached
by one surfaces in the other's run, and persists after the originating
file is gone. `go build`, `go vet`, and `go test ./...` are unaffected —
only the golangci cache carries the phantom.

**Workaround.** `golangci-lint cache clean` before re-linting clears the
stale entry; the real tree then lints clean. This is cache maintenance,
not a gate bypass — the flagged file is not in the repo, and `--no-verify`
is never the answer (global rule).

**Class, not a one-off.** Any two concurrent sessions building the same Go
subrepo on this host can cross-contaminate. If it recurs outside marvel,
promote to an orc-level finding; the cache is machine-global, so the class
is fleet-wide even though it bit here first. Candidate durable fixes (not
taken here): a per-checkout `GOLANGCI_LINT_CACHE`, or `cache clean` wired
into the lint recipe's preflight.

Refs: `.claude/rules/tooling-friction.md` (capture before workaround);
aae-orc-m4of (the change whose commit this blocked).

## Second instance, 2026-09-12: phantom findings on files that DO exist

The lefthook pre-commit `lint` step failed with six `staticcheck SA5011`
(possible nil pointer dereference) findings against `internal/api/manifest_test.go:787`
and `internal/runtime/claudecode/parser_test.go:425,582`. All three sites are
`if x == nil { t.Fatal(...) }` followed by a use of `x`, which staticcheck
only flags when it has lost the fact that `t.Fatal` does not return. The
files are unchanged on main, main's CI lints clean with the same
golangci-lint 2.12.2, and this checkout had touched none of them. Two
consecutive runs reported the same six; `golangci-lint cache clean` and a
rerun reported 0 issues.

Same class, different shape: the first instance reported a file that was
not in the tree, this one reported wrong facts about files that were. The
likely carrier is the same machine-global cache with entries written by a
concurrent session on a different toolchain (`go1.26.5` here against the
module's `go 1.25.4` directive; four other marvel sessions were live on the
host). The workaround is unchanged and is still cache maintenance, not a
gate bypass. The durable-fix candidates above stand; this instance is the
second vote for wiring `cache clean` into the lint preflight.

Refs: aae-orc-bxeh (the commit this blocked).
