# Env-borne permissions: marvel brokers them, a manifest env line does not

- **Status:** proposed, 2026-11-20. Design only, no code. The tickets in section 9 are not filed.
- **Pins:** marvel main 41c8dff1. Nothing was installed or run to write this; every row of section 2 is read from marvel's own source, and what no source settles is in section 10.
- **Ticket:** aae-orc-iyun3, the env-lever half.

## 1. Why, and the ruling

Some harnesses take their permission posture from an environment variable, so a role's `runtime.env` can switch a seat to allow-everything with one line and no review: nothing at apply says the line is a permission change. The operator's ruling on the env lever read at first as "refuse on scheduled unsandboxed launches only". The operator then widened it, in these words:

> we are reading this too literally. in cases where a harness only or principally accepts it's permissions in env, that env should be managed or brokered by marvel and not blindly or casually injected in manifest or by moment to moment whims

So this design does two things: it refuses the casual form (a permission-bearing key in `runtime.env`), and it names the brokered form (a declared field marvel turns into the env). A scheduled unsandboxed launch stays the strictest case of the same rule, not the only one.

## 2. What counts as an env-borne permission

A key counts when the harness reads its permission posture, or the file that holds it, from that key. The set is a table per adapter in code, not a pattern: a pattern such as "contains PERMISSION" catches names nobody measured and misses the ones that matter. Today's table, from source:

| harness | key | what it does | source |
|---|---|---|---|
| goose | `GOOSE_MODE` | the permission mode; goose's default is `auto`, which allows every tool | `internal/runtime/goose_posture.go:24` |
| opencode | `OPENCODE_PERMISSION` | the permission block | `internal/runtime/opencode_config.go:27` |
| opencode | `OPENCODE_CONFIG_CONTENT` | a whole config, which can carry a permission block | `internal/runtime/opencode_config.go:27` |
| claude | none found | the posture is the `--permission-mode` flag and the projected settings file, both marvel's | `internal/runtime/claude.go` |
| codex | none found | the posture is flags (`-s`, `-a`, `-c`), both marvel's in the codex permissions mapping (open as marvel#1532) | `internal/runtime/codex_permissions.go` on that branch |

A second class moves where a harness reads its config, so it moves the posture without naming one: `CODEX_HOME` and `XDG_CONFIG_HOME` are the two the source already treats as levers or leaves open (`opencode_config.go:21-26` leaves `XDG_CONFIG_HOME` as an open reading; the codex adapter owns `CODEX_HOME` and the private home). They are relocation keys. This design lists them in the table as a separate column of the same refusal, and section 10 says which are measured.

Credentials are a different class and stay where they are. `ANTHROPIC_API_KEY` and its kin are bearer material, refused as literals by `mergeRoleEnv` (`adapter.go:686`, `roleEnvRefused` at `:769`, `api.BackendBearerEnv`) and declared by helper pointer (ADR-009). Nothing here changes that.

## 3. How a role asks, through marvel

A role asks for a posture with a field, never with the env key.

- **claude and codex:** the existing `permissions` field. Marvel maps it to flags; no env is involved.
- **goose:** the existing `runtime.goose.posture`. The adapter writes `GOOSE_MODE` itself and already refuses a role env value that disagrees (`role-env-conflict`, `goose_posture.go:53`).
- **opencode:** marvel writes the permission block itself (`openCodePermissionBlock`) and already owns the two levers; `openCodeRoleEnvConflict` names a role that sets one but `Prepare` does not call it yet (`opencode_config.go:105-107`).

