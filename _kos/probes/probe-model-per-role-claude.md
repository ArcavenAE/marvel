# probe: can a marvel manifest set the model per role for claude sessions

**Probe id:** `probe-model-per-role-claude`
**Ticket:** `aae-orc-2lfg` (per-agent model/provider selection in the team
manifest, adapter-resolved native flags). This probe answers the claude
half of that ticket's premise; it does not close it.
**Date opened:** 2026-09-30
**Question:** which model a claude role runs, and whether marvel can
declare it. Neighbour, not this question: `question-model-runtime-posture`
(M6, whether marvel manages the runtime that serves a model).
**Status:** RAN (2026-10-01; H1 confirmed; H2 not run). The first
attempt, 2026-09-30 about 03:51Z, was denied by the auto-mode classifier as
[Shared Cluster Mutation]; the run went ahead on the operator's grant.
Pre-registration below is unchanged; results are in "Results, 2026-10-01"
at the end.

## Why

The operator asked whether marvel can pin a model for claude agent
sessions, with builders on a Sonnet model as the example. There is no
manifest `model` field: `git grep 'toml:"model' origin/main -- internal/`
returns nothing (checked 2026-09-30 at `b1f4953`). Before anyone designs
one, the probe establishes whether an existing passthrough already
expresses the choice, and whether marvel's own readings (the `LLM` column,
CTX%) follow it. If the passthrough works, the remaining work is
ergonomics and validation, not capability.

## Overlap check

`probe-org-model-heterogeneous-teams.yaml` (session-009, complete) settled
that a team holds heterogeneous roles, each with its own runtime and
replicas. It says nothing about the model a runtime is pointed at. This
probe builds on it (a role is the unit the model would attach to) and does
not repeat it.

## What the code says before the run

- **Args pass through untouched.** The claude adapter copies
  `Session.Runtime.Args` verbatim before appending its own flags
  (`internal/runtime/claude.go:99-100`). marvel adds `--settings`,
  headless flags, `--session-id` and `--permission-mode`, never `--model`.
  So `runtime.args: ["--model", "<id>"]` reaches `claude` as written.
- **Role env passes through**, except the identity credentials and literal
  bearers marvel refuses (`internal/runtime/adapter.go:399-410`).
  `ANTHROPIC_MODEL` is neither, so `runtime.env.ANTHROPIC_MODEL` also
  reaches the pane.
- **marvel reads a model from args, not env.** `usage.ModelFromArgs`
  (`internal/usage/limits.go:392-406`) scans `-m`, `--model`, `--model=`
  and `-m=` for the context-window resolver when the stream names no model.
  Its comment: "Marvel injects no model flag, so this reads what the
  operator wrote." Nothing reads `ANTHROPIC_MODEL`.
- **The `LLM` column is `Session.ContextModel`**, written from a heartbeat
  (`internal/api/store.go:747`). For interactive claude that is the
  statusline payload's `model.display_name` (`cmd/marvel/ctxforward.go:70`,
  `:405`). The accountant path covers headless sessions.

## Pre-registration

Written before the scratch team was applied. Not edited after data
existed; corrections append below.

### Hypotheses

- **H1.** `runtime.args: ["--model", "<sonnet id>"]` on a claude role runs
  that role on the named model today, with no marvel change. After one
  turn, marvel's recorded model for the session names that model, not the
  account default.
- **H2 (the env variant).** `runtime.env.ANTHROPIC_MODEL` also selects the
  model in the harness, but marvel's arg-based fallback cannot see it.
  So the two passthroughs are not equivalent from marvel's side.
- **H0 (null).** Either marvel strips or overrides the flag, or the
  session reports the account default model regardless.

### Rig

- A scratch team `probe-model` in its own workspace `probe`, declared in
  a manifest under the session scratchpad, never in `~/.marvel/manifests`.
  No live team's manifest, sessions, or roles are created, changed, or
  deleted. The daemon and the shared broker are not untouched: apply and
  teardown each reload the broker, and apply re-projects every live
  session (see "Run it").
- **Headless** claude roles (`mode: headless`, one fixed prompt: reply
  with the word ok). Headless gives each role exactly one turn with no
  inject, and its stream names the model the harness actually used.
- One role per candidate model id, all otherwise identical:
  `claude-sonnet-5`, `claude-sonnet-5-5` and the alias `sonnet`. The
  operator's example "Sonnet 5.5" is not a known id. The probe reports what
  resolves and what errors, verbatim, and picks nothing.
