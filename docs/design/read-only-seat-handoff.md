# A handoff a read-only seat can write

**Status:** design for review, 2026-10-08. Nothing is built. Refs #604,
#459, finding-067. Revised the same day to reconcile with a measured
per-harness read of each harness's effective config, taken from disk by
another agent; section 1 records what changed and why.

**Why.** The operator ruled to keep the 72h max-age arm on every role. Today
that arm can end in a shift only when the seat writes a file on the daemon
host (finding-067), and the read-only seats cannot: a codex seat at
`-s read-only`, crush, and opencode. So every 72h arm on those roles escalates
`team.shift-handoff-missing` and repeats it once per window, and the seat
keeps running past the age the operator chose. A live data point, the day
this was written: one codex reviewer seat read 81% context and was well past
72h.

This design gives those seats a way to hand off without widening their
sandbox or their permissions.

## 1. What the per-harness read changed

The first version of this note made the handoff a shell verb, `marvel handoff
put`, that each harness's allowlist would let through. A read of each
harness's effective config, taken from disk on one host, shows that cannot
reach two of the three:

| harness | can it write a file? | shell tool | MCP today | marvel-seeded home |
|---|---|---|---|---|
| codex | no (`-s read-only`) | yes | director MCP, tools pre-approved | yes (`codexSeeder`, `internal/runtime/codex.go:100`) |
| crush | no: `allowed_tools` is `grep`, `ls`, `skill`, `view` | **no** | none | none |
| opencode | only through `edit`, which is `ask` | not read | director MCP through the role's environment (corrected, see below) | none |

- **Does a shell verb reach crush?** No. crush has no shell tool to run it
  with, and adding one would be the broad grant this design exists to avoid.
- **Does it reach opencode?** It is unknown, and it would need a bash
  permission that the read did not cover.

A correction to that read, made by its author the same day: opencode does get
a director MCP today. The config arrives in the role's `runtime.env`, which
marvel copies into the pane environment without reading it
(`internal/runtime/adapter.go:494`). On another host it comes from the user's
global opencode config instead. So an MCP channel exists today for codex and
opencode, and only crush has none.

So the shell verb survives only as a codex-only option (section 4). The seat
side of the design moves to the channel all three harnesses can be given: an
MCP tool that marvel seeds.

## 2. The shape: one daemon call; marvel writes the file

**The daemon side (kept from the first version).** One daemon RPC,
`handoff.put`, takes the handoff text from a session.
- It is authenticated by the session's heartbeat token, the pair marvel
  already injects with `MARVEL_SOCKET` (`internal/runtime/adapter.go:445-454`,
  `internal/api/heartbeat.go:13-17`).
- On receipt, **marvel itself writes the role's declared handoff file**,
  ending with the declared marker. `handoffComplete` and the successor's read
  path are unchanged (D5).
- The daemon host can always write, and the seat writes nothing.

That one change of plan, marvel writing the file rather than storing the
bytes in its own store, comes from the per-harness read. It keeps D5 as it is
and takes this design out of #604's storage question. Retention comes back
as a ruling on the file marvel writes (section 7).

**The write gate.** The daemon writes a handoff only when all four hold, and
refuses otherwise, with an event naming the reason:

1. **The token is bound and matches.** `handoff.put` refuses a session record
   with no token hash. The heartbeat path deliberately admits such a record
   as `HeartbeatAuthUnbound` (`internal/api/heartbeat.go:62-78`), but a
   handoff is a write, so it must not inherit that fail-open. The session
   written for is the one the token was minted for. The request names no
   session of its own, unlike the heartbeat request
   (`cmd/marvel/codexctx.go:125-138`), so a client cannot write for another
   seat.
2. **That session holds the pending request.** `ShiftRequests[role].Session`
   must equal the authenticated session (`internal/team/shift_handoff.go:108-113`;
   there is one request per role). A sibling replica, or a seat with no
   request, is refused. This matters most when the handoff template has no
   `{session}`, because `handoffPath` then resolves one file per role
   (`:306-320`).
