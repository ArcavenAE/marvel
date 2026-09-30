# probe: can a marvel manifest set the model per role for claude sessions

**Probe id:** `probe-model-per-role-claude`
**Ticket:** `aae-orc-2lfg` (per-agent model/provider selection in the team
manifest, adapter-resolved native flags). This probe answers the claude
half of that ticket's premise; it does not close it.
**Date opened:** 2026-09-30
**Question:** which model a claude role runs, and whether marvel can
declare it. Neighbour, not this question: `question-model-runtime-posture`
(M6, whether marvel manages the runtime that serves a model).
**Status:** NOT RUN: scratch apply denied by the auto-mode classifier as
[Shared Cluster Mutation], 2026-09-30 about 03:51Z; awaiting operator.
Pre-registration below is unchanged; the manifest to run is in "Run it"
at the end, and results append in a dated section.

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
  No live team or live manifest is read for write, applied, or edited.
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
- Any sign the scratch manifest touches a live team: stop and delete.

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
(`internal/daemon/daemon.go:1127`) iterates only the manifest's teams, so it
cannot touch a live team. The `ANTHROPIC_MODEL` env role is added after
the args roles show which id resolves.

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
