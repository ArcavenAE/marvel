# Global role declaration: every supervisor-type role gets the global tier

Design for review. No code lands until this doc is reviewed.

- Author: the arcaven team's architect seat.
- Issue: #518. Ruling, the operator, 2026-10-03: "all supervisors must have
  global tier comms", and on the follow-up question, "yes": that covers
  research-supervisor and every supervisor-type role, not only a role named
  `supervisor`.
- Constraint from director: the fix must not hard-code a second name in
  `GlobalAddressRoles`.
- Checked against marvel `origin/main` 889e49a and director `origin/main`
  f7334ae.

## 1. The problem, verified

| Premise | Where | Result |
|---|---|---|
| The global tier is keyed by one role name | `internal/config/config.go:459` | `GlobalAddressRoles = []string{"supervisor"}` |
| A test pins that list exactly | `internal/bus/global_roles_test.go:22-27` | fails unless the list is `[supervisor]`, and asserts `research-supervisor` does not count |
| The team user carries the global grants only when the team has a listed role | `internal/bus/declared.go:217` (`hasGlobal := t.Supervisor && s.HubURL != ""`); `t.Supervisor` is `hasSupervisorRole(t)` (`internal/bus/manager.go:363`, `:487-494`) | a research-supervisor in a team with no `supervisor` gets no global grant |
| Stage 3 of per-role users removes the grants from the team user | `declared.go:219-223` comment; `docs/design/per-role-broker-users.md` section 7, phase 2 | after it, a research-supervisor has no global grant in any team |
| The per-role user is named by a fixed suffix, but looked up by role name | the renderer mints only `<team>.supervisor` (`declared.go:85`, `manager.go:365-372`); `Credential` looks up `team + "." + role` (`manager.go:263-272`) | adding `research-supervisor` to the list would look up `<team>.research-supervisor`, never find it, and fall back to the team user forever |
| director's launcher already treats both as one global role | `sim/twin/cast-launch.sh:126-130` (director f7334ae): `supervisor\|research-supervisor) GROLE=supervisor` | the global role WORD is `supervisor` for both (R-94 as amended, director#77) |
| The launcher keys off the wardrobe role, not the manifest role name | `cast-launch.sh:58-66`: a manifest role name maps to a wardrobe role id (`marvel-builder` to `builder`, and so on) | marvel cannot learn the wardrobe role without that table |
| A second hard-coded name exists | `internal/team/controller.go:1913-1923`, `shiftOrder` puts the role named `supervisor` last in a shift | see section 7 |

The fourth row is the reason a list edit is not a fix. The issue's own
question ("could `GlobalAddressRoles` include `research-supervisor`") would
render the grants on the team user (stage 2) and then fail at stage 3,
because no `<team>.research-supervisor` user is ever minted.

