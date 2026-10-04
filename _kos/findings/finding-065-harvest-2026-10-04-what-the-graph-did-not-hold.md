# finding-065: harvest 2026-10-04, what marvel's graph did not hold

**Date:** 2026-10-04
**Harvest:** `harvest-2026-10-04`, team arcaven, marvel items M1 to M4, M7 to M10 and MQ1 to MQ10, collated by the supervisor from the seats' notes. Source file: the supervisor's review record for the harvest, not committed.
**Placement:** marvel. The subject of every item is marvel's own daemon, resource model, store or tests. Items whose subject is the composition (bd identity, the operator-signal family across repos) are the orchestrator's and are not placed here.
**Numbering:** next free id after finding-064. Two findings already carry `finding-039` (see open question Q4); this one does not reuse or renumber either.
**What this is not:** a charter edit. No charter prose is touched. The nodes named at the end carry the back-references.

Each claim below was read against the cited artifact on 2026-10-04. Where a claim comes from a seat's report and I did not re-measure it, it says so.

## The finding

1. **A role holds the global broker tier only when two keys agree.** The manifest declares `global_role` and the cluster's config admits the role name (`global_roles` on the bus entry; `supervisor` is admitted without being listed). One resolver, `config.ResolvedGlobalRole`, serves the renderer, `Credential` and the seat env, so they cannot disagree.
   - Admission is checked at every render, not only at apply. The renderer builds from stored teams and a daemon start rehydrates without an apply, so an apply-only check would leave a de-admitted role with its user until someone re-applied. Apply's refusal is the early, loud half.
   - With no bus a declaration is inert. With an adopted bus nothing renders and `bus.global-role-unadmitted` never fires, yet apply still refuses and the env is still gated.
   - Evidence: design marvel#519, build marvel#520 (merged), docs marvel#522 (merged); `internal/config/global_role.go`; `TestDeAdmittingARoleAcrossARestart` in `internal/bus/global_role_decl_test.go`; the review on #520 made the admission check dead and six tests failed.
   - `DIRECTOR_GLOBAL_ROLE` holds for a fresh spawn only; a seat already running keeps the env it started with.

