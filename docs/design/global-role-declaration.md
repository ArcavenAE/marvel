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
for a manifest that declares nothing. A declaration widens a role only if the
cluster's own config admits that role name as well (section 6): one wrong
line in a manifest cannot give a worker the global tier.

## 3. The declaration

A new optional role field:

```toml
[[team.role]]
name = "research-supervisor"
replicas = 1
global_role = "supervisor"
```

- **Values:** `"supervisor"` or `"none"`. Anything else is refused at apply,
  with the allowed set in the error. An empty string is the same as absent
  (the field is a Go string, so `global_role = ""` and no field cannot be told
  apart); the explicit opt-out is `"none"`. `director` is not a manifest value: the
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

There is one resolver, and admission is part of it (section 6):
`ResolvedGlobalRole(role api.Role, admitted []string) string`. It returns the
declared or default global role only when the role's name is admitted, and
none otherwise. Every caller below reads it with the cluster's current
admitted set. No caller compares a role name, and no caller reads the stored
declaration directly.

## 4. Broker users and grants

**One broker user per global role-holding role, named `<team>.<role>`.** The
renderer mints a user for each role whose `ResolvedGlobalRole()` is
`supervisor`, with the grants `<team>.supervisor` carries today. For the role
named `supervisor` this is the same user, by the same name, so nothing that
runs today changes. A research-supervisor gets `<team>.research-supervisor`.

| Code | Today | After |
|---|---|---|
| `hasSupervisorRole(t)` (`manager.go:487-494`) | a role name is in `GlobalAddressRoles` | any role's `ResolvedGlobalRole() == "supervisor"` |
| renderer, minting (`manager.go:363-379`) | one `<team>.supervisor` user when `hasSupervisorRole` | one `<team>.<role>` user per role that resolves to `supervisor` |
| `declared.go:217-251` | grants on `<team>`, and one `<team>.supervisor` principal | grants on `<team>` (until stage 3), and one principal per such role, same grants |
| `Credential(team, role)` (`manager.go:262-279`) | `<team>.<role>` if the role name is listed | `<team>.<role>` if the role resolves to a global role, else the team user |

`Credential` keeps its signature: it already looks up `team + "." + role`,
which is now the name the renderer mints, so the section 1 mismatch closes
without changing the caller. It needs the role's resolved global role, which
it reads from the stored team spec through the resolver instead of the name
list. `SupervisorUserSuffix` (`declared.go:85`) goes away, since the suffix is
now the role name. The user name is `<team>.<role>`, and both parts are
already held to the R-76 class.

**Why per role, not one shared user per global role** (ruling 1). A shared
`<team>.supervisor` for every supervisor-type role would match the one inbox
per cluster, but it cannot be revoked one role at a time. The renderer mints
a password only when it is missing (`manager.go:368-376`) and drops it only
when the user leaves (`:382-386`); there is no rotation. With a shared user,
turning `research-supervisor` to `"none"` while `supervisor` stays would
leave the same live password in that seat, and the running research-supervisor
would keep its global reach. With a user per role, that user leaves the
rendered set at the next reload. Revocation then takes effect at once. The
reviewer measured it on review 5400892009 (local nats-server v2.14.6 on
loopback scratch ports, with a scratch config and HOME, no fleet broker):
removing user `t.research-supervisor` and reloading dropped its live
subscriber immediately (EOF), `/connz` listed only `t` afterwards, the
removed user's reconnects and publishes were refused, and user `t` was
untouched. So the revoked seat stays disconnected and retrying until it is
restarted.

## 5. What per-role-users stage 3 does to it

Stage 3 (phase 2 in `per-role-broker-users.md` section 7) strips the global
grants from `<team>`. After this change:

- Every seat whose role resolves to `supervisor`, research-supervisor
  included, connects as its own `<team>.<role>` user once the broker accepts
  it, so stage 3 removes nothing it uses.
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

## 6. A wrong declaration, and the launcher

### The hazard

