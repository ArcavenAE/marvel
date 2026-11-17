# Runbook: moving a cluster's local bus from a launchd-run broker to `mode: managed`

Status: runbook for review. Docs only. **Nothing here runs on a live host
without the operator present.** The rehearsal (section 3) is an
operator-attended session of its own and never touches the live bus, hub or
daemon; the window (section 6) is scheduled after it and run only when the
operator says go. Written to assume the window goes badly.

This runbook is host-neutral. It describes one cluster that hosts the global
hub and runs its own local broker, where both servers are launchd jobs. The
host's name, its launchd labels and its paths are filled in from the
operator's private window document, which carries every host-specific value
and the placeholder-to-name mapping. The values used below as variables:

| Variable | Meaning |
|---|---|
| `$CLUSTER` | the cluster's name; the managed broker's `server_name` |
| `$LOCAL_LABEL`, `$HUB_LABEL`, `$DAEMON_LABEL` | the launchd labels of the old local broker, the hub and the marvel daemon |
| `$GUI` | the launchd domain both jobs live in, `gui/$(id -u)` |
| `$W` | the window directory; every capture goes into it and none holds a seed, password or token |
| `$R` | the kept rollback directory for the old local plist |

- Design: `bus-as-service.md` sections 6 and 8.2, and the director design for
  the hub's deployment. Run after the store-migration runbook in this
  directory, in its own window (that runbook, section 7).
- The old local broker's server name was `director-phase0`. The managed broker
  renders `server_name: $CLUSTER`. Nothing in code reads the old name; the
  window captures its readers so the operator sees that on the day (step 0).

## 1. What runs today

| Piece | Live state | Touched by this window |
|---|---|---|
| Old local broker | a launchd job running director's `probe/nats-phase-0/start.sh`, which execs `nats-server -c <its directory>/nats-server.conf --pid $DIRECTOR_PHASE0_HOME/nats-server.pid`; 127.0.0.1:4222, monitor 8222, anonymous; its store is `~/.director/nats/store` | stopped and replaced |
| Global hub | a launchd job running the hub launcher; leafnode listener 0.0.0.0:7442, monitor 127.0.0.1:8242, domain `global` | not touched, except the optional KeepAlive step 8 |
| marvel daemon | a launchd job; `~/.marvel/config.yaml` has no bus entry | restarted once (step 5) |
| Sessions | every seat on the cluster | every one respawned |
| The operator's director session | a registration running the bare `director-mcp` binary with no `DIRECTOR_NATS_*` key | moved to the seat wrapper (step 9) |

The old local broker is a launchd job, so stopping it by `kill` does not hold:
its `RunAtLoad` plist starts it again at the next login, and a `KeepAlive` job
restarts at once. The window moves its plist aside first and then boots it out
(step 3).

## 2. Preconditions (all before the window is set)

1. **Daemon build.** It includes the managed bus, the structural health
   reading, `bus.binary` and the binary rows of `marvel bus status` (the
   configured path, its realpath, whether it exists, and the serving
   version).
2. **The director seat credential.** What each client reads:
   - the bare `director-mcp` binary reads `DIRECTOR_NATS_CREDS`, or else
     `DIRECTOR_NATS_USER` plus `DIRECTOR_NATS_PASS`, the password **value**;
   - the `director-mcp-seat` wrapper (director `probe/nats-phase-0/`) uses
     `DIRECTOR_NATS_USER` and `DIRECTOR_NATS_PASS` when both are set, and
     otherwise reads `$MARVEL_BUS_STATE/director.pass` (default
     `~/.marvel/state/nats/director.pass`, rendered by the managed daemon) and
     exports `DIRECTOR_NATS_USER=director`. With no such file it exits 1. It
     also needs `DIRECTOR_TEAM` and `DIRECTOR_WORKSPACE` set outside a marvel
     session.

   The operator's registration runs the bare binary, so after the flip it
   offers no credential and is refused. Prepare a `director-mcp-seat`
   registration (command, `DIRECTOR_TEAM`, `DIRECTOR_WORKSPACE`, no
   `DIRECTOR_NATS_*` key) and switch to it at step 9, not before: until the
   daemon renders `director.pass`, the wrapper exits 1. Putting a password
   value into the harness config for the bare binary is the fallback, not the
   plan, because it leaves the secret at rest.
