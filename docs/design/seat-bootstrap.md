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
- Revised again 2026-10-03 (r5) after the operator's rulings on section 11:
  SB-4M (marvel manages trust actively, section 5a) replaces SB-4F.

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
read or write, the login check, the MCP projection), the settings sources passed, and
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
  (SB-4M).** A seat now runs under the operator's own config, where Claude
  Code also loads `projects.<realpath>.mcpServers` for the seat's directory.
  If that entry names the same server as the projection (on kinu, the orc
  root's `director-mcp`), marvel projects nothing and records
  `mcp: operator-config`. marvel reads the server names there and nothing
  else (section 5a). Which of two same-named servers Claude Code keeps was
  not checked, so marvel never creates the pair (ruling 7, accepted).
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
| `role://` delivery | `DIRECTOR_ROLE`: the shim subscribes to the role subject only when it is set (director `probe/nats-phase-0/director-mcp/bus.go:234-235`) | **not set by marvel today**; `cast-launch.sh` sets it | **breaks**: messages to `role://<team>/<role>` would not reach the seat. SB-4M adds `DIRECTOR_ROLE` to `baseEnv` |
| Placement of seats whose manifest names no root | the orc root as cwd, so the project `CLAUDE.md` and the tracked settings load | rule 3 would give a managed directory | **stays**: SB-1 stamps each existing root-less record `legacy` with the daemon's cwd at first start, so no seat moves; only teams created after SB-1 get a managed directory. SB-0 line 12 checks both a never re-applied record and a re-applied one |
| Global address | `DIRECTOR_GLOBAL_DOMAIN`, `DIRECTOR_CLUSTER`, `DIRECTOR_GLOBAL_ROLE`; unset means the global tier is off (`global.go:78`) | not set by marvel (`cast-aae.sh:14-17` says marvel does not pass them through) | off. Correct for most roles (R-94 gives global addresses to supervisors only). A supervisor keeps the wrapper until marvel carries the global levers, which is out of scope here |

So nothing breaks for seats launched as they are today. A bare-claude seat
works for local traffic once `DIRECTOR_ROLE` is injected, subject to the one
unverified inheritance claim, and the rollout probe checks it first.

**Rollout probe (SB-0), one seat before any wider rollout.** The builder
applies one throwaway bare-claude seat with the SB-1, SB-2 and SB-4M behaviour, in its
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
   and 1 on 2.1.288; record the version). Under `warn`, a seat whose check
   fails comes up at its login screen with `session.login-unverified`.
10. Placement and trust: the pane's cwd is the managed or declared
    directory, and no trust dialog appears. For a directory that had no key,
    the operator's config now carries `projects.<realpath>.hasTrustDialogAccepted`
    true, the ledger names the grant, and every other key in the file is
    unchanged (compared by value, before and after). Under `require` the same
    seat is refused `trust-absent` and nothing is written.
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
    appears. marvel's default, `exact`, writes the child's own key either
    way; this record decides whether `trust_match = "ancestor"` is safe to
    use. On kinu, 9
    directories under the trusted orc root carry their own untrusted entry
    today, so the answer matters.
14. MCP: a seat in the orc root shows one director server, not two, and
    records `mcp: operator-config`; a seat in a declared directory with no
    per-path entry records `mcp: director (projected)`.
15. What exit 0 means, operator-run by hand: (a) with a valid login,
    `claude auth status` exits 0; (b) with the stored token expired (or the
    seat's clock past its expiry), record whether it still exits 0 (a
    present login rather than a valid one), whether it refreshes, and
    whether the refresh writes the credential store and reaches the
    network. If exit 0 means only "present", the check catches a missing
    login and not an expired one, and the doc says so.
16. The lock and lost writes, operator-run: with an interactive Claude Code
    open in another directory, spawn three seats into new directories.
    Record whether Claude Code creates `.claude.json.lock` around its own
    writes, whether any grant needed a retry or ended `trust-write-lost`, and
    whether any seat showed the trust dialog (the residual in section 5a).

The probe reports what broke, as a list, before SB-7 touches any fleet
manifest.

## 5. First-run state, per harness

