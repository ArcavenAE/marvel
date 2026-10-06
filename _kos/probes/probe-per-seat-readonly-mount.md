# probe: can a read-only mount give a seat a view of the default branch that follows a refresh under a held cwd

**Ticket:** `aae-orc-ao1u` (workspace-VFS study)
**Follows:** `_kos/findings/finding-marvel-jj0s-per-seat-readonly-view.md`
(marvel#596, #603), remedy (iii).
**Idea:** `_kos/ideas/per-seat-readonly-default-branch-view.md`, mechanism 3.
**Date opened:** 2026-10-06
**Status:** BRIEF. Nothing has run. No rig exists.

Everything under "Pre-registration" is written before any rig exists. The
outcome is appended in a dated section; nothing above it is revised after
data exists.

## Why

finding-marvel-jj0s showed that a read-only archive behind a symlink gives
a seat a view nobody can move. The finding has one hole, H6b2: a seat whose
working directory is inside the view keeps reading the old tree after a
refresh, with no error. The rig's own seats hold a cwd this way, so this is
the common case. Remedy (iii) in that finding is a read-only mount, where
the content could change under a held cwd. This probe asks whether any
mount does that on the hosts the fleet runs, what it costs, and what
privilege it needs.

The operator ruled on 2026-10-06, verbatim via director: "slotefs ii now
and also kick off iii but should include plans to test linux also, should
not hold up development".

**Nothing here gates remedy (ii).** The (ii) build goes ahead whatever this
probe finds. A result here can only add an option. It cannot block, delay
or reorder (ii).

**Placement:** marvel. The view is something marvel builds at spawn and
removes at teardown. git, Claude Code, the kernel and the mount tools are
objects. Without marvel there is nothing to file.

## The architect's premise check

These were checked by command on one macOS host (26.5.2, arm64) before
writing this. They are premises, not results.

- `ls /sbin | grep '^mount_'` lists apfs, hfs, tmpfs, nfs, smbfs, webdav,
  virtiofs and others, but no nullfs and no bind mount helper.
- `/Library/Filesystems` holds only NetFSPlugins, so no FUSE filesystem is
  installed. `bindfs` is absent from the PATH.
- `hdiutil` is present at `/usr/bin/hdiutil`.
- `bwrap` and `unshare` are absent, as expected on macOS.

So on macOS the cheap Linux answer, a read-only bind mount, is not
available without adding software. That shapes the macOS candidate list.

## Pre-registration

### The question

On macOS and on Linux, is there a read-only mount marvel can create per
seat, refresh to a newer default-branch commit, and remove, such that:
1. it refuses every write the seat's tools make;
2. no git command, from the seat or from the shared checkout, can move it;
3. a refresh is whole, with no reader seeing a mix of trees;
4. a seat holding a cwd inside it reads the new content after a refresh,
   or is told loudly that it cannot;
5. its cost fits spawn;
6. and its privilege needs are within what a seat's host already grants?

### Candidate mechanisms

Each is named with how it is checked. None is assumed to work.

**macOS**

| id | Mechanism | How it is checked | Privilege to record |
|---|---|---|---|
| M1 | Read-only disk image: `hdiutil create -srcfolder <archive>` (read-only format) then `hdiutil attach -readonly -nobrowse -mountpoint <view>` | create, attach as the seat's user, run H1 to H5; refresh as a new image attached at a new mountpoint, then the symlink swap, then detach the old | whether attach works without root; whether `-nobrowse` keeps it out of Finder and Spotlight; any prompt |
| M2 | APFS snapshot mount, `mount_apfs -s <snapshot>` of a volume holding the archive | create a snapshot, mount it read-only, run H1 to H5 | whether snapshot creation and mount need root or an entitlement |
| M3 | Loopback NFS export, read-only, of the archive directory | export, mount over `localhost`, run H1 to H5; refresh in place on the exported directory | `nfsd` and `/etc/exports` are system configuration; record what they need, do not change them on a fleet host without the operator's word |
| M4 | macFUSE with `bindfs -r` | **not installed on this host.** Recorded as an option only. Installing it needs a system extension approval | an operator decision; the brief does not install it |

**Linux**

| id | Mechanism | How it is checked | Privilege to record |
|---|---|---|---|
| L1 | Read-only bind mount: `mount --bind <src> <view>` then `mount -o remount,bind,ro <view>` | as root, then inside `unshare -rm` (user and mount namespace) | root, or unprivileged user namespaces |
| L2 | `bwrap --ro-bind <src> <view>` wrapping the seat's process | launch a test process in it, run H1 to H5 | unprivileged user namespaces; on some distributions an AppArmor or sysctl setting restricts them; record the setting's value |
| L3 | overlayfs, read-only, archive as the lower layer and no upper | mount, run H1 to H5; refresh by remounting with a new lower | root, or a user namespace on a kernel that allows it; record the kernel version |
| L4 | squashfs image of the archive, mounted read-only | loop mount as root, or `squashfuse` as the user | root or FUSE |
| L5 | `bindfs -r` (FUSE) | mount as the user, run H1 to H5 | FUSE access (`/dev/fuse`, the `fuse` group or `user_allow_other`) |

### Hypotheses, per mechanism

Each hypothesis runs per mechanism that mounts at all.

- **H1, the seat cannot edit it.** Edit, Write and a Bash redirect each
  fail loudly, as `EROFS` or `EACCES`. Run by a second Claude Code session,
  not the rig's own session, which closes the jj0s H1 deviation. Control: the
  same tools succeed on a writable copy.
- **H2, git cannot move it.** The view holds an archive with no `.git`, so
  `git -C <view> rev-parse` gives `fatal: not a git repository`. The five
  jj0s H5 commands, run from the seat and from the shared checkout, leave
  the tree hash unchanged. The view must not sit inside any work tree.
- **H3, the refresh is whole.** A reader loop like the one in jj0s H6b, run
  through each refresh, sees only whole old or whole new trees, never a mix
  or a missing file. The refresh form is the mechanism's own: a remount, a
  new mount plus a symlink swap, or an in-place update of the source.
- **H4, a held cwd follows the refresh (the H6b2 case).** This is the
  hypothesis the probe exists for. A shell holding a cwd at the view root,
  and one in a directory the target commit removes, run `cat ./VIEW_SHA` and
  `pwd` after each refresh. **Holds** if both read the new commit, or the
  removed-directory shell gets an error at once. **Fails** if either reads
  the old content with no error. Pre-registered expectation: a remount or a
  new mount leaves a held cwd on the old mount, so it fails like jj0s; an
  in-place update of a source behind a read-only bind or FUSE view may hold
  for files but not for removed directories. The probe measures this rather
  than assuming it.
- **H5, the cost fits spawn.** Create plus mount, and refresh, each timed
  over 5 runs on the jj0s orc clone (761 tracked files). The bar is jj0s's
  archive view: create median 182 ms, refresh median 186 ms. Record disk use
  per view.
- **H6, privilege.** For each mechanism, record what it needs: root,
  `sudo`, a system extension, an entitlement, a user namespace, FUSE access,
  or nothing. **This is a finding in itself.** The rig does not get around a
  refusal (no `sudo` it was not given, no installs, no sysctl changes). A
  refusal is recorded with its exact error, and the mechanism stops there.

### Root on a fleet host

Several candidates may need root (M2, M3, L1 as root, L3, L4). Running them
as root on a fleet host is an **operator decision**. The rig runs the
unprivileged form of each mechanism first and records the refusal. It runs
a root form only on a host the operator names for it, and only after that
ruling.

### The Linux host, a named need

No Linux host was free for jj0s. This run needs one, and the choice is the
operator's. What each option can test:

| Option | Can test | Cannot test faithfully |
|---|---|---|
| A Linux VM on a fleet Mac | L1 to L5 with root inside the VM; user-namespace settings as the VM's distribution ships them | a fleet seat's real host, its distribution and kernel settings |
| A container (docker or podman) | L1 to L5 only with added privileges (`CAP_SYS_ADMIN` or `--privileged`); `bwrap` often blocked by the default seccomp profile | unprivileged behavior, because the container's own restrictions mask the host's |
| A fleet Linux host | the real kernel, distribution and seat user | nothing missing, but root forms need the ruling above |

Recommended: a Linux VM first. It is the cheapest way to test both the
unprivileged and the root forms without touching a fleet host. This
recommendation is valid until 2026-10-27; the architect re-checks it then,
and no default applies without a ruling.

### Build condition

A mechanism is a build candidate for remedy (iii) when, on that OS, H1,
H2, H3 and H4 hold, H5 is within twice the jj0s archive numbers, and H6
needs nothing the seat's host does not already grant. A mechanism that
needs root or an install can still pass. If so, the finding names it with
the privilege as the operator's choice, never as a hidden step.

If no mechanism holds H4 on an OS, remedy (ii) stands alone on that OS,
which is where the fleet already is.

### What this probe does not claim

- It does not test slotefs or the curtain mount; those are separate work.
- It does not test the seat's harness rules (permission prompts, sandbox
  settings); only filesystem behavior.
