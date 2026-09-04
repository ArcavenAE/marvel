# Agent identity landscape study — the golden-path thin principal

**Ticket:** `aae-orc-odia` (study half). Blocks `aae-orc-bs3x` (M1 principal
model + per-method RPC authorization) and `aae-orc-ukz` (role-based
capability checks).
**Lane:** roadmap Track M item M1, identity lane — forked feature, merges
back as proven; the main line does not block on it
(`docs/marvel-remap-2026-08.md` Lane 4).
**Repo pin:** marvel `main` @ `1466313`, tree clean except one untracked
test file. **Study only — no code changed, no ticket filed, work is tasked
out in §11 and not done.**
**Spine:** `aae-orc::question-marvel-identity-authority-topology`
sub-questions A–F.
**Date:** 2026-09-04.

---

## 1. Headline

**Marvel already computes the caller's identity on its authenticating path
and throws it away three lines later, and that discard — not the absence of
a certificate authority, a policy language, or a token format — is the whole
gap the golden path has to close.**

`SSHServer.authorizeKey` (sshserver.go:147) authenticates a client public
key against `~/.marvel/authorized_keys` and returns
`ssh.Permissions{Extensions: {"user", "pubkey-fp"}}`. Fourteen lines later
`handleConnection` uses those extensions for one log line and then calls
`s.daemon.handleRWC(ch)` with the channel alone (sshserver.go:142). The
identity does not cross into `dispatch`. There is no `principal` or
`capability` symbol anywhere in `internal/daemon` or `internal/api`
(grep at the pin: only `authorizeKey` itself matches).

The recommendation that follows is deliberately small: **three confidence
grades, one string name, three action buckets over the existing 19 dispatch
methods, and no new cryptography.** Its only day-one behavior change is that
marvel-spawned agents stop being able to `inject` into each other's panes and
`stop` the daemon. Everything a human does today keeps working unchanged.
That is a shippable, demonstrable security result that needs no CA, no bus,
no policy engine, and no revision of SOUL §3.

---

## 2. What this is, and what it is not

It is a survey and a recommendation. It answers sub-questions A–F, states
which it cannot answer and what evidence is missing, surveys the prior art
named in the ticket against what marvel actually needs, and proposes a flat
task list for the design phase.

It is **not** a design document, an ADR, or an implementation. It files no
tickets and touches no code. Where it recommends a shape it says so as a
recommendation, and §12 states the limits that would make it wrong.

**A trap in the ticket, named here because working from the ticket's text
alone will spring it.** `aae-orc-odia`'s reading list was assembled
2026-08-01. The marvel service-provider design set
(`docs/design/service-provider/`, 2026-08-15) postdates it by two weeks and
is **not on that list** — yet its §"The credential boundary" already rules
on sub-question A's tension (marvel brokers, does not durably custody) and
introduces the `Binding` primitive that identity exchange hangs off.

Anyone who reads odia and starts designing will therefore re-derive a ruling
that already exists, and may re-derive it differently. Read
`docs/design/service-provider/02-architecture.md` §"The credential boundary"
and §"One word was hiding two: Endpoint versus Binding" **before** odia's own
list. This study did, and treats that document as more current than odia's
framing wherever the two touch.

---

## 3. Premises verified at the pin

Every load-bearing claim in §4 was checked against `1466313` rather than
carried from a ticket or a node.

| Premise | Method | Result |
|---|---|---|
| SSH path authenticates the client | read `sshserver.go:50,147` | CONFIRMED — `PublicKeyCallback` is a membership test against `authorized_keys` |
| Identity does not survive `handleRWC` | read `sshserver.go:102-142`, `daemon.go:651-664` | CONFIRMED — `Permissions` used for one log line, `handleRWC(rwc io.ReadWriteCloser)` takes no caller |
| Unix socket path has no authentication at all | read `daemon.go:646-649` | CONFIRMED — `handleConn` → `handleRWC`, no check |
| The socket is protected by its directory, not its mode | read `paths.go:22-29` | CONFIRMED, and the code says so — `net.Listen` creates it 0755 under a 0700 `run/` |
| No principal or capability machinery exists | grep `principal\|Principal\|capabilit\|Capabilit\|authorize\|Authorize` over `internal/daemon`, `internal/api`, non-test | CONFIRMED — 20 hits, all `authorizeKey`/`authorized_keys` in `sshserver.go` |
| The heartbeat token shipped | read `internal/api/heartbeat.go:60-78,92-98`, `store.go:565` | CONFIRMED — 256-bit token, digest on the record, constant-time compare inside the store write |
| `dispatch` has 19 methods | `awk` the function body at the pin, count `case` arms | CONFIRMED — 19 (list in §4.4) |
| Client revocation exists | read `cmd/marvel/main.go:1636-1650` | CONFIRMED — `keys revoke <fp>` is a shipped subcommand |

---

## 4. The measured starting point

### 4.1 Two doors, one gate, and only one of them has a lock

Marvel's RPC has two entrances into the same `handleRWC`:

- **`mrvl://` over embedded SSH** — authenticated (client pubkey against
  `authorized_keys`), host-key TOFU, rejections logged with the offending
  fingerprint. finding-029 verified it fail-closed three independent ways,
  including that an empty `authorized_keys` rejects everything and that
  host trust does not confer client authorization.
- **the local unix socket** — unauthenticated. Its boundary is the
  filesystem: a 0700 `run/` directory, same-uid. Every marvel-spawned agent
  is handed `MARVEL_SOCKET` by design, so every agent on the host is already
  inside this boundary.

Both doors then reach the same 19-method dispatch with no per-method check.
The consequence is the one finding-022 named from the other end: the
realistic caller here is not a stranger on the host, it is a sibling agent
that was told to do something.

### 4.2 The one thing already bound, and it is the model to generalize

