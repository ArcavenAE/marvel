# Round 2 brief: converge on a candidate list

Read every round-1 file and round-2/input-host-watch-draft.md first.

## Chair's checks on round 1 claims (fa228925; do not repeat these)

| claim | result | command |
|---|---|---|
| homeguard discards the scratch HOME removal error | holds: `defer func() { _ = os.RemoveAll(home) }()` at `internal/homeguard/homeguard.go:51` | grep |
| #541: only the shift path sets a predecessor | holds: the one assignment is `internal/team/controller.go:2472`; the env is set from it at `internal/session/manager.go:1854` and `internal/runtime/adapter.go:578` | grep `Predecessor *[:=]` |
| #951: mail is lost across a restart | narrower: a role inbox `agent.<ws>.<team>.role.<role>.inbox` exists (`internal/bus/role_inbox.go:50`) and is granted (`provision.go:42`). Mail sent to the generation-named agent address is still lost; mail sent to the role is not | grep |
| goreleaser and the alpha job write the same tap file | holds: `ci.yml:577` copies to `Formula/marvel.rb`; `.goreleaser.yml:80` `brews` writes to directory `Formula` with no name override, so the same file | sed |
| README quick start needs a source tree | holds: `just build` at README lines 29, 89, 286 | grep |
| #390 fixed with no comment | holds: `cobra.NoArgs` at `cmd/marvel/daemon_lock_hint.go:70` | sed |
| fixed-but-open issues | 365, 364, 348, 341, 289, 202, 201, 119 each carry a "looks fixed on main by #N" comment; 186 says only its reporting half landed; 317 and 324 carry scoping comments, not fix claims, so they are NOT in the fixed set | `gh issue view N --json comments` |
| orphans are invisible | narrower: `marvel orphans` exists (`internal/daemon/orphans.go`), but it lists only holders that present a stale heartbeat token. A codex app-server never heartbeats, so the 11 orphaned app-servers are invisible to it | read the file header |
| test layouts leak `marvel-h-*` bases because a long HOME fails the socket-length check | UNCHECKED inference (Host operator says so) | none |

## Candidate list (the chair's merge of round 1)

| id | item | raised by |
|---|---|---|
| C1 | stable pipeline: goreleaser has never run; the two channels collide on one formula; tap commit unsigned; action version unpinned. Proposal: cut `v0.1.0-rc.1` first as a rehearsal | Release engineer |
| C2 | upgrade safety: #523, #756 | Release engineer |
| C3 | a written compatibility contract for store schema, config keys, upgrade and rollback (#524, #636, `bolt.go:150-152`) | Release engineer, Skeptic |
| C4 | reconcile the board at one sha (close the fixed set above with shas), and a changelog | Skeptic, Seat lifecycle, First adopter, Release engineer |
| C5 | quick start that works from an installed binary, and a stable formula | First adopter |
| C6 | #337: the name `get sessions` prints is refused by capture, inject, describe | First adopter |
| C7 | mail across restart: #541 (crash repair sets no predecessor) and #951 (agent address is generation-named) | Seat lifecycle |
| C8 | test homes leak on normal runs (read-only module cache; error discarded) | Host operator, Skeptic |
| C9 | test layouts leak `marvel-h-*` bases | Host operator |
| C10 | orphaned harness helper processes (codex app-server) are invisible; #186 remainder | Host operator |
| C11 | test brokers and tmux servers outlive a run (#884, #1162, #463, #805) | Host operator |
| C12 | a host watch: in the daemon (the draft), as a read-only command, or not in this release | draft; Host operator, Skeptic, Release engineer |
| C13 | silent defaults and false health: #1131, #334 and #897, #386, #953, #813, #917, #1079 and #703 | First adopter, Seat lifecycle |
| C14 | scope fence: fleet features (shift, max-age, hub and leaf bus, suspend) are documented as outside the stable promise, or held to it | First adopter, Seat lifecycle |

## Questions

1. Class every C item: blocker, should, after-release, or drop. One line each, with your reason in a clause.
2. For the items where your class differs from another seat's round-1 class, argue it in at most three lines, citing a file:line or an issue.
3. C12 and the draft: where does a host watch live, if anywhere, in this release? Is a spawn cap on low memory a gate on a vital sign (diagnostic-not-gate)? Answer both.
4. C9 is unchecked. If your lens cares, check it with one read-only command and say what it shows.
5. Anything missing from the list that is a blocker under your lens? At most one, with evidence.

Same limits as round 1. Write round-2/<your seat label>.md, under 60 lines, and run prefile-scan.
