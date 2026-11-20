## marvel run

Run a one-off agent session

### Synopsis

Run a one-off agent session

```
marvel run <command> [args...] [flags]
```

### Options

```
      --env stringArray      KEY=VALUE for the session environment, repeatable (experimental)
      --experimental         allow the experimental flags above
      --mode string          launch mode: headless (experimental)
      --name string          session name, so the key is workspace/name; a reused name is refused (experimental)
      --prompt-file string   file holding the prompt a headless run carries (experimental)
      --role string          role name (default "adhoc")
      --script string        Lua script path
      --team string          team name (default "adhoc")
      --workdir string       directory the session runs in; default the current directory
      --workspace string     workspace name (default "default")
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
