## marvel reap

List (and with --confirm, destroy) marvel tmux state the daemon does not own

### Synopsis

List (and with --confirm, destroy) marvel tmux state the daemon does not own.

With --homes, list instead the harness homes and orphaned codex app-server daemons under
this daemon's two home bases. A home a session record names is never touched. A home no
record names is removed with --confirm only when it holds no codex history; one that does
is kept and listed. A daemon pid is signalled (SIGTERM, once) only when it is written in a
pid file inside such a home and the live process has the recorded start time.

With --empty-bases, list instead the temp bases other layouts left under /tmp/marvel-h-<tag>.
With --confirm, only a base that is a plain directory, mode 0700, owned by this user and empty
is removed, with one rmdir and never a recursive delete; this layout's own base is never listed.

```
marvel reap [flags]
```

### Options

```
      --confirm       actually destroy the listed state (without this, reap only lists)
      --empty-bases   remove other layouts' empty temp bases under /tmp (one rmdir each) instead of tmux state
      --homes         sweep harness homes and orphaned codex app-server daemons instead of tmux state
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
