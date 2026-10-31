# Seat lifecycle, round 3

Owned: C7a, C7b, C13. Read-only on fa228925. Round 2 put C7a at 1 blocker, 3 should, 1 after; I keep it a blocker and say why under C7a, so round 4 can test the claim.

## C7a: crash repair names no predecessor (#541)

Fix:
- `restartSession` (`internal/team/controller.go:1909`) deletes the crashed row at its end and records nothing; the replacement is made later by the ordinary create path (`controller.go:1464`), which sets no `Predecessor`.
- Before the delete, `restartSession` records the crashed session's key on the role's `RoleHealth` (`controller.go:183`) as a pending predecessor, oldest first.
- The create path at `:1464` takes one pending key per new session and sets `sess.Predecessor`, the field the shift path already sets at `:2472`. From there it reaches the seat as `MARVEL_PREDECESSOR` by the existing path (`internal/session/manager.go:1854`).
- Unchanged: backoff, saturation, the shift path, and anything a scale-up spawns with no crash behind it.

Failing test first: `TestCrashRepairReplacementNamesItsPredecessor` (internal/team, fake session manager). One role at 1 replica; mark its session dead; advance the clock past backoff and reconcile. It asserts:
- the new session's `Predecessor` equals the crashed session's key;
- a second crash names the first replacement, not the original;
- a scale-up from 1 to 2 with no crash gives the new seat an empty `Predecessor`.

It fails today on the first assertion, because `:1464` never sets the field.

Why a blocker: it is the one defect in my lens where marvel alone causes the loss of finished work and marvel alone can fix it in one path. The shift path proves the shape, and the seat side already reads the variable (director-mcp, `dossier.go`). If round 4 holds that the bus is outside the stable promise (C14), this drops to should, and I would accept that.

Ticket: "team: crash repair passes the crashed seat's key as MARVEL_PREDECESSOR (#541)". No blocks edges; it can land in any order before the rc.

## C7b: the generation-named agent address (#951)

Fix (docs only; I accept the round-2 majority that marvel builds no agent-address subject, only the wildcard grant at `internal/bus/declared.go:180`):
- The user guide's bus section says which address survives a restart: the role inbox (`agent.<ws>.<team>.role.<role>.inbox`, `internal/bus/role_inbox.go:44`) does; a seat's own address lasts one generation, and mail to it after a restart expires unread.
- The C14 fence lists the bus as outside the stable promise, so this is a note on an experimental feature, not a promise.
- The sender-side fix (address by role by default) is director's, and goes to director as its own issue; the supervisor routes it.

Failing test first: a docs check in CI. `grep -n 'role.<role>.inbox' docs/user-guide.md` must match, together with the phrase "one generation". It fails today: the role inbox form appears in code and design docs only (`git grep -n 'role.*inbox' docs/user-guide.md` finds no line).

Ticket: "docs: say which bus address survives a seat restart (#951)". Blocked by C14.

## C13: false or silent readings, named as known gaps

Fix:
- The release notes carry a "Known reading gaps" section with one line per issue: #953 (healthy with no bus connection or no inbox consumer), #813 (no readiness to wait for after reexec), #917 (two replicas share a seat URI in a shift overlap), #1079 and #703 (a lapsed login reads healthy), #334 and #897 (plan cannot preview, and a no-match exits 0), and #386 (a spawn-time crash keeps no cause). Each line says what the reading shows and what to check instead.
- README says HEALTH means liveness, not work done.
- #1131 goes into the rc as code: an advisory for a role whose runtime names no model, beside the two the daemon already prints at apply (`internal/daemon/daemon.go:1575-1579`, `ContextFeedAdvisories` and `ShiftHeadroomAdvisories` in `internal/api/manifest.go:682`, `:708`). It is advisory, never a refusal.

Failing tests first:
- `TestUnnamedModelAdvisoriesNamesARoleWithNoModel` (internal/api). It asserts that a manifest with one role that has a model and one without yields exactly one line, naming the second. It fails today: no such advisory exists (`git grep -n -i 'names no model' internal cmd` finds parser comments only).
- A release-notes check (script, run at tag time): every issue in "Known reading gaps" is OPEN at the tag, and none is in C4's closed set. It fails today because the section does not exist.

Tickets:
- "api: advise at apply when a role names no model (#1131)". No edges.
- "release: known reading gaps section, and HEALTH means liveness in README". Blocked by C4, since the list must be read off the board at the tag's sha.

## Blocker order for an rc (edges)

- C14 blocks C3: the contract has to name the verbs it covers.
- C3 and C4 block the C1 rc tag: the tag's notes carry the contract and the changelog.
- C4 blocks the known-gaps section (C13).
- C1 blocks C5's install check, which needs the rc formula to exist.
- C7a blocks nothing and is blocked by nothing; land it before the C1 tag.
