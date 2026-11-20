## marvel daemon logs

Print the daemon's recent log lines (over mrvl:// if --cluster is set)

### Synopsis

Fetch the daemon's in-memory log ring and print the most recent N lines.

The ring is populated by every 'log.Printf' the daemon emits: RPC
dispatch, session creation, health-check decisions, shifts, auth
events. Lines survive as long as the daemon process does; the ring
is bounded so memory stays flat.

Examples:
  marvel daemon logs                      # local daemon, last 100 lines
  marvel daemon logs -n 500               # last 500 lines
  marvel --cluster desk daemon logs       # remote daemon via mrvl://

```
marvel daemon logs [flags]
```

### Options

```
  -n, --lines int   number of recent lines to return (0 = all buffered) (default 100)
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel daemon](marvel_daemon.md)	 - Start the marvel daemon
