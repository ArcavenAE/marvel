# goose adapter v1.1: what the first live run changes

- **Status:** proposed, 2026-10-31. Nothing here is built. It revises `goose-adapter-v1.md`; every
  section not named below stands as written there.
- **Author seat:** architect, team arcaven.
- **Pins:** marvel main dfe210e8; goose v1.57.0, verified by sha256 and release attestation.
- **Evidence:** the arm 2 run on mokuzai. Ollama 0.34.2 with qwen3:0.6b (Q4_K_M, context 4096) and
  qwen3:8b (Q4_K_M), inside the arm 2 sandbox-exec profile. That profile allows the network only to
  localhost port 11434 and keeps the bus port denies; its 11 controls passed.

## 1. Why

v1 was written from goose source before anyone ran goose. The first live run shows three places
where a seat built to v1 would be wrong:
- A long stream outlives goose's own timeout.
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
| L4 | a 27m49s stream ran past goose's 120 s timeout. The chunks were steady, with a largest gap of 1 s; no gap over 30 s was seen, so idle and first-byte behavior are untested | R10, qwen3:8b, 15,976 output tokens |
| L5 | headless `approve` fails with "Use GooseMode::Auto for headless sessions" | |
| L6 | a `never_allow` shell call was refused as a tool result, the run exited 0, and the model then claimed the command ran | R3 |
| L7 | 4,096 tokens is the practical context ceiling for the small models | qwen3:0.6b |

Open from the report: which setting holds the 120 s (L4), and which `GOOSE_MODE` R3 ran under (L6).
Both are asked of the run's host, and the answers go in this table before ticket C starts.

## 3. Changes to v1

**C1. Posture (replaces v1 section 4, "Postures").**
- The headless posture is `GOOSE_MODE=auto`, and it is allowed only inside the sandbox profile.
- `approve` and `smart_approve` on a headless role are posture faults (`approve-headless`,
  `smart-approve-headless`), because both fail there (L5).
- `auto` with no sandbox in force is a posture fault (`auto-unsandboxed`). Section 6,
  decision 2, says what counts as a sandbox.
- All three take v1's refusal stub (exit 78), never a launch.
- v1's fail-closed allowlist posture stays for interactive roles only. The rule never to degrade
  to `chat` stands.

**C2. No resume (amends v1 section 3, launch line).** Drop `--resume`: every launch is a new
session (L3). v1's ticket H already picks the launch's row by a bound set at launch, not by
recency, and that stands.

**C3. The adapter's own clock.** goose's timeout does not bound a run (L4), so the adapter does.
- goose runs under a small marvel wrapper. This is the same shared runtime code as v1's refusal
  stub, and it is used by this adapter only.
- The wrapper holds two limits:
  - *wall-clock*: the role's limit, which is `active_deadline` for a scheduled role, and 45
    minutes otherwise;
  - *idle*: no stream line for 300 s.
- On either limit it sends TERM, then KILL after 10 s. It writes `watchdog: wall-clock` or
  `watchdog: idle` as the last line of the per-launch stderr file (v1 section 3, the failure
  reason), and exits 124.
- The reap records exit 124 with that reason. 124 is not one of goose's codes, since every goose
  failure exits 1 (G8).
- The 300 s default is a guess above the measured 1 s gap. The probe's fixtures (section 4)
  measure a real idle gap before the default is fixed.

**C4. What ran comes from tool results only (amends v1 section 3, the parser and the three
outcomes).**
- A tool counts as run only when a tool-result event for its call carries a success.
- A refusal tool result (L6) maps to a tool-denied event that names the tool, and it is counted
  on `session.ended`.
- The exit code and the model's text never decide what ran, because a refused shell call exited
  0 and the model said it succeeded.
- `complete` still means the run finished. It does not mean every tool ran.

**C5. Context.** `GOOSE_CONTEXT_LIMIT` is projected from `runtime.context_window` as v1 says. For
a small local model the adapter takes the lower of the role's value and the model's measured
ceiling (L7, 4,096 for qwen3:0.6b). A role that declares more draws a NOTE at prepare time, not a
refusal.

## 4. Acceptance tests

Each test runs against recorded fixtures from arm 2 and a fake goose, so no model is needed.

1. **The clock bounds the run.** A fake goose that writes one line a second for longer than the
   wall-clock limit is ended at the limit with exit 124 and `watchdog: wall-clock`. A fake that
   writes one line and then nothing is ended after the idle limit with `watchdog: idle`. A run
   that ends before both limits is untouched.
2. **Headless runs Auto in the sandbox, or not at all.** A headless role with `approve`,
   `smart_approve`, or `auto` without the sandbox gets the stub (exit 78), with its reason, and
   goose never starts. A headless role with `auto` inside the profile launches with
   `GOOSE_MODE=auto` in its env.
3. **Only tool results say what ran.** The R3 fixture (a `never_allow` refusal as a tool result,
   exit 0, and model text claiming success) yields one tool-denied event, zero runs counted for
   that call, and `session.ended` with exit 0 and a denied count of 1. No event says the command
   ran.

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
   on a macOS host, or a container placement where the role has one (`SandboxRecommender`).
   Headless `auto` runs only when one of them is in force. C1's `auto-unsandboxed` fault reads
   both, and v1's ticket K takes this definition.
3. **`never_allow` under `auto` is settled by the evidence.** When the run's host reports R3's
   `GOOSE_MODE` (L6), the answer goes into this brief. Until then, the adapter does not rely on
   goose's `never_allow` in either reading. The controls are marvel's: the sandbox (decision 2),
   and C4's rule that only a successful tool result counts as run. v1's `auto-with-deny-list`
   fault stays as written until R3's mode is known.

## 7. Tickets

There are no new tickets. v1's tickets change as follows:
- **A:** done for Ollama by arm 2, open for Bedrock (section 5).
- **C:** takes C3, C4 and C5.
- **D:** takes C1's three faults.
- **K:** takes the sandbox definition in section 6, decision 2.
- **H:** stands as written.
