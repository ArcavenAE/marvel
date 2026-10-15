# MARVEL_HOME: naming the layout root

One variable, `MARVEL_HOME`, and one flag, `--marvel-home`, name the directory
marvel calls its home. It is `~/.marvel` unless one of them says otherwise.
That is the whole feature, and unset it changes nothing.

## Why

A second daemon on a host needs its own files. Before this, the only lever was
`HOME`, which moves the toolchain's caches and the harness's login with it
(interactive claude reports "not logged in" under an overridden `HOME`), or
five per-daemon flags plus `MARVEL_TMUX_SOCKET`, which two daemons under one
`HOME` still share files through: the bus store and the broker pid file come
from the layout, not from a flag (marvel#883). A named root is one value that
moves all of them together, and leaves `HOME` alone.

Ruled by the operator on 2026-10-14 (aae-orc-7t7d).

## The value

An absolute path, cleaned. Refused, with an error that names the source and
the value:

- a relative path, including `.`;
- an empty `--marvel-home=` (an empty `MARVEL_HOME` is read as unset, the way
  a shell exports it);
- a root whose control socket, `<root>/run/marvel.sock`, would not fit in a
  unix socket path (`paths.CheckSocketPath`). This is checked when the root is
  resolved, so a long root fails at the command and not later at listen.

## Precedence

`--marvel-home`, then `MARVEL_HOME`, then `$HOME/.marvel`.

The flag is applied in `main`, before the command tree is built, by setting
`MARVEL_HOME`; `paths.Default()` reads only the variable. It is not set in
`PersistentPreRun`: `marvel daemon` binds the defaults of `--log-file`,
`--pidfile` and `--state-bolt` when its command is constructed, so a variable
set after that would move the socket and the config and leave those three
under the old root. The flag is also declared on the root command so cobra
accepts and documents it. A bare `--` ends the scan, so a command run through
`marvel run` keeps its own arguments.

The socket precedence of daemon-isolation Decision 3 is unchanged: `--socket`,
then an explicit `--cluster`, then `MARVEL_SOCKET`, then the cluster entry,
then the layout default. Only the last rung moves, to `<root>/run/marvel.sock`.

## What moves

Everything derived from `Layout.Home` or `Layout.Tag()`: `config.yaml`, keys
and `known_hosts`, the log, run and state directories, the control socket, the
bolt store, the daemon pid file, the bus store, the broker's conf and pid file,
the harness-home and stream tags, and the tmux server name. Two roots give two
tags, so two daemons share none of these.

`config.configPath()` asks the layout, so the client reads and writes the
config in the root.

## What does not move

These read the user's real home on purpose, and each has a test that sets
`HOME` and `MARVEL_HOME` apart:

- the `~/.ssh/id_ed25519` and `id_rsa` fallback for an outbound SSH dial;
- the harness's own directories: codex's `~/.codex` source and the
  claude and codex default config directories that name an account;
- the `~/` in a workspace's `trusted_folders`;
- the `~/` in a role's handoff path template;
- a cluster entry's `~/` in `config.yaml`, which stays the user's home.

marvel's own client key, `keys/client_ed25519`, is in the root and does move.

## Seats

A seat's environment carries `MARVEL_HOME` beside `MARVEL_SOCKET`, but only
when the root is not the default, so a seat of a default-rooted daemon sees
the environment it always saw. Both builders, the adapter path
(`runtime.baseEnv`) and the direct path (`Manager.directCommand`), do it. A
seat already running keeps the environment it started with.

## Limits

- Two daemons under one root are still one cluster. The root names a cluster;
  it does not make two clusters share one.
- A relocated root does not move a running daemon. Start the daemon under the
  root you mean.
- The tmux server name is derived from the root, so a new root is a new tmux
  server; adopt-on-restart finds the old panes only under the old root.
