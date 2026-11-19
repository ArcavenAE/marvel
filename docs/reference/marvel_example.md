## marvel example

Print a manifest the binary carries

### Synopsis

Print an example manifest that is built into marvel, so a first run needs
no source tree. With no name it lists the examples.

  marvel example demo > demo.toml
  marvel work demo.toml

A bare name prints the TOML file. A name ending in .toml or .yaml prints that
format, for an example that ships both:

  marvel example first-cluster.yaml > first-cluster.yaml

The demo runs two shell loops and needs only tmux. This command is local: it
does not contact a daemon.

```
marvel example [name]
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
