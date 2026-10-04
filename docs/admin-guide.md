# Admin Guide

This guide covers daemon setup, remote access configuration, SSH key
management, and operational concerns.

## Host prerequisites

### The host must stay awake

A cluster host must not idle-sleep while it carries seats. A sleeping host
stops everything at once: the daemon, the bus and its leaf link to a shared
hub, and every session. Its peers see silence, not an error. Nothing on the
bus says why, and another cluster cannot wake it.

This matters most on laptops, whose power settings usually let them
idle-sleep.

Display sleep is fine; only system sleep stops the cluster. Closing a
laptop's lid is a separate case that the settings below do not address.
Wake-on-LAN is not a remedy: its magic packet has to reach the host on the
same network segment, so a peer on another network, or across a router or a
VPN, cannot send it.

On macOS, either of these keeps the system awake (pick one):

- **Hold a power assertion** for as long as the cluster runs:

  ```sh
  caffeinate -s    # prevents system sleep; valid only on AC power
  caffeinate -i    # prevents idle sleep, on battery as well
  ```

  The assertion lasts while the `caffeinate` process runs and is released
  when it exits. No system setting changes. To tie it to the daemon's
  lifetime, pass the daemon's process id:

  ```sh
  caffeinate -i -w <daemon-pid>    # released when that process exits
  ```
- **Turn off system sleep on AC power:**

  ```sh
  sudo pmset -c sleep 0
  ```

  This needs admin rights and changes a system setting that persists until
  you set it back.

To check what is holding the system awake, and when it last slept:

```sh
pmset -g assertions    # current power assertions, by process
pmset -g log           # history of sleeps and wakes
```

On Linux, run the daemon under `systemd-inhibit --what=idle:sleep`, which
holds an inhibitor lock for as long as the daemon runs. A desktop
environment's own automatic suspend setting (GNOME's, for example) is
separate and must also be off. To rule out sleep on the host entirely:

```sh
sudo systemctl mask sleep.target suspend.target hibernate.target hybrid-sleep.target
```

That changes a system setting until the targets are unmasked. These Linux
lines were not verified on a Linux cluster host.

### macOS: grant the terminal Local Network access

On macOS, an app needs the Local Network permission (System Settings, Privacy
& Security, Local Network) before it can reach hosts on the LAN. Without it,
every LAN connection from that terminal fails with `No route to host`, which
reads as a routing problem. The permission prompt can sit unanswered on a
fresh machine, so grant it to the terminal that will run the daemon and its
checks before testing any LAN path.

## Starting the daemon

### Local only (default)

```bash
marvel daemon
```

Listens on `~/.marvel/run/marvel.sock`. Only processes on the same machine can
connect. No authentication required — anyone who can reach the socket
can issue commands.

**When to use:** Personal development machine, single-user, no remote
access needed.

### With remote access

```bash
marvel daemon --mrvl
```

Starts both the Unix socket (local) and the mrvl:// listener (remote,
port 6785). The mrvl:// listener is an embedded SSH server — no dependency
on the system's sshd.

On first run, the daemon generates an ed25519 host key at
`~/.marvel/ssh_host_ed25519_key`. This key identifies the daemon to
connecting clients.

Output:
```
marvel daemon listening on ~/.marvel/run/marvel.sock (unix)
mrvl:// listener on :6785
remote access: --cluster <name>  (config: mrvl://kinu:6785)
```

**When to use:** You want to manage agents from another machine, or
you're running a shared daemon that multiple people connect to.

### Custom port

```bash
marvel daemon --mrvl :7000
```

**When to use:** Port 6785 is taken, or you're running multiple daemons
on the same host.

### Custom socket path

```bash
marvel daemon --socket /var/run/marvel.sock --mrvl
```

**When to use:** System service configuration, multiple daemons on one
host (each with a different socket path).

### Background daemon

```bash
marvel daemon --mrvl &
# or with systemd, launchd, etc.
```

By default the daemon tees its stderr into `~/.marvel/log/daemon.log`
and writes its pid to `~/.marvel/run/daemon.pid`. Both are created
with marvel-standard permissions (log 0600, pid 0644) inside the
0700 data directory. A second daemon started while the first is
still running refuses with a clear error.

Tail the log from anywhere on the daemon host:

```bash
tail -f ~/.marvel/log/daemon.log
```

