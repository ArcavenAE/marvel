# Seats do not read the macOS privacy folders: a default deny profile

- **Status:** proposed, 2026-11-23. Measured on one darwin host; nothing here is built yet.
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

The first four rows are the classes Apple documents. The last two rows are over-inclusive on purpose: a deny that was not needed costs nothing, and a missing deny costs a prompt. I did not trigger a prompt to test any single row, because the measurement in section 9 is meant to produce none. The list is data in one Go table, so a row can be added or dropped without touching the mechanism.

## 4. Launch and effect on tmux and children

The prepared seat command already runs under `sandbox-exec` when a role names `sandbox_profile` (`internal/session/manager.go`, the staged copy). The default deny reuses that path: the wrapped command is the pane's command, so tmux itself is outside the profile and the seat and every child it starts is inside it. A profile applies to the process and is inherited by everything it forks; no child can lift it. `tmux` clients started by the seat run under the profile, and the tmux server they reach does not.

A seat whose `workdir` lies under a denied folder would fail every command. Launch refuses it, naming the folder, rather than starting a seat that cannot read its own tree.

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

I recommend 1 for the first release, because it is the only one that cannot break a run, and 3 as the follow-up. This is a recommendation for the supervisor, not a decision.

Claude Code's own seatbelt on its Bash tool was not present on the seat I measured from (a top-level `sandbox-exec -p '(version 1)(allow default)'` applied). A seat that does run under it would be in the nested case above, and the same shapes apply.

## 6. The opt-out field

A role field `tcc_deny`, a boolean on the role beside `sandbox_profile`. Absent means on (deny). `tcc_deny = false` skips the default block for that role. A role that sets it keeps today's behavior and its prompts. The default for roles that nest is the supervisor's to propose to the operator; this note only names the field.

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

The `tccd` log for that window held 45 lines; none names `du`, `ls` or `com.arcavenae.marvel`, so no new TCC request was raised for marvel. The positive control is the operator's own record: `tccd` at 09:41:45 the same day, responsible `com.arcavenae.marvel`, accessing `/usr/bin/du`. I did not repeat that run without the profile, since it would raise the prompt this change removes.

One thing to note: `du -sh ~` under the profile exits 1 and prints errors for the denied folders. A seat that treats a nonzero `du` as failure will see one. That is the intended "fail quickly and quietly", and the build documents it.

## 10. Build plan after this note

1. A table of the section 3 paths and a function that renders the deny block for a home.
2. The `tcc_deny` field, parsed and validated, default on.
3. Staging of the composed profile (section 8) and the launch refusal for a `workdir` under a denied folder.
4. Tests first: the block names every row; a role profile's allow cannot re-open a folder; `tcc_deny = false` yields the role's profile unchanged; Linux yields none; the adapter flag stays unset.

Step 3 waits on the nesting ruling in section 5.
