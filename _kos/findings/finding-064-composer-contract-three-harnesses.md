# finding-064: the composer contract of claude, codex and opencode under marvel's inject, clear and repaint

**Date:** 2026-10-02 (probes), harvested 2026-10-03
**Tickets:** `aae-orc-g88i1` (composer contract), `aae-orc-d1ldq` (repaint), `aae-orc-dgb35` (claude captures)
**Placement:** marvel. The subject is how marvel drives an interactive harness through a tmux pane: submit, clear, interrupt, repaint. The harnesses are the objects; the probes were commissioned for marvel's `inject`, `capture --repaint` and `--clear`, and their results became marvel#478, #487, #501, #508 and #511 (#491, #492, #494, #506 and #509 were earlier rounds, closed and superseded).
**Medium:** a scratch tmux server per run (`tmux -L <unique>`), a throwaway harness session in an empty scratch dir, never a live seat. Submit was decided from each harness's own store (a per-run nonce), never from the screen.

## The finding

1. **Submit is bracketed paste then Enter, for all three.** Literal newlines typed with `send-keys -l` stay in the draft and do not submit, in codex and opencode alike.
   - **Exception that matters:** on a codex startup MENU, a newline selects the highlighted option. On 2026-10-02, two inputs sent to codex's "Update available" menu started the vendor's `curl | sh` installer. Both installers were killed mid-download and nothing was installed.
2. **Clear is one C-c on a non-empty composer, and C-c on an EMPTY composer exits codex and opencode** (one press, no confirmation).
   - Escape does nothing to a draft in either.
   - C-u removes about one line at a time in codex, so it can't be used to clear blind.
   - codex writes a C-c-cleared draft to `history.jsonl` (up-arrow recall), but no session file, so nothing is submitted.
   - Rule for marvel: send C-c only after a capture shows text in the composer and no turn running, and send it exactly once.
3. **Interrupt differs per harness:**

   | Harness | Interrupt | C-c during a turn |
   |---|---|---|
   | codex | one Escape | interrupts, process lives |
   | opencode | two Escapes (the first only arms it) | exits the process |
   | claude | **not measured**: the probe showed only that Escape leaves a staged draft alone (marvel#511 records the key as "not measured") | not measured |

   claude's clear is measured: one C-c cleared a staged draft of any line count and submitted nothing (2.1.288, marvel#511).

4. **A claude composer reads as idle only beside a finished turn's done line.** No spinner shows while a reply streams, so the absence of a spinner is not evidence of idle. Model-controlled text can carry the same marker shapes: a reviewer had the model echo `✻ Worked for 3s · done …`, `· Cooking… (3s · thinking)` and similar lines, and they rendered indented under the reply's `⏺` bullet, not at the margin. So the reader trusts a marker only at the margin, and a pane whose done line has scrolled off reads `unknown` (the mimicry measurement is in #492's approval review 5398089990; also #494's reviews 5398108634 and 5398161520, #507's review 5398646684 for the scrolled-off case, and #511's tightening of `holds_text`).

5. **Repaint needs a real size change.**
   - A tty `TIOCSWINSZ` of cols+1 and back, sent from outside the pane, repaints claude, opencode and codex. tmux `pane_width` is unchanged throughout, and the restored screen is identical to the pre-stale one.
   - A bare SIGWINCH with no size change repaints none of them.
   - This settled marvel#478's design.
6. **codex launch facts marvel depends on** (codex-cli 0.157.0):
   - `-c check_for_update_on_startup=false` suppresses the update menu. That is marvel#487's seeded key.
   - Folder trust passed as an inline table, `-c 'projects={"<dir>"={trust_level="trusted"}}'`, is honored with no config write. The dotted form `-c 'projects."<dir>".trust_level="trusted"'` is NOT honored.
   - A scratch `CODEX_HOME` fails twice over: the daemon socket path exceeds the Unix limit (`--no-daemon` avoids it), and the login lives in `CODEX_HOME/auth.json`, so a scratch home lands on the sign-in screen.
   - `packages/app-server-daemon/auto-update-version` was rewritten (same value) even with `--no-daemon` and the update check off.
7. **Escape-sequence content survives a plain-text scrub.** claude's `.ansi` captures carried an OSC 8 hyperlink whose target was the sandbox `file://` URL. A plain-text scrub could not see it; fixtures need an escape-aware scrub (the fixtures that landed in marvel#508 were scanned clean).

## Unmeasured, on purpose

- The approval-menu cancel for codex and opencode needs a tool call or a cloud model, which was ruled out (2026-10-02T22:32Z).
- claude's interrupt key. It stays an open question in `question-session-state-observation` until a probe measures it.

## Edges

- informs: `elem-runtime-adapter-framework` (the interactive half of the codex and opencode adapters: submit, clear and interrupt keys, and the update-menu hazard)
- evidence for: marvel#478 (repaint), #487 (update check off), #501 (inject refused on the update menu), #508 (claude composer reader, fixtures from these captures), #511 (the per-adapter composer contract, and clear only beside a done line)

Data: `.session/research/2026-10-02-handoff-measurements/result-composer-codex-opencode.md`, `composerprobe.sh`, `repaintprobe.sh`, `claude-composer-captures/` (scrubbed).
