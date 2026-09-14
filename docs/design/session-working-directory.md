# Session working directory: a declared placement field

Status: proposed 2026-09-14 on an operator directive; decisions 1 to 9
below are the design, the build sequence names the tickets. Not yet
ratified; the operator merges.

---

## The miss

marvel cannot say where an agent session runs. There is no working
directory anywhere in the manifest, the API types, the runtime adapters,
or the CLI: the only `dir :=` in the tree is bolt path handling. A spawned
pane inherits whatever directory the tmux server happened to be created
in, which is wherever the operator typed `marvel daemon`. Every session's
cwd is an accident of the daemon's launch, not a declaration.

The consequence surfaced standing up the marvel twin (director brief 7,
S0): the first session launched from a daemon started in a director
worktree, Claude Code found that directory untrusted, and it stopped at
the trust dialog before the director shim loaded. The interim fix is a
`cd` inside the launcher (director#21, `TWIN_CWD`). The operator ruled
that a launcher default is not the answer; placement is a requirement the
manifest has to carry, and it is treated as a feature, not a patch.

## What is measured, and what is asserted

Measured at marvel `70a31fe` and on this host, 2026-09-14:

- `ManifestWorkspace` is `{name}`; `ManifestTeam` and `ManifestRole` carry
  no directory; `api.Workspace`, `api.Team`, `api.Role`, `api.Session`
  carry none (`internal/api/types.go:134, 352, 439, 190`).
- `Driver.NewSession` runs `tmux new-session -d -s <name>` and
  `Driver.NewPane` runs `tmux new-window -t <session> -d -P -F ... [-n
  title] [-e K=V]... <command>`; neither passes `-c` (`internal/tmux/
  driver.go:197, 277`). The pane's directory is the server's.
- No adapter passes a directory flag to its harness (`claude.go`,
  `codex.go`, `opencode.go`: zero `-C` or `--cwd`).
- `marvel work` reads the manifest file and posts its bytes as
  `manifest_data`; the daemon parses bytes (`handleApply`,
  `internal/daemon/daemon.go:812`). The manifest's own path never reaches
  the daemon, so "relative to the manifest" is a CLI-side fact only.
- `marvel run` takes `--workspace`, `--team`, `--role`, `--script`; no
  directory.
- All nine hand-run fleet sessions on this host run with the orc root as
  cwd, and that root is the one directory Claude Code has trust for.

Asserted, and the design rests on it: the harness's trust decision is
keyed on the cwd, so a session's cwd is a security-relevant placement
fact, not cosmetics.

## Decisions

**1. The field is `workdir`, at three levels, one anchor.**
`[workspace] root` names the filesystem root the workspace lives in.
`[[team]] workdir` and `[[team.role]] workdir` name where a team's or a
role's sessions run. Precedence is role over team over workspace root. A
relative `workdir` resolves against `workspace.root` only, never against
the enclosing team's `workdir`, so there is one anchor and no chain to
reason about. The TOML key is `workdir`, the Go field `WorkDir`; no alias
(`cwd`, `dir`), because the loader drops unknown keys silently and an
alias that half-works is a second spelling to reconcile.

**2. The default is the manifest's own directory, filled by the CLI.**
`marvel work` resolves `workspace.root` before posting: if the manifest
sets it, the CLI makes it absolute against the manifest file's directory;
if it does not, the CLI sets it to that directory. The daemon never sees
a manifest without an absolute root. A project applies its manifest from
its own checkout, which is the directory the operator trusts; a bare `.`
or `~` never reaches a session. This is what makes another operator's
manifest portable: skippy's manifest names no path at all and lands in his
own checkout.

**3. The daemon refuses what it cannot place.** At apply, `workspace.root`
must be present and absolute; every resolved `workdir` must exist on the
daemon's host and be a directory; `~` is not expanded and is refused as
not absolute. A manifest posted through the raw API without a root is
refused with `workspace.root is required (marvel work fills it from the
manifest's directory)`. Validation is placement, not trust: marvel does
not read the harness's trust store, and does not claim to.

**4. Placement rides into the pane through tmux, uniformly.** The role's
resolved `WorkDir` is copied onto the `Session` at spawn, carried in
`LaunchContext`, and passed to `Driver.NewPane` as a start directory
(`new-window -c <dir>`). This places every harness the same way with no
adapter code, which is why no adapter gains a flag in this design; a
harness-specific flag (`codex -C`) can follow if a harness ignores its
pane cwd, and none of the four measured does.

**5. The environment carries it too.** `baseEnv` adds
`MARVEL_WORKDIR=<resolved dir>` beside `MARVEL_SESSION`, for wrappers and
hooks that want the declared value without inspecting the pane. It is
informational, never a second source: the pane is already there.

**6. The session shows it.** `Session.WorkDir` persists in the store and
`marvel get sessions` gains a column, so an operator reading the fleet can
see placement without opening a pane. Adding a field to the persisted
record is additive; whether the bolt schema version moves is the build
lead's call at implementation.

**7. Restart and shift re-resolve.** A restart or a shift spawns with the
role's current `workdir` from the applied manifest, not the dead
session's persisted value, the same way every other role field applies on
respawn. Editing a `workdir` and re-applying then rotating is how a team
moves.

**8. Ad-hoc runs place explicitly.** `marvel run` gains `--workdir
<dir>`, default the caller's cwd made absolute by the CLI. An interactive
operator's cwd is the trusted one by construction; a script that wants a
different placement says so.

**9. Placement stays out of the role library.** `workdir` is a marvel
manifest field and nothing else. A wardrobe role definition carries no
path, no host, no directory (wardrobe keeps liveness and placement off the
role by ruling); the launcher's `TWIN_CWD` default retires the day this
field ships, replaced by `MARVEL_WORKDIR` when set and a refusal when not.
Adjacent to R-84 (identity at launch) and kept apart from it: `workdir`
never enters the session name or key, and it follows the same one-source
discipline, declared once in the manifest and projected by marvel into
the pane and the environment with no second record to reconcile.

## What this does not decide

- Per-replica placement (replica 0 here, replica 1 there). One `workdir`
  per role; a role that needs two homes is two roles.
- Creating the directory. marvel places into a directory that exists; a
  worktree-per-session model (the wardrobe worker's "one task in its own
  worktree") is a workspace-preparation concern, upstream of spawn.
- Trust itself. The harness decides trust from its own store; marvel's
  contribution is a placement that defaults to the directory the operator
  applied from.

## Build sequence

Flat bd tickets with dependency edges (labels `aae-orc`, `marvel`,
`source:session`); ids are recorded in the PR that lands this document.

1. Types, manifest, validation: `WorkDir` on `Team`, `Role`, `Session`,
   `Root` on `Workspace`; parse `workdir` and `root`; daemon-side
   validation (absolute, exists, directory, root required); CLI-side
   resolution in `marvel work` (root from the manifest's directory,
   relative fields against root, posted absolute).
2. Spawn wiring: `Session.WorkDir` set at spawn from the role, carried in
   `LaunchContext`, `Driver.NewPane` start directory (`-c`),
   `MARVEL_WORKDIR` in `baseEnv`, restart and shift re-resolve. After 1.
3. CLI surface: `marvel run --workdir`, the `get sessions` column, the
   apply-error text. After 1.
4. Docs, examples, the twin: user guide and CLAUDE.md manifest table,
   every `examples/*.toml` gains `root` or shows the default, the director
   twin manifest declares `root` and the launcher retires `TWIN_CWD`.
   After 2 and 3.

## Cross-references

- director brief 7 (`director/sim/design/marvel-twin-manifest-and-
  cutover.md`) and director#21 (the interim launcher `cd`).
- director requirements R-73, R-84 (one source at launch); marvel
  `_kos/ideas/identity-at-launch-and-managed-nats.md` (the `baseEnv`
  seam, where `MARVEL_WORKDIR` lands beside `DIRECTOR_AGENT_ID`).
- `docs/design/daemon-isolation.md` (the HOME-rooted layout this field
  deliberately does not join: a session's placement is per role, the
  daemon's paths are per home).
- finding-020 (a constructed environment can leak): `MARVEL_WORKDIR` is a
  path, not a secret, and is safe to stamp.
