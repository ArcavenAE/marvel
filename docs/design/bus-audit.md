# marvel bus audit: read one audit record, on any cluster

Status: design for review, 2026-10-20. Owner: the architect role. Docs only;
nothing is built by this document.

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
- **Text from the record is quoted, never printed raw.** Headers print with
  `strconv.Quote`. A body that is valid JSON prints through `json.Indent` of a
  re-marshalled value, so a control character in a message's text arrives as
  `\u001b`, never as a terminal escape. A body that is not JSON prints as
  `body: <strconv.Quote of it>`. `-o json` prints the record as one JSON
  object, encoded by `encoding/json`.
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

Misses: `no record at seq N; AGENT_AUDIT holds F to L` (from the stream's
info), `this broker has no AGENT_AUDIT stream`, and a connect or read error
returned as `read AGENT_AUDIT: <error>`.

**What it never does:** publish, ack, delete, purge, or create a consumer.
`GetMsg` is a direct read by sequence, so no ack floor moves.

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
3. Daemon: seq past the last returns the miss text with the stream's range;
   seq 0 is refused.
4. Daemon: no managed bus returns the no-managed-bus text.
5. Daemon: a `credential-push` caller is refused with "not permitted"
   (as `TestBusStatusIsNotACredentialPushMethod`).
6. Daemon: the stream's consumer count is the same before and after a read.
7. CLI: `marvel bus audit abc`, `0`, `-1` and no argument exit 1 before any
   dial (run with `--socket` pointing at a path with nothing listening, and
   the error must be the argument text, not a dial error).
8. CLI: a body carrying `\x1b[2J` prints as `\u001b[2J`; a header carrying a
   newline prints quoted on one line.
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