3. **The leaf seed, as a file.** The operator has the cluster's leaf NKey
   seed as a file for `credential put bus/leaf --value-file`, named on the
   command line, and has checked without printing it that its public key is
   the cluster's leaf user in the hub's conf. Nobody prints, copies or pastes
   the seed. Extracting a seed literal from the old broker's include file is
   not part of this window and waits on its own ruling. A mismatch leaves the
   cluster with a working local bus and no hub link (`bus.leaf.unenrolled`):
   survivable, and recovered in the window.
4. **A pinned binary.** `bus.binary` names an absolute nats-server path, in
   the same form the cluster's peers use, and `marvel bus status` reports its
   path, realpath and serving version.
5. **The hub's service file** is the host's own installed file. The public
   reference copy in director is checked against it by the launch-agents
   test's agreement check; the reference is not what is installed.
6. **Rehearsal passed** (section 3), its transcript recorded on the window's
   ticket, with the broker gap and the per-seat respawn time measured.
7. **The installer's host row** no longer lists the old local broker, so a
   later installer run does not load it again over the managed broker.
8. **Quiet fleet.** No merge, shift or `marvel work` in flight. The other
   clusters' supervisors are told this cluster's seats will be unreachable for
   the window, and that global mail to them queues at the hub.
9. **Start directories.** Each team declares its root, or the operator
   accepted the legacy stamp of the daemon's cwd. Capture each team's
   `WorkDir` (`marvel describe team`) into `$W` at step 0.
10. **Handoffs.** Every seat on the cluster is told before the window to write
    its handoff, so a respawn does not lose its context.

## 3. Rehearsal (operator-attended, before the window)

The rehearsal starts nats-servers, a daemon and a shim beside the live ones, on
the same host, with the operator present. Every process is scratch: scratch
HOME, scratch ports, scratch labels, scratch NKeys and a scratch tmux server.
The real seed, the real stores, the live hub and the live daemon are never
read, signalled or touched, and that is checked, not assumed. The checks run
from `scripts/rehearse-bus-cutover.sh`, which takes the scratch ports, labels
and cluster name as arguments; this section states the rules it enforces.

```sh
for v in ${(k)parameters[(I)MARVEL_*]}; do unset "$v"; done   # zsh; bash: unset $(compgen -v MARVEL_)
env | grep '^MARVEL_'                       # must print nothing
S=$(mktemp -d)                              # scratch HOME: config, StateDir, stores
m() { HOME="$S" marvel --socket "$S/m.sock" "$@"; }   # the ONLY way the rehearsal calls marvel
```

The daemon reads `~/.marvel/config.yaml` through `os.UserHomeDir`, so
`HOME="$S"` gives the scratch daemon its own config; the client must still name
`--socket`. **Every marvel command in the rehearsal goes through `m`.** A bare
`marvel` with `MARVEL_*` unset resolves the live daemon: a bare `work` would
put a shell team on the live fleet, and a bare `credential put bus/leaf` would
store the scratch seed in the live store.

Run the rehearsal from a fresh operator terminal, never a seat pane: a seat's
shell carries the live `NATS_URL` and `DIRECTOR_*` values.

**Isolation checks, before each scratch process starts:**

- `env | grep -E '^(MARVEL_|DIRECTOR_|NATS_)'` prints nothing in the shell
  that starts it, except the variables a step sets on purpose on that one
  command line.
- Its port is free: `lsof -nP -iTCP:<port> -sTCP:LISTEN` prints nothing for
  every scratch port and its monitor. This is the guard against a scratch
  broker binding a live port.
