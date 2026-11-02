# marvel cp: a contained, verified file copy between marvel daemons

**Status:** design for review, 2026-11-01; R1 to R4 ruled by the operator 2026-11-01 (section 9). Nothing here is built, and nothing here is on by default.
**Starts from:** `_kos/ideas/marvel-scp-verified-transfer.md` (2026-09-21) and the draft brief `_kos/probes/brief-cross-host-artifact-transfer.md` (2026-10-24). Neither scoped containment on the receiving side; this design does.
**Read at:** marvel `origin/main` a19ec4b5; revision 2 re-read at 52af1756 and against the Go 1.26.5 `os` source.
**Revision 2 (2026-11-01).** A review measured three overstatements in revision 1, and they are fixed here: `Root.OpenFile` follows a link inside the jail even with `O_NOFOLLOW` (C6, C8); `os.Root` contains names, not inodes, so a hard link could reach outside content (C10); and the 32 MiB bound the size cap cited is the client's reply bound, while the daemon reads a request with no bound (I3, I5). It also takes two lows: temporary files move to a reserved directory (O1, C11), and the policy file's ancestors and the read itself are checked (A1).

**Why.** marvel already carries an authenticated channel between hosts (`mrvl://`), and the one recorded transfer failure (section 1) happened between two hosts that channel joined. A file copy over it is useful only if it cannot be turned into a way to write anywhere on the receiving host, so this design puts containment, no-clobber writes and an owner-held off switch ahead of the verb.

**Revision 3 (2026-11-01).** The operator asked whether only marvel or sideshow should manage the files transferred or stored for casts, as reference copies rather than working copies. Section 2a answers that: cp writes no reference copy and no seat's working directory, only the owner's jail, and the jail is refused if it overlaps any store a cast is read from. It uses the terms of marvel `docs/design/companies.md` and sideshow `docs/marvel-company-target.md` (both in review) without renaming anything. Two text lows from the second review are folded in (C10, I5). Revision 3a aligns section 2a with the companies design's third revision: marvel writes the builtin company and nothing else in its own root, enforcement is named E0, E1 and E2 as that design names it, and C12 also refuses the per-session working-copy directories.

## 1. Use cases

I searched the fleet's records for every case where a file was needed on a host other than the one holding it: marvel's `_kos`, the orchestrator's `_kos`, the director simulation's notes and requirements, and the orchestrator's rules. The incident that earned the idea was the positive control, and it was found. Ten cases turned up. Grouped by what serves them today:

| # | case (date) | what had to move | served today by | cp only? |
|---|---|---|---|---|
| 1 | the ops manifest (2026-09-21) | a ~15 KB manifest held in one seat's scratch on host A, needed to run `marvel work` against host B | remote `marvel --cluster B work <file>`: the client sends the manifest bytes in the `apply` RPC (`cmd/marvel/main.go`, `manifest_data`), so the file never needs to be on B. The record shows the binding constraint was that the seat's harness refused to run `marvel work` at all, and the copy was a workaround for that | no |
| 2 | two skills for a seat on another cluster (about 2026-09-25) | skill directories | git: they were already published in a repository the other side could read | no |
| 3 | a review written on one cluster, needed by seats on another (2026-09-26) | a review document | git, if the review may live in a repository; otherwise it was re-sent as message text, in parts, and a count was misstated on the way | only for an artifact that may not enter a repository |
| 4 | private data, between two clusters (2026-09-30) | private data | nothing. SSH was refused both ways; git is wrong for private data; the record says the bus is the wrong carrier for it. It went to the operator, unsolved | **yes** |
| 5 | design notes in a git-ignored session directory, during a cross-cluster work migration (finding-220) | session notes | partly: code went on a pushed branch, and the answers the successor needed were written into tracker notes both clusters read | only for notes kept out of git on purpose |
| 6 | the operator's global agent instructions, to two more hosts (2026-10-05) | one config file | git and an installer. The operator ruled out the copy: "we do not scp files around" | no, by the operator's word |
| 7 | a rule added to per-host seat manifests on one host, needed on two more (2026-10-10) | untracked manifest files | nothing today; the operator's objection was to "so many bits and parts strewn around in many places", which argues for tracked manifests, not copies | no |
| 8 to 10 | a bus seed, a provider key script, seats kept on the host that holds their credentials | credentials | `marvel credential put --cluster` for the first; the credential plane for the rest | no, by ADR-009 |

