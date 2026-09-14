# Reproduce the A2A JSON bundle from the pinned proto (provenance chain)

Status: idea, deferred. Filed during b69n slice 2 (schema-first contracts,
2026-09-12). Not a bd ticket: the three-layer rule keeps this a local note until
we commit to doing it on a timeframe.

## The point

b69n vendors the A2A v1.0 JSON Schema by consuming the published bundle at
`https://a2a-protocol.org/v1.0.0/spec/a2a.json`, pinned by sha256
(`contracts/a2a/PINNED.md`). That bundle is a non-normative build artifact: at
A2A v1.0 the normative machine source is the proto (`specification/a2a.proto`),
and the JSON is generated from it and not committed upstream. The sha256 pin
gives integrity, but it does not give us a chain back to the normative source
that we produced ourselves.

The stronger option is to reproduce the bundle from the pinned proto: fetch
`specification/a2a.proto` at the pinned tag, run A2A's own
`scripts/proto_to_json_schema.sh` (bufbuild `protoc-gen-jsonschema`), and confirm
the output matches the vendored bundle (or vendor our own reproduction). That
turns "we trust the published file" into "we can regenerate it from source and
prove it."

## Why this org would want it

Source-material provenance is treated as load-bearing here, not a nicety.
Sideshow's frozen-composition pipeline already does the analog for content packs:
upstream installers run once in auditable CI and emit a signed artifact with an
attestation tracing every upstream source (charter F24). An A2A anchor that a
peer marvel or beadle relies on is exactly the kind of upstream that should trace
to its normative source, not to a docs-site download.

## What it would take

- Vendor `specification/a2a.proto` at the pinned commit
  (`173695755607e884aa9acf8ce4feed90e32727a1`, tag v1.0.0) alongside the JSON.
- A reproduction step (protoc, buf, protoc-gen-jsonschema) that regenerates the
  JSON bundle and diffs it against the pin, run in CI.
- Optionally a signed attestation (cosign, the sideshow model) tying the JSON we
  ship to the proto commit it came from.

## When to pick it up

When any of these lands: a signed source-attestation requirement for fleet
contracts; a peer marvel or external consumer that must verify our anchor's
origin; or an A2A version bump where reproducing-and-diffing is the safest way to
see what actually changed. At that point this becomes a bd ticket (commitment)
rather than a note.

## References

- `contracts/a2a/PINNED.md` (the current sha256 pin and provenance record)
- aae-orc-b69n (schema-first contracts)
- charter F24 (sideshow source-material provenance chain)
- A2A `specification/json/README.md` (the a2a.json non-normative statement)
