package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/daemon"
)

var (
	clientBuild = daemon.Build{Version: "0.1.0-alpha.20261002.232554.bc327df", Channel: "alpha"}
	daemonBuild = daemon.Build{Version: "0.1.0-alpha.20261002.231214.b533ca5", Channel: "alpha", Commit: "b533ca5d1e2f"}
)

func answer(b daemon.Build) buildQuery {
	return func() (*daemon.BuildInfo, error) {
		return &daemon.BuildInfo{
			Build:     b,
			StartedAt: time.Date(2026, 10, 3, 1, 30, 0, 0, time.UTC),
			PID:       4242,
		}, nil
	}
}

func report(client daemon.Build, q buildQuery) string {
	var buf bytes.Buffer
	versionReport(&buf, client, q)
	return buf.String()
}

// The first line is what `marvel version` has always printed; scripts read it.
func TestVersionReportKeepsTheClientLineFirst(t *testing.T) {
	out := report(clientBuild, answer(clientBuild))
	first, _, _ := strings.Cut(out, "\n")
	if first != "marvel "+clientBuild.Version+" (alpha)" {
		t.Errorf("first line = %q", first)
	}
}

func TestVersionReportShowsTheDaemonBesideTheClient(t *testing.T) {
	out := report(clientBuild, answer(clientBuild))
	for _, want := range []string{"daemon", clientBuild.Version, "pid 4242", "2026-10-03T01:30:00Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "warning") {
		t.Errorf("a matching build was flagged:\n%s", out)
	}
}

// The case the issue is about: the daemon did not move. The report says so and
// names the one command that adopts the installed binary.
func TestVersionReportFlagsAMismatch(t *testing.T) {
	out := report(clientBuild, answer(daemonBuild))
	for _, want := range []string{"warning", "different build", daemonBuild.Version, "marvel daemon reexec"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestVersionReportFlagsADifferentChannel(t *testing.T) {
	other := clientBuild
	other.Channel = "stable"
	if out := report(clientBuild, answer(other)); !strings.Contains(out, "different build") {
		t.Errorf("a different channel was not flagged:\n%s", out)
	}
}

// A daemon from before this change answers, but cannot say its build. That is
// reported as such, and it is not a build mismatch.
func TestVersionReportSaysWhenTheDaemonPredatesTheMethod(t *testing.T) {
	out := report(clientBuild, func() (*daemon.BuildInfo, error) { return nil, errDaemonPredates })
	if !strings.Contains(out, "does not report its build") {
		t.Errorf("report does not say the daemon cannot report its build:\n%s", out)
	}
	if strings.Contains(out, "different build") {
		t.Errorf("an unknown build was reported as a mismatch:\n%s", out)
	}
}

// `marvel version` must work with no daemon: the components are independent.
func TestVersionReportIsSilentWhenNoDaemonIsRunning(t *testing.T) {
	out := report(clientBuild, func() (*daemon.BuildInfo, error) { return nil, errors.New("connect: no such file") })
	if out != "marvel "+clientBuild.Version+" (alpha)\n" {
		t.Errorf("report = %q, want only the client line", out)
	}
}

// A wedged daemon must not wedge `marvel version`; there is no read deadline
// on the socket, so the query is bounded here. The call runs in a goroutine so
// a missing bound fails this test instead of hanging it.
func TestBoundedQueryGivesUpOnAHungDaemon(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	type result struct {
		info *daemon.BuildInfo
		err  error
	}
	done := make(chan result, 1)
	go func() {
		info, err := boundedQuery(50*time.Millisecond, func() (*daemon.BuildInfo, error) {
			<-release
			return nil, nil
		})
		done <- result{info, err}
	}()

	select {
	case r := <-done:
		if r.err == nil || r.info != nil {
			t.Fatalf("a hung query returned %v, %v", r.info, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("boundedQuery did not return: a hung daemon would hang marvel version")
	}
}

func TestBoundedQueryReturnsAnAnswerInTime(t *testing.T) {
	info, err := boundedQuery(time.Second, answer(daemonBuild))
	if err != nil || info == nil || info.Version != daemonBuild.Version {
		t.Fatalf("got %v, %v", info, err)
	}
}
