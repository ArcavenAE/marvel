# goose adapter v1.1: what the first live run changes

- **Status:** proposed, 2026-10-31. Nothing here is built. It revises `goose-adapter-v1.md`; every
  section not named below stands as written there.
- **Author seat:** architect, team arcaven.
- **Pins:** marvel main fa228925; goose v1.57.0, verified by sha256 and release attestation.
- **Evidence:** the arm 2 run on mokuzai. Ollama 0.34.2 with qwen3:0.6b (Q4_K_M, context 4096) and
  qwen3:8b (Q4_K_M), inside the arm 2 sandbox-exec profile. That profile allows the network only to
  localhost port 11434 and keeps the bus port denies; its 11 controls passed.

## 1. Why

v1 was written from goose source before anyone ran goose. The first live run shows three places
where a seat built to v1 would be wrong:
- Nothing measured bounds a stream: one run streamed for 27m49s.
- The fail-closed `approve` posture cannot run headless.
- A denied tool can end in exit 0, with the model saying the command ran.

Each one turns into a seat that hangs, never starts, or reports work it did not do. This revision
fixes the adapter's rules for all three before ticket C is built.

## 2. Facts from the run (goose v1.57.0)

| # | fact | arm 2 |
|---|---|---|
| L1 | `goose run --output-format stream-json` writes one line per chunk, and one `complete` event per run with totals | all runs |
| L2 | per-request usage appears only in the sessions table, never in the stream (G3 confirmed) | all runs |
| L3 | each launch makes a new session row, and nothing resumes | all runs |
| L4 | total stream time is not bounded: a run streamed for 27m49s. The chunks were steady, with a largest gap of 1 s; no gap over 30 s was seen, so idle and first-byte behavior are untested. No goose config key, env var or flag sets a stream timeout | R10, qwen3:8b, 15,976 output tokens |
| L5 | headless `approve` that meets a tool not in `permission.yaml` exits 1 with no `complete` line and no hang, with "Tool approval required in non-interactive mode with GooseMode::approve ... Use GooseMode::Auto for headless sessions" | R2 |
| L5b | headless `approve` ran to `complete` when the only tool the model asked for was under `never_allow`: the call was declined without a prompt (L6) | R3 |
| L6 | under `GOOSE_MODE=approve`, a `never_allow` shell call was refused as a tool result, the run exited 0, and the model then said "echo p3 has been executed successfully". The refusal has no type of its own (section 3, C4) | R3, qwen3:0.6b, `max_turns` 4 |
| L7 | 4,096 tokens is the practical context ceiling for the small models | qwen3:0.6b |
| L8 | a tool call into a stdio extension has its own timeout, 300 s by default; `--with-extension` fixes it at 300 s. That fits a seat's 120 s bus wait (director-mcp `wait_for_message`, timeout 120) blocking inside one tool call. By source, not measured: no arm 2 run made a long-blocking extension call | goose source, read by the research seat |

Settled from the run's records (relayed 2026-10-31): R3 ran under `approve`, not `auto`, with
`never_allow` holding `shell` and `extensionmanager__manage_extensions`, as the earlier brief
expected. The 120 s in earlier notes is the seat's bus wait inside a tool call (L8), not a goose
stream timeout; nothing in goose bounds a model stream (L4). The model stream (L4) and the
extension tool-call timeout (L8) are separate timers.

Pending, one measurement: a stub stdio extension whose tool blocks for 120 s, run headless, to
show that a 120 s bus wait returns inside the 300 s extension timeout (L8).

## 3. Changes to v1

**C1. Posture (replaces v1 section 4, "Postures").**
- The headless posture is `GOOSE_MODE=auto`, and it is allowed only inside the sandbox profile.
- `approve` and `smart_approve` on a headless role are posture faults (`approve-headless`,
  `smart-approve-headless`), because both fail there (L5).
- `auto` with no sandbox in force is a posture fault (`auto-unsandboxed`). Section 6,
  decision 2, says what counts as a sandbox.
- All three take v1's refusal stub (exit 78), never a launch.
- A headless role that carries a deny list, as R3's `never_allow` on `shell` did, is refused
  under both postures: under `approve` as `approve-headless`, under `auto` as v1's
  `auto-with-deny-list`. This is intended. `auto` does not read the list (G6), so no headless
  goose seat relies on one; its containment is the sandbox (section 6, decision 2).
- v1's fail-closed allowlist posture stays for interactive roles only. The rule never to degrade
  to `chat` stands.

**C2. No resume (amends v1 section 3, launch line).** Drop `--resume`: every launch is a new
session (L3). v1's ticket H already picks the launch's row by a bound set at launch, not by
recency, and that stands.

