# Probe brief: what a first-class goose adapter needs, and whether a goose seat can work in a team

**Status:** OPEN 2026-10-30. Opened by the operator, ruling I-51 ("goose: yes"), relayed by arcaven-supervisor (ask I-51:goose-probe, bus message 01M66V1S1GG63W5QH36J7DDCQ5). The operator's words: "how to we promote getting goose as an agent with a first class adaptor, and in the teams, working?"

Finding: to be minted with `kos id finding goose-adapter` when the finding is written, never hand-allocated.

## Why

The orchestrator graph's harness survey (finding 2w6k, 2026-10-08) already ranks goose first to add, in its section "1. goose: add next". It is the only surveyed harness that offers both native OpenTelemetry export and an auth path (ACP providers) that hands the subscription grant to the vendor's own CLI rather than storing it. The second fits SOUL section 3 better than any harness marvel runs today. That survey read the project's own docs and ran nothing. This probe moves goose from "ranked" to "measured". It names the adapter surface marvel needs, and it records where one goose seat breaks on marvel's generic adapter in a real team, before anyone writes `internal/runtime/goose.go`.

## Overlap check

- Prior art: `docs/design/goose-adapter-v1.md` (written at goose v1.54.0, 2026-10-10) and its tickets, including aae-orc-q13fv (goose A, the v1.54.0 characterization probe, P1 to P6). This brief extends that work to v1.57.0 and does not replace it. Research arm 1 of I-51 at v1.57.0 partly answers P4 and P5; P1 to P3 and P6 are still open.
- The harness survey (finding 2w6k) ranks harnesses; it does not measure goose. This probe builds on its citations and does not repeat the survey.
- `internal/runtime/opencode.go` and `internal/runtime/opencode/mapping.md` are the pattern for a non-Claude headless adapter: run mode, a JSON stream parser, a composer contract. The goose adapter is expected to follow that shape. This probe checks the expectation; it does not assume it.
- finding-020 (crush context-pressure channel) and the interactive context-remainder probe set the bar for context and token reporting. The operator's I-47 rollup idea consumes the same signal.

## What the v1.54.0 design already answers

`docs/design/goose-adapter-v1.md` read goose v1.54.0 source; its fact rows are G1 to G14 and M1 to M6. This probe re-measures only what that doc leaves open, or what may have moved by v1.57.0.

