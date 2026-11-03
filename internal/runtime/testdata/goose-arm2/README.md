# goose arm 2 recordings (Ollama)

Lines of `goose run --output-format stream-json` recorded on 2026-10-31 against local Ollama
(goose v1.57.0, qwen3:0.6b), kept so the L2 parser (aae-orc-g6hla) is written against the real
line envelope and not a synthetic one.

Each `R<n>.stdout.jsonl` holds the first line of every distinct shape in run R<n>, in stream
order, byte for byte except for the redaction below. A run streams one `message` line per chunk
(hundreds to tens of thousands), so only one line per shape is kept.

Envelope:

- `{"type":"message","message":{id,role,created,content:[...],metadata}}`. `content` items are
  `thinking`, `text`, `toolRequest`, `toolResponse` or `error`.
- `{"type":"complete",total_tokens,input_tokens,output_tokens,cache_read_input_tokens,cache_write_input_tokens}`,
  once per successful run, with accumulated totals.
- `{"type":"error","error":"..."}` after a failed request (R4). R2 ends with no `complete` line
  and exits 1 with its reason on stderr.

Runs: R1 tool call (`tree`) and answer; R3 denied tool (`shell` under never_allow); R4 unknown
model (404); R5 plain answer; R2 tool outside the permission list in `approve` mode.

What Ollama does not show: `thinking.signature` is empty, `id` is `chatcmpl-N`, cache token
counts are 0, and there is no cost. Bedrock streaming, usage and cost fields are not recorded.

Added in a second commit, trimmed (the raw runs are 940, 15610 and 40958 lines):

- `R9.stdout.jsonl`, 8 of 940 lines: the first line, the line before the tool lines, the one
  `message` line carrying nine parallel `toolRequest` items, the one carrying the nine
  `toolResponse` items, the next two lines, and `complete`. This is the multi-tool sequence.
- `R10.stdout.jsonl` and `R11.stdout.jsonl`, 43 of 15610 and 43 of 40958 lines each: the first
  40 lines, then the last two `message` lines and `complete`. `created` is whole seconds and
  ticks about once per 9 to 60 chunks, so the first 40 lines show a steady stream, and the raw
  runs never had a `created` step above 1 s. R10 ran 27m49s and R11 10m32s with no timeout.
  The 4 s `created` steps in R9 and the 30 s one in R8 are the only larger steps in the set.

`SHA256SUMS.txt` covers only the files in this directory, so it differs from the raw-set sums
(212f0493), which list every raw file.

Redaction: the scratch working directory prefix is `/redacted`. In `R2.stderr` one em dash in
goose's text became a colon. Source evidence set sha256 of SHA256SUMS.txt: 212f0493.
