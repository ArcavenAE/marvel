# vsdd probe T6: arm B's derived pack, the dispatch shim and their checks

Probe-local scripts for the vsdd-factory marvel-team probe
(`../probe-vsdd-factory-as-marvel-team.md`, sections 3, 4 and 5.1). Ticket
aae-orc-sbp9j. Nothing here is part of marvel's build, nothing is published,
and no sideshow-packs release is made. The derived pack is built locally from
the cosign-verified rc.25 tarball and installed with `--no-activate` after
rc.25 (section 5.1). Standard library Python and bash only; no network except
what `cosign verify-blob` needs.

| file | job |
|---|---|
| `build-pack.sh` | `build`: checks the tarball and manifest against pinned sha256 values, runs `cosign verify-blob`, unpacks, applies the patch, stamps the version, adds the shim, then runs the manifest check and writes `report.txt`. `check`: the manifest check alone |
| `apply_patch.py` | the patch: the section 3 edits to `agents/orchestrator/orchestrator.md` and `agents/orchestrator/per-story-delivery.md` |
| `derive.py` | `stamp` (the version line) and `check` (the manifest comparison) |
| `shim` | `dispatch` and `await` over the T1 surface (`marvel run --experimental`, `marvel wait --experimental`); installed in the pack as `bin/shim` |
| `test_shim.sh`, `test_build.sh` | the unit tests; `testdata/fake-marvel` is the fake marvel the shim tests run against |

## Run

```sh
bash _kos/probes/vsdd-probe-t6/test_shim.sh
bash _kos/probes/vsdd-probe-t6/test_build.sh
_kos/probes/vsdd-probe-t6/build-pack.sh build TARBALL TARBALL.bundle file-manifest.csv OUTDIR 1
```

`TARBALL` and `file-manifest.csv` are the rc.25 release assets of
`vsdd-factory-v1.0.0-rc.25` (tarball sha256 `6d38a905...cee1`, manifest sha256
`e0ec70a1...325c`). `OUTDIR` must not exist. The last argument is the arm-b
counter: the derived version is `1.0.0-rc.25.arm-b.<N>`, and each rebuild of
the patch bumps it.

## The patch

A unified diff would carry rc.25 lines into this tree, so the patch is an edit
script. Each edit is anchored on the leading text of one line and must match
exactly one line. The script asserts each target's rc.25 sha256 before it
edits and the expected sha256 after, and writes nothing if either check or any
anchor fails. The script's own sha256 is the patch hash the protocol file
records.

## What changes in the pack

The architect's ruling of 2026-11-05 amends section 3: sideshow reads a pack's
version from `pack.yaml`, so the derived pack must change that file's version
line. The allowed set, enforced by `derive.py check`, is:

- changed: `agents/orchestrator/orchestrator.md`, `agents/orchestrator/per-story-delivery.md`, and `pack.yaml` (the version line only; `stamp` asserts nothing else in the file moved);
- added: `bin/shim`;
- every other rc.25 path byte-identical, and none missing.

`.claude-plugin/plugin.json` is untouched, so the harness sees the same plugin
version in both arms. Arm identity rests on the S1 and S4 readlink checks.
`check` prints each changed and added path with its sha256.

## The shim

`shim dispatch <key> <prompt-file>` runs `marvel run --experimental --mode
headless` with the workspace, team `arm-b-<run-id>`, role `branch-run`, the key
as `--name`, `PROBE_BRANCH_KEY=<key>` in the seat's environment, and `claude
--agent vsdd-orchestrator --model ... --permission-mode ...` after `--`. It
refuses when `PROBE_BRANCH_KEY` is already set. An "already exists" reply counts
as dispatched; any other error prints `refused` with marvel's first line.

`shim await <key>...` runs one `marvel wait --experimental` per key,
concurrently, and prints `<key> <word> <harness session id>`. The word comes
from the exit code and the state `wait` prints, which must agree:

| exit | state printed | word |
|---|---|---|
| 0 | succeeded | succeeded |
| 1 | failed | failed |
| 2 | crashed | crashed |
| 4 | unknown | unknown |
| 3, 5, anything else, or no match | | pending |

A wait still running at the overall limit (115 s by default) is killed and its
key reads pending. Exit 5 means the daemon could not be reached or answered
with an error, so the seat's state is not known and it reads pending. The
shim never retries and never reports a story done.

## Not here

Live tests 1 and 2 (section 4) run on the probe host and wait on the host
ruling (section 0.1, R3). The protocol file records the pack hash, the patch
hash and the arm-b counter in use; this directory does not write it.
