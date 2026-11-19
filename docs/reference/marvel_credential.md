## marvel credential

Manage bus credentials the daemon holds (transient, never persisted)

### Synopsis

Manage bus credentials the daemon holds (transient, never persisted)

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel](marvel.md)	 - Agent orchestration control plane
* [marvel credential delete](marvel_credential_delete.md)	 - Delete a credential (its value is zeroed)
* [marvel credential get](marvel_credential_get.md)	 - Show a credential's metadata, or its value with --reveal (local socket only, and only when this cluster's config entry sets allow_credential_reveal: true)
* [marvel credential list](marvel_credential_list.md)	 - List credentials (metadata only)
* [marvel credential put](marvel_credential_put.md)	 - Push a bus credential to the daemon (value from --value-file, or - for stdin)
