# finding-marvel-k1vd: cross-cluster file sharing is snapshots pulled into a read-only view, not sync

**Date:** 2026-11-02
**Asks:** I-139, which extends I-101 (marvel#1193)
**Status:** study; nothing built, nothing run against a daemon

The operator asked for a study of file sharing between marvel clusters that is wider than `marvel cp`: rsync-like transfer, a read-only view of a directory such as `~/.director` on each cluster, and local copies of remote files. The operator named the risks: agents poisoning the pool, instability, data loss, damage to ongoing work, and performance. This finding scopes that ask. It recommends one composition of two designs marvel already has, and it rules out two-way sync and live mirrors.

## 1. Starting point

- `docs/design/marvel-cp.md` (marvel#1193): one regular file per call, pushed into or pulled from an owner-named jail. It is no-clobber by default and checks sha256 end to end. It stays off unless an owner-held policy file, which marvel cannot write, enables it. The operator ruled R1 to R4 on 2026-11-01. Not built.
- `docs/design/readonly-view.md` (marvel#609), built: a seat's fixed read-only view of a git branch. It is an archive extract in a read-only directory, swapped atomically behind a symlink, with `VIEW_SHA` and sealing of superseded trees (finding-marvel-jj0s).
- Without a file path between clusters, the only carrier left is message text. The director inbox caps a message at 64 KiB (director `docs/shim-reference.md`, `AGENT_INBOX`). A larger file must be split and rejoined by hand, and each hop can change it, through line endings or a dropped line. The rules below assume that cost is worth removing.

## 2. Use-case classes

| class | verdict |
|---|---|
| A. One-off artifact copy between clusters | Served by `marvel cp` as ruled. |
| B. Read-only published view of an owner-chosen subtree on another cluster | Worth building, as the composition in section 4. |
| C. One-way continuous mirror | Not worth it. A snapshot pulled at an interval serves the same reads without a live mirror's delete and clobber paths. |
| D. Two-way sync among several writers | Ruled out. It is the one class where every failure in section 3 is possible, and a pool that agents write is the case it handles worst. |
| E. Live remote mount (9P or NFS style) | Not now. It would add a file server to marvel and put read load on the source host; B serves reads from static files. |

**Limits of the evidence.** The record behind these classes comes from a period when marvel and director were being built, so most of what had to move between clusters served that development work. Steady-state operation of agent teams at scale may need other classes, or different weights on these. This is a first cut, and the use cases are slanted toward development by the way they were collected.

## 3. Failure modes and the control for each

| failure | control |
|---|---|
| Poisoning: a wrong or injected file is read elsewhere as truth or as instructions | One writer per published path, the owning cluster. Read-only on every receiver. A manifest with sha256 and source cluster. No write-back path. Readers treat content as data. |
| Instability: loops, flapping, re-send storms | One-way only. The receiver pulls on its own schedule. cp's per-daemon concurrency limit (I6). |
| Data loss: a delete or an empty source spreads | No delete propagates. A new snapshot never removes an old one before the owner's retention count. No mirror. |
| Clobbering live work | Unpack only into a fresh directory, then swap a symlink. Never write a working tree, company root or seat workdir (cp C12). |
| Receive-side escape | One regular file per transfer, named by the receiver, with links refused through `os.Root` (cp C1 to C11). The snapshot is unpacked by marvel's view code with its own refusals, never by a general `tar` on the receiver. |
| Performance | The owner builds snapshots on the owner's schedule, and receivers never scan the source. The transport bounds set the ceiling, plus cp's daily byte budget (I4). The 32 MiB reply bound exists today. The 24 MiB request bound does not: the daemon decodes requests with no bound, and the bound is cp's precondition (cp I5, I6; marvel#1197, open). The operator's R4 ruling reads "the existing transport limits (the 24 MiB request bound and the 32 MiB reply bound) set the ceiling"; the request half of it is not yet built. |
| Exposure of state such as handoffs or keys | The owner names an allowlist of paths, never a whole home. cp's credential-name refusal (K1). Key stores are never in a snapshot (ADR-009 in the orchestrator). |
| Need to stop it now | The owner-held policy file (cp A1, A6) also gates publishing. |

## 4. The composition

1. **Publish, owner side.** An owner-run step outside the daemon packs an allowlisted subtree into one archive. It writes a manifest (relative path, size, sha256, source cluster, time) and places both under a versioned name in that cluster's cp jail. marvel gains no read reach beyond the jail.
2. **Pull.** `marvel cp <cluster>:<snapshot> <dst>`, the file.get already designed, at an interval the reader owns, checked end to end (cp I1, I2).
3. **View.** The readonly-view code gains a second source type, "pulled snapshot", beside the git branch. It unpacks into a fresh read-only directory, applies the built extractor's refusals (`internal/view/view.go`: no `..`, no link whose target leaves the tree, no entry type it does not know), swaps the symlink, and writes `VIEW_SHA`. Seats read the view and never write it.
4. **Retention.** N snapshots per source, set by the owner.

Not covered: a file over the transport limits, and any reader that needs a view fresher than its pull interval.

## 5. Prior art, and what it teaches

- **Receive-side containment is where the defects cluster.** kubectl cp needed four CVE fixes on one unpack path, three of them follow-ups to incomplete fixes (CVE-2018-1002100, CVE-2019-1002101, CVE-2019-11246, CVE-2019-11249). rsync 3.4.0 fixed three receive-side CVEs (CVE-2024-12087, 12088, 12747). Taking one regular file that the receiver names, as cp does, avoids that path.
- **Two-way sync fails when its last-synced state is lost.** A Syncthing database restore over an empty folder propagated deletes to every device. A lost Unison archive makes every difference a conflict.
- **Delete guards exist but are opt-in.** rsync `--max-delete` and `--backup-dir`, rrsync `-ro`, `-no-del` and `-no-overwrite`, Unison `confirmbigdel`, and Syncthing Receive Only and versioning.
- **Temp file then rename** is how rsync and Syncthing avoid half-written files; rsync's man page warns against `--inplace` on files in use.
- **Write-once, hash-named versions** (restic, borg append-only, git-annex, ZFS and btrfs send) remove overwrite. Each tool's documented losses sit in its one destructive escape hatch: borg normal-mode prune, `git annex drop --force`, `zfs receive -F`.
- **Read-only exports** (Plan 9 `exportfs -R`, NFS `ro`) stop write-back. Their documented risk is escape from the exported subtree.

Sources:
- rsync(1), rsyncd.conf(5), rrsync(1): man7.org.
- The oss-security post of 2025-01-14: openwall.com/lists/oss-security/2025/01/14/3.
- Syncthing docs: Folder Types, Understanding Synchronization, Versioning. Forum thread 20976.
- The Unison manual.
- kubernetes#61297, kubernetes#80984, and the kubernetes-security-announce post for CVE-2019-11246.
- Plan 9 exportfs(4) and exports(5).
- restic design references, borg append-only notes, git-annex drop and backends, OpenZFS zfs-send(8).

## 6. Open

- **Which subtrees an owner publishes.** For `~/.director`, the allowlist is the operator's.
- **The publish step.** It could be a marvel verb the owner runs or an owner script. Either way it must keep the owner outside marvel's write reach, as the cp policy does.
- **Defaults** for the pull interval and the retention count.
