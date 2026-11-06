# A SERVICE column: the account key in words

**Status:** proposed design, view only.

- **Author:** the architect seat, team arcaven.
- **Checked against:** marvel `origin/main` a2f298b4.
- **Valid until:** 2026-11-13, or until the first PR for it opens, whichever comes first. The author re-checks it then.

## 0. Why

ACCT in `marvel get sessions` prints a six-hex fingerprint. It reads like an identity, and it is not one.

- Three different Enterprise logins on kinu, corporate and mokuzai all print `e5d543`, because the key holds no host and each uses its harness's default home.
- On kinu on 2026-11-06 (`marvel get sessions --columns runtime,acct`), 34 claude seats print `e5d543`, one prints `35f9c2`, and two codex seats print `cc8e81`.
- Nothing on the row says what any of those means.

The words already exist. `marvel get budgets` prints each account key spelled out (`claude default ambient default-home`). One column that spells the key on each row, plus an operator-declared login label, answers "which service is this seat on?" with no new daemon state that holds or reads a credential.

## 1. The premises, verified

| Claim | Where |
|---|---|
| The account key is harness, resolved backend, credential source and config home | `internal/api/account.go:43-48` (`AccountKey`) |
| A claude seat's home falls back to its `CLAUDE_CONFIG_DIR` when no account home is recorded (#1297) | `internal/api/account.go:65-82`, `ClaudeConfigDirEnv` at `:90-95` |
| ACCT is a SHA-256 of those four fields, cut to 3 bytes, with the home left unfolded | `cmd/marvel/acct.go:37-58` |
| `get budgets` already spells the key in words, one row per account | `internal/daemon/account_rows.go:60-73` (`k.String()`); live on kinu: `claude default ambient <home>/.claude-acct/<name>`, `claude default ambient default-home`, `codex default ambient default-home` |
| Backend values: `default`, `subscription` (intent only), `bedrock`, `anthropic-aws`, `vertex`, `foundry`, `mantle`, `gateway`, `anthropic-google-cloud`, `custom` | `internal/api/backend.go:199-226` |
| A subscription resolves to `default`; only the intent field tells it apart | `internal/api/backend.go:199-207`; `BackendIntended`, `internal/api/types.go:520` |
| Credential sources: `ambient`, `api-key-helper`, `aws-credential-export`, `aws-auth-refresh`, `aws-profile`, and empty for a session never classified | `internal/api/backend_credential.go:19-51` |
| marvel refuses `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN` and the AWS secrets in any env or overlay it builds, so "oauth-token" and "api-key" are not credential sources marvel can declare | `internal/api/backend.go:80-103` (`backendBearerEnv`) |
| An `ambient` pane authenticates with whatever it inherits, so it can still carry one of those keys from the daemon's environment | `internal/api/backend_credential.go:47-50` |
| Columns are one registry entry each; opt-in columns are listed in `optInColumns` | `cmd/marvel/columns.go:29-59` |
| A fan-out read tags each row with its cluster | `internal/api/types.go:288` (`Cluster`, set by the client, not sent on the wire); `cmd/marvel/fanout.go:207-209` (`rows[i].Cluster = t.Name`) |
| The rollup design counts only the cells a row shows | `docs/design/cluster-rollup-header.md` section 2.1 (#1302) |

### 1.1 Measured: `claude auth status`

Claude Code 2.1.318, on kinu, against this seat's own login. Only field names and the type fields were printed. No email, organization name, org id or credential was read or recorded.

- **Time:** 0.15 to 0.20 s per call, with a peak RSS of 155 MB (`/usr/bin/time -l`, three runs).
- **Network:** none. Under a `sandbox-exec` profile that denies network except localhost, it returned the same answer, rc 0. The profile's control refused a connection to 1.1.1.1:443 with `PermissionError: [Errno 1] Operation not permitted`.
- **Writes:** none outside TMPDIR. Under a profile that denies writes outside scratch and `/dev`, it returned the same answer. The control write outside scratch was refused with `Operation not permitted`; the write inside scratch succeeded.
- **Spawns:** yes. It runs `/usr/bin/security`, the macOS keychain reader:
  - with `process-fork` denied, it reports `loggedIn: false, authMethod: none`;
  - with only `/usr/bin/security` denied, the answer is the same (control: `security` under that profile exits 71).
  - So it reads the stored login credential to answer. The value stays inside the harness and is never printed.
- **Output fields:** `loggedIn`, `authMethod` (`claude.ai`), `apiProvider` (`firstParty`), `subscriptionType` (`enterprise`), `loginExpiresAt`, `configDirectory`, `projectsDirectory`, plus `email`, `orgId` and `orgName`, which are personal or organization data. The `--text` form prints login method, organization, email and sign-in end.

The login type is learnable this way, but only by having the harness read its credential for each seat, and the answer arrives alongside personal data marvel would have to drop. That is why the design does not lean on it (section 3.3).

## 2. The column

**`service`, header SERVICE, opt-in** (in `optInColumns`, not in `wide`). It is one cell per row, in words:

```
<harness> <backend> <source> <home>[ <label>]
```

Examples, for illustration only:

```
SERVICE
claude default ambient default-home enterprise
claude default ambient alt-ultra subscription
claude bedrock aws-credential-export default-home
codex default ambient default-home
```

The fields:
- **harness:** `Runtime.Name`.
- **backend:** `BackendResolved`, except `subscription` when `BackendIntended` is `subscription` and the resolved value is `default`. That is the one place the record already knows a subscription.
  - When intended and resolved disagree otherwise, the cell prints `resolved!intended` (for example `default!bedrock`). The mismatch is shown, not hidden; `marvel backend verify` stays the place to explain it.
  - A session never classified prints `-`.
- **source:** `BackendCredentialSource`, or `-` when empty.
- **home:** the config home, from the same rule as ACCT:
  - the recorded account home, else the claude seat's `CLAUDE_CONFIG_DIR`, else `default-home`;
  - printed as its last path element, so the cell stays short. The full path stays in `get budgets` and `describe`.
  - A last element shared by two homes on one cluster prints the full path in both rows.
- **label:** the declared login label (section 3), when one is declared; otherwise nothing.

**ACCT stays.** It still answers "do these rows share an account" in six characters. SERVICE answers "what is that account". The help for both says so in one sentence each.

### 2.1 ACCT across clusters

The fingerprint holds no cluster, so the same hash on two clusters is two accounts on two hosts. In a fan-out read (`--cluster a,b`, `--all-clusters`), the proposal folds the row's `Cluster` into the ACCT fingerprint for display only. The daemon key, the account rows and the limit readings do not change, because each daemon sees only its own sessions. A single-cluster read keeps today's hash.

## 3. The login type

### 3.1 Declared, not probed

The login type is declared by the operator. It is never read from a credential or from `~/.claude.json`.
- **Field:** an optional `account_label` string on the role runtime (`[team.role.runtime]`), next to the `CLAUDE_CONFIG_DIR` that names the login.
- **Default:** an optional team-level `account_label` covers roles that name none.
- **Form:** free text, one word by convention (`enterprise`, `subscription`, `console`, or a name the operator picks).
- **Display:** the label is printed as written and never parsed.
- **Storage:** recorded on the session at spawn as status, like the backend fields (`toml:"-"` on the session; `json` to bolt). An adopted session keeps the label it launched with.

A declared label can be wrong, as a declared backend can. The help says "declared" in plain words. Checking a label against the login is the probe in 3.3, which is deferred.

### 3.2 Inherited bearer keys: names, never values

`ambient` hides the case the ask names: a pane that inherits `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` or `CLAUDE_CODE_OAUTH_TOKEN` from the daemon's environment.
- **Record:** at spawn, for an `ambient` session only, marvel records which of those key **names** are present and non-empty in the environment the pane inherits. The value is never stored, logged or compared, only tested for presence.
- **Display:** the cell prints the source as `ambient:api-key`, `ambient:auth-token` or `ambient:oauth-token`, in that precedence (the order is this design's choice, not a claim about how Claude Code ranks them).
- **Not in the account key**, so ACCT and the account rows do not move. Two seats on one host and one home, one on an inherited API key and one on its login, are then one key for two accounts. That key gap predates this design, and fixing it is left out here (section 6).

### 3.3 The `auth status` probe: deferred

Running `claude auth status --json` once per seat at spawn would observe `authMethod`, `apiProvider` and `subscriptionType`. It is left out of the first PR:
- it makes marvel cause a credential read (the keychain, section 1.1) for every seat it launches;
- it returns email and organization data that marvel would have to drop before anything is stored;
- it is claude only; codex and goose would need their own.

If the operator wants it later, the shape is: opt-in per cluster, at spawn only (never on a `get` path), under the seat's own `CLAUDE_CONFIG_DIR`, with a 2 s bound, keeping only those three fields, and shown as `observed` beside a declared label when the two differ.

## 4. Every cell is diagnostic

Nothing reads SERVICE to gate a spawn, a scale or a merge. No colour or mark acts on it. It is a view (SOUL section 8, ADR-007).

## 5. The plan

**One PR** for the column:
- **Files:** `cmd/marvel` (`service.go` new, `columns.go`, `main.go`, `fanout.go`); `internal/api/types.go` for the label and the inherited-key names on the session; the spawn path that already sets `BackendCredentialSource`; and the manifest schema for `account_label`.
- **Renderer:** `serviceCell(s api.Session, homes map[string]int) string` in `cmd/marvel/service.go`, where `homes` counts last path elements per cluster for the collision rule.
- **Help text:** one paragraph for `get` naming the fields, "declared" for the label, and "diagnostic only".

Tests:
- a subscription-intended, default-resolved row prints `subscription`;
- an intent mismatch prints `resolved!intended`;
- a never-classified row prints `-` for backend and source;
- home prints as the last element, and as the full path when two homes on the cluster share it;
- a declared label prints as written, and no label prints nothing;
- an ambient row with `ANTHROPIC_API_KEY` present prints `ambient:api-key`, and the session record holds the name, not the value;
- ACCT is unchanged on a single-cluster read and differs per cluster on a fan-out read for the same key;
- `-o json` carries the new fields, and the default table is byte-identical to before.

Rule d: the builder runs the shared-file check against open PRs first. A cluster-totals PR from #1302 touches `main.go` and `fanout.go`; if one is open, this PR chains on it.

## 6. Not in this design

- **The `auth status` probe** (3.3).
- **Putting the inherited-key names into the account key.** This would split one key into two where an inherited bearer differs. It changes ACCT and the limit readings, so it needs its own design.
- **Codex and goose login types.** Codex records its home today, and its label is declared the same way.
- **Any total over SERVICE.** The #1302 totals line counts sessions, not services.

## 7. Decisions for the operator

Each carries the author's recommended default. Valid until 2026-11-13.

1. **Name and scope:** an opt-in `service` column in words, with ACCT kept. Default: yes.
2. **Login type from a declared `account_label`, not a probe.** Default: yes.
3. **The `auth status` probe at spawn.** Default: not now (3.3).
4. **Inherited bearer key names recorded at spawn, shown as `ambient:<kind>`, kept out of the account key.** Default: yes.
5. **The cluster folded into the ACCT fingerprint on fan-out reads only.** Default: yes.
