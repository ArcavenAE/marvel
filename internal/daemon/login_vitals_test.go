package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/hostvitals"
	"github.com/arcavenae/marvel/internal/loginexpiry"
)

func loginRow(account, state string, in time.Duration, now time.Time) api.AccountLogin {
	r := api.AccountLogin{Account: account, State: state, ReadAt: now}
	if in != 0 {
		exp := now.Add(in)
		r.ExpiresAt = &exp
		if in > 0 {
			h := int(in.Hours())
			r.HoursLeft = &h
		}
	}
	return r
}

// The "login expiry" row reads under while every login is ok, over while any
// is expiring or lapsed, and gap while nothing has been read or every read
// failed, never a clean row for logins nobody read.
func TestLoginVitalsRowStates(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		logins []api.AccountLogin
		state  string
	}{
		{"all ok", []api.AccountLogin{loginRow("claude/a", "ok", 200*time.Hour, now)}, "under"},
		{"one expiring", []api.AccountLogin{loginRow("claude/a", "ok", 200*time.Hour, now), loginRow("claude/b", "expiring", 40*time.Hour, now)}, "over"},
		{"lapsed", []api.AccountLogin{loginRow("claude/a", "lapsed", -time.Hour, now)}, "over"},
		{"nothing read", nil, "gap"},
		{"only unreadable", []api.AccountLogin{loginRow("claude/a", "unreadable", 0, now)}, "gap"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := loginVitalsRow(api.AccountLogins{Enabled: true, Logins: c.logins}, now)
			if got.Name != "login expiry" || got.Line != "72 hours" {
				t.Errorf("row = %+v, want name %q and line %q", got, "login expiry", "72 hours")
			}
			if got.State != c.state {
				t.Errorf("state = %q, want %q (detail %q)", got.State, c.state, got.Detail)
			}
		})
	}
}

// The detail names the soonest expiry and its key, and nothing but the key
// string marvel already prints.
func TestLoginVitalsRowDetailNamesTheSoonestExpiry(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	res := api.AccountLogins{Enabled: true, Logins: []api.AccountLogin{
		loginRow("claude/far", "ok", 300*time.Hour, now),
		loginRow("claude/near", "expiring", 40*time.Hour, now),
	}}
	got := loginVitalsRow(res, now)
	if !strings.Contains(got.Detail, "claude/near") || strings.Contains(got.Detail, "claude/far") {
		t.Errorf("detail = %q, want the soonest key claude/near only", got.Detail)
	}
	if !strings.Contains(got.Detail, now.Add(40*time.Hour).Format(time.RFC3339)) {
		t.Errorf("detail = %q, want the soonest expiry time", got.Detail)
	}
}

func vitalsWithLogins(t *testing.T, on bool) *Daemon {
	t.Helper()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	d := &Daemon{vitals: hostvitals.New(true, func() hostvitals.Reading {
		return hostvitals.Reading{SwapOK: true, SwapUsed: 10, SwapTotal: 100}
	}, nil)}
	d.vitals.Sample()
	if on {
		ask := func(context.Context, string) (loginexpiry.Reading, error) { return readingIn(40*time.Hour, now), nil }
		list := func() []api.Session { return []api.Session{claudeSeat("a1", "/homes/a", api.SessionRunning)} }
		d.logins = newLoginService(true, &loginSink{}, list, ask)
		d.logins.pass(context.Background(), now)
	}
	return d
}

func thresholdNamed(snap hostvitals.Snapshot, name string) (hostvitals.Threshold, bool) {
	for _, th := range snap.Thresholds {
		if th.Name == name {
			return th, true
		}
	}
	return hostvitals.Threshold{}, false
}

// host.vitals carries the login row when the login service is on.
func TestHostVitalsCarriesTheLoginRowWhenTheServiceIsOn(t *testing.T) {
	snap := vitalsSnapshot(t, vitalsWithLogins(t, true).handleHostVitals())
	th, ok := thresholdNamed(snap, "login expiry")
	if !ok {
		t.Fatalf("thresholds = %+v, want a login expiry row", snap.Thresholds)
	}
	if th.State != "over" {
		t.Errorf("login expiry state = %q, want over for a login 40 hours from expiry", th.State)
	}
	if _, ok := thresholdNamed(snap, "swap used"); !ok {
		t.Errorf("the swap row is gone: %+v", snap.Thresholds)
	}
}

// A daemon whose login service is off adds no row: it has nothing to say, and
// a row would read as a login that is fine.
func TestHostVitalsHasNoLoginRowWhenTheServiceIsOff(t *testing.T) {
	snap := vitalsSnapshot(t, vitalsWithLogins(t, false).handleHostVitals())
	if th, ok := thresholdNamed(snap, "login expiry"); ok {
		t.Errorf("got a login expiry row %+v from a daemon with no login service", th)
	}
}
