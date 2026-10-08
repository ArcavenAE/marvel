# Hot-reload of bus.hub.url: what it would take, for later

Writeup, not a design for build. Exploration is tracked as a reminder.

- Author: the arcaven architect seat.
- Issue: #514.
- Code read at marvel `origin/main` 833f692. Tags: **CODE** means read at that sha; **INFERRED** means reasoning, not observed.

## Why this exists

Adding `bus.hub.url` to a running cluster's client config changes nothing until the daemon restarts. The error the operator then sees, "this cluster declares no hub", reads as if the config were wrong when it is only stale. The operator ruled to fix the message now and to keep a written record of the larger idea, a hot-reload, so it can be explored later without starting from zero.

## What happens today

- **CODE.** The daemon reads the client config once, at start. `attachServices` calls `config.Load()` (`internal/daemon/daemon.go:3338`) and hands the bus spec to `attachBus` (`:3395`). `attachBus` builds the bus manager with it (`:3407`), and the manager keeps it (`internal/bus/manager.go:191`). Nothing reads the bus section again.
- **CODE.** `leaf connect` checks the hub URL held in memory. With none held, it answers "this cluster declares no hub (bus.hub.url); there is no leaf to connect or disconnect" (`daemon.go:3673`).
- **CODE.** `leaf connect` is not a config reload, though its log line says "by config reload" (`daemon.go:3684`). It re-renders the broker's own files from the spec already in memory and asks the broker to pick them up (`manager.go:567`, `SetLeafAttached`).
- **CODE.** The one path that re-reads the config is `marvel daemon reexec` (`daemon.go:2845-2900`). It keeps the agents running and adopts the broker, but it drops the leaf seed, the credential the broker uses to log in to the hub, so the seed has to be pushed again. That is #339, still open.

## What a hot-reload would do

INFERRED, step by step:

1. A verb (for example `marvel bus reload`) or a signal re-reads the client config and picks out this cluster's bus entry.
2. Only hub fields may change (`hub.url`, `hub.urls`, `hub.ca_file`). Any other bus change, such as the listen address, the mode or the store directory, is refused with "needs a reexec".
3. The hub fields are swapped into the manager's spec under the manager's lock.
4. The broker files are re-rendered. The global-role users appear or disappear with the hub, because they are rendered only when a hub URL is set (CODE `manager.go:397`; `internal/bus/declared.go:213`).
5. The broker picks up the change. A SIGHUP is enough when the broker already holds the leaf seed in its environment. A broker that booted with no hub needs a restart, because nats-server reads that seed once, at start; `leafSeedNeedsRestart` (CODE `daemon.go:3644`) already makes that choice for the seed path. A restart drops local connections for a moment; seats reconnect on their own (finding-marvel-1kg8 measured a director shim back within 5 s).
6. An event names the old hub and the new one.

## Size

INFERRED: one handler in `daemon.go` and a CLI verb; one setter in `internal/bus/manager.go`; the hub-only diff check; reuse of `leafSeedNeedsRestart`; tests for no change, a hub added, a hub changed, a hub removed, and a non-hub change refused. Roughly 150 to 300 lines with tests, in three or four files.

## Risk

INFERRED:

- **Adding a hub** creates the global-role users. A seat already running keeps the team user it started with, which is today's behavior after an apply, so this is not new.
- **Changing or removing a hub** removes those users from the broker's authorization file, and the reload closes their live connections. How the director shim handles that refusal is unmeasured. A running global-role seat could lose its global link until it is recast.
- **SIGHUP against restart, chosen wrong**, leaves the leaf down until a restart. The existing check covers the seed case.
- **A wrong URL** leaves the leaf down. The local bus and local seats keep working.
- **Two config sources.** The daemon would run on start-time config plus a newer hub. `bus status` would have to say so.

## Compared with the message fix and with reexec

| path | what it saves | cost | risk |
|---|---|---|---|
| fix the message | the confusion; the operator learns to reexec | about 5 lines | none at runtime |
| reexec, with #339 fixed | a second step (the seed push) | the #339 fix | the existing reexec path |
| hot-reload | both, and no daemon re-exec | 150 to 300 lines | the revocation edge and a second config source |

The message would say that the daemon holds the config it read at start, which has no `bus.hub.url`, and that `marvel daemon reexec` re-reads it, followed by a seed push until #339 is fixed.

## Recommendation and status

- **Now:** fix the error message and rename the "by config reload" log line to say re-render. This is routed to a marvel builder.
- **Next:** fix #339, so a reexec keeps the leaf seed. Reexec then becomes the one reload path, with no second config source.
- **Deferred:** hot-reload. Explore it if hub moves become routine; a reminder ticket tracks it.

This recommendation is valid until 2026-10-22, or until #339 is fixed or a second hub move happens, whichever comes first; the architect re-checks it then.
