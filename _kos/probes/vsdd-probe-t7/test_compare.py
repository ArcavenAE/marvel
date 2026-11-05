"""Tests for the series comparison script (brief section 9.4), on synthetic
run data. Nothing here reads a real transcript or a real run.
"""
import json
import math
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import compare  # noqa: E402

SCRIPT = os.path.join(HERE, "compare.py")
EXAMPLE = os.path.join(HERE, "example", "series.json")
SMALL_COVERAGE = {"sims": 300, "seed": 7, "resamples": 200}


def run(num, den, wave=3):
    return {"widest_wave": wave, "cost": num, "accepted": den}


def spec(a_rows, b_rows, effect=0.2, blind=False, extra_axes=None, waves=None, **top):
    """One cost axis (cost per accepted) over paired synthetic runs."""
    pairs = []
    for i, ((na, da), (nb, db)) in enumerate(zip(a_rows, b_rows)):
        wa, wb = (waves[i] if waves else (3, 3))
        pairs.append({"block": i + 1, "A": run(na, da, wa), "B": run(nb, db, wb)})
    axes = {"cost": {"num": "cost", "den": "accepted", "smallest_effect": effect, "blind": blind}}
    axes.update(extra_axes or {})
    out = {"alpha": 0.05, "seed": 1, "resamples": 500, "coverage": SMALL_COVERAGE,
           "axes": axes, "pairs": pairs}
    out.update(top)
    return out


def tight_rows(ratio, k=7):
    """B costs `ratio` times A per accepted story, with a little run noise."""
    a = [(100.0 * (1 + 0.02 * i), 10.0) for i in range(k)]
    b = [(n * ratio * (1 + 0.01 * (-1) ** i), d) for i, (n, d) in enumerate(a)]
    return a, b


class Distribution(unittest.TestCase):
    def test_t_quantiles(self):
        self.assertAlmostEqual(compare.t_crit(0.05, 6), 2.4469, places=3)
        self.assertAlmostEqual(compare.t_crit(0.05, 1), 12.706, places=2)
        self.assertAlmostEqual(compare.t_crit(0.05, 1000), 1.9623, places=3)

    def test_p_matches_the_quantile(self):
        self.assertAlmostEqual(compare.t_two_sided_p(2.4469, 6), 0.05, places=3)
        self.assertEqual(compare.t_two_sided_p(0, 6), 1.0)

    def test_holm_adjusted_p(self):
        adj, rank = compare.holm([0.01, 0.04, 0.03])
        self.assertEqual([round(x, 10) for x in adj], [0.03, 0.06, 0.06])
        self.assertEqual(rank, [1, 3, 2])


class Estimate(unittest.TestCase):
    ROWS = [(10, 2, 30, 2), (20, 2, 10, 2)]

    def test_hand_counted_two_pairs(self):
        # NA 30, DA 4, NB 40, DB 4: ln(40/4) - ln(30/4) = ln(4/3).
        # Pair shares w = +-5/12, so variance = 2/1 * 2 * (5/12)^2 and se = 5/6.
        self.assertAlmostEqual(compare.log_ratio(self.ROWS), math.log(4 / 3), places=12)
        self.assertAlmostEqual(compare.delta_se(self.ROWS), 5 / 6, places=12)

    def test_no_ratio_with_a_zero_sum(self):
        self.assertIsNone(compare.log_ratio([(0, 2, 5, 2), (0, 2, 5, 2)]))


