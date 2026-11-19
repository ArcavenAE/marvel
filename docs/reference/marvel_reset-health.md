## marvel reset-health

Clear a role's crash-loop restart count and backoff without deleting the team

### Synopsis

Clear a role's accumulated crash-loop state.

A role's RestartCount only ever climbs and drives the backoff window for its
next crash, so a long-lived healthy role that crashed a few times long ago
still gets a lifetime-sized backoff the next time it fails. Success-based decay
resets the count automatically after a role has run healthy for a while; this
verb is the operator override for when you know a role is fine now.

It is also the only way to thaw a role frozen by max_restarts saturation or
restart_policy=never, short of deleting and re-applying the whole team.

```
marvel reset-health <workspace/team> [flags]
```

### Options

```
      --role string   role to reset (required)
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
