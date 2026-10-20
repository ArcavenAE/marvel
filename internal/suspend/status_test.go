package suspend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
)

func sampleRecord(state State) Record {
	return Record{
		State: state, Actor: "op", Source: "cli",
		Since:  time.Date(2026, 10, 20, 22, 0, 0, 0, time.UTC),
		Server: Proc{Pid: 100, Identity: childproof.Identity{Start: "linux:b:1", Pgid: 100}},
		Seats: []Proc{
			{Pid: 200, Identity: childproof.Identity{Start: "linux:b:2", Pgid: 200}},
			{Pid: 201, Identity: childproof.Identity{Start: "linux:b:3", Pgid: 201}},
		},
	}
}

func TestStatusOfAndExitCode(t *testing.T) {
	if st := StatusOf(nil); st.State != StateRunning || st.Record != nil || st.ExitCode() != 0 {
		t.Errorf("no record: %+v exit %d, want running and 0", st, st.ExitCode())
	}
	for _, s := range []State{StateSuspending, StateSuspended, StateResuming} {
		rec := sampleRecord(s)
		if st := StatusOf(&rec); st.State != string(s) || st.ExitCode() != 3 {
			t.Errorf("%s: state %q exit %d, want %q and 3", s, st.State, st.ExitCode(), s)
		}
	}
}

// The recipe resumes the server first, then each seat, each line carrying the
// start time and process group it was recorded with.
func TestRecipeServerFirstWithStartAndGroup(t *testing.T) {
	lines := Recipe(sampleRecord(StateSuspended))
	want := []string{
		"kill -CONT 100   # tmux server, started linux:b:1, pgid 100",
		"kill -CONT 200   # seat, started linux:b:2, pgid 200",
		"kill -CONT 201   # seat, started linux:b:3, pgid 201",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("recipe:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestRender(t *testing.T) {
	now := time.Date(2026, 10, 20, 23, 2, 3, 0, time.UTC)
	if got := Render(StatusOf(nil), now, false); got != "cluster: running\n" {
		t.Errorf("running = %q", got)
	}
	rec := sampleRecord(StateSuspended)
	got := Render(StatusOf(&rec), now, true)
	for _, want := range []string{
		"daemon not answering; read from the suspend mirror",
		"cluster: suspended since 2026-10-20T22:00:00Z (1h2m3s ago) by op via cli",
		"seats: 2 recorded at stop time",
		"bus presence lapses",
		"kill -CONT 100",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(Render(StatusOf(&rec), now, false), "not answering") {
		t.Error("a daemon answer was rendered as read from the mirror")
	}
}

func TestMirrorPath(t *testing.T) {
	if got := MirrorPath("/run/m/marvel.sock"); got != "/run/m/marvel.sock.suspend.json" {
		t.Errorf("unix socket mirror = %q", got)
	}
	for _, a := range []string{"", "127.0.0.1:7000", "mrvl://host:2222"} {
		if got := MirrorPath(a); got != "" {
			t.Errorf("MirrorPath(%q) = %q, want none", a, got)
		}
	}
}

func TestMirrorRoundTrip(t *testing.T) {
	path := MirrorPath(filepath.Join(t.TempDir(), "marvel.sock"))
	if _, found, err := ReadMirror(path); found || err != nil {
		t.Fatalf("no mirror yet: found %v err %v", found, err)
	}
	rec := sampleRecord(StateSuspended)
	if err := WriteMirror(path, rec); err != nil {
		t.Fatal(err)
	}
	got, found, err := ReadMirror(path)
	if err != nil || !found || got.State != StateSuspended || len(got.Seats) != 2 || got.Server.Identity.Start != "linux:b:1" {
		t.Fatalf("read back %+v found %v err %v", got, found, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mirror mode = %v (err %v), want 0600", info.Mode().Perm(), err)
	}
	if err := RemoveMirror(path); err != nil {
		t.Fatal(err)
	}
	if err := RemoveMirror(path); err != nil {
		t.Errorf("removing a mirror that is gone: %v", err)
	}
	if _, found, _ := ReadMirror(path); found {
		t.Error("mirror still there after remove")
	}
}
