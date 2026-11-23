# Seats do not read the macOS privacy folders: a default deny profile

- **Status:** built in the pull request that carries this note (marvel#1574), 2026-11-23. Measured on one darwin host. The seat-run proof in section 11 has not been run.
- **Scope:** darwin seats. Linux is a no-op (section 7).

## 1. Why

A seat that walks the home folder (`du -sh ~`, a `find` from `~`) makes macOS ask "marvel would like to access ..." for Music, Photos, Documents and the rest. The prompts are noise to the person at the keyboard, and they read as the daemon reaching for files it has no reason to touch. Denying those folders to a seat at launch makes the walk fail quickly and quietly, and no prompt is raised.

The cost is one thing that does not fit: a seat cannot start its own `sandbox-exec` once it runs under a profile (section 5). That decides the shape of the opt-out, so it comes first in the proposal.

## 2. Mechanism

macOS TCC charges a privacy request to the responsible process. For a command a seat runs, that is the marvel daemon (bundle id `com.arcavenae.marvel`), not the command. So `du` run by a seat is reported as marvel reading the folder. A seatbelt denial happens in the kernel before TCC is consulted, so a read the profile refuses never reaches the privacy prompt.

## 3. The folder list

Denied for both `file-read*` and `file-write*`, as `subpath` rules under the seat's real home:

| path under `~` | guarded by TCC | note |
|---|---|---|
| `Desktop`, `Documents`, `Downloads` | yes, per folder | documented by Apple |
| `Library/Mobile Documents` | yes (iCloud Drive) | documented by Apple |
| `Library/Calendars`, `Library/Mail`, `Library/Messages`, `Library/Safari` | yes (full disk access class) | documented by Apple |
| `Library/Application Support/AddressBook` | yes (Contacts) | |
| `Pictures` | the Photos library inside it is; the folder itself may not be | denied whole: seats have no use for it |
| `Music`, `Movies` | not shown to be file-guarded; the media APIs are | denied whole for the same reason; **unverified as TCC classes** |

Network volumes are a TCC class too ("access files on a network volume"). They are denied by path, not under `~`:

| path | note |
|---|---|
| `/Volumes` | every external and network volume mounts here |
| `~/OrbStack` | on the measured host, the only network mount, an NFS share inside `$HOME`; a `du` from `/Users` crossed into it |
| every mount whose type is `nfs`, `smbfs`, `afpfs` or `webdav` | found at launch, see below |

**Finding them at launch.** Marvel reads the mount table when it stages the profile (`getfsstat`, the same list `mount` prints: on the measured host `OrbStack:/OrbStack on /Users/<me>/OrbStack (nfs, ...)`) and adds a `subpath` deny for each mount point whose type is one of the four. A mount that is already there is covered, including one outside `/Volumes` and `$HOME`.

**A mount that appears after the seat starts.** A profile is fixed when the seat starts, so a network mount made later outside `/Volumes` and `~/OrbStack` is not denied, and a seat that walks into it raises the prompt. Marvel does not rewrite a running seat's profile. The next start of the seat reads the mount table again and covers it. A mount under `/Volumes` is covered from the first start, since the whole path is denied. This gap is stated in the manifest docs, and it is not closed by polling.

The first four home rows are the classes Apple documents. The last two rows are over-inclusive on purpose: a deny that was not needed costs nothing, and a missing deny costs a prompt. I did not trigger a prompt to test any single row, because the measurement in section 9 is meant to produce none. The list is data in one Go table, so a row can be added or dropped without touching the mechanism.

## 4. Launch and effect on tmux and children

The prepared seat command already runs under `sandbox-exec` when a role names `sandbox_profile` (`internal/session/manager.go`, the staged copy). The default deny reuses that path: the wrapped command is the pane's command, so tmux itself is outside the profile and the seat and every child it starts is inside it. A profile applies to the process and is inherited by everything it forks; no child can lift it. `tmux` clients started by the seat run under the profile, and the tmux server they reach does not.

A seat whose `workdir` lies under a denied folder (a user who keeps code in `~/Documents`) would fail every command. The seat's working directory is carved out of that folder's deny with `require-not`, so the rest of the folder stays denied. The block never contains an allow rule: a later allow would lift a deny that the role's own profile put on the same tree (measured under the real `sandbox-exec`: a role profile that refuses writes to the tree returned rc 0 once an allow followed it, and a refusal with the carve-out). Other trees a manifest names under a denied folder (a company directory, a settings file) are read by the daemon, which is not under the profile, so they are unaffected.

Staging the profile is a guard, not containment, so a failure to stage it (the profile directory cannot be written, the command is not one simple command, the home is unknown, `sandbox-exec` is not at its path) is logged and the seat starts without it. Refusing every seat over a nuisance guard is worse than a prompt. The failure is not silent: the reason is stored on the seat (`tcc_deny_note`, copied onto the stored session at launch) and `marvel doctor` prints a `WARN tcc-deny` line that names the seat and the reason. A test pins that the profile directory and the home both resolve on a normal install, so the path stays rare. A role that names its own `sandbox_profile` is not in this path: it already refuses to start when its profile cannot be staged.

## 5. Nesting: an inner `sandbox-exec` fails under an outer profile

Reviewer and builder seats run `sandbox-exec` for the run conditions (network deny, write deny, and a refused-connect control). Measured on Darwin 27.2.0, `sandbox-exec` from `/usr/bin`:

| outer profile | inner profile | result |
|---|---|---|
| `(allow default)` | `(allow default)` | applies |
| deny rules for the folders in section 3 | `(allow default)` | `sandbox_apply: Operation not permitted`, exit 71 |
| deny rules for the folders in section 3 | the same file as the outer | applies |
| deny rules for the folders in section 3 | outer rules plus `(deny network*)` | `sandbox_apply: Operation not permitted`, exit 71 |
| `(allow default)` | `(allow default)(deny network*)` | `sandbox_apply: Operation not permitted`, exit 71 |
| one unrelated file deny | the same plus one more file deny | `sandbox_apply: Operation not permitted`, exit 71 |

An inner profile applies only when it is the same as the outer. Any other profile, including a stricter one, is refused. So under a default deny, a run-conditions profile (network deny, write deny, per-run listener denies) cannot start. This is a blocker for builder and reviewer seats, not a detail.

Shapes that avoid it, in the order I would take them:

1. **Opt out the roles that nest.** A role that runs `sandbox-exec` sets the opt-out (section 6). Those seats keep the prompts, and are the ones most likely to walk a home folder, so this protects the least. It is the only shape that breaks nothing today.
2. **Fold the deny block into the run-conditions profile.** The profile generator for a run writes the section 3 denies as its first lines. The seat is unwrapped, so the test run is covered and the rest of the seat is not.
3. **A launcher the run conditions call.** A marvel-owned wrapper takes the run's profile, appends the deny block, and starts the seat's test command under that one profile. Same coverage as 2 with one place to change the list.

4. **A daemon-side run endpoint.** A seat asks marvel to run a command; the daemon starts it outside the seat's profile, under one profile it composes (the run's profile plus the deny block), and returns the output and exit status. The seat itself stays under the deny block, so every seat is covered, and a sandboxed run still works because it is never nested.

   Measured: a process the daemon starts is not inside the seat's profile. A seat under the section 3 profile ran `ls ~/Pictures` and got `Operation not permitted` (exit 1). The same seat connected to a unix socket (allowed, since the profile does not deny it) served by a process outside the profile, and that process's `ls ~/Pictures` returned exit 0. A profile follows a process and its forks, not a request made to another process.

   Costs, each a thing the first build must settle:
   - **Escalation.** The daemon is not sandboxed, so a seat that can name any profile can name a permissive one and so leave its own. The endpoint must compose, not obey: the role's own profile, then the requested run profile, then the deny block, one text, with the block last. A requested profile is also checked against the role's, and a request the role could not have made itself is refused.
   - **Authentication.** The seat proves who it is (the heartbeat token it already carries) and the daemon reads the role from its own record, never from the request.
   - **Fidelity.** Working directory, environment, stdin and tty, signals, timeouts, exit status and streamed output must match a direct run, or the run-conditions scripts change behavior.
   - **Lifetime.** The daemon owns the child, so it must kill it when the seat's request ends or the seat dies.
   - **Surface.** A new socket call on the daemon, and the run-conditions scripts call it in place of `sandbox-exec`.

   Options 1 to 3 give up coverage on the seats that nest. Option 4 does not, which is why it is the one that meets the "every seat" bar in section 11.

