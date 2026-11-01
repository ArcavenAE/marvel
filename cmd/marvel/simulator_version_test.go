package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemon"
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

// marvel version runs the real lookup and the real exec: a marvel-simulator
// script on PATH that answers -version reaches the report, with no daemon
// answering. This covers the wiring in versionReport, which the injected cases
// above do not.
func TestVersionReportReadsTheSimulatorOnPath(t *testing.T) {
	// t.Setenv changes PATH, so this test cannot run in parallel.
	for _, tc := range []struct {
		name    string
		answer  string
		want    []string
		notWant []string
	}{
		{
			name:    "same build",
			answer:  "marvel-simulator 0.1.0 (stable)",
			want:    []string{"marvel 0.1.0 (stable)", "simulator 0.1.0 (stable)"},
			notWant: []string{"note:"},
		},
		{
			name:   "different build",
			answer: "marvel-simulator 0.0.9 (stable)",
			want:   []string{"simulator 0.0.9 (stable)", "note: the simulator on PATH is 0.0.9 (stable) and marvel is 0.1.0 (stable)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\nif [ \"$1\" = -version ]; then echo '" + tc.answer + "'; fi\n"
			if err := os.WriteFile(filepath.Join(dir, "marvel-simulator"), []byte(script), 0o755); err != nil { //nolint:gosec // a test script must be executable
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")

			var out bytes.Buffer
			versionReport(&out, daemon.Build{Version: "0.1.0", Channel: "stable"}, func() (*daemon.BuildInfo, error) {
				return nil, errors.New("no daemon")
			})
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
			if strings.Contains(got, "daemon") {
				t.Errorf("a failed daemon query printed a daemon line:\n%s", got)
			}
		})
	}
}
