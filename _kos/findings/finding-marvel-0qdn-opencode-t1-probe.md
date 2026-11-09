# finding-marvel-0qdn: opencode T1 probe, eleven measurements on tool naming, config, provider sign-in, first start and rule matching

**Date:** 2026-11-09
**Confidence:** frontier. Eleven measurements on one host, one opencode version
and one 0.6B model. Where a result is inferred, it says so.
**Placement:** marvel. The subject is how marvel launches and constrains an
opencode seat; opencode is the object measured.
**Refs:** aae-orc-rurrg (opencode end to end)

## Why this matters

The opencode chain has three links and one write run that rest on facts only a
live run can settle: how a director tool is named under a deny-everything rule,
where provider sign-in lives, and whether a private home needs its own provider
entry. These eleven measurements let the marvel owner rule on those links from
evidence instead of inference, and two of them change what the chain must guard
against: a private home needs a provider entry or the seat hangs, and an
ambient provider key chooses the model when none is configured.

## Run conditions, as met

- Version: opencode 1.18.34; model ollama qwen3:0.6b on 127.0.0.1:11434 (already
  on the host; nothing fetched). No key of any kind.
- Scratch home: every call ran as `env -i HOME=<S>/home PATH LANG TMPDIR=<S>/tmp`
  with all four XDG variables inside scratch, bounded by a perl alarm, under
  `sandbox-exec`. Cases 5 to 9 each used a fresh XDG tree.
- No bus or credential names: the sandboxed environment held only HOME, PATH,
  LANG, TMPDIR and the four XDG variables.
- Localhost only. **The first attempt failed.** The profile carried
  `(allow network* (local ip "localhost:*"))`, and both `curl` to 1.1.1.1:80 and
  `nc` to 1.1.1.1:443 connected, so that form does not confine a run to
  localhost. The run stopped before any opencode call. **Retry:** the profile
  `(deny network*)` followed by `(allow network* (remote ip "localhost:*"))`,
  plus `deny network-outbound` for the five live local ports 3307, 4222, 4242,
  7422 and 7442, which were never dialed. Under it, `nc` to 1.1.1.1:443 and
  192.0.2.1:80 failed with `connectx ... failed: Operation not permitted`,
  `curl` logged `Immediate connect fail for 1.1.1.1: Operation not permitted`,
  and a Python connect raised errno 1. The control was re-run at the start of
  cases 5 to 9 with the same refusal. Ollama answered 200.
- Write proof (sandbox form): writes outside scratch were denied;
  `touch /private/tmp/t1-blocked-probe` was refused with `Operation not
  permitted` and the file was absent afterwards, and a write inside scratch
  succeeded.
- Method note: `opencode debug agent` lists built-in tools only, so cases 1 and
  2 used a small localhost recorder script, set as the provider base URL. It
  logged the tool names in each chat request and answered 500.

## Results

1. **The director tool key under a `"*"` deny. MEASURED.** An MCP tool reaches
   the model as `<server>_<tool>` (`director_send_message`). `{"*":"deny"}`
   removes the director tools with every built-in tool. `"director_*":"allow"`
   restores both stub tools and nothing else; an exact `director_send_message`
   restores one; a colon key `director:send_message` restores none.
