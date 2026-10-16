"""Fixture test for the cache ledger (brief section 10, validation 1).

The totals below were counted by hand from fixtures/run, not computed by
the reader. fixtures/README.md lists every record and its place in the count.
"""
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import ledger  # noqa: E402

RUN = os.path.join(HERE, "fixtures", "run")


def cls(i, w5, w1, rd, out):
    return {"input": i, "write_5m": w5, "write_1h": w1, "read": rd, "output": out}


class LedgerFixture(unittest.TestCase):
    @classmethod
    def setUpClass(cls_):
        cls_.out = ledger.read_run(RUN)

    def test_run_totals_read_back_exactly(self):
        self.assertEqual(self.out["totals"], cls(27, 4050, 100, 6100, 235))
        self.assertEqual(self.out["requests"], 13)

    def test_streaming_copy_is_dropped_by_message_id(self):
        orch = self.out["by_spawn"]["orch"]
        self.assertEqual(orch["requests"], 6)
        self.assertEqual(orch["tokens"], cls(14, 2350, 0, 3400, 90))
        self.assertEqual(self.out["dedupe"]["duplicates_dropped"], 1)
        self.assertEqual(self.out["dedupe"]["conflicting_duplicates"], 0)

    def test_subagents_are_read_and_joined_to_the_seat(self):
        subs = self.out["by_spawn"]
        a1 = subs["orch/agent-a1"]
        a2 = subs["orch/agent-a2"]
        self.assertEqual(a1["tokens"], cls(6, 400, 0, 1300, 80))
        self.assertEqual(a2["tokens"], cls(2, 900, 0, 900, 25))
        self.assertEqual(a1["role"], "vsdd-implementer")
        self.assertEqual(a1["seat"], "orch")

    def test_by_seat_by_role_by_phase(self):
        seat = self.out["by_seat"]
        # a seat's total carries its own subagents: orch main 6 requests + a1 + a2
        self.assertEqual(seat["orch"]["tokens"], cls(22, 3650, 0, 5600, 195))
        self.assertEqual(seat["orch"]["requests"], 10)
        self.assertEqual(seat["r1-c1-w1-g1-a1"]["tokens"], cls(5, 400, 100, 500, 40))
        role = self.out["by_role"]
        self.assertEqual(role["vsdd-orchestrator"]["tokens"], cls(14, 2350, 0, 3400, 90))
        self.assertEqual(role["vsdd-implementer"]["tokens"], cls(13, 1700, 100, 2700, 145))
        self.assertEqual(role["vsdd-implementer"]["requests"], 7)
        phase = self.out["by_phase"]
        self.assertEqual(phase["p3"]["tokens"], cls(20, 2900, 100, 6100, 205))
        self.assertEqual(phase["p3"]["requests"], 11)
        self.assertEqual(phase["p4"]["tokens"], cls(7, 1150, 0, 0, 30))
        self.assertEqual(phase["p4"]["requests"], 2)

    def test_ttl_as_applied_lists_each_one_hour_write(self):
        ttl = self.out["ttl"]
        self.assertEqual(ttl["write_total"], 4150)
        self.assertEqual(ttl["write_1h"], 100)
        self.assertAlmostEqual(ttl["share_1h"], 100 / 4150)
        self.assertFalse(ttl["pass"])  # the pin wants under 0.1%
        self.assertEqual([r["message_id"] for r in ttl["one_hour_records"]], ["m_b1"])

    def test_first_request_read_against_the_sharing_ceiling(self):
        first = self.out["first_request"]
        self.assertEqual(first["orch"]["read"], 0)
        self.assertEqual(first["orch/agent-a1"]["read"], 500)
        self.assertAlmostEqual(first["orch/agent-a1"]["fraction_of_ceiling"], 0.5)
        self.assertEqual(first["orch/agent-a2"]["read"], 0)
        self.assertEqual(first["orch/agent-a2"]["fraction_of_ceiling"], 0.0)

    def test_cold_starts(self):
        self.assertEqual(sorted(self.out["cold_starts"]), ["m_b1", "m_o1", "m_o5", "m_t1"])

    def test_both_cache_classes_zero_is_undetermined_never_cold(self):
        self.assertEqual(sorted(self.out["undetermined"]), ["m_b3", "m_o6"])
        self.assertNotIn("m_o6", self.out["cold_starts"])
        self.assertNotIn("m_b3", self.out["cold_starts"])

    def test_cache_death_needs_the_gap_and_half_the_prior_prompt(self):
        deaths = self.out["cache_deaths"]
        self.assertEqual([d["message_id"] for d in deaths], ["m_o5"])
        self.assertEqual(deaths[0]["gap_seconds"], 1080.0)
        self.assertEqual(deaths[0]["ttl_seconds"], 300)

    def test_concurrent_cold_write_is_a_sibling_started_before_the_first_response(self):
        self.assertEqual(self.out["concurrent_cold_writes"], ["orch/agent-a2"])

    def test_hub_wait_rows(self):
        hub = self.out["hub_wait"]
        win = hub["waves"]["c1-w1"]
        self.assertEqual(win["requests"], 3)
        self.assertEqual(win["tokens"], cls(4, 200, 0, 3400, 50))
        self.assertEqual(hub["await_only_turns"]["requests"], 2)
        self.assertEqual(hub["await_only_turns"]["tokens"], cls(2, 0, 0, 2400, 10))
        self.assertEqual(hub["union"]["requests"], 3)
        self.assertEqual(hub["union"]["tokens"], cls(4, 200, 0, 3400, 50))

    def test_a_supervisor_with_no_transcript_is_unmeasured_not_zero(self):
        sup = self.out["by_seat"]["sup"]
        self.assertEqual(sup["status"], "unmeasured")
        self.assertIsNone(sup["tokens"])
        self.assertEqual(self.out["unmeasured_seats"], ["sup"])

    def test_versions_are_read_back_against_the_pin(self):
        self.assertEqual(self.out["versions"], {"2.1.283": 13})
        self.assertEqual(self.out["version_mismatch"], [])


