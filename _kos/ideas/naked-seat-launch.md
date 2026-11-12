# Naked seats: marvel launches a one-shot seat with almost nothing loaded

- **Status:** idea (pre-hypothesis, no commitment). Operator, 2026-11-12.
- **Subject:** marvel (how a seat is launched, and what the manifest can say
  about what the seat loads). The role text is an OBJECT from the role library; the
  dispatch is an OBJECT from director. Filed in the marvel graph per the subject
  test.
- **Related:** role-library idea `naked-role-shape`; director idea
  `one-shot-dispatch-to-naked-seats`; aae-orc SOUL.md section 9 (same work,
  fewer tokens).

## The observation

The operator, verbatim: "we could also save tokens if we created "naked
agents" that do not include the vast majority of instructions, context or
tools, and reduce the context use for one shot processes for specialized
actions, in addition to leveraging deterministic scripts and tools to reduce
token useage/output".

Every seat today starts with the full harness load: the orc CLAUDE.md and its
imported rules, the user CLAUDE.md, the memory index, every installed skill's
listing, and every MCP server's tool schemas. A seat that only has to publish
one page or check one PR pays for all of that on every model call. On kinu on
2026-11-12, the director session averaged about 270k cached input tokens per
call (367 calls, 99.4M cache-read tokens over 24 h, read from its own
transcript). A one-shot task needs a small fraction of that.

## What marvel would provide

A manifest field that says what a seat loads, so "naked" is a launch setting
and not a habit. Claude Code already has the levers (from `claude --help`,
version on kinu 2026-11-12):

- `--bare`: skips hooks, plugins, auto-memory and CLAUDE.md auto-discovery;
- `--system-prompt` or `--append-system-prompt`: the role text, and only it;
- `--tools` / `--allowedTools`: a short tool list;
- `--strict-mcp-config` with `--mcp-config`: only the MCP servers named;
- `--setting-sources`: which settings files load;
- `--disable-slash-commands`: no skill listing.

Other harnesses (codex, opencode, crush) have their own equivalents; marvel
would map one manifest field to each, the way it maps the console binary
today.

## Constraints already visible

- `--bare` reads auth only from `ANTHROPIC_API_KEY` or an `apiKeyHelper`;
  "OAuth and keychain are never read" (`claude --help`). So a naked seat on a
  claude.ai subscription cannot use `--bare` as is. It would use the narrower
  flags above instead, or run on Bedrock or an API key, which `--bare` allows.
  That changes which hosts and accounts can carry it.
- A naked seat cannot reach the director bus unless the director MCP server is
  in its MCP config. A one-shot seat may not need the bus at all; its result can
  be a file or an exit code.
- Pairs with ADR-010 completion semantics: a one-shot seat runs, exits, and
  holds its slot as finished.

## Open questions

1. Measure first: the per-call input tokens of a naked seat against a full seat
   on the same one-shot task, and whether quality holds (SOUL section 9: "a
   cheaper run that loses quality is not an improvement").
2. One field with levels (full, lean, naked), or the raw flags passed through?
3. Which tasks are one-shot today: page publish, PR state checks, card
   parsing, CI reruns, merge-guard runs, bus relays?
