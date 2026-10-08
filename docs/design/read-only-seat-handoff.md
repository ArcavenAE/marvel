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
| opencode | only through `edit`, which is `ask` | not read | only from a project-local config | none |

- **Does a shell verb reach crush?** No. crush has no shell tool to run it
  with, and adding one would be the broad grant this design exists to avoid.
- **Does it reach opencode?** It is unknown, and it would need a bash
  permission that the read did not cover.

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
and takes this design out of #604's retention and storage questions.

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
- **opencode:** needs a seeder first, so the server no longer depends on a
  project-local config that cannot follow a seat whose workdir changes.
  Whether opencode prompts for MCP tool calls the way it does for `edit` is
  not measured.

## 3. Answers to the three questions

1. **Does `marvel handoff put` over the socket reach crush?** No, because
   crush has no shell tool. The daemon RPC does reach it, through an MCP tool
   marvel seeds.
2. **Is MCP `submit_handoff` the one channel all three share?** It is the
   only candidate that needs no new permission on any of them. For codex it
   is the shape marvel already seeds. For crush and opencode it depends on two
   checks that have not been run, listed in section 5. Until those pass, it is
   the recommended shared channel, not a measured one.
3. **Which half of each design survives?**
   - From the first version: the daemon RPC, the heartbeat-token
     authentication, the rule that receipt is the signal, and the codex
     allow-rule result as a measured fallback.
   - From the per-harness read: the MCP tool as the seat side, seeding crush
     and opencode, and marvel writing the file.
   - Dropped: the shell verb as the shared channel, the daemon-side store
     (marvel writes the declared file instead), and the pane fallback, since
     an MCP tool covers the case it was for.

## 4. Codex: two more carriers, measured or partly measured

- **A shell verb through one exec-policy rule (measured).** A codex
  exec-policy prefix rule runs one matching command outside the sandbox, even
  at `-s read-only` with approval policy `never`. The reviewer seats already
  use this pattern for forge access
  (`docs/design/codex-reviewer-forge-access.md`). Measured on codex-cli
  0.160.1, with a throwaway repo and `CODEX_HOME`, disk-verified, one run per
  arm:

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

## 7. Rulings needed

Each recommendation is valid until 2026-10-22 or until the section 5 checks
have run, whichever comes first; the architect re-checks it then.

1. **Seeding crush and opencode homes.** This is a new adapter
   responsibility, as codex's was in #308. Recommended: yes. The same seed
   fixes a seat whose MCP config is project-local and does not follow it when
   its workdir changes.
2. **Readers.** Recommended: unchanged from D5, because the file is where it
   is today.
3. **Scope.** Recommended: max-age first. Context pressure and an operator
   `marvel shift` can use the same tool later (#442).

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
