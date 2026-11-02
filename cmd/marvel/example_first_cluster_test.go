package main

import (
	"os"
	"strings"
	"testing"
)

// The binary carries copies of examples/first-cluster.toml and .yaml so an
// installed marvel prints the file a first user applies. A copy that drifts
// from the file in examples/ would hand that user stale bytes, so the printed
// bytes are compared with the source file. If this fails, recopy the file
// from examples/ into cmd/marvel/examplefiles/.
func TestExampleFirstClusterPrintsTheExamplesFilesByteForByte(t *testing.T) {
	t.Parallel()
	cases := []struct{ arg, src string }{
		{"first-cluster", "../../examples/first-cluster.toml"},
		{"first-cluster.toml", "../../examples/first-cluster.toml"},
		{"first-cluster.yaml", "../../examples/first-cluster.yaml"},
	}
	for _, c := range cases {
		want, err := os.ReadFile(c.src)
		if err != nil {
			t.Fatal(err)
		}
		got, err := runExample(t, c.arg)
		if err != nil {
			t.Fatalf("marvel example %s: %v", c.arg, err)
		}
		if got != string(want) {
			t.Errorf("marvel example %s differs from %s; recopy the file into cmd/marvel/examplefiles/", c.arg, c.src)
		}
	}
}

// The list shows one name per example, however many formats it ships in, and
// never a name with a suffix.
func TestExampleListShowsOneNamePerExample(t *testing.T) {
	t.Parallel()
	list, err := runExample(t)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(list), "\n") {
		if line == "first-cluster" {
			count++
		}
		if strings.HasSuffix(line, ".toml") || strings.HasSuffix(line, ".yaml") {
			t.Errorf("the list names %q with a suffix", line)
		}
	}
	if count != 1 {
		t.Errorf("first-cluster appears %d times in the list, want once: %q", count, list)
	}
}

// A suffix selects a file that exists; it does not make up one. demo ships
// only as TOML, so demo.yaml is refused with the known names.
func TestExampleSuffixOfAMissingFormatIsRefused(t *testing.T) {
	t.Parallel()
	_, err := runExample(t, "demo.yaml")
	if err == nil {
		t.Fatal("demo.yaml was accepted, but demo ships only as TOML")
	}
	if !strings.Contains(err.Error(), "demo.yaml") || !strings.Contains(err.Error(), "first-cluster") {
		t.Errorf("the refusal does not name the bad name and the known ones: %v", err)
	}
}

// The help says which suffixes exist, so a user can find the YAML form
// without reading the source.
func TestExampleHelpNamesTheSuffixes(t *testing.T) {
	t.Parallel()
	long := exampleCmd().Long
	// The sentence that lists the suffixes, not an example line that happens
	// to contain one of them.
	const want = "A name ending in .toml or .yaml prints that"
	if !strings.Contains(strings.Join(strings.Fields(long), " "), want) {
		t.Errorf("the example help does not say which suffixes exist (%q):\n%s", want, long)
	}
}
