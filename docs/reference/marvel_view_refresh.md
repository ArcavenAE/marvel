## marvel view refresh

Follow a seat's view branch now

### Synopsis

Follow the branch of one view of a seat now, or of every view the seat
declares when no name is given, without waiting for the refresh_every tick.
The seat reads the new tree through the same path once the swap lands.

```
marvel view refresh <session> [<name>]
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel view](marvel_view.md)	 - Work with a seat's read-only views
