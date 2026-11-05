# finding-marvel-36f7: scheduled work has two halves with different first customers

Date: 2026-11-05
Status: frontier
Answers, in part: `_kos/nodes/frontier/question-scheduled-cues.yaml`
Designs: `docs/design/scheduled-runs.md`, `docs/design/declared-cadence.md`

## Why this matters

A periodic job that lives only in a seat's memory is lost at the next handoff, and nothing records that it was ever due. marvel has two designs for owning that clock: a `schedule` block on a headless role, and a declared `cadence` that prompts a live interactive seat. They are built to different depths, and they serve different first customers. Ordering the remaining work by customer, not by design document, puts one marvel ticket on the critical path instead of nine.

Tags: **CODE** (read at marvel `8211b31` and director `ff0d94c`), **INFERRED** (reasoning, not observed). Nothing here was measured on a live cluster.

## 1. What is built

- **CODE:** the headless schedule block is built through S-3: parse and validation with the daylight-saving trap (#429), run history, `schedule.stale` and the `describe team` block (#431, #531), the clock that advances one firing per due time and spawns through the reconciler (#650), refusal of a schedule on a role that skips permission checks (#1100), and fleet suspend kept out of the freshness alarm (#1267, #1270).
- **CODE:** declared cadence is built through T3: the declaration and apply validation (#1052), `DIRECTOR_CLUSTER` at spawn (#1049), and the slot clock that writes `open` or `skipped` records per due time (#1071).
- **CODE:** one ticker drives both clocks (`internal/team/controller.go:604`, `:607`). They write separate store buckets with near-identical accessors and duplicated catch-up, lateness and posture-skip logic.
- **CODE:** the `Schedule` resource kind is model-only by design. scheduled-runs.md section 2 chose a role block, with a stated promotion trigger; nothing has tripped it.

## 2. What is not built, and where it bites

- **CODE:** admission has five triggers (`internal/admission/admission.go:90-94`) and no `TriggerSchedule`. A firing spawns under the reconcile backstop, so the cumulative token clause is not evaluated for it.
- **CODE:** `active_deadline` is required at validation but enforced only by the goose watchdog (`internal/runtime/goose.go:179`). A claude or codex headless run has no wall-clock kill.
- **INFERRED:** with `concurrency = "forbid"` and no kill, one hung run holds the firing slot, and every later firing is skipped as overlap until someone acts. That is the one silent failure of the headless half today, and it is ticket aae-orc-su453.
- **CODE:** skips and misses are events on the ring only (`internal/team/schedule_clock.go:169-191`); `describe team` has no record of them. `schedule.stale` measures time since the last success, so for a weekday job its floor is the weekend gap: it cannot report a single missed weekday on the same day.
- **CODE:** cadence has no claim, swept or overdue state, no event kinds for them, and no reader: nothing outside the clock reads `cadence_status`. The T5 wake parts are absent: no `pane_in_mode` check, no second read, and `notifyHandoff` still sends text and Enter in one call.
- **CODE (director):** since director #491, `cast-launch.sh:190` gives every interactive supervisor cast `--dangerously-load-development-channels` unless `DIRECTOR_CUE` is set, and that consent prompt returns on every spawn and shift. marvel has no readiness pattern for it (aae-orc-i9dli is open).
- **INFERRED:** a cadence slot held by an interactive supervisor therefore cannot survive a handoff today: the successor sits at the prompt and cannot claim. A headless run never meets the prompt.

## 3. The two halves and their customers

- **Headless half.** It has a customer now: a weekday sweep that has to fire on its own and survive a supervisor handoff, with nobody watching it. Everything that firing needs is built except the deadline kill.
- **Cadence half.** No manifest declares a `cadence` block, and no interactive seat is named as needing a scheduled prompt. Its first customer is an open operator question.

Design rule, from this party: **a declared schedule that is never applied produces no record of any kind.** marvel cannot report a job it was never given, so the guard against that case sits with whoever applies manifests, not in the clock.

## 4. Ordered work

Headless, now:

1. aae-orc-su453, retitled to the `active_deadline` kill: adapter-neutral, settles `failed` via `SettleRun`, emits `run.deadline`, and leaves goose's own kill as the single one. Red test: with `retries = 0`, `on_failure = "wait"` and `forbid`, a claude firing past its deadline is deleted, settles `failed`, emits one `run.deadline`, and the next firing spawns rather than skipping.
2. aae-orc-i9dli as a seat-readiness pattern under `internal/panestate/patterns/claude/<build>/`, so a shift that stops at the consent prompt is named instead of reading as unknown. Depends on fresh captures (W-1).

Headless, follow-ups: a stored skip or miss record with a reason field in `describe team` (storage only); then `TriggerSchedule` in admission, asserting the `budget` reason through describe; aae-orc-labbh (the events mirror) as filed.

Cadence, now: a `Cadences` block in `describe team` mirroring S-2's `Schedules` block, and fresh P1 captures at the installed claude build with one expected state per fixture (permission and consent prompts never read `Empty` or `MidTurn`, since the confirm path accepts either; a dim placeholder reads `Empty`).

Cadence, held until a first customer is named: the claim and swept verbs (strict `AuthenticateLiveSession`, then the role on the record it returns; refuse sessions with no token hash, not running, or ending), overdue, lease release on holder departure, the grace shift on fleet resume, the T5 injector as its own entry point, the overdue mail with the consent reason, and the shared slot record. These stay as REMAINING notes on aae-orc-80kzr rather than new tickets, since no one is committing to close them yet. Probe P3 is withdrawn: it served only the session-cron bridge, whose re-check date passed unrenewed.

## 5. Gaps

- Nothing here was run on a live cluster; every behavior claim is CODE or INFERRED.
- Whether a headless run on a given host can reach the director send is unchecked.
- The consent screen's reading by the composer (`Unknown`) is an inference until the P1 captures exist.

## 6. Harvest

- `question-scheduled-cues`: "who owns the clock" and "where does last-ran live" are answered by built code (S-1 to S-3, T1 to T3). "What a cue delivers into an interactive seat" is designed and unbuilt, with no customer. "Who receives the alarm" is open: before the events mirror lands, nothing subscribes to `schedule.stale`.
