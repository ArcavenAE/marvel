# marvel bus audit: read one audit record, on any cluster

Status: design for review, 2026-10-20. Owner: the architect role. Built in
#1051 (operator go, 2026-10-24); this document itself changes nothing.

A send that is refused or fails on a cluster's bus leaves a numbered record
in that broker's `AGENT_AUDIT` stream, and the sender is told the number. On
a cluster where marvel runs the broker, marvel already holds the broker's
admin login, but nothing reads that stream back. This adds
`marvel bus audit <seq>`, which returns one record by number. With the root
`--cluster` flag it reads another cluster's record from wherever the operator
is, because it runs on that cluster's daemon. Additive only: no existing
command or method changes.

## 1. What was ruled

Card b5-agent-audit-read, ruled 2026-10-20: marvel-bus-audit, with the
operator's note, verbatim except the flag, which the note shows with one
dash and is written here as `--cluster`: "do mcp-audit-lookup AND
marvel-bus-audit, and the second should be able to be read with marvel bus
audit `--cluster` (target-cluster) from cli also." The director half (a lookup in director's MCP
tool, over a seat's own connection) is designed in director's repository,
`sim/design/audit-lookup.md`. The card's stated cost for this half: "it reads
through marvel's broker connection, so it works only where marvel runs the
broker."

## 2. Premises (checked 2026-10-20, marvel main 695fdff2)

- **`--cluster` already reaches another cluster's daemon.** It is a root
  persistent flag, and `send` resolves it through `resolveDaemonRung`
  (`cmd/marvel/main.go`): flag, env, cluster, default. An explicit
  `--cluster` outranks `MARVEL_SOCKET`, `--cluster ""` is refused, and an
  unknown name is refused rather than falling back to the local daemon. A
  cluster with a `server` address is dialled over `mrvl://` with its identity.
  So a new `bus` subcommand that calls `send` gets `--cluster` with no new
  flag.
- **The bus commands are daemon methods.** `marvel bus status` calls
  `bus.status`; `leaf connect|disconnect` call `bus.leaf.connect|disconnect`
  (`busCmd`, same file; `dispatchAs`, `internal/daemon/daemon.go`).
- **The daemon already reads its broker as admin.** The bus supervisor's
  structure check connects with `connectAdmin` using `mgr.URL()` and
  `mgr.Admin()` (`internal/bus/supervisor.go`, `readStructure`;
  `internal/bus/health.go`, `CheckStructure`).
- **The stream** is `bus.AuditStream`, `AGENT_AUDIT`, subject `agent.audit`,
  720 hours (`internal/bus/declared.go`). Director writes its records: the
  envelope as the body, and on a refusal the headers `Director-Outcome`,
  `Director-Stage`, `Director-Refusal` and a `Nats-Msg-Id` ending `-refused`.
- **Scopes.** An admin key may call any method; a `credential-push` key only
  its listed set (`internal/daemon/scope.go`). `localOnlyMethods` holds the
  methods that never go through the tunnel.
- **Not yet known:** whether the corporate cluster's broker is run by marvel
  there. One read-only command on that host answers it: `marvel bus status`,
  line `managed:`.

## 3. The command

```
marvel bus audit <seq> [-o json] [--cluster <name>]
```

- `<seq>` is required, a whole number of 1 or more, parsed with
  `strconv.ParseUint(s, 10, 64)`; anything else is refused before any dial.
- It calls one daemon method, `bus.audit`, with `{"seq": N}`.
- Output, as key and value lines like `bus status`:

```
seq:      <N>
time:     <RFC3339, UTC>
subject:  agent.audit
outcome:  refused | sent
stage:    <the header>
refusal:  "<the header, quoted>"
msg_id:   "<the header, quoted>"
envelope:
  <the body, indented JSON>
```

  `outcome` is `refused` when `Director-Outcome` is `refused`, else `sent`;
  `stage` and `refusal` print only on a refusal.
