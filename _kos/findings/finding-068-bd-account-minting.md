# finding-068: bd attribution is self-asserted today; dolt can authenticate a seat by JWT, but delegated CREATE USER is root-equivalent

**Date:** 2026-10-03
**Placement:** marvel. The subject is marvel minting the identity a seat writes to bd under. It answers `question-bd-managed-extended-service` (d) and part of (e).
**Number:** 068, the next free id across main and open PRs (#528 065, #530 066/067).
**Rulings (operator, 2026-10-04, ratified defaults):**
- (i) a marvel signing key trusted by the bd server is custody-adjacent until ruled otherwise, and nothing is built on it in the meantime;
- (ii) the identity is the slot, not the session;
- (iii) path C now (per-cluster SQL users, `BEADS_ACTOR` from the slot), with path B parked behind TLS on the bd server (aae-orc-4ascm).
**Medium:**
- bd v1.1.2 source;
- read-only SELECTs on kinu's live bd server;
- a scratch dolt 2.2.3 sql-server on loopback with throwaway users, keys and certs.

Scripts and transcripts are in `data/`. Every claim marked MEASURED below has its command or script line there.

## 1. What bd records as a change's author today

There are three identities, and only one is authenticated.

| Layer | Set from | Authenticated? | Evidence |
|---|---|---|---|
| `actor`, `assignee`, `created_by`, `owner`, `events.actor` | `--actor` > `BEADS_ACTOR` > `BD_ACTOR` > git `user.name` > `$USER` | No: any connection can write any string | bd `cmd/bd/main.go:480-490`; MEASURED: a session account inserted actor `marvel/aae/someone-else` and was accepted (`run2.txt` T3) |
| dolt commit author and committer | bd always passes `--author "<GIT_AUTHOR_NAME or beads> <GIT_AUTHOR_EMAIL or beads@local>"` | No | `internal/storage/dolt/store.go:901-910, :1774`; `versioncontrolops/commit.go:52` |
| SQL user | `BEADS_DOLT_SERVER_USER` and password | Yes | finding-149 minted `skippy@'%'` |

- **The SQL user is recorded nowhere** (MEASURED).
  - `--author` overwrites BOTH the `author` and `committer` columns of `dolt_log` (`run3.txt` P1).
  - Live, 14 days: 875 of 875 commits are author `beads` (a rolling window, measured 2026-10-03; over the fixed window 2026-09-19 to 2026-10-03, 857 of 857), while `events` carries 1,408 rows in at least three actor classes (host/tty for two people's terminals, and `marvel/<ws>/<session>`).
  - All-time: 4,478 `beads@local`, 8 `beads@localhost`, and 639 `root@%`, the last only between 2026-04-11 and 05-01.
- **So skippy's writes and kinu's writes are indistinguishable in dolt history** except by the self-asserted actor string. finding-149's "distinct attribution" was that string.
- **Per session over one connection:** only the actor can differ (it's a per-call flag). The SQL user is per connection, and the commit author is per process environment.

## 2. What dolt sql-server 2.2.3 offers (all MEASURED on the scratch server)

- **Grants:**
  - Database- and table-level grants are enforced. An events-only user is refused on `issues` (`run2.txt` T5).
  - Grants are checked per statement. `DROP USER` and `REVOKE` cut a live connection at its next statement (T4, T4b).
- **DOLT_COMMIT** is denied with SELECT, INSERT, UPDATE and DELETE on the database, and allowed with ALL (P1); the minimal set is unmeasured. A bd writer account therefore needs broad database rights.
- **Roles:** `CREATE ROLE` and `GRANT role TO user` work, and role privileges apply with no `SET ROLE`. `SET ROLE ALL` and `SET DEFAULT ROLE` are syntax errors (T6).
- **Delegated creation works but has no fence.** A non-root user holding `CREATE USER ON *.*` plus database rights `WITH GRANT OPTION`:
  - CAN create users and grant within what it holds;
  - CANNOT grant DELETE it lacks, `ALL ON *.*` or SUPER, nor read `mysql.user`;
  - but ALSO CAN:
    - `DROP USER` an account it did not create;
    - `ALTER USER` root's password, which locks root out (T2);
    - switch root to JWT under a subject it controls, then log in as root (`run5.txt` Q3);
    - re-point an existing JWT user to its own subject (Q3).

  **A delegated minter is root-equivalent.**
- **JWT auth (`authentication_dolt_jwt`):**
  - **Setup:** a `jwks:` entry in the server config (`name`, `location_url`, `claims`); users are created as `IDENTIFIED WITH authentication_dolt_jwt AS 'jwks=<name>,sub=..,iss=..,aud=..'`.
  - **Transport:**
    - `location_url` must be http(s); `file://` fails with `Non-2xx status code from JWKS fetch` (`run3.txt`).
    - Login needs TLS (`Cannot use clear text authentication over non-SSL connections`).
  - **Validation:** a valid token logs in. Expired, wrong `sub`, wrong `aud`, wrong `iss` and a rogue signing key are each refused with a named reason (`run4.txt` J1-J6).
  - **No replay guard:** one token logged in three times (J7).
  - **`exp` bounds login only:** a connection opened with a 5-second token kept writing and committing after it expired (J8).
  - **The JWKS is fetched at every login.** With the endpoint down, a new login is refused while an established connection keeps working (`run5.txt` Q4).
  - **Subject-free user:** a JWT user created with no `sub` accepts any subject and records all of them as one SQL user. The subject appears only in the server log (`Authenticating with JWT: sub: ...`); `jti` logs empty despite `fields_to_log` (Q1).
  - **Authorship:** a commit without `--author` by a JWT user records that user as author and committer (J8).
- **The bd 1.1.2 client cannot log in as a JWT user** (MEASURED with go-sql-driver v1.9.3 and bd's DSN flags).
  - The error: `this user requires clear text authentication. If you still want to use it, please add 'allowCleartextPasswords=1' to your DSN`.
  - With that flag it logs in. bd's DSN never sets it (`internal/storage/doltutil/dsn.go:38-58`); TLS is supported (`BEADS_DOLT_SERVER_TLS`).

## 3. Could marvel mint and revoke accounts against kinu's server? Three paths

- **A. marvel holds a delegated CREATE USER credential and mints password users.**
  - Section 2 shows that credential is root-equivalent, and it is bearer authority at a server marvel neither runs nor can re-mint for itself. By ADR-009's test that is custody, so not against kinu's operator-run server.
  - Against a marvel-managed bd server, it is the "root credential in a store marvel can revoke and re-mint" case that `question-bd-managed-extended-service` already scopes.
- **B. JWT issuance.**
  - Once, someone with admin rights creates one JWT user per slot (`sub` = the slot id) with grants on the bd database. Each marvel signs short-lived tokens with its own key and publishes the JWKS.
  - No delegated CREATE USER is needed. marvel can rotate its key without a human at another party's console, which reads as issuance under ADR-009.
  - **Ruled (i):** the signing key is long-lived authority at the bd server for every user that trusts it, so it is treated as custody-adjacent and nothing is built on it until ruled otherwise.
  - **Revocation, three levers:**
    - token expiry, which blocks new logins only;
    - `DROP USER` or `REVOKE`, which cuts the next statement;
    - removing the key from the JWKS, which blocks new logins.
  - **Costs:**
    - TLS on the bd server, which is plaintext on the LAN today (aae-orc-4ascm);
    - a JWKS endpoint that every login depends on;
    - an upstream bd change to set `allowCleartextPasswords`;
    - bd committing with the SQL user as author (it overwrites it today);
    - broad database grants for DOLT_COMMIT.
- **C. No new dolt accounts.**
  - Keep one SQL user per cluster, as skippy has today, and have marvel stamp `BEADS_ACTOR` from the slot (aae-orc-ep8n3).
  - This fixes consistency (aae-orc-64kn2), not authenticity: the server still accepts any actor string. It is cheapest, and half of it is built (marvel stamps `BEADS_ACTOR` at spawn).

## 4. Several clusters on one server, and respawn

- **Issuers stay separate:** the server takes a list of `jwks` entries, and a user binds one `iss`. A token from the wrong issuer or key is refused (J5, J6), so mokuzai cannot log in as a kinu slot user.
- **The cost is availability coupling:** every login on kinu fetches the issuer's JWKS, so a corporate or mokuzai JWKS outage blocks only that cluster's new logins.
- **Respawn:**
  - **Per-instance accounts** would orphan claims and attribution on every shift. That is the 64kn2 failure moved from the actor string into the account.
  - **Keyed on the slot**, as R-151 and ep8n3 propose, a successor gets a fresh token for the same `sub` and inherits the claims; the instance can still ride in the actor string.
  - **Ruled (ii):** the slot, not the session. The instance still rides in the actor string.

## Decision as ruled

Path C now: per-cluster SQL users, as finding-149 did for skippy, and `BEADS_ACTOR` keyed off the slot (aae-orc-ep8n3). Path B waits for TLS on the bd server (aae-orc-4ascm).

Not part of the ruling: section 3.A finds that path A would be custody against kinu's server (ADR-009), because a delegated CREATE USER credential is root-equivalent. That is this finding's analysis; the operator ruled on (i) to (iii) only.

Two upstream bd items from section 2 go to the beads project:
- the cleartext DSN setting is already in flight upstream (gastownhall/beads#6667, PR #6668);
- the commit author ignores the SQL user (filed separately).

## Not a live exposure

On kinu today, no account other than root holds CREATE USER (`SHOW GRANTS` read-only: the bd account has database-level rights plus global SHOW DATABASES and SUPER). The root-equivalence in section 2 applies only if someone creates a delegated minter.

## Edges

- answers: `question-bd-managed-extended-service` (d), and (e) in part
- informs:
  - `question-marvel-identity-authority-topology` (orc, RELOCATION-PENDING): JWT as the bd projection of a marvel-minted principal;
  - aae-orc-ep8n3, aae-orc-64kn2, aae-orc-cwhwt, aae-orc-4ascm
- extends: aae-orc finding-149 (its attribution was the actor string)
- supports (kos schema has no relates type): `question-credential-custody-beyond-nkey` (new in marvel#528); the credential-delivery finding draft (`.session/research/2026-10-04-bd-credential-via-marvel/`)

Data: `.session/research/2026-10-03-bd-account-minting/data/` (probe.py, probe2.py and probe3.py with run2-5.txt; jwtprobe-main.go; the scratch config with paths elided).
