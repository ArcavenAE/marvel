# vsdd probe T2: transcript ledger reader and cost meter

Probe-local scripts for the vsdd-factory marvel-team probe
(`../probe-vsdd-factory-as-marvel-team.md`, sections 10 and 11). Ticket
aae-orc-ou94x. Nothing here is part of marvel's build, and nothing here is
critic code (the operator ruled probe-local scripts only). Standard library Python only; no network; the
scripts write nothing.

| file | job |
|---|---|
| `ledger.py` | reads a run's main and subagent transcripts, drops streaming repeats by `message.id`, and prints the section 10 outputs 1 to 8 |
| `meter.py` | prices the ledger from `prices.json`, adds OTEL-only sources (the permission classifier, since the probe runs in auto mode, R4) as their own rows, and reconciles with OTEL `api_request` events |
| `prices.json` | the frozen price table from section 11 |
| `test_ledger.py`, `test_meter.py` | the fixture test: hand-counted totals read back exactly (brief M3b) |
| `fixtures/` | a small run (`run/`), its OTEL events (`otel.jsonl`) and the hand count (`fixtures/README.md`) |

## Run

```sh
python3 -I -B -m unittest discover -s _kos/probes/vsdd-probe-t2 -p 'test_*.py'
python3 -I -B _kos/probes/vsdd-probe-t2/ledger.py RUN_DIR
python3 -I -B _kos/probes/vsdd-probe-t2/meter.py cost RUN_DIR --otel OTEL.jsonl
python3 -I -B _kos/probes/vsdd-probe-t2/meter.py reconcile RUN_DIR --otel OTEL.jsonl
```

## Fail closed

`meter.py cost` prices a run only when it has read all of it. Otherwise it
prints no cost, writes each cause to stderr as `refused: <cause>: <detail>`
and exits 4 (3 is the smoke stub; `reconcile` exits 1 on a failed
comparison). The causes:

| cause | when |
|---|---|
| `no_requests` | no transcript request was read |
| `unmeasured_seat` | any seat has `transcript: null` |
| `seat_no_requests` | a measured seat has no requests |
| `bad_lines` | any transcript line was not JSON (any at all, so a cut file never prices) |
| `bad_otel` | an OTEL line was not JSON |
| `no_otel_events` | the OTEL file has no `api_request` event |
| `unpriced_model` | a transcript or OTEL model has no price in `prices.json` |
| `undeclared_otel_source` | an OTEL source is in neither `source_map` group nor `otel_only` |
| `reconcile_failed` | transcript and OTEL totals differ by more than `--pct` (default 1) in any class, or both sides are empty |
| `unreadable_input` | a named file is missing or `run.json` is not JSON |

A misnamed main-thread source lands under `undeclared_otel_source` and
`reconcile_failed`, not in the total. `cost` also prints what it assumed:
unsplit writes at the 5-minute rate (an underprice in the defaults cell),
OTEL-only creation tokens at the 5-minute rate with a per-row
`usd_if_creation_was_1h` beside it, and that the 1-hour pin is checked on
transcript writes only.

## run.json

```json
{
  "pinned_version": "2.1.283",
  "sharing_ceiling": 1000,
  "await_tools": ["await"],
  "await_pattern": "optional regex for a Bash call that is the await shim",
  "seats": [
    {"name": "orch", "kind": "orchestrator", "role": "vsdd-orchestrator", "transcript": "orch.jsonl"},
    {"name": "sup", "kind": "supervisor", "role": "supervisor", "transcript": null}
  ],
  "phases": [{"phase": "p3", "from": "<RFC3339>", "to": "<RFC3339>"}],
  "waves": [{"wave": "c1-w1", "first_dispatch": "<RFC3339>", "last_terminal": "<RFC3339>"}],
  "source_map": {"main": ["<OTEL query_source>"], "subagent": ["<OTEL query_source>"]},
  "otel_only": ["<OTEL query_source with no transcript, such as the permission classifier>"]
}
```

A seat's subagents are read from `<transcript minus .jsonl>/subagents/`, each
with its `.meta.json` for the `agentType`. A seat with `transcript: null` is
reported `unmeasured`, never as zero. A seat's totals include its subagents.

## Choices made where the brief leaves room

These are readings, not rulings. Each is a line in `ledger.py` or `meter.py`.

- **Cache death.** The prior request's TTL is 1 hour when it wrote 1-hour
  tokens, else 5 minutes when it wrote any, carried from the last write. A
  death needs a gap over that TTL and a write of at least half the previous
  request's prompt (input plus read plus write). Undetermined rows are
  skipped. A death is usually also a cold start; both are counted.
- **Concurrent cold write.** A spawn whose first request is cold and whose
  first transcript record is later than a same-role sibling's first record
  and earlier than that sibling's first response. "Starts" is the first
  record in the spawn's transcript, because the transcript carries no request
  start time.
- **Hub wait.** The orchestrator seat's own requests (not its subagents)
  with a timestamp from a wave's first dispatch to its last terminal state,
  inclusive; turns whose tool calls are all `await` (by tool name, or a Bash
  command matching `await_pattern`); and their union by `message.id`.
- **Dedupe.** The first copy of a `message.id` is kept. Copies whose usage
  differs are counted (`conflicting_duplicates`), and tool calls from every
  copy are merged, since one API message can span several transcript records.
- **A write with no 5-minute/1-hour split** is kept as 5-minute and listed
  under `unsplit_writes`.
- **OTEL-only pricing.** OTEL has no write split, so creation tokens are
  priced at the 5-minute rate.
- **Reconcile.** Per class (input, creation, read, output), main thread and
  subagent groups, `|a - b| <= max(a, b) * pct / 100`. Transcripts carry no
  `query_source`, so `source_map` says which OTEL sources are main-thread and
  subagent requests. Any other source is reported as its own row.

## Not done here

- **Reconciling one smoke run with OTEL.** It runs on the probe host and
  waits on the probe host ruling (brief section 0.1, R3). `meter.py reconcile-smoke` exits 3 and says so.
  The `reconcile` command and its tests do cover the comparison itself, on
  the fixture. The real `query_source` names (the classifier's, the
  subagent's) and the `source_map` are unknown until a smoke run shows them;
  the fixture uses placeholders.
- **The result-line cross-check** (section 11, 1% per class against the
  headless result line) waits on smoke check S13, which decides whether
  subagent tokens appear there at all.
- **Phase, wave and seat-to-key joins** come from `run.json`, which the
  probe operator writes per run. Nothing here derives them from marvel.
