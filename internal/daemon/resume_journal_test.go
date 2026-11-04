package daemon

import (
	"os"
	"path/filepath"
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
	if m, found, _ := resumejournal.ReadMirror(path); !found || !m.Complete || len(m.Entries) != 0 {
		t.Fatalf("start mirror = %+v found=%v, want a complete empty mirror", m, found)
	}

	_ = d.store.RecordResume(resumeEntry("acme/a", "id-1"))
	_ = d.store.RecordResume(resumeEntry("acme/a", "id-2"))
	_ = d.store.RecordResume(resumeEntry("acme/b", "id-3"))

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
	_ = d.store.RecordResume(resumeEntry("acme/a", "id-1"))
	if ents, _ := os.ReadDir(filepath.Dir(d.store.BoltPath())); len(ents) != 1 {
		t.Fatalf("a TCP daemon wrote %d files beside its state, want only the bolt file", len(ents))
	}
}
