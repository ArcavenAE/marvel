package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/arcavenae/marvel/internal/asof"
	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
)

// headerInfo is what the get sessions header is built from: the client's own
// facts (the address it resolved, the rung that chose it, the cluster name it
// asked for) and the daemon's status record from one daemon.status call.
type headerInfo struct {
	Address    string
	Rung       resolveRung
	ClientName string
	// Status is nil when the daemon could not answer, with StatusErr saying why.
	Status    *daemon.DaemonStatus
	StatusErr string
}

// stdoutIsTTY is a variable so a test can stand in for a terminal.
var stdoutIsTTY = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// wantHeader says whether get sessions prints the header: when stdout is a
// terminal, or when --header forces it for a piped run.
func wantHeader(force bool) bool {
	return force || stdoutIsTTY()
}

// clientClusterName is the cluster name this client asked for, known only
// when the cluster rung chose the address: a --socket or MARVEL_SOCKET names
// no cluster, and the default socket belongs to none.
func clientClusterName(rung resolveRung) string {
	if rung != rungCluster {
		return ""
	}
	if clusterName != "" {
		return clusterName
	}
	if cfg, _ := config.Load(); cfg != nil {
		return cfg.CurrentCluster
	}
	return ""
}

// collectHeader resolves the address and rung and makes the one
// daemon.status call. A daemon that cannot answer leaves Status nil with the
// reason, so the listing itself is never refused for want of a header.
func collectHeader() headerInfo {
	var h headerInfo
	addr, opts, rung, err := resolveDaemonRung()
	if err != nil {
		h.StatusErr = err.Error()
		return h
	}
	h.Address, h.Rung, h.ClientName = addr, rung, clientClusterName(rung)
	// Not send(): its daemon-home warning would print here and again on the
	// listing's own request, and the listing's is the one that stays.
	resp, err := daemon.SendRequestWith(addr, daemon.Request{Method: "daemon.status"}, opts)
	switch {
	case err != nil:
		h.StatusErr = err.Error()
	case resp.Error != "":
		h.StatusErr = resp.Error
	default:
		var st daemon.DaemonStatus
		if err := json.Unmarshal(resp.Result, &st); err != nil {
			h.StatusErr = fmt.Sprintf("decode daemon status: %v", err)
		} else {
			h.Status = &st
		}
	}
	return h
}

// renderHeader renders the header block, ending in a blank line. It is
// diagnostic only: nothing gates on any line of it.
func renderHeader(h headerInfo, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cluster  %s (rung: %s, %s)\n", clusterNames(h), orDash(string(h.Rung)), orDash(h.Address))
	if h.Status == nil {
		fmt.Fprintf(&b, "daemon   no status (%s)\n", orDash(h.StatusErr))
	} else {
		if line := mrvlLine(h.Status.MRVL); line != "" {
			fmt.Fprintf(&b, "mrvl://  %s\n", line)
		}
		fmt.Fprintf(&b, "bus      %s\n", busLine(h.Status.Bus, now))
	}
	b.WriteString("\n")
	return b.String()
}

// clusterNames is the cluster's name and, where the other names for it
// differ, those too: the one this client asked for, the one the daemon's own
// config gives it, and the bus domain. Two tools that name one cluster
// differently is what the 2026-10-04 mismatch was.
func clusterNames(h headerInfo) string {
	var daemonName, domain string
	if h.Status != nil {
		daemonName = h.Status.Cluster
		if h.Status.Bus != nil {
			domain = h.Status.Bus.Domain
		}
	}
	main := daemonName
	for _, n := range []string{h.ClientName, domain} {
		if main == "" {
			main = n
		}
	}
	if main == "" {
		return "-"
	}
	var others []string
	if h.ClientName != "" && h.ClientName != main {
		others = append(others, "client "+h.ClientName)
	}
	if daemonName != "" && daemonName != main {
		others = append(others, "daemon "+daemonName)
	}
	if domain != "" && domain != main {
		others = append(others, "bus domain "+domain)
	}
	if len(others) == 0 {
		return main
	}
	return main + " (" + strings.Join(others, ", ") + ")"
}

// mrvlLine is the mrvl:// reach: off, loopback or network, with the address
// the daemon bound. It is empty when the daemon reported no bind, so a guess
// is never shown as a fact.
func mrvlLine(m daemon.MRVLStatus) string {
	switch m.State {
	case "":
		return ""
	case daemon.MRVLOff:
		return string(m.State)
	}
	addr := m.Addr
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
			addr = ":" + port
		}
	}
	return strings.TrimSpace(string(m.State) + " " + addr)
}

// busLine is the bus link with its age. The word is "link up", never
// "connected": a leafnode count is not proof the subjects are carried. An
// expired reading prints "?" and how long ago it was read, and unknown
// prints as unknown, never as down.
func busLine(b *bus.Status, now time.Time) string {
	if b == nil {
		return "none"
	}
	if b.Leaf != "up" && b.Leaf != "down" {
		return orDash(b.Leaf)
	}
	cell := asof.Cell[string]{
		Value:      b.Leaf,
		ObservedAt: parseRFC3339(b.LeafObservedAt),
		ValidUntil: parseRFC3339(b.LeafValidUntil),
	}
	switch cell.State(now) {
	case asof.None:
		return "link " + b.Leaf + " (age unknown)"
	case asof.Stale:
		return "link " + asof.MarkStale + ", last read " + ageWords(now.Sub(cell.ObservedAt)) + " ago"
	default:
		return "link " + b.Leaf + ", " + ageWords(now.Sub(cell.ObservedAt)) + " ago"
	}
}

func parseRFC3339(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// ageWords renders an age: whole seconds under a minute, whole minutes
// under an hour, then hours and minutes ("12s", "9m", "2h9m").
func ageWords(d time.Duration) string {
	d = max(d, 0)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
