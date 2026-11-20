## marvel shift

Initiate a rolling shift for a team

### Synopsis

Initiate a rolling shift for a team.

A role that declares a handoff (shift.handoff and shift.handoff_marker) is asked
for it first: the seat is sent a notice, marvel waits up to handoff_window for
the marker, and the shift starts when it arrives. If the window ends without it,
the seat keeps running and marvel emits team.shift-handoff-missing; run the shift
again with --no-handoff to proceed without one. A shift of the whole team that
covers such a role is refused until you name the role with --role or pass
--no-handoff. A role that declares no handoff shifts at once.

```
marvel shift <workspace/team> [flags]
```

### Options

```
      --no-handoff    shift without asking a role that declares a handoff for it
      --role string   shift only this role (default: all roles)
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
