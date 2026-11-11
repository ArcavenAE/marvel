# finding-marvel-3gz5: after a macOS upgrade, remote cluster calls failed with "no route to host" while nc and ping to the same hosts succeeded

**Date:** 2026-11-11
**Confidence:** frontier. One host (kinu), one terminal (Ghostty), one OS upgrade, two failed calls and one recovery.
**Probe:** none; observed during the post-upgrade status sweep.
**Placement:** marvel. The subject is the first-use and after-upgrade experience of marvel's remote cluster client (`marvel ... --cluster <name>`); macOS is the object.

## Why this matters

`docs/admin-guide.md` ("macOS: grant the terminal Local Network access") already says the Local Network permission is needed. This event shows three ways that section leaves an operator stuck: the grant can be lost on a machine where marvel already worked, the usual network checks pass while marvel fails, and the daemon now runs under launchd, which has no terminal to grant.

## What happened

Host kinu, macOS 27.2.1 (26B131), freshly upgraded and rebooted. marvel client and daemon 0.1.0-alpha.20261110.204143.6b1de3e, from Homebrew. Command run from Claude Code inside Ghostty.

1. 13:56Z: `marvel get teams --cluster mokuzai` and `--cluster corporate` both failed:
   `Error: ssh connect <user>@<mokuzai-addr>:6785: dial tcp <mokuzai-addr>:6785: connect: no route to host`
2. Same shell, seconds later: `nc -z -G3 <mokuzai-addr> 6785` and `nc -z -G3 <corporate-addr> 6785` both succeeded, and `ping` to mokuzai succeeded. The default route and the en0 address were normal.
3. Unified log, at the second of the failed call:
   - `13:56:45 UserEventAgent ... LocalNetwork: found bundle id com.mitchellh.ghostty by PID`
   - `13:56:45 nehelper ... UUID cache miss for com.mitchellh.ghostty`
   - `13:56:48 nehelper ... Successfully posted generation 307` (a network configuration change)
4. On retry after 13:56:48 both calls worked, and freebusy read all three clusters.

## Reading

- macOS attributes the LAN connection of a CLI binary to its responsible app, the terminal (Ghostty here), not to `marvel`. A UUID cache miss for that app at the failing call, followed three seconds later by a configuration change, fits a Local Network decision being made at that moment. The operator confirmed afterwards that, after the upgrade, they clicked Allow on the Local Network prompts they saw and recognized; which prompt matched this call was not recorded.
- `nc` and `ping` are system binaries and are exempt, so they succeed where marvel fails. This contradicts the admin guide's "every LAN connection from that terminal fails", and it sends diagnosis toward marvel or the peer.
- marvel had reached these clusters from the same terminal before the upgrade. That the upgrade reset the terminal's grant is consistent with the evidence but not proven: no pre-upgrade permission state was captured.

## Limits

1. One host, one terminal, one upgrade. No second terminal and no fresh user account was tried.
2. The permission state before the upgrade was not read, so "the upgrade reset it" is inferred.
3. The operator answered several Local Network prompts after the upgrade, approving those they saw and recognized. A first-time user may not recognize a prompt that names the terminal rather than marvel, and a prompt nobody sees leaves the call failing; neither case was tested.
4. Not tested: whether the launchd-started daemon (`com.arcaven.marvel-daemon`, no responsible GUI app) is subject to the permission for its own outbound LAN connections (leaf or federation), and whether it could ever show a prompt. Both clusters it was tested against accepted inbound reads from kinu's client; the daemon's own outbound path was not exercised.

## Possible follow-ups (not filed)

- Admin guide: say the grant can be lost on an OS upgrade; say `nc`/`ping` succeed while it is missing, so they do not rule it out; say what applies to a launchd daemon once limit 4 is tested.
- Client: when a dial to a private-range address fails with EHOSTUNREACH on darwin, add a hint naming the Local Network permission.
