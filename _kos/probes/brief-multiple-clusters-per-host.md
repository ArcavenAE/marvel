# Probe brief: can one host still run more than one marvel cluster?

**Status:** RUN IN PART 2026-10-14; result in
`_kos/findings/finding-marvel-7i6o-multiple-clusters-per-host.md`. The pane
steps wait on an operator ruling. Revised after review the same day. Static half
read at marvel `main` @ `3188a08`; the live half (two daemons under scratch
homes) is commissioned and not yet run. The finding follows the run.
**Question asked:** "did we break the ability to run multiple marvel clusters
per host in how we currently organize ~/.marvel? we previously did a lot of
work to isolate and make running more than one cluster safe, where have we
broken that and what can we do to restore that ability?"
**Medium:** reading this repo; one read-only listing of the operator's
`~/.marvel` (names only), done once for the static half and not repeated by
the trial; a two-daemon trial under scratch homes with the
simulator runtime. No model calls, no harness logins, no service changes on
the host, nothing written under the real `~/.marvel`.
**bd:** aae-orc-7t7d (no layout-home selector, open), aae-orc-cg6w1 (the
test HOME guard, in build), aae-orc-cxdf (the rehydrate-and-spawn hazard).

## 1. The earlier isolation work, and what it guaranteed

| Work | What it guaranteed |
|---|---|
| `docs/design/daemon-isolation.md`, ratified 2026-08-07 | Decision 1: the isolation unit is the HOME-rooted layout (`paths.Layout`). Decision 2: the control socket moves to `RunDir()/marvel.sock`. Decision 3: `--socket` > `MARVEL_SOCKET` > cluster entry > layout default. Decision 4: the tmux server name derives from the layout. Decision 5: leave unrecorded tmux state alone by default. Its own "Out of scope" says concurrent daemons are not made safe by it alone. |
| #122 (socket through the layout, advisory lock), #123 (leave-alone default), #128 (tmux name from the layout), #126 (the daemon reports its layout home on every response, Decision 8) | The code side of Decisions 2 to 5 and 8, all merged 2026-08-07. #118 (name the acting daemon in the kill event) merged the same day and predates the design. |
| finding-012 (`scripts/rig-daemon-isolation.sh`) | Eight scenarios pass (0 to 6 and 4b; 0 and 5 run one daemon, the rest two, HOME-separated): a second daemon no longer destroys the first's fleet, takes its socket, or strands it. Some scenarios deliberately share one socket or one tmux server across the two HOMEs. Its own correction (2026-08-07) found the first reap scenario measured the wrong object, and scenario 4b no longer reproduces. |
| finding-013 | Two HOMEs get two tmux servers by default. |
| finding-025, 2026-08-09 | A daemon under another HOME de-authenticates claude ("Not logged in"). The recipe that works for a real-model second daemon keeps the real HOME and moves marvel state by flag: `--socket`, `--state-bolt`, `--log-file`, `--pidfile`, plus `MARVEL_TMUX_SOCKET`. #188 made the demo runbook default to that scratch layout. aae-orc-7t7d asks for one selector in place of five settings. |
| #586 (issue, closed) | An explicit `--cluster` outranks `MARVEL_SOCKET`, so a seat aiming at another cluster no longer lands on its own daemon. |

So there were two guarantees, and they differ:

- **G-HOME:** two daemons under two HOMEs do not collide. Measured with the
  simulator only. A real harness under the second HOME has no login.
- **G-FLAGS:** two daemons under one HOME, separated by the four flags and
  `MARVEL_TMUX_SOCKET`. finding-025 measured one scratch-flag daemon beside
  the default home, with a real model turn, leaving the default home's bolt
  and log untouched. It did not run two such daemons at once. G-FLAGS for
  two concurrent daemons is untested until T1 runs.

G-FLAGS is the shape a host with real seats would need.

## 2. What on `main` assumes one daemon per HOME

Every row below resolves through `paths.Default()` or the HOME tag
(`Layout.Tag()`, the first 8 hex of sha256 of the layout home), with no
per-daemon marvel flag to move it. Row 1 follows `TMPDIR`, and its overlay
dir alone has an env override; nothing else here moves without moving HOME. Under G-HOME each is per daemon. Under G-FLAGS two
daemons share it.

