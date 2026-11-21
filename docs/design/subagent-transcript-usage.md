# Count a seat's subagent and workflow transcripts in its spend

- **Status:** design for review, 2026-11-21. Nothing here is built. A build waits until this design is reviewed and the operator says go.
- **Author:** the arcaven-ent team's architect seat, from a six-seat design party.
- **Checked at:** marvel `origin/main` a007c4a6. Every line cite is at that commit.
- **Related:** `internal/daemon/transcript_usage.go` (the spend reader), `internal/usage/accountant.go` (the accountant), `docs/design/get-sessions-output.md` section 4.3, `_kos/ideas/inference-backend-registry-and-quota-estimation.md`.
- **Prior art, design only:** cc-switch (farion1231/cc-switch, MIT), read at commit `2c084ec752a985fa77470facc14cad76ee67e8e1`, for its file layout and its rule on which records count. No code is copied; marvel is Go and cc-switch is Rust. Cites written `cc-switch <path>` are under that repository's `src-tauri/src/`.

## 1. Why

marvel meters a claude seat's spend in one of two ways, and only one of them counts subagents:

- **The accountant counts them.** On a seat it meters, it adds a subagent sample to spend (`internal/usage/accountant.go:487-489`).
- **The transcript reader does not.** It meters the other claude seats, skipping any seat the accountant owns (`transcript_usage.go:160-162`). It reads the main transcript only: "the main transcript only: subagent transcripts are not counted" (`:27`). `findTranscript` stats `<projects>/<dir>/<id>.jsonl` and nothing under it (`watchdog.go:735-760`).

So on a transcript-reader seat, a seat that spawns subagents reads as cheap while its account drains. The subagent requests are billed to the same account. cc-switch reads them from `<dir>/<id>/subagents/*.jsonl` and `<dir>/<id>/subagents/workflows/wf_*/*.jsonl` (cc-switch `services/session_usage.rs:495-506`).

The fix brings the transcript reader in line with the accountant. An operator scheduling against a quota wall then sees the seat that uses it.

## 2. What already works and stays

- **Dedup per message id, at the largest figure seen.** A replaced or truncated file keeps its per-id figures, so a re-read adds nothing for an id already counted (`transcript_usage.go:206-211`). The maps are the seat's, not the file's (`:76-84`).
- **An exact restart.** `seed()` rebuilds the per-id figures from the file up to the stored offset and inode, so the sum stays exact (`:355-388`; test `TestRestartOverTheSameFileKeepsTheSumExact`, `transcript_usage_test.go:448`). The header comment at `:25-26`, which says a restart marks the sum partial, is out of date; M2 corrects it.
- **The loose rule on which records count.** A record counts when `output_tokens` is an integer, zero included (`:283-286`). cc-switch dropped a stricter rule (a `stop_reason`, and output above zero) after measuring that it under-counted subagent and workflow files (cc-switch `services/session_usage.rs:884-895`). marvel needs no change here, only a test that pins it for subagent files.
- **`partial`.** It means the sum is a lower bound (`transcript_usage.go:200-203`). It is restored across restarts (`:249`) and never clears within a daemon's life (`TestPartialFlagDoesNotClearWithinADaemonLife`, `transcript_usage_test.go:326`). This design does not set it.

## 3. The change

- **Find the seat's files at two fixed depths.** Besides `<id>.jsonl`, the reader matches `<dir>/<id>/subagents/*.jsonl` and `<dir>/<id>/subagents/workflows/wf_*/*.jsonl`. There is no recursion and no other file name.
  - The files are found from the same marvel-assigned session id and config dir.
  - No symlink that leads out of the seat's directory is followed.
  - A workflow directory's `journal.jsonl` has no assistant rows (cc-switch `services/session_usage.rs:502-504`), so it adds nothing.
  - The workflow layout is cc-switch's reading. It has not been observed on Claude Code 2.1.329, so one workflow run confirms it before M1 lands.
- **One map per seat, one cursor per file.** All of a seat's files fold into the seat's existing per-id peak map, so an id seen in two files counts once, at its largest figure. Each file keeps its own offset, inode and size.
- **A list of places.** The stored place (`SpendTail`, `internal/api/types.go:1628-1632`, held in the field at `:695-698`) becomes a list of `(relative path, offset, inode)` entries, so a restart over unchanged files stays exact. A file that first appears later is read from byte 0, which is also exact.
- **Upgrade.** A single place stored by an older daemon is read as the main file's place. The subagent files are read from byte 0, adding to the total; this is not `seed()`, which adds nothing (`transcript_usage.go:355-357`). `partial` is left as stored, since the result is exact. The change of scope is recorded in its own marker on the stored sum, not in `partial`.
- **The name says the scope.** The field and its docs say that seat spend now covers the main transcript plus subagents and workflows, and the scope marker shows which sums do. Otherwise a comparison with an older figure reads a jump that is really a definition change.

## 4. What it never does

- Read a seat the accountant owns, a session marvel did not launch, or a client workspace. Account-wide use is already inside the account's window reading.
- Read any field beyond `type`, `message.id` and `message.usage.*` (`transcript_usage.go:268-280`). Transcript text, tool input and tool output are never decoded.
- Price anything.
- Dedup across seats. A fork or a successor that copies history into a new seat is a separate claim, and is filed only if it is measured.

## 5. Red tests

All are in `internal/daemon/transcript_usage_test.go`, with synthetic fixtures and the existing usage rig. Each fails at a007c4a6, where nothing under `subagents/` is read, and passes once M1 lands.

- `TestSubagentTranscriptCountedUnderTheSeat`
- `TestWorkflowTranscriptCountedUnderTheSeat`
- `TestMessageIdInMainAndSubagentFileCountsOnce`
- `TestRestartWithSubagentFilesKeepsTheSumExact`
- `TestUpgradeReadsSubagentFilesWholeAndStaysExact`: a single stored place from an older daemon; it asserts the exact total, an unchanged `partial`, and the scope marker set.
- `TestSubagentRecordWithZeroOutputCountsItsPromptClasses`
- `TestOnlyJsonlAtTheTwoDepthsIsRead`: the fixture holds a `journal.jsonl`, a non-jsonl file and a deeper `.jsonl`, none of which add anything, plus one counted subagent file.
- `TestSubagentSymlinkOutOfTheSeatIsNotRead`: the fixture holds one in-seat subagent file that must count and one symlink out that must not.

## 6. Rulings this design needs

- **The go:** whether to build this, after review.

## 7. Plan

| ticket | does | depends on | red tests it turns green |
|---|---|---|---|
| M1 | find, fold and persist every file of a transcript-reader seat (sections 3 and 4) | the go; one observed workflow run | all eight in section 5 |
| M2 | correct the stale header comment at `transcript_usage.go:25-26` | none | none: a comment fix, reviewed by reading |

Plan check:

- M1's tests live in the package M1 changes (`internal/daemon`), next to `TestRestartOverTheSameFileKeepsTheSumExact`, and use the same rig.
- Each test fails at a007c4a6 because of the counted file it carries, including the two tests whose point is what is not read.
- M2 changes no behaviour and needs no test. It can land with M1 or alone.
- Cross-seat dedup is not a ticket. It is filed only if it is measured.
