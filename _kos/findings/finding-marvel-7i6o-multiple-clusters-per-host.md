# finding-marvel-7i6o: one HOME holds one marvel cluster; a second daemon under the same HOME takes over the first one's bus

**Date:** 2026-10-14
**Probe:** `_kos/probes/brief-multiple-clusters-per-host.md`
**Confidence:** frontier. Row 2 below is measured, and so is row 5's
cross-HOME half; row 5's one-HOME half and rows 1, 3 and 4 are read from
code at `3188a08`, with row 3's shared path seen in the daemon
logs but nothing written there. The pane steps of the trial (T0.3, T0.4,
T1's adopt check, T2) did not run; they wait on an operator ruling about the
sandbox profile.
**Bears on:** aae-orc-7t7d (no layout-home selector), aae-orc-cg6w1 (the
test HOME guard), `docs/design/daemon-isolation.md` Decision 1.

## The answer

The question was whether the way `~/.marvel` is organized broke running
more than one cluster per host. It did not. What holds and what does not:

- **Two daemons under two HOMEs (G-HOME) still separate.** T0.1 and T0.2
  passed at `3188a08`: both start, each answers on its own socket, a third
  start under a live HOME exits nonzero, and the first still answers. The
  brief's "each `version` reports its own home" check is carried by T4's
  mismatch case, since `version` prints no home when the homes match. This
  is the isolation unit `daemon-isolation.md` ratified. Its cost was
  measured in finding-025: a claude seat under a moved HOME is not logged in.
- **Two daemons under one HOME (G-FLAGS) do not separate.** That is the
  shape real seats need, since it keeps the harness login. The four
  per-daemon flags (`--socket`, `--state-bolt`, `--log-file`, `--pidfile`)
  and `MARVEL_TMUX_SOCKET` place exactly what they name (T1). Everything
  else daemon-owned derives from the HOME layout or its tag, so two daemons
  share it. On the bus that sharing is destructive (T3).

So the regression is narrower than the question: no change undid G-HOME.
The one-HOME recipe was never made safe for two concurrent daemons, and
each daemon-owned place added since was keyed to the layout, which under
G-FLAGS both daemons share.

## What was measured

All under one scratch root, `env -i`, no `MARVEL_*`, `TMUX` or `TMUX_PANE`,
localhost only, write-deny outside the root, every kill gated on `ps`.

- **T1, start and list.** Two daemons under one HOME, each with its own four
  flags and `MARVEL_TMUX_SOCKET`. Both start; every pid file, socket, bolt
  and log lands at its flag path; each lists empty. Both logs print the same
  harness-home tag: `cannot create /tmp/marvel-h-87c989f3, using
  <root>/tmp/marvel-harness-homes-87c989f3`. Under two HOMEs the tags
  differed (`567d3a90`, `e0ba0b3d`). No session spawned, so nothing was
  written there.
- **T3, the bus.** One HOME, one `config.yaml` with two clusters, each bus on
  its own random localhost port, no `store_dir`, nats-server v2.14.6. Both
  daemons render the same files: `state/nats/nats-server.conf` and
  `authorization.conf`, the store at `state/nats/store`,
  `run/nats-server.pid` and its `.pid.identity`, and `log/nats-server.log`.
  The first starts its broker. The second then logs `bus: pidfile names our
  nats-server pid <A's pid>, which is not listening on 127.0.0.1:<B's
  port>; stopping it`, starts its own on the same store directory (`state/nats/store`).
  The first logs `bus.crashed ... restart #1 in 30s`, restarts from the
  second one's rendered conf, and then `did not answer on
  127.0.0.1:<A's port>`. End state: the first daemon restarted once from the
  second one's conf and did not answer on its own port.
- **T4, self-report.** With a matching client HOME, `version` prints no home
  line and no warning. With a mismatch, `version` and `get sessions` both
  print `warning: the daemon at <root>/ha/.marvel/run/marvel.sock is rooted
  at <root>/ha/.marvel, but this client resolved against <root>/hb/.marvel.
  The answer above came from a different marvel. Pass --socket, or set
  MARVEL_SOCKET, to reach the one you meant.` Decision 8 works across HOMEs.
  Under one HOME both daemons report the same home, so it cannot fire; that
  half is read from code (`New` takes the home from `paths.Default()`,
  `internal/daemon/daemon.go:419-421`), not measured.

## Each row, when it came in, and the command that shows it

Each `gh` line reads the PR's merged state; each `git` line shows the HOME
keying at `3188a08`.

| # | Resource | How it came in | Commands |
|---|---|---|---|
| 1 | stream, policy, overlay temp dirs | rekeyed from the pid to the HOME tag (the pid kept two daemons apart, but broke adopt-on-restart) | `gh pr view 154 --repo ArcavenAE/marvel --json state,mergedAt`; `git show 3188a08:internal/session/manager.go \| grep -n 'prefix+"-"+layout.Tag()'` |
| 2 | bus conf, store, broker pid and log | new with the bus; HOME-keyed from the start. Measured: the second daemon displaces the first | `gh pr view 265 --repo ArcavenAE/marvel --json state,mergedAt`; `git show 3188a08:internal/daemon/daemon.go \| grep -n 'layout.StateDir(), "nats"'`; `git show 3188a08:internal/bus/supervisor.go \| grep -n 'nats-server.pid'` |
| 3 | private harness homes | moved to a short `/tmp` base keyed on the HOME tag; tag seen shared in T1 | `gh pr view 362 --repo ArcavenAE/marvel --json state,mergedAt`; `git show 3188a08:internal/session/manager.go \| grep -n '"marvel-h-"'` |
| 4 | read-only views | new; under `StateDir` | `gh pr view 641 --repo ArcavenAE/marvel --json state,mergedAt`; `git show 3188a08:internal/daemon/daemon.go \| grep -n 'layout.ViewsDir()'` |
| 5 | self-reported home | by design, per HOME | `gh pr view 126 --repo ArcavenAE/marvel --json state,mergedAt` |

Only row 1 changed behavior that existed before; rows 2 to 4 were HOME-keyed
when they were written. All four agree with Decision 1, which makes HOME the
unit. None of them is a mistake against the design; the design and the
one-HOME recipe disagree.

## Restore options

- **R1, one layout root selector (aae-orc-7t7d).** A marvel setting (a flag
  and a `MARVEL_`-prefixed variable) that roots the whole layout somewhere
  other than `$HOME/.marvel`, without moving HOME. Rows 1 to 5, the socket,
  the tag and the tmux name all follow it, because they all resolve through
  `internal/paths`. One setting per cluster, the harness keeps its login,
  and anything added later is covered by default. Decision 1 left this open
  on purpose: "Whether to add one is deliberately left open; the override in
  Decision 3 solves the immediate need without introducing a second way to
  relocate everything." Cost: that last clause is the case against R1. It
  adds a second way to relocate marvel's state beside `--socket` and
  `MARVEL_SOCKET`, so the precedence between the root setting and the
  existing flags has to be defined and tested, and every `paths.Default()`
  caller changes meaning at once.
- **R2, a flag per resource.** Extend the four flags to rows 1 to 4. Matches
  today's recipe, but every new daemon-owned place needs its own flag, which
  is how rows 2 to 4 came to be shared.
- **R3, the socket in the tag.** Derive `Layout.Tag()` from the home and the
  socket. Covers rows 1 and 3 only; rows 2 and 4 live under `StateDir`.

**Recommendation: R1.** It is the only option where the next daemon-owned
place is separated without anyone remembering to do it, and the test HOME
guard (aae-orc-cg6w1) already takes its expected root from `internal/paths`,
so it needs no change. Adding the selector answers a question a ratified
design left open, so it goes to the operator as a decision.

Separate from the choice: the T3 log shows the second daemon treating a pid
file it did not write as its own (`pidfile names our nats-server pid`). Under
R1 the two daemons would not share that file. Whether the broker's pid
identity check should also refuse a pid another daemon started is a
question for the bus supervisor, not a restore option. It is filed as
marvel#883.

## Limits

- No session was spawned, so rows 1, 3 and 4 are not measured writing.
  Row 3's shared tag is seen; a shared write is inferred.
- T0.3, T0.4, T1's adopt check and T2 need a tmux pane, and under the
  sandbox marvel's tmux absence check cannot run setuid `/bin/ps`. They wait
  for the operator's ruling on the profile.
- The trial's `ps` gate matched only binaries built into the root, so an
  orphaned nats-server (the installed binary) would have been skipped, not
  stopped. Every daemon in this run stopped cleanly, which takes its broker
  down with it, and no process naming the root remained afterwards.