**The answer.** One recorded case, 4, is served by nothing but a file copy. Two more, 3 and 5, need one only for an artifact that is deliberately kept out of every repository. The rest are served by remote `marvel work`, by git, or by the credential plane, and in two of them (6 and 7) the operator's stated preference is fewer copies of files on hosts, not an easier way to make them.

That makes the use case narrow and sensitive. The one case nothing serves is private data. This design is therefore written for that case: a jail the owner names, off unless the owner turns it on, and no way for marvel to widen its own reach. If the operator rules that case 4 has another carrier, nothing is left for `marvel cp` to serve and it should not be built (section 9, R1).

## 2. Shape

- `marvel cp <src> <cluster>:<dst>` pushes a local file to a remote daemon. `marvel cp <cluster>:<src> <dst>` pulls one. `--workspace <ws>` names the jail (section 3); it defaults to the client's workspace.
- Two daemon methods, `file.put` and `file.get`, over the same RPC as every other method. The client reads and writes its own side; the daemon touches only its jail.
- One regular file per call. No directories, no recursion, no streaming. A tree is a tarball the caller makes, or a git push.
- `--replace` is the only flag that changes what a write may do (section 4).

## 2a. Reference copies, working copies, and where cp writes

**The tiers, as the companies design and the sideshow target define them:**
- **Master.** The source a library is edited in, outside marvel: for roles, the role library's git repository, under its owner's review. It stays the source. Neither marvel nor sideshow writes it, and cp never reads or writes it.
- **Reference copies.** Each installed company version, `<root>/<name>/<version>/`, is immutable. An installer writes it: sideshow into its own store (`$SIDESHOW_HOME/packs/<name>/<version>/`, with `SIDESHOW_HOME` defaulting to `$HOME/.local/share/sideshow`), or any tool that produces a valid version directory from the master (a release tarball, `git archive`). marvel owns one root, `~/.local/share/marvel/companies`, and writes only the builtin company there, materialized from its own binary; it writes no other reference copy. That is the companies design's rule, and this design keeps it.
- **Working copies.** A session's working copy lives under `~/.marvel/run/sessions/<s>/company/`, with an `origin.json` naming the reference copy it came from, as the companies design specifies. It is never a root and is never promoted into one. marvel still renders the prompt from the pinned version at launch and stamps its hash on the session (`marvel.prompt_artifact.sha256`).

**Who writes a reference copy, and how that is enforced.** The owning writer is the installer, in its own store, and marvel for the builtin only; marvel records the tree hash the first time it resolves `name@version` and refuses any later resolve whose hash differs. The companies design names three enforcement tiers, and this design uses them unchanged rather than adding its own:
- **E0.** Modes plus hash: detection only, since the owner of a file can restore its bits.
- **E1.** marvel starts every session under a write-deny profile covering all company roots and other sessions' working copies.
- **E2.** An opt-in dedicated store owner, reached by `sudo -u`, so no seat's user can write the store at all.
Which tier a host runs, and whether any is on by default, belong to the companies design and its rulings, not to cp: companies.md calls 2 and 3, and call 3 of the sideshow target doc (`docs/marvel-company-target.md`). cp needs no tier of its own: its writes are made by the daemon, not by a session, so E1 does not cover them, and C12 below is what keeps the daemon's cp writes out of every root and working copy whatever tier is in force.

