## marvel credential delete

Delete a credential (its value is zeroed)

### Synopsis

Delete a credential (its value is zeroed)

```
marvel credential delete <name>
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
