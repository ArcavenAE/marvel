# finding-046: a codex reviewer runs as a standing role inside a claude team

finding-001 established that a marvel team's roles each carry their own runtime,
but it validated that claude-only (1 supervisor, 3 workers). This is the first
live cross-harness demonstration: a claude worker and a codex reviewer in the
SAME team, each under its own adapter. The mixed-harness team works, and it
tells us where a codex reviewer's output goes today.

Probed on 2026-09-17 against the running daemon (the same one from finding-042),
with a throwaway workspace torn down after.

## The setup

A throwaway manifest (workspace `codex-review-test`, team `review`) with two
roles: a claude `worker` (interactive, replicas 1, cwd pinned to a scratch dir)
and a codex `reviewer` (headless, replicas 1, cwd pinned, prompt = review the
scratch dir's `sample.go` adversarially and write REVIEW.md). Applied with
`marvel work`; the two live workspaces already on the daemon stayed present
and untouched, which is finding-043's per-team upsert confirmed again.

## What launched

```
WORKSPACE          TEAM    ROLE      GEN  STATE               DESK  RUNTIME
codex-review-test  review  reviewer  1    succeeded (exit 0)  27    codex
codex-review-test  review  worker    1    running             26    claude
```

Both roles came up, each resolved to its own adapter from `Runtime.Name`
(`elem-runtime-adapter-framework`). Mixed-harness composition in one team is not
just architectural (finding-001); it runs.

## The codex reviewer, end to end

The codex adapter's JSONL stream (`codex exec --json`) was parsed into marvel's
event vocabulary and the accountant metered it:

```
agent.session.started    model unknown
agent.turn.started       tokens in=0 out=0
agent.message.completed  assistant: I'll inspect sample.go with line numbers...
agent.tool.call          command_execution (item_1)
agent.tool.result        error (item_1)
agent.message.completed  assistant: The workspace is mounted read-only, so creation of REVIEW.md was rejected...
agent.message.completed  assistant: # Advisory review - sample.go:9 json.Marshal(raw) converts the input bytes into a base64-encoded JSON string...
agent.turn.completed     tokens in=57855 out=1080 ctx=57855
session.succeeded        pane %27 exited 0; replica satisfied
```

Three things to keep:

- **The review is real.** It caught the primary defect (the encode-then-decode
  path at `sample.go:9`, `json.Marshal` of the raw bytes) and returned an
  advisory review, not a rubber stamp.
- **Completion semantics hold (ADR-010).** The finished one-shot ended
  `session.succeeded ... replica satisfied`, holding its replica slot rather
  than restart-looping. This is the behavior finding-015 was the bug against,
  now correct for a codex headless role.
- **The accountant metered it** (`in=57855 out=1080`) off the same stream, so a
  reviewer's token cost is visible per run.

## Where the review went, and where it did not

The review lives in marvel's **event ring**: the `agent.message.completed`
events carry the review text, readable through `marvel events` and
`marvel describe session`. It did NOT reach a file, and it did NOT reach a team
bus.

- **No file.** codex's own default sandbox mounts the workspace read-only, so
  the reviewer's attempt to write REVIEW.md was rejected by codex, not by
  marvel. marvel injects no sandbox or approval policy for codex (codex.go: the
  adapter injects neither, to avoid widening a harness's authority by default).
  So a review that only reports works out of the box; a reviewer that must WRITE
  its output (a file, a patch) needs the operator to widen codex's sandbox
  through `runtime.Args`.
- **No team bus.** There is no per-team NATS bus yet (vision layer 4 is
  unbuilt). The director roster already showed codex reaching the director bus
  for the codex#46210 review; what was unproven, and is proven here, is the
  standing manifest role inside a marvel team. Its output surfaces in the event
  ring, not as a message delivered to teammates.

## The fresh-cwd gate, codex side

finding-045 recorded that claude's folder-trust gate is NOT pre-cleared by
marvel, so a first cast into a new workspace can freeze on trust. The codex
analogue is the git-repo-check, and marvel ALREADY clears it: the adapter passes
`--skip-git-repo-check` on every headless launch (codex.go). So the codex
reviewer did not freeze on a fresh non-repo cwd. codex's separate constraint is
the read-only sandbox above, which is a write-permission question, not a trust
prompt.

## What this leaves open

The reviewer's output reaching teammates depends on the unbuilt per-team bus.
The reviewer as a merge-gate is deliberately not built: it stays advisory (SOUL
section 8; the wardrobe `reviewer` role draft carries the authority model). A
write-capable reviewer (one that lands its review as a file or a patch) needs
codex's sandbox widened by the operator. None of these block the standing-role
result: a codex adversarial reviewer runs as a role in a mixed-harness marvel
team today, and its review is captured.

Related: finding-001 (heterogeneous team model, claude-only), finding-043
(per-team upsert, additive apply), finding-045 (claude folder-trust gate, the
contrast), finding-015 + ADR-010 (headless completion semantics),
`elem-runtime-adapter-framework` (the adapters composed here), and orc
`question-atelier-team-lineup` (the reviewer-seat gap this unblocks; the
wardrobe `reviewer` role is the seat).
