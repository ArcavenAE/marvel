## marvel scale

Scale a team role to N replicas

### Synopsis

Scale a team role to N replicas

```
marvel scale <workspace/team> [flags]
```

### Options

```
      --replicas int   desired replica count (default 1)
      --role string    role to scale (required)
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
