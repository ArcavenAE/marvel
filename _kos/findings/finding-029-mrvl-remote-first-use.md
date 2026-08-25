# finding-029 — mrvl:// remote first-use: fail-closed auth behind an all-interfaces bind, with a circular trust handshake

Probe: recon task [C], commissioned in
[ArcavenAE/aae-orc#243](https://github.com/ArcavenAE/aae-orc/issues/243).
Operator: Claude (Opus 5), no human at the keyboard.
Date: 2026-08-25 (16:17–16:22Z). **Spend: $0.00.** Setup only — no agents, no
team, no workload.

Binary: **`marvel 0.1.0-alpha.20260823.211648.2f76ccf`** (brew-installed,
notarized). Operator directive: prefer the signed install; the tree build is
unsigned.

**Measurement only.** No code changed, no fix proposed. The comms layer is
the conductor's.

Related: finding-027 (truth table), finding-028 (event-ring inventory),
run-01b (local first-use, which tested the Unix socket only).

---

## 1. Pre-state — the three surfaces were genuinely virgin

Captured before touching anything:

```
~/.marvel/log/daemon.log          <- run-01b residue, recorded and left
~/.marvel/run/marvel.sock.lock
~/.marvel/state/marvel.bolt
```

- **No `keys/`** — run-01b's test keypair was removed at that teardown.
- **No `known_hosts`** — TOFU had never run on this host.
- **No `config.yaml`** — no cluster had ever been configured.
- **No `authorized_keys`**, no host key.

So every gate below fired for the first time rather than replaying recorded
state. This matters because of the method hazard finding-027 recorded: first-use
gates are one-shot per scope, and a prior run can silently disarm what a later
run measures.

## 2. The headline: `--mrvl` binds all interfaces

`marvel daemon --mrvl` with no argument:

```
$ lsof -nP -iTCP -sTCP:LISTEN | grep marvel
marvel  51098 skippy  8u  IPv6 0xc106...  TCP *:6785 (LISTEN)
```

`*:6785`, not `127.0.0.1:6785`. Verified reachable from the host's own LAN
address, which is what a third party on the same network would target:

```
$ nc -z -w2 192.168.100.196 6785
Connection to 192.168.100.196 port 6785 [tcp/dgpf-exchg] succeeded!
```

The flag's help text — *"start mrvl:// listener (use `--mrvl=:<port>` for a
custom port)"* — is about the **port**. Neither it nor `docs/keys.md` states
the bind address. `docs/keys.md` frames the daemon-side step as
`marvel daemon --mrvl  # start daemon + SSH listener`, with no interface
mention.

The consequence for a first-time operator, which is #243's stated subject: the
natural first move is *"remote administration — let me try it on this machine
first,"* and that opens an administrative port to the entire local network on
the first documented command.

**This is exposure of an authenticating port, not an open door** — §3 verifies
fail-closed three ways. The finding is that the blast radius of the default is
undocumented, not that it is unguarded.

## 3. Fail-closed, verified three ways

### 3a. Empty `authorized_keys` rejects everything

The state immediately after a first `--mrvl` start is *no `authorized_keys`
file at all*:

```
$ ls ~/.marvel/authorized_keys
No such file or directory
$ marvel keys authorized
No authorized keys. Add one with: marvel keys authorize <pubkey-file>
```

A client with a freshly generated, valid, unauthorized key is refused:

```
ssh: handshake failed: ssh: unable to authenticate,
  attempted methods [none publickey], no supported methods remain
```

**Empty is not fail-open.** Worth stating explicitly because the opposite
convention exists in the wild (empty allowlist meaning "no restrictions").

### 3b. TOFU refuses non-interactively, exactly as documented

First contact from a client with no `known_hosts`:

```
Error: ssh connect skippy@192.168.100.196:6785: ssh: handshake failed:
  host key for 192.168.100.196:6785 is not trusted
  (SHA256:jwCKLQhHORjuaqSK2+m8/gqxFkKpe4f4blyiLlUI2K8);
  run 'marvel keys trust <cluster>' to accept non-interactively
```

`docs/keys.md` promises precisely this: *"Non-interactive (CI, pipelines):
marvel refuses and tells you to run `marvel keys trust <cluster>`."* The error
names the fingerprint and the remedy. **The docs called this correctly and the
behavior is right for an unattended operator** — it neither hangs on a prompt
nor silently accepts.

