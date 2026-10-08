# A handoff a read-only seat can write

**Status:** design for review, 2026-10-08. Nothing is built. Refs #604,
#459, finding-067.

**Why.** The operator ruled to keep the 72h max-age arm on every role. Today
that arm can end in a shift only when the seat writes a file on the daemon
host (finding-067), and the read-only seats cannot: a codex seat at
`-s read-only`, crush, and opencode. So every 72h arm on those roles escalates
`team.shift-handoff-missing` and repeats it once per window, and the seat
keeps running past the age the operator chose. A live data point, the day
this was written: one codex reviewer seat on one host read 81% context and
was well past 72h.

This design gives those seats a handoff path they can complete without
widening their sandbox. It is the "give those roles a handoff path they can
satisfy" option from the max-age discussion, but it does not take that
option's codex form (running codex without `-s read-only`), because that
trades away the guarantee the seat exists to keep.

## 1. The shape: one allowlisted verb, and marvel stores the handoff

A seat hands off by running one command:

```
marvel handoff put < handoff.md      # or: marvel handoff put --text "..."
```

- The command reads the handoff from stdin or `--text`, and sends it over
  `MARVEL_SOCKET`, authenticated by `MARVEL_HEARTBEAT_TOKEN`. That is the
  pair marvel already injects into a seat that can reach the daemon
  (`internal/runtime/adapter.go:445-454`, `internal/api/heartbeat.go:13-17`),
  and the pair `marvel codex-ctx` already uses from inside a codex seat
  (`cmd/marvel/codexctx.go`).
- The daemon stores the bytes in its own store, keyed by workspace, team,
  role and generation, which answers #604's key question (the workspace is in
  the key). The seat writes nothing on the host's disk, so #604's "missing
  channel" is the socket.
- The request that `put` answers is the pending max-age `ShiftRequest`. A
  `put` from a session with no pending request is stored and reported, but it
  does not shift anything.
- The successor reads it with `marvel handoff get`, which takes the same
  socket and token, and resolves to its predecessor's record
  (`MARVEL_PREDECESSOR`, `internal/api/types.go:765`).

The command is the marker. A completed `put` is the atomic "handoff written"
signal, so no last-line marker and no file are needed, and a partial write
cannot read as finished (the #612 class does not arise). The declared-file path
(`shift.handoff` plus `handoff_marker`, D5) stays as it is for seats that can
write. A role may use either one, and the controller shifts on whichever it
observes first.

## 2. Each harness lets exactly that verb through, and nothing else

The seat keeps its sandbox. Its harness's own allowlist permits the one verb.

**codex (measured).** A codex exec-policy rule runs a matching command
outside the sandbox, even at `-s read-only` with approval policy `never`.
This is the pattern the reviewer seats already use for their forge access
(`docs/design/codex-reviewer-forge-access.md`). Measured on codex-cli
0.160.1, with a throwaway repo and `CODEX_HOME`, disk-verified, one run per
arm:

| arm | rule | result on disk | codex said |
|---|---|---|---|
| no rule | none | no file | "ran" |
| one rule | `prefix_rule(pattern=["<helper>"], decision="allow")` | file written | "ran" |

So a single prefix rule opens one command and nothing else. The no-rule arm
also repeats finding-049's lesson: the model reported "ran" when nothing had
been written. That is why the handoff signal must be the daemon receiving the
`put`, never the seat's account of it.

marvel already seeds each codex seat's private `CODEX_HOME`
(`internal/runtime/codex_home.go`), so the seed adds one rules file:

```
prefix_rule(pattern=["<absolute marvel binary>", "handoff", ["put", "get"]], decision="allow")
```

It names the absolute binary that `codexSeeder` already resolves with
`os.Executable()`, so a `marvel` found earlier on `PATH` cannot match. It
also allows `put` and `get` and no other verb, so the rule cannot be stretched
to `marvel inject` or `marvel shift`.

**crush and opencode (not measured).** Both have per-tool permission
settings, and the design assumes each can allow one shell command by pattern
while denying file writes. That premise has to be checked, one command per
harness on the scratch rig, before their half is built. If either harness
cannot allow one command without allowing all of them, it gets the pane
fallback in section 4, or it keeps the max-age escalation and says so.

## 3. What changes in the shift path

- **The notice.** For a role with no writable path, the notice names the verb
  instead of a file: `shift: max age reached; write your handoff now with:
  marvel handoff put (stdin or --text); you have <window>`. Today's notice
  for a declared file is unchanged (`internal/team/shift_handoff.go:83-87`).
- **The controller.** `advanceShiftRequest` also checks the store for a `put`
  recorded after `RequestedAt` from the requested session. When one exists,
  the controller shifts with the same event text as for a marker, naming
  `put` rather than a path. The escalation, `handoffMissingReason`, gains one
  more reason: no `put` was received from the session within the window.
- **apply.** A role with max-age and no writable path declares `handoff =
  "marvel"` (the store). apply refuses `marvel` for an adapter that has no
  allowlist mapping, rather than accepting an arm the seat cannot answer.
  That is finding-067's "validate at apply", made checkable.

## 4. Fallback for a harness with no allowlist: the pane

If a harness cannot let one command through, the seat prints the handoff in
its pane between two lines, and marvel reads the pane:

- the opening line is `HANDOFF-BEGIN <nonce>`;
- the closing line is `HANDOFF-END <nonce>`.

The nonce is one that marvel puts in the notice. This needs no write and no
command.

It is weaker than the verb, and I would build it only if section 2's check
fails:
- pane text can be wrapped by the TUI and cut off by scrollback;
- anything that echoes the notice back into the pane carries the nonce, so a
  match must need the closing line after an opening line that the notice did
  not contain.

Recommended order: the verb first; the pane only for a harness the check
rules out.

## 5. Rulings needed

Each recommendation is valid until 2026-10-22 or until the crush and
opencode checks in section 2 have run, whichever comes first; the architect
re-checks it then.

1. **Retention (#604 item 2).** The succession contract says "never expire
   the handoff bytes". The succession-protocol idea wipes them on completion.
   - Recommended: keep every `put` (they are small, and the contract is
     explicit), and list them with `marvel handoff list` so a human can prune
     them.
   - Alternative: keep the last N generations per role.
2. **Readers.** Recommended: the successor and the operator. Not other seats:
   a handoff can carry the seat's open work, and a reviewer seat should not
   read a builder's.
3. **Scope.** Recommended: max-age first, because that is where the escalation
   repeats. Context pressure and an operator `marvel shift` can use the same
   `put` later; #442 already tracks that context pressure shifts without a
   handoff.

## 6. Not this

- No change to any seat's `-s` mode or permission profile beyond the one
  allowed verb.
- No director dependency. The channel is marvel's own socket, so component
  independence holds (#604 Q4).
- No move of daemon state between hosts (#604 item 4).
- The `ops/supervisor` case, which is a seat whose coordination contract
  denies writes, is the same shape if its harness allows the verb. Whether it
  should is the open write-grant decision, not this design.

## 7. Candidate requirements (provisional)

- **RH-A:** a role with max-age on a seat that cannot write declares the store
  as its handoff, and apply refuses that declaration for a harness with no
  allowlist mapping.
- **RH-B:** the daemon receiving the handoff is the signal; neither a seat's
  report nor a file it claims to have written counts.
- **RH-C:** the allowlist entry names the absolute marvel binary and only the
  `handoff put` and `handoff get` verbs.
