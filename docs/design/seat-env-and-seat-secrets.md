# The environment of a seat, and how a seat gets a secret

Status: design, for review before the second and third PRs. The first PR
(`seat_env.path_prepend` and `seat_env.unset`) is built and is described in
section 3.

## 1. Why

A seat's pane starts with the environment of the daemon's tmux server, which is
the environment the daemon was started with. Two things follow today.

- A daemon under launchd or systemd has a short `PATH`, so a seat can miss tools
  an interactive shell finds. The fix needs nothing secret.
- Every seat inherits every variable the daemon carries, including the bd dolt
  password (`BEADS_DOLT_PASSWORD`) when the daemon was started from a shell that
  sourced the bd client environment. A seat that never runs `bd` holds write
  access to the shared bd server for the life of its pane, and a process listing
  with environments (`ps -E`) shows the key to any process of the same user.

The aim is that a seat holds a secret only when its role says it needs one, that
the value never rides a command line or sits in a file, and that the daemon
carries it in memory only (ADR-009 as amended, 2026-10-19).

Rulings this note applies: the director's ruling of 2026-11-23 on the seat secret (the daemon reads the
value once and holds it in memory; no argv and no file at rest; a per-spawn read
of the keychain by a seat is refused, because seats may not read the keychain)
and director's rulings of 2026-11-23 (FIFO default, `seat_secrets`, strip the
tmux server environment; valid until 2026-11-30).

## 2. What was measured

Script `d716.sh` (kept with the PR evidence). A dedicated tmux socket, a fake
marker value, and a sampler that reads `ps -axww -o command=` about seven times
a second for the marker, with the pattern read from a file so the sampler does
not match itself. A control that puts the marker on a long-lived process's
argument list is seen in 48 of 66 samples, so the sampler can see a leak.

| Hand-over | Pane got the value | Argument-list hits | File at rest |
|---|---|---|---|
| FIFO in a 0700 directory, read once by the pane prelude | yes | 0 of 65 | none; the empty FIFO node is left until removed |
| Inherited descriptor from the tmux client | no, the pane read nothing | 0 of 57 | none |
| `set-environment` fed on stdin through `source-file -`, then `set-environment -u` | yes | 0 of 70 | none |

The inherited descriptor does not work: tmux does not pass the client's
descriptors to a pane. `set-environment` works, but while it is set every window
created in that session gets the value, so it needs a lock around set,
`new-window` and unset to keep a second seat from receiving it. The plain
`tmux set-environment NAME value` puts the value on the tmux argument list, which
is why the driver refuses a secret by value today (`secretValueKeys`).

## 3. `seat_env`, the first PR

`seat_env` on the daemon's own cluster entry (found by socket, as
`account_warn_percent` is):

```yaml
seat_env:
  path_prepend: ["~/.local/bin"]
  unset: [AWS_PROFILE]
```

`path_prepend` goes in front of the daemon's `PATH` as `-e PATH=` unless the
role sets `PATH`; `unset` names join the `env -u` prefix of the pane command.
Bad names and directories are refused when the config loads, and the daemon
ignores a bad block with a log line instead of failing to start. It carries no
secret and no value other than directories.

## 4. The secret hand-over, the next PRs

### 4.1 Declaring a need

A role declares what it needs by name, never by value:

```yaml
roles:
  builder:
    seat_secrets: [bd]
```

The cluster entry maps the name to an environment variable:

```yaml
seat_env:
  secrets:
    bd: BEADS_DOLT_PASSWORD
```

A manifest therefore holds a need, and the host holds the mapping. A role that
names a secret the cluster does not map is refused at apply with the role and the
name, not the value. A role with no `seat_secrets` gets none.

### 4.2 Where the value lives

The daemon reads the value once, at its own start, from where it lives today (its
own environment), then removes it from its process environment. From then on it
is held in memory only, in a type whose printed forms are redacted. Removing it
from the process environment means the tmux server the daemon starts later never
holds it, and no child the daemon execs inherits it.

