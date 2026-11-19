## marvel work

Load a manifest and reconcile desired state

### Synopsis

Load a manifest and reconcile desired state.

With --prune, the apply also removes the teams this file put there and no
longer names, as the prune set stands when the apply runs. marvel plan -f
<manifest> --prune previews that set; running it first is optional.

```
marvel work <manifest.toml> [flags]
```

### Options

```
      --prune   also remove the teams this file no longer names; marvel plan -f <manifest> --prune previews them
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel](marvel.md)	 - Agent orchestration control plane
