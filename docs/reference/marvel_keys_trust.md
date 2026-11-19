## marvel keys trust

Trust and record a cluster's host key in ~/.marvel/known_hosts

### Synopsis

Connect to the named cluster (or the current one) and add its
host key to ~/.marvel/known_hosts.

The command prints the key type and SHA256 fingerprint the daemon
presented and asks before it records the key. Run
'marvel keys host-fingerprint' on the daemon machine and compare the
two before answering y.

With --yes it records the key without asking, still printing the
fingerprint. Use that in scripted bootstraps where the fingerprint was
checked out of band. Without a terminal and without --yes the command
refuses and names the fingerprint.

```
marvel keys trust [cluster] [flags]
```

### Options

```
      --yes   record the host key without asking (the fingerprint is still printed)
```

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel keys](marvel_keys.md)	 - Manage SSH keys for marvel clients and the daemon
