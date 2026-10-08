# finding-marvel-0w59: three shapes for a credential manager, each classified under ADR-009 as written

Date: 2026-10-08
Status: frontier
Supports: question-marvel-service-provider-shape
Cites: ADR-009 (credential custody boundary), `docs/design/services-list.md` section 4.2, `aae-orc-yuem0` (secret-manager target), `aae-orc-p4fzv` (the ADR-009 revisit)
Source: research by the research-supervisor seat, 2026-10-08, from local records, marvel source at origin/main 535d167, and public web pages read the same day

## Why this matters

The operator asked whether a credential manager could be a marvel Service, embedded in marvel, or a separate system marvel manages. The answer turns on how ADR-009 classifies what marvel would then hold, and ADR-009 is being revisited (`aae-orc-p4fzv`).

This finding is evidence for that revisit. It does not rule on it. Each shape is classified under ADR-009 as written, and the points the revisit owns are named, not decided.

## Where the question stands

- The secret-manager target has been open since 2026-09-16, candidates A to G (`aae-orc-yuem0`). `docs/design/services-list.md` section 4.2 lists a self-hosted vault as candidate D, class `secret-store`, not reserved in code.
- marvel's Service record carries class, provider, mode (managed, adopted, external; internal reserved) and caller_identity. Only message-bus/nats-server is registered.
- Today marvel holds a heartbeat token, broker passwords and a pushed leaf seed (memory only). All three are issuance. Nothing is brokered and nothing is in custody.
- The nearest precedent is ADR-009's tripwire: "Marvel holding the NATS account signing key is issuance only while NATS is marvel-supervised inside marvel's own trust domain."

## The three shapes

Shape (a), a marvel Service, and shape (c), a separate system marvel manages, overlap: a Service in `managed` mode is shape (c) plus a Service record. This finding reads them as:

- **(a) Service, mode `external` or `adopted`:** a Service record for a vault marvel reaches but does not run. Seats discover it through marvel, and marvel brokers access.
- **(b) Embedded:** the credential manager runs in marvel's process.
- **(c) Managed:** marvel runs the vault as a supervised child, covering start, health, and unseal or bootstrap, with a Service record in `managed` mode.

## Candidates and fit

Vendor statements are the vendors' own, as read on 2026-10-08, and are not verified behavior. Sources are listed at the end.

| Candidate | Embeddable in Go | Short-lived credentials | What a manager must hold | Fit note |
|---|---|---|---|---|
| OpenBao | No. The README says importing it as a dependency "is NOT, and has NEVER been" a supported way | Yes per its docs: leases, TTLs, child tokens, revocation | Unseal material, or a seal config; a token or role to administer it | MPL-2.0, Linux Foundation project. A PKCS#11 seal is available. A Static Key seal unseals from a host file. Cloud KMS seals became plugins in 2.7.0 |
| HashiCorp Vault | No | Yes (same model) | Same | BSL 1.1 since 2023-08-10. The additional use grant permits production use unless it means "offering the Licensed Work to third parties on a hosted or embedded basis in order to compete with IBM Corp's paid version(s)". What "embedded" covers for marvel is unresolved, because the license FAQ page did not render its answers. PKCS#11 is Enterprise only |
| Infisical | No (a server with Postgres and Redis) | Dynamic secrets are on a paid plan | `ENCRYPTION_KEY` env var as the root key (HSM is Enterprise) | MIT core. Heavier footprint. The audit-tier documentation and the pricing page read differently |
| SOPS plus age | Yes (`sops/v3/decrypt`, `filippo.io/age` in-process) | No | The age private key | No leases, no revocation, no record of who decrypted what |
| AWS Secrets Manager with IAM Roles Anywhere | No (cloud) | Secrets Manager rotates stored secrets at most every 4 hours but mints nothing. Roles Anywhere issues sessions of 15 minutes to 12 hours from a CA marvel would run | The CA private key; the AWS trust anchor setup | Adds a cloud dependency. A seat id can appear in CloudTrail as the session name |

No maintained first-party NATS credential engine was found for Vault or OpenBao. The best-known community plugin (edgefarm) was archived on 2025-01-21, so NATS NKey and JWT issuance stays with marvel either way.

## Each shape under ADR-009 as written

### (a) Service record, vault reached and not run (external or adopted)

