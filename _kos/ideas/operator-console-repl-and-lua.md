# An operator console: a marvel shell, and Lua that reaches the daemon

Raised by the operator 2026-08-07. Pre-hypothesis: generative, not
committed, and deliberately holding several readings at once.

## The seed

> How could we offer a Lua console or editor or some way to kick off Lua
> (it is embedded in marvel, isn't it?) and should we offer some runtimes
> ... think like a Cisco switch with supervisor cards, and include those
> in the display also, or do those belong as other tmux panes perhaps,
> with some kind of shell like IOS, but also supporting scripting and all
> the marvel commands natively (as in, if you enter `marvel cmd arg` you
> don't need to specify marvel, you can just say `get sessions` or
> `work < manifest.yaml`) and some settings like set/get/show?

## What exists today, measured

Lua is embedded, and almost entirely unreachable.

- `github.com/yuin/gopher-lua v1.1.2` in go.mod.
- One implementation: `internal/simulator/lua.go`.
- One caller: `cmd/simulator/main.go`. **Not the daemon, not the CLI.**
- Five functions on a `marvel` module: `create_agent`, `kill_agent`,
  `list_agents`, `scale_team`, `log`.
- One entry point: an `on_tick(pct, tick)` callback.
- Two scripts: `scripts/scaler.lua`, `scripts/chaos.lua`.

So the engine, the module pattern, and two worked examples exist. What
does not exist is any path from an operator to a Lua VM that can see a
real daemon.

## Four things tangled in the seed, worth separating

**A. A marvel shell.** A REPL where `get sessions` means
`marvel get sessions`. Cheap in the mechanism: cobra can execute a parsed
line against the existing root command. The value is not saved keystrokes,
it is a *session*: one connection, one resolved cluster, one context to
carry between commands.

**B. Lua that reaches the daemon.** Promote the simulator's VM from
driving a fake fleet to driving a real one, over the daemon RPC rather
than over the simulator's in-process objects.

**C. A supervisor/card display.** Cisco `show module`: the control plane
and its subordinate modules, each with state, all visible in one table.

**D. Where it lives.** A marvel subcommand, a tmux pane in the operator
console, or both.

## The mapping that earns its keep, and the one that does not

**Earns it: running-config vs startup-config.** IOS distinguishes what
the box is doing now from what it will do on reboot. Marvel has exactly
this distinction and has never named it: DESIRED state (manifests plus
the bolt) versus ACTUAL state (tmux panes and processes). Half of today's
daemon-isolation work was about that gap. `marvel work manifest.toml` is
`configure replace`. A console that made desired-vs-actual a first-class
view would be showing something marvel genuinely has and currently makes
an operator assemble by hand from `get sessions` plus `reap` plus the
event ring.

**Does not earn it yet: supervisor cards.** A chassis view needs
subordinate modules to display. Today there is one daemon on one host, so
`show module` would render one row. It becomes real at M5 (multi-host),
where each host IS a card and active/standby means something. Filing the
display ahead of the thing it displays is building the dial before the
engine.

Per vision.md value 11 (function first, analogy as annotation): the IOS
register is fine as naming, but the console has to be justified by the
desired-vs-actual view, not by resembling a switch.

## The `set` problem, which is the real design question

IOS `set` mutates running-config imperatively. Marvel's model is
declarative: you edit a manifest and re-apply. A `set` verb in a marvel
shell has three possible meanings and they are not compatible:

1. **Session-local preference.** `set cluster kinu`, `set workspace mixed`
   — narrows what subsequent commands address. Harmless, genuinely
   useful, and not really "set" in the IOS sense at all.
2. **Mutate desired state.** `set team/role replicas 5` — this is
   `marvel scale` with a different spelling, and it silently diverges the
   live desired state from the manifest on disk that produced it. That
   drift is the thing the declarative model exists to prevent.
3. **Mutate actual state.** Meaningless: actual state is an observation.

Reading 1 is safe and small. Reading 2 needs an answer to "where does
desired state live now, the file or the store?" before it can be built
without creating a config-drift problem marvel does not currently have.
`show running-config` (render current desired state as a manifest) plus
`diff` against a file is arguably the honest version of reading 2: make
divergence visible instead of easy.

## The gate: a Lua console is an authority surface

The simulator's five functions include `kill_agent` and `scale_team`.
Pointed at a live daemon that is destructive power exercised by a script,
with no principal attached to it. Marvel's authority model is the M1 gap,
and the roadmap ruling (2026-08-01) is study first.

This does not block A, C, or D. It blocks the interesting half of B. A
read-only Lua surface (`list_agents`, `describe`, event queries) has none
of this problem and is most of the exploratory value.

Sharper framing: **a Lua script that can kill agents is an agent**, in
the resource-matrix sense. It consumes authority. Giving it a channel
before there is a principal model means inventing one implicitly, which
is exactly the failure mode `docs/marvel-remap-2026-08.md` warns about
for the bus.

## The drift risk to design against

A shell that reimplements command dispatch becomes a second surface that
ages differently from the first. Every new subcommand would need adding
twice, and the two would diverge quietly, which is the same shape as the
socket default that survived a fix by being declared twice (#122).

Constraint, if this is built: the REPL dispatches into the SAME cobra
tree. A new `marvel` subcommand appears in the shell for free, or the
design is wrong.

## Where it lives (D), unresolved

- **`marvel console`** as a subcommand: works over `mrvl://` to a remote
  daemon, needs no tmux, keeps the independence rule (SOUL §2).
- **A pane in `just demo-watch`**: zero new surface, and the operator
  console already exists as four panes. But it is a demo recipe, not a
  product.
- Both, if the console is a subcommand that the recipe happens to launch.
  This looks right and costs nothing to keep open.

## Ambiguity to resolve with the operator

"Should we offer some runtimes" has two readings and they lead to
different work:

1. **More runtime adapters** (more harnesses in the `runtime` field).
   Independent of the console entirely.
2. **Marvel's own components rendered as modules** in a card-style
   display: the daemon, the adapters it has registered, the hosts.
   This is C.

Reading 2 is what the "include those in the display" clause suggests, but
1 is what "offer some runtimes" says. Ask before building either.

## Cheapest first probe, if this goes anywhere

`marvel console` with NO Lua and NO `set`: a REPL over the existing cobra
tree, plus `show desired` and `show actual` as the two views nothing
currently renders side by side. That tests the one claim worth testing
first, which is whether a session-scoped console makes operating a fleet
meaningfully better than the flat CLI. Lua and the card display both
become easier to judge afterward, and neither is wasted if the answer is
no.

## Related

- `question-substrate` and finding-004 (shim-in-pane): the console is
  another consumer of whatever the supervision layer becomes.
- `aae-orc-412j`: a console is where "which tmux server does this cluster
  use" would naturally be answered.
- M1 (principal model) gates the write half of B.
- M5 (multi-host) is the precondition for C meaning anything.
- vision.md value 11: function first, analogy as annotation.