- A scratch nats-server's conf names its store as a **literal** path under
  `$S`, never a variable. The checked-in phase-0 conf has `store_dir:
  $DIRECTOR_PHASE0_STORE`; left in, it resolves from the environment, and if
  that points at the live store, two servers share it. Before start, a grep for
  `\$[A-Z_]`, `include`, and the live ports anchored on non-digits (every
  scratch port contains a live one, so an unanchored pattern never passes) must
  print **nothing**. Run the grep's control once first (`echo 'port: 4222' |
  grep -E '<pattern>'` prints the line), so a typo cannot pass as a clean
  conf. After start, `curl -s 127.0.0.1:<scratch monitor>/varz | jq -r
  .jetstream.config.store_dir` is under `$S`; `ps -o command=` shows only `-c`,
  so it is not the check.
- The scratch daemon's socket is `$S/m.sock`, and `MARVEL_TMUX_SOCKET` is a
  scratch name. `tmux -L <name> ls` lists only the scratch server. Its first
  call is a read-only `bus status` with no "is rooted at" warning.
- Every client command names its server, `nats -s nats://127.0.0.1:<scratch
  port>`, never a default context.
- **The live-unchanged check**, at the start and after each step, read-only:
  the live hub's `/leafz` and the live local monitor's `/jsz`, saved and
  compared with the first capture, and the same pids and start times on the
  live listeners. No nats client is ever pointed at a live port during the
  rehearsal. Any change stops the rehearsal.
- **Scratch labels.** The rehearsal's launchd jobs carry scratch labels and
  scratch plists, never the live labels, and a live label is never booted out.

Steps, each with a pass condition:

1. **Build today's shape without the live include.** Never start the scratch
   broker through the checked-in `start.sh`: it always runs the checked-in conf,
   which includes the real leaf file pointed at the live hub, so a scratch
   broker started through it would join the live hub. Write a scratch conf with
   scratch ports, a literal scratch `store_dir`, and a leafnode remote only to
   a scratch hub, and run it under a scratch-labelled launchd job. Start a
   scratch hub (domain `global`) and a second scratch leaf standing in for
   another cluster. Pass: the scratch hub's `/leafz` lists both scratch
   leaves, and the live hub's is unchanged.
2. **Move aside, boot out, prove the port free.** Move the scratch local
   plist into a scratch rollback directory, boot out its scratch label, and
   prove the scratch port free. Pass: the job is gone, the plist is in the
   rollback directory byte-identical to the one moved (`cmp`), and `lsof`
   prints nothing for the port (`CheckPortFreeAfterBootout`,
   `CheckOldPlistMovedToRollbackDir`).
3. **Flip.** Write the scratch `config.yaml` with the managed bus entry on
   scratch ports, `bus.binary` set, `store_dir` the scratch store, and
   `hub.url` at the scratch hub. Copy the store cold. Start the scratch daemon
   (`HOME="$S"`, `--socket "$S/m.sock"`, `--state-bolt`, `--log-file`,
   `--pidfile "$S/marvel.pid"`, scratch tmux socket). Before the first write,
   `m get teams` lists none, which proves `m` reached the scratch daemon. Then
   `m credential put bus/leaf --value-file <scratch seed file>`. Pass: `m bus
   status` reads ready, provisioned, authorized, leaf up; stream counts match
   step 1; an anonymous `sub '>'` is refused. **Measure** the gap from the
   old broker's stop to ready.
4. **Read the monitors.** Pass: the scratch hub's `/leafz` names the scratch
   cluster and the other scratch leaf and does not name the old server name
   (`CheckHubLeafzNamesThisCluster`, `CheckHubLeafzLacksOldName`); the scratch
   broker's `/varz` reports `server_name` equal to the cluster name and a
   serving version equal to the configured binary's
   (`CheckServingVersionMatchesConfiguredBinary`); the config differs from the
   peers' only in values, not in key names or binary form
   (`CheckConfigMatchesFleetShape`).
5. **Spawn with a credential.** `m work $S/team.yaml`, a one-role scratch team
   whose runtime is a shell. Pass: the session's env carries `NATS_URL` at the
   scratch broker and a team user, and that user can subscribe its inbox.
   **Measure** one respawn.
6. **The seat, through the wrapper it will use.** With `DIRECTOR_NATS_*`
   unset, run `director-mcp-seat` with `MARVEL_BUS_STATE=$S/.marvel/state/nats`
   (named, not derived from `HOME`), `NATS_URL` at the scratch broker, and
   `DIRECTOR_TEAM`/`DIRECTOR_WORKSPACE` set. Pass: it connects as `director`
   and `inbox_summary` answers. Negative control: the same with an empty
   `MARVEL_BUS_STATE` exits 1 and prints that there are no bus credentials, so
   a pass cannot come from a credential the test did not intend.
7. **The other leaf held.** Pass: the other scratch leaf never left the
   scratch hub's `/leafz` during steps 3 to 6.
8. **Credentials against an anonymous broker.** Point the bare shim, with
   `DIRECTOR_NATS_USER=director` and `DIRECTOR_NATS_PASS` set, and the step-5
   session at an anonymous scratch broker. Record whether each connects. This
   decides rollback step 8.
9. **Roll back** as section 7 does, with every path, port, label and socket
   replaced by its scratch value, and rehearse the dead-daemon case: read the
   daemon pid from `$S/marvel.pid`, require its command line to contain
   `$S/m.sock`, then `kill -KILL` it; confirm its broker still answers, then
   stop the broker from its pidfile by the pid check. Pass: the scratch old
   broker, started again from its moved plist, serves the step-1 streams
   (`CheckRollbackRestoresLaunchdBroker`); the old leaf conf kept its mode and
   mtime (`CheckLeafConfKeptForRollback`, mode and mtime only, never its
   contents). Record whether the store the managed broker opened (it adds a
   JetStream domain) loads under the old conf. That decides rollback step 5.
10. **The hub's service file.** Pass: with the installed hub plist's `PATH`,
    `jq` and the `nats` CLI both resolve, and the plist's `HOME`, or
    `DIRECTOR_GLOBAL_HOME`, names the hub home the launcher reads; otherwise
    each start logs `jq not on PATH: provision skipped`.
11. **Tear down.** Stop every scratch process and job, kill the scratch tmux
    server, remove `$S` and the scratch plists.

## 4. Who loses the bus, and for how long

- **Every seat on the cluster and the director seat**, from step 3 of the
  window (the old broker's stop) until that seat's respawn (step 9). In that
  span a seat's sends fail loudly and it receives nothing. After step 5 an
  unrolled seat's shim reaches an authorized broker with no credential and is
  refused, which is also loud. Estimate, to be replaced by the rehearsal's
  numbers: a few minutes for steps 3 to 7, then the roll, which waits on
  handoffs. Budget 60 to 90 minutes.
- **The cluster's global reach**, over the same span: its seats cannot
  publish to or read the global tier. Global mail to them waits at the hub, in
  streams with a 72 hour max age, and is read after respawn.
- **The other clusters' seats keep their own buses and their hub links**
  (section 5). They lose only their reach to this cluster's seats.

## 5. Why the other clusters' leaf links survive

The other clusters' links end at the hub's leafnode listener. The old local
broker holds only an outbound leaf to that same hub. The window replaces the
local broker and never stops, reloads or edits the hub or its state
directory (`~/.director/nats-global`), so their links have nothing to drop,
except in step 8, which is its own approval and a short hub outage. Checks:
the hub's `/leafz` lists the other leaves before step 3 and after step 7. The
managed broker's own leaf authenticates as the cluster's leaf user; if the hub
refuses it, only this cluster's link is down.

Out of scope, stated so nobody adds it in the window: moving the hub under
marvel, and any hub conf edit.

## 6. The window

Each step: what it does and its pass condition. Stop at the first failed pass
condition; do not continue "to see what happens". Steps 3 and 4 are the only
ones that cut the bus, and the bus is up until step 3 starts.

**Step 0. Capture (bus up).** Into `$W`: `launchctl print "$GUI/$LOCAL_LABEL"`
and the same for the hub; a `cp -p` of each installed plist
(`*.plist.as-found`), of `~/.marvel/config.yaml` (`config.yaml.pre-bus`) and
of the hub's installed launcher; the hub's `/leafz`; the local broker's
`/varz` and `/jsz?streams=true`; `marvel get sessions`; `marvel bus status`;
each team's `WorkDir`; the mode and mtime of the old leaf conf and seed file
(`stat`, never their contents); and the old server name's readers, read-only:
`grep -rn director-phase0` over `~/.director`, `~/.marvel/config.yaml`, and the
director and marvel checkouts' `docs`, `sim` and `probe` directories. Pass:
every file exists and is not empty, except `bus status`, which reads "no bus
configured".

**Step 1. Handoffs (bus up).** Director broadcasts to the cluster's seats:
write your handoff now and start no new work. Wait for the markers or the
deadline. Pass: every marker is present, or the operator accepts the named
exceptions in `$W/window-note`.

**Step 2. Add the managed bus entry (bus up).** Add the bus entry to the
cluster in `~/.marvel/config.yaml`: class `message-bus`, provider
`nats-server`, mode `managed`, listen and url `127.0.0.1:4222`, `store_dir` an
absolute path (it is not `~`-expanded), the seat pair the director
registration uses, the hub url `nats-leaf://127.0.0.1:7442`, and `bus.binary`
in the same form the peers use. One spelling only: a `bus:` block or a Services
entry, never both. The daemon reads its config only at start, so nothing
changes yet. Pass: `diff` of `config.yaml.pre-bus` against the live file shows
only the bus entry, with the same key names and binary form as the peers
(never compare values that are secrets), and `bus.binary` resolves (`ls -l`,
then `<path> --version`).

