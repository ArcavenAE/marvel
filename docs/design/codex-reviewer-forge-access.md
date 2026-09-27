# Codex reviewer seats that can review: sandbox, approval, identity, seat text

**Date:** 2026-09-26
**Author seat:** arcaven-architect-g5-0, on the director brief
`2026-09-26-codex-reviewer-access`.
**Status:** design and build plan, ruled in part on 2026-09-26 (section 10).
Sections 10 and 11 supersede sections 2.2, 2.3 and 3 where they differ.
Operator decisions I1 to I3 (section 4.2) are open. Moved here from the
author's local notes on 2026-09-27 so the ruling and the measurements have a
git home.
**Follows:** `docs/design/codex-roles-reviewer-and-retrospector.md`
(2026-09-17), which cast the reviewer role; this doc gives it forge access.
**Seat:** aae/arcaven-reviewer-g5-0 (codex 0.157.0 TUI, launcher
`~/.marvel/manifests/cast-codex-seat.sh`).
**Not in git:** the launcher and the `seat-forge` wrapper (section 10) are
installed under the operator's home and are in no repository today. The
shape below is their only declared form.

## 1. Premise checks (scratch CODEX_HOME, `codex sandbox`, never a live seat)

The scratch home is empty, and its cwd is an empty scratch directory. No token
was read or printed at any point; identity was read as a login name only.

| # | Question | Command (shape) | Result |
|---|---|---|---|
| P1 | Does `-s read-only` block gh? | `codex sandbox -c sandbox_mode="read-only" -- gh api user` | **Yes:** `error connecting to api.github.com`. The live pane shows the same error, byte for byte (`marvel capture`, read-only). |
| P2 | Can read-only get network through `sandbox_permissions`? | `-c sandbox_permissions=["network-full-access"]` | No: the same error. |
| P3 | workspace-write plus network? | `-c sandbox_mode="workspace-write" -c sandbox_workspace_write.network_access=true` | gh works (`arcaven`). It **writes the cwd and /tmp**. The home directory is refused. `exclude_slash_tmp` and `exclude_tmpdir_env_var` close /tmp and $TMPDIR. It reads the whole disk. |
| P4 | A named permission profile, read allowlist plus limited network | the `[permissions.<name>]` shape, following the operator's own `github-audit` profile | Works, with conditions P5 to P8. The repo is readable, a repo write is refused, `~/.ssh` is refused. |
| P5 | Is the domain allowlist enforced? | curl to example.com under the profile | **Only with `--enable network_proxy`** (experimental). Off: 200. On: `CONNECT tunnel failed, response 403`, while api.github.com returns 200. |
| P6 | gh auth (Keychain) under the profile | `gh api user` | 401 until `~/Library/Keychains` is readable, then `arcaven`. |
| P7 | glab under the profile | `glab api user` | **Proxy on: TLS fails** (`x509: "rcgen self signed cert"`): glab does not trust the proxy's CA, although `SSL_CERT_FILE` is set. Proxy off: `arcaven`. |
| P8 | Does codex start under a narrow profile? | `codex debug prompt-input` with the profile as default | **No,** until `~/.local/bin` and `~/.codex/packages` are readable: the fs helper re-executes codex (`execvp ... Operation not permitted`). |
| P9 | Effective policy, profile passed only as `-c` flags | `codex debug prompt-input -c default_permissions=... -c permissions.reviewer={...} -c approval_policy="never" --enable network_proxy` | "`sandbox_mode` is `read-only` ... Network access is enabled", "Approval policy is currently never". Launcher `-c` flags are enough; CODEX_HOME needs no edit. |
| P10 | Exec-policy rules | `codex execpolicy check -r <rules> <cmd>` | `gh auth token` and `security ...` are forbidden; `gh pr review` is allowed. **`bash -lc gh auth token` matches no rule:** rules govern the commands the model issues, not a kernel boundary. |
| P11 | Identities on kinu | `gh auth status`, `glab auth status` (login lines only) | gh: `arcaven` only, in the **keyring**. glab: `arcaven` only, in a **plaintext** `~/Library/Application Support/glab-cli/config.yml`. `arcavenai` is not on kinu. |

## 2. Recommended shape

