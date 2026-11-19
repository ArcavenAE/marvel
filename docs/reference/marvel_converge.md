## marvel converge

Release a team (or all teams) from the start-line hold and converge to desired

### Synopsis

By default a daemon holds each team at the start line: it adopts surviving
panes but does not spawn toward a team's desired replica count until told to.
converge is that go-line. With no argument it converges every team; with a
workspace/team it converges just that one.

```
marvel converge [workspace/team]
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
