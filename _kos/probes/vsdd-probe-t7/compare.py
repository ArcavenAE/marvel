#!/usr/bin/env python3
"""vsdd probe T7: the series comparison script (brief section 9.4).

For each axis it estimates arm B over arm A as a ratio of sums, with a delta
method interval on the log of the ratio blocked by pair, a percentile
bootstrap over pairs as a sensitivity, a Holm-adjusted verdict, and the real
coverage of both intervals at seven pairs beside every verdict. Standard
library only; no network; it writes nothing.

    compare.py INPUT.json [--text]

It exits 0 on a comparison, 2 on bad usage, and 4 when it refuses the input
(`refused: <cause>: <detail>` on stderr, nothing on stdout). See README.md.
"""
import argparse
import hashlib
import json
import math
import random
import sys

REFUSED = 4
COVERAGE_PAIRS = 7
DEFAULT_ALPHA = 0.05
DEFAULT_RESAMPLES = 10000
DEFAULT_COVERAGE = {
    "sims": 1000,
    "seed": 7,
    "sigma_block": 0.2,
    "sigma_run": 0.2,
    "rho": 0.5,
    "resamples": 500,
    "true_log_ratio": 0.0,
}
MAX_N = 1000
COST = "cost"


class Refused(Exception):
    def __init__(self, cause, detail):
        super().__init__(f"{cause}: {detail}")
        self.cause = cause
        self.detail = detail


# ---- Student t, from the regularized incomplete beta -----------------------


def _betacf(a, b, x):
    tiny = 1e-300
    qab, qap, qam = a + b, a + 1.0, a - 1.0
    c = 1.0
    d = 1.0 - qab * x / qap
    d = tiny if abs(d) < tiny else d
    d = 1.0 / d
    h = d
    for m in range(1, 400):
        m2 = 2 * m
        aa = m * (b - m) * x / ((qam + m2) * (a + m2))
        d = 1.0 + aa * d
        d = tiny if abs(d) < tiny else d
        c = 1.0 + aa / c
        c = tiny if abs(c) < tiny else c
        d = 1.0 / d
        h *= d * c
        aa = -(a + m) * (qab + m) * x / ((a + m2) * (qap + m2))
        d = 1.0 + aa * d
        d = tiny if abs(d) < tiny else d
        c = 1.0 + aa / c
        c = tiny if abs(c) < tiny else c
        d = 1.0 / d
        delta = d * c
        h *= delta
        if abs(delta - 1.0) < 1e-14:
            break
    return h


def betai(a, b, x):
    if x <= 0.0:
        return 0.0
    if x >= 1.0:
        return 1.0
    bt = math.exp(
        math.lgamma(a + b) - math.lgamma(a) - math.lgamma(b)
        + a * math.log(x) + b * math.log1p(-x)
    )
    if x < (a + 1.0) / (a + b + 2.0):
        return bt * _betacf(a, b, x) / a
    return 1.0 - bt * _betacf(b, a, 1.0 - x) / b


def t_two_sided_p(t, df):
    """P(|T| >= |t|) for Student t with df degrees of freedom."""
    if t == 0:
        return 1.0
    return betai(df / 2.0, 0.5, df / (df + t * t))


def t_crit(alpha, df):
    """The t with P(|T| >= t) = alpha, by bisection."""
    lo, hi = 0.0, 1.0
    while t_two_sided_p(hi, df) > alpha:
        hi *= 2.0
        if hi > 1e12:
            break
    for _ in range(200):
        mid = (lo + hi) / 2.0
        if t_two_sided_p(mid, df) > alpha:
            lo = mid
        else:
            hi = mid
    return (lo + hi) / 2.0


# ---- the estimate ----------------------------------------------------------


def sums(rows):
    na = sum(r[0] for r in rows)
    da = sum(r[1] for r in rows)
    nb = sum(r[2] for r in rows)
    db = sum(r[3] for r in rows)
    return na, da, nb, db


def log_ratio(rows):
    na, da, nb, db = sums(rows)
    if min(na, da, nb, db) <= 0:
        return None
    return math.log(nb / db) - math.log(na / da)


def delta_se(rows):
    """Delta method on the log of the ratio of sums, blocked by pair.

    Each pair contributes w_i = (nB_i/NB - dB_i/DB) - (nA_i/NA - dA_i/DA),
    the pair's share of the linearized error. The w_i sum to zero, and the
    variance is k/(k-1) times their sum of squares.
    """
    k = len(rows)
    na, da, nb, db = sums(rows)
    w = [(r[2] / nb - r[3] / db) - (r[0] / na - r[1] / da) for r in rows]
    return math.sqrt(k / (k - 1) * sum(x * x for x in w))