- **Text from the record is escaped, never printed raw, on every path.**
  `encoding/json` alone is not enough: it escapes C0 controls such as ESC
  (U+001B) and the line and paragraph separators (U+2028, U+2029), but
  passes C1 controls and the bidi controls through as raw UTF-8. Measured
  (Go 1.26.5 here; the reviewer saw the same on 1.27.0): after `json.Marshal`
  and `json.Indent`, U+0085 comes out as bytes `c2 85`, U+009B as `c2 9b`,
  U+202A as `e2 80 aa`, U+202E as `e2 80 ae`, U+2066 as `e2 81 a6` and U+2069
  as `e2 81 a9`. U+009B is a one-byte CSI on terminals that read C1 controls,
  and the bidi controls reorder what a reader sees. `strconv.Quote` escapes
  all of them, because it escapes every rune `strconv.IsPrint` rejects.

  So one rule covers every printed path: **no rune that `strconv.IsPrint`
  rejects reaches the terminal raw.** That set includes C0 and C1 controls
  (U+0000 to U+001F, U+007F, U+0080 to U+009F), the bidi embedding and
  override controls (U+202A to U+202E), the bidi isolates (U+2066 to U+2069),
  and the other format characters such as U+200E, U+200F and U+061C.
  - Headers and a body that is not JSON print with `strconv.Quote`, which
    already applies the rule.
  - A JSON body prints through `json.Indent` of a re-marshalled value, then
    one pass, `escapeInStrings`, that works **inside JSON string literals
    only**. Outside a string it copies every byte, so the newlines and spaces
    `json.Indent` writes between tokens stay as they are: `strconv.IsPrint`
    rejects U+000A, and a pass over the whole output would turn that
    structure into `\u000a` and the result would no longer be valid JSON
    (the reviewer measured that). Inside a string it copies a backslash and
    the byte after it as they are, ends the string at an unescaped `"`, and
    rewrites each other rune that `strconv.IsPrint` rejects as `\uXXXX`, or
    as two `\u` escapes (a UTF-16 surrogate pair) for a rune above U+FFFF.
    Measured here on Go 1.26.5 with a short version of that pass, for each
    rune in test 8 placed in a key, a top-level value and a nested value: the
    output passed `json.Valid`, decoded to the original text, held none of
    the rune's raw bytes, and kept every newline `json.Indent` wrote. Sixteen
    cases, no failures.
  - **Invalid UTF-8.** A byte that does not start a valid UTF-8 sequence, such
    as a lone 0x9B or the first byte of a truncated sequence, decodes as
    U+FFFD with width 1. Both passes write it as the literal U+FFFD (bytes
    `ef bf bd`), one for each invalid byte, and never copy the raw byte. That
    is what `encoding/json` itself writes for invalid UTF-8, and U+FFFD is
    printable, so the output stays valid UTF-8 and valid JSON. On today's
    paths no such byte reaches either pass (measured, Go 1.26.5):
    `json.Valid` accepts a string holding 0x9B, but the body is unmarshalled
    and re-marshalled first, which already writes U+FFFD for each invalid
    byte (0x9B to `ef bf bd`, the truncated `e2 80` to two of them); a
    non-JSON body and the headers go through `strconv.Quote`, which writes
    `\x9b`; and the daemon's error arrives inside the JSON RPC response. The
    rule is there so the passes stay safe if a later path feeds them raw
    bytes.
  - `-o json` is `encoding/json` output, then the same `escapeInStrings`
    pass.
  - The daemon's error text, printed on stderr, is plain text, not JSON.
    `send` already passes every daemon error through `drawn`, for every
    command, which replaces each control and format character, a newline
    included, with U+FFFD. The command then passes it through `escapeAll`,
    which writes any other rune `strconv.IsPrint` rejects as `\uXXXX`, and
    the CLI ends the line with its own newline. A broker message cannot
    carry a raw control either.
- Exit 0 on a record, 1 on any refusal or miss, with the daemon's text on
  stderr.

## 4. The daemon method

`bus.audit`, handled by `handleBusAudit(params)`:

1. No managed bus (`d.bus == nil`): `no managed bus is configured for this
   cluster; marvel reads the audit log only where it runs the broker`.
2. Decode `{"seq": N}`; refuse N of 0.
3. `d.bus.AuditRecord(ctx, N)`, a new method on the bus supervisor: connect
   with `connectAdmin(ctx, s.mgr.URL(), admin.Name, admin.Password)`, open
   `AuditStream`, `GetMsg(ctx, N)`, close. A 10-second bound, as
   `structureRPC`.
4. Return the record: seq, time, subject, the four headers by name only, and
   the body as bytes. No other header and nothing else from the broker.

Misses, each with its own text, from the stream's info and the `GetMsg`
error. The stream is declared with neither `DenyDelete` nor `DenyPurge`
(`internal/bus/declared.go`, the `AuditStream` object), so a number inside the
range can be missing:

| case | text |
|---|---|
| no stream | `this broker has no AGENT_AUDIT stream` |
| the stream is empty | `AGENT_AUDIT holds no records` |
| N below the first sequence | `seq N has aged out; AGENT_AUDIT holds F to L` |
| N past the last sequence | `seq N is past the last record, L; check the number came from this cluster's audit line` |
| N inside F to L with no message | `seq N was deleted or purged; AGENT_AUDIT holds F to L with gaps` |
| a connect or read error | `read AGENT_AUDIT: <error>` |

