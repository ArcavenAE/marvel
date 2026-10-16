# Zero-downtime upgrades: pre-flight, activation, migration and rollout

Proposal, 2026-10-16. Two decisions are open (section 7); nothing here is ruled.

- Author: the architect seat, team arcaven.
- Tickets: aae-orc-7wu0a (this note is its design output); aae-orc-k28s (the M3 remainder, whose staged-activation core this note designs).
- Builds on: finding-062 (recommendations 6, 7, 8 and 10, and its 2026-10-03 review note), finding-042 (reexec under a live fleet), the bedrock node `elem-staged-activation-upgrades`.
- Code read at marvel `ac67c07`. Every path:line below is at that commit.

## 0. Why

The goal on record is that marvel upgrades itself under a live fleet and nobody loses an agent or a message. The agents already survive: `daemon reexec` keeps the pid, the panes and the broker (finding-042). What does not survive yet is the control plane when something goes wrong. A successor that refuses the store at start leaves no daemon. An exec that fails after the detach leaves no daemon. A one-way store migration makes the prior binary unusable. Nothing tells an operator before an upgrade what the new version will change.

finding-062 named ten steps in order. Steps 1 to 3 shipped. This note designs 6, 7, 8 and 10, the ones the finding said need a design before a builder, and says where 4, 5 and 9 stand.

## 1. Where things stand, verified

| Fact | Where | Since |
|---|---|---|
| A failed brew or package upgrade is an error; `--daemon` reexecs only when the binary changed | `internal/upgrade/upgrade.go:224-232`, `:151-154`; `cmd/marvel/upgrade_after.go:15-23` | #498 |
| An exact version is honored or refused; a dev build refuses to self-upgrade | `upgrade.go:533-575`, `:348-363` | #499 |
| The daemon reports its build: a startup log line and a `version` RPC with build, start time and pid | `internal/daemon/daemon.go:334-337`; `internal/daemon/build.go:11-75` | #500, #510 |
| Only `marvel version` warns when client and daemon builds differ | `cmd/marvel/version_daemon.go:100-120`, sole caller `main.go:2021` | |
| No mise channel in `marvel upgrade`; reexec can take the client's own path | `upgrade.go:64-71`, `:160-182`; `internal/daemon/reexec_target.go:99-166` | #729 (partial) |
| No checksum or attestation check, and the prior binary is deleted after the swap | `upgrade.go:415-481`, `_ = os.Remove(oldPath)` at `:477` | |
| The store is schema 2, opened read-write with an exclusive lock and a 5 s timeout | `internal/api/bolt.go:74`, `:116-123` | #482 |
| A newer store is refused, v1 is migrated, any other older store is refused | `bolt.go:134-145` | #482 |
| The v1 migration writes a `.v1.bak` with `tx.WriteTo` and then stamps teams and the version in one transaction | `bolt.go:562-649`, `:676-717` | #482 |
| Migration runs inside Open, before the daemon serves | `daemon.go:356-358`; listen in `Start`, `:575` | |
| Reexec resolves and validates its target before the detach, and refuses a downgrade | `daemon.go:2901-2961`; `reexec_target.go:99-166` | #729 |
| Reexec carries the hub leaf seed and makes every drop loud; the CLI waits up to 90 s for the leaf after it | `internal/daemon/reexec_handoff.go:32-80`, `:202-237`; `cmd/marvel/reexec_postcheck.go:104-143` | #896 |
| An exec failure after the detach logs and exits 1, with no daemon left | `daemon.go:2955-2958` | |
| A successor that fails at start returns an error from `New`, with no fallback | `daemon.go:356-358` | |
| The post-reexec check reads the hub leaf only, not panes or sessions | `reexec_postcheck.go:104-143` | #896 |
| `marvel upgrade` has `--version` and `--daemon` only; no check or staged mode | `main.go:2093-2095` | |
| No skew rule or table exists | grep of code and `docs/` | |

So, of finding-062's list: 1, 2 and 3 are done, with one remainder for 3 (the mismatch warning on every verb). 4 is partly done through reexec's own path. 5 is not started. This note's designs depend on 5, because a fallback needs a prior binary that still exists.

## 2. Pre-flight as a report: `marvel upgrade --check` (finding-062 item 6)

**What it answers.** "If I activate this version on this host, what changes, and what will refuse?" It answers before anything is activated, and it never activates.

