# Open-issue triage, 2026-09-26

A pass over the 40 open marvel issues (the Renovate dashboard, #191, is excluded), so the queue says what is still true. Fifteen issues look already fixed on `main`, one duplicates another, and several open issues share a root cause that one change would cover. Nothing here closes or comments on an issue; each close and each comment is a separate operator decision.

- **Code read:** `main` at 222478a (#359).
- **Method:** for each issue, check each load-bearing claim with one command (grep the path, run the named test, read the default); check `main` and merged PRs for a fix; check the graph, ADRs and the 2026-08-01 rulings for a prior decision; check the proposed fix against SOUL.md and marvel's constraints (zero CGo, reexec under running agents, the adapter contract, manifest compatibility).
- **What ran:** targeted Go tests on private tmux servers (the inject `SendKeys` tests with `-race`, #350's regression tests both pre- and post-fix, the toolchain pin tests). No marvel binary, daemon or live session was used.

## Summary

| Verdict | Count |
|---|---|
| Already fixed (or superseded) | 15 |
| Valid | 16 |
| Valid but overstated | 7 |
| Premise no longer holds | 1 |
| Duplicate | 1 |

## Verdicts and plans

### Inject, tmux and terminal delivery

The inject path changed form four times between 2026-09-21 and 2026-09-25; the current form (#357) pastes through a tmux buffer with bracketed paste, then sends Enter. Most issues here were filed against an earlier form.

| Issue | Verdict | Severity | Plan |
|---|---|---|---|
| #202 inject -e does not submit to a TUI | already fixed (#357) | n/a | Close after one live Claude Code session on a build with #357 submits. |
| #341 bare Enter can cancel a pending action | already fixed (#342) | n/a | Close. A default-behaviour change can be its own issue if wanted. |
| #343 inject truncates the head of a long payload | duplicate of #317 | n/a | Close as duplicate; carry the requested-vs-delivered byte logging into #317. |
| #317 single send-keys can truncate the head | valid but overstated | medium | Re-measure on a #357 build (payloads with head and tail markers at about 1.2, 1.7 and 4.7 KB, read back from the transcript). A scratch probe suggests the loss happens above tmux. |
| #340 inject records no injector; appends to a stale draft | valid, largely addressed | medium | Delivery acknowledgement per the #355 ruling covers provenance. Injector identity belongs with the principal model (M1) and the message envelope rather than a socket attribute. |
| #337 capture/inject/describe reject the name `get sessions` prints | valid | low | Accept the printed name, or print the accepted key. |
| #324 `TestSendKeysConcurrentInjectNoInterleave` flaky on Linux | valid but overstated | low | The likely cause is terminal echo racing the reader in the test, not delivery. Rework the test; do not return to a CR folded into the paste, which reintroduces #202. |
| #203 run-01 papercuts | valid, in part | low | Split the remaining items (pane workdir, readiness for blocked TUIs, parse-noise severity) into their own issues. |
| #92 stop --teardown leaves an orphaned tmux session | premise no longer holds | n/a | Not reproduced on current `main`; the contract half is covered by #157. Close unless it recurs. A sweep by session name would conflict with daemon isolation. |

### Lifecycle: shift, scale, plan, health

| Issue | Verdict | Severity | Plan |
|---|---|---|---|
| #348 role shift on a 1-replica role deletes the new seat | already fixed (#350) | n/a | Close after the running daemon is on a build with #350. |
| #345 selective --role shift strands the previous generation | already fixed (#350) | n/a | Same check as #348; the message wording could be a small follow-up. |
| #335 replicas: 0 refused by the parser, accepted by scale | valid | medium | One shared validator for parser and `scale`. `scale --replicas -1` appears to reach an unguarded negative index in the scale-down path; worth a test in the same change. |
| #334 plan zero-match filter reports false state, exits 0 | valid | medium | Let `plan` take a manifest path and diff it against the store; fail on a filter that matches nothing. This also answers #335's drift half. |
| #225 plan does not show a convergence hold | valid | low | Small rendering change. |
| #201 completed headless one-shot reaped as crashed | already fixed (#244, #246, ADR-010) | n/a | Close. |
| #186 nothing reports an agent orphaned by a previous daemon | already fixed (#192) | n/a | Reporting shipped; killing orphans was ruled out on the issue. Close. |
| #276 detect and remediate frozen sessions | valid | medium | Detection and classification can be automatic. Remediation should stay confirm-or-override (ADR-007), and anything that re-mints credentials falls under ADR-009. Consider consolidating with the watchdog idea. |
| #34 HEALTH and CTX% should reflect real state | already fixed | n/a | Columns are fed for claude and codex. Close; "liveness is not progress" is #276. |
| #37 MaxRestarts saturation test | already fixed | n/a | Blocker #28 closed; tests cover saturation and clearing. Close. |
| #38 shift rolling-replace test | valid but overstated | n/a | Surge behaviour is intended and documented; the edge case has a regression test. Close. |
| #41 describe team surfaces RoleHealth | superseded | n/a | `plan` HOLD and `get sessions` show it. Close, or retitle to the `describe team` rendering gap. |

### Bus, credentials, keys, policy, env

| Issue | Verdict | Severity | Plan |
|---|---|---|---|
| #344 credential put under an unknown name no-ops | valid | medium | Warn at put time when the name has no consumer, and name `bus/leaf` in the put help. A hard rejection may shut out later consumers of the store. |
| #339 upgrade --daemon drops the bus leaf | valid but overstated | medium | Dropping the seed is by design (credentials are transient) and the daemon logs it; the CLI output is what stays silent. Surface the drop in `upgrade --daemon` and `daemon reexec`. Preserving the seed across exec would reverse a design decision and is an operator call. #307's reexec-adoption test is the regression test. |
| #319 same team name in two workspaces | valid | medium-high | Fail the apply on the collision first. Broker passwords appear to be keyed by team name, which the loud failure would also close. Then the cross-workspace model, keeping existing manifests valid. |
| #312 R-94 advisory inside a team | valid | low now | Per-role broker users; the interim narrowing described in the issue is not on `main` yet. |
| #313 re-projection does not apply a changed permissions block live | valid | medium-high | Correct the live-reload claim in the example and the two code comments that repeat it, and make the event say the change applies at next spawn. Auto-shift on a permissions change only as an opt-in. |
| #307 leaf connect/disconnect hardening | valid | low | Transition lock, reset `leafUp` on toggle, treat unreadable (not missing) state as an error. |
| #272 keys authorize cannot change a key's scope | valid but overstated | low | An in-place scope update behind a flag, with a log line. These are local file edits, so the access window in the issue is narrower than described. |
| #311 Role/Runtime has no env field | already fixed (#315) | n/a | Close, or narrow to one residual: codex's director MCP child receives only marvel's constructed env names, not role-declared env. |
| #308 codex should seed an allowlisted config.toml | already fixed (#359) | n/a | Close after one codex session confirms it after install and reexec. |
| #289 bus tests overflow the monitoring port on macOS | already fixed (#299, #304) | n/a | Close. |
| #278 bus.hub has no ca_file | already fixed (#285) | n/a | Close, or narrow to the reload and URL-scheme question. |

### Tooling, docs, CI, platform

| Issue | Verdict | Severity | Plan |
|---|---|---|---|
| #333 PRs based off main get no Quality Gate or CodeQL | valid but overstated | low | The cited stack was gated at its final PR into `main`. The wider point: `main` has no required status checks, so the gate is advisory everywhere. Drop the `branches` filter on `pull_request`; whether to require the gate is an owner decision. |
| #329 toml/yaml example twins declare different teams | valid | low | Add a twin-equivalence test. The finding cited is marvel's finding-044. The forestage pair could be retired or relabelled as reference. |
| #326 "accepts a prompt on stdin" is false for every adapter | valid | low | README fixed in #325. Remaining homes: CLAUDE.md and the `elem-console-agnostic` node (title and content), then re-render the charter. |
| #314 can macOS reap a live agent's files under $TMPDIR | valid (answered) | medium on macOS | Yes, likely. macOS removes temp files whose birth and access times are both older than three days, and an open descriptor does not protect them; orphaned policy dirs were observed emptied on schedule. Move durable per-session files to the layout home, keeping reexec and adoption on the path a live agent reads. |
| #143 os.Executable bakes a version-pinned path (Linux) | valid but overstated | low to medium | #239 narrows the window to "prune before the next daemon event." #359 adds a second site (the codex home seed) that re-projection does not appear to refresh. One stable indirection would cover both. |
| #158 should --log-file dedup like the ring | valid (design question) | low | The file is bounded by default rotation, so the cost is losing older history sooner, not unbounded growth. Decide, and keep a first/last timestamp summary if the file dedups. |
| #181 window resolver can return a confident wrong window | already fixed (#211, #219, #224) | n/a | Close, recording the ladder-design residue (the learned rung still outranks the manifest, now with a warning). |
| #119 lint fails without golangci-lint on PATH | already fixed (#157, #226) | n/a | Close; a pin test keeps mise and CI in step. |

## Across issues

- **Duplicate:** #343 of #317.
- **One fix, several issues:**
  - #345 and #348 were one generation-bookkeeping defect (#350).
  - #334 and #335 both want `plan` to read a manifest.
  - #317, #343 and #340 part 2 share one re-measure on a current build.
  - Delivery acknowledgement (#355) covers the verification asks in #317, #340, #343 and #203.
- **Success that isn't:** #313, #319, #334, #339, #344 and #333 each report success while nothing changed. #181's repair (refuse, and render the value as absent rather than guess) is the pattern to follow.
- **Paths set at spawn and invalidated later:** #314 and #143 both bake a path into a live agent at spawn, and an outside event (a temp reaper, a package prune) later removes it. A marvel-owned location answers both.
- **Grant unit:** #312 and #319 both come from one broker principal per team name. Design the per-role and cross-workspace changes together.

## Suggested close list (18)

#202, #341, #343, #92, #348, #345, #201, #186, #34, #37, #38, #41, #308, #289, #311, #278, #119, #181. Several have a one-step check first (a live session on a build that includes the fix), noted in the tables above.
