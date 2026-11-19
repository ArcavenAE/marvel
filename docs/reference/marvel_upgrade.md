## marvel upgrade

Upgrade marvel to the latest version

### Synopsis

Upgrade marvel to the latest version.

If installed via Homebrew, delegates to brew upgrade. Homebrew installs only
the tap's current formula, so --version is refused there unless it names that
build. Otherwise downloads the latest release from GitHub; --version takes an
exact release tag and nothing else. A dev build is not upgraded. A direct
download is checked against the release's checksums.txt when it publishes
one, and the build it replaces is kept under the marvel home in versions/.

A direct download carries marvel alone. When a marvel-simulator on PATH is
then a different build from the marvel just installed, upgrade prints one
note; it downloads nothing. A Homebrew install replaces both programs.

This replaces the binary on disk. A running daemon keeps executing the
old image until it restarts. Pass --daemon to tell the running daemon to
re-exec in place after the install, adopting its live panes so agents
keep running (see 'marvel daemon reexec'). The daemon is re-executed only
when the installed binary actually changed.

A failed brew upgrade, and a binary owned by a system package manager,
exit non-zero. --daemon is refused, before anything is upgraded, on a
Homebrew install on Linux, where the daemon cannot re-exec into the new build.

```
marvel upgrade [flags]
```

### Options

```
      --daemon           after installing, tell the running daemon to re-exec in place so it adopts the new binary without stopping agents
      --version string   exact release tag to install (default: latest)
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
