# finding-marvel-l1bh: replicas 0 parks a never-used seat cleanly, if the manifest says so

Date: 2026-10-06
Status: frontier
Brief: `_kos/probes/brief-replica-zero-never-used.md`

## Why this matters

A seat that has never taken a turn still holds a harness process, its MCP children and a pane. On one cluster on 2026-10-06, four interactive seats had been up for about four and a half hours with no turn at all, at about 430 MB RSS each. Scaling three of them to 0 freed about 1.3 GB, and nothing they carried was lost. This finding says how to spot such a seat, what replicas 0 keeps and drops, and who should act.

Tags: **MEASURED** (a command run on the cluster or on a scratch daemon), **CODE** (read at `origin/main`), **INFERRED** (reasoning, not observed).

## 1. Telling never used from idle

- **MEASURED:** on all four never-used seats, `describe session` shows `LastHeartbeat` and `ContextAt` at the zero time, and the CTX% and LLM columns read `-`. The harness wrote no transcript file for the session id marvel passed it (`--session-id`): none exists under the harness's projects directory for any of the four.
- **MEASURED (positive control):** a used seat that was idle at the time has a transcript with thousands of records and a heartbeat time.
- So the signal is: a statusline-fed seat (`runtime.context_feed = "statusline"`) whose heartbeat has never arrived since spawn. **INFERRED:** it is reliable only on statusline-fed roles. A role with no context feed always reads `-`, used or not, and a headless or codex role has other channels. The transcript check is the harness-side confirmation, but marvel does not read transcripts today.

## 2. What replicas 0 keeps and drops

- **MEASURED (scratch daemon, isolated socket, home and tmux server, a `sleep` runtime):** scaling a role from 1 to 0 to 1 returned the same session name at the same generation. The name is `<team>-<role>-g<generation>-<index>`; the generation changes only on a shift (CODE `internal/team/controller.go:1862`), and the index restarts at 0 when no session of that generation exists (`nextIndex`, `controller.go:2441-2453`). So the seat's address comes back unchanged.
- **MEASURED (scratch):** a `marvel work` re-apply after a scale to 0 restored the manifest's count, 1. `marvel scale` changes only the live team's replicas (CODE `internal/daemon/daemon.go:1692-1700`), and an apply resets the roles from the manifest (`internal/api/manifest.go:868`). **So a lasting 0 belongs in the manifest; a bare scale lasts only until the next apply.**
- **MEASURED (live, read only):** the three seats scaled to 0 left the director roster, and their harness and shim processes exited.
- **CODE (director shim):**
  Read at ArcavenAE/director `main`, under `probe/nats-phase-0/director-mcp/`:
  - presence lapses about 90 s after the last beat (`askledger.go:22`).
  - Mail sent to a seat at 0 waits on the inbox stream, which keeps 72 h (`bus.go:71-76`).
  - A new instance with the same agent id resumes after the departed instance's last acknowledged message (`bus.go:195-238`).
  - **INFERRED** from those three: mail sent while a seat is at 0 is delivered on scale-up within 72 h, and lost after that.
  - **Not tested here.** The brief planned one message sent at 0 on the scratch run. It was not sent, and the reason is the scratch rig, not the live bus: the scratch daemon ran a `sleep` runtime with no director shim and no message bus, so there was nothing to receive it. Testing it needs a scratch bus server and a shim-backed seat, which this probe did not build. A message to a live seat at 0 was ruled out because it writes to the live bus.

## 3. Who acts

- Today it is an operator act: a manifest edit to `replicas = 0` plus an apply, or a scale followed by the same edit. On 2026-10-06 the director ran the live scale at the operator's word, with backups of the manifests (reported by the team supervisor).
- **Audit gap, MEASURED:** the live scale to 0 left three `session.deleted` lines in the event ring and the daemon log, and no scale event and no request line. CODE: `handleScale` emits no event (`internal/daemon/daemon.go:1614-1713`). marvel cannot say afterwards who scaled a role to 0 or why.
- **Recommendation (SOUL section 8, marvel proposes and does not judge):**
  - emit a `team.scaled` event naming the role and the old and new counts;
  - surface "never used" as a proposal, either a marker in `get sessions` or a once-per-change event for a statusline-fed seat with no heartbeat some time after spawn.

  An automatic scale-to-zero with wake on first message is not recommended now. It would judge for the operator, and waking on mail needs the director to tell marvel, which neither does today.

  These are proposals only; nothing here executes on silence or on a clock. Each of the three (the `team.scaled` event, the never-used proposal, and not recommending automatic scale-to-zero now) is valid until 2026-10-20, or a marvel change to `handleScale` or session liveness, whichever comes first; the architect seat that wrote this finding re-checks it then.

## 4. Interactions

- **CODE:** `marvel scale` refuses while a shift is in progress (`daemon.go:1625-1627`).
- **CODE:** a role with a `max_age` shift trigger can be scaled to 0 or 1 but not above 1 (`daemon.go:1658-1663`, marvel#452). Every one of the four roles carries a shift block, and all were at 1, so 0 is allowed.
- **INFERRED:** a pending max-age handoff request has nothing to ask while the role is at 0. Not tested.

## 5. Codex daemons (observations only, cleanup out of scope)

Codex seats spawned by marvel run with `--no-daemon`, and their only long-lived child is a code-mode host process, which belongs to the live seat. Several codex `app-server --managed-daemon` processes on the host are older than a week, and none belongs to a live marvel seat. They sit under homes from an earlier manual check, an earlier marvel instance and an earlier scratch run, plus the operator's own codex home. marvel did not start them as seats, so replicas 0 cannot reap them. The host also holds many old per-session harness-home directories, hundreds of MB in total. Both are left for a later pass, by the operator's ruling.

## Pass and fail against the brief

- Q1: pass on statusline-fed roles, 4 of 4 never used against 1 used control.
- Q2: pass for the address and the manifest (measured). Mail delivery was read from code, not measured.
- Q3 and Q4: answered from code, above.
