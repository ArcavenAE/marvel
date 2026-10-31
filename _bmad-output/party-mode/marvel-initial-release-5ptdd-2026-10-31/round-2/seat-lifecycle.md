# Seat lifecycle, round 2

Lens unchanged: a stable release may not report a seat state, health or delivery that did not happen, unless it says it does not know. I accept the chair's narrowing of #951 and reclass it below.

## 1. Classes

| id | class | reason |
|---|---|---|
| C1 | blocker | the stable path has never run; an rc tag is the cheapest rehearsal |
| C2 | should | #523's version lie is mostly gone (`reexec_target.go:14-30`, per Skeptic); #756 surprises a pinned user but does not lose state |
| C3 | blocker | "stable" is this contract; without it the tag is a rename |
| C4 | blocker | the known-issues list is something marvel says; it must be true at one sha |
| C5 | blocker | the first command a stranger copies must work |
| C6 | should | #337 fails loudly with a refusal; a loud failure is not a false reading |
| C7 | #541 blocker, #951 should | see 2a |
| C8 | should | dev-host churn; no user runs the suite to use marvel |
| C9 | should | checked, see 4; same class as C8 |
| C10 | should | `marvel orphans` reads as the orphan list but covers only stale-token holders (chair's check); it must say what it covers |
| C11 | should | dev-host hygiene, as C8 |
| C12 | read-only footprint verb: should. Daemon watcher: after-release | see 3 |
| C13 | should, each one named in the release notes as a known reading gap | the notes make the reading honest even where the fix waits |
| C14 | blocker | doc only: it fences what "true" is promised about |

## 2. Where I differ

a. **C7, against my own round 1.** Mail sent to the role survives a restart (`internal/bus/role_inbox.go:50`). Mail sent to a seat's own address does not, and that address carries the generation. The address form is shared with director, so fixing #951 is not marvel's alone. #541 is marvel's alone: one assignment in the crash-repair path, beside the shift one at `internal/team/controller.go:2472`. director-mcp reads `MARVEL_PREDECESSOR` (`director/cmd/director-mcp/dossier.go`), so with #541 fixed, a crash replacement can find its predecessor exactly as a shift successor does. Blocker for #541; #951 should.
b. **C8, against Host operator's blocker.** The leak is real and cheap to fix, but it lives where only contributors run. Classing it a release blocker makes the tag wait on hygiene no user sees. It should land before the tag because it is small, not because the tag needs it.
c. **C6, against First adopter's blocker.** The refusal is clear and the right key is printable with `describe`. It is a sharp edge, not a lie.

## 3. C12 and the draft

- **Where it lives:** not in the daemon for this release. A sampler in the one process every seat depends on adds a new way for the daemon to hang just before a stability promise. Host operator's read-only footprint verb covers marvel's own share and nothing else, which is what marvel can truthfully report. Host signals (swap, the file-event daemon) belong in a script or a scheduled role, as the draft already allows for hosts without marvel.
- **Spawn cap on low memory:** yes, it is a gate on a vital sign. Free memory is a health reading, not structural validity, so `diagnostic-not-gate.md` requires a ratified decision before it may block a spawn. The draft's own recommendation (report the reading in the spawn event, decide after a week) is the compliant one.

## 4. C9, checked

The inference holds by arithmetic, not by a run. `homeguard` makes the scratch HOME under TMPDIR (`os.MkdirTemp("", ...)`, homeguard.go:46). The layout home is `$HOME/.marvel` (`internal/paths/paths.go:72`), and the durable base is `<home>/h` (paths.go:127). With this host's 48-byte TMPDIR, the socket path under a durable home is 142 bytes against `MaxUnixSocketPath = 104` (paths.go:218), so `durableHarnessHomeBase` reports false and every test layout falls to `/tmp/marvel-h-<tag>` (`internal/session/manager.go:300-303`). Check: `python3` join of those parts, then `len`.

## 5. Missing blocker

None new. The fixed-set close (C4) should include #364 and #201 from my round 1; the chair's set already has both.
