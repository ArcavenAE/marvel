package bus

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
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
