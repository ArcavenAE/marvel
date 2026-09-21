# mrvl proxy: NATS over mrvl, and generic RBAC-gated relays

Status: idea (pre-hypothesis). Subject: marvel (the mrvl remote-admin CLI).
Objects: director reach, the NATS bus, switchboard, RBAC.

## The itch

Director sits on both the local and the global bus, but it cannot always talk
to a cluster directly. The global tier depends on a NATS leaf mesh between
hosts, and that path is fragile: the 2026-09-21 backslide investigation found
dispatches to mokuzai piling up in `GLOBAL_TO_mokuzai` unconsumed, with the
topology technically up but nothing reachable end to end. When the direct path
is unavailable (NAT, firewall, a leaf that will not form, a host that is only
reachable by ssh), there is no first-class way for an authorized client to
reach that cluster's bus.

We already carry the pieces of an answer. mrvl embeds SSH for remote admin,
with TOFU and kubeconfig-style cluster definitions. kubectl solved the same
shape years ago: `kubectl proxy` and `kubectl port-forward` tunnel arbitrary
API and TCP traffic through the API server's already-authenticated channel, so
a client that can reach the control plane can reach a workload it otherwise
could not route to.

## The idea

Give mrvl a proxy option, modeled on `kubectl proxy` / `kubectl port-forward`.
An authorized client tunnels a protocol THROUGH mrvl's existing SSH channel to
a target cluster, instead of requiring direct connectivity to that cluster's
broker.

Concretely, `mrvl proxy --cluster mokuzai --nats` would expose that cluster's
local NATS broker on a loopback port on the caller, so `nats://127.0.0.1:<p>`
reaches mokuzai's bus over the mrvl SSH transport. Director (or any tool) then
talks to the cluster's local bus as if it were local, and the leaf mesh stops
being the only path.

## Two permission tiers

The point the operator drew: not everything should be equally proxyable.

1. **Native-allowed apps/modules.** Specific protocols are first-class over
   mrvl: the director bus (NATS), and a small allowlist of known modules.
   These get a native proxy that understands the protocol (subjects, auth,
   framing) and can enforce policy at the protocol level. Lower bar to grant,
   because mrvl knows what it is carrying.
2. **Generic relays.** A different, higher-bar permission that lets an
   authorized identity relay arbitrary TCP or streams through mrvl for anything
   not natively supported. mrvl carries bytes it does not interpret, so the
   grant is coarser and the audit heavier. This is the escape hatch, priced
   accordingly.

RBAC gates both. mrvl already authenticates the caller; extend its authz so
"may proxy protocol X to cluster Y" and "may open a generic relay to cluster Y"
are distinct granted capabilities per identity and role. `umw8p` (the bus has
no authorization block yet) is the precondition on the bus side.

## System-owner control is first-class, and external to the control plane

The two tiers above are administered inside mrvl's RBAC. That is not enough, and
treating it as enough is why many operators will refuse to run a marvel cluster
with remote connections at all. The system owner must hold control over what can
be proxied as a first-class capability that sits OUTSIDE the mrvl control plane,
so that constraining the proxy never depends on marvel being healthy, correctly
configured, or honest. marvel administering its own kill switch is circular and
does not earn an operator's trust.

Required owner controls:

- **A global off switch, external to marvel.** Disabling all proxying must be
  enforceable without asking marvel to disable itself: a host-level config marvel
  reads and cannot write, an environment gate, or kernel-enforced containment
  (curtain is the natural home, denying the proxy and relay paths at the sandbox
  boundary regardless of marvel's own settings).
- **Policy the owner sets, above marvel's RBAC:**
  - prevent proxy management entirely (no identity inside marvel may grant or open a proxy),
  - forbid generic relays while still allowing native protocols, or the reverse,
  - allow only a whitelist of specific protocols, clusters, and identities, default deny.
- **The owner's policy is a ceiling.** marvel's RBAC can only narrow it, never
  widen it. A grant inside marvel that exceeds the owner's external policy is
  refused, not honored.

Rationale, and it is adoption rather than polish: a proxy into a cluster and
remote connections are exactly the surface a careful operator locks down first.
If the only control lives inside the thing being controlled, the answer is no. An
owner-held, control-plane-external off switch plus a default-deny policy ceiling
is the precondition for those operators to enable any of this at all. SOUL 1
(user sovereignty: the owner controls their tools) and SOUL 3 (custody: the proxy
brokers under a ceiling the owner can revoke from outside, it does not hold
authority the owner cannot reach).

## Why this helps

- It gives director a reliable reach into a cluster's bus even when the direct
  NATS path is down, which is the concrete failure behind the backslide.
- It reuses marvel's existing embedded-SSH transport rather than inventing a
  second network plane.
- It generalizes: any tool that speaks a supported protocol reaches a cluster
  the same way, the kubectl-proxy property.
- It keeps the trust story legible: native protocols get protocol-aware policy,
  generic relays are a separate, rarer grant.

## Open questions

- Latency. NATS over an SSH-tunneled loopback port adds a hop; is it acceptable
  for the bus, or only for control-plane calls and not the hot path?
- RBAC shape. Per-identity, per-cluster, per-protocol. How does it compose with
  the fleet/account model on the hub (`v3ie2`, `tq2no`)?
- Relationship to switchboard. Switchboard is the blind-relay component in the
  vision (SSH end to end, HMAC frames, tenant isolation). Is mrvl-proxy a
  marvel-native alternative for the bus case, or does it delegate to switchboard
  as the carrier? `2tndb` (switchboard access node as a managed remote-access
  node) and `h8dq1` (switchboard<->marvel integration contract) are the seam.
- Custody. The proxy must broker, not hold: it may open a channel an authorized
  caller uses, it must not become a standing credential-bearing chokepoint
  (SOUL 3 / ADR-009). A generic relay especially must not launder authority.
- Does this subsume or complement the director two-hop supervisor relay
  (`7gnvo`, `srfdl`)? The relay is application-level (a supervisor forwards a
  message); the proxy is transport-level (the bus itself is reachable). They may
  stack: proxy when you can open the transport, relay when you can only reach a
  human-or-agent hop.

## Cross-links

[[desk-addressed-tmux-connection]] (addressed connections to a cluster),
[[identity-at-launch-and-managed-nats]] (the managed-NATS direction this rides).
curtain (kernel-enforced containment, the natural home for the owner's external
off switch: deny the proxy and relay paths at the sandbox boundary).
Director reach: aae-orc-7gnvo, aae-orc-srfdl (two-hop relay), aae-orc-av2v1
(mokuzai on the global tier), aae-orc-bxg5f (managed bus mode). Bus authz:
aae-orc-umw8p. Remote access: aae-orc-2tndb, aae-orc-yl63 (mrvl:// remote),
aae-orc-h8dq1 (switchboard<->marvel). Backslide evidence: the director board
entry 2026-09-21 (GLOBAL_TO_mokuzai unconsumed; presence is not consumption).