class Verdicts(unittest.TestCase):
    def test_clear_gap_is_c_and_past_the_smallest_effect(self):
        a, b = tight_rows(1.5)
        r = compare.analyse(spec(a, b))["axes"]["cost"]
        self.assertEqual(r["verdict"], "c")
        self.assertTrue(r["past_smallest_effect"])
        self.assertAlmostEqual(r["ratio"], 1.5, delta=0.02)
        self.assertTrue(r["holm"]["rejected"])
        self.assertGreater(r["interval"]["delta"][0], 1.0)

    def test_clear_gap_below_a_larger_smallest_effect(self):
        a, b = tight_rows(1.5)
        r = compare.analyse(spec(a, b, effect=0.6))["axes"]["cost"]
        self.assertEqual(r["verdict"], "c")
        self.assertFalse(r["past_smallest_effect"])

    def test_gap_that_goes_the_other_way_is_c_too(self):
        a, b = tight_rows(1 / 1.5)
        r = compare.analyse(spec(a, b))["axes"]["cost"]
        self.assertEqual(r["verdict"], "c")
        self.assertTrue(r["past_smallest_effect"])
        self.assertLess(r["interval"]["delta"][1], 1.0)

    def test_noise_is_d_and_states_the_n_to_resolve(self):
        a = [(100, 10), (140, 10), (90, 10), (120, 10), (80, 10), (130, 10), (110, 10)]
        b = [(120, 10), (100, 10), (115, 10), (95, 10), (125, 10), (105, 10), (118, 10)]
        r = compare.analyse(spec(a, b))["axes"]["cost"]
        self.assertEqual(r["verdict"], "d")
        self.assertIn("pairs_to_resolve", r)
        self.assertGreaterEqual(r["pairs_to_resolve"], 2)
        lo, hi = r["interval"]["delta"]
        self.assertLessEqual(lo, 1.0)
        self.assertGreaterEqual(hi, 1.0)

    def test_blind_flag_is_taken_as_input(self):
        a, b = tight_rows(1.5)
        out = compare.analyse(spec(a, b, blind=True))
        r = out["axes"]["cost"]
        self.assertEqual(r["verdict"], "b")
        self.assertNotIn("ratio", r)
        self.assertEqual(out["series"]["close"], "b")

    def test_smallest_effect_is_an_input(self):
        a, b = tight_rows(1.5)
        bad = spec(a, b)
        del bad["axes"]["cost"]["smallest_effect"]
        with self.assertRaises(compare.Refused) as cm:
            compare.analyse(bad)
        self.assertEqual(cm.exception.cause, "no_smallest_effect")


class ZeroTotals(unittest.TestCase):
    def test_both_zero_closes_a_and_reports_counts(self):
        a = [(100.0, 0.0)] * 3
        b = [(120.0, 0.0)] * 3
        out = compare.analyse(spec(a, b))
        r = out["axes"]["cost"]
        self.assertEqual(r["status"], "both_zero")
        self.assertEqual(r["verdict"], "a")
        self.assertNotIn("ratio", r)
        self.assertEqual(r["totals"]["A"], {"num": 300.0, "den": 0.0})
        self.assertEqual(r["totals"]["B"], {"num": 360.0, "den": 0.0})
        self.assertEqual(out["series"]["close"], "a")

    def test_one_zero_closes_c_against_that_arm_with_no_ratio(self):
        a = [(100.0, 0.0)] * 3
        b = [(120.0, 4.0)] * 3
        r = compare.analyse(spec(a, b))["axes"]["cost"]
        self.assertEqual(r["status"], "one_zero")
        self.assertEqual(r["verdict"], "c")
        self.assertEqual(r["against"], "A")
        self.assertNotIn("ratio", r)
        self.assertNotIn("interval", r)
        self.assertEqual(r["totals"]["B"]["den"], 12.0)

    def test_one_zero_against_b(self):
        r = compare.analyse(spec([(100.0, 4.0)] * 3, [(90.0, 0.0)] * 3))["axes"]["cost"]
        self.assertEqual(r["against"], "B")

    def test_a_run_with_zero_accepted_still_counts(self):
        a, b = tight_rows(1.0)
        b[3] = (b[3][0], 0.0)
        r = compare.analyse(spec(a, b))["axes"]["cost"]
        self.assertEqual(r["status"], "estimated")
        self.assertGreater(r["ratio"], 1.0)