My recommendation is 4, with 1 as the stopgap until it lands, since 1 is the only shape that cannot break a run today. It is a recommendation, not a decision.

Claude Code's own seatbelt on its Bash tool was not present on the seat I measured from (a top-level `sandbox-exec -p '(version 1)(allow default)'` applied). A seat that does run under it would be in the nested case above, and the same shapes apply.

## 6. The opt-out field

A role field `tcc_deny`, a boolean on the role beside `sandbox_profile`. Absent means on (deny). `tcc_deny = false` skips the default block for that role. A role that sets it keeps today's behavior and its prompts. It is documented in the admin guide. A user who never sets it never sees a prompt caused by a seat. A role that runs `sandbox-exec` itself sets it, and carries the same rules in its own run profile (section 13).

## 7. Linux

No-op. The profile is generated only when `runtime.GOOS` is darwin, as `sandbox_profile` is. A linux seat with `tcc_deny` set parses and does nothing.

## 8. Composition with `sandbox_profile`

`sandbox_profile` is the absolute path of a profile the role supplies (`internal/api/manifest.go`, key ratified 2026-11-03), empty meaning none. The default deny composes in three cases:

- No `sandbox_profile`, deny on: marvel stages a profile of `(version 1)(allow default)` plus the deny block.
- A role profile, deny on: the staged copy is the role's text followed by the deny block. Seatbelt lets a later rule win, so the denies come last and a role's own `(allow default)` or broad allow cannot re-open the folders.
- Deny off: the role's profile alone, as today.