`aae-orc-mr5c` shipped (marvel PR #168). It is the platform's first real
principal-shaped credential and its shape is right:

- `session.Manager.Create` mints a 256-bit token **before the record
  exists**;
- the **digest** goes on the persisted record (so adopt-on-restart
  survives), the **plaintext** goes only into the pane environment as
  `MARVEL_HEARTBEAT_TOKEN`;
- the check moved **into `Store.UpdateSessionHeartbeat`'s signature**, under
  the same lock as the write, so it is not caller discipline;
- refusals emit `heartbeat.refused` on the event ring;
- the environment carries it rather than argv, because argv is world-readable
  from the process table;
- one deliberate, bounded fail-open: a record with no hash is admitted and
  emits `heartbeat.unbound`, because refusing would have made adopt-on-restart
  destructive on upgrade. It drains as those sessions end.

Every one of those six decisions is reusable verbatim. **The golden path
should generalize this credential, not invent a second one beside it.**

finding-022's broader point is the constraint the generalization must
respect: `api.Session` is serialized by the same `encoding/json` that
persists it to bbolt and that answers every `get sessions`, so any
credential added to a store resource is published by default. The digest-on-
record / plaintext-in-environment split is what makes the token safe there,
and it is a property of the *layout*, not of the token.

### 4.3 What the keys layer is, and what it is not

295 LOC (`internal/keys/keys.go`). It generates ed25519 client keypairs and
the daemon host key, writes private material 0600 and public 0644, validates
key names, and loads/appends/removes `authorized_keys` entries. The CLI
surface is `keys generate|show|list|doctor|authorize|authorized|revoke|
host-fingerprint|trust`.

It is a **key manager**, not a mint: no certificates, no CA, no principal
names, no expiry, no attenuation.

**Correction to the topology node.** The node states "no expiry, no
revocation." Revocation of a *client* exists — `keys revoke <fp>` removes
the line from the allowlist, and because authorization is a membership test
evaluated per handshake, removal takes effect on the next connection. What
is absent is revocation of an *issued* credential a holder already carries
(no CRL, no short-lived certificate, no attenuated token to narrow). The
distinction matters for §9: the golden path deliberately stays on the side
of the line where allowlist removal is sufficient.

### 4.4 Two recorded counts, corrected

The load-bearing claim in both sources — *one check gates every method, and
`inject` shares its gate with `get`* — is re-verified at the pin and stands
unchanged. The numbers do not.

- `aae-orc-bs3x` (2026-07-31) says **"the 13-method dispatch."**
- finding-029 / `question-mrvl-exposure-and-trust-bootstrap` (2026-08-25)
  says **"one check gates 26 RPC methods"** and lists them.
- At `1466313`, `Daemon.dispatch` has **19** `case` arms: `apply`, `get`,
  `describe`, `delete`, `scale`, `converge`, `reap`, `heartbeat`, `run`,
  `shift`, `reset-health`, `inject`, `capture`, `stop`, `reexec`, `logs`,
  `events`, `orphans`, `plan`.

The 26 is a CLI surface count, not an RPC method count: ten of its entries
(`budgets`, `endpoint`, `endpoints`, `policies`, `session`, `sessions`,
`team`, `teams`, `workspace`, `workspaces`) are resource *kinds* passed to
`get`/`describe`, not distinct methods; and it omits `converge`,
`reset-health`, and `plan`, which are. 26 − 10 + 3 = 19, which reconciles
the two exactly. The 13 is simply stale — the dispatch grew.

This matters for §9 only because the authorization table is a table of
methods, and it should be built from `dispatch` at implementation time
rather than from either recorded list.

---

## 5. The sub-questions

### A. Issuer vs delegate — ANSWERED, with a sharper test than "certificates vs secrets"

The topology node's candidate resolution is "identity issuance
(certificates, principals) is distinct from credential custody (secrets);
marvel mints the former, never holds the latter." That resolution is right
in direction and **wrong in its discriminator**. Marvel already mints a
*secret* — a 256-bit heartbeat token — and did not thereby become a
credential store. If the line were "secrets," mr5c crossed it in August and
nobody thought so, correctly.

The discriminator that actually holds:

> **Marvel may hold an artifact whose only meaning is inside marvel's own
> trust domain. It must not hold an artifact that is bearer authority at a
> third party.**

The heartbeat token means nothing to anyone but this daemon: it is issuance.
An OAuth refresh token for a model backend is authority at Anthropic: it is
custody. The test is the artifact's *audience*, not its format, and an
adapter author can apply it without a lawyer.

**Where `aae-orc-dqhf` moves this, and why the study flags it.** dqhf
proposes that marvel broker credentials and hold session state on the
agent's behalf — explicitly including the OAuth session for a model backend
— with durable custody delegated to a vault. The service-provider
architecture (2026-08-15, §"The credential boundary") already writes that
position down. Under the audience test above, holding a backend OAuth
session **is** custody, so dqhf is not a clarification of SOUL §3; it is a
substantive move of the line, and it should be ruled on as one.

If dqhf is ruled in, the boundary as dqhf words it becomes *durable vs
session*, which is a time predicate and much harder to test than an audience
predicate (how long is a session?). This study's recommendation, offered
without prejudice to the ruling:

> **Marvel may hold an artifact it can itself revoke or re-mint without a
> human at a third party's console.**

That admits a vault-minted short-lived scoped credential (marvel asks again;
the vault revokes) and excludes a long-lived OAuth refresh token (re-minting
needs the user's browser). It preserves dqhf's intent, stays testable, and
keeps SOUL §3's single-user/multi-user routing boundary untouched, which
dqhf explicitly wants kept.

**What changes in this study if dqhf is ruled the other way** — i.e. if the
current SOUL §3 wording stands strictly: nothing in §9. The golden-path thin
principal holds no third-party material of any kind. What changes is §10 step
5 and beyond: token exchange at the `Binding` boundary becomes out of scope
for marvel entirely and has to live in a separate credential service, and the
service-provider architecture's credential-boundary section needs revising
rather than ratifying. The thin principal is deliberately designed to be
correct under either ruling — that is one of its selection criteria, not an
accident.

### B. Trust domain — ANSWERED, conditionally

**The issuing boundary is the daemon.** Not the workspace, not the
deployment.

The evidence is that every other boundary in the code already agrees:
`LoadOrGenerateHostKey` makes the host key per-`~/.marvel`; `authorized_keys`
is per-`~/.marvel`; the store is per-daemon; and `Daemon.stamp` puts
`DaemonHome` on **every** response, including the decode-error reply,
precisely so an answer can be attributed to the daemon that gave it. A trust
domain drawn anywhere else would need bridging machinery on day one to reach
the key material and the state.

Workspace is too small: workspaces are manifest content, they come and go,
and one reconciler serves all of them. Deployment is too large: there is no
deployment object, and multi-host is M5, unbuilt.

Name form: `marvel://<trust-domain>/...`, with the trust domain being the
daemon's stable name. (§9.1 gives the full form.)

**Conditional (finding-045 discipline, and the topology node asks for this
explicitly).** This answer is conditional on single-host operation and on one
daemon per `~/.marvel`. It is the answer most likely to need revisiting when
M5 lands, because the moment two daemons on one host serve one operator, the
question "is the trust domain the daemon or the operator?" becomes live and
this study has no evidence for it.

### C. Federation topology — PARTLY ANSWERED; the peer-marvel half cannot be answered yet

**Answerable — the direction, for the three named counterparties:**

- **Peer marvels: bundle exchange, not cross-signing.** Cross-signing makes
  every peer's issuance transitively authoritative in your domain, and
  un-cross-signing requires re-issuing rather than deleting. SPIFFE's trust
  bundle model — each domain publishes its verification material, each domain
  chooses which others to load — has the revocation story that hierarchy and
  web-of-trust both lack at this scale.
- **callbook/beads: identity consumer, not co-authority.** callbook's
  enrollment doc already terminates enrollment in a per-identity SQL account
  whose password lives in an external credential store, and the
  service-provider architecture already files callbook under provisioning
  ("stands up the Dolt service shape and enrolls humans and agents under
  durable names. Feeds the trust plane"). It feeds the trust plane; it does
  not co-own it. Making it a co-authority would require it to validate
  marvel-issued material, which nothing in its design wants.
- **NATS: a projection surface, not an authority.** NATS decentralized auth
  is itself a full issuer hierarchy (Operator → Account → User NKEYs, with
  subject permissions carried inside the user JWT). Treating it as the
  authority means running two issuers and reconciling two revocation
  stories. The right shape is that marvel's principal is the source of truth
  and a short-lived NATS user JWT is *minted from it*.

  **One conditional that must not go invisible.** Minting NATS users means
  holding the account signing NKEY. That is on the *issuance* side of §A's
  audience test **only because** the 2026-08-01 ruling makes NATS an external
  server that marvel supervises as a declared workload — the cluster is
  inside marvel's own trust domain. If the bus ever moves to a shared or
  externally-operated NATS cluster, the same key becomes bearer authority at
  a third party and must move to the vault. That is a decision that will be
  made for unrelated reasons (ops convenience) and will silently move the
  credential boundary if nobody is watching for it.

**Not answerable — whether peer federation is hierarchical or mutual.**
Missing evidence: a second deployment. M5 is unbuilt, there has never been a
two-deployment scenario, and the answer turns on an operating question
nobody has had to decide (are deployments peers of equals, or is one a hub?).
Recording a guess here would be the "deliberating inside today's assumptions"
failure the topology node's own substrate caveat warns about. The
recommendation is to answer it when the second deployment exists and to
design the principal so it does not care (§9.1's `assertor` field is the
provision for it).

### D. What the keys layer must grow — ANSWERED, and the answer is "for the golden path, roughly nothing"

| Need | What it costs |
|---|---|
| A principal *name* per caller | A string built from data marvel already has. No crypto. |
| A per-session credential | **Already shipped** (mr5c). Generalize the env var name; do not re-mint. |
| The mrvl:// caller's identity at `dispatch` | Plumbing. `pubkey-fp` is already computed at `authorizeKey` and discarded at `handleRWC`. |
| Expiry | The session's own lifetime. Sessions end; the credential dies with the record. |
| Revocation | `keys revoke` for clients (shipped); session end for agents. No CRL needed because nothing is issued that outlives its holder. |
| Certificates / a CA | **Not needed for the golden path.** Needed for peer federation (C), which is deferred. |
| Attenuation | Not needed until something delegates. Nothing does. |

**The finding worth keeping from D:** the gap is not cryptographic. `internal/keys` is adequate for the recommended step and stays untouched by it. What has to change is `internal/daemon` — one plumbing change and one table. Any design that starts by growing the keys layer has misread where the hole is.

### E. Enrollment — ANSWERED

**Marvel should not adopt the verb "enroll."** callbook owns it publicly
(`docs/enrollment.md`, `kit/enroll.sh`) and the service-provider architecture
already places callbook's enrollment upstream of marvel's trust plane. Two
systems using one verb for adjacent-but-different acts is the exact naming
collision B14 was written to stop.

Marvel already has the right verbs and uses them:

- **`authorize`** — admitting a client key. Shipped (`keys authorize`).
- **`mint`** / **`issue`** — creating a principal for a session marvel
  spawns. Shipped in substance (mr5c mints the token); unnamed.

Recommendation: marvel *authorizes* clients and *mints* session principals;
it never enrolls. If cross-driving is later wanted, marvel enrollment drives
callbook enrollment (callbook-operational-layers layer 1: one implementation,
three drivers), never the reverse.

There is a naming asymmetry worth stating, because it is the shape of the
gap: marvel today has an admission act for humans (`keys authorize`) and
**no admission act at all for agents**. The thin principal is what gives
agents the second one.

### F. Consumers in build order — ANSWERED, and re-ordered because one of them shipped

The node's order was: M1 supervisor rights → M2 bus envelope
`sender.principal` → NATS subject ACLs → director attach → service directory.
`aae-orc-mr5c` has since shipped, which changes what is first.

| # | Consumer | State | Note |
|---|---|---|---|
| 0 | Per-session credential | **SHIPPED** (mr5c / PR #168) | The lane's first consumer already exists in production. Not re-specified here. |
| 1 | Caller identity through `handleRWC` | recommended first | Small, unblocks 2 and 3, and is the §1 headline gap. |
| 2 | Per-method authorization (`ukz`) | recommended second | Three action buckets, not a role matrix. |
| 3 | Envelope `sender.principal` populated | with the bus slice | Field only. No enforcement (see §7). |
| 4 | NATS user JWT minted from the principal | when the bus lands | Projection, per C. |
| 5 | director attach; service directory | later | Needs the AgentCard decision (§6). |

---

## 6. Prior art — what each actually provides, against what marvel needs

**Evidence class for this whole section.** Every claim about a third-party
system below comes from that project's own specification or vendor
documentation, read 2026-09-04 via search. **Nothing here was executed,
built, or benchmarked by us.** Per `.claude/rules/upstream-claim-gate.md`,
none of it may be published to a third party without running the gate first;
inside this study it is survey material, and where it is extrapolation rather
than documentation it is labelled as such.

### Summary verdicts

| System | What it provides | What marvel needs from it | Verdict |
|---|---|---|---|
| **Cedar** | Authorization policy language + engine; Apache-2.0; CNCF Sandbox; verification-guided development (executable Lean model, differential testing against the Rust implementation) | A decision over 19 methods and three grades | **Steal the tuple, defer the engine** |
| **AgentCore Identity** | Inbound auth (JWT/OAuth2/SigV4), outbound auth, OBO token exchange, Token Vault keyed by (workload identity, user id) | The exchange shape at the `Binding` boundary | **Steal the exchange, refuse the vault** |
| **SPIFFE / SPIRE** | Trust domains, SPIFFE ID as URI, SVIDs as X.509 (SAN URI) or JWT, server-side workload attestation, trust bundles for federation | Naming and the federation model | **Adopt the naming, do not run SPIRE** |
| **Biscuit** | Offline attenuation via chained signed blocks, Datalog checks, third-party blocks for cross-domain authorization with no out-of-band sync | Attenuation, once delegation exists | **Right shape for step 5+, wrong for step 1** |
| **A2A AgentCard** | Well-known discovery document; `securitySchemes`/`security` on the OpenAPI 3.2 model; `AgentCardSignature` (JWS over a JCS-canonicalized card) | An outward description of an agent | **Adopt as outward projection, never as the internal principal** |
| **NATS NKEY/JWT** | Operator→Account→User chain; subject permissions inside the user JWT; add users without touching server config | A projection surface for subject ACLs | **Project into it; it is not the authority** |
| **AIP** (arXiv 2603.24775) | Invocation-Bound Capability Tokens: identity + attenuated authz + provenance in one append-only chain; compact = signed JWT, chained = Biscuit + Datalog; MCP/A2A/HTTP bindings | A published shape for what step 1 grows into | **Read properly in the design phase; unverified** |

### Cedar

Cedar is the answer to a question marvel does not yet have. Its value is
externalized, operator-authored, analyzable policy over a rich entity model;
marvel's need is nineteen methods sorted into three buckets. Adopting it now
would be a policy engine with a policy file that says "supervisors may
mutate."

Two things to take anyway, both free:

1. **The decision tuple.** Write the capability check's signature as
   `(principal, action, resource, context) → allow/deny`. It costs nothing
   today and makes Cedar a drop-in later instead of a rewrite.
2. **The verification posture as a reason to not hand-roll a language.**
   Cedar's own development method is the argument against inventing one:
   if a formally-modelled, differentially-tested engine exists under
   Apache-2.0, a bespoke authorization DSL in marvel would have to justify
   itself against it.

> **CORRECTED 2026-09-04 by task 7 — the paragraph below is WRONG, and it was
> flagged as inference when written. Kept in place rather than deleted, because
> the flag worked and that is worth showing.** `cedar-policy/cedar-go` is a
> NATIVE PURE-GO implementation maintained in the same org as the Rust
> reference (Apache-2.0, CNCF Sandbox, v1.8.0 2026-06-01). Verified by
> building, not by reading: a module importing it compiles under
> `CGO_ENABLED=0`, runs, cross-compiles clean to `linux/amd64` and
> `linux/arm64`, and `go list -deps` reports **zero** cgo-using packages in the
> closure. Adopting Cedar is `go get`, not an architecture decision, and the
> zero-CGo posture does not collide with it. A trap for future readers:
> `cedar-go` DOES contain Rust under `test/cedar-*-tool/` with dependabot cargo
> PRs, and it has an internal package literally named `rust` — all test-fixture
> and parser machinery, not runtime, which the cross-compile proves.
>
> The study's OTHER reason to defer Cedar survives and is the real one: marvel
> has 19 methods in 3 buckets, which is a table, not a policy engine. Defer on
> **need**, not on cost. Full detail: `biscuit-cedar-go-viability.md`.

**A concrete, checkable reason to defer** rather than a preference: Cedar's
implementation is Rust. A Go daemon adopting it means cgo or an out-of-process
sidecar, and marvel holds a zero-CGo cross-compile posture. That collides
directly, and it lines up with the 2026-08-01 language ruling (Go stays the
main line; Rust enters as satellite processes where its ecosystem wins). If
Cedar is ever adopted it should be as a satellite, and that is a bigger
decision than authorization needs right now. *(Extrapolation flagged: the
cgo/sidecar characterization is inference from Cedar being a Rust SDK, not a
statement read in Cedar's documentation about Go bindings. §11 task 7 checks
it.)*

### AgentCore Identity — the "stage door"

The single most useful thing in AgentCore for this lane is a vocabulary
marvel lacks: **inbound** auth (who may invoke an agent) versus **outbound**
auth (an agent reaching a downstream service on a user's behalf). Marvel's
recorded thinking — supervisor rights, capability tokens, envelope principals
— is entirely about authority *among* agents. That is the agentcore-crosswalk
"stage door" miss, restated from inside marvel: `question-credential-adjacency`
covers what a spawned harness may *see*, `question-permission-model` covers
what marvel *grants*, and nothing covers who may *call in*.

The mechanism worth stealing is **on-behalf-of token exchange**: an inbound
caller's token is exchanged for a scoped downstream token that carries *both*
the agent's identity and the original caller's, so a downstream service can
authorize at every hop without re-consenting. That is precisely what the
service-provider architecture's `Binding` primitive ("with this identity
exchange") reaches for, and it is a better-specified version of it.

The mechanism to refuse is the **Token Vault**: it holds OAuth tokens and API
keys keyed by workload identity and user id. Under §A's audience test that is
custody, and it is the thing SOUL §3 exists to keep out of marvel. The clean
sentence: **steal the exchange, refuse the vault.**

### SPIFFE / SPIRE

Adopt the naming (`marvel://<trust-domain>/<path>` is SPIFFE-shaped, so later
interop is mechanical) and the federation model (trust bundles, §C). Do not
run SPIRE.

**The reason is not cost, it is that marvel is a strictly better attester
than SPIRE for its own agents.** SPIRE's central contribution is workload
attestation: the workload does not announce itself; the agent infers its
identity from OS-observable attributes because nothing trustworthy vouches
for it. Marvel does not have that problem for a spawned session — **it is the
parent process.** It chose the workspace, the team, the role, the generation
and the index; it constructed the environment; it minted the token before the
record existed. Inference is strictly worse than knowledge, and adding a
second daemon to infer what marvel already knows is a net loss.

**The limit, and it is the interesting half.** This is true only for sessions
marvel spawned. Marvel has an `orphans` method and adopts sessions on restart,
and adoption is exactly the case where identity is *inferred from what is
running* rather than known from having started it. mr5c's one deliberate
fail-open (`heartbeat.unbound`) is that same case surfacing already. If a real
attestor ever earns its keep in marvel, it earns it on the adoption path and
nowhere else. That is a narrow enough target to be worth remembering rather
than a reason to run SPIRE.

### Biscuit

Biscuit's property is the one marvel will actually want: **offline
attenuation** — a holder can add blocks that only ever *restrict*, chained
with single-use keypairs so the restriction cannot be undone, plus
third-party blocks that let policies span security domains with no ahead-of-
time consolidation. That is a supervisor narrowing a token for a worker
without a round trip to the daemon, and it is a peer marvel accepting a
delegation without a bridge — both real future needs (§C, §F).

It is wrong for step 1 because **nothing delegates today.** Adopting a
token format whose defining feature is unused buys the format's cost with
none of its benefit. The provision to make now instead is cheap: keep the
principal's credential field **opaque and format-versioned**, so a bearer
token can be replaced by a biscuit without an envelope schema change.

**Unverified and load-bearing for later:** biscuit-rust is the reference
implementation; a Go implementation (`biscuit-go`) is referred to in our own
notes and in `aae-orc-odia`'s reading list, but this study did **not**
establish its maturity, maintenance status, or spec version coverage. Marvel
is Go. That is a real gate on any step-5 plan and it is §11 task 7.

### A2A AgentCard

The AgentCard is a **discovery and description** artifact: a machine-readable
document at a well-known path declaring an agent's identity, endpoints,
capabilities and skills, with `securitySchemes` / `security` fields following
the OpenAPI 3.2 security-scheme model (apiKey, http, oauth2, openIdConnect,
mtls), and an `AgentCardSignature` — a JWS over a JCS-canonicalized form of
the card, so signatures are stable across serializers.

Adopt it as **the outward-facing projection** of a marvel agent — the answer
to `question-agent-service-directory` and to director attach — and never as
the internal principal. The card is a public document; the principal is an
enforcement input. Conflating them puts a published artifact in the decision
path, which is the shape of every "authority inferred from a document
somebody can write" failure.

*(Version caveat, stated rather than resolved: the well-known path moved from
`agent.json` in early A2A to `agent-card.json` in later versions. Any
implementation should pin the spec version rather than take a path from this
study.)*

### NATS NKEY/JWT

Covered under §C. The one thing to add: the Operator→Account→User chain is
genuinely good and marvel should *use* it, not compete with it. Marvel's
principal decides *who*; the NATS user JWT expresses *which subjects*, minted
short-lived from that decision. The subject-permission grammar lives where it
is enforced, which is right.

### AIP — Agent Identity Protocol (arXiv 2603.24775)

The closest published thing to what §9 grows into, and it is worth a proper
read in the design phase rather than a summary here. Per the paper: neither
MCP nor A2A verifies agent identity (its authors report scanning ~2,000 MCP
servers and finding none authenticating); AIP proposes Invocation-Bound
Capability Tokens combining identity, attenuated authorization and provenance
in a single append-only chain, with **two wire formats — a signed JWT for
single-hop, a Biscuit with Datalog for multi-hop delegation** — and transport
bindings across MCP, A2A and HTTP.

That compact/chained split maps precisely onto the step-1/step-5 split this
study recommends on independent grounds, which is either corroboration or
convergent obviousness; either way it is worth knowing before designing.

**Limits, stated plainly:** it is a preprint. Its adversarial-testing result
(100% rejection across 600 attempts) and its latency figures are the
authors' own, unreplicated by us. The reference implementations named are
Python and Rust; no Go implementation is named. We have verified none of it.

### `aae-orc-mq76` — attribution, and the cheapest early payoff in this lane

mq76 is not prior art; it is the sibling question, and its scope boundary is
correct and should be preserved: **attribution is who acted; auth is who is
permitted.** Its five axes (human principal, fleet installation, harness
invocation, agent within the invocation, session role) are a *naming* schema.
This study is a *permission* schema. They must not merge.

But they should converge at one point, and it is free. mq76's own
premise-check found that bd already ships `--actor`, resolving
`$BEADS_ACTOR > git user.name > $USER`, with an `events.actor` column that is
100% populated and — before that probe — 100% the single value
`Michael Pursifull`. **The gap is an unused field, not a missing capability.**

Recommendation: **the thin principal's string form is exactly what marvel
writes into `BEADS_ACTOR` in the session environment at spawn.** Marvel
already constructs that environment (enforcement locus 1, built), the name is
free because §9 builds it anyway, and the effect is that every bd write by
every marvel-spawned agent becomes attributable the day the principal exists —
before any authorization is enforced at all.

This matters for the lane's politics as much as its engineering: it gives the
identity lane a visible, useful result that lands before the enforcement work,
which is exactly what a forked lane that "merges back as proven" needs. It
also respects ADR-007 — a hook stamping invocation identity at write time is
automating the *recording*, not the judging. It does not pre-empt mq76: mq76
still chooses the shape (structured assignee vs sidecar record vs first-class
entity), and this only supplies a value for whatever shape it picks.

---

## 7. The recorded dissent — represented, not resolved

**The dissent (bet-memo, "What We Deliberately Do NOT Build Yet"):** *"no
director protocol commitment (jabber-like vs A2A vs broker-topics) until the
identity layer question is answered — protocol-after-principal-model, in that
order."*

**The standing compromise (finding-064, ratified operator posture,
2026-07-05):** designs lean identity-first and the envelope carries
sender/recipient principal fields from day one, **but** identity must never
block a non-identity path; the transport probes run without the identity
plane; identity attaches to a working channel rather than gating it.

**The dissent's argument is not timidity, and this study will not flatten
it.** Its claim is that a protocol shipped without a principal model bakes in
the habit of inferring authority from the channel, and that the habit is far
more expensive to remove than to avoid. That claim has field evidence behind
it: M-7 (vsdd-factory #316/#410) is a platform without a principal model
failing in **both** directions — authority spuriously granted (system content
read as user command) and spuriously denied (a relayed human approval
refused). "Origin and authority must be carried out-of-band, attached to
principals, never inferred from content or channel" is the lesson, and it was
learned from a live system, not a whiteboard.

**The compromise's argument is equally real:** a channel with no traffic
teaches nothing, and an identity plane that gates a channel is an identity
plane that never gets built because nothing forces its shape.

The compromise stands. This study is not the place to overturn a ratified
operator posture, and it does not.

**What the study can add is a falsifier instead of a vote.** The dissent
predicts a specific harm — *authority inferred from the channel*. That is
testable, and it can be foreclosed by one rule the envelope can carry today:

> A principal that is absent or unattested must be treated as **unknown**.
> Never as the daemon, never as the operator, never as "local therefore
> trusted."

and one conformance test in the bus slice:

> A message whose principal is `unattested` must not be able to cause any
> state change that a message from a *known but unprivileged* principal could
> not.

If that test can be written and passes, the compromise holds and the
dissent's specific harm is foreclosed by construction rather than by
sequencing. If it cannot be written — if there is any path where the absence
of a principal is more permissive than the presence of a weak one — then the
dissent is right about that path and the ordering should revert for it.

This is why §9.2 recommends replacing the envelope draft's reserved
`"principal": null` with an explicit three-field object whose floor value is
`confidence: "unattested"`. A null invites the reading "not applicable";
`unattested` is a value that can be checked, denied, and tested against. It
is the same information, minus the ambiguity the dissent is worried about.

---

## 8. The hard boundary, carried through

**SOUL §3, as currently ratified: marvel may mint and federate identity; it
never becomes a credential store. Issuance is not custody.**

Applied to every option in this study:

| Option | Boundary status |
|---|---|
| Principal name derived at spawn | Issuance. Clean. |
| Per-session token (mr5c, shipped) | Issuance — the artifact means nothing outside this daemon (§A). Clean. |
| SSH client fingerprint carried to `dispatch` | Not a credential at all; a fact about an already-completed handshake. Clean. |
| Per-method capability table | Policy. Clean. |
| `BEADS_ACTOR` stamping | A name, not a credential. Clean. |
| NATS user JWT minted from the principal | Issuance **only while** NATS is marvel-supervised inside the trust domain (§C conditional). **Becomes custody** on a shared/external cluster. |
| Biscuit attenuation (step 5+) | Issuance. Attenuation strictly reduces authority marvel already granted. Clean. |
| Token exchange at a `Binding` (AgentCore OBO shape) | **On the line.** Clean if marvel never holds the input credential and only relays an exchange; custody if it holds the inbound token to re-use. This is the seam to design carefully, not to hand-wave. |
| Holding a model-backend OAuth session | **Custody under the current wording.** This is dqhf's proposal (§A). Contested, not settled. |
| A token vault (AgentCore Identity shape) | Custody. Refused. |

**Nothing in §9 crosses the line under either the current wording or dqhf's
proposed wording.** That was a selection criterion, not luck: it is what lets
the golden path proceed while `aae-orc-dqhf` is still open, without the lane
having to bet on a ruling.

---

## 9. Recommendation — the golden-path thin principal

Three grades, one name, three buckets, no new cryptography.

### 9.1 The principal

A principal is a **name** plus a **confidence grade** plus an **assertor**.
It is not a token; a token is one of the things that can establish it.

```
marvel://<trust-domain>/<workspace>/<team>/<role>/<session-key>   # a session
marvel://<trust-domain>/client/<pubkey-fp>                         # an mrvl:// client
marvel://<trust-domain>/local                                      # the unix socket
marvel://<trust-domain>                                            # the daemon itself
```

`<trust-domain>` is the daemon (§B). The form is SPIFFE-shaped on purpose, so
that if SPIFFE interop is ever wanted it is a scheme change, not a redesign.

**Three grades, and the third one is a value rather than an absence:**

| Grade | Established by | Who gets it today |
|---|---|---|
| `attested` | marvel spawned the process and minted its token | every marvel-managed session |
| `authenticated` | SSH public-key handshake against `authorized_keys` | every `mrvl://` client |
| `unattested` | reached the unix socket; same-uid, nothing more | the operator's local CLI, and anything else on the host |

`unattested` is the load-bearing one. It must be a named grade because it is
what the operator's own CLI uses today, and because §7's rule requires that
absence of identity be representable and checkable rather than null.

**Nothing new is generated to build this.** The session's token exists
(mr5c). The client's fingerprint exists (`authorizeKey` computes
`ssh.FingerprintSHA256(key)` and puts it in `Permissions.Extensions`
already). The workspace/team/role/generation/index are the session key marvel
already assigns.

### 9.2 What rides the envelope

The envelope draft v1 reserves `"sender": { ..., "principal": null }` and
requires the envelope to validate with it null. Recommended refinement —
same information, no null:

```jsonc
"principal": {
  "name":       "marvel://kinu/aae-orc/ops/reviewer/aae-orc-ops-reviewer-g3-0",
  "confidence": "attested",        // attested | authenticated | unattested
  "assertor":   "marvel://kinu"    // who says so
}
```

Three properties earn their place:

- **No signature in v1.** The assertion is trusted because sender and
  verifier are the same trust domain. This is what keeps finding-064's
  compromise intact: the bus slice proceeds with no identity plane, and the
  fields ride from day one.
- **`assertor` is the whole federation provision.** Today it is always the
  local daemon. When a message crosses a trust domain, the same field carries
  a JWS and the verifier checks it against a bundle (§C). No schema change,
  which is why §C's unanswerable half does not block anything.
- **`confidence: "unattested"` replaces `null`.** §7's argument: a null
  invites "not applicable"; an explicit floor value can be denied and tested
  against.

### 9.3 What is enforced, and where

| Locus | What it decides | Mechanism | State |
|---|---|---|---|
| Spawn (locus 1) | what the process is *given* | environment construction + Policy projection | **BUILT** (finding-006, PR #84) |
| RPC admission | who may call which method | grade check in `dispatch` | **RECOMMENDED — the core ask** |
| Session write path | that a session's writes are its own | token check inside the store's write signature | **BUILT** (mr5c; heartbeat only) |
| Bus | nothing | — | **deliberately none in v1** |
| Tool path (locus 2) | what a running agent may *do* | curtain / gateway interposition | **NOT THIS LANE** |
| Mid-flight revocation (locus 3) | withdrawing authority in flight | — | **NOT THIS LANE** |

**The RPC table.** Nineteen methods, three buckets — a table, not a language.
(Build it from `dispatch` at implementation time, not from this list; §4.4.)

| Bucket | Methods |
|---|---|
| `observe` | `get`, `describe`, `logs`, `events`, `orphans`, `plan` |
| `mutate` | `apply`, `delete`, `scale`, `converge`, `reap`, `run`, `shift`, `reset-health`, `inject`, `capture`, `stop`, `reexec` |
| `self-report` | `heartbeat` — already bound by its own token; the grade is "the session named in the request" |

The check's signature should be written as Cedar's tuple —
`(principal, action, resource, context) → allow/deny` — so the buckets can
become policies later without a rewrite (§6).

**Default grants, and the one that actually tightens:**

| Grade | `observe` | `mutate` | `self-report` |
|---|---|---|---|
| `unattested` (local socket) | yes | **yes** | own session only |
| `authenticated` (mrvl:// client) | yes | **yes** | own session only |
| `attested` (spawned session) | yes | **NO** | own session only |

The first two rows are today's behavior, preserved deliberately: narrowing
the operator's own CLI or an authorized remote admin is a separate decision
with its own blast radius, and doing it in the same change would make the
security result impossible to attribute.

**The third row is the entire day-one behavior change, and it is the one that
matters.** It closes the sibling-agent surface finding-022 named from the
other end: an agent that has been talked into something can no longer
`inject` keystrokes into a peer's pane, `stop` the daemon, `reap`, `scale`,
`shift`, or `reexec` the binary. It can still read, and it can still report
its own heartbeat.

Denials emit an event (`rpc.denied`, alongside the existing
`heartbeat.refused` / `heartbeat.unbound`), which is `ukz`'s "denied calls
produce audit events" and `question-permission-model`'s third open
sub-question answered by the same mechanism mr5c used.

**One deliberate fail-open, bounded in the same shape as mr5c's.** A session
record predating the generalized token has no hash and therefore no attested
grade; it falls to `unattested` and emits an event, rather than losing
`mutate` and breaking a live fleet on upgrade. It drains as those sessions
end. Stating it here because mr5c's version of this decision was the right
one and the same reasoning applies unchanged.

### 9.4 Deliberately deferred

Named, so that the design phase does not relitigate them and so their absence
is a decision rather than an oversight:

certificates and a CA; expiry beyond session lifetime; CRLs; cross-signing;
peer-marvel federation (§C, blocked on a second deployment); biscuit
attenuation; Cedar as an engine; SPIRE; AgentCard publication; NATS user
JWTs; token exchange and credential brokering (dqhf territory); multi-human
principals (mq76's territory); inbound identity / the stage door; mid-flight
revocation; and any policy language whatsoever.

---

## 10. Which sub-questions could not be answered, and what is missing

| | Status | What is missing |
|---|---|---|
| A | Answered, and sharpened | — (but the *ruling* on `aae-orc-dqhf` is a decision, not evidence) |
| B | Answered, conditionally | Nothing for today. M5 (multi-host) will make "daemon or operator?" live. |
| C | **Half unanswerable** | **A second deployment.** Hierarchy vs mutual recognition turns on whether deployments are peers or hub-and-spoke, which nobody has had to decide. Guessing would be the exact failure the topology node's substrate caveat warns about. |
| D | Answered | — |
| E | Answered | — |
| F | Answered, re-ordered | — |

One further item this study deliberately did not settle: **the maturity of
`biscuit-go`, and Cedar's Go story.** Both are load-bearing for step 5+ and
neither was verified here (§6). They are §11 task 7 rather than a conclusion.

---

## 11. What the design phase should task out

Flat units per `.claude/rules/bd-hierarchy.md`. **No umbrella and no
children** — these are seven independent issues with three dependency edges
between them. Sizes are shape, not estimates.

1. **Carry the caller's identity through `handleRWC`.** SSH path: pass the
   `ssh.Permissions` extensions (`user`, `pubkey-fp`) through to `dispatch`.
   Unix path: supply the `unattested` local grade. Plumbing only, no
   behavior change, no enforcement. *This is the §1 headline gap and it
   unblocks 3.*

2. **Name the principal.** The `marvel://` string form, the `kind`, and the
   three confidence grades as a type in `internal/api`; plus rendering the
   name into `BEADS_ACTOR` in the session environment at spawn (§6, mq76
   convergence — the lane's early visible payoff, and it lands without any
   enforcement). *Independent; can start immediately.*

3. **Enforce per-method grades in `dispatch`.** Build the three-bucket table
   from `dispatch` at HEAD, apply the §9.3 default grants, emit `rpc.denied`
   on refusal, with the bounded fail-open for pre-upgrade session records.
   *Depends on 1 and 2. Closes `aae-orc-ukz`.*

4. **Generalize the session token from heartbeat-specific to
   session-general.** `MARVEL_SESSION_TOKEN` with `MARVEL_HEARTBEAT_TOKEN`
   kept as an alias so nothing in flight breaks, so that an `attested`
   principal exists on *every* RPC rather than only on `heartbeat`.
   *Depends on 2. Does not re-specify mr5c — mr5c shipped; this widens its
   scope.*

5. **Add a read-only grade to `authorized_keys` entries.** `keys authorize
   --readonly`, so a monitoring client gets `observe` without `inject` and
   `stop`. *Depends on 3. Closes the third OPEN in
   `question-mrvl-exposure-and-trust-bootstrap`.*

6. **Amend the envelope draft and add the dissent's conformance test.**
   Replace `"principal": null` with the §9.2 three-field object; add the §7
   test that an `unattested` principal cannot cause a state change a known-
   but-unprivileged principal could not. *Independent of 1–5; belongs with
   the M2 bus slice, not with this lane's daemon work.*

7. **Verify the two deferred-format assumptions, no code.** `biscuit-go`
   maturity, maintenance and spec coverage; Cedar's Go integration story
   against marvel's zero-CGo posture. Record the result so that the step-5
   format choice is not made on this study's unverified extrapolation.
   *Independent. Cheap. Should happen before any step-5 design.*

Dependency edges only: 1 → 3, 2 → 3, 2 → 4, 3 → 5. Items 6 and 7 are
unblocked.

What this list deliberately does **not** contain: a principal-model epic, a
policy-engine ticket, a keys-layer ticket (§D: the keys layer is adequate and
untouched), a federation ticket (§C: blocked on evidence that does not
exist), or anything that presumes a ruling on `aae-orc-dqhf`.

---

## 12. Limits of this study

- **No code was run.** Everything in §3 and §4 is reading and grep at
  `1466313`. No probe, no build, no test executed. The premise table is what
  it is worth.
- **No third-party system was executed.** §6's claims about Cedar,
  AgentCore, SPIFFE/SPIRE, Biscuit, A2A, NATS and AIP come from those
  projects' own specifications and vendor documentation read 2026-09-04, not
  from running them. Per `.claude/rules/upstream-claim-gate.md`, none of it
  may be published to a third party without running the gate. Two
  characterizations are explicitly **extrapolation**, labelled at the point
  of use: Cedar's cgo/sidecar cost for a Go consumer, and `biscuit-go`'s
  fitness. Task 7 exists to retire both.
- **Conditional validity (finding-045).** §B and §C assume single-host
  operation, one daemon per `~/.marvel`, and `mrvl://` as the administrative
  transport. M5 is unbuilt. §C's federation direction is the conclusion most
  likely to be wrong when a second host exists, and §C's NATS conditional
  (account signing key on the issuance side only while the cluster is
  marvel-supervised) will move quietly if the bus is ever re-hosted for
  unrelated reasons.
- **A ruling is pending underneath §A.** `aae-orc-dqhf` is open and proposes
  moving the credential boundary. §9 is designed to be correct under either
  ruling, but §6's AgentCore verdict ("refuse the vault") and §8's table
  would both need revisiting if dqhf is ruled in with the durable-vs-session
  wording rather than the revoke-or-re-mint wording proposed in §A.
- **This study does not price the work.** §11 gives shapes and dependencies,
  not estimates.
- **Provenance of the ticket-sourced corrections in §4.4 and §4.2.** They
  were read through `aq show` (i.e. `bd show`), not through `bd sql`'s table
  renderer, which silently truncates long cell values while still reporting
  a full row. Re-checked at write time against `bd sql --json`:
  `aae-orc-bs3x` carries 647 description + 537 notes characters and
  `aae-orc-mr5c` carries 487 + 2567, all of which `aq show` rendered in
  full. So the "13-method dispatch" and "mr5c shipped" readings are not
  truncation artefacts. The topology node's revocation claim (§4.3) came
  from the YAML file directly and never touched bd. Any future work that
  reads ticket prose through `bd sql` without `--json` should assume it is
  seeing a prefix.
- **What would falsify the recommendation.** If per-method authorization on
  the daemon turns out not to be the binding constraint — for example if the
  first real multi-agent workload's failure is an agent reading a sibling's
  environment rather than calling a sibling's RPC — then §9 solves the wrong
  half and `question-credential-adjacency` (locus 2/3, curtain) outranks this
  lane. finding-022 names that limit explicitly: an agent that hunts can take
  a sibling's token from the environment, and no amount of RPC authorization
  reaches it.

---

## Sources

Internal (this repo and the orc):
`aae-orc::question-marvel-identity-authority-topology`,
`aae-orc::question-agent-identity-authority`,
`aae-orc::question-credential-adjacency`,
`question-permission-model`, `question-mrvl-exposure-and-trust-bootstrap`,
`finding-022`, `finding-029`, `finding-031` (voice),
`aae-orc::finding-064`, `aae-orc::_kos/ideas/marvel-as-federation-authority.md`,
`aae-orc::_kos/ideas/agent-team-infra-services.md`,
`aae-orc::_kos/ideas/agentcore-crosswalk.md`,
`aae-orc::_kos/ideas/supervisor-as-agent-not-infrastructure.md`,
`aae-orc::_kos/ideas/marvel-agentic-resource-matrix.md` (rows 11, 17),
`aae-orc::docs/design/director-envelope-and-adapter-events.md`,
`aae-orc::docs/atelier-review-2026-07/bet-memo.md`,
`aae-orc::docs/marvel-remap-2026-08.md` (Lane 4),
`aae-orc::docs/roadmap.md` (Track M),
`docs/design/service-provider/02-architecture.md`,
`callbook::docs/enrollment.md`,
bd `aae-orc-odia`, `aae-orc-bs3x`, `aae-orc-ukz`, `aae-orc-mr5c`,
`aae-orc-mq76`, `aae-orc-dqhf`.

External (documentation read 2026-09-04; none executed):

- [Agent2Agent (A2A) Protocol Specification](https://a2a-protocol.org/latest/specification/)
- [Cedar policy language](https://github.com/cedar-policy/cedar) and [Cedar's verified development with Lean](https://lean-lang.org/use-cases/cedar/)
- [SPIFFE concepts](https://spiffe.io/docs/latest/spiffe-about/spiffe-concepts/) and [Working with SVIDs](https://spiffe.io/docs/latest/deploying/svids/)
- [Biscuit specification](https://doc.biscuitsec.org/reference/specifications.html) and [Third-party blocks](https://www.biscuitsec.org/blog/third-party-blocks-why-how-when-who/)
- [NATS decentralized JWT authentication](https://docs.nats.io/running-a-nats-service/configuration/securing_nats/auth_intro/jwt)
- [AgentCore Inbound and Outbound Auth](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-oauth.html) and [On-behalf-of token exchange](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/on-behalf-of-token-exchange.html)
- [AIP: Agent Identity Protocol for Verifiable Delegation Across MCP and A2A](https://arxiv.org/abs/2603.24775) (preprint; claims unverified)
