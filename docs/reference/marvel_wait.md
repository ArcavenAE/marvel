## marvel wait

Wait for a session to finish (experimental)

### Synopsis

Wait for a session to reach a terminal state, reading its stored state.

Exit codes: 0 succeeded, 1 failed, 2 crashed, 3 timeout (still running),
4 unknown key, 5 the daemon could not be reached or answered with an error
(the session's state is not known; a caller should treat it as still pending).
A refused flag or a bad argument exits 1.

```
marvel wait <session-key> [flags]
```

### Options

```
      --experimental       allow this experimental command
      --poll duration      how often to read the session state (default 5s)
      --timeout duration   give up after this long and exit 3 (default 10m0s)
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
