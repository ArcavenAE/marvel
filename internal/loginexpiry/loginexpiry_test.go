package loginexpiry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

const (
	fakeEmail = "someone.private@example.invalid"
	fakeOrg   = "Example Private Org"
)

// statusJSON is the shape the harness prints: the three fields wanted, and the
// personal data that must never leave Decode.
func statusJSON(loggedIn bool, expires string) string {
	exp := ""
	if expires != "" {
		exp = `"loginExpiresAt":"` + expires + `",`
	}
	return fmt.Sprintf(`{"loggedIn":%t,"authMethod":"claude.ai",%s"subscriptionType":"max","email":%q,"orgName":%q,"orgId":"org-0000","configDirectory":"/x"}`,
		loggedIn, exp, fakeEmail, fakeOrg)
}

// stub writes an executable shell script and returns its path.
func stub(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDecodeKeepsTheThreeFieldsAndDropsTheRest(t *testing.T) {
	rd, err := Decode(strings.NewReader(statusJSON(true, "2026-11-30T05:48:14Z")))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := time.Date(2026, 11, 30, 5, 48, 14, 0, time.UTC)
	if !rd.LoggedIn || !rd.ExpiresAt.Equal(want) || rd.Subscription != "max" {
		t.Errorf("reading = %+v, want logged in, expiry %v, subscription max", rd, want)
	}
	for _, leak := range []string{fakeEmail, fakeOrg, "org-0000"} {
		if got := fmt.Sprintf("%+v %#v", rd, rd); strings.Contains(got, leak) {
			t.Errorf("a decoded reading carries %q: %s", leak, got)
		}
	}
}

func TestDecodeLoggedOutNeedsNoExpiry(t *testing.T) {
	rd, err := Decode(strings.NewReader(statusJSON(false, "")))
	if err != nil {
		t.Fatalf("Decode of a logged-out answer: %v", err)
	}
	if rd.LoggedIn || rd.Subscription != "max" || !rd.ExpiresAt.IsZero() {
		t.Errorf("reading = %+v, want logged out, subscription max, no expiry", rd)
	}
}

func TestDecodeRefusesAnswersItCannotUse(t *testing.T) {
	huge := `{"loggedIn":true,"loginExpiresAt":"2026-11-30T05:48:14Z","pad":"` + strings.Repeat("x", MaxOutput) + `"}`
	cases := []struct {
		name string
		in   string
		want Reason
	}{
		{"not json", "login: please sign in " + fakeEmail, ReasonNotJSON},
		{"empty", "", ReasonNotJSON},
		{"over the cap", huge, ReasonNotJSON},
		{"logged in with no expiry", statusJSON(true, ""), ReasonExpiryMissing},
		{"an expiry that is not a time", statusJSON(true, "soon"), ReasonExpiryMissing},
	}
	for _, c := range cases {
		_, err := Decode(strings.NewReader(c.in))
		var re *ReadError
		if !errors.As(err, &re) || re.Reason != c.want {
			t.Errorf("%s: err = %v, want a ReadError with reason %q", c.name, err, c.want)
			continue
		}
		if strings.Contains(err.Error(), fakeEmail) || strings.Contains(err.Error(), "sign in") {
			t.Errorf("%s: the error carries output text: %q", c.name, err.Error())
		}
	}
}

func TestAskPassesTheConfigDirExactlyAsGiven(t *testing.T) {
	// The harness reports loggedIn false for another spelling of one home, so
	// the stub answers logged in only for the exact string.
	prog := stub(t, `if [ "$CLAUDE_CONFIG_DIR" = "/homes/a/" ]; then echo '`+statusJSON(true, "2026-11-30T05:48:14Z")+`'; else echo '`+statusJSON(false, "")+`'; fi`)
	rd, err := Ask(context.Background(), prog, "/homes/a/", 5*time.Second)
	if err != nil || !rd.LoggedIn {
		t.Fatalf("exact spelling: reading %+v, err %v, want logged in", rd, err)
	}
	rd, err = Ask(context.Background(), prog, "/homes/a", 5*time.Second)
	if err != nil || rd.LoggedIn {
		t.Errorf("another spelling: reading %+v, err %v, want logged out", rd, err)
	}
}

func TestAskRemovesTheConfigDirForTheDefaultHome(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/inherited")
	prog := stub(t, `if [ -z "${CLAUDE_CONFIG_DIR+x}" ]; then echo '`+statusJSON(true, "2026-11-30T05:48:14Z")+`'; else echo '`+statusJSON(false, "")+`'; fi`)
	rd, err := Ask(context.Background(), prog, "", 5*time.Second)
	if err != nil || !rd.LoggedIn {
		t.Errorf("default home: reading %+v, err %v, want CLAUDE_CONFIG_DIR unset in the child", rd, err)
	}
}

func TestAskErrorsCarryNoOutputText(t *testing.T) {
	prog := stub(t, `echo 'bad login for `+fakeEmail+`'; echo 'detail '`+fakeOrg+` >&2; exit 1`)
	_, err := Ask(context.Background(), prog, "", 5*time.Second)
	var re *ReadError
	if !errors.As(err, &re) || re.Reason != ReasonExitNonZero {
		t.Fatalf("err = %v, want a ReadError with reason %q", err, ReasonExitNonZero)
	}
	if strings.Contains(err.Error(), fakeEmail) || strings.Contains(err.Error(), "Example") {
		t.Errorf("the error carries output text: %q", err.Error())
	}
}

func TestAskBoundsAHungChildAndKillsItsGroup(t *testing.T) {
	// The harness forks the keychain reader, so the stub forks a grandchild: a
	// kill of the child alone leaves it running, only a kill of the group ends it.
	pidFile := filepath.Join(t.TempDir(), "pid")
	prog := stub(t, `sleep 30 & echo $! > `+pidFile+`; wait`)
	start := time.Now()
	_, err := Ask(context.Background(), prog, "", 300*time.Millisecond)
	var re *ReadError
	if !errors.As(err, &re) || re.Reason != ReasonTimedOut {
		t.Fatalf("err = %v, want a ReadError with reason %q", err, ReasonTimedOut)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("returned after %v, want about the bound", d)
	}
	assertGone(t, pidFile)
}

// A grandchild that holds the stdout pipe open after the harness exits (the
// harness forks the keychain reader) must not hang the wait.
func TestAskDoesNotHangOnAGrandchildHoldingThePipe(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "gpid")
	prog := stub(t, `echo '`+statusJSON(true, "2026-11-30T05:48:14Z")+`'; sleep 30 & echo $! > `+pidFile+`; exit 0`)
	start := time.Now()
	rd, err := Ask(context.Background(), prog, "", 500*time.Millisecond)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("returned after %v, a grandchild held the pipe", d)
	}
	if err != nil || !rd.LoggedIn {
		t.Errorf("reading %+v, err %v, want the complete answer the child printed", rd, err)
	}
	assertGone(t, pidFile)
}

