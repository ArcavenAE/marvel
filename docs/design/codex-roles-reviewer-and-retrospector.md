# Two codex roles for the live arcaven team: reviewer and retrospector

Status: design and plan, 2026-09-17. The apply to the running fleet is
operator-gated and executed by the director, not by this session. This doc is
the design, the exact manifest change, and the follow-on work. Nothing here has
been applied to the live `aae/arcaven` team.

Both roles run codex on gpt-5.6-luna, to be economical with tokens.

## Verify-premise results

- **Live manifest shape.** `aae/arcaven` is ten claude roles (supervisor,
  envoy, research-supervisor, maintainer, builder, marvel-builder,
  sideshow-builder, architect, filer, author), each `replicas 1`,
  `permissions auto`, `restart_policy always`, `image claude`,
  `command cast-aae.sh`, `context_feed statusline`. The offline manifest is
  never checked in; it mirrors the running twin. Every role is cast from the
  wardrobe install root by `cast-aae.sh` then `cast-launch.sh`, which slices
  the named wardrobe role and passes it as `--append-system-prompt`.
- **Retrospector and SLAES already exist.** wardrobe `contents/roles/
  retrospector.md` is a ratifiable proposal that supersedes
  `multiclaude-enhancements/agents/retrospector.md` (the SLAES prior art). It
  is read-only (`permission_floor: read`) and its records
  (`retrospector/finding@1`, `retrospector/recommendation@1`) are described as
  an append-only findings log and recommendation queue. The wardrobe role is
  the thing to EXTEND with a write surface, not a new role to invent.
- **gpt-5.6-luna is selectable.** It is in codex's model cache (slug
  `gpt-5.6-luna`; the codex default here is `gpt-5.6-sol`), and the codex
  adapter passes `runtime.args` verbatim, so `-m gpt-5.6-luna` selects it.
  Confirmed live: the throwaway retrospector's rollout recorded gpt-5.6-luna
  (finding-048).

## The casting problem (shared by both roles)

Every live role casts through `cast-launch.sh`, which is claude-specific: it
execs `claude -n <id> --strict-mcp-config --mcp-config <director shim> --append-
system-prompt <wardrobe slice>`. codex has none of those flags (no
`--append-system-prompt`; identity travels only through the environment,
codex.go). So a codex role cannot reuse `cast-launch.sh`.

A codex role needs a small codex cast path that does three things:

1. **cwd.** Pin it. For the reviewer, the workspace cwd (read-only anyway). For
   the retrospector, the retro output dir (which defines the write surface, see
   below).
2. **Role text.** codex has no system-prompt flag, so the wardrobe slice is
   prepended to the codex prompt. The wrapper slices the wardrobe role (reuse
   `scripts/slice.sh`, as `cast-launch.sh` does) and sets the codex prompt to
   `<slice>\n\n<task>`. The durable role definition stays in wardrobe; the
   manifest `prompt` carries only the task.
3. **Bus (optional for v1).** The claude roles wire the director shim via
   `--mcp-config`. codex configures MCP servers through its own config
   (`CODEX_HOME/config.toml [mcp_servers]` or `-c mcp_servers...`). The first
   cut does not need it: the reviewer's output lands in the event ring and the
   retrospector's in its retro dir, both consumable without the bus. Put these
   roles on the director bus only if they must message teammates directly; that
   is follow-on integration, and codex-reaching-the-bus is already shown
   elsewhere (the director roster).

This codex cast wrapper is the one piece of new plumbing. It is a flat follow-on
task (below), and it is what the live apply depends on.

## Workstream 1: adversarial reviewer (read-only)

Proven end to end in finding-046. The role reuses wardrobe `reviewer.md`
(advisory-not-blocking, `permission_floor: read`). Sandbox stays read-only, so
its output is the event ring, and it cannot write the tree.

Proposed manifest role block (for the live team, held for operator authorization):

```toml
  # wardrobe: role/reviewer  (codex, read-only, advisory)
  [[team.role]]
  name = "reviewer"
  replicas = 1
  restart_policy = "always"
    [team.role.runtime]
    image = "codex"
    command = "/Users/michael.pursifull/.marvel/manifests/cast-aae-codex.sh"
    mode = "headless"
    args = ["-m", "gpt-5.6-luna", "-s", "read-only"]
    prompt = "<the review task; the wardrobe reviewer slice is prepended by the cast wrapper>"
```

Notes: no `permissions` field (that projects a claude permission-mode, inert for
codex). `-s read-only` is explicit even though it is codex's default, so the
read-only contract is visible in the manifest. Headless one-shot per review:
completion semantics hold the slot (ADR-010).

## Workstream 2: retrospector (constrained write)

The retrospector must write its findings log and recommendation queue, and
nothing else. finding-048 proves the write surface can be exactly one directory.

