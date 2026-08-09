#!/usr/bin/env python3
"""Mine the local Claude Code transcript corpus for labelled compaction
events and replay marvel's shipped compaction detector against them.

Answers SP1 and SP4 of _kos/probes/probe-compaction-ground-truth-mining.md.
SP2 and SP3 were answered by finding-016 and are not recomputed here.
See finding-019 for the result.

WHAT IT READS. Every *.jsonl under --root, recursively. Per line it keeps
only: type/subtype, the four token classes of message.usage, message.id,
message.model, isSidechain, agentId, the compactMetadata numbers, the
line timestamp, and the record's position in its file. It never reads,
stores, or prints message content, file paths beyond a salted digest, or
any tool argument. The corpus is the operator's own working history.

WHAT IT EXCLUDES, and why.

  - Lines whose type is not assistant-with-usage or
    system/compact_boundary. Everything else carries no accounting.
  - Sidechain (subagent) usage lines are kept but SEGREGATED: a subagent
    runs its own context window, so folding it into the parent's series
    is the defect internal/usage/accountant.go excludes structurally.
    They live in their own files under <session>/subagents/, so path and
    isSidechain agree; the script asserts that and reports any
    disagreement rather than trusting either.
  - A usage line repeating the immediately preceding message.id, which
    is how the shipped parser dedupes (parser.go emitRequestUsage
    compares against lastRequestID only, not a set). Replaying a
    different dedupe would not be replaying the shipped detector.
  - Samples whose model identity key differs from the file's first
    non-sidechain model. accountant.fold routes these to spend and out
    of the occupancy series. Counted and reported.
  - Files that changed size while the run was in progress. The corpus is
    live and this script's own session appends to it. They are reported,
    not silently dropped.

NOT EXCLUDED, deliberately: nothing is filtered on project directory,
date, or harness version. Version skew is reported instead.

Usage:
    scripts/mine_claude_compactions.py                  # report
    scripts/mine_claude_compactions.py --json out.json  # machine-readable
    scripts/mine_claude_compactions.py --fixture f.json # regression fixture
"""

import argparse
import hashlib
import json
import os
import re
import statistics as st
import sys
import time
from collections import Counter, defaultdict

# marvel's shipped defaults, internal/usage/accountant.go:28-31.
HYST_TOKENS = 2048
HYST_FRACTION = 0.10

DATE_SUFFIX = re.compile(r"-\d{8}$")


def identity_key(model):
    """Port of usage.IdentityKey (limits.go): normalize, then strip [1m]."""
    m = (model or "").strip()
    for p in ("us.", "eu.", "apac."):
        if m.startswith(p):
            m = m[len(p):]
    if m.startswith("anthropic."):
        m = m[len("anthropic."):]
    m = DATE_SUFFIX.sub("", m)
    return m[:-4] if m.endswith("[1m]") else m


def occupancy(rec):
    """Port of usage.Sample.Occupancy for the additive Claude layout."""
    return rec["in"] + rec["cache_read"] + rec["cache_creation"]


def hysteresis(tokens):
    """Port of Accountant.hysteresis."""
    frac = int(tokens * HYST_FRACTION)
    return frac if frac > HYST_TOKENS else HYST_TOKENS


def digest(s, salt):
    return hashlib.sha256((salt + s).encode()).hexdigest()[:12]


