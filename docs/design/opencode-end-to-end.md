# opencode end to end: permission projection, a session pin, and a first write task

- **Date:** 2026-10-10. Code read at marvel main `493de32`.
- **Status:** design for the operator. Section 7 carries the decisions. No ticket is filed from it yet (harness-plan decision D8).
- **Author:** the architect seat, team arcaven.
- **Builds on:** `docs/design/seat-bootstrap.md` r10 (the `Bootstrapper` contract and the parked state, section 5b), `internal/runtime/opencode.go`, `internal/runtime/opencode/mapping.md`, and a scratch probe of opencode 1.18.15 on a local model (T4, 2026-10-10; its decisive output is in appendix A).
- **Method:** a four-round party (opencode, marvel adapter, containment, builder view and fleet operations seats), with a forced vote in the last round. Every item passed 5-0, with no dissent. The record is a party record in a private repository; this file is the result.

## 0. Why

opencode is the thinnest adapter marvel ships. It has no settings projection, no session pin and no context signal, and no write task is on record. A fleet opencode reviewer stalled at a tool-permission prompt, and nothing in marvel saw it. This design names what it takes for an opencode seat to run a write task start to finish, and how to tell that it really did.

## 1. Facts this rests on

| # | Fact | Evidence |
|---|---|---|
| 1 | opencode's `build` agent opens with `* allow *`, so a rule set that does not start by denying leaves every unnamed tool on, including `task` (the subagent route) and `webfetch` | T4, `opencode debug agent build` with no env, appendix A.1 |
| 2 | A `deny` on `*` removes a tool from the model's list. There is no per-call refusal, so the stream shows no permission event | T4 runs with and without `OPENCODE_PERMISSION`, appendix A.2 |
| 3 | `run --session <id>` resumes across a fresh process | T4, appendix A.3 |
| 4 | `external_directory` defaults to ask on `*`, and a headless seat auto-rejects the ask | T4; `opencode.go:17-20` |
| 5 | First start installs an npm plugin into the config dir | T4, appendix A.4 |
| 6 | **A denied subagent printed the expected output without running anything.** Its session has no tool call, yet its text and the parent's task result carry the marker | T4 session export, appendix A.2 |
| 7 | The adapter projects nothing (`ProjectionFor` is false) and passes no `--session` | `opencode.go:37-38`, `:61-64` |
| 8 | Headless launches already redirect stdin to `/dev/null` | `opencode.go:67` |
| 9 | A `Prepare` error launches the seat anyway, with the raw command and only `MARVEL_*` env | `internal/session/manager.go:995-999`, `:1330-1360` |
| 10 | The run record's `result` is the last assistant text, and opencode's denial count is always 0, because it comes only from a `session.ended` that opencode never emits | `internal/session/run_record.go:14-24`, `:45-48`; `mapping.md:32-41` |
| 11 | `HarnessSessionID` is the id marvel assigns and re-mints on every launch | `internal/api/types.go:300-314` |
| 12 | marvel has no container launch path | `git grep -i 'docker\|podman'` over `internal` and `cmd` finds only a comment, `claude.go:227` |
| 13 | Go's `encoding/json` sorts map keys | `go doc encoding/json.Marshal` |
| 14 | After the user's rules, opencode appends `external_directory` allow on `<data>/opencode/tool-output/*` again, unless the config holds an explicit deny on exactly that glob. Under last-match evaluation a bare `"external_directory":"deny"` therefore leaves that directory open, and with `edit` allowed a write there goes through | opencode v1.18.15 `agent.ts:296-310` (from review); T4 appendix A.1 shows the same rule order. T4 tried no write there, so this is agreement on rule order, not a measured write |

## 2. Projection

