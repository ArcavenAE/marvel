#!/usr/bin/env python3
"""SP5 of _kos/probes/probe-compaction-ground-truth-mining.md: does a
labelled compaction corpus exist locally for gemini or opencode?

Separate from mine_claude_compactions.py because the corpora have nothing
in common but the question. codex is covered by finding-017 and Crush by
the k2mi arm; neither is touched here.

READ-ONLY, and it starts no harness. The opencode store is copied to a
temp file before it is opened, so a live opencode process is not sharing
a connection with this script.

WHAT IT EXCLUDES: nothing. Both corpora are small enough to read whole.
Absence of a marker is the result, so filtering would beg the question.
"""

import argparse
import glob
import json
import os
import shutil
import sqlite3
import statistics as st
import tempfile
from collections import Counter

HYST_TOKENS = 2048
HYST_FRACTION = 0.10


def hysteresis(tokens):
    frac = int(tokens * HYST_FRACTION)
    return frac if frac > HYST_TOKENS else HYST_TOKENS


def gemini(root):
    files = sorted(glob.glob(os.path.join(root, "**", "chats", "*.json"),
                             recursive=True))
    keys = Counter()
    token_keys = Counter()
    models = Counter()
    identities = Counter()
    rows = 0
    cached_over_input = 0
    max_input = 0
    steps = []
    # Cumulation check, per finding-024: a SESSION accumulator can never
    # decrease, so any session whose input series falls excludes that
    # reading. A per-TURN accumulator is not excluded by this and is
    # indistinguishable from a level on a single-request turn.
    sessions_scored = 0
    sessions_with_decrease = 0
    # Within-turn accumulation needs a MULTI-REQUEST turn, which is the
    # test the Crush arm proposed (finding-020). A pair of consecutive
    # model messages with no user message between them and a toolCalls
    # array on the first is a second request inside one turn. An
    # accumulator predicts the second value at roughly 2x the first.
    turn_pairs = []
    for f in files:
        try:
            with open(f, errors="replace") as fh:
                doc = json.load(fh)
        except (OSError, ValueError):
            continue
        prev = None
        series = []
        for m in doc.get("messages") or []:
            if not isinstance(m, dict):
                continue
            keys.update(m.keys())
            t = m.get("tokens")
            if not isinstance(t, dict):
                continue
            token_keys.update(t.keys())
            models[m.get("model")] += 1
            rows += 1
            i = t.get("input") or 0
            o = t.get("output") or 0
            c = t.get("cached") or 0
            th = t.get("thoughts") or 0
            tl = t.get("tool") or 0
            tot = t.get("total") or 0
            max_input = max(max_input, i)
            if c > i:
                cached_over_input += 1
            if c > 0:
                identities["cached_nonzero"] += 1
                identities["total==in+out+thoughts+tool (cached>0)"] += tot == i + o + th + tl
                identities["total==in+out+thoughts+tool+cached (cached>0)"] += tot == i + o + th + tl + c
            series.append(i)
            if prev is not None and prev - i > hysteresis(prev):
                steps.append((prev, i))
            prev = i
        if len(series) >= 3:
            sessions_scored += 1
            if any(b < a for a, b in zip(series, series[1:])):
                sessions_with_decrease += 1

        msgs = doc.get("messages") or []
        toks = [(i, m) for i, m in enumerate(msgs)
                if isinstance(m, dict) and isinstance(m.get("tokens"), dict)]
        for (i1, m1), (i2, m2) in zip(toks, toks[1:]):
            if not (m1.get("toolCalls") or []):
                continue
            if any((msgs[k] or {}).get("type") == "user" for k in range(i1 + 1, i2)):
                continue
            a = m1["tokens"].get("input") or 0
            b = m2["tokens"].get("input") or 0
            if a:
                turn_pairs.append(b / a)
    return {
        "files": len(files),
        "messages_with_tokens": rows,
        "message_keys": keys.most_common(),
        "token_keys": token_keys.most_common(),
        "models": models.most_common(),
        "compaction_marker_fields": [k for k in keys
                                     if "compact" in k.lower() or "summar" in k.lower()],
        "identities_on_cached_rows": identities.most_common(),
        "rows_with_cached_over_input": cached_over_input,
        "max_input_tokens": max_input,
        "cumulation_check": {
            "sessions_with_3plus_rows": sessions_scored,
            "sessions_whose_input_decreases": sessions_with_decrease,
            "session_cumulative_excluded": sessions_with_decrease > 0,
            "same_turn_pairs": len(turn_pairs),
            "same_turn_ratio_p50": round(st.median(turn_pairs), 3) if turn_pairs else None,
            "same_turn_ratio_max": round(max(turn_pairs), 3) if turn_pairs else None,
            "same_turn_pairs_at_or_above_1_8": sum(1 for r in turn_pairs if r >= 1.8),
            "per_turn_accumulation_excluded": bool(turn_pairs)
            and not any(r >= 1.8 for r in turn_pairs),
        },
        "downward_steps_past_marvel_hysteresis": steps,
    }


def opencode(db_path):
    if not os.path.exists(db_path):
        return {"present": False}
    tmp = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
    tmp.close()
    shutil.copy2(db_path, tmp.name)
    try:
        con = sqlite3.connect(tmp.name)
        cur = con.cursor()
        tables = [r[0] for r in cur.execute(
            "select name from sqlite_master where type='table' order by name")]
        out = {"present": True, "tables": tables}
        for t in ("session", "session_context_epoch", "message", "part"):
            if t in tables:
                out[f"rows_{t}"] = cur.execute(f"select count(*) from {t}").fetchone()[0]
        if "session_context_epoch" in tables:
            out["session_context_epoch_schema"] = cur.execute(
                "select sql from sqlite_master where name='session_context_epoch'"
            ).fetchone()[0]
        con.close()
        return out
    finally:
        os.unlink(tmp.name)


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--gemini-root", default=os.path.expanduser("~/.gemini/tmp"))
    ap.add_argument("--opencode-db",
                    default=os.path.expanduser("~/.local/share/opencode/opencode.db"))
    args = ap.parse_args()
    print(json.dumps({
        "gemini": gemini(args.gemini_root),
        "opencode": opencode(args.opencode_db),
    }, indent=2, default=str))


if __name__ == "__main__":
    main()
