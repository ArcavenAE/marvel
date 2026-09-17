# finding-041: the marvel daemon must start in a keychain-capable macOS session

Date: 2026-09-17
Scope: marvel operations, macOS auth, cast-launch verification
Status: measured

## Symptom

A fleet-wide "Not logged in" failure: every cast claude session came up at
the login prompt.

```
❯ say only the word ready
  ⎿  Not logged in · API Usage Billing · Please run /login
```

The panes were healthy, `marvel get sessions` showed `running`, and every
agent could do nothing. This is the HEALTH-is-liveness gap in the large
(aae-orc-9box, finding-025): the process is alive and the pane exists, so
health calls it healthy while the agent sits unauthenticated.

The obvious first suspect, an overridden `HOME`, was ruled out (finding-025
covers that separate route). `HOME` was correct here and the failure held.

## Mechanism: two layers, the second decisive

**Layer 1: the daemon inherits Claude Code's own session markers.**
Starting the daemon from inside a Claude Code Bash tool leaks `CLAUDECODE`,
`CLAUDE_CODE_*`, and `AI_AGENT` into the daemon process, and from there into
every cast child. A harness launched with these set behaves as if it were
running under another harness rather than as a fresh top-level session. This
is real and worth stripping, but on its own it is not what produced the
login prompt.

**Layer 2 (decisive): a detached-tmux-parented daemon runs in a macOS
security session that cannot read the login keychain.** macOS gates keychain
access by the caller's security/audit session, not by environment or by
`HOME`. A daemon parented under a detached tmux server is a member of a
security session that has no access rights to the "Claude Code-credentials"
item in the login keychain. So every cast `claude` resolves credentials, is
refused at the keychain read, and falls back to the login prompt, even with
a clean environment and a correct `HOME`.

## Proof

Three observations taken together isolate the security session as the cause:

1. A scrubbed bare `claude -p` run from a keychain-capable shell (a normal
   login terminal) authenticated and answered. So the account and the
   credential are good.
2. The credential existed in `login.keychain-db` at the time of the failure.
   So the item is present; the daemon-parented child could not read it.
3. The cast child's environment was clean and its `HOME` was correct, and it
   still failed. So neither the leaked markers (layer 1) nor a moved `HOME`
   (finding-025) accounts for this instance; the remaining difference is the
   security session the process is a member of.

Layers 1 and 2 are independent. Layer 1 can degrade a session on its own;
layer 2 denies authentication outright. Fixing only layer 1 leaves the login
prompt in place.

## The shape that works

Start the daemon directly in a keychain-capable session, with no detached
tmux server as its parent, and strip the harness markers on the way in:

```sh
env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u AI_AGENT \
  marvel daemon --socket "$D/m.sock" ...
```

Use `-u` for each `CLAUDE_CODE_*` variable actually present in the launching
environment; the three names above are the ones observed to leak from a
Claude Code Bash tool. The load-bearing part is not the `env -u` line but
where the command runs: the operator's own login terminal is the safest
hand, because it is already a member of the security session that holds the
login keychain. A daemon started there passes that access to the cast
children it spawns.

This does not contradict finding-025's scratch-layout recipe; the two
compose. Keep `HOME` real (finding-025), keep the security session
keychain-capable (this finding), and move marvel's own state off `HOME`
through the individual flags. All three conditions must hold together for a
cast claude to authenticate.

## Consequence

Any launch path that puts the daemon under a detached tmux server, a
`launchd` agent in the wrong session type, or an ssh session without the
keychain, inherits this failure. The daemon reports nothing wrong because
from marvel's side nothing is: the panes are live, the reconciler is
content, and the credential read happens inside the harness where marvel
does not look.

## Aftermath and follow-ons

- The detection half is the tmux harness-state watchdog idea
  ([[tmux-harness-state-watchdog]]): this finding is the daemon-side cause of
  the same fleet-wide incident, and the watchdog is the signal that would
  have surfaced the logged-out state minutes before a human noticed. The two
  are complementary, cause and detection.
- The health-surface half is aae-orc-9box: marvel reports `running` and
  `healthy` for a session whose agent is stuck at a login prompt. Expired
  credentials, a revoked token, and an unreachable model all present the
  same way.
- A launch-path guard is worth considering: marvel could detect at daemon
  start that it is running under a detached tmux parent or a non-login
  security session and warn, since that posture silently de-authenticates
  every child. Left as a follow-on rather than a claim; the safest current
  guidance is the launch discipline above.
- The env-marker strip belongs in the adapter's environment construction
  (enforcement locus 1), so a daemon started from any context hands its
  children a clean top-level environment regardless of how the daemon itself
  was launched. Filed as a follow-on to the environment-construction path.
