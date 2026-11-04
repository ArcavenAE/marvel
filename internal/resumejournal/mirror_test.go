package resumejournal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

func sample(n int) []api.ResumeEntry {
	var out []api.ResumeEntry
	for i := 0; i < n; i++ {
		out = append(out, api.ResumeEntry{
			Session: "acme/squad/s" + string(rune('a'+i)), Workspace: "acme", Team: "squad",
			Role: "worker", ResumeID: "id-" + string(rune('a'+i)),
			LaunchedAt: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC),
		})
	}
	return out
}

func TestMirrorPathBesideTheSocketAndNoneForTCP(t *testing.T) {
	if got := MirrorPath("/run/m.sock"); got != "/run/m.sock.resume.jsonl" {
		t.Fatalf("MirrorPath = %q", got)
	}
	if got := MirrorPath("127.0.0.1:7000"); got != "" {
		t.Fatalf("a TCP address has no file beside it, got %q", got)
	}
}

func TestMirrorRoundTripIsComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.resume.jsonl")
	if err := WriteMirror(path, sample(3)); err != nil {
		t.Fatal(err)
	}
	m, found, err := ReadMirror(path)
	if err != nil || !found {
		t.Fatalf("ReadMirror = %v, found %v", err, found)
	}
	if !m.Complete || len(m.Entries) != 3 || m.Entries[2].ResumeID != "id-c" {
		t.Fatalf("mirror = %+v, want 3 entries and Complete", m)
	}
}

// An empty fleet is a whole mirror with a marker and no entries; it is not the
// absence of a mirror.
func TestEmptyJournalIsCompleteNotMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.resume.jsonl")
	if err := WriteMirror(path, nil); err != nil {
		t.Fatal(err)
	}
	m, found, err := ReadMirror(path)
	if err != nil || !found || !m.Complete || len(m.Entries) != 0 {
		t.Fatalf("empty mirror = %+v found=%v err=%v, want complete with no entries", m, found, err)
	}
}

func TestMirrorWithoutTheMarkerReadsAsIncomplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.resume.jsonl")
	if err := WriteMirror(path, sample(3)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	// Cut on a line boundary: two entries and no marker.
	cut := strings.Join(lines[:2], "\n") + "\n"
	if err := os.WriteFile(path, []byte(cut), 0o600); err != nil {
		t.Fatal(err)
	}
	m, found, err := ReadMirror(path)
	if err != nil || !found {
		t.Fatalf("ReadMirror = %v found=%v", err, found)
	}
	if m.Complete {
		t.Fatal("a mirror with no terminal marker read as complete")
	}
	if len(m.Entries) != 2 {
		t.Fatalf("entries = %d, want the 2 that parsed", len(m.Entries))
	}
}

func TestMirrorCutMidLineReadsAsIncomplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.resume.jsonl")
	if err := WriteMirror(path, sample(3)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, data[:len(data)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	m, found, err := ReadMirror(path)
	if err != nil || !found || m.Complete {
		t.Fatalf("half a mirror = %+v found=%v err=%v, want found and incomplete", m, found, err)
	}
}

// The marker's count must match: a marker followed by nothing but preceded by
// a lost entry line is still a cut-short mirror.
func TestMarkerWithAWrongCountReadsAsIncomplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.resume.jsonl")
	if err := WriteMirror(path, sample(3)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	lost := append([]string{lines[0], lines[2]}, lines[3:]...)
	if err := os.WriteFile(path, []byte(strings.Join(lost, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _, err := ReadMirror(path)
	if err != nil || m.Complete {
		t.Fatalf("mirror with a lost entry = %+v err=%v, want incomplete", m, err)
	}
}

func TestReadMirrorMissingFileIsNotFound(t *testing.T) {
	_, found, err := ReadMirror(filepath.Join(t.TempDir(), "none"))
	if err != nil || found {
		t.Fatalf("found=%v err=%v, want neither", found, err)
	}
}

func TestWriteMirrorLeavesNoTempFileAndReplacesInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.resume.jsonl")
	_ = WriteMirror(path, sample(2))
	_ = WriteMirror(path, sample(1))
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("directory holds %d files, want only the mirror", len(ents))
	}
	m, _, _ := ReadMirror(path)
	if !m.Complete || len(m.Entries) != 1 {
		t.Fatalf("mirror = %+v, want the second write", m)
	}
}