Override the paths with `--log-file PATH` or `--pidfile PATH`, or
disable either with an empty string:

```bash
marvel daemon --log-file="" --pidfile=""     # pure stderr, no pidfile
marvel daemon --log-file /var/log/marvel.log # systemd-friendly path
```

Stop with:
```bash
marvel stop              # detach: agents keep running
marvel stop --teardown   # end every agent, then stop
```

### Shift timeout

A rolling shift that never reaches readiness (for example a heartbeat-checked
role whose new generation never beats) is aborted and rolled back with a
`team.shift-timed-out` event. The bound defaults to 10 minutes. Tune it with
`--shift-timeout` (a Go duration) or the `MARVEL_SHIFT_TIMEOUT` environment
variable:

```bash
marvel daemon --shift-timeout 2m        # abort a stuck shift after 2 minutes
MARVEL_SHIFT_TIMEOUT=15s marvel daemon  # same, via the environment
```

The flag wins when set; otherwise `MARVEL_SHIFT_TIMEOUT` is parsed; unset keeps
the 10-minute default. A short value is also how you demonstrate the timeout
without a 10-minute wait (see the Act 1d beat in `docs/demo.md`).

### Detach vs teardown

`marvel stop`, SIGINT, and SIGTERM all *detach*: the daemon
checkpoints its state file and exits while every agent keeps running
in its tmux pane. The next `marvel daemon` reads that state back and
adopts the live panes, so restarts and upgrades cost no agent context.
Agents whose panes died while no daemon was running are reaped on the
first reconcile pass, exactly as if the daemon had never left.

`marvel stop --teardown` is the clean-machine variant: every session
is deleted and every workspace tmux session killed before the daemon
exits, leaving nothing to adopt.

Detach relies on the state file. With `--state-bolt=""` there is no
recorded intent to come back to, so the next start kills every
`marvel-*` pane it finds.

## SSH key management

The mrvl:// listener authenticates clients using SSH public keys stored
in `~/.marvel/authorized_keys` (OpenSSH format, same as `~/.ssh/authorized_keys`).

For the full client + daemon key workflow — including generating
dedicated marvel keys, permission conventions, the `~/.marvel/` layout,
and `marvel keys doctor` — see the [keys guide](keys.md).

### Typical workflow

1. **Client:** `marvel keys generate` creates `~/.marvel/keys/client_ed25519`
2. **Client:** `marvel keys show | pbcopy` copies the public key
3. **Admin:** `marvel keys authorize /path/to/client.pub` on the daemon machine
4. **Client:** `marvel config add-cluster prod mrvl://host` (auto-attaches the default identity)
5. **Client:** `marvel --cluster prod get sessions`

### Authorizing a client

On the daemon machine:

```bash
marvel keys authorize /path/to/client.pub
# or, if you received the pubkey as text:
echo 'ssh-ed25519 AAAA... alice@laptop' | marvel keys authorize /dev/stdin
```

`authorize` is aliased as `add` for compatibility with earlier releases.

### Listing authorized clients

```bash
marvel keys authorized
```

Output:
```
FINGERPRINT                                         TYPE         COMMENT
SHA256:abc...                                       ssh-ed25519  michael@laptop
SHA256:def...                                       ssh-ed25519  deploy@ci
```

### Revoking a client

```bash
marvel keys revoke SHA256:abc...
```

The client can no longer connect via mrvl://. Local Unix socket access
is unaffected (it has no authentication).

### Viewing the host key fingerprint

```bash
marvel keys host-fingerprint
```

Share the fingerprint with clients so they can verify they're connecting
to the right daemon. Clients record trusted daemon keys in
`~/.marvel/known_hosts`; first connection prompts interactively or is
bootstrapped with `marvel keys trust <cluster>`. Host key changes are
detected and refused — see the [keys guide](keys.md) for details.

## Cluster configuration

Clusters are stored in `~/.marvel/config.yaml`. This is the client-side
config — it tells the CLI how to reach each daemon.

### Add a cluster

```bash
marvel config add-cluster kinu mrvl://kinu
marvel config add-cluster staging mrvl://deploy@staging.example.com:7000
marvel config add-cluster dev /tmp/marvel-dev.sock
```

### List clusters

```bash
marvel config list
```

