# finding-040: `marvel inject` cannot carry a programmatic dispatch, and fails partially rather than loudly

Date: 2026-09-15
Measured on: macOS arm64, marvel `0.1.0-alpha.20260915.164011.422eb11`, tmux driver, interactive Claude Code TUI (2.1.270) in detached panes
Extends: #202 (the non-submit defect); related #272 (the sibling `keys authorize` gap found in the same session)
Scope: `inject` as an operator surface. The conclusions are about marvel's side of the channel only.

## Why this matters

`inject` is the documented way an operator or a supervising process hands work to an interactive session. Treated that way, it does not hold. Three behaviours combine so that a dispatch can arrive altered, or arrive as a fragment, without anything erroring.

## 1. `inject` appends, it does not replace

Measured on a session whose input box was empty:

```
inject "<A>"   ->   ❯ <A>
inject "<B>"   ->   ❯ <A><B>
```

There is no replace, and no report of what was already there.

## 2. Combined with #202, a failed dispatch silently alters the next one

#202 records that `-e` does not submit: the text lands in the box and stays. With (1), the next dispatch to that session is prepended by the previous prompt and submits the concatenation. The second dispatch is therefore silently *wrong* rather than silently absent, which is harder to detect and worse when it runs.

## 3. There is no reliable way to clear the box

`inject "C-u" -l=false` clears the box of a session that has only ever been driven by `inject`. It does **not** clear a box holding text a person typed into the pane directly. Reproduced four or more times across two sessions in one fleet, same daemon, same tmux server. Both stuck boxes held text typed by hand that contained a multi-byte character.

The cause is not isolated here. Candidates are the multi-byte character, the provenance of the text, or something else. The measurable fact is that the same keystroke through the same code path clears one box and not another, which is enough to disqualify it as the basis of a protocol.

Consequence for an operator: a session whose box holds text that will not clear cannot be dispatched to at all, and there is no marvel-side way to recover it.

## 4. A long payload is chunked, and content can be lost

A payload of roughly 4.7 KB arrived in the box as `[Pasted text #1][Pasted text #2][Pasted text #3]...` and submitted. The receiving agent had the final portion of the text and not the opening portion: it replied asking what its task was, while quoting constraints that appeared near the end of the payload.

The chunking itself is the harness's paste handling and not marvel's. It is recorded here because `inject` is the surface that produced it, and because the combination is what an operator experiences: a dispatch that is accepted, submitted, billed, and acted upon in part.

## What the operator surface needs

1. **A replace mode.** `inject --replace`, clearing server-side through the same path the pane already uses, rather than hoping a control character is interpreted. This removes the compounding in (2) independently of the #202 fix.
2. **Failing that, disclosure.** If the box is non-empty, say so and say what is in it, so a caller can refuse instead of concatenating into it.
3. **A size behaviour that is defined.** Either chunk with a guarantee, or refuse above a threshold and say so. Silent partial delivery is the worst available option.

## The workaround in use, which needs nothing from marvel

Stop sending payloads. Write the brief to a file and inject a short pointer to it:

```sh
inject <session> "Read <path> in full and execute it exactly as written." -e
inject <session> "" -e -l=false
```

Length stops mattering, and chunking cannot truncate a one-line path. The first dispatch using this form landed intact.

## A reading hazard worth recording beside it

The input box is the `❯` line **between the pane title separator and the status separator**. A `capture | grep '❯' | head -1` returns a *submitted* prompt from the transcript instead, because submitted prompts carry the same marker. That produced two wrong readings of box state in one session. The reliable form:

```sh
marvel capture <session> \
  | awk '{l[NR]=$0} END{for(i=2;i<=NR;i++) if(l[i] ~ /^❯/ && l[i-1] ~ /─$/) print l[i]}'
```

## Addendum 2026-09-25: the non-submit defect is root-caused and fixed, not yet live

Section 2's compounding rests on #202 (`-e` stages the text without submitting). That half now has a cause and a fix:

- **Cause** (marvel#355, `docs/design/inject-submit-bracketed-paste.md`, aae-orc-6vcr2): marvel sends the text and its Enter in one tmux call, so the pane reads them in one read. Claude Code treats a long read as a paste and a carriage return inside a paste as a newline, so a dispatch of roughly 80 bytes or more is staged instead of submitted. The threshold is the harness's heuristic (between 60 and 80 bytes on 2.1.282) and can move.
- **Fix** (marvel#357, merged at 1df684c): deliver the text as a bracketed paste (`paste-buffer -p`) followed by the Enter, so the Enter lands outside the paste and submits.
- **Live state:** not yet running. The installed binary on the measuring host is `0.1.0-alpha.20260925.024343.8037d09`, and 1df684c is not an ancestor of 8037d09. The fix takes effect after an install that includes it and a `marvel daemon reexec` (or restart). Until then the workaround above stands, and the cost is manual: about 30 hand-typed Enters across one fleet day of director dispatches (2026-09-25).

Sections 1, 3 and 4 (append rather than replace, no reliable clear, long-payload chunking) are unchanged by this fix.