class WideWave(unittest.TestCase):
    def test_a_run_under_wave_2_closes_a_and_never_d(self):
        a = [(100, 10), (140, 10), (90, 10), (120, 10), (80, 10), (130, 10), (110, 10)]
        b = [(120, 10), (100, 10), (115, 10), (95, 10), (125, 10), (105, 10), (118, 10)]
        waves = [(3, 3)] * 7
        waves[4] = (3, 1)
        out = compare.analyse(spec(a, b, waves=waves))
        self.assertEqual(out["series"]["close"], "a")
        self.assertEqual(out["series"]["widest_wave_under_2"],
                         [{"block": 5, "arm": "B", "widest_wave": 1}])
        r = out["axes"]["cost"]
        self.assertEqual(r["verdict"], "a")
        self.assertEqual(r["verdict_before_series_close"], "d")

    def test_wave_exactly_2_does_not_close(self):
        a, b = tight_rows(1.5)
        out = compare.analyse(spec(a, b, waves=[(2, 2)] * 7))
        self.assertIsNone(out["series"]["close"])
        self.assertEqual(out["axes"]["cost"]["verdict"], "c")


class HolmFamily(unittest.TestCase):
    def test_blind_and_zero_axes_sit_out_of_the_family(self):
        a, b = tight_rows(1.5)
        pairs = []
        for i, p in enumerate(zip(a, b)):
            (na, da), (nb, db) = p
            pairs.append({"block": i + 1,
                          "A": {"widest_wave": 3, "cost": na, "accepted": da, "hours": 2.0 + 0.1 * i, "stops": 0},
                          "B": {"widest_wave": 3, "cost": nb, "accepted": db, "hours": 1.5 + 0.1 * i, "stops": 0}})
        s = {"alpha": 0.05, "seed": 1, "resamples": 500, "coverage": SMALL_COVERAGE, "pairs": pairs,
             "axes": {
                 "cost": {"num": "cost", "den": "accepted", "smallest_effect": 0.2},
                 "wall_clock": {"num": "hours", "smallest_effect": 0.2},
                 "stops": {"num": "stops", "smallest_effect": 0.5},
                 "noisy": {"num": "hours", "smallest_effect": 0.2, "blind": True}}}
        axes = compare.analyse(s)["axes"]
        self.assertEqual(axes["cost"]["holm"]["family_size"], 2)
        self.assertEqual(axes["wall_clock"]["holm"]["family_size"], 2)
        self.assertEqual(axes["stops"]["status"], "numerator_zero")
        self.assertEqual(axes["noisy"]["verdict"], "blind")
        self.assertGreaterEqual(axes["cost"]["holm"]["adjusted_p"], axes["cost"]["p"])


class Sensitivities(unittest.TestCase):
    def test_hub_wait_and_first_attempt_sit_beside_cost_without_a_verdict(self):
        pairs = []
        for i in range(7):
            pairs.append({"block": i + 1,
                          "A": {"widest_wave": 3, "cost": 100.0 + i, "hub": 10.0, "accepted": 8.0, "first": 6.0},
                          "B": {"widest_wave": 3, "cost": 130.0 + i, "hub": 5.0, "accepted": 8.0, "first": 7.0}})
        s = {"alpha": 0.05, "seed": 1, "resamples": 500, "coverage": SMALL_COVERAGE, "pairs": pairs,
             "axes": {"cost": {"num": "cost", "den": "accepted", "smallest_effect": 0.2,
                               "sensitivities": [
                                   {"name": "hub_wait_removed", "num": "cost", "minus": "hub", "den": "accepted"},
                                   {"name": "first_attempt_only", "num": "cost", "den": "first"}]}}}
        r = compare.analyse(s)["axes"]["cost"]
        by = {x["name"]: x for x in r["sensitivities"]}
        main = r["ratio"]
        self.assertAlmostEqual(by["first_attempt_only"]["ratio"], main * 6.0 / 7.0, places=9)
        # Removing the hub wait takes 10 from every A run and 5 from every B run.
        na = sum(100.0 + i - 10.0 for i in range(7))
        nb = sum(130.0 + i - 5.0 for i in range(7))
        self.assertAlmostEqual(by["hub_wait_removed"]["ratio"], nb / na, places=9)
        for x in by.values():
            self.assertNotIn("verdict", x)
            self.assertIn("interval", x)


