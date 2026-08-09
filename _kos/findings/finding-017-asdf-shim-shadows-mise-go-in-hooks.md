# finding-017: the asdf shim shadows mise's go, so every bare `go` in a git hook fails

**Date:** 2026-08-08
**Status:** MEASURED on this host. Captured before the workaround, per
`.claude/rules/tooling-friction.md` (orc `_index.md` sibling).
**Surfaced by:** `git push` on branch `probe/recovered-eooi-tzzn`.
**Related:** issue #119 and the in-flight `mise.toml` pin, which addresses
the sibling case for `golangci-lint` and explicitly leaves `go` alone.

## Symptom

`git push` from marvel fails in the lefthook `pre-push` hook before any
network operation:

```
🥊 lefthook v2.1.10   hook: pre-push
┃  test-race ❯
No version is set for command go
Consider adding one of the following versions in your config file at
/Users/michael.pursifull/work/aae-orc/marvel/.tool-versions
golang 1.25.4
golang 1.24.10
exit status 126
error: failed to push some refs to 'github.com:ArcavenAE/marvel.git'
```

The hook is `run: go test ./... -race -count=1`, so the failure is a bare
`go` that cannot resolve, not a test failure. Exit 126 is the shim
refusing, not a compile or test error.

## Mechanism

Two version managers are installed and both claim `go`.

| Fact | Value |
|---|---|
| `command -v go` | `/Users/michael.pursifull/.asdf/shims/go` |
| asdf golang versions installed | 1.25.4, 1.24.10 |
| `.tool-versions` in marvel | absent |
| `.tool-versions` at orc root or `$HOME` | absent |
| mise config providing go | `~/.config/mise/config.toml`, go 1.25.4 |
| `mise exec go@1.25.4 -- go version` | `go1.25.4 darwin/arm64` |
| `go.mod` directive | `go 1.25.4` |

So the toolchain is installed twice and reachable by exactly one of the
two managers. asdf's shim directory precedes mise's on `PATH`, and asdf
resolves a version only from a `.tool-versions` file, which no directory
on the lookup path provides. The shim therefore refuses rather than
falling through to the next `go` on `PATH`, which is the behavior that
makes this a hard failure instead of a silent version mismatch.

An interactive shell with mise activated does not show this, because
activation prepends mise's shims. A git hook is spawned without that
activation, so the asdf shim wins. That asymmetry is why the defect is
invisible during ordinary work and fires only at commit or push time.

## Why this is worth a finding rather than a config tweak

The single-repo reading is that marvel lacks a `.tool-versions`. That is
true and would fix it. It is not the whole shape:

1. **It is not marvel-specific.** Any repo whose hooks call a bare `go`,
   `gofumpt`, or `golangci-lint` fails the same way on this host. marvel
   is where it was noticed, not where it is scoped.
2. **The remedy has a second-order cost.** Adding `.tool-versions`
   alongside the `mise.toml` that issue #119 is adding means two
   toolchain manifests pinning overlapping tools, with nothing asserting
   they agree. That is the drift the `mise.toml` pin comment is
   explicitly trying to prevent for `golangci-lint`, reintroduced one
   file over.
3. **The failure lands exactly where `--no-verify` is tempting.** A hook
   that blocks a push for a reason unrelated to the change is the
   documented trigger for bypassing the hook, which would also skip
   `fmt-check` and `vet`. The `mise.toml` comment names this hazard for
   the lint step; it applies with more force to pre-push, which is the
   last gate before the branch leaves the machine.

## Options, not yet decided

- **(a) Retire asdf on this host.** Removes the shadowing at the root.
  Largest blast radius: asdf may be providing tools nothing else does.
- **(b) Pin `go` in marvel's `mise.toml`** alongside `golangci-lint`, and
  ensure hooks run under mise. Consistent with #119's direction, but does
  not stop the asdf shim from winning inside a non-activated hook, so it
  needs the hook invocation to change too.
- **(c) Add `.tool-versions` to marvel.** Smallest change, immediate fix,
  and accepts the two-manifest drift risk in (2) above.
- **(d) Make lefthook invoke the toolchain explicitly** rather than by
  bare name.

Option (b) plus (d) is the shape that matches the reasoning in #119's
pin comment, but this finding does not rule; it records that the choice
exists and that (c) alone buys a fix at the price the pin comment warns
about.

## Workaround applied in this session

The push was completed by running it with mise's toolchain ahead of the
shim, changing no repository file and skipping no hook:

```sh
mise exec go@1.25.4 -- git push -u origin <branch>
```

The hook still ran, and the race suite still executed. Recording it here
rather than only in a shell history so the next person does not
rediscover the shim behavior from the error text alone.