A manifest that declares a worker `global_role = "supervisor"` would make
marvel mint that worker a global user and hand it the credential: publish on
`global.director.inbox`, `global.*.supervisor.inbox` and `$JS.global.API.>`
(`declared.go:216`, `:240-243`). director's launcher does NOT catch it. For a
worker cast `GROLE` is empty (`cast-launch.sh:129`), the contradiction test
(`:143-145`) sits inside `if [[ -n "$DIRECTOR_GLOBAL_DOMAIN" && -n "$GROLE" ]]`
(`:132`), and the `else` branch (`:157`) unsets `DIRECTOR_GLOBAL_ROLE` and
launches. The broker credential is not touched, so the seat connects with
global grants and nothing reports it. One wrong line would be a silent
widening.

### The guard is in marvel: two keys

marvel must stay safe without depending on a director change, so a
declaration widens a role only when two keys agree:

1. **The manifest declares** the role `global_role = "supervisor"`.
2. **The cluster admits** that role name, in the operator's client config,
   on the message-bus service entry:

   ```yaml
   services:
     - name: bus
       class: message-bus
       global_roles: [research-supervisor]
   ```

   `supervisor` is admitted without being listed, which keeps today's
   behavior.

Admission is checked in two places, and only the second is the guard:

- **At resolution, every time (the guard).** `ResolvedGlobalRole` takes the
  admitted set, and the renderer, `Credential` and the seat env all resolve
  through it with the set the daemon loaded. The renderer builds from STORED
  teams (`internal/bus/manager.go:349`, `m.teams.ListTeams()`), and a daemon
  start rehydrates teams without an apply, so a check at apply alone would
  not hold: an operator who removed a name from `global_roles` and
  reexeced would still see stored declarations resolve, the user minted and
  the grants rendered, until someone re-applied. With admission in the
  resolver, the first render after the reexec drops the user, its live
  connection is closed at reload (section 4, measured), and `Credential`
  hands any new spawn of that role the team user.
- **At apply (an early, loud refusal).** A declaration on a role name the
  cluster does not admit is refused before anything is stored, naming the
  role and the config key, so a manifest author sees the mistake at once
  rather than at a render.

A stored declaration whose name is no longer admitted is reported, not
silently ignored: the render emits `bus.global-role-unadmitted` (team, role)
once per change, so a de-admission that strips a role reads in `marvel
events`.