Two adapters read a non-empty `LaunchContext.SandboxProfile` as "this seat is contained" and relax their own rules (`internal/runtime/goose_posture.go`, `internal/runtime/opencode_config.go`). A profile that only blocks six folders is not containment. The default block therefore must not set that field; the build carries a separate flag for it.

## 9. Measurement

Read only. A child process I started, not a live seat; profile in a scratch directory; no write controls. The profile is the section 3 denies over `(allow default)`.

```
$ sandbox-exec -f outer.sb /bin/ls ~/Pictures
ls: /Users/<me>/Pictures: Operation not permitted            (exit 1)
$ sandbox-exec -f outer.sb /usr/bin/du -sh ~
du: /Users/<me>/Music: Operation not permitted               (exit 1, no total printed)
$ log show --start "2026-11-23 09:46:27" --predicate 'process == "tccd"'
```

With the network rows added (`/Volumes`, `~/OrbStack`), a walk of the share, run with `du -x` and a named root:

```
$ sandbox-exec -f outer2.sb /usr/bin/du -x -sh ~/OrbStack
du: /Users/<me>/OrbStack: Operation not permitted            (exit 1)
$ sandbox-exec -f outer2.sb /bin/ls /Volumes
ls: /Volumes: Operation not permitted                        (exit 1)
```

The `tccd` log for that window held no line naming `du`, `marvel`, `network` or `OrbStack`.

Disclosure: the first measurement above ran `du -sh ~` under a profile that did not yet deny `~/OrbStack`, so that walk entered the share (`du` reported a cyclic symlink under it). The `tccd` log showed no marvel line for it, but it is the case this note is about, and the later runs name their roots and use `-x`.

For the first measurement, the `tccd` log for that window held 45 lines; none names `du`, `ls` or `com.arcavenae.marvel`, so no new TCC request was raised for marvel. The positive control is the host's own record: `tccd` at 09:41:45 the same day, responsible `com.arcavenae.marvel`, accessing `/usr/bin/du`. I did not repeat that run without the profile, since it would raise the prompt this change removes.

With the profile marvel renders for the measured host (the section 3 rows, and the NFS mount the table reports, `~/OrbStack`), a read-only run of `ls ~/Pictures`, `ls ~/Music`, `ls ~/Desktop`, `du -x -sh ~/OrbStack` and `ls /Volumes` each gave `Operation not permitted` (exit 1), `du -x -sh` of the seat's own working tree gave a size (exit 0), and `ls ~/.marvel` listed it (exit 0). The `tccd` log from 09:57:42 held no line naming `marvel`, `du`, `ls`, `OrbStack`, `network` or `AUTHREQ`.

One thing to note: `du -sh ~` under the profile exits 1 and prints errors for the denied folders. A seat that treats a nonzero `du` as failure will see one. That is the intended "fail quickly and quietly", and the build documents it.

## 10. What the change builds

Built in this change: the path table and the block renderer (`internal/session/tcc_deny.go`); the mount table read through `getfsstat` on darwin, and nothing elsewhere; the `tcc_deny` role key, in TOML and YAML, absent meaning on; staging of the deny alone for a role with no profile, and appended after a role's own profile for one that names it; the manager switch the daemon sets; the admin-guide section. Tests came first: the block names every row and mount, drops a path that would break the quoting, allows the working directory back after the deny, comes after a role's own allow, leaves an opted-out profile byte for byte, does nothing on Linux or with the switch off, and is not read as containment by the goose posture rule.