class Coverage(unittest.TestCase):
    def test_coverage_is_reported_beside_every_estimated_verdict(self):
        a, b = tight_rows(1.5)
        r = compare.analyse(spec(a, b))["axes"]["cost"]
        c = r["coverage_at_7_pairs"]
        self.assertEqual(c["pairs"], 7)
        self.assertEqual(c["sims"], 300)
        self.assertTrue(0.0 <= c["delta"] <= 1.0)
        self.assertTrue(0.0 <= c["bootstrap"] <= 1.0)
        self.assertEqual(c["model"]["seed"], 7)

    def test_coverage_is_near_nominal_at_the_default_model(self):
        cfg = dict(compare.DEFAULT_COVERAGE, sims=600, resamples=200)
        t = compare.coverage_table([0.05], cfg)[0.05]
        self.assertTrue(0.88 <= t["delta"] <= 0.99, t)
        self.assertTrue(0.80 <= t["bootstrap"] <= 0.99, t)

    def test_bootstrap_undercovers_against_the_delta_interval_at_seven_pairs(self):
        cfg = dict(compare.DEFAULT_COVERAGE, sims=600, resamples=200)
        t = compare.coverage_table([0.05], cfg)[0.05]
        self.assertLessEqual(t["bootstrap"], t["delta"])

    def test_coverage_is_seeded(self):
        cfg = dict(compare.DEFAULT_COVERAGE, sims=100, resamples=50)
        self.assertEqual(compare.coverage_table([0.05], cfg), compare.coverage_table([0.05], cfg))

    def test_a_series_with_other_than_seven_pairs_still_reports_seven_pair_coverage(self):
        a, b = tight_rows(1.5, k=5)
        r = compare.analyse(spec(a, b))["axes"]["cost"]
        self.assertEqual(r["coverage_at_7_pairs"]["pairs"], 7)


class FamilyFlag(unittest.TestCase):
    def pairs(self, k=7):
        a, b = tight_rows(1.5, k=k)
        out = []
        for i, ((na, da), (nb, db)) in enumerate(zip(a, b)):
            out.append({"block": i + 1,
                        "A": {"widest_wave": 3, "cost": na, "accepted": da, "hours": 2.0 + 0.1 * i, "p3": na / 2},
                        "B": {"widest_wave": 3, "cost": nb, "accepted": db, "hours": 1.5 + 0.1 * i, "p3": nb / 2}})
        return out

    def spec(self, with_breakdowns):
        axes = {"cost": {"num": "cost", "den": "accepted", "smallest_effect": 0.2},
                "wall_clock": {"num": "hours", "smallest_effect": 0.2}}
        if with_breakdowns:
            axes["cost_phase3"] = {"num": "p3", "den": "accepted", "smallest_effect": 0.2, "family": False}
            axes["cost_wave3"] = {"num": "cost", "den": "accepted", "smallest_effect": 0.2, "family": False}
        return {"alpha": 0.05, "seed": 1, "resamples": 500, "coverage": SMALL_COVERAGE,
                "axes": axes, "pairs": self.pairs()}

    def test_breakdowns_leave_the_family_size_and_the_cost_verdict_unchanged(self):
        plain = compare.analyse(self.spec(False))["axes"]
        broken = compare.analyse(self.spec(True))["axes"]
        self.assertEqual(plain["cost"]["holm"]["family_size"], 2)
        self.assertEqual(broken["cost"]["holm"]["family_size"], 2)
        self.assertEqual(broken["wall_clock"]["holm"]["family_size"], 2)
        self.assertEqual(broken["cost"]["holm"], plain["cost"]["holm"])
        self.assertEqual(broken["cost"]["verdict"], plain["cost"]["verdict"])
        self.assertEqual(broken["cost"]["interval"], plain["cost"]["interval"])

    def test_a_breakdown_has_an_estimate_and_an_interval_at_alpha_and_no_verdict(self):
        r = compare.analyse(self.spec(True))["axes"]["cost_phase3"]
        self.assertEqual(r["status"], "estimated")
        self.assertAlmostEqual(r["ratio"], 1.5, delta=0.02)
        self.assertEqual(r["interval"]["alpha"], 0.05)
        self.assertNotIn("verdict", r)
        self.assertNotIn("holm", r)
        self.assertNotIn("coverage_at_7_pairs", r)

    def test_a_breakdown_is_not_closed_by_a_narrow_wave(self):
        s = self.spec(True)
        s["pairs"][0]["B"]["widest_wave"] = 1
        out = compare.analyse(s)
        self.assertEqual(out["axes"]["cost"]["verdict"], "a")
        self.assertNotIn("verdict", out["axes"]["cost_phase3"])

    def test_family_must_be_a_boolean(self):
        s = self.spec(True)
        s["axes"]["cost_wave3"]["family"] = "no"
        with self.assertRaises(compare.Refused) as cm:
            compare.analyse(s)
        self.assertEqual(cm.exception.cause, "bad_axis")