**C3. The adapter's own clock.** Nothing measured bounds a goose model stream (L4), so the
adapter does. This is a different timer from the extension tool-call timeout (L8), which C3
leaves alone.
- goose runs under a small marvel wrapper. This is the same shared runtime code as v1's refusal
  stub, and it is used by this adapter only.
- The wrapper holds two limits:
  - *wall-clock*: the role's limit, which is `active_deadline` for a scheduled role, and 45
    minutes otherwise;
  - *idle*: no stream line for 300 s.
- The idle timer pauses while a tool call is open. goose writes no stream line between a
  `toolRequest` and its `toolResponse`, so a builtin shell call that runs a test suite for
  longer than 300 s would otherwise read as idle and be killed. The wrapper keeps the set of
  `toolRequest` ids with no `toolResponse` yet (the same keying as C4). While that set is
  non-empty the idle timer does not run; when it empties, the timer restarts from zero. The
  wall-clock limit never pauses, so a tool call that hangs is still ended by it.
  - Why this and not an idle limit above the tool limit: only extension calls have a tool limit
    (L8, 300 s). Nothing bounds a builtin shell call, so there is no number to set the idle
    limit above, and a limit long enough for the slowest test suite stops catching a stalled
    model stream at all. Pausing keeps idle meaning "the model stopped talking" and leaves long
    tool work to the wall-clock limit.
- On either limit it sends TERM, then KILL after 10 s. It writes `watchdog: wall-clock` or
  `watchdog: idle` as the last line of the per-launch stderr file (v1 section 3, the failure
  reason), and exits 124.
- The reap records exit 124 with that reason. 124 is not one of goose's codes, since every goose
  failure exits 1 (G8).
- The 300 s default is a guess above the measured 1 s gap. The probe's fixtures (section 4)
  measure a real idle gap before the default is fixed.

**C4. What ran comes from tool results only (amends v1 section 3, the parser and the three
outcomes).**
- The adapter keys every tool call by its id, from the assistant's `toolRequest` to the user's
  `toolResponse` with the same id.
- A call counts as run only when its `toolResponse` has `toolResult.status` "success" AND
  `toolResult.value.isError` false.
- A response with `isError` true is not-ran. There is no distinct denial type: goose's refusal
  is a `toolResponse` whose `toolResult.status` is "success" and whose `value.isError` is true,
  with the decline text in `value.content` (R3). The adapter never trusts `toolResult.status`
  alone. It maps such a response to a tool-denied event that names the tool, and counts it on
  `session.ended`.
- A `toolRequest` whose `toolCall.status` is "success" means the request was well formed, not
  that the tool ran.
- A request with no matching response by `complete` is not-ran.
- The exit code and the model's text never decide what ran, because a refused shell call exited
  0 and the model said it succeeded.
- `complete` still means the run finished. It does not mean every tool ran.

**C5. Context.** `GOOSE_CONTEXT_LIMIT` is projected from `runtime.context_window` as v1 says. For
a small local model the adapter takes the lower of the role's value and the model's measured
ceiling (L7, 4,096 for qwen3:0.6b). A role that declares more draws a NOTE at prepare time, not a
refusal. Where the measured ceiling lives is open (section 6b); until it is ruled, ticket C
builds the projection from `runtime.context_window` alone and leaves the lower-of step out.

## 4. Acceptance tests

Each test runs against recorded fixtures from arm 2 and a fake goose, so no model is needed.

1. **The clock bounds the run.** The wrapper takes its three durations (wall-clock, idle, and
   the TERM-to-KILL grace) as arguments, so the test sets them small, for example 2 s, 1 s and
   1 s, and runs on the real clock. A settable limit is chosen over an injected clock because the
   wrapper is its own process, started by exec like the refusal stub, and a clock does not cross
   an exec. Cases:
   - a fake goose that writes one line every 0.2 s past the wall-clock limit is ended at the
     limit with exit 124 and `watchdog: wall-clock`;
   - a fake that writes one line and then nothing is ended after the idle limit with
     `watchdog: idle`;
   - a fake that writes a `toolRequest`, is silent for longer than the idle limit, then writes
     the matching `toolResponse` and `complete`, is untouched;
   - the same fake with its silence longer than the wall-clock limit is ended with
     `watchdog: wall-clock`;
   - a run that ends before both limits is untouched.
2. **Headless runs Auto in the sandbox, or not at all.** A headless role with `approve`,
   `smart_approve`, or `auto` without the sandbox gets the stub (exit 78), with its reason, and
   goose never starts. A headless role with `auto` inside the profile launches with
   `GOOSE_MODE=auto` in its env.