def bootstrap_interval(rows, alpha, resamples, rng):
    k = len(rows)
    draws = []
    skipped = 0
    for _ in range(resamples):
        v = log_ratio([rows[rng.randrange(k)] for _ in range(k)])
        if v is None:
            skipped += 1
        else:
            draws.append(v)
    if len(draws) < 2:
        return None, skipped
    draws.sort()
    lo = draws[int(math.floor(alpha / 2.0 * (len(draws) - 1)))]
    hi = draws[int(math.ceil((1.0 - alpha / 2.0) * (len(draws) - 1)))]
    return (lo, hi), skipped


def holm(pvals):
    """Holm step-down. Returns two lists in the input order: the adjusted p
    values and the ranks (1 = smallest p)."""
    m = len(pvals)
    order = sorted(range(m), key=lambda i: (pvals[i], i))
    adj = [0.0] * m
    running = 0.0
    for j, i in enumerate(order):
        running = max(running, min(1.0, (m - j) * pvals[i]))
        adj[i] = running
    rank = [0] * m
    for j, i in enumerate(order):
        rank[i] = j + 1
    return adj, rank


def resolving_n(se, k, alpha, effect_log):
    """The least number of pairs at which the interval half-width falls to the
    smallest effect, scaling the observed se by sqrt(k/n). None past MAX_N."""
    if se == 0:
        return k

    def half(n):
        return t_crit(alpha, n - 1) * se * math.sqrt(k / n)

    if half(MAX_N) > effect_log:
        return None
    lo, hi = 2, MAX_N
    while lo < hi:
        mid = (lo + hi) // 2
        if half(mid) <= effect_log:
            hi = mid
        else:
            lo = mid + 1
    return lo


# ---- coverage at seven pairs ----------------------------------------------


def draw_pairs(rng, k, cfg):
    """k synthetic pairs (nA, dA, nB, dB). Each pair has a block factor shared
    by both arms and a run factor per run, lognormal. On both levels the
    numerator and denominator factors are correlated at rho, since a run that
    accepts more stories spends more. Arm B's numerator is scaled by
    exp(true_log_ratio)."""
    theta0 = cfg["true_log_ratio"]
    sb, sr, rho = cfg["sigma_block"], cfg["sigma_run"], cfg["rho"]
    rest = math.sqrt(1.0 - rho * rho)

    def corr():
        x = rng.gauss(0, 1)
        return x, rho * x + rest * rng.gauss(0, 1)

    rows = []
    for _ in range(k):
        bn, bd = corr()
        an, ad = corr()
        cn, cd = corr()
        rows.append((
            math.exp(sb * bn + sr * an),
            math.exp(sb * bd + sr * ad),
            math.exp(sb * bn + sr * cn + theta0),
            math.exp(sb * bd + sr * cd),
        ))
    return rows


def coverage_table(alphas, cfg, k=COVERAGE_PAIRS):
    """Real coverage of the delta and bootstrap intervals at k pairs, by
    simulation over draw_pairs. The model has no zero-accepted runs, so it
    says nothing about the zero-total cases."""
    rng = random.Random(cfg["seed"])
    theta0 = cfg["true_log_ratio"]
    hit = {a: [0, 0, 0] for a in alphas}
    crit = {a: t_crit(a, k - 1) for a in alphas}
    for _ in range(cfg["sims"]):
        rows = draw_pairs(rng, k, cfg)
        theta = log_ratio(rows)
        se = delta_se(rows)
        draws = []
        for _ in range(cfg["resamples"]):
            v = log_ratio([rows[rng.randrange(k)] for _ in range(k)])
            if v is not None:
                draws.append(v)
        draws.sort()
        for a in alphas:
            h = hit[a]
            h[2] += 1
            if abs(theta - theta0) <= crit[a] * se:
                h[0] += 1
            if len(draws) > 1:
                lo = draws[int(math.floor(a / 2.0 * (len(draws) - 1)))]
                hi = draws[int(math.ceil((1.0 - a / 2.0) * (len(draws) - 1)))]
                if lo <= theta0 <= hi:
                    h[1] += 1
    return {
        a: {
            "pairs": k,
            "delta": h[0] / h[2],
            "bootstrap": h[1] / h[2],
            "sims": cfg["sims"],
            "model": {
                "sigma_block": cfg["sigma_block"],
                "sigma_run": cfg["sigma_run"],
                "rho": cfg["rho"],
                "true_log_ratio": theta0,
                "seed": cfg["seed"],
                "bootstrap_resamples": cfg["resamples"],
            },
        }
        for a, h in hit.items()
    }


