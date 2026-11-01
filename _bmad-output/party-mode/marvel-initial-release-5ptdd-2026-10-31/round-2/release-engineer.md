# Release engineer, round 2

Lens unchanged: does the stable path work, and does it keep its promise across an upgrade. Read-only on fa228925.

## 1. Classes

| id | class | reason |
|---|---|---|
| C1 | blocker | the stable pipeline and the stable formula are the release; an untried path ships the first `v*` (the rc rehearsal is how it gets tried) |
| C2 | blocker for #756, should for #523 | #523's core is fixed by marvel#592 (`internal/daemon/reexec_target.go:14-30`, `exec_path`), leaving help text; #756 holds, since `internal/upgrade/store.go:258-260` still swaps a mise install in place |
| C3 | blocker | without it a `v` tag promises nothing an alpha does not; `bolt.go:141-152` refuses both newer and older schemas, so neither upgrade nor rollback is defined |
| C4 | blocker | the changelog and the release notes are written from the board; a stale board puts fixed bugs in the known-issues list |
| C5 | blocker | a quick start that fails from the installed binary is the release's first impression; it depends on C1's stable formula |
| C6 | should | real and cheap, but a user can recover by reading `--help` |
| C7 | after-release, behind C14 | mail across restart is a fleet feature (director bus); the role inbox already carries mail that matters (brief check on #951) |
| C8 | should | dev-host hygiene; no stable user runs marvel's suite |
| C9 | should | same class as C8 |
| C10 | should | the reap verbs cover homes; app-server listing can join `marvel reap --homes` later |
| C11 | should | dev-host hygiene, as C8 |
| C12 | after-release | see section 3 |
| C13 | should, except #1131 | a NOTE for an unnamed model costs nothing and saves a stranger money, so #1131 is a blocker-cheap should I would take into the rc |
| C14 | blocker | it is the other half of C3: the compatibility contract has to say which features it covers |

## 2. Where I differ

- **C8, C9, C11 (Host operator: blocker).** A blocker is something that breaks the release for its user. Test homes and test brokers exist only on hosts that run marvel's own suite (Skeptic, round 1, item 2). Fix them in the release window, but they do not hold the tag.
- **C7 (Seat lifecycle: blocker).** #951 is narrower than filed: role mail survives (`internal/bus/role_inbox.go:50`). Hold it to the stable promise only if C14 puts the bus inside the promise; I recommend it stays outside.
- **C2 #523 (my own round 1: blocker).** I withdraw it as a blocker on Skeptic's evidence, checked above.

## 3. C12 and the draft

- Not in this release, and not in the daemon. The daemon is the one process every seat depends on; a new always-on loop in it, landed just before the first stable tag, adds untested surface to the path C1 has not yet run. A read-only `marvel footprint`-style read of what marvel itself left (Host operator, round 1, item 5) can ship later as an operator verb, the same shape as `marvel reap`.
- A spawn cap on low memory is a gate on a vital sign. Free memory is a health reading, not a structural check, and a cap would block a spawn on it with no ratified gate definition (`.claude/rules/diagnostic-not-gate.md`, SOUL section 8). The draft itself recommends no gate now; I agree, and add that recording the reading in the spawn event is fine because it informs and does not block.

## 4. C9

Not my lens; not checked.

## 5. Missing blocker

**Downgrade.** C3 as written names upgrade and rollback, but nothing says what a user does when `v0.1.0` misbehaves. `bolt.go:141-144` refuses a store whose schema is newer than the binary, so going back to an older alpha after any future schema bump fails at open. The contract should state that within `v0.1.x` the schema does not change, so rollback within the line is a binary swap. Check: `sed -n 135,155p internal/api/bolt.go`.