**Where cp writes: neither a reference store nor a seat's working area.**
- **C12.** cp writes only into the owner's jail (section 3). cp refuses a jail that is, holds, or sits inside any company root: marvel's own store (`~/.local/share/marvel/companies`, which is not in `companies.roots`) and every path in `companies.roots`, read from marvel's config at each check, never from the daemon's environment. cp reads no sideshow setting of its own (it does not need sideshow, ADR-005): sideshow's store is refused once it is registered in `companies.roots`, and a store registered later while it holds a jail is refused from the companies side. The jail is also refused if it is inside, contains, or equals any session's working-copy directory (`~/.marvel/run/sessions/<s>/company/`, and the sessions directory that holds them), or any seat's `workdir`. A refused jail turns both methods off for that workspace. The check runs on the resolved paths at each call, after A1's policy read. So cp can never create, change or replace a reference copy, never write into a session's working copy, and never write into a working tree a seat is using. The companies design makes the same refusal from its side: a company root that is, or holds, a cp jail is refused.
- **No promotion.** A file that arrives by cp is neither a reference copy nor a working copy of a cast. Nothing in marvel or sideshow reads the jail as a root, and no cp verb moves a file out of it. A file that should become part of a library goes into the master, by its owner's review, and reaches hosts the way every version does: through an installer, as a new reference copy with its own hash.
- **A copy derived from a reference copy.** When a cp source is a file inside a reference copy, the client records `derived_from = "<name>@<version>:<relative path>"` with that file's sha256 in the audit line (U1), so the copy is traceable to the exact reference it came from. It is still a jail file, not a reference copy.

**How this fits R1 to R4.** Section 1's one case served only by cp is private data, which is not cast material. Distributing a cast is already served by the master and the installers, so it is not a use case for cp, and this section adds no case to section 1. R1 to R4 are unchanged in substance; R2 now also reads "off until the request-bound change merges" (I5). There is no R5: which enforcement tier a host runs is ruled under the companies design.

## 3. Containment on the receiving side

**The jail.** Each workspace that the owner enables has one jail directory on each daemon host, named in the owner's policy (section 6). Every `dst` and `src` is a path relative to that jail. There is no default jail: a workspace the policy does not name has none, and both methods refuse it.

**Path rules, checked on the string before any file system call:**
- C1. Relative only: a leading `/`, a drive or volume form, or an empty path is refused.
- C2. No `..` component, no `.` component, no empty component (`a//b`), no NUL, no component over 255 bytes, no path over 1024 bytes.
- C3. Each component is printable UTF-8 with no control characters. A leading `-` is allowed (it is never passed to a shell).

**Resolution, with no gap between the check and the use:**
- C4. The daemon opens the jail once per call with `os.OpenRoot` and does every later operation (stat, open, create, link, rename, remove) through that root. `os.Root` resolves each path relative to the opened directory and refuses any resolution that would leave it, including through a symlink and including a link that appears after an earlier check, because it walks the path with the opened directory's descriptor rather than re-resolving a string. It does follow a link whose target stays inside the jail, so containment and "no links" are separate properties; C6 adds the second. This is the property that closes the time-of-check, time-of-use gap; a resolve-then-compare-the-prefix check on a string does not. Two limits, stated so nobody relies on more: `os.Root` needs Go 1.25 for `Link` and `Rename` (marvel's `go.mod` is 1.26), and it does not detect a mount point inside the jail, which only the owner can create there.
- C5. The jail root itself is checked once, when the call opens it: it must be a directory, not a link (`Lstat`), and `filepath.EvalSymlinks` of its configured path must equal the path, so the owner's configured location is the real one. This repeats the handoff write's directory check (`internal/team/handoff_put.go:290-330`).
- C6. Links inside the jail are refused. `O_NOFOLLOW` alone does not do this: `Root.OpenFile` adds `O_NOFOLLOW`, and on the resulting error it reads the link and follows it within the root, except when the flags are `O_CREATE|O_EXCL` (Go 1.26.5 `src/os/root_unix.go`, `rootOpenFileNolog` and `checkSymlink`). So:
  - **Parents.** Before any open, the daemon `Lstat`s each parent component through the root and refuses a link anywhere on the path.
  - **The final name, on a read or a replace.** It `Lstat`s the name through the root and requires a regular file, opens it, `fstat`s the open file, and refuses unless `os.SameFile` holds between the two. A link or a different file swapped in between the `Lstat` and the open is caught by that comparison, so the bytes served are always those of the file the `Lstat` saw.
  - **The final name, on a create.** The temporary file is created with `O_CREATE|O_EXCL`, which never follows a link.
  - A link swapped into a parent after the walk can still only point inside the jail (C4), so containment holds; the walk and the identity check exist so a link that stays inside is refused too, and the audit records the refusal.