**Where the rules come from.** From the staged binary itself, as finding-062 proposed: the target carries its own schema version, its own manifest schema and its own migrations, so its rules cannot go stale against its code. The running binary cannot know what a newer one will refuse.

**How the target reads the store.** finding-062's review note measured that a second process cannot open the live store while the daemon holds it (bbolt's exclusive lock, `bolt.go:120`). Of the two shapes that review named, this design takes the snapshot:

- The running daemon writes a consistent copy with `tx.WriteTo` inside a read transaction, which is the same call the v1 backup already uses (`bolt.go:676-717`). A read transaction does not block writers for its duration beyond bbolt's normal rules, and the copy is consistent as of one point.
- The staged binary opens the copy, never the live file, and reads it fully.
- The copy goes in the marvel home with mode 0600 and is removed when the check ends, whatever the outcome. The store holds no bearer secrets by design (ADR-009; `internal/api/backend_credential.go` records only non-secret facts about how a backend gets its credentials), so the copy is no more sensitive than the store it copies.

The other shape, where the daemon sends the facts it knows over RPC, was rejected: an old daemon would have to know which facts a newer version cares about, which is the stale-rules problem moved to the other side.

**Two parts, two PRs.**

1. The target side: a subcommand on every build from now on, for example `marvel upgrade report --store <copy> --running <build json>`. It prints a JSON report and exits with a severity code. The report has a small fixed envelope so any CLI can print a newer target's report: `report_version`, and a list of findings, each with `severity` (info, warn, refuse), `object` (the team, role, field or file it names), `rule` and `text`. It covers:
   - the schema step, from N to M, and whether it is one-way;
   - manifest fields the target removes or renames, found in applied teams;
   - placement facts the target will stamp (the daemon's working directory, as #482's migration does);
   - the result of running the target's migration on the copy (section 4).
2. The driver: `marvel upgrade --check [--version V | --exec-path P]`. It stages or locates the target, asks the daemon for the copy, runs the report, prints it, and removes the copy. `--exec-path` names a binary a channel has already staged (a brew keg, a mise install), so the check works on every channel without marvel staging anything itself. On the direct channel it stages into the version store of section 5.

**Severity and gating.** It reports; it does not gate (ADR-007, and the diagnostic-not-gate rule). The exit codes follow pluto's convention (0 clean, 2 warnings, 3 something the target will refuse), so a script can choose to stop. The one place a finding acts on its own is where the refusal is already structural and ratified: a target that would refuse this store at start. That is section 3's pre-check, and it already refuses a newer store today.

## 3. Activation with a health check and a bounded fallback (finding-062 item 7)

**Before the detach: will the successor start?** Today reexec checks the target's build info, refuses a downgrade and probes its capabilities (`reexec_target.go:99-166`, `reexec_handoff.go:202-237`). It does not ask whether the target will accept the store. Add that: run the section 2 report against a fresh copy, and refuse the reexec when it has a finding of severity refuse that comes from the store (newer schema, no migration path, a migration that failed on the copy). This is a structural check, "will Open accept this file", so it may gate. Without it, the failure it prevents is the worst one on the list: no daemon at all.

**After the exec: did activation take?** `checkAfterReexec` already waits up to 90 s for a successor with a new start time and a hub leaf that is up (`reexec_postcheck.go:104-143`). Extend what it reads, all from the daemon's own answers:

- the build the successor reports equals the target;
- the adopted pane count equals the count before the reexec (the CLI reads it before sending reexec);
- the hub leaf is up (as today);
- no session went from healthy to degraded across the window.

**Fallback: only for structural failure, and only once.** Two failures leave no daemon today, and both get one automatic try of the prior binary:

- the exec itself fails after the detach (`daemon.go:2955-2958`): exec the prior binary before exiting;
- the successor fails at start (`New` returns an error): the departing daemon passes the prior binary's path in the environment, and a successor that cannot start execs it, with a marker so this happens at most once.

Both need a prior binary that still exists, which is section 5. Neither helps once a one-way store migration has committed, because the prior binary refuses the newer store (`bolt.go:134-137`); that case is section 4's. If it does not exist, or the fallback also fails, the behavior is today's: log, exit, and say which command starts a daemon to adopt the panes.