# ---- input -----------------------------------------------------------------


def number(run, field, where):
    if field not in run:
        raise Refused("missing_field", f"{where} has no {field!r}")
    v = run[field]
    if isinstance(v, bool) or not isinstance(v, (int, float)) or not math.isfinite(v):
        raise Refused("bad_number", f"{where}.{field} is {v!r}")
    if v < 0:
        raise Refused("negative_number", f"{where}.{field} is {v!r}")
    return float(v)


def load(spec):
    if not isinstance(spec, dict):
        raise Refused("bad_input", "the input is not an object")
    alpha = spec.get("alpha", DEFAULT_ALPHA)
    if not isinstance(alpha, (int, float)) or not 0 < alpha < 1:
        raise Refused("bad_alpha", f"alpha is {alpha!r}")
    resamples = spec.get("resamples", DEFAULT_RESAMPLES)
    if not isinstance(resamples, int) or resamples < 100:
        raise Refused("bad_resamples", f"resamples is {resamples!r}")
    cov = dict(DEFAULT_COVERAGE)
    cov.update(spec.get("coverage", {}))
    for key in ("sims", "resamples"):
        v = cov[key]
        if isinstance(v, bool) or not isinstance(v, int) or v < 1:
            raise Refused("bad_input", f"coverage {key} is {v!r}, need a whole number of at least 1")
    rho = cov["rho"]
    if isinstance(rho, bool) or not isinstance(rho, (int, float)) or not -1 < rho < 1:
        raise Refused("bad_coverage", f"rho is {rho!r}, need a number above -1 and below 1")
    pairs = spec.get("pairs")
    if not isinstance(pairs, list) or len(pairs) < 2:
        raise Refused("too_few_pairs", "need at least 2 pairs")
    blocks = set()
    for i, p in enumerate(pairs):
        if not isinstance(p, dict) or "A" not in p or "B" not in p:
            raise Refused("unpaired_block", f"pairs[{i}] needs both an A run and a B run")
        b = p.get("block", i + 1)
        if b in blocks:
            raise Refused("duplicate_block", f"block {b!r} appears twice")
        blocks.add(b)
    axes = spec.get("axes")
    if not isinstance(axes, dict) or not axes:
        raise Refused("no_axes", "the input names no axes")
    for name, ax in axes.items():
        if "num" not in ax:
            raise Refused("bad_axis", f"axis {name!r} has no num field")
        if not isinstance(ax.get("family", True), bool):
            raise Refused("bad_axis", f"axis {name!r} has a family that is not true or false")
        e = ax.get("smallest_effect")
        if isinstance(e, bool) or not isinstance(e, (int, float)) or e <= 0:
            raise Refused("no_smallest_effect", f"axis {name!r} needs a smallest_effect above 0")
    return alpha, resamples, cov, pairs, axes


def axis_rows(pairs, defn):
    """Per pair: (nA, dA, nB, dB). With no den field the denominator is one
    per run, so the ratio of sums is the per-run mean."""
    rows = []
    for i, p in enumerate(pairs):
        vals = []
        for arm in ("A", "B"):
            run = p[arm]
            where = f"pairs[{i}].{arm}"
            n = number(run, defn["num"], where)
            if "minus" in defn:
                n -= number(run, defn["minus"], where)
                if n < 0:
                    raise Refused("negative_number", f"{where}: {defn['minus']} exceeds {defn['num']}")
            d = number(run, defn["den"], where) if "den" in defn else 1.0
            vals.extend([n, d])
        rows.append((vals[0], vals[1], vals[2], vals[3]))
    return rows


def totals_note(rows):
    na, da, nb, db = sums(rows)
    return {"A": {"num": na, "den": da}, "B": {"num": nb, "den": db}}


# ---- one axis --------------------------------------------------------------


