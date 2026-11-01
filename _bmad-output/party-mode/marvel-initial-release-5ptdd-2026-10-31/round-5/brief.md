# Round 5 brief: the forced vote

Read every round-4 file first. Every seat votes on every item below. One line each: the option, and a reason in a clause. A seat that loses a vote may record dissent in one line. No new items.

## Chair's checks on round 4 claims

| claim | result | command |
|---|---|---|
| README already states a stable-formula plan | holds: README line 48, "A dedicated stable formula will be added when the first `v*` release ships". Who wrote it is not shown by git, since seats commit under the same identity | sed |
| `daemon reexec` help already names mise | holds: `cmd/marvel/main.go:519-536` describes the mise case | grep |
| README recommends a direct download | holds: README line 82, "Option 3: Download a pre-built binary" | sed |
| upgrade picks a formula by channel | named by Host operator at `internal/upgrade/upgrade.go:121`; not re-read by the chair | none |

## Order the chair will record unless a vote changes it

Splits accepted from round 4: C4a (close the fixed set, no edges) and C4b (ledger and changelog); C5-code, C5-docs and C5-install; C13-code (#1131, per harness) and C13-notes.

- C14 blocks C3, C4b and C7b.
- C4b blocks C13-notes.
- C1a, C3, C4b, C5-docs and C13-notes block C1b (the `v0.1.0-rc.1` tag).
- The C1-name ruling blocks C5-docs and `v0.1.0`.
- C1b blocks C5-install, and C5-install blocks `v0.1.0`.
- C8 before C9 (same file), C9 before C10, C10 before C12b; C11 parallel.

## Votes

| # | item | options |
|---|---|---|
| V1 | C2, #756 | A blocker (an edge into C1b); B should |
| V2 | C2 fix scope | A refuse a mise install only; B refuse mise, and any path marvel did not install unless `--replace-pinned` |
| V3 | #523 | A close it in C4a (the help already names mise); B keep a help test |
| V4 | C6, #337 | A blocker; B should |
| V5 | C7a, #541 | A blocker; B should, if C14 rules (a); C should regardless |
| V6 | how a "land before the rc if ready" item is recorded (C8, C9, C10, C11, C15, C6, C7a) | A a should with a sequencing note and no edge; B a named class "rc-window"; C an edge into C1b |
| V7 | quick start test before a tag exists | A a PR-time `goreleaser --snapshot` job that runs the README quick start (Skeptic); B the post-tag archive check only; C both |
| V8 | C14-scope (product, card) | a single-host core only; b core plus the managed bus on one host; c everything that ships |
| V9 | C1-name (product, card) | A alpha keeps `marvel` through the rc, stable takes `marvel` at `v0.1.0`, alpha moves to `marvel-alpha` with a one-time note; B alpha keeps `marvel`, stable gets its own name from the start |
| V10 | the order above | A accept; B amend (name the edge) |

For V8 and V9 also say: can the ruling be predicted from a recorded ruling? (yes with the cite, or no).

Same limits. Write round-5/<your seat label>.md, under 40 lines, and run prefile-scan.
