## marvel status

Say whether the cluster is running or suspended

### Synopsis

Say whether the cluster is running or suspended.

The daemon answers from its suspend record without taking its controller lock
or calling tmux, so status answers while the fleet is frozen. If the daemon
does not answer, status reads the mirror the daemon keeps beside its socket,
and says so. A TCP or mrvl:// address has no mirror on this host.

A suspended cluster lists the manual kill -CONT recipe: for the operator to
run by hand only if marvel cannot.

Exit codes: 0 running, 3 suspended, partway through a suspend or resume, or
unknown (the stored record could not be read; the mirror is left in place),
1 error.

```
marvel status
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
