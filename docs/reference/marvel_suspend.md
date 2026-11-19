## marvel suspend

Freeze every seat and the tmux server in place

### Synopsis

Freeze every seat and the tmux server in place, so the fleet keeps its
context and its in-flight turns while nothing runs. The daemon keeps
answering; marvel status reports the suspend and marvel resume ends it.

On a terminal, suspend states how many seats it freezes and asks [y/N]. With no
terminal it refuses unless --yes is given.

Limits in this release:
  - The processes are collected from one snapshot of the process table, plus one
    walk after the tmux server stops. A process forked after that walk keeps
    running while its seat is stopped.
  - Resume shifts each resumed seat's heartbeat by the length of the pause. It
    does not yet shift the other timers (activity stamps, backoff, health
    windows), so those read the pause as elapsed time.
  - Bus presence lapses during a suspend and returns shortly after resume.

Exit codes: 0 suspended, 1 error.

```
marvel suspend [flags]
```

### Options

```
      --yes   suspend without asking
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
