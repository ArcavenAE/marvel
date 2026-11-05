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
| `build-pack.sh` | `report`: the pins, the sha256 of the patch, the gate (`derive.py`) and the shim, then the manifest check. `build`: checks the tarball and manifest against pinned sha256 values, runs `cosign verify-blob`, unpacks, applies the patch, stamps the version, adds the shim, then runs the manifest check and writes `report.txt`. `check`: the manifest check alone |
| `apply_patch.py` | the patch: the section 3 edits to `agents/orchestrator/orchestrator.md` and `agents/orchestrator/per-story-delivery.md` |
| `derive.py` | `stamp` (the version line) and `check` (the manifest comparison) |
| `shim` | `dispatch` and `await` over the T1 surface (`marvel run --experimental`, `marvel wait --experimental`); installed in the pack as `bin/shim` |
| `test_shim.sh`, `test_build.sh`, `test_apply_patch.py` | the unit tests; `testdata/fake-marvel` is the fake marvel the shim tests run against |

## Run

```sh
bash _kos/probes/vsdd-probe-t6/test_shim.sh
bash _kos/probes/vsdd-probe-t6/test_build.sh
python3 -I -B _kos/probes/vsdd-probe-t6/test_apply_patch.py
_kos/probes/vsdd-probe-t6/build-pack.sh build TARBALL TARBALL.bundle file-manifest.csv OUTDIR
```

`TARBALL` and `file-manifest.csv` are the rc.25 release assets of
`vsdd-factory-v1.0.0-rc.25` (tarball sha256 `6d38a905...cee1`, manifest sha256
`e0ec70a1...325c`). `OUTDIR` must not exist. The last argument is the arm-b
counter, default 2: the derived version is `1.0.0-rc.25.arm-b.<N>`. Each change
to the pack's bytes (the patch, the shim, `pack.yaml` or any other path) bumps
it, so one version names one byte set.

## Versions

A version is never reused. `pack file manifest sha256` is the sha256 of the
pack's file listing in the rc.25 manifest format (`derive.py manifest`; run on
the unmodified rc.25 tree it gives the pinned manifest sha256).

| version | status | shim sha256 | pack file manifest sha256 |
|---|---|---|---|
| `1.0.0-rc.25.arm-b.1` | superseded | `f6e073567cde7dd41ea210c1ef073a289e67e5f6045df68ef5b3ea7755d3ccb4` | `ff932bddad37265caac2e8820021ca08fcd6b5a033f1b57d759d7182435803e9` |
| `1.0.0-rc.25.arm-b.2` | current | `3123d3a458b0189f6afb68bb3533161d37bcf76491394d5dd6ff10f2d65142b2` | `ec207f9c7a02874ccee507061576e1934cef231f0bd15ae36953c0851eaef979` |

The patch hash (`apply_patch.py` sha256 `9c372d0c3ba2465ad3b02afedc90355269fa2c523ebecbea1b6c96da7a8b2b6b`)
is the same in both. Nothing under arm-b.1 is invalidated, since no live test
has run. Before live tests 1 and 2, run `sideshow list` on the probe host. If
arm-b.1 is installed there, install arm-b.2 beside it and enable
`@1.0.0-rc.25.arm-b.2` explicitly.

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
