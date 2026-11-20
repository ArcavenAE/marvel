## marvel handoff put

Write this seat's handoff through the daemon (text on stdin)

### Synopsis

Send the handoff text on stdin to the marvel daemon, which writes the file the
role declares for it, ending with the role's marker.

For a seat that cannot write a file on the daemon host (a read-only sandbox, or
a deny-Write permission) but can run this one command. The seat's own heartbeat
token, from the environment marvel started it with, says whose handoff it is,
so the command takes no session name and cannot write for another seat. The
daemon accepts it only while the seat holds a pending max-age handoff request,
and only on its own local socket.

The text is capped at 64 KiB. Run it when the seat is asked for a handoff:

  marvel handoff put <<'EOF'
  ...what was done, what is left, what the successor needs...
  EOF

```
marvel handoff put
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel handoff](marvel_handoff.md)	 - Hand a seat's work to its successor through the daemon
