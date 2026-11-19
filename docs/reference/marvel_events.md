## marvel events

List recent session/team state-transition events

### Synopsis

Fetch the daemon's structured event ring and print matching events.

Two families share the ring. Control-plane events report what marvel
did to a session: session.created / deleted / crashed from
session.Manager, and restart, crashloop-backoff, saturation, shift and
health events from team.Controller. Agent events (agent.*) report what
the agent inside a session did: session start and end with cost and
timing, messages, tool calls and results, permission prompts. Agent
events only appear for sessions whose runtime marvel can observe (a
headless stream-json launch today; see examples/claude-headless.toml).

Each event has a timestamp, kind, severity (info or warning), and
session coordinates.

Examples:
  marvel events                              # last 100 events
  marvel events -n 500                       # last 500 events
  marvel events --workspace demo             # filter by workspace
  marvel events --session util/shell-g1-0    # filter by session key
  marvel events --kind session.crashed       # only crashes
  marvel events --kind agent.tool.call       # what the agents are doing
  marvel events --kind agent.session.ended   # per-session cost and timing
  marvel events --kind context.limit-unresolved  # why a CTX% cell is blank
  marvel events --kind admission.refused     # spawns a team budget refused
  marvel events --warnings                   # only warning-severity events
  marvel events --follow                     # live tail, streamed from the ring as events land
  marvel events --list-kinds                 # every kind --kind accepts
  marvel --cluster desk events               # remote daemon via mrvl://

A --kind that matches nothing prints no events rather than an error, so a
misspelled kind and a kind that never fired look the same. --list-kinds
prints the catalog, and needs no daemon.

```
marvel events [flags]
```

### Options

```
  -f, --follow             stream new events as they land until interrupted
      --kind string        filter by event kind (e.g. session.crashed, health.failed, agent.tool.call)
  -n, --lines int          number of events to return (0 = all buffered) (default 100)
      --list-kinds         print every event kind --kind accepts, then exit
      --role string        filter by role
      --session string     filter by session key (workspace/name)
      --team string        filter by team
      --warnings           show only warning-severity events
      --workspace string   filter by workspace
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel](marvel.md)	 - Agent orchestration control plane
