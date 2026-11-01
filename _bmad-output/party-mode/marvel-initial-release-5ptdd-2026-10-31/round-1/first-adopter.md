# First adopter, round 1

Lens: a stranger installs marvel from the tap, has tmux and one harness (bare claude), and has never seen this fleet. Everything below was read on main fa228925 with read-only commands.

## Items

1. **The Quick Start only works from a source checkout. Blocker.**
   README lines 85 to 103 start with `just build`, then `marvel work examples/demo.toml`. demo.toml runs `command = "../bin/simulator"`, a relative path into a built tree. A tap or mise user has no `just`, no `examples/` and no `bin/simulator`, so the first command they copy fails. [`sed -n 85,108p README.md`; `grep -n command examples/demo.toml`]
   Fix shape: a quick start that runs from the installed binary alone, for example an example manifest the binary can print (`marvel example demo`), or one written for a bare `claude` seat.

2. **A role with no model gets the harness default, silently (#1131). Should.**
   There is no NOTE anywhere for an unnamed model: `grep -rn -i 'names no model'` over internal and cmd finds only parser comments. A new user pays for a model they never chose. Fix shape: a plan-time and describe-time NOTE, not a refusal.

3. **The open-issue list overstates what is broken. Blocker for the release notes, cheap.**
   Two of the five first-adopter issues the brief names look fixed but are still open:
   - #390 (an unknown `marvel daemon` word starts a second daemon): `cmd/marvel/daemon_args_test.go:22` asserts `status`, `start` and a bogus word are refused.
   - #391 (runtime.prompt ignored for interactive roles): #1046 passes it for claude (`internal/runtime/claude.go:219` onward, `composeFirstPrompt`). The last comment on #391 predates that.
   A stranger reads the open bug count as the quality of the release. Fix shape: one triage pass that closes or re-scopes each open bug against main before the tag, with the evidence in the close comment.

4. **The name `get sessions` prints is not the name the other verbs accept (#337). Blocker.**
   Still open. It is the second command a new user types (`marvel capture <what I just saw>`), and it fails with a name they cannot guess. Fix shape: accept the printed name, or print the accepted key.

5. **No supported path to "see what it would do" before `marvel work` (#897). Should.**
   A first user applies a manifest to a live tmux server with no preview. `marvel plan` exists; #334 shows its no-match case exits 0 with a misleading line. Fix shape: `plan` names the filter that matched nothing and exits nonzero.

6. **Independence holds where it matters. Not a change; a check for the notes.**
   The managed bus is off by default (`Managed bool` zero value, `internal/config/config.go:521`), and with it on, a missing nats-server is a clear error naming the fix (`internal/bus/supervisor.go:272`). A stranger without nats can run marvel. The release notes should say so in one line.

7. **The stable formula does not exist yet. Blocker, owned by the Release engineer.**
   README line 48: "A dedicated stable formula will be added when the first `v*` release ships." The tap's `marvel` tracks alpha today, so `brew install` gives a stranger the alpha, not the release.

8. **Crash loops at spawn leave no cause (#386). Should.**
   A first run that fails seconds after exec (wrong harness path, missing login) is the most likely first failure, and it is the one with no record. Fix shape: keep the last lines of the pane and the exit status on the session.

## Not in scope

The fleet features: shifts, max-age, global roles, the hub and leaf bus, suspend and resume. A stranger with one harness does not reach them. Hardening them belongs to this fleet's own operations, and holding the release for them makes the first stable release wait on the busiest machine's problems.

## What the release promises

The install path a stranger finds gives them a pinned version they can upgrade from without losing their store, and the commands in the README work as written from that install. Beyond that, a stable tag promises that what `get sessions` says is true enough to act on, and that a failure leaves a reason.
