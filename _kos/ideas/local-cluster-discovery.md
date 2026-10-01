# Discover local marvel clusters by name (mDNS / DNS-SD, Bonjour style)

Status: idea, pre-hypothesis. Raised by the operator on 2026-10-01: "what if
we had a way to detect other local marvel clusters, service discovery ala
bonjour?" No commitment, no ticket, no frontier node.

## The trigger

On 2026-10-01 a remote cluster's host moved to a new network address. The
client config named that cluster by an IP literal (`server: mrvl://<ip>:6785`),
so every `marvel ... --cluster <name>` failed with a dial timeout that did not
suggest a move. After a hand edit to the new address, host key trust refused,
because trust is keyed by host and port: a moved cluster looks the same as a
different machine. Filed as marvel#434 (cluster identity and key trust) and
aae-orc#451 (who in the fleet records a cluster's address, and how a cluster
dropping off reaches the operator).

## The idea

Clusters on the local network announce themselves over mDNS / DNS-SD, the
way Bonjour services do: a `_marvel._tcp` service record carrying the cluster
name and its host key fingerprint in its TXT data. The client browses for
that service and resolves a cluster by name, so an address change on the LAN
needs no config edit.

Partial prior art in use today: one cluster is already configured by a
`.local` name, which gets name resolution over mDNS but not discovery or
identity. Related pattern in `docs/design/adopted-services-2026-09-16.md`: a
local inference router that discovers peers over mDNS, pairs them with a PIN,
and pins self-signed certificates.

## Open questions

- **Trust.** Discovery is unauthenticated; anyone on the LAN can announce
  `_marvel._tcp`. An announced fingerprint is a hint for matching, never a
  reason to trust. Trust stays pinned per cluster identity (marvel#434): the
  same pinned key at a new address is recognized, and any other key refuses
  as it does today.
- **Scope.** mDNS is link-local. Remote clusters, and clusters behind another
  subnet, still need an address from somewhere else; the bus already sees a
  cluster's leaf peer address (aae-orc#451). Is discovery a LAN convenience
  layered on that, or is the bus-announced address enough on its own?
- **Independence.** It must be optional (SOUL section 2): marvel with no
  mDNS responder, or on a network that blocks multicast, works exactly as it
  does now from its config.
- **Identity.** What is the cluster name in the record: the name the operator
  gave it, a generated id, or both? Two clusters announcing the same name on
  one LAN need a defined outcome.
- **Exposure.** Announcing a fleet on a shared or guest network advertises
  that it exists. Should announcing be off by default and on per cluster?

## Not this idea

Fleet-wide address tracking and cluster-gone alerts are aae-orc#451's
subject. Binding trust to identity is marvel#434's. This idea is only local
discovery, and it depends on #434's identity binding to be safe.