`--cluster` already binds the number to one daemon and so one broker, so
the cluster binding director's half needs is the flag here.

**What it never does:** publish, ack, delete, purge, or create a consumer.
`GetMsg` is a get by sequence: the stream does not set `AllowDirect`, so the
client sends `$JS.API.STREAM.MSG.GET.AGENT_AUDIT` (nats.go v1.53.1
`jetstream/stream.go:577`, `api.go:97`), and no ack floor moves.

**Scope and reach.**
- Admin keys only: `bus.audit` is not added to `credentialPushMethods`, so a
  credential-push key gets the existing "not permitted" refusal.
- It is not in `localOnlyMethods`, because the ruled use is reading another
  cluster from the CLI, which goes through the tunnel. An admin key on that
  daemon is what `--cluster` already needs for every other command there.
- It is added to `startingMethods`: it reads the broker, not tmux, so it can
  answer while the daemon adopts.

A record holds the full envelope of a send, text included. The command's
help says so, and says that any admin caller on that daemon can read any
record by number.

## 5. Tests (red first)

Every test that starts a broker uses the package's existing scratch broker
harness, under the `TestMain` home guard, a scratch `TMPDIR` and no
`MARVEL_SOCKET`.

1. Daemon: a record published to `agent.audit` with the refusal headers is
   returned with all four headers and the body unchanged.
2. Daemon: a record with no `Director-Outcome` header returns `outcome:
   sent` and no stage or refusal.
3. Daemon: each miss in section 4's table returns its own text: an empty
   stream; seq below the first (the test deletes from the front with
   `Stream.DeleteMsg` to move the first sequence); seq past the last; and a
   record deleted from inside the range, which must return the deleted-or-
   purged text and never the aged-out or past-the-end one. Seq 0 is refused.
4. Daemon: no managed bus returns the no-managed-bus text.
5. Daemon: a `credential-push` caller is refused with "not permitted"
   (as `TestBusStatusIsNotACredentialPushMethod`).
6. Daemon: the stream's consumer count is the same before and after a read.
7. CLI: `marvel bus audit abc`, `0`, `-1` and no argument exit 1 before any
   dial (run with `--socket` pointing at a path with nothing listening, and
   the error must be the argument text, not a dial error).
8. CLI: for each of U+001B, U+0085, U+009B, U+202A, U+202B, U+202C, U+202D,
   U+202E, U+2066, U+2067, U+2068, U+2069, U+200E, U+200F and U+061C, a body
   carrying it, a non-JSON body carrying it and a header carrying it each
   print with the rune written as `\uXXXX`, and a daemon error carrying it
   prints with the rune replaced by U+FFFD and no raw byte of the rune left.
   The daemon error reaches the command already filtered: `send` replaces
   every control and format character in a daemon error with U+FFFD for
   every command (`drawn`, `cmd/marvel/undrawable.go`), and `escapeAll` then
   writes any other rune `strconv.IsPrint` rejects as `\uXXXX`.
   The check reads the printed bytes and fails on the rune's raw UTF-8 bytes,
   in both the line output and `-o json`. A rune above U+FFFF that IsPrint
   rejects (U+E0001) prints as a surrogate pair. For every rune case, in a
   key, a top-level value and a nested value, the JSON body and the `-o json`
   output each pass `json.Valid`, decode to the original text, and keep the
   same count of newlines that `json.Indent` wrote (so the structure was not
   escaped). A daemon error carrying U+000A prints on one line.
   Invalid UTF-8, fed to each pass directly: a lone 0x9B inside a JSON
   string, and a truncated sequence (`e2 80` then an ASCII letter), each
   come out as one U+FFFD per invalid byte, with no raw 0x9B, 0xE2 or 0x80
   byte left, and the output passes `json.Valid` and `utf8.Valid`. A header carrying a newline
   prints quoted on one line. Fixtures name each rune by its number in the
   test source (`"\u202e"`), never as the raw rune.
9. CLI: `marvel bus audit 1 --cluster ""` is refused with the existing
   `--cluster needs a cluster name` text (the rung is shared; this pins that
   the new command goes through `send`).

## 6. Build

One marvel PR: the subcommand in `busCmd`, `handleBusAudit` and its case in
`dispatchAs`, `AuditRecord` in `internal/bus`, the `startingMethods` entry,
and section 5's tests. It shares no file with the director half, so the two
build in parallel. After release, the first real read is on kinu with
`--cluster` naming another cluster that has a managed broker, and on corporate
once `marvel bus status` there shows `managed: true`.

Valid until 2026-10-27, or until the build PR opens, whichever comes first;
the architect role re-checks it then.