| here | the design's answer | left for this probe |
|---|---|---|
| Q1, H1 | G1 (four line types), G2 (`complete` only without a terminal error), G3 (no per-request usage); section 3 parser and the three run outcomes | whether `message` events are whole messages or chunks (arm 1 at v1.57.0: one event per chunk, same id) |
| Q2, H2 | G11 (goose mints the id; `--session-id` is accepted only with `--resume`; names are not unique; a bare `--resume` takes the newest), G12, M4; `SessionIDAssigner` is no, and `-n` is a label, never a pin | H2 holds in source: an id cannot be set before launch, and resume by id works with `--resume`. Section 3 and M4 rule out pinning a launch by name, not resume by id. P5 (two launches in one root, the newest row is this launch's) is partly answered by arm 1 and still open |
| Q3, H3 | G3, M5, M6; section 5: level only from `sessions.db` (ticket H), no OTLP receiver | P6 (per-request usage in the stream, and the row's level columns against the `complete` totals) is still open. Whether the reading feeds the I-47 rollup is not in the design |
| Q4 | `ComposerCapable` is no in v1 (headless only) | out of v1 scope; arm 1 recorded the composer anyway |
| Q5, H4 | not covered; extensions are narrowed with `--no-profile --with-builtin` | all of it: director-mcp as an extension and the 120 s wait |
| Q6, H5 | section 6 and the operator's ruling of 2026-10-10, "(a) with (c) as future option". The design reads that ruling as v1 keeping the keyring and projecting no `GOOSE_DISABLE_KEYRING`; that reading is the design's. Auth paths: section 8 sets the probe provider to an API key or Bedrock, not ACP, and the "Not in v1" line lists ACP providers | H5 is restated below to match the ruling. Arm 1's keyring-off run was a scratch measurement, not the v1 posture, so P4's keyring half (the key still found through the keyring under an absolute `GOOSE_PATH_ROOT`) is open. Vertex is not covered by the design |
| Q7 | G4 to G7 and section 4: `approve` plus a seeded `permission.yaml` fails closed; `auto` only in a sandbox (ticket K); never `chat`, which reports a skipped tool call as success (G7) | whether G4 to G7 still hold at v1.57.0; P1 (`approve` meets an unlisted tool) and P3 (`approve` meets a `never_allow` tool) are open |
| H6 | not covered | all of it |

The design's pin (v1.54.0, valid until 2026-10-24) has passed; this probe runs at v1.57.0 and moves the pin there if it lands.

## Questions

1. **Headless run.** What does `goose run` emit in its json and stream-json modes? Which lines carry turn boundaries, tool calls, errors and usage, and how do they map onto marvel's event vocabulary (the opencode mapping table is the template)?
2. **Session identity.** Where does goose write a session id and history? Can marvel pin or read the id at launch, and can a session be resumed by id after a shift or restart?
3. **Context and tokens.** Does goose report context-window use and token counts per turn in a form marvel can read live, from the stream, a file or OTLP, without screen scraping? Does that reading feed the I-47 rollup?
4. **Composer.** In interactive mode: how does a draft submit (paste plus Enter, or a literal newline), what clears a draft, does a clear exit the program when the composer is empty or mid-turn, and what interrupts a turn? These are the fields of marvel's `ComposerContract`, and composer-check and inject depend on them.
5. **MCP and the bus.** Does goose load director-mcp as an extension, so that `wait_for_message` and `send_message` work as the seat's bus channel? What does it do with a long-blocking tool call (the 120 s wait)?
6. **Auth under SOUL section 3.** Which auth paths work for a seat: the ACP handoff to a vendor CLI, an API key, or Bedrock or Vertex? Can a provider key stay in memory only, read from the operator's store at launch and never written to disk by goose (goose's docs say secrets go to the OS keyring by default)?
7. **Safety defaults.** `GOOSE_MODE` defaults to auto, and the OS sandbox is goose Desktop only. What permission mode does a seat need, and what containment (an OS sandbox tool, a container) does a CLI seat need before it holds a write grant?

## Hypotheses (to be confirmed or refuted)

- H1. `goose run` stream-json is line-delimited with stable type fields, so a parser on the opencode model works, and the adapter needs no `serve` surface for headless use.
- H2. goose writes a session id that marvel can read after launch but not set before it. Resume by id works from the CLI.
- H3. Token counts are in the stream per turn; context-window remaining is not, and must come from OTLP or the model's limit.
- H4. director-mcp loads as a stdio extension, but goose's tool-call timeout ends the 120 s wait before it returns, so a seat needs a shorter wait or a timeout setting.
- H5. Under the 2026-10-10 ruling a v1 seat keeps the keyring and reads the operator's own goose entry. A private `GOOSE_PATH_ROOT` does not move that entry (G10). Whether goose writes any other copy of the key at rest is open: goose#12413 (keys in logs) is unresolved, which is why section 3 caps and scans the stderr file it keeps. A keyring-off posture is not tested (director, 2026-10-30).
- H6. On the generic adapter a goose seat launches and takes a prompt, but marvel sees it only by capture-pane. It breaks first at bus wake (no MCP channel configured) or at a first-run trust or model prompt.

## Method

Two halves. The desk half runs first, and the experiment half waits for the supervisor's check.

- **Desk (read only):**
  - goose docs and source, collected through `ae` into quarantine (`ae start`, `ae mirror` of aaif-goose/goose, which is prompted), and read as data. This covers the CLI run modes, session storage, the stream schema, the extension loader and its timeouts, the keyring and secrets code, and `GOOSE_MODE`.
  - marvel source: `internal/runtime/adapter.go`, `generic.go`, `opencode.go` and `opencode/`, and how a team manifest names a runtime and its args.
- **Experiment (only after the supervisor checks the install and the cast against the rules):**
  - Install: the method, the version and the host are proposed first. Nothing is installed by this seat until that check returns.
  - Scratch first: under the relay run conditions (scratch HOME, `env -i`, short TMPDIR, no MARVEL_ variables, a sandbox profile with network limited to localhost plus the one provider endpoint the run needs, a blocked-write control at a fixed /private/tmp path), capture `goose run` json and stream-json fixtures, the session files it writes, and the composer behaviour in a scratch tmux pane.
  - Then one goose seat on marvel's generic adapter, in the reviewer lane next to reviewer-codex and reviewer-opencode, cast by the supervisor. Record, in order, where it breaks: login, trust prompts, model pick, MCP extension load, bus wake, first reviewed item.
  - No provider key at rest at any point. The run reads a key from the operator's store into memory, or uses the ACP handoff.

## Out of scope until the finding lands

The adapter itself (`internal/runtime/goose.go`, modeled on `opencode.go`), role-library harness entries, and the director dsi inventory.

## Evidence to keep

Stream fixtures (redacted of keys and paths under the real home), session-file listings (names and record types only), the composer measurements with the goose version, each break point with its exact error text, and the sandbox profile with its control results.