**Step 3. Move the old local plist aside and boot it out (bus goes down).**
Move first, then boot out: a boot-out alone is undone at the next login by
`RunAtLoad`. Before either, check the process by command and by listener:

```sh
pid=$(cat ~/.director/nats/nats-server.pid)        # read once
ps -o command= -p "$pid" | grep -F 'nats-phase-0/nats-server.conf'
lsof -nP -a -p "$pid" -iTCP:4222 -sTCP:LISTEN
```

Both must print a line. If either prints nothing, send nothing and stop: the
bus is still up and no seat has lost it. Restore `config.yaml.pre-bus`, record
what `lsof -nP -iTCP:4222 -sTCP:LISTEN` shows in `$W/window-note`, and
reschedule. If both print a line, one command each, in this order:

```sh
mkdir -p "$R"
mv ~/Library/LaunchAgents/$LOCAL_LABEL.plist "$R/"
launchctl bootout "$GUI/$LOCAL_LABEL"
```

Pass: `launchctl print "$GUI/$LOCAL_LABEL"` fails (the job is gone), the file
is in `$R`, and it is byte-identical to `$W`'s as-found copy (`cmp`).

**Step 4. Prove 4222 is free (bus down).** `lsof -nP -iTCP:4222
-sTCP:LISTEN` prints nothing. Anything else stops the window here and the
rollback runs from its first line. Do not start the managed broker onto an
occupied port: marvel refuses to adopt a stranger on its port, and that
refusal is not the check. Then take the cold copy of the store:
`cp -Rp ~/.director/nats/store ~/.director/nats/store.pre-managed-$(date
+%Y%m%d)`, kept until the operator deletes it.