Not built: option 4 (section 5), which goes to the operator as its own decision.

A test that runs the real `sandbox-exec` with the rendered profile is not in the suite, because a run under the run conditions is already inside a profile and cannot start another (section 5). The rendered profile for the real home was run once by hand, outside a test, as section 9 records.

## 11. What a user must see

The bar is that a marvel user does not face these prompts at all:

1. **On by default.** On macOS every seat marvel launches gets the section 3 deny, with no setting for a new user to touch. `tcc_deny = false` exists, is off by default, and is named in the manifest reference.
2. **Marvel's own code does not walk those folders either** (section 12).
3. **Proof from seats marvel launches**, on the host that raised the prompts. It needs no new macOS user and no script run in a terminal, because a terminal would be the responsible app and not marvel. It compares `tccd` log lines, not dialogs, because a stored answer can make a request silent while it is still logged. `tccd` names every seat of the daemon by one bundle id, `com.arcavenae.marvel`, so a log line cannot say which seat raised it; the windows below are what attribute it. It runs after the build exists and after the person at that keyboard has answered the network-volume dialog they were already shown; if a dialog still appears, it is that one, and the report says so.
   - A **proof seat**, a role with the default deny, runs `du -x -sh ~`, `ls ~/Pictures ~/Music ~/Desktop` and `du -x -sh ~/OrbStack`, then marvel's own `marvel vitals`, `marvel doctor` and `marvel get sessions`.
   - A **control seat**, a role with `tcc_deny = false`, runs `ls ~/OrbStack` and nothing else. `~/OrbStack` is a network volume, so the control tests the network-volume class this change adds. It is expected to log an `AUTHREQ_ATTRIBUTION` line for `com.arcavenae.marvel`, including when a stored "Don't Allow" answers the request without a dialog. That is an expectation, not a measurement: no run has shown it yet, and the first control run is what measures it.
   - Both read the log from before the run: `log show --start "<time>" --style compact --predicate '(process == "tccd" AND eventMessage CONTAINS "com.arcavenae.marvel") OR process == "SecurityAgent"'`, and keep every line.
   - The pass bar is scoped to TCC and nothing else: in the proof seat's window, no `tccd` `AUTHREQ` line for `com.arcavenae.marvel` and no `SecurityAgent` prompt; in the control's, at least one `tccd` `AUTHREQ_ATTRIBUTION` line for it. Other subsystems' lines do not count either way. The two windows are separate and each records its start time.
   - Because the bundle id is shared, a seat that opted out and touches a protected folder inside the proof window would fail the proof, and the same seat inside the control window would satisfy the control. So the host is quiet for the run: before each window, `marvel get sessions` lists the seats, every opted-out seat other than the control is idle (no command in flight, none sent during the window), and the list is taken again after the window. A line in the window that a quiet host does not explain is reported with its time, and the window is run again.
   - The roles to cast are two entries in one team, `tcc-proof` (default) and `tcc-control` (`tcc_deny = false`), each a shell seat that takes its commands from the person casting it. Casting them is a manifest change on the host, made outside this repository.
4. **Release notes**, one line: "macOS seats no longer read the protected folders or network volumes, so a seat's `du` or `find` no longer raises a privacy prompt."

Linux is unchanged.

## 12. Audit of marvel's own file walks

Method: a search of the non-test Go source for `filepath.Walk`, `filepath.WalkDir`, `filepath.Glob`, `os.ReadDir`, `Statfs`, `UserHomeDir`, and `exec.Command` of `du`, `find` or `df`. Each hit is read for its root. This is a search, not a proof that no other path exists.

| site | root it walks | reaches a protected folder or a network mount |
|---|---|---|
| `internal/view/view.go` (`makeReadOnly`, `forceRemove`, `sealTree`) | a view tree under marvel's own home | no |
| `internal/company/treehash.go` (`TreeHash`) | a company recipe directory the caller names | only if the recipe sits under a protected folder |
| `internal/daemon/watchdog.go` (`findTranscript`) | `<config dir>/projects`, one level | no; it is under `~/.claude` or `CLAUDE_CONFIG_DIR` |
| `internal/session/base_sweep.go`, `home_sweep.go` | the session scratch base and per-seat homes | no |
| `internal/api/trusted_folders.go`, `internal/config/config.go`, `internal/session/projection.go` | `~` is expanded, then a named path is read or checked; nothing is enumerated | only when a manifest names a path under a protected folder |
| `internal/bus/headroom.go` | `Statfs` of the bus store directory; it enumerates nothing | no |
| `internal/procstat`, `internal/suspend`, `internal/procargs` | `ps` and `lsof -d cwd -p <pid>`; no file tree is opened | no |
| `internal/hostvitals`, `cmd/marvel/doctor.go` | no walk found; `doctor` runs the commands it is given | not found by this search |