| # | Resource | Where | Since | Under G-FLAGS |
|---|---|---|---|---|
| 1 | stream, policy and backend-overlay temp dirs, `$TMPDIR/marvel-{streams,policies,backend-overlays}-<tag>` | `internal/session/manager.go` `daemonTempDir` | #154, 2026-08-09 (keyed on the pid before) | shared when both daemons also share `TMPDIR` (the base is `os.TempDir()`); the overlay dir alone has its own override, `MARVEL_BACKEND_OVERLAY_DIR` |
| 2 | bus manager dir, always `<StateDir>/nats`; the default store under it; the broker's `nats-server.pid` in `RunDir` and `nats-server.log` in `LogDir`, both fixed names | `internal/daemon/daemon.go` `attachBus` (:3374); `internal/bus/supervisor.go:233-234`; `internal/config/config.go` bus `Resolve` | #265, 2026-09-15 | shared whenever two daemons in one HOME each attach a bus; `store_dir` moves only the store, not the manager dir or the pid and log |
| 3 | private harness homes, `/tmp/marvel-h-<tag>/<hash of session key>` | `internal/session/manager.go` `defaultHarnessHomeDir` | #362, 2026-09-26 | shared; two clusters with the same workspace, team and role names get the same home |
| 4 | read-only views, `<StateDir>/views` | `internal/daemon/daemon.go` view keeper; `manager.go` launch context | #641, 2026-10-06 | shared |
| 5 | daemon self-reported home | `daemon.go` `New` | (Decision 8) | both daemons report the same home, so the client's home-mismatch warning cannot tell them apart |
| 6 | `config.yaml`, `known_hosts`, `keys/`, `authorized_keys`, host key | `internal/paths` | before 2026-08-07 | shared by design (client config and identity), and safe to share while only one writer edits them |
| 7 | the cluster a daemon thinks it is | `config.ClusterForSocket`, `config.go:783` | #265 | per daemon: picked by matching the daemon's own socket in the shared `config.yaml` |

Not on the list, checked: the socket and its lock (`--socket`, lock beside
it), the bolt store, the pid file and the log (all flags), the tmux server
(`MARVEL_TMUX_SOCKET`), the mrvl listener (an explicit address), and
launchd or brew services (the formula has no `service` block; no marvel
LaunchAgent on the host; the running daemon was started by hand).

**Hypothesis.** Nothing broke G-HOME. G-FLAGS, the recipe that works with
real seats, no longer separates four places added or rekeyed since it was
written (rows 1 to 4). Each was keyed to the HOME layout, which the ratified
unit says is per daemon, while the working recipe shares one HOME. The operator's `~/.marvel` organization is not the cause.

**What would refute it:** the trial shows rows 1 to 4 per daemon under
G-FLAGS (for example, a value outside the code paths read here), or shows
G-HOME failing at `3188a08`.

## 3. The trial

Run by a marvel builder seat. Simulator runtime only. All four run
conditions:

1. `env -i` with a scratch `HOME` and `TMPDIR` under one short root
   (`/tmp/mc-<random>`; a session scratchpad path is too long for
   `sun_path`), `TMUX_TMPDIR` inside it, and no `MARVEL_*`, `TMUX` or
   `TMUX_PANE`. The marvel and simulator binaries are built into the root.
2. Localhost only, with a refused-connection control (a connect to a closed
   localhost port fails first).
3. `sandbox-exec` denying writes outside the scratch root, with a positive
   control (a write outside is refused, a write inside succeeds).
4. Nothing under the real `~/.marvel` is read or written, not even names.

Cleanup stops only the daemons and tmux servers this trial started, by the
pid files and tmux socket names inside the root (for T0, the daemon pid file
is `<root>/ha/.marvel/run/daemon.pid`; tmux by `tmux -S <root>/tmux/...
kill-server`). Never `pkill -f` by a pattern: write-deny does not stop a
signal, and a pattern can match another session's process.

Each kill is gated, because after a crash a stale pid file can name a pid
the system has since reused for an unrelated process:

- before each kill, run `ps -p "$pid" -o command=` OUTSIDE the sandbox;
- kill only if the command names the binary built into `<root>`; otherwise
  record the pid and the command, and skip it;
- for a nats-server pid in T3 (the installed binary, not one built into the
  root), kill only if the command is the installed `nats-server` path AND
  carries a config or store argument under `<root>`, including the pids in
  `<root>/h*/.marvel/run/nats-server.pid`. The first run matched only
  root-built binaries, so an orphaned broker would have been skipped; a
  clean daemon stop takes its broker down, and every daemon in that run
  stopped cleanly.