def scan(root, salt):
    """Return (files, skipped) where files is a list of per-file records."""
    paths = []
    for dirpath, _dirs, names in os.walk(root):
        for n in names:
            if n.endswith(".jsonl"):
                paths.append(os.path.join(dirpath, n))
    paths.sort()

    before = {}
    for p in paths:
        try:
            before[p] = os.stat(p).st_size
        except OSError:
            pass

    files = []
    skipped = Counter()
    for p in paths:
        if p not in before:
            skipped["stat_failed"] += 1
            continue
        rel = os.path.relpath(p, root)
        project = rel.split(os.sep)[0]
        is_sub_path = f"{os.sep}subagents{os.sep}" in rel
        usage = []
        boundaries = []
        markers = []  # (pos, subtype) for the system markers a boundary may sit on
        versions = Counter()
        bad_json = 0
        try:
            fh = open(p, "r", errors="replace")
        except OSError:
            skipped["open_failed"] += 1
            continue
        with fh:
            for pos, line in enumerate(fh):
                line = line.strip()
                if not line:
                    continue
                try:
                    o = json.loads(line)
                except ValueError:
                    bad_json += 1
                    continue
                t = o.get("type")
                if o.get("version"):
                    versions[o["version"]] += 1
                if t == "assistant":
                    m = o.get("message") or {}
                    u = m.get("usage")
                    if not isinstance(u, dict):
                        continue
                    usage.append({
                        "pos": pos,
                        "msg_id": m.get("id") or "",
                        "model": m.get("model") or "",
                        "in": int(u.get("input_tokens") or 0),
                        "out": int(u.get("output_tokens") or 0),
                        "cache_read": int(u.get("cache_read_input_tokens") or 0),
                        "cache_creation": int(u.get("cache_creation_input_tokens") or 0),
                        "sidechain": bool(o.get("isSidechain")),
                        "agent_id": o.get("agentId") or "",
                        "ts": o.get("timestamp") or "",
                    })
                elif o.get("subtype") in ("scheduled_task_fire", "away_summary"):
                    markers.append((pos, o["subtype"]))
                elif o.get("subtype") == "compact_boundary":
                    cm = o.get("compactMetadata")
                    if not isinstance(cm, dict):
                        boundaries.append({"pos": pos, "meta": None,
                                           "ts": o.get("timestamp") or ""})
                        continue
                    pm = cm.get("preservedMessages") or {}
                    uuids = pm.get("allUuids") or pm.get("uuids") or []
                    boundaries.append({
                        "pos": pos,
                        "ts": o.get("timestamp") or "",
                        "meta": {
                            "trigger": cm.get("trigger"),
                            "pre": cm.get("preTokens"),
                            "post": cm.get("postTokens"),
                            "dropped": cm.get("cumulativeDroppedTokens"),
                            "duration_ms": cm.get("durationMs"),
                            "preserved": len(uuids) if isinstance(uuids, list) else None,
                        },
                    })
        try:
            after = os.stat(p).st_size
        except OSError:
            after = -1
        files.append({
            "id": digest(rel, salt),
            "project": digest(project, salt),
            "is_sub_path": is_sub_path,
            "usage": usage,
            "boundaries": boundaries,
            "markers": markers,
            "versions": versions,
            "bad_json": bad_json,
            "grew": after != before[p],
        })
    return files, skipped


def replay(usage_records, order="file"):
    """Replay the shipped detector over one file's occupancy series.

    order="file" feeds records in the order the harness wrote them, which
    is the closest available proxy for the arrival order a live stream
    reader sees. order="time" sorts by the line's own timestamp first.
    The two differ, and the difference is measured rather than assumed.

    Returns (series, detections, routed, deduped).
    """
    recs = [r for r in usage_records if not r["sidechain"]]
    if order == "time":
        recs = sorted(recs, key=lambda r: (r["ts"], r["pos"]))
    series = []
    detections = []
    routed = 0
    deduped = 0
    last_id = ""
    primary = ""
    tokens = 0
    requests = 0
    for r in recs:
        if not r["msg_id"] or r["msg_id"] == last_id:
            deduped += 1
            continue
        last_id = r["msg_id"]
        key = identity_key(r["model"])
        if primary == "" and key:
            primary = key
        if key and primary and key != primary:
            routed += 1
            continue
        occ = occupancy(r)
        if requests > 0:
            drop = tokens - occ
            if drop > hysteresis(tokens):
                detections.append({"pos": r["pos"], "from": tokens, "to": occ,
                                   "drop": drop, "index": len(series)})
        tokens = occ
        requests += 1
        series.append({"pos": r["pos"], "occ": occ, "model": r["model"],
                       "ts": r["ts"]})
    return series, detections, routed, deduped


