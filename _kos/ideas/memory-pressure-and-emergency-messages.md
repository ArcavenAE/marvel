# Memory pressure, and the emergency messages marvel sends to protect the work

- **Status:** idea (pre-hypothesis, no commitment). No frontier node is filed;
  one candidate question in section 7 is crisp enough to probe if the operator
  wants it.
- **Date:** 2026-09-24
- **Origin:** operator, relayed by the director: "could we suspend, pause, or
  kill sessions for OOM? could we detect runaway memory? what could be done to
  protect the cluster and the team? what emergency messages might we want to
  design marvel to communicate to the agents under different situations, with a
  mind to protecting the work?"
- **Extends, does not duplicate:** `elem-agentic-resource-matrix` (compute is
  one of 17 rows; this idea is that row plus durable state and human
  attention), finding-009 (admission; `max_team_rss_bytes` is registered and
  refused at parse, not enforced), `internal/procstat` (wave-1 per-session
  CPU and RSS over the pane's process tree), `health-signal-taxonomy.md`
  (RSS is listed as an OS signal, with no action attached),
  `question-healthchecks`, `question-shifts`, `question-shift-triggers`,
  `shift-change-succession-protocol.md`, and crash-loop backoff
  (`noteCrashAndBackoff`). Orc parent frame:
  `_kos/ideas/marvel-agentic-resource-matrix.md`.

## 0. The one-sentence version

Marvel can measure memory today and does nothing with it. The gap is not
the actuator (it can already delete and shift sessions); it is a policy that
picks the gentlest action that still protects the host, and a message
vocabulary that gives the agent one turn to make its work durable before
marvel acts.

## 1. Evidence from today (2026-09-24)

- **Shift is not yet a safe remedy.** On one team, seven of eight role shifts
  drained nothing and left two seats for a one-replica role. The cleanup then
  removed one at random; four times it removed the seat the shift had just
  created (#345, #348). Any policy below that says "shift" depends on those two
  fixes first.
- **An idle session holding memory.** The director's roll call reported a
  session idle for 578 minutes holding 568 MB. That is not runaway growth
  (flat, no activity). It is a separate class, an idle hoarder, and the right
  action is different (section 2).
- **Orphaned state accumulates.** 185 durable consumers on the director bus,
  one per shim start, none reaped. Memory policy must not repeat the pattern
  by leaving suspended or half-killed sessions behind.
- **The host was at 98 percent disk** on its data volume. Disk is a sibling
  resource with the same shape: detect, warn the agents, refuse new work,
  then act.
- **Most of the host's largest resident processes were not agents** (file
  system indexing, media analysis, a container VM, browsers). Host pressure
  does not mean an agent caused it, and marvel may only act on its own
  sessions.

## 2. Detection

**Per session.** `procstat` already walks the pane's process tree. That is
the right unit, because a harness is a tree: the harness itself plus MCP
children (`director-mcp`, codex helpers, language servers). Sum the tree, and
keep the per-child breakdown, because a leaking MCP child is a different fix
from a leaking harness.

- RSS double-counts shared pages. On macOS the physical footprint (what
  Activity Monitor calls Memory) is the better figure. Which call gives it per
  pid without elevated rights is to be verified.
- **Runaway versus big work.** A growth slope over a window, read beside
  activity signals marvel already has (agent events, tool calls, CTX%):
  - growth with activity and rising context is plausibly legitimate
  - growth with no activity is the runaway signature
  - flat and high with no activity is the idle hoarder
  - a sudden step with a new child process points at the child

**Per host.**

- macOS: the kernel's pressure level (`sysctl kern.memorystatus_vm_pressure_level`:
  normal, warn, critical), `memory_pressure`, and `vm_stat` for compressor and
  swap activity. A dispatch memory-pressure source can deliver the transition
  as an event rather than a poll. macOS jetsam will kill processes under
  critical pressure whatever marvel does, so marvel's job is to act first and
  choose the victim itself.
- Linux: PSI (`/proc/pressure/memory`, the `some` and `full` averages) and
  cgroup v2 `memory.events` (`high`, `max`, `oom`, `oom_kill`). If marvel ever
  places sessions in cgroups, `memory.high` is a gentle actuator: it throttles
  and reclaims without killing.

