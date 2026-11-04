package api

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func journalStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s := NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	t.Cleanup(func() { _ = s.CloseBolt() })
	return s, path
}

func entryFor(session, id string) ResumeEntry {
	return ResumeEntry{
		Session: session, Workspace: "acme", Team: "squad", Role: "worker",
		Runtime: "claude", ResumeID: id, WorkDir: "/work/x",
		LaunchedAt: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC),
	}
}

func TestRecordResumeStoresOneEntryPerSession(t *testing.T) {
	s, _ := journalStore(t)
	if err := s.RecordResume(entryFor("acme/squad/a", "id-1")); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordResume(entryFor("acme/squad/b", "id-2")); err != nil {
		t.Fatal(err)
	}
	got, err := s.ResumeJournal()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Session != "acme/squad/a" || got[1].ResumeID != "id-2" {
		t.Fatalf("journal = %+v, want entries a and b in key order", got)
	}
}

// A seat whose resume id changes is recorded again, and the entry for the new
// id replaces the old one: a crash must find the id of the launch that ran.
func TestRecordResumeReplacesTheEntryOnAnIDChange(t *testing.T) {
	s, _ := journalStore(t)
	_ = s.RecordResume(entryFor("acme/squad/a", "old-id"))
	_ = s.RecordResume(entryFor("acme/squad/a", "new-id"))
	got, _ := s.ResumeJournal()
	if len(got) != 1 || got[0].ResumeID != "new-id" {
		t.Fatalf("journal = %+v, want one entry holding new-id", got)
	}
}

func TestRecordResumeSurvivesReopen(t *testing.T) {
	s, path := journalStore(t)
	if err := s.RecordResume(entryFor("acme/squad/a", "id-1")); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.CloseBolt() }()
	got, _ := s2.ResumeJournal()
	if len(got) != 1 || got[0].ResumeID != "id-1" {
		t.Fatalf("journal after reopen = %+v, want the entry", got)
	}
}

func TestRecordResumeTellsTheHookTheWholeJournal(t *testing.T) {
	s, _ := journalStore(t)
	var seen [][]ResumeEntry
	s.SetResumeJournalHook(func(e []ResumeEntry) { seen = append(seen, e) })
	_ = s.RecordResume(entryFor("acme/squad/a", "id-1"))
	_ = s.RecordResume(entryFor("acme/squad/b", "id-2"))
	if len(seen) != 2 || len(seen[0]) != 1 || len(seen[1]) != 2 {
		t.Fatalf("hook saw %d calls with sizes %v, want 2 calls of 1 then 2 entries", len(seen), sizes(seen))
	}
}

func sizes(v [][]ResumeEntry) []int {
	var out []int
	for _, e := range v {
		out = append(out, len(e))
	}
	return out
}

func TestRecordResumeRefusesAnEmptyKey(t *testing.T) {
	s, _ := journalStore(t)
	if err := s.RecordResume(ResumeEntry{}); err == nil {
		t.Fatal("an entry with no session key was accepted")
	}
}

func TestRecordResumeWithoutBoltIsHarmless(t *testing.T) {
	s := NewStore()
	if err := s.RecordResume(entryFor("acme/squad/a", "id-1")); err != nil {
		t.Fatalf("RecordResume with no bolt: %v", err)
	}
	if got, err := s.ResumeJournal(); err != nil || len(got) != 0 {
		t.Fatalf("journal with no bolt = %v, %v; want empty", got, err)
	}
}

// HB-D: the entry holds no secret. Every sensitive field on the session is set
// to a marker string, and none of them may reach the entry's JSON.
func TestResumeEntryOfCarriesNoSecretEnvOrPrompt(t *testing.T) {
	sess := &Session{
		Name: "a", Workspace: "acme", Team: "squad", Role: "worker",
		Runtime: Runtime{
			Name: "claude", Command: "claude-SECRETCMD", Args: []string{"--token=SECRETARG"},
			Env:    map[string]string{"ANTHROPIC_API_KEY": "SECRETENV"},
			Prompt: "SECRETPROMPT",
		},
		HarnessSessionID: "resume-id", WorkDir: "/work/x", Predecessor: "acme/squad/old",
		FirstPromptSha256: "SECRETHASH",
	}
	sess.HeartbeatToken = "SECRETBEAT"
	raw, err := json.Marshal(ResumeEntryOf(sess, "/h/a.md", time.Unix(1, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SECRET") {
		t.Fatalf("entry carries a secret field: %s", raw)
	}
	var e ResumeEntry
	_ = json.Unmarshal(raw, &e)
	if e.Session != sess.Key() || e.ResumeID != "resume-id" || e.HandoffPath != "/h/a.md" ||
		e.Predecessor != "acme/squad/old" || e.Runtime != "claude" || e.WorkDir != "/work/x" {
		t.Fatalf("entry = %+v, missing a named field", e)
	}
}
