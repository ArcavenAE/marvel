# Drift view: which sessions are behind their role

Design for review. No code lands until this doc is reviewed.

- Author: arcaven-architect-g5-0.
- Issue: #416. Tracks: aae-orc-b8ptm, step 1 of the orc's
  `docs/design/wardrobe-iteration.md` section (e).
- Checked against marvel `origin/main` 3cb6101.

## 1. The problem, verified

| Premise | Where | Result |
|---|---|---|
| A session's runtime is the role's, copied at spawn | `internal/team/controller.go:1230`, `:2061` (`Runtime: role.Runtime`) | a copy; `git grep` for assignments to a session's `Runtime.Args`, `Env`, `Prompt` or `Command` in `internal/` finds none |
| The copy persists | `api.Session.Runtime`, exported, stored through encoding/json in bbolt | survives a daemon restart |
| No view compares them | `renderSessionTable` (`cmd/marvel/main.go:2487`) receives sessions only; `marvel work` prints `workspace/<name> ready` and advisories (`main.go:904-912`) | no "behind" anywhere |
| A respawn takes the role's current runtime | operator ruling D4, 2026-09-28, INTERIM ("current version (for now)") | so "behind" means "will change at next spawn" |
| A finished headless run keeps its slot | ADR-010, built (#244, #246) | it never respawns, so it stays behind forever once its role changes |

## 2. What "behind" means

A session is **behind** when its stored `Runtime` differs structurally from its
role's current `Runtime` in the live team spec. Every field of `api.Runtime`
counts: `Name`, `Command`, `Args` (order matters), `Script`, `Mode`, `Prompt`,
`ContextWindow`, `ContextFeed`, `Env`, `Backend`. Equality is field by field,
with nil and empty slices and maps treated as equal.

| Value | When |
|---|---|
| `current` | the runtimes are equal |
| `behind` | they differ |
| `-` | no role to compare against: a `marvel run` session, or a session whose role is no longer in the team spec |

Headless sessions in `succeeded` are compared like any other, so a finished
run whose role has since changed shows `behind`. That is the ADR-010 slot rule
made visible; the rule itself does not change (the study's dissent D2,
`rerun_on_spec_change`, stays open).

## 3. Where it is computed

The daemon computes it, because only the daemon holds both sides: the session
list RPC and `describe session` look up each session's team and role in the
store and attach a derived field, `spec` (`current`, `behind`, or empty for
`-`), on the response. It is never persisted: a derived answer that went stale
in the store would be the problem this view exists to show.

For `behind` sessions, `describe` also returns `spec_diff`: the **names** of
the differing fields (for example `args, prompt`). The new field carries names,
never values, so it adds no exposure; a name is enough to act on.

**The existing exposure, named and not changed here.** `describe session`
already prints the whole stored session as JSON (`cmd/marvel/main.go:1131`),
and `Session.Runtime` (`internal/api/types.go:214`) and `Runtime.Env` (`:189`)
carry no json tag, so today's output includes every `Env` value, every `Args`
entry and the `Prompt`. This design neither adds to that nor redacts it:
redacting would be a behavior change with its own review, and director has
raised the `Env` exposure with the operator separately.

## 4. How it shows

- **`get sessions`:** one new column, `SPEC`, holding `current`, `behind` or
  `-`. Seven characters at most, so it adds one narrow column to a table that
  must stay usable in a terminal (the same constraint #417 carries for
  `limited`). No field names in the table.
- **`describe session`:** a `Spec:` line, `current`, `behind (args, prompt)`,
  or `- (no role in the team spec)`.
- **`marvel work`:** after `workspace/<name> ready`, one line when any session
  in the applied teams is behind: `N sessions are behind their role and take
  the new spec at their next spawn`. Nothing when N is zero. It is information,
  not a warning: under D4 this is the normal state right after an apply.

## 5. What it deliberately leaves out

- **Role fields outside `Runtime`.** `permissions`, `dangerous_permissions` and
  `policy` live on the role, not the runtime, and a session does not store the
  values it was spawned with. A permissions change takes effect at next spawn
  (marvel#313), so it is real drift this view cannot see. Step 2 records a
  digest of the spawn spec on the session, and that digest should cover these
  fields; this doc names the gap so step 2 closes it.
- **Pinned files.** A runtime that points at a file (for example
  `--append-system-prompt-file <path>`) compares by path. A changed file
  behind an unchanged path reads `current`. Step 2's digest and pinned-file
  validation are the answer; step 1 is structural only, as the study scoped it.
- **Hashing, labels, or any new manifest field.** None.

## 6. Tests (red first on 3cb6101)

1. A session spawned from role R, then R's `args` changed by re-apply:
   `get sessions` shows `behind`, `describe` lists `args`.
2. The same session after a shift: its successor shows `current`.
3. A headless role that ran to `succeeded`, then its `prompt` changed: the
   succeeded session shows `behind`, and no new run is spawned (ADR-010
   unchanged).
4. A `marvel run` session shows `-`.
5. `Env` differs: `spec_diff` lists `env` and carries no value (assert on
   the `spec_diff` field only; the rest of `describe` still prints the
   runtime, see section 3).
6. `marvel work` that changes one role with two live sessions prints
   `2 sessions are behind...`; a no-op apply prints nothing extra.
7. Nil and empty `Args` and `Env` compare equal.

## 7. Edits (none made by this PR)

One PR: the comparison function beside `api.Runtime`, the derived field on the
session list and describe responses, the `SPEC` column, the describe line, and
the apply count. marvel-builder builds it after this design is reviewed.
