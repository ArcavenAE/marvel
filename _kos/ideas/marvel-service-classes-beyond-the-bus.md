# Marvel service classes beyond the bus: bus, task, kv, file, secret, and whether several providers of one class run at once

- **Status:** idea (pre-hypothesis, no commitment). Operator thought relayed
  via director, 2026-11-02. Nothing here is answered; the questions are kept
  open on purpose.
- **Date:** 2026-11-02
- **Asks:** I-144
- **Subject:** marvel (which classes of service it manages or fronts, and how
  providers of a class are chosen). The providers named below are objects.
  Filed in the marvel graph per the subject test.
- **Related:** `_kos/ideas/services-catalog.md` (the catalog of classes and
  the ratified record shape), `_kos/ideas/service-directory.md` (how a
  cluster finds the services it depends on),
  `_kos/ideas/mcp-service-provider-and-credential-custody.md`, the frontier
  node `question-marvel-service-provider-shape`,
  `_kos/findings/finding-marvel-k1vd-cross-cluster-file-sharing-scope.md`
  (marvel#1232, not merged when this was written; a scoped study of the file
  class across clusters), and
  `_kos/findings/finding-marvel-y5ir-credential-broker-service-class.md`
  (a credential broker as a service class).

## The thought

The operator, word for word:

> today marvel has the idea of marvel bus, for agent to agent communication. In theory, this is a marvel service provider model manage service, one instance of which is a2a-over-nats, the only instance, but the idea was users could swap to other systems rabbitmq, kafka, etc of their preference. It occurse to me that modern compute uses several major classes of things, and this sort of represents the IPC/queue area, while others also exist: marvel task could be an interface to bd, jira, linear and other task/work tracking systems, marvel kv could be a key value system perhaps, marvel file could be a distributed file system, maybe marvel secret(s) .... I'm not sure of the shape of this, or if we might mix and have more than one service running at the same time, ie nats and rabbitmq, for example one team might use bd for tasks while another uses linear or jira? It's not clear what marvel file might consist of yet, if it is s3 shaped or CIFS or nfs or rsync or pmxcfs or CephFS or something else

In short: the bus is one class among several. The operator sees the swap
idea (nats today, rabbitmq or kafka later) as a pattern that other classes
could follow.

## What the record already has

The services catalog already maps the bus to the registered class
`message-bus` and sketches `task-graph` and `secret-manager` for tasks and
secrets, with `scheduler`, `notification` and `forge-events` as further
proposals. It does not list a key-value class or a file class. The operator's
shorthand ("marvel bus", "marvel task") and the catalog's class names are two
vocabularies for one idea; reconciling them is left open below.

## Open questions

- **Which classes.** The operator names bus, task, kv, file and secret, and
  leaves room for others. Which of these earn a class, which are one class
  under two names, and what else belongs on the list?
- **One provider or several, at once.** Does a cluster run exactly one
  provider per class, or several side by side (nats and rabbitmq on the same
  cluster, say)? If several, is the choice made per team (one team on bd for
  tasks, another on linear or jira), per workspace, or per cluster? What does
  a seat see when two providers of a class are bound to different teams in
  the same workspace?
- **What a class contract promises.** A class is the interface a consumer
  depends on, never a provider's API. For task, kv and file, what is the
  common surface that bd, jira and linear (or s3 and nfs) could all honor,
  and what is left outside the class?
- **The shape of file.** The operator is unsure whether file is s3 shaped,
  CIFS, nfs, rsync, pmxcfs, CephFS or something else. Each implies a
  different contract: object store, network filesystem, transfer tool, or
  cluster-replicated config store. Left open here. The k1vd finding scopes
  one slice (cross-cluster sharing) and does not settle the class.
- **Secret.** Bounded by the orchestrator's SOUL section 3 and ADR-009:
  marvel may broker a short-lived credential from a vault and hand it on, and
  may not hold a long-lived third-party grant. What a `secret` class would
  expose inside that boundary is open; the y5ir finding is the nearest worked
  case.
- **Names.** Does marvel adopt the operator's short names as aliases for the
  catalog's class names, or keep one vocabulary?

## What this idea does not do

It proposes no class, no contract and no manifest field. It edits none of the
linked files or nodes. If any of these questions is taken up, it becomes a
probe with its own brief, and this file gets a note pointing to it.
