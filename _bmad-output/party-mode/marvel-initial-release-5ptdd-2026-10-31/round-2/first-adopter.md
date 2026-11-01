# First adopter, round 2

Lens unchanged: a stranger with the installed binary, tmux and one harness, and none of this fleet's services. Read-only checks on fa228925.

## 1. Classes

| id | class | reason |
|---|---|---|
| C1 | blocker | a stranger's first `brew install` must get the tag it names; rehearse on `v0.1.0-rc.1` as proposed |
| C2 | should | #756 should refuse a mise or pinned path and say how to upgrade; #523 is down to help text (Skeptic's check) |
| C3 | blocker | one page, not a framework: the schema the first stable opens, and that the next stable opens it with no hand step |
| C4 | blocker | the known-issues list is the first thing a stranger reads; close the fixed set with shas |
| C5 | blocker | the first command a stranger copies fails today (README 85 to 103, demo.toml `../bin/simulator`) |
| C6 | blocker | the second command fails with a name they cannot guess (#337) |
| C7 | should | real, but it needs the director bus, which a stranger does not run; it sits behind the C14 fence |
| C8 | should | hits contributors, not users; cheap, so do it, but it does not hold a tag |
| C9 | should | same class as C8; checked below |
| C10 | should | the reap for new orphans landed (#1159); old orphans exist only on long-lived hosts |
| C11 | should | dev-host hygiene, same as C8 |
| C12 | read-only footprint verb: should; daemon watcher: after-release | see 3 |
| C13 | split | #1131, #334 and #897, #386, #1079 and #703: should, each a NOTE or a reason, no refusal. #953, #813, #917: behind the C14 fence |
| C14 | blocker | it is one section of text, and it is what makes C7 and half of C13 safe to defer |

## 2. Where I differ

- **C7 (Seat lifecycle: blocker).** The loss needs a bus and a restart into a new generation. A stranger has neither: the managed bus is off by default (`internal/config/config.go:521`). Holding the tag for it holds the release for this fleet's use. Fence it in C14 and fix it in the first point release.
- **C8 and C9 (Host operator: blocker).** Neither touches an installed binary: `internal/homeguard` is test-only, and the `/tmp/marvel-h-*` growth comes from test layouts. I agree on the fix and on doing it now; I do not agree it gates the tag.
- **C13 (#1079, Seat lifecycle: should).** Agreed on class. From my lens it is the likeliest first failure: a stranger whose harness login has lapsed sees `healthy`. The release notes should say HEALTH is liveness (CLAUDE.md already does), in the README.

## 3. C12, and the spawn cap

- **Where:** not in the daemon for this release. A read-only verb that reports what marvel holds (homes, bases, codex daemons, brokers it started, oldest seat) is should. Host facts (swap, free memory, the file-event daemon) are the host's, and a script or a scheduled role can sample them without marvel (SOUL section 2). The draft's own data (item 5) says temp churn does not explain the busiest host alone, so a watcher in marvel would watch mostly other tools.
- **The spawn cap:** yes, a spawn cap on low memory is a gate on a vital sign. Free memory is a health number, not a structural check, so under diagnostic-not-gate it needs a written, ratified decision first. The draft already recommends no gate now. I agree: put the reading in the spawn event and nothing more.

## 4. C9 check

Holds by arithmetic. The durable base is `<home>/.marvel/h` (`internal/paths/paths.go:127`), and a socket under it must fit 104 bytes (`paths.go:218`) with a reserve of 1 + 12 + 43 bytes (`internal/session/manager.go`, `harnessHomeSocketReserve`). A scratch HOME under a 48-byte TMPDIR is about 76 bytes, so the path is about 142 bytes, over the limit. The durable base is refused and the spawn falls to `/tmp/marvel-h-<tag>`, one per scratch layout. [`python3` length sum over `$TMPDIR`; the code path read at `manager.go:280-320`.] I did not count which tests build a Manager.

## 5. Missing blocker

**The tmux floor is not stated where a stranger looks.** `docs/user-guide.md:383` says the exit status of a dead pane is reliable only from tmux 3.5, and #714 shows exit-0 runs recorded as crashed below it. README states no tmux version, and nothing in `internal/tmux` checks one (`grep` for a minimum finds only comments naming 3.4 and 3.7b). Ubuntu 24.04 ships tmux 3.4, so a common Linux first run records success as a crash. Fix shape: a Requires line in README, and a daemon-start NOTE when the server is below 3.5. The line is the blocker; the NOTE is should.