class RunBuilder:
    """Writes a one-seat run into a temp directory."""

    def write_run(self, d, records, extra=None):
        with open(os.path.join(d, "t.jsonl"), "w") as f:
            for r in records:
                f.write(json.dumps(r) + "\n")
        run = {"seats": [{"name": "s", "kind": "orchestrator", "role": "r", "transcript": "t.jsonl"}]}
        run.update(extra or {})
        with open(os.path.join(d, "run.json"), "w") as f:
            json.dump(run, f)

    def rec(self, mid, ts, usage, version="2.1.283"):
        return {"type": "assistant", "isSidechain": False, "version": version, "timestamp": ts,
                "requestId": "r" + mid,
                "message": {"id": mid, "model": "claude-opus-5-5", "content": [], "usage": usage}}


class LedgerEdges(RunBuilder, unittest.TestCase):
    def test_duplicates_with_different_usage_are_counted_not_hidden(self):
        with tempfile.TemporaryDirectory() as d:
            u1 = {"input_tokens": 1, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 5, "output_tokens": 1}
            u2 = dict(u1, output_tokens=9)
            self.write_run(d, [self.rec("m1", "2026-10-15T10:00:00Z", u1),
                               self.rec("m1", "2026-10-15T10:00:01Z", u2)])
            out = ledger.read_run(d)
            self.assertEqual(out["requests"], 1)
            self.assertEqual(out["dedupe"]["conflicting_duplicates"], 1)

    def test_a_write_with_no_split_is_flagged_and_kept_as_5m(self):
        with tempfile.TemporaryDirectory() as d:
            u = {"input_tokens": 1, "cache_creation_input_tokens": 70, "cache_read_input_tokens": 0, "output_tokens": 1}
            self.write_run(d, [self.rec("m1", "2026-10-15T10:00:00Z", u)])
            out = ledger.read_run(d)
            self.assertEqual(out["totals"]["write_5m"], 70)
            self.assertEqual(out["unsplit_writes"], ["m1"])

    def test_a_split_that_does_not_add_up_is_flagged(self):
        with tempfile.TemporaryDirectory() as d:
            u = {"input_tokens": 1, "cache_creation_input_tokens": 100, "cache_read_input_tokens": 0, "output_tokens": 1,
                 "cache_creation": {"ephemeral_5m_input_tokens": 30, "ephemeral_1h_input_tokens": 20}}
            self.write_run(d, [self.rec("m1", "2026-10-15T10:00:00Z", u)])
            out = ledger.read_run(d)
            self.assertEqual(out["unsplit_writes"], ["m1"])
            self.assertEqual((out["totals"]["write_5m"], out["totals"]["write_1h"]), (80, 20))

    def test_a_version_off_the_pin_is_reported(self):
        with tempfile.TemporaryDirectory() as d:
            u = {"input_tokens": 1, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 5, "output_tokens": 1,
                 "cache_creation": {"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": 0}}
            self.write_run(d, [self.rec("m1", "2026-10-15T10:00:00Z", u, version="2.1.999")],
                           {"pinned_version": "2.1.283"})
            out = ledger.read_run(d)
            self.assertEqual(out["version_mismatch"], ["s"])

    def test_a_missing_transcript_file_is_an_error_not_a_zero(self):
        with tempfile.TemporaryDirectory() as d:
            self.write_run(d, [])
            os.remove(os.path.join(d, "t.jsonl"))
            with self.assertRaises(FileNotFoundError):
                ledger.read_run(d)


