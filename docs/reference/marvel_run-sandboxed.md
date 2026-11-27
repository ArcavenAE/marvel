## marvel run-sandboxed

Run a command under a sandbox profile the daemon composes, outside this seat's own profile

### Synopsis

Ask the marvel daemon to run a command for this seat. A seat under marvel's
default privacy deny cannot start its own sandbox-exec (a nested profile that
differs from the outer one is refused, exit 71); the daemon is not under the
seat's profile, so it runs the command instead and returns the output and
exit status.

The daemon never applies the profile on its own. It composes the role's own
profile, then the requested profile, then the privacy deny block, and refuses a
request outside a closed grammar: deny forms, plus the three localhost network
allows the run conditions use, and no (version), (allow default), (define),
(import), (param) or comment tricks. The seat is identified by its heartbeat
token, from the environment marvel started it with.

The command runs with this process's environment (minus the heartbeat token)
and working directory, whole output at exit, no stdin and no tty, capped at 1 MiB
per stream. The daemon ends the run when this connection reaches end of file, so
the client must keep its side of the socket open until the answer comes (this
command does). This command exits with the command's own status; a daemon refusal
exits 1 with the reason on stderr.

  marvel run-sandboxed --profile run.sb -- /usr/bin/env go test ./...

```
marvel run-sandboxed [flags] -- <program> [args...]
```

### Options

```
      --dir string       working directory for the command (default: this one)
      --profile string   file holding the run profile (deny forms and the localhost network allows)
      --timeout int      seconds before the daemon kills the command (default 600, at most 3600)
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
