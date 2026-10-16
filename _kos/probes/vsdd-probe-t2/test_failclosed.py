"""The meter fails closed (review of marvel#901).

A cost is never computed from nothing: every case below is an input the
meter used to price and exit 0 on. Each must now exit 4, name its cause on
stderr and print no cost on stdout. The closed fixture is the control.
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from decimal import Decimal as D

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import ledger  # noqa: E402
import meter  # noqa: E402

FIXTURE = os.path.join(HERE, "fixtures")
REFUSED = 4


class Case(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.dir = self._tmp.name
        self.run_dir = os.path.join(self.dir, "run")
        self.otel = os.path.join(self.dir, "otel.jsonl")
        shutil.copytree(os.path.join(FIXTURE, "run"), self.run_dir)
        shutil.copy(os.path.join(FIXTURE, "otel.jsonl"), self.otel)
        # the closed control: every seat measured
        self.edit_run(lambda r: r.update(seats=[s for s in r["seats"] if s["transcript"]]))

    def edit_run(self, fn):
        p = os.path.join(self.run_dir, "run.json")
        with open(p) as f:
            r = json.load(f)
        fn(r)
        with open(p, "w") as f:
            json.dump(r, f)

    def blank(self, rel):
        open(os.path.join(self.run_dir, rel), "w").close()

    def append(self, rel, text):
        with open(os.path.join(self.run_dir, rel), "a") as f:
            f.write(text)

    def cli(self, *args):
        return subprocess.run([sys.executable, "-I", "-B", os.path.join(HERE, "meter.py"), *args],
                              capture_output=True, text=True)

    def cost(self):
        return self.cli("cost", self.run_dir, "--otel", self.otel)

    def assert_refused(self, p, cause):
        self.assertEqual(p.returncode, REFUSED, p.stdout + p.stderr)
        self.assertIn("refused", p.stderr)
        self.assertIn(cause, p.stderr)
        self.assertEqual(p.stdout.strip(), "", "a refused run printed a cost")
        self.assertNotIn("Traceback", p.stderr)


class TheControl(Case):
    def test_the_closed_fixture_is_priced(self):
        p = self.cost()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(json.loads(p.stdout)["total_usd"], "0.021002")


class RefusesNothingToPrice(Case):
    def test_every_transcript_and_the_otel_file_empty(self):
        for rel in ("orch.jsonl", "branch.jsonl", "orch/subagents/agent-a1.jsonl", "orch/subagents/agent-a2.jsonl"):
            self.blank(rel)
        open(self.otel, "w").close()
        p = self.cost()
        self.assert_refused(p, "no_requests")
        self.assertIn("no_otel_events", p.stderr)

    def test_every_seat_unmeasured(self):
        self.edit_run(lambda r: [s.update(transcript=None) for s in r["seats"]])
        p = self.cost()
        self.assert_refused(p, "unmeasured_seat")
        self.assertIn("no_requests", p.stderr)

    def test_one_seat_unmeasured(self):
        self.edit_run(lambda r: r["seats"].append({"name": "sup", "kind": "supervisor", "role": "supervisor",
                                                   "transcript": None}))
        self.assert_refused(self.cost(), "unmeasured_seat")

    def test_a_measured_seat_with_no_requests(self):
        self.blank("branch.jsonl")
        self.assert_refused(self.cost(), "seat_no_requests")

    def test_no_otel_events(self):
        open(self.otel, "w").close()
        self.assert_refused(self.cost(), "no_otel_events")


class RefusesUnreadableInput(Case):
    def test_a_transcript_line_cut_short(self):
        self.append("orch.jsonl", '{"type": "assistant", "message": {"id": "m_cut"')
        self.assert_refused(self.cost(), "bad_lines")

    def test_a_transcript_of_garbage(self):
        self.append("branch.jsonl", "not json\n{{{\n")
        self.assert_refused(self.cost(), "bad_lines")

    def test_a_missing_otel_file_is_refused_not_a_traceback(self):
        os.remove(self.otel)
        self.assert_refused(self.cost(), "unreadable_input")

    def test_a_missing_transcript_is_refused_not_a_traceback(self):
        os.remove(os.path.join(self.run_dir, "branch.jsonl"))
        self.assert_refused(self.cost(), "unreadable_input")

    def test_an_otel_file_cut_mid_line(self):
        with open(self.otel, "a") as f:
            f.write('{"event": "api_request", "query_sou')
        self.assert_refused(self.cost(), "bad_otel")


class RefusesWhatItCannotPrice(Case):
    def test_a_model_with_no_price_is_not_dropped_from_the_total(self):
        p = os.path.join(self.run_dir, "branch.jsonl")
        text = open(p).read().replace("claude-sonnet-5-5", "claude-mystery-9")
        open(p, "w").write(text)
        self.assert_refused(self.cost(), "unpriced_model")

    def test_an_otel_event_with_no_price(self):
        with open(self.otel, "a") as f:
            f.write(json.dumps({"event": "api_request", "query_source": "auto_mode_classifier",
                                "model": "claude-mystery-9", "input_tokens": 1, "output_tokens": 1,
                                "cache_read_tokens": 0, "cache_creation_tokens": 0}) + "\n")
        self.assert_refused(self.cost(), "unpriced_model")


class RefusesASourceMapThatDoesNotMatch(Case):
    def test_a_misnamed_main_source_is_not_counted_twice(self):
        self.edit_run(lambda r: r["source_map"].update(main=["repl_main"]))
        p = self.cost()
        self.assert_refused(p, "undeclared_otel_source")
        self.assertIn("repl_main_thread", p.stderr)
        self.assertIn("reconcile_failed", p.stderr)

    def test_an_otel_source_the_run_did_not_declare(self):
        with open(self.otel, "a") as f:
            f.write(json.dumps({"event": "api_request", "query_source": "compact", "model": "claude-opus-5-5",
                                "input_tokens": 3, "output_tokens": 1, "cache_read_tokens": 0,
                                "cache_creation_tokens": 0}) + "\n")
        p = self.cost()
        self.assert_refused(p, "undeclared_otel_source")
        self.assertIn("compact", p.stderr)

    def test_declaring_the_source_makes_it_priced_again(self):
        with open(self.otel, "a") as f:
            f.write(json.dumps({"event": "api_request", "query_source": "compact", "model": "claude-opus-5-5",
                                "input_tokens": 3, "output_tokens": 1, "cache_read_tokens": 0,
                                "cache_creation_tokens": 0}) + "\n")
        self.edit_run(lambda r: r["otel_only"].append("compact"))
        p = self.cost()
        self.assertEqual(p.returncode, 0, p.stderr)
        # 3 x $4 + 1 x $20 per million, on top of $0.021002
        self.assertEqual(json.loads(p.stdout)["total_usd"], "0.021034")

    def test_a_subagent_group_that_went_missing_from_otel_is_a_reconcile_failure(self):
        rows = [json.loads(line) for line in open(self.otel)]
        open(self.otel, "w").write("".join(json.dumps(r) + "\n" for r in rows if r["query_source"] != "agent"))
        self.assert_refused(self.cost(), "reconcile_failed")


class ReconcileRefusesEmptyInput(Case):
    def test_both_sides_zero_does_not_pass(self):
        for rel in ("orch.jsonl", "branch.jsonl", "orch/subagents/agent-a1.jsonl", "orch/subagents/agent-a2.jsonl"):
            self.blank(rel)
        open(self.otel, "w").close()
        led = ledger.read_run(self.run_dir)
        r = meter.reconcile(led, [], {"main": ["repl_main_thread"], "subagent": ["agent"]}, pct=1.0)
        self.assertFalse(r["pass"])
        self.assertEqual(r["failures"][0]["cause"], "empty")
        p = self.cli("reconcile", self.run_dir, "--otel", self.otel)
        self.assertNotEqual(p.returncode, 0)


class StatesWhatItAssumes(Case):
    def test_otel_only_rows_carry_their_creation_tokens_and_a_one_hour_upper_bound(self):
        p = self.cost()
        row = json.loads(p.stdout)["otel_only"]["auto_mode_classifier"]
        self.assertEqual(row["tokens"]["creation"], 40)
        self.assertEqual(D(row["usd"]), D("0.000320"))
        self.assertEqual(D(row["usd_if_creation_was_1h"]), D("0.000380"))

    def test_unsplit_writes_are_named_as_an_underprice_for_the_defaults_cell(self):
        u = {"input_tokens": 1, "cache_creation_input_tokens": 70, "cache_read_input_tokens": 0, "output_tokens": 1}
        rec = {"type": "assistant", "isSidechain": False, "version": "2.1.283", "timestamp": "2026-10-15T10:30:00Z",
               "requestId": "r", "message": {"id": "m_unsplit", "model": "claude-opus-5-5", "content": [], "usage": u}}
        self.append("branch.jsonl", json.dumps(rec) + "\n")
        with open(self.otel, "a") as f:
            f.write(json.dumps({"event": "api_request", "query_source": "repl_main_thread", "model": "claude-opus-5-5",
                                "input_tokens": 1, "output_tokens": 1, "cache_read_tokens": 0,
                                "cache_creation_tokens": 70}) + "\n")
        p = self.cost()
        self.assertEqual(p.returncode, 0, p.stderr)
        notes = json.loads(p.stdout)["assumptions"]
        self.assertEqual(notes["unsplit_write_tokens"], 70)
        self.assertIn("5-minute", notes["unsplit_writes_priced_at"])
        self.assertIn("underprice", notes["unsplit_writes_note"])


if __name__ == "__main__":
    unittest.main()