A reexec replaces the process image, so the value has to cross it. The existing
credential handoff (`reexec_handoff.go`, a pipe whose descriptor number rides one
environment variable) is the channel, and its `carryTable` needs an entry for
this kind. Carrying is a custody decision, so this note proposes carried and asks
for it to be ruled with the PR.

### 4.3 The hand-over

For a seat whose role declares a secret, the daemon creates a FIFO in a fresh
directory of mode 0700 under the marvel run directory, one per seat, and starts
the pane with a prelude that reads it once:

```sh
bash -c 'IFS= read -r -t 10 V < "$1" && export NAME="$V"; unset V; exec sh -c "$2"' -- <fifo> '<the original command>'
```

- Only the process that opens the FIFO gets the value. The command line holds the
  FIFO path and the seat's own command, never the value.
- No `eval`. The value is read into a variable and exported; the original command
  is run by `sh -c` from an argument.
- `read -t 10` bounds the wait, so a daemon that dies between `new-window` and
  the write does not leave a pane hung. `bash` is `/bin/bash` 3.2 on macOS, which
  has `read -t`.
- The daemon opens the FIFO for writing without blocking and retries until the
  reader is there or the bound passes, writes the value and a newline with a
  builtin-style write (no process holds the value on its argument list), closes
  it, and removes the directory.
- Fail open. If the write or the read times out, the seat starts without the
  value, the daemon logs a line naming the seat and the key (never the value),
  and the session records a note that `marvel doctor` shows as a `WARN
  seat-secret` line, as the privacy-folder note is shown (`tcc-deny`).

The fallback, if a host cannot use a FIFO, is `set-environment` fed on stdin,
under one lock around set, `new-window` and unset.

### 4.4 Removing it from seats that do not need it

The tmux server environment is stripped of the dolt password: the driver's scrub
list gains `BEADS_DOLT_PASSWORD`, and a server started earlier by a dirty process
gets `tmux set-environment -g -u BEADS_DOLT_PASSWORD` at daemon start (a name on
the argument list, no value). The pane prefix of a seat that does not declare the
secret also carries `env -u BEADS_DOLT_PASSWORD`, which covers a pane started
before the strip. The proof is `ps -E` for the tmux server and for a pane of a
seat that does not declare the need, printing key names only: the key is absent
for that seat and present for one that declares `seat_secrets: [bd]`.

Sequencing. The strip landed opt-in in its own PR. With the hand-over in place
it is the default: a cluster whose `seat_env` has no `secrets` key maps
`bd: BEADS_DOLT_PASSWORD`, and `secrets: {}` opts out. A role that runs `bd`
lists `seat_secrets: [bd]`, so the manifests of those roles change in step with
the merge of this default, and the merge waits for the operator's go.

## 5. What this does not do

- It rotates nothing and touches no credential at the third party (the standing rule against rotating credentials).
- No seat reads the keychain, and nothing here adds a keychain read per spawn.
- It writes the value to no file, log, event or argument list. The FIFO node
  exists for the length of one write and carries no data.
- It does not stop another process of the same user from reading a pane's
  environment with `ps -E` while the pane runs. The aim is that a seat holds the
  value only when its role needs it, not that the same user cannot see its own
  processes.

## 6. The plan

Each PR is red first, with a second seat running the red commit, and every test
uses a fake value.

1. `seat_env.path_prepend` and `seat_env.unset` (done, local).
2. Strip `BEADS_DOLT_PASSWORD` from the tmux server environment and add it to the
   pane prefix of a seat that does not declare it; the `ps -E` proof.
3. `seat_secrets` on a role, `seat_env.secrets` on the cluster, the FIFO
   hand-over, the in-memory holder and its reexec carry, and the doctor WARN.
4. Admin guide text for the whole block.

## 7. Open points for the reviewer

- The `carryTable` entry for this credential kind (section 4.2).
- Whether the daemon's source is its own environment only, or also the file the
  bd client environment names; this note takes the environment, which is where
  the value is today.
- The `read -t` bound: 10 seconds is a guess to be measured under load.
