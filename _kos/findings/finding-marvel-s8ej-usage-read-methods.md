# finding-marvel-s8ej: a headless seat reads plan usage at zero tokens, and the readings agree with the interactive panel

**Date:** 2026-11-23
**Confidence:** frontier. Measured on one host (kinu) over about five hours, on two logins, with one more login read from a second host. One scheduled reset was observed. No out-of-cycle reset was observed, so the one-hour detection target is calibrated against the scheduled reset and is not proven.
**Placement:** marvel. The subject is how marvel obtains and keys usage readings; the harness and the logins are objects.

## Why this matters

Marvel decides which seats can run by what each login has left, and it learns that only from a statusline feed that exists once a seat has already made a model request. A read that needs no model request would let marvel see a login's windows, its reset times and its credit state before it spends anything, and notice a reset that came off schedule. This finding measures four ways to read the same logins and says which fields and which failures each one has.

## Accounts

Named by role only. The Enterprise login is the default-home login (`CLAUDE_CONFIG_DIR` unset). The Max 20x login is the one under a named config home. A third, a Max 20x login on another host, was read there by that host's own seat and is cited as that seat's reading.

## What was read, and how

At marvel `4da31668`, on kinu, hourly from 22:00Z to 00:30Z, with extra reads at 23:10Z and earlier calibration reads at 19:46Z to 19:50Z on 2026-11-22.

| | (a) statusline feed | (b) headless `get_usage` | (c) `/usage` in an idle interactive seat | (d) schedule alone |
|---|---|---|---|---|
| Model cost per read | a real request; the feed exists only after the seat has parsed one API response | none; zero tokens, zero cost in the result | none; the pane reports an estimated cost of $0 | none |
| Wall time | whatever the seat is doing | about 5 s | about 30 s | none |
| 5h and 7d with reset | yes, `used_percentage` and `resets_at` only | yes, with `limits[]` and a scoped weekly figure | 7d with reset on a Max login; credits on an Enterprise login | predicted only |
| Credits and spend limit | no | yes: `extra_usage` (limit, used, utilization) and `spend` | yes: the credits bar, dollars, and the reset date | no |
| Failure seen | absent until the first API response; a populated payload is marked not verified in the code | a login read through a path spelled differently (see below) | a new working directory on a config home with no trust entry stops on the workspace-trust dialog, and the scripted Enter picks "No, exit" | an out-of-cycle reset is invisible |
| Survives a daemon restart | the series is written under `~/.marvel/state/account-series`; that directory on kinu holds no file, and no daemon was restarted for this probe, so survival was not measured | nothing in marvel stores it; the probe wrote files outside the repository | same | not applicable |

The method (a) cells come from reading the code at `4da31668` (`cmd/marvel/accountlimits.go`, `cmd/marvel/ctxforward.go`, `cmd/marvel/accountstamp.go`, `internal/api/account.go`). No manifest seat was started.

### Method (b)

The request is one control message over `claude -p --input-format stream-json --output-format stream-json --verbose`: `{"type":"control_request","request_id":"u1","request":{"subtype":"get_usage"}}`. The reply is a `control_response` carrying `rate_limits` and `spend`. The session's own cost in the result was 0 on every read. `claude -p "/usage"` also costs nothing but returns only a local behaviour breakdown, with no windows and no reset.

### Method (c)

An interactive `claude` started under tmux with an empty MCP config, Enter on the first dialog, `/usage`, a pane capture, then `/exit`. Enterprise reads worked at every round. The Max login read worked at 19:48Z and 19:49Z and failed at all five scheduled rounds with a 0 byte pane. Reproduced cause: the scheduled seat ran in a new directory, the config home had no trust entry for it, and the default answer exits. Method (b) is not affected. No trust entry was written for that login, because that is a write into the login's config home and was not authorised.

## Identity

Every (b) and (c) reading is bracketed by `claude auth status --json` before and after, compared with the expected config home and, for marvel's purposes, its account key. All before and after pairs were identical and matched the expected home. No reading was discarded for mismatch.

Limits of that proof:

