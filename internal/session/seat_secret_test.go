package session

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// Every value here is a fake marker.

type fakeSecrets map[string]struct {
	name  string
	value string
}

func (f fakeSecrets) SeatSecret(need string) (string, []byte, bool) {
	s, ok := f[need]
	return s.name, []byte(s.value), ok
}

func secretRig(t *testing.T, src SeatSecretSource) (*sandboxRig, string) {
	t.Helper()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	dir := filepath.Join(t.TempDir(), "fifos")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	r.mgr.SeatSecrets = src
	r.mgr.SeatSecretDir = dir
	r.mgr.SeatSecretWait = 5 * time.Second
	return r, dir
}

func hashOf(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// pane runs a launch command the way tmux does, through sh -c, with the
// original command replaced by one that records a hash of the variable.
func pane(t *testing.T, command string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", command)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
}

func waitFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return strings.TrimSpace(string(b))
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ""
}

func recordCommand(out string) string {
	return `printf %s "$BEADS_DOLT_PASSWORD" | /usr/bin/shasum -a 256 | cut -d' ' -f1 > ` + out
}

// A seat whose role declares a need gets the value in its own pane, never on the
// launch command line.
func TestARoleThatDeclaresANeedGetsTheValueInItsPaneAndNotOnTheCommandLine(t *testing.T) {
	r, _ := secretRig(t, fakeSecrets{"bd": {"BEADS_DOLT_PASSWORD", "mxfake-0042"}})
	out := filepath.Join(t.TempDir(), "out")
	plan := launchPlan{command: recordCommand(out), env: map[string]string{}}
	sess := r.session()
	r.mgr.wrapSeatSecrets(sess, api.Role{Name: "seat", SeatSecrets: []string{"bd"}}, &plan)
	if strings.Contains(plan.command, "mxfake-0042") {
		t.Fatal("the value is on the launch command line")
	}
	if !strings.Contains(plan.command, "/bin/bash") {
		t.Fatalf("the command was not wrapped: %q", plan.command)
	}
	for k, v := range plan.env {
		if strings.Contains(v, "mxfake-0042") {
			t.Errorf("the value is in the launch environment under %s", k)
		}
	}
	pane(t, plan.command)
	if got := waitFile(t, out); got != hashOf("mxfake-0042") {
		t.Errorf("the pane did not receive the value (hash %q)", got)
	}
	if sess.SecretNote != "" {
		t.Errorf("a delivered seat carries a note: %q", sess.SecretNote)
	}
}

// A role that declares nothing is not touched.
func TestARoleThatDeclaresNothingGetsNothing(t *testing.T) {
	r, dir := secretRig(t, fakeSecrets{"bd": {"BEADS_DOLT_PASSWORD", "mxfake-0042"}})
	plan := launchPlan{command: "echo hi", env: map[string]string{}}
	r.mgr.wrapSeatSecrets(r.session(), api.Role{Name: "seat"}, &plan)
	if plan.command != "echo hi" {
		t.Errorf("the command changed: %q", plan.command)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%d FIFO directories for a role that declared nothing", len(entries))
	}
}

// A need the cluster does not map starts the seat without it and says why, naming
// the need. The command is untouched: fail open.
func TestAnUnmappedNeedStartsTheSeatWithoutItAndSaysWhy(t *testing.T) {
	r, dir := secretRig(t, fakeSecrets{})
	plan := launchPlan{command: "echo hi", env: map[string]string{}}
	sess := r.session()
	r.mgr.wrapSeatSecrets(sess, api.Role{Name: "seat", SeatSecrets: []string{"bd"}}, &plan)
	if plan.command != "echo hi" {
		t.Errorf("the command changed: %q", plan.command)
	}
	if !strings.Contains(sess.SecretNote, "bd") {
		t.Errorf("the note does not name the need: %q", sess.SecretNote)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%d FIFO directories for a need nothing maps", len(entries))
	}
}

// No private directory to make the FIFO in: the seat starts without the value and
// the note says so.
func TestNoSecretDirectoryStartsTheSeatWithoutTheValueAndSaysWhy(t *testing.T) {
	r, _ := secretRig(t, fakeSecrets{"bd": {"BEADS_DOLT_PASSWORD", "mxfake-0042"}})
	r.mgr.SeatSecretDir = filepath.Join(t.TempDir(), "absent", "deeper")
	plan := launchPlan{command: "echo hi", env: map[string]string{}}
	sess := r.session()
	r.mgr.wrapSeatSecrets(sess, api.Role{Name: "seat", SeatSecrets: []string{"bd"}}, &plan)
	if plan.command != "echo hi" {
		t.Errorf("the command changed: %q", plan.command)
	}
	if !strings.Contains(sess.SecretNote, "bd") || strings.Contains(sess.SecretNote, "mxfake-0042") {
		t.Errorf("the note = %q; want one naming the need and never the value", sess.SecretNote)
	}
}

