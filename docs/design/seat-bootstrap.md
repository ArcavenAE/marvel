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

`BootstrapResult` names what was seeded (trust, onboarding, MCP projection),
the settings sources passed, and the config home used. An error with
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

**Root-less fleet manifests move unless they declare a root first.** Today a
manifest with no `workspace.root` and no `workdir` places its seats in the
daemon's cwd, which for the fleet is the orc root. Every current fleet manifest
on kinu is that shape (`~/.marvel/manifests`, 11 of 11 `.toml` files carry no
`root` or `workdir` key; the reviewer reports the same for the mokuzai
manifests). Under rule 3 each of those seats would move to a managed
directory, away from the orc root and its project `CLAUDE.md`. So SB-7a
declares a `workspace.root` or `workdir` in every such fleet manifest, and
SB-1 does not land until SB-7a has.

The daemon's own directory is never a result. The tmux session itself is
created with `new-session -c <StateDir>/seats`, so even the base pane is off
the daemon's cwd, and `new-window -c <dir>` places each seat (the spawn
wiring in aae-orc-5as2e, extended with the managed fallback).

The session records `WorkDir` and `WorkDirSource` (`role`, `team`, `root`,
`run`, `managed`).

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
| `role://` delivery | `DIRECTOR_ROLE`: the shim subscribes to the role subject only when it is set (director `probe/nats-phase-0/director-mcp/bus.go:234-235`) | **not set by marvel today**; `cast-launch.sh` sets it | **breaks**: messages to `role://<team>/<role>` would not reach the seat. SB-4 adds `DIRECTOR_ROLE` to `baseEnv` |
| Placement of seats whose manifest names no root | the orc root as cwd, so the project `CLAUDE.md` and the tracked settings load | rule 3 would give a managed directory | **moves** unless SB-7a declares a root first; SB-1 waits on SB-7a, and SB-0 line 12 re-applies one such manifest |
| Global address | `DIRECTOR_GLOBAL_DOMAIN`, `DIRECTOR_CLUSTER`, `DIRECTOR_GLOBAL_ROLE`; unset means the global tier is off (`global.go:78`) | not set by marvel (`cast-aae.sh:14-17` says marvel does not pass them through) | off. Correct for most roles (R-94 gives global addresses to supervisors only). A supervisor keeps the wrapper until marvel carries the global levers, which is out of scope here |

So nothing breaks for seats launched as they are today. A bare-claude seat
works for local traffic once `DIRECTOR_ROLE` is injected, subject to the one
unverified inheritance claim, and the rollout probe checks it first.

**Rollout probe (SB-0), one seat before any wider rollout.** The builder
applies one throwaway bare-claude seat with the SB-1..SB-4 behaviour, in its
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
9. Login: the seat reaches its prompt logged in; `claude auth status` exit
   status recorded (SB-3).
10. Placement: the pane's cwd is the managed or declared directory, and no
    trust dialog appears.
11. Env inheritance: in the projected seat, with the seat-credential fallback
    unavailable to the child, the `director-mcp` child authenticates with the
    `DIRECTOR_NATS_*` values marvel injected. A child that authenticates only
    through the fallback is a fail for this line.
12. A root-less manifest: re-apply one existing fleet manifest that had no
    root before SB-7a (now carrying its declared root). The pane's cwd is that
    root, and the project `CLAUDE.md` loads (the seat can name a rule from it).
13. Links at exit: when the probe seat exits, every `LinkIn` entry in its
    home is still a symlink to the operator's file; record any that is not.

The probe reports what broke, as a list, before SB-7 touches any fleet
manifest.

## 5. First-run state, per harness

