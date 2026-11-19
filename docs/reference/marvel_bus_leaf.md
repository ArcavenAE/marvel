## marvel bus leaf

Connect or disconnect the local broker's leaf link to the hub

### Synopsis

Connect or disconnect the local broker's leaf link to the hub.

Both toggle the leafnodes remote in the broker's config and reload it with
SIGHUP, so the broker and every local session stay up. The leaf seed stays
enrolled across a disconnect, so a later connect needs no re-enrollment.
The link comes up only when a bus/leaf seed is enrolled; connect on a cluster
with no seed records the intent and the link comes up when a seed is pushed.

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel bus](marvel_bus.md)	 - Inspect the cluster's message bus
* [marvel bus leaf connect](marvel_bus_leaf_connect.md)	 - Attach the leaf to the hub without bouncing the broker
* [marvel bus leaf disconnect](marvel_bus_leaf_disconnect.md)	 - Detach the leaf from the hub without bouncing the broker
