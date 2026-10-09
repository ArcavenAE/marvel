# Probe brief: where are the seams in marvel's three largest files, and what does the current layout cost?

**Status:** COMPLETE 2026-10-09. Finding: `_kos/findings/finding-marvel-4m2m-code-seams-and-their-cost.md`.
**Question:** where the natural seams are in `internal/daemon/daemon.go`, `internal/team/controller.go` and `internal/session/manager.go`; which splits fit the services backplane (`docs/design/service-provider/`) and the supervisor extraction gate (`docs/design/services-list.md` section 3.5); and what the layout costs in merge conflicts, review size and test isolation.
**Medium:** read-only, against marvel `origin/main` at `7f1c81e` (2026-10-08T22:30:53-05:00), in a worktree; GitHub PR history for the window; `go test` on the same tree. No source edits.
**Commissioned by:** the operator ("have arcaven supervisor detail and dispatch the code study"), relayed by director and the supervisor.

## Why this probe exists

Two earlier pieces of work point at the same files. The 2026-09 merge-conflict reflection (`docs/merge-conflict-reflection-2026-09.md`) named `daemon.go` the hottest file and proposed a same-package split of its handler bodies. The services design gates any second managed service on extracting the bus supervisor into `workload.Process`. Neither says what the layout costs today, measured. This probe measures it before anyone proposes a restructure.

## Method

Every number in the finding names the command that produced it. The commands, in short:

1. **Size and shape.** `wc -l` on the three files at six dates (`git rev-list -1 --before=<date> origin/main`); `go list ./internal/...` for the package count; `go list -f '{{.ImportPath}}{{range .Imports}} {{.}}{{end}}'` for the internal import graph, with fan-in and fan-out computed from it.
2. **Seams.** A `go/ast` walk over each file listing every function with its receiver and line span, then grouped by contiguous concern. Coupling of a candidate seam: references to the receiver (`d.`) inside its line range, and its callers by `grep -rlw`.
3. **Window.** 2026-09-08T00:00Z to 2026-10-09T00:00Z (31 days) for commits; `merged:2026-09-08..2026-10-08` for PRs.
4. **Touch rate.** `git log <window> --format=%h origin/main -- <file> | wc -l`.
5. **Conflict proxy.** For every merged PR in the window, `gh api repos/ArcavenAE/marvel/pulls/<n>/commits` and count commits with two parents (a base merge folded into the branch), and among those, merges whose message contains "onflict". Compared between PRs that touch one of the three files and PRs that touch none, bucketed by PR diff size. Limits: a base merge is also done when a branch is merely behind, and a conflict resolved by rebase leaves no trace, so this is a proxy, not a count.
6. **Review size.** From `gh pr list --json files,additions,deletions`: lines changed in the file and in the whole PR, medians.
7. **Test isolation.** A `go/ast` walk over `internal/daemon/*_test.go` classifying each `Test` function by whether it reaches `Start`/`startTestDaemon`/`StartMRVL` (starts a daemon), reaches `New`/`NewWithOptions` or a `Daemon{}` literal (constructs one), or neither, following same-package helpers. `skipIfNoTmux(t)` call counts per package. `go test -count=1` and `go test -count=1 -json` wall and per-test times, with every `MARVEL_*` variable unset so no test reaches a live daemon.

## Out of scope

Any code change. Any judgment of whether a split is worth building: the finding gives options and a recommendation for the operator.
