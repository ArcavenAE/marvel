# The bus leaf: a status surface, and whether "bus" is the right word

- **Status:** idea (pre-hypothesis, no commitment). Two parts, one subject: the
  marvel leaf that connects a local cluster to a remote NATS tier.
- **Date:** 2026-09-18
- **Origin:** commission via director, carrying the operator's intent. Part B
  draws on a web survey of the terms in current use (citations inline).
- **Subject:** marvel (the leaf feature and how it is named and surfaced). Filed
  in the marvel graph per the subject test.
- **Related:** [[identity-at-launch-and-managed-nats]] (the managed bus and its
  leaf; it already notes leaf-link state on the events ring),
  [[get-views-connection-and-service-context]] (where a leaf status might surface
  to an operator). The leaf connect and disconnect lifecycle is the buildable
  work tracked as bd aae-orc-ct0l4 (director side); this idea sits beside it.

## Part A: what should leaf status expose, and why

The leaf feature today has connect and disconnect. The operator wants to explore
adding STATUS: a surface for the leaf link's state. The obvious states are
connected, disconnected, and reconnecting. The open question is what ELSE a
status surface should expose, and the discipline is that each field earns its
place by the operator decision it enables, not by being available.

Candidate facts a status might carry, each posed as a question, none prescribed:

- Is a single link state (connected, disconnected, reconnecting) enough, or does
  the operator also need the transition history (last connected at, how long
  since, how many retries in the current gap)?
- Does the status name the far side (which remote tier the leaf is dialing, its
  URL or profile), or is that context the operator already holds?
- Is the credential and enrollment state part of status (the leaf presents an
  enrolled credential; a link that is down because the credential is missing or
  rejected is a different operator problem than a link down because the tier is
  unreachable)?
- Does status distinguish "the link is up" from "the link is up and carrying the
  subject interest it should," which are not the same fact?
- Where does this belong? [[identity-at-launch-and-managed-nats]] already notes
  leaf-link state on the events ring, marvel-builder's recommendation that a
  cluster surfaces its own link state on that ring and never as a health gate. So
  the open question is whether the events ring is the whole answer, or whether the
  operator also wants a first-class status view or a decoration on the `get`
  views ([[get-views-connection-and-service-context]]), and if so which fields.

The standing constraint from that related idea holds: a link-state surface stays
diagnostic and never becomes a health gate (SOUL section 8, ADR-007). This part
captures the question of WHAT status should expose and WHY; it designs no output
and proposes no field names.

## Part B: is "bus" the right word for this

A separate reflection the operator raised, on the same feature. The current term
is "marvel bus." Two things make it worth questioning before it hardens.

First, a collision inside marvel. NATS is an external, optional module. There is
also a PLANNED INTERNAL marvel event bus, a program-structure concern with no
operator CLI surface. "Bus" names both, and the survey below confirms the word
is overloaded exactly this way: an in-process event bus inside one program versus
a network-level message bus running as a separate broker process are both called
a bus, and nothing in the word signals which is meant. So "marvel bus" risks
naming the external NATS thing with a word the internal thing also wants.

Second, a transport-versus-pattern question. What marvel runs over NATS is
agent-to-agent messaging (the envelope direction is A2A). NATS is the transport,
a message bus; A2A is the application-level pattern that rides it. Calling the
whole thing "the bus" names the transport and drops the pattern, and the survey
shows these two get folded together in casual use.

### The terms in current use (survey, as data, not a recommendation)

- **A2A / Agent2Agent**: a real application-layer specification, now under the
  Linux Foundation. Agent Cards, Tasks; transport bindings over HTTP, Server-Sent
  Events, JSON-RPC 2.0. It does NOT specify a broker, and NATS is not one of its
  named transports, so "A2A over NATS" is an app-layer protocol carried on a
  transport the protocol itself does not name. (https://a2a-protocol.org/latest/specification/)
- **message bus vs event bus vs service bus**: the classic distinction is that a
  message carries an expectation of how the consumer handles it (a contract),
  while an event is a lightweight notification of a state change with no
  expectation on the consumer; a "service bus" is a managed broker product
  (for example Azure Service Bus). (https://learn.microsoft.com/en-us/archive/blogs/nickmalik/draw-the-distinction-between-a-message-bus-and-a-services-bus)
- **agent mesh / service mesh for agents**: vendor and open-source usage (Solace,
  Solo.io, kagent) for an infrastructure layer doing agent discovery, routing,
  policy, and observability, described as analogous to a service mesh but
  operating on tasks and intent rather than packets. (https://www.solo.io/blog/agent-mesh-for-enterprise-agents)
- **agent gateway**: an application-layer proxy for routing, security, and
  observability of MCP, A2A, LLM, and HTTP traffic (the agentgateway project,
  under a Linux Foundation agentic effort). Note this collides with NATS's own
  "gateway," which is a topology primitive wiring clusters into a supercluster.
  (https://www.ibm.com/think/topics/agent-gateway)
- **NATS's own words**: NATS calls itself "a connective technology," responsible
  for addressing, discovery, and exchanging messages. Its topology vocabulary is
  leaf node (an outbound bridge of subject interest, for edge and local traffic),
  gateway (wires clusters inside a supercluster), and supercluster. (https://docs.nats.io/nats-concepts/overview, https://docs.nats.io/learn/topologies/leaf-nodes)
- **message fabric / messaging fabric / event fabric**: used loosely, no single
  settled technical meaning across sources; reads as descriptive language more
  than a fixed referent.
- **older and adjacent**: FIPA-ACL and KQML (older academic agent communication
  languages), IBM ACP, ANP, and a vendor cluster (Cisco Outshift's "Internet of
  Agents," AGNTCY, an "Agentic Service Bus," and SLIM as a named protocol). (https://arxiv.org/html/2505.02279v1)

### The confusion risks, laid out

- "Bus" is overloaded across scope: an in-process event bus versus a network
  message or service bus. This is the exact collision with marvel's planned
  internal event bus.
- A transport can be confused with the pattern riding it: "we use NATS" answers a
  different question than "we do agent-to-agent messaging" or "we implement A2A,"
  and the three get conflated.
- "Mesh" carries service-mesh meaning (packets, connections, millisecond
  timescales) into agent-mesh (tasks, intent, second-to-minute timescales); the
  word is the same, the operating layer is not.
- "Gateway" names two distinct objects that could both appear here: a NATS
  topology primitive and an application-layer agent gateway.
- "Fabric" has no settled technical meaning across the sources; it reads as
  descriptive rather than precise.

This part captures the open naming and framing question and the confusion risk.
It proposes no term and recommends no rename; the choice is the operator's. What
it records is that the current word is doing two jobs (naming the external NATS
transport and colliding with the internal event bus) and that the thing being
named is a transport plus an application-level messaging pattern, not one
undifferentiated "bus."
