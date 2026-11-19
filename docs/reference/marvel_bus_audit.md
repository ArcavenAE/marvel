## marvel bus audit

Read one record from the broker's audit log by number

### Synopsis

Read one record from the broker's audit log by number.

A send that is refused or fails on a cluster's bus leaves a numbered record in
that broker's AGENT_AUDIT stream, and the sender is told the number. This reads
it through marvel's own admin login to the broker, so it works only where marvel
runs the broker. With --cluster it reads another cluster's record, because it
runs on that cluster's daemon.

A record holds the full envelope of a send, text included, and any admin caller
on that daemon can read any record by number.

Text from the record is escaped, never printed raw: a character a terminal
could act on prints as \uXXXX. Exit 0 on a record, 1 on a refusal or a number
with no record.

```
marvel bus audit <seq> [flags]
```

### Options

```
  -o, --output string   print the record as json
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
