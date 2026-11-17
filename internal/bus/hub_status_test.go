package bus

import (
	"strings"
	"testing"
	"time"
)

// The hub's leaf list is a diagnostic. These lines are readings, not alerts:
// the cluster-gone alert stays with director (aae-orc-stqn9). The names are
// invented.

var hubNow = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

const hubValid = 90 * time.Second

func joined(lines []string) string { return strings.Join(lines, "\n") }

func lineWith(lines []string, sub string) (string, bool) {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return l, true
		}
	}
	return "", false
}

func TestHubStatusNamesMissingExpectedLeafAsDiagnostic(t *testing.T) {
	t.Parallel()
	seen := hubNow.Add(-10 * time.Second)
	lines := HubStatusLines(HubReading{ObservedAt: seen, Leafs: []string{"alpha", "charlie"}},
		[]string{"alpha", "bravo", "charlie"}, "alpha", hubNow, hubValid)
	if len(lines) == 0 {
		t.Fatal("no lines for a fresh reading with an expected leaf missing")
	}
	miss, ok := lineWith(lines, "bravo")
	if !ok || !strings.Contains(strings.ToLower(miss), "missing") {
		t.Errorf("no line names bravo as missing:\n%s", joined(lines))
	}
	for _, l := range lines {
		if !strings.Contains(l, "not an alert") {
			t.Errorf("line %q is not labelled \"not an alert\"", l)
		}
		if !strings.Contains(l, seen.Format(time.RFC3339)) {
			t.Errorf("line %q lacks its reading time %s", l, seen.Format(time.RFC3339))
		}
		if strings.Contains(l, "not observed") {
			t.Errorf("a fresh reading printed %q", l)
		}
	}
	if _, ok := lineWith(lines, "partition"); ok {
		t.Errorf("one leaf missing printed a partition line:\n%s", joined(lines))
	}
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l), "missing") && (strings.Contains(l, "alpha") || strings.Contains(l, "charlie")) {
			t.Errorf("a present leaf shares a line with \"missing\": %q", l)
		}
	}
}

// Control: every expected leaf present names none missing and no partition.
func TestHubStatusAllPresentNamesNoneMissing(t *testing.T) {
	t.Parallel()
	lines := HubStatusLines(HubReading{ObservedAt: hubNow.Add(-time.Second), Leafs: []string{"alpha", "bravo"}},
		[]string{"alpha", "bravo"}, "alpha", hubNow, hubValid)
	if len(lines) == 0 {
		t.Fatal("no lines for a fresh reading")
	}
	all := strings.ToLower(joined(lines))
	if strings.Contains(all, "missing") || strings.Contains(all, "partition") || strings.Contains(all, "not observed") {
		t.Errorf("a complete reading printed a problem:\n%s", joined(lines))
	}
	if !strings.Contains(all, "alpha") || !strings.Contains(all, "bravo") {
		t.Errorf("the present leafs are not named:\n%s", joined(lines))
	}
}

func TestHubStatusPartitionLineWhenAllRemoteMissing(t *testing.T) {
	t.Parallel()
	seen := hubNow.Add(-5 * time.Second)
	lines := HubStatusLines(HubReading{ObservedAt: seen, Leafs: []string{"alpha"}},
		[]string{"alpha", "bravo", "charlie"}, "alpha", hubNow, hubValid)
	part, ok := lineWith(lines, "partition")
	if !ok {
		t.Fatalf("no partition line when every remote leaf is missing and the local one is present:\n%s", joined(lines))
	}
	if !strings.Contains(part, "not an alert") || !strings.Contains(part, seen.Format(time.RFC3339)) {
		t.Errorf("partition line %q lacks its label or reading time", part)
	}
	for _, n := range []string{"bravo", "charlie"} {
		if !strings.Contains(joined(lines), n) {
			t.Errorf("remote leaf %s is not named:\n%s", n, joined(lines))
		}
	}
}

// Control: with the observing cluster's own leaf also missing, the reading is
// not the partition case.
func TestHubStatusNoPartitionLineWhenTheLocalLeafIsMissingToo(t *testing.T) {
	t.Parallel()
	lines := HubStatusLines(HubReading{ObservedAt: hubNow.Add(-time.Second), Leafs: nil},
		[]string{"alpha", "bravo"}, "alpha", hubNow, hubValid)
	if _, ok := lineWith(lines, "partition"); ok {
		t.Errorf("a partition line with the local leaf absent:\n%s", joined(lines))
	}
}

