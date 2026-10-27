package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// Section 4 of the reexec credential handoff (aae-orc-4h9xp): after the exec
// the CLI reads the supervisor's leaf reading and says what it measured. A
// reexec that leaves a leafed cluster off the hub exits nonzero.

var (
	epoch    = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	oldStart = epoch.Add(-time.Hour)
	newStart = epoch.Add(5 * time.Second)
)

// world is a fake daemon and clock: time moves only when the check sleeps, and
// the daemon answers according to what elapsed.
type world struct {
	now       time.Time
	polls     int
	restarted func(elapsed time.Duration) bool
	leaf      func(elapsed time.Duration, now time.Time) bus.Status
	leafErr   func(elapsed time.Duration) error // the bus.status request fails, as an error response does
	startErr  func(elapsed time.Duration) error
}

func (w *world) observer() reexecObserver {
	elapsed := func() time.Duration { return w.now.Sub(epoch) }
	return reexecObserver{
		startedAt: func() (time.Time, error) {
			if w.startErr != nil {
				if err := w.startErr(elapsed()); err != nil {
					return time.Time{}, err
				}
			}
			if w.restarted(elapsed()) {
				return newStart, nil
			}
			return oldStart, nil
		},
		leaf: func() (bus.Status, error) {
			w.polls++
			if w.leafErr != nil {
				if err := w.leafErr(elapsed()); err != nil {
					return bus.Status{}, err
				}
			}
			return w.leaf(elapsed(), w.now), nil
		},
		now:   func() time.Time { return w.now },
		sleep: func(d time.Duration) { w.now = w.now.Add(d) },
		wait:  90 * time.Second,
		every: 2 * time.Second,
	}
}

func newWorld() *world {
	return &world{now: epoch, restarted: func(time.Duration) bool { return true }}
}

func validUp(now time.Time) bus.Status {
	return bus.Status{
		Managed: true, Ready: true, Leaf: "up", LeafSource: "store",
		LeafObservedAt: now.Format(time.RFC3339), LeafValidUntil: now.Add(time.Minute).Format(time.RFC3339),
	}
}

func run(t *testing.T, w *world, handoff string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := checkAfterReexec(&out, oldStart, handoff, w.observer())
	return out.String(), err
}

func TestPostCheckReportsTheMeasuredLeafAndNotAClaim(t *testing.T) {
	w := newWorld()
	w.leaf = func(_ time.Duration, now time.Time) bus.Status { return validUp(now) }
	out, err := run(t, w, "carried")
	if err != nil {
		t.Fatalf("a leaf that is up failed the check: %v", err)
	}
	if !strings.Contains(out, "hub leaf up") {
		t.Errorf("output does not say the leaf is up:\n%s", out)
	}
	if strings.Contains(out, "agents keep running") {
		t.Errorf("output still carries the unmeasured claim:\n%s", out)
	}
}

func TestPostCheckPassesWithNoHubAndSaysThereWasNothingToCheck(t *testing.T) {
	w := newWorld()
	w.leaf = func(time.Duration, time.Time) bus.Status { return bus.Status{Managed: true, Leaf: "n/a"} }
	out, err := run(t, w, "none")
	if err != nil || !strings.Contains(out, "no hub") {
		t.Errorf("err = %v, output %q; want a pass that says no hub is configured", err, out)
	}
}

// noBusReply is the text a daemon answers bus.status with when its cluster has
// no bus, as the live observer turns an error response into an error. It is
// written out here so the test fails if the daemon's wording and the check's
// recognition drift apart (marvel#1102).
const noBusReply = "no bus is configured for this cluster; add a bus section to its entry in the client config"

