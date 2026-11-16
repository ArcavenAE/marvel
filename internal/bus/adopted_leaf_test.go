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

// monitorOf is the monitor address a client address from leafzServer maps to.
func monitorOf(t *testing.T, listen string) string {
	t.Helper()
	mon, err := MonitorAddr(listen)
	if err != nil {
		t.Fatalf("monitor address: %v", err)
	}
	return mon
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
	hub := "nats-leaf://hub.example.test:7442"
	client := func() *http.Client { return &http.Client{Timeout: 3 * time.Second} }

	t.Run("leaf up is read with no credential", func(t *testing.T) {
		listen, headers := leafzServer(t, `{"leafnodes":1}`)
		st := AdoptedStatus(adoptedBus(listen, hub), client(), now)
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
		for _, h := range []string{"Authorization", "Cookie", "X-Api-Key", "Proxy-Authorization"} {
			if got[0].Get(h) != "" {
				t.Errorf("the read carried a %s header, so it used a credential", h)
			}
		}
	})

	t.Run("a url with userinfo is not sent as a credential", func(t *testing.T) {
		listen, headers := leafzServer(t, `{"leafnodes":1}`)
		rb := adoptedBus(listen, hub)
		rb.URL = "nats://admin:secret@" + listen
		if st := AdoptedStatus(rb, client(), now); st.Leaf != "up" {
			t.Fatalf("leaf = %q, want up", st.Leaf)
		}
		for _, h := range headers() {
			if h.Get("Authorization") != "" {
				t.Errorf("the broker url's userinfo reached the monitor as an Authorization header")
			}
		}
	})

	t.Run("the monitor read is loopback whatever host the url names", func(t *testing.T) {
		listen, headers := leafzServer(t, `{"leafnodes":1}`)
		_, port, err := net.SplitHostPort(listen)
		if err != nil {
			t.Fatalf("split: %v", err)
		}
		rb := adoptedBus("", hub)
		rb.URL = "nats://192.0.2.1:" + port
		if st := AdoptedStatus(rb, client(), now); st.Leaf != "up" {
			t.Errorf("leaf = %q, want up read from the loopback monitor", st.Leaf)
		}
		if n := len(headers()); n != 1 {
			t.Errorf("loopback monitor requests = %d, want 1", n)
		}
	})

	t.Run("a redirect from the monitor is not followed", func(t *testing.T) {
		target, targetHeaders := leafzServer(t, `{"leafnodes":1}`)
		_ = target
		redirector := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://"+monitorOf(t, target)+"/leafz", http.StatusFound)
		}))
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		redirector.Listener = l
		redirector.Start()
		t.Cleanup(redirector.Close)
		mon := l.Addr().(*net.TCPAddr).Port
		if mon <= monitorPortOffset {
			t.Skipf("monitor port %d is too low to place a client port below it", mon)
		}
		listen := "127.0.0.1:" + strconv.Itoa(mon-monitorPortOffset)
		if st := AdoptedStatus(adoptedBus(listen, hub), client(), now); st.Leaf != "unknown" {
			t.Errorf("a redirecting monitor read as %q, want unknown", st.Leaf)
		}
		if n := len(targetHeaders()); n != 0 {
			t.Errorf("the redirect target was read %d times, want 0", n)
		}
	})

	t.Run("a monitor reporting no leaf reads down", func(t *testing.T) {
		listen, _ := leafzServer(t, `{"leafnodes":0}`)
		if st := AdoptedStatus(adoptedBus(listen, hub), client(), now); st.Leaf != "down" || !st.HubDeclared {
			t.Errorf("read {Leaf:%q HubDeclared:%v}, want down with the hub declared", st.Leaf, st.HubDeclared)
		}
	})

	t.Run("an unreadable monitor reads unknown, not down", func(t *testing.T) {
		if st := AdoptedStatus(adoptedBus(closedMonitorListen(t), hub), client(), now); st.Leaf != "unknown" || !st.HubDeclared {
			t.Errorf("read {Leaf:%q HubDeclared:%v}, want unknown with the hub declared", st.Leaf, st.HubDeclared)
		}
	})

	t.Run("a bus with no hub declared is not read", func(t *testing.T) {
		listen, headers := leafzServer(t, `{"leafnodes":1}`)
		if st := AdoptedStatus(adoptedBus(listen, ""), client(), now); st.Leaf != "n/a" || st.HubDeclared {
			t.Errorf("read {Leaf:%q HubDeclared:%v}, want n/a and not declared", st.Leaf, st.HubDeclared)
		}
		if n := len(headers()); n != 0 {
			t.Errorf("the monitor was read %d times, want 0", n)
		}
	})
}