2. **`--auto` under a deny-first block. MEASURED in part, INFERRED in part.**
   `--auto` does not bring back a tool hidden by `"*":"deny"`, and with
   `"*":"ask"` the tool list is the same with and without it. That `--auto`
   approves `ask` rules without a prompt is from `--help` ("auto-approve
   permissions that are not explicitly denied"), not measured.
3. **Lever precedence. MEASURED.** Permissions merge per key, the later lever
   winning: global file < workdir `opencode.json` < `OPENCODE_CONFIG_CONTENT` <
   `OPENCODE_PERMISSION`. A workdir file can add a key the projection does not
   name.
4. **A private `XDG_CONFIG_HOME` drops the global config. MEASURED.** opencode
   loads its global config only from `$XDG_CONFIG_HOME/opencode/`; a marker in
   the scratch global file vanished when the variable pointed elsewhere.
5. **A private home needs a provider entry. MEASURED, one point INFERRED.** With
   none, the local model is not found (`Provider not found: ollama`) and a run
   dials the remote default `opencode/big-pickle`, is refused, and never
   answers. With the entry, the run completes. No `models.json` appears in the
   private cache; that the catalog fetch was refused is inferred.
6. **Ambient non-Anthropic keys are read. MEASURED.** Dummy `OPENAI_API_KEY` and
   `OPENROUTER_API_KEY` made both providers configured (61 and 387 models
   listed). With a configured ollama model, build and title stayed on it. With
   no provider configured, the OpenRouter key chose the model for build and
   title; the sandbox refused the connection.
7. **Provider auth lives at `$XDG_DATA_HOME/opencode/auth.json`. MEASURED** (path
   named by `auth list`); that `auth login` writes it there is INFERRED. `auth
   list` creates nothing. `opencode auth` is an alias of `opencode providers`.
8. **`OPENCODE_PERMISSION=""` counts as unset. MEASURED.** Empty and `{}` change
   nothing; a value overrides per key.
9. **First start with a private config and network denied. MEASURED.** rc=0,
   about 6.9 s to first output, 9.3 s wall; no plugin, npm or install activity
   and no refused connection in the DEBUG log; the cache stayed empty. An
   earlier design note assumed the first start installs a plugin; that does not
   hold for 1.18.34 with no plugin configured. With a plugin named, untested.

10. **The exact-glob rule order and the re-append. MEASURED.** `opencode debug
    agent build` prints the resolved rules in order. With `{"*":"deny"}`, the
    user rules come after opencode's defaults, and then opencode re-appends
    `external_directory allow <XDG_DATA_HOME>/opencode/tool-output/*` after
    them, so that allow is last and wins under last-match (an earlier design
    note assumed this, and it holds). An `external_directory` exact deny with
    the same pattern string replaces that allow, and nothing follows it. The
    same deny written as `edit` or `read` path rules does not: the allow is
    still re-appended last. `external_directory` patterns are absolute, with
    the leading slash.
11. **The matcher table. MEASURED.** A scripted localhost recorder issued one
    `read` call per row, and opencode's DEBUG log printed its own evaluation of
    the rule against the path. With `"read": {"*":"allow", "<rule>":"deny"}`
    and a path under a directory `B`:

    | question | rule and file | outcome |
    |---|---|---|
    | does `*` cross `/`? | `B/a/*` on `a/b/c` | yes, denied |
    | does `**` differ from `*`? | `B/a/**` on `a/b/c` | no difference seen, denied |
    | what does `?` match? | `B/a/?` on `a/x` and `a/xy` | one character: `a/x` denied, `a/xy` allowed |
    | is matching anchored? | `a/x`, `*a/x`, `*/a/x` | at both ends: `a/x` did not match the full path, `*a/x` matched `za/x`, `*/a/x` matched |
    | leading slash | `/B/a/x` on `a/x` | `read` paths are evaluated without their leading slash, so it did not match |
    | case-sensitive? | `B/A/*` on `a/x`, `B/a/*` on `A/x` | yes, neither matched |

    The host filesystem is case-insensitive, so `A/x` and `a/x` are one file;
    the rows show the matcher compares the requested path string, not the file.

## What each result unblocks

- **Case 1, widening the policy gate to every opencode role:** the director
  allow key that matched is `director_*`, or exact `director_<tool>` names. Nothing
  measured blocks the widening on that key, and the colon form restored no tool.
- **Case 5, the provider entry for a private home:** the open question was
  whether a seat can run with no provider entry. It cannot: an entry is needed,
  or the seat dials a remote default and hangs. The step that waits on
  marvel#411 still waits, and no option without an entry remains.
- **Case 7, the scope of the per-seat data folder:** moving `XDG_DATA_HOME`
  takes `auth.json` with it, and linking that file in would place third-party
  bearer material in a marvel-written tree, which the credential custody
  boundary (SOUL section 3, ADR-009) governs. The measurement supports
  treating the scope as "exempt": a seat on a paid provider would keep the
  operator's own data folder, so its sign-in file stays where the operator put
  it and marvel never copies it. This finding does not make that decision.
- **Case 6, ambient provider keys:** an ambient non-Anthropic key is used when no
  provider is configured, and marvel records only three Anthropic key names.
  This supports a host sandbox profile on the first write run, and argues for
  stripping named provider keys on any opencode seat that has no profile.
- **Case 3 and case 8, a leftover permission variable:** a non-empty leftover
  `OPENCODE_PERMISSION` overrides the projection; an empty one does not. The
  `env -u` in the pane prefix is needed and sufficient.
- **Case 4, where `XDG_CONFIG_HOME` moves:** it must move only in the step that
  seeds the director config, since moving it drops the global file.
- **Case 2, `auto-unsandboxed`:** nothing measured argues against the existing
  refusal; a deny-first block with no
  `ask` left makes `--auto` harmless on tools.
- **Case 9, first start:** a pre-seeded config directory is not needed for a
  first start with no plugin configured.
- **Case 10, the projection's second fixture:** it writes
  `external_directory: {"<XDG_DATA_HOME>/opencode/tool-output/*": "deny"}`,
  the pattern byte for byte as opencode prints it (absolute, leading slash). An
  `edit` or `read` deny is not a substitute.
- **Case 11, which glob forms may be claimed as measured for 1.18.34:** `*`
  (crosses `/`), `**` (same as `*` in the one row tried), `?` (one character),
  whole-string anchoring, case-sensitive comparison, and `read` paths without a
  leading slash. Every other form (character classes, braces, negation,
  escapes) returns "unverified".
- **The end-to-end write run** still waits on the host sandbox profile decision
  and on the earlier steps of the chain.

## Limits

One host, one opencode version, one 0.6B model. The recorder answered 500, so no
run in cases 1 and 2 reached a tool call. Case 2's ask-to-allow behavior and
case 7's write location on login are inferred. Case 11 measured only the forms
in its table; any other pattern form is unmeasured. Every recommendation drawn
from this finding is valid until 2026-11-16 and is re-checked then; nothing
takes effect on silence.
