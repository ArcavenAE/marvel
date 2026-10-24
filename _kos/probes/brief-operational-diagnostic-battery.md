# Probe brief: which of marvel's designed behaviors can already be checked on a running cluster, and what a battery would add

**Status:** DRAFT 2026-10-24, not run. Read at marvel `origin/main` @ `9672ca5`
and director `origin/main` @ `4dc2d0a`; bd state read the same day.
**Source idea:** `_kos/ideas/marvel-operational-diagnostic-battery.md` (filed
2026-09-27, shape RULED the same day).
**Sibling:** `director/_kos/probes/brief-operational-diagnostic-battery.yaml`
(the same ask with director as the subject: reach, presence, replay, grants,
the launcher). One ask, two subjects, filed once in each graph.
**Question asked (operator, 2026-09-27):** "we should have operational tests,
diagnostics, that verify designed, intended functionality is working, because
the design is complex, and instances wont know what is intended vs what is
working vs what is broken".
**Medium:** reading this repo; running existing tests and commands under the
relay run conditions against a scratch daemon with the simulator runtime. No
model calls, no harness logins, nothing written under the real `~/.marvel`, no
command against a live daemon other than read-only ones.
**bd:** aae-orc-kdin1 (director battery 1: the runner, gate 0, the
scratch-daemon helper; open, unassigned), aae-orc-x6bky (gate A, which holds
marvel's spawn-environment case A1), aae-orc-31mlk (seats inherit the daemon's
messaging credential, open), aae-orc-kjix3 (bus write probe and store limits,
in_progress).

## 1. What is already built

The idea's six areas, each against what `main` carries today. "Test" means a
Go test that runs at build time; "command" means something an instance can
call on a running cluster.

