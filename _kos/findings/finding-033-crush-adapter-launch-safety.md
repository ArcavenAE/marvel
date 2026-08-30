# finding-033: Crush adapter launch-safety — the shared-socket env exposure measured at the source, and the config-execution gate

> Security review requested by the operator 2026-08-09 (aae-orc-x007), with
> the severity-deciding measurement raised as aae-orc-5jj6 and the launch
> ruling as aae-orc-8ooq / aae-orc-xdg6. This finding extends finding-020
> (§6 environment exposure, §9 `.crushrc` execution), which measured both
> behaviours empirically but left the load-bearing question — *who else can
> read a client's environment over the socket* — explicitly unestablished
> ("What was not established → Multi-workspace behavior"). That question is
> the one this finding answers, from Crush's own source at a pinned tag.
>
> **Number collision note:** highest existing finding at authoring time was
> finding-032; this took the next free number (033). Several sibling lanes
> in the same fan-out may also grab "highest+1"; if a collision surfaces at
> merge, renumber this entry (it cross-references by content, not number).

## The ruling, in one sentence

A marvel Crush adapter **may be built and may launch**, but only when the
workspace's Crush config files match an operator-approved allowlist (the
direnv model), with Crush's observability socket **off by default** and no
secret in the constructed environment whenever it is on; until curtain can
sandbox the launch (and isolate that per-uid socket) it must **not** be
pointed at untrusted (factory-tier) workspaces at all — and because marvel
today ships no Crush adapter and its model has no per-workspace checkout
directory, this finding is the gate on that future work, not a change to
shipped code.

## Severity

