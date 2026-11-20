## marvel credential get

Show a credential's metadata, or its value with --reveal (local socket only, and only when this cluster's config entry sets allow_credential_reveal: true)

### Synopsis

Show a credential's metadata, or its value with --reveal (local socket only, and only when this cluster's config entry sets allow_credential_reveal: true)

```
marvel credential get <name> [flags]
```

### Options

```
      --reveal   print the secret value to stdout (served only on the local unix socket)
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel credential](marvel_credential.md)	 - Manage bus credentials the daemon holds (transient, never persisted)
