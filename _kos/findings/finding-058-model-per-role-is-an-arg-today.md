# finding-058: a claude role's model is a runtime arg today, and marvel records the model the harness declares, not the one that served

Probe: `_kos/probes/probe-model-per-role-claude.md` (brief merged in #407 as
0245681, results in #432 as 0b07145). Ticket: `aae-orc-2lfg`, the claude half
of its premise; this finding does not close it. Code cites are at marvel
origin/main ee7666d unless a line says otherwise.

**Provenance.** I did not run the probe. The director ran it on the
operator's grant and tore it down. The supervisor and the reviewer each read
the cluster's event ring afterwards, read-only, and quoted the lines this
finding relies on. Each claim below names which of those it rests on. I
verified every code claim myself.

## What the run showed

| role | `--model` arg | init model (ring) | ContextModel (director) | exit |
|---|---|---|---|---|
| `args-sonnet-5` | `claude-sonnet-5` | `claude-sonnet-5` | `claude-sonnet-5` | 0 |
| `args-sonnet-5-5` | `claude-sonnet-5-5` | `claude-sonnet-5-5` | `claude-sonnet-5-5` | 0 |
| `args-alias-sonnet` | `sonnet` | `claude-sonnet-5-5` | `claude-sonnet-5-5` | 0 |

- **H1 holds at the harness init report.** `runtime.args: ["--model", "<id>"]`
  selects the model per role with no marvel change. The claude adapter copies
  `Runtime.Args` verbatim and never adds `--model`
  (`internal/runtime/claude.go:99-100`). Each role's `agent.session.started` line
  names the requested model.
- **H0 is excluded at the same level.** Two distinct init models in one run,
  each equal to its arg, cannot both be one account default, and marvel
  passed each flag through. The account default itself was never recorded.
- **The served model was not observed.** No response's `message.model` was
  captured; the ring's `agent.turn.completed` lines carry no model (the
  reviewer's ring read). So the run shows the harness was told the model
  and declared it. It does not show which model served the tokens.
- **H2, the `ANTHROPIC_MODEL` env role, was not run.** It stays open as an
  optional follow-up.

## What ContextModel is, and what it is not

ContextModel is the harness's report only from the init line onward:

1. **At launch it echoes the arg.** `Bind` resolves with no stream model, so
   `ResolveGraded` falls back to `ModelFromArgs`, which returns the `--model`
   value verbatim with no alias resolution (`internal/usage/limits.go:546-549`).
2. **The init line overwrites it.** The claude parser takes `model` from
   `system/init` and emits `session.started`
   (`internal/runtime/claudecode/parser.go:150-178`). `observeStart`
   re-resolves with that as the stream model and sets `rawModel`
   (`internal/usage/accountant.go:290-317`), which `describe` shows as
   ContextModel (`:774`).
3. **The served model never replaces it.** The parser captures each
   response's `message.model` (`parser.go:238`), but the accountant adopts a
   sample's model only when none is set (`accountant.go:396`).

So for a claude session ContextModel is the declared model. The alias role
is the one row where ContextModel alone proves the harness wrote it,
because `claude-sonnet-5-5` differs from both the arg and marvel's own alias
table. For the two full-id rows ContextModel equals the arg, and only the
`session.started` lines separate an init report from the echo.

## Three defects the run exposed

None of these was the probe's subject. Each is recorded here so it is not
lost; whether each gets a ticket is a separate call.

1. **The alias table is behind the harness.** `aliases` maps `sonnet` to
   `claude-sonnet-5` (`internal/usage/limits.go:336`, "bumped 2026-08-30"),
   but the harness resolved `sonnet` to `claude-sonnet-5-5`. Until init
   arrives, and for any harness that never names a model, a role launched
   with `--model sonnet` is keyed to the wrong model for its window lookup.
2. **`claude-sonnet-5-5` has no context-window entry.** The table holds
   `claude-sonnet-5` and `claude-sonnet-5[1m]` and no `claude-sonnet-5-5`
   (`internal/usage/limits.go:219-240`), and it is not an alias key, so the
   table and alias rungs both miss (`:590-622`). The reviewer's ring read
   shows `context.limit-unresolved ... no window resolved for model
   "claude-sonnet-5-5"; CTX% stays blank` for both 5-5 sessions and none for
   `claude-sonnet-5` (emitted at `internal/usage/accountant.go:274-284`).
   The blank is narrower than "every Sonnet 5.5 role", and I checked why:
   claude declares `contextWindow` on its terminal result line
   (`parser.go:430`), which the stream rung takes and the resolver then
   learns for later sessions on the same daemon (`limits.go:551-576`,
   held in an in-memory map, `:468`); and an interactive session gets the
   statusline's `context_window_size` through the feed rung
   (`cmd/marvel/ctxforward.go:192`). So the blank covers a headless session
   until its first result line, which for a one-turn role is its whole
   life, and every session on a daemon that has not yet learned the window.
   A table entry closes it.
3. **The claude parser does not map four current stream types.** The
   reviewer counted 18 `agent.error` warnings reading `unknown system
   subtype: "hook_started"` or `"hook_response"`, plus `"commands_changed"`
   and `unknown vendor event type: "rate_limit_event"`, across three
   one-turn sessions (messages built at `parser.go:146` and `:155`). They are
   noise at warning severity on every plain headless run.

## What this means for aae-orc-2lfg

For claude the capability exists today, so the remaining work is ergonomics
and validation, not new plumbing. A first-class `runtime.model` field
(designed in #412) would at least need the alias table and the window table
kept current with the harness, because both are keyed off the model name and
both were stale for the model this run picked.
