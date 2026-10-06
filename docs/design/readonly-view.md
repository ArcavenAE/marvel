# A per-seat read-only view of the default branch

- **Status:** design for review, 2026-10-06. No code lands until this is
  reviewed. Tracks marvel#609.
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
```

A role may declare several views, one per repository. Apply refuses a
duplicate `name` in one role, a `name` that is not one path element, and a
`refresh_every` below the floor. A view is per seat, not per role: two
replicas each get their own, so one seat's refresh never moves another's
tree.

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
- **The swap** writes `cur.new -> trees/<sha>` and renames it over `cur`
  (`rename(2)`; BSD `mv -h`, GNU `mv -T`). Readers that resolve the path see
  the old tree or the new one, never a mix (H6b, 3,189 reads across 20
  swaps).
- **What this guards against is accident, not intent.** The seat runs as
  the same user, so it can still `chmod` a tree or replace `cur`. The same
  limit is stated in the probe brief and the finding.

## 4. Refresh

On each `refresh_every` tick, and on `marvel view refresh <session> [<name>]`:

1. `git fetch` into `mirror.git`. Resolve `ref` to a commit.
2. If it equals the current tree's commit, stop.
3. Extract `trees/<new>`, write `VIEW_SHA`, make it read-only.
4. Swap `cur`.
5. Remove the tree before the one just replaced, if any (section 6).
6. Notify the seat (section 5).
7. Emit `view.refreshed` with the session, view name, old and new commit.

Steps 1 to 3 run off the controller lock, as the handoff-file read already
does, so a slow fetch never stalls reconciliation.

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
handoff request uses, so the same refusals apply (a seat showing a usage
limit menu or an update menu is not typed into, and the notice is recorded
as undelivered). A seat that is mid-turn gets it when the pane is quiet, by
the same `quiet_for` rule as the handoff request.

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

## 6. Retention, and what a seat that ignores the notice sees

marvel keeps two trees: the current one and the one it replaced. A refresh
removes the tree before that. So a seat that ignores one notice still reads
a complete, consistent old tree, and its own `VIEW_SHA` says which commit it
is. A seat that ignores two notices is in a removed directory: reads fail
with `No such file or directory` and `pwd` with `getcwd: cannot access
parent directories` (measured in H6b2). That failure is loud, which is the
point of removing the tree rather than keeping it forever.

## 7. Failure handling

| What fails | What marvel does |
|---|---|
| fetch (network, auth) | keep the current tree; emit `view.refresh-failed` with the cause; retry on the next tick |
| archive or extract (disk full, a bad ref) | remove the partial `trees/<new>`; keep the current tree; emit `view.refresh-failed` |
| the swap | keep the current tree; remove `trees/<new>`; emit `view.refresh-failed` |
| removing the old tree | log it, leave it, retry on the next refresh; a stuck tree never blocks a swap |
| the notice | record it undelivered on the event, as the handoff request does; the swap stands |
| first build at spawn | the session still starts, without `MARVEL_VIEW_<NAME>`; emit `view.unavailable` once per change |
| daemon restart | views on disk persist; the controller reads `cur` and resumes the ticks |
| teardown | remove `views/<session-key>/` (`chmod -R u+w` first) |

No failure fails a spawn, a shift or a reconcile. Every one is an event.

## 8. Shared with option (iii), the read-only mount

Not designed here. These are the parts a mount would reuse, so the seat sees
one contract whichever mechanism serves it:

- `[[team.role.view]]` and its fields;
- `MARVEL_VIEW_<NAME>` naming the path;
- `VIEW_SHA` at the root of the tree;
- the notice text, and the `view.refreshed`, `view.refresh-failed` and
  `view.unavailable` events.

A mount would change what is under the path, and might make the notice
unnecessary, since content changes under a held directory.

## 9. Testing, Linux included

Per the ruling, Linux is tested and does not hold the build up.

- **Unit tests, red first:** the manifest refusals; the env var name; the
  layout; the refresh steps with a fake git; each failure row in section 7;
  retention of exactly two trees; the notice text; the restart resume.
- **An integration test with real git**, in a temp directory: build, swap,
  read through the path during swaps (the H6b reader), `git -C <path>
  rev-parse` refused (H6c), and the Edit-tool control replaced by a write to
  a read-only directory.
- **Linux coverage is CI.** marvel's CI has `ubuntu-24.04` jobs and one
  `macos-latest` job (`.github/workflows/ci.yml`), so the integration test
  runs on both on every PR. The swap is one `rename(2)` call in Go, the same
  on both, so neither `mv -h` nor `mv -T` is needed.
- **The probe rig** (`scripts/probes/per-seat-readonly-view.sh`) is
  macOS-shaped (its socket default and `mv -h`). A Linux run of it is a
  follow-up, not a gate.