`docs/design/per-role-broker-users.md:33` records "`research-supervisor` does
not count, which is correct under R-94". That predates the R-94 amendment
(director#77, 2026-09-24) and the 2026-10-03 ruling; this design supersedes
that line.

## 2. The rule

**A role holds a global address because its manifest says so.** The
manifest names the global role (the address word) a role holds, and marvel
renders grants, picks the broker user, and sets the seat's environment from
that one declaration. The role's name stops mattering, except as the default
for a manifest that declares nothing.

## 3. The declaration

A new optional role field:

```toml
[[team.role]]
name = "research-supervisor"
replicas = 1
global_role = "supervisor"
```

- **Values:** `"supervisor"` or `"none"`. Anything else is refused at apply,
  with the allowed set in the error. `director` is not a manifest value: the
  director seat is rendered from the client config's `seat:` block, not from
  a team role.
- **The word is the address word, not a role class.** It is the same token
  director's launcher derives (`GROLE`) and exports as `DIRECTOR_GLOBAL_ROLE`,
  and the same token in the inbox subject `global.<cluster>.supervisor.inbox`.
  Naming the address keeps marvel out of the business of classifying roles.
- **Stored:** `api.Role` and `ManifestRole` gain `GlobalRole string`, copied
  at apply like `Persona` and `Identity`.

### Default for a manifest that declares nothing

An absent `global_role` resolves by name, through the existing list, renamed
for what it now is:

- `config.GlobalAddressRoles` becomes `config.DefaultGlobalRoleByName`, still
  `{"supervisor": "supervisor"}`, read only when `global_role` is absent.
- So an old manifest behaves exactly as today: a role named `supervisor` is
  global, and a role named `research-supervisor` is NOT until its manifest
  declares it. Nothing widens silently at upgrade.
- `global_role = "none"` lets a role named `supervisor` opt out.

There is one resolver, `api.Role.ResolvedGlobalRole() string`, and every
caller below reads it. No caller compares a role name.

## 4. Broker users and grants

**The per-role user is keyed by the global role, not by the role name.** One
user per team and global role, `<team>.supervisor`, presented by every seat
whose role resolves to `supervisor`. This matches the address model (one
supervisor inbox per cluster, which every supervisor-type seat reads) and
removes the lookup mismatch in section 1.

| Code | Today | After |
|---|---|---|
| `hasSupervisorRole(t)` (`manager.go:487-494`) | a role name is in `GlobalAddressRoles` | any role's `ResolvedGlobalRole() == "supervisor"` |
| renderer, `TeamUser.Supervisor` (`manager.go:363`) | from `hasSupervisorRole` | unchanged call, new meaning |
| `declared.go:217-251` | grants on `<team>` and a `<team>.supervisor` user when `t.Supervisor` | unchanged |
| `Credential(team, role)` (`manager.go:262-279`) | `<team>.<role>` if the role name is listed | `<team>.<resolved global role>` if it resolves to one, else the team user |

`Credential` needs the role's resolved global role, not its name. The
signature changes to `Credential(team string, globalRole string)`. The one
caller, `internal/session/manager.go:912`, passes the role's
`ResolvedGlobalRole()` from the stored team spec. The `BusEnv` interface
(`session/manager.go:1654`) changes with it.

**Option, not the default:** a user per role (`<team>.research-supervisor`
beside `<team>.supervisor`) with identical grants. It gives each role its own
revocation and audit line, at the price of one more password per role, and a
second place where the renderer and `Credential` must agree on a name. The
default is one user per global role, since both roles hold one address. This
is ruling 1.

## 5. What per-role-users stage 3 does to it

Stage 3 (phase 2 in `per-role-broker-users.md` section 7) strips the global
grants from `<team>`. After this change:

- Every seat whose role resolves to `supervisor`, research-supervisor
  included, connects as `<team>.supervisor` once the broker accepts it, so
  stage 3 removes nothing it uses.
- The rotation step before stage 3 must rotate research-supervisor seats as
  well as supervisors, and the observational check (`/connz?auth=true`, the
  same design's section 7, step 3) must look for any global-role seat still connected
  as `<team>`, not only a session named for the supervisor.
- **This change must land, and research-supervisors must rotate onto the
  dotted user, before stage 3 ships.** Otherwise stage 3 cuts them off, which
  is the failure #518 names. The stage 3 ticket gets a `blocks` edge from this
  one.

A research-supervisor in a team with no `supervisor` role gains the global
grants at the first reload after its manifest declares `global_role`. That is
the intended widening, and it happens only by declaration.

## 6. Not a second source of truth with the launcher

There are two decisions about one fact, and the design keeps them from
drifting rather than pretending there is one:

- **marvel decides grants** from the manifest declaration. It never reads
  wardrobe: marvel stays independent of the role library (SOUL section 2),
  and it could not do it anyway, because the manifest role name is not the
  wardrobe role id (`cast-launch.sh:58-66` maps between them).
- **The launcher decides the seat's global address** from the cast
  (`cast-launch.sh:119-130`, "derived from the cast, never read from the
  environment").

They are tied by a check that already exists. marvel exports
`DIRECTOR_GLOBAL_ROLE=<resolved>` into every seat whose role resolves to a
global role, through the adapter's base env (`internal/runtime/adapter.go`,
beside `DIRECTOR_TEAM`), and leaves it unset otherwise. The launcher already
refuses a `DIRECTOR_GLOBAL_ROLE` that contradicts the cast
(`cast-launch.sh:143-145`), before the harness starts. So a manifest that
declares a role global, when the cast says it is not, fails at launch,
loudly, and marvel records a failed session.

One direction is not covered today: the cast says global and the manifest
declares nothing. The env is unset, the launcher derives `supervisor`, and
the seat connects as the team user. Before stage 3 that still works in a team
that also has a `supervisor`; after stage 3 the bus pre-flight
(`cast-launch.sh:290-292`) fails, which is loud. Closing it earlier needs one
line in director: refuse when the cast derives a global role, marvel set a
bus user, and `DIRECTOR_GLOBAL_ROLE` is absent. That is director's change to
make; this design proposes it and does not depend on it (ruling 2).

## 7. The other hard-coded name

`shiftOrder` (`internal/team/controller.go:1913-1923`) shifts the role named
`supervisor` last, so the team keeps its coordinator while workers turn
over. It is not a grant, and the ruling was about comms, so changing it is
not required for #518. Reading `ResolvedGlobalRole()` there instead would
also shift a research-supervisor last. The default is to leave it as is and
file it separately (ruling 3).

## 8. Tests (red first on 889e49a)

The pin at `global_roles_test.go:22-27` is replaced, not loosened:

1. **The default holds for an undeclared manifest.** A role named
   `supervisor` with no `global_role` resolves to `supervisor`, and a role
   named `research-supervisor` with no `global_role` resolves to none. This
   keeps the old test's negative half, so nothing widens by name.
2. **A declaration grants.** `research-supervisor` with
   `global_role = "supervisor"` in a team with no `supervisor` role: the team
   is `Supervisor`, the renderer emits `<team>.supervisor`, and `Credential`
   returns it once confirmed.
3. **Opt out.** A role named `supervisor` with `global_role = "none"` gets no
   grant and the team user.
4. **The renderer and Credential agree** for every role in a table of
   manifests: whatever user `Credential` returns, the declared principals
   contain it. This is the test that would have caught the name mismatch in
   section 1.
5. **Apply refuses a bad value.** `global_role = "director"` and any other
   word is refused at apply, with the allowed set named.
6. **The env.** A global-role seat's environment carries
   `DIRECTOR_GLOBAL_ROLE=supervisor`; a worker's does not.
7. **The list is not a grant source any more.** Setting
   `DefaultGlobalRoleByName` to another name changes only undeclared roles; a
   declared role ignores it. This replaces the old test's "the check reads the
   list" assertion.
8. **Persistence.** `GlobalRole` survives a daemon restart (bolt rehydrate)
   on a stored team.

## 9. Edits, in order (none made by this PR)

1. `api.Role` / `ManifestRole` gain `GlobalRole`, with validation and
   `ResolvedGlobalRole()`; the default map replaces `GlobalAddressRoles`.
   Tests 1, 3, 5, 7, 8.
2. `hasSupervisorRole` and `Credential` read the resolver; `Credential`'s
   signature and its caller change. Tests 2 and 4.
3. The adapter base env exports `DIRECTOR_GLOBAL_ROLE`. Test 6.
4. Fleet manifests declare `global_role = "supervisor"` on every
   research-supervisor role, then those seats rotate onto
   `<team>.supervisor`. That is an operator step, run by director.
5. `per-role-broker-users.md` is updated: line 33 is superseded, and the
   stage 3 check covers every global-role seat.

Steps 1 to 3 can be one builder PR. Step 4 must precede per-role-users
stage 3.

## 10. Rulings needed

1. **One user per global role, or one per role** (section 4). Default: one
   per global role, `<team>.supervisor`.
2. **The launcher's missing-env refusal** (section 6): propose it to
   director, or accept that the gap closes only at stage 3. Default: propose
   it as a director issue; this design does not wait on it.
3. **`shiftOrder`** (section 7): leave it, or read the resolver. Default:
   leave it and file it separately.

## 11. Rejected

| Option | Why it was rejected |
|---|---|
| Add `research-supervisor` to `GlobalAddressRoles` | director's constraint; also broken: `Credential` would look for `<team>.research-supervisor`, which the renderer never mints, so stage 3 cuts it off anyway |
| Match role names ending in `supervisor` | convention as code: a role named `notes-supervisor` would gain global reach by its name |
| marvel reads the wardrobe role's class | marvel would depend on the role library, and the manifest role name is not the wardrobe role id, so marvel would need the launcher's mapping table as well |
| The launcher reads marvel's env instead of the cast | director's launcher derives the role from the cast on purpose (`cast-launch.sh:119-121`); changing that is director's design, not marvel's |
| A boolean `global = true` | the address word is what both sides exchange (`DIRECTOR_GLOBAL_ROLE`); a boolean would hard-code `supervisor` again, one level down |
