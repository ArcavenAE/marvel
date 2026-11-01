# Skeptic, round 1

Lens: falsify the inputs and the likely blocker claims. Read-only commands on main at fa228925 and on the host's temp directory; each check is in brackets.

## Falsified or reframed

1. **The test-home leak is not "a killed test binary".** It happens on normal runs. `homeguard.RunWith` does call `os.RemoveAll(home)` on return, but discards the error (`defer func() { _ = os.RemoveAll(home) }()`). Tests that shell out to `go build` or `go list` (rolescheck, applycheck, watcherprobe, reexec_target, readiness, envelopeschema) do so under the scratch HOME, so Go fills `$HOME/go/pkg/mod` with a module cache whose directories are read-only. RemoveAll then fails, and the failure is silent. [sampled leaked dirs: they hold `go/pkg/mod/{gopkg.in,go.etcd.io,golang.org,github.com}`; of the 300 newest, 164 hold 1801 files, 54 hold 960, 54 hold 555; 87 of the directories in one dir are not writable; `grep -c GOMODCACHE\|modcacherw` in homeguard and the go-calling tests: 0]. Side effect: every such test binary downloads the module set again, onto the network and into the temp tree. Fix shape: set `GOMODCACHE` to the real cache (or `GOFLAGS=-modcacherw`) in the guard, and report a RemoveAll failure.
2. **The incident does not point mostly at marvel.** The leading file-event source in the root trace was git from harness sessions over the orc tree (about 280 worktrees), and the supervisor's own sample 2 shows the file-event daemon not growing while no marvel test home was touched in the last hour. marvel's share is the test-home churn above and the harness homes (already handled by #1157, #1159, #1160). Neither ships to a user: test homes exist only on hosts that run marvel's own suite.
3. **The open-issue count overstates the release debt.** 11 open issues carry a comment that says the issue is fixed, resolved, or landed: 365 364 348 341 324 317 289 202 201 186 119 [`gh issue list --state open --json number,comments` filtered for "fixed on main|resolved on main|landed in #|looks fixed"]. Three were in round 1's lifecycle and adopter lists: #348 (fixed by #350), #201 (fixed by #244 and #246), #186 (reporting half in #192). #390 is also fixed with no comment: `daemonPositionalArgs` applies `cobra.NoArgs` [cmd/marvel/daemon_lock_hint.go:70]. #365 is guarded by `api.ValidateReplicas` [internal/daemon/daemon.go:1932]. Any blocker list built from issue titles inherits this drift.
4. **#523 is partly addressed.** `reexec` takes an `exec_path` so a mise install reaches the new build, and an unproven target refuses with the stop-and-start path [internal/daemon/reexec_target.go:14-30, marvel#592]. What remains is the help text (comment on #523 names `cmd/marvel/main.go:378`).
5. **#951's subject claim needs a recheck before it is called a blocker.** A role-scoped inbox subject exists now (`agent.<ws>.<team>.role.<role>.inbox`, internal/bus/declared.go:298), so whether mail still rides a generation-numbered subject depends on what the seats subscribe to, which the issue (no comments since 10-19) does not say.

## What I would class

| item | class | why |
|---|---|---|
| Board reconcile: close the fixed issues, recheck the rest at one sha | blocker | a release note written from this board would list fixed bugs as known issues |
| A written compatibility promise: what a stable version guarantees about store schema, config keys, and upgrade and rollback | blocker | "stable" is that promise; without it a `v` tag is only a rename of `alpha-*` |
| homeguard cleanup (item 1) | should | cheap, removes most of the temp churn on dev hosts; not user facing |
| #1162 and #884 test brokers | should | same class as item 1: dev-host hygiene, not product |
| A host-health watcher in the daemon | after-release, or never in marvel | see below |

## Not in the release scope

A host-health watcher inside the daemon. Sampling swap, file-event memory, and other tools' temp dirs is host monitoring. It would make marvel own failures it does not cause (git polling, other tools' temp dirs, host agents) and puts a new always-on loop into the one process every seat depends on, right before a stability promise. The reap verbs already cover what marvel leaves behind. If the fleet wants a host watch, it can be a separate small tool or a scheduled role, which keeps marvel independent of it (SOUL section 2).

## Q3: what a release promises

That an upgrade within the version line does not break a working setup or lose state, and that what `get` and `describe` print is true. An alpha promises neither. So the release question is less "which bugs" and more "which state can an upgrade lose, and which readings lie".

## Q4: marvel's share of the incident

marvel's: test-home cleanup (item 1), leaked test brokers (#884, #1162), harness homes and codex daemons left by stopped seats (landed). Not marvel's: harness git polling, seat age and harness version (an operator and harness choice), the lost GUI session and keychain logins, other tools' temp dirs.
