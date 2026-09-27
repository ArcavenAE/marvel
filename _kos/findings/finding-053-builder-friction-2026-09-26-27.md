# finding-053: build friction in marvel, 2026-09-26 and 2026-09-27

Work: arcaven-marvel-builder-g5-0, harvest placement ruling 2026-09-27
("friction goes in a finding first"). Each item is marvel's own tooling,
tests or adapter behavior, met while building marvel#375, #376, #378 and
#381. Error text is verbatim.

**Placement.** marvel is the subject: its commit hooks, its test harness
and its adapters. Items about the shared checkout, the harness sandbox or
the network are left out; they have no marvel home.

## Friction

1. **A failed pre-commit hook reads as a commit when its output is
   filtered.** lefthook's errcheck failed the #375 red commit on an
   unchecked `CloseBolt()`, and the commit did not land (HEAD unchanged).
   Piping the commit's output through a filter hid the failure. Check
   `git log -1` after any commit whose output was piped.
2. **gofumpt realigns a whole const block.** Adding a commented `Kind`
   constant made the neighbours misalign:
   `internal/events/events.go:25:1: File is not properly formatted (gofumpt)`.
   Run `gofumpt -w` after any const-block edit. It also causes adjacency
   conflicts when two PRs each add a kind (#376 and #381 met this).
3. **Inserting a method under an existing doc comment steals it.**
   `ST1020: comment on exported method ValidateTeamNames should be of the form "ValidateTeamNames ..."`
   after a method went in between `Apply`'s doc comment and `Apply`.
4. **`PaneStatus` reads "gone" only from a live tmux server.** A test with
   no server running got
   `pane-status %987654: error connecting to /private/tmp/tmux-501/marvel-test-session-87005 (No such file or directory)`,
   and `ReapDead` (correctly) refused to act. Start a session on the
   driver before asserting on an unknown pane.
5. **The generic adapter shell-quotes Args itself.** A manifest arg that
   carried its own quotes ran as
   `sh -c ''\''env > ... && sleep 300'\'''` and did nothing. Write Args
   unquoted.
6. **Scratch daemon sockets under the scratchpad are too long.** The
   daemon socket moved to a short `/private/tmp/...` path. Same limit as
   finding-036 and finding-012 record. The off-by-one in the check is
   marvel#383.

## Defects noted (local note, then issue)

- **Tests write harness homes into the live base.** Session tests build a
  manager with `NewManager` and never override `HarnessHomeDir`, so codex
  sessions they create land under the operator's `/tmp/marvel-h-<tag>`.
  Four leaves were there at time of writing. marvel#384.
- **`CheckSocketPath` accepts 104 bytes; macOS binds at most 103.**
  Measured with Python `socket.bind` and Go `net.Listen`. marvel#383.

## Recorded elsewhere, not repeated here

- The kill-failed row defect: marvel#364, fixed by #376.
- The team-name collision: marvel#319, PR #378.
- The seat environment leak: aae-orc#418, PR #381.
