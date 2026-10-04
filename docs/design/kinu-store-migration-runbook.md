# Runbook: kinu's marvel upgrade across the v1-to-v2 store migration

Status: runbook for review, 2026-10-04. Docs only. **Nothing here runs on
kinu without the operator present.** The rehearsal (section 3) is an
operator-attended session of its own; the upgrade (section 5) is a scheduled
window after it. Written to assume the upgrade goes badly.

- Issue: #524 (the migration logs nothing). The migration itself is #482
  (1cc0a84); its backup and rollback rules are `seat-bootstrap.md` lines
  126-146.
- Sibling: `kinu-bus-cutover-runbook.md` (#534). Run this one first, in its
  own window (section 7).
- Checked against marvel `origin/main` c99ce98, the tap's
  `Formula/marvel.rb`, and the live kinu daemon on 2026-10-04 between 03:45Z
  and 03:55Z (read-only).

## 1. What changes, and why it is one-way

| Fact | Where | So |
|---|---|---|
| kinu runs b533ca5 (Homebrew keg), store schema 1 | `marvel version`; `bolt.go:67` at b533ca5 | the next binary that contains #482 migrates on its first open |
| **The tap's floating alpha already migrates.** `Formula/marvel.rb` is `0.1.0-alpha.20261004.033357.c99ce98`, and c99ce98 contains 1cc0a84 | `gh api repos/ArcavenAE/homebrew-tap/contents/Formula/marvel.rb`; ancestry check | an unpinned upgrade on kinu would migrate the store at once. kinu is now pinned (precondition 1). The window installs the exact rehearsed release, never the floating alpha |
| A v2 store is refused by a v1 binary | `bolt.go:122-134` ("newer than binary's ... refusing to load") | after migration, going back needs the backup **and** the old binary |
| The migration writes `<store>.v1.bak` first, and refuses a v1 store while one sits beside it | `bolt.go:535-540`, `:642-680` | an interrupted migration leaves the store at v1, and any retry must move the old `.v1.bak` aside first |
| Teams with an empty `WorkDir` are stamped with the **daemon's** cwd, source `legacy`; a cwd of `/` with such teams is refused before the backup | `bolt.go:535-611` (the refusal at `:556-561`) | every kinu team shows `WorkDir: ""` (`marvel describe team`); the daemon's cwd is `~/work/aae-orc` (`lsof -d cwd` on the daemon pid) |
| Today a team with no workdir spawns where the **tmux server** is, not where the daemon is | `internal/tmux/driver.go:322-323` ("An empty dir starts the pane where the tmux server is") | on kinu, 30 of the 38 panes on the marvel tmux server start in a worktree path (the server's cwd), not `~/work/aae-orc`. So the stamp **changes** where each team's next spawn starts. Section 2 item 5 decides what to do about it |
| The migration prints nothing about itself | #524; `daemon.go:329-345` logs only per-team placement stamps | without #524 fixed, the evidence is the `.v1.bak` file and the placement lines |
| No `marvel.bolt.v1.bak` exists on kinu, and its config has no `services:` entry | `ls ~/.marvel/state`; `grep -c services: ~/.marvel/config.yaml` = 0 | nothing blocks the migration; no service attaches at start |

## 2. Preconditions (all before the window is set)

1. **Pinned. DONE 2026-10-04, before 04:02Z** (director-reported: director
   ran `brew pin arcavenae/tap/marvel`);
   kinu is still on b533ca5. Verify with `brew list --pinned --formula`, which
   must list `marvel`. **Every brew step here uses the tap-qualified name.** A
   bare `brew pin marvel` fails on kinu: Homebrew resolves the bare name to a
   cask ("Treating marvel as a cask ... Error: marvel not installed") and pins
   nothing. `marvel upgrade` delegates to `brew upgrade arcavenae/tap/marvel`
   (`admin-guide.md`, Upgrading), so the pin holds it too: `brew upgrade
   --dry-run arcavenae/tap/marvel` answers "Not upgrading 1 pinned package"
   (run by this runbook's author at about 04:05Z with
   `HOMEBREW_NO_AUTO_UPDATE=1`, together with `brew list --pinned --formula`,
   which listed `marvel`).
2. **The release.** The exact release to install is named in the window note
   by its full version and its release asset, and it is the one rehearsed.
   #524 is fixed in it (recommended, not required).
3. **Rehearsal passed** (section 3) on that release, with its transcript linked
   from #524.
4. **Quiet.** No shift, scheduled run, or `marvel work` apply is in flight.
5. **The start directory, decided by the operator.** Two branches:
   - **Accept the move (default, no writes).** Each team's next spawn starts
     in `~/work/aae-orc` instead of the tmux server's cwd. Today that cwd is
     `~/work/aae-orc/marvel-wt-318`, a feature-branch worktree
     (`feat/backend-swaps-overlay-writer`), so today's start directory looks
     accidental, and the move may be the correction. Nothing is applied.
   - **Declare roots (operator-attended, before the window).** `marvel work`
     applies and reconciles at once, with no dry run (`cmd/marvel/main.go:880-920`),
     and it also sets the workspace root (`cmd/marvel/workroot.go:19`). A workdir change
     alone moves nothing live. But any other drift between a team's manifest
     and the live team, across about 30 teams, would be applied at the same
     moment. So, per team, with the operator present: save `marvel describe
     team <ws/team>` into `$W/describe-pre-root-<team>` (the way back); diff
     the live team against the manifest; and apply only when the only
     difference is the root. A team with any other difference is left
     unapplied and takes the default branch.

## 3. Rehearsal (operator-attended, on kinu, before the window)

It runs on kinu with the operator present, because it starts daemons beside
the live one and needs one brief stop of the live daemon for a clean copy.
Everything else is scratch and checked to be scratch before each step.

**Isolation, checked, not assumed.**

```sh
for v in ${(k)parameters[(I)MARVEL_*]}; do unset "$v"; done   # zsh; bash: unset $(compgen -v MARVEL_)
env | grep '^MARVEL_'                                          # must print nothing (CLAUDE.md, #504)
S=$(mktemp -d); mkdir -p "$S/state" "$S/bin" "$S/out"
run() { (cd ~/work/aae-orc && HOME="$S" MARVEL_TMUX_SOCKET=rehearse-a4 \
  "$1" daemon --socket "$S/m.sock" --state-bolt "$S/state/marvel.bolt" \
  --log-file "$S/daemon.log" --pidfile "$S/daemon.pid" &) }
cli() { HOME="$S" "$1" --socket "$S/m.sock" "${@:2}"; }
```

Before each daemon start, in the shell that starts it, `env | grep
'^MARVEL_'` prints nothing (`run()` sets only `HOME` and `MARVEL_TMUX_SOCKET`,
so a leaked `MARVEL_BACKEND_OVERLAY_DIR` or `MARVEL_SOCKET` would pass through).
After each start: the scratch daemon's first answer is a read-only `cli <bin>
bus status` with no "is rooted at" warning, and two live counts taken before
the rehearsal are unchanged: `marvel get sessions | wc -l` against the live
daemon, and `tmux -S /private/tmp/tmux-501/marvel-75c803c5 list-panes -a | wc
-l` on the live marvel tmux server. A hand-run daemon cannot take the live socket, since it takes the
socket lock first (`daemon.go:447-457`). It reaches the live panes only through
`MARVEL_TMUX_SOCKET`, which is set to the scratch server.

**The copy.** An open bolt file copied while the daemon writes can tear, and a
read-only open of the live file blocks on the daemon's lock. So the copy is
taken cold: `marvel stop` (agents keep running; `admin-guide.md` lines
185-189), `cp -p ~/.marvel/state/marvel.bolt "$S/state/marvel.bolt"`, then start
the live daemon again with the command and from the cwd recorded first, the
same capture as section 5 step 1 (`ps` for the command, `lsof -d cwd` for the
directory). This stop
and start is the only touch on the live daemon. It costs the daemon gap of
section 4 and nothing else.

**Binaries.** `$S/bin/marvel-old` is a copy of today's keg binary (b533ca5).
`$S/bin/marvel-new` is the release asset named in precondition 2, downloaded
by its exact URL, not by `brew`.

Steps, each with a pass condition:

1. **Positive control, old binary, v1 store.** `run "$S/bin/marvel-old"`. Save
   `cli marvel-old get teams > $S/out/teams-v1` and `describe team` for each team
   into `$S/out/describe-v1-*`. Pass: `teams-v1` lists the same rows as the live
   `marvel get teams`; the log has one "held at the start line" line per team.
   Stop it.
2. **Migrate with the new binary.** `run "$S/bin/marvel-new"`. Pass:
   `$S/state/marvel.bolt.v1.bak` exists at about the store's size; there is one
   `placement: legacy daemon cwd` line per team; `describe team` shows `WorkDir`
   = `~/work/aae-orc` with source `legacy` (or no stamp, if precondition 5
   declared the roots); with #524 fixed, one line names the migration and the
   backup. Then check the **old client against the new daemon** (`get
   sessions`, `get teams`) and record the result. Stop it.
3. **Roll back.** `mv "$S/state/marvel.bolt" "$S/state/marvel.bolt.failed"`;
   `cp -p "$S/state/marvel.bolt.v1.bak" "$S/state/marvel.bolt"`. Then
   `run "$S/bin/marvel-old"`. Pass: `get teams` and each `describe team` equal
   the step-1 files byte for byte.
4. **New client against the old daemon.** With step 3's daemon up, run
   `cli marvel-new get sessions`, `get teams` and one `ctx-forward`
   heartbeat, and record the result. This decides rollback step 5. Stop it.
5. **Retry after a rollback.** First, without moving anything,
   `run "$S/bin/marvel-new"` must refuse with `bolt.go:538`'s message. Then
   `mv "$S/state/marvel.bolt.v1.bak" "$S/state/marvel.bolt.v1.bak.attempt1"` and
   `run "$S/bin/marvel-new"` again. Pass: it migrates (step 2's checks).
6. **Tear down.** Stop the scratch daemon; `tmux -L rehearse-a4 kill-server`;
   remove `$S`.

**Guards.** Never run `converge` against the scratch socket. The copied teams
have no live panes on the scratch tmux server, so every team holds at the
start line and nothing spawns (`daemon.go:505-515`, `controller.go:905-930`).
Stop at once if the scratch log shows a spawn.

**Not exercised here.** Adopting live panes across the upgrade. The scratch
daemon has none to adopt, by design. The window's step 7 checks adoption
directly, and the rollback restores it the same way.

## 4. Who loses what, and for how long

- **No seat loses its pane or its context.** `marvel stop` detaches; the next
  daemon adopts the live panes (`admin-guide.md` lines 185-189).
- **No seat loses the bus.** Before the bus cutover, kinu's broker is the
  hand-started phase-0 process, not a daemon child, so stopping the daemon
  does not touch it.
- **For each daemon gap (expected under 2 minutes; twice, once for the
  rehearsal's copy and once in the window):** marvel verbs, heartbeats,
  `ctx-forward`, max-age injections and automatic shifts pause; a scheduled
  firing that falls in a gap is skipped. Seats keep working.
- **After the window,** each team's next spawn starts in `~/work/aae-orc`
  unless precondition 5 declared its root (section 1).
- **mokuzai and corporate:** untouched.

## 5. The window

Capture first, into `W=~/.marvel/window-$(date +%Y%m%d)`:

1. `marvel version > $W/version`; `readlink -f /opt/homebrew/bin/marvel >
   $W/keg`; `ps -o pid,lstart,command -p $(pgrep -f "marvel daemon") >
   $W/daemon-cmd` and `lsof -a -p $(pgrep -f "marvel daemon") -d cwd -Fn >
   $W/daemon-cwd` (the command and the cwd to reuse); `cp -p
   ~/.marvel/config.yaml $W/config.yaml`; `marvel get teams > $W/teams-before`;
   `marvel get sessions > $W/sessions-before`; `ls -la ~/.marvel/state >
   $W/state-before`.
2. Keep the old binary: `mkdir -p ~/.marvel/rollback && cp -p "$(cat
   $W/keg)" ~/.marvel/rollback/marvel-b533ca5`.
3. `marvel stop`.
4. Cold copy: `cp -p ~/.marvel/state/marvel.bolt
   ~/.marvel/state/marvel.bolt.pre-v2-$(date +%Y%m%d)`.
5. Install the rehearsed release, never the floating alpha. Run every brew
   command with `HOMEBREW_NO_AUTO_UPDATE=1`, so brew does not fetch a newer
   formula on the way.
   - **Through the tap, only if** `brew info arcavenae/tap/marvel` names the
     rehearsed version: `brew unpin arcavenae/tap/marvel`, then `brew upgrade
     arcavenae/tap/marvel`.
   - **Otherwise:** install the rehearsed release asset by its URL, or with
     mise at that exact version.

   Then `marvel version` must name the rehearsed release. **If it does not, do
   not start the daemon.** Go to section 6 step 5 with the old binary instead:
   the store is still v1, since nothing has opened it.
6. Start the daemon with `$W/daemon-cmd`, from the directory in `$W/daemon-cwd`.
7. Verify, each a pass condition:
   - `~/.marvel/state/marvel.bolt.v1.bak` exists;
   - `marvel get teams` equals `$W/teams-before`;
   - `marvel get sessions` shows every row in `$W/sessions-before` adopted
     and running, and no new spawns;
   - the daemon log has one placement line per stamped team, and the
     migration line if #524 shipped;
   - one seat's statusline updates (`ctx-forward` works).
8. Pin again (`HOMEBREW_NO_AUTO_UPDATE=1 brew pin arcavenae/tap/marvel`, verified with `brew list
   --pinned --formula`) if it was unpinned in step 5. Watch 30
   minutes. Keep `.v1.bak`, `.pre-v2-*` and `$W` until the operator deletes
   them.

## 6. Rollback (on any failed check in step 7, or the operator's word)

1. `marvel stop`.
2. Keep the failed store: `mv ~/.marvel/state/marvel.bolt
   ~/.marvel/state/marvel.bolt.failed-v2-$(date +%Y%m%d)`.
3. `cp -p ~/.marvel/state/marvel.bolt.v1.bak ~/.marvel/state/marvel.bolt`,
   then `mv ~/.marvel/state/marvel.bolt.v1.bak
   ~/.marvel/state/marvel.bolt.v1.bak.attempt1`. The old binary does not run
   `migrateV1`, but a later retry with the new one refuses while `.v1.bak` sits
   beside a v1 store (`bolt.go:538`; rehearsal step 5).
4. `cp -p $W/config.yaml ~/.marvel/config.yaml`.
5. Start the saved binary with the recorded command and cwd, by full path:
   `~/.marvel/rollback/marvel-b533ca5 daemon ...`. If rehearsal step 4 showed the
   new client cannot talk to the old daemon, put the saved binary first on the
   operator's `PATH` for the rollback; seats' `ctx-forward` then stays broken
   until a fixed release ships. Say which in the window note.
6. Verify `marvel get teams` equals `$W/teams-before`, and the sessions are
   adopted.

Records applied during the window are lost by design (`seat-bootstrap.md`);
precondition 4 keeps the window free of them.

## 7. Order against the bus cutover: this one first, separate windows

1. **The bus flip restarts the daemon too.** Upgrading first means the flip's
   one restart runs on the final binary. Upgrading after the flip adds a second
   bus-affecting restart. After the flip the broker is a daemon child, and a
   reexec drops the `bus/leaf` credential (`admin-guide.md` lines 460-471,
   #339), so kinu's hub link would go down until the operator pushes the seed
   again.
2. **Rollback stays small.** This rollback stops the daemon. Before the flip
   that touches no bus; after it, the same rollback would take the managed
   broker down with it.
3. **Blast radius.** This window costs zero respawns. The bus flip costs one
   per kinu session (32 today). Two windows keep a failure attributable to one
   change.
