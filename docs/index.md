# Marvel documentation

A router, not a summary. Each entry says what the document is for and who it
is for, so you can tell from here whether to open it.

## Start here

| If you want to | Read |
|---|---|
| Run a team and operate it day to day | [User Guide](user-guide.md) |
| Run the daemon, reach it remotely, upgrade it | [Admin Guide](admin-guide.md) |
| Understand the model before trusting it | [Architecture Overview](architecture.md) |
| See it work without reading anything first | [the four-act demo](demo.md), then [examples/](../examples/) |

New to marvel: the demo first, then the User Guide. The demo runs on the
simulator, so it needs no model auth and costs no tokens.

## Operating

- [User Guide](user-guide.md). Daily use: manifests, sessions, capture,
  inject, shift, scale, watch mode, and how CTX% is produced.
- [Admin Guide](admin-guide.md). Daemon setup, remote access over `mrvl://`,
  SSH key management, cluster configuration, upgrades, monitoring.
- [SSH Keys](keys.md). Dedicated marvel keys, the `~/.marvel/` layout,
  permissions, `keys generate` / `show` / `doctor`, and auth precedence.
- [Demo](demo.md). A four-act walkthrough of health, loss, recovery, context
  pressure and budgets, with the manifests it uses.
- [examples/](../examples/). A manifest per scenario, each in TOML and YAML,
  with its own index.

## Understanding the design

- [Architecture Overview](architecture.md). How marvel works, the resource
  model, and how the components fit together. Read this before the design
  notes below; they assume it.

Design notes cover one decision or one subsystem each. They explain why a
thing is shaped the way it is, which the guides deliberately do not:

- [The bus as a first-class Service](design/bus-as-service.md) and
  [The cluster Services list](design/services-list.md). How a cluster declares
  the broker and other services, and what marvel does with that declaration.
- [marvel's internal bus](design/internal-bus.md). The events ring, its watch
  seam, and the lifecycle kinds on it. Distinct from the cluster bus above.
- [Daemon isolation](design/daemon-isolation.md). The socket and the tmux
  namespace, and why a second daemon cannot reclaim the first one's fleet.
- [Session working directory](design/session-working-directory.md). The
  declared placement field and what it does to a spawn.
- [Two codex roles](design/codex-roles-reviewer-and-retrospector.md). Reviewer
  and retrospector as run on the live team.
- [Marvel as a Service Provider](design/service-provider/README.md). A design
  set: brief and PRD, architecture, probes and roadmap.

## Specification

The MVP specification, on IEEE and ISO formats. These describe the MVP scope
rather than everything marvel does now, so read them as the original contract
rather than as current reference:

- [Requirements](spec/requirements.md), ISO/IEC/IEEE 29148.
- [Design](spec/design.md), IEEE 1016.
- [Epics, stories, tasks](spec/stories.md).

## Point-in-time records

These are dated records of what was true or decided on a particular day. They
are kept for the reasoning, and they are **not maintained**. Do not read them
as current behaviour:

- [Adopted services, 2026-09-16](design/adopted-services-2026-09-16.md).
- [Agent identity landscape study](studies/agent-identity-landscape.md).
- [biscuit-go and Cedar-in-Go viability](studies/biscuit-cedar-go-viability.md).
- [Reflection on marvel merge conflicts](merge-conflict-reflection-2026-09.md).

## Elsewhere

The repo root `README.md` is the short introduction and the CLI summary.
`CLAUDE.md` carries the working conventions for agents editing this repo.