**High, latent.** Two composable hazards, neither live today (no Crush
adapter exists), both structural in Crush v0.88.1. **Hazard A** (config
executes as Bash) is present the moment an adapter launches Crush in a
workspace *whether or not* the observability socket is enabled — its
sharpest sub-case is **unauthenticated code execution as the operator from
untrusted repository content, before any permission model applies**, which
is the factory tier (repositories grown from agent output) marvel most
wants a second harness for. **Hazard B** (the socket serves every
workspace's env) is present whenever the adapter sets
`CRUSH_CLIENT_SERVER=1` in a multi-agent, single-operator, single-host
deployment — precisely marvel's intended shape. The two hazards have
different triggers (a hostile repo vs. an enabled socket) and are
mitigated by different controls; the ruling keeps them separate for that
reason.

---

## 1. Scope and provenance

- **Target:** `charmbracelet/crush`, tag **v0.88.1** (the build finding-020
  measured, darwin/arm64). All source quotes below are from that tag.
- **Method:** source reading at the pinned tag, cross-checked against
  finding-020's empirical measurements. An isolated-rig empirical probe
  (the experiment aae-orc-5jj6 describes) was **not** re-run here: crush is
  not installed on this host (mokuzai), and the source is dispositive for
  the specific question 5jj6 asks, because the list handler discards the
  HTTP request entirely, so it cannot filter by caller *as written* —
  filtering would require connection/peer-cred plumbing Crush does not have
  here (§3). The probe would confirm, not decide.
- **The upstream-claim gate is NOT satisfied for any upstream filing.** No
  clean-tree reproduction against a build we made; the pin is a tag read,
  not a `go build` we ran. Nothing here is to be filed on
  `charmbracelet/crush` before the operator answers x007(b) below. This
  finding is a layer-1 (in-repo) + intended-layer-3 (bd) artifact under the
  three-layer rubric, not an upstream disclosure.

---

## 2. The two hazards, stated once

**Hazard A — code execution from the workspace (the lead item).** A
`.crushrc` in a project directory is a plain Bash script Crush executes at
config load, with the same embedded shell the `bash` tool uses, *before any
agent turn exists to be permitted*. finding-020 §9 measured it firing on
`crush run` with no trust prompt and no `--yolo`; `allowed_tools` does not
apply because it runs before the permission system is running. It needs only
a hostile **repository**, not a hostile process. `charmbracelet/crush#3410`
(merged 2026-07-31) makes the Bash-config behaviour deliberate and
documented — so there is no upstream fix to wait for. The PR's own text
widens the surface past `.crushrc`: it states that "both `crushrc` and
`crush.json` are backed with the Bash interpreter and both are trusted
code, and should be treated with the same precautions as any other
script." So a project `crush.json` is, *per the upstream author's own
description* (documented, not separately confirmed against the config
loader here), not inert JSON but executed through the same Bash
interpreter. Any config-admission guard must therefore cover
`crush.json`/`.crush.json`, not only `.crushrc` (§7a).

**Hazard B — credential disclosure over the socket.** `GET /v1/workspaces`
on Crush's local unix socket returns, per workspace, the full process
environment of the client that registered it — finding-020 measured 76
entries including `BEADS_DOLT_PASSWORD` (48 chars, write access to the bd
server) and both `SSH_AUTH_SOCK` paths. It needs a hostile **process**
running as the operator. (`GITHUB_TOKEN` and `ANTHROPIC_API_KEY` were absent
— auth delegation working, SOUL §3.)

They **compose**: a repo-supplied script runs *inside* the very environment
that the same harness *publishes* over its socket. One says a workspace can
run code; the other says a local process can read the constructed
environment that code runs in.

---

## 3. The aae-orc-5jj6 measurement: does the socket serve *other* clients' environments?

**This is the question that sets the severity of the whole writeup.** If
only the registering client can read its own environment back, this is close
to a non-finding (a process reading its own env is not an exposure). If any
same-uid process can read *another* client's environment, it is a real
cross-agent credential leak. finding-020 measured the endpoint returns *a*
client's full env; it did not measure *who else can ask*. The source answers
it unambiguously.

### 3a. One socket per uid, per host — shared by construction

The socket path (`internal/server/server.go`) prefers `$XDG_RUNTIME_DIR`,
else `os.TempDir()`, and the file is **`crush-<uid>.sock`**, with a fallback
to **`/tmp/crush-<uid>.sock`** when the composed path would exceed the
~104-byte `sun_path` limit. It is keyed by **uid**, not by client, not by
workspace, not by cwd. One `Server` binds one address and creates one
`backend.New(...)`. Every Crush client of that uid on that host that runs in
client-server mode talks to the **same** backend over the **same** socket.

### 3b. The list handler discards the request and returns every workspace

`internal/server/proto.go`:

```go
func (c *controllerV1) handleGetWorkspaces(w http.ResponseWriter, _ *http.Request) {
	jsonEncode(w, c.backend.ListWorkspaces())
}
```

The request parameter is `_`. The handler has **no** access to the calling
connection, so it cannot filter by caller as written (filtering would
require peer-cred/`ConnContext` plumbing that is absent);
`c.backend.ListWorkspaces()` takes no argument and returns the whole set.

### 3c. Each workspace carries the registering client's full environment

`internal/proto/proto.go`:

```go
type Workspace struct {
	ID       string         `json:"id"`
	Path     string         `json:"path"`
	YOLO     bool           `json:"yolo,omitempty"`
	Debug    bool           `json:"debug,omitempty"`
	DataDir  string         `json:"data_dir,omitempty"`
	Version  string         `json:"version,omitempty"`
	ClientID string         `json:"client_id,omitempty"`
	Config   *config.Config `json:"config,omitempty"`
	Env      []string       `json:"env,omitempty"`
	Channels []string       `json:"channels,omitempty"`
	Skills   []SkillState   `json:"skills,omitempty"`
}
```

`Env []string` is the `os.Environ()` "KEY=VALUE" form, transmitted by the
client at registration. `internal/workspace/client_workspace.go`
`recreateArgs()` sends `Env: ws.Env` to the server, "mirroring the startup
handshake". So each entry in the list response carries **that client's**
environment, keyed by workspace.

*(The exact `os.Environ()` capture call-site was not located in source —
grep.app was rate-limited — but it is not load-bearing: the `[]string`
field format plus finding-020's measured 76-entry block with real host
secrets establishes that the full process environment is what gets sent.)*

### 3d. There is no authentication on the socket, of any kind

- **No peer-credential check.** No `SO_PEERCRED`, `getpeereid`, or `ucred`
  anywhere in the server package. The socket does not check who connected.
- **The only "client id" is self-asserted and format-only.**
  `requireClientID` is:

  ```go
  func (c *controllerV1) requireClientID(w http.ResponseWriter, r *http.Request) (string, bool) {
  	cid := r.URL.Query().Get("client_id")
  	if cid == "" { /* 400 */ }
  	if _, err := uuid.Parse(cid); err != nil { /* 400 */ }
  	return cid, true
  }
  ```

  It reads a caller-supplied query parameter and validates only that it is a
  well-formed UUID. Any caller invents any UUID. It is a shape check, not an
  authorization check.
- **It is not even on the read paths.** Only
  `handlePostWorkspaceCurrentSession`, `handleDeleteWorkspaces`, and
  `handleGetWorkspaceEvents` call it; `handleGetWorkspaces` and
  `handleGetWorkspace` do not call it at all.

Scope of this claim: three specific absences are source-confirmed (no
`SO_PEERCRED`/`getpeereid`/`ucred`; `requireClientID` is format-only; it is
absent from the GET handlers), and route registration is direct
`mux.HandleFunc` (§3b, §3a). A server-wide auth *middleware* wrapping the
mux was not separately ruled out. But it does not change the exposure: even
if such middleware gated *connecting* to the socket, `handleGetWorkspaces`
still returns **every** workspace's env regardless of caller (it discards
the request), so any client that clears the gate reads all env blocks.
Middleware could restrict *who connects*; it could not make the list
response caller-specific. The severity rests on the handler shape, which is
confirmed, not on the absence of middleware, which is only audited.

