# Pattern sets

`<harness>/<harness version>/<pattern id>.yaml`, each beside the sample it was
built from (`<pattern id>.sample.txt`). A pattern set is added only from a
captured sample (docs/design/harness-state-watchdog-p1.md section 4). One
ships today: `claude/2.1.290/logged-out`, captured under probe P-WD1, which
covers claude 2.1.285 to 2.1.293 through its `version_range` (inclusive, compared
by dotted numeric parts, and `harness_version` must lie inside it). A
range is added for versions that were captured and read the same; the
shipped range also spans 2.1.286, which was not available to capture and is
covered on the strength of its neighbours. A version outside the range reads
`unknown`, and the watchdog reports it uncovered. A part of a version with a
leading zero is not a version.

A pattern may carry `state: parked` and a `reason` (trust, permission, update or
other) for a harness waiting at a prompt that needs a person; with no `state` it
is logged-out. A pattern whose sampled version is not known is kept under
`internal/panestate/testdata-parked` and does not ship until a version is read
from a seat that shows the prompt.