1. `get_usage` names only `subscription_type`. It carries no account identifier, so the proof is the auth pair plus the fact that the script binds the config home.
2. The `/usage` panes show no email or organisation.
3. One early (c) reading, `c-mike-20261122T194800Z`, has no `.end` file, so its closing time is unrecorded.
4. The account key `api.AccountKey{Harness, Backend, CredentialSource, ConfigHome}` is derived from the path, not read from the credential. A path spelled two ways is two accounts in marvel's view. It is also a false-negative source in the other direction: `claude auth status` reports `loggedIn:false` for the same login if `CLAUDE_CONFIG_DIR` has a trailing slash or names the parent directory. Only the exact string reports the login. A reader that takes its path from a manifest therefore needs the same spelling the login was made under.

## The scheduled-reset calibration

The Max 20x login on the other host was read by its own seat with method (b) at 22:00Z, 23:00Z and 23:05Z:

- 22:00Z: seven_day utilization 99, `resets_at` 22:59:59.598Z, a `seven_day_breakdown` object with `window_started_at`.
- 23:00Z: utilization 0, `resets_at` 2026-11-29T23:00:00Z (the fractional seconds gone), `seven_day_breakdown` null.
- 23:05Z: the same as 23:00Z.

So a (b) read sees the reset within its own cadence: a read taken any time after the boundary shows the new window. Two reads five minutes apart agreed after it. The hourly cadence therefore detects a scheduled reset within the hour, and by the same arithmetic would detect an out-of-cycle one within the read interval plus the read time. That is a calibration against the scheduled case. It is not evidence about out-of-cycle behaviour, which was not observed.

Three field behaviours that bear on a detector:

- `resets_at` jitters on the same instant. The Max login on kinu read 02:00:00.426Z, 02:00:00.436Z, 02:00:00.036Z and 01:59:59.950Z across reads of one window. A comparison needs a tolerance of at least one second.
- A reset can change the format of the value (the fractional part disappeared at 23:00Z).
- A block can drop out after a reset (`seven_day_breakdown`), so no reader may require `window_started_at`.

The two kinu logins did not reset during the series. The Max login stayed at utilization 100 with a reset on 2026-11-25. The Enterprise login has no 5h or 7d window; its figure is a monthly credit allowance.

## Where an Enterprise user sees their monthly spend limit

In the tool's own surfaces: the `/usage` panel, in its credits section, which shows the amount spent against the limit and the reset date. The same numbers are in the `get_usage` reply under `extra_usage` (`monthly_limit` and `used_credits`, in minor units, and `utilization`) and `spend`. `/status` shows the login method but no limit. Over 4 h 44 min the Enterprise login's utilization rose by about 0.3 points, so the spend is visible hourly. The panel and the reply agreed to within the time between reads. The reset date is in the panel only.

## Credit inventory

The reply has `spend.balance` and `spend.auto_reload`, both null on every login read, including the other host's. Those are the slots a reset-credit or prepaid balance would occupy. No login read here carried a credit inventory, so a value-aware estimate of such an inventory cannot be derived from these readings and is not attempted.

## What it means for marvel (design only)

- A usage reader seat that runs method (b) on a schedule costs no tokens, and its reading is richer than the statusline feed's (credits, scoped weekly limits, reset times with no need for a prior request).
- It needs an identity check per read. The cheap one is the `auth status` pair against the exact config-home string, and the account key should record the spelling it was read under.
- A reset detector needs a timestamp tolerance of one second or more and must not depend on optional blocks.
- The credit inventory has a place in the reply but no data; a value estimate waits for a login that carries a balance.

No marvel code or manifest change is made here.

## Evidence

Raw readings are kept outside this repository and are cited by the first 12 hexadecimal digits of their sha256. They contain account identifiers and are not published.

- (b) Enterprise 22:00Z: `19fbcac3a367`; 23:00Z: `71355baf17f3`; 23:10Z: `69c581a76e8c`; 00:30Z: `2b4f94797fae`.
- (b) Max login, kinu, 22:00Z: `ea372b0a5de2`; 23:00Z: `045212bda1c2`; 23:10Z: `18cefa34c67d`; 00:30Z: `8d2a66b10d3b`.
- (c) Enterprise 22:00Z: `5283fded2512`; 00:00Z: `af5a04e4ad15`; 00:30Z: `4b543f76d423`.
- (c) Max login, scheduled, empty pane: `e3b0c44298fc` (the hash of nothing) at each of five rounds.
- The other host's seat, from its own files: 22:00Z `70505dd8`, 23:00Z `8013c703`, 23:05Z `03f5fb19` (8-digit prefixes as that seat reported them).
