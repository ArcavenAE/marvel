package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/resumejournal"
)

func resumeEntry(session, id string) api.ResumeEntry {
	return api.ResumeEntry{
		Session: session, Workspace: "acme", Team: "squad", Role: "worker",
		ResumeID: id, LaunchedAt: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC),
	}
}

// At start the mirror is written from bolt, so a restart finds a whole mirror
// even before any seat launches.
func TestStartWritesTheResumeMirrorFromBolt(t *testing.T) {
	d, sock := suspendDaemon(t)
	if err := d.store.RecordResume(resumeEntry("acme/a", "id-1")); err != nil {
		t.Fatal(err)
	}

	d.loadResumeJournal(sock)

	m, found, err := resumejournal.ReadMirror(resumejournal.MirrorPath(sock))
	if err != nil || !found || !m.Complete || len(m.Entries) != 1 || m.Entries[0].ResumeID != "id-1" {
		t.Fatalf("mirror = %+v found=%v err=%v, want one complete entry", m, found, err)
	}
}

// After start, each launch's entry reaches the mirror with the marker last.
func TestEveryRecordedEntryReachesTheMirror(t *testing.T) {
	d, sock := suspendDaemon(t)
	d.loadResumeJournal(sock)
	path := resumejournal.MirrorPath(sock)
	if m, found, err := resumejournal.ReadMirror(path); err != nil || !found || !m.Complete || len(m.Entries) != 0 {
		t.Fatalf("start mirror = %+v found=%v err=%v, want a complete empty mirror", m, found, err)
	}

	for _, e := range []api.ResumeEntry{
		resumeEntry("acme/a", "id-1"), resumeEntry("acme/a", "id-2"), resumeEntry("acme/b", "id-3"),
	} {
		if err := d.store.RecordResume(e); err != nil {
			t.Fatalf("RecordResume %s: %v", e.Session, err)
		}
	}

	m, _, err := resumejournal.ReadMirror(path)
	if err != nil || !m.Complete || len(m.Entries) != 2 || m.Entries[0].ResumeID != "id-2" {
		t.Fatalf("mirror = %+v err=%v, want 2 entries with a's id replaced", m, err)
	}
}

func TestTCPListenerHasNoResumeMirror(t *testing.T) {
	d, _ := suspendDaemon(t)
	d.loadResumeJournal("127.0.0.1:7000")
	if d.resumeMirror != "" {
		t.Fatalf("resumeMirror = %q, want none for a TCP address", d.resumeMirror)
	}
	if err := d.store.RecordResume(resumeEntry("acme/a", "id-1")); err != nil {
		t.Fatalf("RecordResume: %v", err)
	}
	ents, err := os.ReadDir(filepath.Dir(d.store.BoltPath()))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("a TCP daemon wrote %d files beside its state, want only the bolt file", len(ents))
	}
}

// Launches run concurrently (the controller and the RPC path both create
// sessions). However they interleave, the last mirror write must hold every
// entry: a mirror that reads Complete with an entry missing is the smaller
// fleet the marker exists to rule out.
func TestConcurrentLaunchesLeaveEveryEntryInTheMirror(t *testing.T) {
	d, sock := suspendDaemon(t)
	d.loadResumeJournal(sock)
	const n = 24
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- d.store.RecordResume(resumeEntry(fmt.Sprintf("acme/s%02d", i), fmt.Sprintf("id-%02d", i)))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("RecordResume: %v", err)
		}
	}

	m, found, err := resumejournal.ReadMirror(resumejournal.MirrorPath(sock))
	if err != nil || !found || !m.Complete || len(m.Entries) != n {
		t.Fatalf("mirror holds %d entries (complete=%v found=%v err=%v), want all %d", len(m.Entries), m.Complete, found, err, n)
	}
}
