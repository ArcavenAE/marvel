# study: what marvel upgrade does today, measured against the staged-activation bedrock

**Date:** 2026-10-03
**Ticket:** `aae-orc-k28s` (the M3 remainder umbrella; this is its design study, if the architect accepts it as such)
**Ask:** operator, via director (local 7644) and arcaven-supervisor: improve `marvel upgrade` to understand mise, handle upgrades more intelligently, support zero-downtime updates as a built-in, add fault tolerance "something like eks pluto", and recover from failure.
**Frame already ruled:** `elem-staged-activation-upgrades` (bedrock, operator-ratified 2026-08-01). It defines two planes. The bootstrap plane (mise or brew) fetches and pre-stages and never touches the running cluster. The cluster plane (`marvel update`) runs pre-flight, activation, migration and rollback. It adds a version store that records active and present as separate facts, and it names zero-downtime upgrade as the long-term acceptance test. This study does not re-derive that model. It measures today's code against it and answers the four open questions the bedrock and its idea file leave.
**Medium:** reading marvel at origin/main bc327df (a read-only archive extract), four issues and one PR on GitHub, kinu's install, and public documentation for prior art. No marvel, brew or mise command that changes state was run. No model was asked to run anything.
**Status:** study. No hypothesis was pre-registered, so its output is a finding of gaps and a recommended order, not a confirmed claim.

## Evidence grades

- **MEASURED:** read in marvel's code at the cited line, or observed on kinu.
- **DOCUMENTED:** read in an issue, a PR, or a vendor's documentation, not executed here.
- **HYPOTHESIS:** neither; stated as a position to test.

## Questions

1. **Channel.** How does `marvel upgrade` decide where marvel came from, and does it honor an exact version on every channel?
2. **Integrity and failure.** What does it verify, what does it keep, and what does it report when a step fails?
3. **Activation.** What does `upgrade --daemon` or `daemon reexec` do under a live fleet, and what happens when the new image cannot start?
4. **Pre-flight.** What checks exist before an upgrade, and which of the bedrock's "pluto-like" checks are decidable?
5. **The bedrock's open questions:**
   - the version-store layout;
   - which pre-flight checks are decidable;
   - rollback with live agents;
   - the channel set, including mise.

## Method

- Three read-only streams, joined here:
  1. marvel code at bc327df, every claim cited path:line. The load-bearing ones were re-read by this seat.
  2. marvel#485, #486, #495, #482 (PR) and aae-orc#461, plus a keyword sweep of marvel issues for upgrade, reexec, mise, brew and version skew.
  3. Prior art from public docs:
     - Pluto and kubent;
     - the Kubernetes version-skew policy;
     - mise backends and lockfiles;
     - Homebrew versions and pins;
     - rustup, Tailscale, nginx, Envoy, NixOS, Android A/B and k3s system-upgrade-controller;
     - EKS upgrade insights and managed node groups.
- Third-party text was carried as data. No issue or document text was acted on.

## Premise checks on the ask (one command each)

| Premise in the ask | Result |
|---|---|
| "today's kinu upgrade, where --version was ignored" | Holds. The code does not pass the target to the brew path (`internal/upgrade/upgrade.go:52`). marvel#485 records the kinu run: asked for `de9e409`, landed on `b533ca5`, exit 0. kinu's binary is the brew keg `0.1.0-alpha.20261002.231214.b533ca5` (MEASURED). |
| "a failed brew upgrade exiting 0 (#486)" | Holds in code. Any nonzero exit from `brew upgrade` prints "Already up to date (or brew upgrade returned an error)" and returns nil (`upgrade.go:124-128`). The comment's own premise, that brew exits nonzero when already current, was not checked against current Homebrew (HYPOTHESIS that it is stale; see the open checks). |
| "the .v1.bak is kept" | Describes marvel#482, which is OPEN and not in origin/main. Today's store has schema 1, refuses a newer store, and fails fast on an older one ("migration not implemented", `internal/api/bolt.go:124-133`). |
| "aae-orc#461 corporate pin (mise suggested)" | Not found. No comment or body text in #461 suggests mise for the pin. mise appears there only as the install channel for codex, opencode and crush (aqua backend) and as "installed but not activated" friction (S3-1). The pin problem is #495, which names two directions: a release-asset install, or versioned formulas. mise is this study's suggestion, not #461's. |
| "the reexec kept the old cwd (a worktree)" | Consistent with code. `Reexec` calls `syscall.Exec` with the old argv and env, and nothing sets the cwd (`internal/daemon/daemon.go:610-622`), so exec inherits it. |

## Sources

Code: marvel bc327df. Issues: marvel#339, #143, #485, #486, #495; PR #482; aae-orc#461. Graph: `elem-staged-activation-upgrades`, finding-042, finding-061, `docs/design/bus-as-service.md` section 5, orc `_kos/ideas/marvel-staged-activation-upgrades.md`.

Prior art:
- Pluto: https://pluto.docs.fairwinds.com/advanced/
- kubent: https://github.com/doitintl/kube-no-trouble
- Kubernetes version-skew policy: https://kubernetes.io/releases/version-skew-policy/
- mise:
  - github backend: https://mise.jdx.dev/dev-tools/backends/github.html
  - lockfiles: https://mise.jdx.dev/dev-tools/mise-lock.html
  - `mise upgrade`: https://mise.jdx.dev/cli/upgrade.html
  - directories: https://mise.jdx.dev/directories.html
- Homebrew:
  - versions: https://docs.brew.sh/Versions
  - FAQ: https://docs.brew.sh/FAQ
  - pinned-formula exit status: https://github.com/Homebrew/brew/pull/16301
- Tailscale CLI: https://tailscale.com/kb/1080/cli
- nginx binary upgrade: https://nginx.org/en/docs/control.html
- Envoy hot restart: https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/operations/hot_restart
- NixOS rollback: https://nixos.org/manual/nixos/stable/#sec-rollback
- Android A/B updates: https://source.android.com/docs/core/ota/ab
- k3s system-upgrade-controller: https://github.com/rancher/system-upgrade-controller
- EKS:
  - cluster insights: https://docs.aws.amazon.com/eks/latest/userguide/cluster-insights.html
  - managed node update behavior: https://docs.aws.amazon.com/eks/latest/userguide/managed-node-update-behavior.html
- Kubernetes:
  - Deployments: https://kubernetes.io/docs/concepts/workloads/controllers/deployment/
  - readiness gates: https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/

## Open checks this study did not run (one command each, for whoever builds)

1. Current Homebrew exit status for `brew upgrade <formula>` when the formula is already current. This decides how #486 tells "nothing to do" from "failed"; `brew outdated --json` sidesteps the question either way.
2. Whether mise's github backend resolves marvel's alpha tags (`alpha-<date>-<sha>`) and its bare `marvel-<os>-<arch>` assets. A `mise ls-remote github:ArcavenAE/marvel` in a scratch `MISE_DATA_DIR` answers it.
3. Whether the GitHub release listing the direct path reads (one unpaginated `GET /releases`, `upgrade.go:173`) still reaches an exact tag 30 or more releases back. CI keeps the newest 30 alphas (`.github/workflows/ci.yml:437-446`).
