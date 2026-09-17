# finding-043: re-applying a manifest hot-updates the teams it names and leaves the rest running

Answers the standing "does updated work apply after instancing" question. It
does, at the level the manifest reaches, and the reach is the teams the
manifest names. A team the manifest stops naming keeps running.

## Mechanism

`Manifest.Apply` (manifest.go:517) walks the manifest's own teams. For each:

- If the team already exists, `UpdateTeam` replaces `live.Roles` and
  `live.Budget` in place (manifest.go:116-117).
- If it does not exist, `CreateTeam`.

There is no loop that deletes teams present in the store but absent from the
manifest. Apply reconciles toward the manifest only for the teams the manifest
carries.

## What this means for a live daemon

Re-applying an edited manifest hot-updates a named team without a restart:

- A role added to the manifest spawns.
- A role dropped from the manifest drains its sessions
  (`role.removed` + `session.deleted`, finding-006). Roles reconcile to the
  manifest; they are not append-only.
- Budget is replaced wholesale by the re-applied value.

But a team dropped entirely from a re-applied manifest is untouched and keeps
running. Apply is not a teardown. Teardown is a separate explicit action:
`marvel delete team`, and `marvel stop --teardown` deletes sessions but never
Teams (question-convergence-posture, aae-orc-cxdf).

## The hazard

Two easy accidents follow from "reconcile to the manifest, at the level the
manifest reaches":

- Narrowing a manifest to update one team silently leaves the omitted teams
  running. The narrowed apply does not converge the fleet to the narrowed file.
- Dropping a role from a re-applied team drains its agents. That is the
  intended behavior when deliberate, and a silent loss when not.

Neither is a bug. Both are consequences of the upsert-no-team-prune shape, and
an operator editing a manifest against a live daemon should know the file's
reach stops at the teams it names.

Related: finding-006 (role drain on re-apply), question-convergence-posture +
finding-034 (teardown does not clear durable desired Teams; the read-side
posture, not a write-side delete, is how the fossil-team spend is discharged).