Output:
```
* local           ~/.marvel/run/marvel.sock
  kinu            mrvl://michael@kinu
  staging         mrvl://deploy@staging.example.com:7000
```

The `*` marks the current cluster.

### Switch clusters

```bash
marvel config use-cluster kinu
```

All subsequent commands go to the `kinu` daemon until you switch again.

### Remove a cluster

```bash
marvel config remove-cluster staging
```

### Config file location

`~/.marvel/config.yaml`. Created automatically on first use with a
`local` cluster resolving to `~/.marvel/run/marvel.sock`.

## The message bus

A cluster may declare a message broker. Marvel provisions it, keeps its
structure correct, and can attach it to a shared hub. This section covers what
you declare, what marvel does with it, and what it does when something is
wrong.

### Declaring it

The broker is a cluster Services entry in the client config:

```yaml
services:
  - name: bus
    class: message-bus
    provider: nats-server
    mode: managed
```

`class` and `provider` are checked against registries, so a typo is refused at
read time rather than at attach. `mode` is one of:

| Mode | What marvel does today |
|---|---|
| `managed` | Renders the broker's config, starts it, supervises it, provisions its objects, mints the credentials sessions present, and keeps the structure correct. |
| `adopted` | Does not start or supervise the broker. Sessions receive `NATS_URL` and nothing else. |
| `external` | The same path as `adopted`: sessions receive `NATS_URL` and nothing else. |

The split that matters in the code is managed against not-managed, and
`adopted` and `external` are on the same side of it. The daemon branches once,
on whether the mode is `managed`; both other modes take the identical arm and
get a record that carries a URL. The declared mode is kept and reported, so
`marvel bus status` tells you which one you wrote, but nothing in the daemon
behaves differently between them yet. Choose by what you mean, and do not
expect the choice to change marvel's behaviour today.

The consequences of not-managed are worth stating plainly, because they are
what an operator actually feels:

- no credentials. Marvel mints nothing for a broker it did not render, and
  there is deliberately no field for a foreign credential, because holding one
  would be custody rather than issuance (ADR-009). Sessions get the URL and
  must be authorized some other way, or the broker must not require it.
- no structural health, no hold. Everything in the next two sections is
  managed-only. A not-managed broker is not read for structure and does not
  gate spawns.
- no leaf management. `marvel bus leaf connect` and `disconnect` act on a
  managed broker's rendered config, so they do not apply. `bus status` reports
  the leaf as `n/a`.
- a `url` is required. A not-managed entry with neither `url` nor `listen` is
  refused at read time.

A `bus:` block is the same record under an older spelling, lifted at read time
as the entry named `bus`. Declaring both is refused, with an error naming the
cluster and asking you to keep one spelling.

A `seat:` block gives a human director seat its own broker user:

```yaml
    seat:
      workspace: <ws>
      team: <team>
```

That user is named `director` and its password is written to
`<state dir>/nats/director.pass`. Sessions marvel spawned get their own
credentials stamped into their environment instead; the seat exists for a
session marvel did not start. `hub.ca_file` trusts a TLS hub from the leaf
remote; it must be an absolute path, or `~`-relative, because the broker
process resolves it rather than the daemon.

### What survives a restart

Passwords are recovered from the `authorization.conf` marvel rendered last
time, so a daemon restart or a `daemon reexec` leaves running sessions'
credentials valid. The daemon logs how many it recovered.

### Structural health

Readiness on a managed broker is structural, and it is read from the running
broker rather than inferred from the files marvel wrote:

- every declared service-scope object exists
- `/varz` reports `auth_required`
- `/varz` reports `tls_required`, which is recorded but does not yet join the
  ready predicate; until the client listener is rendered with TLS, false is
  the interim posture rather than a miss

It is structural only, by design. Consumer counts, undelivered durables and
stream age are vital signs and never enter this reading.

It is read at start, after a reload, and on the same thirty-second poll that
checks the leaf link.

### What a miss does, and what it does not

On a structural miss marvel re-provisions and holds new spawns. It does not
restart the broker. Specifically:

- the missing objects are re-provisioned
- an authorization miss gets one SIGHUP
- readiness drops while the problem stands, so new sessions are held rather
  than launched onto a broker that cannot carry them
- `bus.unprovisioned` is emitted once per change in the problem set, not once
  per tick

