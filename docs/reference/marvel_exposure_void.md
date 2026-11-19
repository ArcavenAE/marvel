## marvel exposure void

Void a wrong record: the gate stops counting it; nothing is deleted (local socket only)

### Synopsis

Void a wrong record: the gate stops counting it; nothing is deleted (local socket only)

```
marvel exposure void <workspace/team> <role> <function> --reason WHY [flags]
```

### Options

```
      --reason string   why the record was wrong (required)
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
