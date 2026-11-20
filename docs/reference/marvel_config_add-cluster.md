## marvel config add-cluster

Add or update a cluster

### Synopsis

Add a named cluster to ~/.marvel/config.yaml.

Examples:
  marvel config add-cluster kinu mrvl://kinu
  marvel config add-cluster staging mrvl://deploy@staging.example.com:7000 --identity ~/.marvel/keys/staging_ed25519
  marvel config add-cluster dev ~/.marvel/run/dev.sock

For remote (mrvl:// or ssh://) clusters without an --identity flag,
marvel defaults to ~/.marvel/keys/client_ed25519 when that key exists.
Use --no-default-identity to opt out and fall back to SSH_AUTH_SOCK
or ~/.ssh/ keys.

Use a DNS name for a remote host. An IPv4 literal is written, with a
warning, because a literal keeps dialing the old address after the host moves.

```
marvel config add-cluster <name> <address> [flags]
```

### Options

```
      --identity string       private key file to use for SSH auth on this cluster
      --no-default-identity   do not auto-attach ~/.marvel/keys/client_ed25519
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel config](marvel_config.md)	 - Manage marvel cluster configuration
