## marvel plan

Preview the convergence delta per team without spawning (dry run)

### Synopsis

Print the plan the next reconcile tick would enact: desired vs alive
replica counts, sessions that would spawn, sessions that would scale down, and
any hold (crash-loop backoff or admission refusal), computed without applying
it. No session is spawned or deleted, so this is a spawn-free way to inspect
what the daemon is about to do.

With no argument every team is shown. An optional workspace/team narrows the
preview; a bare name matches that workspace or that team.

Teams mid-shift are omitted: their convergence runs at shift-aware
generations, which this steady-state preview does not model.

With -f <manifest> --prune it instead previews what marvel work --prune would
delete for that file, and why every other team is kept. It writes nothing
(docs/design/work-prune.md section 4).

```
marvel plan [workspace/team] [flags]
```

### Options

```
      --detailed-exitcode   with --prune, exit 2 when something would be deleted
  -f, --file string         the manifest to plan (with --prune)
      --prune               preview what marvel work --prune would delete for -f <manifest>
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