Remove the root at the end only after both daemons have exited.

Two notes on the conditions, from the run:

- Localhost only is tested, not enforced: the refused-connection control
  shows the localhost path works, and no outbound connection was seen at
  daemon start. Nothing in the profile blocks the network.
- Under `sandbox-exec`, marvel's tmux absence check cannot run `/bin/ps`,
  which is setuid: the write-deny refuses it (`file-write-setugid`), so the
  steps that need a pane are blocked. A narrow allow for `/bin/ps` was
  refused by a policy control and awaits the operator's ruling; the profile
  is unchanged. Until then the trial runs only the steps that need no pane
  (T0.1, T0.2, T1's start-and-list, T3, T4). T0.3, T0.4, T1's adopt check
  and T2 wait for the ruling.

Steps:

- **T0, G-HOME control, as direct steps.** `scripts/rig-daemon-isolation.sh`
  is not run: it hardcodes `/tmp/mrig-*` paths, its scenario 0 writes under a
  fixed scratchpad path, and its exit trap uses `pkill -f`. Daemons A and B
  run under two homes inside the root, `<root>/ha` and `<root>/hb`, with
  default flags.
  - T0.1: both start; `marvel --socket <root>/ha/.marvel/run/marvel.sock get
    sessions` and the same for B each answer, and each `version` reports its
    own home.
  - T0.2: a third start under `<root>/ha` exits nonzero (record its message), and
    A still answers afterwards.
  - T0.3: apply a one-role simulator team to A only. B lists no sessions, and
    the two tmux servers differ: `ls "$TMUX_TMPDIR"/tmux-$(id -u)` shows
    two `marvel-<tag>` sockets, and `tmux -L <B's name> ls` shows no session
    of A's.
  - T0.4: stop B; A's sessions are unchanged (same keys, same pane pids).
  Expected: all pass.
- **T1, G-FLAGS:** two daemons A and B under ONE scratch HOME, each with its
  own `--socket`, `--state-bolt`, `--log-file`, `--pidfile` and
  `MARVEL_TMUX_SOCKET`. Expected to work: both start, each lists only its own
  sessions, neither adopts or reaps the other's panes.
- **T2, collisions:** apply the same one-role simulator team (same workspace,
  team and role names) to A and B. Record, for rows 1, 3 and 4: the paths
  each daemon used (its log, `ls` of the scratch `TMPDIR` and `StateDir`),
  and whether B's spawn rewrote a file A's session reads (mtime and inode
  before and after). Row 3 under the sandbox: `/tmp/marvel-h-<tag>` is
  outside the root, so record the fallback line the daemon logs and which
  base it used.
- **T3, the bus (optional):** if `nats-server` is installed, give A and B
  each a cluster entry in the scratch `config.yaml` with a bus section on a
  random localhost port and no `store_dir`. Record the store path each
  starts with and whether the second start fails or shares.
- **T4, self-report:** `marvel --socket <A> version` and the same for B.
  Record the reported home and whether the client warns.

Report: the commands, exit codes, and outputs for each step, byte for byte
where something fails.

## 4. Restore options (to be settled by the finding)

- **R1, one layout selector (aae-orc-7t7d).** A marvel root that moves the
  whole layout, and everything derived from it (rows 1 to 5, the tmux name,
  the socket, the store), without moving HOME. One setting per cluster; the
  harness keeps its login. This makes the layout, not HOME, the isolation
  unit in practice. Decision 1 left a `MARVEL_HOME` open on purpose; R1
  answers that open question.
- **R2, per-resource flags.** A flag for each of rows 1 to 4. Matches the
  G-FLAGS recipe as it stands; every future daemon-owned place needs its own
  flag, which is how rows 1 to 4 happened.
- **R3, an instance key in the tag.** Derive `Layout.Tag()` from the home
  and the socket. Fixes rows 1 and 3 only; rows 2 and 4 live under the
  home.

The architect's working default is R1, pending the trial. Changing the
isolation unit in practice is a design change to a ratified document, so it
goes to the operator as a decision.

## 5. What this brief does not cover

The harness login under a moved HOME (finding-025) stays as measured; this
probe does not log in or test credentials. Peer authentication between
daemons at the same uid stays out of scope, as in daemon-isolation.md.