func TestHubStatusStaleReadingIsNotObserved(t *testing.T) {
	t.Parallel()
	for name, r := range map[string]HubReading{
		"older than the window": {ObservedAt: hubNow.Add(-10 * time.Minute), Leafs: []string{"alpha"}},
		"never read":            {},
		"timed out":             {ObservedAt: hubNow.Add(-time.Second), Leafs: []string{"alpha"}, Err: "timeout"},
		"failed":                {Err: "connection refused"},
	} {
		lines := HubStatusLines(r, []string{"alpha", "bravo"}, "alpha", hubNow, hubValid)
		if len(lines) == 0 {
			t.Errorf("%s: no line, want a \"not observed\" line", name)
			continue
		}
		all := strings.ToLower(joined(lines))
		if !strings.Contains(all, "not observed") {
			t.Errorf("%s: lines do not say \"not observed\":\n%s", name, joined(lines))
		}
		if !strings.Contains(all, "not an alert") {
			t.Errorf("%s: lines are not labelled \"not an alert\":\n%s", name, joined(lines))
		}
		if strings.Contains(all, "missing") || strings.Contains(all, "partition") || strings.Contains(all, "down") {
			t.Errorf("%s: an unobserved hub was read as a finding:\n%s", name, joined(lines))
		}
	}
}

// A reading exactly at the window's edge is still observed; one a second past
// it is not.
func TestHubStatusWindowEdge(t *testing.T) {
	t.Parallel()
	at := func(age time.Duration) string {
		return strings.ToLower(joined(HubStatusLines(HubReading{ObservedAt: hubNow.Add(-age), Leafs: []string{"alpha"}},
			[]string{"alpha"}, "alpha", hubNow, hubValid)))
	}
	if strings.Contains(at(hubValid), "not observed") {
		t.Error("a reading exactly at the window edge printed \"not observed\"")
	}
	if !strings.Contains(at(hubValid+time.Second), "not observed") {
		t.Error("a reading a second past the window did not print \"not observed\"")
	}
}

// With no expected list declared, no comparison is made and the line says so.
// It never reads as all present.
func TestHubStatusNoExpectedListMakesNoComparison(t *testing.T) {
	t.Parallel()
	for name, expected := range map[string][]string{"absent": nil, "empty": {}} {
		lines := HubStatusLines(HubReading{ObservedAt: hubNow.Add(-time.Second), Leafs: []string{"alpha", "bravo"}},
			expected, "alpha", hubNow, hubValid)
		if len(lines) == 0 {
			t.Errorf("%s: no line, want one saying no comparison is made", name)
			continue
		}
		all := strings.ToLower(joined(lines))
		if !strings.Contains(all, "no comparison") {
			t.Errorf("%s: lines do not say no comparison is made:\n%s", name, joined(lines))
		}
		for _, bad := range []string{"all present", "missing", "partition"} {
			if strings.Contains(all, bad) {
				t.Errorf("%s: lines say %q without an expected list:\n%s", name, bad, joined(lines))
			}
		}
		if !strings.Contains(all, "not an alert") {
			t.Errorf("%s: lines are not labelled \"not an alert\":\n%s", name, joined(lines))
		}
	}
}

// One remote leaf expected and absent, the local one present, is the partition
// case too. Only the local leaf expected has no remote to miss, so it is not.
func TestHubStatusPartitionNeedsAtLeastOneRemoteLeaf(t *testing.T) {
	t.Parallel()
	one := HubStatusLines(HubReading{ObservedAt: hubNow.Add(-time.Second), Leafs: []string{"alpha"}},
		[]string{"alpha", "bravo"}, "alpha", hubNow, hubValid)
	if _, ok := lineWith(one, "partition"); !ok {
		t.Errorf("one remote leaf missing, the local present: no partition line:\n%s", joined(one))
	}
	local := HubStatusLines(HubReading{ObservedAt: hubNow.Add(-time.Second), Leafs: []string{"alpha"}},
		[]string{"alpha"}, "alpha", hubNow, hubValid)
	if _, ok := lineWith(local, "partition"); ok {
		t.Errorf("only the local leaf expected, a partition line printed:\n%s", joined(local))
	}
}

// A reading stamped after now (clock skew) is observed, not stale.
func TestHubStatusReadingFromTheFutureIsNotStale(t *testing.T) {
	t.Parallel()
	lines := HubStatusLines(HubReading{ObservedAt: hubNow.Add(3 * time.Second), Leafs: []string{"alpha"}},
		[]string{"alpha"}, "alpha", hubNow, hubValid)
	if strings.Contains(strings.ToLower(joined(lines)), "not observed") {
		t.Errorf("a reading a few seconds ahead of now read as not observed:\n%s", joined(lines))
	}
}

// A leaf the hub lists that nobody expected is named, and is not missing.
func TestHubStatusNamesAnUnexpectedLeaf(t *testing.T) {
	t.Parallel()
	lines := HubStatusLines(HubReading{ObservedAt: hubNow.Add(-time.Second), Leafs: []string{"alpha", "zulu"}},
		[]string{"alpha"}, "alpha", hubNow, hubValid)
	l, ok := lineWith(lines, "zulu")
	if !ok || !strings.Contains(strings.ToLower(l), "unexpected") {
		t.Errorf("an unexpected leaf is not named as such:\n%s", joined(lines))
	}
	if ok && strings.Contains(strings.ToLower(l), "missing") {
		t.Errorf("an unexpected leaf was named missing: %q", l)
	}
}
