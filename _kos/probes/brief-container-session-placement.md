# brief-container-session-placement: a marvel-managed agent session inside a local container

**Date:** 2026-09-24
**Status:** BRIEF. Not run. Running it is the operator's go.
**Question:** `question-container-session-placement` (frontier). This brief
is attached to that node; the node carries the sub-questions (a) to (h) and
the use case, and this document carries the experiment.
**Depends on:** the director leaf-fabric design brief (2026-09-24) for
everything about the bus that is not container-specific.
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

## What the code says (read at 97698a3, not run)

| fact | where |
|---|---|
| `Prepare` returns one command string, run by tmux `new-window` through `sh -c` | `internal/runtime/adapter.go`, `internal/tmux/driver.go:279` |
| `resolveCommand` returns `runtime.command` verbatim, unquoted, so a multi-word `docker run ...` is legal | `adapter.go:457` |
| The claude adapter appends `--settings`, `--permission-mode`, `--append-system-prompt`, `--session-id` after the command, so they reach a containerized `claude` as its arguments | `claude.go:88` |
| Marvel's env is applied per window with `tmux new-window -e K=V`, which is the host shell | `driver.go:287` |
| The heartbeat token and bus user/password ride that env, and a manifest may not override them | `adapter.go:317`, `roleEnvRefusedKeys` |
| Health is `remain-on-exit` plus `pane_dead` / `pane_dead_status`, read by `ReapDead` | `driver.go:217-352` |
| Metering reads the `pane_pid` process tree | `internal/procstat/procstat.go` |
| Generic reports no projection surface; a referenced policy is logged as advisory | `generic.go`, `ProjectionFor` |

## Sub-questions this probe answers

From the node, in the order the steps reach them: (a) placement, (b) health
semantics, (c) restart and shift, (e) director and NATS reach, (d)
credentials, (f) mounts and signing, (h) runtime choice. (g) image
provenance is answered on paper in the write-up, not by experiment.

## Rig (before any step)

- A scratch daemon only. Its own `--socket`, its own `MARVEL_TMUX_SOCKET`,
  its own `HOME`. Without those a second daemon takes the default socket and
  kills every `marvel-*` session it does not recognise
  (`question-daemon-isolation-boundary`). **No live team, daemon or
  manifest is touched.**
- A scratch NATS broker for steps 5 and 6, or a leaf into the hub on a
  scratch account. Not the fleet's inbox streams.
- A throwaway image. `alpine` for steps 1 to 4; an image with the harness
  CLI for step 6.
- One container runtime at a time. Start with whichever is installed;
  record which. Repeat steps 1 to 3 on a second runtime only if the first
  passes.

## Steps, smallest first

1. **Shell in a container, generic adapter.** Manifest role:
   `command = "docker run --rm -it --name $MARVEL_SESSION alpine sh"`.
   Check `marvel get sessions`, `capture`, `inject "echo hi" -e`,
   `inject "env | grep MARVEL" -e`. Expect capture and inject to work and
   the MARVEL env to be absent inside. (a)
2. **Env pass-through.** Add `-e MARVEL_SESSION -e MARVEL_ROLE ...` (names
   only, values come from the pane env). Confirm values arrive, confirm
   nothing was written into an image. (a), (d)
3. **Exit and kill, both directions.** `docker kill` the container from
   outside: does the pane die, what does `pane_dead_status` say (137 is an
   exit code, not a signal), does the restart policy re-run it, does
   `--name` collide on the rerun? Then `marvel` kill or scale-to-zero: is
   the container still running afterwards (`docker ps`)? Record every
   orphan. (b), (c)
4. **Shift.** `marvel shift` the team. Fresh container for the successor,
   predecessor's gone, no name collision? (c)
5. **Bus reach.** Run the director MCP shim (or a bare `nats` CLI publish
   and subscribe standing in for it) inside the container, with each of:
   host networking, a published port through the runtime's host alias, and
   a leaf node inside the container. For each: can it reach the broker,
   can it reach anything else (the reach limit), does presence appear once
   and only once, and after `docker kill` plus restart how many durables
   and presence rows exist (count them before and after). Bus identity
   arrives by pass-through env or a mounted file, never the image. (e)
6. **A real harness.** A claude seat through the claude adapter with
   `command = "docker run --rm -it ... image claude"`. Model auth by a
   read-only bind mount of the operator's own credential, or by the
   operator's documented helper; record which and whether it holds up
   against ADR-009. Bind-mount the policy projection directory at the same
   path so `--settings` resolves. Try `MARVEL_SOCKET` as a bind-mounted
   unix socket and record whether the runtime carries it; if not,
   `ctx-forward` heartbeats fail and that is the finding. (d), (e), (h)
7. **Analyst shape, on paper plus one check.** A container network that
   reaches only the clone ports and the broker; confirm one disallowed
   host is unreachable. Mount a legion repo; run one signed commit with the
   SSH agent socket forwarded and the hardware key touch on the host. (f)

Stop after any step that refutes the hypothesis and write the finding from
there.

## Success signal

A marvel-managed claude seat in a container is visible in
`get sessions`, healthy, capturable, injectable, present on the bus exactly
once, and restarted by marvel after `docker kill`, with no orphaned
container, no extra durable, and no credential in any image layer.

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
marked MEASURED, INFERRED or HYPOTHESIS; the node updated; one bd ticket per
marvel code change the probe shows is needed, filed flat.

## Not in scope

- Building a container field on `Runtime`. Decided after the finding.
- The fabric (addresses, retention, per-seat credentials, presence
  aggregation). That is the director leaf-fabric brief.
- Curtain. aae-orc-10x stays the record for kernel sandboxing; the finding
  says whether a container changes its priority.
