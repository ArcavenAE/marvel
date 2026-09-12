# marvel contracts

Schema-first contracts for the agent bus and the marvel adapter surface. One
schema set is the source of truth; codegen emits Go and Rust types plus
validators from it, so the bound consumers cannot drift apart (the finding-034
parallel-type-drift class, killed by construction). This is bd aae-orc-b69n.

## Decisions

- **JSON Schema, not protobuf.** The wire is JSON on NATS, and A2A v1.0
  publishes a JSON Schema, so JSON Schema consumes A2A directly and keeps the
  JSON wire. Confirmed against ratified direction (vision.md director section:
  the envelope is A2A v1.0, codegen generates from it). Not a one-way door: a
  `.proto` target can be emitted from the same schema if a peer ever needs gRPC.
- **A2A v1.0 is the anchor.** The director envelope is an A2A-compatible
  profile. The FIPA-ACL performative subset maps onto A2A message intent, and
  the mapping is recorded below. Vendoring the pinned A2A v1.0 schema and the
  formal derivation land with the codegen harness (next PR); this PR is the
  profile schema and its validation.
- **Bound consumers:** marvel (Go) and beadle (Rust). Both are codegen targets;
  a schema change regenerates both.

## Files

- `schema/director-envelope.schema.json`: the envelope agents send (peer to
  peer, including the human), draft 2020-12.
- `schema/testdata/valid-*.json`, `invalid-*.json`: fixtures the validation
  recipe checks; the invalid set pins each guard (bad `agent_id`, missing
  `authority`, a smuggled credential field).

Validate locally with `just contracts-validate`.

## Envelope shape (the frozen and the reserved)

The identity and authority shape was set with the architect against the naming
register (director/sim/requirements.md, R-01 through R-86).

Locked now:
- `sender.agent_id` is the address and nothing else, closed class
  `^[a-z0-9][a-z0-9-]{0,31}$` (R-76), the same class on every segment of
  `recipient.address`.
- `sender.session` is the harness session UUID, nullable now, required once
  marvel sets it at spawn (R-73, R-84).
- `authority` is a top-level block (a sibling of `sender`, never inside
  `content`): `strength` is a closed enum `direct | relayed | none` (stated,
  never inferred, R-02), and `seat` is `null` or `{role, key, epoch}` where
  `epoch` is a monotonic fencing token (R-55).
- `content.type` is a closed enum, and content never carries authority
  (R-67, R-68).
- No credential or secret field appears anywhere: the credential-to-subject
  binding (R-77, R-85) is transport level, so the envelope carries claims, not
  the thing that proves them.

Reserved, deliberately not frozen:
- `sender.principal` is a nullable, extensible container (`null | {kind, ...}`,
  `kind` open, seeded `none` and `launcher`). The envelope validates with it
  null; nothing reads it yet. Contents are deferred (R-53) until a principal
  writes the bus without a human (the R-77 trigger).
- A signature field reserves nothing; when it arrives it wraps the envelope or
  rides the transport, additively.

## A2A v1.0 mapping (recorded here, formalized with the codegen PR)

| envelope field | A2A v1.0 |
|---|---|
| `performative` | message intent (FIPA-ACL subset over A2A message semantics) |
| `content.type` / `content.data` | message parts |
| `content.refs` | pointer parts / artifact references |
| `sender`, `recipient`, `authority`, `principal` | profile extension metadata on the A2A message |
| `message_id`, `correlation_id`, `conversation_id`, `in_reply_to` | message and task correlation |

## Evolution

Additive optional fields only; reserved objects stay nullable; a breaking
change bumps `schema_version`. The rule is repeated in the schema `$comment` so
it travels with the file.

## Open, for the next passes

- Vendor and pin the A2A v1.0 schema; wire Go and Rust codegen; add the CI check
  (this recipe is local for now).
- The marvel adapter event vocabulary (the co-designed twin that must not drift)
  is a sibling schema in a following PR.
- The finding-151 semantic fields beyond identity and authority
  (evidence-standard, deviation-license, ask-state, blocked-at-delivery, and
  message `duplicate_of`) are a separate design pass, not in this schema; their
  envelope-versus-transport placement follows the finding-151 layer split.
