# finding-052: codex CTX% stayed blank for three stacked reasons, and two gaps remain

Work: arcaven-marvel-builder-g5-0, 2026-09-25 to 2026-09-27, from the
harvest of those days (placement ruling 2026-09-27). Verified against the
codex seed (`internal/runtime/codex_home.go`), `cmd/marvel/codexctx.go`, and
live seats on kinu, read only. Builds on finding-017 (codex context
pressure channel) and finding-023 (codex rollout reader).

**Placement.** The subject is marvel's CTX% feed for codex seats. codex is
the object whose behavior the feed has to survive, and the operator wrapper
is an input. Without marvel's feed there would be nothing to file.

## The mechanism, on 222478a and later

marvel#359 seeds three codex hooks into each session home: SessionStart,
Stop and PostToolUse. Each runs `marvel codex-ctx`, which reads the
rollout's latest `token_count` (`info.last_token_usage.input_tokens` over
`model_context_window`) and sends it on the heartbeat RPC. So a codex
seat's CTX% comes from the heartbeat, `ContextSource` heartbeat. The
statusline has no effect for codex, and the `codex exec` stream is a
running total, not a level (finding-017). codex runs a hook only once its
trust record holds: `[hooks.state."<resolved path>:<event>:0:0"]
trusted_hash`, the value `codex app-server` reports from `hooks/list`.

## Three causes, stacked (each alone kept the column at "-")

1. **The operator wrapper re-pointed CODEX_HOME.** The launch wrapper
   exported its own CODEX_HOME, so codex never read the seeded home and
   never ran the hooks. Routed to the wrapper's owner. It is not marvel
   code.
2. **The TUI exited on the socket-path limit.** codex 0.157's interactive
   TUI opens its app-server control socket at
   `CODEX_HOME/app-server-control/app-server-control.sock`. Under the old
   per-layout TMPDIR home that path ran 155 to 164 bytes against the
   macOS limit, and codex printed "path must be shorter than SUN_LEN"
   and exited before any hook ran. `--no-daemon` avoids the socket.
   Fixed in marvel#362 (short homes under `/tmp/marvel-h-<tag>`,
   live-verified at 86 bytes). The limit itself is 103 usable bytes, not
   104; see marvel#383.
3. **A long first turn never fires Stop.** aae/arcaven-reviewer-g5-0 read
   "-" because its first turn called `director.wait_for_message` inside
   the turn and then blocked at codex's command-approval prompt. No Stop
   hook fired, although its rollout read 7.22%. This is the approval
   policy, not the feed. Routed to the wrapper's owner with the
   folder-access prompt.

## Two gaps that remain (bd aae-orc-pt8k REMAINING)

- **No reading inside a long turn.** A seat inside one long turn reports
  only at SessionStart and Stop in practice. PostToolUse is seeded, but
  no reading from it was observed during the long turn above. Whether
  codex supplies a payload the reader can use there is unverified. A probe
  is needed before a fix can be designed.
- **The seeded `[projects]` untrusted entry names the daemon's cwd**
  (marvel-wt-318 in the observed case), not the directory the pane ends up
  in when a wrapper changes directory. The seed is right for marvel's own
  start directory. A role-declared workdir is aae-orc-g71ad, which
  resolves this.

## Defects this produced

- marvel#383: `CheckSocketPath` accepts the 104-byte path macOS refuses.

## Evidence

marvel#359 (merged 222478a), marvel#362 (merged in 7f62729), bd
aae-orc-pt8k notes, aae-orc-z5wuq (closed, fixed by #359, verified live).
Session snapshots and manifests in the builder's scratchpad were not kept.
