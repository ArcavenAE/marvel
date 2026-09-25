# Why an injected message sits unsent in a Claude Code composer, and the fix

Status: design note, 2026-09-25. Probe run on a scratch tmux server and a
scratch Claude Code 2.1.282 (`--model haiku`, empty scratch directory), never on
a working seat. Answers the probe asked for in aae-orc-6vcr2. Rig:
`inject-submit-probe/`.

## The mechanism

**1. marvel delivers the text and its submit in one read.** `SendKeys`
(`internal/tmux/driver.go`) sends `send-keys -l TEXT ; send-keys Enter` as one
tmux invocation, the interleave guard from aae-orc-sa2yt. The byte probe shows
the target reading the text and the `\r` together:

| form | reads seen by the pane |
|---|---|
| marvel today: `send-keys -l TEXT ; send-keys Enter` | one read, `...composer\r` (81 bytes) |
| two tmux calls: `send-keys -l TEXT`, then `send-keys Enter` | two reads: the text (80 bytes), then `\r` 6 ms later |
| proposed: `paste-buffer -p` then `send-keys Enter`, one call | one read, `\x1b[200~...composer\x1b[201~\r` (92 bytes) |

`send-keys -l` never wraps text in bracketed-paste markers, even when the
application has turned bracketed paste on. Only `paste-buffer -p` does.

**2. Claude Code treats a long read as a paste, and a `\r` inside a paste as a
newline.** Same one-call form, same warm seat, varying only the length:

| text length | result |
|---|---|
| 39, 60 | submitted |
| 80, 88, 100, 150, 300 | staged: text plus an empty second line, no turn |

The threshold on 2.1.282 is between 60 and 80 bytes. It is Claude Code's
internal heuristic and can move. Of the 150 submitting injects in this host's
daemon log (`literal=true, enter=true`), 131 are 80 bytes or longer, so
director dispatches sit in the staging range.

**3. After a staged paste, the next Enter is swallowed and the second one
submits.** One separate Enter 15 s after staging did nothing, and a second
Enter submitted. The transcript of the scratch session is the proof, since it
records what the agent actually received:

- The `A1` turn arrived as `"<text>\nx"`. The first bare Enter was swallowed,
  the typed `x` landed on the second line, and `C-m` submitted both.
- `T1` and `T2`, two injects staged before any submit, arrived as **one** turn
  joined by a newline. This is the stacking seen on e98-architect.

This explains the operator's report. `Enter` and `C-m` are the same byte
(`\r`, verified). `C-m` "worked" because it was the second key, not because it
was a different key. The daemon log carries the same pattern for
e98-architect on 2026-09-24: a 1386-byte `enter=true` inject at 06:32:54, then
two bare 5-byte `Enter` keys at 06:36 and 06:37.

Not established: *why* Claude Code swallows the first Enter after a staged
paste. The fix below never enters that state, so it does not depend on the
answer.

**The regression window.** #323 (8467c33, 2026-09-21) sent the text and the
Enter as two tmux calls, and the `\r` usually arrived in its own read (6 ms
later on an idle host) and submitted. The one-call interleave guard landed with
#318 (97698a3, 2026-09-23) and put both into one read. So on a marvel built from
#318 or later, every submit of 80 bytes or more stages. The two-call form was never the fix either: it worked
because the pane happened to read between the two writes.

## Recommended fix

**A. Send the text as a bracketed paste (fix the cause).** Change the claude
path of `SendKeys` to `load-buffer -b <unique> -` from stdin, then
`paste-buffer -p -d -b <unique> ; send-keys Enter` as one invocation.

Verified on the scratch seat. The one-call bracketed form submitted at 100,
300 and 2000 bytes, each as exactly one user turn in the transcript. That
holds even though the paste, the end marker and the `\r` arrive in a single
read: the `ESC[201~` marker tells the parser where the paste ends, so the `\r`
after it is a keypress. Timing does not enter into it. The single-invocation
interleave guard stays.

- **Cost:** a small driver change and its tests. The buffer name must be unique
  per call, and `-d` deletes it (parallel safety). Text reaches tmux on stdin,
  not argv.
- **Limit:** tmux brackets only when the application has enabled bracketed
  paste (mode 2004). Claude Code does. codex and opencode are unverified, and
  where a harness does not enable it the bytes are the same as today, so there
  is no regression. The per-harness choice belongs in the composer contract
  (aae-orc-g88i1).
- **Cleanup:** once A lands, director's post-check that presses a second Enter
  must go. Against a staged stack, that Enter is what submits two messages as
  one turn.

**B. Acknowledge delivery from the harness, not the composer text.** Matching
"Director:" in the last composer line fails on a stack, and on Claude Code's
dim suggestion text. The source that cannot be fooled is the one this probe
used: a new user entry in the session transcript. marvel launches claude with
`--session-id` and knows the cwd, so the transcript path is known. Put a short
nonce in the injected text and wait for a user entry that contains it. A
`UserPromptSubmit` hook that writes to marvel's event ring would be the same
signal as a push. codex and opencode already emit `turn.started`.

- **Cost:** moderate, per adapter. It lands as the evidence source for
  verify-by-effect (aae-orc-dgb35), replacing CTX and cost movement there.
- **Rule:** a missing ack escalates. It never triggers an automatic resend,
  which is the ratchet the operator ruled out.

**C. Move payloads to the bus, and keep a doorbell (the direction).** The
dispatch body goes to the director inbox. Only a short, fixed, content-free
doorbell is injected, so a stacked or swallowed doorbell loses nothing (fln6p,
R-111, R-112).

- **Cost:** the largest. It depends on the seat draining its inbox (director
  PR #79, batch drain), and the doorbell still has to submit, so it needs A.
  A doorbell under 60 bytes submits today, but only because the threshold sits
  where it does on this version.

**Order:** A first, since it is small and removes the cause. B next, so a
failure is seen and reported. C as dispatches move to the inbox. None of the
three adds a retry, a sleep, or an extra keypress.

## Reproduce

- `inject-submit-probe/byte-reads.sh` shows the reads for the three forms. It
  uses a scratch tmux server and needs no harness.
- Claude Code behavior: run `claude --model haiku` in an empty directory on a
  scratch tmux server (`tmux -L <name>`). Inject each form, then read the user
  turns from `~/.claude/projects/<encoded-cwd>/<session>.jsonl`. Do not use a
  working seat.

Related: finding-184 and finding-186 (orc), aae-orc-2uiw9, aae-orc-sa2yt,
marvel#202, #323, #324, #340, #342.