def zero_census(usage_records):
    """Zero-occupancy assistant records, split by whether the shipped
    non-primary-model guard would keep them out of the occupancy series.

    A record whose three prompt classes are all zero is not a level. Codex
    writes such a sentinel at every compaction (aae-orc-mob4); this counts
    the Claude equivalent and says which ones marvel already excludes.
    """
    primary = ""
    guarded = 0
    unguarded = 0
    models = Counter()
    for r in usage_records:
        if r["sidechain"]:
            continue
        key = identity_key(r["model"])
        if primary == "" and key:
            primary = key
        if occupancy(r) != 0:
            continue
        models[r["model"] or "(unnamed)"] += 1
        if key and primary and key != primary:
            guarded += 1
        else:
            unguarded += 1
    return guarded, unguarded, models


def latch_census(usage_records):
    """Does this session switch model and never switch back?

    accountant.fold latches the first model it sees as primary and never
    re-latches. Every later sample naming a different model is routed to
    spend, so the occupancy level FREEZES at its pre-switch value and the
    sink is never written again. Returns None when no permanent switch
    happened, else the frozen level and the true peak after it.
    """
    seq = []
    last_id = ""
    for r in usage_records:
        if r["sidechain"] or not r["msg_id"] or r["msg_id"] == last_id:
            continue
        last_id = r["msg_id"]
        seq.append((identity_key(r["model"]), occupancy(r)))
    if not seq:
        return None
    primary = next((k for k, _ in seq if k), "")
    routed = [i for i, (k, _) in enumerate(seq) if k and primary and k != primary]
    if not routed:
        return None
    first = routed[0]
    tail = seq[first:]
    if not all(k and k != primary for k, _ in tail):
        return None  # interleaved side calls, not a permanent switch
    if all(k == "<synthetic>" for k, _ in tail):
        return None  # a trailing synthetic record is not a model switch
    return {
        "series": len(seq),
        "switch_index": first,
        "lost": len(tail),
        "frozen_at": seq[first - 1][1] if first else 0,
        "true_peak_after": max(o for _, o in tail),
        "from": primary,
    }


def out_of_order(usage_records):
    """Count non-sidechain usage lines whose timestamp precedes the line
    before them in file order, and the largest such inversion in seconds."""
    prev_ts = ""
    inversions = 0
    worst = 0.0
    total = 0
    for r in usage_records:
        if r["sidechain"] or not r["ts"]:
            continue
        total += 1
        if prev_ts and r["ts"] < prev_ts:
            inversions += 1
            worst = max(worst, seconds_between(r["ts"], prev_ts))
        else:
            prev_ts = max(prev_ts, r["ts"])
    return inversions, total, worst


def seconds_between(a, b):
    """Seconds from ISO timestamp a to b; 0 when either is unparseable."""
    import datetime
    try:
        fmt = "%Y-%m-%dT%H:%M:%S.%fZ"
        ta = datetime.datetime.strptime(a, fmt)
        tb = datetime.datetime.strptime(b, fmt)
    except ValueError:
        return 0.0
    return (tb - ta).total_seconds()


def pct(part, whole):
    return 0.0 if not whole else 100.0 * part / whole


