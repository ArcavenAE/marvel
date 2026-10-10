# Watcher claude probe kit

Measures what a claude seat's hooks and statusline actually do, before the
watcher relies on them (`docs/design/marvel-watcher.md` section 6, plan row C1,
marvel#841). It is report-only: it measures and writes a file. It rings,
restarts and routes nothing, and it never touches a live seat, a daemon or a
real claude config.

## Run

```sh
WATCHER_PROBE_CREDENTIAL=<a key made for this run> \
  scripts/probes/watcher-claude/run.sh --scratch /abs/path/not/yet/used
```

`--dry-run` prints the paths the kit would use and creates nothing. The scratch
directory must be absolute, new or empty, and outside every place a real claude
keeps its state. The kit resolves symlinks before it compares, takes the home
directory from the passwd entry as well as `HOME`, and refuses an empty `HOME`. It holds the whole run: an empty `HOME` and `CLAUDE_CONFIG_DIR`,
the kit's own tmux socket, a throwaway repository, `events.tsv`, and the result.

## What it writes

`probe_result.json`: `build_stamp` (the claude version and the first 12 hex
digits of the binary's sha256) and `c1` to `c13`. Each check is `pass`, `fail`,
`observed` (a value with no right answer) or `not-run` (its marks are missing).

| key | measures |
|---|---|
| c1 | hooks that fire while the trust dialog is open |
| c2 | statusline cost and context movement while idle |
| c3 | exactly one `UserPromptSubmit` and one `Stop` or `StopFailure` per prompt; hooks become the turn source only if this passes |
| c4 | the ring form: which hook it produced and which payload keys it held |
| c5 | the ring's latency to its first submit, in ms |
| c6 | denial text |
| c7 | what an interrupt does (stops and failures) |
| c8 | turn hooks with no prompt sent (fails on any); statusline changes are recorded |
| c9 | whether a hook's stdout reaches the context |
| c10 | what an exit 1 and an exit 2 do to a prompt |
| c11 | subagent hooks and whether they carry an agent id |
| c12 | whether the turn's `Stop` comes before a background task ends |
| c13 | whether a hook can see the claude version (payload field or environment) |

Section 6 lists twelve measurements for thirteen keys. The kit reads the ring
form's source and its latency as two (c4, c5).

## Parts

- `run.sh`: the driver. It writes the scenario marks and stops with the pane saved on any timeout.
- `cmd/watcherprobe`: the hook and statusline loggers, the mark writer and the checker. A hook command prints nothing and exits zero whatever happens.
- `hook-canary.sh`, `hook-exit.sh`: the two helper hooks behind c9 and c10.
- `key-helper.sh`: claude's `apiKeyHelper`. The credential reaches claude through it and not through `ANTHROPIC_API_KEY`, so claude has no custom-key dialog that could show part of it.
- `lib.sh`: `save_capture`, which every capture the driver writes goes through. It hides anything shaped like an API key and any run of 8 or more characters of the credential, so no key text lands in a file.

## Status

The loggers and the checker are covered by `internal/watcherprobe` and
`cmd/watcherprobe`, and the kit's guards (scratch placement, the credential never
printed, a socket on every tmux call) by `kit_test.go`. The driver has not been
run against a live claude: the dialog and permission-prompt handling is a first
guess, and C2 (the probe run) is where it gets measured and corrected. A hook's
logged environment names `CLAUDE*` variables and records a value only for names
ending in `VERSION`.
