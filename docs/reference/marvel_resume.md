## marvel resume

Resume a suspended cluster

### Synopsis

Resume a suspended cluster: send SIGCONT to every recorded process, seats first
and the tmux server last, shift each resumed seat's heartbeat by the length of
the pause, and lift the daemon's hold. A daemon that started with the cluster
suspended then adopts the live panes in the background; marvel status shows it
until it finishes. There is no dialog.

A seat whose process had gone, or that still reads stopped, is listed and the
exit code is 3. Nothing is restarted.

Limits in this release:
  - The processes are collected from one snapshot of the process table, plus one
    walk after the tmux server stops. A process forked after that walk keeps
    running while its seat is stopped.
  - Resume shifts each resumed seat's heartbeat by the length of the pause. It
    does not yet shift the other timers (activity stamps, backoff, health
    windows), so those read the pause as elapsed time.
  - Bus presence lapses during a suspend and returns shortly after resume.

Exit codes: 0 resumed, 3 resumed with processes gone or still stopped, 1 error.

```
marvel resume
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
