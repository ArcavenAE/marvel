# Daemon isolation: the socket and the tmux namespace

Status: ratified 2026-08-07, except Decision 6 which stays open and is
no longer load-bearing. Item 0 of `aae-orc-mh6g`.
Covers `aae-orc-t6da`, `aae-orc-kvcs` decisions 1 to 3, and the build
items that follow from them.

Items 1 through 7 of `aae-orc-mh6g` are implemented against this
document, not against the ticket. If writing it had shown those items do
not hold together, that would have been the result and the scope would
have changed. It did not; one item moved, and it is called out in
[Build sequence](#build-sequence).

---

## Why this document covers two namespaces

`aae-orc-t6da` was filed as a socket collision. Working it found a
second machine-global namespace, the `marvel-*` tmux session prefix,
which destroys running fleets and is untouched by any socket fix.

The two drifted apart because nobody ever decided them together. Marvel
has a HOME-rooted path layout (`internal/paths`) that is mode-enforced
and honored by every other runtime artifact, and both of these escape
it. Writing two design documents is how a third one escapes in a year.

So this document decides the isolation unit once, and then applies it to
both namespaces.

## What is measured, and what is asserted

Measured on kinu, 2026-08-06 and 2026-08-07, in an isolated rig with
HOME-separated daemons and the simulator runtime:

- A second daemon takes the socket from the first. The pid-file guard
  cannot fire, because the guard is HOME-scoped and the socket is
  machine-global.
- A client that reaches the wrong daemon gets a well-formed, successful,
  empty answer with exit code 0. A mutating call (`marvel work`) reports
  success against the wrong daemon.
- A second daemon kills the first daemon's entire running fleet through
  the tmux prefix. Verified independent of the socket, and independent
  of the starting daemon's own recorded state.
- A daemon restarting against its own records adopts correctly. The
  discrimination is by record, not by prefix.
- Both namespaces already have a working manual override, `--socket` and
  `MARVEL_TMUX_SOCKET`, and both defaults decline to use them.
- `/private/tmp` is mode 1777 with 579 entries. `/tmp/tmux-501` is 0700,
  so tmux already ships the correct pattern one directory above marvel's
  socket.
- umask is 022, so the socket is created 0755. There is no `Chmod` on
  the socket anywhere in the tree.
- `$HOME/.marvel/run/marvel.sock` is 48 bytes against a `sun_path`
  ceiling of 104 on macOS and 108 on Linux. A 136-byte path and a
  109-byte tmux socket name were both hit accidentally during the probe;
  tmux refused the latter with `File name too long` and left sessions
  pending rather than failing the run.

Everything below this line is a claim about what a change would do. No
fix to either namespace has been built.

## The current resolution chain

Socket, client side. `resolveDaemon()` (`cmd/marvel/main.go:46-73`) has
four fall-through branches to `config.DefaultSocket`, plus
`defaultConfig()` (`internal/config/config.go:92`) and `ResolveCluster`
(`:106`, `:114`). Precedence today is `--socket`, then the selected
cluster's `Socket` or `Server`, then the hardcoded default.

Socket, daemon side. `cmd/marvel/main.go:184` falls back to
`config.DefaultSocket`. The constant is declared twice, at
`internal/config/config.go:19` and `internal/daemon/daemon.go:39`. A
single-site fix leaves the second one live.

`paths.RuntimeSocket()` reads `XDG_RUNTIME_DIR` and otherwise returns
`/tmp/marvel.sock`. It has no callers outside its own test. It is dead
code, and on Darwin `XDG_RUNTIME_DIR` is unset, so its live branch would
be the `/tmp` one anyway.

`MARVEL_SOCKET` already exists as a name. It is produced at
`internal/runtime/adapter.go:138` and `internal/session/manager.go:681`,
injected into the environment of every agent marvel spawns, and consumed
at `cmd/marvel/ctxforward.go:108` and nowhere else. `resolveDaemon()`
ignores it. Marvel tells every agent where the socket is and then throws
that answer away in its own CLI.

tmux. `NewDriver` reads `MARVEL_TMUX_SOCKET` (`internal/tmux/driver.go:59`)
and `Driver.cmd` applies it as `-L <name>` (`:85-94`). It is a socket
NAME, not a path: tmux places the socket under its own per-uid directory.
Unset means the user's shared default server. Session names are
`marvel-<workspace>`, from `marvelSessionPrefix` in
`internal/session/manager.go`.

---

## Decision 1: the isolation unit is the HOME-rooted layout

`paths.Layout` rooted at `os.UserHomeDir()/.marvel`. Every runtime
artifact marvel owns already keys off it: config, keys, known_hosts,
logs, the bolt store, the pid file.

Why this rather than an explicit named instance or a declared cluster:

- It already exists and is already mode-enforced at 0700.
- One flag isolates everything, which is exactly what the `d0pt` repro
  daemons were trying to achieve when they collided anyway.
- It makes a guard that already ships load-bearing for the first time.
  `--pidfile` is not opt-in; it defaults to `$HOME/.marvel/run/daemon.pid`,
  so the refuse-if-live check at `internal/daemon/daemon.go:246-251` runs
  on every normal start. Today it cannot protect the socket by
  construction, because the guard is HOME-scoped and the socket is not.
  Put the socket in the layout and the same guard starts covering it.

The cost, recorded rather than dismissed: it puts the socket on the home
filesystem, and unix sockets do not work on NFS homes at all. That is
what the environment override in Decision 3 is for, and it is why the
override ships in the same change rather than later.

There is no `MARVEL_HOME`. `paths.WithHome` is test-only. Whether to add
one is deliberately left open; the override in Decision 3 solves the
immediate need without introducing a second way to relocate everything.

## Decision 2: the socket resolves to `RunDir()/marvel.sock`

`~/.marvel/run/marvel.sock`. That directory already exists at mode 0700,
is already mode-checked by the paths package, and already holds the pid
file.

This closes four things at no extra cost, none of which are urgent on a
single-operator machine and all of which are free here: a world-
connectable socket (umask 022 yields 0755 and nothing chmods it), a
fixed name squatted in a 1777 directory, `/tmp` reaping, and systemd
`PrivateTmp` namespacing.

Both hardcode sites go. `paths.RuntimeSocket()` becomes a `Layout`
method returning the layout-derived path, or it is deleted; what it must
not do is survive as a free function whose fallback contradicts this
decision.

Resolved paths are asserted under 104 bytes at resolution time, with an
error naming the number. The home-derived path measures 48, so this is a
guardrail against deep automounts and long CI checkouts rather than a
current problem. It is a test, not a separate work item. It would have
caught two accidents during the probe.

## Decision 3: the precedence chain, with the environment override shipped in the same change

```
--socket flag
  > MARVEL_SOCKET environment variable
  > selected cluster's Socket or Server from config.yaml
  > Layout-derived default (~/.marvel/run/marvel.sock)
```

`MARVEL_SOCKET` is not a new name. It is the name marvel already injects
into every agent it spawns, and finishing the seam closes a live
inconsistency of the same leak class as the bug this work fixes.

Two known future consumers justify cutting the seam now rather than
retrofitting it into a config schema, a client resolution chain, and a
launchd plist that has already shipped: NFS home directories, where unix
sockets do not work, and socket activation, which wants `/run` and hands
the daemon a descriptor rather than a path.

## Decision 4: the tmux socket name derives from the same unit

This is `aae-orc-kvcs` decision 1, and the answer is yes. Two
machine-global namespaces, one fix pattern.

The default becomes a layout-derived tmux socket NAME rather than the
shared server. `MARVEL_TMUX_SOCKET` keeps its current meaning and
precedence as the explicit override.

Constraints the derivation has to respect:

- It is a name, not a path. tmux places the socket itself.
- It must be short. A 109-byte name was refused with `File name too
  long`, and the failure was silent from marvel's side.
- It must be stable across daemon restarts under the same HOME, or
  adopt-on-restart breaks.

`marvel-<first 8 hex of sha256(Layout.Home)>` satisfies all three. The
exact scheme is an implementation choice; the constraints are not.

Two consequences to state plainly:

- After this lands, a daemon no longer sees marvel sessions on the
  shared default server. Pre-existing ones become invisible rather than
  adopted or killed. That is safer, and it also means they linger
  unmanaged until someone reaps them. Migration should say so.
- This substantially shrinks, but does not eliminate, the blast radius
  of Decision 5's kill posture. Two daemons sharing one HOME still share
  one tmux server.

## Decision 5: the default posture toward unrecognised state (RESOLVED, Reading B)

This is `aae-orc-kvcs` decision 2. **Ratified 2026-08-07: err on silent
accumulation, not silent destruction. Reading B.** The default becomes
leave alone; killing requires an explicit act; the reaper ships with it,
because Reading B is incomplete without one.

Both readings are kept below as the reasoning, unedited. The tradeoff
was between two real bugs pointing in opposite directions, and the
ruling picked which one we would rather have, not which one was
imaginary.

The mechanism, for both readings: `AdoptOrKill`
(`internal/session/manager.go:120`) lists tmux sessions under the
prefix. A workspace not in the store means the whole tmux session dies.
A pane not matching a recorded `PaneID` dies. Discrimination is by
record, which is why a daemon restarting against its own store adopts
correctly and a foreign daemon does not, whatever its own state.

### Reading A: kill stays the default

Kill-all exists because of a measured bug. Orphan panes accumulated and
consumed resources, and a fresh daemon reclaiming its prefix is the fix
that shipped (`ArcavenAE/marvel#13`, `aae-orc-72u`). Reverting the
default reopens a closed bug.

Decision 4 changes the arithmetic in this reading's favour. Once each
HOME has its own tmux server, "unrecognised" stops meaning "another
daemon's fleet" and starts meaning "leftovers from my own previous
incarnation", which is exactly the population kill-all was written for.
The destructive case then requires two daemons deliberately sharing one
HOME, which is not an ordinary action.

What it costs: the failure mode stays catastrophic when it does occur,
and it stays reachable by anyone who sets `MARVEL_TMUX_SOCKET` to a
shared value or runs two daemons under one HOME. The blast radius
shrinks; the severity does not.

### Reading B: leave alone becomes the default, kill requires a flag

The trigger for total fleet loss is an ordinary action with no
confirmation, and the loss is running work rather than a configuration
inconvenience. Adopt-by-record already covers the legitimate restart
case, verified directly: a same-HOME restart against a populated store
re-adopted its own sessions as running and healthy.

What it costs: orphan panes accumulate again, which is the bug kill-all
was built to fix. This reading is only complete with a reaper, either an
explicit verb or a `--reclaim` flag on the daemon, and operators have to
remember to use it. It trades a loud destructive failure for a quiet
accumulating one.

### The ruling

Which bug do we prefer to have. Silent destruction of running work, or
silent accumulation of orphaned panes. Decision 4 makes the first rarer
without making it less severe, and does nothing to the second.

Ratified 2026-08-07: **accumulation.** An orphaned pane costs memory and
clutter and is recoverable at any later time by an operator who notices.
Destroyed work is not recoverable at all, and Decision 4 lowers its
frequency rather than its cost. A failure we can clean up beats a
failure we can only regret.

What this obliges, and all three are required for the change to be
complete rather than merely safer:

1. `AdoptOrKill` becomes adopt-or-leave. Unrecognised `marvel-*` state
   is reported and left running.
2. Killing moves behind an explicit act, not a default. A `--reclaim`
   flag on the daemon covers the "I know this host is mine, clean it"
   case at startup.
3. A reaper verb, so accumulation stays a nuisance rather than becoming
   the next silent failure. It must show what it would kill before
   killing it, since the whole point of this ruling is that destruction
   is the part we make deliberate.

The reporting in item 1 is not optional. Leaving state alone silently
would trade one silent failure for another, which is not what was
ratified. The `reconcile.killed` event added in #118 gains a sibling for
the left-alone case, and both name the acting daemon.

Consequence for Decision 6, below: with leave-alone as the default, a
daemon no longer kills what it did not create as a matter of course, so
the question Decision 6 asks stops being load-bearing. It is retained as
open because `--reclaim` and the reaper still have to decide what they
are allowed to touch.

## Decision 6: may a daemon kill what it did not create (UNRESOLVED, and narrower than it looks)

This is `aae-orc-kvcs` decision 3. The original framing asked whether a
daemon starting with `resource_version=0` should be forbidden from
killing anything.

That framing does not survive its own follow-up test. A foreign daemon
carrying a populated store and its own workspace still killed the
first daemon's sessions. The rule is not "empty state kills everything",
it is "any `marvel-*` session not in my records gets killed", and a
version-0 restriction addresses a case that was incidental.

Restated, the live question is whether marvel should be able to
distinguish "not in my records" from "in someone else's records" at all.
That requires a pane or session to carry the identity of the daemon that
created it, which is available (tmux supports user options on sessions)
but is a larger change than either reading of Decision 5, and it is
adjacent to the identity work in `aae-orc-odia`. Recorded as open, and
sequenced after Decision 5, since Decision 5 may make it unnecessary.

## Decision 7: migration and the loud-failure requirement

A `config.yaml` pinning `/tmp/marvel.sock` must not quietly keep the old
behavior. A stale client that silently works reproduces the exact bug
this closes.

There are two branches, both measured, with opposite results:

- Nothing at the stale path: already loud. `Error: connect to daemon at
  /tmp/marvel.sock (unix): ... no such file or directory`, exit 1.
  Nearly free.
- Another daemon at the stale path: silent. A well-formed, successful,
  EMPTY session table with exit code 0, and `marvel work` printing
  `workspace/alpha ready` against the wrong daemon.

So the requirement is not "error when the path is gone". It is "the
client must be able to tell whether the daemon it reached is the one it
meant", and no path change alone closes that. It is closed by the
self-report field, which is why that item exists and why it is sequenced
where it is.

Migration behavior: on finding a cluster entry pinning the old default,
warn on every invocation naming both the stale path and the new one.
Do not rewrite the user's config silently.

## Decision 8: the self-report field is diagnostic, and it is ordered

One additive field on `daemon.Response`, which is `{Result, Error}`,
shared by all 14 methods, with no version field and no handshake.
`omitempty` makes it wire-compatible in both directions. No protocol
version bump, no negotiation, no new message type.

It must follow Decision 2. Measured, the client's only notion of which
daemon it means is a path, and in every collision mode both daemons sit
at the same path, so there is nothing for the field to be compared
against. Once the socket is layout-derived, the client's own layout
becomes the expectation and the same field acquires meaning. Building it
first ships a field nobody can check.

What it does not do: a field on the RESPONSE is read after the request
has already been sent. For read methods that is fine. For mutating
methods it is not, and this was measured, not inferred. Prevention would
require the expectation to travel on the REQUEST with the daemon
rejecting a mismatch, which is authorization-shaped and belongs with
`aae-orc-sqh0`.

---

## Build sequence

Status, 2026-08-07: steps 1 to 4 shipped in `ArcavenAE/marvel#122`, step
7 in `#123`. Steps 5 and 6 are open. Step 6 was sequenced after 7 in the
end, because 7 is what stops the destruction and 6 only lowers its
frequency.

1. Decision 2. Socket resolves through `Layout`, both hardcode sites
   removed, `RuntimeSocket()` reconciled or deleted, path-length
   assertion as a test.
2. Decision 3. Precedence chain including `MARVEL_SOCKET`, wired into
   `resolveDaemon()` and the daemon-side fallback.
3. Liveness probe plus a lock before unlink. `internal/daemon/daemon.go:257`
   removes the socket unconditionally and discards the error, which is
   how a live daemon gets left unreachable when a second daemon exits.
   `flock` on a lock file is the standard shape and the kernel releases
   it when the process dies. A bare connect-probe has a TOCTOU window
   between probe and bind.
4. Decision 7. Migration warning.
5. Decision 8. Self-report field. Strictly after step 1.
6. Decision 4. tmux socket name derivation.
7. Decision 5. Adopt-or-leave as the default, `--reclaim` for the
   deliberate case, and the reaper verb with a dry run.

Steps 1 through 5 are the socket namespace and were independent of the
two decisions left open in the first draft. Step 6 is independent of
Decision 5 and can land before it; it is sequenced late only because the
socket work was already scoped.

One item moved while writing this. `aae-orc-mh6g` item 3 filed the
environment override as new work. It is not: `MARVEL_SOCKET` already
exists, is already produced in two places, and is already consumed in
one. The item is finish-an-existing-seam, with the name already chosen.

## Out of scope

**Peer authentication.** Once the socket is 0700 and uid-scoped it is
private to the user and still reachable by every agent marvel spawns,
because those run at the same uid. Nothing in this document is an
authorization boundary and none of it may be described as one. There is
no `SO_PEERCRED`, `Ucred` or `getpeereid` call anywhere in the tree, and
adding one would not change this, because the credentials would match.
Tracked at `aae-orc-sqh0`. Related: `aae-orc-mr5c` (unauthenticated
heartbeat RPC), `aae-orc-wbqi` (agent-writable policy file),
`aae-orc-odia` (identity lane).

**Concurrent daemons are not made safe by this document.** Completing
every item here closes one machine-global namespace and scopes a second.
It does not address what happens once something has been destroyed: the
victim daemon records nothing, crash-loop backoff fires against an
external kill as though it were an application crash and suppresses
replacement, and sessions report `state=crashed` with `health=healthy`
at the same time. That chain is `aae-orc-4bz2`. Reconcile-on-start is
also destructive in the forward direction, spawning live agents from
stale desired state, which no namespace scoping touches.

**Socket activation.** Wants `/run` and a passed descriptor rather than
a path. Decision 3's override is the seam it would use. Belongs to
`aae-orc-k28s`.

## References

- Probe: `aae-orc::_kos/probes/brief-marvel-runtime-socket-placement.md`
- Node: `_kos/nodes/frontier/question-daemon-isolation-boundary.yaml`
- Work: `aae-orc-t6da`, `aae-orc-mh6g`, `aae-orc-kvcs`, `aae-orc-4bz2`,
  `aae-orc-sqh0`
- Shipped alongside: `ArcavenAE/marvel#118`, which made the kill
  attributable (`reconcile.killed` with the acting daemon named). It is
  not part of this design and does not constrain it.