| Harness | First-run state | What marvel does |
|---|---|---|
| claude, interactive | folder trust per path; onboarding flags; per-path MCP; the login | manages trust for the seat's directory (writes the one key when it is absent, under Claude Code's lock), delivers settings and MCP as per-session files, checks the login in the mode the role sets, and never moves or touches the login (section 5a) |
| claude, headless | none shown (`-p` skips the dialog) | placement and settings sources only |
| codex | trust per path in `config.toml` | existing seed (#308, #359); `Untrusted` becomes the resolved workdir instead of `os.Getwd()` |
| opencode, generic, forestage | none known | placement only; recorded as `none` |

**Which config a claude seat uses: the operator's own.** On a macOS host a
seat uses the file Claude Code would use for the operator:
`$CLAUDE_CONFIG_DIR/.claude.json` when the seat's environment carries
`CLAUDE_CONFIG_DIR`, else `.claude.json` in the daemon user's home. SB-3 r2
showed that a private `CLAUDE_CONFIG_DIR` there loses the login, so the first
form of this design (a private home per seat) is not the macOS mechanism.
marvel manages the keys it needs inside that file (section 5a) rather than
giving the seat a copy of it.

**The login check.** Before launch marvel runs `claude auth status` under
the config the seat will use and reads only its exit status; stdout and
stderr go to `/dev/null` unread, since they carry account identity. SB-3 r2
measured the contract on 2.1.288: 0 when logged in, 1 when not. What a
nonzero result does depends on the role's `login_check` mode (section 5a):
refuse, warn, or off. The call has a 10-second bound, the figure codex's one
call uses (`codexTrustTimeout`), and it bounds the whole process tree, not
only the child: `claude auth status` forks the `security` CLI to read the
keychain (the reviewer's PATH stand-in was called twice as
`find-generic-password`), so a keychain prompt or hang lives in a
grandchild. The command runs with `Setpgid`, and on timeout marvel kills the
group (`-pgid`), only after a successful `Start` and only for a pid greater
than 1; it never calls `kill(0)` or `kill(-1)`. Its stdout and stderr go to
`/dev/null`, so `Wait` cannot block on a pipe a surviving grandchild still
holds. codex's call (`codex_home.go:232`) kills only its child and keeps a
pipe, so it is the figure's source, not the pattern's. The check always runs
the harness binary the new session will run (the resolved command), so a
harness upgrade is judged by the new binary, never by the old session's.

**Order: trust first, then the CLI.** marvel settles trust first (section
5a), and runs `claude auth status` only for a spawn that passed that step, so
a spawn refused for trust never causes a CLI write. A spawn that passed and
is logged out does cause one: the CLI writes and locks its own config
(measured by the reviewer on 2.1.286: `.claude.json`, a file under
`backups/`, and a `.claude.json.lock` directory). Any check that asks the CLI
has that cost, and it is no more than the seat itself writes a second later.
marvel never logs in for a seat.

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
the next spawn; it deletes nothing there either. On a macOS host claude
links nothing, so the check never fires for a claude seat there.

## 5a. SB-4M: marvel manages the seat's harness state (r5, 2026-10-03)

**History.** SB-3 r2 (below) ruled out a private config on macOS, and r4
answered with SB-4F: read trust, refuse when absent, write nothing. The
operator rejected that on 2026-10-03 (ruling 4, verbatim in section 11):
marvel should manage these files actively. SB-4M is the design that does,
without moving the login.

**The r2 result (a fact this design keeps).** Operator-run on kinu, Claude
Code 2.1.288, in a fresh `0700` temp tree that the run removed at the end. I
read the operator's terminal record of the run; it is not published.

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
others; names read only). Director judged it outcome C: the trust seed works,
and a private `CLAUDE_CONFIG_DIR` does not carry the login.

**What marvel manages, and by which path.** Each piece of harness state goes
by the path that delivers it without touching the login:

