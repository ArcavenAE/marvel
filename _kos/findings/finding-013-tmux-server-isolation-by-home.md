# finding-013: the tmux namespace is scoped, and the rig's own 4b was vacuous

Date: 2026-08-07
Work: `aae-orc-by6j`, decision 4 and build step 6 of
`docs/design/daemon-isolation.md`
Follows: finding-012 (which verified survivability on a SHARED tmux
server and said explicitly that isolation was not established)
Rig: `scripts/rig-daemon-isolation.sh` scenario 6

## Result

Two daemons under different HOMEs, with `MARVEL_TMUX_SOCKET` set
nowhere, land on different tmux servers and cannot see each other's
sessions.

```
derived: /tmp/mrig-a -> marvel-0960b344
derived: /tmp/mrig-b -> marvel-926b8048
panes: marvel-0960b344=3  marvel-926b8048=3  (-1 means no such server)
sessions on marvel-0960b344: marvel-alpha
sessions on marvel-926b8048: marvel-beta
```

Both fleets alive at 4 simulators. Each daemon's session table shows its
own workspace only. B's `marvel reap` lists nothing from workspace
alpha, and B's log carries no `reconcile.left` line, which is the
discriminator between "isolated" and "sharing a server but declining to
kill". No marvel session reached the shared default tmux server. A
restarted daemon A found the same derived server and re-adopted its own
2 panes with no agent lost, which is the stability property the
sha256-of-HOME shape exists for.

The derivation is `paths.Layout.TmuxSocketName`, beside `RuntimeSocket`
and for the same reason: both namespaces are the isolation unit, and
`internal/paths` is the one place either is defined.

## Migration: no adoption sweep, and why

The one-time sweep over the shared default server was declined on a
measurement rather than a preference. On this machine, at the time of
the change: `tmux -L default list-sessions` reported no server, `pgrep
-l tmux` returned nothing, and `/private/tmp/tmux-501` held 529 socket
files, all stale. There was no population to adopt, so the sweep would
have been code for a case that did not exist, and it would have
reintroduced one pass of the cross-server reach the change removes.

Two facts were verified rather than assumed before being documented:

- `tmux list-sessions` and `tmux -L default list-sessions` both report
  the socket `/private/tmp/tmux-501/default`. `default` is tmux's own
  default server name, so `MARVEL_TMUX_SOCKET=default` is an exact
  reproduction of the old shared behavior, not an approximation.
- The derived name is 15 bytes for any home, including a 508-byte one.
  The 109-byte name that tmux refused with `File name too long`, while
  marvel emitted nothing and sessions stayed pending, is not reachable
  through this path.

## The flaky check, third methodology note in this family

Scenario 4b (`--reclaim` still destroys) failed on the first run of the
extended rig, twice, with `--reclaim did not destroy: 2 -> 2`. It fails
the same way on the unmodified binary at `358d882` with the unmodified
script, so it is not a regression from this change. finding-012
recorded it passing at `2 -> 0`, so the scenario is nondeterministic
rather than simply broken.

`--reclaim` itself is fine. Given a fleet of its own it takes 2
simulators to 0, takes the tmux server with them, and logs
`AdoptOrKill[pid=2619 socket=/tmp/mrig-b/.marvel/run/marvel.sock]:
killed unrecorded workspace tmux session marvel-alpha`. Measured
directly against the `358d882` binary, outside the rig.

What made the rig read `2 -> 2` is two properties compounding:

- 4b inherited scenario 4's aftermath rather than building its own
  fleet. Scenario 4 ends with `reap --confirm` having destroyed
  `marvel-alpha`, so 4b's `marvel work` is asking a daemon to rebuild a
  fleet an external actor destroyed underneath it. That is the behavior
  `aae-orc-4bz2` is about, and it did not happen on this run. Whether
  it happens is the difference between finding-012's numbers and these.
- `sims()` counts `mrig-sim` processes machine-wide, and `marvel stop`
  is detach rather than teardown, so scenario 2's fleet was still alive
  on a different tmux server. The `2` on both sides of `2 -> 2` was
  scenario 2's simulators, on a server `--reclaim` never touched. The
  check was comparing a number `--reclaim` could not move.

Fixed by giving 4b its own setup from a clean slate, asserting the
precondition out loud before reclaiming, and counting panes on the
target server rather than processes on the machine. Scenario 3 now
kills scenario 2's tmux server first, so its numbers stop carrying a
previous scenario's processes; with that, its counts match the 3 panes
and 2 simulators finding-012 recorded.

This is the third methodology flaw found in this ticket family, after
the incomplete artifact set of the 2026-08-06 blind read and `pgrep
-fc`. All three were caught by reading raw output rather than the tally,
and all three made a result look stronger than it was. Scenario 6's
helpers report `-1` for an absent tmux server for this reason: `tmux
... | wc -l` reports 0 both for "live and empty" and for "no server",
and a comparison against 0 then passes without measuring anything.

## Adjacent defect, pre-existing and unrelated to isolation

`marvel reap` lists one permanent candidate per live session that the
daemon itself owns. A single healthy daemon running its own two-replica
fleet reports:

```
  pane %0 in workspace probe

1 unrecorded item(s), left running. Re-run with --confirm to destroy them.
```

`%0` is the shell pane tmux creates with the session, before marvel
splits replicas into it:

```
%0 pid=19198 cmd=zsh
%1 pid=19203 cmd=mrig-sim
%2 pid=19209 cmd=mrig-sim
```

So reap can never report clean, and an operator following its own
prompt destroys part of a healthy session. Reproduced on the binary at
`358d882` as well, so it predates this change. Filed separately; not
touched here.

## What this does NOT establish

- Nothing about two daemons under one HOME. They still share one tmux
  server, which is why the leave-alone default (decision 5) ships
  alongside this rather than instead of it.
- Nothing about real harnesses. The simulator runtime throughout, so no
  model tokens were spent.
- Nothing about the victim side. Under leave-alone there is no victim,
  and the `--reclaim` and reap paths still leave the owner of destroyed
  state with no record (`aae-orc-tt5e`).
- Nothing about peer authentication. Every daemon ran at the same uid
  (`aae-orc-sqh0`).
