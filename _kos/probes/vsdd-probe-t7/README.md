# vsdd probe T7: the series comparison script

Probe-local script for the vsdd-factory marvel-team probe
(`../probe-vsdd-factory-as-marvel-team.md`, section 9.4). Nothing here is part
of marvel's build, and nothing here is critic code. Standard library Python
only; no network; the script writes nothing. It sits beside the ledger reader
and cost meter in `../vsdd-probe-t2/`, and reads per-run totals, not
transcripts.

The brief asks for the script to be written and frozen before M1, so the
analysis is pre-registered. Every number in `example/` and in the tests is
synthetic.

| file | job |
|---|---|
| `compare.py` | estimates arm B over arm A per axis, gives each axis a Holm-adjusted verdict, and reports the real coverage of its intervals at seven pairs beside the verdict |
| `test_compare.py` | tests on synthetic run data: both totals zero, one total zero, a widest wave under 2, hand-counted estimate, Holm, coverage |
| `example/series.json` | a synthetic seven-pair series showing the input shape |

## Run

```sh
python3 -I -B -m unittest discover -s _kos/probes/vsdd-probe-t7 -p 'test_*.py'
python3 -I -B _kos/probes/vsdd-probe-t7/compare.py INPUT.json          # JSON
python3 -I -B _kos/probes/vsdd-probe-t7/compare.py INPUT.json --text   # a short table
```

Exit 0 is a comparison. Exit 4 is a refusal: nothing on stdout, and
`refused: <cause>: <detail>` on stderr. The causes are `unreadable_input`,
`bad_input`, `bad_alpha`, `bad_resamples`, `bad_coverage`, `too_few_pairs`, `unpaired_block`,
`duplicate_block`, `no_axes`, `bad_axis`, `no_smallest_effect`,
`missing_field`, `bad_number` and `negative_number`.

## Input

```json
{
  "alpha": 0.05,
  "seed": 1,
  "resamples": 10000,
  "coverage": {"sims": 1000, "seed": 7, "sigma_block": 0.2, "sigma_run": 0.2, "rho": 0.5, "resamples": 500},
  "axes": {
    "cost": {"num": "cost_usd", "den": "accepted", "smallest_effect": 0.2, "blind": false,
             "sensitivities": [
               {"name": "hub_wait_removed", "num": "cost_usd", "minus": "hub_wait_usd", "den": "accepted"},
               {"name": "first_attempt_only", "num": "cost_usd", "den": "accepted_first_attempt"}]}
  },
  "pairs": [{"block": 1, "A": {"widest_wave": 3, "cost_usd": 95.0, "accepted": 7}, "B": {"...": "..."}}]
}
```

- **Each pair is a block** with one arm A run and one arm B run. Every run
  carries `widest_wave` and the fields its axes name.
- **An axis** is a ratio of sums over the series: the `num` field over the
  `den` field (less the `minus` field, where named). With no `den`, the
  denominator is one per run, so the ratio is the per-run mean. A run with a
  zero denominator still counts in the sums.
- **`smallest_effect` is an input** for every axis (0.2 for cost, as ruled).
  The protocol file states the others at M1.
- **`blind` is an input** for every axis, decided from the pilot (section
  8.5). The script does not compute it.
- **More axes** (per phase, per wave width) are more entries under `axes`,
  each with `"family": false`. An axis with `family: false` gets an estimate
  and an interval at alpha and no verdict, as the sensitivities do, and it
  stays out of the Holm family, so a breakdown never weakens the cost verdict.
  `family` defaults to true. The protocol file names the family at M1; the
  recommended family is the section 9.3 axes, one entry each.

## What it reports

For an axis with an estimate:

- the ratio B over A, its standard error on the log scale, and its p value;
- the delta method interval (a t interval on the log of the ratio of sums,
  blocked by pair) and a percentile bootstrap over pairs as a sensitivity;
