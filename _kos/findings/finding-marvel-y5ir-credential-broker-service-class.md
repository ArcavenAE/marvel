# finding-marvel-y5ir: a credential broker fits marvel as an IdP-neutral service class that hands seats a helper pointer, never a value

**Date:** 2026-10-09
**Confidence:** frontier. This is design read from code and vendor
documentation. No broker exists, and nothing here was run against a live
identity provider or cloud.
**Placement:** marvel. The subject is a service class marvel would run; the
identity providers and the services that accept its tokens are objects.
**Source:** a research report delivered to director on 2026-10-09, from
documentation and code reading (its section on changes in marvel). The code
cites below were re-read at marvel main 7561643. The vendor claims are the
report's and were not re-checked for this finding.
**Depends on:** seat RPC authorization (`aae-orc-bs3x`). This is a
prerequisite, not a part: the broker has no safe caller check until it lands.

## Why this matters

A seat that needs a short-lived token at a cloud or an API today gets it
from a helper the operator wires by hand, under the operator's own identity.
A broker would give each (team, role) its own identity and one-hour tokens,
with per-role attribution, while marvel still holds nothing that is bearer
authority at a third party (SOUL section 3, ADR-009). The report found that
marvel already has the seams this needs, so the class is an addition, not a
redesign.

## The shape

- **A new service class.** Register `credential-broker` beside the message
  bus in the class and provider registries (`internal/service/service.go:108-123`).
  Drivers: `generic-oidc`, a `mock`, and later vault-style drivers. A
  vendor such as Okta is a configuration profile of `generic-oidc`, never
  its own code path in core marvel. Provider settings ride in the cluster
  config's `Service` entry (`internal/config/config.go:237-300`), so the
  provider body stays inside its driver.
- **IdP-neutral first.** The first driver is generic OIDC client
  credentials with `private_key_jwt`: the role identity signs a short-lived
  assertion with a private key that never leaves the host, and the identity
  provider keeps only the public key. A cluster with no identity provider
  runs the mock driver. Core marvel imports no vendor SDK.
- **One identity per (team, role).** Seat names are
  `<team>-<role>-g<gen>-<idx>` (`internal/team/controller.go:1346`,
  `:2304`); the generation is a per-team counter (`internal/api/types.go:910`)
  and the index is max plus one, never reused (`controller.go:2533-2546`).
  An identity per generation or instance would need an identity-provider
  admin call on every shift. Per (team, role) matches the existing
  credential seam (`internal/session/manager.go:965`) and the per-role bus
  users (`internal/bus/declared.go:239-249`), and finding-068 ruling (ii),
  "the identity is the slot". The slot, generation and instance go in each
  token's downstream session name and its `jti`, not in the identity.
- **Seats get a helper pointer, never a value.** A `Credentials` interface
  on `session.Manager` beside `BusEnv` (`manager.go:44-47`, `:962-968`,
  `:1745-1751`) gives the seat the path of a helper; the helper's stdout is
  the token, and it stays between the harness and the helper. This is the
  pattern marvel already uses for backend credentials
  (`internal/api/backend_credential.go:5-16`, `internal/api/backend.go:87-103`),
  and it keeps the existing refusal of literal bearer variables in a role's
  env (`internal/runtime/adapter.go:488-506`).
- **The helper calls a broker RPC authenticated by the seat's heartbeat
  token** (minted at `internal/session/manager.go:578-587`). That RPC is the
  unbuilt seat RPC authorization in `aae-orc-bs3x`, hence the dependency
  above.
- **Revocation** on session exit, shift and reexec; revoking the role
  identity is one deactivation at the identity provider.

## What this does not settle

- Who provisions the non-exportable signing key, and whether a key marvel
  generates would be custody-adjacent. finding-068 ruling (i) holds: nothing
  is built on a marvel-held signing key until the operator rules.
- Whether replicas of one role need separate principals. Per-slot
  identities wait on a stable slot identity (marvel#363).
- Which downstream services accept the token by federation, and with which
  claim names. The report lists candidates from vendor documentation and
  marks the claim mappings unverified; a pilot against one service settles
  them.
- How marvel records a service it reaches through this broker while
  holding nothing. That is an open operator decision outside this finding.

## Next

No ticket is filed from this finding. A broker skeleton (the class, the
generic-oidc and mock drivers, the helper pointer in the session env, and
revoke on exit) becomes work only after `aae-orc-bs3x` lands and the
operator rules on the key question above.
