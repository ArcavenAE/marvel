## marvel version

Print marvel version and channel

### Synopsis

Print marvel version and channel

```
marvel version [flags]
```

### Options

```
      --capabilities   print what this binary can do for a daemon that execs into it, and exit (local; no daemon is asked)
      --json           with --capabilities, print one JSON object
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
