#!/usr/bin/env python3
"""Cost meter for the vsdd-factory probe (brief section 11).

Prices the ledger's tokens by class from the frozen table (prices.json),
adds the OTEL-only sources (the permission classifier, for one) as their
own rows, and compares transcript totals with OTEL api_request events.
Probe-local: standard library only, no network, writes nothing.

    python3 -I -B meter.py cost RUN_DIR --otel OTEL.jsonl [--prices prices.json]
    python3 -I -B meter.py reconcile RUN_DIR --otel OTEL.jsonl [--pct 1]
    python3 -I -B meter.py reconcile-smoke        # stubbed: waits on the probe host (brief 0.1, R3)

OTEL events are one JSON object per line with event=api_request, query_source,
model, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens.
OTEL has no 5-minute/1-hour write split, so OTEL-only creation tokens are
priced at the 5-minute rate (the pin makes 1-hour writes under 0.1%).
"""
import argparse
import json
import os
import sys
from decimal import Decimal as D

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import ledger  # noqa: E402

CLASSES = ledger.CLASSES
MILLION = D(1000000)
OTEL_CLASSES = ("input", "creation", "read", "output")
REFUSED = 4


class InputError(Exception):
    """An input the meter will not price from: it names its cause."""

    def __init__(self, cause, detail):
        super().__init__("%s: %s" % (cause, detail))
        self.cause, self.detail = cause, detail


class Table(dict):
    match = ()


def load_prices(path):
    with open(path, encoding="utf-8") as f:
        raw = json.load(f)
    t = Table({m: {c: D(v) for c, v in row.items()} for m, row in raw["models"].items()})
    t.match = tuple((p, name) for p, name in raw["match"])
    return t


def price_key(model, table):
    for prefix, name in table.match:
        if model.startswith(prefix):
            return name
    return None


def usd_of(tokens, row):
    by = {c: D(tokens[c]) * row[c] / MILLION for c in CLASSES}
    return by, sum(by.values(), D(0))


def cost_models(models, table):
    """models: {raw model id: {"tokens":..}} -> ({priced name or raw id: row}, unpriced raw ids)"""
    out, unpriced = {}, []
    for model, info in models.items():
        key = price_key(model, table)
        if key is None:
            unpriced.append(model)
            out[model] = {"usd": None, "by_class": None, "tokens": info["tokens"]}
            continue
        by, usd = usd_of(info["tokens"], table[key])
        row = out.setdefault(key, {"usd": D(0), "by_class": {c: D(0) for c in CLASSES}, "tokens": ledger.zero()})
        row["usd"] += usd
        for c in CLASSES:
            row["by_class"][c] += by[c]
        ledger.add(row["tokens"], info["tokens"])
    return out, sorted(unpriced)


def fold(groups, table):
    """groups: {name: {model: info}} -> {name: {"usd": D|None}}. Any unpriced model makes the row None."""
    res = {}
    for name, models in groups.items():
        rows, unpriced = cost_models(models, table)
        if unpriced:
            res[name] = {"usd": None, "unpriced_models": unpriced}
        else:
            res[name] = {"usd": sum((r["usd"] for r in rows.values()), D(0))}
    return res


def cost_run(led, table):
    by_model, unpriced = cost_models(led["by_model"], table)
    by_seat = fold(led.get("by_seat_model", {}), table)
    for name, info in led["by_seat"].items():
        if info["status"] == "unmeasured":
            by_seat[name] = {"usd": None, "status": "unmeasured"}
    return {"by_model": by_model, "unpriced_models": unpriced,
            "transcript_usd": sum((r["usd"] for r in by_model.values() if r["usd"] is not None), D(0)),
            "by_seat": by_seat, "by_role": fold(led.get("by_role_model", {}), table),
            "unmeasured_seats": list(led["unmeasured_seats"])}


