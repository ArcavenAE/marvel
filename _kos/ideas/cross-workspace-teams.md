# Cross-workspace teams: a team as a cluster-global identity that can span workspaces

- **Status:** idea (pre-hypothesis), operator-raised 2026-09-20.
- **Subject:** marvel. What a team IS in marvel's data model, and its relation
  to workspace and to the bus. director/kos/the global tier are objects that
  consume the answer.
- **Tracking:** none yet. Blocks aae-orc-q9mtd (global supervisor addressing).
  Resolves the current-state defect in ArcavenAE/marvel#319. Relates
  aae-orc-nny4g (work-management), aae-orc-z3wta (director role),
  director/_kos/ideas/live-fleet-knowledge-and-staffed-roles.md.

## The idea

An agent team should be able to spread its sessions across more than one
workspace: one team, sessions in workspace A and workspace B, sharing one team
identity. The operator conceived this as a useful feature (it was not the
original intent), and it also happens to be the clean resolution of a defect
the q9mtd investigation surfaced.

## What forces it

marvel does not currently agree with itself about what a team is (see #319):

- **Store / reconciler / accountant / admission / sessions:** a team is
  workspace-scoped. `Team.Key()` is `<workspace>/<name>` (internal/api/types.go:653);
  the same team name in two workspaces is two distinct teams.
- **Bus:** a team is cluster-global. The broker user IS the team name
  (internal/bus/manager.go:171), and a team name applied in two workspaces is
  refused (internal/bus/declared.go:203), because the second would share the
  first's grants.

Two coherent ways out. One is to make team strictly workspace-scoped and gate
uniqueness per workspace (this forecloses the feature). The chosen direction is
the other: make a team a coherent CLUSTER-GLOBAL identity that MAY span
workspaces.

## What the model would have to answer

- **Data model.** Today `Team` carries a single `Workspace` and its key is
  `workspace/name`. A cross-workspace team means the team is the cluster-global
  grouping and its sessions carry the workspace, or the team carries a SET of
  workspaces. Session key stays `workspace/agent` (sessions still live in a
  workspace); team identity is above workspace.
- **Uniqueness enforcement.** Team names become cluster-unique BY DESIGN,
  enforced at apply time (a store gate), replacing the soft, post-apply,
  bus-only, cluster-breaking check (#319). Apply must fail loudly on a
  collision, not succeed-then-break-bus.
- **Bus grant.** A team user today is confined to `agent.<workspace>.<team>.>`.
  A cross-workspace team needs a grant that spans its workspaces
  (`agent.<ws1>.<team>.>` + `agent.<ws2>.<team>.>`, or a subject layout that
  puts team above workspace). The team credential is already keyed by team
  name, which fits a cluster-global team; the subtree confinement is what needs
  rework.
- **Addressing (q9mtd).** With team as a cluster-global identity that spans
  workspaces, a supervisor CANNOT be workspace-qualified, so the global address
  resolves to team-only: `global://host/cluster/team/supervisor` (routing
  subject cluster-first to stay in the `global.<cluster>.>` grant). q9mtd is
  blocked on this model for exactly that reason.
- **Reconciliation and shift.** Replicas, generations, and shift already work
  per team; confirm they behave when a team's replicas span workspaces (does a
  role's replicas all sit in one workspace, or can they split?).

## Open questions

- Is workspace still a meaningful boundary for a cross-workspace team, or does
  it become purely a session-placement/filesystem concern once team is the
  identity? (Ties to the cwd-is-part-of-the-work finding, aae-orc-vdcwm.)
- What is the migration for the current inconsistent state: does making team
  cluster-global require a rename pass on any existing collisions, and how does
  the bus grant change roll out without dropping live sessions?
- Does a cross-workspace team change how the accountant attributes spend
  (currently `workspace/team`)? Team-level totals would sum across workspaces.

## Why an idea, not a probe

It reshapes marvel's team data model, the apply-time uniqueness gate, and the
bus grant layout at once, and it has a downstream (q9mtd addressing) waiting on
it. It crystallizes into a probe when a concrete use case first needs one
team's sessions in two workspaces, or when #319's loud-failure fix is taken up
and the model decision can no longer be deferred.
