# finding-035 — the statusline hook's baked binary path is safe or fatal depending on install topology, not on platform

Source ticket: `aae-orc-d6sc` (P2 bug, GH `ArcavenAE/marvel#143`).
Commissioned by the fanout lead as a bug-fix lane, 2026-09-04. Converted to a
finding when the premise check overturned the ticket's own prior falsification.
Author: an agent in the marvel bug fan-out, no human at the keyboard.

Repo pin: marvel `main` @ `1466313`. **No code changed** — a fix is reserved,
not applied (operator ruling of 2026-08-08, reaffirmed in §7). The working tree
carried other lanes' edits to `docs/demo.md`, `internal/team/`, and `justfile`;
none are mine and none bear on this.

Related: `aae-orc-e1mq` (projected path must stay derivable), `aae-orc-ajnh`
(statusLine override as by-design), `aae-orc-rd80` (mise install story),
`aae-orc-4ytf` (mise `@latest` prerelease semantics), finding-063 (mise
distribution routes), finding-112 (brew/mise channel parity).

---

## 1. Headline

**Whether marvel bakes a version-pinned path into every projected statusline
hook is determined by the install topology, not by the operating system.** A
package manager that interposes a stable symlink (Homebrew) is safe. A package
manager that puts a version-named directory directly on `PATH` (mise) is not,
and there is nothing in `injectStatuslineFeed` that can tell the difference.

Two prior passes each got half of this. The ticket said macOS was unsafe via
Homebrew; that is false. The 2026-08-08 falsification said the residual was
Linux-only; that is also false. The platform was never the variable.

## 2. The correction chain

Three links, in order. Recorded rather than quietly fixed, because the shape of
the error is the transferable part.

**Link 1 — the original claim (2026-08-08, CTX% party-mode review).**
`injectStatuslineFeed` writes `os.Executable()` into every projected settings
file, and "on this machine that resolves to
`/opt/homebrew/Cellar/marvel/<version>/bin/marvel`". A `brew cleanup` after an
upgrade would therefore point every live agent's hook at a deleted binary,
during exactly the window `daemon reexec` exists to create. Filed unreproduced,
and said so.

**Link 2 — the falsification (2026-08-08, same day, fancy-dragon arm).**
Measured, and correct: `os.Executable()` on macOS returns the **symlink** path,
not the resolved one. Go's `os/executable_darwin.go` reads the runtime's
`executablePath` from the kernel exec path and performs no symlink resolution,
so a Homebrew install launched through `/opt/homebrew/bin/marvel` writes exactly
that, which `brew cleanup` does not invalidate. It further established that
`daemon.Reexec`'s `selfExecPath` only trims Linux's `" (deleted)"` suffix and
does not resolve, so re-exec cannot introduce a Cellar path either; and that the
*proposed* fix — reuse `internal/upgrade/upgrade.go`'s `EvalSymlinks` reasoning
— cannot work, because symlink resolution is one-way and nothing recovers
`/opt/homebrew/bin/marvel` from a resolved path.

This pass was right about the mechanism and rigorous about its own limits. It
identified a real residual in `os/executable_procfs.go` (`readlink`
`/proc/self/exe`, which the kernel hands back fully resolved), labelled it
**"REAL RESIDUAL, AND IT IS LINUX-ONLY"**, and marked it explicitly
source-verified and **NOT executed**, no Linux host being available. The
operator then ruled: reproduce on Linux before choosing a fix, do not implement
against the source-read. That ruling was correct discipline and is the reason
this ticket was never fixed against a wrong model.

**Link 3 — the reachability is wrong (2026-09-04, this pass).** The residual is
not Linux-only. It reproduces on macOS, today, under mise PATH-activation, which
has no symlink indirection at all. Link 2 reasoned from one macOS install
topology — Homebrew's, the only one on the machine — to a claim about macOS. The
`executable_darwin.go` reading it based that on is exactly right; the
generalization from it is what fails. `os.Executable()` returns *the path the
kernel exec'd*, so it is version-pinned precisely when the exec'd path is
version-pinned. Homebrew is safe because of its symlink, not because of Darwin.

**The durable lesson:** a platform-level conclusion drawn from a single install
topology is a topology-level conclusion wearing the wrong label. `GOOS` was
never the discriminator.

## 3. The mechanism

- `internal/session/projection.go:199` — `exe, err := os.Executable()`
- `internal/session/projection.go:205` — `feeder.StatuslineFeed(exe + " ctx-forward")`

The result lands verbatim in `statusLine.command` and
`subagentStatusLine.command` (`internal/runtime/claude.go:48`), which Claude Code
executes as a shell command on every statusline tick.

## 4. Executed measurements

Both halves the operator's STEP 1 demanded, run on macOS (Darwin arm64), not
read. A probe binary printing `os.Executable()`, `filepath.EvalSymlinks`, and
`os.Args[0]` was built once and placed into each topology.

