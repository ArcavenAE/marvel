package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// A seat on the simulator needs marvel-simulator on PATH, and a simulator from
// a different release than marvel is worth a line, so marvel version reads it.
func TestSimulatorReport(t *testing.T) {
	t.Parallel()
	found := func(string) (string, error) { return "/usr/local/bin/marvel-simulator", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	says := func(line string) func(string) (string, error) {
		return func(string) (string, error) { return line, nil }
	}

	for _, tc := range []struct {
		name    string
		look    func(string) (string, error)
		run     func(string) (string, error)
		want    []string
		notWant []string
	}{
		{
			name:    "same build prints the simulator and no note",
			look:    found,
			run:     says("marvel-simulator 0.1.0 (stable)\n"),
			want:    []string{"simulator 0.1.0 (stable)"},
			notWant: []string{"note:"},
		},
		{
			name: "a different version prints a note naming both",
			look: found,
			run:  says("marvel-simulator 0.0.9 (stable)\n"),
			want: []string{"simulator 0.0.9 (stable)", "note: ", "0.0.9", "0.1.0"},
		},
		{
			name: "a different channel prints a note",
			look: found,
			run:  says("marvel-simulator 0.1.0 (alpha)\n"),
			want: []string{"simulator 0.1.0 (alpha)", "note: "},
		},
		{
			name:    "no simulator on PATH prints nothing",
			look:    missing,
			run:     says("unused"),
			notWant: []string{"simulator", "note:"},
		},
		{
			name: "a simulator that does not answer says so",
			look: found,
			run:  func(string) (string, error) { return "", errors.New("exit status 2") },
			want: []string{"simulator ", "does not report its version"},
		},
		{
			name: "output that is not a version line is reported as unreadable",
			look: found,
			run:  says("usage: simulator [flags]\n"),
			want: []string{"does not report its version"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			simulatorReport(&out, "0.1.0", "stable", tc.look, tc.run)
			got := out.String()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("output lacks %q:\n%s", w, got)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("output has %q:\n%s", w, got)
				}
			}
		})
	}
}