### 3c. Host trust does not confer client authorization

Generated a second keypair (`intruder_ed25519`), with the host key **already
trusted** in `known_hosts`, and connected:

```
$ marvel --cluster lan --identity ~/.marvel/keys/intruder_ed25519 get sessions
Error: ... ssh: unable to authenticate, attempted methods [none publickey]
```

Rejected. The two trust directions are properly independent: trusting the
server does not authorize the client. This is the specific question #243 asked
about a second identity, and the answer is that it is stopped at the door.

### Daemon-side logging is honest about rejections

```
11:20:26 ssh handshake failed: [ssh: no auth passed yet, unknown key for user skippy: SHA256:kh+n...]
11:20:35 ssh: client connected: skippy@192.168.100.196 (SHA256:kh+n...)
```

Failed attempts are logged **with the offending fingerprint**, and successes
name the authenticated key. For an exposed port that is the right telemetry,
and it is more than the local socket path produces.

## 4. The circular trust handshake

`docs/keys.md` TL;DR presents the client sequence as:

```bash
marvel keys generate
marvel keys show | pbcopy                        # send pubkey to the admin
marvel config add-cluster prod mrvl://prod-host
marvel --cluster prod get sessions               # "it just works"
```

with `marvel keys trust <cluster>` as the non-interactive escape hatch.

**`keys trust` cannot run until the client is already authorized.** Measured
against a daemon that had not yet authorized my key:

```
$ marvel keys trust lan
Error: ssh connect ...: ssh: handshake failed: ssh: unable to authenticate,
  attempted methods [none publickey], no supported methods remain
```

So the connection attempt says *run `keys trust`*, and `keys trust` fails for
lack of authorization. **Two commands each pointing at the other.**

The way out is daemon-side authorization first, after which trust succeeds:

```
$ marvel keys authorize ~/.marvel/keys/client_ed25519.pub
authorized key added: SHA256:kh+n... 
$ marvel keys trust lan
Host key for mrvl://192.168.100.196:6785 trusted and recorded.
$ marvel --cluster lan get sessions
WORKSPACE  TEAM  ROLE  GEN  AGENT NAME  STATE  ...        # works
```

On one host, where I am both admin and client, this cost about a minute. **On a
real two-machine setup it is a genuine blocker in the direction that matters:**
a client cannot pre-trust a host key before the admin authorizes them, so the
documented client-side-first ordering is unachievable, and the error message
sends them to a command that cannot yet work.

Two observations that bound the fix, neither of them a proposal:

- The docs' ordering is the cheap part to correct — daemon-side `authorize`
  belongs before client-side `trust` in the TL;DR.
- `keys trust` demonstrably *has* the host key before authorization completes,
  because the earlier connection error printed its fingerprint. Whether
  recording it at that point is acceptable is a security question (it is the
  moment TOFU is meant to be a decision), not a mechanical one, so it belongs
  with whoever owns the trust model.

## 5. Authorization is binary, and it is total

`internal/daemon/sshserver.go:50`:

```go
config := &ssh.ServerConfig{PublicKeyCallback: s.authorizeKey}
```

`authorizeKey` (`:147`) loads `~/.marvel/authorized_keys` and tests
membership. That is the whole authorization model: **no scopes, no roles, no
read-only mode, no per-method checks.**

The RPC surface that one check gates (from `internal/daemon/daemon.go`):

```
apply  budgets  capture  delete  describe  endpoint  endpoints  events
get  heartbeat  inject  logs  orphans  policies  reap  reexec  run
scale  session  sessions  shift  stop  team  teams  workspace  workspaces
```

26 methods. Confirmed mutating over the wire rather than inferring it —
`marvel --cluster lan reap` executed and answered (`Nothing to reap: every
marvel tmux session is in the daemon's records`).

So **any authorized key can `inject` keystrokes into any agent's pane, `stop`
the daemon, `delete` resources, `scale`, `shift`, and `reexec` the binary.**
The README calls `inject` "executive privilege"; over `mrvl://` that privilege
has exactly one gate, and it is the same gate as `get sessions`.

This is a coherent v1 posture for a single operator, and marvel's own roadmap
places multi-user work at M1 (supervisor rights + principal model), so it is
not an oversight. It is recorded because #243 asked what a second identity
hits: a binary gate, and once through it, administrator.

## 6. Key material and permissions — the documented table is accurate