**The write surface.** codex `-s workspace-write` makes the primary workspace
(the cwd) and system temp writable, allows reads broadly, and disables network.
Pinning the cwd to a dedicated retro output dir makes that dir the only writable
surface; the retrospector reads the repo for context and is refused every write
outside the retro dir. Proved: it wrote `RETRO.md` to the retro dir, read the
repo, and was refused an out-of-scope write to the repo (finding-048).

**Which directory.** Recommend an out-of-repo dir for v1, one the throwaway
proved safe: `~/.marvel/retro/aae/` (per workspace). It is not inside a git
repo, so there is no chance of codex's workspace-write widening to a repo root.
The retrospector's records land there; the consuming seat reads them from there.
An in-repo location (versioned retrospective output, e.g. under an SLAES dir) is
a reasonable follow-up, but it needs a one-line verify first that workspace-write
does not add the repo root as writable when cwd is a repo subdir; until then,
out-of-repo is the proven choice.

**The exact runtime.args.** `["-m","gpt-5.6-luna","-s","workspace-write"]`. If
the retrospector ever needs a second writable root, add it with
`--add-dir <path>` rather than widening the sandbox mode. Nothing else is
granted: no network, no write outside the retro dir.

**A verify step for the apply.** Before trusting a live retrospector, the same
two-part check the throwaway ran: (1) it writes its record to the retro dir; (2)
it is refused a write into the repo it reads. If (2) ever succeeds, the sandbox
is misconfigured and the role must not run write-enabled.

Proposed manifest role block (held for operator authorization):

```toml
  # wardrobe: role/retrospector  (codex, constrained write to the retro dir)
  [[team.role]]
  name = "retrospector"
  replicas = 1
  restart_policy = "always"
    [team.role.runtime]
    image = "codex"
    command = "/Users/michael.pursifull/.marvel/manifests/cast-aae-retro.sh"
    mode = "headless"
    args = ["-m", "gpt-5.6-luna", "-s", "workspace-write"]
    prompt = "<the retro task; the wardrobe retrospector slice is prepended by the cast wrapper>"
```

`cast-aae-retro.sh` is the codex cast wrapper with its cwd pinned to
`~/.marvel/retro/aae/` (not `TWIN_CWD`), so the write surface is the retro dir,
not the repo. The reviewer's `cast-aae-codex.sh` pins cwd to `TWIN_CWD` (read
only, so cwd choice is not load-bearing there).

## Migration to curtain (vision Gap 4)

The write restriction is expressed today as codex's own sandbox in
`runtime.args` plus the pinned cwd. That is the interim Layer-1 enforcement for a
harness marvel does not project into (claude gets a projected settings fragment;
codex reports no projection surface, so its own sandbox is the lever). Expressed
as a single named writable-root set (the retro dir, plus any `--add-dir` roots),
it maps one-to-one onto a later declarative curtain policy (permission-through-
environment, `question-permission-model` Layer 1; aae-orc-10x). Keeping the write
surface one named parameter, not scattered paths, keeps that a mechanism swap.

## The discrete live-team manifest change (for operator authorization)

Adding both role blocks above to `~/.marvel/manifests/aae-arcaven.toml` and
re-applying with `marvel work` is a per-team upsert: it adds the two roles and
leaves the ten existing roles running (finding-043). It is additive and
reversible (drop the two blocks and re-apply, or `marvel delete` the two roles).

This change is NOT applied here. It is the operator's to authorize and the
director's to execute, and it depends on the codex cast wrappers existing
(`cast-aae-codex.sh`, `cast-aae-retro.sh`) and the retro dir being created. The
two-part write verify above runs against the retrospector before it is trusted
write-enabled.

## Follow-on work (flat)

- marvel: map codex `file_change` items in the codex parser, so a write-capable
  codex role's writes read as `tool.result` rather than `agent.error` on the
  ring (finding-048, aae-orc-tvlcg).
- marvel/director: the codex cast wrappers (aae-orc-gz9pd) (`cast-aae-codex.sh`,
  `cast-aae-retro.sh`): pin cwd, prepend the wardrobe slice, select the model
  and sandbox. The live apply depends on these.
- wardrobe: the retrospector role extension (the write surface) is drafted this
  round; the reviewer role is already in wardrobe.
- curtain: the declarative write-policy home for both roles (aae-orc-10x,
  blocked on curtain), the eventual replacement for the sandbox-in-args
  mechanism.

## Evidence

finding-046 (the read-only codex reviewer, proven), finding-048 (the codex
constrained write surface, proven on gpt-5.6-luna), question-permission-model
(Layer 1), finding-043 (per-team upsert, why the apply is additive), wardrobe
`reviewer.md` and `retrospector.md`.
