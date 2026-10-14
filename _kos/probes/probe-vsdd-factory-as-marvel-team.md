# Probe brief: vsdd-factory as a marvel team, one session per parallel branch

**Status:** proposed. Nothing in this brief has run.
**Question:** `_kos/nodes/frontier/question-vsdd-factory-as-marvel-team.yaml`
**Idea:** `_kos/ideas/vsdd-factory-as-marvel-team-pack.md`
**Origin:** the operator's idea, worked by a twelve-seat design party on 2026-10-14. Every cite below was checked by one command during that party.
**Upstream:** vsdd-factory (BOHICA-LABS/vsdd-factory) is MIT-licensed; the rc.25 pack metadata reads `license: MIT`. Its main attribution is its upstream author.

## 0. Terms

- **Arm A:** vsdd-factory release rc.25, commit `51023185`, run in one Claude Code session, as it ships.
- **Arm B:** the same release, with each parallel branch run as its own headless marvel session.
- **Branch:** one story group in greenfield's single `type: parallel` node (`plugins/vsdd-factory/workflows/greenfield.lobster:652-654`). `git show 51023185:<that file> | grep -c 'type: parallel'` prints 1.
- **Branch seat:** the marvel session that runs one branch.
- **Shim:** the command arm B's orchestrator calls, in place of the Agent tool, to start and await branch seats.
- **Key:** `<run-id>-c<cycle>-w<wave>-g<group>-a<attempt>`: lowercase, no slashes, with a fresh run-id for every probe run. It is both the seat's session name and the idempotency key, meaning a repeated request with the same key is a no-op.
- **Accepted story:** a story whose scenarios in the probe acceptance suite all pass (section 9).
- **The four closes:**
  - (a) not comparable;
  - (b) instrument blind;
  - (c) a comparison with stated power;
  - (d) not distinguishable at the ceiling.

**The contrast is separate sessions, not parallelism.** Arm A already fans branches out concurrently, as subagents of one session. Arm B gives each branch its own session: its own process, conversation prefix and cache TTL class, drawing on the same account.

## 0.1 Open operator rulings

Five decisions in this brief are recommendations until the operator rules on them. Each site that depends on one names it here. Nothing below runs on a recommendation alone. No step on either track spends on models before R1 and R3 are ruled: M3 is track M's first model spend, and B2 is track B's.

