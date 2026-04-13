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

Stop with:
```bash
marvel stop
```

## SSH key management

The mrvl:// listener authenticates clients using SSH public keys stored
in `~/.marvel/authorized_keys` (OpenSSH format, same as `~/.ssh/authorized_keys`).

### Authorizing a client

On the daemon machine:

```bash
# Add a client's public key
marvel keys add /path/to/client_id_ed25519.pub

# Or pipe it
cat ~/.ssh/id_ed25519.pub | marvel keys add /dev/stdin
```

**Typical workflow:**

1. The client generates an SSH key pair (or already has one in `~/.ssh/`)
2. The client sends their public key to the admin
3. The admin runs `marvel keys add` on the daemon machine
4. The client adds the cluster: `marvel config add-cluster prod mrvl://host`
5. The client connects: `marvel get sessions --cluster prod`

### Listing authorized keys

```bash
marvel keys list
```

Output:
```
SHA256:abc123...  ssh-ed25519  michael@laptop
SHA256:def456...  ssh-ed25519  deploy@ci
```

### Revoking a client

```bash
marvel keys remove SHA256:abc123...
```

The client can no longer connect via mrvl://. Local Unix socket access
is unaffected (it has no authentication).

### Viewing the host key fingerprint

```bash
marvel keys host-fingerprint
```

Clients can use this to verify they're connecting to the right daemon
(defense against man-in-the-middle). Currently marvel uses
trust-on-first-use (TOFU) — the first connection is trusted, future
connections verify the key hasn't changed.

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
marvel keys add ~/.ssh/id_ed25519.pub  # authorize yourself
```

**On the laptop:**
```bash
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
marvel keys add alice_id_ed25519.pub
marvel keys add bob_id_ed25519.pub
marvel keys add carol_id_ed25519.pub
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
    marvel keys add ${{ secrets.CI_SSH_PUBKEY }}
    marvel work manifests/review-team.yaml
    sleep 300  # let agents work
    marvel stop
```

**Why:** Ephemeral agent fleets for automated tasks. The daemon starts,
runs the team, and stops. No persistent state needed.

## Upgrading

```bash
marvel upgrade
```

If installed via Homebrew:
```
Installed via Homebrew. Running: brew upgrade ArcavenAE/tap/marvel
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
ssh: client connected: michael (SHA256:abc...)           # remote connection
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
marvel keys add your_id_ed25519.pub
```

### "no SSH auth available"

Your SSH agent isn't running or has no keys loaded.

```bash
eval $(ssh-agent)
ssh-add ~/.ssh/id_ed25519
```

### Sessions keep restarting

Check the restart policy and health check configuration. A session that
can't send heartbeats will be marked unhealthy and restarted:

```bash
marvel describe session dev/squad-worker-g1-0
```

Lower the `failure_threshold` or increase the `timeout` if agents need
more time to initialize.