func TestPostCheckPassesOnAClusterWithNoBusAndSaysThereWasNothingToCheck(t *testing.T) {
	w := newWorld()
	w.leafErr = func(time.Duration) error { return errors.New(noBusReply) }
	out, err := run(t, w, "none")
	if err != nil {
		t.Fatalf("a cluster with no bus failed the check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no hub is configured, so there is no leaf to check") {
		t.Errorf("output does not say there was nothing to check:\n%s", out)
	}
	if strings.Contains(out, "marvel credential put") {
		t.Errorf("output tells an operator with no bus to push a leaf credential:\n%s", out)
	}
	if elapsed := w.now.Sub(epoch); elapsed > 4*time.Second {
		t.Errorf("waited %s for a cluster that has nothing to wait for", elapsed)
	}
}

func TestPostCheckDoesNotReadTheOldDaemonsNoBusAnswerAsTheSuccessors(t *testing.T) {
	w := newWorld()
	w.restarted = func(el time.Duration) bool { return el >= 10*time.Second }
	w.leafErr = func(time.Duration) error { return errors.New(noBusReply) }
	if out, err := run(t, w, "none"); err != nil {
		t.Fatalf("a cluster with no bus failed the check: %v\n%s", err, out)
	}
	if elapsed := w.now.Sub(epoch); elapsed < 10*time.Second {
		t.Errorf("passed after %s, before the successor answered", elapsed)
	}
}

func TestPostCheckStillWaitsAndFailsWhenTheDaemonAnswersWithAnotherError(t *testing.T) {
	w := newWorld()
	w.leafErr = func(time.Duration) error { return errors.New("the daemon is busy") }
	out, err := run(t, w, "carried")
	if err == nil {
		t.Fatal("a daemon whose bus.status keeps failing passed the check")
	}
	if elapsed := w.now.Sub(epoch); elapsed < 90*time.Second {
		t.Errorf("gave up after %s, before the 90s bound", elapsed)
	}
	for _, want := range []string{"the daemon did not answer", "marvel credential put bus/leaf --value-file <seed-file>"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestPostCheckStillFailsForAManagedBusWhoseLeafStaysDown(t *testing.T) {
	w := newWorld()
	w.leaf = func(_ time.Duration, now time.Time) bus.Status {
		return bus.Status{Managed: true, Leaf: "down", LeafSource: "store", LeafValidUntil: now.Add(time.Minute).Format(time.RFC3339)}
	}
	out, err := run(t, w, "carried")
	if err == nil {
		t.Fatal("a leaf that stayed down passed the check")
	}
	if !strings.Contains(out, "hub leaf is not up") || !strings.Contains(out, "down") {
		t.Errorf("output does not name the down leaf:\n%s", out)
	}
	if elapsed := w.now.Sub(epoch); elapsed < 90*time.Second {
		t.Errorf("gave up after %s, before the 90s bound", elapsed)
	}
}

func TestPostCheckWaitsForALeafThatComesUpLate(t *testing.T) {
	w := newWorld()
	w.leaf = func(el time.Duration, now time.Time) bus.Status {
		if el < 20*time.Second {
			return bus.Status{Managed: true, Leaf: "down", LeafSource: "store", LeafValidUntil: now.Add(time.Minute).Format(time.RFC3339)}
		}
		return validUp(now)
	}
	if out, err := run(t, w, "carried"); err != nil {
		t.Fatalf("a leaf that came up after 20s failed: %v\n%s", err, out)
	}
}

func TestPostCheckFailsAtOnceWhenTheLeafWasDroppedAndNamesTheCommand(t *testing.T) {
	w := newWorld()
	w.leaf = func(time.Duration, time.Time) bus.Status {
		return bus.Status{Managed: true, Leaf: "unenrolled", LeafSource: "dropped-on-adopt"}
	}
	out, err := run(t, w, "carried")
	if err == nil {
		t.Fatal("a dropped leaf passed the check")
	}
	if w.polls > 2 {
		t.Errorf("polled %d times for a state that cannot recover by waiting", w.polls)
	}
	for _, want := range []string{"dropped on adopt", "marvel credential put bus/leaf --value-file <seed-file>", "waited"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestPostCheckSaysWhenTheHandoffWasDeclinedOnItsOwnLine(t *testing.T) {
	cases := map[string]string{
		"declined": "not carried: target cannot read the handoff",
		"over-cap": "not carried: payload over cap",
		"failed":   "not carried: handoff failed",
	}
	for handoff, want := range cases {
		w := newWorld()
		w.leaf = func(time.Duration, time.Time) bus.Status {
			return bus.Status{Managed: true, Leaf: "unenrolled", LeafSource: "dropped-on-adopt"}
		}
		out, err := run(t, w, handoff)
		if err == nil {
			t.Fatalf("%s: a dropped leaf passed", handoff)
		}
		var own bool
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, want) {
				own = !strings.Contains(line, "marvel credential put")
			}
		}
		if !own {
			t.Errorf("%s: no line of its own says %q:\n%s", handoff, want, out)
		}
	}
}

func TestPostCheckIsBoundedWhenTheSuccessorNeverReports(t *testing.T) {
	w := newWorld()
	w.restarted = func(time.Duration) bool { return false } // the old daemon never goes away
	w.leaf = func(time.Duration, time.Time) bus.Status {
		t.Error("a reading was taken from the daemon that has not restarted")
		return bus.Status{}
	}
	out, err := run(t, w, "carried")
	if err == nil {
		t.Fatal("a successor that never reported passed")
	}
	if waited := w.now.Sub(epoch); waited < 90*time.Second || waited > 100*time.Second {
		t.Errorf("waited %s, want the 90s bound", waited)
	}
	for _, want := range []string{"90s", "marvel credential put bus/leaf"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestPostCheckDoesNotTrustTheOldDaemonAnswerBeforeItRestarts(t *testing.T) {
	w := newWorld()
	w.restarted = func(el time.Duration) bool { return el >= 6*time.Second }
	w.leaf = func(el time.Duration, now time.Time) bus.Status {
		if el < 6*time.Second {
			t.Errorf("read the leaf at %s from the daemon that had not restarted yet", el)
		}
		return validUp(now)
	}
	if out, err := run(t, w, "carried"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestPostCheckReadsAStaleUpAsUnknownAndFailsAtTheBound(t *testing.T) {
	w := newWorld()
	w.leaf = func(_ time.Duration, now time.Time) bus.Status {
		st := validUp(now)
		st.LeafValidUntil = now.Add(-time.Second).Format(time.RFC3339)
		return st
	}
	out, err := run(t, w, "carried")
	if err == nil || !strings.Contains(out, "unknown") {
		t.Errorf("err = %v, output %q; want a failure that says unknown", err, out)
	}
}

func TestPostCheckSurvivesTheDaemonNotAnsweringWhileItExecs(t *testing.T) {
	w := newWorld()
	w.startErr = func(el time.Duration) error {
		if el < 4*time.Second {
			return errors.New("connection refused")
		}
		return nil
	}
	w.leaf = func(_ time.Duration, now time.Time) bus.Status { return validUp(now) }
	if out, err := run(t, w, "carried"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestTheHandoffOutcomeIsReadFromTheReexecResponse(t *testing.T) {
	read := func(body string) string {
		return reexecHandoffOutcome(&daemonrpc.Response{Result: json.RawMessage(body)})
	}
	if got := read(`{"status":"reexec","binary":"x","handoff":"declined"}`); got != "declined" {
		t.Errorf("handoff = %q, want declined", got)
	}
	if got := read(`{"status":"reexec","binary":"x"}`); got != "" {
		t.Errorf("an older daemon with no field read as %q, want empty", got)
	}
	if got := read(`not json`); got != "" {
		t.Errorf("garbage read as %q, want empty", got)
	}
}

func TestAnOlderDaemonThatReportsNoHandoffIsNamedInTheOutput(t *testing.T) {
	w := newWorld()
	w.leaf = func(time.Duration, time.Time) bus.Status {
		return bus.Status{Managed: true, Leaf: "unenrolled", LeafSource: "dropped-on-adopt"}
	}
	out, err := run(t, w, "")
	if err == nil {
		t.Fatal("a dropped leaf passed")
	}
	if !strings.Contains(out, "did not report") {
		t.Errorf("an older daemon gave no handoff field; the output should say so:\n%s", out)
	}
}
