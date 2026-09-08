# finding-037: Codex ignores OTEL env vars entirely — and does not need them, because config is per-process

Date: 2026-09-08
Serves: `aae-orc-rjru`; unblocks `aae-orc-1n6b`, `aae-orc-jcle`
Measured on: mokuzai, macOS arm64, **codex-cli 0.153.4** (rjru's premise was 0.146.0)
Method: two local `nc` listeners, one named by config and one by env, per run

## The question, and the answer

`aae-orc-rjru` asked: does Codex honor standard per-process OTEL env vars
(`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_RESOURCE_ATTRIBUTES`) as an override of,
or supplement to, its user-level `[otel]` config?

**No. Not as an override, not as a supplement, not at all.**

| test | setup | result |
|---|---|---|
| T1 | config → `:4318`, env → `:4319` | **4980 bytes to :4318, 0 to :4319** |
| T2 | no exporter in config, env → `:4319` only | **0 bytes.** Env cannot even enable export |
| T3 | **empty** config.toml, `-c otel.exporter=…` → `:4320` | **30993 bytes to :4320** |

T1 also shows `OTEL_RESOURCE_ATTRIBUTES` never reaches the wire: none of
`marvel.session`, `rjru-env` or the env-set `service.name` appear in the
payload. The `OTEL_*` strings in the binary come from the linked
`opentelemetry-rust` SDK; they are not evidence that Codex's exporter is built
through the env-reading path, and T1/T2 show it is not.

## Why this does NOT force rjru's bad options

rjru framed the fallback as: (a) marvel writes/manages `~/.codex/config.toml`
— invasive, a SOUL §1 user-sovereignty problem; (b) accept one shared endpoint
for all Codex sessions and correlate by `conversation.id`; (c) leave Codex on
the stream path only.

**There is a fourth option the ticket did not consider, and it is the good
one: per-process configuration.** Codex 0.153.4 offers two independent
mechanisms, both measured working:

1. **`-c` dotted-path overrides at spawn.** T3 exported to a CLI-named
   endpoint with an EMPTY `config.toml`:
   `-c 'otel.exporter={"otlp-http"={endpoint="http://…",protocol="json"}}'`.
   Flag injection at spawn is exactly what marvel's runtime adapters already
   do, so this needs no new mechanism.
2. **`CODEX_HOME` per session.** T1 read its config from a scratch
   `CODEX_HOME`, never touching the operator's real `~/.codex/`. A per-session
   config directory is a normal thing for marvel to construct — it is the same
   permission-through-environment shape as enforcement locus 1.

Either one gives per-session endpoints and per-session tagging without
mutating one byte of user-global state. **Option (a)'s user-sovereignty
objection is moot, and (b)'s single-endpoint concession is unnecessary.**

## The `[otel]` config surface in 0.153.4

Recovered from the binary and confirmed by use:

```
[otel]
environment      = "<string>"          # rides to the wire (verified)
exporter         = none | statsig | otlp-http | otlp-grpc
trace_exporter   / metrics_exporter    # same kinds, split by signal
span_attributes  = { "k" = "v" }       # see UNRESOLVED below
tracestate, tool_result, log_user_prompt
[otel.exporter.otlp-http]              # endpoint, headers, protocol, tls
                                       # protocol = binary | json
```

Attributes observed riding natively, no configuration required:
`conversation.id`, `host.name`, `service.name`, `service.version`,
`app.version`, plus a rich `auth.*` and `codex.*` span vocabulary
(`codex.conversation_starts`, `codex.startup_phase`, `codex.user_prompt`).

`conversation.id` being native means rjru's option (b) correlation key exists
regardless of which mechanism is chosen — useful as a cross-check, not as the
only handle.

## UNRESOLVED: span_attributes did not propagate

`otel.span_attributes` did not reach the payload in either form — as a `-c`
override or in `config.toml` — while `otel.environment` set the same two ways
did. Two readings, untested between:

1. The syntax is wrong (it parsed without error, but a silently-ignored
   unknown shape would look identical).
2. It applies to spans this run never emitted. **This host is not logged in to
   Codex**, so every run terminated in the auth path and only startup/auth/
   error spans were exported. A logged-in run emitting turn spans may well
   carry them.

Reading 2 is the more likely and is cheap to settle: re-run T3 on a
Codex-authenticated host and grep the payload. Until then, per-session tagging
should be assumed to rest on `otel.environment` + `conversation.id`, both
verified, rather than on `span_attributes`.

## Scope and caveats

- **Version drift is real.** rjru's premise was 0.146.0 "enforces user-level
  config, explicitly ignoring project-level config"; this is 0.153.4. I did
  not test project-level (`.codex/` in cwd) config, because `-c` and
  `CODEX_HOME` already answer the operational question and do not depend on it.
- Not logged in, so no turn-level telemetry was observed (see UNRESOLVED).
- Endpoints were raw `nc` listeners, so this measures what Codex *sends* and
  where, not that a real collector accepts it.

## Recommendation for the OTEL arc

`aae-orc-1n6b` (per-harness telemetry advertisement) should treat Codex as
**advertisable per session**, not as the awkward exception rjru feared. That
strengthens topology shapes (a) and (b) in `aae-orc-fkvv` — marvel injecting
endpoint config per session is viable for Codex as well as Claude Code — and
removes the argument that Codex forces a shared-collector design.
