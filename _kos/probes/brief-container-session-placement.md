# brief-container-session-placement: a marvel-managed agent session inside a local container

**Date:** 2026-09-24 (review of #346 and the operator's credential model
folded in the same day)
**Status:** RUN 2026-09-24 on the scratch rig (operator go). Results:
finding-051. Not edited after the run.
**Question:** `question-container-session-placement` (frontier). This brief
is attached to that node; the node carries the sub-questions (a) to (h) and
the use case, and this document carries the experiment.
**Fabric:** the director leaf-fabric design, ArcavenAE/director#77
(`sim/design/leaf-fabric-one-address-space.md`), a CANDIDATE and unmerged.
This probe does not wait on it. It runs against the current bus and records
each bus result under both models: what it means today, and what it would
mean under the fabric.
**Prior:** `question-managed-runtime-placement` and finding-021 (the cloud
arm: Instance is the seam for an API-driven session),
`question-daemon-isolation-boundary` (why the rig needs its own socket and
tmux server), `question-permission-model` (environment construction is
enforcement locus 1).

## Why now

A SOC-analyst evaluation team is about to run ephemeral analyst seats that
set their software up from nothing each run, next to two long-term analyst
seats. Their requirements (a clean room per run, three MCP tools and no
more, no live credentials, reach limited to the vendor clones and the
supervisor) are containment requirements. Curtain is design-only. A
container is the commodity containment available today, and nobody has
checked whether marvel can supervise a session inside one.

## Hypothesis

The generic adapter (and the claude adapter, through its `command`
override) can host a containerized session today with no marvel code
change, by placing the container inside the pane (P1: `docker run -it` as
the pane command). `capture`, `inject` and pane-alive health work
unchanged. What breaks is everything marvel hands the harness as a host env
var or a host path: identity and bus env, the heartbeat socket, the
projected settings file, the stream FIFO. Each of those needs a
pass-through or a bind mount. The director shim's reach to the broker is
the hardest part, because it is a network-namespace question rather than a
mount.

What would refute it: `capture` or `inject` misbehaving through the
container tty, or a restart policy unable to re-run the container cleanly
without marvel code.

## Premises, stated as facts (so nobody reads this as testing what exists)

| fact | source |
|---|---|
| The fleet's phase-0 broker runs with NO authorization today; there is no per-session NATS credential anywhere in the live fleet | director#77 ground truth |
| The hub leaf link authenticates with an nkey held by the broker process, not by any session | director#77 |
| Marvel in `managed` bus mode renders `authorization.conf`, mints an admin password and one password PER TEAM, and injects `DIRECTOR_NATS_USER` / `DIRECTOR_NATS_PASS` into every session of that team through the constructed env | `internal/bus/manager.go`, `internal/runtime/adapter.go:317` |
| A role may not override those two variables or the heartbeat token | `roleEnvRefusedKeys` |
| The director shim accepts either `DIRECTOR_NATS_USER`/`_PASS` from env or `DIRECTOR_NATS_CREDS`, a path to a NATS creds file (JWT plus seed) | director `probe/nats-phase-0/director-mcp/bus.go:75` |
| The shim creates one durable consumer per shim instance on `AGENT_INBOX` and writes a presence key in `AGENT_STATE`; it expects both objects to be provisioned already | same file, lines 113 and 168 |

So a PER-SESSION credential does not exist. The probe mints one itself, on a
scratch broker with authorization enabled.

## The credential model under test (operator direction, 2026-09-24)

Per session, at spawn: marvel mints a short-lived, revocable, per-seat bus
credential when it spawns the session and injects it into the container at
launch. This is twelve-factor config from the environment, upshifted the way
a workload identity platform does it: minted per workload, short TTL,
rotatable, never in the image.

Two carriers, compared:

- **Env var.** Visible to `docker inspect` (Config.Env of the container) and
  to anything reading the process environment; cannot rotate without a
  restart. Needs no new shim support.
- **Projected file.** A mounted file the minting side can rewrite (the
  projected service-account token pattern). Rotatable in place; not in
  `docker inspect`. The shim's only file form is a JWT creds file, which
  needs decentralized (operator/account JWT) authorization on the broker.

The probe records which carrier it used and why.

Custody: this is issuance, not custody (SOUL section 3, ADR-009). Marvel can
revoke and re-mint without a human at anyone's console. A NATS leaf node
inside the container is different: a leaf credential is a cluster
credential, and putting one in a container is custody of a credential the
session does not need. That option is out (review item 1).

## What the code says (read at 97698a3, not run)

