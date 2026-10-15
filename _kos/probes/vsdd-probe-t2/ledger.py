#!/usr/bin/env python3
"""Cache ledger reader for the vsdd-factory probe (brief section 10).

Turns a run's Claude Code transcripts into per-request records, drops
streaming repeats by message.id, and sums them by run, seat, role, phase
and spawn. Probe-local: standard library only, no network, writes nothing.

    python3 -I -B ledger.py RUN_DIR [--ceiling TOKENS]

RUN_DIR holds run.json (see README.md) and the transcripts it names. A
seat's subagent transcripts are read from <transcript minus .jsonl>/subagents/,
with each .meta.json giving the agentType.
"""
import argparse
import json
import os
import sys
from datetime import datetime

CLASSES = ("input", "write_5m", "write_1h", "read", "output")
TTL_5M, TTL_1H = 300, 3600
ONE_HOUR_SHARE_LIMIT = 0.001  # the pin in brief section 6: under 0.1%


def zero():
    return {c: 0 for c in CLASSES}


def add(into, tokens):
    for c in CLASSES:
        into[c] += tokens[c]


def parse_ts(s):
    return datetime.fromisoformat(s.replace("Z", "+00:00"))


def tools_of(message):
    out = []
    content = message.get("content")
    if isinstance(content, list):
        for block in content:
            if isinstance(block, dict) and block.get("type") == "tool_use":
                inp = block.get("input") if isinstance(block.get("input"), dict) else {}
                out.append((block.get("name", ""), inp.get("command", "")))
    return out


def tokens_of(usage):
    """Return (tokens, unsplit). A write with no 5m/1h split is kept as 5m and flagged."""
    total_w = usage.get("cache_creation_input_tokens", 0) or 0
    split = usage.get("cache_creation")
    unsplit = False
    if isinstance(split, dict) and "ephemeral_5m_input_tokens" in split:
        w1 = split.get("ephemeral_1h_input_tokens", 0) or 0
        w5 = split["ephemeral_5m_input_tokens"] or 0
        if w5 + w1 != total_w:
            unsplit = True
            w5 = total_w - w1
    else:
        w5, w1 = total_w, 0
        unsplit = total_w > 0
    return ({"input": usage.get("input_tokens", 0) or 0, "write_5m": w5, "write_1h": w1,
             "read": usage.get("cache_read_input_tokens", 0) or 0,
             "output": usage.get("output_tokens", 0) or 0}, unsplit)


class Spawn:
    def __init__(self, sid, seat, role, path):
        self.sid, self.seat, self.role, self.path = sid, seat, role, path
        self.first_record = None
        self.requests = []