So the brokered form already exists for goose and opencode as adapter-owned output. What is missing is the refusal of the casual form beside it, and one place where the per-adapter table is read. The proposal: extend the `PermissionMapper` interface (added by the codex permissions mapping, marvel#1532, so this waits on that merge) with `EnvLevers() []string`, the keys that adapter owns. The table in section 2 is then what each adapter returns, so adding a harness adds its keys in the same file that maps its flags, and a pin test (like `pinnedRoleEnvProbe` in `role_env_refusal_pin_test.go`) holds the set.

Choice for the operator or director, stated as a recommendation: keep the per-adapter fields (`permissions`, `runtime.goose.posture`) as the ask, and do not invent a generic `env_permissions` block. A generic block would be a second way to say what each field already says.

## 4. Where the value lives

A posture value is not bearer material. By the audience test (`SOUL.md` section 3) it means nothing to any third party: it is a setting that only the harness on this host reads, and marvel can revoke or re-mint it by changing a field. So it needs no operator store and no new at-rest place. It lives where the rest of the role lives, in the manifest and the daemon's store, and reaches the seat as env only at spawn, built by the adapter, in memory. The one file marvel writes for it is the per-seat private config that already exists (opencode's block, goose's private home), mode 0600, swept with the seat.

If a harness ever takes a credential through the same env key as a posture, the credential half follows ADR-009 and its 2026-10-19 amendment: in memory while running, at rest only in the operator's store, brokered by helper pointer. This design adds no case of that today.

## 5. How the casual form is refused at apply

`PermissionEnvRefusals(refuse)` beside `PermissionRefusals` in `internal/api`, with the same shape: an injected predicate (`Manager.RoleEnvLeverRefusal`) resolves the role's adapter and returns, for each `runtime.env` key the adapter owns, a refusal naming the team, the role, the key (never the value) and the field to use instead. Apply runs it in the same pre-flight block as `PermissionRefusals`, so one apply lists every such role and commits nothing.

- **Scope, ruled by director on the operator's note: every role whose adapter owns the key.** A scheduled, unsandboxed launch is not the only case; it is the strictest one of the same rule. The condition that came with the ruling is the managed path in section 5.1: a role that needs a lever on purpose gets a way to set it through marvel, not only a refusal.
- **Per key.** `GOOSE_MODE` and `OPENCODE_PERMISSION` are refused outright: each carries nothing but a posture, and a declared field already says it. `OPENCODE_CONFIG_CONTENT` is not, because it carries a whole config and a live role sets it today (section 6); it follows the staged rule in section 5.1.
- **Equal values.** goose accepts a role env value equal to the declared posture today. Under B the line is refused even when equal, because the field is the ask and a duplicate line is how the two drift.
- **Scheduled roles** keep their existing refusals (`ScheduledLaunchRefusal`, `StoredScheduleRefusal`, `schedule.go:119` and `:134`); this adds the env lever to what they read, so a scheduled role cannot get an unsandboxed allow-all through env.
- **Spawn-time guard.** `mergeRoleEnv` skips an owned key and logs it, as it does for credentials and topology keys, so a role stored before the refusal does not get the lever either.

### 5.1 The managed path for a role that needs a lever on purpose

Refusing the env line is half of "managed or brokered". The other half is a way to ask for the same effect that marvel validates and then sets itself.

**How a role declares the need.** A typed block on the role's runtime, beside `runtime.goose`, one per harness that owns a lever: `runtime.opencode.permission`, holding the permission rules the role wants in the harness's own shape (per tool, allow or deny, with patterns). It is a manifest field, so it is read at apply, stored with the role and visible in `marvel describe`; a raw env line is none of those. A role that needs a non-permission part of a config (`OPENCODE_CONFIG_CONTENT` carries a whole config) does not get it through this block; section 10 lists that as open.

**How marvel sets the value at spawn.** The adapter renders the declared rules into the lever's value itself and merges them with its own floor, in memory, at spawn, and writes nothing else (section 4). For opencode that is `openCodePermissionBlock` extended: opencode reads the last match, so the order is: the floor's base (`"*":"deny"`), then the role's rules, then the floor's protective denies (the `.env` read denies, the `.git` and config edit denies, the private data-home glob) last, so a role's rule cannot loosen them. A role rule that names one of those protected patterns is refused at apply, naming the rule.

**What the managed path does not permit.** `auto` on an unsandboxed headless launch stays refused (`openCodeLaunchRefusal`), whichever way it is asked for. A scheduled role keeps its existing refusals.

**Goose** already has this shape: `runtime.goose.posture` is the declared need and the adapter writes `GOOSE_MODE`. Nothing new is needed there; the env line is refused beside it.

**What a "mail contract" role does today** is not known (section 6). If the opencode comment's mail role needs a lever, it declares `runtime.opencode.permission` with the rules it needs, and the scan below found no live role that would have to.

#### The whole-config key, `OPENCODE_CONFIG_CONTENT`

This key carries a whole opencode config, of which the permission rules are one member among others (model, provider, tools, servers). A live role on another cluster sets it, so refusing the line on day one would break that role. The path is two steps, so the role keeps working and the key still ends up managed.

1. **Transitional rule, at apply.** Marvel reads the value as it is written in the manifest, in memory, never logging or printing it. A value that parses as a JSON object with no permission-bearing member (`permission`, and any agent-level `permission` or `tools` member that opencode reads as one) is accepted, with an advisory naming the team, role and key. A value that does not parse, or that carries such a member, is refused, naming the team, role, key and member, and pointing at `runtime.opencode.permission`. The value is checked by marvel itself, so no operator has to read a value that may hold a provider secret.
2. **The managed form, then the refusal.** A second typed block, `runtime.opencode.config`, holds the non-permission config as a structured map. The adapter renders it and the permission block together into `OPENCODE_CONFIG_CONTENT` at spawn, in memory, so a role never writes the key. Once the live role has moved to it, the refusal of the raw key lands outright, like the other two. Until then the exception stands, and it is stated as an expiry on the recommendation: valid until 2026-12-04, re-checked on that date by the builder seat that wrote this, who then renews it or lands the refusal. Nothing happens on the date by itself.

What the live role's value contains is not known: the scan read key names only. The transitional rule is what decides whether that role applies unchanged (no permission member) or must move first (a permission member).

## 6. What happens to roles that set one today

Stored roles keep spawning. The spawn skips the lever and logs it (the same choice as the codex permissions mapping: an upgrade must not stop a running team), and the role is refused at its next apply, with a message naming the field to move to. Refusing stored roles at rehydrate stays director's decision after a warning period, as the ticket already says.

The live-manifest scan, key names only, was run on 2026-11-20 as a read-only pass through the marvel CLI against one cluster's daemon (`marvel get teams -o json`, piped to a script that prints team, role and key name for the section 2 keys and nothing else; no value was printed, stored or sent). It read the desired state of 4 teams and 25 roles; 18 of those roles set some `runtime.env` key, and none sets a section 2 key. The repository scan is done too: no file in `examples/` or `docs/` sets one. The same script was run on two more clusters by whoever holds them. The scan is not complete: `marvel get teams` has no workspace selector, and the first cluster's read covered only two of its workspaces, the third's one, so a team in any other workspace was not read.

In the workspaces read, the count of live roles that touch a lever is 0 of 25 on the first cluster, 1 of 53 on the second (one opencode reviewer role on another cluster sets `OPENCODE_CONFIG_CONTENT`) and 0 of 22 on the third, which is partly scanned. So refusing `GOOSE_MODE` and `OPENCODE_PERMISSION` changes no live manifest read so far, and the whole-config key needs the staged rule in section 5.1. One lead was checked: the opencode source comment calls a role env that names a lever "today's documented mail contract" (`opencode_config.go:105-107`), so a mail role might set one on purpose. The one live role found is an opencode reviewer, not a mail role; whether a mail role on the unscanned cluster sets one is not known. Either would move to the managed path in section 5.1.

| scan | result |
|---|---|
| repository (`examples/`, `docs/`) | no role sets a table key |
| live manifests, first cluster, 2026-11-20, in the workspaces read (two of the cluster's workspaces) | 4 teams, 25 roles, 18 with env, 0 set a table key |
| live manifests, second cluster, 2026-11-20 | 14 teams, 53 roles, 50 with env, 1 sets a table key: one opencode reviewer role sets `OPENCODE_CONFIG_CONTENT` |
| live manifests, third cluster, 2026-11-20, in the workspace read (one) | 2 teams, 22 roles, 22 with env, 0 set a table key; partly scanned |

## 7. What stays out of scope

- Credential env keys (ADR-009), the topology keys and the identity credentials: already refused.
- Backend selectors (`CLAUDE_CODE_USE_*`): they are Layer B, with their own overlay and `Backend` label.
- Any change to what a posture value means for a harness.

## 8. Independence

Marvel owns the refusal and the table. No harness changes, no director change, no other component needs this to work. A host with no goose or opencode role sees nothing.

## 9. Tickets, flat, in build order, red first (not filed)

1. `EnvLevers()` on the adapters that own one (goose, opencode), with a pin test of the set. Red: the set is empty, the test names `GOOSE_MODE`, `OPENCODE_PERMISSION`, `OPENCODE_CONFIG_CONTENT`.
2. `PermissionEnvRefusals` and `Manager.RoleEnvLeverRefusal`, wired in apply after `PermissionRefusals`. Red: apply accepts `GOOSE_MODE` in a goose role's env.
3. `mergeRoleEnv` skips an owned key and logs it. Red: a stored role's spawn env carries the lever.
4. Opencode `Prepare` calls `openCodeRoleEnvConflict` (the half the source comment says is not wired).
5. The scheduled-role read: `StoredScheduleRefusal` and `validateSchedule` include the lever. Red: a scheduled goose role with `GOOSE_MODE=auto` in env applies.
6. `runtime.opencode.permission`: parse, validate against the floor, render into the lever at spawn. Red: a role that declares a rule loosening the secrets read deny applies.
7. The transitional rule for `OPENCODE_CONFIG_CONTENT`: apply parses the value in memory and refuses a permission-bearing member. Red: a value with a `permission` member applies.
8. `runtime.opencode.config`: the typed non-permission block, rendered with the permission block into the key at spawn.
9. The refusal of the raw `OPENCODE_CONFIG_CONTENT` key, after the live role has moved (not before).
10. The user guide names the refused keys, the field to use, and the managed path.

## 10. Not measured, and open

- **Live scan, partial:** three clusters read, but only the workspaces `marvel get teams` returned (two on the first, one on the third). It has no workspace selector, so workspaces it did not return are unread. A selector, or a read per workspace, would complete it.
- **Relocation keys:** `CODEX_HOME` and `XDG_CONFIG_HOME` are read from source comments only. Whether either moves a posture on the pinned harness versions is not measured here; a probe with no model call can settle it.
- **claude and codex env levers:** none were found in marvel's source. That is a source reading, not a measurement of the harnesses; neither harness's documentation was read for an undocumented env key.
- **Whether a generic `env_permissions` block is wanted** (section 3 recommends against it).
- **What the live role's `OPENCODE_CONFIG_CONTENT` value holds:** unread by design (the scan reads key names only). The transitional rule in section 5.1 settles it at apply without a person reading the value.
- **Which opencode config members read as permission-bearing:** `permission` is known from `openCodePermissionBlock`; the agent-level and `tools` members are named from opencode's config shape and not measured here.
- **The shape of `runtime.opencode.permission`:** the rule syntax is opencode's own and was read only from `openCodePermissionBlock`; its full grammar is not measured here.
