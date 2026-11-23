package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/loginexpiry"
)

func claudeSeat(name, home string, state api.SessionState) api.Session {
	s := api.Session{
		Name: name, State: state, BackendResolved: api.BackendDefaultName,
		Runtime: api.Runtime{Name: "claude", Env: map[string]string{}},
	}
	if home != "" {
		s.Runtime.Env["CLAUDE_CONFIG_DIR"] = home
	}
	return s
}

func TestLoginTargetsOnePerKeyWithEverySpelling(t *testing.T) {
	codex := claudeSeat("c", "/homes/c", api.SessionRunning)
	codex.Runtime.Name = "codex"
	sessions := []api.Session{
		claudeSeat("a1", "/homes/a/", api.SessionRunning),
		claudeSeat("d1", "", api.SessionRunning),
		claudeSeat("a2", "/homes/a", api.SessionPending),
		claudeSeat("a3", "/homes/a/", api.SessionRunning), // a spelling already held
		claudeSeat("dead", "/homes/dead", api.SessionCrashed),
		codex,
		claudeSeat("b1", "/homes/b", api.SessionRunning),
	}
	got := loginTargets(sessions)
	type want struct {
		home      string
		spellings []string
	}
	wants := []want{{"/homes/a", []string{"/homes/a/", "/homes/a"}}, {"", []string{""}}, {"/homes/b", []string{"/homes/b"}}}
	if len(got) != len(wants) {
		t.Fatalf("targets = %+v, want %d (one per live claude account key, in session order)", got, len(wants))
	}
	for i, w := range wants {
		if got[i].Key.ConfigHome != w.home || fmt.Sprint(got[i].Spellings) != fmt.Sprint(w.spellings) {
			t.Errorf("target %d = %+v, want home %q and spellings %q", i, got[i], w.home, w.spellings)
		}
	}
}

type loginSink struct{ got []events.Event }

func (s *loginSink) Emit(e events.Event) { s.got = append(s.got, e) }

func readingIn(h time.Duration, now time.Time) loginexpiry.Reading {
	return loginexpiry.Reading{LoggedIn: true, ExpiresAt: now.Add(h), Subscription: "max"}
}

func TestLoginPassTriesSpellingsInOrderAndTheFirstLoggedInWins(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	sink := &loginSink{}
	var asked []string
	ask := func(_ context.Context, spelling string) (loginexpiry.Reading, error) {
		asked = append(asked, spelling)
		if spelling == "/homes/a/" { // a spelling the login was not made under
			return loginexpiry.Reading{LoggedIn: false}, nil
		}
		return readingIn(48*time.Hour, now), nil
	}
	list := func() []api.Session {
		return []api.Session{
			claudeSeat("a1", "/homes/a/", api.SessionRunning),
			claudeSeat("a2", "/homes/a", api.SessionRunning),
			claudeSeat("b1", "/homes/b", api.SessionRunning),
		}
	}
	svc := newLoginService(true, sink, list, ask)
	svc.pass(context.Background(), now)
	if want := []string{"/homes/a/", "/homes/a", "/homes/b"}; fmt.Sprint(asked) != fmt.Sprint(want) {
		t.Errorf("asked %q, want %q (b stops at its first logged-in answer)", asked, want)
	}
	res := svc.result(now)
	if len(res.Logins) != 2 {
		t.Fatalf("logins = %+v, want one per account key", res.Logins)
	}
	for _, l := range res.Logins {
		if l.State != string(loginexpiry.StateExpiring) {
			t.Errorf("login %s state = %q, want expiring", l.Account, l.State)
		}
	}
	// The false logged-out of the first spelling raised no lapse.
	for _, e := range sink.got {
		if e.Kind == events.KindAccountLoginLapsed {
			t.Errorf("a spelling the login was not made under raised %s: %s", e.Kind, e.Message)
		}
	}
	if n := len(sink.got); n != 2 {
		t.Errorf("events = %d, want one expiring event per login", n)
	}
}

func TestLoginPassAllSpellingsLoggedOutIsALapse(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	sink := &loginSink{}
	ask := func(context.Context, string) (loginexpiry.Reading, error) {
		return loginexpiry.Reading{LoggedIn: false}, nil
	}
	svc := newLoginService(true, sink, func() []api.Session { return []api.Session{claudeSeat("a1", "/homes/a", api.SessionRunning)} }, ask)
	svc.pass(context.Background(), now)
	if len(sink.got) != 1 || sink.got[0].Kind != events.KindAccountLoginLapsed {
		t.Errorf("events = %+v, want one %s", sink.got, events.KindAccountLoginLapsed)
	}
}

func TestLoginPassRecordsAFailedReadAsUnreadable(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	ask := func(context.Context, string) (loginexpiry.Reading, error) {
		return loginexpiry.Reading{}, &loginexpiry.ReadError{Reason: loginexpiry.ReasonTimedOut}
	}
	svc := newLoginService(true, &loginSink{}, func() []api.Session { return []api.Session{claudeSeat("a1", "/homes/a", api.SessionRunning)} }, ask)
	svc.pass(context.Background(), now)
	res := svc.result(now)
	if len(res.Logins) != 1 || res.Logins[0].State != string(loginexpiry.StateUnreadable) || res.Logins[0].Why != string(loginexpiry.ReasonTimedOut) {
		t.Fatalf("logins = %+v, want one unreadable row with why %q, never a row of zeros", res.Logins, loginexpiry.ReasonTimedOut)
	}
	if res.Logins[0].ExpiresAt != nil || res.Logins[0].HoursLeft != nil {
		t.Errorf("an unread login carries an expiry: %+v", res.Logins[0])
	}
}

