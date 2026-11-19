## marvel stop

Stop the marvel daemon, leaving agents running

### Synopsis

Stop the marvel daemon.

By default this detaches: the daemon checkpoints its state and exits
while every agent keeps running in its tmux pane. The next
'marvel daemon' start reads that state back and adopts the live panes,
so a restart or an upgrade costs no agent context.

Use --teardown when you want the machine clean. Every session this
daemon recorded is deleted and every recorded workspace's tmux session
killed before it exits, so it leaves nothing of its own to adopt.
marvel-* tmux state it never recorded is reported rather than
destroyed; 'marvel daemon --reclaim' and 'marvel reap --confirm' are
the acts that destroy that.

```
marvel stop [flags]
```

### Examples

```
marvel stop              # detach, agents keep running
  marvel stop --keep-bus   # detach and leave a managed nats-server up for the next daemon
  marvel stop --teardown   # end every agent, then stop
```

### Options

```
      --keep-bus   leave a managed nats-server running for the next daemon to adopt (ignored with --teardown)
      --teardown   delete every session and kill its tmux session before stopping
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