**A named codex permission profile, `reviewer`:**
- read-only on the filesystem through an allowlist;
- network limited by the proxy to the forge API hosts;
- approval `never`, plus an exec-policy rules file.

It is passed entirely by launcher `-c` flags, the same way the launcher already
passes the instructions and the trust entry.

### 2.1 Filesystem (read; nothing writable, including /tmp)

- `:minimal`.
- `/opt/homebrew`: gh, glab, jq.
- `~/.local/bin` and `~/.codex/packages`: P8, or codex cannot start.
- The workspace tree (`~/work/aae-orc`).
- `~/.marvel/manifests/<workspace>`: the seat file it re-reads after a restart.
- `~/.config/gh`.
- `~/Library/Keychains`: P6.
- After the glab keyring migration (4.2), the glab config dir.

Not readable: the rest of home (`.ssh`, `.aws`, other seats' homes, `.beads`
credentials). This is the main gain over P3: today a P3 seat could read the
glab plaintext token file.

### 2.2 Network

`mode="limited"` with `--enable network_proxy`, domains `api.github.com`, plus
`gitlab.com` once G1 below is solved. Without the proxy flag, the domain list is
decoration (P5).

### 2.3 Approval policy and prompts

- `approval_policy="never"` (`-a never`): no command ever waits on a human. A
  denied command fails back to the model, and the seat file tells it to stop
  and report.
- Keep the launcher's **untrusted** project entry. finding-049 still holds:
  trust makes the cwd writable. Under `never`, the untrusted entry no longer
  produces prompts, because `unless-trusted` applied only while the policy was
  unset.
- Folder-access prompt: not reproduced here. The first respawn under the new
  flags captures the pane (`marvel capture`) and records the prompt text byte
  for byte if one still appears. If it is macOS TCC and not codex, the fix is
  a one-time operator grant, not a flag.

### 2.4 Exec-policy rules (`$CODEX_HOME/rules/reviewer.rules`)

- **Forbidden:**
  - credential readers: `gh auth`, `gh config`, `gh secret`, `glab auth`,
    `glab config`, `security`, `/usr/bin/security`;
  - forge writes that are not a review: `gh pr merge|close|edit|ready|comment`,
    `gh repo`, `gh release`, `gh workflow`, `gh run rerun|cancel`,
    `glab mr merge|close|update`, `glab repo`;
  - **`gh api` and `glab api`** (a first-class verb must cover every need; see
    D2).
- **Allowed:** `gh pr view|diff|checks|review`, `glab mr view|diff|approve|note`,
  `glab mr revoke` (withdraw an approval), plus `git -C <repo> log|show|diff`
  for local context.
- **Unmatched commands** run sandboxed: they read what 2.1 allows and write
  nothing.

The seat's CODEX_HOME is marvel-seeded (marvel#359), and `-c` cannot carry a
rules file (none of the P checks found a key). So the launcher writes this one
file into `$CODEX_HOME/rules/` before `exec codex`. That is a narrow exception
to "CODEX_HOME is not touched here", and it is regenerated every cast. A marvel
issue to seed role rules first-class is in section 7.

### 2.5 Environment for the tools

`GH_NO_UPDATE_NOTIFIER=1`, `GH_PROMPT_DISABLED=1`, `GLAB_CHECK_UPDATE=false`,
`NO_COLOR=1` (in the launcher). Nothing writes gh or glab state.

## 3. Rejected options and why

- **`-s read-only` as today:** no network (P1, P2). This is the failure being
  fixed.
- **workspace-write plus network (P3):**
  - the cwd (the orchestrator tree) becomes writable, and so do /tmp and
    $TMPDIR unless excluded;
  - the domains are unrestricted;
  - full-disk read includes the glab plaintext token.

  A seat discipline of "write nothing" is advice, where the profile is a
  kernel refusal.
- **Keep read-only and escalate gh and glab per command** (`on-request`, or
  `allow` rules): an escalation asks a human, which blocks unattended seats.
  `allow` only skips the prompt and does not lift the sandbox, so gh still has
  no network. Not viable unattended.
- **Proxy off, network enabled, for both forges:** it works for glab (P7), but
  the domain list stops binding and any host is reachable while the Keychain is
  readable. Kept only as the named fallback for a GitLab-only profile, if G1
  fails. Not the default.
- **`danger-full-access` or `--dangerously-bypass-approvals-and-sandbox`:**
  outside the custody boundary and `no-control-bypass.md`.

