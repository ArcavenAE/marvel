## marvel hibernate

Sign off the named teams, end every seat, and hold the teams until a wake

### Synopsis

Ask every seat of the named teams for its handoff, end each seat once its
handoff ends in its marker, and mark the team hibernated so nothing respawns it.
Workers are asked together, then the supervisors, once every worker has ended or
been reported. A seat that writes no handoff is never ended by this command: it
is reported by name and keeps running. marvel hibernate --end <session> ends that
one seat without a handoff, which loses its context.

Seats return as successors that read their handoff: marvel wake starts each one
fresh, and nothing carries over except that handoff.

--backend and --except-backend select roles by their declared backend label;
the word "none" names a role with no label. A selected subset is marked per role,
and the rest of the team keeps running.

On a terminal it states how many seats it signs off, per label, and asks [y/N].
With no terminal it refuses unless --yes is given. It waits until every team has
settled or every remaining seat is reported; --no-wait returns at once.

Exit codes: 0 hibernated, 3 a seat was reported with no handoff, 1 error.

```
marvel hibernate <workspace/team>... | --all | --end <session> [flags]
```

### Options

```
      --all                      hibernate every team
      --backend strings          hibernate only roles on these backend labels (none names an unlabeled role)
      --end string               end this seat without a handoff (a seat of a hibernating team)
      --except-backend strings   hibernate every role except those on these backend labels
      --no-wait                  return once the sign-off has started
      --yes                      hibernate without asking
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
