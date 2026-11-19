## marvel bus status

Show the broker: pid, listener, readiness, hub leaf link

### Synopsis

Show the broker: pid, listener, readiness, hub leaf link

```
marvel bus status [flags]
```

### Options

```
      --require-leaf   exit nonzero unless a hub is configured and the leaf is attached and up on a current reading, saying why
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel bus](marvel_bus.md)	 - Inspect the cluster's message bus