Result: no marvel path enumerates `~`, `/`, `/Users` or `/Volumes`. The one way marvel's own code can reach a protected folder is a manifest that names one: `workdir`, `workspace.root`, `trusted_folders`, a company directory or a settings file under `~/Documents` or `~/Desktop`. The launch check in section 4 covers `workdir`; the same check belongs on the others, and the build adds it so the daemon fails with a message instead of raising a prompt. Nothing needs fixing in a walk, and this section says so on the evidence of the search above and the reading of each root, not on a prompt-free run of the daemon.

## 13. Outside marvel: what must change before this ships

The default is on for every seat, so a role that runs `sandbox-exec` itself breaks the day a marvel with this change reaches its host unless the role opts out first. These places are outside this repository, are not edited here, and need their own changes:

1. **The manifests on each macOS host**: every role whose seats run `sandbox-exec` for the run conditions (builder and reviewer roles) gets `tcc_deny = false`.
2. **The run-profile generators**: the scripts that write the per-run profile for the run conditions add the section 3 deny block (and the mount rows, read from the mount table at run time), so a test run stays covered while its seat is not.
3. **The run-conditions rule text** in the fleet's rule files and skills, so a new generator carries the block.

What an older marvel does with the key: nothing. The manifest decoders drop a role key they do not know, in TOML and in YAML. The one place an unknown key is refused is the `runtime.goose` table (`unmarshalManifestTOML` in `internal/api/manifest.go`). That leniency is older than the build on the measured host: the comment that states it is dated 2026-11-01 by `git blame` on `main`, the decoder ignoring unknown keys is older than the comment, and the installed build is from 2026-11-21. I read the parser and ran nothing live, so the claim is about the source, not about each installed binary.

So a manifest that carries `tcc_deny = false` parses on a marvel that predates this change and is ignored there, which is also why a misspelled key fails silently. That makes the safe order:

1. Release the run-profile generators with the deny block, and apply the manifests with `tcc_deny = false` on the nesting roles. An older marvel ignores the key and keeps running those roles as before, and the generated run profiles already carry the block.
2. Install the marvel with this change. Its default-on deny reaches seats only as they restart, and the opted-out roles skip it.
3. Re-apply every manifest with the new marvel, before any nesting seat restarts. The daemon keeps the roles an older marvel applied, and that marvel dropped `tcc_deny` when it read the manifest, so a stored role has no value for it, which reads as on. Until the re-apply, a restarting builder or reviewer seat starts under the deny and its own `sandbox-exec` fails with exit 71. A check for this was not built: a stored role cannot tell "the manifest said nothing" from "an older marvel dropped the key", so the step is part of the order, and the first thing to look at when a nesting seat fails to start after an upgrade.

A release flag that manifests opt into (a shape where the deny ships off) is not needed, because the key does not have to exist before anything relies on it. It would also leave the default off for a user who changes nothing, which is the case this change is for. The cost of the order above is that step 1 cannot be confirmed by a seat on an older marvel, since the key does nothing there; the check is that the key reads back from the applied role after step 2.

The seat-level shells of those roles are the remaining gap: a command a builder types outside a test run is not covered, so it can still prompt. A rule for the seat's own shell commands covers it until option 4 exists.

## 14. Security read of option 4

A broker that runs a command for a seat widens what a seat can do, so it needs its own decision. The points a reviewer should hold it to:

- The daemon is the confused deputy: it is unsandboxed and holds the user's rights. The request must be composed, never obeyed (section 5, cost 1). A run profile that is more permissive than the role's own must be refused.
- The broker is a new local service on the daemon's socket. It needs the seat's identity from the daemon's own record, not from the request, and a log of what it ran for whom.
- A seat that can ask the daemon to run any command can read the folders this change denies, by asking. The deny block stays on the composed profile for that reason, and the broker refuses a request whose profile removes it.
- Output and exit status come back through the daemon, so the broker must not echo environment values or file contents it was not asked for.

No code for it is in this change.
