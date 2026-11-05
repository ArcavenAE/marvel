#!/usr/bin/env python3
"""Tests for apply_patch.py's guards on synthetic files (brief section 3).

The real edits are pinned to the rc.25 files, which are third-party and not in
this tree, so these tests swap the module's pins and edits for a two-file
fixture and check each guard on its own: an anchor that matches no line, an
anchor that matches two, a patched result that is not the expected bytes, and
the pass case. Every failure must exit before any file is written, and say
which guard fired.

Run: python3 -I -B _kos/probes/vsdd-probe-t6/test_apply_patch.py
"""
import hashlib
import importlib.util
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent


def load():
    spec = importlib.util.spec_from_file_location("apply_patch", HERE / "apply_patch.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def sha(data):
    return hashlib.sha256(data).hexdigest()


class Guards(unittest.TestCase):
    def setUp(self):
        self.m = load()
        self.root = Path(tempfile.mkdtemp())
        (self.root / "agents/orchestrator").mkdir(parents=True)
        self.orch = b"alpha\nanchor-one\nomega\n"
        self.psd = b"p1\np2\n"
        self.write(self.orch, self.psd)
        self.m.EDITS = [
            (self.m.ORCH, "startswith", "anchor-", "append", " +x"),
            (self.m.PSD, "equals", "p2", "after", ["p3"]),
        ]
        self.want_orch = b"alpha\nanchor-one +x\nomega\n"
        self.want_psd = b"p1\np2\np3\n"
        self.pin(self.orch, self.psd, self.want_orch, self.want_psd)

    def write(self, orch, psd):
        (self.root / self.m.ORCH).write_bytes(orch)
        (self.root / self.m.PSD).write_bytes(psd)

    def pin(self, before_o, before_p, after_o, after_p):
        self.m.BEFORE = {self.m.ORCH: sha(before_o), self.m.PSD: sha(before_p)}
        self.m.AFTER = {self.m.ORCH: sha(after_o), self.m.PSD: sha(after_p)}

    def refuses(self, text):
        before = ((self.root / self.m.ORCH).read_bytes(), (self.root / self.m.PSD).read_bytes())
        with self.assertRaises(SystemExit) as cm:
            self.m.apply(self.root)
        self.assertIn(text, str(cm.exception.code))
        after = ((self.root / self.m.ORCH).read_bytes(), (self.root / self.m.PSD).read_bytes())
        self.assertEqual(before, after, "a refused run wrote a file")

    def test_pass_writes_the_expected_bytes(self):
        got = self.m.apply(self.root)
        self.assertEqual((self.root / self.m.ORCH).read_bytes(), self.want_orch)
        self.assertEqual((self.root / self.m.PSD).read_bytes(), self.want_psd)
        self.assertEqual(got[self.m.ORCH], sha(self.want_orch))

    def test_an_anchor_that_matches_no_line_is_refused(self):
        self.m.EDITS[0] = (self.m.ORCH, "startswith", "no-such-anchor", "append", " +x")
        self.refuses("matched 0 lines")

    def test_an_anchor_that_matches_two_lines_is_refused(self):
        two = b"alpha\nanchor-one\nanchor-two\nomega\n"
        self.write(two, self.psd)
        self.pin(two, self.psd, self.want_orch, self.want_psd)
        self.refuses("matched 2 lines")

    def test_a_result_that_is_not_the_expected_bytes_is_refused(self):
        self.pin(self.orch, self.psd, b"something else\n", self.want_psd)
        self.refuses("patched sha256")

    def test_a_target_that_is_not_the_rc25_file_is_refused(self):
        self.write(b"changed\n", self.psd)
        self.refuses("is not the rc.25 file")


if __name__ == "__main__":
    unittest.main()
