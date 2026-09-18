# finding-042: daemon reexec executed under a live fleet, resolving through the brew symlink

Confirms the activation primitive of `elem-staged-activation-upgrades` end to
end, and answers the standing question of whether `marvel daemon reexec` is
safe when the old brew keg is gone. It is. Executed on f601b6e.

## What ran

`marvel daemon reexec` against a live daemon (pid 82578) supervising 18 agent
panes. Result:

- The process image was replaced in place via `syscall.Exec`; the daemon pid
  stayed 82578 across the reexec (`Reexec` at daemon.go:576; the exec function
  defaults to `syscall.Exec`, daemon.go:330).
- All 18 panes were adopted after the reexec; 0 were left orphaned.
- HEALTH lagged one heartbeat, then settled. A transient, not a loss.

## Why a deleted old keg does not strand it

`Reexec` resolves the binary through `selfExecPath` (daemon.go:594), which
calls `os.Executable()` and passes the result through `cleanExecPath`
(daemon.go:605). `cleanExecPath` only trims the Linux `" (deleted)"` suffix; it
does not resolve symlinks.

On macOS `os.Executable()` returns the invocation path, which for a brew
install is the stable symlink in the brew bin, not the resolved keg target. So
reexec re-execs through the symlink, and brew has already repointed that
symlink at the new keg by the time reexec runs. The old keg being deleted does
not matter, because the path reexec holds never named the keg.

On-machine check: a scratchpad Go binary reached over a PATH symlink confirmed
`os.Executable()` returns the symlink path, not the target it points at.

## Consequence

reexec is safe on macOS with brew as the bootstrap plane, and it does the job
`elem-staged-activation-upgrades` assigns it: activate a staged version under a
running cluster without losing the agents. A full daemon restart is not needed
to pick up a brew upgrade.

This does not reopen finding-041. That finding is about the daemon needing a
keychain-capable macOS session at START. reexec preserves the session and
environment of the running process (it is the same pid), so it stays
keychain-capable and does not re-trigger that failure.

Related: `elem-staged-activation-upgrades` (the two-plane model; reexec is the
activation primitive), finding-041 (keychain-capable session at start, a
separate concern), the idea file `_kos/ideas/marvel-staged-activation-upgrades.md`.
