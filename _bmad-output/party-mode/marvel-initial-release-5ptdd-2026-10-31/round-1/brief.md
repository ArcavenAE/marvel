# Round 1 brief: what must change before a first stable release

Read 00-commission.md and inputs.md first, and the open issues with `gh issue list --repo ArcavenAE/marvel --state open --limit 200`. The tree is marvel main at fa228925.

## Seat lenses

| seat | lens |
|---|---|
| Release engineer | what "stable" promises: versions and channels, install and upgrade (#523, #756), store migrations (#524, #636), the release workflows, what breaks a user on upgrade |
| Host operator (SRE) | the host footprint marvel leaves: temp trees, leaked processes and brokers, cleanup, a host-health watch, and what in the incident is marvel's to fix and what is not |
| First adopter | someone outside this fleet installs marvel with a bare harness: quick start (#203), defaults that fail silently (#1131, #391), errors that mislead (#337, #390), independence from the other components |
| Seat lifecycle | whether marvel's view of a seat is true: health reads, shifts, replicas, mail across restarts (#951, #917, #348, #345, #813, #201, #186) |
| Skeptic | falsify the other four: which claimed blockers are not, what a label "stable" can and cannot fix, and what scope creep looks like here |

## Questions

1. Name at most 8 items that must change before a stable release. For each, give an issue number or a file:line, the one read-only command you ran to check it still holds on fa228925, and a class: blocker, should, or after-release.
2. Name one thing that must NOT be in the release scope, and why.
3. In two or three sentences: what does an initial release promise its user that an alpha does not?
4. Host operator and Skeptic only: of the host incident, what is marvel's to fix, and what belongs to the host or the harness?

## Limits

- Read-only. No go test, no build, no run of the marvel binary, no write under any marvel home, no network beyond `gh` reads on ArcavenAE/marvel.
- Message nobody. Write only round-1/<your seat label, lowercase, hyphens>.md, under 70 lines.
- The file is PUBLIC text: run the orchestrator's `tools/prefile-scan` on it, and fix any block-class hit. Seat labels only; no em dashes; none of the banned words.
