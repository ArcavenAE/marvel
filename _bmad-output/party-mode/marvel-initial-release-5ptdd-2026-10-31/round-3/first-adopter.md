# First adopter, round 3

Owned: C5, C14, C6, C15. Read-only on fa228925.

## Two corrections to my round 2

- **`config.go:521` was the wrong cite.** That line is the `Managed` field, as the chair found. The claim itself holds on a better line: `attachServices` returns with no bus when there is no client config or no cluster for the daemon's socket, "A cluster with no services leaves d.bus nil" (`internal/daemon/daemon.go:3144-3160`). A fresh install has no client config, so it runs with no bus.
- **C15 was overstated.** Below tmux 3.5 a lost exit code "is treated as unknown (the run is retried once more), never as completion" (`docs/user-guide.md:383-385`). So the cost is a headless run re-run and paid twice, not success recorded as a crash. I drop C15 from blocker to should.

## C5. Quick start from the installed binary, and a stable formula

Fix:
- The release archive holds only `marvel`, LICENSE and README (`.goreleaser.yml:11-13`, `:32-34`), so the quick start can use neither `just`, `examples/` nor the simulator.
- Add `marvel example <name>`, which prints a manifest embedded in the binary (`go:embed`). `demo` runs a generic shell loop with `process-alive` health, so it needs only tmux; `claude` is the same with a bare `claude` seat.
- README Quick Start becomes: install, `marvel daemon &`, `marvel example demo > demo.toml`, `marvel work demo.toml`, `marvel get sessions`, `marvel stop --teardown`. The build section moves under Contributing.
- The stable formula is C1's (its own name, apart from alpha); C5 only points README at it.
- Unchanged: `examples/` and the simulator stay for contributors.

Failing test first:
- `TestExampleDemoParsesAndNeedsNoBuildTree` (cmd/marvel): `marvel example demo` exits 0, its output parses as a manifest, and no runtime command in it is a relative path. It fails today because there is no `example` verb.
- CI job `quickstart-from-archive`, run on the rc tag: unpack the release archive into a clean scratch `MARVEL_HOME` with every `MARVEL_*` unset and no Go on PATH. Extract the README Quick Start block and run it line by line. Assert `get sessions` shows one running session and `stop --teardown` leaves no `marvel-` tmux server. Today it fails at `just build` (command not found).

Ticket: "Quick start runs from the installed binary (`marvel example`, README rewrite, archive smoke job)". blocks: none. blocked by: C1 (the archive and formula it installs from).

## C14. The stability fence

Fix:
- `docs/stability.md` lists the stable verbs and the experimental ones. Stable: work, get, describe, delete, scale, run, kill, capture, inject, attach, events, stop, daemon, reap, version, upgrade, config, keys. Experimental: shift and its manifest triggers, max-age, global roles, services and the bus, hub and leaf, suspend and resume, schedule, exposure.
- Each cobra command carries `Annotations["stability"]`. `--help` for an experimental verb starts with "Experimental: not covered by the compatibility promise (docs/stability.md)."
- `marvel plan` and `marvel work` print one NOTE per experimental manifest key a manifest uses (`shift`, `global_role`, `schedule`, services).
- Unchanged: no verb is removed or refused.

Failing test first:
- `TestEveryCommandDeclaresStability` walks the root command tree and fails on any command without the annotation. Today every command lacks it.
- `TestStabilityDocMatchesAnnotations` reads the table in `docs/stability.md` and compares it with the tree. Today there is no doc.
- `TestWorkNotesExperimentalKeys`: a manifest with a `shift` block prints the NOTE. Today nothing is printed.

Ticket: "Write the stability fence (docs/stability.md, a command annotation, NOTEs for experimental keys)". blocks: C3 (the contract cites the fence), C4 (the changelog sorts entries by it).

## C6. #337, the printed name

It holds on fa228925: no bare-name resolution in `cmd/marvel` (`grep -rn -i 'unambiguous'` finds only the plan filter at `main.go:1465`).

Fix:
- `capture`, `inject`, `describe session`, `kill` and `attach` accept the AGENT NAME that `get sessions` prints when exactly one session in the daemon has it. Two or more matches are refused, listing each `<workspace>/<name>`.
- A miss prints `no session named X; keys are <workspace>/<agent-name>, see marvel get sessions`, with no usage dump.
- Unchanged: `<workspace>/<name>` works as before; the daemon RPC keeps full keys only, and the CLI resolves.

Failing test first:
- `TestSessionArgAcceptsPrintedName` (table, cmd/marvel against a fake store): a bare name with one match resolves to its key; with two matches it is refused with both keys listed; a miss prints the hint and no `Usage:`. Today the bare name returns `resource not found` plus usage.

Ticket: "Pane verbs accept the session name get sessions prints (#337)". No edges.

## C15. The tmux floor (now should)

Fix:
- README Requires names tmux 3.5 or newer for headless exit codes, and older tmux works with a retry cost.
- At daemon start, read `tmux -V` once. Below 3.5, log one line and emit a `tmux.below-floor` NOTE event; `describe daemon` shows the version. Nothing refuses.

Failing test first:
- `TestTmuxVersionFloor` (table): "tmux 3.4" is below, "tmux 3.5" and "tmux 3.7b" are not, "tmux next-3.6" is not, garbage reads as unknown with no event. Today there is no version read: `grep` for `-V` in `internal/tmux` finds none.
- A daemon test with a fake `tmux` on PATH printing 3.4 sees the event.

Ticket: "State the tmux 3.5 floor and note an older server at daemon start (#714)". No edges.

## Order for the rc

C14 -> C3 (the contract cites the fence). C14 -> C4 (the changelog sorts by it). C1 -> C5 (the archive and formula). C3 and C4 -> the rc tag. C5's archive job runs on the rc, so it lands before the tag and proves itself on it.
