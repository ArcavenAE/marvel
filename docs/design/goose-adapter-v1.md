# goose adapter v1: design brief

- **Status:** proposed, 2026-10-10. Nothing here is built. The tickets in section 10 are listed, not filed: filing waits on the harness plan's decision D8.
- **Author seat:** architect, team arcaven, as chair of a four-round design party. The seats were goose, adapter, telemetry, containment and a second harness (gemini-cli). Round 4 was a forced vote and every item carried; the reason store carried 3-2, with the dissent in section 9.
- **Pins:** marvel main 493de32; goose v1.54.0 at cd643d2 (released 2026-10-08), read in source through the GitHub API on 2026-10-10. Nothing was installed or run.
- **Companion:** `docs/adapter-kit-checklist.md`, the lines any new harness walks.

## 1. Why, and what v1 is

goose ranked first among the harnesses beyond claude and codex in the fleet's harness survey: native OpenTelemetry, auth delegated to the provider, and open governance. The fleet has never run it, and today marvel would launch it through the generic adapter at whatever `GOOSE_MODE` the host has. goose's default is `auto`, which allows every tool. This brief fixes the shape of a first adapter before anyone builds one, so that the first goose seat launches with a known posture.

v1 is **headless only**, **stream-capable**, **bound by a private home**, and **posture-explicit**. An interactive goose role launches plain, but still gets `GOOSE_MODE` and the private home.

## 2. What the source shows at v1.54.0

Paths are in the goose repository (`aaif-goose/goose`; the old `block/goose` name redirects). SES is `crates/goose-cli/src/session/mod.rs`.

| # | fact | source |
|---|---|---|
| G1 | `goose run --output-format stream-json` writes one JSON object per line: `message`, `notification`, `error`, `complete` | SES:147-174 |
| G2 | `complete` carries session-cumulative token totals and cost, and is written only when there was no terminal error | SES:1602-1636 |
| G3 | per-request usage is never streamed: `AgentEvent::Usage` is kept, `MessageUsage` is dropped | SES:1488-1494 |
| G4 | in headless `approve` or `smart_approve`, the first tool that needs approval ends the run with an error. It returns before the stream `error` emit, so it leaves neither `error` nor `complete` | SES:1357-1373, :1546-1556 |
| G5 | in headless modes other than those two, a raised confirmation is auto-allowed with a warning | SES:1375-1378 |
| G6 | `auto` allows every tool without reading `permission.yaml`; `never_allow` is honoured only in `approve` and `smart_approve`; `chat` skips tools | `crates/goose/src/permission/permission_inspector.rs:157-171` |
| G7 | in `chat`, a skipped tool call is reported to the model as a success | `crates/goose/src/agents/state_machine/ops_unknown_tool.rs:97-104` |
| G8 | every CLI failure exits 1: 31 `process::exit(1)` sites across four files, and `main` returning an error. No other code | `cli.rs`, `session/builder.rs`, `recipes/recipe.rs`, `recipes/extract_from_cli.rs`; `main.rs:37-56` |
| G9 | `GOOSE_PATH_ROOT` moves config, data and state under one root, and is ignored unless absolute | `crates/goose/src/config/paths.rs:8-17`, :40-46 |
| G10 | the keyring service name is fixed as `goose`, so a private root does not move the keyring entry | `crates/goose/src/config/base.rs:29-32` |
| G11 | goose mints its own session id (`YYYYMMDD_N`). `--session-id` is refused without `--resume`. Names are not unique. A bare `--resume` takes the most recent User session in the store | `cli.rs:413-476` |
| G12 | sessions live in `<data>/sessions/sessions.db`, schema 16 at this tag (17 on main) | `crates/goose/src/session/session_manager.rs:28` |
| G13 | the git tags `stable` and `canary` point at 89d464c (2025-01-17), far behind the release line | GitHub tags API |
| G14 | `GOOSE_DISABLE_SESSION_NAMING` avoids a background model call | environment-variables doc at the tag, :142 |

And in marvel at 493de32:

| # | fact | source |
|---|---|---|
| M1 | a `Prepare` error logs and falls back to `directPlan`, which launches the raw command | `internal/session/manager.go:995-999` |
| M2 | the session home is per session key, reused across restarts | `internal/session/manager.go:798-802` |
| M3 | the reap kills every dead pane marvel created, so stderr left in the pane is lost at the next reap pass | `internal/session/manager.go:1500-1539` |
| M4 | marvel never resumes a harness session by id; `HarnessSessionID` binds the current launch to its transcript and is re-minted every launch | `internal/api/types.go:300-311`; `git grep -- --resume internal cmd` matches only claude's identity check |
| M5 | spend alone never stamps `ContextAt` | `internal/api/store.go:851-860` |
| M6 | marvel has no OTLP receiver | `internal/otel/` holds `metrics.go` only |

