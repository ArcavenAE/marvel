# Pinned A2A upstream

The director envelope is an A2A v1.0 compatible profile. This directory pins the
A2A v1.0.0 schema we map onto, so the anchor cannot move under us.

## What is pinned

- **File:** `a2a-v1.0.0.json` (JSON Schema, draft 2020-12, 47 definitions
  including `Message` and `Task`).
- **sha256:** `6b6560c726289734799b7d5883be84e4cc0452600736db0f811341bac43b8d62`
- **Retrieved:** 2026-09-12 from `https://a2a-protocol.org/v1.0.0/spec/a2a.json`.

## Provenance, and a point worth knowing

At A2A v1.0.0 the canonical machine artifact is the protobuf definition
(`specification/a2a.proto`), not JSON Schema. `a2a.json` is a non-normative
build artifact generated from that proto (bufbuild `protoc-gen-jsonschema`) and
is deliberately NOT committed to A2A source control; it is published only to the
docs site per release. So the ratified phrasing "generate from the A2A v1.0 JSON
Schema" consumes a derived artifact, and the normative upstream is the proto.

- **Normative upstream:** `specification/a2a.proto` at tag `v1.0.0`, commit
  `173695755607e884aa9acf8ce4feed90e32727a1` (github.com/a2aproject/A2A).
- **What we vendor:** the generated JSON Schema bundle for that release, which
  is the JSON form of the normative proto.
- **Why the generated bundle and not the proto:** the ratified tooling is
  JSON-Schema-first (the wire is JSON on NATS; our envelope is JSON Schema). The
  published bundle is the JSON form of v1.0.0, so it is the direct anchor. This
  keeps us off a protobuf toolchain.

## Reproducibility (deferred)

The stronger provenance option is to reproduce the bundle ourselves from the
pinned proto, running A2A's `scripts/proto_to_json_schema.sh` (needs `protoc`,
`buf`, and `protoc-gen-jsonschema`). Deferred until a reproducibility or signed
source-attestation requirement lands (relates to the fleet source-material
provenance value). Until then the sha256 above is the integrity anchor. The
deferred option is captured as a tracked idea:
`_kos/ideas/a2a-reproduce-from-proto-provenance.md`.

## Bumping the pin

A2A `v1.0.1` exists (a patch: prefer `application/a2a+json` in the HTTP binding,
plus `TaskStatus` fixes). Bumping is deliberate: update the URL, the tag and
commit, the retrieved date, and the sha256 here, regenerate the consumers, and
run the contract validation and codegen recipes.
