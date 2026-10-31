# Host health: watch, detect and guard against memory exhaustion on long-running hosts

Proposal, 2026-10-31. Code read at marvel `fa228925`. Nothing here is built.

- **Author:** the architect seat, team arcaven.
- **The operator's ask (I-70):** "we need to understand this occurance better, it seems like a critical issue that could impact other marvel instances that are long running. Please help investigating this further and ask several supervisors to orgainze their own investigations and architects to propose solutions to monitor, detect and guard against this class of event, to understand it better, determine if it is ongoing locally and on corporate or mokuzai and propose efficient, correct solutions to prevent this from contributing to instability or future re-occurances".
- **Builds on:** the read-only investigation on kinu by the arcaven supervisor seat; a draft from the research-supervisor seat on corporate (2026-10-31, summarized in the initial-release party record, `_bmad-output/party-mode/marvel-initial-release-5ptdd-2026-10-31/round-2/input-host-watch-draft.md`); and that party's outcome (`99-outcome.md` beside it), whose tickets T18 to T22 this proposal leans on.
- **Status:** for review. Three decisions are the operator's (section 8).

## 0. Why

On 2026-10-31 the busiest fleet host ran out of memory headroom. The macOS file-event daemon held 27 to 29 GiB, mostly in swap, and one full core for hours; swap stood at 52.2 of 53.2 GB; the graphical session was lost twice in two days, which logged out the harness logins that live in the keychain. Every seat on the host sat on that host. Nothing in marvel noticed, nothing said which part was marvel's, and the cleanup that followed was by hand and by pattern.

The plain point: most of the load was not marvel's, but marvel left the host with no way to tell, and marvel's own share (leaked test trees, invisible helper processes, seats that are never recycled) made it worse. The fix is mostly reporting and a few narrow guards, not a new controller.

## 1. What was checked

