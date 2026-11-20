package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/hostvitals"
)

// getVitals prints what `marvel get vitals` shows: the daemon's latest host
// vitals sample, its time, each threshold with its state and each gap line.
// It makes one host.vitals call, which the daemon answers from memory. Every
// figure is diagnostic: nothing gates or acts on it. output is "" for the
// table or "json" for the daemon's snapshot as a document.
func getVitals(w io.Writer, output string) error {
	if output != "" && output != "json" {
		return fmt.Errorf("get vitals prints json with -o json; -o %s is not supported", output)
	}
	resp, err := send(daemonrpc.Request{Method: daemonrpc.MethodHostVitals})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	var snap hostvitals.Snapshot
	if err := json.Unmarshal(resp.Result, &snap); err != nil {
		return fmt.Errorf("decode host vitals: %w", err)
	}
	if output == "json" {
		out, err := json.MarshalIndent(snap, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(out))
		return err
	}
	return printVitals(w, snap, time.Now())
}

// printVitals writes the table form of a snapshot. now is the reader's clock,
// used only for the age of the sample.
func printVitals(w io.Writer, snap hostvitals.Snapshot, now time.Time) error {
	switch {
	case !snap.Enabled:
		_, err := fmt.Fprintln(w, "host vitals is off on this daemon: set host_vitals: true on its cluster in the marvel config to turn it on (docs/design/host-vitals.md)")
		return err
	case !snap.Sampled:
		if _, err := fmt.Fprintln(w, "host vitals is on and has taken no sample yet; the first reading comes with the daemon's first metrics pass"); err != nil {
			return err
		}
	default:
		age := ""
		if snap.At != nil {
			age = fmt.Sprintf(", sampled %s (%s ago)", snap.At.UTC().Format(time.RFC3339), now.Sub(*snap.At).Round(time.Second))
		}
		if _, err := fmt.Fprintf(w, "host vitals%s\n", age); err != nil {
			return err
		}
		for _, t := range snap.Thresholds {
			if _, err := fmt.Fprintf(w, "  %-16s line: %-20s %-6s %s\n", t.Name, t.Line, t.State, t.Detail); err != nil {
				return err
			}
		}
	}
	if len(snap.Top) > 0 {
		if err := printTop(w, snap.Top); err != nil {
			return err
		}
	}
	if len(snap.Orphans) > 0 {
		if err := printOrphans(w, snap.Orphans); err != nil {
			return err
		}
	}
	for _, g := range snap.Gaps {
		if _, err := fmt.Fprintf(w, "GAP %s: %s (docs/design/host-vitals.md section %s)\n", g.Vital, g.Reason, g.Section); err != nil {
			return err
		}
	}
	return nil
}

// printTop writes the top processes by RSS: pid, parent, RSS, elapsed time, CPU
// time and command name. The name is the command name only.
func printTop(w io.Writer, top []hostvitals.Proc) error {
	if _, err := fmt.Fprintf(w, "top processes by RSS:\n  %-8s %-8s %-10s %-10s %-8s %s\n", "PID", "PPID", "RSS", "ELAPSED", "CPU", "NAME"); err != nil {
		return err
	}
	for _, p := range top {
		if _, err := fmt.Fprintf(w, "  %-8d %-8d %-10s %-10s %-8s %s\n", p.PID, p.PPID, rssText(p.RSSBytes), spanText(p.ElapsedSeconds), cpuText(p.CPUSeconds), p.Name); err != nil {
			return err
		}
	}
	return nil
}

// rssText prints bytes as GiB at a GiB or more, else as whole MiB.
func rssText(b uint64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
	}
	return fmt.Sprintf("%d MiB", b>>20)
}

// spanText prints seconds as days, hours, minutes and seconds from the largest
// nonzero unit down: 90061 is 1d1h1m1s and 59 is 59s.
func spanText(sec float64) string {
	n := int64(sec)
	d, h, m, s := n/86400, n%86400/3600, n%3600/60, n%60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd%dh%dm%ds", d, h, m, s)
	case h > 0:
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// cpuText prints CPU time: under a minute as seconds with up to two decimals,
// otherwise as a span.
func cpuText(sec float64) string {
	if sec < 60 {
		return strconv.FormatFloat(math.Round(sec*100)/100, 'f', -1, 64) + "s"
	}
	return spanText(sec)
}

// printOrphans writes the processes marked as orphans: pid, command name, why
// it was marked, its MARVEL_SESSION marker, when, the CPU it has burned since
// and its share of a core over the last 30 minutes (a dash until it has been
// watched that long).
func printOrphans(w io.Writer, orphans []hostvitals.Orphan) error {
	if _, err := fmt.Fprintf(w, "orphans:\n  %-8s %-18s %-10s %-20s %-21s %-12s %s\n", "PID", "NAME", "REASON", "SESSION", "MARKED", "CPU SINCE", "SHARE"); err != nil {
		return err
	}
	for _, o := range orphans {
		session, share := o.Session, "-"
		if session == "" {
			session = "-"
		}
		if o.Share != nil {
			share = fmt.Sprintf("%.0f%%", *o.Share*100)
		}
		if _, err := fmt.Fprintf(w, "  %-8d %-18s %-10s %-20s %-21s %-12s %s\n", o.PID, o.Name, o.Reason, session, o.MarkedAt.UTC().Format(time.RFC3339), cpuText(o.CPUSecondsSinceMarked), share); err != nil {
			return err
		}
	}
	return nil
}
