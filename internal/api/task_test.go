package api

import (
	"path/filepath"
	"strings"
	"testing"
)

// A session may carry a task key: the id of the bd ticket or the ask the
// session works on. It is the fleet's form of the A2A attribute a2a.task.id.

func TestValidTask(t *testing.T) {
	for _, ok := range []string{"aae-orc-p0xrm", "I-100", "a2a:task/1", "x", strings.Repeat("a", 128)} {
		if !ValidTask(ok) {
			t.Errorf("ValidTask(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", " ", "a b", "a\tb", "a\nb", "a\x00b", "a\u2028b", "a\u202eb", strings.Repeat("a", 129)} {
		if ValidTask(bad) {
			t.Errorf("ValidTask(%q) = true, want false", bad)
		}
	}
}

// task is a per-role key, in TOML and in YAML, carried to the applied role;
// absent means none.
func TestTaskIsARoleKeyCarriedToTheAppliedRole(t *testing.T) {
	for name, tc := range map[string]struct{ extra, want string }{
		"absent": {"", ""},
		"set":    {`  task = "aae-orc-p0xrm"`, "aae-orc-p0xrm"},
	} {
		m, err := imageManifest(t, tc.extra)
		if err != nil {
			t.Fatalf("toml %s: %v", name, err)
		}
		if got := m.Teams[0].Roles[0].Task; got != tc.want {
			t.Errorf("toml %s: task = %q, want %q", name, got, tc.want)
		}
		store := NewStore()
		if err := m.Apply(store); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		team, err := store.GetTeam("aae/squad")
		if err != nil {
			t.Fatal(err)
		}
		if got := team.Roles[0].Task; got != tc.want {
			t.Errorf("applied %s: Task = %q, want %q", name, got, tc.want)
		}
	}
	y := "workspace:\n  name: aae\nteams:\n  - name: squad\n    roles:\n      - name: w\n        replicas: 1\n        task: I-100\n        runtime:\n          command: pi\n"
	m, err := ParseManifestBytes([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Teams[0].Roles[0].Task; got != "I-100" {
		t.Errorf("yaml: task = %q", got)
	}
}

// A role whose task is not a valid key is refused at parse, naming the role
// and the key, before anything is applied.
func TestManifestRefusesATaskThatIsNotAKey(t *testing.T) {
	for _, bad := range []string{"a b", strings.Repeat("a", 129), "a\\tb"} {
		_, err := imageManifest(t, `  task = "`+bad+`"`)
		if err == nil {
			t.Errorf("task %q parsed, want a refusal", bad)
			continue
		}
		if msg := err.Error(); !strings.Contains(msg, "task") || !strings.Contains(msg, `"w"`) {
			t.Errorf("task %q: error %q does not name the key and the role", bad, msg)
		}
	}
}

// The index the event ring reads without taking the store lock follows the
// session: set when it is created, gone when it is deleted, and empty for a
// session with no task.
func TestStoreTaskOfFollowsTheSession(t *testing.T) {
	s := NewStore()
	if err := s.CreateSession(&Session{Name: "a", Workspace: "w", Team: "t", Role: "r", Task: "I-100"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(&Session{Name: "b", Workspace: "w", Team: "t", Role: "r"}); err != nil {
		t.Fatal(err)
	}
	if got := s.TaskOf("w/a"); got != "I-100" {
		t.Errorf("TaskOf(w/a) = %q, want I-100", got)
	}
	if got := s.TaskOf("w/b"); got != "" {
		t.Errorf("TaskOf(w/b) = %q, want none", got)
	}
	if err := s.DeleteSession("w/a"); err != nil {
		t.Fatal(err)
	}
	if got := s.TaskOf("w/a"); got != "" {
		t.Errorf("TaskOf after delete = %q, want none", got)
	}
}

// A restart keeps the task: the session row and the index both come back from
// the store file.
func TestTaskSurvivesAReopenOfTheStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	if err := s1.CreateSession(&Session{Name: "a", Workspace: "w", Team: "t", Role: "r", Task: "I-100"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got, err := s2.GetSession("w/a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Task != "I-100" {
		t.Errorf("reopened Session.Task = %q, want I-100", got.Task)
	}
	if idx := s2.TaskOf("w/a"); idx != "I-100" {
		t.Errorf("reopened TaskOf = %q, want I-100", idx)
	}
}
