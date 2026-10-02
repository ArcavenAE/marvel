package bus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// Credential hands out the supervisor's own user only once the broker has
// accepted it. Render mints the user and writes the file, but the broker reads
// the file on a reload that lands later, so a spawn in between, or after a
// failed write, would get a user the broker does not know yet, where before the
// per-role user existed it would have used the working team user.

func userFor(m *Manager) string {
	u, _, _ := m.Credential("ops", "supervisor")
	return u
}

func TestCredentialHidesTheSupervisorUserUntilTheBrokerAcceptsIt(t *testing.T) {
	t.Parallel()
	m := hubManager(t, filepath.Join(t.TempDir(), "nats"), &teams{supervisedTeam()})
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if got := userFor(m); got != "ops" {
		t.Fatalf("right after the render the supervisor got %q, want the team user until the broker accepts the new one", got)
	}
	confirmed(t, m)
	if got := userFor(m); got != "ops.supervisor" {
		t.Fatalf("after the broker accepted it the supervisor got %q, want ops.supervisor", got)
	}
}

// A broker that refuses the user (its reload has not landed) keeps it hidden,
// and a later success reveals it.
func TestCredentialStaysOnTheTeamUserWhileTheBrokerRefuses(t *testing.T) {
	t.Parallel()
	m := hubManager(t, filepath.Join(t.TempDir(), "nats"), &teams{supervisedTeam()})
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	m.verify = func(context.Context, string, string, string) error { return errors.New("authorization violation") }
	if err := m.ConfirmRoleUsers(context.Background()); err == nil {
		t.Fatal("a refusal was reported as success")
	}
	if got := userFor(m); got != "ops" {
		t.Fatalf("while the broker refuses it, the supervisor got %q, want the team user", got)
	}
	confirmed(t, m)
	if got := userFor(m); got != "ops.supervisor" {
		t.Fatalf("after the broker accepted it the supervisor got %q", got)
	}
}

// The probe is a real login as the per-role user with its own password, not a
// check of the file.
func TestConfirmationLogsInAsTheRoleUserWithItsPassword(t *testing.T) {
	t.Parallel()
	m := hubManager(t, filepath.Join(t.TempDir(), "nats"), &teams{supervisedTeam()})
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	wantPW, _ := m.TeamPassword("ops.supervisor")
	var gotUser, gotPW, gotURL string
	m.verify = func(_ context.Context, url, user, pw string) error {
		gotURL, gotUser, gotPW = url, user, pw
		return nil
	}
	if err := m.ConfirmRoleUsers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotUser != "ops.supervisor" || gotPW != wantPW || gotURL != m.URL() {
		t.Errorf("probe = %q %q at %q, want ops.supervisor with its password at %s", gotUser, gotPW, gotURL, m.URL())
	}
}

// A render whose write fails mints the user in memory but never reaches the
// file, so it must not be handed out.
func TestCredentialIsNotHandedOutAfterAFailedWrite(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	m := hubManager(t, dir, &teams{supervisedTeam()})
	if err := os.MkdirAll(filepath.Join(dir, AuthName), 0o755); err != nil { // a directory where the file goes
		t.Fatal(err)
	}
	if _, err := m.Regenerate(); err == nil {
		t.Fatal("the write was expected to fail")
	}
	if got := userFor(m); got != "ops" {
		t.Fatalf("after a failed write the supervisor got %q, want the team user", got)
	}
}

// Nothing is known after a restart until the broker has been asked again: the
// adopted broker may still hold the previous daemon's file.
func TestARestartedManagerHidesTheSupervisorUserUntilConfirmed(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	live := &teams{supervisedTeam()}
	m1 := hubManager(t, dir, live)
	if _, err := m1.Regenerate(); err != nil {
		t.Fatal(err)
	}
	confirmed(t, m1)
	m2 := hubManager(t, dir, live)
	if _, err := m2.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if got := userFor(m2); got != "ops" {
		t.Fatalf("a fresh manager handed out %q before the broker was asked, want the team user", got)
	}
	confirmed(t, m2)
	if got := userFor(m2); got != "ops.supervisor" {
		t.Fatalf("after confirming, got %q", got)
	}
}

// A user whose role went away is forgotten, so bringing the role back is
// confirmed afresh.
func TestAReturningSupervisorUserMustBeConfirmedAgain(t *testing.T) {
	t.Parallel()
	live := &teams{supervisedTeam()}
	m := hubManager(t, filepath.Join(t.TempDir(), "nats"), live)
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	confirmed(t, m)
	*live = teams{api.Team{Name: "ops", Workspace: "acme", Roles: []api.Role{{Name: "worker"}}}}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	*live = teams{supervisedTeam()}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if got := userFor(m); got != "ops" {
		t.Fatalf("a re-added supervisor got %q before the broker accepted it, want the team user", got)
	}
}

type fakeReloader struct {
	err   error
	calls atomic.Int32
}

func (f *fakeReloader) Reload() error { f.calls.Add(1); return f.err }

// A regenerate asks the broker to reload and then confirms in the background;
// the supervisor user appears once that succeeds.
func TestRegenerateConfirmsAfterTheBrokerReloads(t *testing.T) {
	t.Parallel()
	m := hubManager(t, filepath.Join(t.TempDir(), "nats"), &teams{supervisedTeam()})
	var probed atomic.Int32
	m.verify = func(context.Context, string, string, string) error { probed.Add(1); return nil }
	rl := &fakeReloader{}
	m.Reloader = rl
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the supervisor user to be confirmed after the reload", func() bool { return userFor(m) == "ops.supervisor" })
	if rl.calls.Load() != 1 || probed.Load() == 0 {
		t.Errorf("reloads = %d, probes = %d, want one reload and at least one probe", rl.calls.Load(), probed.Load())
	}
}

// If the reload itself fails, nothing is confirmed and the probe never runs.
func TestRegenerateDoesNotConfirmWhenTheReloadFails(t *testing.T) {
	t.Parallel()
	m := hubManager(t, filepath.Join(t.TempDir(), "nats"), &teams{supervisedTeam()})
	var probed atomic.Int32
	m.verify = func(context.Context, string, string, string) error { probed.Add(1); return nil }
	m.Reloader = &fakeReloader{err: errors.New("SIGHUP failed")}
	if _, err := m.Regenerate(); err == nil {
		t.Fatal("a failed reload was reported as success")
	}
	time.Sleep(200 * time.Millisecond)
	if probed.Load() != 0 {
		t.Errorf("the broker was probed %d time(s) after a failed reload", probed.Load())
	}
	if got := userFor(m); got != "ops" {
		t.Errorf("supervisor got %q after a failed reload, want the team user", got)
	}
}