class CorrelatedCoverage(unittest.TestCase):
    def test_rho_defaults_to_one_half_and_is_reported_in_the_model(self):
        self.assertEqual(compare.DEFAULT_COVERAGE["rho"], 0.5)
        t = compare.coverage_table([0.05], dict(compare.DEFAULT_COVERAGE, sims=50, resamples=50))
        self.assertEqual(t[0.05]["model"]["rho"], 0.5)

    def test_drawn_numerator_and_denominator_are_correlated_at_rho(self):
        import random
        for rho in (0.0, 0.5, 0.9):
            cfg = dict(compare.DEFAULT_COVERAGE, rho=rho)
            rows = compare.draw_pairs(random.Random(3), 6000, cfg)
            xs = [math.log(r[0]) for r in rows]
            ys = [math.log(r[1]) for r in rows]
            mx, my = sum(xs) / len(xs), sum(ys) / len(ys)
            cov = sum((x - mx) * (y - my) for x, y in zip(xs, ys))
            corr = cov / math.sqrt(sum((x - mx) ** 2 for x in xs) * sum((y - my) ** 2 for y in ys))
            self.assertAlmostEqual(corr, rho, delta=0.05, msg=f"rho {rho}")

    def test_correlation_changes_the_simulated_coverage_inputs(self):
        base = dict(compare.DEFAULT_COVERAGE, sims=200, resamples=50)
        lo = compare.coverage_table([0.05], dict(base, rho=0.0))
        hi = compare.coverage_table([0.05], dict(base, rho=0.9))
        self.assertNotEqual(lo, hi)

    def test_rho_outside_the_open_unit_interval_is_refused(self):
        a, b = tight_rows(1.5)
        s = spec(a, b)
        s["coverage"] = dict(SMALL_COVERAGE, rho=1.0)
        with self.assertRaises(compare.Refused) as cm:
            compare.analyse(s)
        self.assertEqual(cm.exception.cause, "bad_coverage")


class NumeratorZero(unittest.TestCase):
    def test_reports_each_arms_total_and_the_per_pair_counts_outside_the_family(self):
        a, b = tight_rows(1.5, k=4)
        s = spec(a, b)
        for i, p in enumerate(s["pairs"]):
            p["A"]["stops"] = 0
            p["B"]["stops"] = 2 if i == 1 else 0
            p["A"]["hours"], p["B"]["hours"] = 1.0, 1.0
        s["axes"]["stops"] = {"num": "stops", "smallest_effect": 0.5}
        out = compare.analyse(s)["axes"]
        r = out["stops"]
        self.assertEqual(r["status"], "numerator_zero")
        self.assertIsNone(r.get("verdict"))
        self.assertNotIn("holm", r)
        self.assertEqual(r["totals"]["A"]["num"], 0.0)
        self.assertEqual(r["totals"]["B"]["num"], 2.0)
        self.assertEqual(
            r["per_pair"],
            [{"block": i + 1, "A": {"num": 0.0, "den": 1.0}, "B": {"num": 2.0 if i == 1 else 0.0, "den": 1.0}}
             for i in range(4)])
        self.assertEqual(out["cost"]["holm"]["family_size"], 1)