### 3e. The only real gate is an OS socket permission Crush does not set

`internal/server/net_other.go` contains only `net.Listen("unix", address)`
— **no `os.Chmod`, no umask handling, no permission configuration.** So the
socket file's mode is whatever the OS assigns under the launching process's
umask. finding-020 measured `srwxr-xr-x` (0755) under a default umask; BSD /
macOS `connect(2)` requires *write* permission on the socket file, so 0755
restricts connections to same-uid processes.

**This restriction is umask-dependent and not enforced by Crush.** A Crush
started under a permissive umask (e.g. `0000`) would create a
world-writable socket connectable by *any* uid on the host. The same-uid
boundary is an accident of the operator's default umask, not a property
Crush guarantees. *(The umask-widening path is reasoned from the absence of
a chmod, not separately measured here.)*

**Two caveats that sharpen the boundary — one narrows the risk, one widens
it:**

- *The containing directory usually gates access more reliably than the
  socket file.* The preferred path parent — `$XDG_RUNTIME_DIR` on Linux,
  macOS `$TMPDIR` (`/var/folders/...`) — is mode `0700`, which blocks
  cross-uid access to the socket regardless of the socket file's own mode.
  So in the common case the umask-widening path above is largely academic.
- *The `/tmp` fallback removes that gate, and marvel can trigger the
  fallback.* When the composed path exceeds ~104 bytes Crush silently falls
  back to `/tmp/crush-<uid>.sock`, and `/tmp` is `1777` (world-writable,
  sticky). There the socket file's own mode is the only gate, so the
  umask-widening path becomes real. Because **marvel constructs the
  environment**, a long marvel-set `TMPDIR`/path can push the socket into
  `/tmp` — a marvel-triggerable downgrade from directory-gated to
  file-mode-gated. An adapter must pass `-H` explicitly (finding-020 already
  requires this) and choose a short, `0700`-parented socket path rather than
  inherit the fallback.

**Platform note.** This reasoning and finding-020's measurement are
**darwin/arm64**. The BSD `connect(2)` write-permission semantics and the
`0755` socket mode are macOS-observed. marvel is a Go service aimed at
multi-host (Linux) operation; Linux enforces socket-inode permissions on
connect too, but the containing-directory mode is the decisive gate there
as well, and none of this was verified on Linux. Treat the same-uid
conclusion as darwin-measured, Linux-unverified.

### 3f. Verdict on 5jj6

**Source-dispositive, and stronger than the graph claimed.** Every
structural fact is source-confirmed (shared socket, unfiltered handler,
per-workspace env, no audited auth); the composed *live* behaviour — B
reading A's env in a running deployment — is reasoned from those facts, not
re-measured (the empirical probe was not re-run; §9). It is dispositive
because the list handler discards the request, so no filtering is possible
regardless of what a probe would show. A same-uid process B — with no
shared environment, no credential, and no prior relationship to client A —
that connects to `crush-<uid>.sock` and issues `GET /v1/workspaces`
receives **every** registered workspace's full `Env`, including A's,
including any secret A carried. This is abuse path 1
(cross-agent env read) *and* abuse path 2 (any local process running as the
operator). The env returned is not the caller's own env read back; the
handler never looks at the caller. The ordering variant 5jj6 asks about
(B before A) does not matter: the backend serves whatever is registered at
call time.