| State | Where Claude Code keeps it | Path marvel uses |
|---|---|---|
| settings and permissions | `--settings` and the settings sources | a per-session file (the policy projection, today) and `--setting-sources` (SB-2) |
| MCP servers | `--mcp-config`, or per-path entries in the config | a per-session file, with the duplicate rule (section 4) |
| folder trust | `projects.<realpath>.hasTrustDialogAccepted` in the config file only | **managed in place** in the operator's config, below |
| onboarding | top-level flags in the config file | not written: the operator's own config has already completed it |
| the login | the macOS login keychain; on Linux a file under the config directory (per Claude Code's layout, not verified here) | **never touched** |

Container injection and submounts are named delivery paths for the same
contract, not built here: marvel has no container placement yet
(`_kos/probes/brief-container-session-placement.md`). Where the login is a
file, as on Linux and in a container, a per-session config view (a private
directory with the trust key seeded and the credential mounted or linked,
which is SB-4's original shape) becomes possible. It needs its own r2-style
probe on Linux before it is offered.

**Managing trust in place.** For each interactive claude spawn whose role's
`trust` mode is `manage` (the default):

1. Resolve the workdir's realpath (`filepath.EvalSymlinks`; Claude Code keys
   by realpath, r2).
2. Read the key under the role's match rule (ruling 5): `exact` (the
   default) reads `projects.<realpath>.hasTrustDialogAccepted`; `ancestor`
   also accepts a trusted ancestor. If it is trusted, nothing is written.
3. Otherwise take Claude Code's own lock, the `.claude.json.lock` directory
   beside the file (created with `mkdir`, so taking it is atomic), with a
   5-second wait. marvel removes only a lock directory it created. If the
   lock is not free in time, the outcome is `config-locked`.
4. Re-read the file under the lock, set that one key (creating
   `projects.<realpath>` with only that field if it is absent), write a
   temp file in the same directory at `0600`, fsync it, and rename it over
   the config. Every other value is carried through as the bytes it had:
   the top level and `projects` decode as `map[string]json.RawMessage`, and
   only the one project entry is re-encoded. Key order may change; values do
   not.
5. Release the lock, read the file once more, and confirm the key. A
   running Claude Code that rewrites the file from an older copy can drop
   it; marvel retries steps 3 to 5 up to 3 times, then the outcome is
   `trust-write-lost`.
6. Record the grant in marvel's own ledger (path, time, and why: `managed`,
   `declared`, or `listed`), and emit `trust.granted`.

Each spawn re-asserts trust this way, so a key a running Claude Code dropped
between spawns comes back at the next one. Whether Claude Code itself takes
`.claude.json.lock` for its own writes is not verified; SB-0 line 16
measures it. If it does not, the lock still serializes marvel's own
spawns, and step 5 is what catches a lost write. A residual is stated: a
rewrite that lands after step 5 and before the seat's own startup read can
still show the dialog once. SB-0 line 16 measures how often.

The other modes: `require` refuses `trust-absent`, naming the realpath, and
never writes (the r4 behavior, kept as an option); `off` neither reads nor
writes (a headless role, or a wrapped one, ruling 8).

**Which directories marvel trusts (ruling 6).** marvel establishes trust
itself for every managed directory it creates (section 3, rule 3), and for
each seat's resolved workdir whatever its source, under `manage`. The user
can name more: `claude.trust_dirs` in the cluster config (trusted at daemon
start and re-asserted at each spawn), and per role a `trust` mode and a
`trust_match` that override the cluster defaults (`claude.trust` and
`claude.trust_match`). Whether a declared workdir, rather than only a managed
one, should default to `manage` is ruling 11.

**The login check modes (ruling 9).** Per role, `login_check`, with the
cluster default `claude.login_check`:

- `warn` (the default): a nonzero exit or a timeout launches the seat
  anyway, emits `session.login-unverified` with the reason, and shows it in
  `describe session`. A logged-out seat comes up at its login screen, where
  the operator can log in as they would by hand.
- `refuse`: a nonzero exit refuses `not-logged-in`; a timeout refuses
  `login-check-timeout`.
- `off`: no check.

