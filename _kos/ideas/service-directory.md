# A service directory: ask where the hub and bd are, instead of carrying addresses

- **Status:** idea (pre-hypothesis, no commitment). Exploration only: no
  frontier node, probe or ticket.
- **Date:** 2026-10-04
- **Seed (operator's words, via director):** "I love the idea of a marvel
  feature for a list of hub addresses, and maybe also some kind of service
  discovery or broker/directory option". Earlier the same day, about the
  hub host's address: "we might flip flop between [two subnets], ideally
  all the nodes tolerate it".
- **Subject:** marvel (how a cluster finds the services it depends on).
  Filed in the marvel graph per the subject test; the hub, bd and director
  are objects here.
- **Related:** the marvel hub-URL-list issue (number to follow), the
  near-term half of this; `_kos/probes/probe-temporary-host-disconnect.md`
  (the cut that showed the problem; its brief lands in its own PR); [[get-views-connection-and-service-context]]
  (showing which hub and bd a view is connected to);
  [[local-cluster-discovery]] (mDNS for finding clusters on one LAN);
  marvel#434 (cluster identity and key trust).

## Why look at it

Every cluster carries the hub's address in its config (`hub.url`, a single
string, `internal/config/config.go:444-449`), and every seat carries the bd
server's address in its client env. When the host that runs both changes
address, each of those copies is wrong at once, and each is fixed by hand.
That happened on 2026-10-04: during recovery from a planned disconnect, the
hub host came back on a different address. A remote leaf dialed it by a
`.local` name, which mDNS does not carry across subnets, and pinned host
keys were tied to addresses, so the old trust did not match the new place.

A list of hub addresses (the issue above) lets a leaf try the next one.
A directory goes further: a cluster asks "where is the hub, and where is
bd" and gets the current answer, so no node holds an address that can go
stale.

## Shape, loosely

A small service, or a record somewhere every node can already read, that
answers by name: the hub's URLs and CA, the bd server's address, and maybe
the clusters themselves. marvel would read it at start and on a failure to
connect, and fall back to its configured addresses when it gets no answer.

## Open questions (recorded, not settled)

1. **Who runs the directory.** marvel itself (one daemon designated, or
   every daemon a replica), director, a separate small service, or an
   existing system such as DNS SRV records. Each choice gives a different
   component a new job and a new way to fail.
2. **How it is found without an address.** A directory reached by an
   address has moved the problem, not removed it. Candidates: a short list
   of directory addresses (the same list problem, smaller and slower to
   change), DNS names under a domain the fleet controls, multicast on one
   LAN only ([[local-cluster-discovery]]), or asking any peer already
   reachable. Which of these works across subnets, which is the point.
3. **Independence (ADR-005).** marvel must run with no directory: a cluster
   with only configured addresses works exactly as it does now, and a
   directory that is down degrades to that. Is the directory an optional
   enhancement every component can ignore, or does it become a dependency
   by accretion?
4. **Custody (SOUL section 3).** The directory hands out where things are.
   If it also hands out how to reach them (a CA file, a token, a password),
   it holds or brokers credentials. The audience test applies to the most
   durable thing it holds: addresses and public CA certificates look like
   issuance-free data; a bus password or a bd credential would be custody.
   Where is the line, and does the directory ever cross it?
5. **Trust in the answer.** An unauthenticated directory can send a cluster
   to the wrong hub. Trust has to stay pinned to identity (marvel#434), so
   a new address for a known key is accepted and an unknown key is refused,
   whatever the directory says.
6. **Staleness and churn.** How quickly does a changed address reach the
   directory, and who writes it: the moved service announcing itself, or
   an operator?
7. **Scope.** Just the hub and bd, or every service in the cluster
   services list (`docs/design/services-list.md`)?

## Not in this idea

The hub address list itself, which is the issue above and is the near-term
fix. Cluster identity and key trust (marvel#434). Finding clusters on one
LAN ([[local-cluster-discovery]]).
