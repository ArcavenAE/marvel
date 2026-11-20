# vsdd probe T4: canary hash scanner

Probe-local script for the canary search in protocol section 4a (run plan step
9b). Ticket aae-orc-8jkl7. Standard library Python only; it reads and writes
nothing.

| file | job |
|---|---|
| `canary_scan.py` | takes the sha256 of one or more canary tokens, hashes every 32-character window of every hex run of 32 or more characters under the roots it is given, and reports each match by file or object and offset, with the windows scanned and the bytes read. No content is printed. |
| `test_canary_scan.py` | the fixture test. Every token in it is made up. |

The scanner is given hashes, never tokens. Roots are directory trees or files
(`--tree`: transcripts, hook and OTEL logs, a working tree) and git
repositories (`--git`: every object reachable from every ref). The check is
exact: a token embedded in a longer hex run is found, and a token split by a
line break or changed by one character is not.

## Run

```sh
python3 -I -B -m unittest discover -s _kos/probes/vsdd-probe-t4 -p 'test_*.py'
python3 -I -B _kos/probes/vsdd-probe-t4/canary_scan.py --hash SHA256 --tree RUN_DIR --git REPO
```

Exit 0 means no hit, 1 a hit, 2 a usage error.
