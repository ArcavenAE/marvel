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
- `schema/director-event.schema.json`: the wire twin of the marvel runtime
  adapter event vocabulary (the twelve kinds `internal/runtime/events/events.go`
  emits), draft 2020-12. See "Event vocabulary twin" below.
- `schema/testdata/event/valid-*.json`, `event/invalid-*.json`: one valid
  round-trip fixture per kind plus a 64 KiB truncation-boundary frame; the
  invalid set pins the frame guards (unknown kind, wrong data shape, wrong
  `schema_version`).
- `go/envelope`, `go/event`: the Go halves (types + validator for the envelope;
  validator only for the event twin, whose Go source of truth is `events.go`).

Validate locally with `just contracts-validate`.

## Validation scope and a migration step

The `authority` block is required, so the validator applies to emitters that
generate from this schema. The live phase-0 shim (director-mcp) predates the
schema and emits no `authority` block, so today's bus traffic does not validate
against it yet. That is a known migration step, not a defect: the shim is being
regenerated under the director#3 and director#4 work already in flight (director
PRs #10 and #11), and the phase-0 envelope-v1 refresh is a later slice of this
same b69n arc. Until a bus emitter is regenerated from this schema, validate
regenerated emitters and fixtures, not the current live traffic.

The `$id` base is `https://schema.arcaven.com` (operator ruling D8). A `$id`
need not resolve; the `schema` subdomain stays isolated from the apex site and
nothing need be hosted there now. The event-vocabulary twin uses the same base.

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

## A2A v1.0 mapping

A2A v1.0's normative artifact is the proto (`specification/a2a.proto`; see
`a2a/PINNED.md`), so the mapping is written by proto field name. The whole
director profile rides in the A2A `Message.metadata` map under one namespaced
profile key, declared as an A2A extension identified by a URI. Profile fields
never go in `Message.parts`; only `content` maps to parts. (Architect ruling,
2026-09-12.)

- **`Message.extensions`** declares the profile: the extension URI is the
  schema `$id`, `https://schema.arcaven.com/director/envelope/v1`. A plain A2A
  receiver ignores the metadata block; a profile receiver keys on this URI to
  validate it.
- **`Message.metadata["https://schema.arcaven.com/director/envelope/v1"]`**
  carries the profile object: `schema_version`, `sender`, `recipient`,
  `authority`, `principal`, `performative`, `message_id`, `correlation_id`,
  `conversation_id`, `in_reply_to`, `reply_by`, `expires_at`, `sent_at`,
  `trace`.
- **`Message.parts`** carries `content` (the payload): `content.type` selects
  the part kind, `content.data` is the part body, and `content.refs` are pointer
  or artifact-reference parts.
- **`Message.message_id` and task correlation** align with the profile's
  `message_id`, `correlation_id`, `conversation_id`, and `in_reply_to`.

Authority and identity live in metadata, never in parts, so a body (a part)
cannot assert its own authority (R-67, R-68).

## Event vocabulary twin

The envelope is agent speech. The event vocabulary is something else: the
normalized frame every marvel runtime adapter emits after digesting a
harness-specific telemetry stream (session, turn, message, tool, permission,
auth, health, error). The two are distinct seams, and the lift from an event to
an envelope is the boundary between them; the twin invents no envelope field.

Unlike the envelope, the event vocabulary is not schema-first. Its source of
truth is `internal/runtime/events/events.go`, mature Go carrying behavior a
schema cannot express (`Occupancy()` and the additive/subsumptive `Layout`,
`TotalMismatch()`, `AdditiveConfirmed()`, the `SeqAssigner`, the 64 KiB
`Truncate` discipline). So `director-event.schema.json` is a wire twin: it
models the serializable frame and per-kind `data` only, and a Rust consumer
(beadle) generates from it, so the Go emitter and the Rust reader cannot drift
(finding-034) without regenerating the tuned Go. Per the architect ruling
(2026-09-13) the source of truth flips to schema-first only on a second
producer of these events, never a consumer.

Four conditions hold the twin honest, all enforced by `go/event`'s tests:
- the `event` enum equals `events.AllKinds` both ways (a kind in one but not the
  other fails the guard);
- there is a valid round-trip fixture per kind, plus a frame at the 64 KiB
  truncation boundary, each decoded through `events.Event` and re-validated;
- a schema `$comment` names `events.go` as source of truth and lists the
  Go-only behaviors;
- the frame is closed and bumps `schema_version` on a breaking change, while
  per-kind `data` is lenient so additive growth does not bump it.

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