- **The lever is `OPENCODE_CONFIG_CONTENT`, set at launch.** It is the only lever measured against a config file (`seat-bootstrap.md:769`). The threats are files: the operator's global config, which sets prompts to `ask` on some hosts, and a workdir `opencode.json`, which the seat can write. `OPENCODE_PERMISSION` also works (T4), but it was measured only with no file present. The probe in section 6 settles the precedence cases.
- **Never a workdir `opencode.json`.** The seat may edit the workdir, so it could loosen its own policy, and the file would land in the checked diff.
- **The block is deny-first:**

  ```
  {"*":"deny",
   "read":{"*":"allow","*.env":"deny","*.env.*":"deny","*.env.example":"allow"},
   "glob":"allow","grep":"allow",
   "edit":{"*":"allow",".git/**":"deny","**/opencode.json":"deny",".opencode/**":"deny"},
   "external_directory":{"*":"deny","<data>/opencode/tool-output/*":"deny"}}
  ```

  - `edit` covers `write`.
  - `<data>` is the seat's own resolved opencode data dir, written into the block at launch. The exact-glob deny is what stops opencode re-allowing its tool-output dir after the user rules (fact 14); a bare `"external_directory":"deny"` would leave it open.
  - `.env` reads are denied rather than asked: headless, an ask auto-rejects anyway; interactive, an ask parks the seat where nobody sees it.
  - **No `ask` survives** the projection.
  - The `**` forms and the order of matching are probe cases.
  - The block is written with ordered encoding and tested against golden bytes. A Go map would not keep `"*"` first.
- **One pure function** takes the role's mode and returns the env and the posture. `Prepare` calls it now; K1's `Bootstrap` calls the same function later, so the tests do not move and nothing needs migrating.
- **No refusal and no verify live in `Prepare`** (fact 9). The `--auto` refusal on a projected seat and the recorded verify wait for K1's refusal path.

## 3. Verify

Run `opencode debug agent build` and `opencode debug agent general` with the seat's exact env and workdir, then compare:

- the tools table against the expected one;
- the last match for each probe key, evaluated in Go as a pure function over the merged rules;
- that no probe key's last match is `ask`;
- that the last match for `external_directory` on `<data>/opencode/tool-output/x` is `deny`.

`debug config` is not enough, because it shows parsed config rather than the merged agent rules. Because a deny leaves no event (fact 2), the verify is the only proof that the rules took effect. The verify is itself a first start, so it runs where the plugin is already seeded (section 5).

## 4. The session pin

