# Marvel example manifests

Every manifest here ships in both formats. `name.toml` and `name.yaml` are the
same team written twice, so pick whichever your project already uses:

```sh
just build
./bin/marvel daemon &
./bin/marvel work examples/<name>.toml     # or .yaml
./bin/marvel get sessions
./bin/marvel stop --teardown               # when you are done
```

Each file opens with a comment explaining what it demonstrates and how to
watch it work. This index says which one to open.

## Start with these

| Manifest | What it shows | Needs |
|---|---|---|
| `generic-agent` | The smallest thing that works: marvel managing arbitrary commands through the generic adapter. | nothing |
| `claude` | Real Claude Code agents beside a simulator supervisor, with adapter selection, permission injection and identity injection. | claude on PATH, model auth |
| `review-team` | A heterogeneous team: several roles, per-role replicas and restart policy. | claude on PATH, model auth |

`generic-agent` is the honest first run, because it needs no model auth and
still exercises the whole loop: manifest, reconcile, pane, session, teardown.

## Harnesses and adapters

| Manifest | What it shows |
|---|---|
| `claude` | The claude adapter: flags, permission mode, injected session identity. |
| `claude-headless` | Headless Claude Code, the mode marvel can read a usage stream from. |
| `mixed-adapters` | The harness matrix: three BYOA harnesses in one workspace. |
| `generic-agent` | The fallback adapter managing any command. |
| `forestage-team` | The five-primitive taxonomy (persona, identity, role) on the frozen forestage reference adapter. |

## Context metering and shifts

| Manifest | What it shows |
|---|---|
| `context-feed` | The cooperative statusline feed that gives an interactive session a real CTX%. |
| `context-feed-off` | The same team without the feed, so CTX% stays absent. The "before" half of the pair. |
| `auto-shift` | Shifting a role automatically on context pressure, triggered on a token remainder rather than a percentage. |
| `shift-demo` | An operator-driven rolling shift with heartbeats. |

Read `context-feed-off` and `context-feed` together. They are the same team and
the diff between them is the whole feature.

## Health, restart and loss

| Manifest | What it shows |
|---|---|
| `demo-act1-health` | Health-driven terminal cases: restart policy against a failing healthcheck. |
| `demo-act1-recovery` | Recovery after pane loss. |
| `demo-act1-roles` | A team before a role is removed. |
| `demo-act1-roles-removed` | The same team after, so the reconciler's response is visible. |
| `demo-act1-fleet-loss` | Losing every replica of a role in one tick, which is charged to the role once rather than once per replica. |

`demo-act1-roles` and `demo-act1-roles-removed` are a before and after pair, as
are `context-feed-off` and `context-feed`.

## Policy projection

| Manifest | What it shows |
|---|---|
| `policy-projection` | A named settings fragment projected into a per-session file the harness reads. |
| `policy-projection-v2` | The same policy at version 2, to watch a live re-projection reach running agents. |

## Admission and budgets

| Manifest | What it shows |
|---|---|
| `demo-act4-budget` | Admission refusing over-budget work against a team-declared budget. |

## Simulator demos

| Manifest | What it shows |
|---|---|
| `demo` | Simulator agents with a chaos supervisor. No model auth needed. |
| `demo2` | Five simulator agents with healthchecks. |

The simulator exists so the control plane can be exercised without spending
model tokens. `docs/demo.md` walks the acts these manifests belong to.
