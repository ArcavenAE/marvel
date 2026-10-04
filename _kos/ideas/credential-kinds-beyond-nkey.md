# Credential kinds beyond the NATS nkey seed

- **Status:** idea (pre-hypothesis, no commitment).
- **Date:** 2026-10-04
- **Origin:** operator ruling, verbatim: "we put in a kos idea / bd ticket around
  expanding marvel credential to handle more sensitive data transfer ssh keys,
  certs, passwords, license files, api keys lots whatever we need".
- **Subject:** marvel (its credential resource). Filed in the marvel graph per
  the subject test.
- **Related:** [[mcp-service-provider-and-credential-custody]] (a credential
  that stays out of a seat's reach), [[identity-at-launch-and-managed-nats]]
  (the nkey seed's one use today). Orc ADR-009 and SOUL section 3 set the
  custody boundary this idea has to answer to. A research probe on the
  bd credential is in flight and feeds this idea; its findings are its own and
  are not restated here.

## What the operator wants

A way to move more kinds of sensitive material to the machine that needs it
through marvel, not only the one kind marvel carries now: ssh keys,
certificates, passwords, license files, API keys, "whatever we need".

## What is true today

Each fact below was checked against marvel origin/main ea47261 on 2026-10-04.

- **The kind set is closed and holds one member.** `CredentialKind` has one
  constant, `nats-nkey-seed`, and `ValidCredentialKind` returns true for that
  alone (`internal/api/types.go:824-836`; the `Kind` field is at `:851`).
- **The store validates the kind and not the value.** `CreateCredential`
  rejects an unknown kind and then stores whatever bytes it was given
  (`internal/api/store.go:614-627`, from `CreateCredential` at `:614`). Nothing checks that a value looks like a
  seed.
- **A name nothing reads is stored anyway.** A put under an unrecognised name is
  stored, acknowledged, logged, and read by nothing. This is marvel#344, open.
- **Values are transient.** The bolt layer has no credential bucket, `Persist`
  is always false, and a daemon restart drops every value
  (`internal/api/store.go:606-612`). The sender pushes it again.
- **A value never leaves through the read surface.** `get`, `describe` and
  `list` strip it. Revealing it is `marvel credential get --reveal`, and the daemon serves
  it on the local socket only (`internal/daemon/scope.go:111-114`;
  `internal/daemon/credential.go:108-109`).

## A live case

A second cluster needed the bd client password. It went into that cluster's
marvel store as `bd/client` under the `nats-nkey-seed` kind, because that is
the only kind there is. The credential's binding reads "NOT A SEED", and nothing
in marvel reads that field (`aae-orc#461`, the comment on moving it to the
keychain). So the kind field says one thing, the binding says another, and the
operator keeps them apart by hand.

## The design question

This idea does not answer it.

SOUL section 3 and ADR-009 draw the line by audience: a component may hold an
artifact it can itself revoke or re-mint without a human at a third party's
console, and must not hold an artifact that is bearer authority at a third
party. Brokering a short-lived credential from a vault is inside the line;
holding the long-lived grant is not.

The nkey seed is on the issuance side: the daemon can drop it and ask for it
again. An API key, an ssh private key or a password for someone else's service
is closer to the other side: marvel could not re-mint it, and whoever holds it
holds that authority.

So the question is which of the operator's kinds marvel can carry as issuance
(a transient hand-off it can drop and re-request), which would make it a
custodian, and what the answer does to the design: a new kind per material, or
a different resource for material marvel only relays.

## Questions this leaves open

Posed as questions, none prescribed.

- For each kind the operator named, is marvel issuing it, relaying it, or
  holding it? Does the answer differ for a license file, which may not be bearer
  authority at all?
- Does a transient, memory-only value change the custody answer, or does it only
  change how long marvel holds the material?
- Is a closed set of kinds still right, or should a kind carry a declared
  custody class that the store enforces? marvel#344 shows what an unchecked name
  costs.
- Should the store check a value against its kind, so "NOT A SEED" stops being a
  note in a free-text field?
- Which readers need a value, and does `get --reveal` on the local socket stay the one
  way out?

## What would make this testable

A probe that takes one kind the operator named, runs it through the audience
test above, and reports whether marvel is issuing, relaying or holding it. The
in-flight bd credential probe is the first instance and may settle part of this.