| fact | reading | source |
|---|---|---|
| kinu file-event daemon | 29147760 KB RSS at 98.6% CPU (20:56Z); 28680368 KB at 98.8% (21:55Z): flat for the hour, pinned at one core. A jetsam report at 17:29Z named it the largest process at 86.5 GiB resident | supervisor's read-only `ps`, `sysctl vm.swapusage`, diagnostic reports, relayed |
| kinu swap | 52223.56 of 53248 MB used, unchanged over the hour | same |
| kinu file churn, 24 h | 695 `marvel-test-home-*` dirs, 1531 `/tmp/marvel-h-*` bases, 3363 temp dirs from an unrelated tool, about 280 worktrees in the orchestrator checkout | same |
| kinu event sources | a 30 s root trace put git first, from harness-internal status polling over that checkout, then the harness and host-installed agents outside marvel | operator-run trace, mapped by the supervisor |
| kinu seats | 35 harness processes, 19.6 GB RSS; the oldest 27 to 34 days, on harness builds several weeks behind current | same |
| kinu orphans | 11 codex app-servers with parent pid 1, 17 to 35 days old, homes under `marvel-h-*` | same |
| corporate | file-event daemon 7.5 MB after 48 days; no swap in use; 339 test scratch dirs; one leaked test broker (`nats-server`, parent pid 1, up 3 days) | corporate research-supervisor draft, relayed |
| mokuzai | no reading: its supervisor had not answered by 23:00Z | |
| marvel samples per-session CPU and RSS | yes: `RunMetrics` (`internal/daemon/metrics.go`) over a process subtree (`internal/procstat`), surfaced as `CPUPercent` and `RSSBytes` (`internal/api/types.go:645-646`) | read |
| marvel has an admission gate with a ratification rule | yes: `internal/admission` refuses a spawn over a team-declared budget; "No budget declared means no gate", and the operator's manifest declaration is what ratifies it (`internal/admission/doc.go`) | read |
| marvel proves a process before signalling it | yes, for the bus broker: shipped in #799 (`docs/design/bus-pidfile-identity.md`); `internal/procargs` reads a pid's arguments | read |
| marvel sees orphans | only holders that present a stale heartbeat token (`internal/daemon/orphans.go`); a codex app-server never heartbeats, so it is invisible | read |
| marvel recycles seats by age | yes: the `max-age` shift trigger (`internal/api/types.go:800-871`, #443). The default bound in use here is 72 h | read |
| age recycling on kinu, team arcaven | 5 of 12 roles declare no shift policy: supervisor, builder, marvel-builder, architect, reviewer. Two max-age requests are open, one requested 2026-10-15T17:58Z and one 2026-10-31T15:58Z; both show `DeliveredCount: 0` and `Escalated: true` | `marvel describe team aae/arcaven` (read-only) |

## 2. What the incident is, in three layers

1. **The host.** The file-event daemon's history grew to tens of GiB and stayed; the swap does not drain while it holds that; the graphical session dropped. Corporate, which runs marvel's tests too and carries far less of everything else, shows none of it. Recovery needs root or a reboot, so it is the operator's (section 8, card 3).
2. **The harness and the checkout.** Harness sessions poll git status over an orchestrator checkout with about 280 worktrees and hundreds of untracked files. That is the leading event source in the one trace taken. Long-lived seats also run harness builds weeks old.
3. **marvel.** marvel adds churn it never cleans (test homes, test-layout bases), processes it cannot see (codex helpers, test brokers), and seats it never recycles because their roles declare no max-age, or because the max-age notice never reaches the seat.

The cause of the file-event growth is not proven. The reading that fits the evidence is that it tracks very large trees of short-lived files and git polling over them. It is a hypothesis, not a finding.

## 3. What marvel should own

The subject test from `docs/design/marvel-watcher.md`: a feature belongs in marvel only if, without it, a surface marvel already prints would claim something marvel cannot back. By that test:

- **marvel owns** its own footprint (seat RSS and CPU, which it already samples; homes, bases and helpers it created), proving its own processes before it acts on them, and recycling its own seats.
- **marvel does not own** host memory, the file-event daemon, other tools' temp trees, or how a harness polls git. It may report a host reading next to its own numbers, so a reader can tell "marvel is heavy" from "the host is drowning", but it never acts on what it did not create and record.

## 4. Proposal

### 4.1 Watch, report only

**For v0.1, outside the daemon.** A host script, `host-health`, reads the five signals of the corporate draft with no root, run by the host's own scheduler or a scheduled headless role. Each reading appends one line to a local log, and each rule that fires sends one report to the host's supervisor. It never signals a process.

| signal | command | rule (declared constants, report only) |
|---|---|---|
| memory pressure | `memory_pressure` last line | free under 15% for three readings in a row |
| swap | `sysctl vm.swapusage` | over half of total, where swap exists |
| largest processes | `ps -axo pid,ppid,lstart,rss,%cpu,comm` | one process over 25% of physical memory, or growing over 1 GB in 10 minutes |
| a long CPU hog | the same row, kept across readings | one process over 90% CPU for 60 minutes (added here: the file-event daemon pinned a core for hours, which the draft's rules do not catch) |
| file-event daemon slope | its row kept for 24 h | over 1 GB a day |
| orphaned test brokers | `nats-server` with parent pid 1 and a config under a temp directory | any older than 1 hour |

Every report carries pid, parent, start time, resolved path, the reading and the rule. Every threshold is diagnostic (SOUL section 8, `diagnostic-not-gate.md`): nothing blocks a merge, a spawn or a commit on it.

**For v0.1, inside marvel:** only marvel's own footprint, by party ticket T21 (a footprint summary in `marvel reap --homes`, counts only), plus T20 (live codex helpers under other layouts' bases, listed and never signalled).

**After v0.1:** a host reading may join marvel as a ticker beside `RunMetrics`, off unless the cluster config turns it on, emitting `host.memory.*` events to the events ring and adding one host line to `marvel get sessions -w`. That is where it serves the subject test: the line answers "is the host or the seat the problem" on a surface marvel already prints.

**Departure from the corporate draft.** The draft puts the watcher in the daemon now. The initial-release party ruled 5-0 against any new always-on loop in the daemon before the stability promise, and this proposal keeps that ruling. The signals and rules are the draft's, with the CPU-hog rule added.

### 4.2 A spawn cap

A cap that refuses a seat when memory is low is a gate on a vital sign. The party ruled that it needs a ratified decision first, and the corporate draft recommended no gate now.

This proposal adds one thing. marvel already has the shape a ratified gate takes: `internal/admission` refuses growth only against a budget the operator declared in the manifest, and never refuses repair (its rulings R1 and R2). A host floor would follow the same rules: an operator-declared `min_free_memory_percent` on the cluster, checked only on growth (apply, scale up), never on repair or shift, refusing with an event that names the reading and the floor. Undeclared means no gate, exactly as today.

Recommended now: no cap. Record the host's free-memory reading in each spawn event, and decide after a week of `host-health` readings shows whether a floor would have fired on kinu before the incident and stayed quiet otherwise. Card 1.

### 4.3 Cleanup by pid and parent, never by pattern

A pattern kill on another fleet host missed: the running argument list was the interpreter's resolved path, not the command typed. The rule:

1. **Identity is four values, captured when listed:** pid, parent pid, start time, and resolved executable path (`internal/procargs` on darwin, `/proc` on linux).
2. **Act only on a re-match.** At action time, re-read all four; any difference, or a vanished pid, is skipped and reported.
3. **marvel signals only what it created and recorded:** a seat's pane process tree, a codex helper whose home is under a base marvel made and whose pid file marvel read (T20), and the bus broker (already proven by #799). It uses the identity proof the bus broker already uses.
4. **Everything else is listed, never signalled.** For a process marvel did not create, the listing prints the four values and a one-line command for the operator to run by pid. This keeps the never-destroy-what-marvel-did-not-create rule (the orphan registry's 2026-08-09 ruling).
5. **Tests clean up their own children** (T22 for brokers, `aae-orc-l903m` for tmux servers, T18 and T19 for test homes and bases). That removes the largest marvel-made churn at its source instead of sweeping after it.

**Departure from the corporate draft.** The draft's cleanup command sends SIGTERM to any flagged process after a re-match. This proposal limits signalling to processes marvel recorded, and leaves the rest to the operator, because a host-wide kill verb in marvel would act on processes marvel cannot vouch for.

### 4.4 Recycling seats by age

It belongs in marvel, and it already exists: the `max-age` shift trigger. The incident shows two gaps, neither of which is a missing feature.

1. **Coverage.** Five of twelve arcaven roles declare no shift policy, the architect and the supervisor among them, which is how seats here reached 27 to 34 days on weeks-old harness builds. Three of the five (architect, builder, marvel-builder) run two replicas, and max-age is refused at apply on a role with more than one replica, because its shift would drain siblings never asked for a handoff (#452). So for those roles the gap is not a manifest choice: they cannot declare it today. Proposed: an advisory at apply for an interactive single-replica role with no max-age, beside the other advisories (`FirstPromptAdvisories`, `internal/daemon/daemon.go:1575-1580`), the same shape as party ticket T17; and #452 (a per-replica max-age request) moves up, since it is what lets a multi-replica role recycle by age at all. Whether every long-running role should carry one is the operator's (card 2).
2. **Completion.** Both open max-age requests on team arcaven show `DeliveredCount: 0` and `Escalated: true`; one has been open since 2026-10-15, sixteen days. A request that never reaches its seat recycles nothing. This matches open issues #608 (the handoff request never creates its directory) and #955 (the notice is the only channel every runtime shares). Proposed: `describe` shows a request's age and delivery count, and an event fires when a request passes its `MaxDefer` undelivered, so a stuck request is seen in hours, not weeks. Fixing delivery itself is #608 and #955.

The 72 h bound stays as it is.

## 5. Is it ongoing

- **kinu: yes.** At 21:55Z the file-event daemon held 27.4 GiB and one core, and swap had not drained. Nothing marvel can do drains it; recovery is card 3.
- **corporate: no** for memory (7.5 MB file-event daemon, no swap); **yes** for one leaked test broker, the T22 class.
- **mokuzai: unknown.** No reading by 23:00Z. The first `host-health` run there answers it.

## 6. What goes outside marvel

| item | owner | what |
|---|---|---|
| `host-health` script and its schedule | the orchestrator's tools, run per host | section 4.1 |
| git polling over the orchestrator checkout | the orchestrator and the operator | fewer live worktrees (each removed only by its owner, under the owner-lock rule), fewer untracked scratch files at the root, and a report upstream to the harness if the polling proves to be the driver |
| other tools' temp trees | each tool's owner | the unrelated tool's 3363 temp dirs are not marvel's |
| the file-event daemon's recovery | the operator | card 3 |
| the graphical session loss | the operator | outside this proposal |

## 7. Order, now against after v0.1

| when | item |
|---|---|
| now, outside marvel | `host-health` on all three hosts; card 3 on kinu |
| before the rc, marvel (party T18 to T22, rc-window) | test homes, test-layout bases, codex helpers listed, footprint summary, test brokers and tmux servers |
| before the rc, marvel (new) | the max-age advisory (4.4.1); the stuck-request age, delivery count and event (4.4.2) |
| after v0.1, marvel | #452, a max-age request per replica, so multi-replica roles can recycle by age |
| after v0.1 | the host ticker beside `RunMetrics`; a host floor on growth only if card 1 is ruled for it |

Candidate tickets, flat, for the supervisor to file and route: `host-health` script (outside marvel); the max-age advisory; the stuck max-age request surface and event; the spawn-event host reading. T18 to T22 are filed already.

## 8. Operator decisions

Each is written as a Desk card and carried by the supervisor.

1. **A spawn floor on low memory.** Recommended: no floor now; record the reading in spawn events and decide after a week of readings. If a floor is wanted later, it follows the admission rules: operator-declared, growth only, never repair.
2. **Age recycling for every long-running interactive role.** Recommended: declare the 72 h max-age on every single-replica interactive role, the supervisor included, once the stuck-request surface (4.4.2) lands, so a request that does not deliver is seen. Multi-replica roles (architect, builders) follow when #452 lands.
3. **Recovering kinu's file-event daemon.** Recommended: a planned reboot of kinu in a quiet window, after seats hand off, since the swap will not drain while the daemon holds its history and only root or a reboot clears it.

## 9. Candidate requirements (provisional)

- **HH-1** (provisional; source: incident). A health reading is report-only and never signals a process.
- **HH-2** (provisional; source: incident, corporate draft). Any process signal is preceded by a re-match of pid, parent, start time and resolved path captured at listing.
- **HH-3** (provisional; source: the orphan registry ruling of 2026-08-09). marvel signals only processes it created and recorded.
- **HH-4** (provisional; source: `describe` readings above). A max-age request that passes its `MaxDefer` undelivered is visible in `describe` and emits an event.

This recommendation is valid until 2026-11-07, or when the first `host-health` readings from all three hosts are in, whichever comes first. The arcaven architect re-checks it then.