So a manifest author cannot widen a role by one line, and the operator who
owns the cluster decides which names may hold the global tier, at every
render and not only at apply. The admitted list is operator-owned data in the
cluster config, not a name in marvel's code, so it is not the second
hard-coded name director ruled out. Two costs:
- The config is read once at daemon start, with no reload (#514). Admitting
  or removing a role name takes a daemon reexec, and takes effect at that
  reexec's first render.
- A manifest that still declares a de-admitted role fails at its next apply,
  until the declaration is removed or the name is admitted again.
- With no bus at all, a declaration is inert. Apply gives no early error,
  because there is no global tier to widen and no admitted list to check, so
  a manifest written for a fleet still applies on a machine without a bus.
  Nothing renders and no seat env is exported. Once a bus exists, the resolver
  gates the stored declaration at every render, and `bus.global-role-unadmitted`
  fires at the first render if the config does not admit the name. Director
  accepted this default on marvel#518.

### The launcher, as defense in depth

marvel exports `DIRECTOR_GLOBAL_ROLE=<resolved>` into a global-role seat's
environment (`internal/runtime/adapter.go`, beside `DIRECTOR_TEAM`) and
leaves it unset otherwise. Two one-line director changes would make the
launcher a second check in both directions:

- move the contradiction test ahead of `:132`, so a cast that derives no
  global role refuses a set `DIRECTOR_GLOBAL_ROLE`;
- refuse when the cast derives a global role and `DIRECTOR_GLOBAL_ROLE` is
  absent, which catches a cast that is global under a manifest that declares
  nothing. Today that seat connects as the team user and fails loudly only
  after stage 3, at the bus pre-flight (`cast-launch.sh:290-292`).

This design proposes both to director and does not depend on them: the
two-key guard holds without them.

marvel never reads wardrobe. It stays independent of the role library (SOUL
section 2), and it could not anyway, since the manifest role name is not the
wardrobe role id (`cast-launch.sh:58-66` maps between them). The launcher
derives its answer from the cast on purpose (`:119-121`). The two answers are
tied by the env check above, not merged into one.

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
   `global_role = "supervisor"`, admitted by the cluster, in a team with no
   `supervisor` role: the team is `Supervisor`, the renderer emits
   `<team>.research-supervisor`, and `Credential` returns it once confirmed.
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
9. **The two-key guard.** A worker role declaring `global_role = "supervisor"`
   on a cluster whose config does not admit its name is refused at apply,
   and nothing is stored. The same manifest applies once the name is
   admitted.
10. **Revocation.** A role turned from `"supervisor"` to `"none"` leaves the
    rendered principals at the next render, and its password is dropped,
    while a `supervisor` in the same team keeps its own user.
11. **De-admit, then restart.** A stored team whose research-supervisor
    declares `global_role = "supervisor"` under an admitting config; the
    daemon restarts (rehydrate, no apply) with a config that no longer
    admits the name. The first render emits no `<team>.research-supervisor`
    principal, `Credential` returns the team user for that role, the seat env
    has no `DIRECTOR_GLOBAL_ROLE`, and `bus.global-role-unadmitted` is
    emitted once.

## 9. Edits, in order (none made by this PR)

1. `api.Role` / `ManifestRole` gain `GlobalRole`, with validation and
   `ResolvedGlobalRole()`; the default map replaces `GlobalAddressRoles`.
   Tests 1, 3, 5, 7, 8.
2. The cluster config gains `global_roles` on the message-bus entry, and
   `ResolvedGlobalRole` takes the admitted set; apply refuses an unadmitted
   declaration early. Tests 9 and 11.
3. `hasSupervisorRole`, the renderer and `Credential` read the resolver; one
   user per such role. Tests 2, 4 and 10.
4. The adapter base env exports `DIRECTOR_GLOBAL_ROLE`. Test 6.
5. Operator steps, run by director: admit `research-supervisor` in each
   cluster's config and reexec; declare `global_role = "supervisor"` on every
   research-supervisor role; rotate those seats onto their own user.
6. marvel's CLAUDE.md is updated in the builder PR. This PR updates
   `per-role-broker-users.md` itself (line 33, the section 3 principal table
   and its `Credential` paragraph).

Steps 1 to 4 can be one builder PR. Step 5 must precede per-role-users
stage 3.

## 10. Rulings needed

1. **One user per role, or one shared user per global role** (section 4).
   Default: one per role, `<team>.<role>`, so one role can be revoked at a
   reload. The cost of the shared alternative: no rotation exists, so a role
   turned to `"none"` keeps the shared password and its global reach while
   another role in the team still holds the global role.
2. **The guard against a wrong declaration** (section 6). Default: the
   two-key guard in marvel, which does not depend on director. Also propose
   the two launcher changes to director as defense in depth. Alternative:
   rely on the launcher alone, which needs director's change to land first
   and leaves marvel unsafe until it does.
3. **`shiftOrder`** (section 7): leave it, or read the resolver. Default:
   leave it and file it separately.

## 11. Rejected

| Option | Why it was rejected |
|---|---|
| Add `research-supervisor` to `GlobalAddressRoles` | director's constraint; also broken: `Credential` would look for `<team>.research-supervisor`, which the renderer never mints, so stage 3 cuts it off anyway |
| Match role names ending in `supervisor` | convention as code: a role named `notes-supervisor` would gain global reach by its name |
| marvel reads the wardrobe role's class | marvel would depend on the role library, and the manifest role name is not the wardrobe role id, so marvel would need the launcher's mapping table as well |
| The launcher reads marvel's env instead of the cast | director's launcher derives the role from the cast on purpose (`cast-launch.sh:119-121`); changing that is director's design, not marvel's |
| One shared `<team>.supervisor` user for every supervisor-type role | not revocable per role (section 4) |
| Admission checked at apply only | stored teams rehydrate without an apply, so a de-admitted name keeps its grants until a re-apply (section 6) |
| The manifest declaration alone, with no cluster admission | one wrong line gives a worker the global tier, and the launcher does not catch it (section 6) |
| A boolean `global = true` | the address word is what both sides exchange (`DIRECTOR_GLOBAL_ROLE`); a boolean would hard-code `supervisor` again, one level down |
