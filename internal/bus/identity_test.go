package bus

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
)

func sampleIdentity() childproof.Identity {
	return childproof.Identity{
		Start: "darwin:1791533001.303086",
		Exe:   "/opt/homebrew/Cellar/nats-server/2.14.6/bin/nats-server",
		Argv:  []string{"/opt/homebrew/bin/nats-server", "-c", "/state/nats/nats-server.conf"},
		Pgid:  41237,
		Ppid:  4242,
	}
}

func TestIdentityRecordRoundTripsAndIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "nats-server.pid.identity")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 9, 8, 30, 0, 0, time.UTC)
	id := sampleIdentity()
	if err := writeIdentity(path, 41237, id, at); err != nil {
		t.Fatalf("writeIdentity: %v", err)
	}
	rec, err := readIdentityFile(path)
	if err != nil {
		t.Fatalf("readIdentityFile: %v", err)
	}
	if rec.Schema != identitySchema || rec.PID != 41237 || rec.WrittenAt != "2026-10-09T08:30:00Z" {
		t.Errorf("record = %+v", rec)
	}
	got := rec.identity()
	if got.Start != id.Start || got.Exe != id.Exe || !slices.Equal(got.Argv, id.Argv) {
		t.Errorf("identity() = %+v, want %+v", got, id)
	}
	if got.Pgid != 0 || got.Ppid != 0 {
		t.Errorf("the process group and the parent are read live and never recorded; got %d and %d", got.Pgid, got.Ppid)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	// Written to a temporary name and renamed: nothing else is left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries after the write, want only the record", len(entries))
	}
}

func TestIdentityRecordReplacesAnOlderOneWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.identity")
	if err := os.WriteFile(path, []byte("not json, left by something else"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeIdentity(path, 7, sampleIdentity(), time.Now()); err != nil {
		t.Fatalf("writeIdentity over an existing file: %v", err)
	}
	if rec, err := readIdentityFile(path); err != nil || rec.PID != 7 {
		t.Errorf("after the replace: %+v, %v", rec, err)
	}
}

func TestIdentityRecordReadRefusesWhatIsNotOurs(t *testing.T) {
	dir := t.TempDir()
	if _, err := readIdentityFile(filepath.Join(dir, "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing record: err = %v, want fs.ErrNotExist so a legacy pidfile is told from a corrupt record", err)
	}
	for name, body := range map[string]string{
		"corrupt":          "{not json",
		"empty":            "",
		"another schema":   `{"schema":2,"pid":7,"start":"a","exe":"b","argv":["c"]}`,
		"no schema":        `{"pid":7,"start":"a","exe":"b","argv":["c"]}`,
		"a trailing value": `{"schema":1,"pid":7,"start":"a","exe":"b","argv":["c"]} {"x":1}`,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if rec, err := readIdentityFile(path); err == nil {
			t.Errorf("%s: readIdentityFile accepted %q as %+v", name, body, rec)
		} else if errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: a corrupt record read as a missing one", name)
		}
	}
}

func TestSidecarPathBesideThePidfile(t *testing.T) {
	if got := sidecarPath("/run/nats-server.pid"); got != "/run/nats-server.pid.identity" {
		t.Errorf("sidecarPath = %q", got)
	}
}
