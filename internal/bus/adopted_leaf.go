package bus

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/arcavenae/marvel/internal/config"
)

// ReadAdoptedStatus is AdoptedStatus with the client the daemon uses: bounded,
// since bus.status is answered inline, and carrying no credential. The daemon
// package opens no route to a socket itself, so the client lives here.
func ReadAdoptedStatus(rb config.ResolvedBus, now time.Time) Status {
	return AdoptedStatus(rb, &http.Client{Timeout: adoptedReadTimeout}, now)
}

const adoptedReadTimeout = 3 * time.Second

// AdoptedStatus is the status of an adopted broker. The daemon neither starts
// nor supervises it and holds no credential for it, so the leaf is read from
// the broker's loopback monitor, which needs none (aae-orc-c6o29,
// sim/design/kinu-nats-deployment.md KN-11). The read sends no header, whatever
// the broker URL carries, and goes to 127.0.0.1 whatever host the URL names.
//
// A cluster that declares no hub reads "n/a" and the monitor is not touched. A
// declared hub reads "up" or "down" from /leafz, or "unknown" when the monitor
// cannot be read: an unread leaf is never reported as down.
func AdoptedStatus(rb config.ResolvedBus, client *http.Client, now time.Time) Status {
	st := Status{Managed: false, URL: rb.URL, Listen: rb.Listen, Leaf: "n/a"}
	if rb.HubURL == "" {
		return st
	}
	st.HubDeclared = true
	st.Leaf = "unknown"
	mon, err := MonitorAddr(adoptedHostPort(rb))
	if err != nil {
		return st
	}
	// A monitor that redirects is not read through: the answer would come from
	// somewhere the cluster did not name, so it reads as non-200, not as a leaf.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Get("http://" + mon + "/leafz")
	if err != nil {
		return st
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return st
	}
	var body struct {
		Leafnodes *int `json:"leafnodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Leafnodes == nil {
		return st
	}
	st.Leaf = "down"
	if *body.Leafnodes > 0 {
		st.Leaf = "up"
	}
	st.LeafSince = now.UTC().Format(time.RFC3339)
	st.LeafObservedAt = st.LeafSince
	st.LeafValidUntil = now.UTC().Add(leafValidPolls * defaultLeafPoll).Format(time.RFC3339)
	return st
}

// adoptedHostPort is the host:port the adopted broker listens on: its listen
// address, or the one its URL names. Only the port is used for the monitor.
func adoptedHostPort(rb config.ResolvedBus) string {
	if rb.Listen != "" {
		return rb.Listen
	}
	u, err := url.Parse(rb.URL)
	if err != nil {
		return ""
	}
	return net.JoinHostPort(u.Hostname(), u.Port())
}
