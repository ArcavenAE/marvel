# Per-role broker users in marvel (B8, aae-orc-6vy9x)

Design for review. No code lands until this doc is reviewed.

**Status: ratified 2026-09-30** (operator via director 01M3RBGXEW, relayed by
arcaven-supervisor-g5-0 01M3RBMMW2), verbatim "defaults":

- Q1, build it: yes, per-role broker users in the marvel#312 shape.
- Section 4: Option A. Workers hold no global grant.
- Q2: main and #403's negative test win. Read here as: the supervisor's
  publish on every cluster's supervisor inbox (#403, main b1f4953) is kept
  as is, and its negative test is carried into section 6 test 4.
- The kinu hub probe stays unrun until the operator says yes or a scratch
  hub exists (section 10).

- Commission: arcaven-supervisor-g5-0, msg 01M3N6WSZC (2026-09-28T23:51Z).
- Author: arcaven-architect-g5-0 (successor seat, started 2026-09-28T23:58Z).
- Ruling relay: arcaven-supervisor-g5-0, msg 01M3RBMMW2 (2026-09-30T05:12Z).

## 1. The problem, verified

A same-team worker holds the supervisor's global grants, because the broker
user is keyed by team and the rule (R-94) is keyed by role.

Verified against marvel `origin/main` b1f4953 (#403), each with its command:

| Premise | Check | Result |
|---|---|---|
| One broker user per team | `git show origin/main:internal/bus/declared.go`, lines 180-218 | `Principal{Name: t.Team, ...}`, one per applied team |
| Global grants ride that user | same, lines 206-212 | when `t.Supervisor && s.HubURL != ""`: pub `global.director.inbox`, `global.*.supervisor.inbox`, `$JS.global.API.>`; sub `global.<domain>.>` |
| Every seat gets the team user | `git show origin/main:internal/session/manager.go`, lines 826-831 | `m.Bus.TeamCredential(team.Name)` feeds `lctx.BusUser/BusPassword` for every role |
| Injected as env | `internal/runtime/adapter.go:351-355` | `DIRECTOR_NATS_USER`, `DIRECTOR_NATS_PASS` |
| "Supervisor team" means an exact role name | `internal/bus/manager.go:361-368` | `r.Name == "supervisor"`; `research-supervisor` does not count, which was correct under R-94 as first written. Superseded: R-94 as amended (director#77) and the 2026-10-03 ruling make every supervisor-type role global; see `global-role-declaration.md` (#518) |
| #403 widened worker publish | `gh pr view 403 -R ArcavenAE/marvel` | MERGED b1f4953; adds `global.*.supervisor.inbox` to the same shared team user |
| Live teams affected | `marvel get teams` on the operator host | two applied teams, the operator's own team and one client team, each hold `supervisor` plus six or more worker roles |

The measured leak is finding-179 (mokuzai, 2026-09-18): a worker's own user,
with a plain NATS client and no shim, subscribed `global.mokuzai.>` and
received a message addressed to the supervisor.

## 2. Premise corrections (the commission's framing vs the record)

1. **marvel#312 is not per-session users. It is this item.** #312 ("R-94 is
   advisory inside a team") tracks per-role users as its fix, plan item M9:
   "render a user per (team, global-role) ... make TeamCredential role-aware".
   B8 therefore closes #312; it is not a step before or beside it. Per-session
   users are a different, later item: director
   `sim/design/local-broker-supervision.md` section 4 (the R-85 mint-at-spawn
   follow-on, "not in this brief's tickets"). No bd id or branch for per-session
   users exists (predecessor's round-2 reply, and `gh pr list --search` above
   returned none).
2. **The interim narrowing never landed.** #312 says a one-line change,
   `global.%s.>` to `global.%s.supervisor.inbox`, is "being landed". Main still
   carries `global.%s.>` (declared.go:211). No PR for it exists.
3. **6vy9x fix item 2 is already shipped** (#285, 2026-09-16; marvel-builder's
   2026-09-25 premise check in the ticket notes). Global grants already land
   only on supervisor-bearing teams. Item 1 cannot meet the ticket's own
   acceptance criterion without this design (same note).

## 3. The shape

Keep marvel-builder's starting shape, with three changes marked **(changed)**.

**Principals rendered per applied team:**

| User | Holders | Publish | Subscribe |
|---|---|---|---|
| `<team>` | every role that holds no global address | `agent.<ws>.<team>.>`, `agent.audit`, plumbing | `agent.<ws>.<team>.>`, `agent.<ws>.broadcast`, plumbing |
| `<team>.<role>`, one per role that holds the global role, when a hub is set (originally `<team>.supervisor` for the `supervisor` role only; generalized by `global-role-declaration.md`, #518) | that role only | the team user's grants, plus `global.director.inbox`, `global.*.supervisor.inbox`, `$JS.global.API.>` | the team user's grants, plus `global.<domain>.supervisor.inbox` **(changed: narrowed from `global.<domain>.>`)** |

**The global-address role set** is one declared list,
`GlobalAddressRoles = ["supervisor"]`, next to `ReservedBusUsers` in
`internal/config`. `hasSupervisorRole` becomes a lookup against it. The seat
user `director` stays separate and unchanged (a service-scope principal, not a
role in a team).

**User name separator is `.` (changed: the shape's example used it; this
makes it a requirement and names its cost).** Team names are the R-76 class
`[A-Za-z0-9_-]` (render.go:106-120), so no team can be named
`arcaven.supervisor`. Any separator inside that class (`-`, `_`) can collide
with a real team name such as `arcaven-supervisor`. The cost: `RecoverPasswords`
parses user lines with `[A-Za-z0-9_-]+` (manager.go:342), so a `.` user would
silently lose its password on every daemon restart and remint it, which
breaks every running supervisor's credential. The regex widens to
`[A-Za-z0-9_.-]+` in the same change, with a test that recovers a dotted user.

**Credential lookup becomes role-keyed.** `TeamCredential(team)` becomes
`Credential(team, role)`: it returns `<team>.<role>` when `role` resolves to a
global role (`global-role-declaration.md` section 3, admission included) and
the user exists, and `<team>` otherwise. The spawn path at
session/manager.go:828 passes `sess.Role`. `Adopted` keeps returning nothing.
The password map in `Manager` is keyed by user name, not team name.

**Passwords** are recovered from the rendered file as today, and minted only
for users missing from it.

**Hub-side reach (changed: an added red test).** Narrowing the subscribe grant
may not close the read path on its own. `$JS.global.API.>` lets its holder
create a consumer on a hub-domain stream, and the `_INBOX.>` subscribe then
receives the pull replies. Whether the hub's leaf account permissions stop that
is **not checked**; it is the open question in section 10.
The design keeps `$JS.global.API.>` on the supervisor user only, so workers
lose it either way, and the red tests in section 6 include a JetStream pull,
not only a core subscribe.

## 4. The contradiction with 6vy9x, and its ruling

- 6vy9x (sharpened R-94, R-95 asymmetry): a worker publishing
  `global.director.inbox` is "allowed [correct under sharpened R-94]". R-94
  constrains addressability and subscribe, not publish upward.
- #312 Expected: a worker "cannot ... publish to the director's inbox using the
  team credential". #312 names this as the one open intent: "whether the
  operator wants a direct worker-to-director escalation hatch".
- marvel-builder's shape strips every global grant from workers, which sides
  with #312.

Both readings are internally consistent. The difference is who may reach
director without passing a supervisor, and that is an authority question
(WANTED), not a fact. The operator ruled Option A on 2026-09-30; both
options stay below for the record:

- **Option A (RATIFIED): workers hold no global grant.** A worker escalates
  through its supervisor. Matches #312 Expected and the "director should NOT
  be a bottleneck, supervisors can coordinate" ruling (2026-09-27). Cost: a
  worker whose supervisor is down or deaf cannot reach director on the bus.
- **Option B: workers keep publish `global.director.inbox` only**, no global
  subscribe and no `$JS.global.API.>`. Matches 6vy9x and R-95. Cost: any worker
  can put mail in director's inbox, and a worker cannot receive the reply on
  the global tier, so the reply has to come back through the supervisor anyway.
- Why A was the default: a grant is easy to add later and hard to remove once
  workers depend on it. B remains a one-line addition to the team user if the
  operator ever reverses this.

## 5. Relation to per-session users and the identity flip order

The order the commission names: managed-bus cutover, per-session users, the
shim fills `principal`, then enforcement.

- Per-role users are a **step before** per-session users, not a replacement.
  They fix the R-94 authority boundary (role), which is the only boundary R-94
  asks for. They do not tell two replicas of the same role apart; per-session
  users do that, and the envelope `principal` needs it.
- The seam is the same one. Grants are a function of (team, role). Identity is,
  today, (team, role); under per-session it becomes (session). Keeping those two
  apart in code (`grantsFor(team, role)` and `userFor(...)`) means per-session
  later changes only `userFor` and the reload cadence (one reload per spawn,
  instead of one per apply). The grant table does not change.
- Proposed order: **managed-bus cutover, then per-role users (this), then
  per-session users, then the shim fills `principal`, then enforcement.**
  Per-role can land now because it needs no per-spawn reload, and it moves
  R-94 from advisory to enforced at the broker for the case finding-179
  measured.

## 6. Tests

Red first, each failing on main b1f4953:

1. **Unit, declared set.** For a team with `supervisor` and a hub, the `<team>`
   principal carries no subject starting `global.` or `$JS.global.` in
   Publish or Subscribe. The `<team>.supervisor` principal carries exactly the
   section 3 grants. (marvel-builder's red test, widened to publish.)
2. **Unit, credential selection.** `Credential(team, "supervisor")` returns
   the dotted user; `Credential(team, "builder")` returns `<team>`.
3. **Unit, recovery.** A rendered file holding `arcaven.supervisor` recovers
   that password (fails today on the regex).
4. **#403 negative test, against a real nats-server** (the finding-179
   measurement, as a test): connecting as the worker user,
   - publish `global.<other-cluster>.supervisor.inbox`: Permissions Violation;
   - publish `global.director.inbox`: Permissions Violation (Option A);
   - subscribe `global.<domain>.supervisor.inbox`: Permissions Violation;
   - `$JS.global.API.CONSUMER.CREATE.*`: Permissions Violation.
   And as the supervisor user, publish `global.<other>.supervisor.inbox`
   succeeds (the #403 positive, kept).

Test 4 needs a hub topology for the global subjects to mean anything. If the
existing bus tests cannot stand one up, the fallback is the permission check
alone on a single broker, which proves the grant but not the hub path. Say
which one ran in the PR.

## 7. Migration for the running clusters

Two clusters render a managed broker: kinu and mokuzai. Both have live
supervisors that connect today as `<team>` and use its global grants.

Removing global grants from `<team>` in one release cuts every running
supervisor off the global tier at the next reload, until each respawns. So it
ships in two phases:

1. **Phase 1, additive.** Render `<team>.supervisor` beside `<team>`. `<team>`
   keeps its current grants. New spawns of the supervisor role get the dotted
   user. A reload is harmless: nobody loses a grant.
2. **Rotate the supervisors.** Shift or restart each supervisor seat, one
   cluster at a time, with the seat's own handoff written first (the same
   pattern as the E8 shim rotation tonight).
3. **Check, observational.** On each cluster, the loopback monitor
   (`/connz?auth=true` at listen port + 4000) lists no connection whose user is
   `<team>` and whose client name is a supervisor session. This is a read of
   our own broker, not a gate: it tells the operator phase 2 is safe.
4. **Phase 2, subtractive.** Strip the global grants from `<team>` and narrow
   the supervisor subscribe. Workers lose global reach at reload, which is the
   point. Rollback is reverting phase 2 only.

Phase 1 and phase 2 are separate PRs so phase 2 can wait on step 3 per
cluster without holding phase 1.

Rollback, by what is being reverted:

- **Phase 2:** revert it alone. Workers regain the old grants at reload.
- **Phase 1 (M9-3), after supervisors rotated onto `<team>.supervisor`:** the
  dotted user disappears at reload and those supervisors lose auth. Rotate
  them back to `<team>` first (phase 2 not yet applied, so `<team>` still
  carries the global grants), then revert.
- **The daemon binary, past M9-2:** never while a dotted user is rendered.
  The old `RecoverPasswords` drops that user's password and remints it, which
  breaks every rotated supervisor's credential (the section 3 failure, in
  reverse). Revert phase 1 as above first, then downgrade.

## 8. Edits, in order (none made by this PR)

Each could land in its own PR; the edges are the order.

| # | Edit | Depends on |
|---|---|---|
| M9-1 | `GlobalAddressRoles` in `internal/config`; `hasSupervisorRole` reads it | none |
| M9-2 | Widen `RecoverPasswords` regex to allow `.`, with test 3 | none |
| M9-3 | Phase 1: render `<team>.supervisor`; `Credential(team, role)`; spawn path passes the role; tests 1 (supervisor half) and 2 | M9-1, M9-2 |
| M9-4 | Rotate supervisors on kinu, then mokuzai; record the `/connz` read | M9-3 merged and the daemon on each cluster reexeced onto it |
| M9-5 | Phase 2: strip global grants from `<team>`, narrow subscribe; test 1 (worker half) and test 4 | M9-4 on every cluster |
| M9-6 | Update #312 and 6vy9x: record that the interim narrowing never landed and is folded into M9-5 | none |

Tickets are filed as flat items with these edges once this design is
reviewed; marvel-builder builds only after that.

## 9. What would change the design

- If the hub's leaf account already denies `$JS.global.API.>` from leaf users,
  the JetStream half of test 4 is a regression guard, not a fix.
- If the operator reverses to Option B, the worker row gains one publish
  subject; nothing else moves.
- If per-session users are ruled first, section 5's seam still holds, but
  phase 1 and phase 2 collapse into the per-session rollout and this doc
  becomes its grant table.

## 10. Open questions and out of scope

- **Out of scope: cross-team supervisor fan-out on one cluster.** Every
  `<team>.supervisor` user on a cluster subscribes the same subject,
  `global.<domain>.supervisor.inbox`, so a send to
  `global://<cluster>/supervisor` still reaches every supervisor-bearing team
  on that cluster, including teams of different clients. Per-role users close
  #312's leak (a worker reading its own team's supervisor mail); they do not
  close this one, and "R-94 enforced at the broker" in this doc means the
  former only. The fan-out is a director addressing question, owned by
  aae-orc-q9mtd (global role address shared by several teams) and director
  finding-006 (the global tier has no per-agent address). A team segment
  under the domain would close it at the broker, at the cost of a change to
  the global address grammar that director owns; this design does not make
  that change.

- **Hub-side JetStream reach (unrun probe).** Does the kinu hub's leaf account
  stop a leaf user holding `$JS.global.API.>` from creating a consumer on a
  hub-domain stream? The answer decides whether workers losing that grant is a
  fix or only defense in depth. It is not run: it would touch a live broker
  with a seat's credentials, so it waits for an operator yes or a scratch hub.
  The design does not depend on the answer; section 9 says what changes.
