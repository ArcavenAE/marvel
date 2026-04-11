# Health Signal Taxonomy — What We Know and Don't

## The BYOA spectrum

Not all agents are equal. The health system must degrade gracefully:

```
Bare sleep/script  → is the process alive?
Bare claude CLI    → alive + exit code + maybe capture output
forestage          → alive + exit code + hooks + structured health
Custom agent       → whatever they implement
```

The healthcheck system is layered. Basic checks work for anything.
Richer checks reward agents that support them.

## What we know: signals available today

### OS / parent process (any agent)
- PID alive (`kill -0`, `ps`)
- Process memory RSS, CPU%
- Process state (sleeping, running, zombie)
- Exit code (when process terminates)

### tmux substrate (any agent in a pane)
- Pane exists (`tmux display-message`)
- Pane output (`tmux capture-pane` — last N lines)
- Pane current command, PID, title (`tmux list-panes`)
- Pane dead status + exit code (`pane_dead`, `pane_dead_status`)

### Marvel daemon socket (agents that send heartbeats)
- Context window % (simulated today, real from SDK in future)
- Heartbeat timestamp (staleness = liveness proxy)

### File system (any agent)
- Working directory state (git status, file changes)
- `.claude/` directory (conversation state, settings)
- Sidecar health file (agent writes, marvel reads)

## What we know forestage *could* expose

### From Agent SDK / hooks.ts
- Session state: idle, working, waiting for tool approval
- Token usage per turn (SDKAssistantMessage.message.usage)
- Context window % (real, not simulated)
- Tool execution events: type, duration, success/failure
- Conversation turn count
- Error rate (API errors, tool failures, permission denials)
- Model info (which model, which provider)

### From Claude Code runtime
- Slash command output (e.g., /status if it existed)
- MCP server health
- Hook execution results
- Memory usage reporting (Claude Code already reports balloons)

## What we don't know

- What does "healthy" mean for an LLM agent? Process alive ≠ productive.
  An agent could be running, sending heartbeats, consuming context, and
  accomplishing nothing. Health and productivity are different axes.

- What does "stuck" look like from the outside? A long tool execution?
  A long thinking pause? Waiting for user input that will never come
  (in an autonomous session)?

- How do agents degrade? Do they get worse gradually (context drift) or
  fail suddenly (API error, crash)? The health system needs different
  response curves for gradual vs sudden degradation.

- What's the right granularity? Per-session? Per-role? Per-team? An
  individual session might be healthy but the team unproductive.

## What we don't know we don't know

By definition, unlisted. But categories where surprises are likely:

- Agent-to-agent coordination failures that look healthy from each
  agent's perspective but are broken at the team level
- Rate limiting, quota exhaustion, billing surprises from LLM providers
- Interaction between Claude Code's own health management and marvel's
  (two systems trying to manage the same process)
- Security/auth token expiry mid-session
- Network partitions in multi-host scenarios (switchboard)
- The behavior of `tmux capture-pane` under high output volume
- What happens when marvel's health evaluation causes cascading restarts

## Starting points (not design choices)

### Sidecar health file
`/tmp/marvel-sessions/<session-id>/health.json` — agent writes, marvel
reads. Simplest cross-language, cross-agent mechanism. Any program that
can write a JSON file can participate. This is the starting point, not
the final answer.

### Heartbeat over daemon socket
Already works. The simulator sends heartbeats with context %. Real agents
(forestage) would do the same via the Agent SDK's message stream callbacks.

### tmux capture-pane
Available for any agent. Marvel can capture recent output and pattern-match
for errors, stuck states, prompt patterns. Crude but universal.

## What this means for the probe

Implement the thin layer: heartbeat staleness + process-alive + restart
policy. The sidecar file convention. The evaluation hook in the reconcile
loop. Everything else is frontier — we'll discover the right signals by
running agents under health evaluation and seeing what fails.