| ruling | the recommendation | gates |
|---|---|---|
| R1. Pilot spend | spend the pilot ceiling only (the smoke runs, arm B's live tests and entry drill, the seed and three pilot runs, s11); the series is a separate go at M6 | M3 and B2, each track's first model spend; B3 follows B2 |
| R2. The decision metric | one primary, cost per accepted story, with every axis reported (s9.3); the recorded alternative is a verdict per axis | M1, which freezes it into the protocol |
| R3. The probe host | a host with no live seats, whose signing key signs unattended (S15) | M3 and B2, since the live tests and the drill run on the probe host |
| R4. The permission mode | `auto`, the same in both arms, with classifier requests metered (s11) | M1 |
| R5. The marvel CLI surface | the flags and verb proposed in s4 | B1 |

## 1. Order of work and gates

Track M (measurement) and track B (the arm B build) start together and join at step 8. A failed step that triggers no close stops the probe and returns it to this brief's authors.

| step | entry | output | can close |
|---|---|---|---|
| M1. Pre-register (s8) | this brief merged; R2 and R4 ruled | protocol file at a sha | none |
| M2. Test project and suite (s9, s12) | M1 | both frozen at a sha; canary planted | none |
| M3. Smoke (s5) | pack verified; R1 and R3 ruled | every smoke check passes | (a), if hooks cannot load headless |
| M3b. Ledger and meter (s10, s11) | M3 | the reader passes its fixture and reconciles one smoke run | none |
| M4. Seed (s12) | M2, M3 | Phase 2 snapshot at a sha, its wave widths recorded; exit check S8 (resume) | (a), if the seed cannot meet s12 after two revisions of the brief. A brief revision returns the probe to M2; the suite is re-frozen before the next seed. |
| M4b. Suite mapping | M4 | scenarios tagged with the seed's story ids, or with the brief's features if S8 failed (s12), and frozen; the seed snapshot, or the test project's start commit, fails every scenario | none |
| M5. Pilot | M3b, M4b; prices frozen; token ceiling set | 3 arm A runs: noise floor per axis, serial estimate per wave | (b), by s8.5's blind stop |
| M6. Pilot card | M5 | spend; computed n (reported only); the spread with its 80% and 95% intervals; per-run cost and accepted stories, naming any run with zero accepted; the serial estimate beside rate-limit retries in the node; blocks per weekly window the quota allows, and the projected series end date | none; going on is the operator's call |
| B1. marvel PR (s4) | R5 ruled | merged and tested | none |
| B2. Patch and shim (s3, s4) | B1; R1 and R3 ruled | arm B pack at a sha | none |
| B3. Entry drill (s7) | B2 | D1 to D5 pass | none; a failure is a defect to fix |
| 8. Fidelity run | M6 go, B3 | one arm B run, outside the statistics; checklist from transcripts | (a), if a step cannot be reproduced |
| 9. Paired series | 8 | 7 blocks of one run per arm, order by the protocol, no interim looks | none |
| 10. Finding | ceiling, or a stop rule | finding | the close that fired: (a), (b), (c) or (d) |
| 11. Harvest | 10 | node updated; idea file points here | none |
| R. Reference run | after step 10 | one interactive arm A run through the upstream plugin channel (s2) | none |

**The serial estimate.** Arm A already runs its branches concurrently, so arm B gains wall clock only where branches slow each other inside one session. The estimate for each wave is `(T_node - T_longest) / T_run`:
- `T_node`: wall clock inside the parallel node for that wave;
- `T_longest`: its longest branch, from subagent transcript timestamps (inferred readable);
- `T_run`: the run's wall clock (s8.6).

This is an estimate, not a bound: `T_longest` is measured under sibling contention, inside one session and one account rate limit. The pilot card reports it beside the count of rate-limit retries inside the node. The speed axis is never dropped on it.

## 2. Arm A

**Install, by hash.** The pack comes from ArcavenAE/sideshow-packs release `vsdd-factory-v1.0.0-rc.25`, part of the VSDD-as-sideshow-packs work (aae-orc-amet):
- `install.meta.yaml`: upstream commit `51023185658350afaacaa931a175103d915d14ba`, subtree `d4fa6515`;
- tarball sha256 `6d38a905222bcf9de3b0fe74acd4fc29fbaca6d954f18c6ab3db27387831cee1`;
- file manifest sha256 `e0ec70a15ebf74f409ef0b468b59bc400804157224383a62f526c8421c94325c`.

Arm A installs that tarball unchanged. Arm B installs a derived pack under its own version (s3), so a patch never reaches arm A.

**Launch.** A marvel team with one headless role, declared fresh for each run as `arm-a-<run-id>` so its `max_tokens` covers one run, on the probe daemon (s5.1). The role sets `restart_policy = "never"` and declares no `shift`, so marvel never re-runs or replaces the orchestrator mid-run; the harness's own compaction is recorded, not prevented. The role prompt names `.probe/approvals.yaml` and says: at each `type: human-approval` step, apply the answer for that step name (smoke S12). The pilot and series runs resume from the seed snapshot (s12); the seed run starts greenfield on the frozen brief. The orchestrator is the main-thread agent, launched with `--agent vsdd-orchestrator`, `--model <full model id>` and `--permission-mode <permission mode>` in the project path. The role's `runtime.env` carries the fairness profile (s6).

**The scripted answerer** is `.probe/approvals.yaml`. It is listed in `.git/info/exclude`, its sha256 is logged per run, and it is identical in both arms. It holds one fixed answer per `type: human-approval` step in greenfield.lobster at `51023185`:

| step (line) | fires | answer |
|---|---|---|
| phase-1-design-system-approval (180) | `feature_type` ui or full-stack | approve |
| phase-1-multi-variant-approval (200) | ui or full-stack, complex screens | first variant |
| phase-1-human-approval (335) | always | approve |
| multi-repo-human-confirmation (375) | multi-service topology | continue as mono-repo |
| phase-2-human-approval (538) | always | approve |
| phase-6-human-approval (1319) | always | approve |
| session-review-approval (1394) | always | defer every proposal |

The test project pins `feature_type` and a single-service topology (s12), so the fired set is known in advance. Under the seed, only the last two steps fire in a series run. A different fired set between runs is a fidelity defect.

**Recorded per run:**
- every transcript (main thread and subagents), copied at run end and kept private;
- the step trace: each lobster step executed, skipped or retried;
- the hook log;
- `.factory/STATE.md`, the `factory-artifacts` tip, and the timestamp of each phase-gate state commit;
- the approval steps fired, with their answers;
- the full model id per role and the MCP servers present;
- adversary passes to convergence per story;
- re-dispatches per story and model escalations;
- duplicate executions: from the step trace and the marvel session list, any story with two live sessions at once, or any delivery item run twice for one story and attempt. Pass: zero, in both arms (s7.2);
- compaction events per session, each with the step in progress and the re-anchor hook's output;
- the pack digests, read back;
- wall clock as s8.6 defines it.

## 3. Arm B, shape

**The patch.** Two orchestrator files change. They ship as one patch with its own sha256, inside arm B's derived pack.

- `agents/orchestrator/orchestrator.md`:
  1. After line 40, a "Branch dispatch" section. At a `type: parallel` step, the orchestrator calls `shim dispatch` once per story group, then `shim await` with every key, repeating while any key reads `pending`. Each result is a claim (`per-story-delivery.md:133`). Before the wave gate, the orchestrator verifies in git that each story's PR merged and its worktree is gone, then dispatches state-manager once with every story's merge status.
  2. Line 124 ("ALWAYS delegate via the Agent tool") and line 138 ("Use the Agent tool with `subagent_type` for EVERY delegation") each gain: "except `type: parallel` branches; see Branch dispatch." Lines 125 ("ALWAYS update STATE.md via state-manager") and 126 ("never delegate to yourself") each gain: "except in Branch mode, started by the shim."
  3. The Branch dispatch section and Pipeline Resume (`orchestrator.md:360`) each gain: "skipped in Branch mode."
- `agents/orchestrator/per-story-delivery.md:18` gains: "one branch seat per story group, through the shim."

**The branch seat.** Launched with `--agent vsdd-orchestrator`, `--model <full model id>` and `--permission-mode <permission mode>`, in the project path, with `PROBE_BRANCH_KEY=<key>` in its environment and this prompt:

> Branch mode. Deliver only story group `<group>` of wave `<wave>`, key `<key>`. For each story in the group, in order, run `per-story-delivery.md` step 2 items a, b, c, c2, d, e, f and g. Dispatch every item with the Agent tool and `subagent_type`, using the Task Preamble (`orchestrator.md:141-155`). Write under `.factory/` only within `.factory/cycles/<cycle>/<story-id>/` for your own stories. Do not edit `.factory/STATE.md`, commit in `.factory/`, dispatch state-manager, or run `bin/factory-cas-push.sh` or `bin/factory-lock-write.sh`. End with `BRANCH_RESULT key=<key>`, then, for each story: its PR number, merge sha and adversary pass count.

Each step stays its own subagent, one level deep, as in arm A.

**The orchestrator seat** launches exactly as s2's role: no restart, no shift, the same answer file, flags and env, from the derived pack.

**One exception, listed for fidelity.** In arm A, cleanup-worktree updates `STATE.md` from inside the branch (`greenfield.lobster:796-802`). In arm B, that update moves to the orchestrator's single state-manager dispatch after the barrier (s7.1).

**Byte-identical to `51023185`,** checked against the rc.25 file manifest (sha256 `e0ec70a1...325c`): every rc.25 path matches except the two patched files, and the only added path is the shim. Each is listed with its sha256. The derived pack installs beside rc.25 under its own version, with `--no-activate` (s5.1). Unchanged:
- every other file under `agents/`;
- all 16 `.lobster` files;
- `skills/`, `templates/`, `rules/`, `hooks/`, `hook-plugins/`, `bin/` and `config/`;
- `hooks-registry.toml`.

Models stay at their frontmatter values. Phases 1, 2 and 4 to 7 run in the orchestrator seat, as in arm A.

## 4. Arm B, build

**The marvel PR.** Today `marvel run` sets no mode, no prompt and no environment (`internal/daemon/daemon.go:2111-2116`; `cmd/marvel/main.go:1597-1602`), names the session by the clock (`daemon.go:2129`), and returns at once. The proposed surface below stays a proposal until R5 is ruled. The PR would add:

- `--mode headless` and `--prompt-file <f>`, copied into the runtime. The claude adapter already refuses headless without a prompt (`internal/runtime/claude.go:102`), and adds stream-json output (`:119`).
- `--env KEY=VALUE`, repeatable, copied into `Runtime.Env`.
- `--name <key>`. The store refuses an existing key (`internal/api/store.go:331-332`), so a reused name cannot start a second seat.
- `marvel wait <key> --timeout <d> --poll 5s`. It reads store state, never the event ring, which drops events for slow subscribers. Exit codes:
  - 0: succeeded;
  - 1: failed;
  - 2: crashed;
  - 3: timeout, still running;
  - 4: unknown key.

Unit tests (Go):
- each flag;
- headless without a prompt is refused;
- a second run with one name is refused;
- flags after `--` reach the runtime args;
- `wait` on each terminal state, on a timeout and on an unknown key;
- a headless ad-hoc session that exits 0 is stored `succeeded` and stays until an explicit delete;
- budget admission applies to a named run in a declared team under a role the team does not declare;
- such a run is neither shed nor refilled across ten controller ticks.

**The shim** is a shell command in arm B's pack, called through the orchestrator's Bash tool:

- `dispatch <key> <prompt-file>` runs `marvel run --mode headless --workspace <probe workspace> --team arm-b-<run-id> --role branch-run --name <key> --workdir <project path> --prompt-file <f> --env PROBE_BRANCH_KEY=<key> --env ... -- claude --agent vsdd-orchestrator --model <full model id> --permission-mode <permission mode>`. It refuses when `PROBE_BRANCH_KEY` is already set, so a branch seat cannot dispatch. Four reasons for the shape:
  - **Team and role.** Each run declares a fresh team, `arm-b-<run-id>`, with its `max_sessions` and `max_tokens`, and declares no role named `branch-run`. A fresh team matters because the token guard counts ended sessions as well as live ones (`internal/daemon/admission.go:33-39`), so one team across the series would carry earlier runs' tokens. Admission is team-scoped (`internal/daemon/daemon.go:2118-2126`), while the controller sheds a declared role's excess, oldest first, and counts a finished headless session in its slot (`internal/team/controller.go:1265-1281`; `internal/api/budget.go:252-257`). `max_sessions` leaves room for the widest wave above the declared orchestrator.
  - **The `--`.** It keeps `--agent` for the harness, since `cmd/marvel` never disables interspersed flags.
  - **The bare word `claude`.** The adapter is chosen by runtime name, with a generic fallback (`internal/runtime/adapter.go:386-394`).
  - **"Already exists".** That reply counts as dispatched. Any other dispatch error, an admission refusal for one (`internal/daemon/admission.go:73-85`), prints `refused` with marvel's message. The orchestrator raises no `attempt` on it and ends the run as a limit stop (s8.5). It first stops every live session of team `arm-b-<run-id>` and confirms each one is terminal with `marvel wait`; the inter-run gap starts only then.
- `await <key>...` runs one `marvel wait --timeout 100s` per key, all concurrently, so the whole call returns within 120 s, under the pinned 600,000 ms Bash limit (s6). Under the 5-minute TTL pin (s6), a return inside 300 s reads the orchestrator's prefix rather than rewriting it. Per key, it prints one of `succeeded`, `failed`, `crashed`, `pending` or `unknown`, plus the harness session id, so the transcript joins the run. If the call itself errors or times out, every key it named reads `pending`.

The shim never retries and never reports a story done. On a terminal non-success (`failed`, `crashed` or `unknown`), the orchestrator checks each story in the group in git. A merged story is done. Attempt+1 is dispatched only for the stories that did not merge. Only the orchestrator raises `attempt`, and never on `pending` or on a failed `await` call.

Unit tests (shell, with a fake `marvel` on PATH):
- dispatching twice creates one seat;
- the key is deterministic;
- await moves `pending` to `succeeded`;
- await on two keys returns within one 100 s bound;
- a failure triggers no retry;
- dispatch refuses under `PROBE_BRANCH_KEY`;
- the exit codes map to the five words.

**Live tests,** on the probe host, inside the pilot ceiling (s11):
1. One headless seat with a trivial prompt: the state reaches `succeeded`, the session id matches a transcript, and `--env` values reach the seat process.
2. One branch of the test project, end to end, with the patch.
3. The entry drill (s7.4).

**Build order:** the marvel PR, the shim against the fake, the patch, live tests 1 and 2, then the drill. None of it touches arm A.

## 5. Environment and smoke

**5.1 Environment, frozen for each series.**
- **One probe host.** Arms run in series, never at once. Sessions on one macOS host share one config home and one rate limit.
- **A probe marvel daemon** on its own `--socket` and tmux socket, with every `MARVEL_*` variable unset (`env | grep '^MARVEL_'` must print nothing).
- **One account, one auth class, one Claude Code build,** with auto-update off (`DISABLE_AUTOUPDATER=1`). The probe daemon's PATH begins with a directory holding one `claude` symlink to the pinned version's absolute path, and the protocol file records that path. Before each run, the probe daemon launches one probe-socket pane exactly as it launches a seat, and `command -v claude` and `claude --version` in that pane must print the pinned path and build, or the run does not start. A seat resolves `claude` through its pane's environment, since marvel hands a command string to `tmux new-window` (`internal/runtime/adapter.go:287`).
- **The pack:**
  - check `shasum -a 256` of the tarball against s2;
  - then run `cosign verify-blob --bundle vsdd-factory-1.0.0-rc.25-arcaven.tar.gz.bundle --certificate-oidc-issuer https://token.actions.githubusercontent.com --certificate-identity-regexp '^https://github.com/ArcavenAE/sideshow-packs/' <tarball>`. The identity pattern is inferred.
  - install with `sideshow install vsdd-factory --from <unpacked dir>`. Pass: `sideshow list` shows `1.0.0-rc.25`. Arm B's derived pack is installed with `--no-activate`; a pack's first install always activates (sideshow `README.md:170-182`), so rc.25 goes in first.
- **The channel:** sideshow repo-bindings, local scope, in both arms. It writes the hook chain to the untracked `.claude/settings.local.json` (sideshow `docs/repo-bindings-enablement.md:89-93,111`). Enable refuses while a factory run is in flight (`:115-121`). Each run starts from a fresh checkout of the seed sha, enabled with `sideshow enable vsdd-factory@<that arm's version>` before launch. Branch seats run in the project path with `--agent`. Story worktrees are created by the factory in-run and never enabled.
- **tmux 3.5 or later** on the probe tmux server (`tmux -L <probe socket> -V`), because below 3.5 a dead pane's exit status can be lost and a clean exit recorded as crashed (ArcavenAE/marvel#714, open).
- **Pins on the marvel side:** the marvel commit the probe daemon runs (`marvel version`).

**5.2 Smoke checks.** A failure stops the probe before the pilot. Ids S2 and S11 were retired during design and are not reused.

| id | check | pass |
|---|---|---|
| S1 | bindings: `sideshow coexist-check vsdd-factory --repo <project path>`; the hook chain present in `settings.local.json`; `readlink -f plugins/vsdd-factory` and `readlink -f .claude/agents/vsdd-orchestrator*` resolve into that arm's store version | preflight clean; count above 0; links resolve to the arm's version |
| S3 | the agent key | `vsdd-orchestrator` in the project path |
| S4 | hooks fire under `claude -p` in a detached pane on the probe tmux socket (`tmux -L <probe socket>`), with `--output-format stream-json --verbose --include-hook-events` | at least one hook event whose command path resolves (`readlink -f`) into that arm's store version |
| S5 | the pack's agents resolve | the init event lists `vsdd-` agents |
| S6 | a branch seat, and arm A's session, each with `--agent vsdd-orchestrator` | S4 and S5 pass; each init event names the orchestrator agent |
| S7 | the TTL pin: one main-thread turn and one subagent, with both TTL variables at 5 minutes | 1-hour writes under 0.1% of write tokens, as s6 |
| S8 | resume, run as M4's exit check: restore the seed snapshot into a fresh enabled checkout and launch | the first dispatch is the Phase 3 entry; no Phase 1 or 2 step re-runs |
| S9 | unresolved refs: list the 25 namespace references in 13 files that repo-bindings does not rewrite (`install.meta.yaml`, `channel_divergence`) | the list is recorded. A touched ref is a fidelity record in both arms, not a stop. |
| S10 | telemetry: `CLAUDE_CODE_ENABLE_TELEMETRY=1`, exporting to an OTLP collector the protocol file names, its endpoint passed by `--env` and `runtime.env` | `api_request` events present for each session, with `query_source` |
| S12 | the answer file: a headless run reaches one approval step | it applies the file's answer for that step name |
| S13 | result-line coverage | subagent tokens appear in the headless result line; if not, the s11 cross-check is main-thread only |
| S14 | sharing control, run before any other smoke check that makes model requests: after 65 idle minutes on the account, two branch seats with one trivial prompt, the second launched within 60 seconds of the first's first response | the first's first-request read is recorded in the protocol file as the cold reference (s6 gap row), the second's as the sharing ceiling |
| S15 | signing: a branch seat makes one signed commit unattended in a scratch repository | the commit verifies (`git log --show-signature -1`). If signing needs a touch or fails, the probe stops; signing is never turned off |

**5.3 Records per run:**
- the pack and patch sha256;
- `sideshow --version`;
- the harness build and the `claude` path;
- the S1 and S3 outputs for the project path, with the `readlink` targets;
- the hook events per session;
- compaction events per session.

A session without hook events is a fidelity failure, never a data point. A `readlink` target outside the arm's version voids the start before any spend.

## 6. Fairness profile

The harness facts held identical in both arms. They are pinned in the protocol file and read back from every run; a run whose read-back disagrees is flagged, counted in the primary analysis, and left out of a sensitivity analysis. A "server gate" is a vendor-side flag that can change without a client release.

| fact | pin | read-back | pass |
|---|---|---|---|
| Client build | one Claude Code version, by absolute path; auto-update off | `version` on each session's first record, read at session start | one value; a mismatch stops the run at once |
| Auth | one subscription account | login method in clear; the full `claude auth status` output as a sha256 only | same every run |
| Cache TTL | `CLAUDE_CODE_PROMPT_CACHE_TTL=5m`, `CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL=5m`; `FORCE_PROMPT_CACHING_5M` and `ENABLE_PROMPT_CACHING_1H` unset | 5-minute against 1-hour write tokens, for every write record | 1-hour writes under 0.1%, each listed |
| Model per role | the factory frontmatter, with `ANTHROPIC_DEFAULT_SONNET_MODEL` and `ANTHROPIC_DEFAULT_OPUS_MODEL` set to full model ids and `--model <full model id>` on every orchestrator-agent session (the orchestrator has no `model:` line); `CLAUDE_CODE_SUBAGENT_MODEL` unset | `message.model` per assistant record | matches the role, or a logged escalation by the orchestrator (`per-story-delivery.md:139`), counted per arm |
| Permission mode | the permission mode the operator chooses (R4), recorded in the protocol file, as `--permission-mode` in both arms | `permission-mode` records in every main transcript | identical |
| Bash timeout | `BASH_DEFAULT_TIMEOUT_MS=600000` and `BASH_MAX_TIMEOUT_MS=600000`, as headroom; `CLAUDE_CODE_AUTO_BACKGROUND_TIMEOUT_MS` unset, since it caps a main agent's Bash call when set | the env check | identical |
| Subagent caps | depth 3, concurrency 20, set explicitly | the cap messages; peak overlapping subagents per wave | no cap message |
| Forks and teams | `CLAUDE_CODE_FORK_SUBAGENT=0`; agent teams off | subagent `agentType`; teammate records | none |
| Branch seat agent | `--agent vsdd-orchestrator` | the shim's launch log | on every branch seat |
| Env delivery | the same values in arm A's `runtime.env` and arm B's `--env` | the probe operator, outside every agent session, runs `ps -E -ww -p <pid> \| tr ' ' '\n' \| grep -E '^(<pinned key names>)='`, so values outside the pinned list never reach a terminal, log or transcript | identical |
| Server gates | not pinnable | gate values by key name at start and end | unchanged across the series |
| Throttling | none possible | rate-limit and overload retries per session, and the seconds spent in them, from transcripts and telemetry | reported per run; speed is also reported net of throttle time |
| Inter-run gap | no session on the account, on any host, makes model requests during a run or the 65 minutes before it, except the run's own sessions and its named supervising session, which is metered (s11) | the first-request cache read of each run | no more than the S14 cold reference |

**Why the TTL is pinned.** On the host used during design, at defaults, main threads write 1-hour cache entries and subagents write 5-minute ones. A branch seat is a main thread; the same work in arm A is a subagent. So at defaults, the harness would decide part of the cache result.

The 5-minute pin penalizes main threads, and arm B has more of them. So the base cell's cache result is a lower bound for arm B under subscription defaults. A defaults cell of one block, one run per arm, with both variables unset and recorded, runs after the base series closes. It is reported beside the finding and never enters the primary.

## 7. Invariants and the entry drill

**7.1 One writer of `factory-artifacts`.** The factory's CAS push takes its lease from its own fresh fetch, so a concurrent write that landed before that fetch is overwritten, and the push exits 0 (`bin/factory-cas-push.sh:92-126`; reproduced in a scratch repository). The lock holder is `git config user.email` (`bin/factory-lock-write.sh:14`), which every seat on one host shares.

So branch seats write under `.factory/` only within their own stories' `.factory/cycles/<cycle>/<story-id>/` paths. Only the orchestrator session edits `STATE.md`, commits in `.factory/`, or runs either script.

**Writers are three classes:** the orchestrator session, a branch seat, and hooks. Hook plugins write shared files through the dispatcher's `write_file` host call (`crates/factory-dispatcher/src/host/write_file.rs`): `precompact-flush` and `stamp-state-timestamp` write `.factory/STATE.md` (`crates/hook-plugins/precompact-flush/src/lib.rs:32`; `crates/hook-plugins/stamp-state-timestamp/src/lib.rs:472`), and `update-wave-state-on-merge` writes `.factory/wave-state.yaml` (`crates/hook-plugins/update-wave-state-on-merge/src/main.rs:9`). The paths hooks write in a run are recorded, not assumed. Each write is attributed by the hook events in each session's stream.

Detection, every run, both arms: a post-run disk and transcript check. An exception is a tool call by a branch seat (Write, Edit or Bash) on `STATE.md`, on `factory-artifacts`, on either script, or on a `.factory/` path outside its own stories' `.factory/cycles/<cycle>/<story-id>/`. Hook writes are not exceptions. In both arms they are recorded per path, session and time. For `STATE.md`, `wave-state.yaml` and `.factory/sidecar-learning.md`, the check compares the final content with each writer's last write and counts every lost update. Lost updates per run are a fidelity record in both arms, never a stop. Pass: zero exceptions, and every orchestrator commit is in `git log factory-artifacts`.

A marvel Policy is not the wall, because projection fails open: "the session launches unprojected rather than being refused" (`internal/session/projection.go:23-24`).

**7.2 The key.** After a tool timeout or error, the shim re-sends the same key. A refusal means the seat exists, so the shim waits. `attempt` rises only at the orchestrator, only when the previous attempt reads `failed`, `crashed` or `unknown` and git shows the story unmerged. It never rises on `pending` or on a failed `await` call. The per-run duplicate check (s2, Recorded per run) confirms that no story ran twice.

**7.3 The wait.** A missing session is unknown, not success. The reaper keeps at most one crashed marker per role (`internal/session/manager.go:1605-1609`), so another seat's crash can clear it. On every state, succeeded included, the orchestrator verifies in git; only git marks a story done.

**7.4 Entry drill.** A structural gate: every case passes on the probe host before the fidelity run. Drill runs are never counted, and their spend goes in the budget.

| case | action | pass |
|---|---|---|
| D1, kill | kill a branch seat's harness process mid-step | terminal non-success, with the observed latency recorded; the redispatch covers only unmerged stories; one artifact set |
| D2, timeout | with two keys pending at once, set the shim's call bound below the seats' run time | every key reads `pending`; the retry is refused and joins the wait; one session per key; no `attempt` rises; one artifact set |
| D3, writes | against a scratch remote, never the test project's `factory-artifacts`: a branch seat attempts a `STATE.md` edit, a commit in `.factory/`, a CAS push and an edit outside its story paths | all four detected by 7.1 |
| D4, cleared marker | crash two seats of role `branch-run` in turn | the first waiter reports `unknown`, then verifies in git; it never reports success |
| D5, controller | three branch seats live across two reconcile ticks | zero `session.deleted` events on them, and no controller-spawned seat in `branch-run` |

## 8. Protocol file

Committed beside this brief before the pilot. Every run's marker records its sha. An amended protocol starts a new series.

**8.1 Contents:**
1. The question and its four closes.
2. The pins:
   - commit `51023185`;
   - the pack hash and the patch hash;
   - the harness build and its absolute path;
   - the model id per role;
   - the TTL settings;
   - the account;
   - the probe host;
   - the marvel commit and the tmux version;
   - the analysis seed and resample count.
3. The test project sha, the seed snapshot sha, and the acceptance suite's hash. The suite itself is published after the series closes.
4. The accepted story (s9).
5. The primary metric and its smallest effect of interest (the smallest gap the operator would act on). Each secondary gets its own smallest effect.
6. Sections 8.2 to 8.6.
7. Exclusions: none. A canary hit counts the run as zero accepted. A sensitivity analysis without that run sits beside the primary. Its cost stays in.

**8.2 Pilot and ceiling.**
- **The pilot:** three arm A runs from the seed. It sets the noise floor and the intraclass correlation (ICC), meaning how alike scenario results are within one run, reported with its interval; the ICC informs the pilot card, never the ceiling. The pilot is not part of the comparison.
- **The ceiling:** seven blocks after the pilot, one run per arm in each block.
- **One interim look,** after the pilot, on arm A alone.
- **The computed n is reported, never adopted;** the ceiling does not move.

**8.3 Pairing and order.**
- The two runs in a block run back to back, with only s6's gap between them, on one account, host and harness build.
- A block starts only with quota headroom for two runs, from the pilot's measured quota delta. A limit stop during a run is an outcome: the run counts at its cost and its accepted stories, and limit stops per arm are a reported secondary. A block is void only when the limit is reached before its first run starts.
- Order inside a block follows a sequence written into the protocol (AB, BA, repeating).
- Each block falls inside one weekly quota window.

**8.4 Unit of analysis: the run.** Scenarios cluster within a run, so they reduce to one pass rate per run.

**8.5 Intent-to-treat and stop rules.**
- **Intent-to-treat:** every started series run counts, stalls, crashes, timeouts and limit stops included, at its cost. The fidelity run and the drill sit outside the statistics.
- **Fidelity stop:** the node's fidelity checklist fails. Close (a).
- **Blind stop:** 0.86 times the pilot's spread of the log primary exceeds ln(1 + the smallest effect), for example ln 1.3 = 0.262 at 30%. 0.86 is 1.31 (t at 6 degrees of freedom, 2.447, times the square root of 2 over the square root of 7) times 0.659, the lower 80% bound of a spread from 2 degrees of freedom (the square root of 2 over 4.605), so the stop fires only when even a generous reading is blind. Close (b). If any pilot run has zero accepted, (b) does not fire: the card reports the spread of log cost per run, names the run, and the operator decides. A secondary that is blind on its own is reported, not closed.
- **Drift stop:** the harness build, the pack hash, the marvel commit, or a recorded server gate changes inside a series. The series is void from that block. Prices come only from the frozen table.
- **Budget stop:** at a block boundary, spend reaches section 11's ceiling. The series pauses, and an operator card carrying spend only goes up. If the operator ends the series, the report states the count reached and its power, and it is not close (d).
- **Ceiling reached:** close (c) or (d).

**8.6 Wall clock,** in both arms: from the first send to verified completion of the final state commit.

## 9. Judge and metrics

**9.1 The probe acceptance suite.**
- **What it is:** executable black-box tests on the test project's public interface, run by the probe operator. It is not an LLM. The factory's holdout-evaluator is an LLM role inside each arm (`agents/holdout-evaluator.md`, `model: opus`).
- **Size:** at least 30 scenarios, three or more per story, each tagged with one story id.
- **Authorship:** a seat that runs neither arm writes it, separate from the factory's own holdouts, against the brief at M2. After the seed (M4), the author receives only the seed's story ids, titles and wave assignments, never story bodies or acceptance criteria, and adds a story tag to each scenario (M4b). The M2 to M4b diff must touch tags only, and is published with the finding. The suite is stored outside every arm's worktree, and no arm run, the seed run included, has a read path to it.
- **Controls before the freeze:**
  - the Phase 2 seed snapshot fails every scenario, or, if S8 failed, the test project's start commit does;
  - the author's reference solution passes every one;
  - a scenario that varies across three runs on the reference is fixed or removed.
- **When it runs:** after verified completion, on a clean checkout of the run's final commit.

**9.2 The canary.**
- **The token:** a random 32-hex token, placed in a comment in every suite file.
- **The search:** after each run, every transcript of every session, and the git tree.
- **The detector check, in the smoke runs of both arms and the fidelity run:** a second token placed in a file the arm must read; the search must find it.
- **On a hit:** the run counts as zero accepted.

**9.3 Metrics.**
- **Primary** (this brief's recommendation, ruled as R2 before M1 freezes the protocol): API-equivalent cost per accepted story (per accepted feature when S8 fails, s12), as a ratio of sums over the series (total cost over total accepted stories), so a run with zero accepted still counts. Cost covers every request class, classifier and compaction requests included (s11).
- **Secondaries, each with its interval and the n that would resolve it:**
  - wall clock (s8.6), and wall clock net of throttle time;
  - throughput: accepted stories per hour of wall clock;
  - cache: read and write tokens by TTL class per accepted story, with read share beside them;
  - scenario pass rate per run;
  - adversary passes per story, which exposes a cost gap bought with less review;
  - compactions per run, and steps re-run or skipped after one;
  - limit stops per arm;
  - every axis per phase and per wave width.

**9.4 Analysis.**
- **The estimate:** arm B over arm A, cost per accepted story. The interval: the delta method on the log of the ratio of sums, blocked by pair, with the bootstrap over pairs as a sensitivity (at seven pairs, a percentile bootstrap has only 1,716 distinct resamples and under-covers). If both arms' totals are zero, no comparison exists: the finding reports the counts and closes (a), not comparable on this test project. If one arm's total is zero, no ratio is computed; the finding reports per-arm cost and accepted counts and closes (c) against that arm.
- **Sensitivities beside the primary:**
  - the primary with the hub wait row (s10, output 8) removed from both arms. A gap that vanishes without it is a finding about the wait design, not about separate sessions;
  - the primary on first-attempt acceptance only.
- **The verdict:**
  - (b) per s8.5's blind stop, decided at the pilot;
  - (c) when the interval excludes 1; reported as past the smallest effect when its near end is, and otherwise as a gap below the smallest effect;
  - (d) when it includes 1, stating the n that would resolve it. (d) is never written for a series in which any run's widest wave is under 2, because arm B changes nothing in such a run; that series closes (a), with the widths listed.
- **A claim on a secondary** is Holm-adjusted, meaning corrected for testing several axes.
- **Recorded dissent:** two of the twelve design seats recommended a verdict per axis rather than one primary. Every axis is reported with its interval and the n that would resolve it, so that reading stays available.

## 10. Cache ledger

A reader that turns each run's transcripts into per-request cache records, then sums them. It and the cost meter (s11) are built for this probe. The critic run registry (aae-orc-hp96) can ingest these runs later, and is not a dependency. It reads the same files in both arms, reports tokens only, and leaves pricing to s11.

**Inputs.** For every session in the run, from the harness transcript directory:
- the main transcript;
- each subagent transcript and its `.meta.json` (`agentType`).

Arm B's branch seats join to their marvel sessions by key. The supervising session is read the same way, and reported as its own row.

From each assistant record:
- `message.id`, `requestId`, `isSidechain`, `timestamp`, `version`, `message.model`;
- `usage`: `input_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`, the 5-minute and 1-hour creation split, and `output_tokens`.

**Dedupe.** One record per `message.id`. Streaming repeats a message, and on the host used during design the copies carried identical usage. The fixture check (Validation) confirms it on the probe host.

**Outputs, per run, per seat, per role and per phase:**
1. Tokens by class.
2. The TTL as applied, against s6.
3. The first-request cache read per spawn or seat, reported as a fraction of the S14 sharing ceiling. This measures sibling prefix reuse, which arm B may lose across sessions.
4. Cold starts.
5. Cache deaths.
6. Concurrent cold writes, meaning a same-role spawn whose first request starts before a sibling's first response. Per the prompt-caching docs, an entry exists only once the first response begins.
7. Undetermined rows, counted.
8. Hub wait cost: the orchestrator seat's requests, writes and reads between the first dispatch and the last terminal state of each wave, and its turns whose only tool call is `await`, as their own row in both arms.

**Cold start:** write above zero, read zero, no error. A row with both cache classes at zero is undetermined, never cold, because an authentication failure produces the same row.

**Cache death:** a request whose gap exceeds the prior write's TTL and whose write covers at least half the previous request's prompt.

**Validation.**
- Before the pilot: a fixture transcript with hand-counted totals reads back exactly.
- Per run: totals reconcile with OTEL `api_request` events per `query_source` within 1% per class. A source present in OTEL but absent from the transcripts is reported as its own row.

## 11. Cost meter and budget

**The record.** Tokens by class, per request, session and role:
- uncached input;
- 5-minute write;
- 1-hour write;
- read;
- output.

The record comes from transcripts, cross-checked against the headless result line (`internal/runtime/claudecode/parser.go:402-430`) at 1% per class. Cost covers every `query_source` in the OTEL `api_request` events: transcripts attribute tokens to seat and role, and OTEL-only sources (the classifier, for one) are priced from OTEL, per class, as their own row in every cost table. Dollars and quota are derived views.

**Every session that acts on a run is metered:** the orchestrator, each branch seat, and any supervising session. A supervising session off the pinned harness is reported as unmeasured, never as zero.

**marvel's `max_tokens` is a runaway guard, not the meter.** It sums prompt and output tokens without price weights (`internal/daemon/admission.go:36-39`), and `max_cost_usd` is not implemented (`internal/api/budget.go:68-70`).

**Price table, frozen before the pilot,** from platform.claude.com/docs/en/about-claude/pricing, read 2026-10-14. Per million tokens:

| model | input | 5m write | 1h write | read | output |
|---|---|---|---|---|---|
| Opus 5.5 | $4 | $5 | $8 | $0.20 | $20 |
| Sonnet 5.5 | $2 | $2.50 | $4 | $0.10 | $10 |

The smoke run reads which full model ids the factory's `sonnet` and `opus` aliases resolve to.

**Quota.** The weekly percent is read from an idle interactive seat on the same account, before and after each run, through marvel's account-window reading of the statusline (`cmd/marvel/accountlimits.go`); a stale reading is marked stale. It is attributed only when no other session used the account during the run.

**Budget (inferred; the pilot replaces it).** About 385M read, 14.6M 5-minute write and 1M output tokens per full run gives $85 (all Sonnet 5.5) to $170 (all Opus 5.5). The seed lowers series runs, which start at Phase 3.

**Ceiling:** 3 pilot and 14 series runs, plus the seed, fidelity, reference and drill runs, about 21 full-run equivalents; 3 spares for voided blocks and seed revisions; and the defaults block's 2 runs at the 1-hour write rate. That is about 26 full-run equivalents: $2,254 (all Sonnet 5.5) to $4,508 (all Opus 5.5), from 24 runs at $85 and $170 plus the defaults block's 2 runs at $107 and $214, an upper bound since series runs start at Phase 3. Each voided block or seed revision draws on the spares, and the pilot card reports the spares left.

Before the pilot, the pilot ceiling is the smoke runs, arm B's live tests and entry drill, the seed and 3 runs, priced at the all-Opus figure ($680 for the seed and 3 runs), and re-priced once the smoke, live-test and drill counts are in the protocol file. The ceiling is re-stated before the first block from the pilot's median for arm A and the fidelity run for arm B, with cache deaths per run (s10, output 5) shown for each. Reaching it pauses the series for an operator card (s8.5).

## 12. Test project

**What it is:** a product brief (greenfield's input) plus the acceptance suite, both frozen at one sha before the pilot.

**Pins:**
- a `feature_type` and a single-service topology, so the fired approval steps are known (s2);
- the brief's scope.

**Where the arms differ.** Only greenfield's one parallel node, in Phase 3 (`greenfield.lobster:627-1002`). Every axis is recorded per phase. Phases 4 to 7 are controls: a gap there is a fidelity defect. Phases 1 and 2 run once, in the seed.

**The seed.** Wave width is an output of Phase 2, so a brief can only invite it. One arm A run goes through Phase 2 and stops at the Phase 3 entry: its answer file answers phase-2-human-approval (`greenfield.lobster:538`) with "approve, then stop", and the snapshot is taken after phase-2-state-backup (`:530`). Its repository, `.factory/` state and wave schedule are snapshotted at a sha. Every pilot and series run in both arms starts from that seed (smoke S8). If the seed misses a requirement below, the brief is revised (back to M2) and the seed re-run, and every discarded seed is recorded with its widths. If S8 fails, each run starts from the brief, the width each run got is reported as a covariate, the suite maps to the brief's features, and the primary becomes cost per accepted feature. A run on this path whose widest wave is under 2 is reported, and bars a (d) close (s9.4).

**The seed's wave schedule must have:**
- at least two waves;
- one wave of width 3 or more, and one of width 2;
- a later-wave story that `depends_on` an earlier-wave story, so the dependency merge check and the merge to `develop` (`greenfield.lobster:770`, `:779`) run across waves;
- a Phase 3 long enough for idle branch gaps past 5 minutes (inferred; the ledger confirms it).

**Authorship:** a seat outside both arms writes the brief and the suite, before the pilot, and never reads an arm's transcripts or story files (s9.1).

**What a close may claim:** every close claims only the seed snapshot sha and its widths. A claim past one seed needs a second seed.

## 13. Out of scope for this probe

- one session per agent rather than per branch;
- structural walls, such as reviewers as their own seats;
- a marvel workflow primitive;
- per-role model choice;
- dark-factory;
- the rc.22 to rc.24 comparison (aae-orc-17ki), which can reuse this rig, meter and protocol, and measures its own noise floor.
