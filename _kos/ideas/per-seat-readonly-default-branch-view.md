# A per-seat read-only view of the default branch, built at spawn

**Status: idea. Pre-hypothesis. Nothing here is built or tested.**

Raised from a client team's harvest, 2026-09-27. Filed here because the
view would be something marvel builds for a seat when it spawns one, which
makes it a filesystem-access question for marvel (resource matrix row 6:
"repos, worktrees, read-only submounts; visibility as well as mutability").

## The problem

Several seats share one checkout of a repository. A seat that only needs to
read the default branch still reads it through that shared working tree,
and any other seat may have switched the tree to its own branch. Today only
discipline stops the collision:

- the aae-orc rule `session-close-mutex.md`: check the branch in the same
  command as the commit;
- aae-orc finding-165: a shared checkout moves under a session, with
  instances where a commit landed on another seat's branch;
- a warn-only hygiene check in aae-orc.

The seats that reported this worked around it with `git show
origin/main:<path>` and `git archive`. Both work, and both are a
workaround every reading seat has to know about.

## The idea

At spawn, marvel gives each seat that declares a repository a read-only view
of that repository's default branch, at a path only that seat uses. The view
is refreshed on a fetch, never written by the seat, and separate from any
worktree the seat creates for its own branches.

Candidate mechanisms, cheapest first:

1. A `git worktree add --detach origin/<default>` per seat, which marvel
   removes at teardown. This is cheap, but the files stay writable, so it
   relies on the seat leaving them alone.
2. The same worktree with its files made read-only after checkout. Git
   itself can still move it, but the seat's tools cannot edit it.
3. A read-only mount. This is the curtain thin-mission shape (design
   resolved, unbuilt) and the slotefs question. It is the strongest option
   and the furthest away.

## What it would and would not fix

It removes the reading half: a seat that only reads cannot be moved by
another seat's `git switch`. It does not fix the writing half, since two
seats committing in one shared tree still collide. That half is what
per-seat worktrees and the mutex rule are for.

## Open questions

A. Should the view be declared in the manifest (per role or per repo), or be
a default for every seat that names a repository?

B. Who refreshes it, and when: marvel on a timer, the seat on demand, or a
merge event (see the director idea on a merge-event feed)?

C. Is option 2 enough until curtain exists, or does a writable-by-git view
give a false sense of isolation?
