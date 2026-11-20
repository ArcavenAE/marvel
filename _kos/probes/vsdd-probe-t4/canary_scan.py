"""Canary hash scanner (protocol section 4a, run plan step 9b).

Takes the sha256 of one or more canary tokens, never the tokens. Reads every
maximal run of 32 or more hex characters in the roots it is given, lowercases
it, hashes every 32-character window with sha256 and compares each with the
targets. A token embedded in a longer hex run is found; a token split by a line
break or changed by one character is not (the check is exact).

Roots: a directory tree or a file (--tree), and a git repository (--git), where
every object reachable from every ref is read (`git rev-list --all --objects`
into `git cat-file --batch`). It reads only. The report gives each hit's file
or object and offset, the windows scanned and the bytes read. No content is
printed. Exit 0 for no hit, 1 for a hit, 2 for a usage error.

Standard library only. Usage:
  python3 -I -B canary_scan.py --hash SHA256 [--hash SHA256 ...] [--tree PATH ...] [--git REPO ...]
"""
import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile

RUN = re.compile(rb"[0-9a-fA-F]{32,}")
CHUNK = 16 * 1024 * 1024
HEXCHARS = b"0123456789abcdefABCDEF"
HASH = re.compile(r"[0-9a-f]{64}")


def _scan_chunks(chunks, targets):
    """Scan an iterable of byte chunks as one stream. A hex run that touches the end of a
    chunk is carried into the next, so a run is never cut at a chunk boundary.
    Returns (hits, windows, nbytes); a hit is (offset, target hash)."""
    hits, windows, nbytes, carry, base = [], 0, 0, b"", 0
    for chunk in chunks:
        nbytes += len(chunk)
        data, start = carry + chunk, base
        # hold back the trailing hex characters of any length: the next chunk may extend them into a run
        keep = len(data) - len(data.rstrip(HEXCHARS))
        carry, base = data[len(data) - keep:], start + len(data) - keep
        for m in RUN.finditer(data[:len(data) - keep]):
            _window(m.group().lower(), start + m.start(), targets, hits)
            windows += len(m.group()) - 31
    for m in RUN.finditer(carry):
        _window(m.group().lower(), base + m.start(), targets, hits)
        windows += len(m.group()) - 31
    return sorted(hits), windows, nbytes


def _window(run, offset, targets, hits):
    for i in range(len(run) - 31):
        d = hashlib.sha256(run[i:i + 32]).hexdigest()
        if d in targets:
            hits.append((offset + i, d))


def scan_bytes(data, targets):
    hits, windows, _ = _scan_chunks([data], targets)
    return hits, windows


def _file_chunks(path):
    with open(path, "rb") as f:
        while True:
            b = f.read(CHUNK)
            if not b:
                return
            yield b


def scan_tree(root, targets):
    rep = {"hits": [], "files": 0, "bytes": 0, "windows": 0, "unreadable": []}
    paths = [root] if os.path.isfile(root) else [os.path.join(d, n) for d, _, ns in os.walk(root) for n in sorted(ns)]
    for p in paths:
        if os.path.islink(p) or not os.path.isfile(p):
            continue
        try:
            hits, windows, nbytes = _scan_chunks(_file_chunks(p), targets)
        except OSError:
            rep["unreadable"].append(p)
            continue
        rep["files"] += 1
        rep["bytes"] += nbytes
        rep["windows"] += windows
        rep["hits"] += [{"where": p, "offset": o, "target": t[:12]} for o, t in hits]
    return rep


def _object_chunks(pipe, size):
    left = size
    while left:
        b = pipe.read(min(left, CHUNK))
        if not b:
            raise OSError("git cat-file ended inside an object")
        left -= len(b)
        yield b


def scan_git(repo, targets):
    rep = {"hits": [], "objects": 0, "bytes": 0, "windows": 0, "missing": 0}
    rl = subprocess.run(["git", "-C", repo, "rev-list", "--all", "--objects"], capture_output=True, check=True)
    names = {}
    for line in rl.stdout.decode("utf-8", "replace").splitlines():
        sha, _, path = line.partition(" ")
        names[sha] = path
    with tempfile.TemporaryFile() as lst:
        lst.write(("\n".join(names) + "\n").encode() if names else b"")
        lst.seek(0)
        p = subprocess.Popen(["git", "-C", repo, "cat-file", "--batch"], stdin=lst, stdout=subprocess.PIPE)
        try:
            while True:
                head = p.stdout.readline()
                if not head:
                    break
                parts = head.split()
                if len(parts) == 2 and parts[1] == b"missing":
                    rep["missing"] += 1
                    continue
                sha, size = parts[0].decode(), int(parts[2])
                hits, windows, nbytes = _scan_chunks(_object_chunks(p.stdout, size), targets)
                p.stdout.read(1)  # the newline git prints after each object
                rep["objects"] += 1
                rep["bytes"] += nbytes
                rep["windows"] += windows
                where = "object " + sha + (" " + names[sha] if names.get(sha) else "")
                rep["hits"] += [{"where": where, "offset": o, "target": t[:12]} for o, t in hits]
        finally:
            p.stdout.close()
            if p.wait() != 0:
                raise OSError("git cat-file failed")
    return rep


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--hash", action="append", default=[], help="sha256 of a token (64 hex); repeatable")
    ap.add_argument("--tree", action="append", default=[], help="a directory or file to scan; repeatable")
    ap.add_argument("--git", action="append", default=[], help="a git repository; repeatable")
    try:
        a = ap.parse_args(argv)
    except SystemExit:
        return 2
    targets = {x.lower() for x in a.hash}
    if not targets or not all(HASH.fullmatch(x) for x in targets) or not (a.tree or a.git):
        print("canary_scan: need at least one valid --hash (64 hex) and at least one --tree or --git", file=sys.stderr)
        return 2
    reports = [dict(root=r, kind="tree", **scan_tree(r, targets)) for r in a.tree]
    reports += [dict(root=r, kind="git", **scan_git(r, targets)) for r in a.git]
    total = sum(len(r["hits"]) for r in reports)
    print(json.dumps({"targets": sorted(t[:12] for t in targets), "hit_count": total, "roots": reports}, indent=2))
    return 1 if total else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
