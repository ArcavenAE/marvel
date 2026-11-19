## marvel daemon

Start the marvel daemon

### Synopsis

Start the marvel daemon. Listens on a Unix socket for local access.
Use --mrvl to also start the mrvl:// listener for remote access.

Examples:
  marvel daemon                              # Unix socket only
  marvel daemon --mrvl                       # + mrvl:// on port 6785
  marvel daemon --mrvl=:7000                 # + mrvl:// on custom port
  marvel daemon --socket /var/marvel.sock    # custom socket path
  marvel daemon --log-file= --pidfile=       # disable log tee and pidfile
  marvel daemon --state-bolt=                # disable L2 persistence (in-memory only; daemon restart kills agents)

```
marvel daemon [flags]
```

### Options

```
      --log-file string          tee daemon stderr to this file (empty string disables) (default "~/.marvel/log/daemon.log")
      --log-max-files int        keep at most N gzipped archives of --log-file (0 keeps all) (default 5)
      --log-max-size int         rotate --log-file when it exceeds this size in MiB (0 disables rotation) (default 10)
      --log-max-total int        cap total disk usage across --log-file and archives in MiB (0 disables)
      --mrvl seconds[=:6785]     start mrvl:// listener (use --mrvl=:<port> for a custom port)
      --pidfile string           write pid to this file on start, remove on stop (empty string disables) (default "~/.marvel/run/daemon.pid")
      --reclaim                  destroy marvel tmux state this daemon does not own, instead of leaving it running
      --shift-timeout duration   abort and roll back a shift that has not reached readiness within this Go duration (0 uses the built-in 10m default; env MARVEL_SHIFT_TIMEOUT is the fallback when this flag is unset)
      --socket string            Unix socket path (default ~/.marvel/run/marvel.sock, or $MARVEL_SOCKET)
      --state-bolt string        bbolt L2 file for durable state (empty string disables persistence; daemon restart loses state and kills running agents per pre-L2 contract C12) (default "~/.marvel/state/marvel.bolt")
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
```

### SEE ALSO

* [marvel](marvel.md)	 - Agent orchestration control plane
* [marvel daemon logs](marvel_daemon_logs.md)	 - Print the daemon's recent log lines (over mrvl:// if --cluster is set)
* [marvel daemon reexec](marvel_daemon_reexec.md)	 - Re-exec the running daemon in place into the binary this command runs from
