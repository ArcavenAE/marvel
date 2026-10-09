# A handoff that survives a crash: options

**Status:** options for the operator, 2026-10-09. Nothing is built, and
nothing here is a decision. The party that produced it ratifies nothing:
the comparison, the votes and the recommendations are for the operator to
select from. Pins: marvel main ff9d210; director main 860f1aa. R-200 to
R-204 are proposed in director#273. The director redesign the commission
calls "2" is director#274.

**Why.** Today a handoff exists only if the dying seat writes it: marvel's
check is a seat-written file whose last line is the marker. A seat that is
SIGKILLed, that runs read-only (codex at `-s read-only`, crush, opencode),
or whose supervisor respawned with queued mail (R-204, proposed), leaves
nothing a successor can trust. The operator asked for ideas, not a build:
"we need new ideas about how we can do this, given 2 and our vision, and
composable parts, and given an understanding of different harnesses, and we
may need safe places for each harness to write it's handoff files, possibly
keep a running handoff in case it crashes, or ... we need ideas, the
product here are different ideas we can select from, not building it".

Method: a six-round party of nine seats. They covered three harness
squares, the daemon, director, crash consistency, custody, the vision, and
prior art from other fields. Its record is a gitignored party record in a
private repository; this file is the result. No real seat was killed and no
model session was started. Three tests ran on synthetic files or scratch
copies, and section 7 names them.

## 1. What each option is scored on

| id | the question |
|---|---|
| S1 | After a SIGKILL, does the successor get a usable handoff with no act by the dying seat, and how much is lost? |
| S2 | Does the reader accept only a record that validates itself and is bound to this shift request and generation, never the marker alone? |
| S3 | Can a human, or a seat on another harness, use it with a plain file read while marvel is absent? |
| S4 | Does it work for a seat that cannot write a file, without widening that seat's sandbox or grants? |
| S5 | Does the store refuse readers outside the team, does every copy end by the 7-day ruling, and is there one write-time secret check? |
| S6 | Does it carry the R-204 set: open asks, standing terms, unacked sends, an unconfirmed started action, and the harness session id? |
| S7 | Does each part work alone in a reduced form, and what is the smallest form that has value? |

## 2. The options

The options are layers that compose, not rivals.

| layer | id | option | mechanism in one line |
|---|---|---|---|
| format | O6 | tick lines | A one-line JSONL record (seat, generation, request, ordinal, kind, hash of the previous line) that any writer appends; the successor folds it as `dws` folds the ledger |
| writers | O1 | daemon witness log | The daemon appends checksummed records of claims and observations, and seals the log at `ReapDead` (`internal/session/manager.go:1586-1609`) |
| writers | O2 | structure tailer | A versioned, read-only adapter reads the harness's own store (Claude JSONL, codex rollout, opencode or crush SQLite) for structure only, never prose |
| writers | O3 | seat journal by hook or MCP | A per-turn hook, a Pre/PostToolUse pair, or an MCP `log` call appends one line; no file write by the model |
| writers | O5 | outbox shadow | The bus reader logs each send and its ack. Mostly built: director's `auditMirror` publishes every send to `agent.audit` |
| successor | O4 | handoff as a render | Nobody writes a handoff. The successor folds existing records into a dossier marked "reconstructed", with a list of what it could not recover |
| successor | O8 | resume is the handoff | For crash respawn only, launch with `--resume <predecessor id>` |
| successor | O10 | cut marker and read-back | An optional detector: a marker at the cut, then the successor restates its in-flight items for a peer to check |

Dropped by vote (8 of 9), with the reasons:
- **O7, handoff branch** (a local git branch). It cannot reach crush, its
  history outlives the 7-day ruling, and per-turn commits collide with the
  signing rule.
- **O9, peer scribe** (a supervisor writes from the pane). It sees the
  visible screen only, nothing scans it, and it cannot scribe a dead
  supervisor. O2 also reaches crush.

## 3. The comparison

Scores are as each option's developer gave them; the daemon cost is the
marvel seat's.

