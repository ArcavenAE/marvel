# vsdd-factory as a marvel team: protocol file

The protocol file of brief section 8 (`probe-vsdd-factory-as-marvel-team.md`), step M1. It fixes the question, the pins, the unit, the axes and their smallest effects, and the run rules before the pilot.

Freezing: this file lands with FILL fields, values that can only be read on the probe host before the first smoke run ("step 0") or at M4. The commit that fills them is the frozen sha. Every run's marker records that sha, and any later change to this file starts a new series.

What the freeze does not wait on, and what the pilot does: T2 (aae-orc-ou94x, the meter on live transcripts) and T6 (aae-orc-sbp9j, arm B's live tests and entry drill) are still open, and both wait on live runs. The freeze does not wait on them. The pilot does: it starts only after both close.

Rulings this file applies: R1 to R4 as the brief records them; the probe host (corporate) and the Enterprise account, both ruled by the operator; the smallest effects in section 5, ruled by the operator on 2026-11-20; the run-start check and the pinned tool surface in section 2, ruled by director on 2026-11-20. This file decides none of them.

## 1. The question and its four closes

Does running each parallel branch of vsdd-factory rc.25 as its own headless marvel session (arm B) change cost, speed or quality per accepted story, compared with the same release running its branches as subagents of one Claude Code session (arm A)? The contrast is separate sessions, not parallelism.

The four closes:

- (a) not comparable;
- (b) instrument blind;
- (c) a comparison with stated power;
- (d) not distinguishable at the ceiling.

## 2. Pins

| pin | value | source |
|---|---|---|
| upstream commit | `51023185658350afaacaa931a175103d915d14ba` (rc.25) | brief s2, `install.meta.yaml` |
| rc.25 tarball sha256 | `6d38a905222bcf9de3b0fe74acd4fc29fbaca6d954f18c6ab3db27387831cee1` | brief s2; `vsdd-probe-t6/build-pack.sh` TARBALL_SHA |
| rc.25 file manifest sha256 | `e0ec70a15ebf74f409ef0b468b59bc400804157224383a62f526c8421c94325c` | brief s2; build-pack.sh MANIFEST_SHA |
| arm B pack | `1.0.0-rc.25.arm-b.2`, pack file manifest sha256 `ec207f9c7a02874ccee507061576e1934cef231f0bd15ae36953c0851eaef979` | `vsdd-probe-t6/README.md`, Versions |
| patch (`apply_patch.py`) sha256 | `9c372d0c3ba2465ad3b02afedc90355269fa2c523ebecbea1b6c96da7a8b2b6b` | T6 README |
| shim sha256 | `3123d3a458b0189f6afb68bb3533161d37bcf76491394d5dd6ff10f2d65142b2` | T6 README |
| harness build and absolute path | Claude Code 2.1.329, read in a pane of the probe tmux socket on 2026-11-20: `claude --version` prints `2.1.329 (Claude Code)`, and `command -v claude` prints the `claude` on the host's user PATH, a symlink to the 2.1.329 entry of Claude Code's versions directory. `DISABLE_AUTOUPDATER=1` was set in the pane. | step 0 |
| model id per role | `sonnet` resolves to `claude-sonnet-5-5` and `opus` to `claude-opus-5-5`, read from the `model` attribute of the `api_request` events of the step-0 smoke run on 2026-11-20; `ANTHROPIC_DEFAULT_SONNET_MODEL`, `ANTHROPIC_DEFAULT_OPUS_MODEL` and `--model` on every orchestrator-agent session | brief s6, s11 |
| TTL | `CLAUDE_CODE_PROMPT_CACHE_TTL=5m`, `CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL=5m`; `FORCE_PROMPT_CACHING_5M` and `ENABLE_PROMPT_CACHING_1H` unset | brief s6 |
| permission mode | `auto`, as `--permission-mode` in both arms; classifier requests metered from OTEL | R4 |
| account | the Enterprise login (operator, 2026-11-20), the probe host's default login. Recorded by label and by the sha256 of the `claude auth status` lines that name the login method, account and organization, never by a credential. Step 0, 2026-11-20: subscription type `enterprise`, sha256 `5c23875659109759a73ce9ac7bcd13aa29ae9274af3dc3df3b3a4ee0ed49ed6d` of the four `claude auth status` JSON lines `authMethod`, `email`, `orgId` and `orgName`, in that order, each line as printed | brief s6 |
| probe host | corporate | ruling vsdd-c6-host |
| marvel commit and tmux version | the probe daemon reports `marvel 0.1.0-alpha.20261119.082809.d22405c (alpha)`, commit `d22405cb8abb62532b6a031b924374de61966263`; `tmux -L <probe socket> -V` prints `tmux 3.7c` | step 0 |
| analysis | `compare.py` at the marvel sha this file freezes with: alpha 0.05, seed 1, resamples 10000; coverage sims 1000, seed 7, resamples 500, sigma_block 0.2, sigma_run 0.2, rho 0.5 | `vsdd-probe-t7`; values as in its example input |
| price table | `vsdd-probe-t2/prices.json` at the same sha | brief s11 |
| env, both arms | `BASH_DEFAULT_TIMEOUT_MS=600000`, `BASH_MAX_TIMEOUT_MS=600000`, `CLAUDE_CODE_FORK_SUBAGENT=0`, `DISABLE_AUTOUPDATER=1`, `CLAUDE_CODE_ENABLE_TELEMETRY=1`; subagent depth 3 and concurrency 20; `CLAUDE_CODE_SUBAGENT_MODEL` and `CLAUDE_CODE_AUTO_BACKGROUND_TIMEOUT_MS` unset; agent teams off | brief s6 |
| tool surface | every probe session, both arms, runs with `--strict-mcp-config` and an explicit `--mcp-config`. The contents of that config: open with the operator; ruled before the pilot. Each run records the sha256 of the output of `claude mcp list` and of `claude plugin list` | director, 2026-11-20 |
| OTLP collector | `http://127.0.0.1:14318`, OTLP over HTTP with JSON bodies (`OTEL_EXPORTER_OTLP_PROTOCOL=http/json`), a localhost-only listener that writes each POST to a file in scratch; passed by `--env` and `runtime.env`. The step-0 smoke run produced `api_request` events with `query_source` for its main session (`sdk`) and its subagent (`agent:builtin:general-purpose`). | brief S10 |

**Run-start check (director, 2026-11-20).** At every run's start, before its first request, the run record holds the output of `claude --version`, the resolved target of `command -v claude`, and the sha256 of that target. If any of the three differs from its pin, the run is void before its first request. Step 0 recorded no sha256 of the target, so the pilot-start re-read below records it, and every later run compares with that record.

**Pilot-start re-read (director, 2026-11-20).** The step-0 processes stop before the pilot, so at the pilot's start the step-0 live reads are run again on the probe host: the harness build and path, the model ids, the account hash, `marvel version` and `tmux -V` on the probe socket, and the OTLP endpoint. A value that differs from this file voids the run, as in the run-start check. The model ids can only be read from requests, so they are read from the `api_request` events of the first run, and a mismatch voids that run.

## 3. Project, seed and suite

- **Test project:** the T3 freeze, tag `probe-t3-m2-freeze-r2`, commit `f51d2aa7deb8f9849786da4add60ca5e210e9793`. Brief tree `8fdef45ef84362de05ec72ae79fa490de38952fb`; `brief/product-brief.md` sha256 `ce6534bc6c3581ff274d9d501051e391e60242181b9768e169643ef7f437e164`.
- **Acceptance suite:** suite tree `38faa64e8b7f4bb70113ff56e6ada51e2ae7bd81`, 9 files. Suite sha256 `274f9e798f8fed9268a684c66424f53de39ddbfbb12f7ece1c88d7d257b22d2f`, the sha256 of the sorted per-file sha256 listing. Values are from `tools/freeze.sh probe-t3-m2-freeze-r2`, read only on the suite's host, which is not the probe host. The suite is held in a private repository whose only collaborator is arcaven, at the same tag (operator Desk cards vsdd-suite-home and vsdd-suite-repo-create). It is published after the series closes. What keeps s9.1 true is that the probe host cannot read it: the probe host's gh login is not arcaven, checked with `gh api user --jq .login` at step 0 and before every run. After M4b's retag, the suite hash changes and is re-recorded with the tags-only diff.
- **Seed snapshot sha:** FILL at M4, with its wave widths.
- **Canary:** a random 32-hex token in every suite file (s9.2). Recorded only as its sha256, `e53954c4b15c979c9a019882d2a11511349e6c03824643a00b79f53b4f58dc9e`; the token itself is not written here.

## 4. The accepted story

A story whose scenarios in the probe acceptance suite all pass, run by the scoring seat on a host that is not the probe host, against a clean checkout of the run's final commit, after verified completion (s9.1). When S8 fails, the unit is the accepted feature (s12).

## 4a. Scoring, and the canary search

**The scoring seat** scores runs and does nothing else: it runs no arm and writes no finding (s9.1).

- **Where:** a host other than the probe host. The suite never reaches the probe host's disk, and no arm run has a read path to it.
- **What it needs:**
  - the suite's repository (section 3), at tag `probe-t3-m2-freeze-r2` (`f51d2aa7`) and later the M4b retag, read by a login that can see it;
  - each run's final commit, delivered read only as a git bundle or a fetch from the probe host, never as a path from the probe host back to the suite;
  - the run id and the seed's story ids for each run, and nothing from an arm's transcripts or story files.
- **What it produces per run:** the clean-checkout result of every scenario, the run's `accepted` and `accepted_first_attempt` counts, and its scenario pass rate. These are the scoring fields of `compare.py`'s input. The cost fields come from `meter.py` on the probe host, and the finding writer joins the two and runs `compare.py`.
- **What it never does:** run an arm, read an arm's transcripts, write the finding, or move a tag in the suite's repository.

**The canary search (s9.2)** runs on the probe host against the canary's hash, so the token never reaches that host and no file is copied between hosts.

- **The scanner** is `vsdd-probe-t4/canary_scan.py` (marvel#1543). It is given sha256 values, never tokens. It hashes every 32-character window of every hex run of 32 or more characters, lowercased, and reports each match by file or git object and offset, with the windows scanned and the bytes read. No content is printed.
- **What it reads after every run** (smoke, fidelity and series): every transcript of every session, main and subagent; the hook and OTEL logs; the working tree; and every object reachable from every ref of the run's repository, the `factory-artifacts` branch included.
- **The targets:** the canary hash in section 3, and the hash of the s9.2 detector token, a second random 32-hex token placed in a file the arm must read.
- **Reading the exit status:**
  - 0 means no hit and everything was read;
  - 1 means a hit: a canary hit counts the run as zero accepted (section 7);
  - 2 means a usage error, so the scan did not run: it is re-run, and never read as clean;
  - 3 means something could not be read, so the scan proves nothing and is re-run before the next run starts.
- **The positive control:** the detector hash must be found in the smoke runs of both arms and in the fidelity run. If it is not, the scan is broken and the probe stops.
- **What travels:** only the 64-character canary hash goes to the probe host, by bus message, and only a small report comes back. The check is exact, as the brief's canary is: a token embedded in a longer hex run is found, and a token split by a line break or changed by one character is not.

## 5. Axes and their smallest effects

Each axis gets its own verdict, Holm-adjusted (R2, s9.4).

| axis | smallest effect of interest |
|---|---|
| cost per accepted story (ratio of sums; reads the blind stop) | 20%, ruled |
| wall clock, and wall clock net of throttle time | 20%, ruled by the operator 2026-11-20 |
| throughput, accepted stories per hour | 20%, ruled by the operator 2026-11-20 |
| cache read and write tokens by TTL class per accepted story, with read share | 20%, ruled by the operator 2026-11-20 |
| scenario pass rate per run | 10 percentage points, ruled by the operator 2026-11-20 |
| adversary passes per story | 20%, ruled by the operator 2026-11-20 |
| compactions per run, and steps re-run or skipped after one | reported, no verdict proposed (operator 2026-11-20, verbatim "M report the numbers") |
| limit stops per arm | reported, no verdict proposed (operator 2026-11-20, verbatim "M report the numbers") |

Every axis is also reported per phase and per wave width. The cost sensitivities sit beside the cost axis: the hub wait row removed, and first-attempt acceptance only.

## 6. Sections 8.2 to 8.6, as in the brief

- **8.2:** the pilot is 3 arm A runs from the seed, with one interim look on arm A alone. The ceiling is 7 blocks. The computed n is reported, never adopted.
- **8.3:** the two runs of a block run back to back with section 6's gap. Each block needs quota headroom for two runs and falls inside one weekly quota window. Block order: AB, BA, AB, BA, AB, BA, AB.
- **8.4:** the unit of analysis is the run.
- **8.5:** intent-to-treat, with the fidelity, blind, drift and budget stops and the ceiling, as the brief states them.
- **8.6:** wall clock runs from the first send to verified completion of the final state commit.

**Quota on the Enterprise login (the operator's Enterprise-account ruling of 2026-11-20; replaces s8.3's headroom and window lines, and s11's quota line):**

The Enterprise login sends no weekly reading (the measurement recorded in section 2 of marvel#1501, an open design document; the operator's ruling of 2026-11-20 to build that meter is not built). So:

- **Read instead:**
  - the probe's own token counts per class and its API-equivalent spend, per run and cumulative, from `meter.py cost` against the frozen price table;
  - every usage-limit or rate-limit message in the run's transcripts and in OTEL error events, with its time and session;
  - the account's own limit, if the Enterprise meter probe finds one before the series (a 30-day cap is expected), recorded in this file as it is found, by amendment.
- **s8.3 "A block starts only with quota headroom for two runs"** becomes: a block starts only when (a) cumulative spend plus twice the pilot's largest per-run spend stays within the ceiling, and (b) no usage-limit message was seen on the account since the previous run ended.
- **s8.3 "Each block falls inside one weekly quota window"** has no readable window on this login. Its replacement: both runs of a block start on the same day, back to back as s8.3 already requires. Any account window the meter probe later finds replaces this line by amendment, which starts a new series.
- **s11 "Quota"** becomes: "not readable on this account; reported as not readable, never as zero". Dollars stay API-equivalent from tokens.
- **What stops a run:**
  - a usage-limit message that ends a run's session is a limit stop. The run counts at its cost and accepted stories (intent-to-treat), and limit stops per arm stay a reported secondary;
  - a limit message seen before a block's first run voids the block, which draws on the spares;
  - the budget stop is unchanged and is computed from dollars, never from quota;
  - the fidelity, blind and drift stops are unchanged.

**The 65-minute window under Enterprise (replaces the s6 "Inter-run gap" pin for other sessions on the account): what it protects, and what replaces it.**

The brief's rule (s6, "Inter-run gap") reads: "no session on the account, on any host, makes model requests during a run or the 65 minutes before it, except the run's own sessions and its named supervising session". In the brief's own text it does three jobs:

1. **Quota attribution.** s11: the weekly percent "is attributed only when no other session used the account during the run". On Enterprise no reading exists (the quota replacement above), so this job is moot. Cost comes from the probe's own transcripts through `meter.py`, which other sessions cannot touch.
2. **A cold cache at each run's start.** The gap row's read-back is "the first-request cache read of each run", and its pass is "no more than the S14 cold reference". 65 minutes outlasts the 1-hour cache. Two sources could warm a run's prefix:
   - **The probe's previous run.** This is the source that matters, because A then B back to back on the same build and pack would share a warm prefix. It is fully kept, at no cost: the probe operator waits 65 minutes after its own last request before every run. That needs no other team to move.
   - **Other sessions on the account.** A prompt cache matches a prefix, in the order tools, then system, then messages. Fleet seats carry MCP tools and their own system prompts and agents; probe sessions run `--agent vsdd-orchestrator` without those tools. So a fleet session is unlikely to warm a probe prefix. This is inferred, so it is measured, not assumed (the calibration below), and every run's read-back still checks it.
3. **Rate limits shared with other load.** The brief already treats throttling as unpinnable: "rate-limit and overload retries per session, and the seconds spent in them ... reported per run; speed is also reported net of throttle time" (s6, Throttling). Other Enterprise load can add throttle time and noise to wall clock and throughput.

**Replacement (the lighter form), ruled by the operator 2026-11-20, verbatim "could this reliably be achieved more quickly with a model toggle? otherwise light":**

The toggle was answered as not reliable for cache isolation and as changing what the probe measures (protocol author, 2026-11-20), so the lighter form stands (director, 2026-11-20).

- **Kept:** the 65-minute gap after the probe's own last request, before every run.
- **Kept:** the per-run read-back. A run whose first-request cache read exceeds the S14 cold reference is flagged. It is counted in the main analysis and left out of a sensitivity analysis, which is the brief's existing rule for any read-back that disagrees.
- **Calibration, once, before the pilot.** S14 is run with the fleet's Enterprise seats live, on every host, as they normally run. If both seats' first-request reads stay at the cold level, other load does not contaminate the probe's cache, and the lighter form stands. If they read materially warm, the full window is needed, and it goes to the operator with its cost.
- **Covariates per run:**
  - the count of live claude seats on the same Enterprise organization, read from each cluster's `get teams` at the run's start and end. The organization is shared with other teams on corporate, mokuzai and kinu, so the probe shares rate limits with them, which is why throttle time is a covariate and speed is also reported net of it;
  - the run's throttle seconds and retry count.

  Both are reported beside wall clock and throughput.
- **Stop:** unchanged. A limit message during a run is a limit stop (counted, intent-to-treat), and one before a block's first run voids the block.
- **Dropped** (operator 2026-11-20, verbatim "could this reliably be achieved more quickly with a model toggle? otherwise light"): the requirement that no other session on the account makes requests. No team moves for a run. The toggle answer is recorded under the Replacement heading above.

**Effect on validity.**

- **Cost axis,** including the blind stop: no change in bias. Tokens and spend come only from the probe's own transcripts, and cache contamination, the one way other load could lower a run's cost, is measured on every run and handled by the flag and the sensitivity analysis.
- **Wall clock and throughput:** other load adds variance, and could add bias if it differed between arms. The design limits the bias: the two runs of a block run back to back, the order alternates AB, BA, and speed is also reported net of throttle time, with the concurrent seat count beside it. The likely cost is wider intervals on the speed axes, so more of them may read (d), not distinguishable. The cost axis's power is unaffected.
- **What would make it invalid:** the calibration showing warm first-request reads under live load. That is why the calibration comes first, and why its failure sends the full window to the operator instead of being run around.

**The Enterprise login expiry.** The Enterprise login on the probe host expires 2026-11-29T04:07Z (director, 2026-11-20). No run starts unless its expected end (the pilot's longest run time plus margin) is before that time. A re-login is the operator's act, at a block boundary, never mid-block. The fairness read-back for "Auth" hashes the `claude auth status` lines that name the login method, account and organization, and records the expiry line in clear. A re-login that changes only the expiry is then not drift. A change to any hashed line is drift, and the series is void from that block.

## 7. Exclusions

None. A canary hit counts the run as zero accepted, with a sensitivity analysis without that run beside each axis. Its cost stays in.
