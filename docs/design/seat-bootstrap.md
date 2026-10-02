# Seat bootstrap: marvel prepares the seat before the harness starts

Design for review. No code lands until this doc is reviewed.

- Author: arcaven-architect-g5-0.
- Issue: #420. Tracks: aae-orc-sp380 (P1). Blocks the wardrobe step 0 test
  (aae-orc-b8ptm). Builds on `docs/design/session-working-directory.md`
  (#255, aae-orc-g71ad and its children) and amends two of its limits
  (section 7).
- Operator ruling, 2026-09-30, verbatim: "marvel should actively manage this
  because it knows everything before the harness is ever launched and should
  set it up before the agent is launched when this condition exists". Ruled
  out: restarting the daemon from a neutral directory, accepting trust by
  hand, and waiting only for g71ad.
- Checked against marvel `origin/main` 9009e89 and Claude Code 2.1.285.
- Revised 2026-10-02 after the SB-3 r2 probe (section 5a): SB-4F replaces
  SB-4. Checked against marvel `origin/main` 2f4e306 and Claude Code
  2.1.288.

## 1. The problem, verified

| Premise | Check | Result |
|---|---|---|
| A pane starts in the daemon's directory | `internal/tmux/driver.go:228` (`new-session -d -s`), `:308` (`new-window`), neither passes `-c`; `cmd` sets no `Dir` (`:154`) | true |
| The live daemon's directory is a stale worktree | `lsof -a -p <marvel daemon pid> -d cwd` | `marvel-wt-318` |
| codex already treats that directory as the start directory | `internal/runtime/codex_home.go:335` (`os.Getwd()`, declared untrusted) | true, and it names g71ad as the fix |
| The claude adapter has no private home and seeds nothing | `internal/runtime/claude.go` implements no `SessionHome`; `Prepare` (`:89`) adds `--settings`, `--permission-mode`, a prompt | true |
| Claude Code keeps folder trust, onboarding and per-project MCP servers in its user config file, keyed by path | key names in `~/.claude.json`: `projects.<path>.hasTrustDialogAccepted`, `projects.<path>.mcpServers`, top-level `hasCompletedOnboarding` (names only read) | true |
| The fleet's director MCP server reaches claude seats through that per-path entry | on this host (kinu): `projects.<orc root>.mcpServers` holds `director-mcp`; the top-level `mcpServers` does not | true on kinu: a seat placed anywhere else loses director. **Not fleet-wide:** per the #422 review, the second cluster's seats (mokuzai) declare director in the role command (`--strict-mcp-config --mcp-config {...director-mcp-seat}`) and that host's config has no director entry. I did not check that host. Section 4 handles both |
| Claude Code can be told which settings files to load | `claude --help`: `--setting-sources <user,project,local>`, `--settings`, `--mcp-config`, `--strict-mcp-config` | true |
| A headless claude run never shows the dialog | `claude --help` on `-p`: "The workspace trust dialog is skipped when Claude is run in non-interactive mode" | true; the dialog is an interactive-seat problem |
| The nine pre-approved tools came from `marvel-wt-318/.claude/settings.local.json` | `ls marvel-wt-318/.claude` | **false**: no such file there. `settings.local.json` exists in three ancestors (the orc root, `~/work`, `~`), so the list came from a file other than the checkout's own. Which one is not established; the design does not depend on it, because section 4 stops loading local settings by accident from anywhere |

The verified failure, in one line: every seat starts where the daemon was
started, and the harness then decides trust, settings and MCP servers from
that accident.

## 2. The rule

Before any launch, marvel resolves a **bootstrap plan** for the seat from
what it already holds (workspace, team, role, runtime, permissions), applies
it, records it on the session, and launches. If a step the harness needs
cannot be done, marvel does not launch (section 6). A seat never meets a
first-run screen that nobody is there to answer.

One new optional adapter interface carries the per-harness part:

```go
// Bootstrapper prepares harness first-run state for a resolved directory.
type Bootstrapper interface {
    Bootstrap(ctx *LaunchContext, dir string) (BootstrapResult, error)
}
```

`BootstrapResult` names what was checked or written (for claude: the trust
read, the login check, the MCP projection), the settings sources passed, and
the config file read. An error with
`ErrBootstrapRefused` is the refusal path. An adapter that does not implement
it gets placement and nothing else, which is correct for a harness with no
first-run state.

## 3. The working directory

Resolution, first match wins:

1. `role.workdir`, `team.workdir`, `workspace.root`, as #255 decisions 1 and 2
   define them (g71ad). A declared directory must exist; marvel does not
   create directories in the operator's tree.
2. `marvel run --workdir`, or the caller's cwd made absolute (#255 decision 8).
3. Otherwise a **managed directory**: `<StateDir>/seats/<workspace>/<team>/<role>`,
   created `0700` by marvel, per role and stable across restarts, so a
   restart lands where its predecessor worked. It holds nothing marvel did
   not write. A role with more than one replica shares the directory and its
   trust entry; that is fine for trust, but two interactive seats there also
   share any project-scoped state the harness writes into it.

**Existing root-less records keep their place (the legacy stamp).** Today a
team with no `workspace.root` and no `workdir` places its seats in the
daemon's cwd, which for the fleet is the orc root. Every current fleet manifest
on kinu is that shape (`~/.marvel/manifests`, 11 of 11 `.toml` files carry no
`root` or `workdir` key; the reviewer reports the same for mokuzai, where some
teams were reconstructed from the live daemon and have no file at all). Rule 3
reads the daemon's persisted team record, not the file, so ordering manifest
edits before SB-1 cannot prevent the move: the first respawn after the SB-1
daemon starts would relocate those seats whatever the files say. The fix is in
code:

- **Stamp once, as the store's v1 to v2 migration.** SB-1 bumps
  `boltSchemaVersion` from 1 to 2 (`internal/api/bolt.go:64`); no separate
  marker. Today `Open` fails fast on an older on-disk version ("migration not
  implemented", `bolt.go:126-129`). SB-1 replaces that branch with the
  migration: inside the same `db.Update` transaction, before the daemon
  serves anything, every team record that resolves no root is stamped
  `WorkDir = <the daemon's cwd at that start>` and `WorkDirSource = legacy`,
  and the schema version is written as 2. It commits whole or not at all. One
  `placement.legacy-stamped` event per team is emitted after the commit.
- **What it covers.** Every record in a v1 store, whenever it was written:
  a team applied by the old daemon after the SB-1 binary was installed but
  before the daemon restarted is still a v1 record and is stamped too. There
  is no window where a root-less record escapes the pass.
- **It freezes one cwd.** The stamp records the cwd of the first SB-1 start
  and never re-reads it. Starting that first time from a directory other than
  the orc root would pin the fleet there; the SB-1 release note says to start
  it from where the seats run today.
- **The migration takes its own backup.** The old binary refuses a newer
  store ("on-disk schema version 2 is newer than binary's 1",
  `bolt.go:120-123`), and `marvel upgrade` deletes the previous binary once
  the new one is installed (`internal/upgrade/upgrade.go:328`), so nothing
  else keeps a way back. SB-1 therefore owns the backup:
  - **When:** in the migrating `Open`, before the stamp pass, and only when
    the on-disk version is 1.
  - **How:** a read transaction's `tx.WriteTo` writes a consistent copy to
    `<store>.v1.bak`, created `0600`, then fsync. If the backup cannot be
    written, `Open` fails and the store stays v1; the daemon never migrates
    without it. An existing `<store>.v1.bak` is never overwritten (the
    no-deletion convention). The daemon refuses to start and says how to
    recover: "`<store>.v1.bak` exists beside a v1 store: a previous
    migration was interrupted, or the file is a stale backup. Move it aside
    and restart." The file is not trusted either way, since a crash may
    have left it partly written; the operator keeps it until the new
    migration's backup exists.
  - **Retention:** marvel never removes `<store>.v1.bak`. It holds the same
    records as the store (hence `0600`) and is the store's size. The
    operator deletes it once SB-1 is confirmed and no rollback is wanted.
  - **Rollback, by the operator:** stop the daemon; install the prior
    release binary from its tag (the upgrade removed the local copy); move
    `<store>.v1.bak` over `<store>`; start the daemon. Records applied after
    the upgrade are lost by design and are re-applied from their manifests.
  - The release note for SB-1 names the backup path, these steps, the
    interrupted-migration recovery line above, and the retention rule.
- **Managed applies only to records created after SB-1.** A new team with no
  root gets rule 3. A re-apply of a legacy-stamped team that still names no
  root keeps the stamp, and the apply output says so ("placement: legacy
  daemon cwd; declare a root"). A re-apply that declares a root replaces the
  stamp.
- **SB-7a becomes cleanup, not a precondition.** Each root-less fleet manifest
  is edited to declare its root **and re-applied on each cluster that runs
  it**; a team with no file is re-applied from a manifest written for it.
  Until then its seats stay where they are.

The daemon's own directory is never a result for a record created after SB-1;
a legacy stamp is the one exception, and it names itself. The tmux session itself is
created with `new-session -c <StateDir>/seats`, so even the base pane is off
the daemon's cwd, and `new-window -c <dir>` places each seat (the spawn
wiring in aae-orc-5as2e, extended with the managed fallback).

The session records `WorkDir` and `WorkDirSource` (`role`, `team`, `root`,
`run`, `managed`, `legacy`).

## 4. Which settings apply, and whose

Every launch tells the harness which settings sources to load, so what
applies is a declaration rather than a consequence of the directory. For a
bare claude command marvel adds the flag; for a wrapper the wrapper passes it
(section 4a, SB-7).

For Claude Code the sources are managed policy (always, by the harness),
`user` (the operator's own `~/.claude/settings.json`), `project` (the
workdir's tracked `.claude/settings.json`), `local` (the workdir's untracked
`.claude/settings.local.json`), and marvel's own `--settings` projection
(policy, statusline feed).

| Seat placed in | Default `--setting-sources` | Why |
|---|---|---|
| A declared workdir | `user,project` | the operator named the project; its tracked settings are reviewed |
| A managed directory | `user` | there is no project |
| Any | never `local` unless declared | a local file is private and unreviewed, and is the file that rode in on the incident |

A role may declare `settings_sources = ["user", "project", "local"]` to opt
in, and the declaration is recorded on the session. The value is passed as
given; marvel does not interpret settings files (the policy projection rule
stands).

**MCP servers.** On kinu a seat placed outside the orc root loses
`director-mcp`, because Claude Code files it under that path. On mokuzai the
role command already declares it. The rule covers both:

- **The command is a wrapper: marvel projects nothing** (section 4a).
- **The role declares its MCP servers: marvel projects nothing.** If the role's
  command or args carry `--mcp-config` or `--strict-mcp-config`, the role's
  declaration wins whole. marvel adds no second `--mcp-config` and does not
  merge. Merging two declarations is a second source to reconcile (R-84's
  one-source rule), and the role author chose strict on purpose. The session
  records `mcp: role-declared`.
- **Otherwise marvel projects director.** It writes a per-session file,
  `0600` (as `writeSeedFile` does, `internal/runtime/codex_home.go:188`), and
  passes it as `--mcp-config`. The command and args come from the cluster
  config (`claude.director_mcp: {command, args}` in `~/.marvel/config.yaml`),
  not from Claude Code's own file, because the entry there is keyed by project
  path and has no single operator-level value to read. With no configured
  entry, nothing is projected, and the session logs it and records `mcp: none`.
- **Unless the operator's config already gives that directory the server
  (SB-4F).** A seat now runs under the operator's own config, where Claude
  Code also loads `projects.<realpath>.mcpServers` for the seat's directory.
  If that entry names the same server as the projection (on kinu, the orc
  root's `director-mcp`), marvel projects nothing and records
  `mcp: operator-config`. marvel reads the server names there and nothing
  else (section 5a). Which of two same-named servers Claude Code keeps was
  not checked, so marvel never creates the pair (ruling 7).
- **No env in the file.** Only `command` and `args` are written. That is
  enough only if Claude Code hands its process environment, including the
  `DIRECTOR_NATS_*` names marvel injects
  (`internal/runtime/adapter.go:351-355`), to a stdio MCP child. The mokuzai
  seats do not show it: their `--mcp-config` carries only `command`, and their
  `director-mcp` children authenticate, but the `director-mcp-seat` launcher
  defers to the environment and falls back to the seat's own director
  credentials when the environment has none
  (`director/probe/nats-phase-0/director-mcp-seat:29`). Authentication
  therefore does not prove inheritance. The reviewer corrected this evidence;
  SB-0 line 11 tests inheritance directly.
- `args` are copied verbatim, so a secret an operator puts in the command's
  args would be written to that file. Today's command has none.

Whether a projected seat also gets `--strict-mcp-config` is ruling 2. A role
that already declares it keeps it either way.

**Rollout cost, stated.** Today's fleet seats run in the orc root and load
its `local` file, which carries a long allow list. Moving them to `user,project`
changes what they may do without asking. The fleet manifests declare
`settings_sources` explicitly in the same change (SB-7), so no running role
changes behavior by accident in either direction.

## 4a. Will this break director? (operator question, 2026-09-30)

The operator accepted the default settings sources and asked, verbatim: "will
it bread [sic] director? could we do a probe before wider rollout to see what stops
working?". The answer depends on how a seat launches.

**Today's fleet seats: the director wiring does not change.** Every arcaven
role's command is a wrapper (`cast-aae.sh`, then director's
`sim/twin/cast-launch.sh`), and the wrapper already passes
`--strict-mcp-config --mcp-config <json>` itself (`cast-launch.sh:158-159`,
`:262`). marvel cannot see flags inside a wrapper, so the rule for wrappers
is the one the prompt already follows (aae-orc-1vq6z): **for a wrapper, marvel
projects no MCP config and adds no `--setting-sources`**; the wrapper owns its
command line. The session records `mcp: wrapper`. SB-7 adds
`--setting-sources` inside `cast-launch.sh` (a director PR), where it is
visible and reviewed with the rest of that command line.

**A bare-claude seat that marvel projects director into** (the step 0 shape):

| Director function | Needs | From the projection? | Verdict |
|---|---|---|---|
| Local send and receive | `DIRECTOR_AGENT_ID`, `DIRECTOR_WORKSPACE`, `DIRECTOR_TEAM`, `NATS_URL`, and broker credentials when the broker requires them | marvel's `baseEnv` sets all of these (`internal/runtime/adapter.go:323-358`); the file carries `command` and `args` only | works **if** Claude Code hands its process environment to a stdio MCP child. **Not shown yet**: the mokuzai seats authenticate, but `director-mcp-seat` falls back to the seat's own credentials, so that does not prove inheritance; on kinu my own seat's environment carries no broker credentials. SB-0 line 11 tests it |
| `role://` delivery | `DIRECTOR_ROLE`: the shim subscribes to the role subject only when it is set (director `probe/nats-phase-0/director-mcp/bus.go:234-235`) | **not set by marvel today**; `cast-launch.sh` sets it | **breaks**: messages to `role://<team>/<role>` would not reach the seat. SB-4F adds `DIRECTOR_ROLE` to `baseEnv` |
| Placement of seats whose manifest names no root | the orc root as cwd, so the project `CLAUDE.md` and the tracked settings load | rule 3 would give a managed directory | **stays**: SB-1 stamps each existing root-less record `legacy` with the daemon's cwd at first start, so no seat moves; only teams created after SB-1 get a managed directory. SB-0 line 12 checks both a never re-applied record and a re-applied one |
| Global address | `DIRECTOR_GLOBAL_DOMAIN`, `DIRECTOR_CLUSTER`, `DIRECTOR_GLOBAL_ROLE`; unset means the global tier is off (`global.go:78`) | not set by marvel (`cast-aae.sh:14-17` says marvel does not pass them through) | off. Correct for most roles (R-94 gives global addresses to supervisors only). A supervisor keeps the wrapper until marvel carries the global levers, which is out of scope here |

So nothing breaks for seats launched as they are today. A bare-claude seat
works for local traffic once `DIRECTOR_ROLE` is injected, subject to the one
unverified inheritance claim, and the rollout probe checks it first.

**Rollout probe (SB-0), one seat before any wider rollout.** The builder
applies one throwaway bare-claude seat with the SB-1, SB-2 and SB-4F behaviour, in its
own workspace, and records pass or fail for each line. Nothing wider rolls
out until every line passes or is ruled acceptable:

1. Director receive: a message sent to the seat's `agent://` address arrives
   through `wait_for_message`.
2. Director send: the seat sends to a known seat, which confirms receipt.
3. `role://` delivery: a message to `role://<team>/<role>` arrives.
4. Presence: the seat appears once in `list_roster`.
5. Other MCP servers: the operator's user-scope servers still load (strict
   off), or are absent when strict is declared, as intended.
6. The local allow list: a tool call the orc `local` settings would have
   pre-approved now prompts or is refused per the role's permission mode;
   record which, since that is the expected change.
7. Hooks: marvel's statusline feed (`ctx-forward`) and any user-scope hooks
   still fire; project hooks load only under `project`.
8. Skills and commands: user-scope skills load; project skills load only
   under `project`.
9. Login: under the operator's config the seat reaches its prompt logged
   in, and the pre-launch `claude auth status` exited 0 (SB-3 r2 measured 0
   and 1 on 2.1.288; record the version).
10. Placement and trust: the pane's cwd is the managed or declared
    directory, and no trust dialog appears. A second seat in a directory the
    operator has not trusted is refused `trust-absent`, naming the realpath,
    and is never launched.
11. Env inheritance, by what the fallback would change: the seat appears in
    `list_roster` under its session name, not the fallback id
    `director-seat`, and the broker's connection list shows its
    `director-mcp` child connected as the session's own broker user, not the
    seat user `director` (the launcher's own diagnostic,
    `director-mcp-seat:48-49`). Either fallback sign is a fail. The seat's
    environment is not altered for the test.
12. Placement of existing records, two cases. (a) A team applied before SB-1
    and never re-applied: after the SB-1 daemon starts and the seat respawns,
    its record reads `WorkDirSource = legacy`, the pane's cwd is the orc
    root, and the project `CLAUDE.md` loads (the seat can name a rule from
    it). (b) The same team re-applied with a declared root: the pane's cwd is
    that root, `WorkDirSource = root`, and `CLAUDE.md` still loads.
13. Trust by ancestor, operator-run by hand with marvel not involved: open
    Claude Code in a new child of a trusted directory that has no key of its
    own (`<orc root>/sb0-child`) and record whether the trust dialog
    appears. Under ruling 5's default marvel refuses such a seat either way;
    this record decides whether an ancestor rule is offered. On kinu, 9
    directories under the trusted orc root carry their own untrusted entry
    today, so the answer matters.
14. MCP: a seat in the orc root shows one director server, not two, and
    records `mcp: operator-config`; a seat in a declared directory with no
    per-path entry records `mcp: director (projected)`.

The probe reports what broke, as a list, before SB-7 touches any fleet
manifest.

## 5. First-run state, per harness

| Harness | First-run state | What marvel does |
|---|---|---|
| claude, interactive | folder trust per path; onboarding flags; per-path MCP; the login | runs it under the operator's own config; reads, by key name, whether the realpath of the resolved workdir is trusted, and refuses if not; checks the login by exit status; writes none of it (section 5a) |
| claude, headless | none shown (`-p` skips the dialog) | placement and settings sources only |
| codex | trust per path in `config.toml` | existing seed (#308, #359); `Untrusted` becomes the resolved workdir instead of `os.Getwd()` |
| opencode, generic, forestage | none known | placement only; recorded as `none` |

**Which config a claude seat uses: the operator's own.** marvel gives an
interactive claude seat no private config home. The seat uses the file Claude
Code would use for the operator: `$CLAUDE_CONFIG_DIR/.claude.json` when the
seat's environment carries `CLAUDE_CONFIG_DIR`, else `.claude.json` in the
daemon user's home. marvel seeds nothing there, links nothing for it, and
writes nothing to it. The first form of this design gave each seat a private
home seeded with trust and onboarding; SB-3 r2 showed a private
`CLAUDE_CONFIG_DIR` does not carry the login, so that mechanism is dropped
(section 5a).

**The login is checked, never made.** Before launch marvel runs
`claude auth status` under the config the seat will use and reads only its
exit status; stdout and stderr are discarded unread, since they carry account
identity. SB-3 r2 measured the contract on 2.1.288: 0 when logged in, 1 when
not. Anything but 0 is `not-logged-in` (section 6). marvel never logs in for a
seat and never launches one into a login screen.

**A link must stay a link (codex).** codex links its `auth.json` into a
private home (`LinkIn`) rather than copying it. A harness that refreshes a
credential by writing a temp file and renaming it over the path would replace
the symlink with a regular file, and a copy would then live under `StateDir`.
codex is not known to do or not do this. So at every spawn that reuses a
home, `prepareSessionHome` checks, for every adapter that links, that each
`LinkIn` entry is still a symlink to the operator's file. If one is not, the
session is refused with reason `link-replaced`, naming the path, and marvel
deletes nothing (the no-deletion convention): the operator removes the copy
and re-applies. `Session.Bootstrap` records the check. The same check also
runs when a session exits or is reaped, so a replaced link is reported (event
`bootstrap.link-replaced`, naming the path) when it happens rather than at
the next spawn; it deletes nothing there either. Under SB-4F claude links
nothing, so the check never fires for a claude seat.

**Trust stays the operator's act.** marvel reads whether the resolved workdir
is trusted and refuses if it is not; it never writes the key. The
settings-sources rule (section 4) still matters: trusting a directory lets its
project settings load, and `local` loads only by declaration.

## 5a. SB-4F: the redesign after SB-3 r2 (2026-10-02)

**The r2 result.** Operator-run on kinu, Claude Code 2.1.288, in a fresh
`0700` temp tree that the run removed at the end. I read the operator's
terminal record of the run; it is not published.

| Case | `claude auth status` exit | `loggedIn` |
|---|---|---|
| the operator's own config | 0 | true |
| private `CLAUDE_CONFIG_DIR`, unseeded | 1 | false |
| private, seeded with `hasCompletedOnboarding` and the trust key at the workdir's realpath | 1 | false |

Then an interactive `claude --setting-sources user` under the seeded private
home, in the probe workdir, captured after 12 seconds: the login screen was
on the pane (1 match), with no trust dialog, no theme screen, and no prompt
footer (0 matches each). The seeded `projects` key was the workdir's realpath
(`/private/tmp/...`, not `/tmp/...`), and Claude Code added its own keys to
the private file (`firstStartTime`, `autoUpdates`, `machineID`, `userID` and
others; names read only).

The reading, as director judged it: outcome C, fail. Seeding the trust key
at the realpath does remove the trust dialog, but a private
`CLAUDE_CONFIG_DIR` does not carry the login. It is not outcome D: the status
check and the screen agree. Per the probe's outcome table, SB-4F replaces
SB-4.

**The decision.** marvel stops giving claude seats a private config. Seats
run under the operator's own config. marvel only reads, by key name, whether
the realpath of the seat's workdir is trusted; if it is not, marvel refuses
the spawn and names the directory. It never writes the trust key. F1, writing
the key into the operator's file, stays rejected: every running Claude Code
rewrites that file, so a marvel write would race the fleet's, and it would
change the operator's own interactive trust as a side effect.

**What marvel reads, and never writes (the custody boundary, ADR-009).**

Reads, from the config file section 5 names:
- `projects.<realpath>.hasTrustDialogAccepted`, where the realpath is the
  resolved workdir through `filepath.EvalSymlinks`, because Claude Code keys
  by realpath (r2). It is decoded into a struct that names `projects` and
  that one field, so no other value is held past the parse.
- The server names under `projects.<realpath>.mcpServers`, for the duplicate
  rule in section 4 (ruling 7). Names only; the entries' commands, args and
  env are not decoded.
- The exit status of `claude auth status`, run under the seat's config.
  Output discarded unread.

Records: the config path, the realpath, the trust boolean, the exit status.

Never: writes, renames, or locks the operator's config; opens the credential
store (the macOS login keychain, or `.credentials.json` on Linux); logs in or
runs Claude Code interactively on a seat's behalf; copies any part of the
config elsewhere; logs or persists a value from it other than the trust
boolean.

The login is bearer authority at the vendor, and under SB-4F it stays where
the operator's own login put it. marvel holds no credential, links none, and
creates no home where a login could write one. That is less than SB-4 held:
SB-4 linked the credential under `StateDir` and needed the `link-replaced`
guard to stop a copy forming there.

**The cost, stated.** Seats share the operator's config, as today's fleet
seats already do. Per-path state Claude Code writes for a seat's directory
(trust, per-project MCP servers, history) is the operator's, and the
operator's own interactive sessions see it. And a directory needs trusting
once, by the operator, before marvel launches an interactive seat there.

**Managed directories.** A managed directory (section 3, rule 3) is new, so
it is never trusted. Under SB-4F a new root-less team running interactive
claude is refused `trust-absent` on its first spawn, naming
`<StateDir>/seats/<ws>/<team>/<role>`. The operator trusts it once by opening
Claude Code there and accepting; the path is stable across restarts, so that
is once per role per host (ruling 6).

**The re-scope.**

| Item | Before | Under SB-4F |
|---|---|---|
| SB-3 | probe | done 2026-10-02, red (outcome C) |
| SB-4 | private home, trust and onboarding seed, MCP projection, `DIRECTOR_ROLE` | **dropped**. Gone: the private `CLAUDE_CONFIG_DIR`, the trust and onboarding seed and its golden file, the claude credential links, and the claude half of `link-replaced`. Moved to SB-4F: the MCP projection and `DIRECTOR_ROLE` in `baseEnv` |
| SB-4F | F2 alone | the claude `Bootstrapper`: the workdir's realpath; the trust read and refusal; the login check by exit status; the MCP projection with the duplicate rule (section 4); `DIRECTOR_ROLE` in `baseEnv` |
| SB-5 | codex seed uses the resolved workdir | unchanged |
| SB-6 | refusal path, record, events, `describe` | reasons change: no seed step, so no seed failure; adds `trust-absent` and `config-unreadable`; `not-logged-in` and `link-replaced` (codex) stay. `Session.Bootstrap` records the config read and the trust result, not a home |
| SB-0 | rollout probe | runs SB-1, SB-2 and SB-4F; lines 9 and 10 revised, line 13 replaced, line 14 added (section 4a) |
| SB-7 | fleet manifests declare `settings_sources` | unchanged; gated on SB-0 with SB-4F. marvel does not trust-check a wrapped role: `cast-launch.sh` changes to its own `TWIN_CWD` (`:282`), so marvel's workdir is not where that claude runs (ruling 8) |

SB-1 and SB-2 are unchanged; SB-2 landed as #467.

## 6. Refuse, and say why

When a bootstrap step the harness needs fails (the declared workdir is
missing, the managed directory cannot be created, the seat would come up
logged out, a codex `LinkIn` entry is no longer a link, the workdir's realpath
is not trusted in the operator's config, or that config cannot be read):

- the session is not launched. It is recorded `failed` with condition
  `bootstrap-refused` and the reason, and it is **not** charged to the restart
  policy: nothing crashed, so `max_restarts` and `restart_policy = never`
  never freeze a role over it;
- the reconciler does not respawn it until the manifest is re-applied or the
  operator runs `marvel reset-health`, so a refusal does not loop;
- `session.bootstrap-refused` is emitted once, with the session, adapter,
  directory and missing step;
- `describe session` shows the same.
- a `trust-absent` refusal names the realpath and the one-time step: open
  Claude Code there and accept the dialog, then re-apply.
- a config that fails to parse (a running Claude Code may be rewriting it) is
  read once more after one second; a second failure is `config-unreadable`
  (ruling 10).

A best-effort step (the MCP projection when the operator has no director
server configured) logs and continues, as the codex seed does today.

## 7. What this amends in #255

- "Creating the directory": #255 left it out. marvel now creates **managed**
  directories only; declared ones must still exist.
- "Trust itself. The harness decides trust from its own store": still so.
  marvel now reads trust for the directory it placed an interactive claude
  seat in and refuses the spawn when it is absent. It never writes trust and
  claims nothing about other directories.
- Decision 3's refusal of a manifest with no `workspace.root`: unchanged for
  `marvel work`, which always fills it. A raw API apply with no root, for a
  team created after SB-1, gets a managed directory instead of the daemon's
  cwd. A record that existed before SB-1 keeps the daemon's cwd as a `legacy`
  stamp (section 3).

## 8. What the session records

`Session.Bootstrap`, persisted, shown by `describe session`:

```
Bootstrap: workdir <StateDir>/seats/<ws>/<team>/<role> (managed)
           settings user   mcp director (projected)
           config <the .claude.json read>   trust yes   login ok
```

`get sessions` gains nothing (the table stays narrow; #255's `WORKDIR` column
is its own ticket). The event ring gets `session.bootstrapped` once per
launch.

## 9. Tests (red first on 9009e89)

1. A role with no workdir under a daemon started in a directory holding
   `.claude/settings.local.json`: the pane's cwd is the managed directory, and
   the command line carries `--setting-sources user`.
2. A declared workdir: the pane starts there and the command carries
   `--setting-sources user,project`; `local` appears only when declared.
3. The managed directory is created `0700`, is the same path after a
   restart, and nothing in it was copied from elsewhere.
4. The trust read. With `projects.<realpath>.hasTrustDialogAccepted` true
   the seat launches; false or absent, it is refused `trust-absent`, naming
   the realpath, and never launched. A workdir reached through a symlink is
   looked up by its realpath. With `CLAUDE_CONFIG_DIR` in the seat's env,
   that directory's `.claude.json` is read instead. A config that fails to
   parse is read once more, then refused `config-unreadable`. The config
   file's bytes and mtime are unchanged after every case.
5. The codex seed declares the resolved workdir untrusted, not the daemon cwd.
6. A missing declared workdir: not launched, `bootstrap-refused`, restart
   count 0, not frozen under `restart_policy = never`, one event, no respawn
   until re-apply.
7. The projected MCP file carries the configured director command and args,
   no env, and is `0600`. A role whose command already declares
   `--mcp-config` or `--strict-mcp-config` gets no projection and no second
   flag, and its declaration reaches the command line unchanged.
8. Custody. A fake `claude auth status` that exits 1 refuses
   `not-logged-in` and nothing launches; its output is never read or logged;
   the fake's argv shows only `auth status`. No file under `StateDir` is
   created for a claude seat's config or credential. For codex, a `LinkIn`
   entry replaced by a regular file is refused `link-replaced` and nothing is
   deleted, and a link replaced during a session is reported at its exit.
9. A headless claude run gets placement and settings sources, and no trust
   read or login check.
10. The legacy stamp: a v1 store holds a team with no root; the SB-1 daemon
    starts from directory X, stamps it `WorkDir = X`,
    `WorkDirSource = legacy` inside `Open`'s transaction, writes schema
    version 2, and emits one `placement.legacy-stamped`; the respawned seat's
    pane cwd is X. A failure injected mid-pass leaves the store at v1 with no
    record stamped. Restarting the daemon from directory Y leaves the stamp at
    X and emits nothing. The v1 binary opening the v2 store refuses to load.
    Backup: after the migration `<store>.v1.bak` exists, is `0600`, opens as
    a v1 store, and holds exactly the pre-migration records; with the backup
    write forced to fail, `Open` fails and the store is still v1 with nothing
    stamped; with a `<store>.v1.bak` already present, `Open` refuses and the
    existing file is unchanged. Restart after a mid-pass failure (backup
    written, stamp not committed): `Open` refuses with the interrupted-
    migration message naming the file; after the file is moved aside, the
    next start takes a fresh backup and migrates. Rollback: the v1 binary opens the restored
    backup and serves the original, unstamped records. A new
    team with no root, applied after the stamp, gets a managed directory. A
    re-apply of the stamped team with no root keeps X and reports it; with a
    declared root, the stamp is replaced.
11. The MCP duplicate rule. A config whose per-path entry for the workdir
    names the director server: no `--mcp-config` is added and the session
    records `mcp: operator-config`. Without that entry: the projection is
    added. Only server names are decoded.

## 10. Edits, in order (none made by this PR)

| # | Edit | Depends on |
|---|---|---|
| SB-1 | Managed directory fallback for records created after SB-1; `boltSchemaVersion` 2 with the `legacy` stamp as the v1 to v2 migration in `Open`, preceded by the `<store>.v1.bak` backup; `new-session -c`; `WorkDirSource` | aae-orc-5as2e |
| SB-7a | Cleanup: every fleet manifest with no `workspace.root` or `workdir` declares one (the orc root, where its seats run today) and is re-applied on each cluster that runs it; a team with no file gets a manifest | SB-1 |
| SB-2 | Explicit `--setting-sources`, `settings_sources` on the role, the defaults in section 4 | none |
| SB-3 | Probe: does a private `CLAUDE_CONFIG_DIR` keep the login (operator-run). **Done 2026-10-02, red** (section 5a) | none |
| SB-4 | **Dropped** (section 5a) | |
| SB-4F | claude `Bootstrapper` under the operator's config: realpath, trust read and refusal, login check by exit status, MCP projection with the duplicate rule, `DIRECTOR_ROLE` in `baseEnv` | SB-1 |
| SB-5 | codex seed uses the resolved workdir | SB-1 |
| SB-6 | Refusal path (with `trust-absent` and `config-unreadable`), `Session.Bootstrap`, events, `describe` | SB-1 |
| SB-0 | Rollout probe: one bare-claude seat, the section 4a checklist, a written pass or fail per line | SB-1, SB-2, SB-4F |
| SB-7 | Fleet and example manifests declare `settings_sources` (for wrapped roles, inside `cast-launch.sh`, a director PR), and carry forward the roles' existing `--mcp-config` and `--strict-mcp-config` flags unchanged | SB-2, SB-0 passed |

The shortest path to an unblocked step 0 is SB-1, then SB-4F; SB-2 (#467) and
SB-3 are done. SB-1 carries its own guard (the `legacy` stamp), so no host state has to
change first.

## 11. Rulings needed

1. **Default settings sources** (section 4). Default offered: `user,project`
   for a declared workdir, `user` for a managed one, `local` only by
   declaration.
2. **`--strict-mcp-config` for projected seats.** Default offered: off for a
   seat marvel projects director into, so it keeps the operator's user-scope
   servers. A role that declares strict (as the mokuzai seats do) keeps it; the
   default never overrides a role.
3. **The #255 amendments in section 7.** Default offered: adopt as written.
4. **Adopt SB-4F** (section 5a). Default offered: yes. Seats run under the
   operator's config; marvel reads trust and the login status and writes
   neither.
5. **Trust by exact path or by ancestor.** Default offered: the exact
   realpath key only. It can refuse a directory Claude Code would accept,
   never the reverse. SB-0 line 13 records whether Claude Code honours an
   ancestor's trust; if it does, an ancestor rule can be offered then.
6. **Managed directories under SB-4F** (section 5a). Default offered: refuse
   `trust-absent` naming the managed directory, and the operator trusts it
   once. Alternative: refuse managed placement for interactive claude
   outright and require a declared workdir.
7. **The MCP duplicate rule** (section 4). Default offered: when the
   operator's per-path entry for the workdir names the director server, do
   not project; otherwise project as before.
8. **Wrapped claude roles.** Default offered: marvel does not check trust for
   them and records `trust: wrapper`; the wrapper owns its directory.
9. **The login check before each interactive claude spawn.** Default
   offered: on, every spawn, exit status only; 0 launches and anything else
   refuses `not-logged-in`.
10. **An unreadable config.** Default offered: read once more after one
    second, then refuse `config-unreadable`.
