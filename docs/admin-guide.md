# Admin Guide

This guide covers daemon setup, remote access configuration, SSH key
management, and operational concerns.

## Starting the daemon

### Local only (default)

```bash
marvel daemon
```

Listens on `/tmp/marvel.sock`. Only processes on the same machine can
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
marvel daemon listening on /tmp/marvel.sock (unix)
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
* local           /tmp/marvel.sock
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
`local` cluster pointing to `/tmp/marvel.sock`.

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
