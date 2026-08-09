# finding-025: overriding HOME isolates marvel and de-authenticates the harness

Date: 2026-08-09
Scope: marvel operations, demo verification
Status: measured

## Symptom

Running a marvel daemon under an overridden `HOME` is the standing advice
for keeping a test daemon away from the operator's populated
`~/.marvel/state/marvel.bolt` (the rehydrate-and-spawn hazard,
`aae-orc-cxdf`). It works for marvel. It also silently breaks the harness.

An interactive claude session spawned by such a daemon reports:

```
❯ say only the word ready
  ⎿  Not logged in · Please run /login
```

The session spawns, the pane is healthy, `marvel get sessions` shows
`running`, and the agent can do nothing. Nothing in marvel's output says
why, because from marvel's side nothing is wrong.

## Mechanism

Marvel derives its whole layout from `Layout.Home`, which comes from
`os.UserHomeDir()`, which reads `$HOME`. Overriding `HOME` moves
`~/.marvel` and, since #128, the tmux server name with it.

The harness reads `$HOME` too. On macOS Claude Code keeps no
`.credentials.json` (verified absent on this machine); credentials resolve
through the login Keychain, and the lookup does not survive the override.

Symlinking `$MHOME/.claude -> ~/.claude` and `$MHOME/.claude.json` is NOT
sufficient. Tried, still "Not logged in".

Discriminator, run to rule out the tmux/session-context explanation: the
same `claude` binary in a plain tmux pane on a scratch socket, under the
real `HOME`, comes up authenticated at a normal prompt. `HOME` is the
variable.

## Consequence

Every demo beat that needs a real model turn is unverifiable under HOME
isolation. That is the whole CTX% surface, the Act 3 extension, Act 2's
`agent.*` stream, and any future metering act. The two constraints, "do
not touch the default home" and "verify against a live agent", are
unsatisfiable together by that route.

## The shape that works

Keep `HOME` real so the harness authenticates, and move every piece of
marvel state off it individually. All four are existing flags, plus one
existing env var for the namespace that is not a flag:

```sh
D=/tmp/mvl-scratch; mkdir -p "$D"
MARVEL_TMUX_SOCKET=mvl-scratch marvel daemon \
  --socket    "$D/m.sock" \
  --state-bolt "$D/marvel.bolt" \
  --log-file   "$D/daemon.log" \
  --pidfile    "$D/daemon.pid" &
export MARVEL_SOCKET="$D/m.sock" MARVEL_TMUX_SOCKET=mvl-scratch
```

`--state-bolt` at a fresh path is the one that neutralizes `cxdf`: an
empty store has no desired state to rehydrate, so the daemon logs no
`AdoptOrLeave` line at all and spawns nothing.

`MARVEL_TMUX_SOCKET` is the load-bearing one for safety. The tmux server
name is otherwise derived from `Layout.Home`, so a real `HOME` would put
this daemon on the same tmux server as the operator's, where
`AdoptOrLeave` meets their panes. With the override the two daemons cannot
see each other's tmux state at all.

Verified 2026-08-09 across a full Act 3 extension run: claude
authenticated, two real turns taken, CTX% populated, and
`~/.marvel/state/marvel.bolt` plus `~/.marvel/log/daemon.log` both
untouched throughout (mtime unchanged at the operator's own earlier
teardown).

## Aftermath

- The Act 3 extension in `docs/demo.md` was verified end to end on this
  recipe and is now written.
- `docs/demo.md`'s prerequisites told a reader to `rm -f
  ~/.marvel/state/marvel.bolt` and run `marvel daemon` against the default
  home, which is correct only if the `rm` precedes the daemon. PR #184
  added a justfile warning for the reverse order. RULED 2026-08-09: the
  runbook defaults to the scratch layout, with the default home kept as
  the labelled operate-your-real-fleet case. Shipped in PR #188.
- The four flags are documented individually in `marvel daemon --help`.
  Nothing documents them as a set, which is why the obvious lever (`HOME`)
  is the one that gets reached for. `aae-orc-7t7d` carries the ask for one
  selector that sets them coherently, since five flags is four-fifths of
  an isolation an operator can get wrong.
- The health-surface half of this is `aae-orc-9box`: marvel reported the
  session `running` and `healthy` for the entire time its agent sat at a
  login prompt. Expired credentials, revoked tokens and an unreachable
  model all present the same way.
