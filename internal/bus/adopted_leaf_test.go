package bus

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/service"
)

// An adopted broker's leaf is read from its loopback monitor, which needs no
// credential, and marvel is never given one to do it (aae-orc-c6o29,
// sim/design/kinu-nats-deployment.md KN-11).

// leafzServer serves /leafz on a loopback port and returns the client port the
// monitor offset maps back to it, so MonitorAddr(listen) lands on the server.
func leafzServer(t *testing.T, body string) (listen string, headers func() []http.Header) {
	t.Helper()
	var mu sync.Mutex
	var seen []http.Header
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
		if r.URL.Path != "/leafz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	mon := l.Addr().(*net.TCPAddr).Port
	if mon <= monitorPortOffset {
		t.Skipf("monitor port %d is too low to place a client port below it", mon)
	}
	return "127.0.0.1:" + strconv.Itoa(mon-monitorPortOffset), func() []http.Header {
		mu.Lock()
		defer mu.Unlock()
		return append([]http.Header(nil), seen...)
	}
}

// closedMonitorListen is a client address whose monitor port was bound a moment
// ago and is now closed, so the read is refused and no live port is dialled.
func closedMonitorListen(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mon := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if mon <= monitorPortOffset {
		t.Skipf("monitor port %d is too low to place a client port below it", mon)
	}
	return "127.0.0.1:" + strconv.Itoa(mon-monitorPortOffset)
}

func adoptedBus(listen, hub string) config.ResolvedBus {
	rb := config.ResolvedBus{Mode: service.ModeAdopted, Listen: listen, URL: "nats://" + listen}
	if hub != "" {
		rb.HubURL, rb.HubURLs = hub, []string{hub}
	}
	return rb
}

func TestPostcheckAdoptedReadsLeafzWithoutCredential(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	listen, headers := leafzServer(t, `{"leafnodes":1}`)

	st := AdoptedStatus(adoptedBus(listen, "nats-leaf://hub.example.test:7442"), &http.Client{Timeout: 3 * time.Second}, now)
	if st.Leaf != "up" || !st.HubDeclared || st.Managed {
		t.Fatalf("status = {Managed:%v HubDeclared:%v Leaf:%q}, want an unmanaged bus with a declared hub and its leaf up", st.Managed, st.HubDeclared, st.Leaf)
	}
	if st.LeafObservedAt != now.Format(time.RFC3339) || st.LeafValidUntil == "" {
		t.Errorf("reading is not dated: observed %q valid until %q", st.LeafObservedAt, st.LeafValidUntil)
	}
	got := headers()
	if len(got) != 1 {
		t.Fatalf("monitor requests = %d, want exactly one read of /leafz", len(got))
	}
	for _, h := range []string{"Authorization", "Cookie", "X-Api-Key"} {
		if got[0].Get(h) != "" {
			t.Errorf("the read carried a %s header, so it used a credential", h)
		}
	}

	down, _ := leafzServer(t, `{"leafnodes":0}`)
	if st := AdoptedStatus(adoptedBus(down, "nats-leaf://hub.example.test:7442"), &http.Client{Timeout: 3 * time.Second}, now); st.Leaf != "down" {
		t.Errorf("a monitor that reports no leaf read as %q, want down", st.Leaf)
	}

	if st := AdoptedStatus(adoptedBus(closedMonitorListen(t), "nats-leaf://hub.example.test:7442"), &http.Client{Timeout: 3 * time.Second}, now); st.Leaf != "unknown" || !st.HubDeclared {
		t.Errorf("an unreadable monitor read as {Leaf:%q HubDeclared:%v}, want unknown with the hub declared", st.Leaf, st.HubDeclared)
	}

	none, noneHeaders := leafzServer(t, `{"leafnodes":1}`)
	if st := AdoptedStatus(adoptedBus(none, ""), &http.Client{Timeout: 3 * time.Second}, now); st.Leaf != "n/a" || st.HubDeclared {
		t.Errorf("a bus with no hub read as {Leaf:%q HubDeclared:%v}, want n/a and not declared", st.Leaf, st.HubDeclared)
	}
	if n := len(noneHeaders()); n != 0 {
		t.Errorf("a bus with no hub had its monitor read %d times, want 0", n)
	}
}
