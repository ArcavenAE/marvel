#!/usr/bin/env python3
"""Summarize shift_timing.py output: median, min, max, IQR per stage per
arm, plus the role-count scaling fit. See finding-017.

Reads the JSONL path from $RESULTS (default: the finding's dataset).
Never reports a bare mean: the distribution is tick-quantized, so the
median and the extremes carry the information and an average of two
tick-counts is a value the system cannot produce.
"""

import json
import os
import statistics as st
import sys
from collections import defaultdict

DEFAULT = os.path.join(os.path.dirname(__file__), "..", "_kos", "findings",
                       "finding-017-data-shift-timings.jsonl")
PATH = os.environ.get("RESULTS", DEFAULT)

# arm label prefix -> (description, role count)
ARMS = {
    "A-sim-hb": ("A: 1 role, simulator, heartbeat gate", 1),
    "B-shell-nohc": ("B: 1 role, shell, pane-only gate", 1),
    "C-sim-2role": ("C: 2 roles, simulator, heartbeat gate", 2),
    "E-sim-4role": ("E: 4 roles, simulator, heartbeat gate", 4),
}
STAGES = [
    "t_successor_created",
    "t_successor_running_obs",
    "t_successor_heartbeat_obs",
    "t_predecessor_deleted",
    "t_shift_completed",
]

rows = defaultdict(list)
with open(PATH) as fh:
    for line in fh:
        line = line.strip()
        if line:
            r = json.loads(line)
            rows[r["label"].rsplit("-", 1)[0]].append(r)

if not rows:
    print(f"no rows in {PATH}", file=sys.stderr)
    sys.exit(1)


def stat(vals):
    vals = sorted(v for v in vals if v is not None)
    if not vals:
        return None
    if len(vals) >= 4:
        q = st.quantiles(vals, n=4, method="inclusive")
        iqr = q[2] - q[0]
    else:
        iqr = max(vals) - min(vals)
    return st.median(vals), min(vals), max(vals), iqr, len(vals)


print(f"{'arm / stage':<48} {'n':>2} {'median':>8} {'min':>8} {'max':>8} {'IQR':>7}")
print("-" * 86)
for arm, (desc, _) in ARMS.items():
    rs = rows.get(arm, [])
    if not rs:
        continue
    clean = [r for r in rs if r.get("clean")]
    print(f"{desc}  (clean {len(clean)}/{len(rs)})")
    for stage in STAGES:
        s = stat([r.get(stage) for r in clean])
        if s is None:
            print(f"  {stage:<46} {'-':>2} {'n/a':>8}")
            continue
        med, lo, hi, iqr, n = s
        print(f"  {stage:<46} {n:>2} {med:>8.3f} {lo:>8.3f} {hi:>8.3f} {iqr:>7.3f}")
    print()

print("--- role-count scaling (total shift, median) ---")
pts = []
for arm, (_, nroles) in ARMS.items():
    if arm == "B-shell-nohc":   # different readiness gate; not on this axis
        continue
    s = stat([r.get("t_shift_completed")
              for r in rows.get(arm, []) if r.get("clean")])
    if s:
        pts.append((nroles, s[0]))
        print(f"  {nroles} role(s): {s[0]:.3f} s  (n={s[4]})")

pts.sort()
if len(pts) >= 3:
    # Fit on the extremes, then check the interior point. A fit that
    # interpolates its own endpoints proves nothing; the middle residual
    # is the only part of this that can fail.
    slope = (pts[-1][1] - pts[0][1]) / (pts[-1][0] - pts[0][0])
    intercept = pts[0][1] - slope * pts[0][0]
    print(f"\n  fit (on extremes): t = {intercept:.2f} + {slope:.2f} * roles")
    for n, v in pts:
        pred = intercept + slope * n
        print(f"    roles={n}: predicted {pred:.2f}  measured {v:.2f}  "
              f"residual {pred - v:+.3f} s")
    print(f"\n  reconcile ticks per role: {slope / 2.0:.2f} "
          f"(ReconcileInterval is 2 s)")
    for n in (8, 19):
        print(f"    roles={n}: extrapolated {intercept + slope * n:.1f} s")
    print("  compaction median for comparison: 154 s (finding-016)")