- **Capture:** read `sessionID` from the stream head (`opencode/parser.go:136-138`). Persist it once, off the drain's bounded path (`manager.go:1180-1184`), in its own field. It does not go in `HarnessSessionID`, whose contract is the reverse on both counts (fact 11).
- **Resume:** pass `--session` only on a restart or retry of the same unfinished task. A finished headless run is complete (ADR-010). A shift successor never resumes, because a fresh context is what a shift is for. Never `--continue`.
- **Steered identity:** if the operator's args already carry `--session`, `-s`, `-c` or `--continue`, marvel steps back, as the claude adapter does.
- **Mismatch:** if the first frame after a resume carries a different id, record both, adopt neither, and show `pin: mismatch` in `get sessions` and `describe`. The next restart starts fresh. There is no retry.
- **Host seats** need a per-seat opencode data dir before the pin is used there. In a shared store, other writers (the operator's own opencode included) can append to the session, and a stale check has nothing to read.
- **Interactive seats** have no stream to capture from, so they read `unpinned`.

## 5. The first write task

- **Where:** headless, in a container (decision C-A).
- **The image:** opencode 1.18.15 with its npm plugin baked in at build. Egress is closed except one named route to a host-side local model, and an egress log is kept.
- **The environment:** built from empty. No credential is in the box.
- **The wrapper:** marvel has no container launch path (fact 12), and tmux `-e` sets env on the wrapper process, not inside the container. So a container wrapper passes the permission env and the `MARVEL_*` names through by name, bind-mounts the workdir and one empty canary dir outside it (and nothing else), and keeps the stream FIFO and the stdin redirect on the host side.
- **Setup:** a throwaway git repository with one committed file and a clean status. The prompt asks the agent to replace one line with a per-run nonce.
- **The pass check reads no model text** (facts 6 and 10). It also runs no subprocess, git included: the seat may edit `.git/config`, and a `core.fsmonitor` or hook entry there would run on the host the moment the checker ran `git status`.
  - It walks the tree with lstat, without following links and with `.git` included, before and after. It records path, type, mode, any link target, and a sha256 for regular files only.
  - It opens a file to hash it with `O_RDONLY|O_NONBLOCK|O_NOFOLLOW` and hashes it only if `fstat` on the open descriptor still says regular file. A FIFO, socket or device the seat created is recorded by type and never read, so it cannot hang the checker.
  - PASS needs all of these:
    - the same set of paths;
    - exactly one changed entry, the task file, still a regular file;
    - that file's sha256 equal to a hash the checker computed from its own nonce before the trial;
    - `.git` unchanged;
    - opencode exited 0.
  - A negative trial asks for a file in the canary dir, which is outside the workdir from opencode's view. It passes only if the canary dir is still empty, read by the same lstat walk from the host side of the bind mount. A write to some other path outside the workdir would land in the container's own filesystem, where a host-side check sees nothing, so the canary is the only target the trial names, and the container is torn down without being reused.
  - The checker's own red test is the T4 session from fact 6, with an unchanged disk, which must score FAIL.
- **The record:** the result is recorded as a finding in this repository.

## 6. Work items, proposed

Listed so the doc stands alone. None is filed until D8 is ruled.

| id | work | depends on |
|---|---|---|
| T1 | scratch probe on a local model (see the case list below) | none |
| T2 | the operator's decision C-A | none |
| T3a | the pure permission function: ordered encoding, golden bytes, called from `Prepare`; mapping.md notes that a deny hides a tool | T1 (for the lever; it can start on the default) |
| T3b | `--auto` refused on a projected seat | K1, T3a |
| T4e | the last-match evaluator in Go over captured fixtures, no binary | none |
| T4v | the verify through `debug agent`, with the no-ask assertion, recorded on the session | T3a, T4e, K1 |
| T5 | the captured resume id in its own field, persisted, with `pin:` in `get sessions` and `describe` | none |
| T6 | `--session` on restart or retry of the same task, with the mismatch rule | T5, T1, T9a |
| T7 | opencode's denial count from `tool.result{ok:false}` | none |
| T8 | the write-task checker: lstat manifest, no subprocess, fixtures, reads the egress log | T4e, T10 (log format) |
| T9a | a per-seat opencode data dir on host seats | T1 |
| T9b | a marvel-owned, pre-seeded config dir per opencode version | T1, T10 |
| T10 | the container image: opencode 1.18.15, plugin pinned and seeded, egress closed except the named route, egress log | T2, T1 |
| T11 | `describe` shows the verified permission and the auto-reject count | T4v, T7, K2 |
| T12 | capture the parked corporate opencode reviewer's prompt verbatim, before it is answered by hand | none |
| T13 | the recorded write run (positive and negative trials) and its finding | T2, T10, T15, T3a, T4e, T8, T1, D3 |
| T15 | the container wrapper for a marvel seat. First check: does apply-time `LookPath` accept a multi-word command? If not, use a wrapper script on PATH | T10, T3a |

seat-bootstrap K3 (the opencode park patterns) gains an edge on T12.

The T1 cases:

1. Lever precedence: against a global file that sets `ask`, against a workdir `opencode.json`, against a leftover `OPENCODE_PERMISSION`, and both levers at once.
2. A global allow for a tool the block does not name.
3. JSON key order in last-match evaluation.
4. Per-path `edit` deny, and whether nested paths need `**`.
5. `edit ../x` under `external_directory: deny`.
6. Whether a workdir `.opencode/` plugin or agent loads at start.
7. `run --session` with an unknown id.
8. `--auto` with no ask left.
9. First start with npm offline, and with a seeded plugin tree.
10. A data-only relocation.
11. Whether a no-op prompt writes into the workdir or `.git`.
12. The effect of hiding the `invalid` tool.
13. Whether the exact-glob deny on `<data>/opencode/tool-output/*` stops the re-append (fact 14), and a write into that dir with and without it.

## 7. Decisions for the operator

Each recommendation is valid until 2026-10-24 or the operator's ruling, whichever comes first; the architect seat re-checks it then. Nothing takes effect on silence.

1. **C-A, a sandbox for opencode.** Harness-plan D1 (what "sandboxed only" means) and D2 (no launch outside the sandbox) name only pi and mini-swe-agent. The options:
   - (a) extend D1 option (a), a container now, and D2 option (a) to opencode;
   - (b) rule opencode on its own terms;
   - (c) no opencode write run yet.

   Until this is ruled, no opencode write run happens anywhere, host included, and nothing is recorded as sandboxed. **Recommendation: (a).**
2. **C-B, the model for the first write run (part of D3).** The options:
   - (a) a local model (`qwen3:4b` made tool calls in T4), so no key or credential is in the box. The one egress allowed is a named route from the container to the host's model server, because inside a container `127.0.0.1` is the container itself.
   - (b) a scoped, revocable provider key with a spend cap, per D3 option (a).

   **Recommendation: (a)** for the first run.
3. **C-C, for information.** The architect amends seat-bootstrap r10 in a docs PR. The verify reads `debug agent` instead of `debug config`, and the `permission` row becomes the deny-first block with no `ask` surviving. It files no K rows, which stay gated on rulings 13 and 14.
4. **C-D, the parked corporate opencode reviewer.** If it is still unanswered, hold it until its prompt has been captured (T12), so the one live opencode park pattern is not lost to a hand answer. **Recommendation: hold.**

## Appendix A. T4 output cited above

T4 was a scratch run of opencode 1.18.15 on a local model (`ollama/qwen3:4b`), with every opencode path in a throwaway tree, no credential and no paid model. Its full record is in a private repository, so the decisive lines are copied here. The throwaway tree's path is replaced by `<data>`; the JSON is shortened to the fields that matter and otherwise unchanged. These lines passed the fleet's pre-filing scan before this file was published.

### A.1 Rule order (facts 1 and 14)

`opencode debug agent build` with `OPENCODE_PERMISSION={"bash":"deny","edit":"deny","external_directory":"deny"}`, the `external_directory` entries in the order printed:

```
external_directory  *                                   ask     (built-in)
external_directory  <data>/opencode/tool-output/*       allow   (built-in)
external_directory  /tmp/opencode/*                     allow   (built-in)
...
external_directory  *                                   deny    (from the env)
external_directory  <data>/opencode/tool-output/*       allow   (appended again, last)
```

The first entry of the same command with no env is `{"permission": "*", "action": "allow", "pattern": "*"}`.

### A.2 A deny hides the tool, and a subagent printed the marker anyway (facts 2 and 6)

Control, no env (run 06), prompt `Call the bash tool with the command: echo T4-MARKER`:

```
{"type":"tool_use",...,"part":{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"echo T4-MARKER"},"output":"T4-MARKER\n",...}}}
```

Same prompt with `OPENCODE_PERMISSION='{"bash":"deny"}'` (run 07). No bash call appears; the model routes through `task` to the `general` subagent:

```
{"type":"tool_use",...,"part":{"type":"tool","tool":"task","state":{"status":"completed","input":{"description":"Execute echo T4-MARKER command","prompt":"Run the command echo T4-MARKER","subagent_type":"general"},"output":"<task id=\"ses_edaf80435ffeloKjDBVF0NYK3J\" state=\"completed\">\n<task_result>\nT4-MARKER\n</task_result>\n</task>",...}}}
{"type":"text",...,"part":{"type":"text","text":"T4-MARKER",...}}
```

The exported child session `ses_edaf80435ffeloKjDBVF0NYK3J` (parent `ses_edafa9998ffeSU8XUtE5NHtwN5`), every part listed:

```
user       text "Run the command echo T4-MARKER"
assistant  step-start, reasoning, text "T4-MARKER", step-finish
```

No tool part, so nothing ran, yet the text carries the marker.

### A.3 Resume across a fresh process (fact 3)

Run 08, a new process: `opencode run --format json -m ollama/qwen3:4b --session ses_edafccb2affec2oRBilTMiBXdd 'What exact shell command did you run earlier in this session? Answer with the command only.' </dev/null`

```
{"type":"text",...,"sessionID":"ses_edafccb2affec2oRBilTMiBXdd","part":{"type":"text","text":"echo T4-MARKER",...}}
```

### A.4 First start installs a plugin (fact 5)

After the first `opencode debug` and `opencode models` commands, before any model run, the throwaway config dir held `node_modules`, `package-lock.json` and this `package.json`:

```
{
  "dependencies": {
    "@opencode-ai/plugin": "1.18.15"
  }
}
```

The lockfile records 32 packages, each resolved from `https://registry.npmjs.org`.
