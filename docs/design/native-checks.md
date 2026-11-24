# Native checks: login expiry and seat gap as daemon services

Proposal, 2026-11-23. Code read at marvel `7fa1e89b`.

Two fleet checks run today as launchd agents outside marvel: a warning 72 hours before a harness login expires (director#737), and a list of seats that are up in marvel with no director roster row (director#738). Both need something installed on every host, and the second reads another cluster's seats against this host's roster. This note moves both into the marvel daemon, so a host that runs marvel is covered with no agent, no cron entry and no seat.

- **Status:** for review. Nothing here is built.
- **Builds on:** `docs/design/host-vitals.md` (the sampler pattern, the off-by-default ruling), `docs/design/seat-bootstrap.md` (the bounded `claude auth status` call), `docs/design/service-column.md` section 1.1 (what that command prints), and the account readings of #1566.
- **Author:** the marvel builder seat.

## 0. Why

On 2026-11-21 one host's login lapsed and five seats sat at "Login expired" for about 6 hours. The same day, seats sat at a first-run prompt for hours with no row on the director roster, so nothing that reads the roster could see them. Both are cheap to detect from where the facts already are: marvel holds the sessions and their config homes, and it can ask the harness for its own expiry. A check that lives in the daemon covers every host it runs on, keys a login the same way the rest of marvel does, and cannot disagree with marvel about which seats exist.

## 1. Scope

In: a login-expiry service and a seat-gap service in the daemon, each emitting events and showing in `marvel get vitals` and `marvel doctor`.

Out: remediation. Neither service logs in, restarts, nudges or signals anything (`host-vitals.md` section 5, HH-1). Out: deciding who hears about it; the events are the interface, and whoever consumes them decides.

## 2. Login expiry

### 2.1 Where the expiry is read from

From the harness, by asking its own status command, never from its credential store. For claude the daemon runs `claude auth status --json` with `CLAUDE_CONFIG_DIR` set to the exact string the seat was launched with (unset for the default home). The call follows the process handling of the pre-launch login check in `seat-bootstrap.md` (a 10 second bound, `Setpgid`, the whole process group killed on timeout, only after a successful start and only for a pid above 1), and departs from it in one respect: that check reads only the exit status and sends stdout and stderr to `/dev/null` unread, because they carry account identity. This reading needs the expiry, so it reads stdout. The departure is covered as follows:

- stdout goes to a pipe and is read once, with a size cap of 64 KiB; stderr goes to `/dev/null`;
- the wait is bounded (`cmd.WaitDelay` or equivalent), because `claude auth status` forks `security find-generic-password` and a surviving grandchild can hold the stdout pipe open, which would otherwise hang the daemon; on the bound the group is killed and the pipe closed;
- the raw stdout bytes are decoded and dropped, never kept, logged or put in an event;
- the "unreadable" reason is one of a fixed set of words (timed out, exited non-zero, not JSON, expiry missing), never output text.

`service-column.md` section 3.3 deferred a per-seat `auth status` probe because it makes marvel cause a credential read for every seat it launches and returns an email and organization that marvel would have to drop before storing. This reading clears the second reason by the decode described above, and the first by being one read per login every few hours rather than one per spawn, run only for a login a seat already uses, so it asks the harness nothing the seat's own start has not already asked.

The answer carries personal data (an email address, an organization name and id) next to the three fields wanted. The daemon decodes stdout in memory into a struct of exactly `loginExpiresAt`, `loggedIn` and `subscriptionType`, so the other fields never reach a value marvel keeps. The raw bytes sit in a capped buffer only until the decode and are then dropped; no decoded value, log line, event or file holds them. The harness reads its own keychain entry to answer (`service-column.md` section 1.1); marvel sees only the answer. That keeps the custody line of SOUL section 3: marvel holds no third-party bearer material, and does not read any.

Measured cost, from `service-column.md` section 1.1: about 0.2 s and 155 MB peak for the child, no network, no writes under a write-deny profile for a logged-in home. A logged-out home makes the harness write its own config (`seat-bootstrap.md` section on the login check), no more than the seat writes a second later.

Only harnesses with a known status command are asked (`harnessStatusCommands` in `cmd/marvel/doctor.go`, claude today). Another harness reads as "not checked", never guessed.

### 2.2 Which logins

The set of account keys of the sessions the store holds and the manifest declares, never a scan of a directory. Each key is `AccountKey{Harness, Backend, CredentialSource, ConfigHome}` as `internal/api/account.go` derives it, so a reading, a reset and an expiry for one login sit under one key.

The key is cleaned, but the harness is not forgiving: `claude auth status` reports `loggedIn: false` when `CLAUDE_CONFIG_DIR` has a trailing slash or names a parent directory (finding `finding-marvel-s8ej-usage-read-methods`). So the call uses the spelling taken from a seat's own environment (`ClaudeConfigDirEnv`), and the key only names the result. Where two seats of one key carry different spellings, the first spelling that answers `loggedIn: true` is used. A `loggedIn: false` from a spelling no seat uses is never reported.

A declared seat with no live session is covered by the seat-gap check (section 3), not by login expiry: a login belongs to an account and not to a seat, and an account no live seat uses is not being spent.

### 2.3 Schedule

At daemon start, after the store attaches, then every 6 hours; every hour while any known expiry is within 96 hours, and also every hour while any login is expiring, lapsed or unreadable, because the hourly read is what notices a renewal or a recovery, so a renewal is seen within the hour. One login is asked at a time, from one goroutine, with at most one call in flight, so the metrics pass never waits on it. A host with no claude seat asks nothing.

### 2.4 States and events

Per key: `ok` (expiry more than 72 hours away), `expiring` (72 hours or less), `lapsed` (`loggedIn` false, or an expiry in the past) and `unreadable` (the call failed or timed out; the previous state is kept and the reading says since when).

| event kind | severity | when | carries |
|---|---|---|---|
| `account.login-expiring` | warning | key enters `expiring`, once | the key string, the expiry time, hours left |
| `account.login-lapsed` | error | key enters `lapsed`, once | the key string |
| `account.login-renewed` | info | the expiry moves later by more than 1 minute, or a lapsed key reads `ok` | the key string, the new expiry |

Events carry no email, organization, subscription detail beyond its type, or path other than the key string marvel already prints. `account.logged-out` (the watchdog roll-up of three seats reading logged-out) stays; it reports a lapse after the fact, and this reports it ahead of time.

### 2.5 Where it shows

- `marvel get vitals`: a threshold row "login expiry", line "72 hours", state crossed when any key is `expiring` or `lapsed`, with the soonest expiry in its detail.
- `marvel get account-logins` (new, beside `get account-readings`): one row per key with state, expiry, hours left, the last good read and, for a failed read, its fixed reason and when the run of failures began (`failing_since`). Table and `-o json`. Never a row of zeros for an unread key; an unread key says why. A daemon whose switch is off says so and does not answer an empty list. Built; the vitals row and the doctor line are next.
- `marvel doctor`: the `login` line reads the daemon's answer when a daemon is reachable, and keeps today's exit-status check when it is not.

### 2.6 Switch

`login_expiry` and `seat_gap` on the cluster entry, beside `host_vitals`. Director ruled both services on by default (2026-11-23): they are native services, and a marvel user should not have to turn on a check that protects them from a lapse. This departs from the `host_vitals` ruling (off unless enabled) on purpose, and it carries one condition: the proof step in section 2.7. In the code, `login_expiry` ships off by default (`config.DefaultLoginExpiry`, false) until the proof is recorded for the fleet's hosts; the constant is the one line that turns it on, and the release notes say so. If `claude auth status` run as a daemon child raises a keychain dialog, it stays off until that is fixed. `seat_gap` runs no keychain read and is not affected.

### 2.7 The keychain prompt question

The harness reads its keychain entry as a child of the marvel daemon, through `/usr/bin/security` (`service-column.md` section 1.1). A prompt that names marvel would be the kind of surprise this change must not cause, so the default depends on a proof step.

- **Which process the keychain judges: unmeasured.** Whether an item's access control and the privacy daemon judge the requesting binary (`claude`, or the `security` it forks) or the responsible process up the parent chain (the daemon) has not been measured on this fleet, and no source is cited here. `egress-credential-front.md` covers partition lists for marvel's own items, not this case. The first build PR measures it before any default changes.
- **Proof step, run on each fleet host before the release that turns the service on by default:** one scheduled read with the daemon as parent logs 0 `tccd` authorization-request lines in the unified log for the window of the read, logs 0 lines from a process named `SecurityAgent` in the same window (`log show --predicate 'process == "SecurityAgent"'`), and raises 0 keychain dialogs that name marvel. A keychain access prompt is likely to come from `SecurityAgent` and not `tccd`; that is not measured either, which is why both are counted. The count and the log query are recorded in the PR, with the host's before and after. A host that shows either leaves `login_expiry` off by default until it is fixed, and the finding goes to the supervisor.
- **Measured so far (2026-11-23, one host, a login already admitted by the keychain):** three reads of `claude auth status --json`, each run from a seat's own process chain and not from the daemon, each bracketed by a `log show` window of its own length plus 2 seconds. In every window the `SecurityAgent` process logged 0 lines and `tccd` logged 0 authorization-request lines (`AUTHREQ`); one window held two `tccd` `TCCAccessCopyInformationForBundle` lines from another process, which are not authorization requests. No dialog appeared. This does not settle which process is judged: the login was already admitted, so a prompt would not show, and the parent was not the daemon. The daemon-as-parent run on each fleet host is still the proof step above.
- **If the daemon is the judged process**, the login-expiry service ships off by default and the note's fallback is a read by the seat's own harness process, which the keychain already admits: a one-line status file the seat writes through its own `claude auth status`, read by the daemon. That fallback is not designed here.

## 3. Seat gap

### 3.1 What is compared

The daemon's own sessions against the director roster. A session counts when it is alive (`CountsAsAlive`), its harness is claude, and it has been up more than 10 minutes (`now - CreatedAt`, using `AgeAt` where a suspend applies). The daemon sees only its own cluster's sessions, so the cross-cluster false positives of an all-clusters read cannot arise.

### 3.2 The join

The pair (team, name): the session's `Team` and `Name` against the roster row's `team` and `agent_id`. The marvel side is now confirmed in code: `constructedEnvQuiet` sets `DIRECTOR_AGENT_ID` to `ctx.Session.Name` and `DIRECTOR_TEAM` to the team name (`internal/runtime/adapter.go`). It holds until a declared seat name overrides it, and that override seam is named in the same comment; when it ships, the daemon joins on the id it set, which it knows, so the join needs no mapping.

### 3.3 Where the roster comes from

Through director's published interface: `director-mcp roster --json`, which reads the presence rows of the local broker and prints rows with `agent_id`, `team`, `fresh` and more. The daemon runs it with a 10 second bound and decodes only those three fields. It does not read the broker's storage itself; that layout is director's.

Three outcomes, never conflated:

- **director not present**: no `director-mcp` on PATH. Once director ships item 2 below, a program that reports no broker through its distinct exit status reads the same way. Until then a program that ran and failed is "unreadable". The check reports "director not present" and nothing else. Not an error, not zero gaps.
- **unreadable**: the program ran and failed, timed out, or printed a document that does not parse. The check reports the reason, and claims no seat missing.
- **read**: rows compared; each qualifying session with no fresh row is a gap.

What director must expose, so the check does not rest on a text match:

1. A documented, versioned JSON contract for the roster output (a `schema_version` field).
2. A distinct exit status for "no broker reachable", apart from a read error.
3. A way to say which broker the roster came from, so a host with more than one cluster does not read another cluster's rows as its own.

Until those exist, "director not present" means the program is missing, and every other failure is "unreadable". The three items are director's to build. They go to director as a pull request in its own repository, routed by the supervisor after this note merges; this note changes nothing in director.

### 3.4 Where it shows

An event `roster.seat-gap` (warning) once per seat per gap, `roster.seat-gap-cleared` (info) when the row appears or the seat ends; the `get vitals` row "seat gap" with the count and the oldest age; the same in `marvel doctor`. Never a log file.

### 3.5 Schedule

Every 5 minutes, from the same goroutine style as 2.3. A host with no claude seat asks nothing, and a missing director is remembered for the pass, not retried within it.

## 4. Independence

SOUL section 2: marvel works without director. The seat-gap service is optional input, like a harness binary: with no director the check says "director not present" and the rest of the daemon is unchanged. The login-expiry service does not touch director at all. Director, in turn, consumes the events as it consumes any other and is not required for either service to run.

## 5. Retiring director's versions

Once both services ship:

1. director#737 and director#738 are updated to point at `marvel get account-logins`, the vitals rows and the event kinds, and their scheduling notes stop proposing a launchd entry.
2. The launchd agents on each host that has one are removed.

The act in step 2 is a write to a host's launchd, which no seat holds. The operator holds it, per host: on the two hosts that still have an agent loaded, the operator removes it through the host's Login Items, and may do so now, without waiting for the marvel services to ship. The agent on kinu is already unloaded. No seat touches a host's launchd.

## 6. Build plan

Each service is its own PR, red tests first, the failing run recorded by a seat other than the test author.

1. Events: the five kinds in `internal/events` and the catalog test.
2. Login service, after the keychain measurement of section 2.7: parser (a fixture with a fake email and organization, asserting neither string appears in any event, log line or file), the state machine against a fake clock, the bounded runner with a process-group kill, the `get account-logins` verb, the vitals row, the doctor line.
3. Seat-gap service: the roster decoder, the three outcomes, the join, the 10 minute age, the events, the vitals row.
4. Config: the two switches and the reference docs.

Run conditions for the tests as for the rest of the module: `env -i`, scratch HOME, no live socket, a stub program for `claude` and `director-mcp`.

## 7. What it does not do

- It reads no credential and no credential file.
- It logs in for nobody.
- It does not notify anyone; events are the interface.
- It does not check a harness other than claude.
- It does not decide what a lapsed login means for a seat; the watchdog's harness-state reading is unchanged.

## 8. Rulings

Director ruled on three decisions on 2026-11-23, on the operator's direction:

1. **Default switch state:** on for both services, conditional on the section 2.7 proof. Replaces the earlier recommendation of off unless enabled.
2. **Launchd removal:** the operator's act, per host, as section 5 says, with no wait.
3. **What director must expose** (section 3.3): a director pull request, routed after this note merges.

## 9. Service install

The two checks started as host-side services that someone installed by hand on each host, and the install of those services drifted. An inventory of the fleet's hosts, taken when the checks were stopped, found five things, numbered 1 to 5: three install-drift risks (1 to 3) and two facts about how the services came to be where they were (4 and 5).

1. **The check scripts resolve into a builder's worktree.** On one host the installed entry points are links into a git worktree owned by a build seat, not into an installed copy. Removing or switching that worktree, which its owner may do at any time under the worktree rules, breaks the service with no change to the service itself.
2. **A bus agent runs from a probe folder in a shared checkout.** On one host a bus agent is started from a folder inside a checkout that several sessions share. A branch change in that checkout changes the code the live service runs.
3. **The daemon and its bus run inside a terminal multiplexer session.** On one host they do not survive a reboot, because no service definition starts them again afterwards. The first sign is silence. This describes a missing definition; it is not a task assigned by this note.
4. **Most of the agents are tracked nowhere.** On one host three of five agents are described in no repository, so no review saw them and no reader can say what they should be.
5. **Login expiry had three install paths on three hosts,** and none of them was a tracked installer. The same service was therefore three different things.

All five come from the same condition: a service whose definition lives only on the host it runs on.

### 9.1 What the native services remove

Login expiry and seat gap run inside the daemon (sections 2 and 3). A host that runs marvel has no host-side install for either: no agent definition, no script path, no worktree link, no checkout to keep stable. Items 1, 2 and 5 cannot occur for these two services, and the services are tracked because they are marvel's own code and configuration. The services restart and persist with the daemon, so they are as durable as the daemon is.

### 9.2 What stays out of scope

Items 3 and 4 are not removed by this change, because they are about the daemon and the other services around it, not these two checks. Installing a service so that it survives a reboot and restarts on failure (a keep-alive definition per host), and tracking such definitions in a repository, is the gap `finding-marvel-lt30-marvel-service-provisioning-lifecycle-gap.md` names: marvel's service record covers running a service, not installing, configuring, upgrading or persisting one. This note does not design that and does not touch any host's service definitions. Where a host's daemon and bus run in a multiplexer session, that stays a finding for the provisioning work until it is built.