**Login health in the handoff.** Under `warn` and `refuse`, a shift's
successor is not counted ready until its login check has returned 0. A
successor that launched under `warn` with a nonzero result is re-checked every
30 seconds while the shift waits; the predecessor keeps running, and the
shift's existing timeout rolls it back if the successor never passes. The
check runs the successor's harness binary, so a harness upgrade that breaks
the old session's auth never blocks the new session from starting, and a new
binary that cannot authenticate never replaces a working seat.

**What marvel reads and writes (the custody boundary, ADR-009).**

- Writes, to the operator's config: the one trust key per directory it
  trusts, under steps 3 to 5. Nothing else.
- Reads, for decisions: the trust key; the server names under
  `projects.<realpath>.mcpServers` for the duplicate rule (ruling 7). Both
  through structs that name only those fields: the server map decodes as
  `map[string]struct{}`, never `json.RawMessage` or `any`, because per-path
  entries store env values (the declared-fields-only rule codex's decoder
  follows, `codexMCPServer`, `internal/runtime/codex_home.go:36-43`).
- Holds, during step 4 only: the file's other values as opaque bytes, to
  write them back unchanged. They are never decoded, logged, persisted, or
  copied elsewhere; marvel keeps no backup of the file.
- Runs: `claude auth status`, exit status only.
- Never: opens the credential store (the macOS login keychain, or a
  credential file); logs in for a seat; writes any key but the trust key.

Folder trust is not bearer authority at a third party: it tells the local
harness it may load a directory's project settings. So writing it is not
custody, and the login stays where the operator's own login put it. The
cost is the one F1 named and the operator's ruling accepts: marvel's writes
change the operator's own interactive trust for those directories too. The
ledger makes every such change visible and attributable.

**The re-scope.**

| Item | Before | Under SB-4M |
|---|---|---|
| SB-3 | probe | done 2026-10-02, red (outcome C) |
| SB-4 | private home, trust and onboarding seed, MCP projection, `DIRECTOR_ROLE` | **dropped on macOS**. Its per-session config view returns only for hosts where the login is a file, after a Linux probe. The MCP projection and `DIRECTOR_ROLE` move to SB-4M |
| SB-4F | read trust, refuse, write nothing | **rejected** (ruling 4); its reader, the timeout and the trust-first order carry into SB-4M, and its refusal stays as the `require` mode |
| SB-4M | (new) | the claude `Bootstrapper`: realpath; trust managed in place under the lock with verify and ledger; `trust`, `trust_match`, `trust_dirs`; the login check with `login_check` modes and the handoff readiness rule; the MCP projection with the duplicate rule; `DIRECTOR_ROLE` in `baseEnv` |
| SB-5 | codex seed uses the resolved workdir | unchanged |
| SB-6 | refusal path, record, events, `describe` | reasons: `trust-absent` (`require` only), `config-locked`, `trust-write-lost`, `config-unreadable`, `not-logged-in` and `login-check-timeout` (`refuse` only), `link-replaced` (codex); events `trust.granted` and `session.login-unverified` |
| SB-0 | rollout probe | runs SB-1, SB-2 and SB-4M; lines 9 and 10 revised, line 13 kept, lines 14 to 16 added (section 4a) |
| SB-7 | fleet manifests declare `settings_sources` | unchanged; gated on SB-0 with SB-4M. A wrapped role's trust is unchecked today (ruling 8, open) |

SB-1 and SB-2 are unchanged; SB-2 landed as #467.

## 6. Refuse, and say why

When a bootstrap step the harness needs fails (the declared workdir is
missing; the managed directory cannot be created; trust cannot be granted
because the config stayed locked or a write kept being lost; trust is absent
under `require`; the config cannot be read; the login check failed or timed
out under `refuse`; or a codex `LinkIn` entry is no longer a link):

- the session is not launched. It is recorded `failed` with condition
  `bootstrap-refused` and the reason, and it is **not** charged to the restart
  policy: nothing crashed, so `max_restarts` and `restart_policy = never`
  never freeze a role over it;
- the reconciler does not respawn it until the manifest is re-applied or the
  operator runs `marvel reset-health`, so a refusal does not loop;
- `session.bootstrap-refused` is emitted once, with the session, adapter,
  directory and missing step;
