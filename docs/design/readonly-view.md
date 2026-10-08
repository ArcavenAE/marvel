# A per-seat read-only view of the default branch

- **Status:** design for review, 2026-10-06, revised the same day for the
  first review (seal instead of delete; retention keyed to delivery), and
  on 2026-10-06 after merge for the swap contract (#621) and CI coverage. No
  code lands until this is reviewed. Tracks marvel#609.
- **Basis:** finding marvel-jj0s (`_kos/findings/finding-marvel-jj0s-per-seat-readonly-view.md`,
  #603) and the operator's ruling of 2026-10-06, relayed by director:
  "slotefs ii now and also kick off iii but should include plans to test
  linux also, should not hold up development".
- **Builder:** the marvel-builder role, red then green.

## 1. Why

A seat that only reads the default branch reads it through a shared
checkout that another seat may switch (aae-orc finding-165). The probe
showed a view that neither the seat's tools nor git can move: a `git
archive` extract in a read-only directory outside any work tree, swapped
atomically behind a symlink. It left one gap. A shell whose working
directory is inside the view keeps the old tree after a refresh, silently,
until that tree is removed. Remedy (ii) closes it by telling each seat to
re-enter the view and giving it a file to check against.

## 2. What a role declares

```toml
  [[team.role.view]]
  name   = "marvel"                 # one path element; names the view
  remote = "git@github.com:ArcavenAE/marvel.git"
  ref    = "main"                   # the branch to follow
  refresh_every = "10m"             # optional; default 10m, floor 1m
  reenter_grace = "2m"              # optional; default 2m (section 6)
```

A role may declare several views, one per repository. Apply refuses:

- a `name` that is empty or has a character outside `[A-Za-z0-9_-]`, which
  also keeps it one path element;
- two names in one role that map to the same `MARVEL_VIEW_<NAME>` (for
  example `a-b` and `a_b`, or `Repo` and `repo`), a duplicate name included;
- an empty `remote` or an empty `ref`. Both are required, with no default:
  marvel does not guess a repository from the workdir or a branch from the
  remote's HEAD;
- a `refresh_every` below the floor;
- a negative `reenter_grace`. Zero is allowed and means a superseded tree is
  sealed at the first quiet after the notice is delivered (section 6).

A view is per seat, not per role: two replicas each get their own, so one
seat's refresh never moves another's tree.

The seat's environment carries `MARVEL_VIEW_<NAME>` (the name upper-cased,
`-` to `_`) set to the view's path, the symlink in section 3.

## 3. Where it lives

Under marvel's state directory, outside any work tree (the probe's H6c
control showed that an archive inside a checkout lets git reach that
checkout):

```
~/.marvel/state/views/<session-key>/<name>/
  mirror.git/          a bare mirror of `remote`, marvel's own
  trees/<sha>/         one extracted tree per commit, read-only
  cur -> trees/<sha>   the path the seat is given
```

- **The mirror is marvel's.** marvel fetches into `mirror.git` and archives
  from it, so a view never reads or writes the seat's shared checkout.
- **Each tree** is `git archive <sha> | tar -x`, plus a `VIEW_SHA` file at
  its root holding the full commit id, then `chmod -R a-w`. A directory and
  every file in it are read-only, because the probe found that a read-only
  file in a writable directory is replaced by the Edit tool (H1 control).
- **Extraction refuses what could leave the tree.** A symlink whose target
  is absolute, or whose resolved target climbs out of the tree, fails the
  refresh, and so does an entry of an unsupported type; nothing is skipped,
  so the current view stays and `view.refresh-failed` names the entry. A
  relative symlink that resolves inside the tree is extracted as a link, as
  aae-orc's `.agents/skills/*` links (`../../.claude/skills/...`) need.
  Relaxing absolute links (kept dangling, or rewritten, never followed) is
  deferred until a viewed repository needs it; on 2026-10-06 none of marvel,
  director, kos or wardrobe tracked a symlink at all.
- **The swap** writes `cur.new -> trees/<sha>` and renames it over `cur`
  (`rename(2)`; BSD `mv -h`, GNU `mv -T`). A read through the path returns
  content from the old tree or the new one, never a mix and never the wrong
  file. A lookup that races a swap can fail instead: on macOS, open through
  the symlink returns EINVAL while rename(2) replaces it, about 1 in 100
  reads when swaps run back to back (measured 2026-10-06, and by the akocr
  build). Production swaps are minutes apart, so a failed lookup is rare,
  transient, and loud. On both platforms the integration test
  (`internal/view/view_integration_test.go`) holds content errors to zero,
  bounds transient lookup failures at 0.1% of reads, and logs the count on
  every PR; no Linux rate is recorded in this document.
- **What this guards against is accident, not intent.** The seat runs as
  the same user, so it can still `chmod` a tree or replace `cur`. The same
  limit is stated in the probe brief and the finding.

## 4. Refresh

On each `refresh_every` tick, and on `marvel view refresh <session> [<name>]`:

1. `git fetch` into `mirror.git`. Resolve `ref` to a commit.
2. If it equals the current tree's commit, stop.
3. Extract `trees/<new>`, write `VIEW_SHA`, make it read-only.
4. Swap `cur`. The tree it replaced becomes **superseded** (section 6).
5. Set the view's pending notice to name the new commit (section 5).
6. Emit `view.refreshed` with the session, view name, old and new commit.

Steps 1 to 3 run off the controller lock, as the handoff-file read already
does, so a slow fetch never stalls reconciliation. A refresh never seals or
removes a tree; that is keyed to the notice, not to the refresh count.

## 5. The notice and the seat-side check

**Who is told: every seat that declares the view.** marvel cannot tell
which seats hold a working directory inside it. A tmux pane's
`pane_current_path` is the harness process's directory, not its tool shell's,
and in one observed Claude Code session the harness reset its Bash tool to
the project directory after each command that changed directory outside the
project tree. So a Claude seat probably cannot hold a directory in the view
at all, while a persistent shell can (H6b2). The notice goes to every
declaring seat; the check is cheap.

**How:** through the existing `Notify(sess, text)` path that the max-age
handoff request uses, with the same timing: sent once the pane has been
quiet for `quiet_for` (2m), or at `max_defer` (30m) however busy
(`internal/api/types.go:582-583`, `internal/team/shift_handoff.go:50-92`).
The same refusals apply: a seat showing a usage limit menu or an update
menu is not typed into, and the attempt is recorded as undelivered.

**One pending notice per view, coalesced.** A refresh replaces the pending
notice rather than queueing another, so a seat that is busy through three
refreshes gets one line naming the latest commit. An undelivered notice
stays pending and is tried again on the next tick. The controller records,
per view, the time the notice for the current commit was **delivered**,
and the start of the re-entry grace: the first time the pane is observed
quiet after that delivery. A notice delivered at `max_defer` lands
mid-turn, when the seat cannot act on it, so the grace waits for the next
quiet. Those times, not the refresh, are what section 6 keys on.

**The text** (one line):

```
marvel: view <name> is now <short-sha>. If your working directory is under <path>, cd to <path> again; check with: cat <path>/VIEW_SHA
```

**The seat-side check:** a seat is current when the `VIEW_SHA` it reads by
the absolute path equals the one in its own directory's tree:

```sh
[ "$(cat "$MARVEL_VIEW_MARVEL/VIEW_SHA")" = "$(cat "$(pwd -P | sed 's|/trees/\([0-9a-f]*\)/.*|/trees/\1|')/VIEW_SHA")" ]
```

A seat that reads only by absolute path through `$MARVEL_VIEW_<NAME>` never
needs the check. That is the recommended way to use a view, and the seat
guidance that ships with the feature says so.

A read through the path that fails with an error during a refresh is retried
once; a second failure is real.

## 6. Retention, and what a seat that ignores the notice sees

A superseded tree passes through three states, and only the notice moves it.

1. **Readable.** From the swap until the notice naming a later commit has
   been delivered, the pane has next been observed quiet, and
   `reenter_grace` (default 2m, a field on the view) has run from that
   quiet (section 5).
   A seat that has not been told yet, or was told moments ago, reads a
   complete, consistent old tree, and its own `VIEW_SHA` says which commit
   it is. However many refreshes happen while the notice is undelivered,
   every superseded tree stays readable; none is sealed before its seat
   was told.
2. **Sealed.** When the grace ends, marvel hollows the tree and seals it:
   it deletes every file, keeps the directories, and sets each directory
   to mode `000`, deepest first. A seat still holding a working directory
   there now gets an error on every read instead of an empty answer.
   Measured on macOS, 2026-10-06, with shells holding a directory at the
   tree root and two levels down: `ls` printed `ls: .: Permission denied`
   and exited 1, `cat ./f` printed `Permission denied` and exited 1,
   `grep -r` warned `Permission denied` and exited 2, and `find .` printed
   `Permission denied` on stderr but exited 0 (that shell's `find` is a
   wrapper; the `find` binaries on BSD and GNU exit 1, which an integration
   test asserts on every CI run). `pwd` still printed the old
   path and exited 0, which is why the notice names `VIEW_SHA` rather than
   `pwd`. The skeleton is directories only, so it costs almost no disk.
3. **Removed.** At session teardown, or when the role's view is removed
   from the manifest. Not before: a removed tree is the silent case the
   review found (finding marvel-jj0s, H3 and H6b2: `ls` in a removed
   directory prints nothing and exits 0), so marvel never removes a tree a
   seat may still hold while the seat lives.

Why seal and not delete: deleting leaves a held directory that answers
`ls`, globs, `find` and `grep -r` with silence, and only a named file read
fails. A mode-`000` directory makes `ls`, `cat ./f` and `find .` exit
nonzero. A glob is the exception: a bash glob with `nullglob` set is silent
and a zsh glob errors, so a seat that reads by glob can still see an empty
answer, which is one more reason the notice names `VIEW_SHA` as the check.

Why delivery and not refresh count: the notice can be deferred up to
`max_defer` (30m), and at `refresh_every = 10m` a count-keyed rule would
remove a tree up to three refreshes before its seat was told. No ack verb
is proposed. A seat cannot always run one, and delivery, the next quiet and
the grace are all things marvel observes for itself.

**The cost of a seat that is never told** (stuck at a limit menu): its
superseded trees stay readable and accumulate, one per changed commit.
Past five readable superseded trees, marvel pauses that seat's refresh, so
the view stays on its current tree until the notice lands, and emits
`view.retention-held` once per change, so the supervisor sees it. It never
seals a tree early to save disk.

## 7. Failure handling

| What fails | What marvel does |
|---|---|
| fetch (network, auth) | keep the current tree; emit `view.refresh-failed` with the cause; retry on the next tick |
| archive or extract (disk full, a bad ref) | remove the partial `trees/<new>`; keep the current tree; emit `view.refresh-failed` |
| the swap | keep the current tree; remove `trees/<new>`; emit `view.refresh-failed` |
| sealing a tree | log it, leave it readable, retry on the next tick; a stuck seal never blocks a swap |
| the notice | record it undelivered, keep it pending, retry each tick; superseded trees stay readable until it lands |
| first build at spawn | the build runs in the background, off the controller's lock, and the spawn waits for it a tick at a time up to the spawn bound (15s); past it, or on a failure, the session still starts, without `MARVEL_VIEW_<NAME>`; emit `view.unavailable` once per change |
| daemon restart | trees on disk persist; the pending notice, its delivery time and each superseded tree's state are stored with the team, as a pending handoff request is, so a restart neither re-seals early nor forgets an undelivered notice |
| teardown | restore owner permissions top down, then remove `views/<session-key>/` |

No failure fails a spawn, a shift or a reconcile. Every one is an event.

## 8. Shared with option (iii), the read-only mount

Not designed here. These are the parts a mount would reuse, so the seat sees
one contract whichever mechanism serves it:

- `[[team.role.view]]` and its fields;
- the readable, sealed and removed states, if a mount keeps old trees;
- `MARVEL_VIEW_<NAME>` naming the path;
- `VIEW_SHA` at the root of the tree;
- the notice text, and the `view.refreshed`, `view.refresh-failed`,
  `view.unavailable` and `view.retention-held` events.

A mount would change what is under the path, and might make the notice
unnecessary, since content changes under a held directory.

## 9. Testing, Linux included

Per the ruling, Linux is tested and does not hold the build up.

- **Unit tests, red first:** the manifest refusals; the env var name; the
  layout; the refresh steps with a fake git; each failure row in section 7;
  the notice text; coalescing (three refreshes, one pending notice); a
  superseded tree stays readable while its notice is undelivered and through
  the grace; a notice delivered mid-turn starts no grace until the next
  quiet; sealing after delivery, the next quiet and the grace; zero grace
  seals at that quiet; past five readable trees the refresh pauses; no tree
  removed before
  teardown; `view.retention-held` past five; the restart resume, including a
  pending notice and a delivery time that survive it.
- **An integration test with real git**, in a temp directory: build, swap,
  read through the path during swaps (the H6b reader: no torn or wrong
  content, and failed lookups bounded, section 3), `git -C <path>
  rev-parse` refused (H6c), an absolute or escaping symlink failing the
  refresh while an in-tree relative one extracts, the Edit-tool control
  replaced by a write to
  a read-only directory, and a held shell in a sealed tree getting a nonzero
  exit from `ls` and `cat` (the section 6 measurement).
- **Both platforms are CI.** `quality-gate` on `ubuntu-24.04` runs the
  integration test on Linux on every PR, and `view-macos` on `macos-latest`
  runs `go test ./internal/view/... -v -count=1 -race` on every PR (`.github/workflows/ci.yml`,
  marvel#623, per the operator ruling of 2026-10-06). The swap is one
  `rename(2)` call in Go, the same on both, so neither `mv -h` nor `mv -T` is
  needed.
- **The probe rig** (`scripts/probes/per-seat-readonly-view.sh`) is
  macOS-shaped (its socket default and `mv -h`). A Linux run of it is a
  follow-up, not a gate.
