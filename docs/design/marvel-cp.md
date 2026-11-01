# marvel cp: a contained, verified file copy between marvel daemons

**Status:** design for review, 2026-11-01. Nothing here is built, and nothing here is on by default. Whether to build it, and any default, are the operator's rulings (section 9).
**Starts from:** `_kos/ideas/marvel-scp-verified-transfer.md` (2026-09-21) and the draft brief `_kos/probes/brief-cross-host-artifact-transfer.md` (2026-10-24). Neither scoped containment on the receiving side; this design does.
**Read at:** marvel `origin/main` a19ec4b5.

**Why.** marvel already carries an authenticated channel between hosts (`mrvl://`), and the one recorded transfer failure (section 1) happened between two hosts that channel joined. A file copy over it is useful only if it cannot be turned into a way to write anywhere on the receiving host, so this design puts containment, no-clobber writes and an owner-held off switch ahead of the verb.

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

## 3. Containment on the receiving side

**The jail.** Each workspace that the owner enables has one jail directory on each daemon host, named in the owner's policy (section 6). Every `dst` and `src` is a path relative to that jail. There is no default jail: a workspace the policy does not name has none, and both methods refuse it.

**Path rules, checked on the string before any file system call:**
- C1. Relative only: a leading `/`, a drive or volume form, or an empty path is refused.
- C2. No `..` component, no `.` component, no empty component (`a//b`), no NUL, no component over 255 bytes, no path over 1024 bytes.
- C3. Each component is printable UTF-8 with no control characters. A leading `-` is allowed (it is never passed to a shell).

**Resolution, with no gap between the check and the use:**
- C4. The daemon opens the jail once per call with `os.OpenRoot` and does every later operation (stat, open, create, link, rename, remove) through that root. `os.Root` resolves each path relative to the opened directory and refuses any resolution that would leave it, including through a symlink and including a link that appears after an earlier check, because it walks the path with the opened directory's descriptor rather than re-resolving a string. This is the property that closes the time-of-check, time-of-use gap; a resolve-then-compare-the-prefix check on a string does not. Two limits, stated so nobody relies on more: `os.Root` needs Go 1.25 for `Link` and `Rename` (marvel's `go.mod` is 1.26), and it does not detect a mount point inside the jail, which only the owner can create there.
- C5. The jail root itself is checked once, when the call opens it: it must be a directory, not a link (`Lstat`), and `filepath.EvalSymlinks` of its configured path must equal the path, so the owner's configured location is the real one. This repeats the handoff write's directory check (`internal/team/handoff_put.go:290-330`).
- C6. Links inside the jail are refused, not followed. Before a write, the daemon `Lstat`s each parent component through the root and refuses a link anywhere on the path; the final open uses `O_NOFOLLOW`. A link created between that walk and the open can only point inside the jail (C4), so containment holds even then; the walk exists so a link that stays inside is still refused, and the audit records the refusal.
- C7. Parents must exist. `file.put` creates no directories. A missing parent is refused with its name.

**What may be written or read:**
- C8. Regular files only. `file.put` writes a new regular file with mode `0600`, never with set-id, sticky or execute bits, whatever the source's mode. `file.get` opens with `O_NOFOLLOW|O_NONBLOCK` and refuses after `fstat` if the opened file is not regular, so a FIFO, socket or device is never read.
- C9. A target that exists and is not a regular file is refused, with `--replace` or without.

## 4. Overwrite

- O1. **No clobber by default.** The daemon writes a temporary file in the target's directory (`.marvel-cp-<random>.tmp`, `O_CREAT|O_EXCL|O_NOFOLLOW`, through the root), syncs it, checks the digest (section 5), then links it to the target name with `Root.Link`. A link fails if the name exists, so "refuse an existing target" and "place the file" are one atomic step, and two concurrent puts to one name cannot both land. The temporary name is removed afterwards. A refusal removes it and leaves nothing.
- O2. **`--replace`** writes and checks the temporary file the same way, confirms the target is absent or a regular file (C9), and renames the temporary file over it with `Root.Rename`. A reader sees the old file or the new one, never a partial one. The audit line records the replaced file's size and sha256, read through the root before the rename.
- O3. The pull side follows the same rules on the client: no clobber unless `--replace`, temporary file then link or rename, in the destination's directory.

## 5. Integrity and size

- I1. The sender computes the sha256 of the bytes it sends and sends it with them. The receiver hashes while writing and refuses a mismatch before O1's link or O2's rename. The receiver returns its own digest, and the client compares it too, so the check holds end to end and not only at one hop.
- I2. `file.get` returns the digest of the bytes it read from the opened file, and the client checks it before placing the file (O3).
- I3. Size cap: 1 MiB by default per file, which the owner may raise in the policy to at most 16 MiB. The request is refused before any write when the declared size is over the cap, and the write stops and cleans up if the bytes exceed the declared size. The 16 MiB ceiling keeps the base64 body well under the daemon's 32 MiB reply bound (`internal/daemonrpc/exchange.go:38`), so cp needs no new transport. Larger files are out of scope.
- I4. An optional per-workspace daily byte budget in the policy; past it, both methods refuse until the next UTC day.

## 6. Authorization and the owner's off switch

**The owner's policy is a ceiling marvel cannot write.** It follows the stance in `_kos/ideas/mrvl-proxy-nats-and-generic-relays.md`: the control over a channel that can write files onto a host sits outside marvel.
- A1. The policy is a TOML file at a fixed path. The daemon reads it on every `file.put` and `file.get` call, before anything else, so no reload step exists to forget. It must be owned by a user other than the daemon's and not writable by the daemon's user or group; if it is absent, unreadable, or writable by the daemon, the call is refused and the refusal names which. On a single-user host this means the owner installs it with elevated rights, which is the point: marvel cannot turn its own file channel on.
- A2. Absent policy means off. There is no built-in default that enables either method.
- A3. The policy names, per workspace: the jail path; which directions are allowed (`put`, `get`, both, neither); the callers allowed per direction, by `mrvl://` key fingerprint and by the literal `local` for the daemon's own socket; optional path prefixes within the jail per caller and direction; the size cap; the daily budget.
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

- R1. Build `marvel cp` at all, given section 1's answer.
- R2. If built: off unless the owner's policy enables it (A1, A2). This design recommends that, and it is the operator's to rule; nothing here sets a default.
- R3. Whether the new `file` key scope is wanted, or `admin` alone suffices until the principal model (aae-orc-bs3x).
- R4. The size cap and ceiling (I3).

These recommendations are valid until 2026-11-08; the architect re-checks them then.

## 10. Out of scope

Directories, recursion, streaming, resumable transfer, files over 16 MiB, seat-to-seat copies on one host that need no daemon, any content inspection, and any change to how `marvel work` resolves a workspace root.

## 11. Tests a build would need (red first)

Each C, O, I, A and K rule above gets a refusal test that leaves the jail unchanged, read back after the refusal. In addition: a link swapped into a parent between the walk and the open (containment holds, C4); two concurrent puts to one name, where exactly one lands (O1); a replace observed by a concurrent reader that sees old or new bytes only (O2); a digest mismatch injected in transit (I1); a policy file made writable by the daemon's user, which turns both methods off (A1). Run conditions for any of these against a scratch daemon: the four lines of the orchestrator's relay-run-conditions rule.