def read_otel(path):
    events = []
    with open(path, encoding="utf-8") as f:
        for n, line in enumerate(f, 1):
            line = line.strip()
            if not line:
                continue
            try:
                e = json.loads(line)
            except ValueError:
                raise InputError("bad_otel", "%s line %d is not JSON" % (os.path.basename(path), n))
            if e.get("event") == "api_request":
                events.append(e)
    return events


def otel_tokens(e):
    return {"input": e.get("input_tokens", 0) or 0, "creation": e.get("cache_creation_tokens", 0) or 0,
            "read": e.get("cache_read_tokens", 0) or 0, "output": e.get("output_tokens", 0) or 0}


def otel_usd(tokens, model, table, creation="write_5m"):
    key = price_key(model, table)
    if key is None:
        return None
    row = table[key]
    return (D(tokens["input"]) * row["input"] + D(tokens["creation"]) * row[creation]
            + D(tokens["read"]) * row["read"] + D(tokens["output"]) * row["output"]) / MILLION


def source_group(source, source_map):
    for group in ("main", "subagent"):
        if source in source_map.get(group, []):
            return group
    return None


def otel_rows(events, table, source_map):
    groups = {"main": dict.fromkeys(OTEL_CLASSES, 0), "subagent": dict.fromkeys(OTEL_CLASSES, 0)}
    only = {}
    for e in events:
        tok = otel_tokens(e)
        g = source_group(e.get("query_source"), source_map)
        if g:
            for c in OTEL_CLASSES:
                groups[g][c] += tok[c]
            continue
        row = only.setdefault(e.get("query_source"), {"tokens": dict.fromkeys(OTEL_CLASSES, 0), "usd": D(0),
                                                       "usd_if_creation_was_1h": D(0)})
        for c in OTEL_CLASSES:
            row["tokens"][c] += tok[c]
        for key, rate in (("usd", "write_5m"), ("usd_if_creation_was_1h", "write_1h")):
            usd = otel_usd(tok, e.get("model", ""), table, rate)
            row[key] = None if (usd is None or row[key] is None) else row[key] + usd
    return {"groups": groups, "only_in_otel": only}


def total_with_otel_only(led, events, table, source_map):
    rows = {"transcripts": cost_run(led, table)["transcript_usd"]}
    total = rows["transcripts"]
    for src, row in otel_rows(events, table, source_map)["only_in_otel"].items():
        rows[src] = row["usd"]
        if row["usd"] is not None:
            total += row["usd"]
    return {"usd": total, "rows": rows}


def within(a, b, pct):
    return abs(a - b) <= max(a, b) * pct / 100.0


def reconcile(led, events, source_map, pct=1.0):
    rows = otel_rows(events, table=Table(), source_map=source_map)
    failures = []
    if not any(led["totals"].values()) and not any(v for g in rows["groups"].values() for v in g.values()):
        failures.append({"source_group": "all", "class": "all", "transcript": 0, "otel": 0, "cause": "empty"})
    for group in ("main", "subagent"):
        t = led["by_sidechain"][group]
        transcript = {"input": t["input"], "creation": t["write_5m"] + t["write_1h"], "read": t["read"], "output": t["output"]}
        for c in OTEL_CLASSES:
            a, b = transcript[c], rows["groups"][group][c]
            if not within(a, b, pct):
                failures.append({"source_group": group, "class": c, "transcript": a, "otel": b})
    return {"pass": not failures, "pct": pct, "failures": failures, "only_in_otel": sorted(rows["only_in_otel"])}