class ReadmeStatesTheReadings(unittest.TestCase):
    def setUp(self):
        with open(os.path.join(HERE, "README.md"), encoding="utf-8") as fh:
            self.text = fh.read()

    def test_both_ends_of_the_smallest_effect_are_stated(self):
        self.assertIn("at least 1.2", self.text)
        self.assertIn("0.833", self.text)

    def test_the_coverage_model_is_pre_registered_and_its_limit_named(self):
        for needle in ("rho", "sigma_block", "pilot", "zero-accepted"):
            self.assertIn(needle, self.text)


class StreamSeeds(unittest.TestCase):
    def test_the_stream_seed_is_sha256_of_the_seed_and_names(self):
        import hashlib
        want = int.from_bytes(hashlib.sha256(b"1:cost").digest(), "big")
        self.assertEqual(compare.stream_seed(1, "cost"), want)
        want = int.from_bytes(hashlib.sha256(b"7:cost:hub_wait_removed").digest(), "big")
        self.assertEqual(compare.stream_seed(7, "cost", "hub_wait_removed"), want)

    def test_two_runs_on_one_input_give_identical_intervals_whatever_the_hash_seed(self):
        outs = []
        for hash_seed in ("1", "2", "random"):
            env = dict(os.environ, PYTHONHASHSEED=hash_seed)
            p = subprocess.run([sys.executable, "-B", SCRIPT, EXAMPLE],
                               capture_output=True, text=True, env=env)
            self.assertEqual(p.returncode, 0, p.stderr)
            outs.append(json.loads(p.stdout))
        self.assertEqual(outs[0], outs[1])
        self.assertEqual(outs[0], outs[2])
        boot = outs[0]["axes"]["cost"]["interval"]["bootstrap"]
        self.assertIsNotNone(boot)
        for sens in outs[0]["axes"]["cost"]["sensitivities"]:
            self.assertIsNotNone(sens["interval"]["bootstrap"])

    def test_readme_states_the_stream_rule(self):
        with open(os.path.join(HERE, "README.md"), encoding="utf-8") as fh:
            text = fh.read()
        self.assertIn("sha256", text)
        self.assertIn("hash()", text)


