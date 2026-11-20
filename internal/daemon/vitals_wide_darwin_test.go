//go:build darwin

package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/hostvitals"
	"github.com/arcavenae/marvel/internal/procstat"
)

// With a seat live, a vitals pass costs one ps and it is the wide one: the
// metrics pass reads the wide table, the vitals take it, the vitals reader is
// not called and no narrow read runs. A fake ps on PATH counts the calls.
func TestVitalsPassWithASeatMakesOneWideReadAndNoOther(t *testing.T) {
	skipIfNoTmux(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	pid := strconv.Itoa(os.Getpid())
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> '" + log + "'\n" +
		"case \"$*\" in\n" +
		"*etime=*) echo '" + pid + "     1   1.0   2048   00:30  0:00.50 seat-sh' ;;\n" +
		"*) echo '" + pid + "     1   1.0   2048' ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	s := &api.Session{
		Name: "agent-0", Workspace: "wide-once", Team: "squad", Role: "worker",
		Runtime: api.Runtime{Name: "sleep", Command: "sleep"}, State: api.SessionRunning, PID: os.Getpid(),
	}
	if err := d.store.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.store.DeleteSession(s.Key()) })
	tableReads := 0
	d.vitals = hostvitals.New(true, func() hostvitals.Reading { return hostvitals.Reading{} }, d.events)
	d.readTable = func() ([]procstat.Proc, error) { tableReads++; return nil, nil }

	d.metricsPass(procstat.NewSampler(), &metricsHold{})

	b, _ := os.ReadFile(log)
	calls := strings.Split(strings.TrimSpace(string(b)), "\n")
	wide, narrow := 0, 0
	for _, c := range calls {
		switch {
		case c == "":
		case strings.Contains(c, "etime="):
			wide++
		default:
			narrow++
		}
	}
	if wide != 1 || narrow != 0 {
		t.Errorf("ps calls on the vitals pass = %d wide, %d narrow (%q), want 1 wide and none else", wide, narrow, calls)
	}
	if tableReads != 0 {
		t.Errorf("the vitals reader was called %d times with a seat live, want 0", tableReads)
	}
	if top := d.vitals.Snapshot().Top; len(top) != 1 || top[0].Name != "seat-sh" {
		t.Errorf("top = %+v, want the process the metrics pass read", top)
	}
}
