# finding-061: the running daemon's build is unobservable, an inherited model reaches every seat, and a lone orphan row is not a rival

Harvest of 2026-10-02 (director 6244, step 2). Source: the step-1 replies
of every seat on team arcaven, collected by the supervisor. Code claims
verified by this seat against marvel origin/main ee7666d.

**Placement.** marvel is the subject of every item below: its daemon, its
seat environment, its orphan report and its own push hook. The step-1
replies held more marvel lines than this; the ones the graph or the issue
tracker already holds are listed at the end with their home, and are not
restated here.

## 1. Nothing reports which build the running daemon is

`marvel version` prints the client binary's `main.version` and channel
(`cmd/marvel/main.go:1637-1642`), set by `-ldflags` at release. The daemon
never reports its own. Its startup lines (`internal/daemon/daemon.go`, the
`log.Printf` calls from 265 to 524) name the state file, the adopted
sessions and the listen address, and no version or VCS revision. No RPC
returns one either: the `version:` line in the bus status block is the
managed broker's version, not marvel's. A seat that needed the running
build had to infer it from `go version -m` on the binary and the brew
symlink's mtime, and both describe what is installed, not what is running.

This is the gap `elem-staged-activation-upgrades` names as a design
requirement: which version is staged and which one runs are separate facts.
Today only the first has an instrument. After a `brew upgrade` without a
`daemon reexec`, the installed binary and the running daemon differ and
nothing on the socket or in the log says so.

## 2. Model choice is safe as argv and unsafe as inherited env

The seat env denylist (`internal/api/seat_env.go`) holds exactly ten
names, all Claude session and identity variables. The tmux server starts
from the daemon's environment minus those ten
(`internal/tmux/driver.go:164` scrubs every tmux command the daemon
runs, the server start included), so anything else exported where the daemon
starts reaches every pane. #436 records this for launcher opt-ins
(`DIRECTOR_CUE`, `CAST_SCOPE`). `ANTHROPIC_MODEL` is a third member of the
class, with a different effect: it changes which model every claude seat
without a model flag runs on.

Per-role `runtime.args` already carry `--model` to the seat (finding-058,
#440), and claude reads the flag above the env var (the #411 design), so a
role that sets the flag is unaffected. A role that relies on the default is
not. The declared paths (args, and `runtime.env` once #411 lands) are in
the manifest and visible to accounting. The inherited path is in neither.

## 3. A single orphan row after a kill is the predecessor's last beat, not a live rival

After a `marvel kill` and respawn, `marvel orphans` listed the killed
session's key with one refused heartbeat ("token does not match the
session claimed") at spawn time, with no rival process alive. The row comes
from the orphan registry (`internal/daemon/orphans.go`): `Create` rotates
the session token, a beat signed with the old one is refused and recorded,
and the record stays until its last sighting is `orphanTTL` (10 minutes)
old. Which process sent that one beat was not observed; the dying
predecessor's last tick is the likely sender.

The verb's own help says "agents heartbeating against session keys this
daemon owns with a stale token", which reads as a live rival. A successor
running its singleton query reads it that way too, and holds. The record
already carries what separates the two cases: a live orphan beats every 2
to 5 seconds, so its `count` climbs and `last_seen` stays fresh; a
predecessor's last beat shows `count` 1 and a `last_seen` at the spawn.
#186 holds the live-orphan case; this is its false-positive twin.

## 4. The pre-push race suite sits close to a 60 second tool timeout

marvel's lefthook `pre-push` runs `go test ./... -race -count=1` on every
push, with no glob filter. A peer seat reported a push run under a 60
second tool timeout: the hook was killed and the push did not land, with
nothing printed to say so. Not reproduced here. One run of the same
command in a fresh worktree took 34 seconds, exit 0, 29 packages ok. That
margin is under 2x, so a cold build cache or a loaded host plausibly
crosses it. Give a push from a seat a 300 second timeout, and check
`git ls-remote` for the branch head after any push whose output did not end
in the remote's ref update line (the same discipline finding-053 item 1
sets for filtered commits).

## Already held, not restated

| step-1 item | home |
|---|---|
| broker user is per team, so grants land on a team, not a role | #312, #433 |
| host trust keyed by an address that a moved cluster changes | #434, `_kos/ideas/local-cluster-discovery.md` |
| a health restart resets session age | #438 design, `docs/design/shift-trigger-list.md` |
| launcher opt-ins leak to every seat | #436 |
| a respawned interactive seat gets no first prompt | finding-055, #391 |
| model per role is an arg today | finding-058 (#440) |
| truncated inject already filed | #343 |
| reviews are not enforced at merge | not marvel's subject: the same gap was seen on director, so it is a fleet convention for the orc graph |

## Consequences

- The daemon should report its own build: one startup log line and one
  field on an existing status RPC. A candidate issue, not filed here.
- `ANTHROPIC_MODEL` belongs in the #436 discussion as a third name in the
  inherited-env class; whatever remedy #436 takes (allowlist or a longer
  denylist) should cover it.
- `marvel orphans` could mark a record with `count` 1 whose `first_seen`
  is within a tick of the key's current `CreatedAt` as a predecessor's
  last beat rather than a live presenter.
