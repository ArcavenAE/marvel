## marvel backend verify

Name each session's backend and report any disagreement

### Synopsis

Name the backend each session is actually on, and report every layer that
disagrees.

macOS does not let one process read another's environment, so this does not ask
a live pane what backend it ended up on. It verifies against the artifacts
marvel controls: the settings overlay marvel wrote and passed to the harness,
and the intended/resolved pair marvel recorded when the session spawned.

Four things can disagree, and each is reported by name:

  - marvel never classified the session (an adopted pane, or a record written
    before the field existed), so the answer is "cannot tell" rather than a pass
  - the spawn environment resolved to a backend the role did not declare
  - the overlay on disk selects something other than what was declared, or
    something other than what marvel recorded at spawn
  - an IAM session has no refresh marvel can vouch for, so a credential expiry
    mid-shift will need a human

The RESOLVED column names the IAM or static variant for the AWS backends. The
selector cannot distinguish them, so the variant is named from the credential
source the role declared: a helper-script path or a profile name, never a
credential.

Exits non-zero when any session reports a problem, so a shakedown can gate on
it.

```
marvel backend verify [workspace/session] [flags]
```

### Options

```
      --json   emit the full verification records as JSON
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel backend](marvel_backend.md)	 - Inspect the model backend sessions are running on
