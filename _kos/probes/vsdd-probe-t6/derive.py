#!/usr/bin/env python3
"""Helpers for build-pack.sh: the version stamp and the manifest check.

  derive.py stamp PACK_YAML NEW_VERSION
      Rewrites the one line "version: 1.0.0-rc.25" and asserts nothing else
      in the file changed. Exits nonzero if the line is missing or repeated.
  derive.py check PACK_DIR MANIFEST_CSV
      Compares every file under PACK_DIR with the rc.25 file manifest
      (lines of sha256,size,path). Prints one line per changed or added path
      with its sha256. Exits 1 if the changed set is not exactly ALLOWED_CHANGED,
      the added set is not exactly ALLOWED_ADDED, or any manifest path is missing.

The allowed sets follow brief section 3 as amended by the architect's ruling of
2026-11-05 (pack.yaml carries the derived version, so it is a third changed
path). Standard library only; no network.
"""
import hashlib
import sys
from pathlib import Path

BASE_LINE = "version: 1.0.0-rc.25"
ALLOWED_CHANGED = {
    "agents/orchestrator/orchestrator.md",
    "agents/orchestrator/per-story-delivery.md",
    "pack.yaml",
}
ALLOWED_ADDED = {"bin/shim"}


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def stamp(pack_yaml, new_version):
    p = Path(pack_yaml)
    lines = p.read_bytes().decode("utf-8").split("\n")
    hits = [i for i, ln in enumerate(lines) if ln == BASE_LINE]
    if len(hits) != 1:
        sys.exit(f"derive: {pack_yaml}: {BASE_LINE!r} matched {len(hits)} lines, want 1")
    new = list(lines)
    new[hits[0]] = f"version: {new_version}"
    changed = [i for i, (a, b) in enumerate(zip(lines, new)) if a != b]
    if changed != [hits[0]] or len(lines) != len(new):
        sys.exit("derive: stamp changed more than the version line")
    p.write_bytes("\n".join(new).encode("utf-8"))


def read_manifest(path):
    rows = {}
    for ln in Path(path).read_text(encoding="utf-8").splitlines():
        if not ln:
            continue
        digest, _size, rel = ln.split(",", 2)
        rows[rel] = digest
    return rows


def check(pack_dir, manifest):
    root = Path(pack_dir)
    want = read_manifest(manifest)
    have = {}
    for f in sorted(root.rglob("*")):
        if f.is_file() or f.is_symlink():
            have[str(f.relative_to(root))] = sha256(f)
    missing = sorted(set(want) - set(have))
    added = sorted(set(have) - set(want))
    changed = sorted(r for r in set(want) & set(have) if want[r] != have[r])
    problems = []
    for r in missing:
        problems.append(f"missing {r}")
    for r in added:
        if r not in ALLOWED_ADDED:
            problems.append(f"unexpected-add {r}")
    for r in changed:
        if r not in ALLOWED_CHANGED:
            problems.append(f"unexpected-change {r}")
    for r in sorted(ALLOWED_CHANGED - set(changed)):
        problems.append(f"expected-change-absent {r}")
    for r in sorted(ALLOWED_ADDED - set(added)):
        problems.append(f"expected-add-absent {r}")
    for r in changed:
        print(f"changed {have[r]}  {r}")
    for r in added:
        print(f"added {have[r]}  {r}")
    print(f"identical {len(want) - len(missing) - len(changed)} of {len(want)} rc.25 paths")
    for p in problems:
        print(p)
    return 1 if problems else 0


def main(argv):
    if len(argv) == 4 and argv[1] == "stamp":
        stamp(argv[2], argv[3])
    elif len(argv) == 4 and argv[1] == "check":
        sys.exit(check(argv[2], argv[3]))
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main(sys.argv)
