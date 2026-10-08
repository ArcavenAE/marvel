# finding-marvel-1eds: an entitled helper suspends a hardened harness without root, through pid_suspend, while a taskport grant is in effect

Date: 2026-10-08
Status: frontier
Follows: `_kos/findings/finding-marvel-1kg8-fleet-pause-substrate.md` (section 4)
Design: `docs/design/fleet-suspend-resume.md`

## Why this matters

finding-marvel-1kg8 left one way to freeze a single seat without freezing tmux: a Mach suspend, which a SIGCONT from tmux should not undo. It stopped at the task port, and named root as an untested route. The operator asked for research before any root run. This finding is that research.

- **While a taskport grant was in effect, a root run was not needed to suspend a hardened harness.** An unprivileged helper, ad-hoc signed with `com.apple.security.cs.debugger`, suspended claude, codex and crush (hardened runtime) and opencode, through the `pid_suspend` syscall. These runs may have relied on a grant cached earlier in the login session (section 5), so this is not yet shown unattended. `task_for_pid` stays blocked for the hardened three.
- **`kill -CONT` did not undo `pid_suspend`, and a running tmux server did not undo it on its own pane process** across 5 s, a resize and a key press (r3f). That is the per-seat freeze `SIGSTOP` cannot give. Whether any other tmux path undoes it is not measured.
- **Open: whether it works unattended.** Until the grant question is measured, nothing here is a mechanism to build on.

Tags:
- **MEASURED:** run as an ordinary user with SIP on, against binaries built in a scratch directory, or throwaway harness instances in a scratch tmux server with a scratch HOME and no sign-in. No root, no host setting changed, no live seat.
- **CODE:** read in xnu at `f6217f891ac0bb64f3d375211650a4c1ff8ca1ea` (`apple-oss-distributions/xnu`, branch main), `bsd/kern/kern_proc.c`.
- **INFERRED:** reasoning, not observed.

Rig:
- Darwin 26.5.2 on arm64, Apple clang 21.0.0, SIP enabled, developer mode disabled.
- Harnesses: claude 2.1.295, codex-cli 0.160.1, crush v0.88.1, opencode 1.18.15.
- The user is in the `_developer` group.

## 1. What blocks task_for_pid (runs r1r3-20261008T204510Z, r1b-20261008T204631Z)

Four scratch targets: one counter program writing an incrementing number to a file every 100 ms, signed four ways.

| target signing | unentitled helper | cs.debugger helper |
|---|---|---|
| ad-hoc | 5 | 0 |
| ad-hoc + get-task-allow | 0 | 0 |
| ad-hoc + hardened runtime | 5 | 5 |
| ad-hoc + hardened runtime + get-task-allow | 5 | 0 |

- **MEASURED:** a hardened target without `get-task-allow` refuses even the entitled helper. claude, codex and crush ship that way (`flags=0x10000(runtime)`). That matches finding-marvel-1kg8's rc 5 against them.
- **MEASURED:** a successful `task_suspend` from a helper that then exits does not hold. A holder that kept its port for 4 s stalled the counter (11 to 11 over 2 s). After the holder exited without calling `task_resume`, the counter ran again (26 to 35). So a `task_suspend` design would need a resident process per frozen seat.

## 2. pid_suspend (runs r1r3-20261008T204510Z, r3b-20261008T204705Z, r3d-20261008T204842Z, r3e-20261008T205511Z, r3f-20261008T211428Z)

Every result in this section was taken while a taskport grant may have been in effect (section 5).

`pid_suspend(pid)` and `pid_resume(pid)` are syscalls 433 and 434, exported from `libsystem_kernel`, and take no task port.

- **MEASURED, unentitled helper:** EPERM, except against the `get-task-allow` targets.
- **MEASURED, cs.debugger helper:** rc 0 against all four scratch targets, the hardened one without `get-task-allow` included, and against throwaway claude, codex, crush and opencode.
- **MEASURED, the stall is real:**
  - every scratch counter stopped;
  - claude and codex ignored a Down key sent while suspended, then handled it after `pid_resume`. The control: the same key moved the screen while live;
  - crush and opencode showed a typed marker only after resume;
  - CPU time stayed flat while suspended, and every process stayed alive.
- **MEASURED, properties:**
  - it holds after the calling helper exits: each helper call is its own process, and the counter stayed stopped between the suspend call and the resume call (r1r3);
  - `kill -CONT` does not undo it: the counter stayed stopped after it, for all four scratch signings (r1r3);
  - a running tmux server did not undo it on the pane's own process: the counter held at 10 for 5 s, through a window resize and a key, and ran again after `pid_resume` (r3f, part b);
  - it does not nest. A second `pid_suspend` gets EPERM, one `pid_resume` resumes, and a second `pid_resume` gets EPERM (r3e);
  - `ps -o stat` reads `SN`, not `T`, while suspended (r3e). So the "read `T` twice" check from finding-marvel-1kg8 cannot see it;
  - it covers one process: with the parent suspended, the parent's counter held at 10 while its child's ran from 11 to 29 in 2 s (r3f, part a). Children (MCP servers, shells) need their own call.

## 3. What the source says about root (CODE, xnu f6217f89, `bsd/kern/kern_proc.c`)

- `task_for_pid_posix_check`: "If we're running as root, the check passes" (line 5617).
- `task_for_pid`: `mac_proc_check_get_task(kauth_cred_get(), &pident, TASK_FLAVOR_CONTROL)` runs for every caller, root included (5772). The taskgated upcall runs only for a caller that is not root, on a target other than itself, whose task access port is set ("If we aren't root and target's task access port is set...", 5779). Root skips it.
- `pid_suspend`:
  - it passes on `task_for_pid_posix_check` or `PROCESS_RESUME_SUSPEND_ENTITLEMENT` (6210);
  - then `mac_proc_check_suspend_resume(targetproc, MAC_PROC_CHECK_SUSPEND)` runs for every caller (6216);
  - then comes the same taskgated upcall for a non-root caller, with `TASK_FLAVOR_CONTROL` (6226).
