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

| key | name | measures |
|---|---|---|
| c1 | `pre_session_hooks` | hooks that fire at the trust dialog and the setup menu |
| c2 | `idle_cost_moves` | statusline cost and context movement while idle |
| c3 | `one_submit_one_stop` | exactly one `UserPromptSubmit` and one `Stop` or `StopFailure` per prompt; hooks become the turn source only if this passes |
| c4 | `ring_form` | one key for the exact ring form: its source (the payload's `source`, its event and payload keys) and the submit-to-hook latency in ms |
| c5 | `denial_reason` | `PermissionDenied.reason` plus the `Notification.notification_type` values seen |
| c6 | `interrupt_event` | `stop`, `stopfailure` or `none` after Esc |
| c7 | `nonprompt_moves` | turn hooks with no prompt sent (fails on any); statusline changes are recorded |
| c8 | `idle_notification_s` | seconds from when claude went idle to the `idle_prompt` notification, or `never`. Claude went idle at `setup-closed`, or at the last `Stop` or `StopFailure` before the wait window opened, whichever is later; the window's own start if neither is marked. The result's note says which. The driver opens the wait straight after setup, before c2's idle sleep, so a notification that fires early is inside it |
| c9 | `hook_stdout_reaches_context` | whether a hook's stdout reaches the context |
| c10 | `hook_exit2_effect` | what an exit 2 does to a prompt |
| c11 | `subagent_stamps` | subagent hooks and whether they carry an agent id |
| c12 | `stop_background` | whether the turn's `Stop` comes before a background task ends |
| c13 | `version_visible_to_hook` | whether a hook can see the claude version (payload field or environment) |

Keys stay `c1` to `c13`; each check also carries its `name`. Section 6 of the
design lists twelve measurements for these thirteen keys: c8 was lost when it
was condensed, and c4 holds the ring form's source and latency together.

## Parts

- `run.sh`: the driver. It writes the scenario marks and stops with the pane saved on any timeout.
- `cmd/watcherprobe`: the hook and statusline loggers, the mark writer and the checker. A hook command prints nothing and exits zero whatever happens.
- `hook-canary.sh`, `hook-exit.sh`: the two helper hooks behind c9 and c10.
- `key-helper.sh`: claude's `apiKeyHelper`. It avoids `ANTHROPIC_API_KEY`, so claude has no custom-key dialog that could show part of the key. It does not take the key out of claude's environment: claude and its hooks inherit `WATCHER_PROBE_KEY` from the scratch tmux server, and so do Bash-tool commands unless something scrubs them.
- `lib.sh`: `save_capture`, which every capture the driver writes goes through. It hides anything shaped like an API key and any run of 8 or more characters of the credential, so no key text lands in a capture. The hook and statusline loggers mask each payload the same way before it reaches `events.tsv`, and the checker masks `probe_result.json`, since c6 copies notification text.

## Status

The loggers and the checker are covered by `internal/watcherprobe` and
`cmd/watcherprobe`, and the kit's guards (scratch placement, the credential never
printed, a socket on every tmux call) by `kit_test.go`. The driver has not been
run against a live claude: the dialog and permission-prompt handling is a first
guess, and C2 (the probe run) is where it gets measured and corrected. A hook's
logged environment names `CLAUDE*` variables and records a value only for names
ending in `VERSION`.