The exposure window is not bounded by A's lifetime. finding-020 §6 measured
that a lifecycle observer holding the events stream keeps a workspace alive
past the 10–15s reap Crush would otherwise perform, and for a
marvel-spawned observer that outlives its agent this is a leak marvel
creates. Whether a workspace's `Env` is purged from the backend at client-A
exit was not established; if registrations persist, B reads A's secrets
*after A is gone*, a longer window than "live" implies.

| Property | Status | Evidence |
|---|---|---|
| Endpoint returns a client's full env (76 entries, real secrets) | **measured** | finding-020 §6 |
| Socket is one-per-uid, host-shared | **source-confirmed** | server.go path derivation |
| List handler returns ALL workspaces, cannot filter by caller | **source-confirmed** | `handleGetWorkspaces(_ *http.Request)` |
| Each workspace carries its registering client's env | **source-confirmed** | `proto.Workspace.Env []string` + `recreateArgs` |
| No peer-credential auth; client_id is self-asserted + format-only, not on GET | **source-confirmed** | `requireClientID`, absence of `SO_PEERCRED` |
| Same-uid-only reachability is umask-governed, not Crush-enforced | **source-confirmed (chmod absent)** | net_other.go, `net.Listen` only |
| B (same uid, no shared env) reads A's env, live | **source-dispositive; empirical probe not re-run** | §3b–3d compose |
| Permissive-umask widens socket to other uids | **reasoned, not measured** | chmod absent + BSD connect semantics |

---

## 4. Blast radius for a marvel-managed Crush adapter

State the actual marvel today, not the vision:

- **No Crush adapter exists.** `internal/runtime.NewRegistry()` registers
  Forestage, Claude, Codex, OpenCode, Simulator, Generic. There is no
  `crush` runtime. So **nothing is exposed today.**
- **Marvel has no per-workspace checkout directory in its model.**
  `api.Workspace` is `{Name string; CreatedAt time.Time}` — no path, no
  repo, no checkout. `tmux.Driver.NewPane` issues `new-window` with **no
  `-c`** start-directory; panes inherit the daemon's cwd. The "workspaces
  are checkouts of arbitrary repositories" property is *direction*, not
  built. Hazard A becomes live only when marvel gains a per-workspace
  checkout **and** launches Crush with that checkout as cwd.
- **Environment construction at spawn is marvel's one built enforcement
  locus.** `runtime.baseEnv` (and adapter additions) is where marvel decides
  what an agent can prove about itself — the heartbeat token travels only
  with the socket; loci 2 (runtime admission) and 3 (mid-flight revocation)
  do not exist. Permission-through-environment is the whole of what marvel
  enforces.

Against that, the two hazards land as:

- **Hazard B inverts the one enforcement locus.** The act by which marvel
  constrains an agent (constructing its environment, withholding variables)
  becomes the act that *publishes* the constraint's contents to every
  same-uid process, the moment the observability socket is enabled. A
  permission decision expressed as "do not give agent A variable X" is void
  if agent B can read A's whole env block. This is not a cost to price
  against the channel's benefit; SOUL §3 makes credential custody a
  boundary, and an inversion of the boundary has to be designed around, not
  weighed.