def summarize(vals):
    if not vals:
        return None
    s = sorted(vals)
    return {
        "n": len(s),
        "min": s[0],
        "p50": st.median(s),
        "max": s[-1],
        "mean": round(st.mean(s), 1),
    }


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--root", default=os.path.expanduser("~/.claude/projects"))
    ap.add_argument("--salt", default=os.environ.get("MINE_SALT", "marvel-sp4"),
                    help="path-digest salt; change it and file ids change")
    ap.add_argument("--json", dest="json_out", help="write the full result")
    ap.add_argument("--fixture", help="write a regression fixture (numbers only)")
    args = ap.parse_args()

    started = time.time()
    files, skipped = scan(args.root, args.salt)

    n_main = sum(1 for f in files if not f["is_sub_path"])
    n_sub = sum(1 for f in files if f["is_sub_path"])
    grew = [f["id"] for f in files if f["grew"]]
    versions = Counter()
    for f in files:
        versions.update(f["versions"])

    # Path and isSidechain must agree about what a subagent line is.
    disagree = Counter()
    for f in files:
        for r in f["usage"]:
            if r["sidechain"] != f["is_sub_path"]:
                disagree[(f["is_sub_path"], r["sidechain"])] += 1

    all_boundaries = []
    for f in files:
        for b in f["boundaries"]:
            all_boundaries.append((f, b))

    sp1 = []      # boundary-level comparisons of marvel occupancy vs preTokens
    sp4_rows = [] # per-boundary detector outcome, file order
    fp_rows = []  # detections not straddling any boundary, file order
    sp4_time = [] # the same replay in timestamp order
    fp_time = []
    routed_total = 0
    deduped_total = 0
    series_total = 0
    inversions_total = 0
    inversion_files = 0
    worst_inversion = 0.0
    ordered_samples = 0
    zero_guarded = 0
    zero_unguarded = 0
    zero_models = Counter()
    latches = []

    for f in files:
        g, ug, zm = zero_census(f["usage"])
        zero_guarded += g
        zero_unguarded += ug
        zero_models.update(zm)
        latch = latch_census(f["usage"])
        if latch:
            latches.append(latch)

        series, detections, routed, deduped = replay(f["usage"], order="file")
        t_series, t_detections, _, _ = replay(f["usage"], order="time")
        routed_total += routed
        deduped_total += deduped
        series_total += len(series)
        f["_series"] = series
        f["_detections"] = detections

        inv, tot, worst = out_of_order(f["usage"])
        inversions_total += inv
        ordered_samples += tot
        if inv:
            inversion_files += 1
        worst_inversion = max(worst_inversion, worst)

        for rows, fps, ser, dets in ((sp4_rows, fp_rows, series, detections),
                                     (sp4_time, fp_time, t_series, t_detections)):
            bpos = [b["pos"] for b in f["boundaries"]]
            matched = set()
            for b in f["boundaries"]:
                before = [s for s in ser if s["pos"] < b["pos"]]
                after = [s for s in ser if s["pos"] > b["pos"]]
                prev = before[-1] if before else None
                nxt = after[0] if after else None
                hit = None
                for d_i, d in enumerate(dets):
                    # a detection straddles this boundary when the sample
                    # it fired on is the first sample after the boundary
                    if nxt is not None and d["pos"] == nxt["pos"] and before:
                        hit = d_i
                        break
                if hit is not None:
                    matched.add(hit)
                meta = b["meta"] or {}
                rows.append({
                    "file": f["id"],
                    "trigger": meta.get("trigger"),
                    "pre": meta.get("pre"),
                    "post": meta.get("post"),
                    "prev_occ": prev["occ"] if prev else None,
                    "next_occ": nxt["occ"] if nxt else None,
                    "detected": hit is not None,
                    "has_prev": prev is not None,
                    "has_next": nxt is not None,
                    "drop": (prev["occ"] - nxt["occ"]) if (prev and nxt) else None,
                    "guard": hysteresis(prev["occ"]) if prev else None,
                    "model_prev": prev["model"] if prev else None,
                    "model_next": nxt["model"] if nxt else None,
                    "next_is_zero": bool(nxt) and nxt["occ"] == 0,
                    "marker_within_30": sorted({s for p, s in f["markers"]
                                                if 0 <= b["pos"] - p <= 30}),
                    "post_exceeds_pre": bool(meta.get("post") and meta.get("pre")
                                             and meta["post"] > meta["pre"]),
                    "raw_usage_after": sum(1 for r in f["usage"]
                                           if not r["sidechain"] and r["pos"] > b["pos"]),
                })
            for d_i, d in enumerate(dets):
                if d_i in matched:
                    continue
                near = min((abs(d["pos"] - p) for p in bpos), default=None)
                fps.append({"file": f["id"], "from": d["from"], "to": d["to"],
                            "drop": d["drop"], "dist_to_boundary": near})

        # SP1 compares the harness's own preTokens against the level marvel
        # would hold, taking the chronologically newest sample that precedes
        # the boundary in time. File order would conflate the formula with
        # the ordering artifact measured above.
        for b in f["boundaries"]:
            meta = b["meta"] or {}
            if not meta.get("pre") or not b["ts"]:
                continue
            cands = [s for s in t_series if s["ts"] and s["ts"] < b["ts"]]
            if not cands:
                continue
            prev = cands[-1]
            nxt = [s for s in t_series if s["ts"] and s["ts"] > b["ts"]]
            row = {
                "file": f["id"],
                "trigger": meta.get("trigger"),
                "occ": prev["occ"],
                "pre": meta["pre"],
                "delta": prev["occ"] - meta["pre"],
                "ratio": prev["occ"] / meta["pre"],
                "gap_s": round(seconds_between(prev["ts"], b["ts"]), 1),
                "model": prev["model"],
            }
            if nxt and meta.get("post"):
                row["post"] = meta["post"]
                row["next_occ"] = nxt[0]["occ"]
                row["post_delta"] = nxt[0]["occ"] - meta["post"]
            sp1.append(row)

    detected = sum(1 for r in sp4_rows if r["detected"])
    detectable = sum(1 for r in sp4_rows if r["has_prev"] and r["has_next"])
    t_detected = sum(1 for r in sp4_time if r["detected"])
    t_detectable = sum(1 for r in sp4_time if r["has_prev"] and r["has_next"])

    result = {
        "root": args.root,
        "snapshot_started": started,
        "elapsed_s": round(time.time() - started, 1),
        "files_total": len(files),
        "files_main": n_main,
        "files_subagent": n_sub,
        "files_grew_during_run": len(grew),
        "files_skipped": dict(skipped),
        "harness_versions": versions.most_common(),
        "path_vs_issidechain_disagreements": {str(k): v for k, v in disagree.items()},
        "usage_samples_in_series": series_total,
        "samples_routed_nonprimary": routed_total,
        "samples_deduped": deduped_total,
        "boundaries_total": len(all_boundaries),
        "boundaries_in_subagent_files": sum(1 for f, _ in all_boundaries if f["is_sub_path"]),
        "boundaries_without_metadata": sum(1 for _, b in all_boundaries if b["meta"] is None),
        "trigger_mix": Counter(r["trigger"] for r in sp4_rows).most_common(),
        "ordering": {
            "samples_considered": ordered_samples,
            "timestamp_inversions": inversions_total,
            "files_with_inversions": inversion_files,
            "worst_inversion_s": round(worst_inversion, 1),
        },
        "sp1": {
            "n": len(sp1),
            "exact_matches": sum(1 for r in sp1 if r["delta"] == 0),
            "delta": summarize([r["delta"] for r in sp1]),
            "ratio": summarize([round(r["ratio"], 4) for r in sp1]),
            "post_delta": summarize([r["post_delta"] for r in sp1 if "post_delta" in r]),
            "within_1pct": sum(1 for r in sp1 if abs(r["ratio"] - 1) <= 0.01),
            "within_5pct": sum(1 for r in sp1 if abs(r["ratio"] - 1) <= 0.05),
            "contiguous_n": sum(1 for r in sp1 if r["gap_s"] <= 600),
            "contiguous_within_1pct": sum(1 for r in sp1
                                          if r["gap_s"] <= 600 and abs(r["ratio"] - 1) <= 0.01),
            "gap_s": summarize([r["gap_s"] for r in sp1]),
        },
        "sp4": {
            "boundaries": len(sp4_rows),
            "with_samples_both_sides": detectable,
            "detected": detected,
            "false_negatives": detectable - detected,
            "no_sample_before": sum(1 for r in sp4_rows if not r["has_prev"]),
            "no_sample_after": sum(1 for r in sp4_rows if not r["has_next"]),
            "false_positives": len(fp_rows),
            "fp_drop": summarize([r["drop"] for r in fp_rows]),
            "boundary_drop": summarize([r["drop"] for r in sp4_rows if r["drop"] is not None]),
            "boundary_guard": summarize([r["guard"] for r in sp4_rows if r["guard"] is not None]),
        },
        "zero_occupancy_records": {
            "total": zero_guarded + zero_unguarded,
            "excluded_by_nonprimary_guard": zero_guarded,
            "would_enter_the_series": zero_unguarded,
            "models": zero_models.most_common(),
        },
        "primary_model_latch": {
            "sessions_with_permanent_switch": len(latches),
            "samples_routed_away_after_switch": sum(l["lost"] for l in latches),
            "frozen_vs_true_peak": [
                {"frozen_at": l["frozen_at"], "true_peak_after": l["true_peak_after"],
                 "understatement": round(l["true_peak_after"] / l["frozen_at"], 2)
                 if l["frozen_at"] else None,
                 "samples_lost": l["lost"]}
                for l in sorted(latches, key=lambda x: -x["lost"])[:10]
            ],
        },
        "boundary_context": {
            "on_scheduled_task_or_away_marker": sum(
                1 for r in sp4_rows if r["marker_within_30"]),
            "post_exceeds_pre": sum(1 for r in sp4_rows if r["post_exceeds_pre"]),
            "first_post_sample_is_zero": sum(1 for r in sp4_rows if r["next_is_zero"]),
            "no_raw_usage_after": sum(1 for r in sp4_rows if r["raw_usage_after"] == 0),
        },
        "sp4_timestamp_order": {
            "with_samples_both_sides": t_detectable,
            "detected": t_detected,
            "false_negatives": t_detectable - t_detected,
            "false_positives": len(fp_time),
            "fp_drop": summarize([r["drop"] for r in fp_time]),
        },
    }

    print(json.dumps(result, indent=2, default=str))

    if args.json_out:
        with open(args.json_out, "w") as fh:
            json.dump({"summary": result, "sp1": sp1, "sp4": sp4_rows,
                       "false_positives": fp_rows}, fh, indent=2, default=str)
        print(f"\nwrote {args.json_out}", file=sys.stderr)

    if args.fixture:
        write_fixture(files, args.fixture)
        print(f"wrote {args.fixture}", file=sys.stderr)
    return 0


