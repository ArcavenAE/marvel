# Manifest plan: see what `marvel work` would do before it does it

Design for review. No code lands until this doc is reviewed.

- Author: the arcaven team's architect seat.
- Commission: the operator, via director and the team supervisor (ask
  marvel:dry-run-party), 2026-10-03, verbatim: "plan before spawn: yes, and
  route this observation to arcaven supervisor for exploration, examination
  of the missing feature, routing to architect for a bmad party mode casting
  call with three rounds to develop how marvel plan or marvel work dry run
  should work and what unique features it should have for marvel's domain".
- Gap first established by the builder seat (aae-orc#461, comment
  5968875135).
- Worked out in a three-round party with five seats (distributed systems,
  harness internals, runtime substrate, AI finops, AI security). The record,
  with every seat's replies, the per-round checks and the vote, is in the
  orc's `_bmad-output/party-mode/marvel-dry-run-2026-10-03/`.
- Checked against marvel `origin/main` 03dd483; the cited lines are
  unchanged at 889e49a.

## 1. The problem, verified

| Premise | Where | Result |
|---|---|---|
| `marvel plan` previews only teams the daemon already holds | `cmd/marvel/main.go:998-1035`; the `plan` RPC takes no params (`internal/daemon/daemon.go:991-992`, `handlePlan()`) | true; teams mid-shift are omitted |
| The plan/apply split exists inside the controller | `internal/team/controller.go` around :960-1330 (`RolePlan`, `HoldDetail`, `plan.Delete`); aae-orc-nf0w and aae-orc-nrk1, both closed | true: the seam this design reuses |
| `marvel work` has no preview | `apply` params are `manifest_data` and `workspace_root` only (`daemon.go:1186-1195`); no dry-run, diff, preview, validate or check flag under `cmd/marvel` | true |
| Handing `plan` a manifest misleads | #334 (open): "no teams to plan", exit 0 | true |
| The workaround is itself an apply | load at `replicas: 0`, then plan: it stores the manifest, and the plan shows desired 0 and spawn 0 | true; it also skips admission, since a request that adds nothing is never refused (`internal/admission/admission.go:277-280`) |