**No automatic rollback on a health finding.** When the successor starts but a health check fails (fewer adopted panes, the leaf down, a degraded session), the CLI reports it with the one command that rolls back, and a person decides. Two rules settle this, so it is not put up as a decision: automation proposes and does not judge (ADR-007), and nothing executes on a clock or on silence. So finding-062's "fall back automatically if no confirmation arrives in a bounded window" is dropped. The restarts above are kept, because a daemon that cannot start is not a judgment call.

## 4. Migrations and the commit point (finding-062 item 8)

finding-062's review note showed the problem: a one-way migration runs inside Open, before the new daemon serves (`daemon.go:356-358`), so "migrate only after the health check passes" cannot be ordered as written. Once the migration commits, the prior binary refuses the newer store (`bolt.go:134-137`), and section 3's fallback no longer works.

Three ways to handle it:

- **(a) The new binary runs on the old schema until confirmed.** Every release then has to serve both schemas for at least one step. This is the cleanest semantics, and the most expensive to carry in every migration.
- **(b) Rehearse, and keep the backup (recommended).** The section 2 report runs the target's migration on the snapshot copy and reports whether it applies and what it changes. Activation then migrates the live store as today, with its `.vN.bak` kept (as `bolt.go:676-717` does for v1). Rollback after a committed migration is a confirm-gated restore: stop, put the prior binary back, move the backup over the store, start. The CLI prints those steps and the loss they carry (writes made since activation), as `docs/design/kinu-store-migration-runbook.md` already does by hand for the v1 step.
- **(c) Never write one-way migrations.** Every schema change becomes additive, so an older binary can always read a newer store. This cannot be promised for every future change.

(b) costs little now, because the backup and the migration code exist, and it moves the risk to before activation, where the rehearsal can see it. (a) can still be chosen later for a migration the rehearsal cannot cover. This is decision Z1 (section 7), because it sets what rollback means for every future schema change.

## 5. The smallest version store (finding-062 item 5, which sections 3 and 4 need)

finding-062's hypothesis stands: leave each channel's binaries where the channel puts them, and record on marvel's side which version is active and which is prior.

- brew kegs and mise installs are already side by side by version; marvel records their paths.
- The direct channel is the only one that destroys the prior binary (`upgrade.go:477`). It keeps it instead, under the marvel home, by version, together with a checksum verified against the release's `checksums.txt`, which the release already publishes and nothing reads today.
- A small record under the marvel home names the active and prior builds and paths. `upgrade --check`, reexec's fallback and the rollback steps read it.

This is finding-062 item 5 as written, plus the record. It is a builder ticket and needs no design beyond this.

## 6. Version skew and rollout across clusters (finding-062 item 10)

**The skew table** (the rules a mixed-version fleet follows):

