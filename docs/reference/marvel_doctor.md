## marvel doctor

Check this host for what a first run needs

### Synopsis

Check this host for what a first run needs, and say what is missing.

doctor prints one line per check, and a fix line under each failure. A stage
with nothing configured reads SKIP, not FAIL. Name a manifest to check its
harness commands, their logins and the director shim.

The install lines report what the role-library install and the director installs
record as their version, and the last line the installed-copies job logged for
each. The role-library install is read from the directory named by
MARVEL_ROLE_LIBRARY_DIR, under the job name in MARVEL_ROLE_LIBRARY_JOB; with
neither set the line reads "not configured". They compare nothing with a remote, never read as a failure, and read
"not installed" or "unreadable" where a record is missing or cannot be read.

doctor is read-only: it writes nothing and changes no setting. It never reads
a credential file; a harness login is asked of the harness's own status
command. It prints no command text and no env value from a manifest.

The live seats are read only from a daemon on a local unix socket. For a remote
cluster (an mrvl:// or ssh:// address) the seats read SKIP and nothing is
dialed. --remote asks for that read: it loads the cluster's identity key, dials
the host, and may offer an unknown host key for trust, as any marvel command
that talks to a remote cluster does. With no known_hosts file yet, doctor skips
the read and creates nothing: run one interactive remote command to trust the
host first.

doctor exits nonzero when a check failed, for your own scripts. No marvel
command reads that code.

```
marvel doctor [manifest] [flags]
```

### Options

```
      --remote   also read the live seats of a remote cluster (dials it and loads the identity key)
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
