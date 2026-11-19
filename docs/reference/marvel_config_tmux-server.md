## marvel config tmux-server

Print the tmux server name this HOME's daemon uses

### Synopsis

Print the tmux server name (`tmux -L <name>`) for this HOME.

Since marvel #128 each HOME gets its own tmux server, so a bare
`tmux kill-session -t marvel-<workspace>` reaches the wrong server. Scripts
and runbooks use this to get the name instead of recomputing it:

  tmux -L "$(marvel config tmux-server)" kill-session -t '=marvel-demo'

MARVEL_TMUX_SOCKET overrides the derived name and is reported as-is.

```
marvel config tmux-server
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel config](marvel_config.md)	 - Manage marvel cluster configuration