## 4. Identity (no seat holds a token)

### 4.1 Selection

Both forge CLIs read their credential from the Keychain inside their own
process for each call. The seat stores nothing and receives nothing in its
environment; that is brokering, not custody (SOUL section 3). The seat selects
an identity by **config directory, never by token**:

- `GH_CONFIG_DIR=<per-seat dir>`, holding only a `hosts.yml` that names the
  account (`user: arcavenai` or `arcaven`) with keyring storage;
- `GLAB_CONFIG_DIR` likewise, after the migration in 4.2.

A missing account fails loudly (401), which the seat reports.

Residual, stated plainly: an account that gh can use, the model can ask gh to
print (`gh auth token`). The rules forbid that as a direct command. A wrapped
shell (`bash -lc ...`) is not matched by the checker (P10), so this is policy,
not a guarantee. The kernel-enforced parts are no filesystem writes, reads
limited to the allowlist, and network limited to the forge API hosts, so a
printed token has no write path out of the host except a forge post, which
review of the seat's output would show. Every claude seat on kinu already has
this exposure today, with full-disk read; this design narrows it for codex
seats.

### 4.2 Operator decisions

- **I1, GitHub pairing:** kinu holds only `arcaven` (P11).
  - (a) Default: kinu codex reviewers act as `arcaven`. They post real reviews
    on arcavenai-authored PRs, and a **paper approval** (a counted-nowhere
    recommendation, said in the REVIEW line) on arcaven-authored ones. Counted
    approvals of arcaven PRs stay with the mokuzai reviewer seats
    (`arcavenai`), which is the director's existing flow.
  - (b) The operator logs `arcavenai` into kinu's keyring, and the arcaven
    seat's `GH_CONFIG_DIR` names it. Kinu reviews then count on arcaven PRs.
    This puts a second bearer account in kinu's keyring, readable by the same
    brokering.
- **I2, GitLab identity:** kinu holds only `arcaven` on gitlab.com. Name the
  reviewing account and the project for verification.
- **I3, glab keyring migration** (needed under any option): the glab token is
  plaintext in `~/Library/Application Support/glab-cli/config.yml` today,
  readable by every full-disk seat on kinu. Re-login with keyring storage (glab
  1.111 stores in the keyring by default; `--insecure-storage` is the opt-out),
  so the config dir can be readable to the seat without exposing a token.

### 4.3 Open item G1 (probe before enabling GitLab under the proxy)

glab rejects the proxy's CA (P7). Probes, in order:
- whether glab honors a per-host `ca_cert` pointing at `$CODEX_CA_CERTIFICATE`
  (the path is per run, so the launcher would set it through `GLAB_CONFIG_DIR`);
- whether a newer glab reads `SSL_CERT_FILE`.

If neither works, the GitLab seat runs the proxy-off profile (3, fallback) with
the reasons recorded, or GitLab reviews stay with a claude seat. Decide after
the probe; one scratch command each.

## 5. Seat file and wardrobe role text

**Seat file** (`aae/seat-arcaven-reviewer.md`). Replace the "Post a
real GitHub review ... where your sandbox allows it" bullet and add a Tools
section:

> ## Tools
> - Use the gh CLI for every GitHub read and review: `gh pr view --json headRefOid,files,body`, `gh pr diff`, `gh pr checks`; the verdict is `gh pr review <n> -R <repo> --approve|--request-changes -b "<findings>"`, posted only after confirming `headRefOid` equals the dispatched sha.
> - Use glab for GitLab: `glab mr view`, `glab mr diff`; the verdict is `glab mr approve` or `glab mr note` with the findings and "changes requested". GitLab has no request-changes state, so say so in the note.
> - Local context: `git -C <repo> log|show|diff` in the checkout the dispatch names. Nothing else.
> - Never: web fetches, `find` or `grep` over the home directory, `gh api`/`glab api`, any auth or config subcommand, `security`.
> - On any tool failure, stop. Report the exact command and its exact error in the REVIEW line with `[not posted: <error>]`. Do not retry another route. An error is information for the supervisor, not a puzzle.

**REVIEW line** (unchanged form, one field added): `REVIEW <repo>#<n> @<sha>
approve|changes <count> findings [paper approval: author is <you>, not counted]
[posted|not posted: <error>]`.