- One role set by `runtime.env.ANTHROPIC_MODEL` to whichever id the args
  roles show resolving, to test H2.
- Evidence per role: `marvel describe session` (Args, Env keys,
  ContextModel, state) and the model named in the session's own output.
- Teardown: delete the scratch team, then confirm its sessions are gone.

### Success signal

- H1 confirmed: an args role that resolved shows a Sonnet model in the
  harness's own report and in `describe` ContextModel.
- H2 separated: the env role's harness report and marvel's recorded model
  are compared directly.
- Every candidate id is recorded as resolving or erroring, with the text.

### Stop conditions

- Any classifier or permission denial on apply, describe or delete:
  stop at that step and report the denial verbatim.
- Any sign the scratch manifest creates, changes, or deletes a live
  team's manifest, sessions, or roles: stop and delete. The two expected
  broker reloads are not that sign.
- Any `bus.rendered` event whose reason is anything other than `apply`
  or `delete team probe/probe-model`: stop and delete. The reason is the
  parenthesized text in either form `emitBusRender` writes, the info form
  `bus config rendered (<reason>): ...` and the warning form
  `bus config not rendered (<reason>): ...`
  (`internal/daemon/daemon.go:2864-2875`). The teardown reason carries the
  team key exactly as `marvel delete team` received it, so it reads
  `probe/probe-model`, not the bare team name. The warning form is a stop
  whatever its reason: a failed render leaves the shared broker on its
  old config.
- Any apply log line `apply: re-projected policy for N running
  session(s)` (`internal/daemon/daemon.go:1226-1228`): stop and delete.
  It prints only when N is greater than zero. On the first apply the
  scratch team has no sessions yet, and the later apply that adds the env
  role changes no existing role's policy, so any appearance means a live
  session's projection changed.

### Timebox

One session, about 90 minutes including teardown.

### Predicted confidence

- H1: high on the harness side, because it is a verbatim passthrough.
  Medium on marvel recording the model, because headless attribution runs
  through the stream parser rather than the statusline.
- H2: medium that the env var selects the model, and high that marvel's
  window fallback misses it.

## Run it

The scratch manifest, verbatim. Save it outside `~/.marvel/manifests`
and apply it with `marvel work <path>`. It declares only workspace
`probe` and team `probe-model`; `handleApply`
(`internal/daemon/daemon.go:1127`) creates and updates only the manifest's
teams, so no live team's manifest, sessions, or roles are created, changed,
or deleted. The `ANTHROPIC_MODEL` env role is added after the args roles
show which id resolves.

The run still reaches past the scratch team, in three places:

- **Re-projection on apply.** Before reconciling, `handleApply` calls
  `sessMgr.Reproject()` (`internal/daemon/daemon.go:1226`), which walks
  every live session in every team and rewrites any settings file whose
  projection differs. A scratch manifest changes no live team's policy, so
  no live file should change, but the pass runs over all of them.
- **Broker reload on apply.** `regenerateBus("apply")`
  (`internal/daemon/daemon.go:1230`) re-renders the shared managed
  broker's files and reloads it (SIGHUP) when they changed. The scratch
  team's new broker user is such a change.
- **Broker reload on teardown.** Deleting the scratch team calls
  `regenerateBus("delete team ...")` (`internal/daemon/daemon.go:1396`),
  which removes that user and reloads the shared broker again.

Every live team on this daemon shares that broker, so run the probe when
two reloads are acceptable to them.

Watch two places for the stop conditions above, from before the apply
until after the teardown:

- **The marvel event ring:** `marvel events --kind bus.rendered`. Expect
  exactly two, `bus config rendered (apply)` and
  `bus config rendered (delete team probe/probe-model)`. The later apply
  that adds the env role should render nothing, because the team's broker
  user already exists; if it does render, the reason is still `apply`.
  Anything else, and the warning form with any reason, is a stop.
- **The daemon log:** `marvel daemon logs`. The `bus.rendered` lines
  appear here too, and the `apply: re-projected policy` line appears only
  here. Any appearance of it is a stop.

