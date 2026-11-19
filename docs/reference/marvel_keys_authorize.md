## marvel keys authorize

Authorize a client's public key on this daemon

### Synopsis

Authorize a client's public key on this daemon

```
marvel keys authorize <public-key-file> [flags]
```

### Options

```
      --scope string   authority for this key: admin (every method) or credential-push (push, list, delete credentials only) (default "admin")
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel keys](marvel_keys.md)	 - Manage SSH keys for marvel clients and the daemon