class NonCostLetters(unittest.TestCase):
    """9.4 scopes the close letters (a) and (b) to cost; (c) and (d) are the
    per-axis verdicts. Other axes get the same zero handling and no letter."""

    def spec(self, a_other, b_other, blind=False):
        a, b = tight_rows(1.5, k=5)
        pairs = []
        for i, ((na, da), (nb, db)) in enumerate(zip(a, b)):
            pairs.append({"block": i + 1,
                          "A": {"widest_wave": 3, "cost": na, "accepted": da, "cache": 40.0, "acc2": a_other},
                          "B": {"widest_wave": 3, "cost": nb, "accepted": db, "cache": 30.0, "acc2": b_other}})
        return {"alpha": 0.05, "seed": 1, "resamples": 500, "coverage": SMALL_COVERAGE, "pairs": pairs,
                "axes": {"cost": {"num": "cost", "den": "accepted", "smallest_effect": 0.2},
                         "cache": {"num": "cache", "den": "acc2", "smallest_effect": 0.2, "blind": blind}}}

    def test_a_blind_non_cost_axis_reads_blind_not_b(self):
        r = compare.analyse(self.spec(5.0, 5.0, blind=True))["axes"]["cache"]
        self.assertEqual(r["status"], "blind")
        self.assertEqual(r["verdict"], "blind")

    def test_a_blind_non_cost_axis_does_not_close_the_series(self):
        out = compare.analyse(self.spec(5.0, 5.0, blind=True))
        self.assertIsNone(out["series"]["close"])

    def test_non_cost_both_zero_is_not_comparable_with_no_letter(self):
        out = compare.analyse(self.spec(0.0, 0.0))
        r = out["axes"]["cache"]
        self.assertEqual(r["status"], "not_comparable")
        self.assertIsNone(r["verdict"])
        self.assertEqual(r["totals"]["A"], {"num": 200.0, "den": 0.0})
        self.assertEqual(r["totals"]["B"], {"num": 150.0, "den": 0.0})
        self.assertIsNone(out["series"]["close"])

    def test_non_cost_one_zero_names_the_arm_with_no_letter(self):
        out = compare.analyse(self.spec(0.0, 5.0))
        r = out["axes"]["cache"]
        self.assertEqual(r["status"], "one_arm_zero")
        self.assertEqual(r["against"], "A")
        self.assertIsNone(r["verdict"])
        self.assertNotIn("ratio", r)
        self.assertEqual(r["totals"]["B"]["den"], 25.0)
        self.assertIsNone(out["series"]["close"])

    def test_non_cost_zero_axes_sit_outside_the_family(self):
        out = compare.analyse(self.spec(0.0, 0.0))
        self.assertEqual(out["axes"]["cost"]["holm"]["family_size"], 1)

    def test_only_cost_feeds_the_series_close_whatever_the_axis_order(self):
        s = self.spec(5.0, 5.0)
        s["axes"] = {"cache": s["axes"]["cache"],
                     "cost": dict(s["axes"]["cost"], blind=True)}
        out = compare.analyse(s)
        self.assertEqual(list(out["axes"]), ["cache", "cost"])
        self.assertEqual(out["series"]["close"], "b")

    def test_cost_keeps_its_letters(self):
        a = [(100.0, 0.0)] * 5
        b = [(120.0, 0.0)] * 5
        r = compare.analyse(spec(a, b))["axes"]["cost"]
        self.assertEqual((r["status"], r["verdict"]), ("both_zero", "a"))


class SensitivityUnderABlindParent(unittest.TestCase):
    def test_a_sensitivity_of_a_blind_cost_axis_is_shown_at_unadjusted_alpha_with_no_verdict(self):
        pairs = []
        for i in range(7):
            pairs.append({"block": i + 1,
                          "A": {"widest_wave": 3, "cost": 100.0 + i, "hub": 10.0, "accepted": 8.0},
                          "B": {"widest_wave": 3, "cost": 130.0 + i, "hub": 5.0, "accepted": 8.0}})
        s = {"alpha": 0.05, "seed": 1, "resamples": 500, "coverage": SMALL_COVERAGE, "pairs": pairs,
             "axes": {"cost": {"num": "cost", "den": "accepted", "smallest_effect": 0.2, "blind": True,
                               "sensitivities": [{"name": "hub_wait_removed", "num": "cost",
                                                  "minus": "hub", "den": "accepted"}]}}}
        r = compare.analyse(s)["axes"]["cost"]
        self.assertEqual(r["verdict"], "b")
        sens = r["sensitivities"][0]
        self.assertEqual(sens["interval"]["alpha"], 0.05)
        self.assertNotIn("verdict", sens)
        self.assertNotIn("holm", sens)


class BootstrapSkips(unittest.TestCase):
    def test_the_skipped_fraction_sits_beside_every_bootstrap_interval(self):
        a, b = tight_rows(1.5)
        r = compare.analyse(spec(a, b))["axes"]["cost"]
        iv = r["interval"]
        self.assertEqual(iv["bootstrap_skipped"], 0)
        self.assertEqual(iv["bootstrap_skipped_fraction"], 0.0)
        self.assertEqual(iv["bootstrap_resamples"], 500)

    def test_resamples_with_a_zero_total_are_dropped_and_counted(self):
        # Two of seven pairs carry every accepted story in arm B, so a resample
        # that misses both has a zero denominator total and is dropped.
        a = [(100.0, 5.0)] * 7
        b = [(110.0, 5.0) if i < 2 else (110.0, 0.0) for i in range(7)]
        r = compare.analyse(spec(a, b))["axes"]["cost"]
        iv = r["interval"]
        self.assertGreater(iv["bootstrap_skipped"], 0)
        self.assertAlmostEqual(iv["bootstrap_skipped_fraction"], iv["bootstrap_skipped"] / 500)