def write_fixture(files, path):
    """Write one real occupancy series spanning a boundary, numbers only.

    Picks the auto-compaction whose surrounding series is long enough to
    exercise the detector and short enough to read: the fixture is a
    regression input for the shipped hysteresis, not a corpus sample.
    """
    best = None
    for f in files:
        series = f.get("_series") or []
        for b in f["boundaries"]:
            meta = b["meta"] or {}
            if meta.get("trigger") != "auto":
                continue
            before = [s for s in series if s["pos"] < b["pos"]]
            after = [s for s in series if s["pos"] > b["pos"]]
            if len(before) < 8 or len(after) < 8:
                continue
            window = before[-8:] + after[:8]
            head = [s["occ"] for s in window[:8]]
            if any(b < a for a, b in zip(head, head[1:])):
                continue  # the approach must be monotone, or the fixture
                          # is testing a resume artifact rather than a
                          # compaction
            cand = {
                "source": "claude code transcript, one auto-compaction",
                "harness": "claude",  # rtevents harness id
                "boundary_index": 8,
                "pre_tokens": meta["pre"],
                "post_tokens": meta["post"],
                "dropped": meta["dropped"],
                "occupancy_series": [s["occ"] for s in window],
                # selection key: the gentlest approach available, so the
                # fixture exercises the detector rather than a single
                # outsized tool result arriving one request before the
                # boundary.
                "_max_step": max(b - a for a, b in zip(head, head[1:])),
            }
            if best is None or cand["_max_step"] < best["_max_step"]:
                best = cand
    if best is None:
        return
    best.pop("_max_step", None)
    with open(path, "w") as fh:
        json.dump(best, fh, indent=2)
        fh.write("\n")


if __name__ == "__main__":
    sys.exit(main())
