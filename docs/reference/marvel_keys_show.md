## marvel keys show

Print a client public key (to share with the daemon admin)

### Synopsis

Print a client public key (to share with the daemon admin)

```
marvel keys show [flags]
```

### Options

```
      --name string   key name (default "client_ed25519")
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
