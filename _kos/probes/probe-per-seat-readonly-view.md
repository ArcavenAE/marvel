# probe: can a read-only git worktree serve as a seat's view of the default branch

**Ticket:** `aae-orc-ao1u` (workspace-VFS study)
**Issue:** marvel#596
**Date opened:** 2026-10-06
**Idea:** `_kos/ideas/per-seat-readonly-default-branch-view.md`, mechanism 2;
bears on the orc question `question-slotefs-agent-filesystem-mediation`.
**Status:** OPEN, pre-registered, not run.

Everything under "Pre-registration" was written before any rig exists. The
outcome is appended in a dated section; nothing above it is revised after
data exists. One revision was made before any run, after review 5428265052
on marvel#597: H5 added, H3 states its writable window, and H4 times a
local clone (see "Revision before the run").

## Why

A seat that only reads the default branch reads it through a shared working
tree that any other seat may switch. aae-orc finding-165 records commits and
reads that landed on the wrong branch this way, and the only guard today is
discipline (check HEAD in the same command). If marvel could hand each seat
a view that nobody else can move and the seat's own tools cannot edit, the
reading half of that defect goes away without waiting for slotefs or the
curtain mount.

This probe tests the cheapest mechanism in the idea file, before anyone
builds the spawn step or argues for the expensive one.

**Placement:** marvel. The view is something marvel builds at spawn and
removes at teardown (resource matrix row 6). git and Claude Code are
objects. Without marvel there is nothing to file.

## The architect's premise check

One scratch run on kinu (macOS, APFS), 2026-10-06, before writing this:

- a detached worktree with `chmod -R a-w` refused a shell write
  (`permission denied`);
- `git -C view checkout --detach <newer>` failed with `unable to unlink old
  'f.txt': Permission denied`, **and moved HEAD anyway**: HEAD named the new
  commit while the file still held the old content;
- after `chmod -R u+w`, the same checkout was a no-op, so the torn state
  stayed torn;
- `git worktree remove` from the shared checkout refused the torn view as
  "contains modified or untracked files".

So the naive refresh is unsafe, and that shapes H3. The scratch fixture used
a throwaway local repo with an unsigned commit; the rig below needs no new
commits.

## Pre-registration

### The question

On the hosts the fleet runs, can a detached worktree with read-only files
and directories (1) refuse every write a seat's tools make, (2) stay
unmoved by any other seat's git use, in the shared checkout or aimed at the
view itself, and (3) be
refreshed by marvel to a newer default-branch commit with HEAD and files
agreeing, within a cost that fits a spawn step?

### Hypotheses

- **H1, the seat cannot edit it.** Claude Code's Edit and Write tools and a
  Bash redirect each fail loudly on a file in the view. Loud means the tool
  reports an error the seat sees, not a silent no-op.
- **H2, other seats cannot move it.** In the shared checkout, `git switch`,
  `git checkout -b`, `git fetch`, `git gc`, `git worktree prune` and
  `git worktree remove <view>` (without `--force`) leave the view's HEAD and
  files unchanged.
- **H3, marvel can refresh it whole.** A refresh of `chmod -R u+w`, then
  `git -C view checkout --force --detach <sha>`, then `chmod -R a-w`, leaves
  `git -C view status --porcelain` empty and HEAD equal to `<sha>`. The torn
  state from the premise check is not reachable through this sequence.
  The view is writable between the first and last chmod; the rig records
  how long that window is, and a seat reading during it may see a
  half-updated tree.
- **H4, the cost fits spawn.** Create plus first chmod for a local clone of
  the aae-orc checkout (the largest tree a seat reads) takes under 5 s, and
  a refresh under 5 s, median of 5 runs.
- **H5, git aimed at the view cannot move it.** `git -C <view>` with
  `checkout --detach <sha>`, `switch --detach <sha>`, `reset --hard <sha>`,
  `reset --soft <sha>` and `commit --allow-empty`, run once from the seat's
  own Bash and once from another seat, each leaves the view's HEAD and
  files unchanged. The architect expects this to fail. The view's HEAD and
  index live in the shared `.git/worktrees/<name>/`, which `chmod` on the
  view does not touch, and the premise check already saw a checkout move
  HEAD. It is pre-registered so the result is measured, not assumed.

### What H1 does not claim

The seat runs as the same user that owns the view, so `chmod u+w` by the
seat undoes the guard. This probe measures protection against accident, not
against a seat that sets out to write. The idea's question C (is that
enough until curtain) is answered by the operator, using this result; the
probe does not answer it.

### The rig

One self-contained script, run once top to bottom on kinu (macOS) and once
on a Linux host if the fleet has one free; if not, the finding says the
result is macOS only. Transcript kept:

1. A temp dir, a cleanup trap, and a local clone of marvel as the shared
   checkout. No network after the clone; no new commits (use the clone's
   `HEAD~1` and `HEAD` as the old and new default-branch commits).
2. Positive control: a writable detached worktree accepts a write and a
   checkout. Without it, a refusal proves nothing.
3. Create the view at `HEAD~1`, make it read-only, time it.
4. H1: a second Claude Code session, cwd in the view, asked to edit one
   file with Edit, Write and a Bash redirect. Record each tool's error text
   byte for byte.
5. H2: run each command in the H2 list in the shared checkout; after each,
   record the view's HEAD and `git -C view status --porcelain`.
6. H3: reproduce the torn state on purpose (naive checkout), confirm it,
   then run the H3 refresh from both a clean and a torn view; record HEAD
   and status after each.
7. H4: time steps 3 and 6 five times each on `git clone --local` of the orc
   checkout, never on the live checkout, whose `.git/worktrees/` other
   seats use.
8. H5: from the view's cwd and from the shared checkout, run each H5
   command; after each, record the view's HEAD, `git -C view status
   --porcelain`, and the files' content hash. Restore with the H3 refresh
   between commands.

### What would change the plan

- H1 fails for any tool: mechanism 2 is not a guard for that tool; report
  which, and the idea's mechanism 3 (a read-only mount) moves up.
- H2 fails: name the command. A command that moves the view from outside is
  a defect in the mechanism, not in discipline.
- H3 fails: the refresh needs a different primitive (a fresh worktree per
  refresh, swapped in by path); measure that before any build.
- H4 fails: the view is made on demand, not at spawn.
- H5 fails for any command: a git worktree cannot be a fixed view, however
  its files are protected. Measure, in the same rig, a view with no git
  metadata at all: `git archive <sha> | tar -x` into a directory made
  read-only, refreshed by extracting into a fresh directory and swapping
  the path. Report both, and that alternative becomes the build candidate.

## Revision before the run

2026-10-06, before any rig existed, after review 5428265052 on marvel#597:
H1 and H2 together left git commands aimed at the view untested, so H1 and
H2 could pass while the view stayed movable. H5 and rig step 8 add that
case, with a failure branch. H3 now states its writable window. H4 times a
local clone, not the live orc checkout.

## What a builder builds

For the probe: the rig script above, under `scripts/probes/`, and its two
transcripts. No marvel code changes.

After the probe, and only if H1 to H3 hold for the worktree view, or H1
and H3 hold for the archive view after an H5 failure: a spawn step that creates one
view per repository the seat declares, a refresh verb, and teardown
removal. That is a separate ticket, filed from the finding.
