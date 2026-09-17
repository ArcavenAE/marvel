# finding-049: codex project trust defeats the -s sandbox, and a role-scoped CODEX_HOME restores it

finding-046 and finding-048 proved codex roles safe using codex's own `-s`
sandbox: the reviewer read-only, the retrospector constrained to one writable
dir. Both proofs ran in throwaway teams. Against the live `aae/arcaven` target
they do not hold, because the live repo is a codex trusted project, and a
trusted project is writable regardless of `-s`. The same lever that breaks the
guarantee also fixes it: give the codex role its own `CODEX_HOME` whose config
does not trust the target, and `-s` governs writes again, with the operator's
global `~/.codex/config.toml` untouched. All results below are ground-truth from
the filesystem, not the model's self-report, and that distinction is itself the
first finding.

## The model says "refused" while the write lands

The two-part write-verify in the design doc (finding-048, the retrospector
apply gate) asks the role to write its retro dir and then attempt a write into
the repo, and calls the role misconfigured if the second write is allowed. Run
against the live target through `cast-aae-retro.sh` (`-s workspace-write`, cwd
pinned to `~/.marvel/retro/aae`), the model reported the repo write "refused."
The file was on disk. Only checking the filesystem caught it. Every result in
this finding is disk-verified for that reason; a codex role's own account of
whether a write was refused cannot be trusted as the verify signal.

## The mechanism: trusted project overrides -s

`~/.codex/config.toml` carries per-project trust:

```
[projects."/Users/.../work/aae-orc"]
trust_level = "trusted"
```

A trusted project's tree is writable by codex's `apply_patch`/`file_change`
tool no matter the `-s` mode. The shell-exec tool is still sandboxed (its
attempts log `exec_command failed: Rejected`), but the model's own file-editing
tool writes through. Isolated across four disk-verified runs on gpt-5.6-luna:

- `-s workspace-write`, cwd = the trusted retro dir: wrote its cwd, a
  non-trusted `$HOME` dir, AND the repo. Sandbox effectively open.
- `-s workspace-write`, cwd = an untrusted dir: wrote its cwd and the trusted
  repo.
- `-s workspace-write`, cwd untrusted, plus `-c projects."....aae-orc".
  trust_level="untrusted"` at invocation: STILL wrote the repo. A `-c` override
  of the projects table does not lift the config-file trust.
- `-s read-only`, cwd untrusted: the untrusted-cwd write was refused (correct),
  the trusted-repo write succeeded. The only difference between the two targets
  is trust, so trust is the cause, not the sandbox mode.

Confirmed with the actual wrappers: `cast-aae-codex.sh` (reviewer, `-s
read-only`, cwd = the trusted repo) wrote a file into the repo. So the
reviewer's "advisory, cannot write the tree" guarantee (finding-046) does not
hold on the live target either. Both roles point at the trusted repo, so neither
was sandbox-safe as-designed.

Scope correction, not a reversal: finding-046 and finding-048 are correct for an
UNtrusted project (their throwaway teams were fresh dirs). The design assumed the
target behaves like those teams. The live repo is trusted for the operator's own
convenience, which the design did not account for.

## The fix: a role-scoped CODEX_HOME (menu option c)

codex reads its config from `CODEX_HOME` (identity already travels by env; the
adapter seeds `CODEX_HOME` from `~/.codex`). Point the role at its own
`CODEX_HOME` whose `config.toml` does not trust the target project, symlink the
identity files back to the operator's home, and `-s` governs writes again.

Built and disk-verified. Role home: a `config.toml` carrying the model and
`[mcp_servers.director]` (see below) and NO project-trust entries, plus
`auth.json` and `models_cache.json` symlinked from `~/.codex`. Then, on
gpt-5.6-luna:

- Retrospector shape (cwd = retro dir, `-s workspace-write`): wrote `RETRO.md`
  to the retro dir, READ `~/work/aae-orc/charter.md` for context, and the repo
  write was REFUSED (`patch rejected: writing outside of the project`).
- Reviewer shape (cwd = the repo, `-s read-only`): the repo write was REFUSED
  (`writing is blocked by read-only sandbox`).

The operator's global `~/.codex/config.toml` is never touched, so their own
interactive codex in `aae-orc` keeps its trusted convenience. This is the
menu's option (c), decoupling trust per role. Options (a) untrust `aae-orc`
globally and (b) curtain (kernel-enforced, `aae-orc-10x`) remain the operator's
to weigh; (c) is the one that needs no operator config change and no new code
beyond the wrapper.

director-mcp is preserved under (c): for a codex role the bus MCP comes from
`CODEX_HOME/config.toml [mcp_servers.director]`, not the wardrobe `tools` field
and not claude's launch `--mcp-config`. The role home must keep that block. A
minimal role config that omits it would silently drop the retrospector's
reports_to/escalates_to channel.

## Wrapper shape (for the cast wrappers, not applied here)

Both `cast-aae-codex.sh` and `cast-aae-retro.sh` gain: build or reuse a
role `CODEX_HOME` under a fixed path (for example `~/.marvel/codex-home/aae`),
whose `config.toml` is the operator's config with every project-trust entry
stripped while keeping `[mcp_servers.director]` and the model line, and whose
`auth.json` + `models_cache.json` are symlinks into `~/.codex`; then
`export CODEX_HOME=<that dir>` before `exec codex`. The retro dir and the repo
must both be absent from the role config's trusted set. The two-part write
verify (retro write allowed, repo write refused, both on disk) is the apply
gate, and it must read the filesystem, never the model's report.

## Bearing

- The live apply of both codex roles is held until (a), (b), or (c) is chosen
  and the write verify passes on disk.
- `question-permission-model` Layer 1 for a non-projecting harness (finding-048)
  is qualified: codex's own sandbox is Layer 1 ONLY when the target is not a
  codex trusted project. Trust is an out-of-band escalation the `-s` flag does
  not see. A role-scoped `CODEX_HOME` is how marvel controls that trust without
  owning the operator's config, which is the same environment-construction
  posture as the claude projection, one config layer lower.
- The `file_change`-reads-as-`agent.error` parser gap (finding-048,
  aae-orc-tvlcg) is unrelated and still open.

Related: finding-046 (the read-only reviewer, scoped to untrusted here),
finding-048 (the constrained write surface, scoped to untrusted here),
finding-045 (codex/claude folder-trust gate, the adjacent trust surface),
question-permission-model (Layer 1 for a non-projecting harness), ADR-009
(the audience test: symlinking auth into a role home is issuance-adjacent, not
custody, the seed is not held), aae-orc-10x (curtain, option b), the wardrobe
`retrospector` and `reviewer` roles.
