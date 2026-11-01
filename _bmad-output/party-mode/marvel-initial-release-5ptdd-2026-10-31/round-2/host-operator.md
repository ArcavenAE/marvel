# Host operator (SRE), round 2

Lens unchanged: what marvel leaves on a host, and whether marvel can see it. Read-only; nothing killed or removed.

## 1. Classes

| id | class | reason |
|---|---|---|
| C1 | blocker | the first run of a signing pipeline should not be the release; the rc rehearsal is cheap |
| C2 | blocker for #756, should for #523 | a pinned install overwritten in place is a host change nobody asked for; #523 is partly fixed (Skeptic item 4) |
| C3 | blocker | the only thing that makes a `v` tag differ from an alpha |
| C4 | blocker | cheap, and a known-issues list built from this board is false |
| C5 | blocker | a quick start that fails on an installed host is the first impression |
| C6 | should | real, but a refusal with a wrong name harms nothing on the host |
| C7 | should | the chair's check narrows it: role-inbox mail survives; what is lost is mail to the generation address, which the notes can say to avoid |
| C8 | blocker | see section 2 |
| C9 | should | see section 4; real leak, mechanism unproven |
| C10 | should (down from my round 1 blocker) | see section 2 |
| C11 | should | dev-host hygiene, same class as C8 but each leak is one process, not 1,800 files |
| C12 | read-only command in this release; daemon watcher after | see section 3 |
| C13 | should | each is a reading that lies, but none of them damaged the host |
| C14 | blocker | one page of text; without it the stable promise silently covers the fleet features |

## 2. Where I differ

- **C8, blocker (Skeptic: should).** The suite is what every contributor and every release CI run executes, so "not user facing" is wrong for an open-source repo. It leaks about 1,800 read-only files per test binary on the normal path (681 of 695 leaked homes, round 1), and each such binary downloads the module set again over the network. The fix is a few lines in `internal/homeguard/homeguard.go:46-51`; holding a release for a fix this small costs nothing.
- **C10, now should (my round 1: blocker).** The chair's check shows `marvel orphans` exists, and `reap --homes` (#1157) lists codex daemons in this layout's homes. The 11 live orphans sit under other layouts' bases, which that sweep counts and leaves alone. So the gap is narrower than I said: orphans from throwaway layouts, which C9 is the source of. Fix C9 and C10 shrinks with it.

## 3. C12 and the draft

- **Where it lives in this release:** a read-only command (or a section of `marvel reap --homes`) that reports marvel's own footprint: homes, bases by layout, helper processes, brokers it started, oldest seat and its harness build. No ticker in the daemon, no event to a supervisor. The draft's five signals are host facts (swap, free memory, the file-event daemon); four of them are not marvel's, and the draft's own item 5 says temp churn does not explain the growth. A daemon ticker for them puts host monitoring into the one process that must not hang, a week before a stability promise. After release, a scheduled headless role can run the draft's checks with no daemon change.
- **Is a spawn cap on low memory a gate on a vital sign?** Yes. Free memory is a health reading, not a structural check, and refusing a spawn on it is a gate created without a ratified definition (`diagnostic-not-gate`). The draft already recommends no gate now; I agree, and add that the reading in the spawn event is fine only as an event field, never as a condition.
- The draft's item 3 (cleanup by identity: pid, start time, resolved path, re-read before one TERM) is the right shape for any later kill verb, and it should be a stated rule for C10 and C11.

## 4. C9 checked

One read-only command, `stat -f %Sm -t %m-%d` over both sets:

- `/tmp/marvel-h-*` bases by date: 10-27 219, 10-28 66, 10-30 1115; before 10-27 at most 32 a day.
- `marvel-test-home-*` by date: 10-16 103, 10-19 128, 10-27 52, 10-30 263.

So bases are not one per test home: 10-19 made 128 test homes and 32 bases, 10-30 made 263 and 1115. That fits test code added around 10-27 that builds several layouts per binary (each `t.TempDir` marvel home is a new tag) without pointing the Manager's temp root inside the test dir. It does not fit "every run under a long HOME fails the socket check", which would make the two counts move together. The leak is real and comes from tests; the mechanism is still inferred. A `git log --since=2026-10-26 -- internal/session` read by the builder would name the test.

## 5. Missing blocker

None under my lens. The network side of C8 (a module download per test binary) belongs in C8's test, not a new item.
