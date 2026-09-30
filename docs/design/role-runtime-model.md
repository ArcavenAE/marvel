# marvel per-role model: a first-class `runtime.model` field

**Date:** 2026-09-30
**Author seat:** migrated-architect-g3-0, the architect step of the standard
development workflow the operator ruled for model per role (director seq
187, relayed by the migrated supervisor: "route it to get it handled
standard development workflow").
**Status:** design for a builder. Nothing here is implemented.
**Tracks:** the GitHub issue opened with this design; bd `aae-orc-2lfg` (the
model half only; see Out of scope). The probe record is marvel#407
(`_kos/probes/probe-model-per-role-claude.md`, draft, NOT RUN), and it stays
the record of what the passthrough does today. This design does not depend
on its result: every test below is unit-level and needs no live apply.
**Read at:** marvel `origin/main` `b1f4953`.

## 1. Why

An operator who wants a team's builders on one model and its reviewer on
another has no manifest word for it today. There are two ways to say it, and
marvel understands neither:

- `runtime.args = ["--model", "<id>"]` reaches claude verbatim
  (`internal/runtime/claude.go:99-100`). marvel's context-window resolver
  reads it back through `usage.ModelFromArgs`
  (`internal/usage/limits.go:392-406`), but only by scanning a string list,
  and the args are opaque to every other part of marvel.
- `runtime.env.ANTHROPIC_MODEL` also reaches the pane
  (`internal/runtime/adapter.go:399-411`), but nothing in marvel reads it,
  so the resolver treats the session as naming no model.

Neither way is validated, neither is portable across harnesses (codex and
opencode spell the flag differently), and neither can be compared against the
model the session actually runs. The fact the operator is declaring ("this
role runs this model") has no field of its own, so marvel can neither check
it nor report it.

## 2. Field shape and placement

One optional string on the runtime section, next to its neighbours
`context_window` and `backend`:

```go
// ManifestRuntime (internal/api/manifest.go:152-172)
// Model is the per-role model id or alias the harness is launched with,
// rendered by the adapter as its native flag. Empty means the harness's
// own default, exactly as today. See api.Runtime.Model.
Model string `toml:"model,omitempty" yaml:"model,omitempty"`
```

```go
// api.Runtime (internal/api/types.go:155-197)
Model string `toml:"model,omitempty"`
```

It is copied from the manifest into `api.Runtime` the same way
`ContextWindow` and `Backend` are (`internal/api/manifest.go:648-651`). Manifest:

```toml
    [team.role.runtime]
    image = "claude"
    command = "claude"
    model = "sonnet"
```

```yaml
      runtime:
        image: claude
        command: claude
        model: sonnet
```

Placement is the runtime section, not the role, because the model is a fact
about the harness launch (the same layer as `context_window`, which already
depends on it), and a role's runtime is where a mixed-harness team already
varies. It is spec, so it persists with the team and survives a daemon
restart like the rest of `Runtime`.

A change to `model` behaves like any runtime change: it applies to sessions
spawned after the apply. It does not restart a running session (director
R-154 records that an apply alone leaves a running role unchanged); the
operator rolls it with `marvel shift`.

## 3. Mapping to each runtime

Each adapter declares whether it can render a model and with what flag. The
adapter appends the flag, after the operator's args and alongside marvel's
own flags (`--settings`, the headless flags), so the rendered command line is
deterministic.

| Adapter | Rendering | Basis |
|---|---|---|
| claude | `--model <id>` | `claude --help` on 2.1.284: "Provide an alias for the latest model (e.g. 'fable', 'opus', or 'sonnet') or a model's full name." Works in interactive and headless (`--print`) launches. |
| codex | `-m <id>` | `codex --help` on codex-cli 0.154.0: `-m, --model <MODEL>`. |
| opencode | `-m <provider/model>` | marvel finding-007 records opencode's model keyed on the launch `-m` arg (opencode 1.18.5). Not re-verified here: opencode is not installed on the host this was written on. The builder confirms the flag and the `provider/model` form against `opencode run --help` before the adapter ships. |
| generic | refused | marvel does not know the flag of an arbitrary CLI. |
| simulator | refused | there is no model. |
| forestage | refused | frozen reference adapter; it takes no new surface. |

**Refused means loud, before any spawn.** A role that declares `model` on an
adapter that cannot render it fails the apply with an error naming the team,
the role, and the adapter. It is never silently ignored. The seam has a
precedent: `Manifest.ValidateBudgets` takes a capability predicate from the
runtime package so `internal/api` does not import it, and the daemon's apply
path calls it (`internal/api/manifest.go:430`, `internal/daemon/daemon.go:1156`).
`ValidateModels(renders ModelRenderer)` follows the same shape, called
beside it. Each adapter's `Prepare` re-checks and returns an error, as a
second line for launch paths that do not pass through the apply (for example
`marvel run`). The operator who wants a model on a generic CLI keeps
`runtime.args`, which is exactly what they have today.

## 4. Precedence and conflicts: refuse, do not pick

Decision: **a manifest that states the model twice is refused at apply.**
Specifically:

1. `model` set and `runtime.args` contains `--model`, `--model=`, `-m`, or
   `-m=` (the same four spellings `ModelFromArgs` recognizes, so the
   refusal and the reader can never disagree about what counts as a model
   flag): refused, on every adapter that renders a model.
2. `model` set, the adapter is claude, and `runtime.env` contains
   `ANTHROPIC_MODEL`: refused.
3. `model` unset: nothing changes. Args and env pass through exactly as
   today, so every existing manifest keeps its behaviour.

Why refuse rather than let the field win with a warning:

- Two declarations of one fact are an operator error, and the only honest
  response to an error is to say so. A warning in the daemon log is read by
  nobody at apply time; a refused apply is read by the person who typed it.
- "Field wins" is not even marvel's to guarantee in the env case. Claude
  Code's documented priority order is `/model` during the session, then
  `claude --model` at startup, then `ANTHROPIC_MODEL`, then the settings
  file's `model` field (https://code.claude.com/docs/en/model-config,
  read 2026-09-30). It puts the flag above `ANTHROPIC_MODEL`, so the field would win there, but only because of a
  harness rule marvel does not own and cannot see change. Refusing removes the dependency on that rule.
- It matches how marvel already treats a declared intent that disagrees with
  reality: the `backend` label fails loudly on a mismatch rather than
  choosing one (`api.Runtime.Backend`, `types.go:190-196`).
- It keeps `ModelFromArgs` meaningful: with the field set, the args can hold
  no model flag, so there is exactly one source for accounting to read.

Two sources marvel does not control, stated so nobody mistakes them for
covered: a Policy (the projected settings file) is written verbatim and
never parsed (`CLAUDE.md`, Policy row), so a `model` key inside a policy
fragment is not detected; and an agent can change its model mid-session with
the harness's own command. Both are caught after the fact by section 6's
declared-against-observed comparison, not prevented.

## 5. Usage accounting reads the field

`ResolveGraded` picks its model today as `StreamModel`, else
`ModelFromArgs(req.RuntimeArgs)` (`internal/usage/limits.go:547-549`). The
order becomes:

1. the stream's own model (unchanged; what the harness says it is running),
2. `Runtime.Model`, the declared field (new; `Request` gains a
   `DeclaredModel` string the session manager fills from the session's
   runtime),
3. `ModelFromArgs(args)` (unchanged; the legacy args path),
4. for claude only, `runtime.env.ANTHROPIC_MODEL` (new; `ModelFromEnv`),
   below args because that is claude's own order, flag above env.

Steps 2 and 3 cannot both be non-empty on an applied manifest (section 4), so
their order matters only as defense in depth. Step 4 closes the measured gap
for manifests that already set the model by env and never migrate. Nothing
else in the resolver changes: an id the table does not know still resolves to
`LimitUnresolved` and emits `context.limit-unresolved`
(`internal/events/events.go:100`) rather than guessing a window.

## 6. Model id validation: pass-through, with a shape check

Decision: **no allowlist.** marvel passes the string to the harness and lets
the harness judge it.

Why: the set of valid ids is the vendor's, it changes on the vendor's
cadence, and it differs by backend (the same model has different ids on
different backends, which is BT13's territory). An allowlist in marvel would
be wrong on the day a model ships and would need a marvel release to fix, the
same reason `context_window` exists as an escape hatch from the shipped
table (`types.go:166-171`). The concrete case: the operator's example model
name is not confirmed as an id today, and `claude --help` on 2.1.284
documents only aliases (`sonnet`, `opus`, `fable`) or "a model's full name".
An allowlist would force marvel to rule on that; pass-through lets the
harness do it.

What marvel does check at apply, because these are shape errors no harness
could accept and they would otherwise misrender the command line:

- non-empty after trimming, at most 256 bytes, printable ASCII;
- no whitespace;
- does not begin with `-` (it would parse as a flag);
- for opencode, contains exactly one `/` (the `provider/model` form), pending
  the builder's confirmation in section 3.

How an unknown or wrong id surfaces, in order of when:

1. **At apply:** only shape errors, as above.
2. **At first use:** the harness's own rejection. For a headless role this
   already lands as a failed run through the exit status
   (`succeeded`/`failed`, marvel#246). How interactive claude reports a bad
   id is not measured here; the #407 probe, if the operator runs it, is the
   place to record it.
3. **While running, declared against observed.** marvel already observes
   the model a session runs, but in two different forms:
   - headless sessions: the stream's model, which is an id;
   - interactive claude: `Session.ContextModel` from the statusline feed
     (`internal/api/store.go:747`), which `ctxforward` fills from the
     payload's `model.display_name` (`cmd/marvel/ctxforward.go:68-71`). A
     display name ("Sonnet ...") is not an id and cannot be compared to one.

   The statusline payload also carries `model.id` beside
   `model.display_name` (documented at
   https://code.claude.com/docs/en/statusline, read 2026-09-30), and
   `ctxforward` reads only the display name today. This design has
   `ctxforward` forward `model.id` as well, onto a new
   `Session.ObservedModelID` that the heartbeat writes beside
   `ContextModel`. The `LLM` column keeps the display name.

   The comparison runs only where both sides are ids. When the declared
   value is a full id and an observed id (stream or statusline) normalizes
   (`usage.IdentityKey`) to a different identity, marvel emits one
   `model.mismatch` warning event per session per change. The event is
   advisory and never restarts anything, the same boundary as the
   `(stalled)` advisory. When the declared value is an alias (an alias
   names a moving target), or the only observation is a display name,
   marvel records both values and does not compare. A statusline feed from
   an older harness that sends no `model.id` degrades to the display-name
   case.
4. **On describe:** `marvel describe session` shows `Model: declared <x>,
   observed <y>` (either may be `-`). The `LLM` column keeps showing the
   observed value, because the column's job is what is running.

## 7. Tests the builder writes (red first, unit level, no live apply)

Each fails on `b1f4953` because the field does not exist.

**Manifest parse (`internal/api`)**
- TOML and YAML each parse `model` into `api.Runtime.Model`
  (table-driven over both formats).
- An absent `model` parses to `""`, and an existing manifest fixture
  round-trips byte-identical in behaviour (no model flag rendered).
- Shape refusals: empty after trim, whitespace, leading `-`, over-long,
  non-printable. One case each.

**Apply-time validation (`ValidateModels`, with a fake renderer predicate)**
- A model on an adapter that cannot render one (generic, simulator,
  forestage) is refused, and the error names the team, role, and adapter.
- A nil predicate is a wiring error, as `ValidateBudgets` treats it.

**Precedence (`internal/api`)**
- `model` plus each of `--model x`, `--model=x`, `-m x`, `-m=x` in args:
  refused (four cases).
- `model` plus `ANTHROPIC_MODEL` in env on claude: refused.
- `ANTHROPIC_MODEL` in env on codex with `model` set: not refused by rule 2
  (it is claude's variable); documents the scope of the rule.
- No `model`, `--model` in args: accepted unchanged (legacy path).

**Arg rendering per runtime (`internal/runtime`)**
- claude interactive and claude headless: `--model <id>` present once, and
  marvel's `--settings` and headless flags still present.
- codex: `-m <id>`. opencode: `-m <provider/model>`.
- generic, simulator, forestage `Prepare` with a model set: error returned
  (the second line of defense).
- No model set: rendered args equal today's for every adapter (a golden
  per adapter).

**Usage accounting (`internal/usage`)**
- `DeclaredModel` set, no stream model: resolver keys on the declared model.
- Stream model present and different: stream wins (unchanged behaviour).
- claude with only `ANTHROPIC_MODEL` in env: resolver keys on it
  (`ModelFromEnv`; the gap this closes).
- Unknown id: `LimitUnresolved` and one `context.limit-unresolved` event,
  never a default.

**Declared against observed**
- Declared full id, stream-observed different identity: one
  `model.mismatch` event; a second identical observation does not re-emit.
- Declared full id, statusline payload with a different `model.id`: one
  `model.mismatch` event (interactive path).
- Declared full id, statusline payload with a `display_name` and no
  `model.id`: no event, both values recorded (a display name is not an id).
- `ctxforward` parses `model.id` from a fixture payload shaped like the
  documented one and forwards it; the `LLM` column still renders the
  display name.
- Declared alias: no event, both values recorded.
- Observed equal to declared after `IdentityKey` normalization (for example
  a `[1m]` suffix): no event.

## 8. Out of scope

- **Provider and backend selection** beyond the model string: which vendor
  endpoint, account class, or credential path serves the model. That is
  BT13 and `aae-orc-29f04`. `aae-orc-2lfg` asks for model and provider; this
  design closes the model half only and leaves the provider half there.
- Different models for different replicas of one role. A role is the unit.
- Switching a running session's model. A change applies on the next spawn
  or shift.
- An allowlist or catalogue of model ids (section 6 declines it).
- Fitting a seat's prompt to its model's context limit (marvel#379), which
  this field makes easier to key but does not implement.
- Gemini or any adapter marvel does not ship today.

## 9. Open questions for the builder or the operator

- Whether `model.mismatch` should also be a column flag on
  `marvel get sessions`, or stay on describe and the events ring only.
- Whether a Policy that sets a `model` key should be detected at projection
  time. Today marvel never parses policy content, and this design keeps it
  that way.
