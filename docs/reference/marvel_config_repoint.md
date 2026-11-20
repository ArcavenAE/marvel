## marvel config repoint

Move a cluster to a new address, carrying its host key trust over when the key matches

### Synopsis

Move a cluster whose host changed address.

The command reads the host key recorded in ~/.marvel/known_hosts for the
cluster's current address, then reads the key the new address presents
without recording it. If the new address presents a key already recorded for
the old one, it prints both addresses and the fingerprint and asks before it
records the key for the new address and updates the cluster's address. The
cluster's identity and every other field stay as they are.

If the key differs, if no key is recorded for the old address, or if there is
no terminal to ask on, nothing is changed. A different key is a different
host: use 'marvel config add-cluster' and 'marvel keys trust' for that, which
ask in their own way.

The old address's entry in known_hosts is left in place.

Example:
  marvel config repoint prod mrvl://op@10.0.0.77:6785

```
marvel config repoint <cluster> <address>
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel config](marvel_config.md)	 - Manage marvel cluster configuration
