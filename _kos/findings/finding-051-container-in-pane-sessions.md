# finding-051: a containerized session runs under marvel today, but marvel orphans the container on every teardown path

**Date:** 2026-09-24
**Question:** `question-container-session-placement`
**Brief:** `_kos/probes/brief-container-session-placement.md` (steps 1 to 7)
**Rig:** scratch daemon built from 97698a3 with its own HOME, socket and tmux
server; a marvel-managed scratch broker with authorization on a scratch port;
OrbStack (Docker 29.4) on an Apple Silicon Mac; debian trixie-slim and a
throwaway image carrying Claude Code 2.1.282; director shim built from director
3350184. No live team, daemon or broker was touched. Every container, image,
network and broker the probe created was removed, and the host's container and
image lists were diffed back to their pre-probe baseline.

Claims are MEASURED unless marked INFERRED.

## Verdict

Container-in-pane (P1) works with no marvel code change. `capture` and
`inject` reach a shell and a full-screen harness TUI through the container tty,
restart after `docker kill` works, and the bus works from inside on both
network modes with results identical to a host control. The success signal
was not met in full, for three reasons, none of them a tty or bus failure:

1. **Marvel orphans the container** on `marvel kill`, `marvel delete team`,
   a shift's drain, and `marvel stop --teardown`. Killing the pane kills the
   docker CLI client, not the container.
2. **The heartbeat socket does not cross the VM.** A bind-mounted host unix
   socket is visible inside and refuses connections, so heartbeats and
   `ctx-forward` cannot reach the daemon from a container.
3. **The model login is in the macOS keychain,** so there is nothing to mount.
   The harness reaches its login screen and stops there, per the brief.

No kill criterion fired: the tty path is sound, and no bus mode needed a
credential in the image.

## Per step

| step | result | detail |
|---|---|---|
| 1 shell in a container | PASS, with two manifest-level workarounds | capture and inject work; MARVEL env absent inside (count 0). See "Manifest surface" below |
| 2 env pass-through, nothing in the image | PASS | `-e NAME` (name only) carries all eight constructed values (identity, socket path, heartbeat token, bus id, beads actor). Image Config.Env and `docker history` contain no MARVEL, DIRECTOR or NATS name. The running CONTAINER's `docker inspect` shows every passed value, including the heartbeat token |
| 3 exit and kill | PARTIAL | `docker kill`: pane closes, marvel charges a crash, restarts after the 60s backoff, `--rm` avoids a name collision. `marvel kill`: container keeps running; the restart then collided with it by name and crash-looped (2 min backoff) until a launch-time `docker rm -f` was added |
| 4 shift | PARTIAL | successor gets a fresh container (the generation is in the name, so no collision); the predecessor's container keeps running and its name is never reused, so nothing ever reaps it |
| 5 bus reach | PASS | see "Bus" below |
| 6 real harness | PARTIAL, stopped per brief | claude adapter flags reach claude inside (`--settings`, `--session-id`, `--permission-mode`, `--append-system-prompt`); TUI capture and inject work up to the login-method screen; `--settings` needed a mount of a directory the manifest cannot name; unix socket refused; login stops at the keychain |
| 7 analyst shape | PARTIAL | Docker networking is all-or-nothing (details below); SSH agent forwarding works and hands the container touch-free signing keys; no signature attempted |

## Manifest surface: what a role must write today

- **A multi-word `command` is refused.** Pre-flight runs `LookPath` on the whole
  string: `command "docker run --rm -it ...": not on PATH`. The brief's
  premise that this was legal manifest text was wrong at the manifest layer.
- **`$MARVEL_SESSION` in `args` never expands.** `buildCommand` single-quotes
  any arg containing `$`, so docker received a literal and refused the name
  (`Invalid container name ($MARVEL_SESSION)`).
- **What works:** `command = "sh"` with the run in one `-c` argument (the pane
  env expands inside it), or a wrapper script as `command`. For the claude
  adapter, the adapter's appended flags land after the script, so the `-c`
  form needs a trailing `"sh"` for `$0` and the script must end in
  `claude "$@"`. A wrapper script is cleaner and also passes pre-flight as a
  single path.
- **Interaction with #347:** once the claude adapter stops appending its
  identity prompt for wrapper commands, a containerized claude gets no
  identity prompt from marvel; the wrapper must pass one, or read the
  identity from the env it forwards.

## Health and teardown

- Health is pane-shaped and the pane is the docker CLI client. `docker kill`
  from outside is seen correctly (the client exits with the container). The
  reverse is not: tmux killing the pane leaves the container running, in five
  observed paths: `marvel kill`, `marvel delete team`, a shift's drain,
  `marvel stop --teardown` (two marvel-launched containers still running after
  it), and a restart-policy relaunch before a launch-time `docker rm -f` was
  added.
- Metering reads the pane's process tree, so RSS showed the docker client
  (about 45 MB) and never the harness inside.
- A launch-time `docker rm -f "$MARVEL_SESSION"` fixes the collision on a
  same-name restart only. It cannot reach a shift's predecessor or a deleted
  team, because nothing launches under those names again.

## Bus (step 5)

Scratch broker in marvel's managed mode: authorization loaded, one password
per applied team, the team user confined to `agent.<workspace>.<team>.>` plus
`$JS.API.>`, `$JS.ACK.>`, `$KV.AGENT_STATE.>` and `_INBOX.>`.

