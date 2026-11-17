package bus

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseHubLeafzNamesTheLeafs(t *testing.T) {
	t.Parallel()
	body := []byte(`{"server_id":"X","leafnodes":2,"leafs":[{"name":"alpha","account":"A"},{"name":"charlie","account":"A"}]}`)
	got, err := ParseHubLeafz(body)
	if err != nil {
		t.Fatalf("ParseHubLeafz: %v", err)
	}
	if want := []string{"alpha", "charlie"}; !reflect.DeepEqual(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
	if got, err := ParseHubLeafz([]byte(`{"leafnodes":0}`)); err != nil || len(got) != 0 {
		t.Errorf("a hub with no leafs = %v, %v, want none and no error", got, err)
	}
	if _, err := ParseHubLeafz([]byte(`not json`)); err == nil {
		t.Error("a body that is not JSON parsed without error")
	}
}

// hubMonitor is a stand-in hub monitor on a loopback scratch port.
func hubMonitor(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/leafz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestPollHubStoresAReadingThatBusStatusPrints(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	own := m.domain
	m.bus.HubMonitor = hubMonitor(t, `{"leafnodes":1,"leafs":[{"name":"`+own+`"}]}`)
	m.bus.HubExpectedLeafs = []string{own, "bravo"}
	s.pollHub()
	lines := s.Status().HubStatus
	all := strings.Join(lines, "\n")
	if !strings.Contains(all, "bravo") || !strings.Contains(strings.ToLower(all), "missing") {
		t.Errorf("status lacks the missing expected leaf:\n%s", all)
	}
	if !strings.Contains(strings.ToLower(all), "partition") {
		t.Errorf("only the local leaf is listed and the remote one is expected, so a partition line is due:\n%s", all)
	}
}

// A monitor that does not answer, or answers with a bad body, is not observed.
func TestPollHubFailureIsNotObserved(t *testing.T) {
	for name, mon := range map[string]string{
		"refused":  "127.0.0.1:1",
		"bad body": hubMonitor(t, "not json"),
	} {
		s, m, _ := newTestSupervisor(t, "")
		m.bus.HubMonitor = mon
		m.bus.HubExpectedLeafs = []string{"alpha"}
		s.pollHub()
		all := strings.ToLower(strings.Join(s.Status().HubStatus, "\n"))
		if !strings.Contains(all, "not observed") {
			t.Errorf("%s: status does not say not observed:\n%s", name, all)
		}
		if strings.Contains(all, "missing") || strings.Contains(all, "partition") {
			t.Errorf("%s: an unread hub was read as a finding:\n%s", name, all)
		}
	}
}

// Without a hub_monitor the status carries no hub lines and the monitor is not
// touched.
func TestPollHubWithNoMonitorPrintsNothing(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	s.pollHub()
	if got := s.Status().HubStatus; len(got) != 0 {
		t.Errorf("HubStatus = %v, want none without a hub_monitor", got)
	}
}

// A hub reading never raises an event: the cluster-gone alert is director's.
func TestPollHubRaisesNoEvent(t *testing.T) {
	s, m, ring := newTestSupervisor(t, "")
	m.bus.HubMonitor = "127.0.0.1:1"
	m.bus.HubExpectedLeafs = []string{"alpha"}
	before := len(kindsOf(ring))
	s.pollHub()
	if after := len(kindsOf(ring)); after != before {
		t.Errorf("pollHub emitted %d event(s): %v", after-before, kindsOf(ring))
	}
}

// A monitor that redirects is not read through: the answer would come from
// somewhere the cluster did not name.
func TestPollHubDoesNotFollowARedirect(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	target := hubMonitor(t, `{"leafnodes":1,"leafs":[{"name":"alpha"}]}`)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+target+"/leafz", http.StatusFound)
	}))
	t.Cleanup(redir.Close)
	m.bus.HubMonitor = strings.TrimPrefix(redir.URL, "http://")
	m.bus.HubExpectedLeafs = []string{"alpha"}
	s.pollHub()
	all := strings.ToLower(strings.Join(s.Status().HubStatus, "\n"))
	if !strings.Contains(all, "not observed") || strings.Contains(all, "present") {
		t.Errorf("a redirected monitor was read through:\n%s", all)
	}
}