def classify(rows, is_cost):
    """None when a ratio can be computed, else the zero-total cases. 9.4 writes
    the zero-total closes for cost, so only cost gets a letter, (a) for both
    totals zero and (c) against the arm with the zero. Any other axis meets the
    same zeros and reads a status and no letter."""
    na, da, nb, db = sums(rows)
    if da == 0 and db == 0:
        if is_cost:
            return {"status": "both_zero", "verdict": "a"}
        return {"status": "not_comparable", "verdict": None}
    if da == 0 or db == 0:
        arm = "A" if da == 0 else "B"
        if is_cost:
            return {"status": "one_zero", "verdict": "c", "against": arm}
        return {"status": "one_arm_zero", "verdict": None, "against": arm}
    if na == 0 or nb == 0:
        return {"status": "numerator_zero", "verdict": None}
    return None


def interval_block(rows, theta, se, alpha, resamples, rng):
    k = len(rows)
    half = t_crit(alpha, k - 1) * se
    boot, skipped = bootstrap_interval(rows, alpha, resamples, rng)
    out = {
        "alpha": alpha,
        "delta": [math.exp(theta - half), math.exp(theta + half)],
        "bootstrap": None if boot is None else [math.exp(boot[0]), math.exp(boot[1])],
        "bootstrap_resamples": resamples,
        "bootstrap_skipped": skipped,
        "bootstrap_skipped_fraction": skipped / resamples,
    }
    return out


def estimate_axis(rows):
    theta = log_ratio(rows)
    se = delta_se(rows)
    k = len(rows)
    if se > 0:
        p = t_two_sided_p(theta / se, k - 1)
    else:
        p = 0.0 if theta != 0 else 1.0
    return theta, se, p


def stream_seed(seed, *parts):
    """The seed of one bootstrap stream: sha256 of "seed:part:part", read as an
    integer. Never Python's hash() of a string, which differs per process."""
    text = ":".join(str(x) for x in (seed,) + parts)
    return int.from_bytes(hashlib.sha256(text.encode("utf-8")).digest(), "big")


