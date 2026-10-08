# Feature matrix: what marvel does for which harness

Which marvel features work for which harness, and which of them need another service to work at all. It is current as of main at commit 988cd9a. Every cell is backed in the Evidence list under its table, by a code location or by a finding that measured it. Where nothing established a cell it says `unknown`; nothing here is a guess.

## How to read it

- **Columns** are the adapters marvel registers (`internal/runtime/adapter.go:355-360`). `forestage` is a frozen reference adapter and not a live target (`CLAUDE.md`, Independence and Coupling); its cells record what the adapter code does. `generic` is the fallback for any other CLI, and it also stands for a harness run through a wrapper command. Harnesses with no adapter are listed under [Not adapted](#not-adapted).
- **Values.** `yes`: the code does it. `partial`: it works with a stated limit, given in the Evidence. `no`: it does not, and the Evidence says why. `n/a`: the question does not apply. `unknown`: no code or finding settles it. A `yes` rests on code; the Evidence says where a finding measured it, and where none did it says "code only".
- **Needs** is the service a feature depends on besides the daemon and tmux, which every row assumes. `none` means the daemon and tmux are enough. Anything else is a requirement: `needs bus` is the NATS bus (a registered Service, class `message-bus`, provider `nats-server`: `internal/service/service.go:103-123`), `needs director` is the director shim, which lives outside this repository, and `needs hub` is the global hub. Director is not a registered Service.
- **Interactive and headless** differ for several rows, and the cell says which where they do.

## Adapter capabilities (checked against the code)

This table is derived from the registry by a test, so it cannot drift. A new adapter, or a changed capability, fails `TestFeatureMatrixAdapterTableMatchesTheCode` until it is regenerated with `MARVEL_UPDATE_MATRIX=1 go test ./internal/runtime -run TestFeatureMatrix`.

<!-- adapter-capabilities:begin -->
| Capability | claude | codex | opencode | forestage | simulator | generic |
|---|---|---|---|---|---|---|
| StreamCapable | yes | yes | yes | no | no | no |
| SessionIDAssigner | yes | no | no | no | no | no |
| SessionHomeAssigner | no | yes | no | no | no | no |
| StatuslineFeeder | yes | no | no | yes | no | no |
| ComposerCapable | yes | yes | yes | no | no | no |
| ForegroundRule | yes | no | no | no | no | no |
<!-- adapter-capabilities:end -->

What each interface means: `StreamCapable` reads a headless harness's structured output (`internal/runtime/stream.go:52`). `SessionIDAssigner` pins the harness session id (`internal/runtime/adapter.go:219`). `SessionHomeAssigner` gives the session a private state home (`internal/runtime/adapter.go:193`). `StatuslineFeeder` builds the statusline command that feeds context readings (`internal/runtime/adapter.go:237`). `ComposerCapable` declares how the prompt composer takes input (`internal/runtime/adapter.go:321`). `ForegroundRule` says which pane command is the harness (`internal/runtime/adapter.go:631`).

## Shift change

| Feature | claude | codex | opencode | forestage | simulator | generic | Needs |
|---|---|---|---|---|---|---|---|
| shift (rolling replacement) | yes | yes | yes | yes | yes | yes | none |
| max-age rotation (request, marker, shift) | yes | partial | partial | unknown | unknown | unknown | none |
| auto-shift on context pressure | yes | partial | partial | yes | no | no | none |
| handoff put (request typed into the pane) | yes | yes | yes | unknown | unknown | unknown | none |
| handoff read (marker file) | yes | yes | yes | yes | yes | yes | none |
| succession (`MARVEL_PREDECESSOR`, `MARVEL_HANDOFF_REQUESTED_AT`) | yes | yes | yes | yes | yes | yes | none |
| per-seat replace | no | no | no | no | no | no | none |

Evidence:

- **shift.** No harness branch: `internal/team/controller.go:2276` builds successors from the role's runtime alone. It needs only the daemon and tmux (`cmd/marvel/main.go:1604`, `internal/daemon/daemon.go:2175`). Budget admission applies only to a team that declares a budget (`internal/team/controller.go:1905`). A heartbeat readiness gate exists only for a role that declares a heartbeat healthcheck (`internal/team/controller.go:2428`), and a `generic` seat does not heartbeat (`internal/runtime/generic.go:104`).
- **max-age rotation.** The code has no harness branch (`internal/team/shift_handoff.go:82-205`), is refused at apply for headless roles and for roles with more than one replica (`internal/api/manifest_shift.go:175-179`), and has a 15 minute floor (`internal/api/types.go:692`). The request typing was measured for claude, codex and opencode in finding-064. `partial` for codex and opencode: the request is typed, but the seat may be unable to write the marker file (codex at `-s read-only`, opencode), and then the result is a repeating `handoff-missing`, never a shift (finding-067). No finding measures a claude seat writing the marker end to end. Escalation reaches the team's supervisor role as an in-memory event; carrying it over the bus is design only (`docs/design/handoff-missing-delivery.md:3`).
- **auto-shift on context pressure.** The trigger arms only with a resolved window (`ContextLimit > 0`) and a reading newer than 10 minutes (`internal/team/controller.go:2129-2137`, `internal/api/quiet.go:10`). claude: finding-044; interactive needs `context_feed = "statusline"`, headless reads its stream. codex `partial`: interactive only, through the seeded hooks (`internal/runtime/codex_home.go:22-27`); headless `exec` totals are cumulative and give no level (`internal/usage/profiles.go:72`); whether hooks fire under `exec` is unchecked (`docs/design/unresolved-window-context-pressure.md:29`). opencode `partial`: headless, and only when the manifest sets `context_window` (`docs/design/unresolved-window-context-pressure.md:30-34`, `internal/usage/limits.go:277`). forestage: the marvel side is wired (`internal/runtime/forestage.go:43`); code only, forestage's side is unmeasured. simulator `no`: it heartbeats a percentage only, so the trigger never arms (`internal/api/store.go:772-774`). generic `no`: no projection surface (`internal/runtime/generic.go:100-101`).
- **handoff put.** The request is typed with a bracketed paste then Enter (`internal/tmux/driver.go:776-789`), after a pre-flight refusal check (`internal/daemon/daemon.go:468-490`). Measured for claude, codex and opencode in finding-064. A codex seat showing its update menu is refused (`internal/daemon/daemon.go:2385`). forestage, simulator, generic: no composer contract and no measurement (`internal/runtime/adapter.go:326-329`). The pre-flight reader exists for claude and codex only (`internal/composer/composer.go:57-66`).
- **handoff read.** One local regular file, no symlink or FIFO, found by a path template with `{session}` (`internal/team/shift_handoff.go:331-358`). No harness branch. Whether a seat can write that file is the separate question under max-age rotation.
- **succession.** The two variables come from the launch environment every adapter builds (`internal/runtime/adapter.go:384`, `internal/runtime/adapter.go:427-432`), names at `internal/api/types.go:781-782`. `HandoffRequestedAt` is set only when the predecessor was asked (`internal/team/controller.go:2305-2318`). The successor side of the contract is unmeasured per harness.
- **per-seat replace.** Design only: `docs/design/per-seat-replace.md:3`. A max-age or context-pressure shift on a multi-replica role still drains every seat (`internal/team/controller.go:2435-2445`).

## Observability

| Feature | claude | codex | opencode | forestage | simulator | generic | Needs |
|---|---|---|---|---|---|---|---|
| context window read, interactive | partial | yes | no | yes | partial | no | none |
| context window read, headless | yes | no | partial | no | n/a | no | none |
| memory (RSS) | yes | yes | yes | yes | yes | yes | none |
| CPU | yes | yes | yes | yes | yes | yes | none |
| Tin, tokens in, cumulative | headless | headless | headless | no | no | no | none |
| Tin, tokens in, current context level | partial | yes | no | yes | no | no | none |
| Tout, tokens out, cumulative | headless | headless | headless | no | no | no | none |

Tin is tokens in and Tout is tokens out, as the operator ruled. The counts do not compare across harnesses, which is why `get sessions` has no Tin column: raw input is not comparable, because codex counts cached input inside input while claude adds it on top (`docs/design/get-sessions-output.md:141-144`). marvel keeps `PromptTokens`, normalized for each harness's layout, as the one input figure that can be added up (`internal/usage/reader.go:28-47`). Compare Tin across harnesses only through that field, and read Tout as output tokens only, as each harness reports them.

How each harness reports tokens (headless only; a seat with no token stream shows `-`, which is not zero):

| Harness | Reported | Granularity | Cache in the input count | Source |
|---|---|---|---|---|
| claude | headless: yes. Interactive: context level only | per request, summed | alongside: cache classes are added to input (additive) | `internal/runtime/claudecode/parser.go:289`, `internal/usage/profiles.go:38-45` |
| codex | headless: yes. Interactive: context level only | a running session total, so a new report replaces the old one | inside: cached input is part of input (subsumptive) | `internal/runtime/codex/parser.go:202`, `internal/usage/profiles.go:72`, `internal/usage/accountant.go:390` |
| opencode | headless: yes. Interactive: no | per request, summed | alongside (additive) | `internal/runtime/opencode/parser.go:209`, `internal/usage/profiles.go:87-93` |
| forestage | no | none, it is not stream-capable | n/a | `internal/usage/doc.go:79` |
| simulator | no | its heartbeat carries no tokens | n/a | `internal/simulator/engine.go:117-119` |
| generic | no | none | n/a | `internal/runtime/generic.go:5-8` |

Whether the codex accumulator resets at a turn boundary or runs for the whole session is unknown; the code treats both the same (`internal/usage/profiles.go:72`).

Evidence:

- **context window read.** Producers: a stream parser into the accountant (headless), the statusline feed through `marvel ctx-forward` into the heartbeat RPC (opt-in), the codex hook through `marvel codex-ctx` reading the rollout file, and any process that calls the heartbeat RPC itself (`internal/usage/doc.go:1-3`, `cmd/marvel/ctxforward.go:430`, `cmd/marvel/codexctx.go:91`). The window comes from a ladder: stream, learned, manifest `runtime.context_window`, feed, table (`internal/usage/limits.go:74-82`). claude: interactive only with `context_feed = "statusline"` (`internal/team/controller.go:1653`); headless reads a per-request level (`internal/usage/profiles.go:38-45`). codex: interactive through hooks (finding-052); headless is a running total, so spend is recorded and CTX% stays absent (`internal/usage/profiles.go:72`). opencode: no interactive feed (`docs/design/unresolved-window-context-pressure.md:30-34`); headless reads the stream (`internal/usage/profiles.go:87-93`) but the window needs the manifest, because the table entry is empty (`internal/usage/limits.go:277`). forestage: interactive through the shared statusline feed (`internal/runtime/forestage.go:43`), code only; not stream-capable (`internal/usage/doc.go:79`). simulator: percent only, no tokens or window (`internal/simulator/engine.go:117-119`). generic: marvel computes nothing (`internal/runtime/generic.go:5-8`).
- **memory and CPU.** One daemon-side sampler rolls up the pid subtree of every live session that has a pid, with no per-adapter branch (`internal/daemon/metrics.go:39-56`). It reads `ps` on darwin and `/proc` on linux, and other platforms report unsupported (`internal/procstat/read_other.go:3-8`). CPU is a delta, so the first linux pass reads 0 (`internal/procstat/procstat.go:96-99`). For a container-in-pane session the reading was the docker client, not the workload (finding-051).
- **Tin, tokens in, cumulative.** The accountant sums per-request prompt tokens in a layout-normalized form (`internal/usage/reader.go:28-47`, `internal/usage/accountant.go:557-575`) and writes `SpendPromptTokens` to the session (`internal/api/types.go:469-480`); `nil` means never metered, not zero (`internal/api/types.go:472-474`). It needs a stream, so only headless claude, codex and opencode fill it (`internal/runtime/claudecode/parser.go:289`, `internal/runtime/codex/parser.go:202`, `internal/runtime/opencode/parser.go:209`). The codex value is a running total set by replacement (`internal/usage/accountant.go:390`). `get sessions` shows it as the opt-in PROMPT column, and a Tin column was deliberately not made (`docs/design/get-sessions-output.md:141-144`).
- **Tin, tokens in, current context level.** `ContextTokens` is a level, set by the accountant and by heartbeats (`internal/api/heartbeat.go:112`): claude by the statusline feed (`cmd/marvel/ctxforward.go:232`), codex by the hook (`cmd/marvel/codexctx.go:91`), forestage through the shared feed (code only). Interactive seats never get cumulative prompt tokens.

- **Tout, tokens out, cumulative.** `SpendOut` is the session's output tokens summed by the accountant (`internal/api/types.go:469-480`, `internal/usage/accountant.go:557-565`). It is the harness's reported output count and excludes the separately recorded reasoning tokens; `nil` means never metered. The same stream gate applies, so only headless claude, codex and opencode fill it; the `RATE` column is a decaying rate over it (`docs/design/get-sessions-output.md:139-140`). codex's reasoning tokens are a subset of its output tokens (`internal/runtime/codex/parser.go:213-215`), while the team token meter adds reasoning tokens to output (`internal/daemon/admission.go:34-38`), so a codex team's meter may count reasoning twice; this was not checked end to end. Whether claude's and opencode's reported output includes reasoning is unknown.

## Budgets and usage limits

| Feature | claude | codex | opencode | forestage | simulator | generic | Needs |
|---|---|---|---|---|---|---|---|
| team `max_sessions`, enforced | yes | yes | yes | yes | yes | yes | none |
| team `max_tokens`, enforced | headless | headless | headless | no | no | no | none |
| other budget clauses (`max_cost_usd`, `max_team_rss_bytes`, `max_session_ctx_percent`) | no | no | no | no | no | no | none |
| account usage windows read, `limited` condition | yes | yes | no | yes | no | no | none |
| usage-limit pause with an end | no | no | no | no | no | no | none |

Evidence:

- **max_sessions and max_tokens.** Enforced at the operator verbs work, scale, run and shift, with a reconciler backstop (`internal/daemon/admission.go:63-65`). `max_sessions` counts live sessions from the store, so it is harness-independent (`internal/api/budget.go:149-156`). `max_tokens` needs metering, which needs a stream, so it covers headless claude, codex and opencode (`internal/daemon/admission.go:34-38`); a seat the accountant cannot see is unmeasured and is admitted unless the operator sets `on_unmeasured = refuse` (`internal/api/budget.go:164-185`, `internal/usage/reader.go:65-67`).
- **other clauses.** Declared and not implemented (`internal/api/budget.go:70-72`), reasons in `internal/admission/doc.go:73-92`.
- **account usage windows.** claude reads the statusline `rate_limits` five-hour and seven-day windows, and needs `context_feed` (`cmd/marvel/ctxforward.go:441-443`). codex reads the newest `rate_limits` block through its hook (`cmd/marvel/accountlimits.go:73`). Only claude and codex have home directories the account key knows (`internal/api/account.go:80`), so opencode has no reader, which finding-050 and the design doc state. forestage rides the shared feed (`internal/runtime/forestage.go:43`), code only. The daemon marks a session `limited` from its account reading, advisory and restart-neutral (`internal/daemon/limits.go:15`). The observed windows feed that condition, not a budget clause; the windowed budget shape finding-050 recommended was not built (`internal/api/budget.go:38-48`).
- **pause with an end.** Design only; no pause record exists in code, and the design lists its later steps as unbuilt (`docs/design/usage-limit-pause.md:519`). A ring-to-bus tap that would signal agent teams is planned and not shipped (`docs/design/usage-limit-pause.md:703-715`), and would need the bus.

## Other features

| Feature | claude | codex | opencode | forestage | simulator | generic | Needs |
|---|---|---|---|---|---|---|---|
| presence, marvel's own heartbeat | yes | yes | no | yes | yes | no | none |
| presence on the bus | unknown | unknown | unknown | unknown | unknown | unknown | needs bus, needs director |
| bus join (env stamped, credentials) | partial | yes | partial | partial | unknown | partial | needs bus; needs hub for a global role; needs director for the shim |
| usage-limit key-sending | partial | no | no | no | no | no | none |
| permission policy projected to a settings file | yes | no | no | yes | no | no | none |
| role `permissions` injected as a launch flag | yes | no | no | yes | no | no | none |
| folder trust seeded for the seat | no | yes | unknown | unknown | n/a | unknown | none |
| session-id pin | yes | no | unknown | unknown | n/a | no | none |
| prompt delivery at launch | yes | partial | partial | yes | n/a | no | none |
| completion semantics, headless (ADR-010) | yes | yes | yes | partial | partial | partial | none (tmux 3.5 for an exact exit status) |

Evidence:

- **presence.** marvel's heartbeat needs only the daemon socket (`internal/runtime/adapter.go:449-458`, `internal/daemon/daemon.go:1124-1125`). Producers: claude and forestage through the statusline feed, only when `context_feed = "statusline"` (`internal/runtime/claude.go:53`, `internal/runtime/forestage.go:43`, `internal/session/projection.go:140`); codex through its hooks (`internal/runtime/codex_home.go:27`, finding-052); simulator (`internal/runtime/simulator.go:44-48`). generic supplies none, and a heartbeating process under it goes stale (`internal/runtime/simulator.go:5-11`). opencode: no. Heartbeats enter only through the daemon RPC (`internal/daemon/daemon.go:1978`, `internal/api/store.go:812`) and marvel wires no opencode producer, the same evidence as generic. Bus presence is written by the director shim, outside this repository, and needs the bus (`internal/bus/declared.go:124-128`, `docs/design/shim-heartbeat-feed.md:13`); a global presence row also needs a hub (`docs/design/bus-role-users.md:70-77`). No cell is claimed for bus presence because nothing in this repository measures it per harness.
- **bus join.** Bus env (`NATS_URL`, `DIRECTOR_NATS_USER`, `DIRECTOR_NATS_PASS`, `DIRECTOR_GLOBAL_ROLE`) is stamped only when the cluster's bus section yields a URL (`internal/runtime/adapter.go:433-448`). Credentials exist only for a managed broker (`internal/bus/manager.go:549-550`). A per-role `<team>.<role>` user needs a global role and a hub (`internal/bus/manager.go:271-283`). `partial` means the env is delivered and marvel wires no shim, so whether the harness reaches the bus depends on its own config: claude (`internal/runtime/claude.go:211`, `docs/design/seat-bootstrap.md:245-250`), opencode (`internal/runtime/opencode.go:53`), forestage (`internal/runtime/forestage.go:114`), generic (`internal/runtime/generic.go:113`). codex `yes`: marvel also seeds the director MCP server and forwards the env names (`internal/runtime/codex_home.go:100-114`). simulator: no code or doc says.
- **usage-limit key-sending.** The only key ever sent is `2`, no Enter, and the action is handed a sender that takes no key (`internal/daemon/limitaction.go:12`, `internal/limitact/action.go:1-12`). It applies where the adapter has a `ForegroundRule`, which only claude does (`internal/runtime/claude.go:305`, `internal/daemon/panemenu.go:28-37`). `partial` for claude because the shipped sample list is empty, so today nothing is matched or sent (`internal/daemon/limitmenus.go:8`).
- **permission policy and flag.** Policy projection is supported by claude and forestage only (`internal/runtime/claude.go:37-42`, `internal/runtime/forestage.go:32-37`; codex `internal/runtime/codex.go:35-41`, opencode `internal/runtime/opencode.go:33-39`, simulator `internal/runtime/simulator.go:18-23`, generic `internal/runtime/generic.go:97-102`). The `permissions` flag is injected for claude (`internal/runtime/claude.go:129-132`) and forestage, which also injects `dangerous_permissions` (`internal/runtime/forestage.go:80-92`). codex leaves it out on purpose (`internal/runtime/codex.go:19-23`); opencode leaves `--auto` to the operator (`internal/runtime/opencode.go:17-20`).
- **folder trust seeded.** codex only: the seat's private config declares the start directory untrusted unless the workspace lists it in `trusted_folders` (`internal/runtime/codex_trust.go:52-69`, `internal/runtime/codex_home.go:116-124`, finding-049). claude `no`: no code writes claude folder trust; the bootstrap design describes it (`docs/design/seat-bootstrap.md`). opencode, forestage, generic: unknown.
- **session-id pin.** claude mints a `--session-id` per launch unless the args already carry `--session-id`, `--resume` (`-r`) or `--continue` (`-c`) (`internal/runtime/claude.go:90-93`, `internal/session/manager.go:976-981`). codex has no pin and uses a private `CODEX_HOME` as a container instead (`internal/runtime/adapter.go:177-198`, `internal/runtime/codex.go:87`). generic does not implement it (`internal/runtime/adapter.go:205-211`). opencode and forestage: no code or finding says whether the harness accepts a pinned id.
- **prompt delivery.** claude: the identity line through `--append-system-prompt` on a bare claude, skipped when the role carries its own, read from args and from the command words (`internal/runtime/claude.go:152-164`, `internal/api/manifest.go:788`); the headless request is the positional argument (`internal/runtime/claude.go:204-207`). codex `partial`: headless request only, no system prompt (`internal/runtime/codex.go:19-23`, `internal/runtime/codex.go:122-130`). opencode `partial`: headless request only (`internal/runtime/opencode.go:57-64`). forestage: team context after `--` (`internal/runtime/forestage.go:104-112`). generic passes args only (`internal/runtime/generic.go:104-119`). There is no prompt-file feature: the code only recognises a role's own `--append-system-prompt-file`. An interactive first prompt is a proposal (`docs/design/interactive-first-prompt.md`), and prompt moderation is design only (`docs/design/prompt-moderation.md`). A manifest-supplied system prompt drops the identity line (finding-066).
- **completion semantics.** The reap rule keys on `Runtime.Mode` and the pane exit status alone, with no harness branch: the window is kept on exit, the replica slot is held, and a non-zero or empty status becomes `crashed` (`internal/session/manager.go:1553-1648`, `internal/api/budget.go:252-257`). An exact status needs tmux 3.5; a lost status is retried (`internal/session/manager.go:1636-1640`, `docs/user-guide.md:374-380`). Headless launches exist for claude (`internal/runtime/claude.go:100-102`), codex (`internal/runtime/codex.go:114-148`) and opencode (`internal/runtime/opencode.go:50-80`). `partial` for forestage, simulator and generic: they have no headless branch in `Prepare`, so the reap rule applies only if the mode is set, and no test or finding exercises it. The ADR-010 text is not in this repository. Findings on headless completion: finding-015, finding-036, finding-038.

## Not adapted

These have no adapter in `internal/runtime`, so marvel runs them as `generic`, with no launch flags, no context reading and no projection. "Not adapted" is a statement about code, not about whether the harness works in a tmux pane.

- **crush.** No adapter exists. A comment lists what one would need, including `CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1` (`internal/runtime/adapter.go:6-19`). Measured: finding-020 and finding-033.
- **goose, gemini-cli.** Surveyed for a context channel, no adapter (`_kos/probes/probe-interactive-ctx-remainder-sweep.md:365`, `_kos/probes/probe-interactive-ctx-remainder-sweep.md:687`; gemini-cli telemetry in finding-008).
- **pi, mini-swe-agent.** Named as harness-survey candidates. No code or finding in this repository mentions either, so nothing here is known about them.

## Where the docs and the code disagree

The matrix states the code. These older statements disagree with it, so read them with care until they are corrected:

- `CLAUDE.md` says codex reports a session total and stays operator-shifted. The code has an interactive codex hook feed (`internal/runtime/codex_home.go:22-27`, `cmd/marvel/codexctx.go:91`).
- `internal/usage/doc.go:79` says CTX% is headless only. Two interactive producers exist: the statusline feed for claude and forestage, and the codex hook.
- `docs/design/shift-trigger-list.md:12` says only context pressure is implemented. The code implements max-age and a trigger list (`internal/team/controller.go:2031-2086`).
- `internal/admission/doc.go:93-96` calls per-provider accounting unachievable, while the account windows for claude and codex are read (`internal/daemon/limits.go:15`).
- `internal/usage/limits.go:277` leaves the opencode window table empty, where finding-007 describes a table.

## Keeping this current

Two checks run in `go test ./internal/runtime`. The adapter capability table above is compared with the registry, so adding an adapter or changing which optional interfaces it implements fails until the table is regenerated. Every `path:line` citation in this file under `internal/`, `cmd/`, `docs/` or `_kos/` is checked to name an existing file at least that long, which catches deleted files and large shrinkage.

Neither check can tell that a cited line still says what the cell claims. A cell that depends on a measurement rots when the harness changes. Treat a changed adapter, parser or trigger as a prompt to reread its rows, and a review of this file as owed when a harness releases a new version.

## Not established

- forestage, simulator and generic: max-age rotation and handoff put have no measurement.
- claude: max-age end to end, with the seat writing the marker and the shift following.
- codex: whether the context hooks fire under `codex exec`.
- opencode: session-id pin and folder trust; account usage windows, where absence is inferred from the harness list.
- forestage: live statusline and account-window readings, session-id pin and folder trust.
- bus presence per harness, and the simulator on the bus.
- whether the director shim retries after a broker authentication refusal.