func assertGone(t *testing.T, pidFile string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, err := os.ReadFile(pidFile)
		if err == nil {
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			if pid > 1 && syscall.Kill(pid, 0) != nil {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Errorf("the child recorded in %s is still running or was never started", pidFile)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ---- tracker ----

type sink struct{ got []events.Event }

func (s *sink) Emit(e events.Event) { s.got = append(s.got, e) }

func (s *sink) kinds() []events.Kind {
	var k []events.Kind
	for _, e := range s.got {
		k = append(k, e.Kind)
	}
	return k
}

var (
	keyA = api.AccountKey{Harness: "claude", Backend: "default", ConfigHome: "/homes/a"}
	t0   = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
)

func good(expires time.Time) Reading {
	return Reading{LoggedIn: true, ExpiresAt: expires, Subscription: "max"}
}

func TestTrackerIsQuietAboveTheWarningLine(t *testing.T) {
	s := &sink{}
	tr := NewTracker(s)
	tr.Observe(keyA, good(t0.Add(WarnWithin+time.Hour)), nil, t0)
	tr.Observe(keyA, good(t0.Add(WarnWithin+time.Hour)), nil, t0.Add(30*time.Minute))
	if len(s.got) != 0 {
		t.Errorf("events = %v, want none above 72 hours", s.kinds())
	}
	rows := tr.Rows()
	if len(rows) != 1 || rows[0].State != StateOK || rows[0].Key != keyA {
		t.Errorf("rows = %+v, want one ok row for the key", rows)
	}
}

func TestTrackerWarnsOnceAtSeventyTwoHoursOrLess(t *testing.T) {
	s := &sink{}
	tr := NewTracker(s)
	exp := t0.Add(WarnWithin) // exactly 72 hours counts
	tr.Observe(keyA, good(exp), nil, t0)
	tr.Observe(keyA, good(exp), nil, t0.Add(time.Hour))
	tr.Observe(keyA, good(exp), nil, t0.Add(2*time.Hour))
	if got := s.kinds(); len(got) != 1 || got[0] != events.KindAccountLoginExpiring {
		t.Fatalf("events = %v, want exactly one %s", got, events.KindAccountLoginExpiring)
	}
	e := s.got[0]
	if e.Severity != events.SeverityWarning {
		t.Errorf("severity = %v, want warning", e.Severity)
	}
	if !strings.Contains(e.Message, keyA.String()) {
		t.Errorf("message %q lacks the account key %q", e.Message, keyA.String())
	}
	if rows := tr.Rows(); len(rows) != 1 || rows[0].State != StateExpiring || !rows[0].ExpiresAt.Equal(exp) {
		t.Errorf("rows = %+v, want one expiring row with the expiry", rows)
	}
}

func TestTrackerRaisesLapsedOnceAtErrorSeverity(t *testing.T) {
	s := &sink{}
	tr := NewTracker(s)
	tr.Observe(keyA, Reading{LoggedIn: false}, nil, t0)
	tr.Observe(keyA, Reading{LoggedIn: false}, nil, t0.Add(time.Hour))
	if got := s.kinds(); len(got) != 1 || got[0] != events.KindAccountLoginLapsed {
		t.Fatalf("events = %v, want exactly one %s", got, events.KindAccountLoginLapsed)
	}
	if s.got[0].Severity != events.SeverityError {
		t.Errorf("severity = %v, want error", s.got[0].Severity)
	}
	// An expiry already past reads as lapsed too.
	s2 := &sink{}
	tr2 := NewTracker(s2)
	tr2.Observe(keyA, good(t0.Add(-time.Minute)), nil, t0)
	if got := s2.kinds(); len(got) != 1 || got[0] != events.KindAccountLoginLapsed {
		t.Errorf("past expiry: events = %v, want one %s", got, events.KindAccountLoginLapsed)
	}
}

func TestTrackerReportsARenewalOnceAndWarnsAgainAfterwards(t *testing.T) {
	s := &sink{}
	tr := NewTracker(s)
	tr.Observe(keyA, good(t0.Add(48*time.Hour)), nil, t0)
	renewed := t0.Add(30 * 24 * time.Hour)
	tr.Observe(keyA, good(renewed), nil, t0.Add(time.Hour))
	tr.Observe(keyA, good(renewed), nil, t0.Add(2*time.Hour))
	want := []events.Kind{events.KindAccountLoginExpiring, events.KindAccountLoginRenewed}
	if got := s.kinds(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if s.got[1].Severity != events.SeverityInfo {
		t.Errorf("renewed severity = %v, want info", s.got[1].Severity)
	}
	// Seventy-two hours before the new expiry the warning is raised again.
	tr.Observe(keyA, good(renewed), nil, renewed.Add(-48*time.Hour))
	if got := s.kinds(); len(got) != 3 || got[2] != events.KindAccountLoginExpiring {
		t.Errorf("events = %v, want a second %s", got, events.KindAccountLoginExpiring)
	}
}

func TestTrackerTreatsSubMinuteMovementAsJitterNotRenewal(t *testing.T) {
	s := &sink{}
	tr := NewTracker(s)
	exp := t0.Add(48 * time.Hour)
	tr.Observe(keyA, good(exp), nil, t0)
	tr.Observe(keyA, good(exp.Add(400*time.Millisecond)), nil, t0.Add(time.Hour))
	// Thirty seconds is still under the one-minute line.
	tr.Observe(keyA, good(exp.Add(30*time.Second)), nil, t0.Add(2*time.Hour))
	if got := s.kinds(); len(got) != 1 {
		t.Errorf("events = %v, want only the first warning", got)
	}
}

func TestTrackerLapsedThenReadingOKIsARenewal(t *testing.T) {
	s := &sink{}
	tr := NewTracker(s)
	tr.Observe(keyA, Reading{LoggedIn: false}, nil, t0)
	tr.Observe(keyA, good(t0.Add(30*24*time.Hour)), nil, t0.Add(time.Hour))
	want := []events.Kind{events.KindAccountLoginLapsed, events.KindAccountLoginRenewed}
	if got := s.kinds(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestTrackerUnreadableKeepsTheStateAndEmitsNothing(t *testing.T) {
	s := &sink{}
	tr := NewTracker(s)
	exp := t0.Add(48 * time.Hour)
	tr.Observe(keyA, good(exp), nil, t0)
	tr.Observe(keyA, Reading{}, &ReadError{Reason: ReasonTimedOut}, t0.Add(time.Hour))
	if got := s.kinds(); len(got) != 1 {
		t.Errorf("events = %v, want only the first warning", got)
	}
	rows := tr.Rows()
	if len(rows) != 1 || rows[0].State != StateExpiring || rows[0].Why != ReasonTimedOut || !rows[0].ExpiresAt.Equal(exp) {
		t.Errorf("rows = %+v, want the expiring row kept, with why %q", rows, ReasonTimedOut)
	}
	// A login never read says so: an unreadable row, never zeros.
	tr.Observe(api.AccountKey{Harness: "claude", ConfigHome: "/homes/b"}, Reading{}, &ReadError{Reason: ReasonNotJSON}, t0)
	var seen bool
	for _, r := range tr.Rows() {
		if r.Key.ConfigHome == "/homes/b" {
			seen = true
			if r.State != StateUnreadable || r.Why != ReasonNotJSON {
				t.Errorf("never-read row = %+v, want unreadable with why %q", r, ReasonNotJSON)
			}
		}
	}
	if !seen {
		t.Error("a login that was never read has no row")
	}
}

func TestTrackerEventsCarryNoPersonalData(t *testing.T) {
	s := &sink{}
	tr := NewTracker(s)
	rd, err := Decode(strings.NewReader(statusJSON(true, t0.Add(10*time.Hour).Format(time.RFC3339))))
	if err != nil {
		t.Fatal(err)
	}
	tr.Observe(keyA, rd, nil, t0)
	tr.Observe(keyA, Reading{LoggedIn: false}, nil, t0.Add(time.Hour))
	tr.Observe(keyA, good(t0.Add(30*24*time.Hour)), nil, t0.Add(2*time.Hour))
	if len(s.got) != 3 {
		t.Fatalf("events = %v, want expiring, lapsed and renewed", s.kinds())
	}
	for _, e := range s.got {
		if m := fmt.Sprintf("%+v", e); strings.Contains(m, fakeEmail) || strings.Contains(m, fakeOrg) {
			t.Errorf("event %s carries personal data: %s", e.Kind, m)
		}
	}
}
