# finding-047: the bus/daemon pre-push race suite flakes when an ephemeral port leaves no room for the +4000 monitoring offset

**Date:** 2026-09-17
**Probe:** incidental, while pushing a docs-only branch through the pre-push
`test-race` hook (marvel @ c7ab41c).
**Subject:** marvel test harness (`internal/bus`, `internal/daemon` supervisor
tests).
**Confidence:** observed, reproduced on unmodified `origin/main`.

## Symptom

`git push` runs the lefthook `test-race` pre-push hook (`go test -race ./...`).
On some runs `internal/bus` fails a batch of supervisor tests instantly (0.00s)
and `internal/daemon` fails `TestLeafCredentialPutRestartsTheSupervisedBroker`,
so the push is refused. Other runs pass. The failing assertion is explicit
(`internal/bus/supervisor_test.go:193`):

```
render nats-server.conf: listen "127.0.0.1:64269": port cannot carry the monitoring offset of 4000
```

## Mechanism

The test harness binds `nats-server` on an OS-assigned ephemeral port, then
derives the monitoring port as `port + 4000`. macOS hands out ephemeral ports
from 49152 to 65535 SEQUENTIALLY (an incrementing OS-wide counter, not a random
pick), so any assigned port above 61535 makes `port + 4000` exceed 65535 and the
config renderer fails closed with the message above. The flake is therefore not
per-bind random: it tracks where the machine's ephemeral counter currently sits.
When the counter is in the top 4000 of the range, every bind in the run overflows
and all twelve supervisor tests fail deterministically (observed 12 of 12 across
four consecutive runs, all `monitoring offset of 4000`); when the counter is
below 61535 the whole suite passes. The counter wraps back to 49152, so the same
suite that fails now passes later without any code change. This is why an earlier
push in the same session succeeded on retry and later pushes fail: the OS port
counter moved into the high band in between.

This is a test-helper bug, not a product bug: the `+4000` guard is deliberate
and correct; the port PICKER is what lacks headroom. It has nothing to do with
whether `nats-server` is installed (it is, at `/opt/homebrew/bin/nats-server`).

## Impact

The pre-push hook is a real gate and must not be bypassed. But a flaky
infrastructure test blocks legitimate pushes (including docs-only branches that
cannot regress any Go test), and each hook run costs about 30s, so a blocked
push turns into repeated retries waiting for the ports to land in range. This is
ambient-infrastructure friction, captured here per the tooling-friction rule
before working around it by retry.

## Fix candidates (not applied here)

- The port picker reserves headroom: pick a base port from a range capped at
  61535 (so `base + 4000 <= 65535`), or bind the monitoring port independently
  from its own ephemeral allocation rather than deriving it by a fixed offset.
- Failing that, retry the bind inside the helper when the derived monitoring
  port would overflow, instead of failing the test.

Filed as a local finding. Escalation to a GH issue on the marvel repo or a bd
ticket is the operator's call under the three-layer defect rubric.