- A result on one Linux distribution is reported for that distribution and
  kernel version, not for "Linux".

### The rig

A new script beside the jj0s one, `scripts/probes/per-seat-readonly-mount.sh`,
reusing its fixture (the orc clone, `git archive` of two default-branch
commits, `VIEW_SHA`). It holds one function per mechanism, and each one
starts by checking what it needs (`command -v`, OS) and records "not
available" rather than installing. Isolation, from the jj0s fixes:
- a scratch directory from `mktemp -d`;
- a short tmux socket path, under the macOS socket length limit;
- no pipe on git's error output, because a pipe killed git with SIGPIPE in
  jj0s;
- a cleanup trap that unmounts and detaches everything the run made.

A positive control comes first: a writable copy accepts the H1 writes.
Transcripts go to `scripts/probes/per-seat-readonly-mount-<date>-<os>.txt`,
one per host, each from one fresh top-to-bottom run.

### What would change the plan

- H4 holds for any macOS mechanism without root: it becomes the leading
  remedy (iii) candidate there, beside (ii).
- H4 holds on Linux only with a user namespace or `bwrap`: the finding names
  marvel's spawn wrapping the seat as the cost, which is a larger change to
  spawn than (ii).
- H4 fails everywhere: remedy (iii) as a mount is ruled out on these hosts,
  and the graveyard entry says why. (ii) is unaffected.