- The policy behind the MAC hooks that enforces the hardened runtime is not in the open source. So whether root gets a hardened target's control port is UNKNOWN from source.
- Apple's Hardened Runtime page returned no readable body through a page fetch, and the `get-task-allow` page returned 404. Neither was read.

## 4. What SIGSTOP leaves (re-reading finding-marvel-1kg8)

- **MEASURED there:** no seat failed under `SIGSTOP` with the tmux server stopped first (8 of 8 in F1, F2 and F3, with the bus and MCP working on the next call in S9).
- **MEASURED there:** what `SIGSTOP` cannot do is freeze one seat while tmux runs, because tmux undoes it within a second. The server freeze is also what blocks the daemon.
- **MEASURED here, under the grant caveat:** `pid_suspend` closes that gap for the stimuli tried: `kill -CONT` (r1r3) and a running tmux server with a resize and a key (r3f). **INFERRED:** no other tmux path undoes it; not measured.

## 5. Open: a cached grant, and the group

- **The rule:** `security authorizationdb read system.privilege.taskport` gives `allow-root false`, `authenticate-user true`, `class user`, `group _developer`, `shared true`, `timeout 36000`.
- **A conflict with finding-marvel-1kg8:** there, the same entitled helper blocked (10 s bound) against non-platform targets. Here it returned at once.
- **INFERRED:** `pid_suspend` makes the same taskgated upcall as `task_for_pid`, and the right is shared for 10 hours once a user authenticates. So these runs may have ridden a grant answered earlier in the login session.
- **Not checked:** the system log returned no lines at all, even for the last 2 minutes, from the shell these runs used. So the grant could not be read from authd or taskgated.
- **Instrument for the grant, no host change:** re-run r3d with every call bounded at 10 s, more than 10 hours after any taskport prompt was answered, or in a fresh login session. A block or rc 5 means the unattended path still needs the prompt.
- **Not tested: a user outside `_developer`.** Testing it means a host change (a second user, or a group change), which needs the operator's word.
- **Not tested: Linux.** It has no `pid_suspend`; the cgroup freezer is the analogue, and it waits on a Linux machine.

## 6. Outcome and recommendation

The decision rule set before the runs:
- **Recommend a root run** only if the source shows root gets past the hardened runtime, and a measured case shows `SIGSTOP` failing.
- **Rule it out** if the source shows denial regardless of uid, or no `SIGSTOP` failure is found.
- **Moot** if an unprivileged path suspends a hardened target.

Outcome:
- **Root run:** not met. The source leaves root UNKNOWN.
- **Ruled out:** not met. One structural gap was found: the per-seat freeze.
- **Moot:** met under the grant state of these runs. It is not yet shown unattended (section 5).

Recommendation:
- Do not run the root half.
- Hold `pid_suspend` from a cs.debugger-signed helper as the candidate per-seat mechanism for the suspend design.
- Before any build, three checks:
  1. the unattended re-run and the `_developer` question (section 5);
  2. whether a notarized marvel build may carry `com.apple.security.cs.debugger`;
  3. a design for walking each seat's process tree with a resume ledger, since `SIGCONT` cannot recover a missed `pid_resume` and `ps` does not show the state.

This recommendation is valid until 2026-10-22, or until the unattended re-run, whichever comes first; the architect re-checks it then.

**Ruling, 2026-10-08, relayed by director:** the operator chose option (a), whose text was: "Don't run the root half. Re-run the pid_suspend case once, 10+ hours after any taskport prompt or in a fresh login; then check _developer dependence (needs a non-_developer user or host, your word), whether notarized marvel may carry cs.debugger, and a resume ledger". It covers the no-root line and the three checks above (the unattended re-run is part of the first). It does not cover holding `pid_suspend` as the candidate mechanism, which stays this finding's recommendation, not a ruling. The operator's note, verbatim: "dont run root right now, can run later".

My reading, not the operator's words: the root half runs later only on a new ruling, and the UNKNOWN in section 3 is not one.

## Appendix: the helpers

`psusp.c`, built with `cc -o psusp psusp.c`. The entitled copy is re-signed with `codesign -s - -f --entitlements ent.plist`, where `ent.plist` sets `com.apple.security.cs.debugger` to true:

```c
// psusp <suspend|resume> <pid>: the pid_suspend / pid_resume syscalls (no task port).
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
extern int pid_suspend(int pid);
extern int pid_resume(int pid);
int main(int argc, char **argv) {
  if (argc != 3) { fprintf(stderr, "usage: psusp suspend|resume pid\n"); return 2; }
  int pid = atoi(argv[2]);
  int r = strcmp(argv[1], "suspend") == 0 ? pid_suspend(pid) : pid_resume(pid);
  printf("pid_%s(%d) = %d errno=%d (%s)\n", argv[1], pid, r, r ? errno : 0, r ? strerror(errno) : "ok");
  return r == 0 ? 0 : 1;
}
```

Targets: the counter above, signed with `codesign -s - -f`, with `-o runtime` added for the hardened variants, and `--entitlements` carrying `com.apple.security.get-task-allow` for the get-task-allow variants. The `task_for_pid` helper is finding-marvel-1kg8's `msusp`.

This is the code that ran. Two weaknesses a reuse should fix: any verb other than `suspend` resumes, and `atoi` accepts `12abc`. `strtol` with an end-pointer check and an explicit `resume` verb fix both.