- C7. Parents must exist. `file.put` creates no directories. A missing parent is refused with its name.

**What may be written or read:**
- C8. Regular files only. `file.put` writes a new regular file with mode `0600`, never with set-id, sticky or execute bits, whatever the source's mode. `file.get` follows C6's sequence (`Lstat` regular, open with `O_NONBLOCK`, `fstat`, `os.SameFile`), so a FIFO, socket or device is never read and a link is never followed.
- C10. One name per file. `os.Root` contains names, not inodes: a hard link inside the jail to a file outside it passes every name check and reads the outside content. So `file.get`, and the read of a replaced target for its audit digest (O2), refuse a file whose `fstat` link count is over 1. A file `file.put` creates has a link count of 1 once its temporary name is gone (O1). Between `Root.Link` and the removal of the temporary name the new file has a link count of 2, so a `file.get` of it in that window, or after a crash inside that window, is refused until the temporary name is gone: removed by the put, or by the sweep in O1 once it is over an hour old. That is safe: the get fails closed and can be retried, and nothing outside the jail is reachable through it.
- C9. A target that exists and is not a regular file is refused, with `--replace` or without.
- C11. The name `.marvel-cp-tmp` is reserved: a path whose first component is that name is refused by both methods, so no call can read or write a temporary file (O1).

## 4. Overwrite

- O1. **No clobber by default.** The daemon writes a temporary file in the jail's reserved directory, `.marvel-cp-tmp/<random>` (mode `0700` directory, file created with `O_CREATE|O_EXCL`, through the root), syncs it, checks the digest (section 5), then links it to the target name with `Root.Link`. A link fails if the name exists, so "refuse an existing target" and "place the file" are one atomic step, and two concurrent puts to one name cannot both land. The temporary name is removed afterwards. A refusal removes it.
  - The reserved directory is inside the jail, so it is on the jail's file system and `Link` and `Rename` from it stay atomic. No call can name it (C11), so a half-written file is never readable through cp.
  - A crash can leave a temporary file behind. Each call first removes, through the root, any regular file in `.marvel-cp-tmp/` older than one hour, so leftovers do not accumulate.
  - A crash after `Root.Link` and before the temporary name is removed leaves the target in place (C10). A retried put meets it and is refused, as for any existing target. The client recovers by comparing its digest with the landed file's, from the put's audit line if one was written before the crash or from a `file.get` where the policy allows one, or else by sending again with `--replace`. The daemon never replaces on its own.
- O2. **`--replace`** writes and checks the temporary file the same way, confirms the target is absent or a regular file with one link (C9, C10), and renames the temporary file over it with `Root.Rename`. A reader sees the old file or the new one, never a partial one. The audit line records the replaced file's size and sha256, read through the root before the rename.
- O3. The pull side follows the same rules on the client: no clobber unless `--replace`, temporary file then link or rename, in the destination's directory.

## 5. Integrity and size

- I1. The sender computes the sha256 of the bytes it sends and sends it with them. The receiver hashes while writing and refuses a mismatch before O1's link or O2's rename. The receiver returns its own digest, and the client compares it too, so the check holds end to end and not only at one hop.
- I2. `file.get` returns the digest of the bytes it read from the opened file, and the client checks it before placing the file (O3).
- I3. Size: no cp-specific cap (operator ruling R4, option 3). The existing transport limits set the ceiling, and a file over the limit fails at the transport.
  - **On `file.put`** the request bound in I5 (24 MiB) is the ceiling. With base64 and the envelope, the largest file that fits is just under 18 MiB; a larger request is refused by that bound before it is decoded. The daemon still stops and cleans up if the bytes exceed the declared size.
  - **On `file.get`** the client's 32 MiB reply bound (`internal/daemonrpc/client.go`, `maxReplyBytes`, defined in `exchange.go:38`) is the ceiling, so the largest file that fits is just under 24 MiB in base64; the client refuses a larger reply.
  - Larger files are out of scope.