**Half 1a — Homebrew topology (binary in a version dir, stable symlink beside
it). Safe.**

```sh
mkdir -p "$D/Cellar/v1.2.3/bin" "$D/bin"
go build -o "$D/Cellar/v1.2.3/bin/probe" probe.go
ln -s "$D/Cellar/v1.2.3/bin/probe" "$D/bin/probe"
PATH="$D/bin:$PATH" probe
```
```
os.Executable(): .../exeprobe/bin/probe
EvalSymlinks() : .../exeprobe/Cellar/v1.2.3/bin/probe
os.Args[0]     : probe
```

This is the installed marvel on this machine:
`/opt/homebrew/bin/marvel -> ../Cellar/marvel/0.1.0-alpha.20260826.214610.60b994b/bin/marvel`.
The projected hook gets `/opt/homebrew/bin/marvel`, which `brew upgrade`
relinks and `brew cleanup` does not invalidate. **The ticket's claim is false,
and link 2's falsification of it is confirmed independently.**

**Half 1b — mise PATH-activation topology (binary in a version dir which is
itself on `PATH`, no symlink anywhere). Unsafe.**

```sh
mkdir -p "$D/mise/installs/marvel/alpha-20260826-214610-60b994b/bin"
cp probe "$D/mise/installs/marvel/alpha-20260826-214610-60b994b/bin/probe"
PATH="$D/mise/installs/marvel/alpha-20260826-214610-60b994b/bin:$PATH" probe
```
```
os.Executable(): .../mise/installs/marvel/alpha-20260826-214610-60b994b/bin/probe
EvalSymlinks() : .../mise/installs/marvel/alpha-20260826-214610-60b994b/bin/probe
os.Args[0]     : probe
```

`EvalSymlinks` is a no-op here. There is no stable path on disk to resolve to —
which is why no repair confined to `injectStatuslineFeed` can exist (§7).

**Half 2 — does a version change orphan the hook? Yes.** Simulating
`mise use marvel@<newer>` followed by a prune of the old install:

```sh
mkdir -p "$D/mise/installs/marvel/alpha-20260901-000000-deadbee/bin"
cp "$BAKED" "$D/mise/installs/marvel/alpha-20260901-000000-deadbee/bin/probe"
rm -rf "$D/mise/installs/marvel/alpha-20260826-214610-60b994b"
sh -c "'$BAKED' ctx-forward"
```
```
sh: .../alpha-20260826-214610-60b994b/bin/probe: No such file or directory
shell exit=127
```

The replacement binary exists the whole time, at a different pinned path. The
baked string does not follow it.

## 5. The severity multiplier: nothing repairs the file afterwards

`Reproject()` has **exactly one caller** — `internal/daemon/daemon.go:876`, the
manifest `apply` path. It is not called on daemon start and not on
`daemon reexec`. So after an upgrade, every live session's projected settings
file keeps the dead path until someone happens to run `marvel apply`.

Two consequences follow, and both are worse than the ticket assumed:

- **The orphan window is unbounded**, not "until the next restart". `reexec`
  adopts a new binary and leaves every already-projected file untouched.
- **Marvel emits nothing.** The daemon is not in the call path at all — the pane
  shells out to a binary that no longer exists. There is no event, no log line,
  and no `describe session` signal distinguishing this from a session that
  simply has not ticked.

This also interacts with the gh-147 fix as link 2 anticipated: layout-scoped
projection directories mean a `Reproject` *would* reach a live agent's file, so
the repair path exists — it is just never triggered by an upgrade.

## 6. Practical consequence: this is the install channel marvel is moving toward

The reproducing topology is not an exotic edge case. It is a channel marvel is
deliberately adopting, and one the fleet already runs.

**Existing proof on this machine.** The orchestrator's own
`/Users/skippy/work/aae-orc/mise.toml` installs an ArcavenAE binary this exact
way:

```toml
[tools]
"github:ArcavenAE/stave" = { version = "latest", prerelease = "true" }
```

which lands as a bare binary in
`~/.local/share/mise/installs/github-arcaven-ae-stave/alpha-20260823-204429-22d9e2e/stave`
— version-named directory, no symlink, directly on `PATH` (measured). Because
the pin is `version = "latest"`, the directory name changes on every upgrade.
Were marvel installed the same way, every upgrade would orphan every live
agent's hook.

**Marvel's own install story points here.** `aae-orc-rd80` ("marvel: mise
install via ubi backend, brew fallback") tracks it, and `docs/roadmap.md` M3
lists the install story in the M3 remainder.

**Two corrections to that framing, both load-bearing, both already on the
record before this finding.** They were checked rather than assumed, because
writing the stale version into a finding about correction chains would have been
the same error one layer down:

- **ubi is a dead end.** finding-063 §3 names it: "ubi (deprecated → github:)".
  The live route is `github:ArcavenAE/marvel` — which is the topology measured
  in §4 half 1b, so the correction makes the hazard *more* certain, not less.
- **brew is not a fallback.** finding-112 records the settled decision as
  "publish to both, at full channel parity. Most people use brew, so brew is not
  demoted to a stable-only front door." `rd80`'s own premise check (2026-08-06)
  carries both corrections.

`docs/roadmap.md` still reads "mise via ubi backend as primary, brew fallback"
at the M3 remainder. That is stale on both halves. Flagged, not fixed — outside
this lane.

**Status, stated precisely so it is not overread:** `rd80` is OPEN but **not
ready** — `bd ready` does not list it, and it is blocked by three open
dependencies (`aae-orc-4ytf`, `aae-orc-qhil.5`, `aae-orc-qhil.1`).
`aae-orc-4ytf` is itself the observation that mise `@latest` prerelease
resolution behaves three different ways across the fleet. So the mise channel is
**committed in direction and unsettled in mechanism**. Marvel is not currently
installed via mise anywhere measured. The hazard is prospective — but it is
prospective on the path marvel has chosen, which is the difference between an
edge case and a scheduled one.

## 7. Options, and why the choice is reserved

The operator reserved the fix choice at STEP 2 on 2026-08-08. This finding
changes the *input* to that decision — the trigger is reachable now, on the
primary platform, with no Linux host needed — and not the decision itself. No
fix is recommended here.

What the evidence does settle is that **no repair confined to
`internal/session/**` can work.** On the reproducing topology there is no stable
path on disk to resolve to; marvel would have to *create* the indirection it
resolves through. That is necessarily daemon-side.

The options as they now stand:

| Option | Cost |
|---|---|
| Resolve at run time (write bare `marvel ctx-forward`) | Discards the no-PATH-assumption property the comment at `projection.go:173–176` deliberately defends. The pane's `PATH` is not marvel's to assume. |
| Daemon records a stable invocation path at start | `os.Args[0]` is untrustworthy in general, and a launchd/systemd-started daemon may be invoked *by* the versioned path anyway — fixing nothing on the topology that has the problem. |
| Marvel-owned stable indirection (a path under marvel's state dir it maintains and refreshes) | Preserves both properties. Introduces a new on-disk artifact with its own lifecycle, cleanup, and staleness questions, and touches daemon startup. |
| Call `Reproject()` on start and after `reexec` | Narrows the window rather than closing it; does nothing for the interval between the prune and the next daemon event. Cheap, and composes with any of the above. |
| Document and defer | Viable only while marvel is brew-installed. §6 is the argument that this has an expiry date. |

## 8. Limits of this evidence

Stated so the next pass does not inherit an overread, which is the failure this
finding is about:

- **Linux was not tested.** Docker CLI is present on this machine, its daemon is
  not reachable. The `os/executable_procfs.go` residual named in link 2 remains
  source-verified and unexecuted. Nothing here confirms or denies it; §4 makes it
  moot for deciding whether the ticket is actionable.
- **Marvel was not exercised.** The measurements use a purpose-built probe in
  replicated topologies, not a live marvel daemon with live agents. What is
  established is the path-baking mechanism and the orphaning, not an observed
  fleet-wide CTX% loss.
- **The harness's visible behaviour is untested.** That the hook exits 127 is
  measured; whether Claude Code surfaces that to the human in the pane, or just
  renders an empty status line, was not. "Silent" is asserted for *marvel's*
  side only, where it follows from §5 by construction.
- **No mise-installed marvel exists to test.** §6 is an argument from the
  fleet's stated direction and a measured sibling install (stave), not from a
  marvel install.

## 9. Adjacent defect, recorded not absorbed

`exe + " ctx-forward"` is unquoted concatenation. An install path containing a
space produces a broken hook command on every platform and every topology:

```
sh: /.../exeprobe/My: No such file or directory
shell exit=127
```

Same file, same line, different cause, and not what `d6sc` is about. Not filed
and not fixed — left to the lead to sequence.

## 10. Bearing on adjacent tickets

- **`aae-orc-e1mq`** — reinforces and extends it. e1mq establishes that the
  projected settings *path* must stay derivable because it can only be
  recomputed, never recovered. The same is true of the file's *content*: the
  baked binary path is recomputed too. §5 is the extension — recomputation only
  ever happens on `apply`, so "derivable" is necessary but not sufficient.
- **`aae-orc-ajnh`** — bears on its question 2 ("does the override survive the
  session?"). ajnh reasons about marvel's statusLine override as
  correct-by-design. If the baked binary is pruned, the operator's own status
  line has been replaced by a *broken* command rather than a working marvel one
  — a different and worse outcome than the by-design override ajnh is
  validating.