def analyse(spec):
    alpha, resamples, cov, pairs, axes = load(spec)
    k = len(pairs)
    seed = spec.get("seed", 1)

    def axis_rng(*parts):
        # One stream per axis and sensitivity, so adding an axis never moves
        # another axis's bootstrap.
        return random.Random(stream_seed(seed, *parts))

    narrow = []
    for i, p in enumerate(pairs):
        for arm in ("A", "B"):
            w = number(p[arm], "widest_wave", f"pairs[{i}].{arm}")
            if w < 2:
                narrow.append({"block": p.get("block", i + 1), "arm": arm, "widest_wave": int(w)})

    result = {}
    estimable = []
    for name, defn in axes.items():
        rows = axis_rows(pairs, defn)
        rec = {"totals": totals_note(rows), "smallest_effect": defn["smallest_effect"]}
        result[name] = rec
        if defn.get("blind"):
            # 9.4: (b) is cost's close; any other blind axis reads "blind".
            rec.update({"status": "blind", "verdict": "b" if name == COST else "blind"})
            if not defn.get("family", True):
                del rec["verdict"]
            continue
        special = classify(rows, name == COST)
        if special:
            rec.update(special)
            if not defn.get("family", True):
                rec.pop("verdict", None)
            if special["status"] == "numerator_zero":
                rec["per_pair"] = [
                    {"block": pairs[i].get("block", i + 1),
                     "A": {"num": r[0], "den": r[1]}, "B": {"num": r[2], "den": r[3]}}
                    for i, r in enumerate(rows)
                ]
            continue
        theta, se, p = estimate_axis(rows)
        rec.update({
            "status": "estimated",
            "ratio": math.exp(theta),
            "log_ratio": theta,
            "se_log": se,
            "p": p,
        })
        if defn.get("family", True):
            estimable.append((name, rows, theta, se, p))
        else:
            # A breakdown: an estimate and an interval at alpha, no verdict.
            rec["interval"] = interval_block(rows, theta, se, alpha, resamples, axis_rng(name))

    # Holm over the axes that have an estimate; blind and zero-total axes sit out.
    levels = {}
    if estimable:
        m = len(estimable)
        adj, rank = holm([e[4] for e in estimable])
        first_kept = None
        for j in range(1, m + 1):
            i = rank.index(j)
            if estimable[i][4] > alpha / (m - j + 1):
                first_kept = j
                break
        for i, (name, rows, theta, se, p) in enumerate(estimable):
            j = rank[i]
            stop = first_kept if first_kept is not None else m + 1
            levels[name] = alpha / (m - min(j, stop) + 1)
            rejected = first_kept is None or j < first_kept
            rec = result[name]
            rec["holm"] = {"family_size": m, "rank": j, "adjusted_p": adj[i], "rejected": rejected}
            ax = axes[name]
            a_used = levels[name]
            rec["interval"] = interval_block(rows, theta, se, a_used, resamples, axis_rng(name))
            eff = math.log(1.0 + ax["smallest_effect"])
            if rejected:
                near = min(abs(theta - t_crit(a_used, k - 1) * se), abs(theta + t_crit(a_used, k - 1) * se))
                rec["verdict"] = "c"
                rec["past_smallest_effect"] = near >= eff
            else:
                rec["verdict"] = "d"
                n = resolving_n(se, k, a_used, eff)
                rec["pairs_to_resolve"] = n if n is not None else f">{MAX_N}"

    # Sensitivities sit beside an axis: an estimate and an interval, no verdict.
    for name, defn in axes.items():
        for sens in defn.get("sensitivities", []):
            srows = axis_rows(pairs, {**sens, "smallest_effect": defn["smallest_effect"]})
            entry = {"name": sens.get("name", "sensitivity"), "totals": totals_note(srows)}
            special = classify(srows, name == COST)
            if special:
                entry.update({"status": special["status"]})
            else:
                theta, se, _ = estimate_axis(srows)
                a_used = levels.get(name, alpha)
                entry.update({
                    "status": "estimated",
                    "ratio": math.exp(theta),
                    "interval": interval_block(srows, theta, se, a_used, resamples, axis_rng(name, entry["name"])),
                })
            result[name].setdefault("sensitivities", []).append(entry)

    # Coverage at seven pairs beside every verdict that rests on an interval.
    used = sorted({levels[n] for n in levels})
    table = coverage_table(used, cov) if used else {}
    for name, a_used in levels.items():
        result[name]["coverage_at_7_pairs"] = table[a_used]

    # Section 9.4: a series with a run whose widest wave is under 2 closes (a).
    if narrow:
        for name, rec in result.items():
            if not axes[name].get("family", True):
                continue
            rec["verdict_before_series_close"] = rec.get("verdict")
            rec["verdict"] = "a"
    series = {"pairs": k, "alpha": alpha, "widest_wave_under_2": narrow, "close": None}
    if narrow:
        series["close"] = "a"
    else:
        cost = result.get("cost")
        if cost and cost.get("verdict") in ("a", "b"):
            series["close"] = cost["verdict"]
    return {"series": series, "axes": result}


# ---- output ----------------------------------------------------------------


def render_text(out):
    s = out["series"]
    lines = [f"pairs {s['pairs']}  alpha {s['alpha']}  series close: {s['close'] or 'none'}"]
    for w in s["widest_wave_under_2"]:
        lines.append(f"  widest wave under 2: block {w['block']} arm {w['arm']} width {w['widest_wave']}")
    for name, r in out["axes"].items():
        v = r.get("verdict")
        head = f"{name}: {'no verdict' if v is None else f'verdict ({v})'} {r['status']}"
        if "ratio" in r:
            iv = r["interval"]["delta"]
            head += f"  B/A {r['ratio']:.4g}  [{iv[0]:.4g}, {iv[1]:.4g}]"
            if r.get("past_smallest_effect") is not None:
                head += "  past smallest effect" if r["past_smallest_effect"] else "  below smallest effect"
            if "pairs_to_resolve" in r:
                head += f"  pairs to resolve {r['pairs_to_resolve']}"
            c = r["coverage_at_7_pairs"]
            head += f"  coverage at 7 pairs: delta {c['delta']:.3f} bootstrap {c['bootstrap']:.3f}"
        if r.get("against"):
            head += f"  against arm {r['against']}"
        lines.append(head)
    return "\n".join(lines)


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("input")
    ap.add_argument("--text", action="store_true", help="a short table instead of JSON")
    args = ap.parse_args(argv)
    try:
        try:
            with open(args.input, encoding="utf-8") as fh:
                spec = json.load(fh)
        except (OSError, ValueError) as e:
            raise Refused("unreadable_input", f"{args.input}: {e}")
        out = analyse(spec)
    except Refused as e:
        print(f"refused: {e.cause}: {e.detail}", file=sys.stderr)
        return REFUSED
    print(render_text(out) if args.text else json.dumps(out, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