3. **Only tool results say what ran.** The fixture is R3's shape:
   - an assistant `toolRequest` with id `call_ngnvd7s5`, `toolCall.status` "success", name
     `shell`, arguments `{command: "echo p3"}`;
   - a user `toolResponse` with the same id, `toolResult.status` "success", and `value`
     `{resultType: "complete", isError: true, content: [text "The user has declined to run
     this tool. ..."]}`;
   - a `complete` line with `total_tokens` 6749, exit 0, and assistant text "echo p3 has been
     executed successfully".

   It yields one tool-denied event for `call_ngnvd7s5`, zero runs counted for that call, and
   `session.ended` with exit 0 and a denied count of 1. No event says the command ran. A second
   fixture, with `isError` false on the same shape, counts one run.

## 5. Named, not designed (waiting on a Bedrock run)

- Bedrock streaming and usage: whether the stream and `complete` look the same as Ollama's.
- `cost_usd` on `complete`, and whether it is filled for Bedrock.
- The proxy path for a remote provider through the sandbox's single allowed port.
- The keyring read under a private root (v1 section 6, ruled 2026-10-10) on a host with a real
  provider key.

## 6. Decisions past the adapter

These reach past the adapter, so this brief did not decide them. Director decided them on
2026-10-31, as defaults the operator may override.

1. **The watchdog stays in this adapter's wrapper for v1.** The shared stream reader does not
   change. Moving the watchdog there for every `StreamCapable` adapter is its own PR, with tests
   for the claude, codex and opencode seats.
2. **"Sandbox" means either of two things.** It is the sandbox-exec profile the role runs under
   on a macOS host, or a container placement. Headless `auto` runs only when one of them is in
   force, and v1's ticket K takes this definition.
   - **What the fault reads.** C1's `auto-unsandboxed` reads what is in force, never a
     recommendation. For a container that is the resolved placement (`api.ResolvePlacement`),
     not `SandboxRecommender`, which only feeds the resolution as its second step.
   - **How `Prepare` learns it.** Today it does not: `LaunchContext` carries no placement, and
     the manager resolves placement (`containerPlacement`) before it calls `Prepare`. Ticket K
     adds two fields the manager sets before `Prepare`: the resolved placement, and the path of
     the sandbox-exec profile the launch runs under (empty for none). The fault fires when the
     placement is not container and the profile path is empty. An adapter that finds both
     unset treats the launch as unsandboxed, so a missing field refuses rather than launches.
   - **No container by default.** The goose adapter does not implement `SandboxRecommender` as
     true in v1, so it does not move goose workspaces to container. A container placement with
     no image is refused (`ErrPlacementRefused`), so a default to container would refuse every
     goose seat whose role names no image, `approve` interactive seats included. A workspace
     that wants goose in a container says so with its own `placement`.
3. **`never_allow` under `auto` is settled by the evidence, and the evidence is not in yet.**
   R3 ran under `approve`, where `never_allow` held, as v1's G6 says. Behavior under `auto` is
   UNTESTED, so this stays open, and v1's `auto-with-deny-list` fault stands. Until a run
   tests it, the adapter does not rely on goose's `never_allow` under `auto`. The controls are
   marvel's: the sandbox (decision 2), and C4's rule that only a non-error tool result counts
   as run.

## 6a. Headless `approve` stays a fault

Settled by R2, director agreeing, 2026-10-31. Headless `approve` that meets any tool not in
`permission.yaml` fails closed: exit 1, no `complete`, no hang (L5). R3 reached `complete` only
because its one tool was already decided by `never_allow` (L5b). A role cannot know in advance
that the model will ask only for listed tools, so C1's `approve-headless` fault stays in v1, and
acceptance test 2 stands as written.

## 6b. Open: where a model's measured context ceiling lives

C5 needs a measured ceiling per model (L7, 4,096 for qwen3:0.6b), and nothing holds one yet.
This needs a ruling; the brief does not choose it.

1. **A table in the goose adapter** [recommended]: model name to measured ceiling, each entry
   citing the run that measured it. It stays inside this adapter, it changes no other adapter,
   and a model with no entry takes the role's value unchanged. The cost is that a new
   measurement is a code change.
2. **A role field**, such as `runtime.model_context_ceiling`. The operator sets it per role. The
   cost is a second number beside `runtime.context_window` that means almost the same thing, and
   a role that forgets it gets no protection.
3. **A cluster config key**, a model-to-ceiling map. One place for every adapter. The cost is
   that it reaches past this adapter, which is outside this brief's scope.

There is also a fourth reading: no new home, and the operator sets `runtime.context_window` to
the model's ceiling directly. That drops the lower-of step from C5 and leaves only the NOTE.

## 7. Tickets

There are no new tickets. v1's tickets change as follows:
- **A:** done for Ollama by arm 2, open for Bedrock (section 5).
- **C:** takes C3, C4 and C5.
- **D:** takes C1's three faults.
- **K:** takes the sandbox definition in section 6, decision 2, and adds the two `LaunchContext`
  fields it names (the resolved placement and the sandbox-exec profile path).
- **H:** stands as written.
