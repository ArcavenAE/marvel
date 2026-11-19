# tmux over mrvl: a terminal on the existing SSH channel, behind a feature gate

- **Status:** idea (pre-hypothesis, no commitment). Notes only: nothing is built,
  and nothing here is a design decision. Open questions are listed as questions.
- **Date:** 2026-11-19
- **Origin:** the operator asked that a possible feature be written down and
  feature-gated before anyone builds it: "let's ensure we have notes about a
  potential feature we could/should build and feature gate to enable/disable
  tmux-over-mrvl and document it but don't built it right now" (2026-11-19).
- **Subject:** marvel (the `mrvl://` listener and `marvel attach`). Filed in the
  marvel graph per the subject test.
- **Related:** [[desk-addressed-tmux-connection]] (the `marvel attach` verb this
  would serve), [[mrvl-proxy-nats-and-generic-relays]] (another use of the same
  channel, with its own switchboard question), [[os-native-keystore-for-marvel-ssh-keys]]
  (where the keys that would open this terminal are kept). bd: aae-orc-lj44u.

## The idea

A client that already reaches a cluster over `mrvl://` could open a terminal into
the daemon's tmux over the same SSH connection, with no second login to the
host. The daemon would run the attach on the client's behalf and carry the
terminal bytes on the channel. The feature would sit behind a gate that is off by
default, so a cluster that never turns it on has no terminal path at all.

## What the daemon does today

The SSH listener accepts only `session` channels, and each channel carries one RPC
exchange. A pty request or a shell request on that channel is answered with
success and then dropped: the request loop at `internal/daemon/sshserver.go:152-160`
replies true to every channel request and does nothing else with it, and line 164
hands the channel to the RPC handler. By that reading, a pty or shell request to
the listener succeeds on the wire and the channel then carries RPC, not a shell
(I read the code and did not run a client). Nothing in
the daemon allocates a pty, starts a process on a channel, or forwards window
size changes. (Lines checked against main at bfccaca2.)

This is also why the attach verb prints and does not run for a remote desk: the
code comment in `cmd/marvel/attach.go` says the listener "carries RPC and no
terminal".

## Facets to explore

### The gate

- Where the switch lives: a daemon setting in marvel's config, a manifest field,
  or a flag at start. A cluster-wide switch is the plain reading; a per-key
  switch is covered under scope below.
- Off by default, and what an off daemon says to a pty request: refuse it with a
  reason, or keep today's silent success. Today's silent success is a footgun
  that a gate would be a natural place to remove.
- Whether turning the gate on or off needs a restart, and whether the change is
  written to the event log.

### Its own key scope

The terminal is full control of every agent on the cluster: whoever holds it can
type into any pane, read any pane, and stop any agent. That is more than the
`admin` scope's RPC methods express, so it should not ride on `admin` by
accident. The existing scopes are `admin` (every method) and `credential-push`
(push, list and delete credentials only), recorded per key by
`marvel keys authorize --scope`.

Open questions:

- A new scope name for the terminal, held by keys that have nothing else, or a
  capability that an `admin` key must also be granted.
- Whether the terminal scope implies `admin` or stands apart from it.
- Whether a key can be limited to some desks or some teams, or the terminal is
  all-or-nothing per cluster.
- How an enrolled key is shown in `marvel keys authorized` so an operator sees
  who holds terminal access.

### What the daemon would need

Listed so the size of the work is visible, not to choose among them.

- Handle `pty-req`, `shell`/`exec` and `window-change` requests on a channel
  instead of replying true and dropping them.
- Decide what runs on the channel: `tmux -L <socket> attach -t %<desk>` as the
  daemon's own user, or a client that speaks to tmux's control mode.
- Tell a terminal channel apart from an RPC channel, since today every session
  channel is one RPC exchange.
- Clean up on disconnect: a dropped client must detach, not stop the agent
  (typing exit in the pane still ends the agent's process; see the user guide's
  section on opening a session and leaving it).
- Audit: who attached to which desk, when, for how long.
- Whether input is allowed at all, or a first version is a read-only terminal.

### How `marvel attach --cluster` would use it

For a remote cluster, `marvel attach` today prints
`ssh -t <host> tmux -L <socket> attach -t %<desk>` and runs nothing; the Desk card
lj44u-remote-attach-scope ruled that a remote desk is print-only for now. With the
gate on and a key that holds the terminal scope, the verb could open the channel
and run the terminal itself, and fall back to printing when the gate is off or
the key lacks the scope.

Open questions:

- Whether the verb tries the terminal first and prints on refusal, or needs an
  explicit flag to try it.
- Whether the printed ssh line stays as the fallback, given the ruling below
  that host-native ssh is not a supported or required path.
- How the watch's Enter on a remote row would behave: the same choice, inside
  the watch's terminal handling.
- Window size, and what the client does with Ctrl-B when the outer terminal is
  already inside tmux.

## Relation to switchboard

Switchboard in access mode, managed by marvel as a service, is still the ideal
route for remote terminals, per the operator on 2026-11-19, and it is not a
priority right now. A terminal carried on `mrvl://` would be a nearer-term and
narrower thing: one SSH channel marvel already authenticates, with marvel's own
scopes, and no second component.

Open questions:

- Whether tmux-over-mrvl is a stepping stone that switchboard later replaces, or
  a path that stays beside it for clusters without switchboard.
- Whether the gate and the scope are shaped so that the switchboard route can
  reuse them, so an operator's settings carry over.
- The seam `2tndb` (switchboard access node as a managed remote-access node) and
  `h8dq1` (the switchboard and marvel integration contract) already name; this
  idea should not pre-empt either.
- Whether a terminal on the same channel as RPC mixes two kinds of traffic that
  switchboard would keep apart.

## Ruling: host-native ssh is not required

The operator ruled on 2026-11-19 that host-native ssh "is not on by default in
macos, and we're not going to make it a requirement". So no marvel feature
depends on the host's own sshd being on, and the ssh command that
`marvel attach` prints for a remote desk is a convenience for a host where ssh
already works, not a supported or required path. This is part of why a terminal
on `mrvl://` is worth writing down: it would reach a Mac that has no sshd.

One place reads against the ruling and is left as is, because this PR changes no
code: the help text at `cmd/marvel/attach.go:295-302` says a remote desk's ssh
line "may need a login name or an ssh config entry of your own", which assumes
the host's ssh works. A later code ticket can reword it. The user guide's section
on opening a session and leaving it also needs an ssh line; that edit waits on
the open detach-instructions change to the same section.

## Not now

Capture only. No gate, no scope, no daemon change, no verb change, no code. The
operator asked for the notes and the gate to be written down and said not to
build it yet.
