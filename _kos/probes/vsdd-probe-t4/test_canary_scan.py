"""Fixture test for the canary hash scanner (protocol section 4a, run plan step 9b).

Every token here is made up for the test. The scanner is given only the sha256
of a token, so the tests build the hash from a fixture token and never hold a
real canary or the preimage of any real hash.
"""
import contextlib
import hashlib
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

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


class ScanChunks(unittest.TestCase):
    def test_a_token_cut_by_a_chunk_boundary_is_found_at_its_stream_offset(self):
        data = ("pad " + TOKEN + " end").encode()
        for cut in (5, 12, 20, 35):
            hits, windows, nbytes = canary_scan._scan_chunks([data[:cut], data[cut:]], {h(TOKEN)})
            self.assertEqual(hits, [(4, h(TOKEN))], cut)
            self.assertEqual((windows, nbytes), (1, len(data)), cut)


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
            self.assertEqual(rep["hits"][0]["offset"], len('{"t": "'))
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


class Unreadable(unittest.TestCase):
    """A scan that could not read everything must say so (review of 213d2bac): an absent or
    unreadable root is recorded, and it never reads as clean."""

    def test_a_root_that_does_not_exist_is_recorded_as_unreadable(self):
        with tempfile.TemporaryDirectory() as d:
            gone = os.path.join(d, "no-such-dir")
            rep = canary_scan.scan_tree(gone, {h(TOKEN)})
            self.assertEqual(rep["unreadable"], [gone])
            self.assertEqual(rep["files"], 0)

    def test_a_subdirectory_that_cannot_be_listed_is_recorded_as_unreadable(self):
        with tempfile.TemporaryDirectory() as d:
            with open(os.path.join(d, "ok.txt"), "w") as f:
                f.write("clean\n")
            locked = os.path.join(d, "locked")
            os.makedirs(locked)
            os.chmod(locked, 0)
            try:
                rep = canary_scan.scan_tree(d, {h(TOKEN)})
            finally:
                os.chmod(locked, 0o700)
            self.assertEqual(rep["unreadable"], [locked])
            self.assertEqual(rep["files"], 1)