All of this is a vital sign under ADR-007: it informs and triggers
confirm-or-override policy. It is not a merge gate.

## 3. Actions, gentlest first

| action | what it does | preserved | lost or at risk |
|---|---|---|---|
| warn | emergency message (section 5), no state change | everything | nothing |
| request checkpoint | the agent commits or pushes WIP and writes a handoff note | everything, now durable | one turn of the agent's time |
| pause intake | stop dispatching to the seat; director holds its mail | the session and its context | throughput |
| throttle (Linux cgroup `memory.high`) | reclaim under a soft cap | the session | speed; can thrash if the cap is too low |
| suspend (SIGSTOP, later SIGCONT) | freezes the tree | the process and its context in memory | an in-flight model call (the connection times out server-side, so the harness sees a broken stream on resume), heartbeats and presence (they lapse, so the roster must show "suspended", not "dead"), and bus polls. RSS stays resident; a suspend stops growth but frees memory only if the OS compresses or swaps it out |
| shift with handoff | successor on a clean context, predecessor drained | the durable work, if the checkpoint ran | the conversation's working memory; depends on #345 and #348 |
| kill | delete the session; the reconcile respawns one | only what was already durable | everything in the context |

Two notes:

- **Signal the process group, not the pid**, or the harness stops while its
  MCP children keep running, or the reverse.
- **A suspended seat is state marvel must track and resume.** The 185 orphan
  durables are the warning: anything marvel suspends needs a record, a
  timeout, and a resume or kill decision, so nothing is left frozen.

## 4. Protecting the cluster and the team

- **Admission under pressure.** Enforce `max_team_rss_bytes` (already
  registered in finding-009) and add a host-level gate. Under warn pressure,
  refuse new spawns and ad-hoc `marvel run`. Under critical pressure, refuse
  restarts too, except for a supervisor.
- **Priority classes.** Supervisors are touched last, and a team never loses
  its last supervisor to memory policy. Order the victims:
  1. idle hoarders that have already checkpointed
  2. ephemeral seats before long-term seats
  3. workers before leads
- **One action at a time per team.** The back-to-back shifts today are the
  failure mode: serialize memory actions per team, and wait for each one to
  settle before the next.
- **Never act on what marvel does not own.** When the pressure comes from
  non-agent processes, marvel refuses new work and warns its agents, and it
  tells the operator which non-marvel processes lead the host, but it does not
  touch them.

## 5. Emergency message vocabulary

Four severities:

- **NOTICE:** informational, no deadline.
- **WARN:** act within a turn or two.
- **CRITICAL:** act on your next turn.
- **FINAL:** marvel will act at the stated deadline whether or not you reply.

Every message names the situation, the required action, the deadline, and how
to acknowledge. The body stays short, with the one load-bearing token (the
deadline or the action) last, because injected text loses its head (R-111
director-side).