| option | S1 | S2 | S3 | S4 | S5 | S6 | S7 | marvel must add |
|---|---|---|---|---|---|---|---|---|
| O6 tick lines | + | + | + | 0 | 0 | + | + | nothing alone |
| O1 witness log | + | + | 0 | + (crush 0) | 0 | 0 | - | one token-bound append verb, and a seal at `ReapDead` |
| O2 structure tailer | + | 0 | + | + | 0 | - | 0 | an adapter per harness, and a store-locator field |
| O3 seat journal | + | + | 0 | + | 0 | 0 | 0 | the same append verb, and a `log` tool |
| O5 outbox shadow | + | 0 | + | + | 0 | - | + | nothing |
| O4 render | + | 0 | 0 | + | 0 | + | 0 | nothing; director K4, K9 and K15 (director#274) |
| O8 resume | + | - | - | 0 | - | 0 | + | pass `--resume`; keep `HarnessSessionID` |
| O10 read-back | 0 | + | - | + | 0 | 0 | + | a bus publisher (marvel publishes nothing today) |

No option scores + on S1, S4 and S6 together. The options that survive a
kill without the seat carry little of the R-204 set; the ones that carry it
depend on records or writers existing first.

## 4. Bundles, and how they held up

- **P, records first:** O4, O5, and C2 pointers. Smallest, and mostly built.
  It rides on director#274's design D.
- **Q, witness:** O6, O1 and O3 on one shared append verb, plus C1 intake.
  Its loss window is one turn.
- **R, harness-native:** O2, plus O8 for crash respawn only, plus C2.

| scenario | P | Q | R |
|---|---|---|---|
| SIGKILL mid-turn, Claude | partial | partial (pass with the Pre/PostToolUse pair) | partial, best |
| SIGKILL mid-turn, codex | partial | pass | pass |
| compaction mid-task | partial | partial to pass (PreCompact) | partial |
| read-only codex seat at 72h max-age | partial | pass, with a closing log call | fail |
| planned max-age rotation, read-only seat | fail, unless a render may stand for the marker | **pass, the only one** | fail |
| successor dies before acceptance | pass for asks | partial | partial |
| daemon dies with the seat | partial | pass for content, late in time | partial |
| host power loss | partial | partial: a lost suffix is silent without fsync | partial |
| supervisor respawns with queued mail | partial | partial | partial |
| bus down at the shift | partial | partial; the MCP variant fails | partial |
| a token and private text pasted into notes | partial | partial | **fail on O8** |
| crush seat killed | partial | partial | pass for pointers |
| opencode seat killed | partial | partial, pass with a per-turn log | pass for pointers |
| marvel absent | partial | partial; fail for gaps after it died | partial for a human |
| stale marker from an earlier generation | pass | partial: `ShiftRequest` has no request id | partial |
| torn last line | pass | pass | partial for JSONL |

O10 earns its place only as a detector: after a power loss (the only one),
on Q at a planned rotation, on opencode, and on P for Claude.

With marvel absent, a human reads:
- P: the Desk, the ledger and the audit stream, with their own tools;
- Q: `ticks.jsonl` with `cat`, as far as it was written;
- R: the harness stores on disk.

## 5. Attached to every option

The cheap amendments (accepted 8 of 9; one seat excepted the direct hook
appends until a hook's permission escape is checked):
- fsync the ask, term, send, ack and seal records; the first record after a
  restart carries the last sequence number found;
- a Pre/PostToolUse pair, so an unmatched start is the unconfirmed action;
- a PreCompact line, and a re-read at SessionStart with source `compact`;
- seat hooks append tick lines directly, with marvel verbs optional;
- record the harness session id and store path for every harness, and read
  by session id, never "latest";
- a request id on `ShiftRequest`, with readers checking generation and
  request;
- copy the lineage onto a restarted successor (near
  `internal/team/controller.go:1866`);
- scan before `--resume`, falling back to the O4 render on a hit;
- `handoff.secret-flagged` events carrying the session, rule id and line,
  never the match;
- inherited mail (director's U1) in P, within the same team and role.

Custody overlays (8 of 9):
- C2, pointers not payload (8 KiB, inside #713's 64 KiB cap), applies
  everywhere;
- C1, one intake that caps size, scans, labels, writes at mode 0600 and sets
  the expiry, applies on Q;
- C3, hold until second proof, is its own ruling.

The scanner (8 of 9) is gitleaks reading stdin, redacting by position,
flagging and never refusing. It caught a random `ghp_` token in a synthetic
test, and it does not detect private-repo text. The option says so.

## 6. For the operator

Every recommendation is valid until 2026-10-23; the architect role
re-checks it then. Nothing executes on silence.

**Open split.** Should the panel name a starting combination?
- 5 of 9 say P now, then Q's shared append verb, because P is mostly built
  and only Q unblocks read-only seats. One of the five holds this only while
  the verb stays optional beside direct hook appends.
- 3 of 9 say present the options unled: the commission asked for options,
  and no kill test was run.
- 1 of 9 says Q first, with P's render as the fallback.

**Rulings the options need:**
1. May marvel read each harness's private store, read-only and local, with
   optional adapters that fail closed? (O2)
2. Is a 30-day harness transcript an accepted second copy, given the 7-day
   ruling? (O8, O2)
3. May a crash respawn start in the mode `--resume` restores? (O8)
4. Is a daemon-held record of seat activity, under the 7-day retention,
   acceptable? (O1)
5. With no seal, should a successor refuse to take over, or only warn? (O1)
6. Is a short seat-written note acceptable under the 7-day retention, and
   may `transcript_path` be stored? (O3)
7. May a reconstructed dossier be the acceptance-write input? (O4)
8. May the daemon publish on the bus? (O10)
9. May an accepted handoff expire before 7 days? (C3)
10. At rotation, may marvel write a redacted handoff and continue, rather
    than refuse? (C1)
11. May the predecessor's drain wait for an acceptance record? (marvel has
    no acceptance step today)
12. May a dossier stand for the marker at max-age, so that P unblocks a
    read-only seat?

**Found on the way, outside the options:**
- director's `auditMirror` keeps full send bodies for 30 days
  (`director-mcp/bus.go:889-893`), against the 7-day handoff ruling.
- marvel drains the predecessor as soon as the successor runs, and a health
  restart drops the successor's lineage. `Predecessor` is set only on the
  shift spawn (`internal/team/controller.go:2315`); the health path deletes
  and recreates the row (`:1860-1867`).
- The marker check never compares the file with the request time
  (`internal/team/shift_handoff.go:167-176`, `:357`). In a synthetic test,
  a body with a hole or a mid-body truncation before an intact marker
  passed. A path without `{session}` could complete on an earlier
  generation's file. It is a sibling of #612, and it is routed as a defect.

## 7. Checked, and still unchecked

Checked at the pins or on one host:
- the codex hook payload carries `transcript_path` and `session_id`
  (`cmd/marvel/codexctx.go:57`, `:66-67`);
- marvel mints and keeps `HarnessSessionID` for a fresh Claude launch only
  (`internal/runtime/claude.go:86-93`, `internal/api/types.go:314`), and no
  store-path field exists for any harness;
- below tmux 3.5, death by signal and lost status read the same
  (`internal/api/types.go:273-276`);
- the daemon reaps at start (`internal/daemon/daemon.go:624`);
- `remain-on-exit` is on (`internal/tmux/driver.go:424-442`);
- the director MCP shim exits on a bus connect failure
  (`director-mcp/main.go:147-151`);
- the inbox ack floor is keyed to the agent id, so a successor never reads
  instance mail;
- SQLite in WAL mode kept committed rows and dropped an open transaction
  after `kill -9` on a scratch database; a copy of the main file alone lost
  the table;
- opencode's store can be read while two opencode processes hold it.

Unchecked, and each could move a score:
- whether a SIGKILLed Claude session resumes with the same id, and how much
  of a killed turn is flushed;
- whether a codex hook subprocess can write a file at `-s read-only`;
- whether a crush `PreToolUse` command escapes `allowed_tools`;
- a concurrent read of a live crush store;
- the NATS file-store sync policy;
- fsync cost on the fleet's disks;
- whether gitleaks knows the fleet's real token shapes;
- the tmux version on every host.
