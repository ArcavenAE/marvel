## marvel exposure add

Record that the slot read a function

### Synopsis

Record that the slot read a function

```
marvel exposure add <workspace/team> <role> <function> [flags]
```

### Options

```
      --caster string      who records it (default: the caller)
      --path stringArray   a path that was read (repeatable)
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel exposure](marvel_exposure.md)	 - Record, end or void the functions a slot has read (the clean-room exposure gate)