Two design rows in `docs/design/seat-bootstrap.md` disagree with source and are amended by ticket G. Row :771 (goose, "approve: park expected") is wrong: headless `approve` refuses (G4) and does not park. Row :770 (gemini-cli, "headless: fail closed (exits)") is also wrong: a denied call is non-fatal and the run continues (gemini-cli v0.63.0, `packages/core/src/tools/tool-error.ts:100-109`).

## 3. The adapter

**Optional interfaces:**

| interface | v1 | why |
|---|---|---|
| `StreamCapable` | yes | G1 |
| `SessionHomeAssigner` | yes | an absolute `GOOSE_PATH_ROOT` (G9); marvel builds the path, and a relative or empty value is a posture fault |
| `SessionIDAssigner` | no | goose mints its own id and names are not unique (G11). marvel never addresses a launch but the newest (M4) |
| `StatuslineFeeder` | no | goose has no statusline feed |
| `ComposerCapable` | no | headless only |
| `ForegroundRule` | no | not measured |
| `ProjectionFor` | false | goose takes no settings-path flag; config is seeded into the private home |

**Launch line (headless):**

```
goose run --output-format stream-json -q -t <prompt> < /dev/null > <fifo> 2> <home>/stderr-<launch>.log
```

`--resume` is added on relaunch; in a private root it takes this seat's most recent session (G11). `-n` may be passed as a label for humans, never as a pin. `-q` keeps the session banner off stdout, and the parser still skips any line that is not JSON.

**Projected env:**

| key | value | source |
|---|---|---|
| `GOOSE_PATH_ROOT` | absolute session home | G9 |
| `GOOSE_MODE` | from the role's declared posture; adapter-wins over role env, and a conflicting role value is a posture fault | G6 |
| `GOOSE_PROVIDER`, `GOOSE_MODEL` | from the role | goose environment-variables doc |
| `GOOSE_CONTEXT_LIMIT` | from `runtime.context_window` when set | goose environment-variables doc |
| `OTEL_SDK_DISABLED` | `true` | M6 |
| `GOOSE_TELEMETRY_ENABLED` | `false` | goose environment-variables doc |
| `GOOSE_DISABLE_SESSION_NAMING` | `1` | G14 |

No provider key is placed in env, args, seeded files or fixtures (section 6).

**Seeded files** (`SessionHomeSpec.Seed`): `config/config.yaml` with mode, provider, model and extensions; and `config/permission.yaml`, which puts the role's tools under `always_allow` and the deny list plus `manage_extensions` under `never_allow`. Extensions are narrowed with `--no-profile --with-builtin <role list>`.

**Parser** (`goose/stream-json`, `internal/runtime/goose/` with `mapping.md` and live fixtures). `message` and `notification` map to the turn and tool kinds. `error` maps to `KindError`. `complete` maps to `session.ended` with exit code 0 and cumulative usage. That is a vendor terminal line, as claude's result line is, and it stays out of the level fold.

**The three outcomes of a headless run.** `complete` means success. An `error` line means failure, and its text is the reason. Neither line with exit 1 means a refusal before or at the first tool (G4); the reason is in the per-launch stderr file. Exit 0 without `complete` is never clean.

**The failure reason.** It is kept in the per-launch stderr file in the session home, which survives the reap (M2, M3). The file is size-capped. The reap reads its last lines into the run record after a scan for credential shapes: goose#12413, about keys leaking into logs, is open. A generic pane-tail read before the reap, for every headless harness, is a separate ticket (E2). This carried 3-2; the dissent is in section 9.

## 4. Posture and refusal

**Postures.**
- *Fail-closed allowlist*, the unattended default: `GOOSE_MODE=approve` plus the seeded `permission.yaml`. Any unlisted tool ends the run with exit 1 (G4).
- *Auto in a sandbox*: `GOOSE_MODE=auto`, only inside the harness plan's D1 sandbox (ticket K). It is recorded as "deny list not enforced", because auto does not read the list (G6).