A held spawn carries the reason, so you read the problem from the hold rather
than from the event ring. It names the listener and then what is wrong: the
missing objects, or that authorization has not loaded and a reload is
outstanding, or that the broker is down and which restart is waiting on
backoff.

### Connecting to a shared hub

A leaf link attaches a managed local broker to a shared hub:

```sh
marvel bus status              # pid, listener, readiness, hub leaf link
marvel bus leaf connect        # attach without bouncing the broker
marvel bus leaf disconnect     # detach without bouncing the broker
```

Connect and disconnect are reload-only: marvel renders the leaf block and
reloads, so agents on the broker keep running.

The hub itself is declared on the cluster's bus entry, before the daemon
starts. In the Services spelling from "Declaring it", it goes inside the
entry:

```yaml
services:
  - name: bus
    class: message-bus
    provider: nats-server
    mode: managed
    listen: 127.0.0.1:4222
    hub:
      url: nats-leaf://<hub-address>:7442
```

Write `<hub-address>` as a hostname, not a literal IP. A hostname the broker
re-resolves lets the leaf follow the hub to another subnet with no restart and
no seed push, as the 2026-10-04 network move showed. A literal IP breaks when
the hub host changes subnet: the leaf keeps dialing the old address. The key
takes one URL; a list of hub URLs is requested in marvel#575.

A cluster written in the older `bus:` block spelling puts the same `hub:` key
under its `bus:` block instead; the third cluster's bring-up (aae-orc#461)
used that spelling. Use one spelling per cluster, never both: a cluster that
declares a `bus:` block and a message-bus Services entry is refused at load
("keep one spelling"), and a `hub:` key at the top of the file is ignored.