What `handleApply` runs today, in order, before it commits (`daemon.go:1197-1300`):
parse; `ValidateWorkDirs` (placement); `ValidateRuntimes` (the command
resolves); `ValidateBudgets`; `ValidateTeamNames` (marvel#319); admission per
team (`admitGrowth`); advisories; then `m.Apply(d.store)`, the posture write,
`Reproject`, `regenerateBus` and `ReconcileOnce`.

## 2. The decided shape

All five seats agreed on each point in this section by the final round.

1. **The verb is `marvel plan -f <manifest>`.** The file is a flag, never
   the positional, because the positional is the workspace/team filter and
   that is #334's trap. A positional that names an existing file is refused
   with a pointer to `-f`.
2. **There is no `marvel work --dry-run`.** The hazard is omission, not a
   typo. cobra rejects a misspelled flag, but a line with the flag dropped
   (from shell history, a runbook, or an agent retyping a command) applies
   for real. With a separate verb, dropping a word cannot turn a preview
   into an apply.
3. **A new daemon method, `plan_manifest`,** taking the same params as
   `apply`. An older daemon answers "unknown method" (`daemon.go:993-994`),
   so the new client fails closed. The two alternatives fail open:
   - a `dry_run` field on `apply` is dropped by an older daemon, which then
     applies (`applyParams` is a plain `json.Unmarshal`;
     `DisallowUnknownFields` appears nowhere in `daemon.go`);
   - new params on `plan` are dropped by an older daemon, which then
     returns the held-teams plan.
4. **It computes in the daemon.** Every pre-flight answers against the
   daemon's host and store:
   - binaries are resolved with `exec.LookPath` on the daemon's PATH
     (`internal/api/manifest.go:646`);
   - workdirs are checked with `os.Stat` and `EvalSymlinks` on the daemon's
     host (`internal/api/workdir.go:62-90`);
   - the name clash and admission are read from the store.

   A client-side plan would answer for the wrong machine. The client sends
   the bytes and the resolved `workspace_root`, as `marvel work` does, and
   renders the result.
5. **One evaluation, three callers.** `handleApply` is split three ways:
   - a pure `evaluate(manifest, store snapshot)` that runs every pre-flight
     and computes the plan;
   - `apply`, which calls `evaluate`, then logs, emits events and commits;
   - `plan_manifest`, which calls `evaluate` and returns the plan.

   Two existing pieces need care:
   - **Admission:** `admitGrowth` logs and emits `KindAdmissionRefused` /
     `KindAdmissionUnmeasured` (`internal/daemon/admission.go:60-88`), so
     the plan calls the pure `admission.Check` it wraps (`:64`).
     Otherwise every preview would write refusals into the event stream.
   - **Workdirs:** `ValidateWorkDirs` mutates its receiver (it rewrites the
     root through symlinks, or drops it), so `evaluate` runs it on a parsed
     copy and reports the root before and after.
6. **It never writes.** That rules out:
   - the store, `m.Apply`, the posture write, `Reproject`, `regenerateBus`,
     `ReconcileOnce`, and events-as-applied;
   - projection files, `CODEX_HOME` seeding, trust grants, and bus
     credentials;
   - tmux.

   seat-bootstrap's SB-4M trust step (marvel#490, design) needs a
   check-only mode before a plan can report it. Until then the plan prints
   that step as "not evaluated".

## 3. What it prints

Here is the worked case the party used. A cluster holds team `ops` (two
reviewers, one mid-turn). A manifest adds team `core` (supervisor x1 on
claude, builder x2 on codex) and lowers `ops`'s reviewer from 2 to 1.

```
$ marvel plan -f teams.toml          read-only: nothing is applied
target   cluster=kinu socket=unix:<socket> host=kinu uid=501 daemon pid=4412 started=2026-10-03T14:02Z
read at  2026-10-03T15:20:07Z  digest=3f9a1c  manifest=sha256:7be0...  (advice, not a lock)

CHECKED
 ok  [host-fs]  workspace.root /srv/work -> /srv/work (no symlink change)
 ok  [host-fs]  core/supervisor  claude -> /usr/local/bin/claude  (daemon PATH)
 ok  [host-fs]  core/builder     codex  -> /opt/homebrew/bin/codex (daemon PATH)
 ok  [host-fs]  core workdir exists; daemon uid can enter and write (prediction)
 ok  [manifest] budgets satisfiable
 ok  [store]    team name "core" free in every workspace
 ok  [store]    admission core: +3 seats, count ceiling 6, headroom 3
 ok  [store]    admission ops: admitted because it adds nothing (want <= 0)

CHANGES
 + core/supervisor   spawn 1
 + core/builder      spawn 2
 - ops/reviewer      kill 1 (2 -> 1)  candidate ops-reviewer-0 (oldest by store order, may change)
                     turn in flight: not evaluated; a kill loses it whole
 ~ ops               posture: converge (unchanged)
 gross +3 spawn, -1 kill; net the gate sees +2

CREDENTIALS (names and presence only)
 [store]    held now: none named by this manifest's env
 [manifest] env keys set: core/builder DIRECTOR_ROLE

NOT EVALUATED
 seat first launch: login, trust dialog, MCP wiring, quota
 whether the killed reviewer is mid-turn
 wrapper commands: the script exists; the harness behind it is not inspectable
 anything that changed after 15:20:07Z, including a daemon restart (credentials drop on reexec, #339)

3 spawns, 1 kill, 0 refusals
```

## 4. Features specific to marvel, ranked

The party's final rankings, merged; the first three were ranked first or
second by most seats.

1. **Target first, and refuse the wrong one.** The first line names:
   - the cluster;
   - the socket;
   - the host and uid;
   - the daemon's pid and start time.

   `plan` hard-fails on an unknown `--cluster` (today every verb warns and
   falls back to the local daemon, `cmd/marvel/main.go:77-85`, #502). It
   also refuses when `MARVEL_SOCKET` overrides a named cluster (5-0). A
   clean "0 changes" from the wrong daemon reads as a green light for the
   right one.

   The client resolves its daemon four ways (`resolveDaemonAddr`,
   `cmd/marvel/main.go:64-90`), and the party voted on two of them. The
   other two are open (section 10, item 4):
   - `--socket` (`:65-67`) is the operator naming a socket outright. The
     proposal is to print "socket given by flag" in the banner, not to
     refuse.
   - A `config.Load` failure (`:71-73`) falls back to the local daemon with
     NO warning. The proposal is to refuse when `--cluster` was given, and
     otherwise print "config unreadable, local daemon" in the banner.
2. **Gross adds, gross kills, and the net the gate sees, with a kill
   list.** The admission gate sees one net `want` per team and admits
   `want <= 0` (`internal/admission/admission.go:277-280`). So a manifest that lowers one role and raises another can
   pass the gate while it still kills seats. The plan names each kill
   candidate. The controller already picks them, oldest first, as a pure
   function of the store (`controller.go:1216-1232`, the #348 fix). The
   plan labels the candidate "may change", and marks the in-flight turn
   "not evaluated" (a kill loses it whole, orc finding-207). Neither kubectl
   nor terraform destroys a conversation.
3. **Admission as a plan line.** It uses the same arithmetic as apply, from
   `admission.Check`, and prints "admitted because it adds nothing" where
   that is the reason. Cumulative token clauses print "cost of this change
   not knowable": pricing growth needs a usage baseline that is not built
   (`internal/usage/reader.go:86-100`).
4. **Daemon-host verdicts per seat:**
   - the root, visible or dropped. Today an absolute root the daemon cannot
     see is dropped with one log line and the apply places nothing
     (`workdir.go:68-74`); the plan promotes that to a finding;
   - each command resolved to an absolute path on the daemon's PATH;
   - the workdir checked for enter and write as the daemon's uid.
5. **First-launch preconditions per adapter:**
   - claude: setting sources and, once SB-4M exists, trust for the realpath;
   - codex: its private home, and the hook trust that home records;
   - opencode and generic: "nothing prepared";
   - a wrapper command: "harness not inspectable".
6. **A provenance tag on every finding, and a NOT EVALUATED block that is
   always printed.** The tag says how fast the finding goes stale:
   [manifest] never, [host-fs] in seconds, [store] every tick. An unchecked
   item is listed, never omitted.
7. **Credentials as names and presence only** (4-0). The block lists:
   - credentials the daemon holds now, tagged [store];
   - env key names the manifest sets, tagged [manifest].

   Each reads "present, unvalidated". It never prints a value, a hash, a
   prefix or a length. The manifest declares no credential need
   (`manifest.go` has no credential field), so the plan cannot say which
   names are required.
8. **Becoming behind** (architect's addition, not voted). A manifest that
   changes a role's runtime leaves its live sessions on the old copy until
   they respawn (`drift-view.md` section 2, #416). The plan lists them as
   "becomes behind", because apply does not restart them.

## 5. What the plan must never claim

- **That a seat will start and work.** The plan checks preconditions the
  daemon can see. Login, the trust dialog, MCP inheritance and quota are
  known only by launching. Its strongest line reads "would resolve to /path
  on host H", never "will run". An exit of success means nothing the daemon
  can see would stop the apply.
- **A token or dollar forecast** (3-1; see section 9 for the dissent). The
  plan prints counts, not spend.
- **That the apply will match it.** It is a read of a store that moves on
  every reconcile tick. Two defects make even apply unable to promise its
  own verdict (section 8):
  - apply's check-then-commit is not serialized across connections;
  - `m.Apply` is a series of store calls, not one transaction.

  The plan prints its read time and a digest of what it read (the held spec
  and live session ids of the teams the manifest names, plus the daemon's
  start time), so drift is visible. It does not bind the apply.

## 6. Exit codes (OPEN: ruling (a), section 10)

The vote split (section 9).

Agreed:
- success is 0;
- a refusal that apply would return is never 0;
- "could not evaluate" is never 0. That covers an unconfirmed target, an
  unreachable daemon, a daemon without `plan_manifest`, and a pre-flight
  that errored.

Default, OPEN until ruled. It rests on the 3-2 vote that refusal and "could
not evaluate" get distinct codes; the numbering itself tied 2-1-2, so the
numbers below are a choice, not a vote. The flag follows its 3-2 vote:
- 0: evaluated, no refusal;
- 1: apply would refuse;
- 3: could not evaluate;
- 2: changes exist, returned only under `--detailed-exitcode`.

Under the flag, 0 also means no changes. Without it, `marvel plan -f m &&
marvel work m` keeps working.

The cost of this default: today every error exits 1
(`cmd/marvel/main.go:169-170`, `os.Exit(1)` on any error from
`root.Execute`). A dial failure, an unreachable daemon or a mistyped flag
would therefore exit 1 and read as "apply would refuse", unless `plan` maps
its own exits and returns 3 for every failure to evaluate. The mapping is
work this default adds; the 1-for-both option (refusal and failure both
exit 1) avoids that work.

## 7. Edits, in order (none made by this PR)

1. Fix the inline-args pre-flight defect first, in its own change (section
   8). The plan reports what the pre-flight reports (5-0), so the plan
   inherits the fix rather than working around it.
2. Split `handleApply` into `evaluate` / commit, with `admission.Check`
   called from `evaluate` and the log and emit kept in apply. This is a
   refactor with no behavior change, so the existing apply tests cover it.
3. Add the `plan_manifest` method and its result type: the findings with
   tags, the changes, the kill candidates, credential presence, the not
   evaluated list, and the digest.
4. Add `marvel plan -f`, the banner, the cluster and socket-override
   refusals, the redaction rules, and the exit codes.
5. Make a positional `plan` argument that names a file an error pointing at
   `-f` (closes #334's manifest case).
6. Later, outside this design: a check-only mode for the SB-4M bootstrap,
   so the claude trust line moves out of NOT EVALUATED.

Each step is its own PR. Step 2 blocks 3, and 3 blocks 4.

## 8. Defects this work surfaced (separate issues; this PR files none)

- **A command with inline args fails apply's pre-flight (marvel#517).** `ValidateRuntimes`
  passes `Runtime.Command` whole to `validateCommand`, which `os.Stat`s or
  `exec.LookPath`s the whole string (`manifest.go:618`, `:633-646`; no
  `strings.Fields` in the file). The claude adapter splits the same string
  (`internal/runtime/claude.go:186-196`). So `command = "claude --flag
  value"` is refused at apply although the adapter would run it. A
  throwaway unit-level probe at 889e49a confirmed it: `validateCommand("claude")`
  returns nil and `validateCommand("claude --flag value")` returns "not on
  PATH"; `ValidateRuntimes` refuses a role with `sh -c true` the same way.
  A working form exists today: the arguments go in `args`
  (`ManifestRuntime.Args`, `internal/api/manifest.go:201`). A role with
  `command = "sh"` and `args = ["-c", "true"]` passes. Filed as marvel#517.
- **Apply's check-then-commit is not serialized.** Each connection runs in
  its own goroutine (`daemon.go:582`), and I found no apply-level lock. Two
  concurrent `marvel work` calls can both pass the name-clash check.
  (This was partly checked: the store's own locking of that case was not
  read.)
- **The pre-flight is not the resolver for most adapters.** `exec.LookPath`
  runs in the pre-flight, for codex's home, and for tmux and nats-server,
  and nowhere else (`git grep LookPath`). A claude, opencode or generic seat
  is started by the pane's shell with tmux's PATH. Whether that equals the
  daemon's PATH is UNVERIFIED. If it differs, plan and apply agree on a
  binary the pane never finds.
- **#502 for every verb.** The plan's refusal fixes only the plan.
- **The plan as an existence probe.** `plan_manifest` stats paths the
  manifest names, as the daemon's user. On a cluster shared between users,
  a plan would reveal which daemon-host paths exist. This is not a risk on
  a one-user cluster; it is noted for SOUL section 3's multi-user boundary.

## 9. Rejected options, with reasons

| Option | Why it was rejected |
|---|---|
| `marvel work --dry-run` (as the verb or as an alias) | dropping the flag is a real apply; no parser catches an omission (4 seats moved to this by round 2, 5-0 at the end) |
| a `dry_run` field on `apply` | an older daemon drops the field and applies |
| new params on the existing `plan` RPC | an older daemon drops them and returns the held-teams plan, #334 again |
| a client-side plan | it checks the client's PATH, filesystem and store view, not the daemon's |
| calling `admitGrowth` from the plan | it logs and emits refusal events for a preview that never happened |
| a terraform-style saved plan file | it stores resolved paths and env, so it leaks secrets and goes stale; terraform's whole-state stale check also false-positives on no-op applies |
| `work --expect-version N` on the store's ResourceVersion | the counter moves on every persisted mutation (`bolt.go:405-410`), so it would refuse almost always |
| `work --expect <digest>` in v1 (5-0 against) | nobody has measured how often the digest moves under a live team; revisit with that measurement |
| a historical reload cost line (3-1 against) | a number beside a plan reads as a forecast whatever the label, and the one sample is claude seats on one host |
| proceeding past a `MARVEL_SOCKET` override with a banner (5-0 against) | the banner is read after the answer |
| a plan that splits commands itself (5-0 against) | the plan would say ok where apply refuses |
| listing which credentials each role needs | the manifest declares none |
| renaming `marvel plan` because `plan` is also a Claude permission mode | that `plan` is a value in a manifest field, not a verb; renaming costs more than the collision |

## 10. Open items and rulings needed

Five seats voted in the last round. A seat could abstain on an item
outside its expertise, and an abstention counts as no vote, so some
tallies (3-1, 4-0, 2-2) sum to four.

1. **(a) OPEN. Exit-code numbering (V3).** The vote split three ways: 0/2/1-for-both
   (2 seats), 0/2/1-error/3-refusal (1), 0/2/1-refusal/3-not-evaluated (2).
   - On the underlying question, refusal and "could not evaluate" get
     distinct codes, 3-2.
   - On whether exit 2 is the default, `--detailed-exitcode` wins 3-2. The
     minority's reason: a forgotten flag reads "changes" as 0.
   - Default: section 6, with its cost (every error exits 1 today).
2. **(b) OPEN. Home paths in output (V4), split 2-2.**
   - (x) Collapse the home prefix to `~` for display. This shows which
     binary resolved, for every role.
   - (y) Redact home paths except the one resolved binary path per role.
     This keeps another user's directory names out of CI logs.

   The redaction design (`describe-redaction.md`, #421) covers values, not
   paths. Default: (y), the stricter one, until #421 rules on paths.
3. **Recorded dissent (V2).** The finops seat: without any figure the plan
   shows counts and nothing about cost, and operators will invent their own
   number. Reopen when `usage.Baseline` exists.
4. **OPEN, not voted: the other two wrong-daemon paths** (section 4, item
   1). The proposal is to put `--socket` in the banner and to refuse a
   `config.Load` failure when `--cluster` was given. The party voted only on
   the unknown cluster and the `MARVEL_SOCKET` override.

## 11. Prior art weighed

- **`kubectl diff` and `--dry-run=server`.** Server-side dry run runs the
  real admission path, which is the model for section 2 item 4. Admission
  webhooks that cannot declare themselves side-effect free are not called
  on dry run; that is the model for "not evaluated". The diff carries no
  binding; safety at apply comes from per-object resourceVersion.
- **`terraform plan` and saved plans.**
  - From terraform, marvel takes resources marked "must be replaced" (here,
    with a cost terraform lacks) and `-detailed-exitcode`.
  - It does not take the saved plan file.
