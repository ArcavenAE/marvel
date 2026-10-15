# Fixture run, and the hand count

Every number below was counted by hand from `run/` and `otel.jsonl`. The
tests assert these numbers, not what the reader computes. Tokens are
`input / 5m write / 1h write / read / output`. Times are 2026-10-15 UTC.

## Records

| seat or spawn | id | time | tokens | note |
|---|---|---|---|---|
| orch (opus) | m_o1 | 10:00:00 | 3 / 1000 / 0 / 0 / 10 | cold start; written twice (streaming copy) |
| orch | m_o2 | 10:00:30 | 2 / 200 / 0 / 1000 / 40 | dispatch |
| orch | m_o3 | 10:01:00 | 1 / 0 / 0 / 1200 / 5 | only tool call is `await` |
| orch | m_o4 | 10:02:00 | 1 / 0 / 0 / 1200 / 5 | only tool call is `await` |
| orch | m_o5 | 10:20:00 | 2 / 1150 / 0 / 0 / 30 | 18 minute gap; cache death and cold start |
| orch | m_o6 | 10:20:10 | 5 / 0 / 0 / 0 / 0 | both cache classes zero: undetermined |
| orch/agent-a1 (sonnet) | m_s1 | 10:00:40 | 4 / 300 / 0 / 500 / 20 | warm: read is half of the ceiling, 1000 |
| orch/agent-a1 | m_s2 | 10:00:50 | 2 / 100 / 0 / 800 / 60 | |
| orch/agent-a2 | m_t1 | 10:00:45 | 1 / 900 / 0 / 0 / 10 | cold; its first record (10:00:38) is before a1's first response (10:00:40): concurrent cold write |
| orch/agent-a2 | m_t2 | 10:00:55 | 1 / 0 / 0 / 900 / 15 | |
| r1-c1-w1-g1-a1 (sonnet) | m_b1 | 10:00:50 | 3 / 400 / 100 / 0 / 10 | cold; the 1-hour write fails the pin |
| r1-c1-w1-g1-a1 | m_b2 | 10:01:10 | 2 / 0 / 0 / 500 / 30 | |
| r1-c1-w1-g1-a1 | m_b3 | 10:01:20 | 0 / 0 / 0 / 0 / 0 | API error row: undetermined |
| sup | none | | | no transcript: unmeasured |

## Counts

- Requests: 13 after dedupe (14 records read, one duplicate).
- Run total: 27 / 4050 / 100 / 6100 / 235.
- orch main: 14 / 2350 / 0 / 3400 / 90 (6 requests). a1: 6 / 400 / 0 / 1300 / 80. a2: 2 / 900 / 0 / 900 / 25. Seat orch with both: 22 / 3650 / 0 / 5600 / 195 (10).
- Branch seat: 5 / 400 / 100 / 500 / 40 (3).
- By role: vsdd-orchestrator 14 / 2350 / 0 / 3400 / 90; vsdd-implementer 13 / 1700 / 100 / 2700 / 145 (7).
- Phase p3 (to 10:10): 20 / 2900 / 100 / 6100 / 205 (11). Phase p4: 7 / 1150 / 0 / 0 / 30 (2).
- 1-hour share: 100 of 4150 write tokens, 2.4%; record m_b1.
- Cold starts: m_o1, m_o5, m_t1, m_b1. Undetermined: m_o6, m_b3. Cache death: m_o5 (gap 1080 s over 300 s; write 1150 against half of 1201). Concurrent cold write: a2.
- Hub wait, wave 10:00:30 to 10:02:30: m_o2, m_o3, m_o4 = 4 / 200 / 0 / 3400 / 50. Await-only: m_o3, m_o4 = 2 / 0 / 0 / 2400 / 10. Union: the three window requests.

## Dollars (prices.json, per million tokens)

- opus, orch main: 14 x 4 + 2350 x 5 + 3400 x 0.20 + 90 x 20 = 14286 millionths = $0.014286.
- sonnet, a1 + a2 + branch: 13 x 2 + 1700 x 2.50 + 100 x 4 + 2700 x 0.10 + 145 x 10 = 6396 millionths = $0.006396.
- Transcripts: $0.020682. Seat orch: 14286 + (8 x 2 + 1300 x 2.50 + 2200 x 0.10 + 105 x 10) = 14286 + 4536 = $0.018822. Branch seat: $0.001860.
- OTEL `auto_mode_classifier` (sonnet; a placeholder source name): 50 x 2 + 40 x 2.50 + 12 x 10 = 100 + 100 + 120 = $0.000320, the 40 creation tokens at the 5-minute rate. Total with it: $0.021002.

## OTEL file

Main-thread events: 19 / 2850 / 3900 / 130 (input, creation, read, output),
equal to the transcripts' non-sidechain totals (orch plus branch seat; creation
is 5m plus 1h). Subagent events: 8 / 1300 / 2200 / 105, equal to a1 plus a2.
The classifier source has no transcript counterpart.
