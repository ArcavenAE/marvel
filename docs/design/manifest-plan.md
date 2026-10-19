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
- Refreshed 2026-10-19 against `origin/main` f1fe67f, after the operator ruled
  that this design is built without a second party. Every line cite below
  points at f1fe67f, except the first defect in section 8, which is kept as
  found at 03dd483 and says so. What changed underneath the design, and what
  that changes here, is in section 1a.

## 1. The problem, verified

| Premise | Where | Result |
|---|---|---|
| `marvel plan` previews only teams the daemon already holds | `cmd/marvel/main.go:1207-1231`; the `plan` RPC takes no params (`internal/daemon/daemon.go:1176-1177`, `handlePlan()` at :1600) | true; teams mid-shift are omitted |
| The plan/apply split exists inside the controller | `internal/team/controller.go` around :1062-1400 (`RolePlan` :1062, `HoldDetail` :1086, `plan.Delete` :1271, `PlanConvergence` :1395); aae-orc-nf0w and aae-orc-nrk1, both closed | true: the seam this design reuses |
| `marvel work` has no preview | `apply` params are `manifest_data` and `workspace_root` only (`daemon.go:1341-1349`); no dry-run, diff, preview, validate or check flag under `cmd/marvel` | true |
| Handing `plan` a manifest misleads | #334 (open): "no teams to plan", exit 0 | true |
| The workaround is itself an apply | load at `replicas: 0`, then plan: it stores the manifest, and the plan shows desired 0 and spawn 0 | true; it also skips admission, since a request that adds nothing is never refused (`internal/admission/admission.go:277-280`) |

What `handleApply` runs today, in order, before it commits
(`daemon.go:1351-1445`, f1fe67f). Nine of the fifteen steps can refuse the
apply; the other six only add advisories.

1. parse (`:1357`): **refuses** a manifest that does not parse;
2. `ScheduleHistoryCap` (`:1366`): **refuses** a schedule history above the
   cluster's cap (`d.scheduleHistoryMax`, from the daemon's config);

   Between steps 2 and 3, `workspace_root` from the request replaces the
   manifest's root (`:1370-1372`). That is a transform, not a check, and
   `evaluate` applies it the same way.
3. `ValidateWorkDirs` (`:1375`): **refuses** a workdir it cannot place, and
   otherwise adds advisories;
