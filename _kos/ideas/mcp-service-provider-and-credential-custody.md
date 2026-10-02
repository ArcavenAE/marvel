# marvel as an MCP service provider, with the credential kept out of the seat

- **Status:** idea (pre-hypothesis, no commitment). Extracted the same day into
  the frontier node `question-marvel-mcp-service-provider` and the probe
  brief `_kos/probes/brief-marvel-mcp-service-provider.md`.
- **Date:** 2026-10-02
- **Subject:** marvel. The MCP servers and their vendors are objects marvel
  serves; the harnesses are objects it configures.
- **Related:** `question-marvel-service-provider-shape` (the parent: trust
  plane, Endpoint vs Binding, Projection's inject mode),
  `docs/design/service-provider/`, `_kos/ideas/services-catalog.md`,
  `question-agent-communication-broker` (per-session credential injection),
  `question-bd-managed-extended-service` (its custody section).

## The operator's words

> "kos question marvel how might marvel act as service provider (within
> workspaces? in teams? by agent roles?) for mcp, use perpexity and atmos pro
> as examples, and how might it manage/inject and handle the mcp, set it up or
> otherwise manage the credential, the api token, the oauth, jwt, etc to keep
> that out of the agent session control"

## The idea, in one sentence

An MCP server is a service marvel can bind to a scope (workspace, team, role
or seat) and deliver to a seat, and the credential that server needs is
either brokered from a store marvel does not own or held by a front marvel
runs, so the seat can use the tool without ever holding the secret.

## The governing rule

SOUL section 3 and ADR-009: the credential boundary is audience, not format,
and the test applies to the most durable artifact a component holds. A
component may hold what it can itself revoke or re-mint; it must not hold
bearer authority at a third party. So each credential below is sorted by
what its most durable artifact is, not by its name:

| example | most durable artifact | class | what marvel may do |
|---|---|---|---|
| Perplexity | a static API key, bearer at the vendor | custody | broker it from a store at the moment of use, or front the call; never write it to marvel's state |
| Atmos Pro | an OAuth refresh token for one workspace, bearer at the vendor | custody | never hold the refresh token; let the store or the harness's own OAuth do the refresh, and at most pass on a short-lived access token |
| a JWT marvel mints for a seat | a token marvel signs and can revoke | issuance | mint, scope, rotate and revoke it freely |

(The Perplexity and Atmos Pro classes are as the operator's relay states
them, not checked against either vendor here; the probe brief checks them.)

## What marvel already does that points the way

- It writes one MCP server entry into a codex seat's home, carrying only how
  to start the server and dropping the operator's env table, so an identity
  in that table is "never held" (`internal/runtime/codex_home.go`,
  `codexMCPServer`).
- It hands the director shim its bus password as a file path
  (`DIRECTOR_NATS_PASS_FILE` pointing at a 0600 file beside the rendered
  broker conf, `internal/bus/manager.go`), not as a value in the seat's env.
- It mints a per-session heartbeat token, which is issuance (SOUL section 3).

The second one shows the limit to design around: seats run as the same OS
user as the daemon, so any file marvel can read, a seat can read, unless a
sandbox profile denies it. Keeping a credential out of the seat is a
sandbox property, not a file-mode property.

## Shapes to compare

1. **Front.** marvel runs one MCP proxy per binding on a local socket. The
   seat's harness config names the socket; the proxy holds the credential
   (fetched from the store at call time) and makes the vendor call. The seat
   can use the tool and cannot read the key. The proxy is where per-seat
   audit and rate limits live.
2. **Inject.** marvel writes the harness's MCP config at spawn (as it does
   for codex), with the credential delivered as a file path or env var. The
   simplest, and the credential is in the seat's reach unless the sandbox
   hides it.
3. **Sidecar.** marvel starts the vendor's stdio MCP server as a child with
   the credential in the child's env, and the harness talks to it over
   stdio. The child holds the secret; the seat's tools may still reach the
   child's env through the process table unless the sandbox denies it.
4. **Delegate.** For a vendor that offers a remote MCP server with its own
   OAuth, the harness does the OAuth itself (SOUL section 3, "delegate auth
   to the tool that owns it"), and marvel only binds the server URL. marvel
   then holds nothing, but the token lives in the harness's store, which is
   inside the seat's user.

## Tensions

- The parent node says marvel "holds session state on the agent's behalf (for
  example the OAuth or Claude-backend session)". That text predates ADR-009
  (2026-09-04), which forbids holding a refresh token. The new node follows
  ADR-009 and flags the parent's wording for a later edit.
- "Keep it out of the agent session control" has two readings: out of the
  model's context (easy; any shape does it) or out of the seat's reach (only
  the front does it without a sandbox). The probe has to say which the
  operator means, or design for the stronger one.
- Harnesses differ in how they take MCP config (a config file, a flag, an
  env var) and whether they do remote OAuth at all.
