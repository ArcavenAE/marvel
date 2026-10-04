# Runbook: kinu's marvel upgrade across the v1-to-v2 store migration

Status: runbook for review, 2026-10-04. Docs only. **Nothing here runs until
the operator is present, in a scheduled window, and says go.** Written to
assume the upgrade goes badly.

- Issue: #524 (the migration logs nothing). The migration itself is #482
  (1cc0a84); its backup and rollback rules are `seat-bootstrap.md` lines
  126-146.
- Sibling: `kinu-bus-cutover-runbook.md` (#534). Run this one first, in its
  own window (section 7).
- Checked against marvel `origin/main` 39a4a4c and the live kinu daemon on
  2026-10-04 at about 03:45Z (read-only).

## 1. What changes, and why it is one-way

| Fact | Where | So |
|---|---|---|
| kinu runs b533ca5 (Homebrew keg), store schema 1 | `marvel version`; `bolt.go:67` at b533ca5 | the next binary that contains #482 migrates on its first open |
| The tap's current formula (bc327df, #481) predates #482 | ancestry check | `brew upgrade` today does **not** migrate. The window waits for a tap release cut at or after 1cc0a84 |
| A v2 store is refused by a v1 binary | `bolt.go:122-134` ("newer than binary's ... refusing to load") | after migration, going back needs the backup **and** the old binary |
| The migration writes `<store>.v1.bak` first, refuses if one exists, and commits in one transaction | `bolt.go:535-611`, `:642-680` | an interrupted migration leaves the store at v1 |
| Teams with an empty `WorkDir` are stamped with the daemon's cwd, source `legacy`; a cwd of `/` with such teams is refused before the backup | `bolt.go:535-611` (the refusal at `:556-561`) | every kinu team shows `WorkDir: ""` today (`marvel describe team`); the live daemon's cwd is `~/work/aae-orc`, so that is the path stamped. This should match where the teams spawn today; the rehearsal confirms it |
| The migration prints nothing about itself | #524; `daemon.go:329-345` logs only per-team placement stamps | without #524 fixed, the evidence is the `.v1.bak` file and the placement lines |
| No `marvel.bolt.v1.bak` exists on kinu | `ls ~/.marvel/state` | nothing blocks the migration |

## 2. Preconditions (all before the window is set)

1. A tap release that contains #482 exists, and `marvel version` of that build
   is written into the window note.
2. #524 is fixed in that release (recommended, not required). Without it, the
   evidence is section 1's last-but-one row.
3. The rehearsal (section 3) passed on that exact release and its transcript is
   linked from #524.
4. No shift, scheduled run, or `marvel work` apply is in flight (`marvel get
   sessions` shows no shifting rows; nobody is mid-apply).

## 3. Rehearsal on a copy (before the window, no operator needed)

Scratch only: a scratch HOME, a scratch socket, a scratch tmux server, a copy
of the store. The live daemon is never stopped or signalled.

```sh
for v in ${(k)parameters[(I)MARVEL_*]}; do unset "$v"; done   # zsh; bash: unset $(compgen -v MARVEL_)
env | grep '^MARVEL_'                                          # must print nothing (CLAUDE.md, #504)
S=$(mktemp -d); mkdir -p "$S/state" "$S/bin"
cp -p "$(readlink -f /opt/homebrew/bin/marvel)" "$S/bin/marvel-old"   # b533ca5
# place the target release binary at "$S/bin/marvel-new"
cp -p ~/.marvel/state/marvel.bolt "$S/state/marvel.bolt"              # copy of a live file
run() { (cd ~/work/aae-orc && HOME="$S" MARVEL_TMUX_SOCKET=rehearse-a4 \
  "$1" daemon --socket "$S/m.sock" --state-bolt "$S/state/marvel.bolt" \
  --log-file "$S/daemon.log" --pidfile "$S/daemon.pid" &) }
cli() { HOME="$S" "$1" --socket "$S/m.sock" "${@:2}"; }
```

Steps, each with a pass condition:

1. **Positive control on the old binary.** `run "$S/bin/marvel-old"`. Pass:
   the first call is read-only, `cli "$S/bin/marvel-old" bus status`, and
   prints no "is rooted at" warning; then `cli "$S/bin/marvel-old" get teams` lists the same rows as the live
   `marvel get teams`, and the log has one "held at the start line" line per
   team. A copy torn by a live write fails here; re-copy and repeat.
   Stop it (`kill $(cat "$S/daemon.pid")`).
2. **Migrate.** `run "$S/bin/marvel-new"`. Pass: `$S/state/marvel.bolt.v1.bak`
   exists at about the store's size; one `placement: legacy daemon cwd` line
   per team; `describe team` shows `WorkDir` = `~/work/aae-orc`, source
   `legacy`; with #524 fixed, one line naming the migration and the backup.
3. **New client, old daemon, and the reverse.** Record whether `get sessions`
   and `ctx-forward` work across the version skew. This decides section 6
   step 4.
4. **Roll back.** Stop; `cp -p marvel.bolt.v1.bak marvel.bolt` (in `$S`);
   `run "$S/bin/marvel-old"`. Pass: `get teams` and `describe team` match
   step 1 byte for byte.
5. **Tear down.** Stop the scratch daemon; `tmux -L rehearse-a4 kill-server`;
   remove `$S`.

**Guards.** Never run `converge` against the scratch socket. The copied
teams have no live panes on the scratch tmux server, so every team holds at
the start line and nothing spawns (`daemon.go:505-515`,
`controller.go:905-930`); abort at once if the log shows a spawn. Never omit
`MARVEL_TMUX_SOCKET`, or the scratch daemon lands on the live tmux server
(`docs/demo.md`).

## 4. Who loses what, and for how long

- **No seat loses its pane or its context.** `marvel stop` detaches; the next
  daemon adopts the live panes (`admin-guide.md` lines 185-189).
- **No seat loses the bus.** Before the bus cutover, kinu's broker is the
  hand-started phase-0 process, not a daemon child, so stopping the daemon
  does not touch it.
- **For the daemon gap (expected under 2 minutes):** marvel verbs, heartbeats,
  `ctx-forward`, max-age injections and automatic shifts pause; a scheduled
  firing that falls in the gap is skipped. Seats keep working.
- **mokuzai and corporate:** untouched. They talk to their own daemons; the
  hub and the leaf links are not part of this change.

## 5. The window

Capture first, into a window directory `W=~/.marvel/window-$(date +%Y%m%d)`:

1. `marvel version > $W/version`, `readlink -f /opt/homebrew/bin/marvel >
   $W/keg`, `ps -o pid,lstart,command -p $(pgrep -f "marvel daemon")
   > $W/daemon-cmd` (the exact start command and cwd to reuse),
   `marvel get teams > $W/teams-before`, `marvel get sessions >
   $W/sessions-before`, `ls -la ~/.marvel/state > $W/state-before`.
2. Keep the old binary: `mkdir -p ~/.marvel/rollback && cp -p "$(cat
   $W/keg)" ~/.marvel/rollback/marvel-b533ca5`. `brew upgrade` removes the old
   keg.
3. `marvel stop`.
4. A cold copy: `cp -p ~/.marvel/state/marvel.bolt
   ~/.marvel/state/marvel.bolt.pre-v2-$(date +%Y%m%d)`.
5. `brew upgrade arcavenae/tap/marvel`; `marvel version` must name the
   rehearsed release.
6. Start the daemon exactly as `$W/daemon-cmd` shows (same cwd,
   `~/work/aae-orc`).
7. Verify, each a pass condition:
   - `~/.marvel/state/marvel.bolt.v1.bak` exists;
   - `marvel get teams` equals `$W/teams-before`;
   - `marvel get sessions` shows every row in `$W/sessions-before` adopted
     and running, and no new spawns;
   - the daemon log has one placement line per team, and the migration line
     if #524 shipped;
   - one seat's statusline updates (`ctx-forward` works).
8. Watch 30 minutes. Keep both `.v1.bak` and `.pre-v2-*` until the operator
   deletes them.

## 6. Rollback (on any failed check in step 7, or the operator's word)

1. `marvel stop`.
2. Keep the failed store: `mv ~/.marvel/state/marvel.bolt
   ~/.marvel/state/marvel.bolt.failed-v2-$(date +%Y%m%d)`.
3. `cp -p ~/.marvel/state/marvel.bolt.v1.bak ~/.marvel/state/marvel.bolt`
   (copy, not move, so the backup survives a second attempt).
4. Start the saved binary with the recorded command, by full path:
   `~/.marvel/rollback/marvel-b533ca5 daemon ...`. The `marvel` on `PATH` is
   still the new client. If rehearsal step 3 showed the new client cannot talk
   to the old daemon, put the saved binary first on the operator's `PATH` for
   the rollback, and seats' `ctx-forward` stays broken until a fixed release
   ships. Say which in the window note.
5. Verify `marvel get teams` equals `$W/teams-before`, and the sessions are
   adopted.

Records applied during the window are lost by design (`seat-bootstrap.md`);
the window has none, by precondition 4.

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