3. **The path comes from the session record,** through `handoffPath` with the
   stored session, never from the request.
4. **No symlink is followed, in the file or its directory.** A seat shares
   the daemon's uid, and `{session}` is predictable, so any unsandboxed seat
   on the same uid could plant a link where the daemon will write on a
   read-only seat's behalf. The request-time `MkdirAll` (`:89-95`) leaves an
   existing entry alone, a symlink to a directory included, so the check must
   be on the directory, not only the file:
   - **At request time,** after `MkdirAll`, `Lstat` the handoff directory and
     refuse a symlink or a non-directory. Also refuse when
     `filepath.EvalSymlinks(dir)` differs from the cleaned `dir`, so no
     ancestor is a link either. Then record the directory's identity (device
     and inode) on the `ShiftRequest`. A refusal is a named `DirError`, which
     the notice and the missing reason already carry.
   - **At write time,** open the directory with `os.OpenRoot`, the pattern
     `internal/view/view.go:182-187` already uses. `Stat` the opened root and
     refuse unless it is the same directory recorded at request time
     (`os.SameFile` on the identity). This check is on the opened handle, so
     a swap of the directory or an ancestor after the request is caught, and
     a swap after the check cannot redirect the write.
   - **Inside the root,** create a temporary file with
     `O_CREAT|O_EXCL|O_NOFOLLOW`, write the text and marker, `Lstat` the
     target and refuse anything but absent or a regular file, then
     `root.Rename` the temporary file over it. `os.Root` refuses to leave its
     directory, symlinks included, and `rename` replaces a directory entry
     without writing through a link.
   - **Residual, stated.** Same-uid seats are not isolated from each other by
     marvel. A seat that can write the handoff directory can still:
     - delete or replace the finished file after marvel writes it;
     - edit it in place, which keeps its inode: the successor then reads the
       edited text, and nothing at write or read time catches it. The sweep
       does catch it (section 7.1), and leaves such a file unexpired, unless
       the edit also restores the size and the modification time;
     - force a refusal, by planting a link or swapping the directory before
       the request or before the write, so the handoff escalates rather than
       shifts;
     - rename the directory after the write, so the successor reads a
       different file at the declared path.

     The gate makes marvel's own write land only in the directory it
     recorded, and a forced refusal shows up as a named event rather than a
     misplaced write. Isolating seats from each other is a sandbox question
     (curtain), not this design.
   - **One portability note.** `EvalSymlinks` refuses a handoff directory
     under `$TMPDIR`, `/tmp` or a test's `t.TempDir()` on macOS, where `/var`
     and `/tmp` are links into `/private`. No live template is affected,
     since every one is under `~/.marvel`. Tests should resolve their temp
     root first.

The text is capped at a size the handoff tail read can hold several times
over (proposed 64 KiB; `maxHandoffTail` reads 4096 bytes for the marker,
`:32`). A larger `put` is refused rather than truncated.

**The signal.** The daemon's receipt of a `handoff.put` from the requested
session, recorded after `RequestedAt`, is what lets the shift start. A seat's
own account never counts. That rule carries over from the first version and
is measured in section 4: the model said "ran" when nothing had been written.