- I5. **A request bound on the daemon.** Today the daemon decodes each request with no bound (`internal/daemon/daemon.go`, `handleRWCAs`, `json.NewDecoder(rwc).Decode`), so a `file.put` body would sit wholly in daemon memory before any check runs. A build of cp adds a bounded reader on that decode, for every method, since it is read before the method is known: 24 MiB, which under ruling R4 is also the ceiling on a `file.put` (I3). A request past the bound is refused with a size error. Because the bound reaches every method, the build first measures the largest request any existing method sends (an `apply` with a large manifest is the likely one) and confirms it is well under 24 MiB, the way the reply bound was set from a measured worst case. This is worth having for the tunnel without cp, and it is the one change outside cp's own code that cp needs; it is tracked as marvel#1197. cp stays off until the change that closes marvel#1197 has merged, so no build can enable `file.put` without the daemon-side bound (I6).
- I6. At most two `file.put` or `file.get` calls run at once per daemon; a third is refused as busy. With I5 this bounds what two `file.put` requests can hold in memory to about 48 MiB. A `file.get` has no daemon-side bound under ruling R4: the daemon reads and encodes the file before the client's reply bound refuses it. That bound depends on I5, so cp stays off, whatever the owner's policy says, until the change that bounds the daemon's request decode has merged; a daemon without that bound refuses both methods.
- I4. An optional per-workspace daily byte budget in the policy; past it, both methods refuse until the next UTC day.

## 6. Authorization and the owner's off switch

**The owner's policy is a ceiling marvel cannot write.** It follows the stance in `_kos/ideas/mrvl-proxy-nats-and-generic-relays.md`: the control over a channel that can write files onto a host sits outside marvel.
- A1. The policy is a TOML file at a fixed path. The daemon reads it on every `file.put` and `file.get` call, before anything else, so no reload step exists to forget. The read and the checks use one file descriptor: open with `O_NOFOLLOW`, `fstat` that descriptor for owner and mode, and parse from the same descriptor, so the file checked is the file read. The file must be owned by a user other than the daemon's and not writable by the daemon's user or group, and so must every directory from its parent up to `/`: none may be owned by the daemon's user, or writable by its group or by others (a writable ancestor would let the daemon swap the file). If any check fails, or the file is absent or unreadable, the call is refused and the refusal names which check. On a single-user host this means the owner installs it with elevated rights, which is the point: marvel cannot turn its own file channel on.
- A2. Absent policy means off. There is no built-in default that enables either method.
- A3. The policy names, per workspace: the jail path; which directions are allowed (`put`, `get`, both, neither); the callers allowed per direction, by `mrvl://` key fingerprint and by the literal `local` for the daemon's own socket; optional path prefixes within the jail per caller and direction; the daily budget. It names no size cap (ruling R4).
- A4. marvel's own authorization can only narrow the policy. A key needs the `admin` scope, or a new `file` scope that may call `file.put` and `file.get` and nothing else, and it must also be allowed by the policy. A caller the policy allows but whose key scope does not is refused, and so is the reverse. `credential-push` keys may never call either method.
- A5. Local callers are not trusted by being local. Every seat on a host holds the local socket (`internal/daemon/scope.go:110-130`), so `local` is a caller the policy must name like any other, and the design has no way to tell one local seat from another until the principal model lands (aae-orc-bs3x). Until then the recommended policy names `local` for nothing.
- A6. Turning it off: removing the policy file, or setting `enabled = false` in it, takes effect on the next call, with no restart. A call already past its policy read finishes or fails on its own.

## 7. Audit

