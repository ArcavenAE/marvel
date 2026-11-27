## marvel attach

Open a session's tmux pane by desk number, session key or name

### Synopsis

Open a session's tmux pane by desk number, session key or name.

The argument is one of three forms, resolved on the selected cluster
(--cluster) only:

  - a desk, the DESK column of `marvel get sessions --columns name,desk` (opt-in), with or without a leading
    percent sign (23 or %23);
  - a session key, workspace/name, matched exactly;
  - a bare session name, accepted only when exactly one session on the cluster
    has it. A name shared by several sessions is refused and every key is
    listed.

Digits that are both a desk and a session name are refused as ambiguous: pass
%23 for the desk or the key for the session. Every successful resolution
writes one line to stderr naming the session and its desk, so a reused desk
number shows. The verb reads the tmux socket from the daemon and attaches to
the pane.

  - Outside tmux it replaces itself with `tmux -L <socket> attach -t %<desk>`.
  - Inside marvel's own tmux server it runs switch-client, so the client moves
    to the pane and nothing nests.
  - Inside any other tmux server it opens a new window on that server, running
    the attach to marvel's server.
  - --print prints the command and exits 0 in every case.

A desk on a remote cluster is run over the cluster's mrvl:// channel (port 6785)
with the key the cluster pins, which must be an admin key on that daemon. The
daemon refuses unless its own cluster entry sets allow_remote_attach: true, which
it reads at start, so a change needs a daemon restart. It needs no ssh login on
the remote host.

What a remote attach reaches: the session is a full tmux client of the daemon's
tmux server, and nothing unbinds its prefix or command prompt. With the gate on,
an admin key can open windows, run commands and switch to any session on that
server, so it is a shell as the daemon's user and not one desk. --print shows the ssh form instead,
`ssh -t <host> tmux -L <socket> attach -t %<desk>`, which uses your own ssh
access: the host is the one in the cluster's address, so it may need a login
name or an ssh config entry of your own. Against a daemon too old to serve the
channel, the verb prints that ssh line, says to upgrade the daemon, and exits
nonzero. A remote attach checks the link every 5 seconds; if the daemon stops
answering for 15 seconds (a closed lid, a dropped route), the verb prints
"attach: lost contact with <address> (no reply for 15s)" and exits nonzero.

A desk is unique only within one cluster's tmux server, so a desk number is
never looked up across clusters.

Leaving the session. Typing exit or Ctrl-D in its pane stops the agent; detaching
from tmux does not. Detach with Ctrl-B, then d. Ctrl-B is tmux's default prefix;
marvel does not change it, and a prefix set in your own tmux configuration
replaces it in the keys below. Where you land depends on how the session was
opened (measured on tmux 3.7b):

  - Outside tmux, and the watch's Enter outside tmux: Ctrl-B d detaches and
    returns you to the shell, or to the watch.
  - Inside marvel's own tmux server: Ctrl-B d detaches from tmux altogether and
    does not return you to the watch. Ctrl-B L switches back to the session you
    came from, and the agent keeps running.
  - Inside any other tmux server: the session is in a new window of that server,
    and Ctrl-B goes to your own tmux. Ctrl-B p moves to the previous window and
    leaves the session attached. Ctrl-B Ctrl-B d detaches the session and closes
    its window. Ctrl-B d detaches your own tmux, not the session.

```
marvel attach <desk|key|name> [flags]
```

### Options

```
      --print   print the command and exit 0 instead of running it
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