// A pane that never reads leaves the seat without the value after the bound, the
// stored session says so, and no FIFO directory is left behind.
func TestAPaneThatNeverReadsIsRecordedOnTheStoredSession(t *testing.T) {
	r, dir := secretRig(t, fakeSecrets{"bd": {"BEADS_DOLT_PASSWORD", "mxfake-0042"}})
	r.mgr.SeatSecretWait = 300 * time.Millisecond
	sess := r.session()
	if err := r.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	plan := launchPlan{command: "echo hi", env: map[string]string{}}
	r.mgr.wrapSeatSecrets(sess, api.Role{Name: "seat", SeatSecrets: []string{"bd"}}, &plan)
	deadline := time.Now().Add(5 * time.Second)
	var note string
	for time.Now().Before(deadline) {
		if got, err := r.store.GetSession(sess.Key()); err == nil && got.SecretNote != "" {
			note = got.SecretNote
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(note, "bd") || strings.Contains(note, "mxfake-0042") {
		t.Errorf("stored note = %q; want one naming the need and never the value", note)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%d FIFO directories left after a failed hand-over", len(entries))
	}
}

// The launch commit copies a fixed list of fields onto the stored session; the
// note is on it, or doctor never sees it.
func TestTheSecretNoteReachesTheStoredSession(t *testing.T) {
	r, _ := secretRig(t, fakeSecrets{})
	sess := r.session()
	if err := r.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	sess.SecretNote = "the cluster maps no need bd"
	if err := r.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		commitLaunchRecord(live, sess, "%1", 7)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := r.store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if got.SecretNote != sess.SecretNote {
		t.Errorf("stored note = %q, want %q", got.SecretNote, sess.SecretNote)
	}
}

// A re-launch that delivers must not carry the last launch's note.
func TestTheSecretNoteDoesNotOutliveTheLaunchItDescribes(t *testing.T) {
	r, _ := secretRig(t, fakeSecrets{"bd": {"BEADS_DOLT_PASSWORD", "mxfake-0042"}})
	sess := r.session()
	sess.SecretNote = "left over from an earlier launch"
	plan := launchPlan{command: "echo hi", env: map[string]string{}}
	r.mgr.wrapSeatSecrets(sess, api.Role{Name: "seat", SeatSecrets: []string{"bd"}}, &plan)
	if sess.SecretNote != "" {
		t.Errorf("the earlier note survived a launch that handed the value over: %q", sess.SecretNote)
	}
}

// Several needs that fail each leave their own line, not only the last one, and a
// need that has a variable names it.
func TestEveryFailedNeedIsInTheNoteAndNamesItsVariable(t *testing.T) {
	r, _ := secretRig(t, fakeSecrets{"bd": {"BEADS_DOLT_PASSWORD", ""}, "bus": {"MX_BUS", ""}})
	plan := launchPlan{command: "echo hi", env: map[string]string{}}
	sess := r.session()
	r.mgr.wrapSeatSecrets(sess, api.Role{Name: "seat", SeatSecrets: []string{"bd", "bus"}}, &plan)
	for _, want := range []string{`"bd"`, "BEADS_DOLT_PASSWORD", `"bus"`, "MX_BUS"} {
		if !strings.Contains(sess.SecretNote, want) {
			t.Errorf("the note %q does not hold %s", sess.SecretNote, want)
		}
	}
}

// A value the hand-over refuses is reported as that refusal, not as a pane that was
// too slow, and the note never holds the value.
func TestARefusedValueIsNotReportedAsASlowPane(t *testing.T) {
	r, _ := secretRig(t, fakeSecrets{"bd": {"BEADS_DOLT_PASSWORD", "mxfake\n0042"}})
	sess := r.session()
	if err := r.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	plan := launchPlan{command: "echo hi", env: map[string]string{}}
	r.mgr.wrapSeatSecrets(sess, api.Role{Name: "seat", SeatSecrets: []string{"bd"}}, &plan)
	deadline := time.Now().Add(5 * time.Second)
	var note string
	for time.Now().Before(deadline) {
		if got, err := r.store.GetSession(sess.Key()); err == nil && got.SecretNote != "" {
			note = got.SecretNote
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if note == "" {
		t.Fatal("no note was recorded for a refused value")
	}
	if strings.Contains(note, "in time") || !strings.Contains(note, "newline") {
		t.Errorf("note = %q; want the refusal (newline), not a slow pane", note)
	}
	if strings.Contains(note, "mxfake") {
		t.Errorf("the note holds the value: %q", note)
	}
	if !strings.Contains(note, "BEADS_DOLT_PASSWORD") {
		t.Errorf("the note does not name the variable: %q", note)
	}
}
