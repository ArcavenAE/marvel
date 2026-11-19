## marvel keys generate

Generate a new client keypair under ~/.marvel/keys/

### Synopsis

Generate a new client keypair under ~/.marvel/keys/

```
marvel keys generate [flags]
```

### Options

```
      --comment string   embedded comment (default: user@host)
      --force            overwrite an existing key
      --name string      key name (filename under ~/.marvel/keys/) (default "client_ed25519")
      --type string      key type (ed25519) (default "ed25519")
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