**The seat side: a `submit_handoff` MCP tool.** marvel seeds the seat's
harness home with a pre-approved MCP server offering one tool,
`submit_handoff(text)`. The server is marvel's own binary (`marvel mcp
handoff`), and the tool calls `handoff.put`. A tool call is not a file write,
so `-s read-only`, `allowed_tools`, and `edit: ask` are untouched. The tool
needs approval in each harness:

- **codex:** seeded today. The seed already pre-approves the director
  server's tools with `default_tools_approval_mode = "approve"`
  (`internal/runtime/codex_home.go:100-105`). The handoff server gets the same
  setting.
- **crush:** needs a seeder first, the same treatment as codex, and an
  `allowed_tools` entry naming only the handoff tool. Not measured.
- **opencode:** has an MCP channel today through the role's environment, so
  the handoff server can be declared the same way, with no seeder. Whether
  opencode prompts for MCP tool calls the way it does for `edit` is not
  measured.

## 3. Answers to the three questions

1. **Does `marvel handoff put` over the socket reach crush?** No, because
   crush has no shell tool. The daemon RPC does reach it, through an MCP tool
   marvel seeds.
2. **Is MCP `submit_handoff` the one channel all three share?** It is the
   only candidate that needs no new permission on any of them. For codex it
   is the shape marvel already seeds. opencode has an MCP channel today through
   its role environment, and crush has none. Both depend on checks that have
   not been run, listed in section 5. Until those pass, it is
   the recommended shared channel, not a measured one.
3. **Which half of each design survives?**
   - From the first version: the daemon RPC, the heartbeat-token
     authentication, the rule that receipt is the signal, and the codex
     allow-rule result as a measured fallback.
   - From the per-harness read: the MCP tool as the seat side, seeding crush
     (opencode is reached through its role environment), and marvel writing
     the file.
   - Dropped: the shell verb as the shared channel, the daemon-side store
     (marvel writes the declared file instead), and the pane fallback, since
     an MCP tool covers the case it was for.

## 4. Codex: two more carriers, measured or partly measured

- **A shell verb through one exec-policy rule (measured).** A codex
  exec-policy prefix rule runs one matching command outside the sandbox, even
  at `-s read-only` with approval policy `never`. The reviewer seats already
  use this pattern for forge access
  (`docs/design/codex-reviewer-forge-access.md`). Measured with **`codex
  exec`, not the TUI the seats run**, on codex-cli 0.160.1, with a throwaway
  repo and `CODEX_HOME`, disk-verified, one run per arm. Whether the TUI
  honours the rule the same way is untested; the reviewer seats' forge access
  relies on it in the TUI, but that reliance is not a measurement. The script
  and its transcript are in the fold at the end of this note:

  | arm | rule | result on disk | codex said |
  |---|---|---|---|
  | no rule | none | no file | "ran" |
  | one rule | `prefix_rule(pattern=["<helper>"], decision="allow")` | file written | "ran" |

  If the MCP tool fails for codex, the fallback is a rule naming the absolute
  marvel binary and only its `handoff put` verb.
- **The `Stop` hook (partly measured).** marvel's own binary already runs
  inside codex seats as a `SessionStart`, `PostToolUse` and `Stop` hook
  (`internal/runtime/codex_home.go:23-27`). The hook reaches the daemon from a
  `-s read-only` seat: it is codex's only context feed (`cmd/marvel/codexctx.go`),
  and a live codex seat at `-s read-only` reports its context through it. So
  the open question from that read, whether the sandbox confines a hook subprocess,
  matters only for a hook that writes. This hook would not write; it would
  call `handoff.put`.
  - It is not recommended as the primary carrier. To find the handoff, it
    would have to pick the seat's handoff text out of the transcript. That
    means marvel parsing the seat's prose, which the succession contract
    keeps out of marvel: marvel parses the marker and nothing else. An
    explicit tool call carries the text with no parsing.

## 5. Checks before the crush and opencode halves are built

Each runs once, on a scratch rig, never on a live seat:

1. **crush:** with a seeded MCP server and `allowed_tools` naming only the
   handoff tool, the seat calls `submit_handoff`, and the daemon records the
   call. The disk shows the file, and the seat still cannot write anything
   else.
2. **opencode:** the same check, and also whether the MCP call prompts. If it
   prompts, find the opencode setting that pre-approves one tool, or record
   that none exists.
3. **crush `skill`:** whether a crush skill can write. If one can, that is a
   hole in the read-only profile, to be reported on its own, not a channel
   to build on.

If either harness cannot call one pre-approved tool without a broader grant,
that harness keeps the max-age escalation, and its role documents why.

## 6. What changes in the shift path

- **The notice.** For a role whose seat cannot write, the notice names the
  tool instead of a path: `shift: max age reached; call submit_handoff with
  your handoff; you have <window>`. Today's notice for a declared file is
  unchanged (`internal/team/shift_handoff.go:82-87`).
- **The controller.** `advanceShiftRequest` already reads the declared file,
  and marvel has now written it, so the shift starts on the marker as it does
  today. The only new code is the receipt check: a marker that marvel wrote
  for a session counts only after that session's `handoff.put`.
- **apply.** A role with max-age and a seat that cannot write declares
  `handoff_via = "tool"`. apply refuses that declaration for an adapter with
  no seeder that can offer the tool. This is finding-067's "validate at
  apply", made checkable.

## 7. Decisions

**Ruled by the operator on 2026-10-08** (relayed by director): retention,
readers and scope. The seeding item below is still a recommendation.

### 7.1 Retention: expire, configurable, default 7 days (ruled)

The ruling, verbatim: "expire", "marvel user/admin configurable, default 7
days".

- **What expires.** Only the handoff file that marvel itself wrote for a
  `handoff.put`. marvel keeps no other copy, since the daemon-side store is
  gone (section 2). A handoff file a writable seat wrote for itself under
  D5 is the seat's own file, and marvel does not touch it.
- **How marvel knows it wrote the file.** A device and inode pair is not
  enough, because inodes are reused (ext4 reuses them readily). So at write
  time marvel:
  - puts a random nonce on the file's first line, `marvel-handoff-id:
    <nonce>`, above the handoff text (the marker stays the last line). The
    line is **not stripped**, so the successor reads it with the rest of the
    file. It is inert by format: one line, a fixed key and an opaque value,
    which says nothing about the work. The notice and the successor's first
    acts treat the file as starting after that line. Stripping it would mean
    a second write, which is the thing this design avoids;
  - records the path, the nonce, the size, the modification time, the
    device and inode, and the birth time where the filesystem reports one,
    or the change time otherwise.

  The record sits beside the directory identity on the request (section 2),
  and moves into the successor's shift state, so a daemon restart keeps it.
- **When and how the sweep removes it,** once the file is older than the
  retention, always inside the recorded directory's `os.Root`:
  1. open the file `O_NOFOLLOW`, and read its first line and its `fstat`
     from the open handle;
  2. go on only if the nonce, the size, the modification time and the
     identity all match the record;
  3. rename the file to a private name inside the root,
     `.marvel-expire-<nonce>`, with a no-replace rename, so a same-uid swap
     between the check and the removal cannot redirect it and nothing
     already at the private name is overwritten:
     - Linux: `renameat2` with `RENAME_NOREPLACE` (kernel 3.15 and later, on
       filesystems that support it);
     - macOS: `renameatx_np` with `RENAME_EXCL`;
     - both on the root's directory descriptor. `os.Root.Rename` does not
       take these flags, so this one call goes through `golang.org/x/sys/unix`.
       Where the flag is refused (an old kernel or an unsupported
       filesystem), the sweep does not expire the file and says so;
  4. confirm with `os.SameFile` that the file at the private name is the file
     still held open, then unlink the private name;
  5. **never rename back.** If step 4 finds a different file, a seat swapped
     the path between steps 1 and 3. marvel leaves the file at its private
     name, emits `handoff.expire-held` naming both names, and stops sweeping
     that record. Renaming back is not done, because `rename` replaces
     whatever sits at the target, and the seat may have created a file there
     in the meantime. Leaving the file loses nothing: a named event points a
     human at both files.

  **A leftover private name** (a crash between steps 3 and 4, or step 5):
  every sweep, and the daemon at start, lists `.marvel-expire-*` in each
  recorded directory. It unlinks a leftover only when the nonce in its name
  matches a record that is past its retention, and the file itself still
  matches that record's nonce, size, modification time and identity. Any
  other leftover is left, and reported once.

  Each removal emits `handoff.expired`, naming the session, the path and the
  age. Each refusal emits the same kind with the reason.
- **Fails safe.** A device change, such as after a remount, or any mismatch
  means the file never expires, and the event says why once. An in-place
  edit changes the modification time even when it keeps the length, so it
  fails safe the same way. The one exception is an edit that also restores
  the size and the modification time (for example with `touch -r`); that
  file still expires. That is within the same-uid residual in section 2.
- **Where it is set, and who can change it.**
  - The **admin default** is a cluster setting in marvel's cluster config,
    `handoff_retention`, with a default of `168h`. The person who runs the
    daemon changes it there, and it takes effect at the next sweep.
  - The **user override** is a role key, `shift.handoff_retention`, set by
    whoever applies the manifest with `marvel work`.
  - The role key wins when it is present. apply refuses a value shorter than
    the role's `handoff_window` plus the shift timeout (proposed floor), so a
    handoff cannot expire before its successor could read it.
- **A conflict, stated rather than resolved here.** The succession text the
  seats carry says "Never expire the handoff bytes". The operator's ruling
  supersedes it for the file marvel writes. The seats' text, a wardrobe and
  director contract and not marvel's to edit, still says otherwise until its
  owners change it. A reader of both should take the ruling as current for
  marvel's copy.

### 7.2 Readers: the seat's team (ruled)

The ruling: anyone on the seat's team, not only the successor and the
operator.

- **The read path.** `marvel handoff get [--session <key>]`, over the same
  socket.
  - Seat callers: the caller presents its heartbeat token, and only a bound
    token counts (the same rule as `put`, section 2). The daemon looks up the
    token's session record in its store, and compares that record's
    `Workspace` and `Team` with those of the session whose handoff is asked
    for. They must match exactly, or the request is refused.
  - **The checking principal is the daemon,** and the record it checks
    against is its own session store, not anything the caller sends.
  - **No tokenless reads.** `handoff.get` refuses a caller with no bound
    token. A tokenless client cannot be treated as the operator, because every
    seat is given `MARVEL_SOCKET` (`internal/runtime/adapter.go:445-454`), and
    the socket has no peer-credential or operator check: a seat that left out
    its token would otherwise read any team's handoff. The operator reads a
    handoff the way the operator reads anything else on their own host, from
    the file at its declared path.
- **What is enforced, and what is not.** "Team" is enforced on one path
  only: marvel's `handoff.get` verb, for callers that hold a bound token. It
  is not enforced on direct reads. The file sits on the daemon host, readable
  by the daemon's uid, and the seats share that uid, so a seat that can read
  the host's disk can read any handoff file, whatever its team. Peer
  credentials would not help, since they also report that one uid. This is
  the same residual as in section 2: seat-to-seat isolation belongs to a
  sandbox (curtain), not to marvel.

### 7.3 Scope: max-age rotations first (ruled)

The ruling: max-age rotations first. Context pressure and an operator
`marvel shift` can use the same tool later (#442).

### 7.4 Seeding crush and opencode homes (recommended, not ruled)

This is a new adapter responsibility, as codex's was in #308. Recommended:
yes for crush, which has no MCP channel today. opencode already gets one
through its role environment, so it needs no seeder for this design. The
earlier argument that a seed would also fix opencode's channel after a workdir
change is withdrawn: that rested on a claim its author has since corrected. This recommendation is valid until
2026-10-22, or until the section 5 checks have run, whichever comes first;
the architect re-checks it then.

## 8. Not this

- No change to any seat's `-s` mode, `allowed_tools` beyond the one handoff
  tool, or `edit` permission.
- No director dependency. The MCP server is marvel's own, and the channel is
  marvel's socket, so component independence holds (#604 Q4).
- No daemon-side handoff store, and no move of daemon state between hosts.
- The supervisor seat whose coordination contract denies writes is the same
  shape if its harness pre-approves the tool. Whether it should is the open
  write-grant decision, not this design.

## 9. Candidate requirements (provisional)

- **RH-A:** a role with max-age on a seat that cannot write declares
  `handoff_via = "tool"`, and apply refuses that declaration for an adapter
  that cannot seed the tool.
- **RH-B:** the daemon's receipt of the session's `handoff.put` is the
  signal; neither a seat's report nor a file it claims to have written
  counts.
- **RH-C:** each harness pre-approves only the handoff tool. Where a shell
  fallback is used, it names the absolute marvel binary and only the
  `handoff put` verb.
- **RH-D:** marvel expires only the handoff files it wrote, after the
  configured retention (default 168h, cluster setting, role override), and
  never a file whose identity has changed since it wrote it.
- **RH-E:** a handoff read by a seat requires a bound token whose session's
  workspace and team match the handoff's, checked by the daemon against its
  own store.

<details><summary>Section 4 record: the exec-policy script and the transcript of one fresh run</summary>

```bash
#!/usr/bin/env bash
# Can a codex seat under -s read-only, approval_policy=never write ONE file through
# one exec-policy allow rule (the seat-forge pattern), and only through it?
# Arm A: no rule (the helper must be refused by the read-only sandbox).
# Arm B: one prefix_rule allowing exactly the helper. Disk-verified. Throwaway only.
set -euo pipefail
ROOT=$(mktemp -d "${TMPDIR:-/tmp}/rulechk.XXXXXX"); ROOT=$(cd "$ROOT" && pwd -P)
cleanup() { rm -rf "$ROOT"; echo "cleanup: removed $ROOT (exists now: $([[ -e $ROOT ]] && echo yes || echo no))"; }
trap cleanup EXIT
echo "codex: $(codex --version)"
mkdir -p "$ROOT/bin" "$ROOT/out"
cat > "$ROOT/bin/handoff-put" <<EOF
#!/bin/sh
printf '%s\n' "\$*" > "$ROOT/out/handoff.md"
EOF
chmod +x "$ROOT/bin/handoff-put"
arm() {
  local label=$1 rule=$2 repo="$ROOT/$1/repo" home="$ROOT/$1/home"
  mkdir -p "$repo" "$home/rules"; git -C "$repo" init -q
  ln -s "$HOME/.codex/auth.json" "$home/auth.json"
  printf 'check_for_update_on_startup = false\n\n[projects."%s"]\ntrust_level = "untrusted"\n' "$repo" > "$home/config.toml"
  [[ $rule == yes ]] && printf 'prefix_rule(pattern=["%s"], decision="allow")\n' "$ROOT/bin/handoff-put" > "$home/rules/handoff.rules"
  rm -f "$ROOT/out/handoff.md"
  echo; echo "=== $label: -s read-only, approval_policy=never, allow rule for the helper: $rule"
  set +e
  CODEX_HOME="$home" codex exec -m gpt-5.6-luna -s read-only -c 'approval_policy="never"' -C "$repo" --ephemeral \
    "Run exactly this shell command once and nothing else: $ROOT/bin/handoff-put handoff-$label HANDOFF-END . Then reply with one word: ran or refused." \
    > "$ROOT/$label.txt" 2>&1 < /dev/null
  echo "codex rc: $?"; set -e
  if [[ -f "$ROOT/out/handoff.md" ]]; then echo "RESULT $label: file written: $(cat "$ROOT/out/handoff.md")"; else echo "RESULT $label: no file"; fi
  echo "--- transcript ($label), tool lines"
  grep -E 'exec|succeeded|failed|rejected|Rejected|denied|sandbox|^ran|^refused|ERROR' "$ROOT/$label.txt" | cut -c1-200 | tail -12
}
arm no-rule  no
arm one-rule yes
```

Transcript (`./rulecheck.sh > rule1.log 2>&1`, exit 0; tool lines only, as
the script prints them):

```
codex: codex-cli 0.160.1

=== no-rule: -s read-only, approval_policy=never, allow rule for the helper: no
codex rc: 0
RESULT no-rule: no file
--- transcript (no-rule), tool lines
sandbox: read-only
exec
ran
ran

=== one-rule: -s read-only, approval_policy=never, allow rule for the helper: yes
codex rc: 0
RESULT one-rule: file written: handoff-one-rule HANDOFF-END .
--- transcript (one-rule), tool lines
sandbox: read-only
exec
 succeeded in 0ms:
ran
ran
cleanup: removed $TMPDIR/rulechk.VE0mf9 (exists now: no)
```

</details>
