"""Canary hash scanner (protocol section 4a). Stub: the test comes first, so every function
here returns an empty answer and the test fails on its assertions until the scanner exists."""
import sys


def scan_bytes(data, targets):
    return [], 0


def scan_tree(root, targets):
    return {"hits": [], "files": 0, "bytes": 0, "windows": 0}


def scan_git(repo, targets):
    return {"hits": [], "objects": 0, "bytes": 0, "windows": 0}


def main(argv):
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
