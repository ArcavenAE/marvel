## marvel daemon reexec

Re-exec the running daemon in place into the binary this command runs from

### Synopsis

Re-exec the running daemon in place.

The daemon checkpoints its state, releases its state file, and replaces
its own process image (same PID) with a fresh exec of a marvel binary.
Run on the daemon's host, this command names the binary it runs from, so
an install that keeps each version in its own directory (mise) adopts the
new build. Against a remote daemon (--cluster over mrvl://) no path is
sent, and the daemon re-executes its own path. A target that is not a
regular executable owned by the daemon's user, that is writable by others,
that was not built from marvel with a stamped version (the daemon reads the
build from the file and does not run it), or that is older than the
running daemon is refused, and the daemon keeps serving. Every agent keeps
running in its tmux pane; the new process re-opens the same state file,
re-binds the same socket, and adopts those panes.

Use this after installing a new binary out of band. To fetch, install,
and adopt in one step, use 'marvel upgrade --daemon'.

```
marvel daemon reexec
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel daemon](marvel_daemon.md)	 - Start the marvel daemon