| fact | where |
|---|---|
| `Prepare` returns one command string, run by tmux `new-window` through `sh -c` | `internal/runtime/adapter.go`, `internal/tmux/driver.go:279` |
| `resolveCommand` returns `runtime.command` verbatim, unquoted, so a multi-word `docker run ...` is legal | `adapter.go:457` |
| The claude adapter appends `--settings`, `--permission-mode`, `--append-system-prompt`, `--session-id` after the command, so they reach a containerized `claude` as its arguments (the prompt flag stops being appended for a wrapper once #347 lands) | `claude.go:88` |
| Marvel's env is applied per window with `tmux new-window -e K=V`, which is the host shell | `driver.go:287` |
| Health is `remain-on-exit` plus `pane_dead` / `pane_dead_status`, read by `ReapDead` | `driver.go:217-352` |
| Metering reads the `pane_pid` process tree | `internal/procstat/procstat.go` |
| Generic reports no projection surface; a referenced policy is logged as advisory | `generic.go`, `ProjectionFor` |

## Rig (before any step)

- A scratch daemon only. Its own `--socket`, its own `MARVEL_TMUX_SOCKET`,
  its own `HOME`. Without those a second daemon takes the default socket and
  kills every `marvel-*` session it does not recognise
  (`question-daemon-isolation-boundary`). **No live team, daemon, broker or
  manifest is touched.**
- Scratch brokers only, on ports the fleet does not use, each with its own
  store directory, and each with an authorization block. No account is added
  to the live hub; nothing reloads a shared broker (review item 7).
- A throwaway image. `alpine` for steps 1 to 4; an image with the harness
  CLI for step 6.
- One container runtime at a time; record which.

## Steps, smallest first

1. **Shell in a container, generic adapter.** Manifest role:
   `command = "docker run --rm -it --name $MARVEL_SESSION alpine sh"`.
   Check `marvel get sessions`, `capture`, `inject "echo hi" -e`,
   `inject "env | grep MARVEL" -e`. Expect capture and inject to work and
   the MARVEL env to be absent inside. (a)
2. **Env pass-through, and nothing in the image.** Add
   `-e MARVEL_SESSION -e MARVEL_ROLE ...` (names only; values come from the
   pane env). Confirm values arrive. Then run `docker image inspect`
   (Config.Env) and `docker history --no-trunc` on the image and confirm no
   `MARVEL_`, `DIRECTOR_` or `NATS_` name appears (review item 6); record
   what `docker inspect` of the running CONTAINER shows, since that is where
   an env carrier is visible. (a), (d)
3. **Exit and kill, both directions.** `docker kill` from outside: does the
   pane die, what does `pane_dead_status` say (137 is an exit code, not a
   signal), does the restart policy re-run it, does `--name` collide on the
   rerun? Then marvel kill or scale-to-zero: is the container still running
   afterwards (`docker ps`)? Record every orphan. (b), (c)
4. **Shift.** `marvel shift` the team. Fresh container for the successor,
   predecessor's gone, no name collision? (c)
5. **Bus reach, against an authorizing broker.** Scratch broker with
   authorization; a per-seat user minted for this seat with the seat's
   publish and subscribe permissions. First a CONTROL: run the shim (or the
   `nats` CLI standing in for it) on the host, kill and restart it, and
   count durables and presence rows. Then the same inside the container
   under two network modes: host networking and a published port reached
   through the runtime's host alias. For each record: broker reachable;
   which subjects the seat can publish and subscribe to from inside (not
   only which hosts it reaches, review item 2); durable and presence counts
   after kill and restart, passing on "no more than the control" (review
   item 4). The credential arrives by the carrier chosen above, never the
   image. (e)
6. **A real harness.** A claude seat through the claude adapter with
   `command = "docker run --rm -it ... image claude"`. Record where the
   harness keeps its model login on this host (review item 5). If it is a
   file: does a read-only mount break its refresh, does a writable mount let
   the container rewrite the operator's credential. If it is the keychain:
   there is no file to mount, exporting one is a copy, and that is the
   ADR-009 question itself; record it and stop the sub-step. Do not copy
   the credential to make the step pass. Also bind-mount the policy
   projection directory at the same path so `--settings` resolves, and try
   `MARVEL_SOCKET` as a bind-mounted unix socket. (d), (e), (h)
7. **Analyst shape.** A container network that reaches only the clone ports
   and the broker; confirm one disallowed host is unreachable. Mount a
   legion-style repo; one commit inside with the SSH agent socket forwarded,
   signing on the host key. (f)

Stop after any step that refutes the hypothesis and write the finding from
there.

## Durables: bound to the address, not the container

Under the fabric an inbox is a work-queue stream with one durable per
address: a restart rebinds the durable, and a second claimant is refused. So
a long-term seat's durable must outlive any single container, and an
ephemeral seat, which gets a new address each run, has its durable removed
at unapply after its pending mail is drained or reported. Today's shim
instead makes a new durable per instance (aae-orc-iejcx); step 5 measures
that behaviour and does not treat it as a container defect.

## Success signal

A marvel-managed claude seat in a container is visible in
`get sessions`, healthy, capturable, injectable, present on the bus, and
restarted by marvel after `docker kill`, with no orphaned container, no
more durables or presence rows than the host control, and no credential in
any image layer.

## Kill criteria

- `capture` or `inject` is unreliable through the container tty on the
  first runtime tried and on a second. P1 is then dead, and P2 (a second
  Instance implementation) is the remaining path; write that up and stop.
- Bus reach needs a credential in the image on every network mode tried.
  That fails SOUL section 3 and stops the probe, whatever else works.
- Timebox exceeded.

## Timebox

Steps 1 to 4: two hours. Steps 5 to 7: four hours. One working day in
total, including the finding.

## Output

A finding in `marvel/_kos/findings/` answering (a) to (h) with each claim
marked MEASURED, INFERRED or HYPOTHESIS; the node harvested; one bd ticket
per marvel code change the probe shows is needed, filed flat.

## Not in scope

- Building a container field on `Runtime`. Decided after the finding.
- The fabric (addresses, retention, per-seat credentials, presence
  aggregation). That is director#77.
- Curtain. aae-orc-10x stays the record for kernel sandboxing; the finding
  says whether a container changes its priority.