```yaml
# Scratch manifest for probe-model-per-role-claude. Not a live team.
workspace:
  name: probe

teams:
  - name: probe-model
    roles:
      - name: args-sonnet-5
        replicas: 1
        restart_policy: never
        runtime:
          image: claude
          command: claude
          mode: headless
          prompt: "reply with the word ok"
          args: ["--strict-mcp-config", "--model", "claude-sonnet-5"]
      - name: args-sonnet-5-5
        replicas: 1
        restart_policy: never
        runtime:
          image: claude
          command: claude
          mode: headless
          prompt: "reply with the word ok"
          args: ["--strict-mcp-config", "--model", "claude-sonnet-5-5"]
      - name: args-alias-sonnet
        replicas: 1
        restart_policy: never
        runtime:
          image: claude
          command: claude
          mode: headless
          prompt: "reply with the word ok"
          args: ["--strict-mcp-config", "--model", "sonnet"]
```

Teardown: `marvel delete team probe/probe-model`, then confirm with
`marvel get sessions` that no `probe` sessions remain.

## Results, 2026-10-01

Appended after the run. The pre-registration above is not edited.

### Provenance

- **Director run.** The director applied the "Run it" manifest on the
  operator's grant, against the operator's cluster, and tore it down. The
  per-role ContextModel values, the exit codes, the daemon-log checks and
  the teardown session count below are as the director reported them.
  The builder who wrote this section did not see them.
- **Supervisor ring check.** The supervisor who dispatched this write-up
  read the cluster's event ring for the run window and confirmed the two
  `bus.rendered` events and the absence of any probe team or session
  after teardown. It did not see the ContextModel values or the daemon log.
- Nothing in this section was run by its author.

### Timeline (UTC)

| step | time | source |
|---|---|---|
| apply | 00:40:51 | director; ring check |
| teardown | 00:55:03 | director; ring check |

### Candidates

All three are headless roles from the scratch manifest, one turn each.

| role | `--model` arg | exit | ContextModel | outcome |
|---|---|---|---|---|
| `args-sonnet-5` | `claude-sonnet-5` | 0 | `claude-sonnet-5` | resolves |
| `args-sonnet-5-5` | `claude-sonnet-5-5` | 0 | `claude-sonnet-5-5` | resolves |
| `args-alias-sonnet` | `sonnet` | 0 | `claude-sonnet-5-5` | resolves to `claude-sonnet-5-5` |

Source: director.

No candidate errored, so there is no error text to record.

### Hypotheses

- **H1: confirmed.** `runtime.args: ["--model", "<id>"]` selects the
  model per role today with no marvel change, and marvel records it.
  The pre-registered signal asks for the model in both the harness's own
  report and `describe` ContextModel. The relayed results give only
  ContextModel, but the alias row covers the harness half. For a headless
  session the recorded model is the one the stream names, and the
  `--model` arg is read only when the stream names none
  (`internal/usage/limits.go:546-549`, `internal/usage/accountant.go:305-317`
  and `:774`). The arg there was `sonnet`, so a recorded
  `claude-sonnet-5-5` can only have come from the harness's stream. For the two full ids the arg and the stream name agree, so
  ContextModel alone cannot tell which source wrote them.
- **H2: not run.** No `runtime.env.ANTHROPIC_MODEL` role was applied.
  It stays open as an optional follow-up: add the env role named in
  "Rig", set to `claude-sonnet-5-5`, and compare the harness's report with
  marvel's recorded model.
- **H0: rejected** for the args passthrough. marvel neither strips nor
  overrides `--model`.

### Stop conditions checked

- **Classifier or permission denial on apply, describe or delete:** none
  reported for the run (director).
- **A live team's manifest, sessions, or roles created, changed, or
  deleted:** no sign reported (director).
- **A `bus.rendered` event with an unexpected reason, or any warning
  form:** none. The ring shows exactly two `bus.rendered` events in the
  window, `(apply)` at 00:40:51 and `(delete team probe/probe-model)` at
  00:55:03 (supervisor ring check). No `bus config not rendered` line in
  the daemon log (director).
- **`apply: re-projected policy for N running session(s)`:** absent from
  the daemon log (director).

No stop fired.

### Renders observed

| reason | time | source |
|---|---|---|
| `apply` | 00:40:51 | director; ring check |
| `delete team probe/probe-model` | 00:55:03 | director; ring check |

These are the two expected reloads named in "Run it", and there were no
others.

### Teardown

`marvel delete team probe/probe-model` at 00:55:03. Zero `probe`
sessions remain (director), and the ring check found no probe team or
session. The empty workspace `probe` may persist; the run did not
delete it.
