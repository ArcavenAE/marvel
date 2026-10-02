# probe brief: marvel as an MCP service provider, credential out of the seat's reach

**Date:** 2026-10-02
**Question:** `question-marvel-mcp-service-provider`
**Source idea:** `_kos/ideas/mcp-service-provider-and-credential-custody.md`
**Status:** brief, not started. Design only; no build is asked for.

## Hypothesis

An MCP front on a local socket that something other than marvel runs and
holds (a vault-side proxy), bound per scope by marvel, lets a seat
use a custody-class tool (a static API key; an OAuth token whose refresh
token stays out of marvel) while a read from inside the seat cannot recover
the credential, and every call is attributed to the seat. Injecting the
credential into the harness config, or a stdio sidecar, keeps it out of the
model's context but not out of the seat's reach without a sandbox profile.

**What would refute it:** a planted read from inside the seat recovers the
key through the front (its process env, its socket, a file it opens) **or
from the store the front reads** (a same-user keychain or vault query), or
the OAuth refresh can only happen with the refresh token in marvel's hands.
The store path is expected to refute it without a sandbox; the probe
measures whether a sandbox profile closes it.

## Method, in order (each step one command or one small fixture)

1. **Classify the two examples against the vendor docs** (INFERRED, read
   only). Perplexity: the API key's form and whether a scoped or
   short-lived key exists. Atmos Pro: the OAuth grant, the refresh token's
   lifetime, and whether the vendor offers a remote MCP server with its own
   OAuth. Record each artifact's audience and lifetime; sort by the ADR-009
   test.
2. **Harness matrix** (MEASURED where a harness is installed, INFERRED from
   docs otherwise). For claude, codex, crush and opencode: how MCP config is
   supplied (file, flag, env), whether remote MCP OAuth is native, and where
   that OAuth token is stored. marvel's codex path (`codex_home.go`) is the
   in-tree reference.
3. **Reach test, the decisive one.** Scratch seat under the daemon's OS user.
   Plant a dummy key (no real credential is used anywhere in this probe) in
   each shape: (i) env var in the harness config, (ii) a file path, (iii) a
   stdio sidecar's env, (iv) a front on a local socket that reads the key
   from the keychain at call time. From inside the seat, try to read it with
   the seat's ordinary tools (`env`, `cat`, `ps eww`, reading the sidecar's
   `/proc` or `ps` env, connecting to the socket and asking), and query the
   store directly (`security find-generic-password -w` for the dummy item,
   or the vault's CLI with whatever the seat's user can reach). Repeat under a
   curtain sandbox profile that denies the store and the daemon's state
   directory. Record which shapes leak with and without the sandbox.
4. **Refresh and revoke.** For the OAuth example, with a mock OAuth server:
   who performs the refresh in each shape, and whether marvel ever holds the
   refresh token. Withdraw the binding; the next call must fail without a
   vendor-console step.
5. **Attribution.** Through the front, one call from each of two seats is
   recorded with the seat label and binding, never the instance id.
6. **Scope inheritance.** A binding declared at the workspace, overridden at
   one team, withheld from one role: write the manifest shape and check the
   resolution table by hand against the parent's Binding resource.

## Evidence grades

MEASURED (observed here, at a version), INFERRED (reasoned, or read from a
vendor's docs without running it), HYPOTHESIS (untested). Every claim in the
finding carries one.

## Out of scope

Building the front, choosing a vault product, and any write to an operator's
real credential store. The probe uses dummy keys and a mock OAuth server only.

## Output

A finding in `marvel/_kos/findings/` that answers the six sub-questions with
grades, a recommended default shape per credential class, and the edit the
parent node's credential paragraph needs to match ADR-009.