**Not contained.** An allowed `developer__shell` runs anything a shell can run. Shell argument rules are an open request upstream (goose#11399). An `approve` seat is bounded by its tool list, not contained by it; containment is the sandbox.

**Posture faults:**

| reason | condition |
|---|---|
| `no-posture` | the role declares no posture |
| `smart-approve-headless` | `smart_approve` on an unattended role. It errors headless anyway (G4), and swapping in `approve` would be a silent posture rewrite by marvel |
| `relative-root` | the home is relative, empty, or cannot be made, for a role whose posture needs a seed |
| `role-env-conflict` | a role env value overrides the adapter's `GOOSE_MODE` |
| `auto-with-deny-list` | `auto` declared with a deny list it would ignore |

**Before K1** (the bootstrap refusal path, `docs/design/seat-bootstrap.md` section 5b): a posture fault never returns a `Prepare` error, because that launches the raw command (M1). `Prepare` instead returns a refusal stub. The stub never runs goose, writes the reason to stderr, and exits 78, a marvel-reserved code that goose never returns (G8). The reap records it as crashed with exit status 78, and it reruns under backoff, with no spend, until K8's refusal re-evaluation. This was voted 5-0 over launching `approve` as a degrade: the degrade also exits 1, reruns under the same backoff, and pays for tokens on every pass.

**Never** degrade to `chat`, or to any mode that skips or denies tools and still exits 0. `chat` reports skipped calls as success (G7), and exit 0 reads as succeeded and holds the slot (`manager.go:1553-1580`).

**After K1:** the refusals move to the bootstrap refusal path (ticket D2).

## 5. Context

The stream carries no per-request level (G3). So a headless goose seat on the stream alone shows no `ContextAt` for its whole run (M5): LAST-ACTIVE reads `-` and the watchdog sees it as quiet. Spend appears only once, at `complete`. `max_tokens` is checked at admission, so goose spend counts only against the next admission (`docs/feature-matrix.md:99`).

The only level channel is the sessions database. Ticket H is a read-only reader of the seat's private `sessions.db`, with schema 16 pinned and an unknown version refused. It picks this launch's row by a bound known at launch (created at or after launch time, User session type, no parent), not by recency alone. It reads the level columns, which are written per request (`session_manager.rs:2400-2426`), and sends a heartbeat with level and window. The private root also keeps the seat out of the shared-database lock reported in goose#12473.

OTEL is not the channel in v1: marvel has no receiver (M6). Export is off and content capture is never on.

## 6. Custody (ADR-009)

The provider key is bearer authority at a third party, so marvel never holds it, never puts it in env, args, seeded files or fixtures, and never copies it.

A private `GOOSE_PATH_ROOT` does not move the keyring entry (G10). A goose seat therefore reads the operator's own goose keyring entry: the operator's credential, used by the operator's own seat. That is not marvel custody, but it is not isolation either, and it needs the operator's acknowledgement (section 11). Where the operator uses file storage, `config/secrets.yaml` is linked into the home, never copied (`SessionHomeSpec.LinkIn`).

For the probe's credentials, short-lived Bedrock credentials fit the brokering clause best. A per-trial API key is acceptable only if the operator or a vault holds it and can revoke it.

## 7. The pin

goose v1.54.0 at cd643d2, released 2026-10-08. Never `stable` (G13). This recommendation is valid until 2026-10-24; the research role re-checks it then. goose ships a minor about weekly: 15 minors in 14 weeks, v1.40.0 to v1.54.0. If the probe runs later, the pin moves to the tag the probe ran. The re-check reads:
- SES:1357-1378, SES:1546-1556 and :1602, SES:147-174, and SES:1488-1494;
- the emit pass in `crates/goose/src/agents/state_machine/session.rs:108-140`;
- `permission_inspector.rs:157-171`, `cli.rs:403-476` and `paths.rs:40-46`;
- `CURRENT_SCHEMA_VERSION`;
- the releases list, including the dormant `v2.0.0-rc-04-27-0`.

## 8. The held probe

HELD. It needs the harness plan's D1 (sandbox) and D3 (trial credentials, a spend cap, a host). It is written so that it can run unchanged once those are ruled.

- **Command:** `goose run --output-format stream-json`, with a fixed one-line instruction. The provider is API-key or Bedrock, not ACP: ACP has no resume or fork and signs in through the vendor CLI. It runs in a throwaway workdir inside the D1 sandbox, at the pin, with `GOOSE_MODE` set explicitly and a scoped, revocable key and spend cap per D3.
- **P1.** `approve` meets an unlisted tool: the exit code, the stderr text, and whether any `error` or `complete` line is written. Decides the stub-versus-degrade premise and the reason store.
- **P2.** A forced provider error: the `error` line, the exit code and stderr.
- **P3.** `approve` meets a `never_allow` tool: what the model is told, whether the run continues, the exit code, and any line countable as a denial.
- **P4.** An absolute `GOOSE_PATH_ROOT` with a seeded `permission.yaml`: the file is read, nothing is written under the operator's goose directories, the key is still found through the keyring, and the rows are counted by `session_type` and `parent_session_id`.
- **P5.** Two launches in one absolute root, with no `-n` or `--resume`: two rows exist, the newest belongs to the running launch, and its `working_dir` is the seat's.
- **P6.** A run of two or more requests: any per-request usage in the stream, and the row's level columns against the `complete` totals (cache inside or beside input).
- **Fixtures:** the banner on stdout without `-q`; the exit at `--max-turns`; a relative root falling back; the chat-mode exit; the `auto-allowing` warning; `manage_extensions` under `never_allow`; stdin under `-t`; `goose_mode` read back from the row; `-n` on two runs; workdir hint files (`.goosehints`, `AGENTS.md`); a stub launch exiting 78, recorded with its reason intact.

## 9. Votes and dissent

| item | result |
|---|---|
| posture fault before K1: refusal stub, exit 78 | 5-0 |
| checklist line 24, "skips or denies tools and still exits 0" | 5-0 |
| reason store: per-launch stderr file in ticket C, generic pane-tail read as E2 | 3-2 |
| `smart_approve` on an unattended role is a posture fault | 5-0 |
| checklist (29 lines), held probe, tickets, the pin | 5-0 each |
| ticket D split into D and D2 | 5-0 |

**Dissent on the reason store** (the adapter and containment seats): one generic reap capture would cover every headless harness, and it would leave stderr in the pane, the operator's window. A stderr file in the session home is one more place a key can land while goose#12413 is open. The adopted design keeps their two safeguards, a size cap and a credential-shape scan before storage, and E2 stays on the board with no blocker.

## 10. Tickets (proposed, flat, with edges; not filed until D8)

| ref | title | blocked by |
|---|---|---|
| A | goose v1.54.0 characterization probe: P1 to P6 plus the fixtures | harness-plan decisions D1, D3 |
| B | `goose/stream-json` parser: `mapping.md` at the pin, live fixtures, `complete` as a cumulative terminal mapped to `session.ended` | A |
| C | goose adapter v1, headless: `StreamCapable`, `SessionHomeAssigner` (absolute `GOOSE_PATH_ROOT`), a per-launch size-capped stderr file read at reap with a credential-shape scan, the goose matrix column | A, B |
| D | goose posture projection with the pre-K1 refusal stub (exit 78) as shared runtime code: `GOOSE_MODE`, the seeded `permission.yaml`, and the five refusal reasons | A, C |
| D2 | move goose posture refusals from the stub to the bootstrap refusal path | K1, D |
| E2 | generic: the reap reads a bounded, credential-scanned pane tail before it kills a dead headless pane, for every harness | none |
| F | adopt `docs/adapter-kit-checklist.md` as the adapter kit | none |
| G | amend `docs/design/seat-bootstrap.md` rows :770 and :771 and the matrix goose line to the source. Row :770 becomes "fails closed per call; the run continues and exits 0". This covers the goose and gemini-cli parts of K9 | none |
| H | `marvel goose-ctx`: a read-only, schema-pinned `sessions.db` reader with a level-and-window heartbeat | A, C |
| I | gemini-cli adapter v1 against the checklist | the harness plan's T7 probe, F, D |
| J | pi adapter v1 against the checklist | the harness plan's T6 probe, F |
| K | an `auto`-posture goose seat launches only inside the D1 sandbox | harness-plan decisions D1 and D2, and the sandbox build |

There is no ticket E: the goose-specific reason store folded into C. Not tracked in bd: the pin re-check is the dated recommendation in section 7. An upstream ask that goose emit per-request `Usage` in stream-json would be a GitHub issue after A, under the fleet's upstream claim gate.

**Drafted matrix cells for the goose column** (ticket C makes them true and cites them): `StreamCapable` yes; `SessionIDAssigner` no; `SessionHomeAssigner` yes; `StatuslineFeeder` no; `ComposerCapable` no; `ForegroundRule` no; context window read, headless: no until H, then partial; context window read, interactive: no until H; Tin cumulative: headless, from `complete` only; Tin current level: no until H.

## 11. Rulings needed

- D1, the sandbox; D3, trial credentials, spend cap and host, asked once for this probe and the gemini-cli T7 probe. Pin dates: goose v1.54.0 to 2026-10-24; gemini-cli v0.63.0 at 5738466 to 2026-10-17.
- D8, filing the tickets above.
- K6's `unattended` role field, which the posture rules read.
- The operator's acknowledgement that a goose seat reads the operator's own goose keyring entry (section 6).
- Acceptance that the refusal stub reruns under backoff, at no spend, until K8.

## 12. Second harness, in one table

| | goose v1.54.0 | gemini-cli v0.63.0 |
|---|---|---|
| a denied call | ends the run in headless `approve`, exit 1 | non-fatal; the run continues and exits 0 |
| caller session id | no (`-n` is a label) | yes, `--session-id` (in source, not in the docs) |
| exit codes | 1 for every failure | 41 to 55 and 130, named in source |
| reason channel | stderr file (no stream line on refusal) | stream `error` and `result` events |
| per-request level | sessions database only | telemetry outfile only |

**Not in v1:** interactive context, OTEL ingest, ACP providers, `SessionIDAssigner`, composer, foreground rules, and shell argument rules.
