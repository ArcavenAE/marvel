#!/usr/bin/env python3
"""Arm B orchestrator patch for vsdd-factory rc.25 (brief section 3).

Usage: apply_patch.py PACK_ROOT

Edits two files under PACK_ROOT in place and prints the sha256 of each. Every
edit is anchored on the leading text of one line and must match exactly one
line; a missing or repeated anchor stops the run with a nonzero exit and writes
nothing. The anchors are short prefixes, so no rc.25 text is copied here.
Standard library only; no network.
"""
import hashlib
import sys
from pathlib import Path

ORCH = "agents/orchestrator/orchestrator.md"
PSD = "agents/orchestrator/per-story-delivery.md"

BRANCH_DISPATCH = [
    "",
    "## Branch dispatch",
    "",
    "At a `type: parallel` step, do not start the story groups with the Agent tool. For each story group, write the branch prompt below to `.probe/prompts/<key>.md` and call `bin/shim dispatch <key> <prompt-file>` once. The key is `<run-id>-c<cycle>-w<wave>-g<group>-a<attempt>`: lowercase, no slashes. Then call `bin/shim await <key>...` with every key, and repeat the call while any key reads `pending`. Each result is a claim (see Verification Discipline in `per-story-delivery.md`).",
    "",
    "On `failed`, `crashed` or `unknown`, check each story of the group in git. A merged story is done. Raise `attempt` and dispatch again only for the stories that did not merge. Never raise `attempt` on `pending` or on a failed `await` call.",
    "",
    "Before the wave gate, verify in git that each story's PR merged and its worktree is gone, then dispatch state-manager once with every story's merge status.",
    "",
    "Branch prompt:",
    "",
    "> Branch mode. Deliver only story group `<group>` of wave `<wave>`, key `<key>`. For each story in the group, in order, run `per-story-delivery.md` step 2 items a, b, c, c2, d, e, f and g. Dispatch every item with the Agent tool and `subagent_type`, using the Task Preamble. Write under `.factory/` only within `.factory/cycles/<cycle>/<story-id>/` for your own stories. Do not edit `.factory/STATE.md`, commit in `.factory/`, dispatch state-manager, or run `bin/factory-cas-push.sh` or `bin/factory-lock-write.sh`. End with `BRANCH_RESULT key=<key>`, then, for each story: its PR number, merge sha and adversary pass count.",
    "",
    "Skipped in Branch mode.",
]

# sha256 of each target before the edit (the rc.25 file manifest) and after it.
# The script stops before writing if a target is not the rc.25 file, and stops
# after writing if the result is not the expected bytes.
BEFORE = {
    ORCH: "7999a478dcc86e9c77b885879520700f9289c367f02fa8c9f5bf751cd4604fc8",
    PSD: "991b103d313e5c00b53cb98f46d5ab59846fd77043c27dde3c68145ef4dcb197",
}
AFTER = {
    ORCH: "828e4d8a55be78ac79c91abc8bdee0f104fab7aa69566b4ff5b309ee9d06d022",
    PSD: "211728afb2cc984743cdec7a7ce37fa1d8fa8d14bd37171c393cb79df3ce1eba",
}

# (file, anchor kind, anchor text, action, payload)
#   kind "startswith" or "equals"; action "append" adds payload to the line,
#   "after" inserts payload lines after it.
EDITS = [
    (ORCH, "startswith", "For each step, spawn the declared agent via the Agent tool", "after", BRANCH_DISPATCH),
    (ORCH, "startswith", "- You ALWAYS delegate via the Agent tool", "append",
     " except `type: parallel` branches; see Branch dispatch."),
    (ORCH, "startswith", "- You ALWAYS update STATE.md via state-manager", "append",
     " except in Branch mode, started by the shim."),
    (ORCH, "startswith", "- You MUST NOT spawn with `agentId: \"orchestrator\"`", "append",
     " except in Branch mode, started by the shim."),
    (ORCH, "equals", "Use the Agent tool with `subagent_type` for EVERY delegation.", "append",
     " Except `type: parallel` branches; see Branch dispatch."),
    (ORCH, "equals", "## Pipeline Resume", "after", ["", "Skipped in Branch mode."]),
    (PSD, "equals", "2. For each story (parallelized within wave groups):", "after",
     ["   One branch seat per story group, through the shim."]),
]


def matches(line, kind, text):
    return line.startswith(text) if kind == "startswith" else line == text


def apply(root):
    texts = {}
    for rel in (ORCH, PSD):
        raw = (root / rel).read_bytes()
        got = hashlib.sha256(raw).hexdigest()
        if got != BEFORE[rel]:
            sys.exit(f"apply_patch: {rel}: sha256 {got} is not the rc.25 file {BEFORE[rel]}")
        texts[rel] = raw.decode("utf-8").split("\n")
    for rel, kind, text, action, payload in EDITS:
        lines = texts[rel]
        hits = [i for i, ln in enumerate(lines) if matches(ln, kind, text)]
        if len(hits) != 1:
            sys.exit(f"apply_patch: {rel}: anchor {text!r} matched {len(hits)} lines, want 1")
        i = hits[0]
        if action == "append":
            lines[i] = lines[i] + payload
        else:
            lines[i + 1:i + 1] = payload
    out = {}
    for rel, lines in texts.items():
        data = "\n".join(lines).encode("utf-8")
        out[rel] = data
    for rel, data in out.items():
        got = hashlib.sha256(data).hexdigest()
        if got != AFTER[rel]:
            sys.exit(f"apply_patch: {rel}: patched sha256 {got} is not the expected {AFTER[rel]}")
    for rel, data in out.items():
        (root / rel).write_bytes(data)
    return {rel: hashlib.sha256(d).hexdigest() for rel, d in out.items()}


def main(argv):
    if len(argv) != 2:
        sys.exit("usage: apply_patch.py PACK_ROOT")
    root = Path(argv[1])
    for rel, h in apply(root).items():
        print(f"{h}  {rel}")


if __name__ == "__main__":
    main(sys.argv)
