## marvel credential put

Push a bus credential to the daemon (value from --value-file, or - for stdin)

### Synopsis

Push a bus credential to the daemon (value from --value-file, or - for stdin)

```
marvel credential put <name> [flags]
```

### Options

```
      --audience string     who the credential is for (for example the hub or a cluster)
      --binding string      display of the hub grant this credential carries
      --kind string         credential kind (default "nats-nkey-seed")
      --value-file string   file holding the credential value, or - for stdin (required)
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
