## marvel exposure

Record, end or void the functions a slot has read (the clean-room exposure gate)

### Synopsis

A role that declares writes: [function] is not started while its slot
(workspace, team, role) has an exposure record for that function. The record
is kept outside the session rows, so a restart, a crash respawn, a shift and an
ad-hoc run all see it.

  marvel exposure add  <workspace/team> <role> <function> [--path P]... [--caster WHO]
  marvel exposure end  <workspace/team> <role> <function>
  marvel exposure void <workspace/team> <role> <function> --reason WHY

end marks a reading as ended; the refusal still counts it. void is the way out
of a wrong record: it is served on the local socket only (any seat on the
host can reach it), it appends a reason, and the gate stops counting the
record. Nothing is ever deleted.

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel](marvel.md)	 - Agent orchestration control plane
* [marvel exposure add](marvel_exposure_add.md)	 - Record that the slot read a function
* [marvel exposure end](marvel_exposure_end.md)	 - Mark the slot's readings of a function as ended (they still count)
* [marvel exposure void](marvel_exposure_void.md)	 - Void a wrong record: the gate stops counting it; nothing is deleted (local socket only)
