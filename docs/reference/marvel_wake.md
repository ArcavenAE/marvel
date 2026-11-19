## marvel wake

Bring hibernated teams back, supervisors first

### Synopsis

Run the preflight, then wake each hibernated team: the supervisor roles start
first, and the other roles once a supervisor has heartbeated, or after its health
timeout. Every seat starts fresh as a successor of its hibernated session and
reads its handoff. Running wake again on a waking team is safe: it starts no seat
twice.

The preflight checks that the daemon answers, that the bus links the cluster
config declares are up, and that each harness binary is present. A check is
skipped by name with --skip-check (daemon, bus, harness-binary), and the skip is
recorded on the wake event. Credentials are not read.

On a terminal it asks [y/N]; with no terminal it refuses unless --yes is given.
--order wakes the named teams one after another.

Exit codes: 0 woken, 3 a team was still waking when the wait ended, 1 error.

```
marvel wake <workspace/team>... | --all [flags]
```

### Options

```
      --all                  wake every hibernated team
      --order strings        wake these teams one after another, in this order
      --skip-check strings   skip a preflight check by name
      --yes                  wake without asking
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