def refusals(led, events, table, source_map, run, pct=1.0):
    """Every reason not to price this run, as (cause, detail). Empty means priced."""
    out = []
    if led["requests"] == 0:
        out.append(("no_requests", "no transcript request was read"))
    if led["unmeasured_seats"]:
        out.append(("unmeasured_seat", ", ".join(led["unmeasured_seats"])))
    idle = sorted(n for n, s in led["by_seat"].items() if s["status"] == "measured" and s["requests"] == 0)
    if idle:
        out.append(("seat_no_requests", ", ".join(idle)))
    if led["dedupe"]["bad_lines"]:
        out.append(("bad_lines", "%d transcript line(s) were not JSON" % led["dedupe"]["bad_lines"]))
    if not events:
        out.append(("no_otel_events", "no api_request event was read"))
    unpriced = cost_run(led, table)["unpriced_models"]
    unpriced += sorted({e.get("model", "") for e in events if price_key(e.get("model", ""), table) is None})
    if unpriced:
        out.append(("unpriced_model", ", ".join(sorted(set(unpriced)))))
    declared = set(run.get("otel_only", []))
    undeclared = sorted(s for s in otel_rows(events, table, source_map)["only_in_otel"] if s not in declared)
    if undeclared:
        out.append(("undeclared_otel_source", ", ".join(str(s) for s in undeclared)
                    + " (name it under source_map main or subagent, or under otel_only in run.json)"))
    rec = reconcile(led, events, source_map, pct)
    if not rec["pass"]:
        out.append(("reconcile_failed", json.dumps(rec["failures"], sort_keys=True)))
    return out


def assumptions(led):
    return {"unsplit_write_tokens": led["unsplit_write_tokens"],
            "unsplit_writes_priced_at": "the 5-minute rate",
            "unsplit_writes_note": "an underprice in the defaults cell, where 1-hour writes are expected",
            "otel_creation_priced_at": "the 5-minute rate (OTEL has no write split); see usd_if_creation_was_1h per row",
            "ttl_pin_checked_on": "transcript writes only, not OTEL-only sources"}


def jsonable(x):
    if isinstance(x, D):
        return format(x, "f")
    if isinstance(x, dict):
        return {k: jsonable(v) for k, v in x.items()}
    if isinstance(x, (list, tuple)):
        return [jsonable(v) for v in x]
    return x


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    sub = ap.add_subparsers(dest="cmd", required=True)
    for name in ("cost", "reconcile"):
        p = sub.add_parser(name)
        p.add_argument("run_dir")
        p.add_argument("--otel", required=True)
        p.add_argument("--prices", default=os.path.join(HERE, "prices.json"))
        p.add_argument("--pct", type=float, default=1.0)
    sub.add_parser("reconcile-smoke")
    a = ap.parse_args(argv)
    if a.cmd == "reconcile-smoke":
        print("reconcile-smoke: not run. Reconciling a smoke run with OTEL api_request events "
              "waits on the probe host ruling (brief section 0.1, R3); the fixture check does not.", file=sys.stderr)
        return 3
    try:
        led = ledger.read_run(a.run_dir)
        with open(os.path.join(a.run_dir, "run.json"), encoding="utf-8") as f:
            run = json.load(f)
        source_map = run.get("source_map", {})
        events = read_otel(a.otel)
    except InputError as e:
        print("refused: %s: %s" % (e.cause, e.detail), file=sys.stderr)
        return REFUSED
    except (OSError, ValueError) as e:
        print("refused: unreadable_input: %s" % e, file=sys.stderr)
        return REFUSED
    if a.cmd == "reconcile":
        result = reconcile(led, events, source_map, a.pct)
        print(json.dumps(result, indent=2, sort_keys=True))
        return 0 if result["pass"] else 1
    table = load_prices(a.prices)
    reasons = refusals(led, events, table, source_map, run, a.pct)
    if reasons:
        for cause, detail in reasons:
            print("refused: %s: %s" % (cause, detail), file=sys.stderr)
        return REFUSED
    tot = total_with_otel_only(led, events, table, source_map)
    out = {"total_usd": tot["usd"], "rows": tot["rows"], "cost": cost_run(led, table),
           "otel_only": otel_rows(events, table, source_map)["only_in_otel"], "assumptions": assumptions(led)}
    print(json.dumps(jsonable(out), indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