The daemon reads its client config once, at start, and there is no reload
verb (marvel#514). A hub block added to a running daemon's config is not
seen: `marvel bus leaf connect` answers that the cluster declares no hub. If
that happens, `marvel daemon reexec` re-reads the config, and the leaf seed
has to be pushed again afterwards, because a reexec drops it (marvel#339).

The seed does not have to live on the cluster. The hub operator can push it
from the hub's host, over the cluster's `mrvl://` listener, once that host is
enrolled (see SSH key management):

```sh
marvel --cluster <name> credential put bus/leaf --value-file <seed-file>
```

So the cluster holds no copy, and after every daemon stop, start or reexec
the push comes from the hub's side.

Check the cluster name before you push. While marvel#502 is open, an unknown
`--cluster` name only warns and then acts on the local daemon, so a typo
stores the seed in the hub host's own daemon rather than the cluster's.

The exception is the leaf seed, which rides in the broker's environment and is
read once at start. A broker already running with the seed takes a connect on
a reload. A broker that booted without one, or one whose stored seed has since
rotated, needs a fresh process, so marvel restarts it; clients reconnect, and
that restart is counted separately from a crash and is not subject to backoff.
Storing the same seed again is not a rotation and restarts nothing.

A leaf link that goes down is reported and nothing is restarted: the local
broker keeps serving its own sessions.

### Moving a cluster to another network

If a cluster's hub URL has to change (the hub host moved subnets and the URL
is a literal IP), the sequence two clusters ran on 2026-10-04 is:

1. Change the hub URL in the client config.
2. Restart the daemon, because it reads that config once, at start.
3. From the hub's side, push `bus/leaf` again.

Step 3 is needed because of marvel#339: the restart drops the leaf seed, and
until it is pushed again the cluster runs local-only. With a hostname the
broker re-resolves, none of the three steps is needed.

### Ports

A managed broker's monitoring endpoint is on loopback at the listen port plus
4000, or 4000 below the listen port when plus would exceed 65535. So a broker
listening on 4222 monitors on 8222, and one on a high port relocates downward
rather than failing to start. The relocated port is always valid and always
distinct from the listen port.

### Stopping the daemon without stopping the bus

```sh
marvel stop --keep-bus   # detach, leave a managed broker up for the next daemon
```


## Data directory

All marvel daemon and client state lives in `~/.marvel/`:

```
~/.marvel/
  config.yaml                 Client cluster configuration
  ssh_host_ed25519_key        Daemon SSH host key (auto-generated)
  ssh_host_ed25519_key.pub    Host key public part (shareable)
  authorized_keys             Authorized client SSH public keys
```

Permissions: the directory is created with `0700`, key files with `0600`.

## Typical deployment scenarios

### Personal development machine

One machine, one user, local access only.

```bash
marvel daemon &
marvel work manifests/my-team.yaml
marvel get sessions
```

No SSH, no keys, no config file needed. The Unix socket handles everything.

### Two machines (laptop + workstation)

You develop on a laptop but run agents on a workstation with more resources.

**On the workstation:**
```bash
marvel daemon --mrvl
# authorize yourself — copy laptop's ~/.marvel/keys/client_ed25519.pub here
marvel keys authorize /tmp/laptop.pub
```

**On the laptop:**
```bash
marvel keys generate                                  # once
marvel keys show | ssh workstation 'cat > /tmp/laptop.pub'
marvel config add-cluster workstation mrvl://workstation.local
marvel config use-cluster workstation
marvel work manifests/big-team.yaml
marvel get sessions -w
```

**Why:** The workstation has more CPU/RAM for running multiple Claude
instances. You manage everything from your laptop.

### Team shared daemon

Multiple people connect to a shared daemon on a team server.

**On the server:**
```bash
marvel daemon --mrvl
# Authorize each team member
marvel keys authorize alice.pub
marvel keys authorize bob.pub
marvel keys authorize carol.pub
```

**Each team member:**
```bash
marvel config add-cluster team mrvl://team-server.internal
marvel config use-cluster team
marvel get sessions
```

**Why:** Shared visibility into agent fleet state. Anyone on the team
can check session health, capture output, or trigger shifts. The daemon
runs on infrastructure with stable uptime.

### CI/CD pipeline

A CI job runs agents for automated code review or testing.

```yaml
# .github/workflows/review.yml
- name: Start marvel
  run: |
    marvel daemon --mrvl &
    echo "${{ secrets.CI_SSH_PUBKEY }}" | marvel keys authorize /dev/stdin
    marvel work manifests/review-team.yaml
    sleep 300  # let agents work
    marvel stop --teardown
```

**Why:** Ephemeral agent fleets for automated tasks. The daemon starts,
runs the team, and stops. No persistent state needed.

## Upgrading

```bash
marvel upgrade
```

If installed via Homebrew:
```
Installed via Homebrew. Running: brew upgrade arcavenae/tap/marvel
```

If installed as a direct binary:
```
Checking for updates...
Downloading marvel-darwin-arm64 (alpha-20260413-054538-659ceb1)...
Upgraded to alpha-20260413-054538-659ceb1
```

Pin to a specific version:
```bash
marvel upgrade --version v0.2.0
```

On a Homebrew install, `marvel upgrade` delegates to `brew upgrade`, and a tap
offers only its latest formula, so an exact pin cannot be held there once a
newer alpha ships (marvel#485). For an exact version, install with mise (see
the README). If a host ends up with both, the first `marvel` on `PATH` is the
one that runs, which may be the stale one; check it with `command -v marvel`
and `marvel version`, or call the pinned binary by its full path.

### Under mise: stop and start, not reexec

`marvel daemon reexec` re-executes the marvel binary at the running daemon's
own path (`os.Executable`). That picks up an upgrade only when the new binary
replaces the old one at the same path. mise installs each version in its own
directory and leaves the old one in place, so after `mise use` a reexec
restarts the old version (marvel#523).

Under mise, detach the old daemon and start the new one from its mise path:

```sh
mise use -g github:ArcavenAE/marvel@<tag>
marvel --cluster <name> stop --keep-bus     # agents keep running; the broker stays up
"$(mise where github:ArcavenAE/marvel@<tag>)/marvel" daemon --mrvl
```

The new daemon adopts the running sessions and the broker. On the third
cluster's bring-up (aae-orc#461) that path took one second between the stop
and the start, adopted all four sessions and the running broker, and the
daemon then reported the new version. Start it from the same working
directory and with the same flags as before. Then push the leaf seed again,
because the new daemon starts without it (see "Connecting to a shared hub").

## Monitoring

### Watch mode

```bash
marvel get sessions -w
```

Live dashboard showing all sessions, their state, health, context
percentage, and generation. Updates every second.

### Daemon logs

The daemon logs to stderr. In production, redirect to a file or
journal:

```bash
marvel daemon --mrvl 2>&1 | tee /var/log/marvel.log
```

Key log messages:
```
session dev/squad-worker-g1-0 using forestage adapter    # adapter selection
session dev/squad-worker-g1-0 running in pane %5         # session created
health: session ... failed (restart_policy=always)       # health failure
shift: initiated for dev/squad gen 1→2                   # shift started
ssh: client connected: michael@10.0.0.42 (SHA256:abc...) # remote connection
inject: dev/squad-worker-g1-0 <- 42 bytes                # executive injection
```

## Troubleshooting

### "connect to daemon: no such file or directory"

The daemon isn't running or the socket path is wrong.

```bash
# Check if daemon is running
ps aux | grep 'marvel daemon'

# Start it
marvel daemon &
```

### "daemon disconnected" in watch mode

The daemon was stopped or crashed. Watch mode shows the last known state
and reconnects automatically when the daemon restarts.

### "unknown key for user"

Your SSH public key isn't authorized on the daemon. Ask the admin to run:

```bash
marvel keys authorize your.pub
```

### "no SSH auth available"

No marvel client key, no ssh-agent, no usable `~/.ssh/` key.

```bash
marvel keys generate                 # create a marvel client key
# or
eval $(ssh-agent) && ssh-add ~/.ssh/id_ed25519
```

### "permissions ... are too open"

A private key is group- or world-readable. Fix with:

```bash
marvel keys doctor --fix
```

### Sessions keep restarting

Check the restart policy and health check configuration. A session that
can't send heartbeats will be marked unhealthy and restarted:

```bash
marvel describe session dev/squad-worker-g1-0
```

Lower the `failure_threshold` or increase the `timeout` if agents need
more time to initialize.

### A spawn was refused

Two different conditions hold a role back, and they look alike from the
outside. Tell them apart first:

```bash
marvel get budgets                                  # where each ceiling stands
marvel events --kind admission.refused              # a budget refused it
marvel events --kind health.crashloop-backoff       # a crash loop is cooling
marvel describe team fanout/crew                    # the declared budget
```

A budget refusal names its arithmetic, so the fix is usually visible in the
message. `marvel get budgets` gives the standing picture:

```
WORKSPACE  TEAM  DIMENSION     LIMIT    OBSERVED  HEADROOM  STATE       WINDOW  NOTE
fanout     crew  max_sessions  6        6         0         at-ceiling  -       -
fanout     crew  max_tokens    2000000  412118    1587882   ok          14m3s   partial: some sessions unobserved, so this is a floor
```

Read the STATE column carefully, because two of its values look alike and
mean different things:

| STATE | Meaning |
|---|---|
| `ok` | Headroom left. |
| `at-ceiling` | No headroom for growth, and nothing is being refused. This is the resting state of a healthy team, since declared replicas are allowed to equal the ceiling and replacing a crashed replica is exempt. |
| `refusing` | A refusal is standing right now: the reconciler is holding a role back, and the NOTE column carries the arithmetic. Cross-check with `marvel describe team` (`Admission.held`) and `marvel events --kind admission.refused`. |
| `unmetered` | Nothing has been measured for this dimension yet, so the figure is absence rather than zero. |

A session row can also read OBSERVED above LIMIT with a `shift` note. That
is a rotation in flight: the new generation runs beside the old, and a
session ceiling exempts the overlap. It resolves itself when draining
finishes.

Two ways out of a session ceiling: raise `max_sessions` in the manifest and
re-apply, or free headroom with `marvel scale ... --replicas N-1` (a
scale-down is never refused). Either takes effect on the next reconcile
tick, within a couple of seconds. There is no clear command and no resume
verb, because the condition is recomputed from live state every tick.

A token ceiling has no shedding move: retired spend stays counted, since a
fan-out's cost is mostly in sessions that already exited. Killing sessions
does not un-spend tokens. Raise `max_tokens` and re-apply.

Two notes in the table matter for trust. `partial` means some contributing
session was never observed, so the figure is a floor rather than a small
number, which is why a refusal against it is still sound while an admission
carries a caveat. `suspect` means the meter caught a cumulation violation
and the figure may be inflated, so check `marvel daemon logs` before raising
a ceiling on its account.

One limit to know: `max_tokens` counts from when accounting started, and the
meter lives in the daemon's memory. A daemon restart or `marvel daemon
reexec` resets the window, and the daemon says so in its log at startup for
every team that declares one. The `WINDOW` column dropping back near zero is
the visible signal that it happened.