2. **The credential resource is closed and narrow today.** Checked against main ea47261:
   - the kind set has one member, `nats-nkey-seed` (`internal/api/types.go:824-836`);
   - the store validates the kind and not the value (`internal/api/store.go:614-627`);
   - a put under an unrecognised name is stored, acknowledged and read by nothing (marvel#344, open);
   - values are transient, with no bolt bucket (`store.go:606-612`);
   - reveal is `marvel credential get --reveal`, served on the local socket only (`internal/daemon/scope.go:111-114`).
   - A password already sits under the seed kind, with a binding that reads "NOT A SEED" and that nothing reads (aae-orc#461).
   - The design question (issuance versus custody for third-party bearer material) is open. See `question-credential-custody-beyond-nkey`.

3. **Under mise, `marvel daemon reexec` restarts the old binary, and the text tells the operator otherwise.** mise installs each version in its own directory, so the running daemon's own path still points at the old one, and `selfExecPath` resolves that path (marvel#523, open; `internal/daemon/daemon.go:639-676`, `cmd/marvel/version_daemon.go:78,108`, at e979fc2). The measured way through is `stop --keep-bus` and a fresh daemon from the new path, which adopted 4 sessions. marvel#525 (merged) documents that path. The Linux package-keg variant and the bbolt read-only open timeout are already in `elem-staged-activation-upgrades` from the 2026-10-03 harvest.

4. **Max-age had three gaps, each filed; one is fixed and two are open.**
   - **Fixed:** the selector filtered on the team generation, so seats at older generations were invisible (marvel#451; the review counted 9 of 14 over-age seats). marvel#473 ("max age looks at a role's seats at every generation") merged on 2026-10-02 as 3b15508, and `firstSessionOverAge` now lists the role's sessions at any generation (`internal/team/shift_handoff.go:40-58`, read on main 2026-10-04). Only the issue #451 is still open, so it can be closed. An earlier draft of this item read the issue body and recorded the gap as live without checking main.
   - **Open:** on a role with more than one replica a max-age shift drains siblings that were never asked for a handoff (marvel#452). `team.shift-handoff-missing` lives only in the in-memory ring and the escalation is sticky, so a restart leaves a seat held with no one told (marvel#453). The first fails unwatched; the second is silent.

5. **Several operator-facing signals disagree with what happened, or say nothing.** The shared shape is a path where the exit status, the version text, an error, or the log does not match the action. Closed: `upgrade --version` ignored on a brew install and exited 0 (marvel#485); `upgrade` exited 0 when `brew upgrade` failed (marvel#486); nothing reported which build the daemon was (marvel#497). Open: reexec under mise (marvel#523, item 3); the v1 to v2 store migration logs neither that it ran nor where the backup is (marvel#524); a running daemon answers "this cluster declares no hub" from its start-time config while the file declares one (marvel#514). This is the marvel half of an orchestrator-level family; the cross-repo reading is the orchestrator's.

6. **Apply's pre-flight refuses commands the claude adapter runs.** `ValidateRuntimes` runs `exec.LookPath` on the whole `command` string, so `claude --flag value` is refused with a misleading "not on PATH", while the adapter splits the command with `strings.Fields` and takes the first field. Control: `sh` passes and `sh -c true` fails. Measured with a throwaway unit test in package `api`, nothing run against a daemon (marvel#517, open, at 889e49a). The workaround is to put arguments in `args`.

7. **A wire-compat rule for the daemon's JSON.** A released client reads only the fields it knew, so a field's meaning must not change on the wire. New meaning gets a new additive field, and a test decodes the new JSON using the old client's struct. Learned on marvel#510, where an unconfirmed toolchain revision was first placed in `commit` and had to move to a new `revision` field.

8. **The harness sets its own session variables for the processes it starts.** On this seat, measured on 2026-10-04 with a names-only check from the harness's own tool process, nine of the ten names in `api.InheritedSessionEnv` were set, including `CLAUDE_CODE_SESSION_ID`, and its value was the file name of this session's transcript under the harness's projects directory. `CLAUDE_CODE_BRIDGE_SESSION_ID` was unset, and a made-up name was unset as the control. `MARVEL_SESSION` is the seat name, not the transcript id. The denylist strips what the daemon inherited and was checked at the pane (2026-10-03 harvest). The harness adds the names back for its own children, so a tool process cannot test the denylist either way: this measurement neither supports nor refutes that note. Whether the pane process itself holds them before the harness starts was not re-measured. spectacle's reverse-brief command reads the id from this variable only when a transcript of that name exists, which is the safe use.

## Open questions

- **Q1.** Does the director shim retry or exit when its broker user is revoked? Measured only for a nats CLI subscriber (nats-server v2.14.6, loopback scratch), which stayed disconnected and retrying. Unmeasured for the shim. Home: `question-agent-communication-broker`.
- **Q2.** Is it right for the store to check a value against its kind, so a note like "NOT A SEED" is not the only guard? And which of the operator's named kinds (ssh keys, certificates, passwords, license files, API keys) are issuance and which would make marvel a custodian? Home: `question-credential-custody-beyond-nkey`, with the in-flight bd-credential probe feeding it.
- **Q3.** Values passed with `tmux new-window -e` may show in `ps eww` (research seat's note). I did not re-measure this; it is recorded so the credential question can test it.
- **Q4.** Two findings carry the id `finding-039` (the b69n contract-lane harvest, and the tap push race), so neither can be cited by id and `kos validate` exits 1 on the pair (64 findings, 2 duplicate-id failures, read 2026-10-04). Renaming either is a graph move with no forwarding primitive today; this finding does not do it.
- **Q5.** `shiftOrder` in `internal/team/controller.go` still hard-codes `supervisor` as the role that shifts last. Whether a declared global role should order there too was left to a separate ticket by the design. The stage-3 `blocks` edge is a bd action. Home: `question-shifts`.
- **Q6.** A repaint test flake was reported by a seat on a CI run (`internal/tmux/repaint_test.go:212`, the assertion that the pane painted twice, once for the nudge and once for the restore). Is it SIGWINCH coalescing? Unmeasured, and I did not re-read the run. Home: `question-session-state-observation`.
- **Q7.** marvel#516's open rulings V3 (exit-code numbering) and V4 (home paths in output) remain open in `docs/design/manifest-plan.md` section 10. Already recorded there; nothing is added to the graph.
- **Resolved, not placed:** marvel#278 (bus hub `ca_file`) is closed; the doubt that it had outlived its fix is answered.

## Held, not placed

- **bd identity and minting** (research seat): held for the operator's decisions.
- **The reverse-brief run on marvel** (marvel#526): its finding comes after the operator's review of `brs-review.md`. Three claims were found stale or misattributed by the reviewer; the count of corrections is not yet known.

## Not placed here

- A model missing from the context-window default table (`internal/usage/limits.go`) and the test-isolation family (marvel#463, #480, #493, #504, #470) are issue material and go through the filer.
- Tooling friction: the bus flake `TestSupervisorRestartsCrashedBrokerUnderBackoff` and the lost filtered output are captured as aae-orc#469. The `TestHeadlessCompletionHoldsSlotAndIsNotRefilled` sighting on marvel#527 is posted on marvel#470. `TestLinkedWorktree` failing when `TMPDIR` is inside a linked worktree is an existing, unfiled sighting; it is not systemic enough for a finding.

## Back-references

Nodes updated or added with this finding: `question-agent-communication-broker` (items 1, Q1), `question-credential-custody-beyond-nkey` (item 2, Q2, Q3), `elem-staged-activation-upgrades` (items 3, 5, 7), `elem-runtime-adapter-framework` (item 6), `question-shift-triggers` (item 4), `question-permission-model` (item 8), `question-shifts` (Q5), `question-session-state-observation` (Q6).
