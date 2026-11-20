"""Fixture test for the canary hash scanner (protocol section 4a, run plan step 9b).

Every token here is made up for the test. The scanner is given only the sha256
of a token, so the tests build the hash from a fixture token and never hold a
real canary or the preimage of any real hash.
"""
import hashlib
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import canary_scan  # noqa: E402

TOKEN = "0123456789abcdef0123456789abcdef"  # 32 hex, made up for this test
OTHER = "fedcba9876543210fedcba9876543210"  # a second made-up token
assert len(TOKEN) == 32 and len(OTHER) == 32


def h(s):
    return hashlib.sha256(s.encode("ascii")).hexdigest()


class ScanBytes(unittest.TestCase):
    def test_planted_token_is_found_with_its_offset(self):
        data = ("lorem ipsum " + TOKEN + " dolor").encode()
        hits, windows = canary_scan.scan_bytes(data, {h(TOKEN)})
        self.assertEqual([(off, t) for off, t in hits], [(12, h(TOKEN))])
        self.assertEqual(windows, 1)

    def test_a_31_character_run_is_not_found(self):
        control, _ = canary_scan.scan_bytes(("x " + TOKEN + " y").encode(), {h(TOKEN)})
        self.assertEqual(len(control), 1)  # positive control: the full token is found
        data = ("x " + TOKEN[:31] + " y").encode()
        hits, windows = canary_scan.scan_bytes(data, {h(TOKEN)})
        self.assertEqual(hits, [])
        self.assertEqual(windows, 0)

    def test_a_one_character_off_token_is_not_found(self):
        off_by_one = TOKEN[:-1] + ("0" if TOKEN[-1] != "0" else "1")
        hits, windows = canary_scan.scan_bytes(("a " + off_by_one + " b").encode(), {h(TOKEN)})
        self.assertEqual(hits, [])
        self.assertEqual(windows, 1)

    def test_a_token_inside_a_longer_hex_run_is_found(self):
        run = "ab" * 5 + TOKEN + "cd" * 7  # 10 + 32 + 14 = 56 hex characters
        hits, windows = canary_scan.scan_bytes(("k=" + run + "\n").encode(), {h(TOKEN)})
        self.assertEqual([off for off, _ in hits], [2 + 10])
        self.assertEqual(windows, 56 - 32 + 1)

    def test_uppercase_hex_is_lowercased_before_hashing(self):
        hits, _ = canary_scan.scan_bytes(("z " + TOKEN.upper() + " z").encode(), {h(TOKEN)})
        self.assertEqual(len(hits), 1)

    def test_a_token_split_by_a_newline_is_not_found(self):
        control, _ = canary_scan.scan_bytes((TOKEN + "\n").encode(), {h(TOKEN)})
        self.assertEqual(len(control), 1)  # positive control: the unsplit token is found
        data = (TOKEN[:16] + "\n" + TOKEN[16:]).encode()
        hits, _ = canary_scan.scan_bytes(data, {h(TOKEN)})
        self.assertEqual(hits, [])

    def test_two_targets_are_reported_by_their_own_hash(self):
        data = (TOKEN + " " + OTHER).encode()
        hits, windows = canary_scan.scan_bytes(data, {h(TOKEN), h(OTHER)})
        self.assertEqual(sorted((off, t) for off, t in hits), sorted([(0, h(TOKEN)), (33, h(OTHER))]))
        self.assertEqual(windows, 2)


class ScanFiles(unittest.TestCase):
    def test_a_tree_is_walked_and_the_report_names_file_and_offset_and_prints_no_content(self):
        with tempfile.TemporaryDirectory() as d:
            os.makedirs(os.path.join(d, "sub", "agents"))
            with open(os.path.join(d, "sub", "agents", "a.jsonl"), "w") as f:
                f.write('{"t": "' + TOKEN + '"}\n')
            with open(os.path.join(d, "clean.txt"), "w") as f:
                f.write("nothing here\n")
            rep = canary_scan.scan_tree(d, {h(TOKEN)})
            self.assertEqual(len(rep["hits"]), 1)
            self.assertTrue(rep["hits"][0]["where"].endswith(os.path.join("sub", "agents", "a.jsonl")))
            self.assertEqual(rep["hits"][0]["offset"], 8)
            self.assertEqual(rep["files"], 2)
            self.assertGreater(rep["bytes"], 0)
            self.assertNotIn(TOKEN, json.dumps(rep))


class ScanGit(unittest.TestCase):
    def git(self, repo, *args, stdin=None):
        env = {"PATH": os.environ.get("PATH", ""), "HOME": repo, "GIT_CONFIG_NOSYSTEM": "1"}
        r = subprocess.run(["git", "-C", repo] + list(args), input=stdin, capture_output=True, env=env, check=True)
        return r.stdout.decode().strip()

    def test_an_object_reachable_only_from_a_non_head_ref_is_found(self):
        with tempfile.TemporaryDirectory() as d:
            self.git(d, "init", "-q")
            blob = self.git(d, "hash-object", "-w", "--stdin", stdin=("note " + TOKEN + "\n").encode())
            # a ref straight to a blob: no commit, so no signing is involved
            self.git(d, "update-ref", "refs/factory-artifacts/notes", blob)
            rep = canary_scan.scan_git(d, {h(TOKEN)})
            self.assertEqual(len(rep["hits"]), 1)
            self.assertIn(blob, rep["hits"][0]["where"])
            self.assertEqual(rep["hits"][0]["offset"], 5)
            self.assertNotIn(TOKEN, json.dumps(rep))

    def test_a_clean_repo_has_no_hit_and_counts_its_windows(self):
        with tempfile.TemporaryDirectory() as d:
            self.git(d, "init", "-q")
            blob = self.git(d, "hash-object", "-w", "--stdin", stdin=b"no token here\n")
            self.git(d, "update-ref", "refs/factory-artifacts/clean", blob)
            rep = canary_scan.scan_git(d, {h(TOKEN)})
            self.assertEqual(rep["hits"], [])
            self.assertEqual(rep["objects"], 1)
            self.assertEqual(rep["bytes"], len(b"no token here\n"))


class Cli(unittest.TestCase):
    def run_cli(self, *args):
        return subprocess.run([sys.executable, "-I", "-B", os.path.join(HERE, "canary_scan.py")] + list(args),
                              capture_output=True)

    def test_exit_1_on_a_hit_0_on_none_and_2_on_a_bad_hash(self):
        with tempfile.TemporaryDirectory() as d:
            with open(os.path.join(d, "f.txt"), "w") as f:
                f.write(TOKEN + "\n")
            hit = self.run_cli("--hash", h(TOKEN), "--tree", d)
            self.assertEqual(hit.returncode, 1)
            self.assertNotIn(TOKEN.encode(), hit.stdout + hit.stderr)
            none = self.run_cli("--hash", h(OTHER), "--tree", d)
            self.assertEqual(none.returncode, 0)
            bad = self.run_cli("--hash", "not-a-hash", "--tree", d)
            self.assertEqual(bad.returncode, 2)
            nothing = self.run_cli("--hash", h(TOKEN))
            self.assertEqual(nothing.returncode, 2)


if __name__ == "__main__":
    unittest.main()