func TestPollHubNon200IsNotObserved(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"leafs":[{"name":"alpha"}]}`, http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	m.bus.HubMonitor = strings.TrimPrefix(srv.URL, "http://")
	m.bus.HubExpectedLeafs = []string{"alpha"}
	s.pollHub()
	all := strings.ToLower(strings.Join(s.Status().HubStatus, "\n"))
	if !strings.Contains(all, "not observed") || strings.Contains(all, "present") {
		t.Errorf("a 500 with a leaf-shaped body was read:\n%s", all)
	}
}

// After a good reading, a failed one keeps the time of the good one in the
// line and reads as not observed.
func TestPollHubFailureAfterAGoodReadingKeepsItsTime(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	fixed := time.Date(2030, 5, 6, 7, 8, 9, 0, time.UTC)
	s.now = func() time.Time { return fixed }
	good := hubMonitor(t, `{"leafs":[{"name":"alpha"}]}`)
	m.bus.HubMonitor = good
	m.bus.HubExpectedLeafs = []string{"alpha"}
	s.pollHub()
	m.bus.HubMonitor = "127.0.0.1:1"
	s.pollHub()
	all := strings.Join(s.Status().HubStatus, "\n")
	if !strings.Contains(all, "not observed") || !strings.Contains(all, fixed.Format(time.RFC3339)) {
		t.Errorf("a failed read after a good one lost the last good time:\n%s", all)
	}
}

// The tick is what runs the poll.
func TestTickPollsTheHub(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	m.bus.HubMonitor = hubMonitor(t, `{"leafs":[{"name":"alpha"}]}`)
	m.bus.HubExpectedLeafs = []string{"alpha"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	busDriver{s}.Tick(ctx)
	if all := strings.Join(s.Status().HubStatus, "\n"); !strings.Contains(all, "present alpha") {
		t.Errorf("the tick did not poll the hub:\n%s", all)
	}
}

func TestStatusJSONCarriesTheHubLines(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(Status{HubStatus: []string{"x"}})
	if err != nil || !strings.Contains(string(b), `"hub_status":["x"]`) {
		t.Errorf("status JSON = %s, %v", b, err)
	}
	if b, _ := json.Marshal(Status{}); strings.Contains(string(b), "hub_status") {
		t.Errorf("an empty status carries hub_status: %s", b)
	}
}

// With no hub_monitor the supervisor makes no read at all, to any address.
func TestPollHubWithNoMonitorMakesNoRequest(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	calls := 0
	s.hubRead = func(string) ([]string, error) { calls++; return nil, nil }
	s.pollHub()
	if calls != 0 {
		t.Errorf("pollHub read the hub %d time(s) with no hub_monitor configured", calls)
	}
}

// With one, it reads exactly the configured address.
func TestPollHubReadsTheConfiguredAddressOnly(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	m.bus.HubMonitor = "127.0.0.1:9"
	var got []string
	s.hubRead = func(mon string) ([]string, error) { got = append(got, mon); return nil, nil }
	s.pollHub()
	if !reflect.DeepEqual(got, []string{"127.0.0.1:9"}) {
		t.Errorf("pollHub read %v, want only the configured 127.0.0.1:9", got)
	}
}

// A monitor that accepts the connection and never answers is not observed, and
// the line names the timeout. The bound is shortened for the test.
func TestPollHubTimeoutIsNotObserved(t *testing.T) {
	old := hubReadTimeout
	hubReadTimeout = 150 * time.Millisecond
	t.Cleanup(func() { hubReadTimeout = old })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	s, m, _ := newTestSupervisor(t, "")
	m.bus.HubMonitor = ln.Addr().String()
	m.bus.HubExpectedLeafs = []string{"alpha"}
	done := make(chan struct{})
	go func() { s.pollHub(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pollHub was still waiting 5s after a 150ms bound, against a monitor that never answers")
	}
	all := strings.ToLower(strings.Join(s.Status().HubStatus, "\n"))
	if !strings.Contains(all, "not observed") || !strings.Contains(all, "timed out") {
		t.Errorf("a monitor that never answers did not read as not observed with a timeout:\n%s", all)
	}
}

// A body past the cap is cut, so the leaf list it carries is not read.
func TestPollHubBodyPastTheCapIsNotObserved(t *testing.T) {
	old := hubBodyLimit
	hubBodyLimit = 64
	t.Cleanup(func() { hubBodyLimit = old })
	s, m, _ := newTestSupervisor(t, "")
	m.bus.HubMonitor = hubMonitor(t, `{"leafnodes":1,"pad":"`+strings.Repeat("x", 200)+`","leafs":[{"name":"alpha"}]}`)
	m.bus.HubExpectedLeafs = []string{"alpha"}
	s.pollHub()
	all := strings.ToLower(strings.Join(s.Status().HubStatus, "\n"))
	if !strings.Contains(all, "not observed") || strings.Contains(all, "present") {
		t.Errorf("a body past the cap was read:\n%s", all)
	}
}

// The window a reading stays observed for follows the supervisor's poll
// interval, as the leaf window does.
func TestHubWindowFollowsThePollInterval(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	now := at
	s.now = func() time.Time { return now }
	s.hubRead = func(string) ([]string, error) { return []string{"alpha"}, nil }
	m.bus.HubMonitor = "127.0.0.1:9"
	m.bus.HubExpectedLeafs = []string{"alpha"}
	s.pollHub()
	now = at.Add(25 * time.Second)
	s.leafPoll = 10 * time.Second // window 30s: still observed
	if all := strings.Join(s.Status().HubStatus, "\n"); strings.Contains(all, "not observed") {
		t.Errorf("a 25s old reading with a 10s poll (30s window) read as stale:\n%s", all)
	}
	s.leafPoll = 5 * time.Second // window 15s: stale
	if all := strings.Join(s.Status().HubStatus, "\n"); !strings.Contains(all, "not observed") {
		t.Errorf("a 25s old reading with a 5s poll (15s window) was still observed:\n%s", all)
	}
}