4. `ValidateTrustedFolders` (`:1379`): **refuses** a listed folder that does
   not exist or is not the top of a main git checkout (marvel#684);
5. `LegacyPlacementNotes(d.store)` (`:1382`): advisories;
6. `TrustedFolderNotes(d.store)` (`:1383`): an advisory when this apply
   replaces the workspace's stored list;
7. `ValidateRuntimes` (`:1389`): **refuses** a command that does not
   resolve, and now also returns advisories;
8. `ValidateBudgets(d.sessMgr.CanStreamRole)` (`:1400`): **refuses** a
   declared budget dimension no role can report;
9. `refuseUnadmittedGlobalRoles(m, d.sessMgr.Bus)` (`:1406`, defined at
   `:3467`): **refuses** a role that declares the global tier on a cluster
   whose bus config does not admit its name;
10. `ValidateTeamNames(d.store.ListTeams())` (`:1412`, marvel#319):
    **refuses** a name clash;
11. admission per team (`admitGrowth`, `:1435`): **refuses** growth over a
    budget;
12. `ContextFeedAdvisories(d.sessMgr.CanFeedContextRole)` (`:1440`);
13. `DSTAcknowledgements(now)` (`:1441-1442`), with `now` read once at
    `:1441`;
14. `ScheduleAdvisories(now)` (`:1443`);
15. `ShiftHeadroomAdvisories` (`:1444`).

Then it commits: `m.Apply(d.store)` (`:1446`), the posture write, `Reproject`
(`:1499`), `regenerateBus` (`:1503`) and `ReconcileOnce` (`:1506`). The result
now also carries `behind`, the count of the applied teams' sessions still on
an older runtime than their role's (`:1519`, `behindCount` in `spec.go:47`).

## 1a. What changed between 03dd483 and f1fe67f

| Change | Where | What it changes here |
|---|---|---|
| Six pre-commit steps were added or widened since 03dd483: items 2, 4, 6, 7, 9 and 15 above. Three of them refuse (2, 4 and 9). Steps 5, 12, 13 and 14 were already there at 03dd483 (`daemon.go:1222`, `:1271`, `:1273`, `:1274`) and the first version of this design missed them. | `daemon.go:1366-1444` | `evaluate` runs all fifteen (section 2, item 5). A plan that skips a refusing step says ok where apply refuses; one that skips an advisory step hides a line apply would print. |
| Step 9 reads the bus config the daemon loaded, not the store. | `refuseUnadmittedGlobalRoles(m, b session.BusEnv)`, `daemon.go:3467` at f1fe67f, `:3487` on main 69461c4 | `evaluate` takes the bus env as an input beside the store snapshot. It only reads it: the function has no side effect, and with no bus it refuses nothing. |
| Step 4 runs `git` (5 second timeout) under each listed folder. | `internal/api/trusted_folders.go:120-158`, `gitTimeout` :26 | it writes nothing, so it stays in the plan; a slow filesystem makes the plan slow, not wrong. |
| A command with inline args passes the pre-flight. | marvel#517, closed; `validateCommand` now splits on spaces and tabs (`manifest.go:756-794`) and marks shell text it does not parse with an advisory | section 7 step 1 is done; section 8's first defect is fixed. |
| An unknown `--cluster`, or `--cluster` with an unreadable config, is refused for every verb; an explicit `--cluster` outranks `MARVEL_SOCKET`. | marvel#502 and #586, closed; `resolveDaemonRung`, `cmd/marvel/main.go:92-151` | most of section 4 item 1 and of section 10 item 4 is now how every verb behaves. What remains is in section 10. |
| The client reports which rung chose the daemon: flag, env, cluster or default. | `resolveRung`, `main.go:77-80` | the banner prints the rung; no new resolution code is needed. |
| A typed exit code exists. | `exitError{Code, Msg}`, `cmd/marvel/wait.go:31`; `main.go:208-212` exits with its code | section 6's mapping cost is smaller: whichever numbering is ruled, `plan` can return an `exitError` per outcome instead of adding a new mechanism. The numbering is ruled: section 6 (section 10 item 1). |
| Codex folder trust has a judge with no side effect. | `resolveCodexTrust`, `internal/runtime/codex_trust.go:28`, reads the filesystem and runs `git` through `GitMainRoot` but records nothing; `codexFolderTrust` (:52) wraps it and records an event | the plan can report each codex seat's trust by calling the judge, never the wrapper, the same split as `admission.Check` and `admitGrowth`. Claude trust (SB-4M) stays NOT EVALUATED. |
| `behind` is counted at apply. | marvel#416, closed; `spec.go:47` | section 4 item 8 reuses `behindCount`'s rule; it is no longer the architect's guess. |

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
   `apply`. An older daemon answers "unknown method" (`daemon.go:1178-1179`),
   so the new client fails closed. The two alternatives fail open:
   - a `dry_run` field on `apply` is dropped by an older daemon, which then
     applies (`applyParams` is a plain `json.Unmarshal`;
     `DisallowUnknownFields` appears nowhere in `daemon.go`);
   - new params on `plan` are dropped by an older daemon, which then
     returns the held-teams plan.
4. **It computes in the daemon.** Every pre-flight answers against the
   daemon's host and store:
   - binaries are resolved with `exec.LookPath` on the daemon's PATH
     (`internal/api/manifest.go:790`);
   - workdirs are checked with `os.Stat` and `EvalSymlinks` on the daemon's
     host (`internal/api/workdir.go:62-92`, `checkWorkDir` :123-144);
   - trusted folders are checked with `os.Stat`, `EvalSymlinks` and `git` on
     the daemon's host (`trusted_folders.go:120`);
   - the global-role admission is read from the bus config the daemon loaded;
   - the schedule history cap and the two adapter predicates are the
     daemon's own;
   - the name clash and admission are read from the store.

   A client-side plan would answer for the wrong machine. The client sends
   the bytes and the resolved `workspace_root`, as `marvel work` does, and
   renders the result.
5. **One evaluation, three callers.** `handleApply` is split three ways:
   - a side-effect free `evaluate` that runs every pre-commit step in
     section 1, all fifteen, in apply's order, plus the `workspace_root`
     transform, and computes the plan. Side-effect free, not pure: it writes
     nothing, emits nothing and logs nothing, but it reads the daemon host's
     filesystem, PATH and git (steps 3, 4 and 7), so the same input can give a
     different answer a second later. Its inputs, each passed in so a test can
     supply them without a live daemon:
     - the manifest and the request's `workspace_root`;
     - the store, read only through its read methods (`GetWorkspace`,
       `ListTeams`, `ListSessionsByTeam`, and the `*Store` that steps 5 and 6
       take). "Snapshot" in this design means exactly that: each read returns
       value copies (`internal/api/store.go`), but the reads are not one
       frozen view, so two reads can straddle a reconcile tick. The plan's
       digest (section 5) records what was read;
     - the bus env (step 9);
     - the schedule history cap (step 2);
     - the predicates `CanStreamRole` (step 8) and `CanFeedContextRole`
       (step 12);
     - `now`, read once, for steps 13 and 14;
     - the usage accountant's team spend, which the admission snapshot of
       step 11 reads (`AdmissionSnapshot`, `internal/daemon/admission.go:21`,
       `d.usage.TeamSpend` at `:33`).

     A step added to apply later is added to `evaluate`, not beside it,
     since apply calls `evaluate`;
   - `apply`, which calls `evaluate`, then logs, emits events and commits;
   - `plan_manifest`, which calls `evaluate` and returns the plan.

   Two existing pieces need care:
   - **Admission:** `admitGrowth` logs and emits `KindAdmissionRefused` /
     `KindAdmissionUnmeasured` (`internal/daemon/admission.go:61-98`), so
     the plan calls the pure `admission.Check` it wraps (`:65`).
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
   that step as "not evaluated". Codex trust is the exception: its judge
   already has no side effect (section 1a).

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

### Home paths in the output (ruling (y), 2026-10-19)

The plan's output reaches CI logs and pasted messages, so it shows no
directory names under anyone's home, with one exception per role. The rule,
exactly:

- **A home path** is an absolute path, after `filepath.Clean`, that is
  under the home of the process printing the line (`os.UserHomeDir`), or
  under `/Users/` or `/home/`. The daemon applies it to the lines it builds,
  with the daemon user's home; the client applies it to the banner, with
  the client's home. Neither rewrites the other's lines.
- **The exception: one program path per role.** The path `exec.LookPath`
  returns for the role's command on the daemon's PATH (section 4 item 4).
  When it is under the daemon user's home it is written with that home as
  `~` (`~/.local/bin/claude`). Under `/Users/` or `/home/` but not the
  daemon's home, it is hidden like any other.
  - A wrapper script is the program: its own path is the exception, and
    nothing it execs is printed (section 4 item 5, "harness not
    inspectable").
  - `args` are never printed, since they can name paths. The line prints
    their count (`args: 2`).
- **Every other home path is hidden** as `<home:XXXXXXXX>`, the first eight
  hex digits of the SHA-256 of the cleaned path. The same path prints the
  same token, so a before and after can still be compared, and no directory
  name prints. This covers a passing check as well as a failing one.

What that means for each line that can carry a path:

| line | when its path is a home path |
|---|---|
| banner `socket=` | `socket=unix:<home:XXXXXXXX>`, with the rung that chose it (`(MARVEL_SOCKET)`, `(cluster <name>)`, `(layout default)`). The cluster, host, uid, pid and start time already identify the daemon. |
| `workspace.root A -> B` | both sides hidden: `workspace.root <home:1b2c3d4e> -> <home:1b2c3d4e> (no symlink change)` |
| command resolution | the exception above |
| workdir, passing | `core workdir <home:5e6f7a8b> exists; daemon uid can enter and write` |
| workdir or trusted folder, failing | names the check and the token, not the path: `core workdir <home:5e6f7a8b>: does not exist` |
| refusal text from a pre-flight | the same rule, applied to the text the daemon returns before it is printed |

A path not under any home (`/srv/work`, `/opt/homebrew/bin/codex`) prints as
it is, as in the worked case above.

## 4. Features specific to marvel, ranked

The party's final rankings, merged; the first three were ranked first or
second by most seats.

1. **Target first, and refuse the wrong one.** The first line names:
   - the cluster;
   - the socket;
   - the host and uid;
   - the daemon's pid and start time.

   An unknown `--cluster` is refused, as every verb now refuses it (#502,
   `cmd/marvel/main.go:121-131`). `plan` also refuses when `MARVEL_SOCKET`
   picks the daemon while the config's `current_cluster` names a cluster
   (5-0): an explicit `--cluster` already outranks the variable (#586,
   `:101-107`), but `current_cluster` does not, so that case still dials
   the seat's own daemon. A clean "0 changes" from the wrong daemon reads as
   a green light for the right one.

   The client resolves its daemon by four rungs (`resolveDaemonRung`,
   `cmd/marvel/main.go:92-151`; flag, env, cluster, default at :77-80), and
   the party voted on two cases. The operator ruled the other two on
   2026-10-19 (section 10, item 4):
   - `--socket` (`:93-95`) is the operator naming a socket outright. `plan`
     refuses when `--socket` is given, and says why. The cost, accepted in
     the ruling: `plan` cannot preview against a daemon reached only by
     `--socket`.
   - A `config.Load` failure with `--cluster` is now refused for every verb
     (`:108-117`). Without `--cluster` it still falls back to the local
     daemon with NO warning. `plan` refuses here too, and prints the config
     error. A missing config file is not a failure: `config.Load` returns
     the defaults for it (`internal/config/config.go:830-831`), so this
     refusal bites only on a config file that exists and does not parse.
     An `ErrInvalidClusterName` (another cluster's bad name) stays a
     warning, as it is for every verb (`main.go:118-120`).

   **`plan` reads the config itself, on every rung but `--socket`.** With
   `MARVEL_SOCKET` set and no `--cluster`, `resolveDaemonRung` returns at
   `main.go:105-107`, before `config.Load` at `:108`. Every seat has the
   variable set (`:101-104`), so if `plan` relied on the resolver, the
   unreadable-config refusal could never fire from a seat. And `plan` needs
   the config on that rung anyway: the `current_cluster` refusal above reads
   it. So `plan` calls `config.Load` before it dials, whatever rung chose the
   socket. A load error that is not `ErrInvalidClusterName` refuses, with
   `MARVEL_SOCKET` set or not. On `--socket` it refuses before reading
   anything.

   Both refusals, and the `current_cluster` one, mean `plan` did not run, so
   they exit 1, "could not evaluate" (section 6), never 3.

   **How tests reach a daemon.** Every `plan` test builds the child's
   environment from nothing and never inherits the caller's: a seat carries
   `MARVEL_SOCKET`, `MARVEL_HOME` and the other `MARVEL_*` keys that point
   at the live fleet. `MARVEL_HOME` outranks `HOME` for the whole layout
   (`internal/paths/paths.go:62-68`), so a scratch `HOME` alone leaves the
   config and the default socket wherever `MARVEL_HOME` names. Each test
   sets:
   - `HOME` to a scratch directory, and `MARVEL_HOME` to a scratch root
     under it (short enough for `run/marvel.sock` to fit a unix socket
     path);
   - `MARVEL_SOCKET` per case: set to the scratch daemon's socket for the
     cases that run, unset for the cases that check a refusal on the
     default rung;
   - no other `MARVEL_*` key.

   The cases, each with its exit code:

   | case | `MARVEL_SOCKET` | config under the scratch `MARVEL_HOME` | expect |
   |---|---|---|---|
   | positive control | the scratch daemon | missing | runs against the scratch daemon, exit 0 |
   | env rung, unreadable config | the scratch daemon | present, does not parse | refuses, prints the config error, exit 1, dials nothing |
   | default rung, unreadable config | unset | present, does not parse | refuses, exit 1, dials nothing |
   | env rung, `current_cluster` set | the scratch daemon | names a cluster | refuses, exit 1 |
   | `--socket` | unset | missing | refuses, exit 1 |

   "Dials nothing" is checked by the scratch daemon's request count, which
   stays 0. The default-rung case has no daemon at the default socket, so a
   refusal that failed to fire shows as a dial error instead of exit 1's
   refusal text, and the test reads the text, not only the code.
2. **Gross adds, gross kills, and the net the gate sees, with a kill
   list.** The admission gate sees one net `want` per team and admits
   `want <= 0` (`internal/admission/admission.go:277-279`). So a manifest that lowers one role and raises another can
   pass the gate while it still kills seats. The plan names each kill
   candidate. The controller already picks them, oldest first, as a pure
   function of the store (`controller.go:1261-1278`, the #348 fix). The
   plan labels the candidate "may change", and marks the in-flight turn
   "not evaluated" (a kill loses it whole, orc finding-207). Neither kubectl
   nor terraform destroys a conversation.
3. **Admission as a plan line.** It uses the same arithmetic as apply, from
   `admission.Check`, and prints "admitted because it adds nothing" where
   that is the reason. Cumulative token clauses print "cost of this change
   not knowable": pricing growth needs a usage baseline that is not built
   (`internal/usage/reader.go:86-100`, still marked NOT IMPLEMENTED at
   f1fe67f).
4. **Daemon-host verdicts per seat:**
   - the root, visible or dropped. Today an absolute root the daemon cannot
     see is refused, unless no relative workdir depends on it
     (`rootIsClientOnly`, `workdir.go:99`); then it is dropped with an
     advisory and the apply places nothing (`workdir.go:67-73`). The plan
     promotes that advisory to a finding;
   - each command resolved to an absolute path on the daemon's PATH. The
     path printed is what `exec.LookPath` returns on the daemon's PATH, the
     same call the validator makes (`internal/api/manifest.go:790`), not
     the path after `EvalSymlinks`: it names what the daemon will exec, and
     a Homebrew or mise link would otherwise print its versioned target;
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
   they respawn (`drift-view.md` section 2, #416, closed). The plan lists
   them as "becomes behind", because apply does not restart them. Apply now
   returns the count after the fact (`behindCount`, `spec.go:47`); the plan
   computes the same rule before it, and names the sessions.

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

## 6. Exit codes (ruled 2026-10-19, section 10)

The vote split (section 9).

Agreed:
- success is 0;
- a refusal that apply would return is never 0;
- "could not evaluate" is never 0. That covers an unconfirmed target, an
  unreachable daemon, a daemon without `plan_manifest`, and a pre-flight
  that errored.

Ruled by the operator on 2026-10-19. It keeps the 3-2 vote that refusal and
"could not evaluate" get distinct codes; the numbering, which tied 2-1-2, is
the operator's. The flag follows its 3-2 vote:
- 0: evaluated, no refusal;
- 1: could not evaluate;
- 3: apply would refuse;
- 2: changes exist, returned only under `--detailed-exitcode`.

Why 1 for "could not evaluate": marvel already exits 1 on any error it does
not classify. A failure path the builder misses therefore falls through to
1, which here is the true reading, instead of reading as "your manifest
would be refused".

Exit codes are per verb. `marvel wait` already uses 3, for a timeout,
beside 0 succeeded, 1 failed, 2 crashed, 4 unknown and 5 daemon error
(`cmd/marvel/wait.go:18-26`). `plan` never returns wait's codes, and wait
never returns plan's, so neither verb's 3 can be read as the other's. A
script that runs both reads each exit code against the verb it just ran,
which is how `wait`'s codes are documented today.

Under the flag, 0 also means no changes. Without it, `marvel plan -f m &&
marvel work m` keeps working.

The cost of this numbering: an error from `root.Execute` exits 1 unless it
is an `exitError`, which exits with its own code (`cmd/marvel/main.go:207-212`,
`exitError` in `wait.go:31`, used by `marvel wait`). `plan` returns an
`exitError` with code 3 for each refusal apply would return, and a test
checks that each of the nine refusing steps in section 1 (1, 2, 3, 4, 7, 8,
9, 10 and 11, one fixture each, as section 7 step 2 lists them) exits 3. A dial failure, an
unreachable daemon or a mistyped flag needs no wrapping: it exits 1, which
is "could not evaluate".

## 7. Edits, in order (none made by this PR)

1. Fix the inline-args pre-flight defect first, in its own change (section
   8). The plan reports what the pre-flight reports (5-0), so the plan
   inherits the fix rather than working around it. **Done:** marvel#517,
   closed at f1fe67f.
2. Split `handleApply` into `evaluate` / commit, with `admission.Check`
   called from `evaluate` and the log and emit kept in apply. `evaluate`
   takes the inputs of section 2 item 5 and runs all fifteen pre-commit steps
   of section 1. This is a refactor with no behavior change, so the existing
   apply tests cover it. Two new tests:
   - one fixture per refusing step, nine in all (steps 1, 2, 3, 4, 7, 8, 9,
     10 and 11), each asserting that `evaluate` alone returns the same
     refusal apply returns;
   - one manifest that triggers every advisory step (3, 5, 6, 7, 12, 13, 14
     and 15), asserting that `evaluate`'s advisories equal, in order, the
     ones apply logs. A refusal test cannot see a skipped advisory step;
     this one can.
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

- **A command with inline args fails apply's pre-flight (marvel#517,
  closed; fixed at f1fe67f, section 1a; the text below is as found at
  03dd483).** `ValidateRuntimes`
  passes `Runtime.Command` whole to `validateCommand`, which `os.Stat`s or
  `exec.LookPath`s the whole string (`manifest.go:618`, `:633-646` at
  03dd483; no `strings.Fields` in the file then). The claude adapter splits
  the same string (`internal/runtime/claude.go:186-196` at 03dd483; at
  f1fe67f, `commandArgs` at `:241`). So `command = "claude --flag
  value"` is refused at apply although the adapter would run it. A
  throwaway unit-level probe at 889e49a confirmed it: `validateCommand("claude")`
  returns nil and `validateCommand("claude --flag value")` returns "not on
  PATH"; `ValidateRuntimes` refuses a role with `sh -c true` the same way.
  A working form exists today: the arguments go in `args`
  (`ManifestRuntime.Args`, `internal/api/manifest.go:201` at 03dd483, `:230`
  at f1fe67f). A role with
  `command = "sh"` and `args = ["-c", "true"]` passes. Filed as marvel#517.
- **Apply's check-then-commit is not serialized.** Each connection runs in
  its own goroutine (`daemon.go:574`), and I found no apply-level lock (none
  at f1fe67f either). Two
  concurrent `marvel work` calls can both pass the name-clash check.
  (This was partly checked: the store's own locking of that case was not
  read.)
- **The pre-flight is not the resolver for most adapters.** `exec.LookPath`
  runs in the pre-flight, for codex's home, and for tmux and nats-server,
  and nowhere else (`git grep LookPath`). A claude, opencode or generic seat
  is started by the pane's shell with tmux's PATH. Whether that equals the
  daemon's PATH is UNVERIFIED. If it differs, plan and apply agree on a
  binary the pane never finds.
- **#502 for every verb.** Fixed for every verb by #502 and #586 (closed).
  One case remains, `MARVEL_SOCKET` beside a `current_cluster`; the plan
  refuses it (section 4 item 1), other verbs do not.
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
| `work --expect-version N` on the store's ResourceVersion | the counter moves on every persisted mutation (`bumpVersion`, `internal/api/bolt.go:458-462`), so it would refuse almost always |
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

1. **(a) RULED 2026-10-19. Exit-code numbering (V3).** The vote split three ways: 0/2/1-for-both
   (2 seats), 0/2/1-error/3-refusal (1), 0/2/1-refusal/3-not-evaluated (2).
   - On the underlying question, refusal and "could not evaluate" get
     distinct codes, 3-2.
   - On whether exit 2 is the default, `--detailed-exitcode` wins 3-2. The
     minority's reason: a forgotten flag reads "changes" as 0.
   - Ruled: 0 evaluated, 1 could not evaluate, 3 apply would refuse, 2 under
     `--detailed-exitcode`. Section 6 carries it.
2. **(b) RULED 2026-10-19: (y). Home paths in output (V4), split 2-2.**
   - (x) Collapse the home prefix to `~` for display. This shows which
     binary resolved, for every role.
   - (y) Redact home paths except the one resolved binary path per role.
     This keeps another user's directory names out of CI logs.

   The redaction design (`describe-redaction.md`, #421) covers values, not
   paths. Ruled: (y). The exact rule, with what each line prints, is in
   section 3, "Home paths in the output".
3. **Recorded dissent (V2).** The finops seat: without any figure the plan
   shows counts and nothing about cost, and operators will invent their own
   number. Reopen when `usage.Baseline` exists.
4. **RULED 2026-10-19, not voted: the other two wrong-daemon paths**
   (section 4, item 1). Narrowed at f1fe67f: a `config.Load` failure with
   `--cluster` is now refused for every verb (#502). The party voted only on
   the unknown cluster and the `MARVEL_SOCKET` override. The operator ruled
   both remaining paths refuse: `plan` refuses when `--socket` is given, and
   refuses on an unreadable config with no `--cluster`, printing the config
   error. Both differ from the proposal this section carried (print a line
   in the banner and run).

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