| Pair | Rule |
|---|---|
| CLI against daemon | Either may be newer. The daemon keeps each RPC method and field for at least one release after its replacement ships. The CLI warns on every verb when the builds differ (item 3's remainder), not only in `marvel version` |
| A verb the daemon does not support | The CLI should refuse with the missing capability named, never a raw RPC error. No daemon-side capability answer exists yet: `version --capabilities` (#896) is local, dials no daemon, and reports only the binary's reexec handoff format, which a departing daemon reads from its successor (`cmd/marvel/main.go:2009-2027`). This needs new work, for example a capability list on the daemon's `version` RPC |
| Daemon downgrade | Not by reexec, which refuses a downgrade (`reexec_target.go:161-163`). A downgrade is a stop and a fresh start of the older binary, and is safe only where that binary reads every record the newer one wrote: the store schema (`bolt.go:134-137`) and the broker identity record (`internal/bus/identity.go:16-23`) |
| Daemon and the director shim | Out of marvel's scope. The shim is director's and has its own contract (`docs/design/shim-heartbeat-feed.md`) |
| Daemon and nats-server | The broker version is pinned (`mise.toml`); the daemon reports the version it started. The broker's own activation is designed in `docs/design/bus-as-service.md` section 5 and is not repeated here |

**Rollout across clusters.**

1. Run `marvel upgrade --check` on every cluster first, and read the reports side by side. A refusal anywhere stops the rollout before any activation.
2. Activate one cluster at a time. After each one, a person reads the section 3 health check and says go before the next cluster starts. There is no timer, so a quiet cluster never counts as a pass.
3. The order is decision Z2 (section 7). The recommendation is the cluster with the fewest live seats first, and the cluster that hosts director last, so the fleet's coordinator keeps a known-good control plane until the others have taken the build.

Driving this from one command across clusters (an EKS-style rollout) waits until steps 1 and 2 have been done by hand at least once. Until then, each cluster's step is an operator-steps script, as the host upgrade tickets already are.

## 7. Decisions

### Z1. What rollback means once a one-way store migration ships

**In short:** after an upgrade migrates the store, the old marvel can no longer read it. Should rollback then be a restore from the kept backup (losing writes made since the upgrade), or should every release be able to run on the previous schema until someone confirms the upgrade?

- **(a) The new version runs on the old schema until confirmed.** Rollback stays a simple reexec. Cost: every release must read and write two schemas for one step, which makes every schema change about twice the work, indefinitely.
- **(b) Rehearse on a copy, migrate as today, keep the backup (recommended).** `upgrade --check` runs the migration on a copy first, so a failing migration is seen before anything changes. Rollback after the commit is a guided restore that loses the writes made since the upgrade. Cost: small; the backup and migration code exist.
- **(c) Only additive schema changes, ever.** Rollback is always a reexec. Cost: a promise that cannot be kept for every future change; it would be broken the first time a field must change meaning.

Recommendation: (b), with (a) reserved for a migration the rehearsal cannot cover. Valid until 2026-10-30; the architect role re-checks it then.

### Z2. The order clusters take a new marvel build

**In short:** when every cluster needs the same upgrade, which goes first?

- **(a) Fewest live seats first, the cluster that hosts director last (recommended).** A bad build hurts the fewest agents first, and the coordinator keeps a known-good control plane until the end. Cost: director's own host runs the old build longest.
- **(b) The cluster that hosts director first.** Problems show up where the operator is watching. Cost: a bad build hits the coordinator first, and every other cluster's view of the fleet goes with it.
- **(c) All at once.** Fastest. Cost: no cluster stays known-good, which is the opposite of the goal.

Recommendation: (a). Valid until 2026-10-30; the architect role re-checks it then.

## 8. Are aae-orc-7wu0a and aae-orc-k28s one ticket?

No. They overlap in one part and differ in the rest.

- 7wu0a asks for a design of the zero-downtime posture: fault recovery, the upgrade procedure, pre-flight, reexec and the operator's view of all of them. Its stated output is a design note plus flat follow-on tickets.
- k28s is the M3 remainder: service units (launchd and systemd), the install story beyond brew, adopt-by-default, the skew rules, and the staged-activation design core, with the broker's activation riding under it (its 2026-09-16 notes).

The overlap is the staged-activation core and the skew rules, which this note designs. So this note closes 7wu0a's design question once it merges and its tickets are filed. k28s stays open for service packaging, the install story and the broker's activation, with a REMAINING line pointing here for the part this note covers. Merging the two would hide k28s's packaging work under a research ticket.

## 9. Follow-on tickets (proposed, filed after review)

Each is flat, one PR, with its dependency edges:

1. Warn on client and daemon build mismatch on every verb (item 3 remainder). No edges.
2. Version store: keep the direct channel's prior binary and verify its checksum; record active and prior (section 5). No edges.
3. `upgrade report`, the target side of the pre-flight (section 2). No edges.
4. `upgrade --check`, the driver (section 2). Depends on 2 and 3.
5. Reexec successor pre-check against a store copy (section 3). Depends on 3.
6. Post-reexec health check: build, adopted panes, sessions (section 3). No edges.
7. One-time fallback to the prior binary on exec failure and on start failure (section 3). Depends on 2.
8. Migration rehearsal in the report, and guided rollback steps (section 4). Depends on 3, and on Z1.
9. The skew table in the admin guide (section 6). No edges.
10. A capability list on the daemon's `version` RPC, and the CLI refusal that names a missing capability (section 6). No edges.
11. A cluster rollout runbook in the order Z2 sets (section 6). Depends on 4 and 6, and on Z2.

finding-062 items 4 (mise in `marvel upgrade`) and 9 (placement on activation) stay with their existing issues, #756 and #636.

## 10. What this note does not cover

- Fault recovery outside upgrades (a dead bus, a stale credential, tmux server loss), which 7wu0a's first scope item lists. Those are tracked by their own tickets, and this note does not re-survey them.
- Whether a vendor harness behaves the same under a new marvel. That stays a canary question, as finding-062 said.
- The broker's activation (`docs/design/bus-as-service.md` section 5) and director's hub.
- Nothing here was run. The designs rest on the code reads in section 1.
