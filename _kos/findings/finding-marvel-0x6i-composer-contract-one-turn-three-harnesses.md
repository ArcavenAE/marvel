# finding-marvel-0x6i: the composer contract after one turn, three harnesses measured and one read

**Date:** 2026-10-30
**Confidence:** frontier. Read from marvel main 04c258f; no new harness measurement. Builds on finding-064 (the 2026-10-02 probes).
**Probe:** none new; a code and ticket read for aae-orc-g88i1, aae-orc-dgb35 and aae-orc-cw2d7 (operator ruling I-29, stream 2).
**Placement:** marvel. The subject is how marvel drives a harness composer through inject: submit, clear, send-key, verify, and who typed. The harnesses and director are objects.

## Why this matters

Three open tickets describe the composer contract as unbuilt. Most of it is built, and the parts that are not are narrower than the tickets say. Reading the gap correctly decides the next increment for each, and keeps unmeasured harnesses from inheriting fields measured on three.

## Result

1. **The per-adapter contract exists and is half consumed.** `ComposerContract` (`internal/runtime/adapter.go:343-397`) carries measured submit, clear and interrupt for claude, codex and opencode (marvel#511). The daemon reads only `ClearKey` (`internal/daemon/daemon.go:2655-2668`). Submit is one global path (`internal/tmux/driver.go:755`); `InterruptKeys` has no caller.
2. **Readers are uneven.** claude is read from an escapes capture with a done-line-only idle rule (`internal/composer/claude.go:60-156`). codex is read for its update menu only (`internal/composer/composer.go:87-115`). opencode has no reader.
3. **Verify-by-effect confirms "nothing staged", not "a turn started".** `Confirms(Submit)` accepts `empty` (`composer.go:134`), which a lost paste beside an earlier done line also produces. One read after one settle; opt-in; claude only.
4. **Identity is recorded, not surfaced.** Inject and refusal events carry the daemon-verified transport and the caller's declared session and user (`daemon.go:3522-3532`, marvel#472). On the local socket the declared parts are claims.
5. **No stale-draft check in marvel.** A literal inject to a claude seat holding a draft appends to it. director's composer-check refuses HAS-TEXT on the caller side.
6. **Version drift.** The claude contract and fixtures cite 2.1.288; live panes read in marvel#1143 ran 2.1.289 to 2.1.314.

## Limits

1. No harness was run. Every behavior claim about a harness is finding-064's, at the versions it names.
2. How often stale drafts happen today is unmeasured. marvel#340's follow-up traces most past cases to the #318 regression (fixed by #357) and to raw tmux typing; claude's dim suggestion is a likely third source, not confirmed.
3. One turn: crush, goose, pi and mini-swe-agent are unmeasured; their composers may not share the paste, Enter, C-c shape the contract assumes.

## Edges

- informs: `elem-runtime-adapter-framework`, `question-session-state-observation`
- extends: finding-064
- evidence for: aae-orc-g88i1, aae-orc-dgb35, aae-orc-cw2d7; marvel#340
