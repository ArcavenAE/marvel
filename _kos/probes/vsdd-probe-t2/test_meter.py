"""Fixture test for the cost meter (brief section 11).

Prices are the frozen table in prices.json. Dollar figures are counted by
hand from fixtures/run and fixtures/otel.jsonl: see fixtures/README.md.
"""
import json
import os
import subprocess
import sys
import unittest
from decimal import Decimal as D

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import ledger  # noqa: E402
import meter  # noqa: E402

RUN = os.path.join(HERE, "fixtures", "run")
OTEL = os.path.join(HERE, "fixtures", "otel.jsonl")


class MeterFixture(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.led = ledger.read_run(RUN)
        cls.table = meter.load_prices(os.path.join(HERE, "prices.json"))
        cls.cost = meter.cost_run(cls.led, cls.table)

    def test_price_table_is_the_frozen_one(self):
        self.assertEqual(self.table["opus-5.5"], {"input": D("4"), "write_5m": D("5"), "write_1h": D("8"),
                                                   "read": D("0.20"), "output": D("20")})
        self.assertEqual(self.table["sonnet-5.5"], {"input": D("2"), "write_5m": D("2.50"), "write_1h": D("4"),
                                                     "read": D("0.10"), "output": D("10")})

    def test_cost_by_class_and_model(self):
        opus = self.cost["by_model"]["opus-5.5"]
        self.assertEqual(opus["usd"], D("0.014286"))
        self.assertEqual(opus["by_class"], {"input": D("0.000056"), "write_5m": D("0.011750"),
                                             "write_1h": D("0"), "read": D("0.000680"), "output": D("0.001800")})
        son = self.cost["by_model"]["sonnet-5.5"]
        self.assertEqual(son["usd"], D("0.006396"))
        self.assertEqual(self.cost["transcript_usd"], D("0.020682"))

    def test_cost_by_seat_and_role(self):
        self.assertEqual(self.cost["by_seat"]["orch"]["usd"], D("0.018822"))
        self.assertEqual(self.cost["by_seat"]["r1-c1-w1-g1-a1"]["usd"], D("0.001860"))
        self.assertEqual(self.cost["by_role"]["vsdd-implementer"]["usd"], D("0.006396"))

    def test_unmeasured_supervisor_is_null_and_named(self):
        self.assertIsNone(self.cost["by_seat"]["sup"]["usd"])
        self.assertEqual(self.cost["unmeasured_seats"], ["sup"])

    def test_an_unknown_model_is_unpriced_never_zero(self):
        led = json.loads(json.dumps(self.led))
        led["by_model"]["claude-mystery-9"] = {"tokens": {"input": 5, "write_5m": 0, "write_1h": 0, "read": 0, "output": 5},
                                               "requests": 1}
        c = meter.cost_run(led, self.table)
        self.assertEqual(c["unpriced_models"], ["claude-mystery-9"])
        self.assertIsNone(c["by_model"]["claude-mystery-9"]["usd"])


class OtelRows(unittest.TestCase):
    def setUp(self):
        self.led = ledger.read_run(RUN)
        self.table = meter.load_prices(os.path.join(HERE, "prices.json"))
        self.events = meter.read_otel(OTEL)
        with open(os.path.join(RUN, "run.json")) as f:
            self.source_map = json.load(f)["source_map"]

    def test_otel_only_source_is_priced_as_its_own_row(self):
        rows = meter.otel_rows(self.events, self.table, self.source_map)
        cl = rows["only_in_otel"]["auto_mode_classifier"]
        self.assertEqual(cl["tokens"], {"input": 50, "creation": 40, "read": 0, "output": 12})
        self.assertEqual(cl["usd"], D("0.000320"))

    def test_the_run_total_carries_the_classifier_row(self):
        total = meter.total_with_otel_only(self.led, self.events, self.table, self.source_map)
        self.assertEqual(total["usd"], D("0.021002"))
        self.assertEqual(total["rows"]["auto_mode_classifier"], D("0.000320"))

    def test_reconcile_passes_within_one_percent_per_class(self):
        r = meter.reconcile(self.led, self.events, self.source_map, pct=1.0)
        self.assertTrue(r["pass"], r)
        self.assertEqual(r["only_in_otel"], ["auto_mode_classifier"])

    def test_reconcile_names_the_class_that_is_off(self):
        events = [dict(e) for e in self.events]
        for e in events:
            if e["query_source"] == "agent":
                e["cache_read_tokens"] = int(e["cache_read_tokens"] * 1.02)  # 2% off
        r = meter.reconcile(self.led, events, self.source_map, pct=1.0)
        self.assertFalse(r["pass"])
        self.assertEqual(r["failures"], [{"source_group": "subagent", "class": "read", "transcript": 2200, "otel": 2244}])

    def test_reconcile_treats_zero_against_nonzero_as_a_failure(self):
        events = [dict(e) for e in self.events if e["query_source"] != "agent"]
        r = meter.reconcile(self.led, events, self.source_map, pct=1.0)
        self.assertFalse(r["pass"])

    def test_a_source_in_the_map_but_in_neither_group_is_only_in_otel(self):
        events = self.events + [{"event": "api_request", "query_source": "compact", "model": "claude-opus-5-5",
                                  "input_tokens": 3, "output_tokens": 1, "cache_read_tokens": 0, "cache_creation_tokens": 0}]
        r = meter.reconcile(self.led, events, self.source_map, pct=1.0)
        self.assertEqual(r["only_in_otel"], ["auto_mode_classifier", "compact"])


class SmokeCheckIsStubbed(unittest.TestCase):
    def test_the_smoke_run_check_says_it_waits_on_the_host_card(self):
        p = subprocess.run([sys.executable, "-I", "-B", os.path.join(HERE, "meter.py"), "reconcile-smoke"],
                           capture_output=True, text=True)
        self.assertEqual(p.returncode, 3)
        self.assertIn("R3", p.stderr)


class MeterCli(unittest.TestCase):
    def test_cli_prints_the_cost_json(self):
        p = subprocess.run([sys.executable, "-I", "-B", os.path.join(HERE, "meter.py"), "cost", RUN, "--otel", OTEL],
                           capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(json.loads(p.stdout)["total_usd"], "0.021002")


if __name__ == "__main__":
    unittest.main()