- `describe session` shows the same.
- a `trust-absent` refusal (only under `require`) names the realpath and the
  one-time step: open Claude Code there and accept the dialog, then
  re-apply. `config-locked` and `trust-write-lost` name the config path.
- a config that fails to parse (a running Claude Code may be rewriting it) is
  read once more after one second; a second failure is `config-unreadable`
  (ruling 10, accepted).

A best-effort step (the MCP projection when the operator has no director
server configured) logs and continues, as the codex seed does today.

## 7. What this amends in #255

- "Creating the directory": #255 left it out. marvel now creates **managed**
  directories only; declared ones must still exist.
- "Trust itself. The harness decides trust from its own store": still so,
  and marvel now writes to that store. For the directory it placed an
  interactive claude seat in, and the directories the user lists, marvel
  grants trust in the operator's config when it is absent, records each
  grant, and claims nothing about other directories (section 5a).
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
           config <the .claude.json>   trust granted (manage, exact)   login ok (warn)
```

`get sessions` gains nothing (the table stays narrow; #255's `WORKDIR` column
is its own ticket). The event ring gets `session.bootstrapped` once per
launch, `trust.granted` per grant, and `session.login-unverified` when a
`warn` check fails. The trust ledger lives in marvel's store, one record per
directory marvel trusted (path, time, reason, the session that caused it).

## 9. Tests (red first on 9009e89)

1. A role with no workdir under a daemon started in a directory holding
   `.claude/settings.local.json`: the pane's cwd is the managed directory, and
   the command line carries `--setting-sources user`.
2. A declared workdir: the pane starts there and the command carries
   `--setting-sources user,project`; `local` appears only when declared.
3. The managed directory is created `0700`, is the same path after a
   restart, and nothing in it was copied from elsewhere.
4. Trust, managed. With a fixture config whose workdir key is absent, a
   `manage` spawn writes `projects.<realpath>.hasTrustDialogAccepted` true
   and nothing else: every other value in the file is byte-identical before
   and after (a fixture whose other projects carry a canary in an MCP env
   value keeps the canary in the file, and it appears nowhere in the log,
   the session record, or the ledger). A key already true causes no write
   (mtime unchanged). The workdir through a symlink is written by its
   realpath. With `CLAUDE_CONFIG_DIR` in the seat's env, that file is the
   one written. A lock directory held by another process is waited on for
   5 seconds, never removed, and ends `config-locked`. A fake rewriter that
   restores the old file after each write ends `trust-write-lost` after 3
   attempts. `require` refuses `trust-absent` and writes nothing; `off`
   neither reads nor writes. `trust_match = "ancestor"` with a trusted
   parent writes nothing. A config that fails to parse is read once more,
   then refused `config-unreadable`. The `mcpServers` decision map decodes
   into `map[string]struct{}`, and `json.Marshal` of the decoded value does
   not contain the canary. That spec fails for both `json.RawMessage` and
   `any` and passes for `struct{}` (`%#v` alone would miss
   `json.RawMessage`; the reviewer measured both).
4a. The login check. A fake `claude auth status` that forks a never-exiting
   child, which holds the inherited stdout, and then never exits: within 10
   seconds plus a small margin both processes are gone (the group is
   killed), `Wait` returns, and the result is a timeout. With a spawn
   refused at the trust step, the fake `claude` is never invoked. Exit 1
   under `warn` launches the seat and emits `session.login-unverified`;
   under `refuse` it refuses `not-logged-in`; under `off` the fake is never
   run. A shift whose successor's check returns 1 keeps the predecessor
   running, re-checks every 30 seconds, and rolls back at the shift timeout;
   when the check starts returning 0 the shift completes.
5. The codex seed declares the resolved workdir untrusted, not the daemon cwd.
6. A missing declared workdir: not launched, `bootstrap-refused`, restart
   count 0, not frozen under `restart_policy = never`, one event, no respawn
   until re-apply.
7. The projected MCP file carries the configured director command and args,
   no env, and is `0600`. A role whose command already declares
   `--mcp-config` or `--strict-mcp-config` gets no projection and no second
   flag, and its declaration reaches the command line unchanged.