- the Holm family size, rank and adjusted p (not for a `family: false` axis);
- the verdict: **(c)** when the Holm step-down rejects, with
  `past_smallest_effect` true when the near end of the interval is at or past
  `ln(1 + smallest_effect)`; **(d)** otherwise, with `pairs_to_resolve`;
  **(b)** for a blind axis;
- `coverage_at_7_pairs`: the simulated coverage of the delta and bootstrap
  intervals at the level that axis was tested at, with its model.

The zero-total cases follow 9.4. If both arms' denominator totals are zero,
the axis reports the totals and closes **(a)**. If one is zero, no ratio is
computed, the axis reports both arms' totals and closes **(c)** against the
arm with the zero. A series in which any run's widest wave is under 2 closes
**(a)** with the widths listed, and never writes (d); each axis keeps its
estimate and carries `verdict_before_series_close`.

Sensitivities (`hub_wait_removed` and `first_attempt_only` beside cost) get an
estimate and an interval and no verdict. The series `close` reads (a) for the
wave case or a both-zero cost axis, and (b) for a blind cost axis.

## Readings 9.4 leaves open

9.4 names the delta method, the bootstrap, Holm and the coverage check and
leaves their operation to the script. These are the readings used, as the
architect ruled them on 2026-11-05. They are fixed when the script is frozen
at M1.

1. **The critical value is Student t with k - 1 degrees of freedom**, as 8.5's
   blind stop already uses t at 6 degrees of freedom for seven pairs. The
   delta method's variance is `k / (k - 1)` times the sum of squared pair
   contributions.
2. **The Holm family is the axes that have an estimate, less any axis marked
   `family: false`.** Blind axes and axes with a zero denominator total or a
   zero numerator total sit out too. A rejected axis ranked j of m is
   shown at level `alpha / (m - j + 1)`. Axes at or after the first one not
   rejected are shown at the level of that first one, so an interval never
   excludes 1 where the verdict is (d).
3. **"Past the smallest effect" is symmetric in the log:** the near end of the
   interval is compared with `ln(1 + smallest_effect)`, for a gap in either
   direction, as 8.5's blind stop does. In ratio terms, at a smallest effect of
   0.2, B over A is past it when it is at least 1.2 or at most 1/1.2, about
   0.833.
4. **"The n that would resolve it"** is the least number of pairs at which the
   interval's half-width falls to `ln(1 + smallest_effect)`, scaling the
   observed standard error by `sqrt(k / n)`. It is reported, never adopted
   (8.2). It prints `>1000` where no such n exists below 1000.
5. **The coverage model is synthetic.** Each pair draws a block factor shared
   by both arms and a run factor per run, lognormal, on both numerator and
   denominator, with arm B's numerator scaled by `exp(true_log_ratio)`
   (default 0, so the interval should cover a ratio of 1). The numerator and
   denominator factors are correlated at `rho` on both levels, because a run
   that accepts more stories spends more. The defaults (`sigma_block` 0.2,
   `sigma_run` 0.2, `rho` 0.5) are placeholders. Pre-registered rule: after the
   pilot, `sigma_block`, `sigma_run` and `rho` are set from the pilot's
   measured log spreads and correlation, and the protocol file records the
   values. That changes an input, not the frozen script. The figure is for
   seven pairs whatever the series length. One limit: the lognormal model has
   no zero-accepted runs, so the coverage figure says nothing about the
   zero-total cases, which take 9.4's own rules.
6. **A series with a run whose widest wave is under 2 closes (a) for every
   axis**, not only where (d) would have been written.
7. **A numerator total of zero** (a count axis with nothing counted in one
   arm) has no log ratio. The axis reports `numerator_zero`, no verdict, and
   sits outside the family. It carries each arm's total and the per-pair counts
   (`per_pair`), so the finding can describe it, for example limit stops 0
   against 2. 9.4's one-zero rule speaks of the cost denominator and is not
   extended to numerators.

Not covered: the 1% OTEL reconciliation, which needs a real run and waits on
its host card, and the fidelity and drift stops, which are run-time checks and
not analysis.