- U1. Every call, allowed or refused, writes one JSON line to the daemon's audit log and emits a `file.put` or `file.get` event: time, caller (key fingerprint or `local`), key scope, direction, workspace, relative path, declared and actual size, sha256 of the bytes, outcome (`written`, `replaced`, `read`, or `refused` with the rule that refused it, for example `C2` or `A3`), and for a replace the old file's size and sha256.
- U2. Never the file's content, never a credential, and never the absolute jail path in an event that leaves the host. The relative path is recorded because a reviewer needs it; an owner who considers names sensitive can scope the jail so they are not.
- U3. The client prints the same line for its side, so both ends of one copy can be matched by digest and time.

## 8. Credentials and the custody boundary (SOUL section 3, ADR-009)

A file channel can move anything, including a credential file. The test is the most durable artifact marvel would hold:
- In transit, the bytes are in marvel's memory for the length of one call. ADR-009's 2026-10-19 amendment permits third-party bearer material in memory while running.
- At rest, marvel writes the bytes only into the owner's jail, a directory the owner named, and keeps only the digest. marvel's own state (its home, its store, its logs) never holds the content.
- So cp is not custody by marvel, but it is a way for a person to move a secret without the credential plane's scoping, and the credential plane is the path for key material (ADR-009). The design does three things about that, none of which is a control a determined caller cannot pass:
  - K1. A refusal list for common credential file names (`auth.json`, `*.pem`, `id_*` without `.pub`, `.env`, `*credentials*`, `*.kdbx`), on by default when the channel is on, which the owner may change in the policy.
  - K2. The help text and the refusal message point to `marvel credential` for key material.
  - K3. No content inspection. Scanning bytes for secrets would put marvel in the business of reading them, which the boundary argues against.

## 9. For the operator

The operator ruled all four on 2026-11-01 (Desk, 23:54Z to 23:57Z), relayed by director.

- R1. Build `marvel cp` at all, given section 1's answer. Ruled: option 1, build it, narrowly.
- R2. If built: off unless the owner's policy enables it (A1, A2), and off on any daemon without the request bound (I5, I6). Ruled: option 1, as written.
- R3. Whether the new `file` key scope is wanted, or `admin` alone suffices until the principal model (aae-orc-bs3x). Ruled: option 1, add the `file` scope (A4).
- R4. The size cap and ceiling (I3). Ruled: option 3, not the recommendation (1 MiB by default, raisable to 16 MiB). In the ruling's words: "no cp-specific cap, and the existing transport limits (the 24 MiB request bound and the 32 MiB reply bound) set the ceiling, so a file over the limit fails at the transport." I3, I5, I6, A3 and section 10 are written to it.

## 10. Out of scope

Directories, recursion, streaming, resumable transfer, files over the transport limits (I3), seat-to-seat copies on one host that need no daemon, any write to a company root or installer store, distributing casts or company versions, any content inspection, and any change to how `marvel work` resolves a workspace root.

## 11. Tests a build would need (red first)

Each C, O, I, A and K rule above gets a refusal test that leaves the jail unchanged, read back after the refusal. In addition: a link swapped into a parent between the walk and the open (containment holds, C4); a link to an in-jail file at the final name, which `file.get` refuses (C6); a file swapped at the final name between the `Lstat` and the open (C6); a hard link to a file outside the jail (C10); a request body past the daemon bound (I5); a crash mid-write, whose leftover is removed by a later call (O1); a policy file whose parent directory the daemon can write (A1); a jail inside, containing or equal to marvel's own store, a registered root or a seat's workdir (C12); a jail inside, containing or equal to a session's working-copy directory or the sessions directory that holds them (C12); a source inside a reference copy, whose audit line carries `derived_from` (2a); a daemon without the request bound, which refuses both methods (I6); two concurrent puts to one name, where exactly one lands (O1); a replace observed by a concurrent reader that sees old or new bytes only (O2); a digest mismatch injected in transit (I1); a policy file made writable by the daemon's user, which turns both methods off (A1). Run conditions for any of these against a scratch daemon: the four lines of the orchestrator's relay-run-conditions rule.
