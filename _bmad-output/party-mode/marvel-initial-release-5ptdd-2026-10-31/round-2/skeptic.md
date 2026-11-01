# Skeptic, round 2

Lens unchanged: what a "stable" label can and cannot fix, and which round-1 blockers do not hold on fa228925. New checks are in brackets.

## 1. Classes

| id | class | reason |
|---|---|---|
| C1 | blocker | the stable pipeline has never run; an rc tag is the cheapest way to run it once on a tag no one depends on |
| C2 | should | #523's core is handled (`exec_path`, marvel#592); #756 holds but loses no state |
| C3 | blocker | this is the promise itself; without it a `v` tag renames `alpha-*` |
| C4 | blocker | cheap, and the notes are built from it |
| C5 | blocker | the first command a stranger copies fails |
| C6 | should | real friction, workaround printed by `attach`; not a lie and not data loss |
| C7 | split: #541 should, #951 drop from marvel's list | see 2 |
| C8 | should | dev-host hygiene; no user runs marvel's tests to install it |
| C9 | should | confirmed below; same class as C8 |
| C10 | should | the leak stopped with #1159; what remains is a one-time cleanup plus a listing |
| C11 | should | same class as C8 |
| C12 | after-release, outside the daemon | see 3 |
| C13 | split: #1131 and #386 should; the rest after-release | each is a true-reading defect, but none loses state, and the lifecycle seat's own items for most are should |
| C14 | blocker | a written fence costs one page and decides what every other class means |

## 2. Where I differ

- **C7, #951 is not marvel's to fix.** marvel does not build the generation-named agent inbox subject; it grants a wildcard (`agent.*.*.*.inbox`, internal/bus/declared.go:180, provision.go:42) and already ships the role inbox (declared.go:298, role_inbox.go:44). The subject a sender uses is the sender's addressing choice. #951's own first fix is "address by role" (issue body line 17). Under the independence rule (CLAUDE.md, "Marvel requires only Go, tmux, and a harness"), marvel with no bus has no inbox at all, so this cannot gate a marvel release. Keep #541, which is marvel's half: a crash-repaired seat must know its predecessor to drain it. [`grep -rn '"agent\.\|agent\.%s'` in non-test code: only grants, no subject builder for the agent form]
- **C10 is not a blocker.** The Host operator's 11 orphaned app-servers predate #1159, which now reaps a deleted seat's home and codex daemon. A release that lists them (Host operator's own fix shape) is enough; a stable label does not get worse because old leftovers exist on one host.
- **C2, #756 is should, not blocker.** It holds: `installDirect` swaps a mise install in place and says what upgrade does to one "is not this change's to decide" (internal/upgrade/store.go:258-262). The binary still works and no store is touched. The fix is a refusal on `channelMise` that names `mise upgrade`, one branch.
- **C5 versus C14, against the First adopter's "fleet features out of scope".** Out of the promise, yes. Out of the release, no: the code ships either way, so C14 must say plainly which verbs carry the compatibility promise and which are marked experimental.

## 3. C12 and the draft

- **Where:** not in the daemon for this release. The draft's own item 5 undercuts its premise: the other host has 339 test scratch dirs and a 7.5 MB file-event daemon after 48 days, so marvel's churn alone does not drive the growth. A watcher whose main signal marvel does not cause belongs outside marvel. If the fleet wants it now, a scheduled headless role or a script runs the same five reads with no daemon change. The marvel share (its homes, bases, brokers, codex daemons) belongs in the Host operator's footprint read, which is should.
- **Spawn cap:** yes, it is a gate on a vital sign. Free memory and swap are health readings, and SOUL section 8 says health metrics inform and become a gate only through a ratified written decision. Budgets in `internal/admission` are a declared, ratified limit; a memory floor is not. So no cap without a ruling, as the draft itself recommends.

## 4. C9, checked

The inference holds by arithmetic. A test's durable base is `<TMPDIR>marvel-test-home-<n>/.marvel/h`, 86 characters on this host; adding the reserve for a 12-hex leaf and codex's control socket (`harnessHomeSocketReserve`, internal/session/manager.go) gives 142, over the 104 limit, so every test layout falls to `/tmp/marvel-h-<tag>` (manager.go:286-291). One leaked test home holds an empty `.marvel/h`, which fits. [`getconf DARWIN_USER_TEMP_DIR` plus string length; `ls -d` on one leaked home]

## 5. Missing

None under my lens. LICENSE and SECURITY.md exist [`ls`].