**Step 5. Start the managed broker (managed, local only).**
`marvel daemon reexec`. The daemon renders the conf (with `server_name:
$CLUSTER`), starts nats-server on 4222 with authorization, provisions against
the inherited store, and dials the hub. The daemon is itself a launchd job
under `$DAEMON_LABEL`; the rehearsal confirms the label with `launchctl print`
before the window, and follows the rehearsal's wording if `reexec` and the job
interact differently than assumed. Pass: `marvel bus status` reads ready,
provisioned and authorized, and its binary path, realpath and serving version
match step 2's binary. With no leaf seed yet the leaf reads "unenrolled" or
down, and `reexec` exits nonzero with "not enrolled"; that exit is expected
here.

**Step 6. Re-push the leaf seed (managed, hub leaf).**
`marvel credential put bus/leaf --value-file <the seed file of precondition
3>`. marvel restarts a broker that booted without a seed once, to take it;
that restart is expected. Pass: `marvel bus status --require-leaf` exits 0
within two minutes.

**Step 7. Read both monitors.** The hub's `/leafz`, the local broker's
`/leafz`, `/varz` and `/jsz?streams=true`. Pass, all of:

- the hub's `/leafz` names this cluster and the other leaves, and does not name
  `director-phase0`;
- the hub's other leaves are the same as `$W`'s capture, by name, with no gap;
- the local broker's `/varz` reports `server_name` equal to `$CLUSTER` and a
  serving version equal to the configured binary's;