8. Custody. The fake `claude auth status`'s output is never read or
   logged, and its argv shows only `auth status`. No file under `StateDir`
   holds any part of a claude seat's config or credential, and marvel makes
   no copy of the config anywhere. For codex, a `LinkIn`
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
| SB-4 | **Dropped on macOS** (section 5a) | |
| SB-4M | claude `Bootstrapper` under the operator's config: realpath; trust managed in place (lock, verify, retry, ledger); `trust`, `trust_match`, `trust_dirs`; the login check with `login_check` modes and the handoff readiness rule; MCP projection with the duplicate rule; `DIRECTOR_ROLE` in `baseEnv` | SB-1 |
| SB-5 | codex seed uses the resolved workdir | SB-1 |
| SB-6 | Refusal path (with the SB-4M reasons), `Session.Bootstrap`, the trust ledger, events, `describe` | SB-1 |
| SB-0 | Rollout probe: one bare-claude seat, the section 4a checklist, a written pass or fail per line | SB-1, SB-2, SB-4M |
| SB-7 | Fleet and example manifests declare `settings_sources` (for wrapped roles, inside `cast-launch.sh`, a director PR), and carry forward the roles' existing `--mcp-config` and `--strict-mcp-config` flags unchanged | SB-2, SB-0 passed |

The shortest path to an unblocked step 0 is SB-1, then SB-4M; SB-2 (#467) and
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
4. **Adopt SB-4F. RULED 2026-10-03: rejected.** The operator, verbatim:
   "4 rejected. marvel SHOULD manage these kinds of files actively, when
   called for, a key mission for marvel is to actively manage these and other
   settings, directly on local host file system, as injected per-session
   virtual files, injecting into containers, as submounts, and in other
   ways". This revision answers with SB-4M (section 5a).
5. **Trust by exact path or by ancestor. RULED 2026-10-03:** "5 this should
   be an option, with overrides". Applied as `trust_match`, `exact` (the
   default) or `ancestor`, set by `claude.trust_match` in the cluster config
   and overridden per role.
6. **Managed directories. RULED 2026-10-03:** "6 marvel should actively
   manage these, and the user can specify". Applied: marvel grants trust for
   the directories it manages, and the user lists more with
   `claude.trust_dirs` and sets `trust` per role (section 5a).
7. **The MCP duplicate rule. RULED 2026-10-03:** "7 accepted".
8. **Wrapped claude roles. OPEN.** The operator: "8 i dont understand this
   yet. expand some with examples". Director is expanding it for the
   operator; the text below is unchanged. Today nobody checks trust for them:
   `cast-launch.sh:273-282` requires `TWIN_CWD` and changes into it but never
   reads trust, and marvel's workdir is not where that claude runs. The
   session records `trust: unchecked (wrapper)`. Two fixes: (a) marvel reads
   the role's `TWIN_CWD` and checks it, or (b) a director PR adds the same
   key-name read to `cast-launch.sh`. Default offered: (b). The wrapper owns
   its directory and command line (section 4a), and (a) would couple marvel
   to a director variable it does not otherwise know.
9. **The login check. RULED 2026-10-03:** "9 make it a configurable option,
   but i can imagine a situation where a change to the harness means that
   the old session cannot auth and the new harness needed to auto is
   prevented from starting. we may need a middle situation also. we might
   handle this better with healthchecks as part of the handoff, and this
   might be part of it". Applied: `login_check` is `warn` (the default, the
   middle mode), `refuse`, or `off`; the check runs the new session's
   binary; and a shift's successor is not ready until its check passes
   (section 5a).
10. **An unreadable config. RULED 2026-10-03:** "10 accepted". Read once
    more after one second, then refuse `config-unreadable`.
11. **Declared workdirs under `manage` (new).** Ruling 6 covers the
    directories marvel creates. A declared workdir is named by a manifest,
    and trusting it lets its project settings and hooks load without the
    dialog. Default offered: `manage` for declared workdirs too, since the
    manifest author is the operator naming the directory, and each grant is
    in the ledger. Alternative: `require` for declared workdirs unless the
    role sets `trust = "manage"`.
