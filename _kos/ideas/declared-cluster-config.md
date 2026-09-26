# More cluster configuration in TOML or YAML

- **Status:** idea (pre-hypothesis, no commitment). Operator request relayed
  via director, 2026-09-26.
- **Seed (operator's words):** "More configs in toml/yaml for the cluster."
- **Subject:** marvel (where a cluster's and a daemon's settings are
  declared). Filed in the marvel graph per the subject test.
- **Related:** [[services-catalog]] (the services idea, in review at filing as
  #369), `docs/design/services-list.md` (the cluster-level services list that
  already lives in `config.yaml`), [[token-rate-column-and-configurable-columns]]
  (columns as a user option), [[bus-leaf-status-and-terminology]].
- **Optional by construction.** marvel keeps working with no config file and
  flags alone (SOUL §2). A declared config would be a convenience over the
  current defaults, not a new requirement.

## Premise: where settings live today

Inventoried from `origin/main` at `c01452c`, one grep per surface.

| Surface | What it holds today |
|---|---|
| `~/.marvel/config.yaml` (`internal/config`) | `clusters` (name, socket, server, identity), `current_cluster`, and per cluster a `bus` block (managed, listen, url, store_dir, seat, hub) and a `services` list (name, class, provider, mode, caller_identity, url, ca_file). The file is described as the client cluster config. |
| Global CLI flags | `--cluster`, `--socket`, `--identity` |
| `marvel daemon` flags | `--mrvl`, `--socket`, `--reclaim`, `--log-file`, `--pidfile`, `--state-bolt`, `--shift-timeout`, `--log-max-size`, `--log-max-files`, `--log-max-total` |
| `marvel upgrade` flags | `--version`, `--daemon` |
| Environment marvel reads | `MARVEL_SOCKET`, `MARVEL_SHIFT_TIMEOUT`, `MARVEL_BACKEND_OVERLAY_DIR`, `TMUX_TMPDIR` |
| Environment marvel sets for seats | `MARVEL_SESSION`, `MARVEL_ROLE`, `MARVEL_TEAM`, `MARVEL_WORKSPACE`, `MARVEL_TMUX_SOCKET`, `MARVEL_HEARTBEAT_TOKEN`, and the `MARVEL_BACKEND_*` family |
| Workspace manifests | `workspace`, `team`, `endpoint`, `policy`; per role restart_policy, max_restarts, permissions, dangerous_permissions, policy, shift |
| Hardcoded | `get sessions` columns; the `~/.marvel/` layout (`internal/paths`) |

So some cluster settings are already declared (services, bus, clusters),
while daemon behavior (logs, shift timeout, state path) is flags and env
only, and display and upgrade behavior are hardcoded or per-invocation.

## Candidates for a declared cluster config

Listed, not chosen:

- **services** and **bus**: already declared; the question is whether they
  stay in the client file or move to a daemon-side file.
- **clusters**: already declared; unchanged.
- **defaults**: daemon settings now only in flags (shift timeout, log
  rotation, state path), and default values a manifest role inherits when it
  omits them.
- **columns**: the `get sessions` layout, per the configurable-columns idea.
- **upgrade policy**: how `marvel upgrade` treats the daemon (pinning,
  re-exec, staged activation).

## Open questions

1. One file or two? `config.yaml` today is the client's view of clusters;
   daemon settings belong to the host that runs the daemon.
2. TOML, YAML, or both? Manifests accept both tags; `config.yaml` is YAML.
3. Precedence: flag over env over file, the same resolution chain the fleet
   uses elsewhere?
4. Which settings may a remote client change, and which are host-local only?
5. Does a declared file reload live, or only at daemon start?