| path | mode | notes |
|---|---|---|
| `~/.marvel/` | `0700` | |
| `~/.marvel/keys/` | `0700` | |
| `keys/client_ed25519` | `0600` | **unencrypted**: cipher `none`, kdf `none` |
| `keys/client_ed25519.pub` | `0644` | |
| `ssh_host_ed25519_key` | `0600` | auto-created on first `--mrvl` |
| `ssh_host_ed25519_key.pub` | `0644` | |
| `authorized_keys` | `0600` | created by first `keys authorize` |
| `known_hosts` | `0644` | OpenSSH format, created by `keys trust` |

Every mode matches `docs/keys.md`'s table exactly. That document is accurate
and unusually complete — including the `0600`-enforcement behavior it shares
with OpenSSH and the exact `chmod` remedy in its error text.

`config.yaml` after `add-cluster`:

```yaml
clusters:
    - name: local
    - name: lan
      server: mrvl://192.168.100.196:6785
      identity: /Users/skippy/.marvel/keys/client_ed25519
current_cluster: local
```

Note `local` is synthesized (no `server`), and `add-cluster` did **not** switch
`current_cluster` — a deliberate-looking choice that means adding a cluster
cannot hijack subsequent unqualified commands.

### On the unencrypted private key

run-01b measured this for the local case; it holds for the remote case, and
`docs/keys.md` is upfront about the `0600` model. The composition worth naming
for the **network** case specifically: with `--mrvl` bound to all interfaces
(§2) and authorization binary and total (§5), an unencrypted client key at
rest is a single file whose compromise yields full administrative control of
the fleet from anywhere on the network.

Each element is a defensible choice on its own — unencrypted keys are what make
the path headless (finding from run-01b), the all-interfaces bind is what makes
remote administration work at all, and binary auth is a reasonable v1. The
composition is what deserves a stated position rather than three independent
defaults.

## 7. What did NOT assume a human

For #243's first-use-gate question, the negative results:

- `marvel keys generate` — works with stdin closed (`< /dev/null`), no
  passphrase prompt. Re-confirmed from run-01b.
- `marvel keys generate --name X --comment Y` — same, no prompt.
- `marvel keys authorize <pubkey>` — no prompt, logs the added fingerprint.
- `marvel config add-cluster` — no prompt, prints the resulting binding.
- `marvel keys trust <cluster>` — no prompt (once authorized, per §4).
- TOFU — **refuses rather than prompting** when non-interactive, which is the
  correct headless behavior and the opposite of a blocker.
- `marvel daemon --mrvl` — no prompt, auto-creates the host key.

**Nothing on the remote path assumed an interactive human.** The predicted
failure class for this campaign does not appear here either, which now holds
across the local install path (run-01b) and the remote path (this run).

## 8. Method notes

- Ground truth from `lsof`, `nc`, `ls -la`, and the daemon's own log, never
  marvel's rolled-up reporting.
- The second-identity test used a real second keypair rather than reasoning
  about the code path.
- Authorization granularity was confirmed by executing a **mutating** verb over
  the wire (`reap`, chosen because it is destructive-capable but was a no-op
  against a clean daemon), not by reading `sshserver.go` alone.
- **The listener was taken down before writeup**, verified closed both by
  `lsof` (`port 6785: closed`) and by re-running the LAN reachability probe (`no
  longer reachable`). An administrative port bound to all interfaces is not
  residue to leave running while writing.

## 9. Teardown

`marvel stop --teardown`, then removed all test key material:

- `keys/` (both keypairs — unencrypted private keys existing only as test
  residue)
- `authorized_keys` (a standing grant to a key I had just deleted)
- `known_hosts`, `config.yaml`, `ssh_host_ed25519_key{,.pub}`

`~/.marvel/` is back to its pre-task baseline (`log/`, `run/`, `state/` from
run-01b, recorded and deliberately left). No daemons, no marvel tmux servers.

Recorded rather than silently cleaned, because the *contents* of those files
were the evidence: fingerprints, the cluster binding, and the mode bits are all
transcribed above before deletion.

## 10. Out of scope

No fix, no patch, no proposed bind-address default, and no comms-layer
proposal. §2 and §4 name doc gaps and §5 names a v1 posture; all three are
observations, and the trust-model question in §4 is explicitly left to its
owner.

Per the #242 close-out ruling, **no run-registry line is owed** until critic
E1 ships.
