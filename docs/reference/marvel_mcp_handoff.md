## marvel mcp handoff

Serve the submit_handoff tool over MCP on stdin and stdout

### Synopsis

Serve one MCP tool, submit_handoff, so a seat that cannot write a file or run
a shell command can still hand its work to its successor. The tool sends the
text to the daemon as marvel handoff put does, with the seat's own heartbeat
token, and the daemon writes the role's declared handoff file.

The server dials only the daemon socket named by its own MARVEL_SOCKET and
refuses to start with --socket, --cluster or --identity set. It is started by
the seat's harness, not by hand.

```
marvel mcp handoff
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel mcp](marvel_mcp.md)	 - MCP servers marvel offers to its seats