- **Reach.** The shim and the nats CLI connected from the host, from a
  container on host networking (`127.0.0.1` works because OrbStack's host mode
  is the Mac's loopback), and from a container on the default bridge through
  `host.docker.internal`. A NATS leaf in the container was not tried; it is
  out per the brief.
- **Subjects are credential-scoped, not network-scoped.** The three placements
  gave identical results: subscribe allowed on the team's own subjects and
  denied on another team's, another workspace's and `director.inbox`; publish
  allowed on the team subject and denied (`Permissions Violation for Publish`)
  on the others. The network mode decides reachability and nothing else.
- **Durables and presence match the host control.** Each shim start, host or
  container, adds one durable consumer that is never removed (aae-orc-iejcx)
  and one presence row. A killed shim's row outlives it until the 90s TTL
  (aae-orc-4unyk). Container: 1 then 2 durables and 1 then 2 then 1 presence
  rows across a `docker kill` and restart; host control: the same shape under
  `kill -9`. The container adds nothing beyond the control.
- **A seat needs `DIRECTOR_TEAM` and `DIRECTOR_WORKSPACE`, which marvel does not
  set.** Without them the shim registers as team `default` in workspace
  `default`, outside the team user's permissions. Not container-specific, and
  it applies to any marvel-launched seat that runs the shim.
- **The team credential can forge a teammate's presence.** `kv put` to another
  seat's presence key succeeded under the team user. Its `$JS.API.>` grant is
  also broad. Both are properties of per-team scope.

## Credential-injection verdict (the operator's per-session model)

The probe minted a per-seat user on the scratch broker by hand (standing in for
marvel at spawn): publish on the team subject, its own presence key, the
JetStream API; subscribe on its own inbox only. Injected into a fresh container
at launch through env:

- It connected, and the shim registered under it.
- It could subscribe to its own inbox and NOT to a teammate's.
- It could write its own presence key and NOT a teammate's (the put timed out
  unacknowledged, where the team credential's identical put succeeded).
- **Revocation works without restarting anything:** removing the user and
  reloading the broker closed the live connection with an authorization
  violation, and new connects failed. Marvel can revoke and re-mint it with no
  human at anyone's console, which makes it issuance under ADR-009, not
  custody.
- **Carrier used: env var,** for two reasons. It is the carrier the shim and
  marvel already share. The only file form the shim reads,
  `DIRECTOR_NATS_CREDS`, is a JWT creds file, which needs decentralized
  (operator and account JWT) authorization, and marvel renders password
  authorization. The env carrier's cost is measured: the value is in
  `docker inspect` of the container, and rotation needs a restart. A projected,
  rotatable file needs a shim that re-reads a password file on reconnect, or a
  JWT-mode broker; neither exists.
- **Marvel cannot do this from a manifest today,** and that is deliberate:
  `DIRECTOR_NATS_USER` and `_PASS` are refused as role env. Per-session minting
  is a marvel code change: one user line per session in the rendered
  authorization, instead of one per team.

Verdict: the per-session credential injected at launch works across the
container boundary, scopes a seat tighter than today's team credential, and
revokes live. The container is not what limits it. What limits it is marvel
minting per team, and the shim having no rotatable file carrier.

## Harness login and custody (step 6)

On this host the Claude Code login is a macOS keychain item, and no
credential file exists. There is nothing to bind-mount, and exporting the item
to a file would be a copy, which is the ADR-009 question itself. The sub-step
stopped at the login-method screen, and nothing was copied. The documented
alternatives (an API-key helper, a cloud provider's own credential chain) were
not exercised.

Also measured: `--session-id` names a transcript that the harness writes
inside the container's home, which `--rm` destroys. The binding
aae-orc-ca7y relies on (the transcript at a known host path) is lost unless
that directory is mounted.

## Analyst shape (step 7)

- **Network.** The default bridge reached both the broker and a public
  address. An `--internal` network blocked both. Docker has no per-destination
  allow-list, so "broker and clone ports only" needs a gateway container on
  both networks or an egress proxy (INFERRED; not built).
- **SSH agent.** OrbStack's forwarded agent socket gave the container the
  operator's full agent: `ssh-add -l` listed three keys, one hardware-backed
  and two plain ED25519 keys, and plain keys sign without any touch.
  Forwarding the agent therefore hands a container signing authority as the
  operator, and it should not be done for analyst seats. Ephemeral legion
  repos have no remote and need no signing. A long-term seat that commits
  memory needs a key scoped to that seat. No signature was attempted.

## What this needs from marvel (candidates, not yet filed)

1. Container lifecycle: stop and remove the container when marvel kills the
   pane (kill, delete, drain, teardown). Either a runtime-level teardown hook,
   or a container-aware Instance (P2 in the node).
2. Per-session bus credentials minted at spawn, replacing per-team.
3. Set `DIRECTOR_TEAM` and `DIRECTOR_WORKSPACE` in the constructed env beside
   `DIRECTOR_AGENT_ID`.
4. A heartbeat path that is not a host unix socket (a TCP listener scoped to the
   session, or the bus itself), if container seats are to heartbeat.

## Not measured

- Podman, Apple `container`, colima and Docker Desktop: only OrbStack was used.
- A NATS leaf in the container (out per the brief), tmux-in-container (P2),
  image provenance (sub-question g, a paper answer), and a signed commit
  inside a container.