def load_transcript(sp, seen, stats):
    with open(sp.path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                r = json.loads(line)
            except ValueError:
                stats["bad_lines"] += 1
                continue
            ts = r.get("timestamp")
            if ts:
                t = parse_ts(ts)
                if sp.first_record is None or t < sp.first_record:
                    sp.first_record = t
            msg = r.get("message")
            if r.get("type") != "assistant" or not isinstance(msg, dict) or not msg.get("id") or "usage" not in msg:
                continue
            tok, unsplit = tokens_of(msg["usage"])
            mid = msg["id"]
            if mid in seen:
                kept = seen[mid]
                stats["duplicates_dropped"] += 1
                if kept["tokens"] != tok:
                    stats["conflicting_duplicates"] += 1
                for t in tools_of(msg):
                    if t not in kept["tools"]:
                        kept["tools"].append(t)
                continue
            req = {"id": mid, "ts": parse_ts(r["timestamp"]), "model": msg.get("model", ""),
                   "version": r.get("version"), "side": bool(r.get("isSidechain")),
                   "tokens": tok, "unsplit": unsplit, "tools": tools_of(msg),
                   "error": bool(r.get("isApiErrorMessage") or r.get("error")),
                   "spawn": sp.sid, "seat": sp.seat, "role": sp.role}
            req["write"] = tok["write_5m"] + tok["write_1h"]
            seen[mid] = req
            sp.requests.append(req)
    sp.requests.sort(key=lambda q: q["ts"])


def is_await(req, await_tools, await_pattern):
    if not req["tools"]:
        return False
    import re
    for name, cmd in req["tools"]:
        if name in await_tools:
            continue
        if name == "Bash" and await_pattern and re.search(await_pattern, cmd):
            continue
        return False
    return True


def summarize(reqs):
    tok = zero()
    for q in reqs:
        add(tok, q["tokens"])
    return {"tokens": tok, "requests": len(reqs)}


def read_run(run_dir, ceiling=None):
    with open(os.path.join(run_dir, "run.json"), encoding="utf-8") as f:
        run = json.load(f)
    if ceiling is None:
        ceiling = run.get("sharing_ceiling")
    stats = {"bad_lines": 0, "duplicates_dropped": 0, "conflicting_duplicates": 0}
    seen = {}
    spawns = []
    seats = {}
    unmeasured = []
    for seat in run["seats"]:
        name = seat["name"]
        if not seat.get("transcript"):
            seats[name] = {"status": "unmeasured", "kind": seat.get("kind"), "role": seat.get("role"),
                           "tokens": None, "requests": None}
            unmeasured.append(name)
            continue
        path = os.path.join(run_dir, seat["transcript"])
        main = Spawn(name, name, seat.get("role"), path)
        load_transcript(main, seen, stats)
        spawns.append(main)
        sub_dir = path[:-len(".jsonl")] + os.sep + "subagents" if path.endswith(".jsonl") else None
        if sub_dir and os.path.isdir(sub_dir):
            for fn in sorted(os.listdir(sub_dir)):
                if not fn.endswith(".jsonl"):
                    continue
                stem = fn[:-len(".jsonl")]
                role = None
                meta = os.path.join(sub_dir, stem + ".meta.json")
                if os.path.exists(meta):
                    with open(meta, encoding="utf-8") as f:
                        role = json.load(f).get("agentType")
                sp = Spawn(name + "/" + stem, name, role or "unknown-subagent", os.path.join(sub_dir, fn))
                load_transcript(sp, seen, stats)
                spawns.append(sp)
        seats[name] = {"status": "measured", "kind": seat.get("kind"), "role": seat.get("role")}

    allreqs = [q for sp in spawns for q in sp.requests]
    out = {"requests": len(allreqs), "dedupe": stats, "unmeasured_seats": unmeasured}
    out["totals"] = summarize(allreqs)["tokens"]

    by_seat = {}
    for name, info in seats.items():
        if info["status"] == "measured":
            s = summarize([q for q in allreqs if q["seat"] == name])
            info.update(s)
        by_seat[name] = info
    out["by_seat"] = by_seat

    def group(key):
        res = {}
        for k in sorted({q[key] for q in allreqs}):
            res[k] = summarize([q for q in allreqs if q[key] == k])
        return res

    out["by_role"] = group("role")
    out["by_model"] = group("model")

    def split(key):
        res = {}
        for q in allreqs:
            res.setdefault(q[key], {}).setdefault(q["model"], []).append(q)
        return {k: {m: summarize(v) for m, v in sorted(ms.items())} for k, ms in sorted(res.items())}

    out["by_seat_model"] = split("seat")
    out["by_role_model"] = split("role")
    out["by_sidechain"] = {"main": summarize([q for q in allreqs if not q["side"]])["tokens"],
                           "subagent": summarize([q for q in allreqs if q["side"]])["tokens"]}
    out["by_spawn"] = {sp.sid: dict(summarize(sp.requests), role=sp.role, seat=sp.seat) for sp in spawns if sp.requests}

    phases = {}
    for q in allreqs:
        label = "unphased"
        for p in run.get("phases", []):
            if parse_ts(p["from"]) <= q["ts"] < parse_ts(p["to"]):
                label = p["phase"]
                break
        phases.setdefault(label, []).append(q)
    out["by_phase"] = {k: summarize(v) for k, v in sorted(phases.items())}

    w_total = out["totals"]["write_5m"] + out["totals"]["write_1h"]
    share = (out["totals"]["write_1h"] / w_total) if w_total else 0.0
    out["ttl"] = {"write_total": w_total, "write_5m": out["totals"]["write_5m"], "write_1h": out["totals"]["write_1h"],
                  "share_1h": share, "pass": share < ONE_HOUR_SHARE_LIMIT,
                  "one_hour_records": [{"message_id": q["id"], "spawn": q["spawn"], "write_1h": q["tokens"]["write_1h"]}
                                       for q in sorted(allreqs, key=lambda q: q["ts"]) if q["tokens"]["write_1h"] > 0]}
    out["unsplit_writes"] = [q["id"] for q in allreqs if q["unsplit"]]
    out["unsplit_write_tokens"] = sum(q["write"] for q in allreqs if q["unsplit"])

    def undetermined(q):
        return q["write"] == 0 and q["tokens"]["read"] == 0

    def cold(q):
        return q["write"] > 0 and q["tokens"]["read"] == 0 and not q["error"]

    out["cold_starts"] = sorted(q["id"] for q in allreqs if cold(q))
    out["undetermined"] = sorted(q["id"] for q in allreqs if undetermined(q))

    first = {}
    for sp in spawns:
        if sp.requests:
            q = sp.requests[0]
            rd = q["tokens"]["read"]
            first[sp.sid] = {"message_id": q["id"], "read": rd,
                             "fraction_of_ceiling": (rd / ceiling) if ceiling else None}
    out["first_request"] = first

    deaths = []
    for sp in spawns:
        ttl = None
        prev = None
        for q in sp.requests:
            if undetermined(q):
                continue
            if prev is not None and ttl is not None:
                gap = (q["ts"] - prev["ts"]).total_seconds()
                prev_prompt = prev["tokens"]["input"] + prev["tokens"]["read"] + prev["write"]
                if gap > ttl and q["write"] > 0 and q["write"] * 2 >= prev_prompt:
                    deaths.append({"message_id": q["id"], "spawn": sp.sid, "gap_seconds": gap, "ttl_seconds": ttl})
            if q["tokens"]["write_1h"] > 0:
                ttl = TTL_1H
            elif q["tokens"]["write_5m"] > 0:
                ttl = TTL_5M
            prev = q
    out["cache_deaths"] = deaths

    concurrent = []
    live = [sp for sp in spawns if sp.requests]
    for sp in live:
        if not cold(sp.requests[0]):
            continue
        start = sp.first_record or sp.requests[0]["ts"]
        for sib in live:
            if sib is sp or sib.role != sp.role:
                continue
            sib_start = sib.first_record or sib.requests[0]["ts"]
            if sib_start < start < sib.requests[0]["ts"]:
                concurrent.append(sp.sid)
                break
    out["concurrent_cold_writes"] = sorted(concurrent)

    await_tools = set(run.get("await_tools", ["await"]))
    pattern = run.get("await_pattern")
    orch = [q for q in allreqs if q["spawn"] in {s["name"] for s in run["seats"] if s.get("kind") == "orchestrator"}]
    in_window = {}
    win_ids = set()
    for w in run.get("waves", []):
        lo, hi = parse_ts(w["first_dispatch"]), parse_ts(w["last_terminal"])
        rows = [q for q in orch if lo <= q["ts"] <= hi]
        in_window[w["wave"]] = summarize(rows)
        win_ids.update(q["id"] for q in rows)
    awaits = [q for q in orch if is_await(q, await_tools, pattern)]
    union = [q for q in orch if q["id"] in win_ids or q in awaits]
    out["hub_wait"] = {"waves": in_window, "await_only_turns": summarize(awaits), "union": summarize(union)}

    versions = {}
    for q in allreqs:
        versions[q["version"]] = versions.get(q["version"], 0) + 1
    out["versions"] = versions
    pinned = run.get("pinned_version")
    out["version_mismatch"] = sorted({q["seat"] for q in allreqs if pinned and q["version"] != pinned})
    return out


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("run_dir")
    ap.add_argument("--ceiling", type=int, default=None)
    a = ap.parse_args(argv)
    print(json.dumps(read_run(a.run_dir, a.ceiling), indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
