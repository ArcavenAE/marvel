# SSH Keys

marvel uses SSH public-key auth for remote access over the `mrvl://`
protocol. This page covers key generation, sharing, storage, permissions,
and troubleshooting.

## TL;DR

On your client machine:

```bash
marvel keys generate                             # create client keypair
marvel keys show | pbcopy                        # copy pubkey to clipboard
# send the pubkey to the admin running the daemon
marvel config add-cluster prod mrvl://prod-host  # attaches the default key
marvel --cluster prod get sessions               # it just works
```

On the daemon machine:

```bash
marvel daemon --mrvl                             # start daemon + SSH listener
marvel keys authorize client.pub                 # authorize the client key
```

## The `~/.marvel/` directory

All key material lives under `~/.marvel/`. marvel creates and permission-
enforces this directory on first use.

```
~/.marvel/                         0700   root (owner-only)
├── config.yaml                    0600   client cluster config
├── authorized_keys                0600   daemon: trusted client pubkeys
├── ssh_host_ed25519_key           0600   daemon: host private key
├── ssh_host_ed25519_key.pub       0644   daemon: host public key
├── known_hosts                    0644   client: trusted server host keys
├── keys/                          0700   client: private key directory
│   ├── client_ed25519             0600   default client private key
│   └── client_ed25519.pub         0644   default client public key
├── log/                           0700   daemon: log directory
│   └── daemon.log                 0600   daemon stderr tee (created on 'marvel daemon')
└── run/                           0700   daemon: runtime-state directory
    └── daemon.pid                 0644   daemon pid (created on 'marvel daemon')
```

marvel refuses to use any private key whose permissions are not `0600`,
matching OpenSSH's behavior. If a key is found with weaker permissions,
the error tells you exactly how to fix it:

```
cluster identity /Users/you/.marvel/keys/client_ed25519:
  permissions 644 for ".../client_ed25519" are too open;
  run: chmod 600 /Users/you/.marvel/keys/client_ed25519
```

You can also let marvel repair the whole directory for you:

```bash
marvel keys doctor           # audit only (exit code 1 if issues found)
marvel keys doctor --fix     # audit and repair
```

## Client-side keys

### Generating a client key

```bash
marvel keys generate
```

Creates `~/.marvel/keys/client_ed25519` (and `.pub`) with a default
comment of `user@host`. This is the key the CLI will use when connecting
to any `mrvl://` or `ssh://` cluster that does not override it.

**Custom name** (for per-cluster keys):

```bash
marvel keys generate --name prod_ed25519
marvel keys generate --name staging_ed25519 --comment "alice@laptop staging"
```

**Regenerating** (requires `--force`, never silent):

```bash
marvel keys generate --force
```

### Showing / sharing your public key

```bash
marvel keys show                    # default key to stdout
marvel keys show --name prod_ed25519
marvel keys show | pbcopy           # copy to macOS clipboard
marvel keys show | ssh admin@prod 'cat > /tmp/my.pub && marvel keys authorize /tmp/my.pub'
```

### Listing client keys

```bash
marvel keys list
```

```
NAME              TYPE         FINGERPRINT                                         COMMENT
client_ed25519    ssh-ed25519  SHA256:abc...                                       alice@laptop
prod_ed25519      ssh-ed25519  SHA256:def...                                       alice@laptop prod
```

## Daemon-side keys

The daemon maintains its own host key and an `authorized_keys` list.

### Host key (auto-generated)

On the first `marvel daemon --mrvl` run, marvel creates
`~/.marvel/ssh_host_ed25519_key`. This is the server identity the client
connects to. Share its fingerprint so clients can verify they are
talking to the right host:

```bash
marvel keys host-fingerprint
# SHA256:HNSMOpL4gl1n0/lPpWs9bqMbkmm+Yu6rVRHbd6z8GvY
```

### Host key verification on the client

Clients record trusted daemon host keys in `~/.marvel/known_hosts`
(OpenSSH format). On first connection to a new daemon:

- **Interactive (TTY):** marvel prompts with the fingerprint and asks
  whether to trust it.
- **Non-interactive (CI, pipelines):** marvel refuses and tells you to
  run `marvel keys trust <cluster>`.

```bash
marvel keys trust prod                   # record the current host key
```

