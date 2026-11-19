## marvel bus

Inspect the cluster's message bus

### Synopsis

Inspect the cluster's message bus.

A cluster with 'bus.managed: true' in the client config runs nats-server
as a child of the daemon. The daemon starts it, restarts it under the
same crash-loop backoff roles get, reloads its authorization when teams
change, and holds new sessions while it is down. A cluster with
'bus.managed: false' only hands sessions the configured URL.

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel](marvel.md)	 - Agent orchestration control plane
* [marvel bus audit](marvel_bus_audit.md)	 - Read one record from the broker's audit log by number
* [marvel bus leaf](marvel_bus_leaf.md)	 - Connect or disconnect the local broker's leaf link to the hub
* [marvel bus status](marvel_bus_status.md)	 - Show the broker: pid, listener, readiness, hub leaf link
