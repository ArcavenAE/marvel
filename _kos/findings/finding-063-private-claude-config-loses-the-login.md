# finding-063: a private CLAUDE_CONFIG_DIR loses the operator's login on macOS, and a realpath trust key clears the dialog

**Date:** 2026-10-02 (probe), harvested 2026-10-03
**Probe:** SB-3 r2 in `docs/design/seat-bootstrap.md` (operator-run on kinu, Claude Code 2.1.288); the outcome table and the redesign it forced are in marvel#490, section 5a.
**Subject:** marvel's seat bootstrap: whether marvel can give an interactive claude seat its own config home.
**Status:** measured, one run, one host, one version.

## The finding

1. **A private `CLAUDE_CONFIG_DIR` does not carry the login.**

   | Case | `claude auth status` exit | `loggedIn` |
   |---|---|---|
   | the operator's own config | 0 | true |
   | private `CLAUDE_CONFIG_DIR`, unseeded | 1 | false |
   | private, seeded with `hasCompletedOnboarding` and the trust key | 1 | false |

   An interactive `claude --setting-sources user` under the seeded private home, captured after 12 seconds, showed the login screen. The status check and the screen agree (director judged it outcome C, not D).
2. **A trust key at the workdir's realpath clears the trust dialog.** The same capture showed no trust dialog and no theme screen. Claude Code keys trust by realpath: the seeded key was `/private/tmp/...`, not `/tmp/...`.
3. **Claude Code writes its own state into whatever config it is given.** It added `firstStartTime`, `autoUpdates`, `machineID`, `userID` and other keys to the private file (names read only).

## What it changes

- The per-seat private config home is ruled out on macOS (`grv-private-claude-config-home-macos`). Where the login is a file (Linux, a container) the question is open and needs its own run.
- It extends finding-025 (a `HOME` override de-authenticates the harness) to `CLAUDE_CONFIG_DIR`, which 025 did not cover.
- It answers finding-045's mechanism question (can marvel pre-clear the trust gate): yes, with one key at the realpath. How marvel should write that key is open design (`question-seat-harness-state`).

## Limits

- One run, kinu only, 2.1.288. The keychain lookup's dependence on the config directory is inferred from the result, not read from Claude Code.
- No login was attempted inside the private home, by design: that would have written a refresh token under a marvel-held directory.

## Edges

- extends: finding-025
- answers: finding-045 (mechanism)
- rules out: `grv-private-claude-config-home-macos`
- informs: `question-seat-harness-state`
