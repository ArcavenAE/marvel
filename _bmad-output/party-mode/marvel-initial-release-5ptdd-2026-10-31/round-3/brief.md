# Round 3 brief: the shape of each fix and the test that proves it

Read every round-2 file first.

## Where round 2 landed (chair's tally)

| id | item | blocker | should | after or drop | state |
|---|---|---|---|---|---|
| C1 | stable pipeline, rehearsed on `v0.1.0-rc.1` | 5 | | | settled blocker |
| C3 | compatibility contract, now with a schema freeze inside `v0.1.x` so rollback is a binary swap (Release engineer's addition) | 5 | | | settled blocker |
| C4 | board reconcile at one sha, and a changelog | 5 | | | settled blocker |
| C5 | quick start from an installed binary, and a stable formula | 5 | | | settled blocker |
| C14 | a written fence: which verbs and features carry the stable promise; fleet features ship marked experimental | 5 | | | settled blocker |
| C2 | #756 upgrade overwrites a mise or pinned binary (#523 withdrawn to should by its raiser) | 2 | 3 | | contested |
| C6 | #337 printed name refused by capture, inject, describe | 1 | 4 | | contested |
| C7a | #541 crash repair sets no predecessor | 1 | 3 | 1 | contested |
| C7b | #951 generation-named agent address | | 2 | 3 (1 drop: the sender's choice) | contested |
| C8 | test homes leak on normal runs | 1 | 4 | | contested |
| C9 | test layouts fall back to `/tmp/marvel-h-*` | | 5 | | settled should |
| C10 | orphaned codex app-servers | | 5 | | settled should |
| C11 | test brokers and tmux servers outlive a run | | 5 | | settled should |
| C12 | no host watch in the daemon this release; spawn cap is a gate on a vital sign, not now | | | 5 | settled |
| C12b | a read-only verb reporting marvel's own footprint | | 3 | 2 (script or scheduled role; after) | contested |
| C13 | silent defaults and false health, named as known gaps in the release notes; #1131 into the rc | | 5 | | settled should |
| C15 | README names no tmux floor; below 3.5 exit codes are lost (#714) | 1 | | | new, contested |

## Chair's checks on round 2 claims

| claim | result | command |
|---|---|---|
| #756 holds | holds: `internal/upgrade/store.go:258-261` says a mise install "is swapped as it always was" | sed |
| a newer schema is refused | holds: `internal/api/bolt.go:141-144`; an older one other than v1 is refused at `:150-152` | sed |
| C9 mechanism | the limit is `MaxUnixSocketPath = 104` at `internal/paths/paths.go:218`. Seat lifecycle and Skeptic compute 142 bytes for a test layout, so every test layout falls back to `/tmp`; Host operator's dates show more layouts per test binary from about 10-27. Both can hold: fallback explains where, the new tests explain how many | sed |
| tmux floor | holds: `docs/user-guide.md:383` says exit codes are reliable from tmux 3.5; README names no tmux version | grep |
| the director bus is off by default (`config.go:521`) | NOT confirmed: that line is the `Managed` field, not a default. Treat as unverified | sed |

## Assignments

Each seat owns the items below. For each owned item write:
- the fix in at most five lines (what changes, where, and what does not change);
- the failing test first: its name, what it asserts, and why it fails on fa228925 today. For a docs or release item, the test is a check a script or CI job can run (for example: install from the rc tap in a clean HOME and run the quick start);
- the ticket line as it will appear in the outcome: a title, and its `blocks` edges to other C items.

| seat | owns |
|---|---|
| Release engineer | C1, C3, C2 |
| First adopter | C5, C14, C6, C15 |
| Skeptic | C4, and a falsifiability check of every other seat's tests in round 4 |
| Host operator | C8, C9, C10, C11, C12b |
| Seat lifecycle | C7a, C7b, C13 |

Then, in at most five lines, name the order the blockers must land in for an rc, as edges.

Same limits. Write round-3/<your seat label>.md, under 80 lines, and run prefile-scan.