| situation | severity | the agent's next turn | if no acknowledgement by the deadline |
|---|---|---|---|
| memory pressure | WARN | stop starting new work; finish or checkpoint the current step | escalate to CRITICAL |
| memory pressure | CRITICAL | commit and push WIP to a branch; write a handoff note; acknowledge with the commit and note paths | pause intake; escalate to the supervisor |
| shift imminent | CRITICAL | checkpoint and handoff note; acknowledge | shift at the deadline (only after #345 and #348) |
| kill imminent | FINAL | commit and push anything uncommitted; record in-flight state in bd | kill at the deadline |
| host going down | FINAL | same as kill imminent; also stop any external side effect mid-flight | the host goes down anyway; marvel records who acknowledged |
| bus or broker lost | WARN | keep working; do not assume messages were sent; record outbound asks in bd (the durable return channel, R-99 director-side) | nothing; the notice is informational until the bus returns |
| credential revoked | CRITICAL | stop every action that uses it; do not retry or route around it | none needed; the credential no longer works |
| disk low | WARN, CRITICAL at a higher threshold | stop writing large artifacts; clean the session's own scratch; commit what matters | refuse new spawns; escalate to the operator |

**Acknowledgement** is a structured reply: an AGREE with `in_reply_to` on the
bus, or a marker the harness hook writes. It carries what the agent did (a
commit sha, a handoff path, or "nothing to save"), so the operator can check
the acknowledgement rather than trust it. A silent seat is not assumed
compliant: at the deadline marvel takes the stated default and emits an
event naming the seat that did not answer.

## 6. Delivery when the thing failing is the bus

In order of preference:

1. **The bus, on the seat's own address.** The director leaf-fabric design
   (director#77) gives every seat one address from any cluster, and a durable
   inbox. This is the normal path.
2. **A harness hook.** marvel already projects settings at spawn. A hook that
   reads a per-session emergency file on each tool call or prompt reaches the
   agent without the bus and without depending on the composer's state.
   Candidate only; whether each harness has such a hook, and how often it
   fires, is to be verified.
3. **Pane inject**, the fallback: a short pointer only, sent when the composer
   is known to be clear (R-112 director-side), with the full text in the file
   from option 2.

A BUS_LOST notice can never ride the bus, so it always starts at option 2.

## 7. Candidate question and next steps

- **Crisp enough to probe:** what does SIGSTOP of a harness tree mid-stream do
  on SIGCONT? For each harness, does the session resume, retry the call,
  error, or wedge, and after how long a stop? One scratch daemon, one throwaway
  seat per harness, and stops of 10 seconds, 2 minutes, and 10 minutes. That
  answers whether "suspend" belongs in the action table at all.
- Enforce `max_team_rss_bytes` and a host gate in admission. This is the
  smallest real protection, and it needs no agent cooperation.
- Draft the vocabulary as an envelope extension beside the director's
  performatives, not a second protocol.
- Fix #345 and #348 before any policy uses shift.

## 8. Addendum, 2026-11-06: a host-capacity guard, and what is built

### 8.1 The ask

The operator, relayed by director, 2026-11-06, word for word: "kos idea marvel and we might already have part of this, but we need: some kind of resource guard and monitroing for clusters so we do not load so many active agents that we brick or crash a host and 2) didn't we design, plan or suggest adding roll up/summary metrics at the top of the marvel get sessions -w and possibly other views, for memory totals, CPU totals, spend totals, etc? what is the status of that feature(s)?"

In short: a guard that stops marvel from starting more agents than a host can carry, monitoring that would show the risk coming, and the status of the rollup totals. The second part has its own idea file, [[cluster-rollup-header]]; this addendum covers the guard and the monitoring, and section 8.6 points at the rollup.

### 8.2 The evidence since section 1

At the end of October one fleet host ran out of memory headroom. The macOS file-event daemon (fseventsd) had grown for weeks, to 27 to 29 GiB resident, mostly in swap, and it held one core for hours. Swap stood at about 52 of 53 GB and did not drain. The graphical session was lost twice in two days, which logged out the harness logins kept in the keychain. A one-off large job then pushed the host into swap exhaustion. The only warning was a low-memory panel on a screen nobody was watching. The other two fleet hosts read flat over the same weeks.

Most of that load was not marvel's, which is the section 1 point again: host pressure does not mean an agent caused it. But every seat on the host shared what headroom was left, and marvel kept starting seats into it with no reading of its own. `docs/design/host-health-guard.md` (2026-10-31) records the incident in full, with host names edited to roles.

### 8.3 What is built, checked at marvel `ba0ea659`

| piece | where | what it does, and what it does not |
|---|---|---|
| admission budgets | `internal/api/budget.go:68-69`; `internal/admission` | refuses a spawn over a team-declared `max_sessions` (count) or `max_tokens` (cumulative). The spend ceiling enforced today is in tokens, not dollars |
| unbuilt budget rows | `internal/api/budget.go:70-72` | `max_cost_usd`, `max_team_rss_bytes` and `max_session_ctx_pct` are registered with `Implemented: false`, and a manifest that declares one is refused at parse (`internal/api/manifest.go:599-618`), as the header said in September. Still true |
| per-session CPU and RSS | `internal/procstat/procstat.go:32-48`, summed over the pane's process tree; stored at `internal/daemon/metrics.go:116-117`; served as `internal/api/types.go:661-662` | the input a per-team RSS budget would read. Nothing in admission reads it |
| host free memory in each spawn event | `internal/session/hostmem.go:21`, appended at `internal/session/manager.go:1143` (#1168) | `kern.memorystatus_level` on darwin, `MemAvailable` over `MemTotal` on linux. The file says it is "a diagnostic and nothing reads it back to decide anything: no cap, no floor, no gate". No swap reading exists in marvel |
| a report-only host watch | outside marvel, in the fleet's orchestrator tooling, run by each host's scheduler | reads memory pressure, swap, the largest processes and the file-event daemon's slope, and reports to the host's supervisor. It never signals a process |
| the host vitals sampler | `docs/design/host-vitals.md` (2026-11-02) | swap, pressure, top processes and orphans, once a minute, on the existing metrics pass, off unless the cluster config turns it on. Ruled by director on 2026-11-02 to build before the rc. **Not built:** no vitals code is on main |
| the per-team load rollup | `marvel get teams --load`, `cmd/marvel/main.go:1282` | seats running of declared, and activity. It is about how busy a team is, not how full a host is |

So marvel can see each seat's memory and the host's free share at spawn time, and it refuses on two budgets. It has no rule that links the two.

### 8.4 The guard: a host-capacity floor in admission

The smallest guard that answers the ask is an admission refusal that reads the host, in the shape `host-health-guard.md` section 4.2 already set out:

- **Operator-declared, or absent.** A cluster-level floor, for example `min_free_memory_percent` and a swap ceiling, declared in the cluster config. Undeclared means no gate, as every admission budget works today.
- **Growth only.** Checked on apply and scale-up, never on a repair, a restart or a shift. This keeps admission's rule that it never refuses repair, because a host that lost a seat should get it back.
- **The reading it uses.** The #1168 free-memory reader, called at admission time and not only at spawn. The swap half needs the host vitals sampler, since marvel reads no swap today. A floor on free memory alone could ship first.
- **Hysteresis.** A band, not one line: refuse below the floor, admit again only above floor plus a margin, so a host near the line does not alternate refusals and spawns every tick.
- **What a refusal says.** An `admission.refused` event that names the reading, the floor, the time of the reading and whether it was stale, and a `get` and `describe` surface that shows a team held for host capacity. That is different from a team held on its own budget.
- **Supervisors last.** A floor never refuses a team's only supervisor, in line with section 4's priority classes.
- **Never acts on what marvel did not create.** The guard refuses new marvel seats. It does not signal anything, and it does not touch non-marvel processes, which in the October incident were most of the load.

**This is a gate on a vital sign.** Free memory is a health metric, and gating on one needs a ratified, written decision; it does not get there by accretion. `host-health-guard.md` section 8, card 1, puts that decision to the operator. Its recommendation was no floor yet: first record the reading in spawn events (now done, #1168), then decide after a week of host-watch readings shows whether a floor would have fired before the incident and stayed quiet otherwise. That recommendation was valid until 2026-11-07. Nothing in this addendum is built before that ruling.

`max_team_rss_bytes` is the per-team sibling. It is registered, the inputs exist (`procstat`), and it still needs the same kind of ruling, since RSS double-counts shared pages (section 2) and a team cap does not see non-marvel load.

### 8.5 What is missing

1. A ruling on a host-capacity floor (card 1 of `host-health-guard.md`).
2. The host vitals sampler, ruled and not built. It is the monitoring half of the ask, and it carries the swap reading a floor needs.
3. Enforcement of `max_team_rss_bytes`, or its removal from the registry if the host floor replaces it.
4. A surface for "held for host capacity", once a floor exists.
5. The emergency message vocabulary of section 5, which is still an idea. A guard that only refuses spawns gives agents no warning; section 5's WARN on memory pressure is the matching half.

### 8.6 The rollup totals

The totals line at the top of `get sessions -w` is [[cluster-rollup-header]], captured 2026-10-30 with "no design, no ticket and no frontier node". It is a view, so it informs and never gates. It and the guard would read the same per-session numbers, and a host line in that header (`host-health-guard.md` 4.1, "one host line to `marvel get sessions -w`") is where a reader would first see a host near its floor.

Re-check: the arcaven architect re-checks this addendum's guard proposal on 2026-11-13, or when card 1 is ruled, whichever comes first.