class CoverageSizes(unittest.TestCase):
    def test_sims_of_zero_or_below_is_a_refusal_not_a_crash(self):
        a, b = tight_rows(1.5)
        for sims in (0, -3):
            s = spec(a, b)
            s["coverage"] = dict(SMALL_COVERAGE, sims=sims)
            p = cli(json.dumps(s))
            self.assertEqual(p.returncode, 4, p.stderr)
            self.assertEqual(p.stdout, "")
            self.assertIn("refused: bad_input:", p.stderr)


class HolmDocstring(unittest.TestCase):
    def test_holm_returns_two_values_and_the_docstring_says_so(self):
        self.assertEqual(len(compare.holm([0.01, 0.02])), 2)
        self.assertNotIn("first axis not rejected", compare.holm.__doc__)


def cli(text=None, path=None, *flags):
    if text is not None:
        fd, path = tempfile.mkstemp(suffix=".json")
        with os.fdopen(fd, "w") as fh:
            fh.write(text)
    try:
        return subprocess.run([sys.executable, "-I", "-B", SCRIPT, path, *flags],
                              capture_output=True, text=True)
    finally:
        if text is not None:
            os.unlink(path)


class CommandLine(unittest.TestCase):
    def test_example_runs_and_is_repeatable(self):
        p1 = cli(path=EXAMPLE)
        p2 = cli(path=EXAMPLE)
        self.assertEqual(p1.returncode, 0, p1.stderr)
        self.assertEqual(p1.stdout, p2.stdout)
        out = json.loads(p1.stdout)
        self.assertEqual(out["series"]["pairs"], 7)
        self.assertEqual(out["axes"]["compactions"]["verdict"], "blind")

    def test_text_form(self):
        p = cli(None, EXAMPLE, "--text")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("coverage at 7 pairs", p.stdout)

    def refuses(self, text, cause):
        p = cli(text)
        self.assertEqual(p.returncode, 4, p.stderr)
        self.assertEqual(p.stdout, "")
        self.assertIn(f"refused: {cause}:", p.stderr)

    def test_refuses_an_unpaired_block(self):
        a, b = tight_rows(1.5)
        s = spec(a, b)
        del s["pairs"][2]["B"]
        self.refuses(json.dumps(s), "unpaired_block")

    def test_refuses_a_duplicate_block(self):
        a, b = tight_rows(1.5)
        s = spec(a, b)
        s["pairs"][1]["block"] = 1
        self.refuses(json.dumps(s), "duplicate_block")

    def test_refuses_a_negative_number(self):
        a, b = tight_rows(1.5)
        s = spec(a, b)
        s["pairs"][0]["A"]["cost"] = -1
        self.refuses(json.dumps(s), "negative_number")

    def test_refuses_a_missing_field(self):
        a, b = tight_rows(1.5)
        s = spec(a, b)
        del s["pairs"][4]["B"]["accepted"]
        self.refuses(json.dumps(s), "missing_field")

    def test_refuses_a_missing_widest_wave(self):
        a, b = tight_rows(1.5)
        s = spec(a, b)
        del s["pairs"][4]["A"]["widest_wave"]
        self.refuses(json.dumps(s), "missing_field")

    def test_refuses_one_pair(self):
        a, b = tight_rows(1.5, k=1)
        self.refuses(json.dumps(spec(a, b)), "too_few_pairs")

    def test_refuses_input_that_is_not_json(self):
        self.refuses("{not json", "unreadable_input")

    def test_refuses_a_missing_file(self):
        p = cli(path=os.path.join(HERE, "no-such-file.json"))
        self.assertEqual(p.returncode, 4)
        self.assertIn("refused: unreadable_input:", p.stderr)


if __name__ == "__main__":
    unittest.main()