| Area (idea anchor) | Test on `main` | Command on a running cluster |
|---|---|---|
| Spawn environment (question-permission-model, marvel#351) | managed children: `TestStartChildEnvIsAllowlistPlusMinted`, `TestStartLongRunningChildEnvFromKernel` [read] `internal/workload/process_test.go:33,79`; seats: the env is a denylist scrub, not an allowlist [read] `internal/tmux/driver.go:198`, `internal/api/seat_env.go:69` | none found |
| Restart and crash-loop backoff (question-healthchecks) | `TestRestartMatchesTheRoleSchedule` [read] `internal/backoff/backoff_test.go:9`; `TestSupervisorRestartsCrashedBrokerUnderBackoff` [read] `internal/bus/supervisor_test.go:238` | restart count and backoff appear in bus status [read] `internal/bus/supervisor_lifecycle_test.go:117,170` |
| Shifts (question-shifts) | one test file [measured] `git grep -c '^func Test'` on `internal/daemon/shift_timeout_test.go` | none found |
| Inject submit (finding-040 addendum, marvel#357) | `internal/daemon/inject_verify_test.go` | `marvel inject --verify` reads the composer afterwards and exits nonzero if the effect was not seen [read] `cmd/marvel/main.go:1954` |
| Reexec (elem-staged-activation-upgrades) | `TestACarriedLeafSeedSurvivesAReexecWithTheBrokerUntouched` and refusal tests [read] `internal/daemon/reexec_handoff_e2e_test.go:94`, `reexec_target_test.go:104` | none found beyond `marvel daemon reexec` itself |
| Bus auth regeneration (services-list.md plan row 3) | `TestManagerRegenerateKeepsPasswordsDropsRemovedAndReloadsOnChange` [read] `internal/bus/render_test.go:233`; `TestSupervisorUserPasswordSurvivesARestartAndLeavesTheTeamUserAlone` [read] `internal/bus/role_users_test.go:205` | none for the negative (another team's credential refused) |

Callable diagnostics that exist and are not in the idea's list:

- `marvel keys doctor`: permission audit of `~/.marvel`, optional `--fix`
  [read] `cmd/marvel/main.go:2506`. On `main` since `6d804be`, 2026-04-16
  [measured] `git log -S`.
- `marvel backend verify`: names each session's backend and every layer that
  disagrees; exits nonzero on a problem "so a shakedown can gate on it"
  [read] `cmd/marvel/backend.go:26,51`.
- `marvel bus status [--require-leaf]` [read] `cmd/marvel/main.go:2787`.
- `marvel status`: running or suspended, answers while frozen [read]
  `cmd/marvel/status.go:25`.
- The daemon's own bus probes, which run on a live cluster and only report:
  structural health [read] `internal/bus/health.go:16`, the write probe
  [read] `internal/bus/writeprobe.go:14`, and store headroom [read]
  `internal/bus/headroom.go:14`.

The director-side U3 design already places four marvel behaviors in its
gates: A1 (seat environment under a scratch daemon), A2 (heartbeat token
refusal), C3 (a duplicate team apply is refused) and D1 (store limits) [read]
director `sim/design/ops-diagnostics-battery.md:83-86,125,134`.

## 2. Hypothesis

Most of marvel's designed behaviors already have a positive and often a
negative case, but only as build-time tests. The gap the operator named is
that none of them can be run against the cluster an instance is actually on,
so "the tests passed at that commit" and "this cluster behaves as designed"
stay different claims. The callable commands that exist (`keys doctor`,
`backend verify`, `inject --verify`, `bus status`) are single checks without a
registered negative case. Shifts and seat-environment have no callable check.
[inferred] from the table above.

Two premises in the idea need correcting before a battery cites them:

- "a managed child gets its allowlist plus minted secrets and nothing
  inherited" holds for `workload.Process` children. Seat panes get the daemon
  environment minus a denylist [read] `seat_env.go:67-79`, and aae-orc-31mlk
  is open on exactly that. A seat check written to the idea's sentence would
  FAIL by design, not by defect.
- "The installed binary predates the command" (`keys doctor`): the command has
  been on `main` since 2026-04-16 [measured], and no `marvel` is on this
  host's PATH [measured] `command -v marvel` (empty). The claim may hold on
  another host; it cannot be checked here.

**What would refute it:** a callable command on `main` that runs a positive
and a negative case for any of the six areas against a live or scratch daemon
(for example a test-mode flag on one of the existing commands), or evidence
that the build-time tests are already run against the installed binary on a
fleet host.

## 3. The probe

Run by a marvel builder or research seat. All four relay run conditions
(operator ruling, 2026-10-14): scratch `HOME` and a short
`TMPDIR`, no `MARVEL_SOCKET`, a localhost-only sandbox with a
refused-connection control, and the sandbox write proof. Cleanup by pid file
inside the scratch root only, with the `ps -p` check before each kill, as in
`brief-multiple-clusters-per-host.md` section 3.

- **P1, map.** Complete the table in section 1 for every behavior the six
  anchors name, with one more column: which of the existing tests could run
  against a scratch daemon built from the installed release rather than from
  source.
- **P2, callable commands as pairs.** Against a scratch daemon with a
  one-role simulator team, run `keys doctor` on a clean scratch layout
  (positive) and on one with a key file at the wrong mode (negative);
  `inject --verify` on an idle composer (positive) and on a composer holding
  staged text (negative: nothing concatenates). Record exit codes and output
  byte for byte.
- **P3, the seat canary.** Set a canary name in the scratch daemon's
  environment, spawn a simulator seat, and read the pane environment by name
  only. Record whether the canary is present. This measures the denylist
  premise; it is expected to show the canary present unless it is on the
  denylist.
- **P4, NOT-APPLICABLE honesty.** Run `bus status` on a scratch cluster with
  no managed bus and record what it says: an explicit "not managed" or an
  error, versus anything that reads as healthy.

Restart, reexec and shift checks are not run in this probe: each disturbs
running agents, and the idea itself puts them on a scratch daemon on its own
socket (question-daemon-isolation-boundary). P1 records them; a later probe
runs them.

## 4. Pass and fail

- **Supported:** P1 shows no callable command with a registered negative case
  for at least four of the six areas, and P2 shows the existing commands can
  be driven as pairs from outside.
- **Falsified:** P1 finds a callable pair for most areas, or P2 shows the
  existing commands cannot report a negative case distinctly from an error.
- **Void:** the sandbox blocks a step for a reason unrelated to its subject
  (the setuid `/bin/ps` refusal measured in finding-marvel-7i6o is one known
  case); record it as INSTRUMENT.

## 5. Open questions (not settled by this brief)

- Home. U3 puts the runner in the orchestrator repository (`tools/diag/`) and lets a component
  ship its own check files later. Whether marvel's checks ship as a `marvel`
  subcommand, as files under this repo, or only as check files in the orchestrator repository is not
  ruled.
- Who may call what. The idea says reading results and running a disruptive
  check are different permissions; which permission, and who holds it, is
  open.
- Whether the build-time tests in section 1 count as the battery's checks for
  a given installed release, or whether a check must run against the
  installed binary.
- Whether `backend verify`'s nonzero exit for a shakedown is a gate under
  ADR-007, which needs a ratified definition, or stays diagnostic.

## 6. What this brief does not cover

Director's areas (the sibling brief). Building any runner or check
(aae-orc-kdin1 and its siblings). Fixing the seat environment (aae-orc-31mlk).
Any CI or hook wiring. Any harness login or model turn; the "Login expired"
evidence from the idea is a harness-health question that
`_kos/ideas/tmux-harness-state-watchdog.md` and
`_kos/ideas/health-signal-taxonomy.md` already hold.
