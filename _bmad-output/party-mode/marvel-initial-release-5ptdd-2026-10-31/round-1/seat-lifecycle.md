# Seat lifecycle, round 1

Lens: is marvel's word about a seat true? An alpha may be wrong about a seat. A stable release may not, unless it says "unknown". So my blockers are the places where marvel reports success, health or delivery that did not happen. Every check below is a read-only command on fa228925.

## Items

| # | item | check (fa228925) | class |
|---|---|---|---|
| 1 | #951: mail to a seat's own inbox is lost silently when the seat restarts into a new generation. The agent inbox subject still carries `-g<N>-<R>`; only the role inbox (`RoleInboxSubject`) survives a restart | `git grep -n inbox internal/bus/declared.go internal/bus/role_inbox.go` (declared.go:180 still grants `agent.*.*.*.inbox`; role_inbox.go:44 is the role form) | blocker |
| 2 | #541: crash repair starts the replacement with no `MARVEL_PREDECESSOR`, so it cannot pick up its predecessor's unread mail. Only the shift path sets it | `git grep -n Predecessor internal/team/controller.go` (one assignment, :2472, in the shift path; `restartSession` at :1909 sets none) | blocker |
| 3 | #953: a seat reads `running / healthy` with no broker connection, or with a connection and no inbox consumer. Health is the column a release user reads first | issue body has measurements from one cluster; no bus field in the health computation (`git grep -n -E 'BusConnected\|bus_connected' internal` finds nothing) | should |
| 4 | #813: after `daemon reexec`, `get sessions` shows every seat running while mutating verbs refuse with "daemon is starting", and nothing can be polled to wait it out | `git grep -n ErrDaemonStarting internal/daemon/daemon.go` (:1124, :1193; refusal only, no readiness verb) | should |
| 5 | #917: two live replicas from different generations share one seat URI during shift overlap, so `BEADS_ACTOR` and anything keyed on the slot cannot tell them apart | `internal/seaturi/seaturi.go:3-4` (slot is `<role>-<idx>`, no generation) | should |
| 6 | #1079, #703 with #1142: a seat at an expired-login screen reads healthy. `StateLoggedOut` exists, but not every harness version has a pattern for it | `internal/panestate/panestate.go:24`; #1079 and #703 still open | should |
| 7 | Fixed but still open: #365 (`ValidateReplicas`, internal/api/replicas.go:19), #364 (`keepAfterFailedKill`, internal/session/manager.go:1934), #348 (`TestPlanScaleDownKeepsNewestByCreationTime`, internal/team/plan_test.go:419), #201 (`TestHeadlessCompletionHoldsSlotAndIsNotRefilled`), #345 (#350; one message text left), #186 (reporting landed in #192; kill ruled out). Each has a "suggest closing" comment | `git grep -n` for each symbol above | blocker for the release notes, not for code |
| 8 | #186 remainder: heartbeat tokens never expire or rotate, so an orphan from an old daemon keeps a valid-looking credential | issue's last comment | after-release |

Item 7 matters to my lens because a known-issues list is also something marvel says. If the open list keeps six closed defects, the release reads worse than it is. If the notes say they are fixed and they are not, it reads better than it is. Triage it before the cut, with the fix sha in each close.

Items 1 and 2 are one failure seen from two sides. A seat that crashes loses whatever sat in its generation inbox (1), and its replacement is not told whose mail it should read (2). Together they mean a crash loses finished work, which is what #951 measured on a healthy bus.

## Not in the release scope

Per-seat suspend and resume. fleet-suspend-resume.md section 7 says a seat cannot be frozen without a wrapper launch in front of the harness, and that wrapper does not exist. Cluster-wide suspend is enough for a first release. Building the wrapper now would also change how every seat launches, a week before a cut.

## What an initial release promises (Q3)

Whatever marvel says about a seat (state, health, delivered, succeeded) is true, or it says it does not know. Mail sent to a seat or its role outlives the seat's restart, whether by crash or by shift. An upgrade (`daemon reexec`) ends in a state that a script can wait for and then use.
