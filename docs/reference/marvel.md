## marvel

Agent orchestration control plane

### Synopsis

Agent orchestration control plane

### Options

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel attach](marvel_attach.md)	 - Open a session's tmux pane by desk number, session key or name
* [marvel backend](marvel_backend.md)	 - Inspect the model backend sessions are running on
* [marvel bus](marvel_bus.md)	 - Inspect the cluster's message bus
* [marvel capture](marvel_capture.md)	 - Capture a session's pane content
* [marvel config](marvel_config.md)	 - Manage marvel cluster configuration
* [marvel converge](marvel_converge.md)	 - Release a team (or all teams) from the start-line hold and converge to desired
* [marvel credential](marvel_credential.md)	 - Manage bus credentials the daemon holds (transient, never persisted)
* [marvel daemon](marvel_daemon.md)	 - Start the marvel daemon
* [marvel delete](marvel_delete.md)	 - Delete a resource
* [marvel describe](marvel_describe.md)	 - Show detailed information about a resource, or the daemon that answers
* [marvel doctor](marvel_doctor.md)	 - Check this host for what a first run needs
* [marvel events](marvel_events.md)	 - List recent session/team state-transition events
* [marvel example](marvel_example.md)	 - Print a manifest the binary carries
* [marvel exposure](marvel_exposure.md)	 - Record, end or void the functions a slot has read (the clean-room exposure gate)
* [marvel get](marvel_get.md)	 - List resources (sessions, teams, workspaces, endpoints, policies, credentials, budgets, account-readings, vitals)
* [marvel handoff](marvel_handoff.md)	 - Hand a seat's work to its successor through the daemon
* [marvel hibernate](marvel_hibernate.md)	 - Sign off the named teams, end every seat, and hold the teams until a wake
* [marvel inject](marvel_inject.md)	 - Send keystrokes to a session's pane (executive privilege)
* [marvel keys](marvel_keys.md)	 - Manage SSH keys for marvel clients and the daemon
* [marvel kill](marvel_kill.md)	 - Kill a session (alias for delete session)
* [marvel mcp](marvel_mcp.md)	 - MCP servers marvel offers to its seats
* [marvel orphans](marvel_orphans.md)	 - List agents heartbeating against session keys this daemon owns with a stale token
* [marvel plan](marvel_plan.md)	 - Preview the convergence delta per team without spawning (dry run)
* [marvel reap](marvel_reap.md)	 - List (and with --confirm, destroy) marvel tmux state the daemon does not own
* [marvel reset-health](marvel_reset-health.md)	 - Clear a role's crash-loop restart count and backoff without deleting the team
* [marvel resume](marvel_resume.md)	 - Resume a suspended cluster
* [marvel run](marvel_run.md)	 - Run a one-off agent session
* [marvel scale](marvel_scale.md)	 - Scale a team role to N replicas
* [marvel shift](marvel_shift.md)	 - Initiate a rolling shift for a team
* [marvel status](marvel_status.md)	 - Say whether the cluster is running or suspended
* [marvel stop](marvel_stop.md)	 - Stop the marvel daemon, leaving agents running
* [marvel suspend](marvel_suspend.md)	 - Freeze every seat and the tmux server in place
* [marvel upgrade](marvel_upgrade.md)	 - Upgrade marvel to the latest version
* [marvel version](marvel_version.md)	 - Print marvel version and channel
* [marvel view](marvel_view.md)	 - Work with a seat's read-only views
* [marvel wait](marvel_wait.md)	 - Wait for a session to finish (experimental)
* [marvel wake](marvel_wake.md)	 - Bring hibernated teams back, supervisors first
* [marvel work](marvel_work.md)	 - Load a manifest and reconcile desired state