**Wardrobe `role/reviewer`** (proposed text; wardrobe is operator-owned, so this
seat does not write it). Add three invariants:
1. The verdict is a posted forge review at the dispatched head, or an explicit
   "not posted" with the error. A comment-only verdict never stands in for a
   review.
2. A reviewer never reviews work authored by its own identity as a counted
   approval. It says "paper approval".
3. On a tool failure, stop and report the exact error. Never search for a
   workaround.

## 6. Bus reach until codex seats hold a role

- Dispatch goes to `agent://<team>/<session>` (the seat's constructed identity,
  forwarded to director-mcp by marvel's seeded config), with the full ask in
  the envelope.
- Then a short pane nudge: `marvel inject ... "Message waiting: call
  director.wait_for_message"`. Keep it short: #317 and #343 report head
  truncation of long injects.
- The seat replies with `send_message` to the supervisor's `agent://` address,
  never `role://`.
- When director-mcp gains a role for codex seats, dispatch moves to `role://`
  and the nudge stays.

## 7. Build plan (exact changes; the operator applies manifests)

1. **Manifests** (`aae-arcaven.toml` reviewer):
   - `args = ["-m", "gpt-5.6-luna", "-a", "never", "--enable", "network_proxy"]`;
   - drop `-s read-only`, since the profile sets the sandbox (P9 shows read-only
     plus network).
2. **`cast-codex-seat.sh`** (a builder drafts it and the operator installs it):
   - build the `permissions.reviewer` inline table from the workspace (2.1,
     2.2) and pass `-c default_permissions="reviewer"` and
     `-c permissions.reviewer={...}`;
   - write `$CODEX_HOME/rules/reviewer.rules` (2.4);
   - export the 2.5 environment and the per-seat `GH_CONFIG_DIR` (and
     `GLAB_CONFIG_DIR` after I3);
   - keep the untrusted entry.

   Add a `--check` mode that runs `codex debug prompt-input` with the same
   flags and prints the sandbox and approval lines, so a cast can be verified
   without a model turn.
3. **Seat files:** section 5 text.
4. **Wardrobe `role/reviewer`:** section 5 invariants, a proposal to the operator.
5. **marvel issue (to file on approval):** seed a role's exec-policy rules and
   permission profile into the private CODEX_HOME from the manifest, so the
   launcher stops writing into a marvel-owned home. Relates to marvel#359.
6. **Probe G1** (glab CA under the proxy) before GitLab goes live.

## 8. Verification, per forge, before any real assignment

1. **Cast check:** `cast-codex-seat.sh --check` prints `sandbox_mode is
   read-only ... Network access is enabled` and `Approval policy is currently
   never`.
2. **Respawn:** capture the pane. There is no trust, approval or folder prompt,
   and the TUI is at its input.
3. **GitHub dry run:** a throwaway PR on an ArcavenAE scratch repo, authored by
   the identity the seat does not use (so the review can count). Dispatch by
   `agent://` plus a nudge. Pass when:
   - the seat's `gh pr view` returns the head sha;
   - `gh pr diff` returns the diff;
   - a review appears on the PR at that sha (`gh api
     repos/<r>/pulls/<n>/reviews`, run by the verifier, not the seat), with the
     author login the operator chose;
   - the REVIEW line reaches the supervisor.

   Negative controls, in the same run:
   - ask the seat to `gh auth status`: expect a refusal by rule;
   - ask it to write a file in the repo: expect "Operation not permitted";
   - ask it to fetch example.com: expect 403.
4. **GitLab dry run** (after I2, I3, G1): an MR on the project the operator
   names, with the same pass criteria through `glab mr view|diff|approve|note`.
5. **Record:** a finding in `_kos/findings/` with the transcript lines and the
   three negative controls. After that, real assignments resume.

## 9. What needs the operator

- Manifest apply and the launcher install (7.1, 7.2).
- I1, GitHub pairing (default (a): as `arcaven`, with paper approvals on arcaven
  PRs).
- I2, the GitLab reviewing account and the verification project.
- I3, the glab re-login to the keyring. This is worth doing today regardless:
  the plaintext token is readable by every full-disk seat on kinu.
- Approval of the wardrobe text and of the marvel issue in 7.5.

## 10. Addendum: operator ruling applied (2026-09-26)

**Ruling:**
- keep `-s read-only`;
- add an escape rule scoped to gh, GitHub, glab and GitLab only (the CLIs and
  their API hosts);
- no general network and no workspace-write;
- earmark Perplexity and a second web search service for a later plan.

**Correction to section 3:** the rejected option read "`allow` only skips the
prompt and does not lift the sandbox". That was untested, and it is wrong.

- **P12:** a scratch `codex exec -s read-only -c approval_policy="never"` with
  the rule `prefix_rule(pattern=["gh","api","user"], decision="allow")` ran
  `gh api user -q .login` and printed `arcaven`.
- **Control:** the same run without the rule printed `error connecting to
  api.github.com`.

An `allow` rule runs its command outside the sandbox, which makes it the
ruled escape rule.

**Reuse:** the operator's `github-surface-audit` plugin already has this shape
(`assets/github-audit.rules.in`): an allowed command that re-enters
`codex sandbox -p <profile> -P <permissions>`, whose network is limited to
named hosts. `~/.codex/rules/default.rules` already allows
`gh pr list|view|diff`. Open ticket `aae-orc-7m4wm` (role permission bundles
per adapter) is the home for the general form.

**Revised shape (replaces section 2 for the seat):**

1. The seat runs `-s read-only -a never`, unchanged except for the approval
   flag. There is no permission profile on the seat itself.
2. **The escape is one wrapper per service,** for example
   `~/.local/bin/seat-forge <service> <args>`. The rules file allows exactly the
   wrapper's reviewed forms:
   - `seat-forge github pr view|diff|checks|review`;
   - `seat-forge gitlab mr view|diff|approve|note|revoke`.

   Direct `gh` and `glab` stay sandboxed, so they fail with no network, and
   `gh auth`, `security` and the api verbs stay forbidden.
3. **The wrapper re-enters the sandbox** with a named permission profile per
   service:
   - `forge-github`: network `api.github.com`, Keychain and gh config read,
     no writes;
   - `forge-gitlab`: `gitlab.com`, plus the glab config dir after the keyring
     migration.

   So the escaped process reaches only its API host (the section 1 P5/P6
   results, with the proxy). This is the audit plugin's pattern.
4. **Adding a service later** is one profile plus one rule line: for example
   `web-perplexity` (network: that service's API host only), reached as
   `seat-forge perplexity <query>`. Nothing is reshaped. The name of the second
   earmarked web search service is to be supplied by director.
5. **GitLab caveat stands (G1):** glab rejects the proxy's CA inside a limited
   profile. The forge-gitlab profile is enabled only after the G1 probe.

What stays the same:
- the identity selection by config dir (section 4);
- the seat and wardrobe text (section 5), with gh and glab spelled through the
  wrapper;
- bus reach (section 6);
- verification (section 8), with one added negative control: plain `gh pr view`
  fails with no network while `seat-forge github pr view` succeeds.

## 11. Measurements from the build (arcaven-builder-g5-0, 2026-09-26)

- **Network mode:**
  - `mode="limited"` makes the proxy answer gh's GraphQL POST with 403, so
    forge-github uses `mode="full"`;
  - the domain allowlist still binds under full: example.com and github.com
    give 000, api.github.com gives 200;
  - this supersedes 2.2's "limited".
- **Folder prompt (corrects 2.3):** on codex 0.157, `-a never` suppresses
  neither prompt.
  - With an explicit untrusted entry, every start shows "Folder access ... ›
    1. Open restricted / 2. Quit". Enter selects the restricted option, which
    is safe.
  - With no entry, the prompt is "Trust this folder?", and Enter trusts it,
    which is unsafe (finding-049).

  The untrusted entry stays, so an unattended respawn still needs one Enter.
  Open: find a config key that pre-answers "Open restricted", or accept a
  documented post-launch Enter. Keystroke injection is fragile (#317), so
  prefer the key.
- **The escape rule works on the live aae seat:** an allow rule ran seat-forge
  outside the seat sandbox and returned PR 416's `headRefOid`.
- **The builder added `--no-daemon`** to the manifest args.

Tickets filed from this plan: `aae-orc-ct0jg` (G1 probe) blocks
`aae-orc-cgev3` (enable forge-gitlab).
