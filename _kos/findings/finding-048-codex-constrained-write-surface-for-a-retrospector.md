# finding-048: a codex role gets a constrained write surface from its own sandbox plus a pinned cwd

finding-046 left a codex reviewer read-only, its output in the event ring. A
retrospector must WRITE, but only to a defined place. This proves the write
surface can be widened to exactly one directory and no more, using codex's own
sandbox through `runtime.Args`, with no marvel code change and a clean path to a
later curtain policy. Proved on gpt-5.6-luna in a throwaway team, torn down
after.

## The mechanism

codex's sandbox is set per launch (`-s/--sandbox read-only|workspace-write|
danger-full-access`, plus `--add-dir` for extra writable roots). Under
`workspace-write` the primary workspace (the process cwd) and the system temp
are writable, reads are allowed broadly, and network is off. marvel passes these
through: the codex adapter builds `codex exec --json --skip-git-repo-check
<args> <prompt>`, so `runtime.args = ["-m","gpt-5.6-luna","-s","workspace-write"]`
reaches codex verbatim, and the cast wrapper pins the cwd.

So the write surface is defined by two levers together: the sandbox mode, and
which directory is the cwd. Pin the cwd to a dedicated retro output dir that is
not inside a git repo, and that dir (plus temp) is the only writable surface.
`--add-dir` widens it to named extra roots when one output dir is not enough.
This is "restrict and define what it can write," not an open sandbox.

## The proof

Throwaway team `retro-sandbox-test`: one codex `retrospector` role, model
gpt-5.6-luna, `-s workspace-write`, cwd pinned to a retro output dir, with a
sibling `repo/` dir holding a file to read. Prompt: read `../repo/sample.go`,
write `./RETRO.md`, then attempt `../repo/INTRUSION.md` and report whether that
write was allowed. Additive to the live fleet (both live workspaces untouched);
deleted after.

Results, ground-truth from the filesystem and the codex rollout:

- **Model.** The rollout records `gpt-5.6-luna` (6 occurrences). marvel's `-m`
  arg was accepted and used; the codex default is gpt-5.6-sol, so this is the
  override taking effect, not the default.
- **Constrained write worked.** `RETRO.md` was written to the retro dir (the
  cwd), and the repo was read for context (the agent reported the package name
  `sample`).
- **Out-of-scope write was refused.** No `INTRUSION.md` was created in `repo/`,
  and the rollout records the refusal (`REFUSED`, `permission`, `read-only`).
  The write outside the cwd was denied by codex's own sandbox.
- **Completion semantics held.** `session.succeeded ... replica satisfied`
  (ADR-010), same as the reviewer in finding-046.

## One adapter gap worth a fix

When codex writes a file it emits a `file_change` item in its JSONL stream.
marvel's codex parser does not map that item, so each write surfaces as
`agent.error` with `data.kind = "unmapped"` ("unmapped codex item type:
file_change") rather than a clean `tool.result`. The write still lands; the
event ring just reports it as an error. A write-capable codex role (the
retrospector) hits this on every file it produces, so the ring reads noisier
than the run actually is. Flagged as a flat marvel task (aae-orc-tvlcg); it does
not block the role.

## The path to curtain (vision Gap 4)

The write restriction lives today in `runtime.Args` as codex's own sandbox mode
plus the pinned cwd, and optionally `--add-dir`. That is the interim enforcement
for a harness marvel does not project into: the claude adapter gets Layer-1
environment construction via `ProjectionFor` (finding-006), but the codex
adapter reports no projection surface (codex.go) and marvel injects no codex
sandbox, so the enforcement is codex's own, set at launch.

Expressed as a single named writable-root set (the retro dir, plus any
`--add-dir` roots), this maps one-to-one onto a later curtain policy
(permission-through-environment, `question-permission-model` Layer 1;
aae-orc-10x, sandbox composition with curtain). Keeping the write surface a
named parameter rather than scattering paths keeps that migration a mechanism
swap, not a redesign.

Related: finding-046 (the read-only reviewer, same team shape), finding-006
(Layer-1 environment construction for claude), question-permission-model (the
node this harvests; codex's own sandbox is the interim Layer-1 for a
non-projecting harness), ADR-010 (headless completion), the wardrobe
`retrospector` role (extended with this write surface).
