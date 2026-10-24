# Privilege of position: where a cluster is homed is part of what it can do

- **Status:** idea (pre-hypothesis, no commitment). Exploration only: no
  frontier node, probe or ticket, and it may sit in tension with the nodes
  and ideas it links.
- **Date:** 2026-10-24
- **Concept:** "privilege of position" is the operator's coinage, tried first
  for NIST IoT work. It is attributed to them here on purpose.
- **Seed (operator's words, via director):** "a concept we haven't explored
  yet is something i tried to coin for NIST IoT but it never caught on:
  privilege of position. sometimes a key important feature of a system is
  where it is homed, and that can be true of a marvel cluster, too. The NIST
  team were trying to ignore where devices were homed, they felt that the
  internet-of-things should transcend provider/network location by
  definition, but when we face real world guards, inspection points,
  security devices, cameras, vibration monitoring, seismic monitoring,
  ground surveillance location matters, so why not with an AI cluster?
  having "noc" or secops AI clusters with local footprint in a distributed
  system can allow agents to collaborate from different vantage points
  "privilege of position" allowing for more effective function. sometimes
  working remotely over a network restricts view and troubleshooting and
  visibility, having an intelligent actor "over there" is an advantage, and
  having a wide flung network of distributed intelligent actors might prove
  to be very effective, more so than strictly central monitoring"
- **Subject:** marvel (what a cluster is, where it sits, and how work finds
  the cluster that suits it). Other projects are objects here. Filed in the
  marvel graph per the subject test.
- **Related:** [[multicluster-view]] (one client over several clusters, the
  view half of this), [[local-cluster-discovery]] (finding clusters on one
  LAN by name), [[declared-cluster-config]] (where a cluster's declared
  properties would live), [[router-and-backend-as-first-class-concepts]]
  (routing a request to a backend, a different routing question that shares
  the word), [[service-directory]] (asking where a service is instead of
  carrying its address), and the non-LLM bus publishers and watchdog idea
  (marvel#1042, future path `_kos/ideas/non-llm-bus-publishers-and-watchdog.md`),
  whose publishers are the kind of local actor this idea would place.

## The concept, briefly

A system's position is more than an address. Which network segment a
component sits on, which side of a firewall or a NAT it is, and which
physical site it is bolted to decide what it can observe and what it can
touch. For many systems that is incidental. For some it is the point: a
camera sees one scene, a seismometer feels one patch of ground, and a
guard at a gate can inspect what passes the gate.

## A different design premise, not a mistake

The NIST IoT work, as the operator describes it, started from the premise
that the internet of things should transcend provider and network location
by definition. For a framework meant to cover every device, that is a
coherent choice: a model that does not care where a device is can describe
more devices with fewer rules. This idea does not argue it was wrong. It
notes that the premise and the operator's field experience pull in
different directions, and that an AI cluster is a new kind of component to
ask the question of again.

## The claim

A cluster's position grants it observations and actions that a remote
cluster cannot have:

- **Observation.** A cluster on a site's network segment can read a local
  switch, a local log, or a local sensor directly. A remote cluster sees
  only what has been forwarded, at the fidelity and delay the link allows,
  and only what someone decided to forward.
- **Action.** An agent on the near side of a firewall can reach a device
  that is not routable from outside, restart a local service, or check a
  cable run, with no tunnel to build first.
- **Triangulation.** Agents homed at several vantage points can compare what
  each sees. Two clusters that disagree about whether a link is up are
  giving evidence about where the break is. A central monitor sees one
  point of view and can mistake its own path for the fact.
- **Failure of the center.** A local actor can act while the link home is
  slow or down. Central monitoring sees that silence and cannot see why.

The operator's claim is that a wide set of distributed actors with local
reach may be more effective than central monitoring alone. This idea
records it as a claim to test, not a result.

A live example exists: an operations-monitoring seat homed beside a site
gateway. It sees the gateway's side of the network, which a seat at the
center does not. That example is the reason this idea is filed now rather
than later, and nothing more is said about it here.

## What it could mean for marvel

All three are possibilities, not proposals.

1. **Position as a declared cluster property.** A cluster could state where
   it is homed and what that gives it: the network segment, which side of a
   boundary, the site, and the things it can see or reach from there. That
   is a declaration, not a measurement; a cluster can be wrong about where
   it is. [[declared-cluster-config]] is where such a block would live, and
   [[local-cluster-discovery]] already carries a cluster's name and key
   fingerprint, so position could ride beside them. Whether the claim is
   checked, and by what, is open.
2. **Routing work to the cluster with the position.** A task such as "check
   this site's gateway" could go to the cluster homed there rather than to
   whichever cluster has a free seat. Today work is routed to a team in the
   cluster the operator addresses. Choosing a cluster by position is a layer
   above that, and it overlaps with
   [[router-and-backend-as-first-class-concepts]] only in the word: that
   idea routes inference to a model backend, this one routes tasks to
   places.
3. **Collaboration across vantage points.** Seats in different clusters
   could share observations and compare them, which needs the cross-cluster
   reach that [[multicluster-view]] starts for the operator's view and that
   director addresses for agents. Local publishers such as those in the
   #1042 idea would feed the local cluster, and clusters would exchange
   what is worth sharing.

## Tensions

- **More reach, more exposure.** A seat that can touch local devices holds
  more authority and more risk than one that cannot. SOUL section 3 and
  ADR-009 set the custody boundary: marvel may broker a short-lived
  credential and must not hold a long-lived grant at a third party. A
  vantage point is a place where such grants tend to be needed, so
  position raises that boundary question for every site.
- **Trust and authority per position.** What one cluster may tell another,
  and how far a remote cluster should believe it, depends on who runs the
  site and how the link is secured. A report from "over there" is evidence
  from an actor the receiver cannot inspect. Whether authority is granted
  per cluster, per position or per task is not settled.
- **Observe or act.** A vantage point that only observes is easy to
  justify. One that acts locally is where the value is and where mistakes
  cost most, because nobody is watching it act. The line between them may
  differ per site and per task.
- **The partition case.** A vantage point that loses its link home is the
  moment its position matters most and its supervision is thinnest. What it
  may do alone, what it queues, and how it rejoins and reports are open.
  The center cannot tell a quiet site from a cut one, which is the question
  the watchdog in the #1042 idea asks for a single tool, here for a whole
  cluster.
- **Declaration versus fact.** If routing trusts a declared position, a
  wrong or stale declaration sends work to the wrong place. Some positions
  can be probed from outside and some cannot.
- **It may not need marvel.** Clusters already run where they are put, and
  an operator can address one by name. Position-aware routing is worth
  building only if the operator finds themselves choosing the cluster by
  hand often enough to want help.

## What to do with this

Nothing yet. The idea is here so the concept has a name in the graph and
the neighbouring ideas can cite it. If it holds, the first question is
whether position is only a label an operator reads, or a property marvel
uses to route work; that choice decides how much of the rest is needed.