func TestLoginPassAsksOnlyWhileOn(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	asked := 0
	ask := func(context.Context, string) (loginexpiry.Reading, error) {
		asked++
		return readingIn(200*time.Hour, now), nil
	}
	list := func() []api.Session { return []api.Session{claudeSeat("a1", "/homes/a", api.SessionRunning)} }
	newLoginService(true, &loginSink{}, list, ask).pass(context.Background(), now)
	if asked != 1 {
		t.Fatalf("a service that is on asked %d times, want 1", asked)
	}
	newLoginService(false, &loginSink{}, list, ask).pass(context.Background(), now)
	if asked != 1 {
		t.Errorf("a service that is off asked (%d total), want it to ask nothing", asked)
	}
	// A host with no claude seat asks nothing.
	newLoginService(true, &loginSink{}, func() []api.Session { return nil }, ask).pass(context.Background(), now)
	if asked != 1 {
		t.Errorf("a host with no claude seat asked (%d total)", asked)
	}
}

func TestLoginIntervalIsAnHourNearAnExpiryAndSixHoursOtherwise(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	row := func(st loginexpiry.State, in time.Duration) loginexpiry.Row {
		return loginexpiry.Row{State: st, ExpiresAt: now.Add(in)}
	}
	cases := []struct {
		name string
		rows []loginexpiry.Row
		want time.Duration
	}{
		{"none", nil, 6 * time.Hour},
		{"far", []loginexpiry.Row{row(loginexpiry.StateOK, 200*time.Hour)}, 6 * time.Hour},
		{"within 96 hours", []loginexpiry.Row{row(loginexpiry.StateOK, 90*time.Hour)}, time.Hour},
		{"expiring", []loginexpiry.Row{row(loginexpiry.StateExpiring, 10*time.Hour)}, time.Hour},
		{"lapsed", []loginexpiry.Row{{State: loginexpiry.StateLapsed}}, time.Hour},
		{"unreadable", []loginexpiry.Row{{State: loginexpiry.StateUnreadable}}, time.Hour},
		{"one near among far", []loginexpiry.Row{row(loginexpiry.StateOK, 300*time.Hour), row(loginexpiry.StateOK, 80*time.Hour)}, time.Hour},
	}
	for _, c := range cases {
		if got := loginInterval(c.rows, now); got != c.want {
			t.Errorf("%s: interval = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLoginResultCarriesHoursLeftAndNoPersonalData(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	ask := func(_ context.Context, spelling string) (loginexpiry.Reading, error) {
		if spelling == "/homes/a" {
			return readingIn(47*time.Hour+30*time.Minute, now), nil
		}
		return loginexpiry.Reading{LoggedIn: false}, nil
	}
	list := func() []api.Session {
		return []api.Session{claudeSeat("a1", "/homes/a", api.SessionRunning), claudeSeat("b1", "/homes/b", api.SessionRunning)}
	}
	svc := newLoginService(true, &loginSink{}, list, ask)
	svc.pass(context.Background(), now)
	res := svc.result(now)
	if !res.Enabled || len(res.Logins) != 2 {
		t.Fatalf("result = %+v, want enabled with two logins", res)
	}
	byHome := map[string]api.AccountLogin{}
	for _, l := range res.Logins {
		byHome[l.Account[strings.LastIndex(l.Account, " ")+1:]] = l
	}
	a, b := byHome["/homes/a"], byHome["/homes/b"]
	if a.HoursLeft == nil || *a.HoursLeft != 47 || a.ExpiresAt == nil || !a.ExpiresAt.Equal(now.Add(47*time.Hour+30*time.Minute)) {
		t.Errorf("expiring login = %+v, want 47 whole hours left and its expiry", a)
	}
	if b.State != string(loginexpiry.StateLapsed) || b.HoursLeft != nil {
		t.Errorf("lapsed login = %+v, want state lapsed and no hours left", b)
	}
	if off := newLoginService(false, &loginSink{}, list, ask).result(now); off.Enabled || off.Logins == nil {
		t.Errorf("a service that is off answers %+v, want enabled false and an empty list that marshals as []", off)
	}
}

// get account-logins reaches the service through the daemon's get handler, and
// a daemon with no service answers "off" and not an error.
func TestGetAccountLoginsThroughTheDaemon(t *testing.T) {
	now := time.Now().UTC()
	ask := func(context.Context, string) (loginexpiry.Reading, error) { return readingIn(48*time.Hour, now), nil }
	svc := newLoginService(true, &loginSink{}, func() []api.Session { return []api.Session{claudeSeat("a1", "/homes/a", api.SessionRunning)} }, ask)
	svc.pass(context.Background(), now)
	d := &Daemon{logins: svc}
	resp := d.handleGet(mustMarshal(t, map[string]any{"resource_type": "account-logins"}))
	if resp.Error != "" {
		t.Fatalf("get account-logins: %s", resp.Error)
	}
	var got api.AccountLogins
	if err := json.Unmarshal(resp.Result, &got); err != nil || !got.Enabled || len(got.Logins) != 1 {
		t.Errorf("answer = %s (%v), want enabled with one login", resp.Result, err)
	}
	none := (&Daemon{}).handleGet(mustMarshal(t, map[string]any{"resource_type": "account-logins"}))
	if none.Error != "" {
		t.Fatalf("a daemon with no service: %s", none.Error)
	}
	var off api.AccountLogins
	if err := json.Unmarshal(none.Result, &off); err != nil || off.Enabled {
		t.Errorf("a daemon with no service answered %s (%v), want enabled false", none.Result, err)
	}
}