- **What marvel holds:** its own credential to the vault. The fit that stays inside the line is marvel signing per-seat JWTs that the vault validates (`jwt_validation_pubkeys`). marvel's most durable artifact is then its own signing key, which marvel mints and can rotate. The alternative is a long-lived AppRole SecretID or token for marvel itself.
- **What seats get:** short-lived, scoped credentials the vault minted. Revoking a seat's token tree is one call, per the vault's token documentation.
- **Class:** issuance for marvel's signing key. Brokering for what seats receive, which is the case ADR-009 names as permitted.
- **Open, for the revisit:** whether marvel's own vault credential counts as bearer authority at a third party. That depends on whether a fleet-owned vault server is a third party, which is `aae-orc-p4fzv`'s open question.

### (b) Embedded (SOPS plus age, the only candidate that embeds)

- **What marvel holds:** the age private key, plus whatever the encrypted files contain.
- **Class:** decided by what the files hold. Material marvel minted itself is issuance. Third-party bearer authority, such as vendor API keys or OAuth refresh tokens, is custody: marvel holds the most durable artifact and nothing can revoke the copy it decrypts.
- **Also:** nothing can be brokered here, because the shape mints nothing, revokes nothing and audits nothing. It stores, and does not issue.

### (c) Managed (marvel runs OpenBao or Vault as a supervised service)

- **What marvel holds:** the lifecycle (start, a `/sys/health` poll where 503 is sealed and 501 is not initialized) and some form of unseal or bootstrap material. The options:
  - Shamir: a human quorum at every restart.
  - Auto-unseal: a cloud KMS or a Transit server becomes a hard dependency.
  - OpenBao Static Key: the host file becomes the root-key custodian.
- **Class:**
  - By the tripwire precedent, the vault's root and unseal material is issuance while the vault stays marvel-supervised inside marvel's trust domain.
  - Seat credentials are brokering, as in (a).
  - If the vault stores third-party grants, such as a vendor OAuth refresh token, the most-durable-artifact test may follow the grant to whoever can unseal and read it. If marvel holds the unseal material, the case can be argued as marvel holding custody through the vault. ADR-009 as written does not settle this.
- **Cost the others avoid:** a human or an external KMS returns at every reboot, unless the static seal is accepted.

## What the evidence says, without ruling

- Of the three shapes, only (a) with marvel-signed per-seat JWTs keeps everything marvel holds inside ADR-009's line, whatever answer the revisit gives on "fleet-owned server". marvel holds its own key, and seats get brokered, short-lived credentials.
- (c) is (a) plus the unseal question. Its ADR-009 class turns on two points the revisit owns: whether a supervised vault is inside marvel's trust domain, as the tripwire suggests, and what the vault is allowed to store.
- (b) can store, but cannot broker. It is the only shape where a single file read makes marvel a custodian.
- On the evidence read, OpenBao fits shapes (a) and (c) better than Vault: its license has no "embedded basis" exclusion, and PKCS#11 is included. Vault's licensing question is open, not answered.

## Not checked

- Engine parity between Vault and OpenBao.
- OpenBao's JWT auth and telemetry pages.
- Infisical's JWT claim binding, and whether its identities can create other identities.
- Conjur and Bitwarden Secrets Manager. They were not researched, so leaving them out is a judgement, not a measurement.

## Sources

Public pages, read 2026-10-08.

- Vault license: https://github.com/hashicorp/vault/blob/main/LICENSE ; license change announcement, 2023-08-10: https://www.hashicorp.com/en/blog/hashicorp-adopts-business-source-license ; license FAQ (answers did not render): https://www.hashicorp.com/en/license-faq
- Vault seals, tokens, JWT auth, health: https://developer.hashicorp.com/vault/docs/configuration/seal , https://developer.hashicorp.com/vault/docs/concepts/tokens , https://developer.hashicorp.com/vault/docs/auth/jwt , https://developer.hashicorp.com/vault/api-docs/system/health
- OpenBao README and releases: https://github.com/openbao/openbao , https://github.com/openbao/openbao/releases ; seals and plugins: https://openbao.org/docs/configuration/seal/ , https://openbao.org/docs/configuration/seal/static/ , https://openbao.org/community/deprecation/
- Infisical: https://github.com/Infisical/infisical/blob/main/LICENSE , https://infisical.com/pricing , https://infisical.com/docs/self-hosting/configuration/envars , https://infisical.com/docs/documentation/platform/audit-logs
- SOPS and age: https://pkg.go.dev/github.com/getsops/sops/v3/decrypt , https://pkg.go.dev/filippo.io/age
- AWS: https://docs.aws.amazon.com/secretsmanager/latest/userguide/rotate-secrets_schedule.html , https://docs.aws.amazon.com/rolesanywhere/latest/userguide/introduction.html
- Archived NATS plugin: https://github.com/edgefarm/vault-plugin-secrets-nats
