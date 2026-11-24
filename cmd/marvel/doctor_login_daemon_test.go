package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

func claudeManifest() *api.Manifest {
	return &api.Manifest{Teams: []api.ManifestTeam{{Name: "t", Roles: []api.ManifestRole{
		{Name: "r1", Runtime: api.ManifestRuntime{Command: "claude"}},
	}}}}
}

func loginsOf(rows ...api.AccountLogin) func() (api.AccountLogins, error) {
	return func() (api.AccountLogins, error) { return api.AccountLogins{Enabled: true, Logins: rows}, nil }
}

func doctorWithDaemonLogins(t *testing.T, logins func() (api.AccountLogins, error)) (string, int, *[]string) {
	t.Helper()
	in := fakeHost(t)
	stubOnPath(t, "claude")
	in.Manifest = claudeManifest()
	var ran []string
	in.Run = func(name string, args ...string) (string, error) {
		if filepath.Base(name) == "claude" {
			ran = append(ran, name)
			return `{"loggedIn": true}`, nil
		}
		return "tmux 3.5a", nil
	}
	in.Logins = logins
	out, failed := doctorRun(in)
	return out, failed, &ran
}

func hours(h int) *int { return &h }

// When the daemon answers, the login line is its answer for each key and the
// harness is not asked a second time.
func TestDoctorLoginReadsTheDaemonsAnswer(t *testing.T) {
	exp := time.Date(2026, 12, 24, 11, 7, 27, 0, time.UTC)
	out, failed, ran := doctorWithDaemonLogins(t, loginsOf(api.AccountLogin{Account: "claude/default", State: "ok", ExpiresAt: &exp, HoursLeft: hours(720)}))
	l := lineWith(out, "claude/default")
	if !strings.HasPrefix(l, "PASS") || !strings.Contains(l, "720") {
		t.Errorf("login line = %q, want a PASS naming the key and 720 hours\n%s", l, out)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0\n%s", failed, out)
	}
	if len(*ran) != 0 {
		t.Errorf("the harness was asked %v; the daemon's answer should stand in", *ran)
	}
}

// An expiring or unreadable login is a WARN and does not fail the run; a
// lapsed one is a FAIL with a fix line.
func TestDoctorLoginStatesMapToWarnAndFail(t *testing.T) {
	exp := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		row    api.AccountLogin
		prefix string
		failed int
	}{
		{"expiring", api.AccountLogin{Account: "claude/a", State: "expiring", ExpiresAt: &exp, HoursLeft: hours(40)}, "WARN", 0},
		{"unreadable", api.AccountLogin{Account: "claude/a", State: "unreadable", Why: "timed-out"}, "WARN", 0},
		{"lapsed", api.AccountLogin{Account: "claude/a", State: "lapsed", ExpiresAt: &exp}, "FAIL", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, failed, _ := doctorWithDaemonLogins(t, loginsOf(c.row))
			if l := lineWith(out, "claude/a"); !strings.HasPrefix(l, c.prefix) {
				t.Errorf("line = %q, want prefix %s\n%s", l, c.prefix, out)
			}
			if failed != c.failed {
				t.Errorf("failed = %d, want %d\n%s", failed, c.failed, out)
			}
		})
	}
}

// A daemon whose service is off, that has read no login, or that does not
// answer, leaves today's exit-status check in place.
func TestDoctorLoginFallsBackToTheStatusCommand(t *testing.T) {
	off := func() (api.AccountLogins, error) { return api.AccountLogins{}, nil }
	down := func() (api.AccountLogins, error) { return api.AccountLogins{}, errors.New("no daemon") }
	// The service is on and has read no login yet: an empty block would say
	// nothing about the login, so the status command still runs.
	unread := func() (api.AccountLogins, error) { return api.AccountLogins{Enabled: true}, nil }
	for name, fn := range map[string]func() (api.AccountLogins, error){"off": off, "down": down, "unread": unread} {
		t.Run(name, func(t *testing.T) {
			out, failed, ran := doctorWithDaemonLogins(t, fn)
			if len(*ran) != 1 {
				t.Errorf("the harness was asked %d times, want 1\n%s", len(*ran), out)
			}
			if l := lineWith(out, "login claude"); !strings.HasPrefix(l, "PASS") {
				t.Errorf("login line = %q, want today's PASS\n%s", l, out)
			}
			if failed != 0 {
				t.Errorf("failed = %d, want 0\n%s", failed, out)
			}
		})
	}
}