class LedgerBoundaries(RunBuilder, unittest.TestCase):
    """Each case sits on one edge of a rule, so a rule that slips by one reads wrong."""

    def usage(self, w5=0, rd=0, i=1, out=1):
        return {"input_tokens": i, "cache_creation_input_tokens": w5, "cache_read_input_tokens": rd,
                "output_tokens": out, "cache_creation": {"ephemeral_5m_input_tokens": w5, "ephemeral_1h_input_tokens": 0}}

    def test_a_long_gap_with_a_small_write_is_not_a_death(self):
        with tempfile.TemporaryDirectory() as d:
            self.write_run(d, [self.rec("m1", "2026-10-15T10:00:00Z", self.usage(w5=100, rd=1000)),
                               self.rec("m2", "2026-10-15T10:20:00Z", self.usage(w5=100, rd=0))])
            self.assertEqual(ledger.read_run(d)["cache_deaths"], [])

    def test_a_write_of_exactly_half_the_prior_prompt_is_a_death(self):
        with tempfile.TemporaryDirectory() as d:
            # prior prompt = 1 + 0 + 999 = 1000 (input + read + write); the write is 500
            self.write_run(d, [self.rec("m1", "2026-10-15T10:00:00Z", self.usage(w5=100, rd=899)),
                               self.rec("m2", "2026-10-15T10:20:00Z", self.usage(w5=500, rd=0))])
            self.assertEqual([x["message_id"] for x in ledger.read_run(d)["cache_deaths"]], ["m2"])

    def test_a_big_write_inside_the_ttl_is_not_a_death(self):
        with tempfile.TemporaryDirectory() as d:
            self.write_run(d, [self.rec("m1", "2026-10-15T10:00:00Z", self.usage(w5=100, rd=900)),
                               self.rec("m2", "2026-10-15T10:04:59Z", self.usage(w5=900, rd=0))])
            self.assertEqual(ledger.read_run(d)["cache_deaths"], [])

    def test_a_request_at_the_phase_end_belongs_to_the_next_phase(self):
        with tempfile.TemporaryDirectory() as d:
            self.write_run(d, [self.rec("m1", "2026-10-15T10:10:00Z", self.usage(rd=5))],
                           {"phases": [{"phase": "a", "from": "2026-10-15T10:00:00Z", "to": "2026-10-15T10:10:00Z"},
                                       {"phase": "b", "from": "2026-10-15T10:10:00Z", "to": "2026-10-15T10:20:00Z"}]})
            self.assertEqual(list(ledger.read_run(d)["by_phase"]), ["b"])

    def test_a_wave_window_includes_both_of_its_ends(self):
        with tempfile.TemporaryDirectory() as d:
            self.write_run(d, [self.rec("m1", "2026-10-15T10:00:00Z", self.usage(rd=5)),
                               self.rec("m2", "2026-10-15T10:01:00Z", self.usage(rd=5))],
                           {"waves": [{"wave": "w", "first_dispatch": "2026-10-15T10:00:00Z",
                                       "last_terminal": "2026-10-15T10:01:00Z"}]})
            self.assertEqual(ledger.read_run(d)["hub_wait"]["waves"]["w"]["requests"], 2)

    def test_an_error_row_that_wrote_is_not_a_cold_start(self):
        with tempfile.TemporaryDirectory() as d:
            bad = self.rec("m1", "2026-10-15T10:00:00Z", self.usage(w5=50))
            bad["isApiErrorMessage"] = True
            self.write_run(d, [bad])
            out = ledger.read_run(d)
            self.assertEqual(out["cold_starts"], [])
            self.assertEqual(out["undetermined"], [])

    def test_a_turn_with_await_and_another_tool_is_not_await_only(self):
        with tempfile.TemporaryDirectory() as d:
            mixed = self.rec("m1", "2026-10-15T10:00:00Z", self.usage(rd=5))
            mixed["message"]["content"] = [{"type": "tool_use", "name": "await", "input": {}},
                                           {"type": "tool_use", "name": "Read", "input": {}}]
            self.write_run(d, [mixed], {"await_tools": ["await"]})
            self.assertEqual(ledger.read_run(d)["hub_wait"]["await_only_turns"]["requests"], 0)

    def test_a_bash_call_matching_the_await_pattern_counts_as_await(self):
        with tempfile.TemporaryDirectory() as d:
            sh = self.rec("m1", "2026-10-15T10:00:00Z", self.usage(rd=5))
            sh["message"]["content"] = [{"type": "tool_use", "name": "Bash", "input": {"command": "shim await key-1"}}]
            self.write_run(d, [sh], {"await_pattern": r"shim await\b"})
            self.assertEqual(ledger.read_run(d)["hub_wait"]["await_only_turns"]["requests"], 1)

    def test_a_warm_spawn_that_starts_inside_a_siblings_first_request_is_not_a_cold_write(self):
        with tempfile.TemporaryDirectory() as d:
            os.makedirs(os.path.join(d, "t", "subagents"))
            for stem, start, resp, rd, w in (("agent-a", "10:00:00", "10:00:10", 500, 10), ("agent-b", "10:00:05", "10:00:12", 500, 10)):
                recs = [{"type": "user", "timestamp": "2026-10-15T%sZ" % start, "isSidechain": True},
                        self.rec("m_" + stem, "2026-10-15T%sZ" % resp, self.usage(w5=w, rd=rd))]
                with open(os.path.join(d, "t", "subagents", stem + ".jsonl"), "w") as f:
                    f.write("".join(json.dumps(r) + "\n" for r in recs))
                with open(os.path.join(d, "t", "subagents", stem + ".meta.json"), "w") as f:
                    json.dump({"agentType": "x"}, f)
            self.write_run(d, [])
            self.assertEqual(ledger.read_run(d)["concurrent_cold_writes"], [])


class LedgerCli(unittest.TestCase):
    def test_cli_prints_the_same_json(self):
        p = subprocess.run([sys.executable, "-I", "-B", os.path.join(HERE, "ledger.py"), RUN],
                           capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(json.loads(p.stdout)["totals"], cls(27, 4050, 100, 6100, 235))


if __name__ == "__main__":
    unittest.main()