- **Hazard A executes before the locus applies.** A repo-supplied `.crushrc`
  runs as the marvel user, in marvel's constructed environment, before any
  permission model is running — and, via the `provider`/`model` builtins Crush
  documents for crushrc, can redirect which provider/model the session uses
  *after* marvel recorded the environment it constructed, leaving marvel's
  spawn-time record accurate about what it handed over and wrong about what
  ran (the router study's "fifth routing locus", aae-orc-eooi). For the
  factory tier — repositories grown from agent output — the repository is
  content an agent can write.

---

## 5. The generalization (why two arms found this without looking for it)

finding-020 §6 records the same shape a second time the same day: the
`harden-mr5c` arm established that `api.Session` reaches bbolt and every
`mrvl://` client through one encoder, so any credential on a store resource
is published by default (finding-022). Same shape, one serialization path
vendor-owned (Crush) and one ours (marvel):

> **A credential adjacent to a value that gets serialized is a credential
> that gets served. Code adjacent to data that gets loaded is code that gets
> run. The axis that predicts both is where the bytes go and what the loader
> does with them — not who was asked.**

The predictive question for any adapter marvel adds is therefore not "is
this harness trusted" but "what does this harness serialize, to whom, and
what does it execute at load."

---

## 6. The ruling (aae-orc-8ooq / aae-orc-xdg6): four options, why three lose

xdg6 named four options. The honest answer is layered, because the two
hazards have different owners and different time horizons.

1. **REFUSE until upstream gates it.** *Loses as stated.* crush#3410 makes
   the Bash-config deliberate — "until upstream" is "never" — and a
   marvel-side mitigation for hazard A exists (option 2). Permanent refusal
   is disproportionate to a hazard we can gate ourselves for the workspaces
   we control.
2. **MARVEL-SIDE CHECK (direnv model).** Refuse to launch when the workspace
   carries a Crush config file that is not operator-approved (absent, or
   matching a recorded hash). *Wins for hazard A, for controlled
   workspaces.* Small, ours, boring, degrades gracefully. **Incomplete on
   its own:** it does nothing for hazard B (the socket exposure is
   independent of any config file), and an *approved* config can still be
   malicious — approval means the operator vouches for it, which is only
   meaningful when the operator controls the workspace's contents.
3. **CURTAIN.** Launch only sandboxed. *Correct in principle; loses as the
   near-term gate.* curtain is design-only; gating the adapter on it is
   refuse-with-a-promise. But it is the **only** option that neutralizes
   hazard A for *untrusted* repositories, because for a repo whose contents
   no human vouches for, a config hash allowlist has nothing to check
   against. So curtain is the required gate for the factory tier, later.
4. **DOCUMENT AND SHIP.** *Loses outright.* The abuse needs only a hostile
   repository, which is the platform's core use case; a note in the adapter
   docs does not stop code from executing, and "the weakest of the four for
   a second user who will not read the note" is exactly right.

**The decision:**

- A Crush adapter **MAY be built**, and **MAY launch**, subject to the
  guards below. It is **not** categorically refused.
- **Guard 0 (hazard B), the dominant and cheapest control: default the
  observability socket OFF.** Hazard B exists only when the adapter sets
  `CRUSH_CLIENT_SERVER=1` (the flag is what makes any client register and
  the socket serve; finding-020 "For whoever writes the Crush adapter").
  Not setting it eliminates Hazard B entirely. marvel does not need Crush's
  socket for the launch itself — it is the *observability* channel, and
  finding-020 §7 shows a Crush reader can be built from the side-effect-free
  `session … --json` CLI surfaces with no server at all. So: do not enable
  the socket unless marvel positively needs the live channel for that
  session; where it does, Guard 2 applies on top.
- **Guard 1 (hazard A), REQUIRED before any launch:** the direnv-model
  config allowlist (option 2). No launch in a workspace whose Crush config
  files are not operator-approved. This is the *only* thing that bounds
  Hazard A's blast radius short of curtain — see the next point.
- **Guard 2 (hazard B), REQUIRED whenever the socket is enabled:** **no
  secret in the constructed environment.** Keep secrets out of
  `baseEnv`/adapter env for a Crush launch that sets `CRUSH_CLIENT_SERVER=1`.
  Spawn-env hygiene at enforcement locus 1, not a per-channel cost.
  **Scope limit, stated plainly:** Guard 2 bounds only what is *published
  over the socket* (Hazard B). It does **not** bound what executed config
  code (Hazard A) can reach: a `.crushrc`/`crush.json` runs as the operator
  and touches `SSH_AUTH_SOCK`/the ssh-agent, `~/.ssh`, `~/.aws`, the macOS
  keychain (`security`), cloud metadata endpoints, and the whole filesystem
  — none of which live in the env and none of which Guard 2 removes.
  Guard 2 is not a mitigation for Hazard A. Only Guard 1 (don't run
  unapproved config) and curtain (sandbox the process) bound that surface.
- **Factory-tier carve-out:** for workspaces whose contents marvel or the
  operator do **not** control (the factory tier — the case marvel most wants
  a second harness for), Guard 1 has nothing trustworthy to check against.
  Such workspaces require **curtain** (option 3) and must not get a Crush
  launch until curtain can sandbox it — and that sandbox must **also isolate
  the per-uid socket** (a mount/PID/`TMPDIR` namespace, or no socket),
  because same-uid factory agents sharing one `/tmp` re-open Hazard B among
  themselves even when each repo is sandboxed for Hazard A. Naming the tier
  is mandatory, per 8ooq: "accept" without naming which tiers is not an
  answer.

This lands primarily on **option 2 now** (controlled workspaces) **plus
socket-off-by-default and a spawn-env rule**, with **option 3 required
later** (untrusted workspaces, and socket isolation). Option 1 and option 4
are rejected with the reasons above.

---

## 7. The mitigation — implementable in shape; correctness gated on the must-verify items

### 7a. Guard 1 — Crush config admission (direnv model)

A pure, dependency-light inspection the future Crush adapter's `Prepare`
runs against the workspace directory before returning a `LaunchResult`;
returning `(nil, err)` refuses the launch. Shape:

```go
// package crush (internal/runtime/crush), when the adapter is built.

// crushConfigFiles is Crush's documented project-config precedence set
// (finding-020 §9): closest-to-cwd wins among these names.
var crushConfigFiles = []string{".crushrc", "crushrc", ".crush.json", "crush.json"}

// InspectConfig reports, for the directory tree Crush will read config from,
// each present config file and its sha256. approved maps relpath -> sha256
// the operator vouched for. A file present but not matching an approved
// hash is a refusal.
func InspectConfig(cwd string, approved map[string]string) (verdict, error) { ... }
```

**Must-verify before implementing** (do not assume):

1. **Does Crush walk *up* from cwd to find config, or read only cwd?**
   finding-020 says "closer-to-cwd winning", which implies a walk. The scan
   must cover exactly the directories Crush searches, up to (and not beyond)
   the workspace root. Read `internal/config` at the pinned tag to fix the
   walk boundary; a scan that is narrower than Crush's search is a bypass.
2. **`crush.json` / `.crush.json` are Bash-backed too — confirmed
   upstream.** crush#3410's own description states `crush.json` is backed
   by the same Bash interpreter as `crushrc` and is "trusted code". So the
   JSON-named config is not inert data; treat all four config files as
   executable-bearing and do not allowlist only `.crushrc`. Separately,
   these files can declare `hooks`, and whether project-level hooks fire
   without a trust step is *unestablished* (finding-020 "What was not
   established"); the codex arm showed an untrusted hook can fail
   *silently* — no stderr, no log, no nonzero exit — so "I didn't see it
   run" is not "it didn't run".
3. **TOCTOU:** run the check as close to spawn as possible, and only for
   workspaces marvel is not concurrently writing. Marvel owns the workspace
   dir, so this is low-risk, but the window is real if a workspace is being
   populated while a launch is dispatched.
4. **Version coupling:** the config-file set and the search walk are
   Crush-version-specific. Pin the Crush version the adapter supports and
   re-verify #1/#2 on upgrade; a new config path in a later Crush is a new
   bypass.
5. **Case-insensitive / unicode-normalizing filesystems.** The measured
   target is darwin/arm64; APFS is case-insensitive and unicode-normalizing
   by default, so `Crush.json` or `.CRUSHRC` will load while an exact
   lowercase-string match misses them. The scan must match case-insensitively
   (and normalize unicode), not compare fixed strings.
6. **Symlinks.** A config file that is a symlink can point outside the
   scanned tree (evading the walk boundary) or have its target swapped after
   approval (a TOCTOU on the link *target*, distinct from #3's spawn-window
   TOCTOU). `InspectConfig` must resolve and re-check link targets, or refuse
   symlinked config outright.
7. **Transitive sourcing — the hash bounds only the entry file.** An
   approved `.crushrc`/`crush.json` is Bash and can `source ./x`,
   `. "$(cat y)"`, or glob-source `*.sh`. The sha256 covers the entry file,
   not what it loads, so an approved-benign config that later pulls in a new
   or mutated sourced file executes unapproved code under an unchanged,
   still-"approved" hash. This is the classic direnv weakness the model
   inherits; a hash allowlist is not a sandbox.

**What Guard 1 does NOT cover:**

- **The global/user Crush config is out of scope and is a persistence
  vector.** Guard 1 scans the *workspace*; Crush also loads a user/global
  config (`CRUSH_GLOBAL_CONFIG`, default `~/.config/crush/`), which is
  Bash-backed too. A clean workspace still executes global Bash — and worse,
  Hazard-A code from *any* single run can *write* the global config,
  compromising every future launch, including ones over clean, controlled
  workspaces. The adapter must also pin/own the global-config surface
  (marvel controls `CRUSH_GLOBAL_CONFIG`; point it at a marvel-owned,
  read-only-to-the-agent location, or hash-pin it too). Without this, Guard 1
  is bypassable by first poisoning the global config.
- Hazard B (the socket) — that is Guard 0 + Guard 2.
- Hazard A's ambient credential surface once approved config *does* run —
  that is curtain (see §6 Guard 2 scope limit).
- An approved-but-malicious config (approval = operator vouches, only
  meaningful for controlled workspaces).
- A Crush that discovers config by a path this set misses.
- Anything for untrusted/factory-tier workspaces (that is curtain).

### 7b. Guard 2 — spawn-env hygiene

For any Crush launch with `CRUSH_CLIENT_SERVER=1`, the constructed
environment must carry no secret. Concretely: non-secret identity and flags
belong in the env (they already do — `MARVEL_*`, `BEADS_ACTOR`); the
heartbeat token and anything credential-shaped must not be. **"Credential-
shaped" includes path-shaped credential *handles*, not just literal secret
values** — `SSH_AUTH_SOCK` is a path, but a path to a live ssh-agent socket
that grants signing/auth, so it must be excluded (or the agent isolated),
the same way a bearer token would be. finding-020 measured both
`SSH_AUTH_SOCK` paths in the served block; a rule that waves "paths" through
would leave the exact handle it flagged exposed. The classification test is
"does possessing this env entry confer access", not "does it look like a
random string". This is a rule *at the shared spawn path* (enforcement locus
1), because the exposure is a property of what marvel puts in the
environment, not of the Crush adapter's command line — see §7c on where it
belongs. adapter.go's package comment already carries the seed of this rule
("secrets belong somewhere a harness cannot serialize"); Guard 2 is that
sentence made a launch precondition. **Guard 2 closes only the socket
publication (Hazard B); it does not bound what executed config reaches (§6
Guard 2 scope limit).**

### 7c. Where the checks belong — and the frontier question xdg6 asked for

xdg6 asked whether the check belongs in the adapter or in the shared spawn
path, and warned that if the latter, it "grows into a general workspace
admission question and should be said so rather than smuggled in." Both are
true, split cleanly:

- **Guard 1 is Crush-specific and belongs in the Crush adapter.** Only Crush
  executes `.crushrc`; the config-file set and search walk are Crush's. It
  reads `LaunchContext` (which will need to carry the workspace directory —
  it does not today) and refuses in `Prepare`.
- **Guard 2 is not Crush-specific and belongs in the shared spawn path.**
  "Do not serialize secrets into an agent's environment" is a property of
  every harness that can serve or persist its environment (finding-020 §6
  Crush, finding-022 marvel's own store). It is enforcement-locus-1 hygiene.
- **The general case is a real frontier question, named not smuggled:**
  *workspace admission* — each adapter declares which workspace-resident
  files it treats as trusted-executable at load (Crush: the four config
  files; codex: `.crush.json`-style hooks that fail silently; others TBD),
  and marvel gates launch on operator approval of those files uniformly,
  where a per-workspace checkout directory first makes the check possible.
  Per Winston's Rule-of-Three: do **not** build that framework now (two
  instances: Crush, codex). Build Guard 1 for Crush when the adapter lands;
  extract the shared admission check when a third adapter needs it. Recommend
  filing a frontier node `question-workspace-admission` (or folding it into
  the existing curtain / credential-plane frontier) so the generalization is
  tracked rather than reinvented per adapter.

### 7d. Why no code ships in this PR

The guard has no caller and no input source yet: there is no Crush adapter,
and `api.Workspace` has no directory for `InspectConfig` to scan. A
caller-less guard checking a synthetic path, against a data model that does
not represent workspace directories, would be dead code liable to diverge
from the real adapter. The disciplined deliverable is this finding + the
ruling + the implementable spec above (xdg6's "a mitigation specified well
enough to implement, plus what it does NOT cover"). The finding is the gate
on aae-orc-6c2r; Guard 1 is implemented when the adapter and a workspace
directory land; Guard 2 is a launch precondition wired at the same time.

---

## 8. Decisions returned to the operator (aae-orc-x007 a–e)

- **(a) Is the shared-socket claim true?** **Yes — source-confirmed at
  v0.88.1 (§3).** One per-uid host-shared socket; `GET /v1/workspaces`
  returns every workspace's full env to any connecting same-uid process, no
  auth. Abuse path 1 holds. Stronger than the graph claimed: the handler
  cannot filter by caller because it discards the request.
- **(b) Is this a Crush issue or a marvel design constraint?** **Both, and
  neither is an upstream vulnerability report.** Hazard A is documented and
  deliberate (crush#3410); hazard B is documented local-trust behaviour on a
  same-uid socket. Filing a CVE/GHSA against documented local-trust
  behaviour would be wrong and would pollute a shared database others depend
  on — the prior NO on self-filing a GHSA stands (no affected marvel user,
  no adapter). The honest upstream ask, *if* made, is a **feature request**
  for a config-trust gate (direnv `direnv allow`, VS Code Workspace Trust,
  git hardened hooks — each started where Crush is now), run through the
  upstream-claim-gate first. For marvel it is a **design constraint**: the
  two guards above.
- **(c) Survey codex/claude/opencode for the same property?** **Recommended,
  yes** — hazard B is "does the harness serialize its environment to a
  reader", a per-adapter question. The codex arm already found the adjacent
  silent-hook behaviour. Worth a short sweep, scoped as its own probe.
- **(d) Which marvel-side rule?** x007(d) offered three: secrets-out-of-env,
  per-adapter scrubbing, and gating the local-API channel behind opt-in. The
  answer is **the first and third, layered, with the config allowlist on
  top**: gate the local-API channel behind opt-in (**Guard 0 — socket off by
  default**, the dominant and cheapest control for Hazard B), keep secrets
  out of the constructed environment when it *is* on (**Guard 2**), and gate
  the launch itself behind the config allowlist (**Guard 1**, for Hazard A).
  Per-adapter *scrubbing* is the weaker option and is subsumed by Guard 2
  (don't put it there in the first place, rather than scrub it after).
- **(e) Does this raise the priority of question-policy-credential-plane
  (unowned)?** **Yes.** This is the first *measured* cost of that plane's
  absence: marvel's one enforcement locus is credential-custody-by-
  environment, and both hazards are that custody failing — published (B) or
  executed against (A). The credential plane is where "agents never hold
  secrets; a custody service grants scoped access as the exception" would
  make Guard 2 structural rather than a per-launch discipline.

---

## 9. What was not established / probe hygiene

- The empirical rig experiment 5jj6 describes (register from A, read from an
  unrelated B) was **not re-run** — crush is not installed on mokuzai and
  the source is dispositive (§3f). A kinu (or any) session with crush
  v0.88.1 installed can confirm in ~10 minutes; expected result: B sees A's
  marker. Run it with `CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1` and
  `CRUSH_GLOBAL_DATA`/`CRUSH_GLOBAL_CONFIG` pointed at a scratch rig, per
  finding-020's probe-hygiene note (a bare `crush` start rewrites the host's
  `~/.local/share/crush/hyper.json`).
- The `os.Environ()` capture call-site was not located in source (grep.app
  rate-limited); inferred from the `Env []string` field and finding-020's
  76-entry measurement (§3c).
- The permissive-umask socket-widening path (§3e) is reasoned from the
  absence of a chmod plus BSD `connect(2)` semantics, not separately
  measured.
- Whether project-level `.crush.json` hooks require a trust step remains
  **unestablished** (carried from finding-020), and the codex silent-no-run
  precedent means absence of a visible gate is not evidence of no gate —
  §7a #2 requires resolving it before Guard 1 is trusted.
- **Whether a workspace's `Env` is purged from the backend at client
  exit.** Not read from source; if registrations persist past client death,
  the exposure window outlasts the agent (§3f).
- **The global/user Crush config surface** (`CRUSH_GLOBAL_CONFIG`,
  `~/.config/crush/`) was not inspected as a code-execution or persistence
  vector here; §7a flags it as out of Guard 1's workspace scope and requires
  the adapter to pin/own it, but the exact global load + write paths at
  v0.88.1 were not read.
- No source, config, or credential of the operator was touched by this
  work: it was source reading and marvel-repo inspection only.

---

## Cross-references

- marvel finding-020 §6 (env exposure), §9 (`.crushrc` execution), "What was
  not established → Multi-workspace behavior" (the gap this closes)
- marvel finding-022 (the store-serialization seam — same shape, ours)
- `internal/runtime/adapter.go` package comment (the Crush notes this ruling
  resolves)
- bd: aae-orc-5jj6 (measurement), aae-orc-x007 (review), aae-orc-8ooq
  (launch decision), aae-orc-xdg6 (study), aae-orc-6c2r (the adapter this
  gates), question-policy-credential-plane (the plane this is the first
  measured cost of)
- Crush source, tag v0.88.1: `internal/server/{server,proto,net_other}.go`,
  `internal/proto/proto.go`, `internal/workspace/client_workspace.go`;
  charmbracelet/crush#3410 (Bash-config made deliberate)