- `/jsz` stream counts are at least those in `$W`'s capture;
- an anonymous `nats -s nats://127.0.0.1:4222 sub '>'` is refused;
- `marvel bus status --require-leaf` exits 0.

A reading gap not fixed in 15 minutes is the rollback's trigger, with the
operator's word.

**Step 8. KeepAlive on the hub (only if the operator approves it in the
window).** The hub's service file is set to restart after any exit, a clean one
included, because nats-server exits 0 on a graceful stop and the old rule left
the hub down. It touches the hub, so it is its own approval, asked for in the
window by name; a no leaves the hub as found and skips to step 9. It does not
restart the daemon: the daemon must be running under launchd, and the
installer's dry run must read "loaded and running, plist current: nothing to
do" for it, before the hub is replaced; anything else stops this step. Render
the installed hub plist and launcher, check the launcher with its own
`--check` mode under the plist's environment, move the old hub plist into
`$R` with a byte check, boot out the hub job, run the installer's `--apply`,
and check the new job. From here on a deliberate hub stop is `launchctl
bootout`, never a kill, because launchd now relaunches the server after any
exit; a bad conf is relaunched every `ThrottleInterval` seconds, which is loud
on purpose. Pass: the hub job is running with a new pid, the hub's `/leafz`
lists the leaves again within two minutes, and the hub's provision log has a
"provision ok" line newer than the restart and no "jq not on PATH" line newer
than it.

**Step 9. The director seat, then roll the sessions.** Confirm
`~/.marvel/state/nats/director.pass` exists (`test -r`, never read), switch
the operator's registration to the prepared `director-mcp-seat` one, and
reconnect it. Pass: `inbox_summary` answers. Then roll the sessions,
supervisors first, team by team, each after its handoff marker. Pass per team:
a respawned seat answers a roll call; a global publish from a supervisor lands
in the other clusters' global streams; one respawned seat's pane start path is
the team's `WorkDir` from `$W`. **Before the next team, capture every
respawned pane** (`marvel capture <session>`) and read it. A seat respawned in
auto mode can stop on a Claude Code onboarding dialog with an option already
selected. If any pane shows one, stop that team's roll and the teams after it.
The dialog is **never answered automatically**, by marvel, by a seat or by an
inject: the operator reads it and decides each one by hand.

**Step 10. Watch** 30 minutes. Pass: `marvel bus status --require-leaf` exits
0 at the end, and `marvel events` shows no `bus.leaf.down` for the period.
Keep `store.pre-managed-*`, `$W` and `$R` until the operator deletes them.

### Known states after the window

Written down so none of them is read as a fault:

- **A daemon restart drops the cluster's leaf** until the seed is pushed again
  (step 6's command), because credentials are transient by design. Expect a
  `bus.leaf.down` that is about this plan and not about the fabric. Every
  stop, start or reexec of the daemon adds step 6 to its checklist.
- **The ring does not survive a reexec.** A ring-only `bus.leaf.down` is lost
  at the next reexec, and nobody hears it unless they run `marvel bus status`
  or `marvel events`.
- **Logout or reboot of the hub host is a hub outage**, seen only from the
  leaf clusters (their own `bus.leaf.down`). The hub host cannot report it.
- **The local broker is anonymous until the window turns authorization on.**

## 7. Rollback

On a failed pass condition at step 7 not fixed in 15 minutes, a failed step 9,
or the operator's word. It reverses steps 3 to 6 with the same port-free
check.

1. If step 8 ran, restore the hub first: boot out the new hub job, put the old
   hub plist back from `$R` (after `cmp` against `$W`'s as-found copy exits 0)
   and the old launcher from its backup (`cmp` against `$W`'s copy exits 0), and
   only then `launchctl bootstrap "$GUI"` the old plist. The hub is down
   between the bootout and the bootstrap. If step 8 did not run, skip this.
2. Stop the managed broker and the daemon.
   - **Daemon alive:** `marvel stop`, without `--keep-bus`, stops the broker
     with the daemon. Panes survive.
   - **Daemon dead, broker alive:** the managed broker outlives its daemon by
     design, so the next daemon can adopt it. Stop it from its pidfile, with
     the match checked, not eyed:
     ```sh
     pid=$(cat ~/.marvel/run/nats-server.pid)          # read once
     ps -o command= -p "$pid" | grep -F "$HOME/.marvel/state/nats/nats-server.conf"
     lsof -nP -a -p "$pid" -iTCP:4222 -sTCP:LISTEN
     ```
     Only if **both** commands print a line, `kill -TERM "$pid"`. If either
     prints nothing, the pidfile is stale or names another process: send
     nothing, and find the 4222 listener with `lsof` before going on.
3. Prove 4222 is free: `lsof -nP -iTCP:4222 -sTCP:LISTEN` prints nothing. Do
   not go on if it does.
4. `cp -p $W/config.yaml.pre-bus ~/.marvel/config.yaml`.
5. The store. If the rehearsal showed the domain-stamped store loads under the
   old conf, keep it, and nothing is lost. Otherwise `mv ~/.director/nats/store
   ~/.director/nats/store.failed-managed-$(date +%Y%m%d)` and then `cp -Rp
   ~/.director/nats/store.pre-managed-* ~/.director/nats/store`. Mail sent
   during the window is then lost; say so to the senders.
6. Put the old job back: `mv "$R/$LOCAL_LABEL.plist" ~/Library/LaunchAgents/`,
   then `launchctl bootstrap "$GUI" ~/Library/LaunchAgents/$LOCAL_LABEL.plist`.
7. Pass: the hub's `/leafz` lists the old local broker's leaf (`director-phase0`)
   again and the other leaves, as `$W`'s capture shows; `$R` holds neither
   plist; the installed hub launcher is byte-equal to `$W`'s copy when step 8
   ran; the old leaf conf kept its mode and mtime.
8. Start the daemon as it ran before, so it adopts the panes. Unrolled seats
   reconnect by themselves. Respawned seats carry a team user to an anonymous
   broker; whether that connects is the rehearsal's answer (its step 8), and
   if it does not, roll them once more. The director seat goes back to its
   bare registration, since the wrapper exits 1 once no daemon renders
   `director.pass`.
9. Leave the `bus/leaf` credential unpushed. The old broker reads its own
   include file.

Downgrade caution for later: once the managed broker renders a per-role
(dotted) user, a daemon downgrade below that change breaks those roles. Revert
the render first (marvel `CLAUDE.md`, global roles).
