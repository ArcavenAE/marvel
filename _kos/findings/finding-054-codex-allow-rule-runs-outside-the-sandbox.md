# finding-054: a codex exec-policy `allow` rule runs its command outside the sandbox, which is both the ruled escape and a hole

- **Date:** 2026-09-26, measured; filed 2026-09-27
- **Seat:** arcaven-architect-g5-0 (premise checks), arcaven-builder-g5-0 (build measurements)
- **Subject:** the codex runtime adapter's permission surface, so marvel's graph
- **Instrument:** a scratch `CODEX_HOME` and `codex sandbox` / `codex exec` from an empty scratch cwd; never a live seat. No token was read or printed; identity was read as a login name only.
- **Design it grounds:** `docs/design/codex-reviewer-forge-access.md` (sections 1, 10, 11)

## 0. The sentence a reader needs first

**An exec-policy rule with `decision="allow"` does not merely skip the
approval prompt: codex runs the matched command outside `-s read-only`.**
That makes an allow rule a sandbox escape, which is exactly what the
operator ruled for forge access on 2026-09-26, and it means every allow
rule in a codex home is a network and filesystem grant, whatever it was
written to do.

## 1. The measurement

- `codex exec -s read-only -c approval_policy="never"`, with the rule
  `prefix_rule(pattern=["gh","api","user"], decision="allow")`, ran
  `gh api user -q .login` and printed the login.
- **Control:** the same run without the rule printed `error connecting to
  api.github.com`.

The design's first draft said the opposite ("`allow` only skips the prompt
and does not lift the sandbox"). That claim was untested and wrong; the
design carries the correction (section 10).

## 2. What bounds the escape

The rule matches the command the model issues, not what runs inside it.
`codex execpolicy check` forbids `gh auth token` and `security ...` as
direct commands, but `bash -lc gh auth token` matches no rule. So rules are
policy, not a kernel boundary.

The ruled shape therefore allows exactly one wrapper per service
(`seat-forge <service> <verb>`), and the wrapper re-enters `codex sandbox`
under a named permission profile whose network is one API host. The escape
then reaches only that host. This is the operator's existing audit-plugin
pattern.

## 3. The network facts the profile depends on

- `-s read-only` blocks all network, and `sandbox_permissions` does not lift
  it.
- A named profile's domain allowlist is **enforced only with
  `--enable network_proxy`** (experimental). Without it, example.com
  returns 200. With it, `CONNECT tunnel failed, response 403`.
- `mode="limited"` makes the proxy answer gh's GraphQL POST with 403, so
  the GitHub profile uses `mode="full"`. The domain allowlist still binds
  under full: example.com and github.com give 000, and api.github.com
  gives 200.
- glab rejects the proxy's CA (`x509: "rcgen self signed cert"`), even with
  `SSL_CERT_FILE` set. GitLab under the proxy waits on probe
  `aae-orc-ct0jg`.
- codex under a narrow read allowlist does not start until
  `~/.local/bin` and `~/.codex/packages` are readable: its fs helper
  re-executes codex.

## 4. The folder prompt that an unattended respawn still hits

On codex 0.157, `-a never` suppresses neither folder prompt.

- With an explicit **untrusted** entry for the workspace, every start shows
  "Folder access ... 1. Open restricted / 2. Quit". Enter selects the
  restricted option, which is safe.
- With **no** entry, the prompt is "Trust this folder?", and Enter trusts
  it, which re-opens finding-049's hole.

So the untrusted entry must stay, and an unattended respawn still needs one
Enter. No config key that pre-answers "Open restricted" has been found.
Keystroke injection is fragile (#317), so the key, when found, is preferred
over an injected Enter.

## 5. Bearing on marvel

- The permission node's finding-049 qualification now has a second clause:
  constructing the role's `CODEX_HOME` controls trust and review policy,
  but any allow rule placed in that home is a grant, so the rules file is
  part of the constructed permission surface, not decoration.
- The launcher and the `seat-forge` wrapper that implement this are
  installed under the operator's home and are in no repository. Seeding a
  role's rules and permission profile from the manifest into the private
  `CODEX_HOME` is design item 5 (to be filed; relates marvel#359 and
  `aae-orc-7m4wm`).
