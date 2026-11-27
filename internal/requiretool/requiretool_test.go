package requiretool

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestListed(t *testing.T) {
	cases := []struct {
		list, name string
		want       bool
	}{
		{"nats-server", "nats-server", true},
		{"tmux,nats-server", "nats-server", true},
		{" tmux , nats-server ", "nats-server", true},
		{"nats-server,,tmux", "tmux", true},
		{"", "nats-server", false},
		{"tmux", "nats-server", false},
		{"nats-server-x", "nats-server", false},
		{"nats", "nats-server", false},
		{"nats-server", "nats", false},
		{",,", "", false},
		{"nats-server", "", false},
	}
	for _, c := range cases {
		if got := Listed(c.list, c.name); got != c.want {
			t.Errorf("Listed(%q, %q) = %v, want %v", c.list, c.name, got, c.want)
		}
	}
}

// recorder stands in for *testing.T, which a test cannot let fail.
type recorder struct {
	fatal, skip []string
}

func (r *recorder) Helper() {}
func (r *recorder) Fatalf(f string, a ...any) {
	r.fatal = append(r.fatal, fmt.Sprintf(f, a...))
}

func (r *recorder) Skipf(f string, a ...any) {
	r.skip = append(r.skip, fmt.Sprintf(f, a...))
}

func found(string) (string, error)   { return "/opt/bin/nats-server", nil }
func missing(string) (string, error) { return "", errors.New("not found") }

func TestAMissingListedToolFailsTheTest(t *testing.T) {
	r := &recorder{}
	got := pathFor(r, missing, "tmux,nats-server", "nats-server")
	if len(r.fatal) != 1 || len(r.skip) != 0 {
		t.Fatalf("fatal=%v skip=%v, want one failure and no skip", r.fatal, r.skip)
	}
	for _, want := range []string{"nats-server", EnvVar} {
		if !strings.Contains(r.fatal[0], want) {
			t.Errorf("failure %q does not name %q", r.fatal[0], want)
		}
	}
	if got != "" {
		t.Errorf("path = %q for a missing tool", got)
	}
}

func TestAMissingUnlistedToolSkipsTheTest(t *testing.T) {
	for _, list := range []string{"", "tmux", "nats-server-x"} {
		r := &recorder{}
		pathFor(r, missing, list, "nats-server")
		if len(r.skip) != 1 || len(r.fatal) != 0 {
			t.Errorf("list %q: fatal=%v skip=%v, want one skip and no failure", list, r.fatal, r.skip)
		}
	}
}

func TestAFoundToolReturnsItsPathWhateverTheList(t *testing.T) {
	for _, list := range []string{"", "nats-server", "tmux"} {
		r := &recorder{}
		if got := pathFor(r, found, list, "nats-server"); got != "/opt/bin/nats-server" {
			t.Errorf("list %q: path = %q", list, got)
		}
		if len(r.fatal)+len(r.skip) != 0 {
			t.Errorf("list %q: reported fatal=%v skip=%v for a found tool", list, r.fatal, r.skip)
		}
	}
}

// homeguard removes every MARVEL_* variable before the tests run, so the list
// has to be recorded first.
func TestPreserveKeepsTheListAcrossAScrub(t *testing.T) {
	t.Setenv(EnvVar, "nats-server")
	Preserve()
	t.Cleanup(func() { captured = nil })
	t.Setenv(EnvVar, "")
	if got := current(); got != "nats-server" {
		t.Errorf("current() = %q after a scrub, want the preserved list", got)
	}
}

func TestWithNothingPreservedTheLiveEnvironmentIsRead(t *testing.T) {
	captured = nil
	t.Setenv(EnvVar, "tmux")
	if got := current(); got != "tmux" {
		t.Errorf("current() = %q, want the live value", got)
	}
}