class GitOutcomes(unittest.TestCase):
    """Every --git outcome prints its JSON, and a hit found before a failure still wins (review of ef54b755)."""

    def git(self, repo, *args, stdin=None):
        env = {"PATH": os.environ.get("PATH", ""), "HOME": repo, "GIT_CONFIG_NOSYSTEM": "1"}
        r = subprocess.run(["git", "-C", repo, "-c", "init.defaultBranch=main"] + list(args), input=stdin,
                           capture_output=True, env=env, check=True)
        return r.stdout.decode().strip()

    def run_cli(self, *args):
        return subprocess.run([sys.executable, "-I", "-B", os.path.join(HERE, "canary_scan.py")] + list(args),
                              capture_output=True)

    def test_a_git_scan_with_a_hit_prints_the_hit_and_exits_1(self):
        with tempfile.TemporaryDirectory() as d:
            self.git(d, "init", "-q")
            blob = self.git(d, "hash-object", "-w", "--stdin", stdin=("note " + TOKEN + "\n").encode())
            self.git(d, "update-ref", "refs/factory-artifacts/notes", blob)
            r = self.run_cli("--hash", h(TOKEN), "--git", d)
            self.assertEqual(r.returncode, 1, r.stderr[-300:])
            self.assertIn(b'"hit_count": 1', r.stdout)
            self.assertNotIn(b"Traceback", r.stderr)
            self.assertNotIn(TOKEN.encode(), r.stdout + r.stderr)

    def test_a_clean_git_scan_prints_its_report_and_exits_0(self):
        with tempfile.TemporaryDirectory() as d:
            self.git(d, "init", "-q")
            blob = self.git(d, "hash-object", "-w", "--stdin", stdin=b"no token here\n")
            self.git(d, "update-ref", "refs/factory-artifacts/clean", blob)
            r = self.run_cli("--hash", h(TOKEN), "--git", d)
            self.assertEqual(r.returncode, 0, r.stderr[-300:])
            self.assertIn(b'"unreadable_count": 0', r.stdout)

    def failing_after_a_hit(self, d):
        """A tree holding a-hit (the token) then b-boom (a blob the stub cannot read). The tree is
        listed first, then its blobs in name order, so the hit is read before the failure."""
        self.git(d, "init", "-q")
        a = self.git(d, "hash-object", "-w", "--stdin", stdin=("note " + TOKEN + "\n").encode())
        b = self.git(d, "hash-object", "-w", "--stdin", stdin=b"BOOM\n")
        tree = self.git(d, "mktree", stdin=("100644 blob %s\ta-hit\n100644 blob %s\tb-boom\n" % (a, b)).encode())
        self.git(d, "update-ref", "refs/factory-artifacts/tree", tree)
        real = canary_scan._scan_chunks

        def fake(chunks, targets):
            data = b"".join(chunks)
            if b"BOOM" in data:
                raise OSError("simulated cat-file failure")
            return real([data], targets)
        return fake

    def test_a_failure_after_a_hit_keeps_the_hit_and_marks_the_root_unreadable(self):
        with tempfile.TemporaryDirectory() as d:
            fake = self.failing_after_a_hit(d)
            try:
                with mock.patch.object(canary_scan, "_scan_chunks", fake):
                    rep = canary_scan.scan_git(d, {h(TOKEN)})
            except OSError:  # the old code let the failure escape and the hit with it
                rep = {}
            self.assertEqual(len(rep.get("hits", [])), 1)
            self.assertEqual(rep.get("unreadable"), [d])

    def test_a_failure_after_a_hit_still_exits_1(self):
        with tempfile.TemporaryDirectory() as d:
            fake = self.failing_after_a_hit(d)
            out = io.StringIO()
            try:
                with mock.patch.object(canary_scan, "_scan_chunks", fake), contextlib.redirect_stdout(out):
                    rc = canary_scan.main(["--hash", h(TOKEN), "--git", d])
            except Exception as e:  # the old code raised here
                rc = "raised " + type(e).__name__
            self.assertEqual(rc, 1)
            report = json.loads(out.getvalue() or "{}")
            self.assertEqual(report.get("hit_count"), 1)
            self.assertEqual(report.get("unreadable_count"), 1)


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


    def test_a_readable_tree_reports_no_unreadable_and_exits_0(self):
        with tempfile.TemporaryDirectory() as d:
            with open(os.path.join(d, "ok.txt"), "w") as f:
                f.write("clean\n")
            r = self.run_cli("--hash", h(TOKEN), "--tree", d)
            self.assertEqual(r.returncode, 0)
            self.assertEqual(json.loads(r.stdout).get("unreadable_count"), 0)

    def test_exit_3_when_nothing_was_found_but_something_could_not_be_read(self):
        with tempfile.TemporaryDirectory() as d:
            gone = os.path.join(d, "no-such-dir")
            r = self.run_cli("--hash", h(TOKEN), "--tree", gone)
            self.assertEqual(r.returncode, 3)
            out = json.loads(r.stdout)
            self.assertEqual(out["hit_count"], 0)
            self.assertEqual(out.get("unreadable_count"), 1)
            self.assertEqual(out["roots"][0]["unreadable"], [gone])

    def test_a_hit_keeps_exit_1_even_when_something_else_was_unreadable(self):
        with tempfile.TemporaryDirectory() as d:
            with open(os.path.join(d, "f.txt"), "w") as f:
                f.write(TOKEN + "\n")
            r = self.run_cli("--hash", h(TOKEN), "--tree", d, "--tree", os.path.join(d, "no-such-dir"))
            self.assertEqual(r.returncode, 1)
            self.assertEqual(json.loads(r.stdout).get("unreadable_count"), 1)

    def test_a_git_root_that_is_not_a_repository_takes_exit_3_and_prints_no_traceback(self):
        with tempfile.TemporaryDirectory() as d:
            r = self.run_cli("--hash", h(TOKEN), "--git", d)
            self.assertEqual(r.returncode, 3)
            self.assertNotIn(b"Traceback", r.stderr)
            out = json.loads(r.stdout)
            self.assertEqual(out.get("unreadable_count"), 1)
            self.assertEqual(out["roots"][0]["kind"], "git")


if __name__ == "__main__":
    unittest.main()