| Harness | First-run state | Seeding |
|---|---|---|
| claude, interactive | folder trust per path; onboarding flags; per-path MCP | a private config home (below), seeded with trust for the resolved workdir, onboarding complete, and no per-path MCP (the projection in section 4 replaces it) |
| claude, headless | none shown (`-p` skips the dialog) | placement and settings sources only |
| codex | trust per path in `config.toml` | existing seed (#308, #359); `Untrusted` becomes the resolved workdir instead of `os.Getwd()` |
| opencode, generic, forestage | none known | placement only; recorded as `none` |

**The claude config home.** The codex precedent is a private home per session
key (`SessionHomeSpec`, `adapter.go:125`; `prepareSessionHome`,
`internal/session/manager.go:698`): a relocatable directory, credentials
symlinked in rather than copied (ADR-009), and a marvel-authored config file
naming only the keys it sets. Claude Code relocates its config with
`CLAUDE_CONFIG_DIR`. The claude seed writes `.claude.json` with:

- `projects.<workdir>.hasTrustDialogAccepted = true`;
- `hasCompletedOnboarding = true` and the onboarding version the installed
  harness reports;
- nothing copied from the operator's file: no history, no other projects'
  trust, no MCP servers, no identity.

Claude Code later writes account metadata into that file (`oauthAccount`) on
login or use. That is identity, not bearer authority. marvel neither reads it
nor carries it between homes.

**What the home links, per platform (ADR-009).** Like codex's `auth.json`,
the credential is linked and never copied:

| Platform | Claude Code keeps the credential | `LinkIn` |
|---|---|---|
| macOS | the login keychain | nothing (SB-3 decides whether the relocated home still finds it) |
| Linux | a file under its config directory (`.credentials.json`, per Claude Code's layout; SB-3 confirms the name) | that file, symlinked to the operator's own |

**A seat that would come up logged out is refused.** `LinkIn` skips a missing
entry by design (`adapter.go:142-145`), and for codex the session reports its
own auth. For an interactive claude seat that report is a login screen, which
breaks section 2's rule. Worse, completing that login would write a new
refresh token under `StateDir`, which is marvel holding bearer authority at a
third party. So before launch marvel runs `claude auth status` under the
private home (the harness reads its own credential; marvel reads only the
exit status). Not logged in is a `bootstrap-refused` reason (section 6). The
exit-status contract of `claude auth status` is not verified here; SB-3
records it.

**A link must stay a link.** A harness that refreshes a credential by writing
a temp file and renaming it over the path would replace the symlink with a
regular file, and a copy would then live under `StateDir`. Neither harness is
known to do or not do this. So at every spawn that reuses a home,
`prepareSessionHome` checks, for every adapter, that each `LinkIn` entry is
still a symlink to the operator's file. If one is not, the session is
refused with reason `link-replaced`, naming the path, and marvel deletes
nothing (the no-deletion convention): the operator removes the copy and
re-applies. `Session.Bootstrap` records the check. The same check also runs
when a session exits or is reaped, so a replaced link is reported (event
`bootstrap.link-replaced`, naming the path) when it happens rather than at
the next spawn; it deletes nothing there either.

Trust is seeded only for the resolved workdir, and only because section 4
decides what that trust loads. Trusting a directory whose local settings
would then load unreviewed is the thing the declined dialog was protecting,
and the settings-sources rule is what makes seeding trust safe.

**Not verified: whether a relocated config home keeps the operator's login.**
On macOS Claude Code keeps the OAuth credential in the login keychain; whether
the item it looks up changes with `CLAUDE_CONFIG_DIR` was not checked here,
because the check reads the credential store and was declined. SB-3 is a
probe the operator runs (or grants): launch one seeded interactive claude
under a private `CLAUDE_CONFIG_DIR` and read whether it reaches the prompt
logged in, and what `claude auth status` exits with there. **If it is not
logged in, the probe stops there and never logs in inside the private home.**
Also record whether a token refresh leaves the Linux link a link. If the home
does not keep the login, the private home is not the mechanism, and the
fallback is:

- **Fallback F1:** write the one trust key into the operator's own config
  file. Rejected as the default: every running Claude Code rewrites that file,
  so marvel's write races the fleet's, and it would change the operator's own
  interactive trust as a side effect.
- **Fallback F2 (recommended if SB-3 fails):** read, by key name, whether the
  resolved workdir is already trusted in the operator's config. If it is,
  launch. If it is not, refuse (section 6) with the exact directory the
  operator must trust once. marvel then prepares everything else and never
  launches a seat into a dialog.

## 6. Refuse, and say why

When a bootstrap step the harness needs fails (the declared workdir is
missing, the managed directory cannot be created, the claude home cannot be
seeded, the seat would come up logged out, a `LinkIn` entry is no longer a
link, or trust is absent under F2):

- the session is not launched. It is recorded `failed` with condition
  `bootstrap-refused` and the reason, and it is **not** charged to the restart
  policy: nothing crashed, so `max_restarts` and `restart_policy = never`
  never freeze a role over it;
- the reconciler does not respawn it until the manifest is re-applied or the
  operator runs `marvel reset-health`, so a refusal does not loop;
- `session.bootstrap-refused` is emitted once, with the session, adapter,
  directory and missing step;
- `describe session` shows the same.

A best-effort step (the MCP projection when the operator has no director
server configured) logs and continues, as the codex seed does today.

## 7. What this amends in #255

- "Creating the directory": #255 left it out. marvel now creates **managed**
  directories only; declared ones must still exist.
- "Trust itself. The harness decides trust from its own store": marvel now
  seeds trust for the directory it placed the seat in, under settings it
  chose, and does not claim anything about other directories.
- Decision 3's refusal of a manifest with no `workspace.root`: unchanged for
  `marvel work`, which always fills it. A raw API apply or an older record
  with no root gets a managed directory instead of the daemon's cwd.

## 8. What the session records

`Session.Bootstrap`, persisted, shown by `describe session`:

```
Bootstrap: workdir <StateDir>/seats/<ws>/<team>/<role> (managed)
           settings user   mcp director (projected)
           home   <harness home dir>   seeded trust, onboarding
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
4. The claude seed writes exactly the listed keys, trust only for the resolved
   workdir; a golden file guards the allowlist.
5. The codex seed declares the resolved workdir untrusted, not the daemon cwd.
6. A missing declared workdir: not launched, `bootstrap-refused`, restart
   count 0, not frozen under `restart_policy = never`, one event, no respawn
   until re-apply.
7. The projected MCP file carries the configured director command and args,
   no env, and is `0600`. A role whose command already declares
   `--mcp-config` or `--strict-mcp-config` gets no projection and no second
   flag, and its declaration reaches the command line unchanged.
8. Custody: a claude home whose linked credential is missing is refused
   `bootstrap-refused` (not logged in) and never launched; a `LinkIn` entry
   replaced by a regular file is refused `link-replaced` and nothing is
   deleted; on Linux the credential under the home is a symlink, never a
   regular file. A link replaced during a session is reported at its exit.
9. A headless claude run gets placement and settings sources and no seeded
   home.

## 10. Edits, in order (none made by this PR)

| # | Edit | Depends on |
|---|---|---|
| SB-7a | Every fleet manifest with no `workspace.root` or `workdir` declares one (the orc root, where its seats run today) | none |
| SB-1 | Managed directory fallback; `new-session -c`; `WorkDirSource` | aae-orc-5as2e, SB-7a |
| SB-2 | Explicit `--setting-sources`, `settings_sources` on the role, the defaults in section 4 | none |
| SB-3 | Probe: does a private `CLAUDE_CONFIG_DIR` keep the login (operator-run) | none |
| SB-4 | claude `Bootstrapper`: private home, trust and onboarding seed, MCP projection for bare claude only, `DIRECTOR_ROLE` in `baseEnv` | SB-3 green, SB-1 |
| SB-4F | Fallback F2, if SB-3 fails | SB-3 red, SB-1 |
| SB-5 | codex seed uses the resolved workdir | SB-1 |
| SB-6 | Refusal path, `Session.Bootstrap`, events, `describe` | SB-1 |
| SB-0 | Rollout probe: one bare-claude seat, the section 4a checklist, a written pass or fail per line | SB-1, SB-2, SB-4 or SB-4F |
| SB-7 | Fleet and example manifests declare `settings_sources` (for wrapped roles, inside `cast-launch.sh`, a director PR), and carry forward the roles' existing `--mcp-config` and `--strict-mcp-config` flags unchanged | SB-2, SB-0 passed |

The shortest path to an unblocked step 0 is SB-7a, SB-1, SB-2, SB-3 and then
SB-4 or SB-4F. SB-7a comes first because SB-1 would otherwise move every
root-less fleet seat out of the orc root.

## 11. Rulings needed

1. **Default settings sources** (section 4). Default offered: `user,project`
   for a declared workdir, `user` for a managed one, `local` only by
   declaration.
2. **`--strict-mcp-config` for projected seats.** Default offered: off for a
   seat marvel projects director into, so it keeps the operator's user-scope
   servers. A role that declares strict (as the mokuzai seats do) keeps it; the
   default never overrides a role.
3. **The #255 amendments in section 7.** Default offered: adopt as written.
