# Identity at launch and a managed bus: what director's forward requirements ask of marvel

**Status: idea, carrying four forward requirements the operator ruled on
2026-09-12 through the director seat. The measurements are current; the
capabilities are unbuilt. Nothing here is a spec yet.**

Raised out of the naming party in director (director `sim/requirements.md`,
R-71 through R-86). The party first declined "director sets a session's name"
because no installed harness exposes a rename lever. The operator overruled:
the missing lever is the reason director expands beyond an MCP envelope, and
the place a session's identity is set at launch is the component that spawns
it. On this fleet that is marvel. So the requirement is cross-cutting, and this
file is marvel's half of it.

## The four requirements, as they land on marvel

Director's register carries them as R-83 through R-86 under a new source class,
FORWARD (the operator committed to build it; a direction, never retired as
declined because the current envelope cannot satisfy it). Restated here by what
marvel would have to do:

1. **Identity and naming at launch is declarable.** A manifest or launcher field
   from which the session key, the bus id, and the harness display name are all
   projected from one source, so the visible name and the key never become two
   records to reconcile (director R-73, R-84).
2. **marvel manages and exposes NATS.** Already ruled 2026-08-01 as an external
   nats-server supervised by marvel as a declared workload
   (`question-agent-communication-broker`). The new half is the spawn side:
   marvel knows how to start a director-enabled session and connect it to that
   bus (director R-85).
3. **Whatever marvel mints is validated at mint and bound at the bus.** Names
   validated against a closed character class and rejected, never rewritten;
   per-session credentials scoped to the session's own subjects (director
   R-76, R-77). The phase-0 shim's two filed defects (director#3, director#4)
   are what a copy of it inherits.
4. **The bus is two-tier.** A local NATS inside each marvel cluster (team
   traffic, events, heartbeats) and a global NATS for the director-supervisor
   channel across clusters and hosts. An identity minted locally must route
   globally without a rename (director R-86).

## What marvel does today (measured 2026-09-12)

- **The session name is computed, never declared.** `internal/team/controller.go:1139`
  builds `<team>-<role>-g<generation>-<index>`; the same shape at :1894 and
  :2073, with a `-held-` infix at :334. No manifest field overrides it, though
  `Session.Name` carries a toml tag.
- **Identity reaches the harness as environment plus one sentence.**
  `internal/runtime/adapter.go:188` (`baseEnv`) stamps `MARVEL_SESSION`,
  `MARVEL_ROLE`, `MARVEL_TEAM`, `MARVEL_WORKSPACE`, `BEADS_ACTOR`
  (`marvel/<workspace>/<session>`) and, when a socket exists,
  `MARVEL_HEARTBEAT_TOKEN`. The claude adapter (`internal/runtime/claude.go:63`)
  appends `--append-system-prompt "You are <session> (role: ..., team: ...,
  workspace: ...)"` and passes no `-n/--name`, no `--session-id`, and no agent
  id. `Role.Persona` and `Role.Identity` are read only by the frozen forestage
  adapter (`internal/runtime/forestage.go:57`, :62), so a manifest can declare
  them and the claude adapter ignores them silently.
- **`BEADS_ACTOR` is the precedent.** It is the one identity marvel already
  projects into a foreign namespace, derived from the session key, unique per
  session. A bus id is the same move for a second namespace.
- **The heartbeat token is the precedent for a per-session secret at spawn.**
  Minted at `internal/session/manager.go:440` before the store record exists,
  plaintext rides only the launch path, digest stored, `json:"-"` on the field
  so sibling agents cannot read it off ListSessions. A bus credential wants the
  same shape.
- **No bus code exists.** No Go source references nats, jetstream, broker, or
  envelope outside comments (`CLAUDE.md:269`, "UNBUILT"). The events ring, the
  heartbeat RPC, `marvel inject`, and the stdout FIFO are what exists. The
  Endpoint resource is three strings nothing resolves.
- **Multi-host is a stub.** `Host` at `internal/api/types.go:507` has Name and
  Status and no references. Each daemon reconciles its own host; `mrvl://`
  named clusters are client-side naming only. The two-tier topology has no
  substrate on marvel's side yet; daemon isolation (`docs/design/daemon-isolation.md`,
  findings 012, 013, 025) is the boundary a local tier would sit on, and
  finding-025 records that HOME isolation breaks harness auth.

## Where the work attaches (three seams, not alternatives)

1. **Declaratively**: a field on Role or Team, parsed in `internal/api/manifest.go`
   (the `manifestRole` to `api.Role` copy at :553 is the pattern), carried on
   `api.Role`. Candidate shape: `identity: { name: <template>, bus: <ref> }`,
   with the template defaulting to today's computed name so nothing changes for
   manifests that do not declare it.
2. **At spawn**: `baseEnv` or the adapter's `Prepare`. This is where
   `DIRECTOR_AGENT_ID`, the bus URL, and the per-session credential are injected
   the way `MARVEL_HEARTBEAT_TOKEN` is today, and where the claude adapter
   starts passing `-n` and `--session-id` from the same source. The package doc
   at `adapter.go:1-34` still governs: identity and paths in the environment,
   secrets only where the socket makes them provable, because a constructed
   environment can leak (finding-020).
3. **Imperatively**: a flag on `marvel run`, or a daemon verb beside `inject`,
   for the ad-hoc session.

The NATS server itself is not embedded (ruled). It is a declared part in a
manifest, supervised by the same reconciler, which is what the service-provider
sketch (`docs/design/service-provider/02-architecture.md:88`, :304) already
generalizes.

## Open questions

- Which of the three seams ships first. The spawn seam is the smallest change
  and needs no manifest schema work; the declarative seam is what makes
  identity part of desired state.
- Whether the local-tier bus is one nats-server per daemon (matches daemon
  isolation) or one per host shared by daemons (matches the sidecar-collector
  shape). Finding-025's auth cost bears on this.
- What the global tier's credential story is: per-session NKeys under a local
  operator, or JWTs issued at spawn. Director R-77 records the honest cost
  (conf regen plus reload per spawn, or NKeys/JWT), and it lands here.
- Whether `Role.Persona` and `Role.Identity` should reach the claude adapter
  now that identity at launch is a requirement, or whether B14's five
  primitives arrive by a different route (packs).

## Cross-references

director `sim/requirements.md` R-71 through R-86 and `sim/design/identity-at-spawn.md`;
`question-agent-communication-broker` (the 2026-08-01 ruling);
`question-multi-host` (charter F5); `question-marvel-service-provider-shape`;
`docs/design/daemon-isolation.md`; finding-020, finding-022, finding-025;
orc `docs/design/director-envelope-and-adapter-events.md`;
ArcavenAE/director issues #3 and #4.
