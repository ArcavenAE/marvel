# finding-marvel-jj0s: a read-only git worktree is not a fixed view; a read-only archive is, except for a seat that holds its cwd

**Date:** 2026-10-06
**Probe:** `_kos/probes/probe-per-seat-readonly-view.md` (marvel#596, #597)
**Ticket:** `aae-orc-ao1u`
**Rig and transcript:** `scripts/probes/per-seat-readonly-view.sh`,
`scripts/probes/per-seat-readonly-view-2026-10-06.txt`
**Host:** one macOS host (Darwin 25.5.0, APFS, arm64), git 2.55.0. No Linux
host was free, so every result here is macOS only.

## Why this matters

Seats that share one checkout get moved by each other's `git switch`
(aae-orc finding-165). The probe asked whether marvel could hand each seat a
read-only view of the default branch, cheaply, at spawn. The answer is no
for a git worktree and yes for a plain archive, with one condition the
operator has to choose a remedy for: a seat whose working directory sits
inside the view keeps reading the old tree after a refresh, with no error.

## Results

| | Result | Measurement | Consequence |
|---|---|---|---|
| H1, the seat cannot edit it | **held** | Edit and Write each failed loudly, `EACCES: permission denied, open '<file>.tmp.<pid>.<id>'`; a Bash redirect failed with `Permission denied`, exit 1. Same on the archive view | Both tools write a temp file and rename it, so the **directory** write bit is what stops them. Control: with only the file at 0444 in a writable directory, the Edit tool replaced it and kept the 0444 mode |
| H2, other seats cannot move it from the shared checkout | **failed, for one command** | `switch -c`, `checkout -b`, `fetch`, `gc` and `worktree prune` left HEAD and files unchanged. `git worktree remove <view>` without `--force` failed (`failed to delete ... Permission denied`, exit 255) but first deleted the view's `.git/worktrees/<name>` entry. The files stayed, read-only, and the view was no longer a git repository | Any seat can unregister a worktree view by accident, without `--force` |
| H3, marvel can refresh it whole | **held** | From a torn view (HEAD at the new commit, files at the old) and from a clean one, `chmod -R u+w`, `checkout --force --detach <sha>`, `chmod -R a-w` left HEAD at `<sha>`, status empty, and the tree hash equal to a fresh archive of `<sha>`. Writable window 107 to 306 ms over the three refreshes of the clean run, and 146 ms for the removed-directory case | The window is under a second, so the brief's swap clause does not apply |
| H3, held cwds (run note 1) | **held, with one silent case** | Shells holding a cwd at the view root and in `internal/api` read the new content after an in-place refresh. A shell holding a cwd in `docs/reviews`, which the target commit removes, kept printing the removed path from `pwd`, and `ls` returned nothing with no error | An in-place refresh never serves stale content, but a seat inside a removed directory sees an empty one without being told |
| H4, the cost fits spawn | **held** | Local clone of the orc, 761 tracked files: create plus chmod median 168 ms, refresh median 151 ms (5 runs) | Spawn-time creation is affordable |
| H5, git aimed at the view cannot move it | **failed** | From the seat's own shell and from another seat: `checkout --detach`, `switch --detach`, `reset --soft` and `update-ref HEAD` each moved HEAD to the new commit while the files stayed at the old one (17 status lines). `reset --hard` failed and left HEAD alone. A failed `checkout` printed `unable to unlink old 'CLAUDE.md': Permission denied` and **exited 0**. No objects were written by the commands run (`count-objects` unchanged); `commit --allow-empty`, not run, would add one commit object to the shared store and update the worktree's index and reflog, so that side effect is unmeasured | A worktree view cannot be a fixed view however its files are protected, because HEAD and the index live in the shared `.git/worktrees/<name>/`. The torn state is reachable with an exit code of 0 |
| H6a, archive content | **held** | View tree hash equals a fresh `git archive` extract (`14f1e823...` both) | |
| H6b, atomic swap | **held** | A reader loop through the symlink made 3,189 reads across 20 swaps: 3,065 saw the old file hash and 124 the new one; none saw a mix or a missing file | `rename(2)` of a new symlink over the old is atomic for readers that resolve the path |
| H6c, no repository reachable | **held** | `git -C <view> rev-parse` and all five H5 commands gave `fatal: not a git repository`, tree hash unchanged. Control: the same archive inside the shared checkout resolved to that checkout's top level | The view must live outside any work tree; that is part of the mechanism |
| H6d, archive cost | **held** | Orc clone: create plus chmod median 182 ms, refresh with swap median 186 ms (5 runs) | |
| H6b2, a reader that holds its cwd | **failed, as predicted** | A shell that entered through the path read `VIEW_SHA` as the old commit after one refresh, with no error. After the second refresh removed that tree, `cat ./VIEW_SHA` gave `No such file or directory` and `pwd` gave `getcwd: cannot access parent directories` | Stale reads are silent until the tree is removed. The rig's own seats hold a cwd this way, so this is the common case |

**Build condition from the brief.** The worktree view fails it (H2 and H5).
The archive view meets it (H1, H6a to H6c hold) once an H6b2 remedy is
chosen. Run note 2, a HEAD comparison for worktree swaps, does not arise,
because no worktree view is built.

## One more thing the run showed

The directory that holds the `cur` symlink was writable by the same user,
so a seat could replace the link itself. In a build it belongs in a
directory the seat cannot write, such as marvel's state directory. This was
reasoned from a write test on that directory, not measured as an attack.

## Decision for the operator: the H6b2 remedy

| | Remedy | Cost |
|---|---|---|
| (i) | Seats read the view only by absolute path through the symlink | A rule nothing enforces; a seat's Bash keeps its cwd by default |
| (ii) | Each refresh tells the seats holding a cwd in the view to re-enter it, and the seat checks `VIEW_SHA` against the path's | Needs marvel to know which seats hold a cwd there and a way to tell them; the seat must act on it |
| (iii) | Mechanism 3, a read-only mount where content changes under a held cwd | The strongest option and the furthest away (curtain or slotefs) |

**Recommended: (ii).** It turns a silent stale read into a check the seat
can make (`cat VIEW_SHA` against the path's), and it needs nothing that does
not exist in marvel's spawn and inject paths today. This recommendation is
valid until 2026-10-27, or until the operator rules, whichever comes first;
the architect re-checks it then.

## Deviations from the brief, all disclosed

- **H1's tool trials were run by this Claude Code session** against the
  rig's paths, not by a second session. The tools are the same; the seat is
  not a separate process.
- **`commit --allow-empty` was not run.** A commit in the scratch clone
  needs a signing key touch, and the fleet rule forbids unsigned commits.
  `update-ref HEAD`, the ref move a commit makes, was run in its place.
  It writes no object, so the `count-objects` check the brief added for a
  commit's side effect on the shared store did not measure that effect.
  H5's fail does not depend on it.
- **The rig was fixed during the run.** The tmux socket path exceeded the
  macOS socket path limit; a worktree registration from a failed attempt
  was left behind; and piping git's error output to `head` killed git with
  SIGPIPE before it wrote HEAD, which briefly made a failed checkout look
  harmless. Run by hand without the pipe, that checkout moved HEAD in 10 of
  10 trials. After the fixes the whole rig was run again top to bottom on a
  fresh scratch directory, and that run is the transcript.
- **H6b did not apply the old-tree removal rule.** The 20 trees the reader
  loop swapped between were all kept, so a reader resolving the path while
  a tree is removed was not tested. H6b2 covers removal only for a held
  cwd.
- **macOS only.** No Linux host was free. The rig's tmux socket defaults to
  a short `/tmp` path and its swap uses BSD `mv -h`; on Linux the swap is
  `mv -T`.

## What follows

Nothing is built from this finding until the operator picks the H6b2
remedy. Then one ticket, filed flat: a spawn step that creates one archive
view per declared repository outside any work tree, a refresh verb with the
symlink swap and the chosen remedy, and teardown removal. A second, small
item: the failed `checkout` that exits 0 while leaving a torn worktree is
git's behavior, not marvel's, and is noted here, not filed.