If the daemon's host key ever changes (genuine reinstall, or a MITM
attempt) connections will refuse with a clear error showing both the
offered and expected fingerprints. Investigate the reason before
accepting — to override, remove the offending line from
`~/.marvel/known_hosts` and reconnect.

### Authorizing clients

```bash
marvel keys authorize /path/to/client.pub
```

Adds the public key to `~/.marvel/authorized_keys` (OpenSSH format).
After this, the client can connect using its matching private key.

`authorize` is aliased as `add` for muscle memory, but new tooling
should prefer `authorize`.

### Listing authorized clients

```bash
marvel keys authorized
```

```
FINGERPRINT                                         TYPE         COMMENT
SHA256:yrV5M1roJXkgB9qeKBQ/5PoNPd1alhvliNjJr4R9G/s  ssh-ed25519  alice@laptop
```

### Revoking a client

```bash
marvel keys revoke SHA256:yrV5M...
```

## Cluster configuration

`marvel config add-cluster` writes `~/.marvel/config.yaml`. When the
address is `mrvl://` or `ssh://`, marvel defaults the cluster's
`identity` field to `~/.marvel/keys/client_ed25519` when that key
exists. Opt out with `--no-default-identity` to fall back to
`SSH_AUTH_SOCK` or `~/.ssh/` keys.

```bash
# Attach default ~/.marvel/keys/client_ed25519 (most common)
marvel config add-cluster prod mrvl://prod-host

# Attach a custom per-cluster key
marvel config add-cluster staging mrvl://staging-host \
  --identity ~/.marvel/keys/staging_ed25519

# Use ssh-agent / ~/.ssh/* instead
marvel config add-cluster legacy mrvl://old-host --no-default-identity
```

List your clusters:

```bash
marvel config list
#    NAME       ADDRESS                IDENTITY
# *  local      /tmp/marvel.sock       -
#    prod       mrvl://prod-host       /Users/alice/.marvel/keys/client_ed25519
#    staging    mrvl://staging-host    /Users/alice/.marvel/keys/staging_ed25519
```

## Auth precedence (client side)

For each connection, marvel offers auth methods in this order, stopping
at the first that authenticates:

1. **`--identity <path>` flag** (per-invocation override)
2. **Cluster `identity` field** in `~/.marvel/config.yaml`
3. **`~/.marvel/keys/client_ed25519`** (if it exists)
4. **`SSH_AUTH_SOCK`** agent
5. **`~/.ssh/id_ed25519`**, **`~/.ssh/id_rsa`**

## Why a dedicated marvel key?

Most developers already have `~/.ssh/id_ed25519`. Using it for marvel
works, but reusing the same key for GitHub, servers, and marvel means
a compromised daemon could correlate activity across all three. A
dedicated `~/.marvel/keys/client_ed25519`:

- limits the blast radius if a daemon machine is compromised
- can be revoked independently without rotating your main SSH identity
- is created with correct permissions automatically
- gets permission-repaired with `marvel keys doctor`

You can still use your existing `~/.ssh/id_ed25519` — just run
`marvel config add-cluster ... --no-default-identity` (and authorize
that pubkey on the daemon).

## Troubleshooting

### `permissions 644 for "..." are too open`

A private key file is group- or world-readable.

```bash
marvel keys doctor --fix
```

### `handshake failed: ssh: unable to authenticate, attempted methods [none publickey]`

The daemon does not recognize your public key. Ask the admin:

```bash
marvel keys show                                   # client machine
# send output
marvel keys authorize /path/to/you.pub             # daemon machine
```

Or verify which keys marvel is trying with:

```bash
marvel --identity ~/.marvel/keys/client_ed25519 --cluster prod get sessions
```

### `no SSH auth available`

No private key and no agent. Either:

```bash
marvel keys generate                   # create a marvel client key
# or
eval $(ssh-agent) && ssh-add ~/.ssh/id_ed25519
```

### Daemon: `unknown key for user`

The client presented a pubkey that isn't in `authorized_keys`. Check
the fingerprint in the daemon log against `marvel keys authorized`.

## See also

- [Admin guide](admin-guide.md) — daemon setup and deployment scenarios
- [User guide](user-guide.md) — everyday commands
- [Architecture](architecture.md) — transport layers and design
