# First run

Four stages, each optional. Each one works without the stages after it and ends with one proof command, so you can stop at the first stage that does what you need.

Every stage needs marvel installed (see Install in the [README](../README.md)) and tmux:

```sh
brew install tmux
```

`marvel doctor` reads this host and prints one line per check, with a fix line under each failure. It writes nothing. Run it before a stage when you want to know what is missing.

## Stage 0: marvel alone, no model

You need marvel and tmux. Nothing else, and no login.

```sh
marvel daemon &
marvel example demo > demo.toml
marvel work demo.toml
```

The demo is two shell loops in two tmux panes.

Proof: `marvel get sessions` lists both seats as `running`.

Clean up when you are done:

```sh
marvel stop --teardown
```

Next, the [tour](tour.md) runs the simulator that ships beside marvel: kill a session, scale a role, shift a team. It needs no model either.

## Stage 1: one real harness

This stage has not been run end to end: the `marvel doctor` step and the `marvel inject` reply below were not run against a real harness.

You need a harness on your PATH and logged in, for example Claude Code. Save this as `first-seat.toml`:

```toml
[workspace]
name = "first-seat"

[[team]]
name = "solo"

  [[team.role]]
  name = "helper"
  replicas = 1
  permissions = "plan"

    [team.role.runtime]
    image = "claude"
    command = "claude"
```

Check that the harness command resolves and that its login answers, then apply it:

```sh
marvel doctor first-seat.toml
marvel work first-seat.toml
```

> **First seat.** Claude Code may stop at a menu the first time it starts, and marvel never answers it for you.
>
> - **Auto mode.** A menu titled "Teach auto mode about your environment?" has "1. Yes" pre-selected, so Enter picks Yes; do not press Enter on it. Answer "2. Not now". Escape only dismisses it. A third option, "Don't show again", has a number that was not observed. The wording of this menu is from notes, not a capture.
> - **Development channels.** A seat started with a development channel flag stops at this prompt:
>
>   ```text
>   WARNING: Loading development channels
>
>   --dangerously-load-development-channels is for local channel development only. Do not use this option to run channels you have downloaded off the internet.
>
>   Please use --channels to run a list of approved channels.
>
>   1. I am using this for local development
>   2. Exit
>   ```
>
>   Answer "1" only if you started that flag yourself.

Proof: `marvel inject first-seat/solo-helper-g1-0 "reply with the word ready" --key Enter` gets a reply. Read it with `marvel capture first-seat/solo-helper-g1-0`.

## Stage 2: the managed bus

You need nats-server 2.14.0 or newer:

```sh
brew install nats-server
```

The bus is a cluster setting, not a manifest field. Put this in `config.yaml` in your marvel home (`~/.marvel` unless `MARVEL_HOME` names another root). A managed bus needs an explicit listen address, so pick any free port:

```yaml
clusters:
  - name: local
    services:
      - name: bus
        class: message-bus
        provider: nats-server
        mode: managed
        listen: 127.0.0.1:4222
current_cluster: local
```

Marvel wants that file private (mode 600). If `marvel doctor` then reports a keys failure, `marvel keys doctor --fix` sets the mode.

Restart the daemon so it starts the broker:

```sh
marvel stop
marvel daemon &
```

`marvel doctor` checks one thing about the bus: that nats-server is on your PATH at 2.14.0 or newer. It does not check that the broker is running.

Proof: `marvel bus status` prints the line `ready:    yes`.

## Stage 3: seats that reach each other

This stage arrives with the director-mcp release.
