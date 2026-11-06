# Probe brief: which marvel settings belong in a declared file, and how a change reaches a running daemon

**Status:** brief, not run. Written 2026-11-06. Static half to be read at marvel `main`; this brief's own reads were at `ba0ea659`.

**Question asked:** the operator, relayed by director, 2026-11-06: "we also had work to investigate and plan how we could make broader use of yaml or toml for marvel settings". The idea file this brief answers is `_kos/ideas/declared-cluster-config.md` (2026-09-26, #373), whose seed was "More configs in toml/yaml for the cluster."

**Why now.** On 2026-11-06 the host that runs the hub was given a new LAN address. One cluster's leaf named the hub by its old IPv4 address, so it stayed down for about 21 minutes, re-dialling an address the hub no longer held. Another cluster, whose leaf named the hub by host name, stayed up. No marvel verb sets `bus.hub.url`, and the daemon reads it once, at start, or again only through `marvel daemon reexec`. The recovery was outside marvel:
- a hand edit of `bus.hub.url` in `~/.marvel/config.yaml`;
- a hand edit of the leaf remote in the rendered broker configuration;
- a SIGHUP to the broker, after which the leaf was up within 20 ms.

The daemon itself still labels the link with the old address, because it has not re-read the file. A declared setting that only a hand edit can change, and that the daemon reads only at start, is the gap this probe sizes.

**Medium:** reading this repo; a scratch daemon under a scratch home with the simulator runtime, under the fleet's run conditions (scratch `HOME` and `TMPDIR`, no `MARVEL_SOCKET`, network denied except localhost, writes denied outside scratch). No model calls, no real `~/.marvel`, no change to any live cluster.

**Answers are not decided here.** Each question below says what the probe measures and what result would settle it. The choice of answer is the operator's, after the finding.

## 1. The premise, refreshed

The idea's inventory was taken at `c01452c` on 2026-09-26. At `ba0ea659`:

| surface | what it holds now | read when |
|---|---|---|
| `~/.marvel/config.yaml` (`internal/config/config.go:244-253`) | `clusters`, `current_cluster`, and two keys added since the idea: `watchdog.window` (`:265-271`) and `display.session_columns` (`:256-262`). Per cluster, the `bus` block (`:492`), with `hub.url`, `hub.urls` and `hub.ca_file` (`:565-573`), and the `services` list (`:315`) | by the client on each command (`config.Load`, `:959`); by the daemon at start, when `attachServices` (`internal/daemon/daemon.go:649`, `:3233`) renders the broker configuration once in `attachBus` (`:3294-3299`) |
| config verbs (`cmd/marvel/main.go:2430` onward) | `config list`, `add-cluster`, `remove-cluster`, `use-cluster`, `current`, `tmux-server`, and `config repoint` (`cmd/marvel/config_repoint.go:31`), which moves a cluster's address and carries its host key over. `keys trust` (`main.go:2853`) | |
| what no verb sets | the `bus` block, including the hub address, the `services` list, `watchdog` and `display` | |
| config re-read | only `marvel daemon reexec` (`cmd/marvel/main.go:540`; `internal/daemon/daemon.go:1338`) re-reads `config.yaml` in a running daemon. #339, which reported that a re-exec dropped the leaf seed, is closed. `leafSeedNeedsRestart` (`daemon.go:3567`) chooses a broker restart over a SIGHUP when the seed path needs it | |
| how the file is written | `Save` (`config.go:1029-1045`) marshals the parsed struct and writes it with `os.WriteFile` | |
| broker reload | `bus.Manager.Regenerate` re-renders and sends SIGHUP for authorization changes and the leaf toggle (`internal/bus/manager.go:338-360`). `Render` without a reload is used where only a fresh process can pick the change up, because the leaf seed rides the broker's environment, read once at start (`:362-367`) | |
| manifests (`internal/api/manifest.go:52-56`) | YAML and TOML, both tags on every field | at apply |
| daemon flags and env | as in the idea's table; not re-inventoried here | at start |

Two readings from the code that the probe should measure, not assume:

- `Save` writes the struct, so a verb that saves (four call sites in `cmd/marvel`) may drop comments and any key the struct does not know, and a crash mid-write may leave a partial file. That bears directly on question 2 and on any verb that would set the hub address.
- The broker configuration is rendered from `config.yaml` once at daemon start, so a hand edit of `config.yaml` alone changes nothing until a restart, and a hand edit of the rendered file is overwritten at the next render. The 2026-11-06 recovery edited both. Its SIGHUP was enough for the broker, which already held its leaf seed. The daemon's own view stayed stale. Both fit that reading.

## 2. The six questions, and what the probe does for each

### Q1. One file or two?

`config.yaml` is the client's view of clusters, but it also carries the `bus` and `services` blocks the daemon reads, and the client keys (`display`) the daemon ignores.

- **Measure:** classify every key in `config.yaml`, every daemon flag and every env variable by who reads it (client, daemon, both) and on which host it must be true (the client's host, the daemon's host, either). A static table from the code.
- **Measure:** whether a remote client's `config.yaml` can hold a `bus` block that a daemon on another host would act on. The multicluster view (`_kos/ideas/multicluster-view.md`, design #816) puts several clusters in one client file. A `bus` block in such a file describes a daemon the client does not run.
- **Settles it:** if the classification shows keys that only the daemon's host can make true sitting in a file every client holds, that is the case for a second, daemon-side file. If none do, one file stands.

### Q2. TOML, YAML, or both?

- **Measure:** round trip. Write a `config.yaml` with comments and an unknown key, run each saving verb under the scratch home, and diff the result. Do the same for a TOML manifest through apply and `describe`.
- **Measure:** which consumers read the file outside marvel. The Atmos idea (`_kos/ideas/atmos-support.md`, reading 1) asks whether marvel config could be rendered from stacks, with marvel consuming the rendered output only. A rendered file is never hand-edited, which changes what the format must do well.
- **Settles it:** the round-trip result decides whether a verb may write a file a person also edits. The format question is then about readers and writers, not taste.

### Q3. Precedence

- **Measure:** for each setting that has more than one source, the order the code resolves them in today. The socket already has a written ladder (`--socket`, then `--cluster`, then `MARVEL_SOCKET` when no cluster is named, then the current cluster, then the layout default; #586 and the multicluster idea). Record whether every other multi-source setting follows the same flag, env, file order, and list each that does not.
- **Settles it:** a table of exceptions. An empty table means "flag over env over file" is already the rule and needs writing down; a non-empty one names what a declared file would change.

### Q4. What may a remote client change?

- **Measure:** every verb that writes daemon-side state through the control socket, and every one that writes only the client's file. `config repoint` and `keys trust` change client trust; nothing today changes a daemon's `bus` block remotely.
- **Measure:** what a hub address change touches on the daemon's host: `config.yaml`, the rendered broker configuration, the broker's environment (the leaf seed), and the running broker. This is the 2026-11-06 recovery, replayed under the scratch home, with the order and each step's effect recorded.
- **Settles it:** a list of daemon-side settings with the authority each change needs, so a later design can say which a remote verb may set, which need the daemon host's operator, and which need a restart.

### Q5. Live reload or start only?

- **Measure:** for each daemon-side setting, what happens when the file changes under a running daemon: nothing, a re-read on the next tick, a re-render with SIGHUP, or only a restart. Run under the scratch daemon for the hub address, the services list, `watchdog.window` and one log setting.
- **Measure:** whether a SIGHUP after a re-render is enough for the hub address, or whether the leaf seed's start-time read makes a broker restart necessary (`manager.go:362-367`). On 2026-11-06 a SIGHUP was enough for a broker that already held its seed, while the daemon kept the old address in memory. The probe measures both halves: the broker's view and the daemon's.
- **Settles it:** a per-setting table of reload class. A setting in the restart class needs a verb that says so and offers the restart, not a silent file write.

### Q6, added from the 2026-11-06 recovery: how does a declared setting get changed at all?

- **Start from** `docs/design/bus-hub-hot-reload.md`, which sizes a hub-only reload at 150 to 300 lines. The probe checks whether its steps still hold at current main, and whether `daemon reexec` (now that #339 is closed) already gives a working, if heavier, path for the hub address.
- **Measure:** the smallest verb shape that could set the hub address and leave the daemon consistent: which files it writes, in what order, whether each write is atomic, and which reload class it then triggers (from Q5). This is a measurement of the steps, not a design of the verb.
- **Settles it:** whether "set a declared value" is one generic verb over keys with a declared reload class, or a few specific verbs like `repoint`.

## 3. Prior work this probe reads, and what each adds

- `docs/design/bus-hub-hot-reload.md` (#758, merged; reminder ticket aae-orc-y3rfv; origin aae-orc-rim1l). A writeup, not a design for build, of a hot-reload for the hub fields only: re-read, refuse non-hub changes, swap under the manager's lock, re-render, SIGHUP or restart by `leafSeedNeedsRestart`. Its code reads were at `833f692`; the probe re-reads them at current main. It is the worked case for Q5 and Q6: the probe generalizes its per-field reload question to every daemon-side key, and does not redo the hub writeup.

- `_kos/ideas/atmos-support.md`: a rendered config from stacks, consumed as output. Adds the "never hand-edited" case to Q2.
- `_kos/ideas/multicluster-view.md` and its design #816: one client file holding several clusters. Adds the remote-file case to Q1 and Q4.
- `_kos/ideas/token-rate-column-and-configurable-columns.md`: columns as a user option. Already shipped as `display.session_columns` with a `--columns` override, which is a worked example of a client-only key and of flag-over-file precedence for Q1 and Q3.

## 4. What the finding should contain

1. The key classification table (Q1).
2. The round-trip diffs (Q2).
3. The precedence exceptions table (Q3).
4. The daemon-side change list with authority (Q4) and the reload-class table (Q5).
5. The replayed hub-address change, step by step (Q6).
6. The operator's decisions the results point to, each with options and a recommendation, valid until a date.

## 5. Out of scope

- Choosing a format, a file split or a verb. The finding recommends; the operator rules.
- Any change to a live cluster's configuration.
- Atmos integration itself, beyond what Q2 needs.

**bd:** aae-orc-g6fky (this probe). Related:
- aae-orc-y3rfv: explore hot-reload of `bus.hub.url`;
- aae-orc-rim1l;
- aae-orc-t0ie4: a sanctioned `marvel bus leaf repoint`, ranked second by the 2026-10-10 network-move party;
- aae-orc-f71og: a name first, then the last address that answered, at each cross-host dial site, ranked first;
- #514: no working broker reload.

This probe does not redo those. It places the hub address among every other declared setting.
